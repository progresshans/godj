package identity

import (
	"context"
	"reflect"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// CheckUserChange performs advisory model-form uniqueness checks against one
// current authorized snapshot. The form must describe the built-in User and
// retain the server-observed ID/revision in its model candidate. Field errors
// do not suppress checks of unrelated valid fields. No profile, credential,
// session, membership or audit mutation occurs, and no write authority is
// retained: UpdateUser must still perform its own final transaction checks.
func (manager *Manager) CheckUserChange(ctx context.Context, actor auth.Principal, id, revision int64, bound formmodel.BoundForm) error {
	if err := manager.validCall(ctx, actor); err != nil {
		return err
	}
	if err := validUserRevision(id, revision); err != nil {
		return err
	}
	key, keyPresent := bound.Candidate().Integer("id")
	version, versionPresent := bound.Candidate().Integer("revision")
	if !bound.Form().Bound() || !reflect.DeepEqual(bound.Model(), models.UserDescriptor{}.Metadata()) || !keyPresent || key != id || !versionPresent || version != revision {
		return managementError(CodeInvalidInput, "user_form", nil)
	}
	_, err := managementSnapshot(ctx, manager, func(reader db.Queryer) (struct{}, error) {
		row, _, err := manager.managedUserForChange(ctx, reader, actor.ID(), id, revision)
		if err != nil {
			return struct{}{}, err
		}
		failures, err := bound.CheckDatabase(ctx, formmodel.DatabaseChecks{
			UniqueFields: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
				return models.UserObjects.ValidateUniqueFields(ctx, reader, values, &row)
			},
			Constraints: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
				return models.UserObjects.ValidateUniqueConstraints(ctx, reader, values, &row)
			},
		})
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, validation.Reject(failures, nil)
	})
	return err
}
