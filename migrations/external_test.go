package migrations_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestExternalConsumerCanConstructAndRunMigrationPlanner(t *testing.T) {
	t.Parallel()

	initial := migrations.MigrationKey{App: "news", Name: "0001_initial"}
	second := migrations.MigrationKey{App: "news", Name: "0002_second"}
	planner, err := migrations.NewPlanner(
		migrations.Migration{App: initial.App, Name: initial.Name},
		migrations.Migration{App: second.App, Name: second.Name, Dependencies: []migrations.MigrationKey{initial}},
	)
	if err != nil {
		t.Fatalf("NewPlanner() error = %v", err)
	}
	applied, err := migrations.NewAppliedState()
	if err != nil {
		t.Fatalf("NewAppliedState() error = %v", err)
	}
	got, err := planner.Plan(applied, migrations.NamedTarget(second))
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	want := []migrations.PlanStep{
		{Key: initial, Direction: migrations.DirectionForward},
		{Key: second, Direction: migrations.DirectionForward},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Plan() = %v, want %v", got, want)
	}
	if key := (migrations.Migration{App: second.App, Name: second.Name}).Key(); key != second {
		t.Fatalf("Migration.Key() = %v, want %v", key, second)
	}
}

func TestExternalConsumerCanInspectPlanningErrorWithoutMutableAliases(t *testing.T) {
	t.Parallel()

	left := migrations.MigrationKey{App: "left", Name: "0001"}
	right := migrations.MigrationKey{App: "right", Name: "0001"}
	_, err := migrations.NewPlanner(
		migrations.Migration{App: left.App, Name: left.Name, Dependencies: []migrations.MigrationKey{right}},
		migrations.Migration{App: right.App, Name: right.Name, Dependencies: []migrations.MigrationKey{left}},
	)
	var planningError *migrations.PlanningError
	if !errors.As(err, &planningError) {
		t.Fatalf("NewPlanner() error = %#v, want *migrations.PlanningError", err)
	}
	if planningError.Category != migrations.CategoryGraph || planningError.Code != migrations.CodeDependencyCycle {
		t.Fatalf("planning error = %#v", planningError)
	}
	want := []migrations.MigrationKey{left, right}
	members := planningError.Members()
	if !reflect.DeepEqual(members, want) {
		t.Fatalf("Members() = %v, want %v", members, want)
	}
	members[0] = migrations.MigrationKey{App: "mutated", Name: "mutated"}
	if got := planningError.Members(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Members() after mutation = %v, want %v", got, want)
	}
}

func TestExternalConsumerZeroPlannerAndAppliedStateAreValid(t *testing.T) {
	t.Parallel()

	var planner migrations.Planner
	var applied migrations.AppliedState
	plan, err := planner.Plan(applied, migrations.ZeroTarget("unknown"))
	if err != nil {
		t.Fatalf("zero Planner.Plan() error = %v", err)
	}
	if len(plan) != 0 {
		t.Fatalf("zero Planner.Plan() = %v, want empty", plan)
	}
}

func TestExternalConsumerCanConstructBuiltInMigration(t *testing.T) {
	t.Parallel()

	model := ir.Model{
		Name:    "article",
		GoName:  "Article",
		DBTable: "news_article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
		},
	}
	migration := migrations.Migration{
		App:  "news",
		Name: "0001_article",
		Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "news", Model: model},
			migrations.AddField{
				AppLabel:  "news",
				ModelName: "article",
				Field: ir.Field{
					Name:      "summary",
					GoName:    "Summary",
					Column:    "summary",
					Kind:      ir.FieldChar,
					Nullable:  true,
					MaxLength: 200,
				},
			},
		},
	}
	if len(migration.Operations) != 2 {
		t.Fatalf("operations = %d, want 2", len(migration.Operations))
	}
	external := &externalBackend{transaction: &externalTransaction{}}
	state, err := (migrations.Executor{Backend: external}).Apply(
		context.Background(),
		migrations.EmptyProjectState(),
		migration,
	)
	if err != nil {
		t.Fatalf("external Executor.Apply() error = %v", err)
	}
	if _, exists := state.Model("news", "article"); !exists {
		t.Fatal("external Executor.Apply() did not return the applied model state")
	}
}

func TestExternalConsumerCanExecuteAndInspectMigrationPlan(t *testing.T) {
	t.Parallel()

	model := ir.Model{
		Name:    "article",
		GoName:  "Article",
		DBTable: "news_article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
		},
	}
	initial := migrations.Migration{
		App:        "news",
		Name:       "0001_initial",
		Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "news", Model: model}},
	}
	external := &externalBackend{transaction: &externalTransaction{}}
	state, err := (migrations.Executor{Backend: external}).ExecutePlan(
		context.Background(),
		migrations.EmptyProjectState(),
		[]migrations.Migration{initial},
		[]migrations.PlanStep{{Key: initial.Key(), Direction: migrations.DirectionForward}},
	)
	if err != nil {
		t.Fatalf("external Executor.ExecutePlan() error = %v", err)
	}
	if _, exists := state.Model("news", "article"); !exists {
		t.Fatal("external Executor.ExecutePlan() did not return the applied model state")
	}

	second := migrations.Migration{App: "news", Name: "0002_second"}
	_, err = (migrations.Executor{Backend: external}).ExecutePlan(
		context.Background(),
		migrations.EmptyProjectState(),
		[]migrations.Migration{initial, second},
		[]migrations.PlanStep{
			{Key: initial.Key(), Direction: migrations.DirectionForward},
			{Key: second.Key(), Direction: migrations.DirectionBackward},
		},
	)
	var executionError *migrations.Error
	if !errors.As(err, &executionError) {
		t.Fatalf("mixed ExecutePlan() error = %#v, want *migrations.Error", err)
	}
	if executionError.Category != migrations.CategoryExecution || executionError.Code != migrations.CodeMixedDirections {
		t.Fatalf("mixed ExecutePlan() error = %#v", executionError)
	}
}

type externalBackend struct {
	transaction *externalTransaction
}

func (b *externalBackend) BeginMigration(context.Context) (backend.Transaction, error) {
	return b.transaction, nil
}

type externalTransaction struct{}

func (*externalTransaction) CreateModel(context.Context, ir.Model) error           { return nil }
func (*externalTransaction) DeleteModel(context.Context, ir.Model) error           { return nil }
func (*externalTransaction) AddField(context.Context, ir.Model, ir.Field) error    { return nil }
func (*externalTransaction) RemoveField(context.Context, ir.Model, ir.Field) error { return nil }
func (*externalTransaction) RecordApplied(context.Context, string, string) error   { return nil }
func (*externalTransaction) RecordUnapplied(context.Context, string, string) error { return nil }
func (*externalTransaction) Commit(context.Context) error                          { return nil }
func (*externalTransaction) Rollback(context.Context) error                        { return nil }
