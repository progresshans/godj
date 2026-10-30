package accountstoretest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func creationConfig() accountmodel.CreationConfig[models.User] {
	return accountmodel.CreationConfig[models.User]{CreationPolicy: accountmodel.CreationPolicy[models.User]{Fields: []string{"username", "first_name", "last_name", "email", "active", "staff", "superuser", "groups", "permissions"}, Now: func() time.Time { return joined }}, AddPermission: "account.direct", ChangePermission: "account.team"}
}
func creationInput(t *testing.T, login string) orm.ModelInput[models.User] {
	t.Helper()
	return roleInput(t, map[string]query.Value{"username": query.String(login)}, nil)
}
func accountCreator(t *testing.T, backend db.SnapshotReader, hasher auth.PasswordHasher, config accountmodel.CreationConfig[models.User]) *accountmodel.Creator[models.User] {
	t.Helper()
	credentials, err := project.NewAccountCredentialStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := accountmodel.NewSessionStore(credentials, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	creator, err := project.NewAccountCreator(sessions, auth.PrincipalAuthorizer{}, hasher, config)
	if err != nil {
		t.Fatal(err)
	}
	return creator
}

type creationOwner struct {
	Backend
	scopes atomic.Int64
}

func (owner *creationOwner) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	owner.scopes.Add(1)
	defer owner.scopes.Add(-1)
	return owner.Backend.ReadSnapshot(ctx, read)
}
func (owner *creationOwner) CoordinatedAtomicRelation(ctx context.Context, write func(db.RelationSession) error) error {
	owner.scopes.Add(1)
	defer owner.scopes.Add(-1)
	return owner.Backend.CoordinatedAtomicRelation(ctx, write)
}

type creationHasher struct {
	auth.PasswordHasher
	calls  atomic.Int64
	before func(context.Context) error
}

func (hasher *creationHasher) Hash(ctx context.Context, raw string) (string, error) {
	hasher.calls.Add(1)
	if hasher.before != nil {
		if err := hasher.before(ctx); err != nil {
			return "", err
		}
	}
	return hasher.PasswordHasher.Hash(ctx, raw)
}
func creationExists(t *testing.T, backend db.Queryer, login string) bool {
	t.Helper()
	exists, err := models.UserObjects.Using(backend).Filter(models.UserFields.Username.Exact(login)).Exists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return exists
}

func creationLifecycle(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	owner := &creationOwner{Backend: backend}
	hasher := &creationHasher{PasswordHasher: f.hasher, before: func(context.Context) error {
		if owner.scopes.Load() != 0 {
			return errors.New("hash inside database scope")
		}
		return nil
	}}
	config := creationConfig()
	validations, passwordValidations := 0, 0
	config.Validate = func(_ context.Context, _ db.Queryer, row models.User, selection accountmodel.AccountMemberships) (validation.Errors, error) {
		validations++
		_, stored, err := models.UserObjects.PrimaryKey(row)
		if err != nil || stored != (validations == 3) {
			t.Fatal("creation validation did not preserve pre-insert and final-key stages", err)
		}
		if row.Username != "new-user" || row.Revision != 1 || row.EncodedPassword != "" || row.LastLogin != nil || !row.DateJoined.Equal(joined) {
			t.Fatal("typed creation policy lost normalized/server state")
		}
		if groups := selection.Groups(); len(groups) > 0 {
			groups[0] = 999999
		}
		row.PrincipalID = "modified-copy"
		return validation.Errors{}, nil
	}
	config.ValidatePassword = func(_ context.Context, raw string, row models.User) (validation.Errors, error) {
		passwordValidations++
		if raw != password || row.Username != "new-user" || row.EncodedPassword != "" || row.PrincipalID == "modified-copy" {
			t.Fatal("password policy saw wrong profile or credential")
		}
		return validation.Errors{}, nil
	}
	creator := accountCreator(t, owner, hasher, config)
	input := roleInput(t, map[string]query.Value{"username": query.String("ｎｅｗ-user"), "email": query.String("Person@EXAMPLE.COM"), "first_name": query.String("New")}, map[string][]int64{"groups": {f.group.ID, f.group.ID}, "permissions": {f.grants[0].ID}})
	prepared, err := creator.Prepare(t.Context(), actor, input, password)
	if err != nil || hasher.calls.Load() != 1 || validations != 1 || passwordValidations != 1 || creationExists(t, backend, "new-user") {
		t.Fatal("prepare wrote or did not preflight/hash exactly once", err)
	}
	for _, text := range []string{fmt.Sprintf("%+v", prepared), fmt.Sprintf("%#v", prepared), fmt.Sprintf("%v", creator)} {
		if strings.Contains(text, password) || strings.Contains(text, "new-user") {
			t.Fatal("creation diagnostic exposed input")
		}
	}
	wire, err := json.Marshal(prepared)
	if err != nil || strings.Contains(string(wire), password) || strings.Contains(string(wire), "new-user") {
		t.Fatal("creation JSON exposed private candidate")
	}
	other := accountCreator(t, owner, hasher, config)
	if _, err := other.Commit(t.Context(), actor, prepared); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidInput}) {
		t.Fatal("another creator adopted candidate", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := creator.Commit(ctx, actor, prepared); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-entry cancellation lost", err)
	}
	result, err := creator.Commit(t.Context(), actor, prepared)
	if err != nil || result.Revision != 1 || hasher.calls.Load() != 1 || validations != 3 || passwordValidations != 3 {
		t.Fatal("commit did not recheck or rehashed", result, err)
	}
	row := profileRow(t, reopen(t), result.ID)
	if row.Username != "new-user" || row.Email != "Person@example.com" || row.FirstName != "New" || row.Revision != 1 || row.PrincipalID == f.user.PrincipalID || !strings.HasPrefix(row.PrincipalID, "user-") || row.LastLogin != nil || !row.DateJoined.Equal(joined) {
		t.Fatal("created typed row has wrong normalized/default/server state")
	}
	if ok, err := f.hasher.Verify(t.Context(), password, row.EncodedPassword); err != nil || !ok {
		t.Fatal("created password cannot authenticate", err)
	}
	actual, present, err := f.store.CredentialByID(t.Context(), row.PrincipalID)
	requireGrants(t, actual, present, err, "account.direct", "account.shared", "account.team")
	entries := profileHistory(t, reopen(t), actor, row.ID)
	if len(entries) != 1 || entries[0].ActorID() != actor.ID() || slices.Contains(entries[0].ChangedFields(), "principal_id") || slices.Contains(entries[0].ChangedFields(), "revision") || !slices.Contains(entries[0].ChangedFields(), "encoded_password") {
		t.Fatal("creation audit missing or exposes server identity")
	}
	if again, err := creator.Commit(t.Context(), actor, prepared); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) || again.Revision != 0 {
		t.Fatal("candidate copy reused", err)
	}
	if _, err := creator.Prepare(t.Context(), actor, input, password); err == nil || hasher.calls.Load() != 1 {
		t.Fatal("duplicate account was hashed")
	}
	unusable := accountCreator(t, owner, hasher, creationConfig())
	disabled, err := unusable.PrepareUnusable(t.Context(), actor, creationInput(t, "unusable"))
	if err != nil {
		t.Fatal(err)
	}
	disabledResult, err := unusable.Commit(t.Context(), actor, disabled)
	if err != nil || hasher.calls.Load() != 1 {
		t.Fatal("unusable creation hashed", err)
	}
	disabledRow := profileRow(t, backend, disabledResult.ID)
	disabledCredential, found, err := f.store.CredentialByID(t.Context(), disabledRow.PrincipalID)
	if err != nil || !found || disabledCredential.HasUsablePassword() || !disabledRow.Active {
		t.Fatal("unusable password changed role or remains usable", err)
	}
	if reflect.DeepEqual(row, disabledRow) {
		t.Fatal("two creations reused model")
	}
}
