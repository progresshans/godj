package lifecyclefence

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	godjsqlite "github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// These two tests intentionally use the current product packages. They prove
// the stale window that the otherwise independent SQLite spike is evaluating;
// they are not a proposed lifecycle API.
func TestCurrentLifecycleAcceptsStaleSnapshotBeforeFirstWrite(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/current-before-first.sqlite"
	left := openCurrentBackend(t, path)
	right := openCurrentBackend(t, path)

	leftMigration := currentCreateMigration("left", "0001_initial", "current_left")
	rightMigration := currentCreateMigration("right", "0001_initial", "current_right")
	definitions := []migrations.Migration{leftMigration, rightMigration}

	leftBefore, leftPlan := currentSnapshotPlan(t, ctx, left, definitions, leftMigration.Key())
	rightBefore, rightPlan := currentSnapshotPlan(t, ctx, right, definitions, rightMigration.Key())

	if _, err := (migrations.DirectExecutor{Backend: right}).ExecutePlan(ctx, rightBefore, definitions, rightPlan); err != nil {
		t.Fatalf("competing right commit: %v", err)
	}
	if _, err := (migrations.DirectExecutor{Backend: left}).ExecutePlan(ctx, leftBefore, definitions, leftPlan); err != nil {
		t.Fatalf("current lifecycle unexpectedly rejected stale left plan: %v", err)
	}

	records := readRecorderAtPath(t, path)
	want := []string{"left.0001_initial", "right.0001_initial"}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("durable records = %v, want %v", records, want)
	}
	t.Logf("CURRENT_GAP stale snapshot committed after competitor: records=%v", records)
}

func TestCurrentLifecycleAcceptsCompetitorBetweenSteps(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/current-between-steps.sqlite"
	planBackend := openCurrentBackend(t, path)
	competitorBackend := openCurrentBackend(t, path)

	first := currentCreateMigration("plan", "0001_first", "current_plan_first")
	second := currentCreateMigration("plan", "0002_second", "current_plan_second")
	second.Dependencies = []migrations.MigrationKey{first.Key()}
	competitor := currentCreateMigration("other", "0001_competitor", "current_competitor")
	definitions := []migrations.Migration{first, second, competitor}

	before, plan := currentSnapshotPlan(t, ctx, planBackend, definitions, second.Key())
	if len(plan) != 2 {
		t.Fatalf("plan steps = %d, want 2: %#v", len(plan), plan)
	}
	gate := &currentSecondBeginGate{
		delegate:     planBackend,
		beforeSecond: make(chan struct{}),
		release:      make(chan struct{}),
	}

	type result struct {
		state migrations.ProjectState
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		state, err := (migrations.DirectExecutor{Backend: gate}).ExecutePlan(ctx, before, definitions, plan)
		resultCh <- result{state: state, err: err}
	}()

	select {
	case <-gate.beforeSecond:
	case early := <-resultCh:
		t.Fatalf("plan returned before second transaction: %v", early.err)
	case <-time.After(5 * time.Second):
		t.Fatal("plan did not reach second transaction")
	}

	competitorBefore, competitorPlan := currentSnapshotPlan(t, ctx, competitorBackend, definitions, competitor.Key())
	if _, err := (migrations.DirectExecutor{Backend: competitorBackend}).ExecutePlan(
		ctx,
		competitorBefore,
		definitions,
		competitorPlan,
	); err != nil {
		t.Fatalf("competing commit between steps: %v", err)
	}
	close(gate.release)

	got := <-resultCh
	if got.err != nil {
		t.Fatalf("current lifecycle unexpectedly rejected stale second step: %v", got.err)
	}
	if _, exists := got.state.Model("other", "competitor"); exists {
		t.Fatal("plan-local returned state unexpectedly included competitor")
	}
	records := readRecorderAtPath(t, path)
	want := []string{"other.0001_competitor", "plan.0001_first", "plan.0002_second"}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("durable records = %v, want %v", records, want)
	}
	t.Logf("CURRENT_GAP competitor committed between steps and stale next step committed: records=%v", records)
}

type currentSecondBeginGate struct {
	delegate     migrationbackend.AtomicBackend
	beginCount   atomic.Int32
	beforeSecond chan struct{}
	release      chan struct{}
}

func (gate *currentSecondBeginGate) BeginMigration(ctx context.Context) (migrationbackend.Transaction, error) {
	if gate.beginCount.Add(1) == 2 {
		close(gate.beforeSecond)
		select {
		case <-gate.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return gate.delegate.BeginMigration(ctx)
}

func currentSnapshotPlan(
	t *testing.T,
	ctx context.Context,
	reader migrationbackend.AppliedMigrationReader,
	definitions []migrations.Migration,
	target migrations.MigrationKey,
) (migrations.ProjectState, []migrations.PlanStep) {
	t.Helper()
	applied, err := migrations.LoadAppliedState(ctx, reader)
	if err != nil {
		t.Fatal(err)
	}
	planner, err := migrations.NewPlanner(definitions...)
	if err != nil {
		t.Fatal(err)
	}
	if err := planner.CheckHistory(applied); err != nil {
		t.Fatal(err)
	}
	reconstructor, err := migrations.NewStateReconstructor(definitions...)
	if err != nil {
		t.Fatal(err)
	}
	before, err := reconstructor.Reconstruct(migrations.AppliedStateRequest(applied))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planner.Plan(applied, migrations.NamedTarget(target))
	if err != nil {
		t.Fatal(err)
	}
	return before, plan
}

func currentCreateMigration(app, name, table string) migrations.Migration {
	modelName := strings.TrimPrefix(table, "current_")
	return migrations.Migration{
		App:  app,
		Name: name,
		Operations: []migrations.Operation{migrations.CreateModel{
			AppLabel: app,
			Model: ir.Model{
				Name:    modelName,
				GoName:  "Spike_" + table,
				DBTable: table,
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

func openCurrentBackend(t *testing.T, path string) *godjsqlite.Backend {
	t.Helper()
	backend, err := godjsqlite.Open(context.Background(), sqliteDSN(path, 1000, false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close current backend: %v", err)
		}
	})
	return backend
}

func readRecorderAtPath(t *testing.T, path string) []string {
	t.Helper()
	database, err := sql.Open("sqlite", sqliteDSN(path, 1000, false))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close recorder reader: %v", err)
		}
	}()
	records, err := readCanonicalHistory(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	return formatHistory(records)
}
