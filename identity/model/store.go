package model

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"

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
	relations        *accountRelationReader
}

func (CredentialStore[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.CredentialStore{redacted}"))
}

// NewCredentialStore binds the selected group and permission descriptors to
// the SAME project owner as the account. No default models, field-name
// assumptions, caller-written SQL or separately supplied permission loader are
// involved. Construction checks configuration without performing I/O.
func NewCredentialStore[M, G, P any](backend db.SnapshotReader, binding Binding[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P]) (*CredentialStore[M], error) {
	if nilModelValue(backend) {
		return nil, invalidCredentialStore()
	}
	state, err := prepareCredentialStore(binding, groups, permissions)
	if err != nil {
		return nil, err
	}
	state.backend = backend
	return &CredentialStore[M]{state: &state}, nil
}

// A declaration may be inspected before a command acquires a database. Keep
// its typed/source validation separate from a live store; no unbound store or
// placeholder backend is exposed to callers.
func prepareCredentialStore[M, G, P any](binding Binding[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P]) (credentialStoreState[M], error) {
	var zero credentialStoreState[M]
	if binding.state == nil {
		return zero, invalidCredentialStore()
	}
	load, err := bindAccountRelations(binding, groups, permissions)
	if err != nil {
		return zero, &auth.Error{Code: auth.CodeInvalidConfig, Detail: "account permission catalog binding failed", Cause: err}
	}
	metadata, exists := binding.state.project.Model(binding.state.role.Model)
	if !exists {
		return zero, invalidCredentialStore()
	}
	key, exists := primaryField(metadata)
	if !exists {
		return zero, invalidCredentialStore()
	}
	return credentialStoreState[M]{
		binding:   binding,
		login:     orm.NewStringField[M](binding.state.credentialFields[binding.state.role.Login]),
		principal: orm.NewStringField[M](binding.state.credentialFields[binding.state.role.PrincipalID]),
		key:       orm.NewAutoField[M](key), permissions: load.permissions, relations: load,
	}, nil
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
	type observedCredential struct {
		credential auth.Credential
		found      bool
	}
	result, err := db.ReadSnapshotResult(ctx, store.state.backend, func(work context.Context, reader db.Queryer) (observedCredential, error) {
		candidate, found, err := store.lookupIn(work, reader, predicate)
		return observedCredential{candidate, found}, err
	})
	if err != nil {
		return auth.Credential{}, false, accountReadFailure(err)
	}
	return result.credential, result.found, nil
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

func (store *CredentialStore[M]) valid() error {
	if store == nil || store.state == nil || store.state.binding.state == nil || nilModelValue(store.state.backend) || store.state.permissions == nil || store.state.relations == nil {
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
