package migrations

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestExecutorExecutePlanValidatesContextBeforeEmptyNoOp(t *testing.T) {
	t.Parallel()

	backend := &planTestBackend{}
	executor := Executor{Backend: backend}
	before := EmptyProjectState()

	for _, test := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "nil", ctx: nil},
		{name: "canceled", ctx: canceledContext(), want: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			after, err := executor.ExecutePlan(test.ctx, before, nil, nil)
			assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("ExecutePlan() error = %v, want %v", err, test.want)
			}
			if !after.Equal(before) {
				t.Fatal("context failure changed returned state")
			}
		})
	}
	if backend.beginCount != 0 {
		t.Fatalf("BeginMigration() calls = %d, want 0", backend.beginCount)
	}
}

func TestExecutorExecutePlanEmptyPlanDoesNotInspectDefinitionsOrBackend(t *testing.T) {
	t.Parallel()

	var typedNilOperation *CreateModel
	definitions := []Migration{
		{App: "", Name: "", Operations: []Operation{typedNilOperation}},
		{App: "", Name: ""},
	}
	var typedNilBackend *planTestBackend
	before := EmptyProjectState()
	after, err := (Executor{Backend: typedNilBackend}).ExecutePlan(
		context.Background(),
		before,
		definitions,
		nil,
	)
	if err != nil {
		t.Fatalf("ExecutePlan(empty) error = %v", err)
	}
	if !after.Equal(before) {
		t.Fatal("empty plan changed returned state")
	}
}

func TestExecutorExecutePlanRejectsRawRelationBeforeEmptyNoOpOrIO(t *testing.T) {
	t.Parallel()

	definition := Migration{
		App: "blog", Name: "0001_post",
		Operations: []Operation{CreateModel{AppLabel: "blog", Model: relationMigrationSchema().Models[0]}},
	}
	for _, plan := range [][]PlanStep{
		nil,
		{{Key: definition.Key(), Direction: DirectionForward}},
	} {
		fake := &planTestBackend{}
		before := EmptyProjectState()
		after, err := (Executor{Backend: fake}).ExecutePlan(context.Background(), before, []Migration{definition}, plan)
		assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
		var capability *backend.CapabilityError
		if !errors.As(err, &capability) || capability.Feature != "relation_migration" {
			t.Fatalf("ExecutePlan(raw relation) error = %#v capability=%#v", err, capability)
		}
		if fake.beginCount != 0 || !after.Equal(before) {
			t.Fatalf("ExecutePlan(raw relation) touched I/O/state: begin=%d apps=%v", fake.beginCount, after.Apps())
		}
	}
}

func TestExecutorExecutePlanRejectsPrivateRelationStateWithScalarDefinitions(t *testing.T) {
	t.Parallel()

	before := relationExecutorProjectState(t)
	definition := Migration{App: "blog", Name: "0003_summary", Operations: []Operation{AddField{
		AppLabel: "blog", ModelName: "post", Field: summaryField(),
	}}}
	for _, plan := range [][]PlanStep{nil, {{Key: definition.Key(), Direction: DirectionForward}}} {
		fake := &planTestBackend{}
		after, err := (Executor{Backend: fake}).ExecutePlan(context.Background(), before, []Migration{definition}, plan)
		assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
		var capability *backend.CapabilityError
		if !errors.As(err, &capability) || capability.Feature != "relation_migration" || fake.beginCount != 0 || !after.Equal(before) {
			t.Fatalf("ExecutePlan scalar/private relation-state = error:%v begin:%d", err, fake.beginCount)
		}
	}
}

func TestExecutorExecutePlanPostScanCancellationBeatsRelationCapability(t *testing.T) {
	t.Parallel()

	definition := Migration{App: "blog", Name: "0001_post", Operations: []Operation{
		CreateModel{AppLabel: "blog", Model: relationMigrationSchema().Models[0]},
	}}
	ctx := &stagedRawCancellationContext{Context: context.Background(), cancelAt: 2}
	fake := &planTestBackend{}
	_, err := (Executor{Backend: fake}).ExecutePlan(ctx, EmptyProjectState(), []Migration{definition}, nil)
	var capability *backend.CapabilityError
	if !errors.Is(err, context.Canceled) || errors.As(err, &capability) || fake.beginCount != 0 || ctx.calls.Load() < 2 {
		t.Fatalf("ExecutePlan post-scan cancellation = error:%v capability:%#v begin:%d calls:%d", err, capability, fake.beginCount, ctx.calls.Load())
	}
}

func TestExecutorRelationStateScalarDefinitionErrorContextIsInputOrderIndependent(t *testing.T) {
	t.Parallel()

	before := relationExecutorProjectState(t)
	alpha := Migration{App: "alpha", Name: "0002", Operations: []Operation{CreateModel{AppLabel: "alpha", Model: stateModel("entry", "alpha_entry")}}}
	zeta := Migration{App: "zeta", Name: "0001", Operations: []Operation{CreateModel{AppLabel: "zeta", Model: stateModel("entry", "zeta_entry")}}}
	for _, definitions := range [][]Migration{{zeta, alpha}, {alpha, zeta}} {
		_, err := (Executor{}).ExecutePlan(context.Background(), before, definitions, nil)
		var migrationError *Error
		if !errors.As(err, &migrationError) || migrationError.Category != CategoryCapability ||
			migrationError.Code != CodeUnsupported || migrationError.App != alpha.App || migrationError.Migration != alpha.Name {
			t.Fatalf("relation-state scalar error context = %#v", err)
		}
	}
}

func TestExecutorExecutePlanRejectsStructuralErrorsBeforeIO(t *testing.T) {
	t.Parallel()

	first, second, _ := planTestMigrations()
	firstKey := first.Key()
	secondKey := second.Key()
	tests := []struct {
		name        string
		definitions []Migration
		plan        []PlanStep
		code        ErrorCode
	}{
		{
			name:        "invalid definition key",
			definitions: []Migration{{App: "", Name: "0001"}},
			plan:        []PlanStep{{Key: firstKey, Direction: DirectionForward}},
			code:        CodeInvalidExecutionPlan,
		},
		{
			name:        "duplicate definition",
			definitions: []Migration{first, first},
			plan:        []PlanStep{{Key: firstKey, Direction: DirectionForward}},
			code:        CodeInvalidExecutionPlan,
		},
		{
			name:        "invalid step key",
			definitions: []Migration{first},
			plan:        []PlanStep{{Key: MigrationKey{Name: "0001"}, Direction: DirectionForward}},
			code:        CodeInvalidExecutionPlan,
		},
		{
			name:        "invalid direction",
			definitions: []Migration{first},
			plan:        []PlanStep{{Key: firstKey, Direction: Direction("sideways")}},
			code:        CodeInvalidExecutionPlan,
		},
		{
			name:        "duplicate step",
			definitions: []Migration{first},
			plan: []PlanStep{
				{Key: firstKey, Direction: DirectionForward},
				{Key: firstKey, Direction: DirectionForward},
			},
			code: CodeInvalidExecutionPlan,
		},
		{
			name:        "missing definition",
			definitions: []Migration{first},
			plan:        []PlanStep{{Key: secondKey, Direction: DirectionForward}},
			code:        CodeInvalidExecutionPlan,
		},
		{
			name:        "mixed directions",
			definitions: []Migration{first, second},
			plan: []PlanStep{
				{Key: firstKey, Direction: DirectionForward},
				{Key: secondKey, Direction: DirectionBackward},
			},
			code: CodeMixedDirections,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := &planTestBackend{}
			before := EmptyProjectState()
			after, err := (Executor{Backend: fake}).ExecutePlan(
				context.Background(),
				before,
				test.definitions,
				test.plan,
			)
			assertMigrationError(t, err, CategoryExecution, test.code, NoOperation, "")
			if fake.beginCount != 0 {
				t.Fatalf("BeginMigration() calls = %d, want 0", fake.beginCount)
			}
			if !after.Equal(before) {
				t.Fatal("structural preflight error changed returned state")
			}
		})
	}
}

func TestExecutorExecutePlanPreflightsEveryStateTransitionBeforeIO(t *testing.T) {
	t.Parallel()

	first, _, _ := planTestMigrations()
	invalidTail := Migration{
		App:  "news",
		Name: "0002_invalid_tail",
		Operations: []Operation{AddField{
			AppLabel:  "news",
			ModelName: "missing",
			Field:     summaryField(),
		}},
	}
	fake := &planTestBackend{}
	before := EmptyProjectState()
	after, err := (Executor{Backend: fake}).ExecutePlan(
		context.Background(),
		before,
		[]Migration{first, invalidTail},
		[]PlanStep{
			{Key: first.Key(), Direction: DirectionForward},
			{Key: invalidTail.Key(), Direction: DirectionForward},
		},
	)
	assertMigrationError(t, err, CategoryState, CodeInvalidState, 0, "AddField")
	if fake.beginCount != 0 {
		t.Fatalf("BeginMigration() calls = %d, want 0", fake.beginCount)
	}
	if !after.Equal(before) {
		t.Fatal("tail state preflight error changed returned state")
	}
}

func TestExecutorExecutePlanRunsForwardAndBackwardInPlanOrder(t *testing.T) {
	t.Parallel()

	first, second, _ := planTestMigrations()
	definitions := []Migration{first, second}
	forwardBackend := newPlanTestBackend(2)
	executor := Executor{Backend: forwardBackend}
	state0 := EmptyProjectState()
	state2, err := executor.ExecutePlan(
		context.Background(),
		state0,
		definitions,
		[]PlanStep{
			{Key: first.Key(), Direction: DirectionForward},
			{Key: second.Key(), Direction: DirectionForward},
		},
	)
	if err != nil {
		t.Fatalf("ExecutePlan(forward) error = %v", err)
	}
	assertPlanTransactionCalls(t, forwardBackend.transactions[0], "create_model", "record_applied:news.0001_article", "commit")
	assertPlanTransactionCalls(t, forwardBackend.transactions[1], "add_field", "record_applied:news.0002_summary", "commit")
	model, exists := state2.Model("news", "article")
	if !exists || len(model.Fields) != 4 || model.Fields[3].Name != "summary" {
		t.Fatalf("forward state model = %#v, exists = %t", model, exists)
	}

	backwardBackend := newPlanTestBackend(2)
	state0Again, err := (Executor{Backend: backwardBackend}).ExecutePlan(
		context.Background(),
		state2,
		definitions,
		[]PlanStep{
			{Key: second.Key(), Direction: DirectionBackward},
			{Key: first.Key(), Direction: DirectionBackward},
		},
	)
	if err != nil {
		t.Fatalf("ExecutePlan(backward) error = %v", err)
	}
	assertPlanTransactionCalls(t, backwardBackend.transactions[0], "remove_field", "record_unapplied:news.0002_summary", "commit")
	assertPlanTransactionCalls(t, backwardBackend.transactions[1], "delete_model", "record_unapplied:news.0001_article", "commit")
	if !state0Again.Equal(state0) {
		t.Fatal("backward result is not the original empty state")
	}
}

func TestExecutorExecutePlanStopsAtFirstFailureAndReturnsLastDurableState(t *testing.T) {
	t.Parallel()

	first, second, third := planTestMigrations()
	failure := errors.New("forced second-step failure")
	fake := newPlanTestBackend(3)
	fake.transactions[1].failures["add_field"] = failure
	state0 := EmptyProjectState()
	stateAfter, err := (Executor{Backend: fake}).ExecutePlan(
		context.Background(),
		state0,
		[]Migration{first, second, third},
		[]PlanStep{
			{Key: first.Key(), Direction: DirectionForward},
			{Key: second.Key(), Direction: DirectionForward},
			{Key: third.Key(), Direction: DirectionForward},
		},
	)
	assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, 0, "AddField")
	if !errors.Is(err, failure) {
		t.Fatalf("ExecutePlan() error = %v, want second-step cause", err)
	}
	wantState, preflightErr := preflightState(state0, first, DirectionForward)
	if preflightErr != nil {
		t.Fatal(preflightErr)
	}
	if !stateAfter.Equal(wantState) {
		t.Fatal("failure did not return the last durable state")
	}
	if fake.beginCount != 2 {
		t.Fatalf("BeginMigration() calls = %d, want 2", fake.beginCount)
	}
	assertPlanTransactionCalls(t, fake.transactions[0], "create_model", "record_applied:news.0001_article", "commit")
	assertPlanTransactionCalls(t, fake.transactions[1], "add_field", "rollback")
	assertPlanTransactionCalls(t, fake.transactions[2])
}

func TestExecutorExecutePlanBackwardFailurePreservesEarlierReverseCommit(t *testing.T) {
	t.Parallel()

	first, second, third := planTestMigrations()
	definitions := []Migration{first, second, third}
	state := EmptyProjectState()
	var err error
	for _, migration := range definitions {
		state, err = preflightState(state, migration, DirectionForward)
		if err != nil {
			t.Fatal(err)
		}
	}
	wantState, err := preflightState(state, third, DirectionBackward)
	if err != nil {
		t.Fatal(err)
	}

	failure := errors.New("forced backward second-step failure")
	fake := newPlanTestBackend(3)
	fake.transactions[1].failures["remove_field"] = failure
	stateAfter, err := (Executor{Backend: fake}).ExecutePlan(
		context.Background(),
		state,
		definitions,
		[]PlanStep{
			{Key: third.Key(), Direction: DirectionBackward},
			{Key: second.Key(), Direction: DirectionBackward},
			{Key: first.Key(), Direction: DirectionBackward},
		},
	)
	assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, 0, "AddField")
	if !errors.Is(err, failure) {
		t.Fatalf("ExecutePlan() error = %v, want backward failure", err)
	}
	if !stateAfter.Equal(wantState) {
		t.Fatal("backward failure did not preserve the earlier reverse commit")
	}
	if fake.beginCount != 2 {
		t.Fatalf("BeginMigration() calls = %d, want 2", fake.beginCount)
	}
	assertPlanTransactionCalls(t, fake.transactions[0], "remove_field", "record_unapplied:news.0003_description", "commit")
	assertPlanTransactionCalls(t, fake.transactions[1], "remove_field", "rollback")
	assertPlanTransactionCalls(t, fake.transactions[2])
}

func TestExecutorExecutePlanCancellationGates(t *testing.T) {
	t.Parallel()

	first, second, _ := planTestMigrations()
	definitions := []Migration{first, second}
	plan := []PlanStep{
		{Key: first.Key(), Direction: DirectionForward},
		{Key: second.Key(), Direction: DirectionForward},
	}

	t.Run("between committed steps", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		fake := newPlanTestBackend(2)
		fake.transactions[0].hooks["commit"] = cancel
		stateAfter, err := (Executor{Backend: fake}).ExecutePlan(ctx, EmptyProjectState(), definitions, plan)
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ExecutePlan() error = %v, want context.Canceled", err)
		}
		want, preflightErr := preflightState(EmptyProjectState(), first, DirectionForward)
		if preflightErr != nil {
			t.Fatal(preflightErr)
		}
		if !stateAfter.Equal(want) {
			t.Fatal("between-step cancellation lost the first durable state")
		}
		if fake.beginCount != 1 {
			t.Fatalf("BeginMigration() calls = %d, want 1", fake.beginCount)
		}
	})

	t.Run("in flight rollback preserves both causes", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		rollbackFailure := errors.New("forced rollback failure")
		fake := newPlanTestBackend(1)
		fake.transactions[0].hooks["create_model"] = cancel
		fake.transactions[0].failures["create_model"] = context.Canceled
		fake.transactions[0].failures["rollback"] = rollbackFailure
		stateAfter, err := (Executor{Backend: fake}).ExecutePlan(
			ctx,
			EmptyProjectState(),
			[]Migration{first},
			plan[:1],
		)
		assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, 0, "CreateModel")
		if !errors.Is(err, context.Canceled) || !errors.Is(err, rollbackFailure) {
			t.Fatalf("ExecutePlan() error = %v, want cancellation and rollback causes", err)
		}
		if !stateAfter.Equal(EmptyProjectState()) {
			t.Fatal("in-flight cancellation changed returned state")
		}
		if fake.transactions[0].rollbackContextErr != nil {
			t.Fatalf("Rollback() context error = %v, want nil cleanup context", fake.transactions[0].rollbackContextErr)
		}
	})

	t.Run("late cancellation after final commit is success", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		fake := newPlanTestBackend(1)
		fake.transactions[0].hooks["commit"] = cancel
		stateAfter, err := (Executor{Backend: fake}).ExecutePlan(
			ctx,
			EmptyProjectState(),
			[]Migration{first},
			plan[:1],
		)
		if err != nil {
			t.Fatalf("ExecutePlan() error = %v, want success after committed final step", err)
		}
		if ctx.Err() != context.Canceled {
			t.Fatalf("context error = %v, want canceled", ctx.Err())
		}
		if _, exists := stateAfter.Model("news", "article"); !exists {
			t.Fatal("late-cancel success did not return committed state")
		}
	})
}

func TestExecutorExecutePlanSnapshotsCallerPlanAndDefinitions(t *testing.T) {
	t.Parallel()

	first, second, _ := planTestMigrations()
	definitions := []Migration{first, second}
	plan := []PlanStep{
		{Key: first.Key(), Direction: DirectionForward},
		{Key: second.Key(), Direction: DirectionForward},
	}
	fake := newPlanTestBackend(2)
	fake.transactions[0].hooks["commit"] = func() {
		mutated := definitions[1].Operations[0].(AddField)
		mutated.ModelName = "missing"
		mutated.Field.Default = &ir.ScalarDefault{Kind: ir.ScalarString, String: "mutated"}
		definitions[1].Operations[0] = mutated
		plan[1] = PlanStep{Key: first.Key(), Direction: DirectionBackward}
	}
	stateAfter, err := (Executor{Backend: fake}).ExecutePlan(
		context.Background(),
		EmptyProjectState(),
		definitions,
		plan,
	)
	if err != nil {
		t.Fatalf("ExecutePlan() error after caller mutation = %v", err)
	}
	model, exists := stateAfter.Model("news", "article")
	if !exists || len(model.Fields) != 4 || model.Fields[3].Name != "summary" || model.Fields[3].Default != nil {
		t.Fatalf("snapshotted result model = %#v, exists = %t", model, exists)
	}
}

func TestCloneMigrationDefinitionsDeepCopiesDependenciesAndPointerOperationIR(t *testing.T) {
	t.Parallel()

	dependency := MigrationKey{App: "news", Name: "0001_root"}
	create := &CreateModel{AppLabel: "news", Model: ir.Model{
		Name: "article", GoName: "Article", DBTable: "news_article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 64,
				Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: "original"},
				Relation: &ir.ForeignKeyRelation{
					Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
					Cardinality: ir.RelationManyToOne,
					Reverse:     ir.ReverseRelation{Name: "articles"},
					OnDelete:    ir.DeleteProtect,
				}},
		},
	}}
	add := &AddField{AppLabel: "news", ModelName: "article", Field: ir.Field{
		Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean,
		Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false},
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "published_articles"},
			OnDelete:    ir.DeleteProtect,
		},
	}}
	definitions := []Migration{
		{App: "news", Name: "0002_article", Dependencies: []MigrationKey{dependency}, Operations: []Operation{create, add}},
	}

	snapshot := cloneMigrationDefinitions(definitions)
	definitions[0].Dependencies[0] = MigrationKey{App: "mutated", Name: "mutated"}
	create.Model.Name = "mutated"
	create.Model.Fields[1].Name = "mutated"
	create.Model.Fields[1].Default.String = "mutated"
	create.Model.Fields[1].Relation.Target.AppLabel = "mutated"
	create.Model.Fields[1].Relation.Reverse.Name = "mutated"
	create.Model.Fields = nil
	add.ModelName = "mutated"
	add.Field.Name = "mutated"
	add.Field.Default.Boolean = true
	add.Field.Relation.Target.AppLabel = "mutated"
	add.Field.Relation.Reverse.Name = "mutated"
	definitions[0].Operations = nil

	if !reflect.DeepEqual(snapshot[0].Dependencies, []MigrationKey{dependency}) {
		t.Fatalf("snapshotted dependencies = %v, want %v", snapshot[0].Dependencies, []MigrationKey{dependency})
	}
	var clonedCreate CreateModel
	switch operation := snapshot[0].Operations[0].(type) {
	case CreateModel:
		clonedCreate = operation
	case *CreateModel:
		if operation == create {
			t.Fatal("snapshotted CreateModel retained the caller's operation pointer")
		}
		clonedCreate = *operation
	default:
		t.Fatalf("snapshotted pointer CreateModel has type %T", operation)
	}
	if clonedCreate.Model.Name != "article" || len(clonedCreate.Model.Fields) != 2 ||
		clonedCreate.Model.Fields[1].Name != "title" || clonedCreate.Model.Fields[1].Default == nil ||
		clonedCreate.Model.Fields[1].Default.String != "original" || clonedCreate.Model.Fields[1].Relation == nil ||
		clonedCreate.Model.Fields[1].Relation.Target.AppLabel != "authors" ||
		clonedCreate.Model.Fields[1].Relation.Reverse.Name != "articles" {
		t.Fatalf("snapshotted pointer CreateModel = %#v", snapshot[0].Operations[0])
	}
	var clonedAdd AddField
	switch operation := snapshot[0].Operations[1].(type) {
	case AddField:
		clonedAdd = operation
	case *AddField:
		if operation == add {
			t.Fatal("snapshotted AddField retained the caller's operation pointer")
		}
		clonedAdd = *operation
	default:
		t.Fatalf("snapshotted pointer AddField has type %T", operation)
	}
	if clonedAdd.ModelName != "article" || clonedAdd.Field.Name != "published" ||
		clonedAdd.Field.Default == nil || clonedAdd.Field.Default.Boolean || clonedAdd.Field.Relation == nil ||
		clonedAdd.Field.Relation.Target.AppLabel != "authors" ||
		clonedAdd.Field.Relation.Reverse.Name != "published_articles" {
		t.Fatalf("snapshotted pointer AddField = %#v", snapshot[0].Operations[1])
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func planTestMigrations() (Migration, Migration, Migration) {
	first := Migration{
		App:        "news",
		Name:       "0001_article",
		Operations: []Operation{CreateModel{AppLabel: "news", Model: articleSchema().Models[0]}},
	}
	second := Migration{
		App:        "news",
		Name:       "0002_summary",
		Operations: []Operation{AddField{AppLabel: "news", ModelName: "article", Field: summaryField()}},
	}
	thirdField := summaryField()
	thirdField.Name = "description"
	thirdField.GoName = "Description"
	thirdField.Column = "description"
	third := Migration{
		App:        "news",
		Name:       "0003_description",
		Operations: []Operation{AddField{AppLabel: "news", ModelName: "article", Field: thirdField}},
	}
	return first, second, third
}

type planTestBackend struct {
	transactions []*planTestTransaction
	beginCount   int
}

func newPlanTestBackend(transactionCount int) *planTestBackend {
	transactions := make([]*planTestTransaction, transactionCount)
	for index := range transactions {
		transactions[index] = &planTestTransaction{
			failures: make(map[string]error),
			hooks:    make(map[string]func()),
		}
	}
	return &planTestBackend{transactions: transactions}
}

func (b *planTestBackend) BeginMigration(context.Context) (backend.Transaction, error) {
	if b.beginCount >= len(b.transactions) {
		return nil, fmt.Errorf("unexpected BeginMigration call %d", b.beginCount)
	}
	transaction := b.transactions[b.beginCount]
	b.beginCount++
	return transaction, nil
}

type planTestTransaction struct {
	calls              []string
	failures           map[string]error
	hooks              map[string]func()
	rollbackContextErr error
}

func (t *planTestTransaction) CreateModel(ctx context.Context, _ ir.Model) error {
	return t.call(ctx, "create_model")
}

func (t *planTestTransaction) DeleteModel(ctx context.Context, _ ir.Model) error {
	return t.call(ctx, "delete_model")
}

func (t *planTestTransaction) AddField(ctx context.Context, _ ir.Model, _ ir.Field) error {
	return t.call(ctx, "add_field")
}

func (t *planTestTransaction) RemoveField(ctx context.Context, _ ir.Model, _ ir.Field) error {
	return t.call(ctx, "remove_field")
}

func (t *planTestTransaction) RecordApplied(ctx context.Context, app, name string) error {
	return t.call(ctx, fmt.Sprintf("record_applied:%s.%s", app, name))
}

func (t *planTestTransaction) RecordUnapplied(ctx context.Context, app, name string) error {
	return t.call(ctx, fmt.Sprintf("record_unapplied:%s.%s", app, name))
}

func (t *planTestTransaction) Commit(ctx context.Context) error {
	return t.call(ctx, "commit")
}

func (t *planTestTransaction) Rollback(ctx context.Context) error {
	t.rollbackContextErr = ctx.Err()
	return t.call(ctx, "rollback")
}

func (t *planTestTransaction) call(_ context.Context, name string) error {
	t.calls = append(t.calls, name)
	if hook := t.hooks[name]; hook != nil {
		hook()
	}
	if failure := t.failures[name]; failure != nil {
		return failure
	}
	return nil
}

func assertPlanTransactionCalls(t *testing.T, transaction *planTestTransaction, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(transaction.calls, want) {
		t.Fatalf("transaction calls = %v, want %v", transaction.calls, want)
	}
}
