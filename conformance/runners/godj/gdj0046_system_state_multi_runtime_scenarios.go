package godj

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	// Full-repository race runs execute several PBKDF2 bootstraps concurrently
	// across packages. The timeout bounds deadlock diagnosis without treating
	// expected slow cryptographic work as a coordination failure.
	gdj0046ScenarioTimeout           = 2 * time.Minute
	gdj0046ContenderWaitingProofTime = 500 * time.Millisecond
	gdj0046FenceProbeTimeout         = 250 * time.Millisecond
)

// gdj0046MultiRuntimeBackend observes the exact database fence used by the
// product while allowing a scenario to pause one callback after acquisition.
// The contender-attempt signal is emitted before entering the backend, so a
// still-closed contenderEntered channel proves the database fence, rather than
// process scheduling, kept its callback out.
type gdj0046MultiRuntimeBackend struct {
	*systemStateObservedBackend
	holder  bool
	barrier atomic.Pointer[gdj0046CallbackBarrier]
}

func (backend *gdj0046MultiRuntimeBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	barrier := backend.barrier.Load()
	if barrier == nil {
		return backend.systemStateObservedBackend.CoordinatedAtomic(ctx, callback)
	}
	if backend.holder {
		return backend.systemStateObservedBackend.CoordinatedAtomic(ctx, func(session db.Session) error {
			barrier.holderCallbackCalls.Add(1)
			barrier.holderEnteredOnce.Do(func() { close(barrier.holderEntered) })
			select {
			case <-barrier.releaseHolder:
			case <-ctx.Done():
				return ctx.Err()
			}
			return callback(session)
		})
	}

	barrier.contenderAttemptCalls.Add(1)
	barrier.contenderAttemptedOnce.Do(func() { close(barrier.contenderAttempted) })
	contenderFinished := make(chan struct{})
	waitingTimer := time.NewTimer(gdj0046ContenderWaitingProofTime)
	defer func() {
		close(contenderFinished)
		waitingTimer.Stop()
	}()
	go func() {
		select {
		case <-barrier.contenderEntered:
		case <-contenderFinished:
		case <-waitingTimer.C:
			barrier.contenderWaitingOnce.Do(func() { close(barrier.contenderWaiting) })
		}
	}()
	return backend.systemStateObservedBackend.CoordinatedAtomic(ctx, func(session db.Session) error {
		barrier.contenderCallbackCalls.Add(1)
		barrier.contenderEnteredOnce.Do(func() { close(barrier.contenderEntered) })
		return callback(session)
	})
}

type gdj0046CallbackBarrier struct {
	holderEntered      chan struct{}
	releaseHolder      chan struct{}
	contenderAttempted chan struct{}
	contenderWaiting   chan struct{}
	contenderEntered   chan struct{}

	holderEnteredOnce      sync.Once
	releaseHolderOnce      sync.Once
	contenderAttemptedOnce sync.Once
	contenderWaitingOnce   sync.Once
	contenderEnteredOnce   sync.Once

	holderCallbackCalls    atomic.Int64
	contenderAttemptCalls  atomic.Int64
	contenderCallbackCalls atomic.Int64
	probeCallbackCalls     atomic.Int64
	probeFailures          atomic.Int64
}

func newGDJ0046CallbackBarrier() *gdj0046CallbackBarrier {
	return &gdj0046CallbackBarrier{
		holderEntered:      make(chan struct{}),
		releaseHolder:      make(chan struct{}),
		contenderAttempted: make(chan struct{}),
		contenderWaiting:   make(chan struct{}),
		contenderEntered:   make(chan struct{}),
	}
}

func (barrier *gdj0046CallbackBarrier) release() {
	barrier.releaseHolderOnce.Do(func() { close(barrier.releaseHolder) })
}

func (barrier *gdj0046CallbackBarrier) callbackRetries() int64 {
	calls := barrier.holderCallbackCalls.Load() + barrier.contenderCallbackCalls.Load()
	if calls <= 2 {
		return 0
	}
	return calls - 2
}

type gdj0046BackendPair struct {
	directory string
	dsn       string
	holder    *gdj0046MultiRuntimeBackend
	contender *gdj0046MultiRuntimeBackend
	probe     *sqlite.Backend
}

func newGDJ0046BackendPair(ctx context.Context, withArticle bool) (*gdj0046BackendPair, error) {
	directory, err := os.MkdirTemp("", "godj-system-state-multi-runtime-")
	if err != nil {
		return nil, fmt.Errorf("create multi-runtime fixture directory: %w", err)
	}
	pair := &gdj0046BackendPair{
		directory: directory,
		dsn: "file:" + filepath.ToSlash(filepath.Join(directory, "system-state.sqlite3")) +
			"?mode=rwc&_busy_timeout=5000&_pragma=foreign_keys(1)",
	}
	open := func(holder bool) (*gdj0046MultiRuntimeBackend, error) {
		raw, err := sqlite.Open(ctx, pair.dsn)
		if err != nil {
			return nil, err
		}
		return &gdj0046MultiRuntimeBackend{
			systemStateObservedBackend: &systemStateObservedBackend{Backend: raw},
			holder:                     holder,
		}, nil
	}
	pair.holder, err = open(true)
	if err != nil {
		pair.cleanup()
		return nil, fmt.Errorf("open holder multi-runtime backend: %w", err)
	}
	pair.contender, err = open(false)
	if err != nil {
		pair.cleanup()
		return nil, fmt.Errorf("open contender multi-runtime backend: %w", err)
	}
	probeDSN := strings.Replace(pair.dsn, "_busy_timeout=5000", "_busy_timeout=1", 1)
	pair.probe, err = sqlite.Open(ctx, probeDSN)
	if err != nil {
		pair.cleanup()
		return nil, fmt.Errorf("open synchronous fence-probe backend: %w", err)
	}
	if _, err := systemStateMigrate(ctx, pair.holder, withArticle); err != nil {
		pair.cleanup()
		return nil, err
	}
	// Migration DML belongs to fixture setup, never to the product-operation
	// counters published by SYS-013..019.
	pair.resetDML()
	return pair, nil
}

func (pair *gdj0046BackendPair) cleanup() {
	if pair == nil {
		return
	}
	if pair.holder != nil && pair.holder.Backend != nil {
		_ = pair.holder.Backend.Close()
	}
	if pair.contender != nil && pair.contender.Backend != nil {
		_ = pair.contender.Backend.Close()
	}
	if pair.probe != nil {
		_ = pair.probe.Close()
	}
	if pair.directory != "" {
		_ = os.RemoveAll(pair.directory)
	}
}

func (pair *gdj0046BackendPair) arm() *gdj0046CallbackBarrier {
	barrier := newGDJ0046CallbackBarrier()
	pair.holder.barrier.Store(barrier)
	pair.contender.barrier.Store(barrier)
	return barrier
}

func (pair *gdj0046BackendPair) disarm() {
	pair.holder.barrier.Store(nil)
	pair.contender.barrier.Store(nil)
}

func (pair *gdj0046BackendPair) resetDML() {
	pair.holder.resetDML()
	pair.contender.resetDML()
}

type gdj0046RuntimePair struct {
	backends  *gdj0046BackendPair
	holder    *systemstate.Runtime
	contender *systemstate.Runtime
	config    systemStateConfig
}

func newGDJ0046RuntimePair(
	ctx context.Context,
	maxSessions int,
	auditCapacity int,
	withArticle bool,
) (*gdj0046RuntimePair, error) {
	backends, err := newGDJ0046BackendPair(ctx, withArticle)
	if err != nil {
		return nil, err
	}
	config := systemStateFixtureConfig(0xd1)
	config.Password = "gdj0046-multi-runtime-password"
	config.MaxSessions = maxSessions
	config.AuditCapacity = auditCapacity
	if err := systemStateProvisionOperator(ctx, backends.holder, config); err != nil {
		backends.cleanup()
		return nil, fmt.Errorf("provision multi-runtime operator: %w", err)
	}
	holder, err := systemStateOpenExisting(ctx, backends.holder, config)
	if err != nil {
		backends.cleanup()
		return nil, fmt.Errorf("open holder system-state Runtime: %w", err)
	}
	contender, err := systemStateOpenExisting(ctx, backends.contender, config)
	if err != nil {
		backends.cleanup()
		return nil, fmt.Errorf("open contender system-state Runtime: %w", err)
	}
	backends.resetDML()
	return &gdj0046RuntimePair{
		backends:  backends,
		holder:    holder,
		contender: contender,
		config:    config,
	}, nil
}

func (pair *gdj0046RuntimePair) cleanup() {
	if pair != nil {
		pair.backends.cleanup()
	}
}

func gdj0046WaitSignal(ctx context.Context, signal <-chan struct{}, operation string) error {
	timer := time.NewTimer(gdj0046ScenarioTimeout)
	defer timer.Stop()
	select {
	case <-signal:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for %s", operation)
	}
}

func gdj0046AssertBlocked(
	ctx context.Context,
	pair *gdj0046BackendPair,
	barrier *gdj0046CallbackBarrier,
) error {
	if err := gdj0046WaitSignal(ctx, barrier.contenderAttempted, "contender coordination attempt"); err != nil {
		return err
	}
	// Waiting is a positive event emitted only after the contender has remained
	// outside the callback for the same sustained window used by the separate
	// process worker. A scheduler delay alone cannot satisfy this proof.
	select {
	case <-barrier.contenderEntered:
		return errors.New("contender callback entered before holder transaction released the database fence")
	case <-ctx.Done():
		return ctx.Err()
	case <-barrier.contenderWaiting:
	}

	// Couple the asynchronous waiting event to a synchronous independent
	// connection probe. While the holder still owns BEGIN IMMEDIATE, a second
	// short-timeout acquisition must fail before its callback is invoked.
	probeCtx, cancel := context.WithTimeout(ctx, gdj0046FenceProbeTimeout)
	defer cancel()
	probeErr := pair.probe.CoordinatedAtomic(probeCtx, func(db.Session) error {
		barrier.probeCallbackCalls.Add(1)
		return nil
	})
	if probeErr == nil || barrier.probeCallbackCalls.Load() != 0 ||
		!gdj0046IsSQLiteContention(probeErr) ||
		errors.Is(probeErr, &query.Error{Code: query.CodeCommitOutcomeUnknown}) ||
		errors.Is(probeErr, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
		return fmt.Errorf(
			"held-fence synchronous probe = error %v callbacks %d",
			probeErr,
			barrier.probeCallbackCalls.Load(),
		)
	}
	barrier.probeFailures.Add(1)
	select {
	case <-barrier.contenderEntered:
		return errors.New("contender callback entered while the synchronous held-fence probe ran")
	default:
		return nil
	}
}

func gdj0046IsSQLiteContention(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr == nil {
		return false
	}
	switch sqliteErr.Code() & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}

func gdj0046WaitResult[T any](ctx context.Context, result <-chan T, operation string) (T, error) {
	var zero T
	timer := time.NewTimer(gdj0046ScenarioTimeout)
	defer timer.Stop()
	select {
	case value := <-result:
		return value, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-timer.C:
		return zero, fmt.Errorf("timed out waiting for %s", operation)
	}
}

func gdj0046QuerySingleString(
	ctx context.Context,
	backend db.Queryer,
	table string,
	field query.FieldRef,
) (string, int, error) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan, err := query.NewPlan(table, []query.FieldRef{id, field}).WithLimit(4)
	if err != nil {
		return "", 0, err
	}
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return "", 0, err
	}
	count := 0
	value := ""
	for rows.Next() {
		var ignored int64
		var current string
		if err := rows.Scan(&ignored, &current); err != nil {
			_ = rows.Close()
			return "", 0, err
		}
		count++
		if count == 1 {
			value = current
		}
	}
	return value, count, errors.Join(rows.Err(), rows.Close())
}

func systemStateCoordinatedAtomicFence(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	pair, err := newGDJ0046BackendPair(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer pair.cleanup()

	// Prove that two independent backend handles to the same database cannot
	// enter their coordinated callbacks together.
	barrier := pair.arm()
	holderResult := make(chan error, 1)
	contenderResult := make(chan error, 1)
	go func() {
		holderResult <- pair.holder.CoordinatedAtomic(ctx, func(db.Session) error { return nil })
	}()
	if err := gdj0046WaitSignal(ctx, barrier.holderEntered, "holder coordinated callback"); err != nil {
		barrier.release()
		return protocol.Observation{}, err
	}
	go func() {
		contenderResult <- pair.contender.CoordinatedAtomic(ctx, func(db.Session) error { return nil })
	}()
	blocked := gdj0046AssertBlocked(ctx, pair, barrier) == nil
	barrier.release()
	holderErr, waitErr := gdj0046WaitResult(ctx, holderResult, "holder coordinated result")
	if waitErr != nil {
		return protocol.Observation{}, waitErr
	}
	contenderErr, waitErr := gdj0046WaitResult(ctx, contenderResult, "contender coordinated result")
	if waitErr != nil {
		return protocol.Observation{}, waitErr
	}
	pair.disarm()
	if holderErr != nil || contenderErr != nil || !blocked ||
		barrier.holderCallbackCalls.Load() != 1 || barrier.contenderCallbackCalls.Load() != 1 ||
		barrier.probeFailures.Load() != 1 || barrier.probeCallbackCalls.Load() != 0 {
		return protocol.Observation{}, fmt.Errorf(
			"database fence probe failed: holder=%v contender=%v blocked=%v callbacks=%d/%d sync=%d/%d",
			holderErr,
			contenderErr,
			blocked,
			barrier.holderCallbackCalls.Load(),
			barrier.contenderCallbackCalls.Load(),
			barrier.probeFailures.Load(),
			barrier.probeCallbackCalls.Load(),
		)
	}

	// A canceled acquire is exercised against a held real SQLite writer fence.
	cancelBarrier := pair.arm()
	cancelHolder := make(chan error, 1)
	go func() {
		cancelHolder <- pair.holder.CoordinatedAtomic(ctx, func(db.Session) error { return nil })
	}()
	if err := gdj0046WaitSignal(ctx, cancelBarrier.holderEntered, "cancellation holder callback"); err != nil {
		cancelBarrier.release()
		return protocol.Observation{}, err
	}
	acquireCtx, cancelAcquire := context.WithCancel(ctx)
	var acquireCancelledCalls atomic.Int64
	acquireCancelledResult := make(chan error, 1)
	go func() {
		acquireCancelledResult <- pair.contender.CoordinatedAtomic(acquireCtx, func(db.Session) error {
			acquireCancelledCalls.Add(1)
			return nil
		})
	}()
	if err := gdj0046WaitSignal(ctx, cancelBarrier.contenderAttempted, "cancelled contender attempt"); err != nil {
		cancelAcquire()
		cancelBarrier.release()
		return protocol.Observation{}, err
	}
	cancelAcquire()
	acquireCancelledErr, waitErr := gdj0046WaitResult(ctx, acquireCancelledResult, "cancelled contender result")
	cancelBarrier.release()
	cancelHolderErr, holderWaitErr := gdj0046WaitResult(ctx, cancelHolder, "cancellation holder result")
	pair.disarm()
	if waitErr != nil || holderWaitErr != nil {
		return protocol.Observation{}, errors.Join(waitErr, holderWaitErr)
	}
	if cancelHolderErr != nil || !errors.Is(acquireCancelledErr, context.Canceled) || acquireCancelledCalls.Load() != 0 {
		return protocol.Observation{}, fmt.Errorf(
			"cancelled acquire = holder %v contender %v callbacks %d, want nil/context cancellation/0",
			cancelHolderErr,
			acquireCancelledErr,
			acquireCancelledCalls.Load(),
		)
	}

	// A callback error and a callback-boundary cancellation both execute a
	// mutation in the real transaction, then prove that it was rolled back.
	coordinationSecret := "gdj0046-coordination-secret-canary"
	probeID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	probeValue := query.NewFieldRef("value", "value", query.FieldString, false)
	if _, err := pair.holder.Backend.ExecContext(ctx,
		`CREATE TABLE "godj_gdj0046_fence_probe" ("id" INTEGER NOT NULL PRIMARY KEY, "value" TEXT NOT NULL)`,
	); err != nil {
		return protocol.Observation{}, err
	}
	insertProbe := func(identifier int64, value string) query.InsertPlan {
		return query.NewInsertPlan("godj_gdj0046_fence_probe", []query.Assignment{
			query.NewAssignment(probeID, query.Integer(identifier)),
			query.NewAssignment(probeValue, query.String(value)),
		})
	}
	callbackFailure := errors.New("gdj0046 callback rollback marker")
	callbackCalls := int64(0)
	callbackErr := pair.holder.CoordinatedAtomic(ctx, func(session db.Session) error {
		callbackCalls++
		if _, err := session.Insert(ctx, insertProbe(1, coordinationSecret)); err != nil {
			return err
		}
		return callbackFailure
	})
	if !errors.Is(callbackErr, callbackFailure) || callbackCalls != 1 {
		return protocol.Observation{}, fmt.Errorf("callback rollback probe = calls %d error %v", callbackCalls, callbackErr)
	}

	callbackCtx, cancelCallback := context.WithCancel(ctx)
	callbackCancellationCalls := int64(0)
	callbackCancellationErr := pair.holder.CoordinatedAtomic(callbackCtx, func(session db.Session) error {
		callbackCancellationCalls++
		if _, err := session.Insert(callbackCtx, insertProbe(2, coordinationSecret)); err != nil {
			return err
		}
		cancelCallback()
		return callbackCtx.Err()
	})
	cancelCallback()
	if !errors.Is(callbackCancellationErr, context.Canceled) || callbackCancellationCalls != 1 {
		return protocol.Observation{}, fmt.Errorf(
			"callback cancellation probe = calls %d error %v",
			callbackCancellationCalls,
			callbackCancellationErr,
		)
	}
	rolledBackRows, err := systemStateCountRows(ctx, pair.holder, "godj_gdj0046_fence_probe")
	if err != nil || rolledBackRows != 0 {
		return protocol.Observation{}, fmt.Errorf("callback rollback rows=%d: %w", rolledBackRows, err)
	}

	// A deferred foreign-key violation succeeds inside the real callback and
	// fails at SQLite's literal COMMIT boundary. This exercises the product's
	// commit-unknown classification and no-retry rule without a fault adapter.
	if _, err := pair.holder.Backend.ExecContext(ctx,
		`CREATE TABLE "godj_gdj0046_commit_parent" ("id" INTEGER NOT NULL PRIMARY KEY)`,
	); err != nil {
		return protocol.Observation{}, err
	}
	if _, err := pair.holder.Backend.ExecContext(ctx,
		`CREATE TABLE "godj_gdj0046_commit_child" (`+
			`"id" INTEGER NOT NULL PRIMARY KEY, `+
			`"parent_id" INTEGER NOT NULL, `+
			`FOREIGN KEY ("parent_id") REFERENCES "godj_gdj0046_commit_parent" ("id") `+
			`DEFERRABLE INITIALLY DEFERRED)`,
	); err != nil {
		return protocol.Observation{}, err
	}
	commitChildID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	commitParentID := query.NewFieldRef("parent_id", "parent_id", query.FieldInteger, false)
	var commitCallbackCalls atomic.Int64
	commitErr := pair.holder.CoordinatedAtomic(ctx, func(session db.Session) error {
		commitCallbackCalls.Add(1)
		_, err := session.Insert(ctx, query.NewInsertPlan("godj_gdj0046_commit_child", []query.Assignment{
			query.NewAssignment(commitChildID, query.Integer(1)),
			query.NewAssignment(commitParentID, query.Integer(404)),
		}))
		return err
	})
	commitUnknown := errors.Is(commitErr, &query.Error{Code: query.CodeCommitOutcomeUnknown}) &&
		commitCallbackCalls.Load() == 1
	commitRows, err := systemStateCountRows(ctx, pair.holder, "godj_gdj0046_commit_child")
	if err != nil || commitRows != 0 || !commitUnknown {
		return protocol.Observation{}, fmt.Errorf(
			"deferred-FK commit probe = error %v callbacks %d durable rows %d query %v",
			commitErr,
			commitCallbackCalls.Load(),
			commitRows,
			err,
		)
	}

	// Nested acquisition on the same SQLite database is rejected by the live
	// writer fence. Ordinary Atomic remains independently usable afterwards.
	nestedCalls := int64(0)
	nestedErr := pair.holder.CoordinatedAtomic(ctx, func(db.Session) error {
		return pair.probe.CoordinatedAtomic(ctx, func(db.Session) error {
			nestedCalls++
			return nil
		})
	})
	nestingRejected := nestedErr != nil && nestedCalls == 0
	ordinaryErr := pair.holder.Atomic(ctx, func(session db.Session) error {
		_, err := session.Insert(ctx, insertProbe(5, "ordinary-atomic"))
		return err
	})
	durableRows, err := systemStateCountRows(ctx, pair.holder, "godj_gdj0046_fence_probe")
	if err != nil || ordinaryErr != nil || durableRows != 1 || !nestingRejected {
		return protocol.Observation{}, fmt.Errorf(
			"nesting/ordinary probe = nesting %v calls %d ordinary %v rows %d query %v",
			nestedErr,
			nestedCalls,
			ordinaryErr,
			durableRows,
			err,
		)
	}

	callbackRetries := barrier.callbackRetries()
	if callbackRetries != 0 {
		return protocol.Observation{}, fmt.Errorf("coordinated callbacks retried %d times", callbackRetries)
	}
	result := protocol.Object(map[string]protocol.Value{
		"acquire_before_callback": protocol.Boolean(blocked),
		"automatic_retry":         protocol.Boolean(callbackRetries != 0),
		"callback_cancellation":   protocol.String("rolled_back"),
		"callback_invocations": protocol.Object(map[string]protocol.Value{
			"acquire_cancelled": systemStateInt64(acquireCancelledCalls.Load()),
			"acquire_failed":    systemStateInt64(barrier.probeCallbackCalls.Load()),
			"acquire_succeeded": systemStateInt64(commitCallbackCalls.Load()),
		}),
		"commit_failure":           protocol.String("commit_outcome_unknown"),
		"confirmed_callback_error": protocol.String("rolled_back"),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"coordination_scope":                protocol.String("backend_database_or_schema"),
		"cross_domain_nesting":              protocol.String("rejected"),
		"ordinary_atomic_semantics_changed": protocol.Boolean(ordinaryErr != nil),
	})
	storedProbeValue, storedProbeRows, err := gdj0046QuerySingleString(
		ctx,
		pair.holder,
		"godj_gdj0046_fence_probe",
		probeValue,
	)
	if err != nil || storedProbeRows != 1 {
		return protocol.Observation{}, fmt.Errorf("read coordination canary storage: rows=%d: %w", storedProbeRows, err)
	}
	secretCount, err := systemStateSecretOccurrences(
		[]protocol.Value{result, dbState},
		[]string{
			storedProbeValue,
			fmt.Sprint(callbackErr),
			fmt.Sprint(callbackCancellationErr),
			fmt.Sprint(commitErr),
			fmt.Sprint(nestedErr),
		},
		coordinationSecret,
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"callback_retries":         systemStateInt64(callbackRetries),
		"coordination_fences":      systemStateInt(systemStateBoolInt(blocked)),
		"secret_values_serialized": systemStateInt64(secretCount),
	}))
}

type gdj0046OpenResult struct {
	runtime *systemstate.Runtime
	err     error
}

type gdj0046BootstrapFacts struct {
	credentialRows      int
	publishedMaterials  int
	holderReady         bool
	contenderReady      bool
	contenderMismatch   bool
	winnerReopenReady   bool
	winnerPasswordValid bool
	mismatchReopen      bool
	materialInvariant   bool
	bootstrapWinners    int64
	coordinationRetries int64
	secretCount         int64
}

type gdj0046CredentialSnapshot struct {
	id               int64
	principalID      string
	username         string
	encodedPassword  string
	active           bool
	permissions      string
	definitionDigest string
}

func gdj0046ReadCredentialSnapshot(
	ctx context.Context,
	backend db.Queryer,
) (gdj0046CredentialSnapshot, int, error) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	principalID := query.NewFieldRef("principal_id", "principal_id", query.FieldString, false)
	username := query.NewFieldRef("username", "username", query.FieldString, false)
	encodedPassword := query.NewFieldRef("encoded_password", "encoded_password", query.FieldString, false)
	active := query.NewFieldRef("active", "active", query.FieldBoolean, false)
	permissions := query.NewFieldRef("permissions", "permissions", query.FieldString, false)
	definitionDigest := query.NewFieldRef("definition_digest", "definition_digest", query.FieldString, false)
	plan, err := query.NewPlan(systemStateCredentialTable, []query.FieldRef{
		id,
		principalID,
		username,
		encodedPassword,
		active,
		permissions,
		definitionDigest,
	}).WithLimit(4)
	if err != nil {
		return gdj0046CredentialSnapshot{}, 0, err
	}
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return gdj0046CredentialSnapshot{}, 0, err
	}
	var snapshot gdj0046CredentialSnapshot
	count := 0
	for rows.Next() {
		var current gdj0046CredentialSnapshot
		if err := rows.Scan(
			&current.id,
			&current.principalID,
			&current.username,
			&current.encodedPassword,
			&current.active,
			&current.permissions,
			&current.definitionDigest,
		); err != nil {
			_ = rows.Close()
			return gdj0046CredentialSnapshot{}, 0, err
		}
		count++
		if count == 1 {
			snapshot = current
		}
	}
	return snapshot, count, errors.Join(rows.Err(), rows.Close())
}

func (snapshot gdj0046CredentialSnapshot) storageValues() []string {
	return []string{
		snapshot.principalID,
		snapshot.username,
		snapshot.encodedPassword,
		snapshot.permissions,
		snapshot.definitionDigest,
	}
}

func gdj0046RunConcurrentBootstrap(ctx context.Context, mismatch bool) (gdj0046BootstrapFacts, error) {
	pair, err := newGDJ0046BackendPair(ctx, false)
	if err != nil {
		return gdj0046BootstrapFacts{}, err
	}
	defer pair.cleanup()

	holderConfig := systemStateFixtureConfig(0xe1)
	holderConfig.Password = "gdj0046-bootstrap-password"
	contenderConfig := systemStateFixtureConfig(0xe1)
	contenderConfig.Password = holderConfig.Password
	if mismatch {
		contenderConfig.Password = "gdj0046-bootstrap-mismatch"
		contenderConfig.PrincipalID = "gdj0046-bootstrap-mismatch-principal"
	}
	barrier := pair.arm()
	holderResult := make(chan gdj0046OpenResult, 1)
	contenderResult := make(chan gdj0046OpenResult, 1)
	go func() {
		err := systemStateProvisionOperator(ctx, pair.holder, holderConfig)
		holderResult <- gdj0046OpenResult{err: err}
	}()
	if err := gdj0046WaitSignal(ctx, barrier.holderEntered, "bootstrap holder callback"); err != nil {
		barrier.release()
		return gdj0046BootstrapFacts{}, err
	}
	go func() {
		err := systemStateProvisionOperator(ctx, pair.contender, contenderConfig)
		contenderResult <- gdj0046OpenResult{err: err}
	}()
	if err := gdj0046AssertBlocked(ctx, pair, barrier); err != nil {
		barrier.release()
		return gdj0046BootstrapFacts{}, err
	}
	barrier.release()
	holder, err := gdj0046WaitResult(ctx, holderResult, "bootstrap holder result")
	if err != nil {
		return gdj0046BootstrapFacts{}, err
	}
	contender, err := gdj0046WaitResult(ctx, contenderResult, "bootstrap contender result")
	if err != nil {
		return gdj0046BootstrapFacts{}, err
	}
	pair.disarm()
	if holder.err != nil {
		return gdj0046BootstrapFacts{}, fmt.Errorf("holder provisioning = %v", holder.err)
	}
	contenderMismatch := errors.Is(contender.err, &systemstate.Error{Code: systemstate.CodeCredentialPolicyMismatch})
	if mismatch {
		if !contenderMismatch {
			return gdj0046BootstrapFacts{}, fmt.Errorf("mismatched provisioning = %v", contender.err)
		}
	} else if !errors.Is(contender.err, &systemstate.Error{Code: systemstate.CodeCredentialAlreadyExists}) {
		return gdj0046BootstrapFacts{}, fmt.Errorf("identical contender provisioning = %v", contender.err)
	}
	holder.runtime, err = systemStateOpenExisting(ctx, pair.holder, holderConfig)
	if err != nil || holder.runtime == nil {
		return gdj0046BootstrapFacts{}, fmt.Errorf("open holder after provisioning = (%v, %v)", holder.runtime, err)
	}
	if !mismatch {
		contender.runtime, err = systemStateOpenExisting(ctx, pair.contender, holderConfig)
		if err != nil || contender.runtime == nil {
			return gdj0046BootstrapFacts{}, fmt.Errorf("open identical contender after provisioning = (%v, %v)", contender.runtime, err)
		}
	}
	winnerSnapshot, rows, err := gdj0046ReadCredentialSnapshot(ctx, pair.holder)
	if err != nil {
		return gdj0046BootstrapFacts{}, err
	}
	winnerReopen, winnerReopenErr := systemStateOpenExisting(ctx, pair.holder, holderConfig)
	winnerPasswordValid := false
	if winnerReopenErr == nil && winnerReopen != nil {
		principal, authenticateErr := winnerReopen.Authenticator().Authenticate(
			ctx,
			holderConfig.Username,
			holderConfig.Password,
		)
		winnerPasswordValid = authenticateErr == nil && principal.ID() == holderConfig.PrincipalID
	}
	afterWinnerReopen, winnerReopenRows, err := gdj0046ReadCredentialSnapshot(ctx, pair.contender)
	if err != nil {
		return gdj0046BootstrapFacts{}, err
	}
	reopenMismatchConfig := holderConfig
	reopenMismatchConfig.Password = "gdj0046-bootstrap-reopen-mismatch"
	reopenMismatchConfig.PrincipalID = "gdj0046-bootstrap-reopen-mismatch-principal"
	mismatchRuntime, mismatchReopenErr := systemStateOpenExisting(ctx, pair.contender, reopenMismatchConfig)
	mismatchReopen := mismatchRuntime == nil &&
		errors.Is(mismatchReopenErr, &systemstate.Error{Code: systemstate.CodeCredentialPolicyMismatch})
	afterMismatchReopen, mismatchReopenRows, err := gdj0046ReadCredentialSnapshot(ctx, pair.holder)
	if err != nil {
		return gdj0046BootstrapFacts{}, err
	}
	materialInvariant := rows == 1 && winnerReopenRows == 1 && mismatchReopenRows == 1 &&
		winnerSnapshot == afterWinnerReopen && winnerSnapshot == afterMismatchReopen
	if winnerReopenErr != nil || winnerReopen == nil || !winnerPasswordValid || !mismatchReopen || !materialInvariant {
		return gdj0046BootstrapFacts{}, fmt.Errorf(
			"bootstrap reopen facts drifted: winner=(%v, %v, auth=%v) mismatch=(%v, %v) invariant=%v snapshots=%+v/%+v/%+v",
			winnerReopen,
			winnerReopenErr,
			winnerPasswordValid,
			mismatchRuntime,
			mismatchReopenErr,
			materialInvariant,
			winnerSnapshot,
			afterWinnerReopen,
			afterMismatchReopen,
		)
	}
	diagnosticValues := []string{
		fmt.Sprintf("%v", holderConfig),
		fmt.Sprintf("%#v", holderConfig),
		fmt.Sprintf("%v", contenderConfig),
		fmt.Sprintf("%#v", contenderConfig),
		fmt.Sprintf("%v", winnerReopen),
		fmt.Sprintf("%#v", winnerReopen),
		fmt.Sprint(contender.err),
		fmt.Sprint(mismatchReopenErr),
	}
	storageValues := append(winnerSnapshot.storageValues(), diagnosticValues...)
	secretCount, err := systemStateSecretOccurrences(
		nil,
		storageValues,
		holderConfig.Password,
		contenderConfig.Password,
		reopenMismatchConfig.Password,
	)
	if err != nil {
		return gdj0046BootstrapFacts{}, err
	}
	// Empty bootstrap has exactly one durable publication. Count updates as
	// publications too: an identical contender that rewrites the winning bytes
	// must not pass merely because credential cardinality and snapshots stayed
	// unchanged.
	winners := gdj0046BootstrapPublicationWrites(pair)
	return gdj0046BootstrapFacts{
		credentialRows:      rows,
		publishedMaterials:  systemStateBoolInt(winnerSnapshot.encodedPassword != "" && materialInvariant),
		holderReady:         holder.runtime != nil,
		contenderReady:      contender.runtime != nil,
		contenderMismatch:   contenderMismatch,
		winnerReopenReady:   winnerReopen != nil,
		winnerPasswordValid: winnerPasswordValid,
		mismatchReopen:      mismatchReopen,
		materialInvariant:   materialInvariant,
		bootstrapWinners:    winners,
		coordinationRetries: barrier.callbackRetries(),
		secretCount:         secretCount,
	}, nil
}

func gdj0046BootstrapPublicationWrites(pair *gdj0046BackendPair) int64 {
	if pair == nil || pair.holder == nil || pair.contender == nil ||
		pair.holder.systemStateObservedBackend == nil || pair.contender.systemStateObservedBackend == nil {
		return 0
	}
	return pair.holder.inserts.Load() + pair.contender.inserts.Load() +
		pair.holder.updates.Load() + pair.contender.updates.Load()
}

func systemStateConcurrentAdminBootstrap(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	identical, err := gdj0046RunConcurrentBootstrap(ctx, false)
	if err != nil {
		return protocol.Observation{}, err
	}
	mismatched, err := gdj0046RunConcurrentBootstrap(ctx, true)
	if err != nil {
		return protocol.Observation{}, err
	}
	if identical.credentialRows != 1 || identical.publishedMaterials != 1 ||
		!identical.holderReady || !identical.contenderReady || !identical.winnerReopenReady ||
		!identical.winnerPasswordValid || !identical.mismatchReopen || !identical.materialInvariant ||
		identical.bootstrapWinners != 1 ||
		mismatched.credentialRows != 1 || mismatched.publishedMaterials != 1 ||
		!mismatched.holderReady || !mismatched.contenderMismatch || !mismatched.winnerReopenReady ||
		!mismatched.winnerPasswordValid || !mismatched.mismatchReopen || !mismatched.materialInvariant ||
		mismatched.bootstrapWinners != 1 {
		return protocol.Observation{}, fmt.Errorf(
			"bootstrap facts drifted: identical=%+v mismatched=%+v",
			identical,
			mismatched,
		)
	}
	result := protocol.Object(map[string]protocol.Value{
		"concurrent_empty":       protocol.String("identical_material_success"),
		"duplicate_publications": systemStateInt(identical.credentialRows - identical.publishedMaterials),
		"mismatched_material":    protocol.String("fail_closed"),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"credential_rows":     systemStateInt(identical.credentialRows),
		"mismatch_writes":     systemStateInt64(mismatched.bootstrapWinners - 1),
		"published_materials": systemStateInt(identical.publishedMaterials),
	})
	serializedSecrets, err := authSessionSecretOccurrences(
		[]protocol.Value{result, dbState},
		"gdj0046-bootstrap-password",
		"gdj0046-bootstrap-mismatch",
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	serializedSecrets += identical.secretCount + mismatched.secretCount
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"bootstrap_winners":        systemStateInt64(identical.bootstrapWinners),
		"coordination_retries":     systemStateInt64(identical.coordinationRetries + mismatched.coordinationRetries),
		"secret_values_serialized": systemStateInt64(serializedSecrets),
	}))
}
