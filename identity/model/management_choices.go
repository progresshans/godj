package model

import (
	"context"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
)

func bindAccountChoices[M, G, P any](management *AccountManagement[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P]) error {
	state := management.state
	role := state.reader.state.store.state.binding.state.role
	for _, action := range []admin.Action{admin.ActionAdd, admin.ActionChange} {
		allowed := state.editor.state.allowed
		if action == admin.ActionAdd {
			allowed = state.creator.state.allowed
		}
		if allowed[role.Groups] {
			choice, err := accountChoiceSource(management, action, role.Groups, role.Catalog.GroupName, "", role.Catalog.GroupRevision, orm.NewManager[G](groups))
			if err != nil {
				return err
			}
			state.choices[action] = append(state.choices[action], choice)
		}
		if allowed[role.Permissions] {
			choice, err := accountChoiceSource(management, action, role.Permissions, role.Catalog.PermissionName, role.Catalog.PermissionCode, role.Catalog.PermissionRevision, orm.NewManager[P](permissions))
			if err != nil {
				return err
			}
			state.choices[action] = append(state.choices[action], choice)
		}
	}
	return nil
}

func accountChoiceSource[M, T any](management *AccountManagement[M], action admin.Action, collection, name, code, revision string, objects orm.Manager[T]) (admin.RelatedChoices, error) {
	state := management.state
	store := state.reader.state.store
	binding, role := store.state.binding, store.state.binding.state.role
	metadata, err := objects.Metadata()
	if err != nil {
		return nil, err
	}
	key, present := primaryField(metadata)
	if !present {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	nameField, _ := namedField(metadata, name)
	codeField, _ := namedField(metadata, code)
	required, operation := state.editor.state.required, admin.ChoiceChange
	if action == admin.ActionAdd {
		required, operation = state.creator.state.required, admin.ChoiceAdd
	}
	choices, err := formmodel.NewRelationChoices(binding.state.project, role.Model, collection, objects, formmodel.ModelChoiceConfig[T]{
		Label: func(row T) (string, error) {
			values, _, _, err := inspectCatalogRow(objects, metadata, key.Name, name, code, revision, row, true)
			if err != nil {
				return "", err
			}
			label, _ := values[name].String()
			if code != "" {
				value, _ := values[code].String()
				label = value + " — " + label
			}
			return label, nil
		},
		Search: func(term string) ([]orm.Predicate[T], error) {
			if term == "" {
				return nil, nil
			}
			predicate := orm.NewStringField[T](nameField).IContains(term)
			if code != "" {
				predicate = orm.Or(predicate, orm.NewStringField[T](codeField).IContains(term))
			}
			return []orm.Predicate[T]{predicate}, nil
		},
		Order: []orm.Ordering[T]{orm.NewAutoField[T](key).Asc()},
	})
	if err != nil {
		return nil, err
	}
	return admin.NewRelatedChoices(admin.RelatedChoiceConfig[T]{Choices: choices, Permission: required[0],
		Scope: func(ctx context.Context, actor auth.Principal, origin admin.ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[T]) error) error {
			if err := management.validCall(ctx, actor); err != nil {
				return catalogChoiceError(err)
			}
			if origin.Source() != role.Model || origin.Operation() != operation {
				return managementError(CodeInvalidInput, nil)
			}
			_, err := managementSnapshot(ctx, store.state.backend, func(work context.Context, scope db.Queryer) (auth.Principal, bool, error) {
				return store.AuthorizeIn(work, scope, actor, state.reader.state.overlay, required[0], required[1:]...)
			}, func(_ context.Context, scope db.Queryer, _ auth.CredentialDomain) (struct{}, bool, error) {
				return struct{}{}, true, visit(scope, nil)
			})
			return catalogChoiceError(err)
		},
	})
}

func (management *AccountManagement[M]) validCall(ctx context.Context, actor auth.Principal) error {
	if management == nil || management.state == nil {
		return managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) {
		return managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return managementError(CodeInvalidInput, err)
	}
	if !actor.Authenticated() {
		return managementError(CodePermission, nil)
	}
	return nil
}
