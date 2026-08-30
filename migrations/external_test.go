package migrations_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

type externalHiddenCreateModel struct {
	migrations.CreateModel
}

type externalNestedCreateModel struct {
	externalHiddenCreateModel
}

type externalNestedAddField struct {
	migrations.AddField
}

type externalShadowedOperation struct {
	migrations.CreateModel
	externalNestedAddField
}

func TestExternalConsumerSeesOneCurrentMigrationStateVersion(t *testing.T) {
	t.Parallel()

	type consumerVersion int
	const version consumerVersion = migrations.StateFormatVersion
	if migrations.StateFormatVersion != 1 || version != 1 {
		t.Fatalf("migration state version = %d/%d", migrations.StateFormatVersion, version)
	}
}

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

func TestExternalConsumerCanReconstructHistoricalProjectState(t *testing.T) {
	t.Parallel()

	initial := migrations.MigrationKey{App: "news", Name: "0001_initial"}
	second := migrations.MigrationKey{App: "news", Name: "0002_summary"}
	model := ir.Model{
		Name:    "article",
		GoName:  "Article",
		DBTable: "news_article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
		},
	}
	reconstructor, err := migrations.NewStateReconstructor(
		migrations.Migration{
			App: initial.App, Name: initial.Name,
			Operations: []migrations.Operation{migrations.CreateModel{AppLabel: initial.App, Model: model}},
		},
		migrations.Migration{
			App: second.App, Name: second.Name,
			Dependencies: []migrations.MigrationKey{initial},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel: second.App, ModelName: "article",
				Field: ir.Field{Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200},
			}},
		},
	)
	if err != nil {
		t.Fatalf("NewStateReconstructor() error = %v", err)
	}

	before, err := reconstructor.Reconstruct(migrations.BeforeStateRequest(second))
	if err != nil {
		t.Fatalf("Reconstruct(before) error = %v", err)
	}
	beforeModel, exists := before.Model("news", "article")
	if !exists || len(beforeModel.Fields) != 1 {
		t.Fatalf("before model = %#v, exists=%v", beforeModel, exists)
	}

	after, err := reconstructor.Reconstruct(migrations.AfterStateRequest(second, initial))
	if err != nil {
		t.Fatalf("Reconstruct(after reversed targets) error = %v", err)
	}
	afterModel, exists := after.Model("news", "article")
	if !exists || len(afterModel.Fields) != 2 || afterModel.Fields[1].Name != "summary" {
		t.Fatalf("after model = %#v, exists=%v", afterModel, exists)
	}

	applied, err := migrations.NewAppliedState(initial)
	if err != nil {
		t.Fatalf("NewAppliedState() error = %v", err)
	}
	appliedState, err := reconstructor.Reconstruct(migrations.AppliedStateRequest(applied))
	if err != nil {
		t.Fatalf("Reconstruct(applied) error = %v", err)
	}
	appliedModel, exists := appliedState.Model("news", "article")
	if !exists || len(appliedModel.Fields) != 1 {
		t.Fatalf("applied model = %#v, exists=%v", appliedModel, exists)
	}

	latest, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatalf("Reconstruct(latest) error = %v", err)
	}
	if !latest.Equal(after) {
		t.Fatalf("latest = %#v, after = %#v", latest, after)
	}

	empty, err := reconstructor.Reconstruct(migrations.EmptyStateRequest())
	if err != nil {
		t.Fatalf("Reconstruct(empty) error = %v", err)
	}
	if len(empty.Apps()) != 0 {
		t.Fatalf("empty apps = %v", empty.Apps())
	}
}

func TestExternalConsumerUsesCurrentRelationStateButRawExecutionFailsClosed(t *testing.T) {
	t.Parallel()

	field := ir.Field{
		Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "posts"},
			OnDelete:    ir.DeleteProtect,
		},
	}
	model := ir.Model{
		Name: "post", GoName: "Post", DBTable: "blog_post",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}, field},
	}
	state, err := migrations.NewProjectState(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "blog",
		Models:        []ir.Model{model},
	})
	if err != nil || state.FormatVersion() != migrations.StateFormatVersion {
		t.Fatalf("NewProjectState(current relation) = state:%#v err:%v", state, err)
	}

	relationMigration := migrations.Migration{
		App: "blog", Name: "0001_post",
		Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "blog", Model: model}},
	}
	targetMigration := migrations.Migration{
		App: "authors", Name: "0001_author",
		Operations: []migrations.Operation{migrations.CreateModel{
			AppLabel: "authors",
			Model: ir.Model{
				Name: "author", GoName: "Author", DBTable: "authors_author",
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
			},
		}},
	}
	relationMigration.Dependencies = []migrations.MigrationKey{targetMigration.Key()}
	reconstructor, err := migrations.NewStateReconstructor(targetMigration, relationMigration)
	if err != nil {
		t.Fatalf("NewStateReconstructor(relation): %v", err)
	}
	reconstructed, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatalf("Reconstruct(relation): %v", err)
	}
	reconstructedModel, exists := reconstructed.Model("blog", "post")
	if !exists || len(reconstructedModel.Fields) != 2 || reconstructedModel.Fields[1].Relation == nil {
		t.Fatalf("reconstructed current relation = %#v/%t", reconstructedModel, exists)
	}

	_, err = (migrations.DirectExecutor{}).Apply(
		context.Background(), migrations.EmptyProjectState(), relationMigration,
	)
	var migrationError *migrations.Error
	var capabilityError *backend.CapabilityError
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryCapability ||
		migrationError.Code != migrations.CodeUnsupported || migrationError.OperationIndex != migrations.NoOperation ||
		!errors.As(err, &capabilityError) || capabilityError.Feature != "relation_migration" {
		t.Fatalf("DirectExecutor.Apply(raw relation) error = %#v capability=%#v", err, capabilityError)
	}
}

func TestExternalNestedHiddenOperationCannotBypassRawRelationBoundary(t *testing.T) {
	t.Parallel()

	relationModel := ir.Model{
		Name: "post", GoName: "Post", DBTable: "blog_post",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{
				Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
				Relation: &ir.ForeignKeyRelation{
					Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Cardinality: ir.RelationManyToOne,
					Reverse: ir.ReverseRelation{Name: "posts"}, OnDelete: ir.DeleteProtect,
				},
			},
		},
	}
	wrappedRelation := externalNestedCreateModel{externalHiddenCreateModel{migrations.CreateModel{AppLabel: "blog", Model: relationModel}}}
	_, err := (migrations.DirectExecutor{}).Apply(context.Background(), migrations.EmptyProjectState(), migrations.Migration{
		App: "blog", Name: "0001_relation", Operations: []migrations.Operation{wrappedRelation},
	})
	var migrationError *migrations.Error
	var capability *backend.CapabilityError
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryCapability ||
		migrationError.Code != migrations.CodeUnsupported || !errors.As(err, &capability) || capability.Feature != "relation_migration" {
		t.Fatalf("nested hidden relation wrapper error = %#v capability=%#v", err, capability)
	}

	scalarModel := relationModel.Clone()
	scalarModel.Fields = scalarModel.Fields[:1]
	wrappedScalar := externalNestedCreateModel{externalHiddenCreateModel{migrations.CreateModel{AppLabel: "blog", Model: scalarModel}}}
	_, err = (migrations.DirectExecutor{}).Apply(context.Background(), migrations.EmptyProjectState(), migrations.Migration{
		App: "blog", Name: "0001_scalar", Operations: []migrations.Operation{wrappedScalar},
	})
	migrationError = nil
	capability = nil
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryTransaction ||
		migrationError.Code != migrations.CodeBeginFailed || errors.As(err, &capability) {
		t.Fatalf("nested hidden scalar wrapper error = %#v capability=%#v", err, capability)
	}
}

func TestExternalShadowedRelationUsesEffectiveScalarOperation(t *testing.T) {
	t.Parallel()

	scalar := ir.Model{
		Name: "post", GoName: "Post", DBTable: "blog_post",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
	relation := ir.Field{
		Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Cardinality: ir.RelationManyToOne,
			Reverse: ir.ReverseRelation{Name: "posts"}, OnDelete: ir.DeleteProtect,
		},
	}
	operation := externalShadowedOperation{
		CreateModel: migrations.CreateModel{AppLabel: "blog", Model: scalar},
		externalNestedAddField: externalNestedAddField{AddField: migrations.AddField{
			AppLabel: "blog", ModelName: "post", Field: relation,
		}},
	}
	_, err := (migrations.DirectExecutor{}).Apply(context.Background(), migrations.EmptyProjectState(), migrations.Migration{
		App: "blog", Name: "0001_shadowed", Operations: []migrations.Operation{operation},
	})
	var migrationError *migrations.Error
	var capability *backend.CapabilityError
	if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryTransaction ||
		migrationError.Code != migrations.CodeBeginFailed || errors.As(err, &capability) {
		t.Fatalf("shadowed external relation error = %#v capability=%#v", err, capability)
	}
}

func TestExternalConsumerCanInspectInvalidStateRequest(t *testing.T) {
	t.Parallel()

	var reconstructor migrations.StateReconstructor
	_, err := reconstructor.Reconstruct(migrations.StateRequest{})
	var planningError *migrations.PlanningError
	if !errors.As(err, &planningError) {
		t.Fatalf("Reconstruct(zero request) error = %#v, want *migrations.PlanningError", err)
	}
	if planningError.Category != migrations.CategoryPlan || planningError.Code != migrations.CodeInvalidTarget {
		t.Fatalf("Reconstruct(zero request) error = %#v", planningError)
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

	_, err = planner.Plan(applied, migrations.KnownAppZeroTarget("unknown"))
	var planningError *migrations.PlanningError
	if !errors.As(err, &planningError) ||
		planningError.Category != migrations.CategoryPlan ||
		planningError.Code != migrations.CodeTargetNotFound ||
		planningError.Node != (migrations.MigrationKey{App: "unknown"}) {
		t.Fatalf("strict zero Planner.Plan() error = %#v", err)
	}

	initial := migrations.MigrationKey{App: "news", Name: "0001_initial"}
	knownPlanner, err := migrations.NewPlanner(migrations.Migration{App: initial.App, Name: initial.Name})
	if err != nil {
		t.Fatalf("NewPlanner(known app) error = %v", err)
	}
	knownApplied, err := migrations.NewAppliedState(initial)
	if err != nil {
		t.Fatalf("NewAppliedState(known app) error = %v", err)
	}
	legacyPlan, err := knownPlanner.Plan(knownApplied, migrations.ZeroTarget(initial.App))
	if err != nil {
		t.Fatalf("legacy known zero error = %v", err)
	}
	strictPlan, err := knownPlanner.Plan(knownApplied, migrations.KnownAppZeroTarget(initial.App))
	if err != nil {
		t.Fatalf("strict known zero error = %v", err)
	}
	if !reflect.DeepEqual(strictPlan, legacyPlan) {
		t.Fatalf("strict known zero plan = %v, legacy plan = %v", strictPlan, legacyPlan)
	}
}

func TestExternalConsumerCanInspectMigrationStatusesWithoutMutableAliases(t *testing.T) {
	t.Parallel()

	initial := migrations.MigrationKey{App: "news", Name: "0001_initial"}
	second := migrations.MigrationKey{App: "news", Name: "0002_second"}
	missing := migrations.MigrationKey{App: "news", Name: "0000_removed"}
	planner, err := migrations.NewPlanner(
		migrations.Migration{App: second.App, Name: second.Name, Dependencies: []migrations.MigrationKey{initial}},
		migrations.Migration{App: initial.App, Name: initial.Name},
	)
	if err != nil {
		t.Fatalf("NewPlanner() error = %v", err)
	}
	applied, err := migrations.NewAppliedState(initial, missing)
	if err != nil {
		t.Fatalf("NewAppliedState() error = %v", err)
	}
	want := []migrations.MigrationStatusEntry{
		{Key: initial, Status: migrations.MigrationStatusApplied},
		{Key: second, Status: migrations.MigrationStatusUnapplied},
		{Key: missing, Status: migrations.MigrationStatusDefinitionMissing},
	}
	got, err := planner.Statuses(applied)
	if err != nil {
		t.Fatalf("Statuses() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Statuses() = %v, want %v", got, want)
	}
	if string(migrations.MigrationStatusApplied) != "applied" ||
		string(migrations.MigrationStatusUnapplied) != "unapplied" ||
		string(migrations.MigrationStatusDefinitionMissing) != "definition-missing" {
		t.Fatalf("migration status vocabulary changed")
	}

	got[0] = migrations.MigrationStatusEntry{
		Key:    migrations.MigrationKey{App: "mutated", Name: "mutated"},
		Status: migrations.MigrationStatusUnapplied,
	}
	again, err := planner.Statuses(applied)
	if err != nil {
		t.Fatalf("Statuses() after mutation error = %v", err)
	}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("Statuses() after caller mutation = %v, want %v", again, want)
	}
}

func TestExternalConsumerDistinguishesZeroAndLoadedEmptyDefinitionStatusAuthority(t *testing.T) {
	t.Parallel()

	var zero migrations.LoadedDefinitionSet
	statuses, err := zero.Statuses(migrations.AppliedState{})
	if statuses != nil {
		t.Fatalf("zero LoadedDefinitionSet.Statuses() = %v, want nil", statuses)
	}
	var migrationError *migrations.Error
	if !errors.As(err, &migrationError) ||
		migrationError.Category != migrations.CategoryState ||
		migrationError.Code != migrations.CodeInvalidState {
		t.Fatalf("zero LoadedDefinitionSet.Statuses() error = %#v, want state/invalid_state", err)
	}

	loaded, report, err := definition.Load()
	if err != nil {
		t.Fatalf("definition.Load(empty) error = %v", err)
	}
	if report.DefinitionSetsPublished != 1 {
		t.Fatalf("definition.Load(empty) report = %+v, want one published set", report)
	}
	statuses, err = loaded.Statuses(migrations.AppliedState{})
	if err != nil {
		t.Fatalf("loaded empty Statuses() error = %v", err)
	}
	if len(statuses) != 0 {
		t.Fatalf("loaded empty Statuses() = %v, want empty", statuses)
	}
}

func TestExternalConsumerCanLoadAndCheckAppliedMigrationHistory(t *testing.T) {
	t.Parallel()

	initial := migrations.MigrationKey{App: "news", Name: "0001_initial"}
	second := migrations.MigrationKey{App: "news", Name: "0002_second"}
	reader := &externalHistoryReader{records: []backend.AppliedMigration{{App: initial.App, Name: initial.Name}}}
	applied, err := migrations.LoadAppliedState(context.Background(), reader)
	if err != nil {
		t.Fatalf("LoadAppliedState() error = %v", err)
	}
	planner, err := migrations.NewPlanner(
		migrations.Migration{App: initial.App, Name: initial.Name},
		migrations.Migration{App: second.App, Name: second.Name, Dependencies: []migrations.MigrationKey{initial}},
	)
	if err != nil {
		t.Fatalf("NewPlanner() error = %v", err)
	}
	if err := planner.CheckHistory(applied); err != nil {
		t.Fatalf("CheckHistory() error = %v", err)
	}
	plan, err := planner.Plan(applied, migrations.NamedTarget(second))
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	want := []migrations.PlanStep{{Key: second, Direction: migrations.DirectionForward}}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("Plan() = %v, want %v", plan, want)
	}
}

func TestExternalConsumerCanInspectRecorderReadError(t *testing.T) {
	t.Parallel()

	cause := errors.New("external read sentinel")
	_, err := migrations.LoadAppliedState(context.Background(), &externalHistoryReader{err: cause})
	var recorderError *migrations.RecorderError
	if !errors.As(err, &recorderError) {
		t.Fatalf("LoadAppliedState() error = %#v, want *migrations.RecorderError", err)
	}
	if recorderError.Category != migrations.CategoryRecorder || recorderError.Code != migrations.CodeReadFailed {
		t.Fatalf("recorder error = %#v", recorderError)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("LoadAppliedState() error = %v, want cause %v", err, cause)
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
	state, err := (migrations.DirectExecutor{Backend: external}).Apply(
		context.Background(),
		migrations.EmptyProjectState(),
		migration,
	)
	if err != nil {
		t.Fatalf("external DirectExecutor.Apply() error = %v", err)
	}
	if _, exists := state.Model("news", "article"); !exists {
		t.Fatal("external DirectExecutor.Apply() did not return the applied model state")
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
	state, err := (migrations.DirectExecutor{Backend: external}).ExecutePlan(
		context.Background(),
		migrations.EmptyProjectState(),
		[]migrations.Migration{initial},
		[]migrations.PlanStep{{Key: initial.Key(), Direction: migrations.DirectionForward}},
	)
	if err != nil {
		t.Fatalf("external DirectExecutor.ExecutePlan() error = %v", err)
	}
	if _, exists := state.Model("news", "article"); !exists {
		t.Fatal("external DirectExecutor.ExecutePlan() did not return the applied model state")
	}

	second := migrations.Migration{App: "news", Name: "0002_second"}
	_, err = (migrations.DirectExecutor{Backend: external}).ExecutePlan(
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

var _ backend.AtomicBackend = (*externalBackend)(nil)
var _ backend.Transaction = (*externalTransaction)(nil)
var _ backend.AppliedMigrationReader = (*externalHistoryReader)(nil)

type externalHistoryReader struct {
	records []backend.AppliedMigration
	err     error
}

func (r *externalHistoryReader) ReadAppliedMigrations(context.Context) ([]backend.AppliedMigration, error) {
	return r.records, r.err
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
