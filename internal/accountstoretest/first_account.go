package accountstoretest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/account/domain"
	domainmodels "github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func firstAccountConfig() accountmodel.FirstAccountConfig[models.User] {
	evidence := sha256.Sum256([]byte("native selected first-account bootstrap declaration"))
	return accountmodel.FirstAccountConfig[models.User]{CreationPolicy: accountmodel.CreationPolicy[models.User]{Fields: []string{"username", "first_name", "email", "groups", "permissions"}, Now: func() time.Time { return joined }}, EvidenceSHA256: hex.EncodeToString(evidence[:])}
}
func firstAccountOwner(t *testing.T, backend db.SnapshotReader, hasher auth.PasswordHasher, config accountmodel.FirstAccountConfig[models.User]) *accountmodel.FirstAccount[models.User] {
	t.Helper()
	owner, err := project.NewFirstAccount(backend, hasher, config)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}
func firstAccountHasher(t *testing.T) *auth.PBKDF2 {
	t.Helper()
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	return hasher
}
func firstAccountCounts(t *testing.T, backend db.Queryer) []int64 {
	t.Helper()
	counts := creationCounts(t, backend)
	count, err := domainmodels.TransitionObjects.Using(backend).Count(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return append(counts, count)
}
func firstAccountChoices(t *testing.T, backend Backend) (models.Group, models.Permission) {
	t.Helper()
	group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("bootstrap operators"))
	if err != nil {
		t.Fatal(err)
	}
	permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("bootstrap.read", "Read bootstrap"))
	if err != nil {
		t.Fatal(err)
	}
	return group, permission
}

func firstAccountLifecycle(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	nativeHasher := firstAccountHasher(t)
	tracked := &creationOwner{Backend: backend}
	hasher := &creationHasher{PasswordHasher: nativeHasher, before: func(context.Context) error {
		if tracked.scopes.Load() != 0 {
			return errors.New("first hash inside DB scope")
		}
		return nil
	}}
	group, permission := firstAccountChoices(t, backend)
	config := firstAccountConfig()
	validations, strengths := 0, 0
	config.Validate = func(_ context.Context, _ db.Queryer, row models.User, selection accountmodel.RoleMemberships) (validation.Errors, error) {
		validations++
		if row.Username != "first-admin" || !row.Active || !row.Staff || !row.Superuser || row.Revision != 1 || row.EncodedPassword != "" {
			t.Fatal("first account lost fixed roles or typed policy")
		}
		if keys := selection.Groups(); len(keys) > 0 {
			keys[0] = 999999
		}
		return validation.Errors{}, nil
	}
	config.ValidatePassword = func(_ context.Context, raw string, row models.User) (validation.Errors, error) {
		strengths++
		if raw != password || row.EncodedPassword != "" || !row.Superuser {
			t.Fatal("first password policy did not see protected profile")
		}
		return validation.Errors{}, nil
	}
	owner := firstAccountOwner(t, tracked, hasher, config)
	operation := sha256.Sum256([]byte("operator-owned first-account operation"))
	operationID := hex.EncodeToString(operation[:])
	input := roleInput(t, map[string]query.Value{"username": query.String("ｆｉｒｓｔ-admin"), "first_name": query.String("First"), "email": query.String("Root@EXAMPLE.TEST")}, map[string][]int64{"groups": {group.ID}, "permissions": {permission.ID}})
	pending, err := owner.PrepareOperation(t.Context(), operationID, input, password)
	if err != nil || pending.OperationID() != operationID || hasher.calls.Load() != 1 || validations != 1 || strengths != 1 {
		t.Fatal("first preparation", err)
	}
	if counts := firstAccountCounts(t, backend); !reflect.DeepEqual(counts, []int64{0, 0, 0, 0, 0}) {
		t.Fatal("prepare persisted first state", counts)
	}
	for _, text := range []string{fmt.Sprintf("%+v", pending), fmt.Sprintf("%#v", pending)} {
		if strings.Contains(text, password) || strings.Contains(text, "first-admin") {
			t.Fatal("first preparation diagnostic leak")
		}
	}
	wire, err := json.Marshal(pending)
	if err != nil || strings.Contains(string(wire), password) {
		t.Fatal("first JSON leak")
	}
	other := firstAccountOwner(t, tracked, hasher, config)
	if _, err := other.Commit(t.Context(), pending); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidInput}) {
		t.Fatal("foreign first owner adopted preparation", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := owner.Commit(ctx, pending); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-entry cancel lost", err)
	}
	result, err := owner.Commit(t.Context(), pending)
	if err != nil || result.Account.Revision != 1 || result.Ownership.Sequence() != 1 || result.Ownership.OperationID() != operationID || result.Ownership.EvidenceSHA256() != config.EvidenceSHA256 || hasher.calls.Load() != 1 || validations != 3 || strengths != 3 {
		t.Fatal("first commit did not coordinate ownership/policy", err)
	}
	row := profileRow(t, reopen(t), result.Account.ID)
	if !row.Active || !row.Staff || !row.Superuser || row.Username != "first-admin" || row.Email != "Root@example.test" || row.LastLogin != nil || !row.DateJoined.Equal(joined) {
		t.Fatal("first stored account differs")
	}
	store, err := project.NewAccountCredentialStore(reopen(t))
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := accountmodel.NewAuthenticator(t.Context(), store, nativeHasher)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := authenticator.Authenticate(t.Context(), row.Username, password)
	if err != nil || credential.Principal().ID() != row.PrincipalID || !credential.Principal().Superuser() {
		t.Fatal("first account does not authenticate after reopening", err)
	}
	namespace, _ := credential.Principal().CredentialDomain()
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := binding.CredentialDomain(result.Ownership.Generation())
	if err != nil || namespace.Identifier() != expected.Identifier() {
		t.Fatal("first login crossed ownership domain")
	}
	entries := profileHistory(t, reopen(t), credential.Principal(), row.ID)
	if len(entries) != 1 || entries[0].ActorID() != row.PrincipalID || !entries[0].At().Equal(result.Ownership.At()) {
		t.Fatal("first actor/audit is not in initial domain")
	}
	for _, field := range []string{"active", "staff", "superuser"} {
		if !slices.Contains(entries[0].ChangedFields(), field) {
			t.Fatal("forced role omitted from first audit", field)
		}
	}
	receipt, err := domain.Lookup(t.Context(), reopen(t), operationID)
	if err != nil || receipt.Generation() != result.Ownership.Generation() {
		t.Fatal("operator receipt lost", err)
	}
	now := joined
	sessionStore, manager, login := accountSessions(t, reopen(t), &now)
	previous, err := manager.Create(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := login.Login(t.Context(), selectedSessionLogin(credential, previous, now))
	if err != nil {
		t.Fatal("first account DB login", err)
	}
	if !signed.Record.ID().Valid() || !signed.Credential.MatchesSessionStamp(credential.SessionStamp()) {
		t.Fatal("first login was not confirmed")
	}
	accountSessionRecord(t, sessionStore, signed.Record, true)
	accountSessionRecord(t, sessionStore, previous, false)
	if _, err := owner.Prepare(t.Context(), creationInput(t, "second-first"), password); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFresh}) || hasher.calls.Load() != 1 {
		t.Fatal("initialized domain implicitly adopted or hashed", err)
	}
	if _, err := owner.Commit(t.Context(), pending); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
		t.Fatal("first candidate reused", err)
	}
}

func firstAccountNonfresh(t *testing.T, backend Backend, reopen func(*testing.T) Backend, storedKey StoredKeyFixture) {
	hasher := &creationHasher{PasswordHasher: firstAccountHasher(t)}
	owner := firstAccountOwner(t, backend, hasher, firstAccountConfig())
	check := func(name string) {
		t.Run(name, func(t *testing.T) {
			before := firstAccountCounts(t, backend)
			if _, err := owner.Prepare(t.Context(), creationInput(t, "fresh-looking"), password); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFresh}) {
				t.Fatal("nonfresh state accepted", err)
			}
			if after := firstAccountCounts(t, reopen(t)); !reflect.DeepEqual(before, after) || hasher.calls.Load() != 0 {
				t.Fatal("nonfresh attempt changed state or hashed")
			}
		})
	}
	row, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("unadopted", "unadopted", "!disabled", joined))
	if err != nil {
		t.Fatal(err)
	}
	check("rows_without_history")
	if _, err := models.UserObjects.Delete(t.Context(), backend, &row); err != nil {
		t.Fatal(err)
	}
	for _, key := range []int64{0, -7} {
		row, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate(fmt.Sprintf("unadopted-%d", key), fmt.Sprintf("unadopted-%d", key), "!disabled", joined))
		if err != nil {
			t.Fatal(err)
		}
		storedKey(t, backend, "user", row.ID, key)
		check(fmt.Sprintf("rows_with_key_%d", key))
		stored := models.NewUserWithID(key)
		if count, err := models.UserObjects.Delete(t.Context(), backend, &stored); err != nil || count != 1 {
			t.Fatal("signed fixture cleanup", err)
		}
	}
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	change, err := domain.Prepare(t.Context(), fingerprint, firstAccountConfig().EvidenceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	var current domain.Receipt
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		var err error
		current, err = domain.InitializeIn(t.Context(), scope, change, joined)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	check("same_role_history_without_rows")
	foreign, err := domain.Prepare(t.Context(), strings.Repeat("e", 64), firstAccountConfig().EvidenceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		_, err := domain.TransitionIn(t.Context(), scope, current, foreign, joined.Add(time.Second))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	check("foreign_role_history_without_rows")
	if _, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("unadopted", "unadopted", "!disabled", joined)); err != nil {
		t.Fatal(err)
	}
	check("foreign_history_with_rows")
}
