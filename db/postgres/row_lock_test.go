package postgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/snapshotdriver"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type rowLockFixture struct {
	base, selected, values query.Plan
	target                 query.RowLockTarget
	id                     query.FieldRef
	path                   query.RelationPath
}

func rowLockPlans(t *testing.T, nullable bool) rowLockFixture {
	t.Helper()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	category := query.NewFieldRef("category", "category_id", query.FieldInteger, nullable)
	projection, err := query.NewForwardRelationProjection(ir.ModelIdentity{AppLabel: "locks", ModelName: "item"}, "rowlock_items", category, ir.ModelIdentity{AppLabel: "locks", ModelName: "category"}, "rowlock_categories", id, []query.FieldRef{id, name}, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	base := query.NewPlan("rowlock_items", []query.FieldRef{id, name, category})
	selected, err := base.WithRelationProjections(projection)
	if err != nil {
		t.Fatal(err)
	}
	path, err := query.NewForwardRelationChain(projection.Path().Hops(), name, query.RelationTerminalRelatedField)
	if err != nil {
		t.Fatal(err)
	}
	value, err := query.RelatedFieldResult(path)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewProjectionResult(value)
	if err != nil {
		t.Fatal(err)
	}
	values, err := base.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	target, err := query.LockRelated(projection.Path())
	if err != nil {
		t.Fatal(err)
	}
	return rowLockFixture{base: base, selected: selected, values: values, target: target, id: id, path: path}
}

func lockPlan(t *testing.T, plan query.Plan, strength query.LockStrength, wait query.LockWaitPolicy, targets ...query.RowLockTarget) query.Plan {
	t.Helper()
	lock, err := query.NewRowLock(strength, wait, targets...)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithRowLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPostgresRowLockCompilerPreservesScopeAndOptions(t *testing.T) {
	t.Parallel()
	fixture := rowLockPlans(t, false)
	for _, test := range []struct {
		name     string
		plan     query.Plan
		strength query.LockStrength
		wait     query.LockWaitPolicy
		targets  []query.RowLockTarget
		suffix   string
	}{
		{"root_all", fixture.base, query.LockForUpdate, query.LockWait, nil, " FOR UPDATE"},
		{"root_explicit", fixture.base, query.LockForUpdate, query.LockNoWait, []query.RowLockTarget{query.LockSelf()}, ` FOR UPDATE OF "rowlock_items" NOWAIT`},
		{"selected_all", fixture.selected, query.LockForNoKeyUpdate, query.LockSkipLocked, nil, " FOR NO KEY UPDATE SKIP LOCKED"},
		{"selected_root", fixture.selected, query.LockForUpdate, query.LockWait, []query.RowLockTarget{query.LockSelf()}, ` FOR UPDATE OF "t0"`},
		{"selected_related", fixture.selected, query.LockForUpdate, query.LockNoWait, []query.RowLockTarget{fixture.target}, ` FOR UPDATE OF "t1" NOWAIT`},
		{"selected_both", fixture.selected, query.LockForNoKeyUpdate, query.LockWait, []query.RowLockTarget{fixture.target, query.LockSelf()}, ` FOR NO KEY UPDATE OF "t0", "t1"`},
		{"values_root_without_root_columns", fixture.values, query.LockForUpdate, query.LockWait, []query.RowLockTarget{query.LockSelf()}, ` FOR UPDATE OF "t0"`},
		{"values_related", fixture.values, query.LockForUpdate, query.LockWait, []query.RowLockTarget{fixture.target}, ` FOR UPDATE OF "t1"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := test.plan.WithOrderings(query.NewOrdering(fixture.id, query.Ascending)).WithLimit(2)
			if err != nil {
				t.Fatal(err)
			}
			plan, err = plan.WithOffset(1)
			if err != nil {
				t.Fatal(err)
			}
			plan = lockPlan(t, plan, test.strength, test.wait, test.targets...)
			statement, args, err := compilePlan("locks_schema", plan)
			if err != nil || !strings.HasSuffix(statement, " LIMIT $1 OFFSET $2"+test.suffix) || !reflect.DeepEqual(args, []any{int64(2), int64(1)}) {
				t.Fatalf("lock compile = %q / %#v / %v", statement, args, err)
			}
		})
	}
}

func TestPostgresRowLockRejectsUnavailableOrConflictingTargets(t *testing.T) {
	t.Parallel()
	fixture := rowLockPlans(t, false)
	foreign, err := query.NewForwardRelationPath(ir.ModelIdentity{AppLabel: "locks", ModelName: "item"}, "rowlock_items", "category", "category_id", ir.ModelIdentity{AppLabel: "locks", ModelName: "category"}, "wrong_categories", "id", false, fixture.id, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	foreignTarget, err := query.LockRelated(foreign)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		plan   query.Plan
		target query.RowLockTarget
	}{
		{"missing_join", fixture.base, fixture.target},
		{"same_name_foreign_table", fixture.selected, foreignTarget},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, empty := range []bool{false, true} {
				plan := lockPlan(t, test.plan, query.LockForUpdate, query.LockWait, test.target)
				if empty {
					plan, err = plan.WithLimit(0)
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, _, err := compilePlan("locks_schema", plan); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatalf("invalid target, empty=%v: %v", empty, err)
				}
			}
		})
	}
	optional := rowLockPlans(t, true)
	for _, targets := range [][]query.RowLockTarget{nil, {optional.target}} {
		plan := lockPlan(t, optional.selected, query.LockForUpdate, query.LockWait, targets...)
		if _, _, err := compilePlan("locks_schema", plan); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
			t.Fatalf("outer lock = %v", err)
		}
	}
	rootOnly := lockPlan(t, optional.selected, query.LockForUpdate, query.LockWait, query.LockSelf())
	if statement, _, err := compilePlan("locks_schema", rootOnly); err != nil || !strings.Contains(statement, "LEFT OUTER JOIN") || !strings.HasSuffix(statement, ` FOR UPDATE OF "t0"`) {
		t.Fatalf("outer root-only lock = %q/%v", statement, err)
	}
	present, err := optional.selected.WithConditions(query.NewRelatedCondition(optional.path, query.LookupIsNull, query.Boolean(false)))
	if err != nil {
		t.Fatal(err)
	}
	if statement, _, err := compilePlan("locks_schema", lockPlan(t, present, query.LockForUpdate, query.LockWait, optional.target)); err != nil || strings.Contains(statement, "LEFT OUTER JOIN") {
		t.Fatalf("proven-present target = %q/%v", statement, err)
	}
	if _, _, err := compilePlan("locks_schema", lockPlan(t, fixture.base.WithDistinct(), query.LockForUpdate, query.LockWait)); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatalf("distinct lock = %v", err)
	}
	limited, err := fixture.base.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}
	windowed, err := limited.ForPrefetchForeignKey(fixture.base.SourceFields()[2], []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := compilePlan("locks_schema", lockPlan(t, windowed, query.LockForUpdate, query.LockWait)); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatalf("windowed prefetch lock = %v", err)
	}
	filtered, err := fixture.base.WithConditions(query.NewRelatedCondition(fixture.path, query.LookupExact, query.String("one")))
	if err != nil {
		t.Fatal(err)
	}
	filtered = lockPlan(t, filtered, query.LockForUpdate, query.LockWait, fixture.target)
	if statement, _, err := compilePlan("locks_schema", filtered); err != nil || !strings.HasSuffix(statement, ` FOR UPDATE OF "t1"`) {
		t.Fatalf("materialized filter target = %q/%v", statement, err)
	}
	countShape, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []query.Plan{filtered, lockPlan(t, fixture.base.WithDistinct(), query.LockForUpdate, query.LockWait)} {
		count, err := source.WithResultShape(countShape)
		if err != nil {
			t.Fatal(err)
		}
		if statement, _, err := compilePlan("locks_schema", count); err != nil || strings.Contains(statement, "FOR UPDATE") {
			t.Fatalf("derived count retained lock: %q/%v", statement, err)
		}
	}
}

func TestPostgresRowLockRequiresWritableTransactionBeforeIO(t *testing.T) {
	t.Parallel()
	state := &snapshotdriver.State{}
	backend := &Backend{database: sql.OpenDB(snapshotdriver.Connector{State: state}), schema: "public"}
	t.Cleanup(func() { _ = backend.Close() })
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan := lockPlan(t, query.NewPlan("items", []query.FieldRef{id}), query.LockForUpdate, query.LockWait)
	check := func(err error) {
		t.Helper()
		if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeTransactionRequired}) {
			t.Fatalf("non-writable lock = %v", err)
		}
	}
	rows, err := backend.Query(t.Context(), plan)
	if rows != nil {
		_ = rows.Close()
		t.Fatal("autocommit returned rows")
	}
	check(err)
	check(backend.QueryBatches(t.Context(), plan, 1, func(db.Row) error { t.Fatal("autocommit scanned"); return nil }, func(db.Queryer) (bool, error) { t.Fatal("autocommit yielded"); return false, nil }))
	if state.Snapshot().Connections != 0 {
		t.Fatal("autocommit lock acquired a connection")
	}
	empty, err := plan.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = backend.Query(t.Context(), empty)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil || rows.Close() != nil || state.Snapshot().Connections != 0 {
		t.Fatal("empty source performed I/O")
	}
	if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
		rows, err := reader.Query(t.Context(), plan)
		if rows != nil {
			_ = rows.Close()
			t.Fatal("read-only snapshot returned locked rows")
		}
		check(err)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := state.Snapshot(); got.OpenRows != 0 || !reflect.DeepEqual(got.Statements, []string{"BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY NOT DEFERRABLE", "ROLLBACK"}) {
		t.Fatalf("read-only execution = %#v", got)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := backend.Query(canceled, empty); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled empty lock = %v", err)
	}
}

func TestPostgresRowLockNestedAndReverseTargetsUseTheirOwnOccurrence(t *testing.T) {
	t.Parallel()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	categoryKey := query.NewFieldRef("category", "category_id", query.FieldInteger, false)
	regionKey := query.NewFieldRef("region", "region_id", query.FieldInteger, false)
	item := ir.ModelIdentity{AppLabel: "locks", ModelName: "item"}
	category := ir.ModelIdentity{AppLabel: "locks", ModelName: "category"}
	region := ir.ModelIdentity{AppLabel: "locks", ModelName: "region"}
	categoryProjection, err := query.NewForwardRelationProjection(item, "items", categoryKey, category, "categories", id, []query.FieldRef{id, name, regionKey}, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	regionPath, err := query.NewForwardRelationPath(category, "categories", "region", "region_id", region, "regions", "id", false, id, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	regionProjection, err := query.NewRelationProjection(append(categoryProjection.Path().Hops(), regionPath.Hops()...), id, []query.FieldRef{id, name})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("items", []query.FieldRef{id, name, categoryKey}).WithRelationProjections(categoryProjection, regionProjection)
	if err != nil {
		t.Fatal(err)
	}
	target, err := query.LockRelated(regionProjection.Path())
	if err != nil {
		t.Fatal(err)
	}
	statement, _, err := compilePlan("locks_schema", lockPlan(t, plan, query.LockForUpdate, query.LockNoWait, target))
	if err != nil || strings.Count(statement, "INNER JOIN") != 2 || !strings.HasSuffix(statement, ` FOR UPDATE OF "t2" NOWAIT`) {
		t.Fatalf("nested region lock = %q/%v", statement, err)
	}
	reverse, err := query.NewReverseRelationPath(item, "items", "category", "category_id", category, "categories", "id", "item", false, id, ir.RelationOneToOne)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := query.NewRelationProjection(reverse.Hops(), id, []query.FieldRef{id, name, categoryKey})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = query.NewPlan("categories", []query.FieldRef{id, name}).WithRelationProjections(projection)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithConditions(query.NewRelatedCondition(reverse, query.LookupIsNull, query.Boolean(false)))
	if err != nil {
		t.Fatal(err)
	}
	target, err = query.LockRelated(reverse)
	if err != nil {
		t.Fatal(err)
	}
	statement, _, err = compilePlan("locks_schema", lockPlan(t, plan, query.LockForNoKeyUpdate, query.LockWait, target))
	if err != nil || strings.Contains(statement, "LEFT OUTER JOIN") || !strings.HasSuffix(statement, ` FOR NO KEY UPDATE OF "t1"`) {
		t.Fatalf("reverse OneToOne lock = %q/%v", statement, err)
	}
}
