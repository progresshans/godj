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

// Backend is the complete current boundary needed to provision, verify, and
// operate one explicitly migrated framework system schema. ProvisionOperator
// and OpenExisting never invoke migration or schema-editor APIs through this
// interface.
type Backend interface {
	db.Queryer
	db.Mutator
	db.CoordinatedAtomic
	migrationbackend.AppliedMigrationReader
}

// Runtime owns the process-local contention gate for one database-coordinated
// system-state domain and the restart-verified immutable credential
// authenticator.
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

// ProvisionOperator creates the sole durable operator only from an exact,
// explicitly migrated, clean system state. Password hashing happens at most
// once after a preliminary empty observation and before the one authoritative
// coordinated transaction. Existing state is never verified with the supplied
// password, mutated, retried, adopted, or repaired.
func ProvisionOperator(ctx context.Context, backend Backend, config ProvisionOperatorConfig) (resultErr error) {
	defer func() {
		resultErr = redactOperatorFailure(resultErr)
	}()

	if err := validateSystemStateCall(ctx, backend); err != nil {
		return err
	}
	material, err := validateProvisionOperatorConfig(config)
	if err != nil {
		return err
	}
	if err := requireInitialMigration(ctx, backend); err != nil {
		return err
	}

	// This read only decides whether hashing may be necessary. The coordinated
	// callback below owns the authoritative readiness/cardinality decision.
	preliminaryCredentials, err := readCredentialRows(ctx, backend)
	if err != nil {
		return err
	}
	var candidate credentialRow
	candidateReady := false
	if len(preliminaryCredentials) == 0 {
		candidate, err = material.encodedRow(ctx)
		if err != nil {
			return err
		}
		candidateReady = true
	}

	var durable credentialRow
	inserted := false
	var callbackFailure error
	var callbackState operatorErrorSnapshot
	err = backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		callbackFailure = func() error {
			sessionPresent, err := inspectProvisionSessionTable(ctx, session)
			if err != nil {
				return err
			}
			auditPresent, err := inspectProvisionAuditTable(ctx, session)
			if err != nil {
				return err
			}
			credentials, err := readCredentialRows(ctx, session)
			if err != nil {
				return err
			}
			switch len(credentials) {
			case 0:
				if sessionPresent || auditPresent {
					return &Error{
						Code:   CodeCorruptState,
						Field:  "credential",
						Detail: "dependent system rows exist without the sole credential",
					}
				}
				if !candidateReady {
					return &Error{
						Code:   CodeCorruptState,
						Field:  "credential",
						Detail: "credential state changed before coordinated provisioning",
					}
				}
				durable, err = insertCredential(ctx, session, candidate)
				inserted = err == nil
				return err
			case 1:
				durable = credentials[0]
				return nil
			default:
				return credentialCardinalityError()
			}
		}()
		callbackState = snapshotOperatorError(callbackFailure)
		return callbackFailure
	})
	if err != nil {
		return redactOperatorAtomicFailure(err, callbackState)
	}
	if inserted {
		return nil
	}
	if _, err := validateStoredCredential(durable, material.policy); err != nil {
		return err
	}
	return &Error{
		Code:   CodeCredentialAlreadyExists,
		Field:  "credential",
		Detail: "the durable operator credential already exists",
	}
}

// OpenExisting opens an already-provisioned runtime after one coordinated,
// read-only inspection of the exact migrated schema and durable state. It
// never inserts, updates, deletes, or verifies a raw password.
func OpenExisting(ctx context.Context, backend Backend, config RuntimeConfig) (result *Runtime, resultErr error) {
	defer func() {
		resultErr = redactOperatorFailure(resultErr)
		if resultErr != nil {
			result = nil
		}
	}()

	if err := validateSystemStateCall(ctx, backend); err != nil {
		return nil, err
	}
	policy, err := validateCredentialPolicy(config.CredentialPolicy)
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

	var durable credentialRow
	absent := false
	var callbackFailure error
	var callbackState operatorErrorSnapshot
	err = runtime.withAtomic(ctx, func(session db.Session) error {
		callbackFailure = func() error {
			sessionPresent, err := inspectSessionTable(
				ctx,
				session,
				sessionStore.limits,
				sessionStore.maxRecords,
			)
			if err != nil {
				return err
			}
			auditPresent, err := inspectAuditTable(ctx, session, auditCapacity)
			if err != nil {
				return err
			}
			credentials, err := readCredentialRows(ctx, session)
			if err != nil {
				return err
			}
			switch len(credentials) {
			case 0:
				if sessionPresent || auditPresent {
					return &Error{
						Code:   CodeCorruptState,
						Field:  "credential",
						Detail: "dependent system rows exist without the sole credential",
					}
				}
				absent = true
				return nil
			case 1:
				durable = credentials[0]
				return nil
			default:
				return credentialCardinalityError()
			}
		}()
		callbackState = snapshotOperatorError(callbackFailure)
		return callbackFailure
	})
	if err != nil {
		return nil, redactOperatorAtomicFailure(err, callbackState)
	}
	if absent {
		return nil, &Error{
			Code:   CodeCredentialAbsent,
			Field:  "credential",
			Detail: "the migrated system state has no operator credential",
		}
	}
	credential, err := validateStoredCredential(durable, policy)
	if err != nil {
		return nil, err
	}
	authenticator, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, policy.passwordHasher)
	if err != nil {
		return nil, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "credential authenticator could not be initialized",
			Cause:  err,
		}
	}
	runtime.authenticator = authenticator
	runtime.sessionStore = sessionStore
	runtime.auditCapacity = auditCapacity
	return runtime, nil
}

func validateSystemStateCall(ctx context.Context, backend Backend) error {
	if ctx == nil {
		return &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if isNilInterface(backend) {
		return &Error{Code: CodeInvalidConfig, Field: "backend", Detail: "backend is nil"}
	}
	return nil
}

// Provisioning does not own the runtime session/audit deployment limits, but
// it still requires both exact table query surfaces and must distinguish a
// clean empty state from dependent rows without a credential.
func inspectProvisionSessionTable(ctx context.Context, queryer db.Queryer) (bool, error) {
	plan, err := query.NewPlan(
		sessionTableName,
		[]query.FieldRef{systemRowIDField, sessionDigestField, sessionPayloadField},
	).WithLimit(1)
	if err != nil {
		return false, &Error{Code: CodeSchemaUnavailable, Field: sessionTableName, Detail: "required session table is unavailable", Cause: err}
	}
	result, err := queryer.Query(ctx, plan)
	if err != nil {
		if !isNilInterface(result) {
			_ = result.Close()
		}
		return false, &Error{Code: CodeSchemaUnavailable, Field: sessionTableName, Detail: "required session table is unavailable", Cause: err}
	}
	if isNilInterface(result) {
		return false, &Error{Code: CodeSchemaUnavailable, Field: sessionTableName, Detail: "required session table is unavailable"}
	}
	present := result.Next()
	if present {
		var identifier int64
		var digest, payload string
		if err := result.Scan(&identifier, &digest, &payload); err != nil {
			_ = result.Close()
			return false, &Error{Code: CodeCorruptState, Field: "session_row", Detail: "stored session row cannot be decoded", Cause: err}
		}
	}
	if err := ctx.Err(); err != nil {
		_ = result.Close()
		return false, err
	}
	iterationErr := result.Err()
	closeErr := result.Close()
	if iterationErr != nil || closeErr != nil {
		return false, &Error{
			Code:   CodeSchemaUnavailable,
			Field:  sessionTableName,
			Detail: "required session table is unavailable",
			Cause:  errors.Join(iterationErr, closeErr),
		}
	}
	return present, nil
}

func inspectProvisionAuditTable(ctx context.Context, queryer db.Queryer) (bool, error) {
	plan, err := query.NewPlan(auditTableName, auditFieldRefs).WithLimit(1)
	if err != nil {
		return false, &Error{Code: CodeSchemaUnavailable, Field: auditTableName, Detail: "required audit table is unavailable", Cause: err}
	}
	result, err := queryer.Query(ctx, plan)
	if err != nil {
		if !isNilInterface(result) {
			_ = result.Close()
		}
		return false, &Error{Code: CodeSchemaUnavailable, Field: auditTableName, Detail: "required audit table is unavailable", Cause: err}
	}
	if isNilInterface(result) {
		return false, &Error{Code: CodeSchemaUnavailable, Field: auditTableName, Detail: "required audit table is unavailable"}
	}
	present := result.Next()
	if present {
		var identifier int64
		var actorID, model, objectID, action, changedFields, displayLabel string
		if err := result.Scan(
			&identifier,
			&actorID,
			&model,
			&objectID,
			&action,
			&changedFields,
			&displayLabel,
		); err != nil {
			_ = result.Close()
			return false, &Error{Code: CodeCorruptState, Field: "audit_row", Detail: "stored audit row cannot be decoded", Cause: err}
		}
	}
	if err := ctx.Err(); err != nil {
		_ = result.Close()
		return false, err
	}
	iterationErr := result.Err()
	closeErr := result.Close()
	if iterationErr != nil || closeErr != nil {
		return false, &Error{
			Code:   CodeSchemaUnavailable,
			Field:  auditTableName,
			Detail: "required audit table is unavailable",
			Cause:  errors.Join(iterationErr, closeErr),
		}
	}
	return present, nil
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

// Query validates and forwards a top-level read-only plan without acquiring
// the coordination gate. Code already inside Atomic must instead use the
// borrowed Session.Query so it stays on that transaction's pinned connection.
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

// Atomic runs the caller callback under the same database coordination fence
// used by session and audit state. Callbacks must use the borrowed Session and
// must not invoke Runtime.Atomic, a Runtime session-store operation, or another
// backend coordination domain recursively.
func (runtime *Runtime) Atomic(ctx context.Context, callback func(db.Session) error) error {
	return runtime.withAtomic(ctx, callback)
}

// withAtomic is the one cooperative gate shared by the durable session and
// audit adapters. The mutex reduces contention within this Runtime; the
// backend's database/schema fence owns correctness across Runtime instances.
// Every valid call invokes Backend.CoordinatedAtomic exactly once.
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
	return runtime.backend.CoordinatedAtomic(ctx, callback)
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
	return redactOperatorFailureWithDetail(err, "operator credential transaction failed", false)
}

type operatorErrorSnapshot struct {
	source *Error
	code   ErrorCode
	field  string
	detail string
}

func snapshotOperatorError(err error) operatorErrorSnapshot {
	stateError, ok := err.(*Error)
	if !ok || stateError == nil {
		return operatorErrorSnapshot{}
	}
	return operatorErrorSnapshot{
		source: stateError,
		code:   stateError.Code,
		field:  stateError.Field,
		detail: stateError.Detail,
	}
}

func redactOperatorAtomicFailure(err error, callbackState operatorErrorSnapshot) error {
	if err == nil {
		return nil
	}
	if isNilInterface(err) {
		return &Error{
			Code:   CodePersistence,
			Field:  "credential",
			Detail: "operator credential transaction failed",
		}
	}
	if outcomeCode := operatorOutcomeUnknownCode(err); outcomeCode != "" {
		return operatorOutcomeUnknownError(outcomeCode)
	}
	if safeOperatorErrorIs(err, context.Canceled) {
		return context.Canceled
	}
	if safeOperatorErrorIs(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if callbackState.source != nil && operatorErrorChainContainsState(err, callbackState.source) {
		return &Error{Code: callbackState.code, Field: callbackState.field, Detail: callbackState.detail}
	}
	return &Error{
		Code:   CodePersistence,
		Field:  "credential",
		Detail: "operator credential transaction failed",
	}
}

func redactOperatorFailure(err error) error {
	return redactOperatorFailureWithDetail(err, "operator system-state operation failed", true)
}

func redactOperatorFailureWithDetail(err error, fallbackDetail string, preserveStateIdentity bool) error {
	if err == nil {
		return nil
	}
	if isNilInterface(err) {
		return &Error{
			Code:   CodePersistence,
			Field:  "credential",
			Detail: fallbackDetail,
		}
	}
	if outcomeCode := operatorOutcomeUnknownCode(err); outcomeCode != "" {
		return operatorOutcomeUnknownError(outcomeCode)
	}
	if safeOperatorErrorIs(err, context.Canceled) {
		return context.Canceled
	}
	if safeOperatorErrorIs(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if stateError, ok := err.(*Error); preserveStateIdentity && ok && stateError != nil {
		return &Error{Code: stateError.Code, Field: stateError.Field, Detail: stateError.Detail}
	}
	return &Error{
		Code:   CodePersistence,
		Field:  "credential",
		Detail: fallbackDetail,
	}
}

func operatorOutcomeUnknownError(code string) error {
	return &Error{
		Code:   CodePersistence,
		Field:  "credential",
		Detail: "operator credential transaction outcome is unknown; reconciliation is required",
		Cause: &query.Error{
			Category: query.CategoryBackend,
			Code:     code,
		},
	}
}

func operatorOutcomeUnknownCode(err error) string {
	for _, code := range []string{
		query.CodeCommitOutcomeUnknown,
		query.CodeTransactionOutcomeUnknown,
	} {
		if safeOperatorErrorIs(err, &query.Error{Code: code}) {
			return code
		}
	}
	return ""
}

func safeOperatorErrorIs(err, target error) (matched bool) {
	if isNilInterface(err) {
		return false
	}
	defer func() {
		if recover() != nil {
			matched = false
		}
	}()
	return errors.Is(err, target)
}

func operatorErrorChainContainsState(err error, target *Error) (found bool) {
	defer func() {
		if recover() != nil {
			found = false
		}
	}()
	pending := []error{err}
	for inspected := 0; len(pending) > 0 && inspected < 64; inspected++ {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if isNilInterface(current) {
			continue
		}
		if stateError, ok := current.(*Error); ok && stateError == target {
			return true
		}
		switch wrapped := current.(type) {
		case interface{ Unwrap() []error }:
			pending = append(pending, wrapped.Unwrap()...)
		case interface{ Unwrap() error }:
			pending = append(pending, wrapped.Unwrap())
		}
	}
	return false
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
