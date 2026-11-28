package accountstoretest

import (
	"errors"
	"slices"
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

// Bind an opaque session allocation only after its public store/record has
// been checked by the caller. Preserve every pre-existing row and let the
// caller compare all remaining tables with the same native after-state.
func accountNativeSessionAddition(t *testing.T, before, after accountNativeState) (accountNativeState, int64) {
	t.Helper()
	known := map[int64]bool{}
	for _, row := range before.sessions {
		known[row.ID] = true
	}
	var added []sessionmodels.Session
	for _, row := range after.sessions {
		if !known[row.ID] {
			added = append(added, row)
		}
	}
	if len(added) != 1 || added[0].ID <= 0 {
		t.Fatal("session setup did not add exactly one native row")
	}
	before.sessions = append(slices.Clone(before.sessions), added[0])
	slices.SortFunc(before.sessions, func(a, b sessionmodels.Session) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return before, added[0].ID
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
		state = accountNativeRows(t, scope)
		return nil
	})
	if err != nil || visits != 1 {
		t.Fatal("native account observation did not complete", err, visits)
	}
	return state
}

func accountNativeRows(t *testing.T, scope db.Queryer) accountNativeState {
	t.Helper()
	return accountNativeState{
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
}
