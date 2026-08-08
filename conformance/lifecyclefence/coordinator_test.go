package lifecyclefence

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// These shapes are deliberately test-only. They exercise a coordinator/port
// boundary without selecting product names, zero values, or an error taxonomy.
type candidateLifecycleStep struct {
	name      string
	direction stepDirection
	after     migrations.ProjectState
}

type candidateLifecycleResult struct {
	lastDurable migrations.ProjectState
	snapshot    historySnapshot
	attempted   []string
	committed   []string
}

type candidateFenceCapability interface {
	spikeReadAtomicHistory(context.Context) (historySnapshot, error)
	spikeExecuteFencedStep(context.Context, historySnapshot, candidateLifecycleStep) (historySnapshot, error)
}

type sqliteCandidateCapability struct {
	reader         *sql.DB
	writer         *sql.DB
	bootstrapEpoch string
	snapshotCalls  atomic.Int32
	stepCalls      atomic.Int32
}

func (capability *sqliteCandidateCapability) spikeReadAtomicHistory(ctx context.Context) (historySnapshot, error) {
	capability.snapshotCalls.Add(1)
	return readAtomicSnapshot(ctx, capability.reader)
}

func (capability *sqliteCandidateCapability) spikeExecuteFencedStep(
	ctx context.Context,
	expected historySnapshot,
	step candidateLifecycleStep,
) (historySnapshot, error) {
	capability.stepCalls.Add(1)
	return runFencedStep(
		ctx,
		capability.writer,
		expected,
		step.name,
		step.direction,
		capability.bootstrapEpoch,
		nil,
	)
}

type candidateBeforeAttempt func(index int, expected historySnapshot) error

func runCandidateLifecycle(
	ctx context.Context,
	port any,
	before migrations.ProjectState,
	steps []candidateLifecycleStep,
	beforeAttempt candidateBeforeAttempt,
) (candidateLifecycleResult, error) {
	result := candidateLifecycleResult{lastDurable: before.Clone()}
	capability, supported := port.(candidateFenceCapability)
	if !supported {
		return result, errFenceUnsupported
	}
	snapshot, err := capability.spikeReadAtomicHistory(ctx)
	if err != nil {
		return result, err
	}
	result.snapshot = snapshot
	for index, step := range steps {
		if beforeAttempt != nil {
			if err := beforeAttempt(index, result.snapshot); err != nil {
				return result, err
			}
		}
		result.attempted = append(result.attempted, step.name)
		successor, err := capability.spikeExecuteFencedStep(ctx, result.snapshot, step)
		if err != nil {
			return result, err
		}
		result.snapshot = successor
		result.lastDurable = step.after.Clone()
		result.committed = append(result.committed, step.name)
	}
	return result, nil
}

func TestCandidateCoordinatorStopsTailAndReturnsLastDurableProjectState(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/coordinator-between-steps.sqlite"
	initializeReadyDatabase(t, path, "ready-epoch", nil)
	reader := openSpikeDatabase(t, path, 1000, false)
	planWriter := openSpikeDatabase(t, path, 1000, true)
	competitorWriter := openSpikeDatabase(t, path, 1000, true)

	first := coordinatorMigration("0001_first", "first")
	second := coordinatorMigration("0002_second", "second")
	second.Dependencies = []migrations.MigrationKey{first.Key()}
	tail := coordinatorMigration("0003_tail", "tail")
	tail.Dependencies = []migrations.MigrationKey{second.Key()}
	reconstructor, err := migrations.NewStateReconstructor(first, second, tail)
	if err != nil {
		t.Fatal(err)
	}
	state0, err := reconstructor.Reconstruct(migrations.EmptyStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	state1, err := reconstructor.Reconstruct(migrations.AfterStateRequest(first.Key()))
	if err != nil {
		t.Fatal(err)
	}
	state2, err := reconstructor.Reconstruct(migrations.AfterStateRequest(second.Key()))
	if err != nil {
		t.Fatal(err)
	}
	state3, err := reconstructor.Reconstruct(migrations.AfterStateRequest(tail.Key()))
	if err != nil {
		t.Fatal(err)
	}
	steps := []candidateLifecycleStep{
		{name: "plan_first", direction: stepApply, after: state1},
		{name: "plan_second", direction: stepApply, after: state2},
		{name: "plan_tail", direction: stepApply, after: state3},
	}
	capability := &sqliteCandidateCapability{reader: reader, writer: planWriter}
	result, err := runCandidateLifecycle(
		ctx,
		capability,
		state0,
		steps,
		func(index int, expected historySnapshot) error {
			if index != 1 {
				return nil
			}
			_, err := runFencedStep(
				ctx,
				competitorWriter,
				expected,
				"competitor",
				stepApply,
				"",
				nil,
			)
			return err
		},
	)
	if !errors.Is(err, errFenceStale) {
		t.Fatalf("coordinator conflict = %v, want stale", err)
	}
	if !result.lastDurable.Equal(state1) || result.lastDurable.Equal(state0) || result.lastDurable.Equal(state2) {
		t.Fatal("coordinator did not return the first committed ProjectState")
	}
	if !reflect.DeepEqual(result.attempted, []string{"plan_first", "plan_second"}) ||
		!reflect.DeepEqual(result.committed, []string{"plan_first"}) {
		t.Fatalf("attempted=%v committed=%v", result.attempted, result.committed)
	}
	if capability.snapshotCalls.Load() != 1 || capability.stepCalls.Load() != 2 {
		t.Fatalf(
			"coordinator calls snapshot=%d step=%d, want 1/2 with no retry",
			capability.snapshotCalls.Load(),
			capability.stepCalls.Load(),
		)
	}
	if result.snapshot.token.revision != 1 ||
		!reflect.DeepEqual(formatHistory(result.snapshot.identities), []string{"spike.plan_first"}) {
		t.Fatalf("returned last-durable snapshot = %+v", result.snapshot)
	}
	shape := readDurableShape(t, reader)
	if shape.revision != 2 ||
		!reflect.DeepEqual(shape.tables, []string{"step_competitor", "step_plan_first"}) ||
		!reflect.DeepEqual(shape.records, []string{"spike.competitor", "spike.plan_first"}) ||
		contains(shape.tables, "step_plan_second") || contains(shape.tables, "step_plan_tail") {
		t.Fatalf("coordinator durable shape = %+v", shape)
	}
}

type legacyPublicPortFake struct {
	readCalls  atomic.Int32
	beginCalls atomic.Int32
}

var _ migrationbackend.AppliedMigrationReader = (*legacyPublicPortFake)(nil)
var _ migrationbackend.AtomicBackend = (*legacyPublicPortFake)(nil)

func (fake *legacyPublicPortFake) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	fake.readCalls.Add(1)
	return []migrationbackend.AppliedMigration{}, nil
}

func (fake *legacyPublicPortFake) BeginMigration(context.Context) (migrationbackend.Transaction, error) {
	fake.beginCalls.Add(1)
	return nil, errors.New("legacy begin must not be called by candidate coordinator")
}

func TestCandidateCoordinatorFailsClosedForLegacyPublicPortWithoutFallback(t *testing.T) {
	legacy := &legacyPublicPortFake{}
	before := migrations.EmptyProjectState()
	result, err := runCandidateLifecycle(
		context.Background(),
		legacy,
		before,
		[]candidateLifecycleStep{{name: "must_not_run", direction: stepApply, after: before}},
		nil,
	)
	if !errors.Is(err, errFenceUnsupported) {
		t.Fatalf("legacy-only capability error = %v, want unsupported", err)
	}
	if legacy.readCalls.Load() != 0 || legacy.beginCalls.Load() != 0 ||
		len(result.attempted) != 0 || len(result.committed) != 0 ||
		!result.lastDurable.Equal(before) {
		t.Fatalf(
			"unsupported fallback leaked calls/state: reads=%d begins=%d result=%+v",
			legacy.readCalls.Load(),
			legacy.beginCalls.Load(),
			result,
		)
	}
}

func TestCandidateCoordinatorBusyAttemptCountIsExactlyOne(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/coordinator-busy.sqlite"
	initializeReadyDatabase(t, path, "ready-epoch", nil)
	reader := openSpikeDatabase(t, path, 1000, false)
	holder := openSpikeDatabase(t, path, 1000, true)
	contender := openSpikeDatabase(t, path, 25, true)
	lock, err := holder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := migrations.EmptyProjectState()
	definition := coordinatorMigration("0001_busy", "busy")
	reconstructor, err := migrations.NewStateReconstructor(definition)
	if err != nil {
		t.Fatal(err)
	}
	after, err := reconstructor.Reconstruct(migrations.AfterStateRequest(definition.Key()))
	if err != nil {
		t.Fatal(err)
	}
	step := candidateLifecycleStep{name: "busy_once", direction: stepApply, after: after}
	capability := &sqliteCandidateCapability{reader: reader, writer: contender}
	result, err := runCandidateLifecycle(
		ctx,
		capability,
		before,
		[]candidateLifecycleStep{step},
		nil,
	)
	if !errors.Is(err, errFenceContended) || errors.Is(err, errFenceStale) {
		t.Fatalf("busy coordinator error = %v, want contention", err)
	}
	if capability.snapshotCalls.Load() != 1 || capability.stepCalls.Load() != 1 ||
		!reflect.DeepEqual(result.attempted, []string{"busy_once"}) || len(result.committed) != 0 {
		t.Fatalf(
			"busy coordinator retried: snapshot=%d step=%d attempted=%v committed=%v",
			capability.snapshotCalls.Load(),
			capability.stepCalls.Load(),
			result.attempted,
			result.committed,
		)
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	later, err := runCandidateLifecycle(
		ctx,
		&sqliteCandidateCapability{reader: reader, writer: contender},
		before,
		[]candidateLifecycleStep{step},
		nil,
	)
	if err != nil {
		t.Fatalf("explicit later lifecycle attempt: %v", err)
	}
	if !later.lastDurable.Equal(after) || !reflect.DeepEqual(later.committed, []string{"busy_once"}) {
		t.Fatalf("explicit later lifecycle result = %+v", later)
	}
}

func coordinatorMigration(name, model string) migrations.Migration {
	return migrations.Migration{
		App:  "plan",
		Name: name,
		Operations: []migrations.Operation{migrations.CreateModel{
			AppLabel: "plan",
			Model: ir.Model{
				Name:    model,
				GoName:  "Coordinator_" + model,
				DBTable: "coordinator_" + model,
				Fields: []ir.Field{{
					Name:       "id",
					GoName:     "ID",
					Column:     "id",
					Kind:       ir.FieldAuto,
					PrimaryKey: true,
				}},
			},
		}},
	}
}
