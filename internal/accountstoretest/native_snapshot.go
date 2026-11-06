package accountstoretest

import (
	"errors"
	"testing"

	auditmodels "github.com/progresshans/godj/account/audit/models"
	domainmodels "github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
)

type accountNativeState struct {
	users       []models.User
	groups      []models.Group
	permissions []models.Permission
	memberships []models.UserGroupsLink
	direct      []models.UserPermissionsLink
	grants      []models.GroupPermissionsLink
	sessions    []sessionmodels.Session
	audits      []auditmodels.Event
	domains     []domainmodels.Transition
	mappings    []domainmodels.Mapping
}

// Observe the original native backend with an uncanceled context, independently
// of management readers and injected scopes. Keep every typed value and row ID.
func accountNativeSnapshot(t *testing.T, backend Backend) accountNativeState {
	t.Helper()
	var state accountNativeState
	visits := 0
	err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		visits++
		if visits != 1 {
			return errors.New("account observation repeated its native callback")
		}
		state = accountNativeState{
			users:       deletionRows(t, scope, models.UserObjects),
			groups:      deletionRows(t, scope, models.GroupObjects),
			permissions: deletionRows(t, scope, models.PermissionObjects),
			memberships: deletionRows(t, scope, models.UserGroupsLinkObjects),
			direct:      deletionRows(t, scope, models.UserPermissionsLinkObjects),
			grants:      deletionRows(t, scope, models.GroupPermissionsLinkObjects),
			sessions:    deletionRows(t, scope, sessionmodels.SessionObjects),
			audits:      deletionRows(t, scope, auditmodels.EventObjects),
			domains:     deletionRows(t, scope, domainmodels.TransitionObjects),
			mappings:    deletionRows(t, scope, domainmodels.MappingObjects),
		}
		return nil
	})
	if err != nil || visits != 1 {
		t.Fatal("native account observation did not complete", err, visits)
	}
	return state
}
