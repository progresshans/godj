package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

// CredentialStore observes the selected typed account, its current domain and
// effective permissions in one stable native snapshot. It retains no account,
// permission or query-result caches, and does no password work.
type CredentialStore[M any] struct{ state *credentialStoreState[M] }

type credentialStoreState[M any] struct {
	backend          db.SnapshotReader
	binding          Binding[M]
	login, principal orm.StringField[M]
	key              orm.AutoField[M]
	permissions      func(context.Context, db.Queryer, int64) ([]auth.Permission, error)
}

func (CredentialStore[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.CredentialStore{redacted}"))
}

// NewCredentialStore binds the selected group and permission descriptors to
// the SAME project owner as the account. No default models, field-name
// assumptions, caller-written SQL or separately supplied permission loader are
// involved. Construction checks configuration without performing I/O.
func NewCredentialStore[M, G, P any](backend db.SnapshotReader, binding Binding[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P]) (*CredentialStore[M], error) {
	if nilModelValue(backend) || binding.state == nil {
		return nil, invalidCredentialStore()
	}
	load, err := bindPermissionReader(binding, groups, permissions)
	if err != nil {
		return nil, &auth.Error{Code: auth.CodeInvalidConfig, Detail: "account permission catalog binding failed", Cause: err}
	}
	metadata, exists := binding.state.project.Model(binding.state.role.Model)
	if !exists {
		return nil, invalidCredentialStore()
	}
	key, exists := primaryField(metadata)
	if !exists {
		return nil, invalidCredentialStore()
	}
	return &CredentialStore[M]{state: &credentialStoreState[M]{
		backend: backend, binding: binding,
		login:     orm.NewStringField[M](binding.state.credentialFields[binding.state.role.Login]),
		principal: orm.NewStringField[M](binding.state.credentialFields[binding.state.role.PrincipalID]),
		key:       orm.NewAutoField[M](key), permissions: load,
	}}, nil
}

// CredentialByUsername looks up an exact canonical login. NewAuthenticator
// owns the selected normalization policy before calling the shared verifier.
func (store *CredentialStore[M]) CredentialByUsername(ctx context.Context, login string) (auth.Credential, bool, error) {
	if err := store.valid(); err != nil {
		return auth.Credential{}, false, err
	}
	if err := auth.ValidateUsername(login); err != nil {
		return auth.Credential{}, false, err
	}
	return store.lookup(ctx, store.state.login.Exact(login))
}

func (store *CredentialStore[M]) CredentialByID(ctx context.Context, principalID string) (auth.Credential, bool, error) {
	if err := store.valid(); err != nil {
		return auth.Credential{}, false, err
	}
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil {
		return auth.Credential{}, false, err
	}
	return store.lookup(ctx, store.state.principal.Exact(principalID))
}

func (store *CredentialStore[M]) lookup(ctx context.Context, predicate orm.Predicate[M]) (auth.Credential, bool, error) {
	if nilModelValue(ctx) {
		return auth.Credential{}, false, invalidAccountCredential()
	}
	if err := ctx.Err(); err != nil {
		return auth.Credential{}, false, err
	}
	var candidate auth.Credential
	var found bool
	var callbackErr error
	calls := 0
	err := store.state.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		calls++
		if calls != 1 || nilModelValue(reader) {
			callbackErr = errors.Join(callbackErr, invalidAccountCredential())
			return callbackErr
		}
		candidate, found, callbackErr = store.lookupIn(ctx, reader, predicate)
		return callbackErr
	})
	if err = errors.Join(err, callbackErr, ctx.Err()); err != nil {
		return auth.Credential{}, false, accountReadFailure(err)
	}
	if calls != 1 {
		return auth.Credential{}, false, invalidAccountCredential()
	}
	return candidate, found, nil
}

func (store *CredentialStore[M]) lookupIn(ctx context.Context, reader db.Queryer, predicate orm.Predicate[M]) (auth.Credential, bool, error) {
	binding := store.state.binding
	ownership, err := binding.RequireDomainIn(ctx, reader)
	if err != nil {
		return auth.Credential{}, false, err
	}
	return store.credentialInDomain(ctx, reader, predicate, ownership.Generation())
}

// The caller obtained generation from the selected role in this SAME scope.
// This split lets authorization reject an old-domain actor before looking up
// its ID without repeating the domain observation or opening another snapshot.
func (store *CredentialStore[M]) credentialInDomain(ctx context.Context, reader db.Queryer, predicate orm.Predicate[M], generation [sha256.Size]byte) (auth.Credential, bool, error) {
	binding := store.state.binding
	row, present, err := binding.state.objects.Using(reader).Filter(predicate).OrderBy(store.state.key.Asc()).First(ctx)
	if err != nil || !present {
		return auth.Credential{}, false, err
	}
	key, present, err := binding.state.objects.PrimaryKey(row)
	id, integer := key.Integer()
	if err != nil || !present || !integer {
		return auth.Credential{}, false, invalidAccountCredential()
	}
	permissions, err := store.state.permissions(ctx, reader, id)
	if err != nil {
		return auth.Credential{}, false, err
	}
	credential, err := binding.PrepareCredential(generation, row, permissions)
	if err != nil {
		return auth.Credential{}, false, err
	}
	return credential, true, nil
}

func bindPermissionReader[M, G, P any](binding Binding[M], groupDescriptor orm.RelationObjectDescriptor[G], permissionDescriptor orm.RelationObjectDescriptor[P]) (func(context.Context, db.Queryer, int64) ([]auth.Permission, error), error) {
	if !zeroModelDescriptor(groupDescriptor) || !zeroModelDescriptor(permissionDescriptor) {
		return nil, invalidCredentialStore()
	}
	project, role := binding.state.project, binding.state.role
	group, err := orm.BindModel(project, role.Catalog.Group, groupDescriptor)
	if err != nil {
		return nil, err
	}
	permission, err := orm.BindModel(project, role.Catalog.Permission, permissionDescriptor)
	if err != nil {
		return nil, err
	}
	accountMetadata, _ := project.Model(role.Model)
	accountKey, valid := primaryField(accountMetadata)
	if !valid {
		return nil, invalidCredentialStore()
	}
	memberKey := orm.NewAutoField[M](accountKey)
	direct, err := orm.BindInverseManyToManyQuery(binding.state.model, role.Permissions, permission)
	if err != nil {
		return nil, err
	}
	directMember, err := direct.Integer(memberKey)
	if err != nil {
		return nil, err
	}
	members, err := orm.BindInverseManyToManyQuery(binding.state.model, role.Groups, group)
	if err != nil {
		return nil, err
	}
	groupMember, err := members.Integer(memberKey)
	if err != nil {
		return nil, err
	}
	groupGrants, err := orm.BindInverseManyToManyQuery(group, role.Catalog.GroupPermissions, permission)
	if err != nil {
		return nil, err
	}
	viaGroup, err := orm.ChainRelations(groupGrants, members).Integer(memberKey)
	if err != nil {
		return nil, err
	}
	groupMetadata, _ := project.Model(role.Catalog.Group)
	permissionMetadata, _ := project.Model(role.Catalog.Permission)
	groupRevision, ok := namedField(groupMetadata, role.Catalog.GroupRevision)
	if !ok {
		return nil, invalidCredentialStore()
	}
	code, ok := namedField(permissionMetadata, role.Catalog.PermissionCode)
	if !ok {
		return nil, invalidCredentialStore()
	}
	revision, ok := namedField(permissionMetadata, role.Catalog.PermissionRevision)
	if !ok {
		return nil, invalidCredentialStore()
	}
	groupObjects, permissionObjects := orm.NewManager[G](groupDescriptor), orm.NewManager[P](permissionDescriptor)
	groupRevisionField := orm.NewIntegerField[G](groupRevision)
	codeField, revisionField := orm.NewStringField[P](code), orm.NewIntegerField[P](revision)
	type grant struct {
		code     string
		revision int64
	}
	projection := orm.Project2(codeField, revisionField, func(code string, revision int64) grant { return grant{code: code, revision: revision} })
	return func(ctx context.Context, reader db.Queryer, key int64) ([]auth.Permission, error) {
		invalidGroup, err := groupObjects.Using(reader).Filter(groupMember.Exact(key), groupRevisionField.LessThanOrEqual(0)).Exists(ctx)
		if err != nil {
			return nil, err
		}
		if invalidGroup {
			return nil, invalidAccountCredential()
		}
		source := permissionObjects.Using(reader).Filter(orm.Or(directMember.Exact(key), viaGroup.Exact(key))).OrderBy(codeField.Asc()).Distinct()
		selected, err := orm.SelectQuery(source, projection)
		if err != nil {
			return nil, err
		}
		selected, err = selected.Limit(auth.MaximumPermissions + 1)
		if err != nil {
			return nil, err
		}
		rows, err := selected.All(ctx)
		if err != nil {
			return nil, err
		}
		if len(rows) > auth.MaximumPermissions {
			return nil, invalidAccountCredential()
		}
		result := make([]auth.Permission, len(rows))
		for i, row := range rows {
			if row.revision <= 0 || !utf8.ValidString(row.code) || code.MaxLength > 0 && utf8.RuneCountInString(row.code) > code.MaxLength {
				return nil, invalidAccountCredential()
			}
			result[i] = auth.Permission(row.code)
		}
		return result, nil
	}, nil
}

func (store *CredentialStore[M]) valid() error {
	if store == nil || store.state == nil || store.state.binding.state == nil || nilModelValue(store.state.backend) || store.state.permissions == nil {
		return invalidCredentialStore()
	}
	return nil
}

func primaryField(model ir.Model) (ir.Field, bool) {
	for _, field := range model.Fields {
		if field.PrimaryKey {
			return field, field.Kind == ir.FieldAuto
		}
	}
	return ir.Field{}, false
}
func namedField(model ir.Model, name string) (ir.Field, bool) {
	for _, field := range model.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return ir.Field{}, false
}
func zeroModelDescriptor(value any) bool {
	typeOf := reflect.TypeOf(value)
	return typeOf != nil && typeOf.Kind() == reflect.Struct && typeOf.Name() != "" && typeOf.Size() == 0
}
func nilModelValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}
func invalidCredentialStore() error {
	return &auth.Error{Code: auth.CodeInvalidConfig, Detail: "account credential store is uninitialized or invalid"}
}
func accountReadFailure(err error) error {
	return &auth.Error{Code: auth.CodeCredential, Detail: "account credential snapshot failed", Cause: err}
}
