package systemstate

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

type identityLoginPersistence struct {
	runtime *Runtime
	manager *sessions.Manager
}

func (*identityLoginPersistence) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("systemstate.LoginPersistence{redacted}"))
}

// LoginPersistence prepares coupled identity/session writes for this exact
// manager and storage domain, without I/O. Legacy operator runtimes have no
// user last_login field and cannot provide this identity capability.
func (runtime *Runtime) LoginPersistence(manager *sessions.Manager) (auth.LoginPersistence, error) {
	if runtime == nil || runtime.loginRecorder == nil || runtime.sessionStore == nil || manager == nil || manager.Store() != runtime.sessionStore {
		return nil, &Error{Code: CodeInvalidConfig, Field: "login", Detail: "login persistence requires this identity runtime's session manager"}
	}
	return &identityLoginPersistence{runtime, manager}, nil
}

func (p *identityLoginPersistence) Sessions() *sessions.Manager {
	if p == nil {
		return nil
	}
	return p.manager
}

func (p *identityLoginPersistence) Login(ctx context.Context, login auth.SessionLogin) (auth.SessionLoginResult, error) {
	if p == nil || p.runtime == nil || p.manager == nil {
		return auth.SessionLoginResult{}, &Error{Code: CodeInvalidConfig, Field: "login", Detail: "login persistence is uninitialized"}
	}
	if err := p.runtime.validBackendCall(ctx); err != nil {
		return auth.SessionLoginResult{}, err
	}
	if err := login.Validate(); err != nil {
		return auth.SessionLoginResult{}, err
	}
	store := &loginSessionStore{durableSessionStore: p.runtime.sessionStore, runtime: p.runtime, login: login}
	manager, err := p.manager.WithStore(store)
	if err != nil {
		return auth.SessionLoginResult{}, err
	}
	record, err := auth.EstablishSession(ctx, manager, login.Credential, login.Previous)
	if err != nil {
		if store.denied {
			return auth.SessionLoginResult{}, auth.ErrInvalidCredentials
		}
		return auth.SessionLoginResult{}, loginPersistenceFailure(err)
	}
	if !store.current.Principal().Authenticated() || !store.current.MatchesSessionStamp(login.Credential.SessionStamp()) {
		return auth.SessionLoginResult{}, loginPersistenceFailure(nil)
	}
	return auth.SessionLoginResult{Credential: store.current, Record: record}, nil
}

// The manager still prepares time/entropy outside the database scope. Only a
// successful publication enters the same transaction's identity observation.
type loginSessionStore struct {
	*durableSessionStore
	runtime *Runtime
	login   auth.SessionLogin
	current auth.Credential
	denied  bool
}

func (store *loginSessionStore) Create(ctx context.Context, record sessions.Record) (bool, error) {
	_, created, err := store.write(ctx, func(scoped *durableSessionStore) (sessions.Record, bool, error) {
		created, err := scoped.Create(ctx, record)
		return record, created, err
	})
	return created, err
}

func (store *loginSessionStore) Rotate(ctx context.Context, id sessions.ID, record sessions.Record) (sessions.Record, bool, error) {
	return store.write(ctx, func(scoped *durableSessionStore) (sessions.Record, bool, error) {
		published, changed, err := scoped.Rotate(ctx, id, record)
		if err == nil && !changed {
			err = &sessions.Error{Code: sessions.CodeNotFound, Detail: "login session is missing or expired"}
		}
		return published, changed, err
	})
}

func (store *loginSessionStore) Replace(ctx context.Context, id sessions.ID, record sessions.Record) (sessions.Record, bool, error) {
	return store.write(ctx, func(scoped *durableSessionStore) (sessions.Record, bool, error) {
		published, changed, err := scoped.Replace(ctx, id, record)
		if err == nil && !changed {
			err = &sessions.Error{Code: sessions.CodeNotFound, Detail: "login session is missing or expired"}
		}
		return published, changed, err
	})
}

// Manager.Rotate can detect immutable absolute expiry before asking the store.
// Login must not publish that cleanup independently of its failed observation.
func (store *loginSessionStore) Delete(context.Context, sessions.ID) error {
	return &sessions.Error{Code: sessions.CodeNotFound, Detail: "login session is expired"}
}

func (store *loginSessionStore) write(ctx context.Context, mutate func(*durableSessionStore) (sessions.Record, bool, error)) (sessions.Record, bool, error) {
	var record sessions.Record
	var current auth.Credential
	var callbackErr, mutationErr error
	changed := false
	calls := 0
	err := store.runtime.withAtomic(ctx, func(session db.Session) error {
		calls++
		if calls != 1 || isNilInterface(session) {
			callbackErr = loginPersistenceFailure(nil)
			return callbackErr
		}
		gate := &borrowedLoginGate{session: session}
		scoped := *store.durableSessionStore
		scoped.gate = gate
		record, changed, mutationErr = mutate(&scoped)
		callbackErr = mutationErr
		if callbackErr == nil && !gate.called {
			callbackErr = loginPersistenceFailure(nil)
		}
		if callbackErr == nil && changed {
			current, callbackErr = store.runtime.loginRecorder.RecordIn(ctx, session, store.login)
		}
		if callbackErr == nil {
			callbackErr = ctx.Err()
		}
		return callbackErr
	})
	if calls == 1 && err != nil && err == callbackErr {
		if err == auth.ErrInvalidCredentials {
			store.denied = true
			return sessions.Record{}, false, err
		}
		// Only the session mutation's own confirmed refusal may preserve a
		// retryable collision or capacity classification. A login/cleanup error
		// containing one of these causes must never be retried or reclassified.
		if err == mutationErr {
			if typed, ok := err.(*sessions.Error); ok && typed != nil && typed.Cause == nil && (typed.Code == sessions.CodeEntropy || typed.Code == sessions.CodeStoreFull) {
				return sessions.Record{}, false, err
			}
		}
	}
	if err != nil || callbackErr != nil || calls != 1 {
		return sessions.Record{}, false, loginPersistenceFailure(errors.Join(err, callbackErr))
	}
	if changed {
		store.current = current
		return record, true, nil
	}
	return sessions.Record{}, false, nil
}

type borrowedLoginGate struct {
	session db.Session
	called  bool
}

func (gate *borrowedLoginGate) withAtomic(ctx context.Context, callback func(db.Session) error) error {
	if ctx == nil || gate.called || isNilInterface(gate.session) || callback == nil {
		return loginPersistenceFailure(nil)
	}
	gate.called = true
	if err := ctx.Err(); err != nil {
		return err
	}
	return callback(gate.session)
}

// Normalize unknown outcome and cancellation before dropping private driver
// details. A wrapped admission/collision error cannot become an ordinary denial.
func loginPersistenceFailure(err error) error {
	if code := operatorOutcomeUnknownCode(err); code != "" {
		return &Error{Code: CodePersistence, Field: "login", Detail: "identity login outcome is unknown; reconciliation is required", Cause: &query.Error{Category: query.CategoryBackend, Code: code}}
	}
	if safeOperatorErrorIs(err, context.Canceled) {
		return context.Canceled
	}
	if safeOperatorErrorIs(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return &Error{Code: CodePersistence, Field: "login", Detail: "identity login transaction failed"}
}
