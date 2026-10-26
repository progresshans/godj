// Package database provides independently migrated, namespaced server-side
// session storage on native GoDj database scopes. It performs no authentication.
package database

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/sessionwire"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

const DefaultMaxRecords = 4096
const MaximumRecords = 1 << 16
const ownerBatchSize = 256

type Backend interface {
	db.SnapshotReader
	db.CoordinatedAtomic
}

// Config bounds the complete database inventory across every namespace.
// Expired records may be reclaimed for capacity, but active foreign namespaces
// are never victims of a namespace-specific deletion or replacement.
type Config struct {
	Limits     sessions.Limits
	MaxRecords int
}
type Database struct{ state *databaseState }
type databaseState struct {
	backend    Backend
	limits     sessions.Limits
	maxRecords int
}

func (*Database) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("sessions.database.Database{redacted}"))
}

// New only validates configuration. Apply migrations and call Inspect
// explicitly before startup; neither operation happens in this constructor.
// Transition from historical systemstate sessions is a separate host operation.
func New(backend Backend, config Config) (*Database, error) {
	if nilValue(backend) {
		return nil, failure(sessions.CodeInvalidConfig, nil)
	}
	limits, err := sessionwire.NormalizeLimits(config.Limits)
	if err != nil {
		return nil, failure(sessions.CodeInvalidConfig, err)
	}
	maximum := config.MaxRecords
	if maximum == 0 {
		maximum = DefaultMaxRecords
	}
	if maximum < 1 || maximum > MaximumRecords {
		return nil, failure(sessions.CodeInvalidConfig, nil)
	}
	return &Database{state: &databaseState{backend: backend, limits: limits, maxRecords: maximum}}, nil
}

// Store creates an immutable namespace binding without I/O or authorization.
// Account compositions derive this value from their current durable domain;
// caller-controlled request fields must not select an authorization namespace.
func (database *Database) Store(namespace string) (*Store, error) {
	if database == nil || database.state == nil || !validNamespace(namespace) {
		return nil, failure(sessions.CodeInvalidConfig, nil)
	}
	return &Store{state: &storeState{database: database.state, namespace: namespace}}, nil
}

type Store struct{ state *storeState }
type storeState struct {
	database  *databaseState
	namespace string
	scope     db.Session
	lifetime  context.Context
}

var _ sessions.Store = (*Store)(nil)

func (*Store) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("sessions.database.Store{redacted}"))
}

// InSession borrows the caller's already coordinated native write scope.
// It never opens a transaction; every result remains provisional until that
// owner commits. The caller must compose all effects in this same database and
// return errors to its owner. Retained/expired or root scopes are rejected.
func (store *Store) InSession(ctx context.Context, scope db.Session) (*Store, error) {
	if err := store.validCall(ctx); err != nil {
		return nil, err
	}
	if store.state.scope != nil {
		return nil, failure(sessions.CodeInvalidConfig, nil)
	}
	if err := validateWriteScope(ctx, scope); err != nil {
		return nil, err
	}
	state := *store.state
	state.scope, state.lifetime = scope, ctx
	return &Store{state: &state}, nil
}

func (store *Store) validCall(ctx context.Context) error {
	if store == nil || store.state == nil || store.state.database == nil {
		return failure(sessions.CodeInvalidConfig, nil)
	}
	if nilValue(ctx) {
		return failure(sessions.CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return failure(sessions.CodeInvalidInput, err)
	}
	if store.state.scope != nil {
		if err := store.state.lifetime.Err(); err != nil {
			return failure(sessions.CodeStoreFailure, err)
		}
		return validateWriteScope(ctx, store.state.scope)
	}
	return nil
}

func validateWriteScope(ctx context.Context, scope db.Session) error {
	if nilValue(scope) {
		return failure(sessions.CodeInvalidInput, nil)
	}
	validator, valid := scope.(db.SessionValidator)
	_, atomic := scope.(db.Atomic)
	_, coordinated := scope.(db.CoordinatedAtomic)
	_, reader := scope.(db.SnapshotReader)
	if !valid || nilValue(validator) || atomic || coordinated || reader {
		return failure(sessions.CodeInvalidConfig, nil)
	}
	switch scope.(type) {
	case db.RelationAtomic, db.CoordinatedRelationAtomic:
		return failure(sessions.CodeInvalidConfig, nil)
	}
	if err := validator.ValidateSession(ctx); err != nil {
		return failure(sessions.CodeStoreFailure, err)
	}
	return nil
}

// readBorrowed keeps read composition on the caller's exact native scope. A
// read-only snapshot is never promoted to a session or used to open nested I/O.
func readBorrowed[T any](ctx context.Context, store *Store, scope db.Queryer, read func(context.Context, db.Queryer) (T, error)) (T, error) {
	var zero T
	if err := store.validCall(ctx); err != nil {
		return zero, err
	}
	if store.state.scope != nil || nilValue(scope) {
		return zero, failure(sessions.CodeInvalidConfig, nil)
	}
	validator, borrowed := scope.(db.SessionValidator)
	_, atomic := scope.(db.Atomic)
	_, coordinated := scope.(db.CoordinatedAtomic)
	_, snapshot := scope.(db.SnapshotReader)
	if !borrowed || nilValue(validator) || atomic || coordinated || snapshot {
		return zero, failure(sessions.CodeInvalidConfig, nil)
	}
	switch scope.(type) {
	case db.RelationAtomic, db.CoordinatedRelationAtomic:
		return zero, failure(sessions.CodeInvalidConfig, nil)
	}
	if err := validator.ValidateSession(ctx); err != nil {
		return zero, failure(sessions.CodeStoreFailure, err)
	}
	value, err := read(ctx, scope)
	if err = errors.Join(err, ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return zero, failure(sessions.CodeStoreFailure, err)
	}
	return value, nil
}

func readStore[T any](ctx context.Context, store *Store, read func(context.Context, db.Queryer) (T, error)) (T, error) {
	var zero T
	if err := store.validCall(ctx); err != nil {
		return zero, err
	}
	if store.state.scope != nil {
		value, err := read(ctx, store.state.scope)
		if err := errors.Join(err, store.validCall(ctx)); err != nil {
			return zero, failure(sessions.CodeStoreFailure, err)
		}
		return value, nil
	}
	value, err := db.ReadSnapshotResult(ctx, store.state.database.backend, read)
	if err != nil {
		return zero, failure(sessions.CodeStoreFailure, err)
	}
	return value, nil
}

func writeStore[T any](ctx context.Context, store *Store, write func(context.Context, db.Session) (T, error)) (T, error) {
	var zero T
	if err := store.validCall(ctx); err != nil {
		return zero, err
	}
	if store.state.scope != nil {
		value, err := write(ctx, store.state.scope)
		completion := store.validCall(ctx)
		if completion != nil {
			return zero, failure(sessions.CodeStoreFailure, errors.Join(err, completion))
		}
		if rejected, owned := err.(*accessRejection); owned {
			return zero, rejected.cause
		}
		if err != nil {
			return zero, err
		}
		return value, nil
	}
	var mu sync.Mutex
	var callback error
	value, err := db.CoordinatedAtomicResult(ctx, store.state.database.backend, func(work context.Context, scope db.Session) (result T, failure error) {
		defer func() { mu.Lock(); callback = failure; mu.Unlock() }()
		if err := validateWriteScope(work, scope); err != nil {
			return zero, err
		}
		return write(work, scope)
	})
	if err == nil {
		return value, nil
	}
	mu.Lock()
	observed := callback
	mu.Unlock()
	// Only a direct confirmed refusal can invite fresh entropy. An uncertain
	// outcome or cleanup error containing that refusal must retain its cause.
	if !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) && !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
		if own, valid := observed.(*accessRejection); valid && own != nil && err == own {
			return zero, own.cause
		}
		if own, valid := observed.(*sessions.Error); valid && own != nil && own.Cause == nil && err == own {
			return zero, own
		}
	}
	return zero, failure(sessions.CodeStoreFailure, err)
}

// The only producer is AccessPolicy.Apply, before mutation. The transaction
// owner may release its original classification only after exact rollback.
type accessRejection struct{ cause error }

func (*accessRejection) Error() string           { return "session access policy rejected" }
func (rejection *accessRejection) Unwrap() error { return rejection.cause }

func validNamespace(namespace string) bool {
	return namespace != "" && len(namespace) <= 128 && utf8.ValidString(namespace) && !strings.ContainsRune(namespace, 0)
}
func nilValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
func failure(code sessions.ErrorCode, cause error) error {
	return &sessions.Error{Code: code, Field: "database", Detail: "session database operation failed", Cause: cause}
}
