package systemstate

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

var (
	auditIDRef      = query.NewFieldRef("id", "id", query.FieldInteger, false)
	auditActorIDRef = query.NewFieldRef(
		auditActorIDColumn,
		auditActorIDColumn,
		query.FieldString,
		false,
	)
	auditModelRef = query.NewFieldRef(
		auditModelColumn,
		auditModelColumn,
		query.FieldString,
		false,
	)
	auditObjectIDRef = query.NewFieldRef(
		auditObjectIDColumn,
		auditObjectIDColumn,
		query.FieldString,
		false,
	)
	auditActionRef = query.NewFieldRef(
		auditActionColumn,
		auditActionColumn,
		query.FieldString,
		false,
	)
	auditChangedFieldsRef = query.NewFieldRef(
		auditChangedFieldsColumn,
		auditChangedFieldsColumn,
		query.FieldString,
		false,
	)
	auditDisplayLabelRef = query.NewFieldRef(
		auditDisplayLabelColumn,
		auditDisplayLabelColumn,
		query.FieldString,
		false,
	)
	auditFieldRefs = []query.FieldRef{
		auditIDRef,
		auditActorIDRef,
		auditModelRef,
		auditObjectIDRef,
		auditActionRef,
		auditChangedFieldsRef,
		auditDisplayLabelRef,
	}
)

// Backend is the complete current boundary needed to verify and operate one
// explicitly migrated framework system schema. Open never invokes migration
// or schema-editor APIs through this interface.
type Backend interface {
	db.Queryer
	db.Mutator
	db.Atomic
	migrationbackend.AppliedMigrationReader
}

// Runtime owns the cooperative single-runtime transaction gate and the
// restart-verified immutable credential authenticator.
type Runtime struct {
	mu            sync.Mutex
	backend       Backend
	authenticator *auth.MemoryAuthenticator
	sessionStore  *durableSessionStore
	auditCapacity int
}

var _ db.Queryer = (*Runtime)(nil)
var _ db.Mutator = (*Runtime)(nil)
var _ db.Atomic = (*Runtime)(nil)

func (*Runtime) String() string   { return "systemstate.Runtime{redacted}" }
func (*Runtime) GoString() string { return "systemstate.Runtime{redacted}" }

// Open verifies the exact applied system migration and all required table
// query surfaces, then bootstraps only an empty credential table or validates
// the existing durable credential. It never creates, adopts, or repairs a
// schema.
func Open(ctx context.Context, backend Backend, config BootstrapConfig) (*Runtime, error) {
	if ctx == nil {
		return nil, &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if isNilInterface(backend) {
		return nil, &Error{Code: CodeInvalidConfig, Field: "backend", Detail: "backend is nil"}
	}
	material, err := validateBootstrapConfig(config)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{backend: backend}
	sessionStore, err := newDurableSessionStore(runtime, config.SessionLimits, config.MaxSessions)
	if err != nil {
		return nil, err
	}
	auditCapacity, err := normalizeAuditCapacity(config.AuditCapacity)
	if err != nil {
		return nil, err
	}
	if err := requireInitialMigration(ctx, backend); err != nil {
		return nil, err
	}
	sessionPresent, err := inspectSessionTable(
		ctx,
		backend,
		sessionStore.limits,
		sessionStore.maxRecords,
	)
	if err != nil {
		return nil, err
	}
	auditPresent, err := inspectAuditTable(ctx, backend, auditCapacity)
	if err != nil {
		return nil, err
	}
	credentials, err := readCredentialRows(ctx, backend)
	if err != nil {
		return nil, err
	}
	if len(credentials) > 1 {
		return nil, credentialCardinalityError()
	}

	var durable credentialRow
	if len(credentials) == 1 {
		durable = credentials[0]
	} else {
		if sessionPresent || auditPresent {
			return nil, &Error{
				Code:   CodeCorruptState,
				Field:  "credential",
				Detail: "dependent system rows exist without the sole credential",
			}
		}
		candidate, err := material.encodedRow(ctx)
		if err != nil {
			return nil, err
		}
		err = runtime.withAtomic(ctx, func(session db.Session) error {
			current, err := readCredentialRows(ctx, session)
			if err != nil {
				return err
			}
			switch len(current) {
			case 0:
				durable, err = insertCredential(ctx, session, candidate)
				return err
			case 1:
				durable = current[0]
				return nil
			default:
				return credentialCardinalityError()
			}
		})
		if err != nil {
			return nil, redactAtomicFailure(err)
		}
	}

	authenticator, err := verifyCredential(ctx, durable, material)
	if err != nil {
		return nil, err
	}
	runtime.authenticator = authenticator
	runtime.sessionStore = sessionStore
	runtime.auditCapacity = auditCapacity
	return runtime, nil
}

// Authenticator returns the restart-verified immutable credential boundary.
// A nil Runtime returns nil.
func (runtime *Runtime) Authenticator() auth.CredentialAuthenticator {
	if runtime == nil {
		return nil
	}
	return runtime.authenticator
}

// SessionStore returns the durable Store sharing this Runtime's transaction
// gate. A nil or incompletely initialized Runtime returns nil.
func (runtime *Runtime) SessionStore() sessions.Store {
	if runtime == nil || runtime.sessionStore == nil {
		return nil
	}
	return runtime.sessionStore
}

// Query validates and forwards a read-only plan. Reads do not acquire the
// cooperative write mutex and therefore cannot accidentally nest a database
// transaction owned by an Article or Admin operation.
func (runtime *Runtime) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := runtime.validBackendCall(ctx); err != nil {
		return nil, err
	}
	return runtime.backend.Query(ctx, plan)
}

// Insert publishes one top-level mutation through exactly one gated database
// transaction.
func (runtime *Runtime) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	var identifier int64
	err := runtime.withAtomic(ctx, func(session db.Session) error {
		var err error
		identifier, err = session.Insert(ctx, plan)
		return err
	})
	return identifier, err
}

// Update publishes one top-level mutation through exactly one gated database
// transaction.
func (runtime *Runtime) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	var affected int64
	err := runtime.withAtomic(ctx, func(session db.Session) error {
		var err error
		affected, err = session.Update(ctx, plan)
		return err
	})
	return affected, err
}

// Delete publishes one top-level mutation through exactly one gated database
// transaction.
func (runtime *Runtime) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	var affected int64
	err := runtime.withAtomic(ctx, func(session db.Session) error {
		var err error
		affected, err = session.Delete(ctx, plan)
		return err
	})
	return affected, err
}

// Atomic runs the caller callback under the same single-runtime gate used by
// session and audit state.
func (runtime *Runtime) Atomic(ctx context.Context, callback func(db.Session) error) error {
	return runtime.withAtomic(ctx, callback)
}

// withAtomic is the one cooperative write gate shared by the durable session
// and audit adapters. It serializes the complete backend transaction and
// invokes Backend.Atomic exactly once for every valid call.
func (runtime *Runtime) withAtomic(ctx context.Context, callback func(db.Session) error) error {
	if err := runtime.validBackendCall(ctx); err != nil {
		return err
	}
	if callback == nil {
		return &Error{Code: CodeInvalidInput, Field: "callback", Detail: "atomic callback is nil"}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return runtime.backend.Atomic(ctx, callback)
}

func (runtime *Runtime) validBackendCall(ctx context.Context) error {
	if ctx == nil {
		return &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime == nil || isNilInterface(runtime.backend) {
		return &Error{Code: CodeInvalidConfig, Field: "runtime", Detail: "runtime is nil or uninitialized"}
	}
	return nil
}

func requireInitialMigration(ctx context.Context, reader migrationbackend.AppliedMigrationReader) error {
	records, err := reader.ReadAppliedMigrations(ctx)
	if err != nil {
		return &Error{
			Code:   CodeSchemaUnavailable,
			Field:  "migration_history",
			Detail: "system migration history is unavailable",
			Cause:  err,
		}
	}
	want := InitialMigrationKey()
	matching := 0
	systemRecords := 0
	for _, record := range records {
		if record.App != want.App {
			continue
		}
		systemRecords++
		if record.Name == want.Name {
			matching++
		}
	}
	if matching != 1 || systemRecords != 1 {
		return &Error{
			Code:   CodeSchemaUnavailable,
			Field:  "migration_history",
			Detail: "exact initial system migration is not applied",
		}
	}
	return nil
}

func normalizeAuditCapacity(capacity int) (int, error) {
	if capacity == 0 {
		capacity = admin.DefaultAuditCapacity
	}
	if capacity < 1 || capacity > admin.MaximumAuditCapacity {
		return 0, &Error{
			Code:   CodeInvalidConfig,
			Field:  "audit_capacity",
			Detail: "durable audit capacity is outside the current profile",
		}
	}
	return capacity, nil
}

func credentialCardinalityError() error {
	return &Error{
		Code:   CodeCardinality,
		Field:  "credential",
		Detail: "system credential table must contain zero or one row",
	}
}

func redactAtomicFailure(err error) error {
	if err == nil {
		return err
	}
	var outcome *query.Error
	if errors.As(err, &outcome) && outcome.Code == query.CodeCommitOutcomeUnknown {
		return &Error{
			Code:   CodePersistence,
			Field:  "credential",
			Detail: "bootstrap credential transaction outcome is unknown; reconciliation is required",
			Cause:  err,
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var stateError *Error
	if errors.As(err, &stateError) {
		return &Error{Code: stateError.Code, Field: stateError.Field, Detail: stateError.Detail, Cause: err}
	}
	return &Error{
		Code:   CodeSchemaUnavailable,
		Field:  "credential",
		Detail: "bootstrap credential transaction failed",
		Cause:  err,
	}
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
