package migrations

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/internal/definitionhandoff"
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

func TestLoadedStateReconstructorPromotesWholeStepAndSupportsHistoricalRequests(t *testing.T) {
	t.Parallel()

	authorKey := MigrationKey{App: "authors", Name: "0001_author"}
	articleKey := MigrationKey{App: "blog", Name: "0001_article"}
	relationKey := MigrationKey{App: "blog", Name: "0002_author"}
	independentKey := MigrationKey{App: "audit", Name: "0001_event"}
	definitions := []Migration{
		{
			App: authorKey.App, Name: authorKey.Name,
			Operations: []Operation{CreateModel{AppLabel: "authors", Model: ir.Model{
				Name: "author", GoName: "Author", DBTable: "authors_author",
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
			}}},
		},
		{
			App: articleKey.App, Name: articleKey.Name, Dependencies: []MigrationKey{authorKey},
			Operations: []Operation{CreateModel{AppLabel: "blog", Model: ir.Model{
				Name: "article", GoName: "Article", DBTable: "blog_article",
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
			}}},
		},
		{
			App: relationKey.App, Name: relationKey.Name, Dependencies: []MigrationKey{articleKey},
			Operations: []Operation{
				AddField{AppLabel: "blog", ModelName: "article", Field: ir.Field{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean}},
				AddField{AppLabel: "blog", ModelName: "article", Field: ir.Field{
					Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Cardinality: ir.RelationManyToOne,
						Reverse: ir.ReverseRelation{Name: "articles"}, OnDelete: ir.DeleteProtect,
					},
				}},
			},
		},
		{
			App: independentKey.App, Name: independentKey.Name,
			Operations: []Operation{CreateModel{AppLabel: "audit", Model: ir.Model{
				Name: "event", GoName: "Event", DBTable: "audit_event",
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
			}}},
		},
	}
	reconstructor := mustLoadedStateReconstructor(t, definitions, relationKey)

	before, err := reconstructor.Reconstruct(BeforeStateRequest(relationKey))
	if err != nil {
		t.Fatalf("Reconstruct(before relation): %v", err)
	}
	if before.FormatVersion() != StateFormatVersion {
		t.Fatalf("before format = %d, want %d", before.FormatVersion(), StateFormatVersion)
	}
	if _, exists := before.Model("audit", "event"); exists {
		t.Fatal("Before(relation) included an unrelated independent branch")
	}

	after, err := reconstructor.Reconstruct(AfterStateRequest(relationKey, independentKey))
	if err != nil {
		t.Fatalf("Reconstruct(after relation + independent): %v", err)
	}
	if after.FormatVersion() != RelationStateFormatVersion {
		t.Fatalf("after format = %d, want %d", after.FormatVersion(), RelationStateFormatVersion)
	}
	article, exists := after.Model("blog", "article")
	if !exists || len(article.Fields) != 3 || article.Fields[2].Relation == nil || article.Fields[2].Relation.Target.AppLabel != "authors" {
		t.Fatalf("relation state article = %#v", article)
	}
	if _, exists := after.Model("audit", "event"); !exists {
		t.Fatal("ordered independent target union omitted audit.event")
	}
	article.Fields[2].Relation.Reverse.Name = "mutated"
	fresh, _ := after.Model("blog", "article")
	if fresh.Fields[2].Relation.Reverse.Name != "articles" {
		t.Fatal("loaded relation state accessor retained a nested relation alias")
	}

	applied, err := NewAppliedState(authorKey, articleKey)
	if err != nil {
		t.Fatalf("NewAppliedState(): %v", err)
	}
	appliedState, err := reconstructor.Reconstruct(AppliedStateRequest(applied))
	if err != nil || appliedState.FormatVersion() != StateFormatVersion {
		t.Fatalf("Reconstruct(applied roots) = format:%d error:%v", appliedState.FormatVersion(), err)
	}
	forwardPrepared, _, err := reconstructor.preflight(before, definitions[2], DirectionForward)
	if err != nil {
		t.Fatalf("relation forward preflight: %v", err)
	}
	if len(forwardPrepared) != 2 || forwardPrepared[0].from.FormatVersion() != RelationStateFormatVersion ||
		forwardPrepared[0].to.FormatVersion() != RelationStateFormatVersion ||
		forwardPrepared[1].from.FormatVersion() != RelationStateFormatVersion {
		t.Fatalf("whole-step forward promotion = %#v", forwardPrepared)
	}

	prepared, empty, err := reconstructor.preflight(afterWithoutIndependent(t, after), definitions[2], DirectionBackward)
	if err != nil {
		t.Fatalf("relation backward preflight: %v", err)
	}
	if len(prepared) != 2 || prepared[0].index != 1 || prepared[1].index != 0 ||
		prepared[0].from.FormatVersion() != RelationStateFormatVersion || prepared[1].to.FormatVersion() != RelationStateFormatVersion ||
		empty.FormatVersion() != StateFormatVersion {
		t.Fatalf("backward chronology = prepared:%#v final-format:%d", prepared, empty.FormatVersion())
	}
	if len(prepared[0].relationTargets) != 1 || prepared[0].relationTargets[0].TargetPrimaryKey.Kind != ir.FieldAuto ||
		prepared[0].relationTargets[0].TargetPrimaryKey.Nullable {
		t.Fatalf("historical target binding = %#v", prepared[0].relationTargets)
	}
}

func TestLoadedStateReconstructorRejectsUnrelatedTargetCreatorDeterministically(t *testing.T) {
	t.Parallel()

	target := Migration{App: "authors", Name: "0001_author", Operations: []Operation{CreateModel{AppLabel: "authors", Model: ir.Model{
		Name: "author", GoName: "Author", DBTable: "authors_author",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
	}}}}
	source := Migration{App: "blog", Name: "0001_article", Operations: []Operation{CreateModel{AppLabel: "blog", Model: ir.Model{
		Name: "article", GoName: "Article", DBTable: "blog_article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Cardinality: ir.RelationManyToOne,
				Reverse: ir.ReverseRelation{Name: "articles"}, OnDelete: ir.DeleteProtect,
			}},
		},
	}}}}
	planner, err := NewPlanner(target, source)
	if err != nil {
		t.Fatalf("NewPlanner(): %v", err)
	}
	authority := testLoadedAuthority(planner, []Migration{target, source}, source.Key())
	_, err = newLoadedStateReconstructor(authority, []Migration{source, target})
	migrationError := assertStateReconstructionError(t, err, source.Key(), 0, "CreateModel")
	if !strings.Contains(migrationError.Cause.Error(), "not dependency ancestry") {
		t.Fatalf("unrelated creator cause = %v", migrationError.Cause)
	}
}

func TestLoadedStateReconstructorRejectsUnrelatedSourceCreatorBeforeIncidentalLatestReplay(t *testing.T) {
	t.Parallel()

	targetKey := MigrationKey{App: "authors", Name: "0001_author"}
	sourceCreatorKey := MigrationKey{App: "blog", Name: "0001_article"}
	relationKey := MigrationKey{App: "blog", Name: "0002_relation"}
	model := func(app, name, goName string) ir.Model {
		return ir.Model{
			Name: name, GoName: goName, DBTable: app + "_" + name,
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}
	}
	definitions := []Migration{
		{App: targetKey.App, Name: targetKey.Name, Operations: []Operation{CreateModel{AppLabel: targetKey.App, Model: model("authors", "author", "Author")}}},
		{App: sourceCreatorKey.App, Name: sourceCreatorKey.Name, Operations: []Operation{CreateModel{AppLabel: sourceCreatorKey.App, Model: model("blog", "article", "Article")}}},
		{
			App: relationKey.App, Name: relationKey.Name, Dependencies: []MigrationKey{targetKey},
			Operations: []Operation{AddField{AppLabel: "blog", ModelName: "article", Field: ir.Field{
				Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
				Relation: &ir.ForeignKeyRelation{
					Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Cardinality: ir.RelationManyToOne,
					Reverse: ir.ReverseRelation{Name: "articles"}, OnDelete: ir.DeleteProtect,
				},
			}}},
		},
	}
	planner, err := NewPlanner(definitions...)
	if err != nil {
		t.Fatalf("NewPlanner(): %v", err)
	}
	_, err = newLoadedStateReconstructor(testLoadedAuthority(planner, definitions, relationKey), definitions)
	migrationError := assertStateReconstructionError(t, err, relationKey, 0, "AddField")
	if !strings.Contains(migrationError.Cause.Error(), "source creator blog.0001_article is not dependency ancestry") {
		t.Fatalf("unrelated source creator cause = %v", migrationError.Cause)
	}
}

func TestLoadedStateReconstructorAcceptsEarlierSameMigrationCreators(t *testing.T) {
	t.Parallel()

	key := MigrationKey{App: "blog", Name: "0001_models"}
	definition := Migration{App: key.App, Name: key.Name, Operations: []Operation{
		CreateModel{AppLabel: "blog", Model: ir.Model{
			Name: "author", GoName: "Author", DBTable: "blog_author",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
		CreateModel{AppLabel: "blog", Model: ir.Model{
			Name: "article", GoName: "Article", DBTable: "blog_article",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
		AddField{AppLabel: "blog", ModelName: "article", Field: ir.Field{
			Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
			Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "blog", ModelName: "author"}, Cardinality: ir.RelationManyToOne,
				Reverse: ir.ReverseRelation{Name: "articles"}, OnDelete: ir.DeleteProtect,
			},
		}},
	}}
	reconstructor := mustLoadedStateReconstructor(t, []Migration{definition}, key)
	state, err := reconstructor.Reconstruct(LatestStateRequest())
	if err != nil {
		t.Fatalf("Reconstruct(latest): %v", err)
	}
	if state.FormatVersion() != RelationStateFormatVersion {
		t.Fatalf("latest format = %d, want %d", state.FormatVersion(), RelationStateFormatVersion)
	}
	article, exists := state.Model("blog", "article")
	if !exists || len(article.Fields) != 2 || article.Fields[1].Relation == nil ||
		article.Fields[1].Relation.Target != (ir.ModelIdentity{AppLabel: "blog", ModelName: "author"}) {
		t.Fatalf("same-migration relation state = %#v", article)
	}
}

func TestLoadedStateReconstructorRejectsLaterAndSelfCreatorsAtRelationOperation(t *testing.T) {
	model := func(app, name, goName string, fields ...ir.Field) ir.Model {
		if len(fields) == 0 {
			fields = []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}}
		}
		return ir.Model{Name: name, GoName: goName, DBTable: app + "_" + name, Fields: fields}
	}
	relation := func(targetApp, targetModel, reverse string) ir.Field {
		return ir.Field{
			Name: "target", GoName: "TargetID", Column: "target_id", Kind: ir.FieldForeignKey,
			Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: targetApp, ModelName: targetModel}, Cardinality: ir.RelationManyToOne,
				Reverse: ir.ReverseRelation{Name: reverse}, OnDelete: ir.DeleteProtect,
			},
		}
	}
	tests := []struct {
		name          string
		definitions   []Migration
		relationKey   MigrationKey
		wantSubstring string
	}{
		{
			name: "target creator later in same migration",
			definitions: []Migration{{App: "blog", Name: "0001_later_target", Operations: []Operation{
				CreateModel{AppLabel: "blog", Model: model("blog", "source", "Source",
					ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					relation("blog", "target", "sources"),
				)},
				CreateModel{AppLabel: "blog", Model: model("blog", "target", "Target")},
			}}},
			relationKey:   MigrationKey{App: "blog", Name: "0001_later_target"},
			wantSubstring: "created later in the same migration",
		},
		{
			name: "source creator later in same migration",
			definitions: []Migration{
				{App: "authors", Name: "0001_target", Operations: []Operation{CreateModel{AppLabel: "authors", Model: model("authors", "author", "Author")}}},
				{App: "blog", Name: "0001_later_source", Dependencies: []MigrationKey{{App: "authors", Name: "0001_target"}}, Operations: []Operation{
					AddField{AppLabel: "blog", ModelName: "post", Field: relation("authors", "author", "posts")},
					CreateModel{AppLabel: "blog", Model: model("blog", "post", "Post")},
				}},
			},
			relationKey:   MigrationKey{App: "blog", Name: "0001_later_source"},
			wantSubstring: "created later in the same migration",
		},
		{
			name: "self relation",
			definitions: []Migration{{App: "blog", Name: "0001_self", Operations: []Operation{CreateModel{
				AppLabel: "blog", Model: model("blog", "post", "Post",
					ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					relation("blog", "post", "children"),
				),
			}}}},
			relationKey:   MigrationKey{App: "blog", Name: "0001_self"},
			wantSubstring: "self-referential",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			planner, err := NewPlanner(test.definitions...)
			if err != nil {
				t.Fatalf("NewPlanner(): %v", err)
			}
			_, err = newLoadedStateReconstructor(testLoadedAuthority(planner, test.definitions, test.relationKey), test.definitions)
			migrationError := assertStateReconstructionError(t, err, test.relationKey, 0, operationKindAt(test.definitions[len(test.definitions)-1], 0))
			if !strings.Contains(migrationError.Cause.Error(), test.wantSubstring) {
				t.Fatalf("cause = %v, want discriminator %q", migrationError.Cause, test.wantSubstring)
			}
		})
	}
}

func TestLoadedStateRelationCycleErrorIsCanonicalAcrossDefinitionAndFieldOrder(t *testing.T) {
	relationModel := func(name, goName, target, reverse string, relationFirst bool) ir.Model {
		primary := ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}
		relation := ir.Field{
			Name: "peer", GoName: "PeerID", Column: "peer_id", Kind: ir.FieldForeignKey,
			Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "cycle", ModelName: target}, Cardinality: ir.RelationManyToOne,
				Reverse: ir.ReverseRelation{Name: reverse}, OnDelete: ir.DeleteProtect,
			},
		}
		fields := []ir.Field{primary, relation}
		if relationFirst {
			fields[0], fields[1] = fields[1], fields[0]
		}
		return ir.Model{Name: name, GoName: goName, DBTable: "cycle_" + name, Fields: fields}
	}
	for iteration := 0; iteration < 16; iteration++ {
		first := Migration{App: "cycle", Name: "0001_a", Operations: []Operation{CreateModel{
			AppLabel: "cycle", Model: relationModel("a", "A", "b", "from_b", iteration%2 == 0),
		}}}
		second := Migration{App: "cycle", Name: "0002_b", Operations: []Operation{CreateModel{
			AppLabel: "cycle", Model: relationModel("b", "B", "a", "from_a", iteration%3 == 0),
		}}}
		definitions := []Migration{first, second}
		if iteration%2 != 0 {
			definitions[0], definitions[1] = definitions[1], definitions[0]
		}
		planner, err := NewPlanner(definitions...)
		if err != nil {
			t.Fatalf("iteration %d NewPlanner(): %v", iteration, err)
		}
		_, err = newLoadedStateReconstructor(testLoadedAuthority(planner, definitions, first.Key(), second.Key()), definitions)
		migrationError := assertStateReconstructionError(t, err, first.Key(), 0, "CreateModel")
		if !strings.Contains(migrationError.Cause.Error(), "relation cycle") {
			t.Fatalf("iteration %d cycle cause = %v", iteration, migrationError.Cause)
		}
	}
}

func TestLoadedStateRejectsReverseNameCollisionsAtCanonicalOperation(t *testing.T) {
	targetKey := MigrationKey{App: "authors", Name: "0001_author"}
	target := func(withCollisionField bool) Migration {
		fields := []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}}
		if withCollisionField {
			fields = append(fields, ir.Field{Name: "items", GoName: "Items", Column: "items", Kind: ir.FieldBoolean})
		}
		return Migration{App: targetKey.App, Name: targetKey.Name, Operations: []Operation{CreateModel{
			AppLabel: "authors", Model: ir.Model{Name: "author", GoName: "Author", DBTable: "authors_author", Fields: fields},
		}}}
	}
	sourceModel := func(name, goName string) ir.Model {
		return ir.Model{
			Name: name, GoName: goName, DBTable: "blog_" + name,
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Cardinality: ir.RelationManyToOne,
						Reverse: ir.ReverseRelation{Name: "items"}, OnDelete: ir.DeleteProtect,
					},
				},
			},
		}
	}
	tests := []struct {
		name        string
		definitions []Migration
		key         MigrationKey
		operation   int
	}{
		{
			name: "reverse collides with target field",
			definitions: []Migration{
				target(true),
				{App: "blog", Name: "0001_source", Dependencies: []MigrationKey{targetKey}, Operations: []Operation{CreateModel{AppLabel: "blog", Model: sourceModel("post", "Post")}}},
			},
			key: MigrationKey{App: "blog", Name: "0001_source"}, operation: 0,
		},
		{
			name: "two relations share reverse",
			definitions: []Migration{
				target(false),
				{App: "blog", Name: "0001_sources", Dependencies: []MigrationKey{targetKey}, Operations: []Operation{
					CreateModel{AppLabel: "blog", Model: sourceModel("first", "First")},
					CreateModel{AppLabel: "blog", Model: sourceModel("second", "Second")},
				}},
			},
			key: MigrationKey{App: "blog", Name: "0001_sources"}, operation: 1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			planner, err := NewPlanner(test.definitions...)
			if err != nil {
				t.Fatalf("NewPlanner(): %v", err)
			}
			_, err = newLoadedStateReconstructor(testLoadedAuthority(planner, test.definitions, test.key), test.definitions)
			migrationError := assertStateReconstructionError(t, err, test.key, test.operation, "CreateModel")
			if !strings.Contains(migrationError.Cause.Error(), "collides") {
				t.Fatalf("reverse collision cause = %v", migrationError.Cause)
			}
		})
	}
}

func TestLoadedStateRejectsInvalidHistoricalTargetPrimaryKeyAtCreator(t *testing.T) {
	targetKey := MigrationKey{App: "authors", Name: "0001_author"}
	relationKey := MigrationKey{App: "blog", Name: "0001_post"}
	tests := []struct {
		name   string
		fields []ir.Field
	}{
		{
			name:   "non Auto primary key",
			fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldChar, PrimaryKey: true, MaxLength: 32}},
		},
		{
			name:   "nullable Auto primary key",
			fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true, Nullable: true}},
		},
		{
			name: "multiple Auto primary keys",
			fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "other_id", GoName: "OtherID", Column: "other_id", Kind: ir.FieldAuto, PrimaryKey: true},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			target := Migration{App: targetKey.App, Name: targetKey.Name, Operations: []Operation{CreateModel{
				AppLabel: "authors", Model: ir.Model{Name: "author", GoName: "Author", DBTable: "authors_author", Fields: test.fields},
			}}}
			source := Migration{App: relationKey.App, Name: relationKey.Name, Dependencies: []MigrationKey{targetKey}, Operations: []Operation{CreateModel{
				AppLabel: "blog", Model: ir.Model{
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
				},
			}}}
			definitions := []Migration{source, target}
			planner, err := NewPlanner(definitions...)
			if err != nil {
				t.Fatalf("NewPlanner(): %v", err)
			}
			_, err = newLoadedStateReconstructor(testLoadedAuthority(planner, definitions, relationKey), definitions)
			assertStateReconstructionError(t, err, targetKey, 0, "CreateModel")
		})
	}
}

func TestLoadedStatePreflightPreservesCreateModelRelationFieldOrderBothDirections(t *testing.T) {
	t.Parallel()

	targetKey := MigrationKey{App: "authors", Name: "0001_targets"}
	sourceKey := MigrationKey{App: "blog", Name: "0001_article"}
	targets := Migration{App: targetKey.App, Name: targetKey.Name, Operations: []Operation{
		CreateModel{AppLabel: "authors", Model: ir.Model{
			Name: "author", GoName: "Author", DBTable: "authors_author",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
		CreateModel{AppLabel: "authors", Model: ir.Model{
			Name: "editor", GoName: "Editor", DBTable: "authors_editor",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}}
	relationField := func(name, target, reverse string) ir.Field {
		return ir.Field{
			Name: name, GoName: strings.ToUpper(name[:1]) + name[1:] + "ID", Column: name + "_id", Kind: ir.FieldForeignKey,
			Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "authors", ModelName: target}, Cardinality: ir.RelationManyToOne,
				Reverse: ir.ReverseRelation{Name: reverse}, OnDelete: ir.DeleteProtect,
			},
		}
	}
	source := Migration{App: sourceKey.App, Name: sourceKey.Name, Dependencies: []MigrationKey{targetKey}, Operations: []Operation{
		CreateModel{AppLabel: "blog", Model: ir.Model{
			Name: "article", GoName: "Article", DBTable: "blog_article",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				relationField("z_author", "author", "z_articles"),
				relationField("a_editor", "editor", "a_articles"),
			},
		}},
	}}
	definitions := []Migration{targets, source}
	reconstructor := mustLoadedStateReconstructor(t, definitions, sourceKey)
	before, err := reconstructor.Reconstruct(BeforeStateRequest(sourceKey))
	if err != nil {
		t.Fatalf("Reconstruct(before): %v", err)
	}
	forward, latest, err := reconstructor.preflight(before, source, DirectionForward)
	if err != nil {
		t.Fatalf("forward preflight: %v", err)
	}
	backward, _, err := reconstructor.preflight(latest, source, DirectionBackward)
	if err != nil {
		t.Fatalf("backward preflight: %v", err)
	}
	want := []string{"z_author", "a_editor"}
	for label, prepared := range map[string][]preparedOperation{"forward": forward, "backward": backward} {
		if len(prepared) != 1 || len(prepared[0].relationTargets) != len(want) {
			t.Fatalf("%s relation targets = %#v", label, prepared)
		}
		got := []string{prepared[0].relationTargets[0].SourceField.Name, prepared[0].relationTargets[1].SourceField.Name}
		if !slices.Equal(got, want) {
			t.Fatalf("%s target field order = %v, want %v", label, got, want)
		}
	}
}

func TestLoadedStateDuplicateCreatorSelectionIsDeterministicAcrossInputOrder(t *testing.T) {
	t.Parallel()

	create := func(key MigrationKey, model string) Migration {
		return Migration{App: key.App, Name: key.Name, Operations: []Operation{CreateModel{AppLabel: key.App, Model: ir.Model{
			Name: model, GoName: strings.ToUpper(model[:1]) + model[1:], DBTable: key.App + "_" + model,
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}}}}
	}
	alphaFirst := MigrationKey{App: "alpha", Name: "0001_first"}
	alphaSecond := MigrationKey{App: "alpha", Name: "0002_second"}
	zetaFirst := MigrationKey{App: "zeta", Name: "0001_first"}
	zetaSecond := MigrationKey{App: "zeta", Name: "0002_second"}
	canonical := []Migration{
		create(zetaSecond, "shared"), create(alphaSecond, "shared"),
		create(zetaFirst, "shared"), create(alphaFirst, "shared"),
	}
	for iteration := 0; iteration < 32; iteration++ {
		definitions := append([]Migration(nil), canonical...)
		rand.New(rand.NewSource(int64(iteration))).Shuffle(len(definitions), func(left, right int) {
			definitions[left], definitions[right] = definitions[right], definitions[left]
		})
		planner, err := NewPlanner(definitions...)
		if err != nil {
			t.Fatalf("iteration %d NewPlanner(): %v", iteration, err)
		}
		_, err = newLoadedStateReconstructor(testLoadedAuthority(planner, definitions), definitions)
		migrationError := assertStateReconstructionError(t, err, alphaSecond, 0, "CreateModel")
		if !strings.Contains(migrationError.Cause.Error(), "model alpha.shared has multiple historical creators") {
			t.Fatalf("iteration %d duplicate cause = %v", iteration, migrationError.Cause)
		}
	}
}

func TestLoadedRelationCycleScanIsBoundedOnBranchingAcyclicGraph(t *testing.T) {
	t.Parallel()

	const nodes = 512
	declarations := make([]loadedRelationDeclaration, 0, nodes*2)
	for index := 0; index < nodes; index++ {
		for _, target := range []int{index + 1, index + 2} {
			if target >= nodes {
				continue
			}
			declarations = append(declarations, loadedRelationDeclaration{
				source: loadedModelIdentity{app: "graph", model: fmt.Sprintf("m%04d", index)},
				field: ir.Field{Name: fmt.Sprintf("to_%04d", target), Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{
					Target: ir.ModelIdentity{AppLabel: "graph", ModelName: fmt.Sprintf("m%04d", target)},
				}},
			})
		}
	}
	done := make(chan []loadedModelIdentity, 1)
	go func() { done <- firstLoadedRelationCycle(declarations) }()
	select {
	case cycle := <-done:
		if len(cycle) != 0 {
			t.Fatalf("acyclic branching graph cycle = %v", cycle)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("branching acyclic relation graph scan exceeded bounded time")
	}
}

func TestLoadedRelationCycleErrorSelectionIsLinearAfterLargeAcyclicPrefix(t *testing.T) {
	const prefix = 4_096
	const cycleSize = 2_048
	declarations := make([]loadedRelationDeclaration, 0, prefix+cycleSize)
	definitions := make(map[MigrationKey]Migration, prefix+cycleSize)
	for index := 0; index < prefix; index++ {
		key := MigrationKey{App: "a_prefix", Name: fmt.Sprintf("%04d", index)}
		declarations = append(declarations, loadedRelationDeclaration{
			key: key, operationIndex: 0, operationKind: "CreateModel",
			source: loadedModelIdentity{app: "prefix", model: fmt.Sprintf("source_%04d", index)},
			field: ir.Field{Name: "next", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "prefix", ModelName: fmt.Sprintf("target_%04d", index)},
			}},
		})
		definitions[key] = Migration{App: key.App, Name: key.Name}
	}
	for index := 0; index < cycleSize; index++ {
		key := MigrationKey{App: "z_cycle", Name: fmt.Sprintf("%04d", index)}
		target := (index + 1) % cycleSize
		declarations = append(declarations, loadedRelationDeclaration{
			key: key, operationIndex: 0, operationKind: "CreateModel",
			source: loadedModelIdentity{app: "cycle", model: fmt.Sprintf("node_%04d", index)},
			field: ir.Field{Name: "next", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "cycle", ModelName: fmt.Sprintf("node_%04d", target)},
			}},
		})
		definitions[key] = Migration{App: key.App, Name: key.Name}
	}
	sort.Slice(declarations, func(left, right int) bool { return loadedDeclarationLess(declarations[left], declarations[right]) })
	err := (loadedStateReconstructor{definitions: definitions, declarations: declarations}).validateChronology()
	migrationError := assertStateReconstructionError(t, err, MigrationKey{App: "z_cycle", Name: "0000"}, 0, "CreateModel")
	if !strings.Contains(migrationError.Cause.Error(), "relation cycle") {
		t.Fatalf("large-prefix cycle cause = %v", migrationError.Cause)
	}
}

func TestLoadedStateBuilderHandlesLongAddFieldHistoryWithoutWholeStateSnapshots(t *testing.T) {
	const addedFields = 1_024
	targetKey := MigrationKey{App: "authors", Name: "0001_author"}
	sourceKey := MigrationKey{App: "blog", Name: "0001_post"}
	fieldsKey := MigrationKey{App: "blog", Name: "0002_many_fields"}

	target := Migration{App: targetKey.App, Name: targetKey.Name, Operations: []Operation{CreateModel{
		AppLabel: targetKey.App,
		Model: ir.Model{
			Name: "author", GoName: "Author", DBTable: "authors_author",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		},
	}}}
	source := Migration{App: sourceKey.App, Name: sourceKey.Name, Dependencies: []MigrationKey{targetKey}, Operations: []Operation{CreateModel{
		AppLabel: sourceKey.App,
		Model: ir.Model{
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
		},
	}}}
	operations := make([]Operation, addedFields)
	for index := range operations {
		name := fmt.Sprintf("flag_%04d", index)
		operations[index] = AddField{
			AppLabel: "blog", ModelName: "post",
			Field: ir.Field{Name: name, GoName: fmt.Sprintf("Flag%04d", index), Column: name, Kind: ir.FieldBoolean},
		}
	}
	fields := Migration{App: fieldsKey.App, Name: fieldsKey.Name, Dependencies: []MigrationKey{sourceKey}, Operations: operations}
	definitions := []Migration{fields, target, source}
	reconstructor := mustLoadedStateReconstructor(t, definitions, sourceKey)

	latest, err := reconstructor.Reconstruct(LatestStateRequest())
	if err != nil {
		t.Fatalf("Reconstruct(long latest): %v", err)
	}
	model, exists := latest.Model("blog", "post")
	if !exists || len(model.Fields) != addedFields+2 || latest.FormatVersion() != RelationStateFormatVersion {
		t.Fatalf("long latest = fields:%d exists:%t format:%d", len(model.Fields), exists, latest.FormatVersion())
	}
	model.Fields[len(model.Fields)-1].Name = "mutated"
	fresh, err := reconstructor.Reconstruct(LatestStateRequest())
	if err != nil {
		t.Fatalf("Reconstruct(long fresh): %v", err)
	}
	freshModel, _ := fresh.Model("blog", "post")
	if freshModel.Fields[len(freshModel.Fields)-1].Name != "flag_1023" {
		t.Fatal("long replay retained a returned model alias")
	}
}

func TestLoadedFullProjectionPlansSharedChainAndManyLeavesOnce(t *testing.T) {
	const chainLength = 1_024
	const leafCount = 1_024
	definitions := make([]Migration, 0, chainLength+leafCount)
	keys := make([]MigrationKey, chainLength)
	for index := range keys {
		keys[index] = MigrationKey{App: "graph", Name: fmt.Sprintf("c_%04d", index)}
		migration := Migration{App: keys[index].App, Name: keys[index].Name}
		if index != 0 {
			migration.Dependencies = []MigrationKey{keys[index-1]}
		}
		switch index {
		case 0:
			migration.Operations = []Operation{CreateModel{AppLabel: "graph", Model: ir.Model{
				Name: "target", GoName: "Target", DBTable: "graph_target",
				Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
			}}}
		case 1:
			migration.Operations = []Operation{CreateModel{AppLabel: "graph", Model: ir.Model{
				Name: "source", GoName: "Source", DBTable: "graph_source",
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					{
						Name: "target", GoName: "TargetID", Column: "target_id", Kind: ir.FieldForeignKey,
						Relation: &ir.ForeignKeyRelation{
							Target: ir.ModelIdentity{AppLabel: "graph", ModelName: "target"}, Cardinality: ir.RelationManyToOne,
							Reverse: ir.ReverseRelation{Name: "sources"}, OnDelete: ir.DeleteProtect,
						},
					},
				},
			}}}
		}
		definitions = append(definitions, migration)
	}
	for index := 0; index < leafCount; index++ {
		definitions = append(definitions, Migration{
			App: "graph", Name: fmt.Sprintf("l_%04d", index), Dependencies: []MigrationKey{keys[len(keys)-1]},
		})
	}
	planner, err := NewPlanner(definitions...)
	if err != nil {
		t.Fatalf("NewPlanner(shared chain): %v", err)
	}
	reconstructor, err := newLoadedStateReconstructor(testLoadedAuthority(planner, definitions, keys[1]), definitions)
	if err != nil {
		t.Fatalf("newLoadedStateReconstructor(shared chain): %v", err)
	}
	steps, err := reconstructor.fullForwardProjection()
	if err != nil {
		t.Fatalf("fullForwardProjection(shared chain): %v", err)
	}
	if len(steps) != chainLength+leafCount {
		t.Fatalf("shared-chain projection steps = %d, want %d", len(steps), chainLength+leafCount)
	}
	seen := make(map[MigrationKey]struct{}, len(steps))
	for _, step := range steps {
		if step.Direction != DirectionForward {
			t.Fatalf("shared-chain projection direction = %s", step.Direction)
		}
		if _, exists := seen[step.Key]; exists {
			t.Fatalf("shared-chain projection repeated %v", step.Key)
		}
		seen[step.Key] = struct{}{}
	}
}

func TestLoadedMaterializationMarksEmptyScalarStepStateUnchanged(t *testing.T) {
	definitions := append([]Migration(nil), lifecycleLoadedRelationCreateDefinitions()[:2]...)
	emptyKey := MigrationKey{App: "blog", Name: "0002_empty"}
	definitions = append(definitions, Migration{
		App: emptyKey.App, Name: emptyKey.Name,
		Dependencies: []MigrationKey{{App: "blog", Name: "0001_post"}},
	})
	reconstructor := mustLoadedStateReconstructor(
		t,
		definitions,
		MigrationKey{App: "blog", Name: "0001_post"},
	)
	builder := newLoadedStateBuilder()
	for index := 0; index < 2; index++ {
		if err := reconstructor.applyLoadedMigration(builder, definitions[index], DirectionForward); err != nil {
			t.Fatalf("applyLoadedMigration(%d): %v", index, err)
		}
	}
	before, err := builder.projectState()
	if err != nil {
		t.Fatalf("projectState(before empty step): %v", err)
	}
	materialized, err := reconstructor.materializeLoadedStep(
		context.Background(),
		builder,
		PlanStep{Key: emptyKey, Direction: DirectionForward},
		true,
	)
	if err != nil {
		t.Fatalf("materializeLoadedStep(empty scalar): %v", err)
	}
	if !materialized.stateUnchanged || materialized.relation || len(materialized.execution) != 0 ||
		len(materialized.prepared.after.Apps()) != 0 {
		t.Fatalf("empty scalar materialization = unchanged:%t relation:%t ops:%d retained-after:%v", materialized.stateUnchanged, materialized.relation, len(materialized.execution), materialized.prepared.after.Apps())
	}
	after, err := builder.projectState()
	if err != nil {
		t.Fatalf("projectState(after empty step): %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("empty scalar step changed builder state: before=%#v after=%#v", before, after)
	}
}

func TestLoadedDefinitionResourceScanCountsEveryAddFieldArmBeforeClone(t *testing.T) {
	t.Parallel()

	operations := make([]Operation, definitionhandoff.MaxOperations)
	for index := range operations {
		operations[index] = AddField{
			AppLabel: "sample", ModelName: "entry",
			Field: ir.Field{Name: "value", GoName: "Value", Column: "value", Kind: ir.FieldBoolean},
		}
	}
	// Sharing the caller-owned operation backing slice keeps this regression
	// cheap while proving the aggregate budget charges each visible arm in
	// every definition. 64*(2048 operations + 2048 fields)+64 definitions is
	// 262208 nodes, just beyond the shared 262144 ceiling.
	definitions := make([]Migration, 64)
	for index := range definitions {
		definitions[index] = Migration{App: "sample", Name: fmt.Sprintf("%04d", index), Operations: operations}
	}
	err := validateLoadedDefinitionResources(definitions)
	migrationError := assertStateReconstructionError(t, err, MigrationKey{}, NoOperation, "")
	if !strings.Contains(migrationError.Cause.Error(), "nodes exceed aggregate resource limit") {
		t.Fatalf("aggregate AddField resource error = %v", migrationError.Cause)
	}
}

func TestLoadedDefinitionResourceScanChargesNeutralWireOperationKind(t *testing.T) {
	t.Parallel()

	operation := AddField{
		AppLabel: "a", ModelName: "m",
		Field: ir.Field{Name: "f", GoName: "F", Column: "f", Kind: ir.FieldBoolean},
	}
	budget := loadedResourceBudget{}
	loadedScanOperationResource(&budget, Migration{App: "a", Name: "0001"}, 0, operation)
	wantBytes := uint64(len("add_field") + len("a") + len("m") + len("f") + len("F") + len("f") + len("boolean"))
	if budget.bytes != wantBytes || budget.nodes != 1 {
		t.Fatalf("AddField resource charge = bytes:%d nodes:%d, want bytes:%d nodes:1", budget.bytes, budget.nodes, wantBytes)
	}

	exhausted := loadedResourceBudget{nodeOverflow: true}
	loadedScanOperationResource(&exhausted, Migration{App: "a", Name: "0001"}, 0, operation)
	loadedScanFieldResource(&exhausted, Migration{App: "a", Name: "0001"}, 0, operation.Kind(), "operations[0].field", operation.Field)
	wantScan := loadedResourceScanCounts{operations: 1, fields: 1}
	if exhausted.scan != wantScan {
		t.Fatalf("exhausted helper scan counts = %+v, want %+v", exhausted.scan, wantScan)
	}
}

func TestLoadedNullableRelationAddAuthorityRunsInDryAndRematerializationAfterStateChecks(t *testing.T) {
	definitions := lifecycleLoadedNullableSameTargetDefinitions()
	sourceKey := MigrationKey{App: "blog", Name: "0001_article"}
	addKey := MigrationKey{App: "blog", Name: "0002_editor"}
	reconstructor := mustLoadedStateReconstructor(t, definitions, sourceKey, addKey)
	applied, err := NewAppliedState(
		MigrationKey{App: "authors", Name: "0001_author"},
		sourceKey,
	)
	if err != nil {
		t.Fatalf("NewAppliedState(): %v", err)
	}
	plan, err := reconstructor.planner.Plan(applied, NamedTarget(addKey))
	if err != nil || len(plan) != 1 || plan[0] != (PlanStep{Key: addKey, Direction: DirectionForward}) {
		t.Fatalf("nullable Add plan = (%v, %v)", plan, err)
	}
	builder, err := reconstructor.builderForApplied(context.Background(), reconstructor.planner, applied)
	if err != nil {
		t.Fatalf("builderForApplied(): %v", err)
	}
	dry, err := reconstructor.dryLoadedPlan(context.Background(), builder.clone(), plan)
	if err != nil || len(dry) != 1 || !dry[0].relation ||
		dry[0].requirements != loadedRequiresAddNullableForeignKey {
		t.Fatalf("dryLoadedPlan(nullable Add) = (%+v, %v)", dry, err)
	}
	materialized, err := reconstructor.materializeLoadedStep(context.Background(), builder.clone(), plan[0], true)
	if err != nil {
		t.Fatalf("materializeLoadedStep(nullable Add): %v", err)
	}
	if materialized.requirements != loadedRequiresAddNullableForeignKey ||
		len(materialized.intent.operations) != 2 || len(materialized.intent.operations[0].targets) != 0 ||
		len(materialized.intent.operations[1].targets) != 1 ||
		materialized.intent.operations[1].targets[0].sourceField.Name != "editor" {
		t.Fatalf("materialized nullable Add intent = %+v", materialized.intent)
	}

	// Authority decisions come after ordinary state transition and resource
	// validation. A missing historical source therefore retains the exact
	// CategoryState/operation[0] error instead of being relabelled as a
	// NoOperation capability rejection.
	corrupted := builder.clone()
	delete(corrupted.apps["blog"].models, "article")
	_, err = reconstructor.materializeLoadedStep(context.Background(), corrupted, plan[0], false)
	migrationError := assertStateReconstructionError(t, err, addKey, 0, "AddField")
	if migrationError.Category != CategoryState || migrationError.Code != CodeInvalidState ||
		!strings.Contains(migrationError.Cause.Error(), "does not exist") {
		t.Fatalf("state precedence error = %#v", migrationError)
	}
}

func TestLoadedRequiredRelationAddAuthorityRunsInDryAndRematerialization(t *testing.T) {
	definitions := lifecycleLoadedRelationAddDefinitions()
	targetKey := MigrationKey{App: "authors", Name: "0001_author"}
	sourceKey := MigrationKey{App: "blog", Name: "0001_post"}
	addKey := MigrationKey{App: "blog", Name: "0002_post_author"}
	reconstructor := mustLoadedStateReconstructor(t, definitions, addKey)
	applied, err := NewAppliedState(targetKey, sourceKey)
	if err != nil {
		t.Fatalf("NewAppliedState(): %v", err)
	}
	plan, err := reconstructor.planner.Plan(applied, NamedTarget(addKey))
	if err != nil || !reflect.DeepEqual(plan, []PlanStep{{Key: addKey, Direction: DirectionForward}}) {
		t.Fatalf("required Add plan = (%v, %v)", plan, err)
	}
	builder, err := reconstructor.builderForApplied(context.Background(), reconstructor.planner, applied)
	if err != nil {
		t.Fatalf("builderForApplied(): %v", err)
	}
	dry, err := reconstructor.dryLoadedPlan(context.Background(), builder.clone(), plan)
	if err != nil || len(dry) != 1 || !dry[0].relation ||
		dry[0].requirements != loadedRequiresAddRequiredForeignKeyToEmptyTable {
		t.Fatalf("dryLoadedPlan(required Add) = (%+v, %v)", dry, err)
	}
	materialized, err := reconstructor.materializeLoadedStep(context.Background(), builder.clone(), plan[0], true)
	if err != nil {
		t.Fatalf("materializeLoadedStep(required Add): %v", err)
	}
	if materialized.requirements != loadedRequiresAddRequiredForeignKeyToEmptyTable ||
		len(materialized.intent.operations) != 1 || len(materialized.intent.operations[0].targets) != 1 {
		t.Fatalf("materialized required Add intent = %+v requirements=%v", materialized.intent, materialized.requirements)
	}
	target := materialized.intent.operations[0].targets[0]
	if target.sourceField.Name != "author" || target.sourceField.Nullable || target.sourceField.Default != nil ||
		target.sourceField.Relation == nil || target.sourceField.Relation.OnDelete != ir.DeleteProtect ||
		target.targetModel.Name != "author" || target.targetKey.Kind != ir.FieldAuto {
		t.Fatalf("materialized required Add target = %+v", target)
	}
}

func TestLoadedDefinitionResourceScanAcceptsLoaderValidLongSemanticIdentifier(t *testing.T) {
	t.Parallel()

	longApp := strings.Repeat("a", definitionhandoff.MaxSourceIDBytes+1)
	definition := Migration{App: longApp, Name: "m", Operations: []Operation{CreateModel{
		AppLabel: longApp,
		Model: ir.Model{
			Name: "entry", GoName: "Entry", DBTable: "entry",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		},
	}}}
	if err := validateLoadedDefinitionResources([]Migration{definition}); err != nil {
		t.Fatalf("validateLoadedDefinitionResources(long semantic identifier): %v", err)
	}
}

func TestLoadedDefinitionResourceScanStopsSharedAliasTraversalAtAggregateNodes(t *testing.T) {
	fields := make([]ir.Field, definitionhandoff.MaxFieldsPerCreateModel)
	operations := make([]Operation, definitionhandoff.MaxOperations)
	for index := range operations {
		operations[index] = CreateModel{AppLabel: "a", Model: ir.Model{Fields: fields}}
	}
	definitions := make([]Migration, definitionhandoff.MaxDefinitions)
	for index := range definitions {
		definitions[index] = Migration{App: "a", Name: "m", Operations: operations}
	}

	counts, err := scanLoadedDefinitionResources(definitions)
	if err == nil || !strings.Contains(err.Error(), "aggregate resource limit") {
		t.Fatalf("shared-alias loaded scan error = %v", err)
	}
	availableNodes := uint64(definitionhandoff.MaxDefinitionNodes) -
		uint64(definitionhandoff.MaxDefinitions) -
		uint64(definitionhandoff.MaxOperations)
	nodesPerFullOperation := uint64(1 + definitionhandoff.MaxFieldsPerCreateModel)
	fullOperations := availableNodes / nodesPerFullOperation
	want := loadedResourceScanCounts{
		definitions: 1,
		operations:  fullOperations + 1,
		fields:      fullOperations * uint64(definitionhandoff.MaxFieldsPerCreateModel),
	}
	if counts != want {
		t.Fatalf("shared-alias loaded scan counts = %+v, want %+v", counts, want)
	}
}

func TestLoadedRelationBackwardRemoveAuthorityRejectsUnsealedUniversesBeforeCapability(t *testing.T) {
	tests := []struct {
		name   string
		defs   []Migration
		detail string
	}{
		{name: "different symbolic target", defs: lifecycleLoadedNullableDifferentTargetDefinitions(), detail: "different symbolic target"},
		{name: "nested target", defs: lifecycleLoadedNullableNestedTargetDefinitions(), detail: "nested relation fields"},
		{name: "multiple removes on source", defs: lifecycleLoadedMixedMultipleAddDefinitions(), detail: "at most one relation Remove"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := make([]backend.AppliedMigration, len(test.defs))
			for index := range test.defs {
				records[index] = backend.AppliedMigration{App: test.defs[index].App, Name: test.defs[index].Name}
			}
			session := newLifecycleTestSession(records, nil)
			fake := newLifecycleTestBackend(session)
			fake.relationCapabilities = lifecycleAllRelationCapabilities()
			state, err := (Executor{Backend: fake}).Migrate(
				lifecycleLoadedContext(t, test.defs),
				test.defs,
				TargetedLifecycleRequest(NamedTarget(MigrationKey{App: "blog", Name: "0001_article"})),
			)
			assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
			var capability *backend.CapabilityError
			if !errors.As(err, &capability) || capability.Feature != "relation_migration" ||
				!strings.Contains(capability.Detail, test.detail) || session.readCount != 1 ||
				fake.relationCapabilityCount != 0 || session.beginCount != 0 || session.relationBeginCount != 0 ||
				state.FormatVersion() != RelationStateFormatVersion {
				t.Fatalf(
					"backward authority rejection = err:%v capability:%#v read:%d cap:%d scalar:%d relation:%d format:%d",
					err,
					capability,
					session.readCount,
					fake.relationCapabilityCount,
					session.beginCount,
					session.relationBeginCount,
					state.FormatVersion(),
				)
			}
		})
	}
}

func mustLoadedStateReconstructor(t *testing.T, definitions []Migration, relationKeys ...MigrationKey) loadedStateReconstructor {
	t.Helper()
	planner, err := NewPlanner(definitions...)
	if err != nil {
		t.Fatalf("NewPlanner(): %v", err)
	}
	reconstructor, err := newLoadedStateReconstructor(testLoadedAuthority(planner, definitions, relationKeys...), definitions)
	if err != nil {
		t.Fatalf("newLoadedStateReconstructor(): %v", err)
	}
	return reconstructor
}

func testLoadedAuthority(planner Planner, definitions []Migration, relationKeys ...MigrationKey) *loadedDefinitionAuthority {
	relations := make(map[MigrationKey]struct{}, len(relationKeys))
	for _, key := range relationKeys {
		relations[key] = struct{}{}
	}
	profiles := make(map[MigrationKey]loadedDefinitionProfile, len(definitions))
	for _, definition := range definitions {
		profile := loadedDefinitionProfile{definitionFormat: 1, loaderABI: 1, operationCodec: 1, schemaIR: 2}
		if _, exists := relations[definition.Key()]; exists {
			profile = loadedDefinitionProfile{definitionFormat: 1, loaderABI: 2, operationCodec: 2, schemaIR: 3}
		}
		profiles[definition.Key()] = profile
	}
	return &loadedDefinitionAuthority{marker: &loadedDefinitionAuthorityMarker{}, planner: planner, profiles: profiles}
}

func afterWithoutIndependent(t *testing.T, state ProjectState) ProjectState {
	t.Helper()
	next := state.withoutApp("audit")
	if err := next.validate(); err != nil {
		t.Fatalf("state without independent branch: %v", err)
	}
	return next
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
