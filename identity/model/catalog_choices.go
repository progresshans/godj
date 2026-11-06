package model

import (
	"context"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
)

// GroupPermissionChoiceSource supplies bounded current selections from the
// actual catalog types. Assignment requires the group's operation and every
// additional grant, not separate permission-catalog read access. The final
// write resolves the selected keys again inside its own authorized transaction.
func (catalog *Catalog[G, P]) GroupPermissionChoiceSource(action admin.Action) (admin.RelatedChoices, error) {
	if catalog == nil || catalog.state == nil {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	groups, permissions := catalog.state.groups, catalog.state.permissions
	group, permission := groups.state, permissions.state
	if !group.allowed[group.collection] {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	required, operation := group.permissions.add, admin.ChoiceAdd
	switch action {
	case admin.ActionAdd:
	case admin.ActionChange:
		required, operation = group.permissions.change, admin.ChoiceChange
	default:
		return nil, managementError(CodeInvalidInput, nil)
	}
	code, _ := namedField(permission.model, permission.code)
	name, _ := namedField(permission.model, permission.name)
	choices, err := formmodel.NewRelationChoices(group.shared.project, group.identity, group.collection, permissions.Manager(), formmodel.ModelChoiceConfig[P]{
		Label: func(row P) (string, error) {
			values, _, _, err := permission.inspect(row, true)
			if err != nil {
				return "", err
			}
			code, _ := values[permission.code].String()
			name, _ := values[permission.name].String()
			return code + " — " + name, nil
		},
		Search: func(term string) ([]orm.Predicate[P], error) {
			if term == "" {
				return nil, nil
			}
			return []orm.Predicate[P]{orm.Or(orm.NewStringField[P](code).IContains(term), orm.NewStringField[P](name).IContains(term))}, nil
		},
		Order: []orm.Ordering[P]{permission.key.Asc()},
	})
	if err != nil {
		return nil, err
	}
	return admin.NewRelatedChoices(admin.RelatedChoiceConfig[P]{Choices: choices, Permission: required[0],
		Scope: func(ctx context.Context, actor auth.Principal, origin admin.ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[P]) error) error {
			if err := groups.validCall(ctx, actor); err != nil {
				return catalogChoiceError(err)
			}
			if origin.Source() != group.identity || origin.Operation() != operation {
				return managementError(CodeInvalidInput, nil)
			}
			_, err := managementSnapshot(ctx, group.shared.reader, func(work context.Context, scope db.Queryer) (auth.Principal, bool, error) {
				return group.shared.authorize(work, scope, actor, required)
			}, func(_ context.Context, scope db.Queryer, _ auth.CredentialDomain) (struct{}, bool, error) {
				return struct{}{}, true, visit(scope, nil)
			})
			return catalogChoiceError(err)
		},
	})
}

func catalogChoiceError(err error) error {
	if failure, ok := err.(*Error); ok && failure != nil && failure.Code == CodePermission {
		return admin.NewOperationError(admin.OperationDenied, err)
	}
	return err
}
