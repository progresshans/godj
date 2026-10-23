package model_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
)

func credentialBinding(t *testing.T, role ir.AccountRole) accountmodel.Binding[models.User] {
	t.Helper()
	definition, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := accountmodel.Bind(role, models.UserDescriptor{}, definition)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func credentialRow() models.User {
	row := models.User{PrincipalID: "member", Username: "alice", EncodedPassword: "private-salted-hash", Active: true, Revision: 1,
		DateJoined: time.Date(2026, time.October, 24, 0, 0, 0, 0, time.UTC)}
	(models.UserDescriptor{}).SetPrimaryKey(&row, 1)
	return row
}

func TestAccountCredentialTracksRoleAndOwnershipGeneration(t *testing.T) {
	binding := credentialBinding(t, modeldef.AccountRole())
	generation := [sha256.Size]byte{1}
	row := credentialRow()
	permissions := []auth.Permission{"tickets.view_ticket"}
	before, err := binding.PrepareCredential(generation, row, permissions)
	if err != nil || !before.Principal().Has(permissions[0]) || before.Principal().Staff() {
		t.Fatal("selected account role did not create its credential", err)
	}
	permissions[0] = "tickets.change_ticket"
	if before.Principal().Has(permissions[0]) {
		t.Fatal("credential retained caller permission storage")
	}
	fingerprint, err := binding.RoleFingerprint()
	if err != nil || len(fingerprint) != sha256.Size*2 {
		t.Fatal("account role has no stable identity", err)
	}
	reopened := credentialBinding(t, modeldef.AccountRole())
	if next, err := reopened.RoleFingerprint(); err != nil || next != fingerprint {
		t.Fatal("equivalent account binding changed its identity", err)
	}
	current, err := reopened.PrepareCredential(generation, row, permissions)
	if err != nil || !current.MatchesSessionStamp(before.SessionStamp()) {
		t.Fatal("reopened account binding changed a stable credential", err)
	}
	staff := true
	role := modeldef.AccountRole()
	role.Staff = ir.AccountBoolean{Constant: &staff}
	changed := credentialBinding(t, role)
	for _, candidate := range []struct {
		binding    accountmodel.Binding[models.User]
		generation [sha256.Size]byte
	}{{changed, generation}, {binding, [sha256.Size]byte{2}}} {
		after, err := candidate.binding.PrepareCredential(candidate.generation, row, permissions)
		if err != nil || after.MatchesSessionStamp(before.SessionStamp()) || before.MatchesSessionStamp(after.SessionStamp()) {
			t.Fatal("a changed account role or ownership generation inherited the session", err)
		}
		domain, err := candidate.binding.CredentialDomain(candidate.generation)
		if err != nil || domain.MatchesPrincipal(before.Principal()) || !domain.MatchesPrincipal(after.Principal()) {
			t.Fatal("current domain accepted the previous authenticated actor", err)
		}
	}
	constant, err := changed.PrepareCredential(generation, row, nil)
	if err != nil || !constant.Principal().Staff() {
		t.Fatal("constant role was inferred from the old model field", err)
	}
	for _, key := range []int64{0, -1} {
		(models.UserDescriptor{}).SetPrimaryKey(&row, key)
		if _, err := binding.PrepareCredential(generation, row, nil); err != nil {
			t.Fatal("explicit signed AutoField key was confused with absence", key, err)
		}
	}
	encoded, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	outputs := []string{string(encoded)}
	for _, format := range []string{"%v", "%+v", "%#v", "%p", "%w"} {
		outputs = append(outputs, fmt.Sprintf(format, before))
	}
	for _, output := range outputs {
		for _, secret := range []string{row.EncodedPassword, before.SessionStamp(), hex.EncodeToString([]byte(row.EncodedPassword))} {
			if strings.Contains(output, secret) {
				t.Fatal("prepared credential exposed authentication material")
			}
		}
	}
}

func TestAccountCredentialRejectsInvalidStoredStateWithoutPublishing(t *testing.T) {
	binding := credentialBinding(t, modeldef.AccountRole())
	generation := [sha256.Size]byte{1}
	for _, name := range []string{"absent_key", "zero_revision", "negative_revision", "invalid_principal", "oversized_login", "invalid_utf8", "invalid_hash", "malformed_permission", "duplicate_permission", "permission_overflow", "zero_generation", "zero_binding"} {
		t.Run(name, func(t *testing.T) {
			row, selected, epoch := credentialRow(), binding, generation
			permissions := []auth.Permission{"tickets.view_ticket"}
			switch name {
			case "absent_key":
				(models.UserDescriptor{}).ClearPrimaryKey(&row)
			case "zero_revision":
				row.Revision = 0
			case "negative_revision":
				row.Revision = -1
			case "invalid_principal":
				row.PrincipalID = "bad\x00principal"
			case "oversized_login":
				row.Username = strings.Repeat("a", 257)
			case "invalid_utf8":
				row.Username = string([]byte{255})
			case "invalid_hash":
				row.EncodedPassword += "\n"
			case "malformed_permission":
				permissions[0] = "INVALID"
			case "duplicate_permission":
				permissions = append(permissions, permissions[0])
			case "permission_overflow":
				permissions = make([]auth.Permission, auth.MaximumPermissions+1)
			case "zero_generation":
				epoch = [sha256.Size]byte{}
			case "zero_binding":
				selected = accountmodel.Binding[models.User]{}
			}
			credential, err := selected.PrepareCredential(epoch, row, permissions)
			if err == nil || credential.Principal().ID() != "" || credential.SessionStamp() != "" || strings.Contains(err.Error(), "private-salted-hash") {
				t.Fatal("invalid account snapshot published a credential or secret", err)
			}
		})
	}
}
