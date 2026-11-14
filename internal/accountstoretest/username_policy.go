package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/iexacttest"
	"github.com/progresshans/godj/validation"
)

type usernameFixture struct {
	*userReferenceFixture
	existing models.User
}

func provisionUsernamePolicy(t *testing.T, backend Backend, stored string) *usernameFixture {
	t.Helper()
	f := &usernameFixture{userReferenceFixture: provisionUserReference(t, backend)}
	var err error
	f.existing, err = models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("username-existing", stored, f.base.user.EncodedPassword, joined))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := f.runtime.Authenticator().Resolve(t.Context(), f.existing.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		passwordSession(t, f.runtime.Sessions(), credential, fmt.Sprintf("existing-%d", i))
		passwordSession(t, f.runtime.Sessions(), f.actor, fmt.Sprintf("actor-%d", i))
		if _, err := f.runtime.Sessions().Create(t.Context(), map[string]string{"anonymous": fmt.Sprintf("preserved-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

type usernameCreationOwner struct {
	runtime  *accountmodel.Runtime[models.User, models.Group, models.Permission]
	boundary *profileFaultBackend
	hasher   *passwordChangeHasher
	encoded  atomic.Value
}

func newUsernameCreationOwner(t *testing.T, backend Backend, f *usernameFixture, checked bool) *usernameCreationOwner {
	t.Helper()
	owner := &usernameCreationOwner{boundary: &profileFaultBackend{Backend: backend}}
	tracked := &creationOwner{Backend: owner.boundary}
	owner.hasher = &passwordChangeHasher{PasswordHasher: f.base.hasher, scopes: &tracked.scopes, encode: func(ctx context.Context, raw string) (string, error) {
		encoded, err := f.base.hasher.Hash(ctx, raw)
		if err == nil {
			owner.encoded.Store(encoded)
		}
		return encoded, err
	}}
	config := userReferenceConfig(t)
	config.Accounts.CaseInsensitiveCreationLogin = checked
	var err error
	owner.runtime, err = hostproject.NewAccountRuntime(t.Context(), tracked, owner.hasher, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	// Exclude the real authenticator's startup dummy hash from creation work.
	owner.hasher.hashes.Store(0)
	return owner
}

func usernameUnique(t *testing.T, err error) []string {
	t.Helper()
	fields, rejected := validation.Rejected(err)
	items := fields.ByField("username").All()
	if !rejected || len(items) != 1 || items[0].Code() != validation.CodeUnique {
		t.Fatal("selected username duplicate lost its unique diagnostic", err)
	}
	return []string{string(items[0].Code())}
}

func usernameCommitted(t *testing.T, backend Backend, before userReferenceState, f *usernameFixture, owner *usernameCreationOwner, login string) accountmodel.Creation {
	t.Helper()
	after := userReferenceSnapshot(t, backend)
	var matches []models.User
	for _, row := range after.account.users {
		if row.Username == login {
			matches = append(matches, row)
		}
	}
	if len(matches) != 1 || matches[0].ID <= 0 || slices.ContainsFunc(before.account.users, func(row models.User) bool { return row.ID == matches[0].ID }) {
		t.Fatal("username policy did not allocate exactly one new account")
	}
	expected := userReferenceExpectedRow(matches[0].ID, login, "", owner.encoded.Load().(string))
	userReferenceReplace(&before.account, expected)
	assertUserReferenceState(t, before, after, f.actor.Principal(), catalogExpectedEvent{"user", expected.ID, admin.ActionAdd, userReferenceCreationFields})
	return accountmodel.Creation{ID: expected.ID, Revision: 1, ChangedFields: slices.Clone(userReferenceCreationFields)}
}

func runCreationUsernamePolicy(t *testing.T, vendor string, open Open) {
	t.Run("creation_username_policy", func(t *testing.T) {
		inputs, reference := iexacttest.CreationReference(t, vendor)
		for index, input := range inputs {
			t.Run(fmt.Sprintf("reference_%02d", index), func(t *testing.T) {
				backend, reopen := open(t)
				want := reference[index]
				// Seed the independently observed stored spelling. This isolates
				// the candidate policy from the earlier manager's normalization.
				f := provisionUsernamePolicy(t, backend, want.Stored)
				owner := newUsernameCreationOwner(t, backend, f, true)
				creator, actor := owner.runtime.Accounts().Creator(), f.actor.Principal()
				before := userReferenceSnapshot(t, reopen(t))
				candidate, err := creator.Prepare(t.Context(), actor, creationInput(t, input.Candidate), userReferencePassword)
				assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor)
				observed := iexacttest.CreationResult{Stored: f.existing.Username, Accepted: err == nil, Codes: []string{}}
				if want.Accepted {
					if err != nil || want.Username == nil || owner.hasher.hashes.Load() != 1 || owner.boundary.calls != 0 {
						t.Fatal("selected username preparation differs from Django", err)
					}
					result, err := creator.Commit(t.Context(), actor, candidate)
					if err != nil || owner.hasher.hashes.Load() != 1 || owner.boundary.calls != 1 {
						t.Fatal("selected username commit failed or repeated work", err)
					}
					if expected := usernameCommitted(t, reopen(t), before, f, owner, *want.Username); !reflect.DeepEqual(result, expected) {
						t.Fatal("username creation metadata differs")
					}
					current := userReferenceSnapshot(t, reopen(t))
					row := profileRow(t, backend, result.ID)
					observed.Username = &row.Username
					credential, err := owner.runtime.Authenticator().Authenticate(t.Context(), row.Username, userReferencePassword)
					if err != nil || credential.Principal().ID() != row.PrincipalID {
						t.Fatal("selected username spelling or password was not stored", err)
					}
					assertUserReferenceState(t, current, userReferenceSnapshot(t, reopen(t)), actor)
				} else {
					observed.Codes = usernameUnique(t, err)
					if !reflect.DeepEqual(candidate, accountmodel.PreparedCreation[models.User]{}) || owner.hasher.hashes.Load() != 0 || owner.boundary.calls != 0 {
						t.Fatal("duplicate username was hashed, written or published")
					}
				}
				if !reflect.DeepEqual(observed, want) {
					t.Fatal("selected username observation differs from independent Django", index)
				}
			})
		}
		for _, test := range []struct {
			name string
			run  func(*testing.T, Backend, func(*testing.T) Backend)
		}{
			{"optional_policy_and_exact_login", usernameOptionalPolicy},
			{"duplicate_during_hash", usernameDuplicateDuringHash},
			{"denied_before_duplicate_lookup", usernameDeniedBeforeLookup},
			{"duplicate_cleanup_failure", usernameCleanupFailure},
			{"lookup_failure_read", func(t *testing.T, b Backend, r func(*testing.T) Backend) { usernameLookupFailure(t, b, r, "read") }},
			{"lookup_failure_write", func(t *testing.T, b Backend, r func(*testing.T) Backend) { usernameLookupFailure(t, b, r, "write") }},
			{"audit_failure", func(t *testing.T, b Backend, r func(*testing.T) Backend) {
				usernameWriteFailure(t, b, r, "audit_failure")
			}},
			{"unknown_commit", func(t *testing.T, b Backend, r func(*testing.T) Backend) {
				usernameWriteFailure(t, b, r, "unknown_commit")
			}},
		} {
			t.Run(test.name, func(t *testing.T) { backend, reopen := open(t); test.run(t, backend, reopen) })
		}
	})
}

func usernameOptionalPolicy(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionUsernamePolicy(t, backend, "member")
	checked := newUsernameCreationOwner(t, backend, f, true)
	ordinary := newUsernameCreationOwner(t, backend, f, false)
	input := creationInput(t, "MEMBER")
	before := userReferenceSnapshot(t, reopen(t))
	candidate, err := checked.runtime.Accounts().Creator().Prepare(t.Context(), f.actor.Principal(), input, userReferencePassword)
	usernameUnique(t, err)
	if !reflect.DeepEqual(candidate, accountmodel.PreparedCreation[models.User]{}) || checked.hasher.hashes.Load() != 0 || checked.boundary.calls != 0 {
		t.Fatal("optional policy published duplicate input")
	}
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	candidate, err = ordinary.runtime.Accounts().Creator().Prepare(t.Context(), f.actor.Principal(), input, userReferencePassword)
	if err != nil {
		t.Fatal("checked policy changed ordinary creator or shared input", err)
	}
	result, err := ordinary.runtime.Accounts().Creator().Commit(t.Context(), f.actor.Principal(), candidate)
	if err != nil || ordinary.hasher.hashes.Load() != 1 || ordinary.boundary.calls != 1 {
		t.Fatal("ordinary username creation failed", err)
	}
	if expected := usernameCommitted(t, reopen(t), before, f, ordinary, "MEMBER"); !reflect.DeepEqual(result, expected) {
		t.Fatal("ordinary creation metadata differs")
	}
	current := userReferenceSnapshot(t, reopen(t))
	for _, example := range []struct{ login, password, principal string }{{"member", password, f.existing.PrincipalID}, {"MEMBER", userReferencePassword, "reference-created"}} {
		credential, err := ordinary.runtime.Authenticator().Authenticate(t.Context(), example.login, example.password)
		if err != nil || credential.Principal().ID() != example.principal {
			t.Fatal("creation duplicate policy altered exact authentication", err)
		}
	}
	if _, err := ordinary.runtime.Authenticator().Authenticate(t.Context(), "Member", userReferencePassword); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatal("optional duplicate policy became case-insensitive login", err)
	}
	assertUserReferenceState(t, current, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
}

func usernameDuplicateDuringHash(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionUsernamePolicy(t, backend, "member")
	owner := newUsernameCreationOwner(t, backend, f, true)
	writer := reopen(t)
	before := userReferenceSnapshot(t, reopen(t))
	var competitor models.User
	writes := 0
	owner.hasher.beforeHash = func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return writer.CoordinatedAtomic(ctx, func(scope db.Session) error {
			var err error
			competitor, err = models.UserObjects.Create(ctx, scope, models.NewUserCreate("username-competitor", "RACE", f.base.user.EncodedPassword, joined))
			if err == nil {
				writes++
			}
			return err
		})
	}
	creator := owner.runtime.Accounts().Creator()
	candidate, err := creator.Prepare(t.Context(), f.actor.Principal(), creationInput(t, "race"), userReferencePassword)
	if err != nil || writes != 1 || owner.hasher.hashes.Load() != 1 || owner.boundary.calls != 0 {
		t.Fatal("independent duplicate did not arrive during scope-free hashing", err)
	}
	expected := userReferenceExpectedRow(competitor.ID, "RACE", "", f.base.user.EncodedPassword)
	expected.PrincipalID = "username-competitor"
	userReferenceReplace(&before.account, expected)
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	result, err := creator.Commit(t.Context(), f.actor.Principal(), candidate)
	usernameUnique(t, err)
	if !reflect.DeepEqual(result, accountmodel.Creation{}) || owner.hasher.hashes.Load() != 1 || owner.boundary.calls != 1 {
		t.Fatal("late duplicate published or repeated work")
	}
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	result, err = creator.Commit(t.Context(), f.actor.Principal(), candidate)
	requireProfileCode(t, err, accountmodel.CodeConflict)
	if !reflect.DeepEqual(result, accountmodel.Creation{}) || owner.boundary.calls != 1 || owner.hasher.hashes.Load() != 1 {
		t.Fatal("late duplicate candidate was reusable")
	}
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
}
