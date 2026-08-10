package migrations

import (
	"errors"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

var (
	stateAlphaRoot   = MigrationKey{App: "alpha", Name: "0002_root"}
	stateAlphaMiddle = MigrationKey{App: "alpha", Name: "0001_middle"}
	stateAlphaLeaf   = MigrationKey{App: "alpha", Name: "0003_leaf"}
	stateBetaRoot    = MigrationKey{App: "beta", Name: "0001_root"}
	stateGammaRoot   = MigrationKey{App: "gamma", Name: "0001_root"}
	stateDeltaRoot   = MigrationKey{App: "delta", Name: "0001_root"}
	stateLegacy      = MigrationKey{App: "legacy", Name: "0099_retired"}
)

func TestStateReconstructorHistoricalStateContracts(t *testing.T) {
	t.Parallel()

	reconstructor := mustStateReconstructor(t, stateFixtureDefinitions()...)

	t.Run("MIG-037 explicit empty", func(t *testing.T) {
		assertStateApps(t, reconstructState(t, reconstructor, EmptyStateRequest()))
	})

	t.Run("MIG-038 root before", func(t *testing.T) {
		assertStateApps(t, reconstructState(t, reconstructor, BeforeStateRequest(stateAlphaRoot)))
	})

	t.Run("MIG-039 root after", func(t *testing.T) {
		state := reconstructState(t, reconstructor, AfterStateRequest(stateAlphaRoot))
		assertStateApps(t, state, "alpha")
		assertStateFields(t, state, "alpha", "entry", "id", "headline")
		assertStateFields(t, state, "alpha", "zulu", "id", "active")
	})

	t.Run("MIG-040 middle after dependency order", func(t *testing.T) {
		state := reconstructState(t, reconstructor, AfterStateRequest(stateAlphaMiddle))
		assertStateFields(t, state, "alpha", "entry", "id", "headline", "published")
		field := mustStateField(t, state, "alpha", "entry", "published")
		if field.Default == nil || field.Default.Kind != ir.ScalarBoolean || field.Default.Boolean {
			t.Fatalf("published default = %#v, want explicit false", field.Default)
		}
	})

	t.Run("MIG-041 middle before", func(t *testing.T) {
		state := reconstructState(t, reconstructor, BeforeStateRequest(stateAlphaMiddle))
		assertStateFields(t, state, "alpha", "entry", "id", "headline")
	})

	t.Run("MIG-042 cross app prerequisite", func(t *testing.T) {
		state := reconstructState(t, reconstructor, AfterStateRequest(stateBetaRoot))
		assertStateApps(t, state, "alpha", "beta")
		assertStateFields(t, state, "beta", "audit", "id", "code")
	})

	t.Run("MIG-043 target union shared dependency", func(t *testing.T) {
		state := reconstructState(t, reconstructor, AfterStateRequest(stateBetaRoot, stateGammaRoot))
		assertStateApps(t, state, "alpha", "beta", "gamma")
		assertStateFields(t, state, "alpha", "entry", "id", "headline")
	})

	t.Run("MIG-044 latest same app leaves", func(t *testing.T) {
		state := reconstructState(t, reconstructor, LatestStateRequest())
		assertStateApps(t, state, "alpha", "beta", "delta", "gamma")
		assertStateFields(t, state, "alpha", "entry", "id", "headline", "published", "summary")
	})

	t.Run("MIG-045 applied known prefix", func(t *testing.T) {
		state := reconstructState(t, reconstructor, AppliedStateRequest(mustApplied(t, stateAlphaRoot, stateAlphaMiddle)))
		assertStateApps(t, state, "alpha")
		assertStateFields(t, state, "alpha", "entry", "id", "headline", "published")
	})

	t.Run("MIG-046 unrelated known and unknown", func(t *testing.T) {
		state := reconstructState(t, reconstructor, AppliedStateRequest(mustApplied(t, stateAlphaRoot, stateDeltaRoot, stateLegacy)))
		assertStateApps(t, state, "alpha", "delta")
		if _, exists := state.Schema("legacy"); exists {
			t.Fatal("unknown legacy identity materialized schema state")
		}
	})
}

func TestStateReconstructorTargetClosureUnionDoesNotUseSequentialRollbackSemantics(t *testing.T) {
	t.Parallel()

	reconstructor := mustStateReconstructor(t, stateFixtureDefinitions()...)
	want := reconstructState(t, reconstructor, AfterStateRequest(stateAlphaLeaf))
	requests := []StateRequest{
		AfterStateRequest(stateAlphaRoot, stateAlphaLeaf),
		AfterStateRequest(stateAlphaLeaf, stateAlphaRoot),
		AfterStateRequest(stateAlphaLeaf, stateAlphaLeaf, stateAlphaRoot),
		AfterStateRequest(stateAlphaRoot, stateAlphaMiddle, stateAlphaLeaf, stateAlphaMiddle),
	}
	for index, request := range requests {
		got := reconstructState(t, reconstructor, request)
		if !got.Equal(want) {
			t.Fatalf("request[%d] state differs from leaf closure: got=%#v want=%#v", index, got.Clone(), want.Clone())
		}
	}
}

func TestStateReconstructorBeforeFiltersEveryExplicitTarget(t *testing.T) {
	t.Parallel()

	base := MigrationKey{App: "sample", Name: "0001_base"}
	first := MigrationKey{App: "sample", Name: "0002_first"}
	second := MigrationKey{App: "sample", Name: "0003_second"}
	reconstructor := mustStateReconstructor(t,
		Migration{
			App:        base.App,
			Name:       base.Name,
			Operations: []Operation{CreateModel{AppLabel: base.App, Model: stateModel("base_model", "sample_base")}},
		},
		Migration{
			App:          first.App,
			Name:         first.Name,
			Dependencies: []MigrationKey{base},
			Operations:   []Operation{CreateModel{AppLabel: first.App, Model: stateModel("first_model", "sample_first")}},
		},
		Migration{
			App:          second.App,
			Name:         second.Name,
			Dependencies: []MigrationKey{first},
			Operations:   []Operation{CreateModel{AppLabel: second.App, Model: stateModel("second_model", "sample_second")}},
		},
	)

	for _, request := range []StateRequest{
		BeforeStateRequest(first, second),
		BeforeStateRequest(second, first),
		BeforeStateRequest(second, first, second),
	} {
		state := reconstructState(t, reconstructor, request)
		assertStateModels(t, state, "sample", "base_model")
	}
}

func TestStateReconstructorSnapshotsDefinitionsRequestsAndResults(t *testing.T) {
	t.Parallel()

	definitions := stateFixtureDefinitions()
	reconstructor := mustStateReconstructor(t, definitions...)
	wantLatest := reconstructState(t, reconstructor, LatestStateRequest())

	rootCreate := definitions[0].Operations[0].(*CreateModel)
	rootCreate.Model.Name = "mutated"
	rootCreate.Model.Fields[0].Name = "mutated"
	rootCreate.Model.Fields[1].Default.String = "mutated"
	definitions[0].Dependencies = []MigrationKey{{App: "mutated", Name: "mutated"}}
	definitions[0].Operations[0] = AddField{AppLabel: "mutated"}
	middleAdd := definitions[1].Operations[0].(*AddField)
	middleAdd.Field.Name = "mutated"
	middleAdd.Field.Default.Boolean = true
	definitions[1].Operations = nil

	if got := reconstructState(t, reconstructor, LatestStateRequest()); !got.Equal(wantLatest) {
		t.Fatalf("definition mutation changed reconstruction: got=%#v want=%#v", got.Clone(), wantLatest.Clone())
	}

	rest := []MigrationKey{stateAlphaRoot}
	request := AfterStateRequest(stateAlphaLeaf, rest...)
	rest[0] = stateLegacy
	if got := reconstructState(t, reconstructor, request); !got.Equal(wantLatestForAlpha(t, reconstructor)) {
		t.Fatalf("target input mutation changed request: got=%#v", got.Clone())
	}

	applied := mustApplied(t, stateAlphaRoot)
	appliedRequest := AppliedStateRequest(applied)
	applied.keys[stateDeltaRoot] = struct{}{}
	appliedState := reconstructState(t, reconstructor, appliedRequest)
	assertStateApps(t, appliedState, "alpha")

	result := reconstructState(t, reconstructor, LatestStateRequest())
	result.apps["alpha"].Models[0].Name = "mutated"
	result.apps["alpha"].Models[0].Fields[0].Name = "mutated"
	result.apps["alpha"].Models[0].Fields[1].Default.String = "mutated"
	if got := reconstructState(t, reconstructor, LatestStateRequest()); !got.Equal(wantLatest) {
		t.Fatalf("result mutation changed reconstruction: got=%#v want=%#v", got.Clone(), wantLatest.Clone())
	}
}

func TestStateReconstructorGraphPermutationAndConcurrentReplayAreDeterministic(t *testing.T) {
	t.Parallel()

	definitions := stateFixtureDefinitions()
	baseline := mustStateReconstructor(t, definitions...)
	requests := []StateRequest{
		EmptyStateRequest(),
		LatestStateRequest(),
		BeforeStateRequest(stateAlphaMiddle),
		AfterStateRequest(stateAlphaLeaf, stateBetaRoot, stateGammaRoot),
		AppliedStateRequest(mustApplied(t, stateAlphaRoot, stateAlphaMiddle, stateDeltaRoot, stateLegacy)),
	}
	wants := make([]ProjectState, len(requests))
	for index, request := range requests {
		wants[index] = reconstructState(t, baseline, request)
	}

	random := rand.New(rand.NewSource(20260808))
	for iteration := 0; iteration < 100; iteration++ {
		permuted := stateFixtureDefinitions()
		random.Shuffle(len(permuted), func(left, right int) {
			permuted[left], permuted[right] = permuted[right], permuted[left]
		})
		reconstructor := mustStateReconstructor(t, permuted...)
		for index, request := range requests {
			if got := reconstructState(t, reconstructor, request); !got.Equal(wants[index]) {
				t.Fatalf("iteration %d request %d differs: got=%#v want=%#v", iteration, index, got.Clone(), wants[index].Clone())
			}
		}
	}

	const workers = 32
	var wait sync.WaitGroup
	errorsChannel := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				index := (worker + iteration) % len(requests)
				got, err := baseline.Reconstruct(requests[index])
				if err != nil {
					errorsChannel <- err
					return
				}
				if !got.Equal(wants[index]) {
					errorsChannel <- errors.New("concurrent reconstruction differed")
					return
				}
			}
		}(worker)
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatal(err)
	}
}

func TestStateReconstructorStructuredValidationAndReplayErrors(t *testing.T) {
	t.Parallel()

	reconstructor := mustStateReconstructor(t, stateFixtureDefinitions()...)

	_, err := reconstructor.Reconstruct(StateRequest{})
	assertPlanningError(t, err, CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{})

	invalidKey := MigrationKey{Name: "0001_missing_app"}
	_, err = reconstructor.Reconstruct(AfterStateRequest(invalidKey))
	assertPlanningError(t, err, CategoryPlan, CodeInvalidTarget, invalidKey, MigrationKey{})

	missing := MigrationKey{App: "alpha", Name: "9999_missing"}
	_, err = reconstructor.Reconstruct(AfterStateRequest(missing))
	assertPlanningError(t, err, CategoryPlan, CodeTargetNotFound, missing, MigrationKey{})

	_, err = reconstructor.Reconstruct(AppliedStateRequest(mustApplied(t, stateAlphaMiddle)))
	assertPlanningError(t, err, CategoryHistory, CodeInconsistentAppliedHistory, stateAlphaMiddle, stateAlphaRoot)

	invalidApplied := AppliedState{keys: map[MigrationKey]struct{}{{App: "", Name: "bad"}: {}}}
	_, err = reconstructor.Reconstruct(AppliedStateRequest(invalidApplied))
	assertPlanningError(t, err, CategoryHistory, CodeInvalidAppliedState, MigrationKey{App: "", Name: "bad"}, MigrationKey{})

	invalidTransition := Migration{
		App:  "broken",
		Name: "0001_add_without_model",
		Operations: []Operation{AddField{
			AppLabel: "broken", ModelName: "missing", Field: summaryField(),
		}},
	}
	broken := mustStateReconstructor(t, invalidTransition)
	_, err = broken.Reconstruct(LatestStateRequest())
	assertStateReconstructionError(t, err, invalidTransition.Key(), 0, "AddField")
}

func TestStateReconstructorConstructorRejectsAliasedOrInvalidOperations(t *testing.T) {
	t.Parallel()

	base := Migration{App: "sample", Name: "0001"}
	tests := []struct {
		name      string
		operation Operation
		kind      string
	}{
		{name: "nil interface", operation: nil},
		{name: "typed nil CreateModel", operation: (*CreateModel)(nil)},
		{name: "typed nil AddField", operation: (*AddField)(nil)},
		{name: "app mismatch", operation: CreateModel{AppLabel: "other", Model: stateModel("entry", "sample_entry")}, kind: "CreateModel"},
		{name: "unsupported sealed operation", operation: unsupportedStateOperation{CreateModel: CreateModel{AppLabel: "sample", Model: stateModel("entry", "sample_entry")}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			migration := base
			migration.Operations = []Operation{test.operation}
			_, err := NewStateReconstructor(migration)
			assertStateReconstructionError(t, err, migration.Key(), 0, test.kind)
		})
	}
}

func TestStateReconstructorRejectsRelationOperationsAtConstructorBoundary(t *testing.T) {
	t.Parallel()

	relation := relationMigrationField()
	scalarWithRelation := summaryField()
	scalarWithRelation.Relation = relation.Relation
	tests := []struct {
		name      string
		operation Operation
		kind      string
	}{
		{
			name: "CreateModel value ForeignKey kind",
			operation: CreateModel{AppLabel: "blog", Model: ir.Model{
				Name: "post", GoName: "Post", DBTable: "blog_post",
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}, relation},
			}},
			kind: "CreateModel",
		},
		{
			name: "CreateModel pointer hidden relation arm",
			operation: &CreateModel{AppLabel: "blog", Model: ir.Model{
				Name: "post", GoName: "Post", DBTable: "blog_post",
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}, scalarWithRelation},
			}},
			kind: "CreateModel",
		},
		{name: "AddField value ForeignKey kind", operation: AddField{AppLabel: "blog", ModelName: "post", Field: relation}, kind: "AddField"},
		{name: "AddField pointer hidden relation arm", operation: &AddField{AppLabel: "blog", ModelName: "post", Field: scalarWithRelation}, kind: "AddField"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			migration := Migration{App: "blog", Name: "0001_relation", Operations: []Operation{test.operation}}
			reconstructor, err := NewStateReconstructor(migration)
			migrationError := assertStateReconstructionError(t, err, migration.Key(), 0, test.kind)
			if !strings.Contains(migrationError.Cause.Error(), "Schema IR v2 migration state cannot represent relation-bearing field") {
				t.Fatalf("relation constructor cause = %v", migrationError.Cause)
			}
			state, reconstructErr := reconstructor.Reconstruct(LatestStateRequest())
			if reconstructErr != nil || len(state.Apps()) != 0 {
				t.Fatalf("failed constructor published reconstructor = state:%v err:%v", state.Apps(), reconstructErr)
			}
		})
	}
}

func TestCloneReconstructorOperationDeepCopiesNestedRelation(t *testing.T) {
	t.Parallel()

	field := relationMigrationField()
	operation := &AddField{AppLabel: "blog", ModelName: "post", Field: field}
	clonedOperation, kind, supported := cloneReconstructorOperation(operation)
	if !supported || kind != "AddField" {
		t.Fatalf("cloneReconstructorOperation() = %T, %q, %t", clonedOperation, kind, supported)
	}
	cloned := clonedOperation.(*AddField)
	operation.Field.Relation.Target.AppLabel = "mutated"
	operation.Field.Relation.Reverse.Name = "mutated"
	if cloned == operation || cloned.Field.Relation == operation.Field.Relation ||
		cloned.Field.Relation.Target.AppLabel != "authors" || cloned.Field.Relation.Reverse.Name != "posts" {
		t.Fatalf("cloned AddField retained relation alias: %#v", cloned)
	}
}

func TestStateReconstructorZeroValueMatchesEmptyConstructor(t *testing.T) {
	t.Parallel()

	var zero StateReconstructor
	empty := mustStateReconstructor(t)
	requests := []StateRequest{
		EmptyStateRequest(),
		LatestStateRequest(),
		AppliedStateRequest(mustApplied(t, stateLegacy)),
	}
	for _, request := range requests {
		zeroState := reconstructState(t, zero, request)
		emptyState := reconstructState(t, empty, request)
		if !zeroState.Equal(emptyState) || len(zeroState.Apps()) != 0 {
			t.Fatalf("zero reconstructor state = %#v, empty constructor state = %#v", zeroState.Clone(), emptyState.Clone())
		}
	}

	_, err := zero.Reconstruct(AfterStateRequest(stateLegacy))
	assertPlanningError(t, err, CategoryPlan, CodeTargetNotFound, stateLegacy, MigrationKey{})
}

type unsupportedStateOperation struct {
	CreateModel
}

func stateFixtureDefinitions() []Migration {
	falseDefault := &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false}
	trueDefault := &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: true}
	emptyDefault := &ir.ScalarDefault{Kind: ir.ScalarString, String: ""}
	archiveDefault := &ir.ScalarDefault{Kind: ir.ScalarString, String: "archive"}
	return []Migration{
		{
			App:  stateAlphaRoot.App,
			Name: stateAlphaRoot.Name,
			Operations: []Operation{
				&CreateModel{AppLabel: "alpha", Model: ir.Model{
					Name: "zulu", GoName: "Zulu", DBTable: "godj_state_alpha_zulu",
					Fields: []ir.Field{
						{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
						{Name: "active", GoName: "Active", Column: "active", Kind: ir.FieldBoolean, Default: trueDefault},
					},
				}},
				CreateModel{AppLabel: "alpha", Model: ir.Model{
					Name: "entry", GoName: "Entry", DBTable: "godj_state_alpha_entry",
					Fields: []ir.Field{
						{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
						{Name: "headline", GoName: "Headline", Column: "headline_text", Kind: ir.FieldChar, MaxLength: 64, Default: emptyDefault},
					},
				}},
			},
		},
		{
			App: stateAlphaMiddle.App, Name: stateAlphaMiddle.Name,
			Dependencies: []MigrationKey{stateAlphaRoot},
			Operations: []Operation{&AddField{
				AppLabel: "alpha", ModelName: "entry",
				Field: ir.Field{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean, Default: falseDefault},
			}},
		},
		{
			App: stateAlphaLeaf.App, Name: stateAlphaLeaf.Name,
			Dependencies: []MigrationKey{stateAlphaMiddle},
			Operations: []Operation{AddField{
				AppLabel: "alpha", ModelName: "entry",
				Field: ir.Field{Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 255},
			}},
		},
		{
			App: stateBetaRoot.App, Name: stateBetaRoot.Name,
			Dependencies: []MigrationKey{stateAlphaRoot},
			Operations: []Operation{CreateModel{AppLabel: "beta", Model: ir.Model{
				Name: "audit", GoName: "Audit", DBTable: "godj_state_beta_audit",
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					{Name: "code", GoName: "Code", Column: "code", Kind: ir.FieldChar, Nullable: true, MaxLength: 32},
				},
			}}},
		},
		{
			App: stateGammaRoot.App, Name: stateGammaRoot.Name,
			Dependencies: []MigrationKey{stateAlphaRoot},
			Operations: []Operation{CreateModel{AppLabel: "gamma", Model: ir.Model{
				Name: "flag", GoName: "Flag", DBTable: "godj_state_gamma_flag",
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					{Name: "enabled", GoName: "Enabled", Column: "enabled", Kind: ir.FieldBoolean, Default: trueDefault},
				},
			}}},
		},
		{
			App: stateDeltaRoot.App, Name: stateDeltaRoot.Name,
			Operations: []Operation{CreateModel{AppLabel: "delta", Model: ir.Model{
				Name: "archive", GoName: "Archive", DBTable: "godj_state_delta_archive",
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					{Name: "label", GoName: "Label", Column: "label", Kind: ir.FieldChar, MaxLength: 48, Default: archiveDefault},
				},
			}}},
		},
	}
}

func stateModel(name, table string) ir.Model {
	goName := map[string]string{
		"base_model":   "BaseModel",
		"first_model":  "FirstModel",
		"second_model": "SecondModel",
	}[name]
	return ir.Model{
		Name: name, GoName: goName, DBTable: table,
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}
}

func mustStateReconstructor(t *testing.T, definitions ...Migration) StateReconstructor {
	t.Helper()
	reconstructor, err := NewStateReconstructor(definitions...)
	if err != nil {
		t.Fatalf("NewStateReconstructor() error = %v", err)
	}
	return reconstructor
}

func reconstructState(t *testing.T, reconstructor StateReconstructor, request StateRequest) ProjectState {
	t.Helper()
	state, err := reconstructor.Reconstruct(request)
	if err != nil {
		t.Fatalf("Reconstruct() error = %v", err)
	}
	return state
}

func assertStateApps(t *testing.T, state ProjectState, want ...string) {
	t.Helper()
	if got := state.Apps(); !slices.Equal(got, want) {
		t.Fatalf("Apps() = %v, want %v", got, want)
	}
}

func assertStateModels(t *testing.T, state ProjectState, app string, want ...string) {
	t.Helper()
	schema, exists := state.Schema(app)
	if !exists {
		t.Fatalf("Schema(%q) missing", app)
	}
	got := make([]string, len(schema.Models))
	for index, model := range schema.Models {
		got[index] = model.Name
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Schema(%q) models = %v, want %v", app, got, want)
	}
}

func assertStateFields(t *testing.T, state ProjectState, app, modelName string, want ...string) {
	t.Helper()
	model, exists := state.Model(app, modelName)
	if !exists {
		t.Fatalf("Model(%q, %q) missing", app, modelName)
	}
	got := make([]string, len(model.Fields))
	for index, field := range model.Fields {
		got[index] = field.Name
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Model(%q, %q) fields = %v, want %v", app, modelName, got, want)
	}
}

func mustStateField(t *testing.T, state ProjectState, app, modelName, fieldName string) ir.Field {
	t.Helper()
	model, exists := state.Model(app, modelName)
	if !exists {
		t.Fatalf("Model(%q, %q) missing", app, modelName)
	}
	for _, field := range model.Fields {
		if field.Name == fieldName {
			return field
		}
	}
	t.Fatalf("field %s.%s.%s missing", app, modelName, fieldName)
	return ir.Field{}
}

func wantLatestForAlpha(t *testing.T, reconstructor StateReconstructor) ProjectState {
	t.Helper()
	return reconstructState(t, reconstructor, AfterStateRequest(stateAlphaLeaf))
}

func assertStateReconstructionError(t *testing.T, err error, key MigrationKey, operationIndex int, kind string) *Error {
	t.Helper()
	var migrationError *Error
	if !errors.As(err, &migrationError) {
		t.Fatalf("error = %#v, want *Error", err)
	}
	if migrationError.Category != CategoryState || migrationError.Code != CodeInvalidState ||
		migrationError.Direction != DirectionForward || migrationError.App != key.App ||
		migrationError.Migration != key.Name || migrationError.OperationIndex != operationIndex ||
		migrationError.Operation != kind {
		t.Fatalf("state reconstruction error = %#v", migrationError)
	}
	return migrationError
}
