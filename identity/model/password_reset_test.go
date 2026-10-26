package model_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

type resetNilContext struct{ context.Context }

func TestAccountPasswordResetterRequiresExactSourceAndOwnersBeforeIO(t *testing.T) {
	backend := &untouchedProfileBackend{}
	store, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	inner, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	hasher := &passwordSetupHasher{PasswordHasher: inner}
	verifier, err := store.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	other, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	foreignVerifier, err := other.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { callbacks++; return time.Now() }})
	if err != nil {
		t.Fatal(err)
	}
	foreignManager, err := sessions.NewManager(other, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	foreignHost, err := accountmodel.Bind(modeldef.AccountRole(), models.UserDescriptor{}, models.GoDjRelationSchema(), hostmodels.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	role := modeldef.AccountRole()
	role.LoginFormNormalization = ir.AccountExact
	foreignRole, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{64}, 32))
	if err != nil {
		t.Fatal(err)
	}
	config := accountmodel.PasswordResetConfig[models.User]{Keys: keys, Clock: func() time.Time { callbacks++; return time.Now() }, ValidatePassword: func(context.Context, string, models.User) (validation.Errors, error) {
		callbacks++
		return validation.Errors{}, nil
	}}
	before := hasher.calls
	for _, mode := range []string{"nil_store", "zero_store", "nil_binding", "foreign_host", "foreign_role", "nil_manager", "foreign_manager", "nil_verifier", "zero_verifier", "foreign_verifier", "zero_keys", "negative_timeout", "fractional_timeout", "negative_limit", "oversized_limit"} {
		t.Run(mode, func(t *testing.T) {
			s, b, m, v, c := store, binding, manager, verifier, config
			switch mode {
			case "nil_store":
				s = nil
			case "zero_store":
				s = &accountmodel.SessionStore[models.User]{}
			case "nil_binding":
				b = accountmodel.Binding[models.User]{}
			case "foreign_host":
				b = foreignHost
			case "foreign_role":
				b = foreignRole
			case "nil_manager":
				m = nil
			case "foreign_manager":
				m = foreignManager
			case "nil_verifier":
				v = nil
			case "zero_verifier":
				v = &accountmodel.Authenticator[models.User]{}
			case "foreign_verifier":
				v = foreignVerifier
			case "zero_keys":
				c.Keys = auth.PasswordResetKeyRing{}
			case "negative_timeout":
				c.Timeout = -time.Second
			case "fractional_timeout":
				c.Timeout = time.Second + time.Nanosecond
			case "negative_limit":
				c.MaxPasswordBytes = -1
			case "oversized_limit":
				c.MaxPasswordBytes = 4097
			}
			if owner, err := accountmodel.NewPasswordResetter(s, b, m, v, c); owner != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
				t.Fatal("invalid reset owner published", err)
			}
		})
	}
	owner, err := project.NewAccountPasswordResetter(store, manager, verifier, config)
	if err != nil {
		t.Fatal(err)
	}
	var persistence auth.PasswordResetPersistence = owner
	var source auth.PasswordResetMailSource = owner
	second, err := project.NewAccountPasswordResetter(store, manager, verifier, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Keys = auth.PasswordResetKeyRing{}
	config.MaxPasswordBytes = 1
	if persistence.Sessions() != manager || !source.PasswordResetEmailAvailable() || owner.MaxPasswordBytes() != 1024 || !source.PasswordResetAuthority().Valid() || source.PasswordResetAuthority().Matches(second.PasswordResetAuthority()) {
		t.Fatal("reset metadata did not retain its exact owner")
	}
	for _, value := range []any{owner, config} {
		data, err := json.Marshal(value)
		if err != nil || strings.Contains(string(data), strings.Repeat("@", 32)) || !strings.Contains(fmt.Sprintf("%#v", value), "redacted") {
			t.Fatal("reset diagnostics leaked", err)
		}
	}
	for _, invalid := range []string{"", "bad\x00principal"} {
		if token, err := owner.IssueToken(t.Context(), invalid); err != auth.ErrInvalidResetToken || token.Encoded() != "" {
			t.Fatal("invalid delivery identifier reached DB", err)
		}
	}
	for _, encoded := range []string{"", strings.Repeat("x", 4097), "r1.invalid.signature"} {
		if record, err := owner.StartPasswordReset(t.Context(), sessions.ID{}, "member", encoded); err != auth.ErrInvalidResetProof || record.ID().Valid() {
			t.Fatal("malformed entry reached DB", err)
		}
	}
	if err := owner.CheckPasswordReset(t.Context(), sessions.ID{}, "member", nil); err != auth.ErrInvalidResetProof {
		t.Fatal("invalid proof reached DB", err)
	}
	if result, err := owner.ResetPassword(t.Context(), sessions.ID{}, "member", "private-password"); err != auth.ErrInvalidResetProof || !reflect.DeepEqual(result, auth.PasswordResetResult{}) {
		t.Fatal("invalid reset published output", err)
	}
	for _, ctx := range []context.Context{nil, (*resetNilContext)(nil)} {
		if _, err := owner.IssueToken(ctx, "member"); err == nil {
			t.Fatal("nil context admitted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := owner.StartPasswordReset(ctx, sessions.ID{}, "member", "token"); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancelled reset admitted", err)
	}
	for _, empty := range []*accountmodel.PasswordResetter[models.User]{nil, {}} {
		if empty.Sessions() != nil || empty.PasswordResetAuthority().Valid() || empty.PasswordResetEmailAvailable() || empty.MaxPasswordBytes() != 0 {
			t.Fatal("empty reset owner exposed capability")
		}
		if result, err := empty.ResetPassword(t.Context(), sessions.ID{}, "member", "private"); err == nil || !reflect.DeepEqual(result, auth.PasswordResetResult{}) {
			t.Fatal("empty reset owner reached persistence")
		}
	}
	if hasher.calls != before || callbacks != 0 || backend.calls != 0 || backend.writes != 0 {
		t.Fatal("reset startup or malformed invocation performed work")
	}
}

func TestAccountPasswordResetEmailRoleIsExplicit(t *testing.T) {
	role := modeldef.AccountRole()
	role.Email = ""
	role.WriteEmailNormalization = ""
	binding, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	backend := &untouchedProfileBackend{}
	credentials, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := accountmodel.NewSessionStore(credentials, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	inner, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := store.Authenticator(t.Context(), inner)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{65}, 32))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := accountmodel.NewPasswordResetter(store, binding, manager, verifier, accountmodel.PasswordResetConfig[models.User]{Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	if owner.PasswordResetEmailAvailable() {
		t.Fatal("undeclared email column was inferred")
	}
	if tokens, err := owner.PasswordResetCandidates(t.Context(), "member@example.test"); len(tokens) != 0 || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
		t.Fatal("mail admitted without declared Email", err)
	}
	if backend.calls != 0 || backend.writes != 0 {
		t.Fatal("unsupported email performed I/O")
	}
}
