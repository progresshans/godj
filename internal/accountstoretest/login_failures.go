package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

type loginFailureBackend struct {
	Backend
	mode          string
	fault         error
	cancel        context.CancelFunc
	calls, faults int
	effects       []string
	nativeError   error
}

func (owner *loginFailureBackend) CoordinatedAtomic(ctx context.Context, write func(db.Session) error) error {
	owner.calls++
	if owner.mode == "skipped" {
		return nil
	}
	nativeContext := ctx
	if owner.mode == "cancel_confirmed_rollback" {
		var cleanup context.CancelFunc
		nativeContext, cleanup = context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cleanup()
	}
	err := owner.Backend.CoordinatedAtomic(nativeContext, func(scope db.Session) error {
		if owner.mode == "nil_session" {
			return write(nil)
		}
		if owner.mode == "root_session" {
			return write(owner.Backend)
		}
		borrowed := &loginFailureScope{Session: scope, owner: owner}
		err := write(borrowed)
		if owner.mode == "swallowed_failure" || owner.mode == "swallowed_denial" {
			return nil
		}
		if owner.mode == "repeated" {
			return errors.Join(err, write(borrowed))
		}
		if err != nil {
			return err
		}
		if owner.mode == "callback_cancel" || owner.mode == "unknown_rollback" {
			return context.Canceled
		}
		if owner.mode == "cancel_before_commit" || owner.mode == "cancel_confirmed_rollback" {
			owner.cancel()
			if owner.mode == "cancel_confirmed_rollback" {
				return ctx.Err()
			}
		}
		return nil
	})
	owner.nativeError = err
	if owner.mode == "unknown_rollback" {
		return errors.Join(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})
	}
	if owner.mode == "denied_cleanup" {
		return errors.Join(err, owner.fault)
	}
	if err != nil {
		return err
	}
	if owner.mode == "unknown_commit" {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown}
	}
	if owner.mode == "post_commit_cancel" {
		owner.cancel()
	}
	return nil
}

type loginFailureScope struct {
	db.Session
	owner *loginFailureBackend
}

func (scope *loginFailureScope) ValidateSession(ctx context.Context) error {
	return scope.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *loginFailureScope) fail() error { scope.owner.faults++; return scope.owner.fault }
func (scope *loginFailureScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if scope.owner.mode == "query" || scope.owner.mode == "swallowed_failure" {
		return nil, scope.fail()
	}
	if scope.owner.mode == "session_query_entropy" && plan.Table() == "godj_sessions_session" {
		return nil, scope.fail()
	}
	return scope.Session.Query(ctx, plan)
}
func (scope *loginFailureScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if plan.Table() == "godj_sessions_session" && (scope.owner.mode == "before_insert" || scope.owner.mode == "before_insert_entropy") {
		return 0, scope.fail()
	}
	key, err := scope.Session.Insert(ctx, plan)
	if err == nil {
		if plan.Table() == "godj_sessions_session" && key <= 0 {
			return 0, errors.New("login prefix did not allocate a native session row")
		}
		name := "insert:" + plan.Table()
		if plan.Table() == "godj_sessions_session" {
			name = "insert"
		}
		scope.owner.effects = append(scope.owner.effects, name)
		if plan.Table() == "godj_sessions_session" && (scope.owner.mode == "after_insert" || scope.owner.mode == "after_insert_entropy") {
			return 0, scope.fail()
		}
	}
	return key, err
}
func (scope *loginFailureScope) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if plan.Table() == "godj_identity_user" && scope.owner.mode == "before_last_login" {
		return 0, scope.fail()
	}
	count, err := scope.Session.Update(ctx, plan)
	if err == nil {
		if plan.Table() == "godj_identity_user" && count != 1 {
			return 0, errors.New("login prefix did not update exactly one account row")
		}
		name := "update:" + plan.Table()
		if plan.Table() == "godj_identity_user" {
			name = "last_login"
		}
		scope.owner.effects = append(scope.owner.effects, name)
		if plan.Table() == "godj_identity_user" && (scope.owner.mode == "after_last_login" || scope.owner.mode == "after_last_login_entropy") {
			return 0, scope.fail()
		}
	}
	return count, err
}
func (scope *loginFailureScope) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	count, err := scope.Session.Delete(ctx, plan)
	if err == nil {
		if plan.Table() == "godj_sessions_session" && count != 1 {
			return 0, errors.New("login prefix did not delete exactly one prior session")
		}
		name := "delete:" + plan.Table()
		if plan.Table() == "godj_sessions_session" {
			name = "delete"
		}
		scope.owner.effects = append(scope.owner.effects, name)
		if plan.Table() == "godj_sessions_session" && (scope.owner.mode == "after_delete" || scope.owner.mode == "after_delete_entropy") {
			return 0, scope.fail()
		}
	}
	return count, err
}

func runLoginWriteFailures(t *testing.T, open Open) {
	t.Run("login_write_failures", func(t *testing.T) {
		for _, operation := range []string{"create", "rotate", "replace"} {
			t.Run(operation, func(t *testing.T) {
				modes := []string{"skipped", "nil_session", "root_session", "repeated", "query", "swallowed_failure", "session_query_entropy", "before_insert", "before_insert_entropy", "after_insert", "after_insert_entropy", "before_last_login", "after_last_login", "after_last_login_entropy", "callback_cancel", "cancel_before_commit", "cancel_confirmed_rollback", "unknown_rollback", "unknown_commit", "post_commit_cancel"}
				if operation != "create" {
					modes = append(modes, "after_delete", "after_delete_entropy")
				}
				for _, mode := range modes {
					t.Run(mode, func(t *testing.T) {
						backend, reopen := open(t)
						f := provisionLogin(t, backend, true)
						var previous sessions.Record
						if operation != "create" {
							values := map[string]string{"payload": "private old payload"}
							if operation == "replace" {
								values[auth.SessionPrincipalIDKey], values[auth.SessionCredentialStampKey] = f.actor.Principal().ID(), f.actor.SessionStamp()
							}
							var err error
							previous, err = f.runtime.Sessions().Create(t.Context(), values)
							if err != nil {
								t.Fatal(err)
							}
						}
						f.instant.Store(nativeLoginInstant.Add(time.Minute + 999*time.Nanosecond).UnixNano())
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						var fault error = errors.New("private selected login write failure")
						if strings.HasSuffix(mode, "_entropy") {
							fault = &sessions.Error{Code: sessions.CodeEntropy}
						}
						boundary := &loginFailureBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
						owner := newUserReferenceOwner(t, boundary, f.userReferenceFixture, f.config(t))
						before := userReferenceSnapshot(t, reopen(t))
						entropy := f.entropy.count()
						result, err := owner.runtime.LoginPersistence().Login(ctx, auth.SessionLogin{Credential: f.credential, Previous: previous, At: f.clock(), Admit: loginAdmission})
						id := f.entropy.one(t, entropy)
						expectedRecord := loginExpectedRecord(t, f.credential, previous, operation, id, f.clock())
						committed := mode == "unknown_commit" || mode == "post_commit_cancel"
						if mode == "post_commit_cancel" {
							if err != nil || ctx.Err() != context.Canceled || !reflect.DeepEqual(result.Record.Snapshot(), expectedRecord.Snapshot()) || result.Credential.Principal().ID() != f.target.PrincipalID || !result.Credential.MatchesSessionStamp(f.credential.SessionStamp()) {
								t.Fatal("late cancellation erased confirmed login", err)
							}
						} else {
							loginNoResult(t, result, err)
							if err == auth.ErrInvalidCredentials || !errors.Is(err, &sessions.Error{Code: sessions.CodeStoreFailure}) || strings.Contains(fmt.Sprintf("%+v", err), fault.Error()) {
								t.Fatal("login infrastructure failure became denial or disclosed its cause", err)
							}
						}
						if boundary.calls != 1 || owner.boundary.calls != 1 || owner.hasher.hashes.Load() != 0 || owner.hasher.verifies.Load() != 0 || owner.policies.Load() != 0 {
							t.Fatal("selected login replayed persistence or password work")
						}
						want := []string{}
						if operation != "create" {
							want = append(want, "delete")
						}
						want = append(want, "insert", "last_login")
						switch mode {
						case "skipped", "nil_session", "root_session", "query", "swallowed_failure", "session_query_entropy":
							want = nil
						case "before_insert", "before_insert_entropy":
							want = want[:len(want)-2]
						case "after_insert", "after_insert_entropy", "before_last_login":
							want = want[:len(want)-1]
						case "after_delete", "after_delete_entropy":
							want = want[:1]
						}
						if !slices.Equal(boundary.effects, want) {
							t.Fatal("login fault missed its exact native write prefix", mode, boundary.effects, want)
						}
						if mode == "query" || mode == "swallowed_failure" || mode == "session_query_entropy" || strings.HasPrefix(mode, "before_") || strings.HasPrefix(mode, "after_") {
							if boundary.faults != 1 || !errors.Is(err, fault) {
								t.Fatal("login fault did not retain its single private cause", err)
							}
						}
						if mode == "skipped" || mode == "repeated" || mode == "swallowed_failure" || mode == "unknown_rollback" {
							if !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
								t.Fatal("uncertain login ownership was downgraded", err)
							}
						}
						if mode == "unknown_commit" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
							t.Fatal("unknown login commit was downgraded", err)
						}
						if mode == "callback_cancel" || mode == "cancel_before_commit" || mode == "cancel_confirmed_rollback" || mode == "unknown_rollback" {
							if !errors.Is(err, context.Canceled) || boundary.nativeError == nil || !errors.Is(err, boundary.nativeError) {
								t.Fatal("login cancellation lost the native outcome", err)
							}
						}
						if mode == "cancel_confirmed_rollback" && (errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown})) {
							t.Fatal("acknowledged login rollback became uncertain", err)
						}
						after := userReferenceSnapshot(t, reopen(t))
						if committed {
							if boundary.nativeError != nil {
								t.Fatal("committed login had a native failure", boundary.nativeError)
							}
							assertUserReferenceState(t, loginExpectedState(t, before, after, f.credential, f.target, previous, expectedRecord, f.clock()), after, f.actor.Principal())
						} else {
							assertUserReferenceState(t, before, after, f.actor.Principal())
						}
						accountSessionRecord(t, f.runtime.SessionStore(), expectedRecord, committed)
						if previous.ID().Valid() {
							accountSessionRecord(t, f.runtime.SessionStore(), previous, !committed)
						}
					})
				}
			})
		}
	})
}
