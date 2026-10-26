package consumer

import (
	"context"
	"errors"
	"testing"

	"example.com/godj-project-bundle/access"
	"example.com/godj-project-bundle/people"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/tickets"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/schema/ir"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/settings/database"
	"github.com/progresshans/godj/validation"
)

func TestSelectedAccountReset(t *testing.T) {
	withResetDatabases(t, []ir.Schema{access.GoDjRelationSchema(), people.GoDjRelationSchema(), tickets.GoDjRelationSchema()}, func(t *testing.T, backend database.Backend, reopen func() database.Backend) {
		binding, err := project.BindAccount()
		resetCheck(t, err)
		hasher, encoded := accountPassword(t)
		row := createBoundAccount(t, backend, binding, people.NewMemberCreate("selected-target", "target@example.test", encoded, "NZ"))
		role, err := binding.Role()
		resetCheck(t, err)
		if role.Active.Constant != nil {
			row, err = people.MemberObjects.Patch(t.Context(), backend, row, people.MemberPatch{}.WithEnabled(false))
			resetCheck(t, err)
		}
		if role.Active.Constant == nil || *role.Active.Constant {
			t.Run("different_model_and_generation", func(t *testing.T) {
				// Match the original creation's rollback attempt. PostgreSQL
				// sequences retain that consumed key while SQLite rolls it back.
				fault := errors.New("replacement fixture rollback")
				if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
					if _, err := people.ReplacementObjects.Create(t.Context(), scope, people.NewReplacementCreate("rolled-back", "rolled-back@example.test", encoded, "NZ")); err != nil {
						return err
					}
					return fault
				}); !errors.Is(err, fault) {
					t.Fatal("replacement key fixture did not roll back", err)
				}
				replacement, err := people.ReplacementObjects.Create(t.Context(), backend, people.NewReplacementCreate("selected-target", "target@example.test", encoded, "NZ"))
				resetCheck(t, err)
				replacementRole := role
				replacementRole.Model = ir.ModelIdentity{AppLabel: "people", ModelName: "replacement"}
				replacementBinding, err := accountmodel.Bind(replacementRole, people.ReplacementDescriptor{}, access.GoDjRelationSchema(), people.GoDjRelationSchema(), tickets.GoDjRelationSchema())
				resetCheck(t, err)
				credentials, err := accountmodel.NewCredentialStore(backend, replacementBinding, access.TeamDescriptor{}, access.GrantDescriptor{})
				resetCheck(t, err)
				replacementStore, err := accountmodel.NewSessionStore(credentials, sessiondb.Config{})
				resetCheck(t, err)
				selected, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
				resetCheck(t, err)
				assertResetModelBoundary(t, backend, binding, replacementBinding, selected, replacementStore, row, replacement, hasher)
			})
		}
		other, err := people.MemberObjects.Create(t.Context(), backend, people.NewMemberCreate("selected-other", "other@example.test", encoded, "KR"))
		resetCheck(t, err)
		runSelectedResetHTTP(t, backend, reopen, binding, project.NewAccountSessionStore, project.NewAccountPasswordResetter, hasher, row, other, func(_ context.Context, raw string, current people.Member) (validation.Errors, error) {
			if current.Subject != row.Subject || current.Region != "NZ" || current.Secret != "" {
				return validation.Errors{}, errors.New("selected custom reset lost typed required profile")
			}
			if len(raw) < 12 {
				return validation.NewErrors(validation.New("password", "password_too_short")), nil
			}
			return validation.Errors{}, nil
		})
	})
}
