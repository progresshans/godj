//go:build darwin || linux

package projectmigrateproduct_test

import (
	"database/sql"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Read the current durable identity independently of runtime authentication.
// The retained system credential is a tombstone, not the login authority.
type authenticatedRestartIdentityRow struct {
	ID                                                                 int64
	PrincipalID, Username, EncodedPassword, FirstName, LastName, Email string
	Active, Staff, Superuser                                           bool
	DateJoined                                                         time.Time
	LastLogin                                                          sql.NullTime
	Revision                                                           int64
}
type authenticatedRestartGrantRow struct {
	UserID, PermissionID int64
	Code, Name           string
	Revision             int64
}
type authenticatedRestartTransitionRow struct {
	ID, SourceID, UserID           int64
	Kind, PrincipalID, Fingerprint string
	Staff, Superuser               bool
	At                             time.Time
}
type authenticatedRestartIdentityState struct {
	Counts      [4]int64
	Users       []authenticatedRestartIdentityRow
	Grants      []authenticatedRestartGrantRow
	Transitions []authenticatedRestartTransitionRow
}
type authenticatedRestartRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}
type authenticatedRestartQuery func(string) (authenticatedRestartRows, func() error, error)

func authenticatedRestartReadIdentity(t *testing.T, query authenticatedRestartQuery, table func(string) string) authenticatedRestartIdentityState {
	t.Helper()
	var state authenticatedRestartIdentityState
	read := func(statement string, scan func(authenticatedRestartRows) error) {
		t.Helper()
		rows, closeRows, err := query(statement)
		if err != nil {
			t.Fatal("query restart identity state failed")
		}
		for rows.Next() {
			if err := scan(rows); err != nil {
				_ = closeRows()
				t.Fatal("scan restart identity state failed")
			}
		}
		rowErr, closeErr := rows.Err(), closeRows()
		if rowErr != nil || closeErr != nil {
			t.Fatal("finish restart identity state failed")
		}
	}
	read(`SELECT "id", "principal_id", "username", "encoded_password", "first_name", "last_name", "email", "active", "staff", "superuser", "date_joined", "last_login", "revision" FROM `+table("godj_identity_user")+` ORDER BY "id"`, func(rows authenticatedRestartRows) error {
		var row authenticatedRestartIdentityRow
		err := rows.Scan(&row.ID, &row.PrincipalID, &row.Username, &row.EncodedPassword, &row.FirstName, &row.LastName, &row.Email, &row.Active, &row.Staff, &row.Superuser, &row.DateJoined, &row.LastLogin, &row.Revision)
		state.Users = append(state.Users, row)
		return err
	})
	read(`SELECT "link"."source_id", "permission"."id", "permission"."code", "permission"."name", "permission"."revision" FROM `+table("godj_identity_user_permissions")+` AS "link" JOIN `+table("godj_identity_permission")+` AS "permission" ON "permission"."id" = "link"."target_id" ORDER BY "permission"."code", "link"."source_id"`, func(rows authenticatedRestartRows) error {
		var row authenticatedRestartGrantRow
		err := rows.Scan(&row.UserID, &row.PermissionID, &row.Code, &row.Name, &row.Revision)
		state.Grants = append(state.Grants, row)
		return err
	})
	read(`SELECT "id", "source_kind", "source_id", "principal_id", "user_id", "staff", "superuser", "transitioned_at", "source_fingerprint" FROM `+table("godj_system_identity_transition")+` ORDER BY "id"`, func(rows authenticatedRestartRows) error {
		var row authenticatedRestartTransitionRow
		err := rows.Scan(&row.ID, &row.Kind, &row.SourceID, &row.PrincipalID, &row.UserID, &row.Staff, &row.Superuser, &row.At, &row.Fingerprint)
		state.Transitions = append(state.Transitions, row)
		return err
	})
	read(`SELECT (SELECT COUNT(*) FROM `+table("godj_identity_permission")+`), (SELECT COUNT(*) FROM `+table("godj_identity_group")+`), (SELECT COUNT(*) FROM `+table("godj_identity_group_permissions")+`), (SELECT COUNT(*) FROM `+table("godj_identity_user_groups")+`)`, func(rows authenticatedRestartRows) error {
		return rows.Scan(&state.Counts[0], &state.Counts[1], &state.Counts[2], &state.Counts[3])
	})
	return state
}

func authenticatedRestartAssertIdentity(t *testing.T, snapshot authenticatedRestartDatabaseSnapshot, username, password string) {
	t.Helper()
	if snapshot.Identity.Counts != ([4]int64{5, 0, 0, 0}) {
		t.Fatal("restart identity permission/group cardinalities differ")
	}
	rows := snapshot.Identity.Users
	if len(rows) != 1 {
		t.Fatalf("restart identity user count=%d, want 1", len(rows))
	}
	row := rows[0]
	if row.ID != 1 || row.PrincipalID != "article-development-admin" || row.Username != username ||
		row.EncodedPassword == "" || strings.Contains(row.EncodedPassword, password) ||
		!row.Active || !row.Staff || !row.Superuser || row.Revision != 1 || row.DateJoined.IsZero() ||
		row.FirstName != "" || row.LastName != "" || row.Email != "" {
		t.Fatal("restart identity credential, profile, roles, or revision differs")
	}
	codes := []string{"godj.admin.access", "godj_conformance.add_article", "godj_conformance.change_article", "godj_conformance.delete_article", "godj_conformance.view_article"}
	if len(snapshot.Identity.Grants) != len(codes) {
		t.Fatal("restart identity grant count differs")
	}
	seen := map[int64]bool{}
	for i, grant := range snapshot.Identity.Grants {
		if grant.UserID != row.ID || grant.PermissionID <= 0 || seen[grant.PermissionID] || grant.Code != codes[i] || grant.Name != codes[i] || grant.Revision != 1 {
			t.Fatal("restart identity grant differs")
		}
		seen[grant.PermissionID] = true
	}
	transitions := snapshot.Identity.Transitions
	if len(transitions) != 1 {
		t.Fatal("restart identity receipt count differs")
	}
	receipt := transitions[0]
	fingerprint, err := hex.DecodeString(receipt.Fingerprint)
	if receipt.ID != 1 || receipt.Kind != "bootstrap" || receipt.SourceID != 0 || receipt.PrincipalID != row.PrincipalID || receipt.UserID != row.ID || !receipt.Staff || !receipt.Superuser || !receipt.At.Equal(row.DateJoined) || err != nil || len(fingerprint) != 32 || hex.EncodeToString(fingerprint) != receipt.Fingerprint {
		t.Fatal("restart identity receipt differs")
	}
	authenticatedRestartAssertCredential(t, snapshot.Credential, username, password)
}

func authenticatedRestartAssertDurableIdentityUnchanged(t *testing.T, before, after authenticatedRestartDatabaseSnapshot) {
	t.Helper()
	authenticatedRestartAssertCredentialUnchanged(t, before.Credential, after.Credential)
	if len(before.Identity.Users) != 1 || !reflect.DeepEqual(before.Identity, after.Identity) {
		t.Fatal("identity user, grants, or receipt changed across runtime processes")
	}
}

func authenticatedRestartAssertLoginTime(t *testing.T, snapshot authenticatedRestartDatabaseSnapshot, phase authenticatedRestartPhaseAState) {
	t.Helper()
	if len(snapshot.Identity.Users) != 1 {
		t.Fatal("login user missing")
	}
	value := snapshot.Identity.Users[0].LastLogin
	if !value.Valid || value.Time.Before(phase.LoginStarted) || value.Time.After(phase.LoginEnded) || value.Time.Nanosecond()%1000 != 0 {
		t.Fatal("restart login observation missing, outside HTTP login interval, or noncanonical")
	}
}

func authenticatedRestartAssertOnlyLoginChanged(t *testing.T, before, after authenticatedRestartDatabaseSnapshot, phase authenticatedRestartPhaseAState) {
	t.Helper()
	authenticatedRestartAssertLoginTime(t, after, phase)
	if len(before.Identity.Users) != 1 || before.Identity.Users[0].LastLogin.Valid {
		t.Fatal("pre-login identity already records a login")
	}
	// Compare every other persisted field with a detached copy. The actual
	// timestamp was independently bounded by the observed HTTP login above.
	after.Identity.Users = append([]authenticatedRestartIdentityRow(nil), after.Identity.Users...)
	after.Identity.Users[0].LastLogin = before.Identity.Users[0].LastLogin
	authenticatedRestartAssertDurableIdentityUnchanged(t, before, after)
}
