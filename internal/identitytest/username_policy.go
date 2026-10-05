package identitytest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/iexacttest"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
)

func assertUsernameUnique(t *testing.T, err error) {
	t.Helper()
	fields, rejected := validation.Rejected(err)
	items := fields.ByField("username").All()
	if !rejected || len(items) != 1 || items[0].Code() != validation.CodeUnique {
		t.Fatal("username duplicate not classified", err)
	}
}

// RunCreationUsernamePolicy covers only the optional UserCreationForm
// duplicate policy, not the form's remaining username/password validators.
func RunCreationUsernamePolicy(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend), dialect string) {
	t.Helper()
	inputs, reference := iexacttest.CreationReference(t, dialect)
	for index, input := range inputs {
		t.Run(fmt.Sprintf("reference_%02d", index), func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			want := reference[index]
			// The independent observer records the existing manager-normalized
			// username. This seed isolates the new candidate policy.
			var err error
			f.user, err = models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithUsername(want.Stored))
			if err != nil {
				t.Fatal(err)
			}
			beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
			boundary := &userManagementBoundary{ManagementBackend: f.runtime}
			result, err := f.manager(t, boundary).CreateUser(t.Context(), f.actor, identity.NewUserCreate("candidate", input.Candidate).WithCaseInsensitiveUsernameCheck(), managementNewPassword)
			if want.Accepted {
				if err != nil || result.ID == 0 || want.Username == nil || result.Username != *want.Username || result.Revision != 1 || f.hasher.calls.Load() != 1 || boundary.calls != 1 {
					t.Fatal("creation differs from Django", result, err)
				}
				history, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", result.ID, 10)
				if err != nil || len(history) != 1 || history[0].Action != admin.ActionAdd {
					t.Fatal("creation audit missing", err)
				}
				credential, err := f.runtime.Authenticator().Authenticate(t.Context(), result.Username, managementNewPassword)
				if err != nil || credential.Principal().ID() != result.PrincipalID {
					t.Fatal("normalized spelling or password not stored", err)
				}
			} else {
				assertUsernameUnique(t, err)
				if result.ID != 0 || f.hasher.calls.Load() != 0 || boundary.calls != 0 || !reflect.DeepEqual(want.Codes, []string{"unique"}) {
					t.Fatal("duplicate was hashed, written or published")
				}
			}
			afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
			if !reflect.DeepEqual(beforeSessions, afterSessions) || !want.Accepted && !reflect.DeepEqual(beforeAudit, afterAudit) || !reflect.DeepEqual(f.stored(t), f.user) {
				t.Fatal("creation changed existing user/session/audit bytes")
			}
			count, err := models.UserObjects.Using(backend).Count(t.Context())
			wantCount := int64(2)
			if want.Accepted {
				wantCount++
			}
			if err != nil || count != wantCount {
				t.Fatal("candidate outcome not atomic", count, err)
			}
		})
	}
	t.Run("policy_is_optional_and_does_not_change_login", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		input := identity.NewUserCreate("case-distinct", "MEMBER")
		checked := input.WithCaseInsensitiveUsernameCheck()
		if result, err := f.manager(t, f.runtime).CreateUser(t.Context(), f.actor, checked, managementNewPassword); result.ID != 0 {
			t.Fatal("duplicate published")
		} else {
			assertUsernameUnique(t, err)
		}
		result, err := f.manager(t, f.runtime).CreateUser(t.Context(), f.actor, input, managementNewPassword)
		if err != nil || result.Username != "MEMBER" || f.hasher.calls.Load() != 1 {
			t.Fatal("option mutated source or default policy", err)
		}
		for _, c := range []struct{ username, password, id string }{{"member", managementOldPassword, f.user.PrincipalID}, {"MEMBER", managementNewPassword, "case-distinct"}} {
			credential, err := f.runtime.Authenticator().Authenticate(t.Context(), c.username, c.password)
			if err != nil || credential.Principal().ID() != c.id {
				t.Fatal("login case semantics changed", err)
			}
		}
		if _, err := f.runtime.Authenticator().Authenticate(t.Context(), "Member", managementNewPassword); err == nil {
			t.Fatal("lookup policy changed login")
		}
	})
	t.Run("duplicate_arriving_during_hash_is_rejected_inside_fence", func(t *testing.T) {
		backend, second := open(t)
		f := newManagementFixture(t, backend, 3)
		beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
		f.hasher.hook = func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return second.CoordinatedAtomic(ctx, func(session db.Session) error {
				_, err := models.UserObjects.Create(ctx, session, models.NewUserCreate("competing", "RACE", f.user.EncodedPassword, time.Now().UTC()))
				return err
			})
		}
		boundary := &userManagementBoundary{ManagementBackend: f.runtime}
		result, err := f.manager(t, boundary).CreateUser(t.Context(), f.actor, identity.NewUserCreate("candidate", "race").WithCaseInsensitiveUsernameCheck(), managementNewPassword)
		assertUsernameUnique(t, err)
		if result.ID != 0 || f.hasher.calls.Load() != 1 || boundary.calls != 1 {
			t.Fatal("late duplicate published or retried")
		}
		afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
		if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
			t.Fatal("late rejection changed durable rows")
		}
		if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 3 {
			t.Fatal("late duplicate inserted", count, err)
		}
	})
	t.Run("competing_case_variants_have_one_winner", func(t *testing.T) {
		first, second := open(t)
		f := newManagementFixture(t, first, 0)
		other, err := systemstate.OpenIdentity(t.Context(), second, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		arrived := make(chan struct{}, 2)
		release := make(chan struct{})
		f.hasher.hook = func(ctx context.Context) error {
			arrived <- struct{}{}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		type outcome struct {
			result identity.UserDetails
			err    error
		}
		results := make(chan outcome, 2)
		for i, backend := range []identity.ManagementBackend{f.runtime, other} {
			manager := f.manager(t, backend)
			input := identity.NewUserCreate(fmt.Sprintf("racer-%d", i), []string{"variant", "VARIANT"}[i]).WithCaseInsensitiveUsernameCheck()
			go func() {
				result, err := manager.CreateUser(ctx, f.actor, input, managementNewPassword)
				results <- outcome{result, err}
			}()
		}
		for range 2 {
			select {
			case <-arrived:
			case <-ctx.Done():
				close(release)
				t.Fatal("preflight retained read scope", ctx.Err())
			}
		}
		close(release)
		success, rejected := 0, 0
		for range 2 {
			select {
			case got := <-results:
				if got.err == nil {
					success++
					if got.result.ID == 0 {
						t.Fatal("empty winner")
					}
				} else {
					assertUsernameUnique(t, got.err)
					rejected++
					if got.result.ID != 0 {
						t.Fatal("loser published")
					}
				}
			case <-ctx.Done():
				t.Fatal("creation did not finish", ctx.Err())
			}
		}
		if success != 1 || rejected != 1 || f.hasher.calls.Load() != 2 {
			t.Fatal("case-insensitive write fence failed", success, rejected)
		}
		if count, err := models.UserObjects.Using(first).Count(t.Context()); err != nil || count != 3 {
			t.Fatal("duplicate persisted", count, err)
		}
	})
	t.Run("permission_and_failed_snapshot_do_not_hash", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		input := identity.NewUserCreate("denied", "MEMBER").WithCaseInsensitiveUsernameCheck()
		credential, err := f.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID)
		if err != nil {
			t.Fatal(err)
		}
		result, err := f.manager(t, f.runtime).CreateUser(t.Context(), credential.Principal(), input, managementNewPassword)
		if result.ID != 0 || !errors.Is(err, &identity.Error{Code: identity.CodePermission}) {
			t.Fatal("duplicate exposed before admission", err)
		}
		boundary := &managementBoundary{ManagementBackend: f.runtime, mode: "read_end_failure"}
		result, err = f.manager(t, boundary).CreateUser(t.Context(), f.actor, input, managementNewPassword)
		_, rejected := validation.Rejected(err)
		if err == nil || rejected || result.ID != 0 || f.hasher.calls.Load() != 0 {
			t.Fatal("failed read scope published form rejection or hashed", err)
		}
	})
	for _, phase := range []string{"read", "write"} {
		t.Run("lookup_failure_"+phase, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
			fault := errors.New("private username lookup failure")
			boundary := &usernameLookupBoundary{ManagementBackend: f.runtime, phase: phase, fault: fault}
			result, err := f.manager(t, boundary).CreateUser(t.Context(), f.actor, identity.NewUserCreate("candidate", "candidate").WithCaseInsensitiveUsernameCheck(), managementNewPassword)
			_, rejected := validation.Rejected(err)
			wantHashes := int64(0)
			if phase == "write" {
				wantHashes = 1
			}
			if !errors.Is(err, fault) || rejected || result.ID != 0 || boundary.hits != 1 || f.hasher.calls.Load() != wantHashes {
				t.Fatal("lookup failure published, retried or became validation", err)
			}
			afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
			if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
				t.Fatal("failed lookup changed durable state")
			}
			if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 2 {
				t.Fatal("lookup failure wrote user", count, err)
			}
		})
	}
	for _, mode := range []string{"audit_failure", "unknown_commit"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			boundary := &userManagementBoundary{ManagementBackend: f.runtime, mode: mode}
			result, err := f.manager(t, boundary).CreateUser(t.Context(), f.actor, identity.NewUserCreate("uncertain", "uncertain").WithCaseInsensitiveUsernameCheck(), managementNewPassword)
			if err == nil || result.ID != 0 || boundary.calls != 1 || f.hasher.calls.Load() != 1 {
				t.Fatal("failed creation published or retried", err)
			}
			committed := mode == "unknown_commit"
			if committed && !errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown}) || !committed && boundary.faults != 1 {
				t.Fatal("fault boundary missed", err)
			}
			stored, found, readErr := models.UserObjects.Using(backend).Filter(models.UserFields.PrincipalID.Exact("uncertain")).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
			if readErr != nil || found != committed {
				t.Fatal("creation transaction changed", readErr)
			}
			if found {
				history, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", stored.ID, 10)
				if err != nil || len(history) != 1 {
					t.Fatal("committed audit lost", err)
				}
			}
			f.assertOutcome(t, false)
		})
	}
}

type usernameLookupBoundary struct {
	identity.ManagementBackend
	phase string
	fault error
	hits  int
}

func (b *usernameLookupBoundary) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	return b.ManagementBackend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		if b.phase == "read" {
			reader = usernameLookupReader{Queryer: reader, owner: b}
		}
		return callback(reader)
	})
}

func (b *usernameLookupBoundary) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	owner, ok := b.ManagementBackend.(db.CoordinatedRelationAtomic)
	if !ok {
		return errors.New("missing native relation owner")
	}
	return owner.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error {
		if b.phase == "write" {
			session = usernameLookupSession{RelationSession: session, owner: b}
		}
		return callback(session)
	})
}

func (b *usernameLookupBoundary) query(ctx context.Context, reader db.Queryer, plan query.Plan) (db.Rows, error) {
	for _, condition := range plan.Conditions() {
		if condition.Lookup() == query.LookupIExact {
			b.hits++
			return nil, b.fault
		}
	}
	return reader.Query(ctx, plan)
}

type usernameLookupReader struct {
	db.Queryer
	owner *usernameLookupBoundary
}

func (r usernameLookupReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return r.owner.query(ctx, r.Queryer, plan)
}

type usernameLookupSession struct {
	db.RelationSession
	owner *usernameLookupBoundary
}

func (s usernameLookupSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return s.owner.query(ctx, s.RelationSession, plan)
}
func (s usernameLookupSession) ValidateSession(ctx context.Context) error {
	return s.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}
