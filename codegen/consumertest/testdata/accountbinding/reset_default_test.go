package consumer

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/godj-project-bundle/models"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/validation"
)

func TestSelectedAccountReset(t *testing.T) {
	withResetDatabases(t, []ir.Schema{models.GoDjRelationSchema()}, func(t *testing.T, backend database.Backend, reopen func() database.Backend) {
		binding, err := project.BindAccount()
		resetCheck(t, err)
		hasher, encoded := accountPassword(t)
		row := createBoundAccount(t, backend, binding, models.NewUserCreate("selected-target", "target", encoded, time.Now().UTC()).WithFirstName("Selected profile").WithEmail("target@example.test"))
		other, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("selected-other", "other", encoded, time.Now().UTC()).WithFirstName("Other profile").WithEmail("other@example.test"))
		resetCheck(t, err)
		runSelectedResetHTTP(t, backend, reopen, binding, project.NewAccountSessionStore, project.NewAccountPasswordResetter, hasher, row, other, func(_ context.Context, raw string, current orm.ModelView[models.User]) (validation.Errors, error) {
			if passwordViewString(t, current, "principal_id") != row.PrincipalID || passwordViewString(t, current, "first_name") != "Selected profile" || passwordViewString(t, current, "encoded_password") != "" {
				return validation.Errors{}, errors.New("selected default reset lost typed profile")
			}
			if len(raw) < 12 {
				return validation.NewErrors(validation.New("password", "password_too_short")), nil
			}
			return validation.Errors{}, nil
		})
	})
}
