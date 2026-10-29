package model

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// TransitionTargetConfig selects the durable login/profile values supplied by
// an explicit operator mapping. Credential identity, password, roles, revision,
// timestamps and catalog memberships are copied by the transition owner.
// Persisted file references and their dimensions may be selected explicitly;
// this operation neither uploads nor deletes storage objects. Validate on the
// enclosing Transition owns any application-specific mapping policy. As with
// Creator, typed inputs must already have passed their input cleaning policy.
type TransitionTargetConfig[M any] struct {
	Fields      []string
	Relations   []ProfileRelation[M]
	Collections []ProfileCollection[M]
}

// TransitionTarget binds actual typed collection capabilities and profile
// choice policies to one complete source. Construction performs no I/O.
type TransitionTarget[M any] struct{ state *transitionTarget[M] }
type transitionTarget[M any] struct {
	binding             Binding[M]
	metadata            ir.Model
	allowed             map[string]bool
	relations           []ProfileRelation[M]
	collections         []ProfileCollection[M]
	auditFields         []string
	incoming            []query.Plan
	groups, permissions func(M, []int64) (orm.CollectionChange, error)
}

func (TransitionTarget[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.TransitionTarget{redacted}"))
}

func NewTransitionTarget[M, G, P, GL, PL any](binding Binding[M], groups orm.ManyToMany[M, G, GL], permissions orm.ManyToMany[M, P, PL], config TransitionTargetConfig[M]) (TransitionTarget[M], error) {
	var zero TransitionTarget[M]
	if binding.state == nil || len(config.Fields) == 0 || len(config.Fields) >= admin.MaximumChangedFields {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	role, project := binding.state.role, binding.state.project
	for _, err := range []error{groups.ValidateSource(project, role.Model, role.Groups), permissions.ValidateSource(project, role.Model, role.Permissions)} {
		if err != nil {
			return zero, managementError(CodeInvalidConfig, err)
		}
	}
	if err := validateAccountCollectionStorage(project, role, append(slices.Clone(config.Fields), role.Groups, role.Permissions)); err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	protected := map[string]bool{role.PrincipalID: true, role.Password: true, role.Revision: true, role.CreatedAt: true, role.LastLogin: true, role.Active.Field: true, role.Staff.Field: true, role.Superuser.Field: true, role.Groups: true, role.Permissions: true}
	allowed := make(map[string]bool, len(config.Fields))
	for _, name := range config.Fields {
		if name == "" || allowed[name] || protected[name] {
			return zero, managementError(CodeInvalidConfig, nil)
		}
		if _, collection := namedCollection(metadata, name); !collection {
			field, found := namedField(metadata, name)
			if !found || field.PrimaryKey {
				return zero, managementError(CodeInvalidConfig, nil)
			}
		}
		allowed[name] = true
	}
	if !allowed[role.Login] {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	relations, err := prepareProfileRelations(binding, config.Fields, config.Relations)
	if err != nil {
		return zero, err
	}
	collections, err := prepareProfileCollections(binding, config.Fields, config.Collections)
	if err != nil {
		return zero, err
	}
	initial, err := orm.InitialModelValues(metadata)
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	for _, field := range metadata.Fields {
		if field.PrimaryKey || protected[field.Name] || allowed[field.Name] {
			continue
		}
		value := initial[field.Name]
		text, textual := value.String()
		if value.IsNull() && !field.Nullable || textual && text == "" && !field.Blank && field.Default == nil || field.Relation != nil && !value.IsNull() {
			return zero, managementError(CodeInvalidConfig, nil)
		}
	}
	for _, field := range metadata.ManyToMany {
		if field.Name != role.Groups && field.Name != role.Permissions && !field.Blank && !allowed[field.Name] {
			return zero, managementError(CodeInvalidConfig, nil)
		}
	}
	audit := append(slices.Clone(config.Fields), role.Password, role.Groups, role.Permissions)
	for _, flag := range []ir.AccountBoolean{role.Active, role.Staff, role.Superuser} {
		if flag.Field != "" {
			audit = append(audit, flag.Field)
		}
	}
	if _, err := admin.PrepareEventTemplate("transition-creation", role.Model.AppLabel+"."+role.Model.ModelName, admin.ActionAdd, audit, ""); err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	incoming, err := transitionIncomingPlans(project, role.Model)
	if err != nil {
		return zero, err
	}
	return TransitionTarget[M]{&transitionTarget[M]{binding: binding, metadata: metadata, allowed: allowed, relations: relations, collections: collections, auditFields: audit, incoming: incoming,
		groups:      func(row M, keys []int64) (orm.CollectionChange, error) { return groups.SetKeysChange(row, keys) },
		permissions: func(row M, keys []int64) (orm.CollectionChange, error) { return permissions.SetKeysChange(row, keys) },
	}}, nil
}

func transitionIncomingPlans(project orm.ProjectBinding, target ir.ModelIdentity) ([]query.Plan, error) {
	var incoming []query.Plan
	for _, relation := range project.ForwardRelations() {
		if relation.Target != target || relation.Source == target {
			continue
		}
		source, found := project.Model(relation.Source)
		if !found {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		field := query.NewFieldRef(relation.Field, relation.Column, query.FieldInteger, relation.Nullable)
		condition, err := query.NewExpression(query.NewCondition(field, query.LookupIsNull, query.Boolean(false)))
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
		plan, err := query.NewPlan(source.DBTable, []query.FieldRef{field}).WithWhere(condition)
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
		plan, err = plan.WithLimit(1)
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
		incoming = append(incoming, plan)
	}
	return incoming, nil
}

// SourceAccount is exportable evidence of the reviewed source identity. A
// target key does not exist until its INSERT has completed in the native fence.
type SourceAccount struct {
	ID       int64 `json:"id"`
	Revision int64 `json:"revision"`
}

// AccountCreation pairs an explicit source key/revision with typed target
// login/profile intent. Input must be unsaved; no target key is anticipated.
// References names selected FK/OneToOne fields omitted from Input. Each source
// correspondence is checked in the reviewed snapshot and bound to its actual
// target key before the complete typed row is allocated. Current relation
// policies run against the complete identity and membership graph before commit.
// PrepareCreation copies each reference map; later caller edits do not change
// the reviewed intent.
type AccountCreation[M any] struct {
	Source     SourceAccount
	Input      orm.ModelInput[M]
	References map[string]TransitionTargetReference
}

func (AccountCreation[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.AccountCreation{redacted}"))
}

type transitionCreationCandidate[M any] struct {
	prepared   orm.PreparedModel[M]
	row        transitionRow[M]
	references map[string]TransitionTargetReference
	resolve    func(context.Context, db.Queryer, auth.Principal, transitionTargetMappings, bool) (transitionCreationCandidate[M], error)
}

func prepareTransitionTarget[S, T any](ctx context.Context, scope db.Queryer, actor auth.Principal, target *transitionTarget[T], source transitionRow[S], memberships accountMemberships, sourceRole ir.AccountRole, input orm.ModelInput[T], references map[string]TransitionTargetReference, mappings transitionTargetMappings, createdAt time.Time, generation [32]byte, allocating bool) (transitionCreationCandidate[T], error) {
	var zero transitionCreationCandidate[T]
	metadata, valid := input.Model()
	if !valid || !metadata.Equal(target.metadata) {
		return zero, managementError(CodeInvalidInput, nil)
	}
	if key, bound := input.TargetKey(); bound && !key.IsNull() {
		return zero, managementError(CodeInvalidInput, nil)
	}
	original := input
	input, pending, err := bindTransitionTargetReferences(target, input, references, mappings)
	if err != nil {
		return zero, err
	}
	values := input.Values()
	for name := range values {
		if !target.allowed[name] {
			return zero, managementError(CodeInvalidInput, nil)
		}
	}
	role, objects := target.binding.state.role, target.binding.state.objects
	if _, present := values[role.Login]; !present {
		return zero, profileInputError(role.Login, "required")
	}
	if err := normalizeAccountInput(metadata, role, values); err != nil {
		return zero, err
	}
	profile := input.Collections()
	if err := normalizeAccountCollections(role, target.allowed, target.collections, profile); err != nil {
		return zero, err
	}
	for _, field := range metadata.ManyToMany {
		if field.Name != role.Groups && field.Name != role.Permissions {
			if _, present := profile[field.Name]; !present {
				profile[field.Name] = []int64{}
			}
		}
	}
	values[role.PrincipalID] = source.values[sourceRole.PrincipalID]
	values[role.Password] = source.values[sourceRole.Password]
	values[role.Revision] = query.Integer(1)
	for _, pair := range []struct{ old, next ir.AccountBoolean }{{sourceRole.Active, role.Active}, {sourceRole.Staff, role.Staff}, {sourceRole.Superuser, role.Superuser}} {
		value := transitionBoolean(source.values, pair.old)
		if pair.next.Constant != nil {
			if *pair.next.Constant != value {
				return zero, managementError(CodeConflict, nil)
			}
		} else {
			values[pair.next.Field] = query.Boolean(value)
		}
	}
	if role.CreatedAt != "" {
		values[role.CreatedAt] = query.DateTime(createdAt)
		if sourceRole.CreatedAt != "" {
			values[role.CreatedAt] = source.values[sourceRole.CreatedAt]
		}
	}
	if role.LastLogin != "" {
		values[role.LastLogin] = query.Null()
		if sourceRole.LastLogin != "" {
			values[role.LastLogin] = source.values[sourceRole.LastLogin]
		}
	}
	initial, err := orm.InitialModelValues(metadata)
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	maps.Copy(initial, values)
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			continue
		}
		if pending[field.Name] {
			delete(initial, field.Name)
			continue
		}
		value := initial[field.Name]
		if value.IsNull() && !field.Nullable {
			return zero, profileInputError(field.Name, "required")
		}
		if text, textual := value.String(); textual && text == "" && !field.Blank && !field.NonEditable {
			return zero, profileInputError(field.Name, "required")
		}
	}
	if len(pending) != 0 {
		if err := validateTransitionScalarValues(metadata, initial, true, pending); err != nil {
			return zero, err
		}
		knownRelations := slices.DeleteFunc(slices.Clone(target.relations), func(relation ProfileRelation[T]) bool { return pending[relation.state.field.Name] })
		if err := checkProfileRelations(ctx, scope, actor, ProfileRelationOrigin{operation: ProfileRelationTransition}, knownRelations, initial); err != nil {
			return zero, err
		}
		if err := checkProfileCollections(ctx, scope, actor, ProfileRelationOrigin{operation: ProfileRelationTransition}, target.collections, profile); err != nil {
			return zero, err
		}
		namespace, err := target.binding.CredentialDomain(generation)
		if err != nil {
			return zero, err
		}
		if _, err := target.binding.prepareCandidateCredentialValues(namespace, initial, memberships.grants); err != nil {
			return zero, err
		}
		return transitionCreationCandidate[T]{references: maps.Clone(references), row: transitionRow[T]{revision: 1, values: initial, memberships: memberships, profile: profile},
			resolve: func(ctx context.Context, reader db.Queryer, admitted auth.Principal, actual transitionTargetMappings, allocating bool) (transitionCreationCandidate[T], error) {
				return prepareTransitionTarget(ctx, reader, admitted, target, source, memberships, sourceRole, original, references, actual, createdAt, generation, allocating)
			}}, nil
	}
	prepared, err := objects.PrepareValues(nil, values)
	if err != nil {
		return zero, managementError(CodeInvalidInput, err)
	}
	row, err := prepared.Model()
	if err != nil {
		return zero, err
	}
	values, err = objects.ModelValues(row)
	if err != nil {
		return zero, err
	}
	if err := validateTransitionValues(ctx, scope, objects, row, values, true); err != nil {
		return zero, err
	}
	if !allocating {
		if err := checkProfileRelations(ctx, scope, actor, ProfileRelationOrigin{operation: ProfileRelationTransition}, target.relations, values); err != nil {
			return zero, err
		}
		if err := checkProfileCollections(ctx, scope, actor, ProfileRelationOrigin{operation: ProfileRelationTransition}, target.collections, profile); err != nil {
			return zero, err
		}
	}
	namespace, err := target.binding.CredentialDomain(generation)
	if err != nil {
		return zero, err
	}
	if _, err := target.binding.prepareCandidateCredential(namespace, row, memberships.grants); err != nil {
		return zero, err
	}
	return transitionCreationCandidate[T]{prepared: prepared, references: maps.Clone(references), row: transitionRow[T]{row: row, revision: 1, values: values, memberships: accountMemberships{
		groups: slices.Clone(memberships.groups), direct: slices.Clone(memberships.direct), grants: slices.Clone(memberships.grants)}, profile: maps.Clone(profile)}}, nil
}
