package model

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/sessionwire"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

// SessionStore binds server-side sessions to the selected account's CURRENT
// durable credential domain. Every operation observes that domain and its
// records in one native scope. No request field can select another namespace.
type SessionStore[M any] struct{ state *accountSessionState[M] }
type accountSessionState[M any] struct {
	credentials *CredentialStore[M]
	backend     sessiondb.Backend
	database    *sessiondb.Database
	limits      sessions.Limits
}

func (*SessionStore[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.SessionStore{redacted}"))
}

// NewSessionStore requires the credential reader's SAME coordinated backend.
// It performs no I/O. The independent sessions/database migrations and account
// domain must already be installed before use; no legacy table is adopted.
func NewSessionStore[M any](credentials *CredentialStore[M], config sessiondb.Config) (*SessionStore[M], error) {
	if credentials == nil || credentials.state == nil {
		return nil, accountSessionFailure(sessions.CodeInvalidConfig, nil)
	}
	backend, ok := credentials.state.backend.(sessiondb.Backend)
	if !ok || nilModelValue(backend) {
		return nil, accountSessionFailure(sessions.CodeInvalidConfig, nil)
	}
	database, err := sessiondb.New(backend, config)
	if err != nil {
		return nil, err
	}
	limits, err := sessionwire.NormalizeLimits(config.Limits)
	if err != nil {
		return nil, err
	}
	return &SessionStore[M]{state: &accountSessionState[M]{credentials, backend, database, limits}}, nil
}

func (store *SessionStore[M]) valid(ctx context.Context) error {
	if store == nil || store.state == nil {
		return accountSessionFailure(sessions.CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) {
		return accountSessionFailure(sessions.CodeInvalidInput, nil)
	}
	return ctx.Err()
}
func validateAccountSessionScope(ctx context.Context, scope db.Queryer) error {
	if nilModelValue(ctx) || nilModelValue(scope) {
		return accountSessionFailure(sessions.CodeInvalidInput, nil)
	}
	validator, borrowed := scope.(db.SessionValidator)
	if !borrowed || nilModelValue(validator) {
		return accountSessionFailure(sessions.CodeInvalidConfig, nil)
	}
	switch scope.(type) {
	case db.Atomic, db.CoordinatedAtomic, db.RelationAtomic, db.CoordinatedRelationAtomic, db.SnapshotReader:
		return accountSessionFailure(sessions.CodeInvalidConfig, nil)
	}
	return errors.Join(ctx.Err(), validator.ValidateSession(ctx))
}
func (store *SessionStore[M]) namespaceIn(ctx context.Context, scope db.Queryer) (*sessiondb.Store, error) {
	if err := validateAccountSessionScope(ctx, scope); err != nil {
		return nil, err
	}
	binding := store.state.credentials.state.binding
	ownership, err := binding.RequireDomainIn(ctx, scope)
	if err != nil {
		return nil, err
	}
	domain, err := binding.CredentialDomain(ownership.Generation())
	if err != nil {
		return nil, err
	}
	return store.state.database.Store(domain.Identifier())
}

type accountSessionRecord struct {
	record  sessions.Record
	present bool
}
type accountSessionAccess struct {
	record sessions.Record
	status sessions.AccessStatus
}

func (store *SessionStore[M]) Load(ctx context.Context, id sessions.ID) (sessions.Record, bool, error) {
	if err := store.valid(ctx); err != nil {
		return sessions.Record{}, false, err
	}
	if !id.Valid() {
		return sessions.Record{}, false, accountSessionFailure(sessions.CodeInvalidInput, nil)
	}
	result, err := db.ReadSnapshotResult(ctx, store.state.backend, func(work context.Context, scope db.Queryer) (accountSessionRecord, error) {
		bound, err := store.namespaceIn(work, scope)
		if err != nil {
			return accountSessionRecord{}, err
		}
		record, present, err := bound.LoadIn(work, scope, id)
		return accountSessionRecord{record, present}, err
	})
	if err != nil {
		return sessions.Record{}, false, accountSessionFailure(sessions.CodeStoreFailure, err)
	}
	return result.record, result.present, nil
}

func (store *SessionStore[M]) Create(ctx context.Context, record sessions.Record) (bool, error) {
	if err := store.validRecord(ctx, record); err != nil {
		return false, err
	}
	return accountSessionWrite(ctx, store, func(work context.Context, _ db.Session, scoped *sessiondb.Store) (bool, error) {
		return scoped.Create(work, record)
	})
}
func (store *SessionStore[M]) Access(ctx context.Context, id sessions.ID, policy sessions.AccessPolicy) (sessions.Record, sessions.AccessStatus, error) {
	if err := store.valid(ctx); err != nil {
		return sessions.Record{}, sessions.AccessMissing, err
	}
	if !id.Valid() {
		return sessions.Record{}, sessions.AccessMissing, accountSessionFailure(sessions.CodeInvalidInput, nil)
	}
	result, err := accountSessionWrite(ctx, store, func(work context.Context, _ db.Session, scoped *sessiondb.Store) (accountSessionAccess, error) {
		record, status, err := scoped.Access(work, id, policy)
		return accountSessionAccess{record, status}, err
	})
	if err != nil {
		return sessions.Record{}, sessions.AccessMissing, err
	}
	return result.record, result.status, nil
}
func (store *SessionStore[M]) Rotate(ctx context.Context, id sessions.ID, record sessions.Record) (sessions.Record, bool, error) {
	return store.replace(ctx, id, record, false)
}
func (store *SessionStore[M]) Replace(ctx context.Context, id sessions.ID, record sessions.Record) (sessions.Record, bool, error) {
	return store.replace(ctx, id, record, true)
}
func (store *SessionStore[M]) replace(ctx context.Context, id sessions.ID, record sessions.Record, fresh bool) (sessions.Record, bool, error) {
	if err := store.validRecord(ctx, record); err != nil {
		return sessions.Record{}, false, err
	}
	if !id.Valid() || id == record.ID() {
		return sessions.Record{}, false, accountSessionFailure(sessions.CodeInvalidInput, nil)
	}
	result, err := accountSessionWrite(ctx, store, func(work context.Context, _ db.Session, scoped *sessiondb.Store) (accountSessionRecord, error) {
		var next sessions.Record
		var present bool
		var err error
		if fresh {
			next, present, err = scoped.Replace(work, id, record)
		} else {
			next, present, err = scoped.Rotate(work, id, record)
		}
		return accountSessionRecord{next, present}, err
	})
	if err != nil {
		return sessions.Record{}, false, err
	}
	return result.record, result.present, nil
}
func (store *SessionStore[M]) Delete(ctx context.Context, id sessions.ID) error {
	if err := store.valid(ctx); err != nil {
		return err
	}
	if !id.Valid() {
		return accountSessionFailure(sessions.CodeInvalidInput, nil)
	}
	_, err := accountSessionWrite(ctx, store, func(work context.Context, _ db.Session, scoped *sessiondb.Store) (struct{}, error) {
		return struct{}{}, scoped.Delete(work, id)
	})
	return err
}

// RevokeIn is a privileged persistence capability, not an authorization API.
// The caller first authorizes the account change and couples it to this exact
// coordinated scope. Only the current domain's matching principal is affected.
// A returned count remains provisional until the enclosing commit succeeds.
func (store *SessionStore[M]) RevokeIn(ctx context.Context, scope db.Session, principalID string, keep sessions.ID) (int, error) {
	if err := store.valid(ctx); err != nil {
		return 0, err
	}
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil {
		return 0, err
	}
	bound, err := store.namespaceIn(ctx, scope)
	if err != nil {
		return 0, err
	}
	scoped, err := bound.InSession(ctx, scope)
	if err != nil {
		return 0, err
	}
	return scoped.DeleteMatching(ctx, auth.SessionPrincipalIDKey, principalID, keep)
}
func (store *SessionStore[M]) validRecord(ctx context.Context, record sessions.Record) error {
	if err := store.valid(ctx); err != nil {
		return err
	}
	_, err := sessionwire.Encode(record, store.state.limits)
	return err
}

// A private pointer records the precise callback error without comparing host
// error values (which can contain slices). Only exact confirmed rollback may
// release that error; uncertainty/cleanup always retains a storage failure.
type accountSessionRejection struct{ cause error }

func (*accountSessionRejection) Error() string           { return "account session operation rejected" }
func (rejection *accountSessionRejection) Unwrap() error { return rejection.cause }
func accountSessionWrite[M, T any](ctx context.Context, store *SessionStore[M], write func(context.Context, db.Session, *sessiondb.Store) (T, error)) (T, error) {
	var zero T
	if err := store.valid(ctx); err != nil {
		return zero, err
	}
	var mu sync.Mutex
	var rejected *accountSessionRejection
	value, err := db.CoordinatedAtomicResult(ctx, store.state.backend, func(work context.Context, scope db.Session) (T, error) {
		bound, err := store.namespaceIn(work, scope)
		if err != nil {
			return zero, err
		}
		scoped, err := bound.InSession(work, scope)
		if err != nil {
			return zero, err
		}
		result, err := write(work, scope, scoped)
		if err != nil {
			owned := &accountSessionRejection{cause: err}
			mu.Lock()
			rejected = owned
			mu.Unlock()
			return zero, owned
		}
		return result, nil
	})
	if err == nil {
		return value, nil
	}
	mu.Lock()
	callback := rejected
	mu.Unlock()
	if !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) && !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) && callback != nil && err == callback {
		return zero, callback.cause
	}
	return zero, accountSessionFailure(sessions.CodeStoreFailure, err)
}
func accountSessionFailure(code sessions.ErrorCode, cause error) error {
	return &sessions.Error{Code: code, Field: "account", Detail: "account session persistence failed", Cause: cause}
}
