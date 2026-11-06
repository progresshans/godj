package accountstoretest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
)

func accountSessions(t *testing.T, backend sessiondb.Backend, now *time.Time) (*accountmodel.SessionStore[models.User], *sessions.Manager, auth.LoginPersistence) {
	t.Helper()
	store, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	login, err := store.LoginPersistence(manager)
	if err != nil || login.Sessions() != manager {
		t.Fatal("exact login manager binding", err)
	}
	return store, manager, login
}
func selectedSessionLogin(credential auth.Credential, previous sessions.Record, at time.Time) auth.SessionLogin {
	return auth.SessionLogin{Credential: credential, Previous: previous, At: at, Admit: func(context.Context, auth.Principal) error { return nil }}
}
func accountSessionRecord(t *testing.T, store sessions.Store, expected sessions.Record, want bool) {
	t.Helper()
	actual, present, err := store.Load(t.Context(), expected.ID())
	if err != nil || present != want || want && !reflect.DeepEqual(actual.Snapshot(), expected.Snapshot()) || !want && actual.ID().Valid() {
		t.Fatal("selected session observation", present, err)
	}
}
func accountSessionCount(t *testing.T, backend Backend) int64 {
	t.Helper()
	count, err := sessionmodels.SessionObjects.Using(backend).Count(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return count
}
func transitionSessionDomain(t *testing.T, backend Backend, binding accountmodel.Binding[models.User], prior domain.Receipt) domain.Receipt {
	t.Helper()
	fingerprint, err := binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("native session domain transition"))
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	var next domain.Receipt
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		var err error
		next, err = domain.TransitionIn(t.Context(), scope, prior, change, joined.Add(time.Minute))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return next
}

func sessionLoginAndDomain(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	now := joined
	store, manager, owner := accountSessions(t, backend, &now)
	previous, err := manager.Create(t.Context(), map[string]string{"anonymous": "preserved"})
	if err != nil {
		t.Fatal(err)
	}
	if row := profileRow(t, backend, f.user.ID); row.LastLogin != nil {
		t.Fatal("anonymous session recorded login")
	}
	now = now.Add(time.Minute)
	login := selectedSessionLogin(credential, previous, now)
	login.Admit = func(_ context.Context, current auth.Principal) error {
		if !current.Has("account.direct") || current.ID() != credential.Principal().ID() {
			return errors.New("login admission did not receive current account")
		}
		return nil
	}
	result, err := owner.Login(t.Context(), login)
	if err != nil || !result.Credential.MatchesSessionStamp(credential.SessionStamp()) || result.Record.ID() == previous.ID() {
		t.Fatal("selected account login", err)
	}
	if value, _ := result.Record.Value("anonymous"); value != "preserved" {
		t.Fatal("anonymous rotation lost data")
	}
	accountSessionRecord(t, store, previous, false)
	accountSessionRecord(t, store, result.Record, true)
	row := profileRow(t, reopen(t), f.user.ID)
	if row.LastLogin == nil || !row.LastLogin.Equal(now) || row.Revision != 1 || row.EncodedPassword != f.user.EncodedPassword || row.PrincipalID != f.user.PrincipalID {
		t.Fatal("login changed management state or lost observation")
	}
	if count, err := accountaudit.EventObjects.Using(backend).Count(t.Context()); count != 0 || err != nil {
		t.Fatal("observational login appended management audit", count, err)
	}
	reopened, _, _ := accountSessions(t, reopen(t), &now)
	accountSessionRecord(t, reopened, result.Record, true)
	if _, _, err := manager.Peek(t.Context(), result.Record.ID()); err != nil {
		t.Fatal(err)
	}
	// Current grants must be reloaded even though their removal leaves the
	// credential stamp unchanged. The result and Admit observe that same state.
	if _, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &f.direct); err != nil {
		t.Fatal(err)
	}
	login = selectedSessionLogin(credential, result.Record, now.Add(time.Minute))
	login.Admit = func(_ context.Context, current auth.Principal) error {
		if current.Has("account.direct") {
			return errors.New("stale grant entered admission")
		}
		return nil
	}
	result, err = owner.Login(t.Context(), login)
	if err != nil || result.Credential.Principal().Has("account.direct") {
		t.Fatal("fresh login grant observation", err)
	}
	other, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: f.user.PrincipalID})
	if err != nil {
		t.Fatal(err)
	}
	anonymous, err := manager.Create(t.Context(), map[string]string{"anonymous": "independent"})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		count, err := store.RevokeIn(t.Context(), scope, f.user.PrincipalID, result.Record.ID())
		if err == nil && count != 1 {
			return fmt.Errorf("revoke count %d", count)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	accountSessionRecord(t, store, other, false)
	accountSessionRecord(t, store, anonymous, true)
	accountSessionRecord(t, store, result.Record, true)
	transitionSessionDomain(t, backend, f.binding, f.ownership)
	accountSessionRecord(t, store, result.Record, false)
	accountSessionRecord(t, store, anonymous, false)
	// Reusing an identical raw bearer and principal in the new domain cannot
	// select or erase either old namespace row.
	newValues := result.Record.Snapshot()
	newValues.Values = map[string]string{auth.SessionPrincipalIDKey: f.user.PrincipalID, "new": "generation"}
	newRecord, err := sessions.RestoreRecord(newValues, sessions.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if created, err := store.Create(t.Context(), newRecord); !created || err != nil {
		t.Fatal("new domain bearer collision was incorrectly inherited", err)
	}
	accountSessionRecord(t, store, newRecord, true)
	before := profileRow(t, backend, f.user.ID)
	if rejected, err := owner.Login(t.Context(), selectedSessionLogin(credential, sessions.Record{}, now)); err != auth.ErrInvalidCredentials || rejected.Record.ID().Valid() {
		t.Fatal("old credential established a new-domain session", err)
	}
	if after := profileRow(t, backend, f.user.ID); !reflect.DeepEqual(before, after) || accountSessionCount(t, backend) != 3 {
		t.Fatal("old-domain denial persisted partial state")
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		count, err := store.RevokeIn(t.Context(), scope, f.user.PrincipalID, sessions.ID{})
		if err == nil && count != 1 {
			return fmt.Errorf("current namespace revoke count %d", count)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if count := accountSessionCount(t, backend); count != 2 {
		t.Fatal("current revoke erased historical domain sessions", count)
	}
	role, _ := f.binding.Role()
	role.AuthenticateNormalization = ir.AccountUnicode16NFKC
	wrongBinding, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	wrongCredentials, err := accountmodel.NewCredentialStore(backend, wrongBinding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := accountmodel.NewSessionStore(wrongCredentials, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if value, present, err := wrong.Load(t.Context(), result.Record.ID()); value.ID().Valid() || present || !errors.Is(err, domain.ErrMismatch) {
		t.Fatal("mismatched selected role accessed sessions", err)
	}
}

type loginMutationScope struct {
	db.Session
	fault    error
	inserted *int
}

func (scope *loginMutationScope) ValidateSession(ctx context.Context) error {
	return scope.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *loginMutationScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := scope.Session.Insert(ctx, plan)
	if err == nil && plan.Table() == "godj_sessions_session" {
		*scope.inserted++
		return key, scope.fault
	}
	return key, err
}

func sessionCredentialRecheck(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	now := joined
	store, manager, owner := accountSessions(t, backend, &now)
	previous, err := manager.Create(t.Context(), map[string]string{"anonymous": "preserved"})
	if err != nil {
		t.Fatal(err)
	}
	changedHash, err := f.hasher.Hash(t.Context(), "changed-after-verification")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"inactive", "changed_hash", "missing_principal"} {
		t.Run(mode, func(t *testing.T) {
			patch := models.UserPatch{}
			switch mode {
			case "inactive":
				patch = patch.WithActive(false)
			case "changed_hash":
				patch = patch.WithEncodedPassword(changedHash)
			case "missing_principal":
				patch = patch.WithPrincipalID("different-principal")
			}
			if _, err := models.UserObjects.Patch(t.Context(), backend, f.user, patch); err != nil {
				t.Fatal(err)
			}
			before := profileRow(t, backend, f.user.ID)
			admitted := false
			login := selectedSessionLogin(credential, previous, now)
			login.Admit = func(context.Context, auth.Principal) error { admitted = true; return nil }
			result, err := owner.Login(t.Context(), login)
			if err != auth.ErrInvalidCredentials || result.Record.ID().Valid() || admitted {
				t.Fatal("stale verified credential entered login", mode, err)
			}
			if after := profileRow(t, backend, f.user.ID); !reflect.DeepEqual(before, after) || accountSessionCount(t, backend) != 1 {
				t.Fatal("recheck refusal left partial login")
			}
			accountSessionRecord(t, store, previous, true)
			if _, err := models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithActive(true).WithEncodedPassword(f.user.EncodedPassword).WithPrincipalID(f.user.PrincipalID)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func sessionOptionalLastLogin(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	role, _ := f.binding.Role()
	role.LastLogin = ""
	binding, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	transitionSessionDomain(t, backend, binding, f.ownership)
	credentials, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	credential, present, err := credentials.CredentialByID(t.Context(), f.user.PrincipalID)
	if err != nil || !present {
		t.Fatal(err)
	}
	store, err := accountmodel.NewSessionStore(credentials, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return joined }})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.LoginPersistence(manager)
	if err != nil {
		t.Fatal(err)
	}
	result, err := owner.Login(t.Context(), selectedSessionLogin(credential, sessions.Record{}, joined))
	if err != nil || !result.Record.ID().Valid() {
		t.Fatal("optional last-login role was required", err)
	}
	if row := profileRow(t, backend, f.user.ID); !reflect.DeepEqual(row, f.user) {
		t.Fatal("absent role wrote an undeclared timestamp")
	}
}
func sessionLoginFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	now := joined
	base, manager, _ := accountSessions(t, backend, &now)
	previous, err := manager.Create(t.Context(), map[string]string{"anonymous": "rollback"})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"skipped", "nil_session", "root_session", "repeated", "write_failure", "session_insert", "denied", "denied_cleanup", "admission_error", "swallowed_rejection", "unknown_rollback", "unknown_commit", "cancel_confirmed_rollback", "post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private selected login failure")
			probe := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
			inserted := 0
			if mode == "session_insert" {
				probe.decorate = func(scope db.Session) db.Session { return &loginMutationScope{scope, fault, &inserted} }
			}
			if mode == "denied_cleanup" {
				probe.mode = "cleanup_failure"
			}
			_, _, owner := accountSessions(t, probe, &now)
			before := profileRow(t, backend, f.user.ID)
			count := accountSessionCount(t, backend)
			login := selectedSessionLogin(credential, previous, now.Add(time.Minute))
			if mode == "denied" || mode == "denied_cleanup" || mode == "swallowed_rejection" {
				login.Admit = func(context.Context, auth.Principal) error { return auth.ErrInvalidCredentials }
			}
			if mode == "admission_error" {
				login.Admit = func(context.Context, auth.Principal) error { return fault }
			}
			result, err := owner.Login(ctx, login)
			committed := mode == "unknown_commit" || mode == "post_commit_cancel"
			if mode == "post_commit_cancel" {
				if err != nil || !result.Record.ID().Valid() || ctx.Err() == nil {
					t.Fatal("late cancellation erased committed login", err)
				}
			} else if err == nil || result.Record.ID().Valid() || result.Credential.SessionStamp() != "" {
				t.Fatal("failed login published result", err)
			}
			if mode == "denied" {
				if err != auth.ErrInvalidCredentials {
					t.Fatal("confirmed admission denial lost", err)
				}
			} else if err == auth.ErrInvalidCredentials {
				t.Fatal("execution failure became admission denial")
			}
			if (mode == "write_failure" || mode == "session_insert" || mode == "denied_cleanup" || mode == "admission_error") && !errors.Is(err, fault) {
				t.Fatal("login private cause lost", err)
			}
			if (mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection") && !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
				t.Fatal("unknown login callback lost", err)
			}
			if (mode == "unknown_commit" || mode == "unknown_rollback") && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
				t.Fatal("native unknown login outcome lost", err)
			}
			if probe.calls != 1 || mode == "session_insert" && inserted != 1 {
				t.Fatal("login retried or missed native write", probe.calls, inserted)
			}
			after := profileRow(t, reopen(t), f.user.ID)
			if committed {
				if after.LastLogin == nil || !after.LastLogin.Equal(login.At) || after.Revision != before.Revision || accountSessionCount(t, backend) != count {
					t.Fatal("login commit did not couple timestamp/session")
				}
				accountSessionRecord(t, base, previous, false)
				// Reconcile the durable new bearer only when it was confirmed to
				// this caller. Unknown commits return no raw bearer capability.
				if mode == "post_commit_cancel" {
					accountSessionRecord(t, base, result.Record, true)
				}
				previous, err = manager.Create(t.Context(), map[string]string{"anonymous": "rollback"})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if !reflect.DeepEqual(before, after) || accountSessionCount(t, backend) != count {
					t.Fatal("failed login retained partial state")
				}
				accountSessionRecord(t, base, previous, true)
			}
		})
	}
}

type sessionSnapshotOwner struct {
	Backend
	observed chan struct{}
	release  chan struct{}
}

func (owner *sessionSnapshotOwner) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	return owner.Backend.ReadSnapshot(ctx, func(scope db.Queryer) error { return read(&sessionSnapshotReader{Queryer: scope, owner: owner}) })
}

type sessionSnapshotReader struct {
	db.Queryer
	owner *sessionSnapshotOwner
	once  sync.Once
}

func (reader *sessionSnapshotReader) ValidateSession(ctx context.Context) error {
	return reader.Queryer.(db.SessionValidator).ValidateSession(ctx)
}
func (reader *sessionSnapshotReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := reader.Queryer.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	return &sessionSnapshotRows{Rows: rows, after: func() {
		reader.once.Do(func() {
			close(reader.owner.observed)
			select {
			case <-reader.owner.release:
			case <-ctx.Done():
			}
		})
	}}, nil
}

type sessionSnapshotRows struct {
	db.Rows
	after func()
	once  sync.Once
}

func (rows *sessionSnapshotRows) Close() error {
	err := rows.Rows.Close()
	if err == nil {
		rows.once.Do(rows.after)
	}
	return err
}
func sessionDomainSnapshot(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	base, manager, _ := accountSessions(t, backend, &now)
	original, err := manager.Create(t.Context(), map[string]string{"old": "domain"})
	if err != nil {
		t.Fatal(err)
	}
	owner := &sessionSnapshotOwner{Backend: backend, observed: make(chan struct{}), release: make(chan struct{})}
	bound, _, _ := accountSessions(t, owner, &now)
	type result struct {
		record  sessions.Record
		present bool
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		value, present, err := bound.Load(t.Context(), original.ID())
		finished <- result{value, present, err}
	}()
	released := false
	defer func() {
		if !released {
			close(owner.release)
		}
	}()
	select {
	case <-owner.observed:
	case <-time.After(5 * time.Second):
		t.Fatal("session domain snapshot was not observed")
	}
	transitionSessionDomain(t, reopen(t), f.binding, f.ownership)
	close(owner.release)
	released = true
	select {
	case result := <-finished:
		if result.err != nil || !result.present || !reflect.DeepEqual(result.record.Snapshot(), original.Snapshot()) {
			t.Fatal("domain and session mixed different snapshots", result.present, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session snapshot did not complete")
	}
	accountSessionRecord(t, base, original, false)
}
