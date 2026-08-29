package migrationautodetect

import (
	"errors"
	"reflect"
	"regexp"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

var testProducer = definition.Producer{Name: "godj-test", Version: "1.0.0"}

func TestDetectFreshInitialAndRepeatedNoOp(t *testing.T) {
	t.Parallel()

	loaded := mustLoadDefinitions(t)
	desired := mustProjectState(t, testSchema("content", testModel("article",
		testChar("title", false, nil),
	)))

	plan, err := Detect(Request{
		Definitions: loaded,
		Desired:     desired,
		ManagedApps: []string{"content"},
	})
	if err != nil {
		t.Fatalf("Detect(fresh): %v", err)
	}
	generated := plan.Migrations()
	if len(generated) != 1 {
		t.Fatalf("fresh migrations = %#v, want one migration", generated)
	}
	wantKey := migrationspkgKey("content", "0001_initial")
	if generated[0].Key() != wantKey || len(generated[0].Dependencies) != 0 {
		t.Fatalf("fresh migration identity = %#v, dependencies=%#v", generated[0].Key(), generated[0].Dependencies)
	}
	operation, ok := generated[0].Operations[0].(migrations.CreateModel)
	if !ok || operation.AppLabel != "content" || operation.Model.Name != "article" {
		t.Fatalf("fresh operation = %#v, want content.article CreateModel", generated[0].Operations)
	}

	reloaded := mustLoadDefinitions(t, generated...)
	repeated, err := Detect(Request{
		Definitions: reloaded,
		Desired:     desired,
		ManagedApps: []string{"content"},
	})
	if err != nil {
		t.Fatalf("Detect(repeat): %v", err)
	}
	if !repeated.Empty() || len(repeated.Migrations()) != 0 {
		t.Fatalf("repeat plan = %#v, want empty", repeated.Migrations())
	}
}

func TestDetectInitializedEmptyProjectIsNoOp(t *testing.T) {
	t.Parallel()

	plan := mustDetect(t, Request{
		Definitions: mustLoadDefinitions(t),
		Desired:     migrations.EmptyProjectState(),
	})
	if !plan.Empty() || len(plan.Migrations()) != 0 {
		t.Fatalf("empty project plan = %#v, want empty", plan.Migrations())
	}
}

func TestDetectAdditiveNullableCharField(t *testing.T) {
	t.Parallel()

	initialState := mustProjectState(t, testSchema("content", testModel("article",
		testChar("title", false, nil),
	)))
	initial := initialMigrationsFromState(t, initialState)
	loaded := mustLoadDefinitions(t, initial...)
	desired := mustProjectState(t, testSchema("content", testModel("article",
		testChar("title", false, nil),
		testChar("summary", true, nil),
	)))

	plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}})
	got := plan.Migrations()
	if len(got) != 1 || got[0].Name != "0002_article_summary" {
		t.Fatalf("nullable CharField plan = %#v", got)
	}
	wantDependencies := []migrations.MigrationKey{{App: "content", Name: "0001_initial"}}
	if !reflect.DeepEqual(got[0].Dependencies, wantDependencies) {
		t.Fatalf("dependencies = %#v, want %#v", got[0].Dependencies, wantDependencies)
	}
	operation, ok := got[0].Operations[0].(migrations.AddField)
	if !ok || operation.AppLabel != "content" || operation.ModelName != "article" ||
		operation.Field.Name != "summary" || !operation.Field.Nullable || operation.Field.Default != nil {
		t.Fatalf("nullable CharField operation = %#v", got[0].Operations)
	}
	assertGeneratedState(t, loaded, got, desired)
}

func TestDetectNullableForeignKeyDependenciesAndSameAppCreationOrder(t *testing.T) {
	t.Parallel()

	t.Run("fresh cross-app candidates are topologically ordered", func(t *testing.T) {
		t.Parallel()

		desired := mustProjectState(t,
			testSchema("alpha", testModel("entry",
				testChar("title", false, nil),
				testForeignKey("author", true, "zeta", "author", "entries"),
			)),
			testSchema("zeta", testModel("author", testChar("name", false, nil))),
		)
		loaded := mustLoadDefinitions(t)
		plan := mustDetect(t, Request{
			Definitions: loaded,
			Desired:     desired,
			ManagedApps: []string{"zeta", "alpha"},
		})
		got := plan.Migrations()
		if len(got) != 2 || got[0].App != "zeta" || got[1].App != "alpha" {
			t.Fatalf("fresh cross-app order = %#v, want zeta before dependent alpha", got)
		}
		wantDependency := []migrations.MigrationKey{{App: "zeta", Name: "0001_initial"}}
		if !reflect.DeepEqual(got[1].Dependencies, wantDependency) {
			t.Fatalf("fresh cross-app dependency = %#v, want %#v", got[1].Dependencies, wantDependency)
		}
		assertGeneratedState(t, loaded, got, desired)
	})

	t.Run("cross-app AddField depends on both current leaves", func(t *testing.T) {
		t.Parallel()

		history := mustProjectState(t,
			testSchema("accounts", testModel("author", testChar("name", false, nil))),
			testSchema("content", testModel("article", testChar("title", false, nil))),
		)
		loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, history)...)
		desired := mustProjectState(t, testSchema("content", testModel("article",
			testChar("title", false, nil),
			testForeignKey("author", true, "accounts", "author", "articles"),
		)))

		plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}})
		got := plan.Migrations()
		if len(got) != 1 || got[0].Name != "0002_article_author" {
			t.Fatalf("cross-app plan = %#v", got)
		}
		wantDependencies := []migrations.MigrationKey{
			{App: "accounts", Name: "0001_initial"},
			{App: "content", Name: "0001_initial"},
		}
		if !reflect.DeepEqual(got[0].Dependencies, wantDependencies) {
			t.Fatalf("cross-app dependencies = %#v, want %#v", got[0].Dependencies, wantDependencies)
		}
		operation, ok := got[0].Operations[0].(migrations.AddField)
		if !ok || operation.Field.Relation == nil || operation.Field.Relation.Target != (ir.ModelIdentity{AppLabel: "accounts", ModelName: "author"}) {
			t.Fatalf("cross-app operation = %#v", got[0].Operations)
		}

		current := reconstructLatest(t, loaded.Definitions())
		accounts, _ := current.Schema("accounts")
		expected := mustProjectState(t, accounts, mustSchema(t, testSchema("content", testModel("article",
			testChar("title", false, nil),
			testForeignKey("author", true, "accounts", "author", "articles"),
		))))
		assertGeneratedState(t, loaded, got, expected)
	})

	t.Run("new same-app relation target is created first", func(t *testing.T) {
		t.Parallel()

		desired := mustProjectState(t, testSchema("content",
			testModel("author", testChar("name", false, nil)),
			testModel("article",
				testChar("title", false, nil),
				testForeignKey("author", false, "content", "author", "articles"),
			),
		))
		plan := mustDetect(t, Request{
			Definitions: mustLoadDefinitions(t),
			Desired:     desired,
			ManagedApps: []string{"content"},
		})
		got := plan.Migrations()
		if len(got) != 1 || len(got[0].Operations) != 2 {
			t.Fatalf("same-app creator plan = %#v", got)
		}
		first, firstOK := got[0].Operations[0].(migrations.CreateModel)
		second, secondOK := got[0].Operations[1].(migrations.CreateModel)
		if !firstOK || !secondOK || first.Model.Name != "author" || second.Model.Name != "article" {
			t.Fatalf("same-app creator order = %#v", got[0].Operations)
		}
		assertGeneratedState(t, mustLoadDefinitions(t), got, desired)
	})

	t.Run("new model precedes an existing-model AddField that targets it", func(t *testing.T) {
		t.Parallel()

		history := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil))))
		loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, history)...)
		desired := mustProjectState(t, testSchema("content",
			testModel("article",
				testChar("title", false, nil),
				testForeignKey("category", true, "content", "category", "articles"),
			),
			testModel("category", testChar("name", false, nil)),
		))
		plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}})
		got := plan.Migrations()
		if len(got) != 1 || len(got[0].Operations) != 2 {
			t.Fatalf("model plus AddField plan = %#v", got)
		}
		create, createOK := got[0].Operations[0].(migrations.CreateModel)
		add, addOK := got[0].Operations[1].(migrations.AddField)
		if !createOK || !addOK || create.Model.Name != "category" || add.ModelName != "article" || add.Field.Name != "category" {
			t.Fatalf("model plus AddField order = %#v", got[0].Operations)
		}
		assertGeneratedState(t, loaded, got, desired)
	})
}

func TestDetectCrossAppRelationsToHistoricalModelsDoNotCreateCandidateCycle(t *testing.T) {
	t.Parallel()

	history := mustProjectState(t,
		testSchema("alpha", testModel("author", testChar("name", false, nil))),
		testSchema("beta", testModel("category", testChar("name", false, nil))),
	)
	loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, history)...)
	desired := mustProjectState(t,
		testSchema("alpha",
			testModel("author", testChar("name", false, nil)),
			testModel("note",
				testChar("body", false, nil),
				testForeignKey("category", false, "beta", "category", "notes"),
			),
		),
		testSchema("beta",
			testModel("category", testChar("name", false, nil)),
			testModel("post",
				testChar("title", false, nil),
				testForeignKey("author", false, "alpha", "author", "posts"),
			),
		),
	)

	plan := mustDetect(t, Request{
		Definitions: loaded,
		Desired:     desired,
		ManagedApps: []string{"beta", "alpha"},
	})
	got := plan.Migrations()
	if len(got) != 2 || got[0].App != "alpha" || got[1].App != "beta" {
		t.Fatalf("historical-target plan order = %#v", got)
	}
	wantAlphaDependencies := []migrations.MigrationKey{
		{App: "alpha", Name: "0001_initial"},
		{App: "beta", Name: "0001_initial"},
	}
	wantBetaDependencies := []migrations.MigrationKey{
		{App: "alpha", Name: "0001_initial"},
		{App: "beta", Name: "0001_initial"},
	}
	if !reflect.DeepEqual(got[0].Dependencies, wantAlphaDependencies) {
		t.Fatalf("alpha dependencies = %#v, want historical leaves %#v", got[0].Dependencies, wantAlphaDependencies)
	}
	if !reflect.DeepEqual(got[1].Dependencies, wantBetaDependencies) {
		t.Fatalf("beta dependencies = %#v, want historical leaves %#v", got[1].Dependencies, wantBetaDependencies)
	}
	assertGeneratedState(t, loaded, got, desired)
}

func TestDetectIsDeterministicAcrossManagedDesiredAndSourceOrdering(t *testing.T) {
	t.Parallel()

	alphaState := mustProjectState(t, testSchema("alpha", testModel("alpha_item", testChar("name", false, nil))))
	zetaState := mustProjectState(t, testSchema("zeta", testModel("zeta_item", testChar("name", false, nil))))
	alphaMigration := initialMigrationsFromState(t, alphaState)[0]
	zetaMigration := initialMigrationsFromState(t, zetaState)[0]
	forward := mustLoadDefinitions(t, alphaMigration, zetaMigration)
	reverse := mustLoadDefinitions(t, zetaMigration, alphaMigration)

	alphaDesired := testSchema("alpha", testModel("alpha_item",
		testChar("name", false, nil),
		testChar("note", true, nil),
	))
	zetaDesired := testSchema("zeta", testModel("zeta_item",
		testChar("name", false, nil),
		testChar("note", true, nil),
	))
	left := mustDetect(t, Request{
		Definitions: forward,
		Desired:     mustProjectState(t, alphaDesired, zetaDesired),
		ManagedApps: []string{"zeta", "alpha"},
	}).Migrations()
	right := mustDetect(t, Request{
		Definitions: reverse,
		Desired:     mustProjectState(t, zetaDesired, alphaDesired),
		ManagedApps: []string{"alpha", "zeta"},
	}).Migrations()

	if !reflect.DeepEqual(left, right) {
		t.Fatalf("ordering changed plan:\nleft  = %#v\nright = %#v", left, right)
	}
	if len(left) != 2 || left[0].App != "alpha" || left[1].App != "zeta" {
		t.Fatalf("canonical app ordering = %#v", left)
	}
}

func TestDetectPreservesUnmanagedStaticAppsAndReconstructsDesiredManagedState(t *testing.T) {
	t.Parallel()

	history := mustProjectState(t,
		testSchema("content", testModel("article", testChar("title", false, nil))),
		testSchema("godj_system", testModel("keyring", testChar("active_key", false, nil))),
	)
	loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, history)...)
	desired := mustProjectState(t, testSchema("content", testModel("article",
		testChar("title", false, nil),
		testChar("summary", true, nil),
	)))

	plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}})
	current := reconstructLatest(t, loaded.Definitions())
	staticSchema, exists := current.Schema("godj_system")
	if !exists {
		t.Fatal("historical static app is absent before detection")
	}
	contentSchema, _ := desired.Schema("content")
	expected := mustProjectState(t, staticSchema, contentSchema)
	assertGeneratedState(t, loaded, plan.Migrations(), expected)
}

func TestPlanMigrationsAreDeepCopyIsolated(t *testing.T) {
	t.Parallel()

	accounts := mustProjectState(t, testSchema("accounts", testModel("author", testChar("name", false, nil))))
	loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, accounts)...)
	desiredSchema := testSchema("content", testModel("article",
		testChar("title", false, &ir.ScalarDefault{Kind: ir.ScalarString, String: "draft"}),
		testForeignKey("author", true, "accounts", "author", "articles"),
	))
	desired := mustProjectState(t, desiredSchema)
	managed := []string{"content"}
	plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: managed})
	original := plan.Migrations()

	managed[0] = "mutated"
	desiredSchema.Models[0].Fields[0].Default.String = "mutated"
	first := plan.Migrations()
	first[0].Name = "mutated"
	first[0].Dependencies[0].Name = "mutated"
	operation := first[0].Operations[0].(migrations.CreateModel)
	operation.Model.Fields[1].Default.String = "mutated"
	operation.Model.Fields[2].Relation.Target.AppLabel = "mutated"
	first[0].Operations[0] = operation

	second := plan.Migrations()
	if !reflect.DeepEqual(second, original) {
		t.Fatalf("Plan.Migrations aliases caller state:\nsecond   = %#v\noriginal = %#v", second, original)
	}
	secondOperation := second[0].Operations[0].(migrations.CreateModel)
	if secondOperation.Model.Fields[1].Default.String != "draft" || secondOperation.Model.Fields[2].Relation.Target.AppLabel != "accounts" {
		t.Fatalf("nested model state was mutated: %#v", secondOperation.Model)
	}
}

func TestDetectRejectsUnsafeExistingTableAddField(t *testing.T) {
	t.Parallel()

	initial := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil))))
	loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, initial)...)
	tests := []struct {
		name  string
		field ir.Field
	}{
		{name: "non-null CharField", field: testChar("slug", false, nil)},
		{name: "nullable CharField with default", field: testChar("summary", true, &ir.ScalarDefault{Kind: ir.ScalarString, String: ""})},
		{name: "non-null BooleanField", field: testBoolean("published", false)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			desired := mustProjectState(t, testSchema("content", testModel("article",
				testChar("title", false, nil),
				test.field,
			)))
			err := detectError(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}}, CodeUnsupportedChange)
			if err.App != "content" || err.Model != "article" || err.Field != test.field.Name {
				t.Fatalf("error location = %+v", err)
			}
		})
	}
}

func TestDetectRejectsRemovalReorderAndAlter(t *testing.T) {
	t.Parallel()

	base := mustProjectState(t, testSchema("content",
		testModel("article", testChar("title", false, nil), testChar("body", false, nil)),
		testModel("comment", testChar("text", false, nil)),
	))
	loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, base)...)
	tests := []struct {
		name    string
		desired migrations.ProjectState
	}{
		{name: "app removal", desired: migrations.EmptyProjectState()},
		{name: "model removal", desired: mustProjectState(t, testSchema("content",
			testModel("article", testChar("title", false, nil), testChar("body", false, nil)),
		))},
		{name: "model reorder", desired: mustProjectState(t, testSchema("content",
			testModel("comment", testChar("text", false, nil)),
			testModel("article", testChar("title", false, nil), testChar("body", false, nil)),
		))},
		{name: "field removal", desired: mustProjectState(t, testSchema("content",
			testModel("article", testChar("title", false, nil)),
			testModel("comment", testChar("text", false, nil)),
		))},
		{name: "field reorder", desired: mustProjectState(t, testSchema("content",
			testModel("article", testChar("body", false, nil), testChar("title", false, nil)),
			testModel("comment", testChar("text", false, nil)),
		))},
		{name: "field alter", desired: mustProjectState(t, testSchema("content",
			testModel("article", testCharLength("title", false, nil, 512), testChar("body", false, nil)),
			testModel("comment", testChar("text", false, nil)),
		))},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			detectError(t, Request{Definitions: loaded, Desired: test.desired, ManagedApps: []string{"content"}}, CodeUnsupportedChange)
		})
	}
}

func TestDetectRejectsSelfAndLaterSameAppRelationTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		desired migrations.ProjectState
		field   string
	}{
		{
			name: "self target",
			desired: mustProjectState(t, testSchema("content", testModel("article",
				testChar("title", false, nil),
				testForeignKey("parent", true, "content", "article", "children"),
			))),
			field: "parent",
		},
		{
			name: "later-created target",
			desired: mustProjectState(t, testSchema("content",
				testModel("article",
					testChar("title", false, nil),
					testForeignKey("author", false, "content", "author", "articles"),
				),
				testModel("author", testChar("name", false, nil)),
			)),
			field: "author",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := detectError(t, Request{
				Definitions: mustLoadDefinitions(t),
				Desired:     test.desired,
				ManagedApps: []string{"content"},
			}, CodeInvalidRelation)
			if err.App != "content" || err.Model != "article" || err.Field != test.field {
				t.Fatalf("relation error location = %+v", err)
			}
		})
	}

	t.Run("existing model self AddField", func(t *testing.T) {
		t.Parallel()
		history := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil))))
		loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, history)...)
		desired := mustProjectState(t, testSchema("content", testModel("article",
			testChar("title", false, nil),
			testForeignKey("parent", true, "content", "article", "children"),
		)))
		err := detectError(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}}, CodeInvalidRelation)
		if err.App != "content" || err.Model != "article" || err.Field != "parent" {
			t.Fatalf("self AddField error location = %+v", err)
		}
	})

	t.Run("missing cross-app authority", func(t *testing.T) {
		t.Parallel()
		desired := mustProjectState(t, testSchema("content", testModel("article",
			testChar("title", false, nil),
			testForeignKey("author", true, "accounts", "author", "articles"),
		)))
		detectError(t, Request{
			Definitions: mustLoadDefinitions(t),
			Desired:     desired,
			ManagedApps: []string{"content"},
		}, CodeInvalidRelation)
	})

	t.Run("missing cross-app target model identifies source field", func(t *testing.T) {
		t.Parallel()
		desired := mustProjectState(t,
			testSchema("accounts", testModel("group", testChar("name", false, nil))),
			testSchema("content", testModel("article",
				testChar("title", false, nil),
				testForeignKey("author", true, "accounts", "author", "articles"),
			)),
		)
		err := detectError(t, Request{
			Definitions: mustLoadDefinitions(t),
			Desired:     desired,
			ManagedApps: []string{"accounts", "content"},
		}, CodeInvalidRelation)
		if err.App != "content" || err.Model != "article" || err.Field != "author" {
			t.Fatalf("missing cross-app target error location = %+v", err)
		}
	})
}

func TestDetectRejectsCrossAppCandidateCycle(t *testing.T) {
	t.Parallel()

	desired := mustProjectState(t,
		testSchema("alpha", testModel("alpha_item",
			testForeignKey("beta", true, "beta", "beta_item", "alpha_items"),
		)),
		testSchema("beta", testModel("beta_item",
			testForeignKey("alpha", true, "alpha", "alpha_item", "beta_items"),
		)),
	)
	detectError(t, Request{
		Definitions: mustLoadDefinitions(t),
		Desired:     desired,
		ManagedApps: []string{"beta", "alpha"},
	}, CodeInvalidGeneratedPlan)
}

func TestDetectHonorsDefinitionOperationLimit(t *testing.T) {
	t.Parallel()

	history := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil))))
	loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, history)...)
	stateWithAddedFields := func(count int) migrations.ProjectState {
		fields := make([]ir.Field, 0, count+1)
		fields = append(fields, testChar("title", false, nil))
		for index := 0; index < count; index++ {
			fields = append(fields, testChar(indexedFieldName(index), true, nil))
		}
		return mustProjectState(t, testSchema("content", testModel("article", fields...)))
	}

	atLimit := mustDetect(t, Request{
		Definitions: loaded,
		Desired:     stateWithAddedFields(definition.MaxOperationsPerMigration),
		ManagedApps: []string{"content"},
	}).Migrations()
	if len(atLimit) != 1 || len(atLimit[0].Operations) != definition.MaxOperationsPerMigration {
		t.Fatalf("at-limit operation count = %#v", atLimit)
	}
	if _, err := definition.Encode(testProducer, atLimit[0]); err != nil {
		t.Fatalf("definition.Encode(at limit): %v", err)
	}

	detectError(t, Request{
		Definitions: loaded,
		Desired:     stateWithAddedFields(definition.MaxOperationsPerMigration + 1),
		ManagedApps: []string{"content"},
	}, CodeInvalidGeneratedPlan)
}

func TestDetectRejectsAmbiguousNoncanonicalAndInvalidRequests(t *testing.T) {
	t.Parallel()

	t.Run("multiple app leaves", func(t *testing.T) {
		t.Parallel()
		leftModel := mustModel(t, "content", testModel("article", testChar("title", false, nil)))
		rightModel := mustModel(t, "content", testModel("comment", testChar("text", false, nil)))
		loaded := mustLoadDefinitions(t,
			migrations.Migration{App: "content", Name: "0001_article", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "content", Model: leftModel}}},
			migrations.Migration{App: "content", Name: "0001_comment", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "content", Model: rightModel}}},
		)
		desired := reconstructLatest(t, loaded.Definitions())
		detectError(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}}, CodeAmbiguousHistory)
	})

	t.Run("noncanonical leaf name", func(t *testing.T) {
		t.Parallel()
		model := mustModel(t, "content", testModel("article", testChar("title", false, nil)))
		loaded := mustLoadDefinitions(t, migrations.Migration{
			App: "content", Name: "legacy_initial",
			Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "content", Model: model}},
		})
		desired := mustProjectState(t, testSchema("content", testModel("article",
			testChar("title", false, nil),
			testChar("summary", true, nil),
		)))
		detectError(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}}, CodeUnsupportedChange)
	})

	t.Run("zero definition set", func(t *testing.T) {
		t.Parallel()
		detectError(t, Request{
			Desired:     mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil)))),
			ManagedApps: []string{"content"},
		}, CodeInvalidRequest)
	})

	t.Run("desired app is not managed", func(t *testing.T) {
		t.Parallel()
		detectError(t, Request{
			Definitions: mustLoadDefinitions(t),
			Desired:     mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil)))),
		}, CodeInvalidRequest)
	})

	t.Run("duplicate managed app", func(t *testing.T) {
		t.Parallel()
		desired := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil))))
		detectError(t, Request{
			Definitions: mustLoadDefinitions(t), Desired: desired,
			ManagedApps: []string{"content", "content"},
		}, CodeInvalidRequest)
	})
}

func TestDetectCleanHistoryDoesNotNeedSuccessorNaming(t *testing.T) {
	t.Parallel()

	model := mustModel(t, "content", testModel("article", testChar("title", false, nil)))
	loaded := mustLoadDefinitions(t, migrations.Migration{
		App: "content", Name: "legacy_initial",
		Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "content", Model: model}},
	})
	desired := reconstructLatest(t, loaded.Definitions())
	plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}})
	if !plan.Empty() {
		t.Fatalf("Detect(clean noncanonical history) = %#v, want empty plan", plan.Migrations())
	}
}

func TestDetectCompositeNameIsStableAndContentDerived(t *testing.T) {
	t.Parallel()

	initial := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil))))
	loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, initial)...)
	desired := mustProjectState(t, testSchema("content", testModel("article",
		testChar("title", false, nil),
		testChar("summary", true, nil),
		testChar("note", true, nil),
	)))

	request := Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}}
	first := mustDetect(t, request).Migrations()
	second := mustDetect(t, request).Migrations()
	if !reflect.DeepEqual(first, second) || len(first) != 1 {
		t.Fatalf("composite plan is unstable:\nfirst  = %#v\nsecond = %#v", first, second)
	}
	if matched := regexp.MustCompile(`^0002_auto_[0-9a-f]{12}$`).MatchString(first[0].Name); !matched {
		t.Fatalf("composite name = %q, want bounded digest name", first[0].Name)
	}
	if first[0].Name != "0002_auto_f4cce340cc30" {
		t.Fatalf("composite name = %q, want versioned-domain golden", first[0].Name)
	}
	if len(first[0].Operations) != 2 {
		t.Fatalf("composite operations = %#v", first[0].Operations)
	}

	changed := mustProjectState(t, testSchema("content", testModel("article",
		testChar("title", false, nil),
		testCharLength("summary", true, nil, 256),
		testChar("note", true, nil),
	)))
	changedPlan := mustDetect(t, Request{Definitions: loaded, Desired: changed, ManagedApps: []string{"content"}}).Migrations()
	if changedPlan[0].Name == first[0].Name {
		t.Fatalf("composite name did not change with operation content: %q", first[0].Name)
	}
	assertGeneratedState(t, loaded, first, desired)
}

func mustDetect(t *testing.T, request Request) Plan {
	t.Helper()
	plan, err := Detect(request)
	if err != nil {
		t.Fatalf("Detect(): %v", err)
	}
	return plan
}

func detectError(t *testing.T, request Request, code ErrorCode) *Error {
	t.Helper()
	plan, err := Detect(request)
	if err == nil {
		t.Fatalf("Detect() plan = %#v, want %s error", plan.Migrations(), code)
	}
	if !plan.Empty() || len(plan.Migrations()) != 0 {
		t.Fatalf("Detect() returned non-empty plan with error: %#v", plan.Migrations())
	}
	var detection *Error
	if !errors.As(err, &detection) {
		t.Fatalf("Detect() error = %T %v, want *Error", err, err)
	}
	if detection.Code != code {
		t.Fatalf("Detect() code = %q, want %q: %v", detection.Code, code, err)
	}
	return detection
}

func mustLoadDefinitions(t *testing.T, definitions ...migrations.Migration) migrations.LoadedDefinitionSet {
	t.Helper()
	sources := make([]definition.Source, len(definitions))
	for index, migration := range definitions {
		document, err := definition.Encode(testProducer, migration)
		if err != nil {
			t.Fatalf("definition.Encode(%s.%s): %v", migration.App, migration.Name, err)
		}
		sources[index] = definition.Source{
			SourceID: migration.App + "/" + migration.Name,
			Document: document,
		}
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatalf("definition.Load(): %v", err)
	}
	return loaded
}

func initialMigrationsFromState(t *testing.T, state migrations.ProjectState) []migrations.Migration {
	t.Helper()
	result := make([]migrations.Migration, 0, len(state.Apps()))
	for _, app := range state.Apps() {
		schema, _ := state.Schema(app)
		operations := make([]migrations.Operation, len(schema.Models))
		for index, model := range schema.Models {
			operations[index] = migrations.CreateModel{AppLabel: app, Model: model.Clone()}
		}
		result = append(result, migrations.Migration{App: app, Name: "0001_initial", Operations: operations})
	}
	return result
}

func assertGeneratedState(t *testing.T, loaded migrations.LoadedDefinitionSet, generated []migrations.Migration, expected migrations.ProjectState) {
	t.Helper()
	combined := append(loaded.Definitions(), generated...)
	actual := reconstructLatest(t, combined)
	if !actual.Equal(expected) {
		t.Fatalf("generated reconstruction differs:\nactual   = %#v\nexpected = %#v", actual, expected)
	}
}

func reconstructLatest(t *testing.T, definitions []migrations.Migration) migrations.ProjectState {
	t.Helper()
	reconstructor, err := migrations.NewStateReconstructor(definitions...)
	if err != nil {
		t.Fatalf("NewStateReconstructor(): %v", err)
	}
	state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatalf("Reconstruct(latest): %v", err)
	}
	return state
}

func mustProjectState(t *testing.T, schemas ...ir.Schema) migrations.ProjectState {
	t.Helper()
	state, err := migrations.NewProjectState(schemas...)
	if err != nil {
		t.Fatalf("NewProjectState(): %v", err)
	}
	return state
}

func mustSchema(t *testing.T, schema ir.Schema) ir.Schema {
	t.Helper()
	normalized, err := ir.Normalize(schema)
	if err != nil {
		t.Fatalf("ir.Normalize(%s): %v", schema.AppLabel, err)
	}
	return normalized
}

func mustModel(t *testing.T, app string, model ir.Model) ir.Model {
	t.Helper()
	return mustSchema(t, testSchema(app, model)).Models[0]
}

func testSchema(app string, models ...ir.Model) ir.Schema {
	return ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: app, Models: models}
}

func testModel(name string, fields ...ir.Field) ir.Model {
	goName := ""
	for index, current := range name {
		if index == 0 && current >= 'a' && current <= 'z' {
			current -= 'a' - 'A'
		}
		if current != '_' {
			goName += string(current)
		}
	}
	return ir.Model{Name: name, GoName: goName, Fields: fields}
}

func testChar(name string, nullable bool, defaultValue *ir.ScalarDefault) ir.Field {
	return testCharLength(name, nullable, defaultValue, 255)
}

func testCharLength(name string, nullable bool, defaultValue *ir.ScalarDefault, maxLength int) ir.Field {
	return ir.Field{
		Name: name, GoName: testGoName(name), Kind: ir.FieldChar,
		Nullable: nullable, MaxLength: maxLength, Default: defaultValue,
	}
}

func testBoolean(name string, defaultValue bool) ir.Field {
	return ir.Field{
		Name: name, GoName: testGoName(name), Kind: ir.FieldBoolean,
		Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: defaultValue},
	}
}

func testForeignKey(name string, nullable bool, targetApp, targetModel, reverseName string) ir.Field {
	onDelete := ir.DeleteProtect
	if nullable {
		onDelete = ir.DeleteSetNull
	}
	return ir.Field{
		Name: name, GoName: testGoName(name), Kind: ir.FieldForeignKey, Nullable: nullable,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: targetApp, ModelName: targetModel},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: reverseName},
			OnDelete:    onDelete,
		},
	}
}

func testGoName(name string) string {
	goName := ""
	upper := true
	for _, current := range name {
		if current == '_' {
			upper = true
			continue
		}
		if upper && current >= 'a' && current <= 'z' {
			current -= 'a' - 'A'
		}
		upper = false
		goName += string(current)
	}
	return goName
}

func indexedFieldName(index int) string {
	const digits = "0123456789"
	if index == 0 {
		return "field_0"
	}
	value := ""
	for index != 0 {
		value = string(digits[index%10]) + value
		index /= 10
	}
	return "field_" + value
}

func migrationspkgKey(app, name string) migrations.MigrationKey {
	return migrations.MigrationKey{App: app, Name: name}
}
