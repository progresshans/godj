//go:build darwin || linux

package projectmigrateproduct_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	articleproject "github.com/progresshans/godj/examples/article/project"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Read the current durable identity independently of runtime authentication.
// The selected User is the credential source; the domain receipt owns its generation.
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
	ID, Sequence                                             int64
	Previous                                                 sql.NullInt64
	OperationID, RoleFingerprint, Generation, EvidenceSHA256 string
	At                                                       time.Time
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
	read(`SELECT "id","operation_id","sequence","previous_id","role_fingerprint","generation","evidence_sha256","transitioned_at" FROM `+table("godj_account_domain_transition")+` ORDER BY "id"`, func(rows authenticatedRestartRows) error {
		var row authenticatedRestartTransitionRow
		err := rows.Scan(&row.ID, &row.OperationID, &row.Sequence, &row.Previous, &row.RoleFingerprint, &row.Generation, &row.EvidenceSHA256, &row.At)
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
	if snapshot.Identity.Counts != ([4]int64{}) || len(snapshot.Identity.Grants) != 0 {
		t.Fatal("first selected superuser unexpectedly copied catalog rows or grants")
	}
	rows := snapshot.Identity.Users
	if len(rows) != 1 {
		t.Fatalf("restart selected account count=%d, want 1", len(rows))
	}
	row := rows[0]
	if row.ID != 1 || !strings.HasPrefix(row.PrincipalID, "user-") || !authenticatedRestartDigest(strings.TrimPrefix(row.PrincipalID, "user-")) || row.Username != username || !strings.HasPrefix(row.EncodedPassword, "pbkdf2_sha256$") || strings.Contains(row.EncodedPassword, password) || !row.Active || !row.Staff || !row.Superuser || row.Revision != 1 || row.DateJoined.IsZero() || row.FirstName != "" || row.LastName != "" || row.Email != "" {
		t.Fatal("selected account credential, profile, roles or revision differs")
	}
	transitions := snapshot.Identity.Transitions
	if len(transitions) != 1 {
		t.Fatal("selected account receipt count differs")
	}
	receipt := transitions[0]
	policy, err := os.ReadFile(filepath.Join(repositoryRoot(t), "examples/article/internal/accounts/policy.go"))
	if err != nil {
		t.Fatal("read selected first-account policy evidence")
	}
	evidence := sha256.Sum256(policy)
	if receipt.ID != 1 || receipt.Sequence != 1 || receipt.Previous.Valid || !authenticatedRestartDigest(receipt.OperationID) || !authenticatedRestartDigest(receipt.Generation) || receipt.EvidenceSHA256 != hex.EncodeToString(evidence[:]) || receipt.At.IsZero() || receipt.At.Before(row.DateJoined) || receipt.At.Nanosecond()%1000 != 0 {
		t.Fatal("selected account ownership receipt differs")
	}
	namespace := authenticatedRestartDomain(t, snapshot)
	var accountAudits []authenticatedRestartAuditRow
	for _, event := range snapshot.Audits {
		if event.Model == "godj_identity.user" {
			accountAudits = append(accountAudits, event)
		}
	}
	if len(accountAudits) != 1 {
		t.Fatal("first selected account audit count differs")
	}
	event := accountAudits[0]
	if event.Sequence != 1 || event.Domain != namespace || event.ActorID != row.PrincipalID || event.ObjectID != row.ID || event.Action != "add" || event.ChangedFields != `["username","email","active","staff","superuser","encoded_password"]` || !event.RecordedAt.Equal(receipt.At) {
		t.Fatal("first selected account audit differs")
	}
}

func authenticatedRestartDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value && value != strings.Repeat("0", 64)
}
func authenticatedRestartDomain(t *testing.T, snapshot authenticatedRestartDatabaseSnapshot) string {
	t.Helper()
	if len(snapshot.Identity.Transitions) != 1 {
		t.Fatal("account ownership is absent")
	}
	receipt := snapshot.Identity.Transitions[0]
	binding, err := articleproject.BindAccount()
	if err != nil {
		t.Fatal("bind selected account")
	}
	fingerprint, err := binding.RoleFingerprint()
	if err != nil || fingerprint != receipt.RoleFingerprint {
		t.Fatal("stored account role differs")
	}
	encoded, err := hex.DecodeString(receipt.Generation)
	if err != nil || len(encoded) != 32 {
		t.Fatal("decode credential generation")
	}
	var generation [32]byte
	copy(generation[:], encoded)
	domain, err := binding.CredentialDomain(generation)
	if err != nil {
		t.Fatal("bind credential generation")
	}
	return domain.Identifier()
}

func authenticatedRestartAssertDurableIdentityUnchanged(t *testing.T, before, after authenticatedRestartDatabaseSnapshot) {
	t.Helper()
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
