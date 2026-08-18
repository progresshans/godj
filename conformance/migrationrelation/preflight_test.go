package migrationrelation

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// The historical top-level Test names retain "ZeroIO" for inventory
// continuity. Here it means only that this pure candidate does not invoke
// catalog, creator-index, or historical-state I/O ports; runtime session-open
// and connection-pinning behavior belongs to the lifecycle proof.

func TestPreflightWholeProjectMatrixHasOneValidNineFailuresAndZeroIO(t *testing.T) {
	t.Parallel()

	type preflightMatrixCase struct {
		name     string
		wantCode string
		mutate   func(*preflightInput)
	}
	cases := []preflightMatrixCase{
		{name: "valid_cross_app_ancestry"},
		{
			name: "source_model_missing", wantCode: "source_model_not_found",
			mutate: func(input *preflightInput) {
				delete(input.State.apps, "blog")
				input.Definitions[1].Operations = nil
			},
		},
		{
			name: "target_model_missing", wantCode: "target_model_not_found",
			mutate: func(input *preflightInput) {
				delete(input.State.apps, "authors")
				input.Definitions[0].Operations = nil
			},
		},
		{
			name: "target_creator_later_in_same_migration", wantCode: "target_creator_not_ancestor",
			mutate: func(input *preflightInput) {
				preflightMoveTargetCreatorAfterRelation(input)
			},
		},
		{
			name: "declared_table_mismatch", wantCode: "declared_table_mismatch",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.DeclaredTable = "wrong_table"
			},
		},
		{
			name: "declared_column_mismatch", wantCode: "declared_column_mismatch",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.DeclaredColumn = "wrong_column"
			},
		},
		{
			name: "reverse_namespace_collision", wantCode: "reverse_namespace_collision",
			mutate: func(input *preflightInput) {
				schema := input.State.apps["authors"]
				schema.Models[0].Fields = append(schema.Models[0].Fields, ir.Field{
					Name: "articles", GoName: "Articles", Column: "articles", Kind: ir.FieldChar, MaxLength: 32,
				})
				input.State.apps["authors"] = schema
			},
		},
		{
			name: "nullability_metadata_mismatch", wantCode: "relation_nullability_mismatch",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.DeclaredNullable = true
			},
		},
		{
			name: "creator_not_in_dependency_ancestry", wantCode: "target_creator_not_ancestor",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Dependencies = input.Definitions[2].Dependencies[1:]
			},
		},
		{
			name: "relation_editor_unavailable", wantCode: "relation_editor_unsupported",
			mutate: func(input *preflightInput) { input.Capability.RelationEditor = false },
		},
	}

	validCount := 0
	failureCount := 0
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			input := preflightFixture()
			if test.mutate != nil {
				test.mutate(&input)
			}
			snapshot, metrics, err := preflightValidate(input)
			if metrics != (preflightIOMetrics{}) {
				t.Fatalf("preflight performed catalog/creator/state I/O: %+v", metrics)
			}
			if test.wantCode == "" {
				validCount++
				if err != nil || len(snapshot.creators) != 2 || len(snapshot.relations) != 1 {
					t.Fatalf("valid preflight = creators:%d relations:%d error:%v", len(snapshot.creators), len(snapshot.relations), err)
				}
				return
			}
			failureCount++
			var failure *preflightCandidateError
			if !errors.As(err, &failure) || failure.Category != "migration_relation_preflight_candidate_error" ||
				failure.Stage != "preflight" || failure.Code != test.wantCode {
				t.Fatalf("failure = %#v, want %s", err, test.wantCode)
			}
			if len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
				t.Fatalf("failed preflight published partial snapshot: %#v", snapshot)
			}
		})
	}
	if validCount != 1 || failureCount != 9 {
		t.Fatalf("matrix counts = valid:%d failures:%d, want 1/9", validCount, failureCount)
	}
}

func TestPreflightHistoricalRelationMetadataMustExactlyMatchDeclarationWithoutIO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantCode string
		mutate   func(*preflightInput)
	}{
		{
			name: "source field not foreign key", wantCode: "source_field_not_foreign_key",
			mutate: func(input *preflightInput) {
				scalarRelation := input.Definitions[2].Operations[0]
				relation := &scalarRelation.Relation
				relation.Field = "title"
				relation.DeclaredColumn = "title"
				input.Definitions[2].Operations = []preflightOperation{scalarRelation}
			},
		},
		{
			name: "relation target mismatch", wantCode: "relation_target_mismatch",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.Target = stateModelIdentity{App: "blog", Model: "article"}
			},
		},
		{
			name: "relation cardinality mismatch", wantCode: "relation_cardinality_mismatch",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.Cardinality = ir.RelationOneToMany
			},
		},
		{
			name: "relation reverse name mismatch", wantCode: "relation_reverse_mismatch",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.Reverse.Name = "other_articles"
			},
		},
		{
			name: "relation reverse disabled mismatch", wantCode: "relation_reverse_mismatch",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.Reverse = ir.ReverseRelation{Disabled: true}
			},
		},
		{
			name: "relation delete mismatch", wantCode: "relation_on_delete_mismatch",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.OnDelete = ir.DeleteSetNull
			},
		},
		{
			name: "relation nullability mismatch", wantCode: "relation_nullability_mismatch",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.DeclaredNullable = true
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := preflightFixture()
			test.mutate(&input)
			snapshot, metrics, err := preflightValidate(input)
			if metrics != (preflightIOMetrics{}) {
				t.Fatalf("metadata mismatch performed catalog/creator/state I/O: %+v", metrics)
			}
			if !preflightErrorCode(err, test.wantCode) {
				t.Fatalf("metadata mismatch failure = %#v, want %s", err, test.wantCode)
			}
			if len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
				t.Fatalf("metadata mismatch published partial snapshot: %#v", snapshot)
			}
		})
	}

	t.Run("target key is derived from exact historical AutoField and snapshotted in backend intent", func(t *testing.T) {
		input := preflightFixture()
		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.relations) != 1 {
			t.Fatalf("derived target key preflight = snapshot:%#v metrics:%+v err:%v", snapshot, metrics, err)
		}
		intent := snapshot.relations[0].BackendIntent
		if intent.SourceTable != "blog_article" || intent.SourceColumn != "author_id" ||
			intent.TargetTable != "authors_author" || intent.TargetKey.Name != "id" ||
			intent.TargetKey.Column != "id" || intent.TargetKey.Kind != ir.FieldAuto ||
			!intent.TargetKey.PrimaryKey || intent.TargetKey.Nullable {
			t.Fatalf("derived backend intent = %#v", intent)
		}

		// The declaration has no target-field carrier. Mutating the historical
		// creator after publication cannot forge or alias the derived snapshot.
		input.Definitions[0].Operations[0].ModelState.Fields[0].Name = "mutated_caller"
		intent.TargetKey.Name = "mutated_accessor"
		fresh := snapshot.preflightRelations()[0].BackendIntent.TargetKey
		if fresh.Name != "id" || fresh.Column != "id" || fresh.Kind != ir.FieldAuto {
			t.Fatalf("derived target key retained alias: %#v", fresh)
		}
	})

	t.Run("target key derivation fails closed for missing multiple non-Auto and nullable shapes", func(t *testing.T) {
		valid := preflightFixture().Definitions[0].Operations[0].ModelState.Clone()
		for _, test := range []struct {
			name   string
			mutate func(*ir.Model)
		}{
			{name: "missing", mutate: func(model *ir.Model) { model.Fields[0].PrimaryKey = false }},
			{name: "multiple", mutate: func(model *ir.Model) {
				model.Fields = append(model.Fields, ir.Field{
					Name: "second_id", GoName: "SecondID", Column: "second_id", Kind: ir.FieldAuto, PrimaryKey: true,
				})
			}},
			{name: "non_auto", mutate: func(model *ir.Model) {
				model.Fields[0].Kind = ir.FieldChar
				model.Fields[0].MaxLength = 32
			}},
			{name: "nullable", mutate: func(model *ir.Model) { model.Fields[0].Nullable = true }},
		} {
			t.Run(test.name, func(t *testing.T) {
				model := valid.Clone()
				test.mutate(&model)
				if field, ok := preflightAutoPrimaryKey(model); ok || !reflect.DeepEqual(field, ir.Field{}) {
					t.Fatalf("invalid target key derived: %#v ok=%v", field, ok)
				}
			})
		}
	})

	t.Run("nullable set-null with disabled reverse preserves full metadata", func(t *testing.T) {
		input := preflightFixture()
		schema := input.State.apps["blog"]
		field := &schema.Models[0].Fields[2]
		field.Nullable = true
		field.Relation.Reverse = ir.ReverseRelation{Disabled: true}
		field.Relation.OnDelete = ir.DeleteSetNull
		input.State.apps["blog"] = schema
		input.Definitions[2].Operations[0].After = schema.Models[0].Clone()
		relation := &input.Definitions[2].Operations[0].Relation
		relation.DeclaredNullable = true
		relation.Reverse = ir.ReverseRelation{Disabled: true}
		relation.OnDelete = ir.DeleteSetNull
		input.PlanTarget = input.State.stateClone()
		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.relations) != 1 {
			t.Fatalf("disabled/set-null preflight = snapshot:%#v metrics:%+v err:%v", snapshot, metrics, err)
		}
		got := snapshot.relations[0].Declaration
		if got.Cardinality != ir.RelationManyToOne || !got.Reverse.Disabled || got.Reverse.Name != "" ||
			got.OnDelete != ir.DeleteSetNull || !got.DeclaredNullable {
			t.Fatalf("full relation metadata lost: %#v", got)
		}
	})
}

func TestPreflightRelationDeclarationIdentityAndFullGraphAreCanonicalAndZeroIO(t *testing.T) {
	t.Parallel()

	t.Run("duplicate source field wins before conflicting metadata", func(t *testing.T) {
		base := preflightFixture()
		duplicate := base.Definitions[2].Operations[0]
		duplicate.Relation.Target = duplicate.Relation.Source
		duplicate.Relation.DeclaredColumn = "wrong_column"
		base.Definitions[2].Operations = append(base.Definitions[2].Operations, duplicate)
		base.Capability.RelationEditor = false

		for permutation := 0; permutation < 2; permutation++ {
			input := preflightCloneInput(base)
			if permutation != 0 {
				for left, right := 0, len(input.Definitions)-1; left < right; left, right = left+1, right-1 {
					input.Definitions[left], input.Definitions[right] = input.Definitions[right], input.Definitions[left]
				}
			}
			snapshot, metrics, err := preflightValidate(input)
			var failure *preflightCandidateError
			if !errors.As(err, &failure) || failure.Code != "duplicate_relation_declaration" ||
				failure.Reason != "duplicate_source_field_declaration" ||
				failure.Source != (stateModelIdentity{App: "blog", Model: "article"}) ||
				failure.Field != "author" || failure.Target != (stateModelIdentity{}) ||
				failure.Owner != (preflightMigrationKey{}) {
				t.Fatalf("permutation %d duplicate failure = %#v", permutation, err)
			}
			if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
				t.Fatalf("permutation %d duplicate published/performed catalog/creator/state I/O: snapshot=%#v metrics=%+v", permutation, snapshot, metrics)
			}
		}
	})

	t.Run("self relation is rejected from the full operation graph", func(t *testing.T) {
		input := preflightFixture()
		schema := input.State.apps["blog"]
		schema.Models[0].Fields[2].Relation.Target = ir.ModelIdentity{AppLabel: "blog", ModelName: "article"}
		input.State.apps["blog"] = schema
		input.Definitions[2].Operations[0].After = schema.Models[0].Clone()
		relation := &input.Definitions[2].Operations[0].Relation
		relation.Target = relation.Source
		snapshot, metrics, err := preflightValidate(input)
		var failure *preflightCandidateError
		if !errors.As(err, &failure) || failure.Code != "self_relation_unsupported" ||
			failure.Reason != "self_relation_unsupported" || failure.Source != failure.Target ||
			failure.Field != "author" {
			t.Fatalf("self relation failure = %#v", err)
		}
		if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
			t.Fatalf("self relation published/performed catalog/creator/state I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
		}
	})

	t.Run("new edge closing a cycle through unchanged history is invariant", func(t *testing.T) {
		base := preflightFixture()
		authorIdentity := stateModelIdentity{App: "authors", Model: "author"}
		articleIdentity := stateModelIdentity{App: "blog", Model: "article"}
		authorsRelation := preflightMigrationKey{App: "authors", Name: "0002_favorite_article"}
		authorSchema := base.State.apps["authors"]
		authorSchema.Models[0].Fields = append(authorSchema.Models[0].Fields, ir.Field{
			Name: "favorite_article", GoName: "FavoriteArticleID", Column: "favorite_article_id", Kind: ir.FieldForeignKey, Nullable: true,
			Relation: &ir.ForeignKeyRelation{
				Target:      articleIdentity.stateIRIdentity(),
				Cardinality: ir.RelationManyToOne,
				Reverse:     ir.ReverseRelation{Name: "favorite_authors"},
				OnDelete:    ir.DeleteProtect,
			},
		})
		base.State.apps["authors"] = authorSchema
		authorBefore := base.Definitions[0].Operations[0].ModelState.Clone()
		authorAfter := authorSchema.Models[0].Clone()
		base.Definitions = append(base.Definitions, preflightDefinition{
			Key:          authorsRelation,
			Dependencies: []preflightMigrationKey{{App: "blog", Name: "0002_article_author"}},
			Operations: []preflightOperation{{
				Kind:   preflightAddRelation,
				Before: authorBefore,
				After:  authorAfter,
				Relation: preflightRelationDeclaration{
					Source: authorIdentity, Field: "favorite_article", Target: articleIdentity,
					DeclaredTable: "authors_author", DeclaredColumn: "favorite_article_id", DeclaredNullable: true,
					Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Name: "favorite_authors"}, OnDelete: ir.DeleteProtect,
				},
			}},
		})

		var canonical *preflightCandidateError
		for permutation := 0; permutation < 2; permutation++ {
			input := preflightCloneInput(base)
			if permutation != 0 {
				for left, right := 0, len(input.Definitions)-1; left < right; left, right = left+1, right-1 {
					input.Definitions[left], input.Definitions[right] = input.Definitions[right], input.Definitions[left]
				}
			}
			snapshot, metrics, err := preflightValidate(input)
			var failure *preflightCandidateError
			if !errors.As(err, &failure) || failure.Code != "relation_cycle_unsupported" ||
				failure.Reason != "relation_cycle_unsupported" {
				t.Fatalf("permutation %d cycle failure = %#v", permutation, err)
			}
			if canonical == nil {
				copy := *failure
				canonical = &copy
			} else if *failure != *canonical {
				t.Fatalf("permutation %d cycle failure = %+v, want canonical %+v", permutation, failure, canonical)
			}
			if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
				t.Fatalf("permutation %d cycle published/performed catalog/creator/state I/O: snapshot=%#v metrics=%+v", permutation, snapshot, metrics)
			}
		}
		if canonical.Source != articleIdentity || canonical.Field != "author" || canonical.Target != authorIdentity {
			t.Fatalf("canonical cycle edge = %+v, want unchanged article.author edge", canonical)
		}
	})
}

func TestPreflightGraphAndModelFailureSelectionIsPermutationInvariant(t *testing.T) {
	t.Parallel()

	t.Run("missing dependency is canonical under compound permutations", func(t *testing.T) {
		base := preflightFixture()
		base.Definitions = []preflightDefinition{
			{
				Key:          preflightMigrationKey{App: "zeta", Name: "0001"},
				Dependencies: []preflightMigrationKey{{App: "unresolved-z", Name: "0001"}},
			},
			{
				Key: preflightMigrationKey{App: "alpha", Name: "0001"},
				Dependencies: []preflightMigrationKey{
					{App: "unresolved-z", Name: "0002"},
					{App: "unresolved-a", Name: "0001"},
				},
			},
		}
		wantOwner := preflightMigrationKey{App: "alpha", Name: "0001"}
		wantDependency := preflightMigrationKey{App: "unresolved-a", Name: "0001"}
		for permutation := 0; permutation < 4; permutation++ {
			input := preflightCloneInput(base)
			if permutation&1 != 0 {
				input.Definitions[0], input.Definitions[1] = input.Definitions[1], input.Definitions[0]
			}
			if permutation&2 != 0 {
				for index := range input.Definitions {
					dependencies := input.Definitions[index].Dependencies
					for left, right := 0, len(dependencies)-1; left < right; left, right = left+1, right-1 {
						dependencies[left], dependencies[right] = dependencies[right], dependencies[left]
					}
				}
			}
			snapshot, metrics, err := preflightValidate(input)
			var failure *preflightCandidateError
			if !errors.As(err, &failure) || failure.Code != "dependency_not_found" || failure.Owner != wantOwner || failure.Dependency != wantDependency {
				t.Fatalf("permutation %d dependency failure = %#v", permutation, err)
			}
			if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
				t.Fatalf("permutation %d published/performed catalog/creator/state I/O: snapshot=%#v metrics=%+v", permutation, snapshot, metrics)
			}
		}
	})

	t.Run("duplicate dependency is canonical and zero catalog IO", func(t *testing.T) {
		base := preflightFixture()
		duplicate := base.Definitions[2].Dependencies[0]
		base.Definitions[2].Dependencies = append(base.Definitions[2].Dependencies, duplicate)
		for permutation := 0; permutation < 2; permutation++ {
			input := preflightCloneInput(base)
			if permutation != 0 {
				for left, right := 0, len(input.Definitions)-1; left < right; left, right = left+1, right-1 {
					input.Definitions[left], input.Definitions[right] = input.Definitions[right], input.Definitions[left]
				}
			}
			snapshot, metrics, err := preflightValidate(input)
			var failure *preflightCandidateError
			if !errors.As(err, &failure) || failure.Code != "duplicate_dependency" ||
				failure.Owner != base.Definitions[2].Key || failure.Dependency != duplicate {
				t.Fatalf("permutation %d duplicate dependency failure = %#v", permutation, err)
			}
			if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
				t.Fatalf("permutation %d duplicate dependency published/performed catalog/creator/state I/O: snapshot=%#v metrics=%+v", permutation, snapshot, metrics)
			}
		}
	})

	t.Run("duplicate creators are derived canonically from operation records", func(t *testing.T) {
		base := preflightFixture()
		base.Definitions = append(base.Definitions, preflightDefinition{
			Key: preflightMigrationKey{App: "authors", Name: "0002_duplicate"},
			Operations: []preflightOperation{{
				Kind:       preflightCreateModel,
				Model:      stateModelIdentity{App: "authors", Model: "author"},
				ModelState: base.Definitions[0].Operations[0].ModelState.Clone(),
			}},
		})
		for permutation := 0; permutation < 2; permutation++ {
			input := preflightCloneInput(base)
			if permutation != 0 {
				for left, right := 0, len(input.Definitions)-1; left < right; left, right = left+1, right-1 {
					input.Definitions[left], input.Definitions[right] = input.Definitions[right], input.Definitions[left]
				}
			}
			_, metrics, err := preflightValidate(input)
			var failure *preflightCandidateError
			if !errors.As(err, &failure) || failure.Code != "duplicate_model_creator" ||
				failure.Source != (stateModelIdentity{App: "authors", Model: "author"}) ||
				failure.Owner != (preflightMigrationKey{App: "authors", Name: "0002_duplicate"}) {
				t.Fatalf("permutation %d creator failure = %#v", permutation, err)
			}
			if metrics != (preflightIOMetrics{}) {
				t.Fatalf("permutation %d creator failure I/O = %+v", permutation, metrics)
			}
		}
	})
}

func TestPreflightRelationOwnerAndSourceCreatorVisibilityAreExplicitAndZeroIO(t *testing.T) {
	t.Parallel()

	t.Run("unrelated operation owner rejects source creator", func(t *testing.T) {
		input := preflightFixture()
		relationOperation := input.Definitions[2].Operations[0]
		input.Definitions[2].Operations = nil
		input.Definitions = append(input.Definitions, preflightDefinition{
			Key:        preflightMigrationKey{App: "blog", Name: "0003_unrelated"},
			Operations: []preflightOperation{relationOperation},
		})
		snapshot, metrics, err := preflightValidate(input)
		if !preflightErrorCode(err, "source_creator_not_ancestor") {
			t.Fatalf("unrelated source creator failure = %#v", err)
		}
		if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
			t.Fatalf("unrelated owner published/performed catalog/creator/state I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
		}
	})

	t.Run("same migration chronology comes only from ordered operations", func(t *testing.T) {
		input := preflightFixture()
		authorDefinition := input.Definitions[0]
		article := input.Definitions[1].Operations[0]
		relation := input.Definitions[2].Operations[0]
		combinedKey := preflightMigrationKey{App: "blog", Name: "0001_combined"}
		input.Definitions = []preflightDefinition{
			authorDefinition,
			{
				Key:          combinedKey,
				Dependencies: []preflightMigrationKey{authorDefinition.Key},
				Operations:   []preflightOperation{article, relation},
			},
		}
		planStart, planErr := stateNewProject(stateFormatRelation, input.State.apps["authors"])
		if planErr != nil {
			t.Fatalf("combined migration plan start: %v", planErr)
		}
		input.PlanStart = planStart
		input.PlanTarget = input.State.stateClone()
		input.PlanApplied = []migrations.MigrationKey{{App: authorDefinition.Key.App, Name: authorDefinition.Key.Name}}
		input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(combinedKey)}
		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 2 || len(snapshot.relations) != 1 {
			t.Fatalf("ordered creators = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}
		for identity, want := range map[stateModelIdentity]struct {
			owner preflightMigrationKey
			index int
		}{
			{App: "authors", Model: "author"}: {owner: authorDefinition.Key, index: 0},
			{App: "blog", Model: "article"}:   {owner: combinedKey, index: 0},
		} {
			creator, exists := snapshot.preflightCreator(identity)
			if !exists || creator.CreatorOperation != want.index || creator.Creator != want.owner {
				t.Fatalf("creator %v = %#v, want owner %v operation %d", identity, creator, want.owner, want.index)
			}
		}

		input.Definitions[1].Operations = []preflightOperation{relation, article}
		snapshot, metrics, err = preflightValidate(input)
		if !preflightErrorCode(err, "source_creator_not_ancestor") {
			t.Fatalf("later source creator accepted: %#v", err)
		}
		if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
			t.Fatalf("later creator published/performed catalog/creator/state I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
		}

		input = preflightFixture()
		preflightMoveTargetCreatorAfterRelation(&input)
		if _, _, err := preflightValidate(input); !preflightErrorCode(err, "target_creator_not_ancestor") {
			t.Fatalf("later target creator accepted: %#v", err)
		}
	})
}

func TestPreflightFailurePrecedenceAndCreatorVisibilityAreDeterministic(t *testing.T) {
	t.Parallel()

	t.Run("state validation is explicitly before graph and capability", func(t *testing.T) {
		input := preflightFixture()
		schema := input.State.apps["blog"]
		schema.Models[0].Fields[1].Column = ""
		input.State.apps["blog"] = schema
		input.Definitions[2].Dependencies = append(input.Definitions[2].Dependencies, preflightMigrationKey{App: "missing", Name: "0001"})
		input.Capability.RelationEditor = false
		snapshot, metrics, err := preflightValidate(input)
		if !stateErrorHas(err, "schema_not_normalized", "normalization_would_change_state") {
			t.Fatalf("unnormalized state precedence = %#v", err)
		}
		if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
			t.Fatalf("invalid state published/performed catalog/creator/state I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
		}
	})

	t.Run("invalid IR state is deterministic before operation inspection", func(t *testing.T) {
		input := preflightFixture()
		schema := input.State.apps["blog"]
		schema.Models[0].Fields[2].Relation.OnDelete = ir.DeleteSetNull
		input.State.apps["blog"] = schema
		input.Definitions[0].Operations[0].Kind = preflightOperationKind("forged")
		_, metrics, err := preflightValidate(input)
		if !stateErrorHas(err, "schema_invalid", "invalid_nullability") || metrics != (preflightIOMetrics{}) {
			t.Fatalf("invalid IR precedence = metrics:%+v err:%#v", metrics, err)
		}
	})

	t.Run("scalar state cannot enter relation preflight", func(t *testing.T) {
		scalar, err := stateNewProject(stateFormatScalar, stateScalarFixture())
		if err != nil {
			t.Fatalf("scalar state fixture: %v", err)
		}
		input := preflightFixture()
		input.State = scalar
		_, metrics, err := preflightValidate(input)
		if !preflightErrorCode(err, "relation_state_required") || metrics != (preflightIOMetrics{}) {
			t.Fatalf("scalar relation preflight = metrics:%+v err:%#v", metrics, err)
		}
	})

	t.Run("unsupported operation is fail closed", func(t *testing.T) {
		input := preflightFixture()
		input.Definitions[0].Operations[0].Kind = preflightOperationKind("forged")
		_, metrics, err := preflightValidate(input)
		if !preflightErrorCode(err, "unsupported_operation") || metrics != (preflightIOMetrics{}) {
			t.Fatalf("unsupported operation = metrics:%+v err:%#v", metrics, err)
		}
	})

	t.Run("operation union is closed", func(t *testing.T) {
		input := preflightFixture()
		input.Definitions[0].Operations[0].Relation = input.Definitions[2].Operations[0].Relation
		_, metrics, err := preflightValidate(input)
		if !preflightErrorCode(err, "conflicting_operation_arms") || metrics != (preflightIOMetrics{}) {
			t.Fatalf("create-model sibling arm = metrics:%+v err:%#v", metrics, err)
		}

		input = preflightFixture()
		input.Definitions[2].Operations[0].Model = stateModelIdentity{App: "blog", Model: "article"}
		_, metrics, err = preflightValidate(input)
		if !preflightErrorCode(err, "conflicting_operation_arms") || metrics != (preflightIOMetrics{}) {
			t.Fatalf("add-relation sibling arm = metrics:%+v err:%#v", metrics, err)
		}
	})

	t.Run("every historical relation has an operation-derived owner", func(t *testing.T) {
		input := preflightFixture()
		input.Definitions[2].Operations = nil
		snapshot, metrics, err := preflightValidate(input)
		if !preflightErrorCode(err, "relation_owner_operation_not_found") {
			t.Fatalf("missing relation operation = %#v", err)
		}
		if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
			t.Fatalf("missing relation owner published/performed catalog/creator/state I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
		}
	})

	compound := preflightFixture()
	delete(compound.State.apps, "blog")
	compound.Definitions[1].Operations = nil
	compound.Capability.RelationEditor = false
	compound.Definitions[2].Operations[0].Relation.DeclaredTable = "wrong"
	_, metrics, err := preflightValidate(compound)
	if !preflightErrorCode(err, "source_model_not_found") || metrics != (preflightIOMetrics{}) {
		t.Fatalf("compound failure precedence = metrics:%+v err:%#v", metrics, err)
	}
}

func TestPreflightChronologicalReplayUsesExactOperationSnapshotsWithoutIO(t *testing.T) {
	t.Parallel()

	assertFailure := func(t *testing.T, input preflightInput, code string) {
		t.Helper()
		snapshot, metrics, err := preflightValidate(input)
		if !preflightErrorCode(err, code) {
			t.Fatalf("chronological replay failure = %#v, want %s", err, code)
		}
		if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
			t.Fatalf("chronological replay failure published/performed catalog/creator/state I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
		}
	}

	t.Run("relation-bearing create is its actual owner", func(t *testing.T) {
		input := preflightFixture()
		authorsRoot := input.Definitions[0].Key
		articleFinal := input.Definitions[2].Operations[0].After.Clone()
		input.Definitions[1].Dependencies = []preflightMigrationKey{authorsRoot}
		input.Definitions[1].Operations[0].ModelState = articleFinal
		input.Definitions[2].Operations = nil
		planStart, planErr := stateNewProject(stateFormatRelation, input.State.apps["authors"])
		if planErr != nil {
			t.Fatalf("relation-bearing create plan start: %v", planErr)
		}
		input.PlanStart = planStart
		input.PlanTarget = input.State.stateClone()
		input.PlanApplied = []migrations.MigrationKey{{App: authorsRoot.App, Name: authorsRoot.Name}}
		input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(input.Definitions[1].Key)}

		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 2 || len(snapshot.relations) != 1 {
			t.Fatalf("relation-bearing CreateModel replay = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}
		relation := snapshot.relations[0]
		if relation.Owner != input.Definitions[1].Key || relation.OwnerOperation != 0 || relation.Declaration.Field != "author" {
			t.Fatalf("relation-bearing CreateModel owner = %#v", relation)
		}
	})

	t.Run("operation source app matches its migration while relation target may cross apps", func(t *testing.T) {
		valid := preflightFixture()
		if _, metrics, err := preflightValidate(valid); err != nil || metrics != (preflightIOMetrics{}) {
			t.Fatalf("valid cross-app target rejected: metrics=%+v error=%v", metrics, err)
		}

		createMismatch := preflightFixture()
		createMismatch.Definitions[1].Operations[0].Model.App = "authors"
		assertFailure(t, createMismatch, "operation_app_mismatch")

		relationMismatch := preflightFixture()
		relationMismatch.Definitions[2].Operations[0].Relation.Source.App = "authors"
		assertFailure(t, relationMismatch, "operation_app_mismatch")
	})

	t.Run("relation already present at create rejects forged later add", func(t *testing.T) {
		input := preflightFixture()
		input.Definitions[1].Dependencies = []preflightMigrationKey{input.Definitions[0].Key}
		input.Definitions[1].Operations[0].ModelState = input.Definitions[2].Operations[0].After.Clone()
		assertFailure(t, input, "operation_before_state_mismatch")
	})

	t.Run("add before state must equal replayed predecessor", func(t *testing.T) {
		input := preflightFixture()
		input.Definitions[2].Operations[0].Before.Fields[1].MaxLength = 199
		assertFailure(t, input, "operation_before_state_mismatch")
	})

	t.Run("add after state must be the exact one-field relation delta", func(t *testing.T) {
		input := preflightFixture()
		input.Definitions[2].Operations[0].After.Fields = append(
			input.Definitions[2].Operations[0].After.Fields,
			ir.Field{Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, MaxLength: 200},
		)
		assertFailure(t, input, "relation_operation_delta_mismatch")
	})

	t.Run("relation operation cannot omit either exact state arm", func(t *testing.T) {
		for _, missing := range []string{"before", "after"} {
			t.Run(missing, func(t *testing.T) {
				input := preflightFixture()
				if missing == "before" {
					input.Definitions[2].Operations[0].Before = ir.Model{}
				} else {
					input.Definitions[2].Operations[0].After = ir.Model{}
				}
				assertFailure(t, input, "relation_operation_snapshot_invalid")
			})
		}
	})

	t.Run("final historical state must equal the replayed exact IR", func(t *testing.T) {
		input := preflightFixture()
		input.Definitions[0].Operations[0].ModelState.Fields[1].Default.String = "replayed-only"
		assertFailure(t, input, "historical_state_replay_mismatch")
	})

	t.Run("remove replays exact reverse and rejects unapply predecessor mismatch", func(t *testing.T) {
		input := preflightFixture()
		add := input.Definitions[2].Operations[0]
		remove := preflightOperation{
			Kind: preflightRemoveRelation, Before: add.After.Clone(), After: add.Before.Clone(), Relation: add.Relation,
		}
		input.Definitions = append(input.Definitions, preflightDefinition{
			Key:          preflightMigrationKey{App: "blog", Name: "0003_remove_article_author"},
			Dependencies: []preflightMigrationKey{input.Definitions[2].Key},
			Operations:   []preflightOperation{remove},
		})
		blog := input.State.apps["blog"]
		blog.Models[0] = add.Before.Clone()
		input.State.apps["blog"] = blog
		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.relations) != 0 {
			t.Fatalf("exact remove replay = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}

		input = preflightCloneInput(input)
		input.Definitions[3].Operations[0].Before.Fields[1].MaxLength = 199
		assertFailure(t, input, "operation_before_state_mismatch")

		input = preflightFixture()
		add = input.Definitions[2].Operations[0]
		remove = preflightOperation{
			Kind: preflightRemoveRelation, Before: add.After.Clone(), After: add.After.Clone(), Relation: add.Relation,
		}
		input.Definitions = append(input.Definitions, preflightDefinition{
			Key:          preflightMigrationKey{App: "blog", Name: "0003_remove_article_author"},
			Dependencies: []preflightMigrationKey{input.Definitions[2].Key},
			Operations:   []preflightOperation{remove},
		})
		assertFailure(t, input, "relation_operation_delta_mismatch")

		input = preflightFixture()
		add = input.Definitions[2].Operations[0]
		remove = preflightOperation{
			Kind: preflightRemoveRelation, Before: add.After.Clone(), After: add.Before.Clone(), Relation: add.Relation,
		}
		remove.Relation.DeclaredColumn = "forged_column"
		input.Definitions = append(input.Definitions, preflightDefinition{
			Key:          preflightMigrationKey{App: "blog", Name: "0003_remove_article_author"},
			Dependencies: []preflightMigrationKey{input.Definitions[2].Key},
			Operations:   []preflightOperation{remove},
		})
		assertFailure(t, input, "declared_column_mismatch")
	})

	t.Run("product Planner orders one mixed scalar relation step for candidate-local replay", func(t *testing.T) {
		mixed := preflightFixture()
		relationKey := mixed.Definitions[2].Key
		blogRoot := mixed.Definitions[1].Key
		originalRelation := mixed.Definitions[2].Operations[0]
		scalarAfter := originalRelation.Before.Clone()
		scalarAfter.Fields = append(scalarAfter.Fields, ir.Field{
			Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean,
		})
		relationAfter := scalarAfter.Clone()
		relationAfter.Fields = append(relationAfter.Fields, originalRelation.After.Fields[len(originalRelation.After.Fields)-1].Clone())
		mixed.Definitions[2].Operations = []preflightOperation{
			{Kind: preflightAddScalar, Before: originalRelation.Before.Clone(), After: scalarAfter.Clone()},
			{
				Kind: preflightAddRelation, Before: scalarAfter.Clone(), After: relationAfter.Clone(),
				Relation: originalRelation.Relation,
			},
		}
		blogFinal := mixed.State.apps["blog"].Clone()
		blogFinal.Models[0] = relationAfter.Clone()
		mixed.State.apps["blog"] = blogFinal
		blogStart := blogFinal.Clone()
		blogStart.Models[0] = originalRelation.Before.Clone()
		start, err := stateNewProject(stateFormatRelation, mixed.State.apps["authors"], blogStart)
		if err != nil {
			t.Fatalf("mixed plan start state: %v", err)
		}
		mixed.PlanStart = start
		mixed.PlanTarget = mixed.State.stateClone()
		mixed.PlanApplied = []migrations.MigrationKey{
			{App: mixed.Definitions[0].Key.App, Name: mixed.Definitions[0].Key.Name},
			{App: blogRoot.App, Name: blogRoot.Name},
		}
		mixed.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(relationKey)}
		snapshot, metrics, err := preflightValidate(mixed)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.relations) != 1 {
			t.Fatalf("mixed forward Planner-ordered candidate replay = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}

		backward := preflightCloneInput(mixed)
		backward.PlanStart = mixed.State.stateClone()
		backward.PlanTarget = start.stateClone()
		backward.PlanApplied = append(backward.PlanApplied, migrations.MigrationKey{App: relationKey.App, Name: relationKey.Name})
		backward.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(blogRoot)}
		snapshot, metrics, err = preflightValidate(backward)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.relations) != 1 {
			t.Fatalf("mixed backward Planner-ordered candidate replay = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}
	})

	t.Run("prepared sequence adapts scalar and transient relation membership exactly in both directions", func(t *testing.T) {
		input := preflightFixture()
		stepKey := input.Definitions[2].Key
		if stepKey != (preflightMigrationKey{App: "blog", Name: "0002_article_author"}) {
			t.Fatalf("prepared fixture key = %#v, want exact blog/0002_article_author", stepKey)
		}
		originalRelation := input.Definitions[2].Operations[0]
		scalarAfter := originalRelation.Before.Clone()
		scalarAfter.Fields = append(scalarAfter.Fields, ir.Field{
			Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean,
		})
		relationAfter := scalarAfter.Clone()
		relationField := originalRelation.After.Fields[len(originalRelation.After.Fields)-1].Clone()
		relationAfter.Fields = append(relationAfter.Fields, relationField.Clone())
		declaration := originalRelation.Relation
		input.Definitions[2].Operations = []preflightOperation{
			{Kind: preflightAddScalar, Before: originalRelation.Before.Clone(), After: scalarAfter.Clone()},
			{Kind: preflightAddRelation, Before: scalarAfter.Clone(), After: relationAfter.Clone(), Relation: declaration},
			{Kind: preflightRemoveRelation, Before: relationAfter.Clone(), After: scalarAfter.Clone(), Relation: declaration},
		}
		blog := input.State.apps["blog"]
		blog.Models[0] = scalarAfter.Clone()
		input.State.apps["blog"] = blog
		planStart := input.State.stateClone()
		planStartBlog := planStart.apps["blog"]
		planStartBlog.Models[0] = originalRelation.Before.Clone()
		planStart.apps["blog"] = planStartBlog
		input.PlanStart = planStart.stateClone()
		input.PlanTarget = input.State.stateClone()
		input.PlanApplied = []migrations.MigrationKey{
			{App: input.Definitions[0].Key.App, Name: input.Definitions[0].Key.Name},
			{App: input.Definitions[1].Key.App, Name: input.Definitions[1].Key.Name},
		}
		input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(stepKey)}

		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.relations) != 0 {
			t.Fatalf("transient prepared preflight = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}
		var prepared preflightPreparedStep
		for _, candidate := range snapshot.preflightSteps() {
			if candidate.Key == stepKey {
				prepared = candidate
				break
			}
		}
		if prepared.Key != stepKey || len(prepared.Operations) != 3 ||
			!reflect.DeepEqual(prepared.Dependencies, input.Definitions[2].Dependencies) {
			t.Fatalf("prepared step membership/ancestry = %#v", prepared)
		}
		if _, wireHasTargetField := reflect.TypeOf(preflightRelationDeclaration{}).FieldByName("TargetField"); wireHasTargetField {
			t.Fatal("migration relation declaration unexpectedly carries wire target_field")
		}
		wantTarget := preflightPreparedRelationTarget{
			SourceField:      relationField.Clone(),
			TargetModel:      input.Definitions[0].Operations[0].ModelState.Clone(),
			TargetKey:        input.Definitions[0].Operations[0].ModelState.Fields[0].Clone(),
			Creator:          input.Definitions[0].Key,
			CreatorOperation: 0,
		}
		if len(prepared.Operations[0].Targets) != 0 ||
			!reflect.DeepEqual(prepared.Operations[1].Targets, []preflightPreparedRelationTarget{wantTarget}) ||
			!reflect.DeepEqual(prepared.Operations[2].Targets, []preflightPreparedRelationTarget{wantTarget}) {
			t.Fatalf("prepared historical targets = %#v, want %#v", prepared.Operations, wantTarget)
		}
		if got := prepared.Operations[1].Targets[0]; got.SourceField.Column != "author_id" || got.SourceField.Nullable ||
			got.SourceField.Relation == nil || got.SourceField.Relation.OnDelete != ir.DeleteProtect ||
			got.TargetModel.DBTable != "authors_author" || got.TargetKey.Column != "id" ||
			got.TargetKey.Kind != ir.FieldAuto || !got.TargetKey.PrimaryKey || got.TargetKey.Nullable {
			t.Fatalf("prepared table/column/nullability/delete/key metadata = %#v", got)
		}

		// Prepared steps are diagnostics only. Mutating every provenance and
		// membership arm before the first handoff request cannot be re-sealed as
		// authority because adaptation already completed inside preflightValidate.
		callerVisible := preflightClonePreparedStep(prepared)
		callerVisible.Key = preflightMigrationKey{App: "forged", Name: "singleton"}
		callerVisible.Dependencies = nil
		callerVisible.Operations = callerVisible.Operations[:1]
		callerVisible.Operations[0].After.Fields[0].Name = "forged_operation"
		callerVisible.plan.definitions = []migrations.Migration{{App: "forged", Name: "singleton"}}
		callerVisible.plan.applied = nil
		callerVisible.plan.targets = []preflightPlanTarget{preflightNamedPlanTarget(callerVisible.Key)}
		callerVisible.plan.expected = migrations.PlanStep{
			Key: migrations.MigrationKey{App: "forged", Name: "singleton"}, Direction: migrations.DirectionForward,
		}
		apply, hasRelation := snapshot.preflightHandoff(stepKey)
		if !hasRelation || len(apply.plan.definitions) != len(input.Definitions) {
			t.Fatalf("apply snapshot authoritative graph = relation:%t handoff:%#v", hasRelation, apply)
		}
		if forged, exists := snapshot.preflightHandoff(callerVisible.Key); exists ||
			!reflect.DeepEqual(forged, lifecyclePreparedRelationStep{}) {
			t.Fatalf("caller-visible forged key acquired handoff: exists=%t handoff=%#v", exists, forged)
		}
		wantApply := RelationMigrationIntent{Operations: []RelationMigrationOperation{
			{OperationIndex: 0, Kind: RelationMigrationAddField, Before: originalRelation.Before.Clone(), After: scalarAfter.Clone()},
			{
				OperationIndex: 1, Kind: RelationMigrationAddField, Before: scalarAfter.Clone(), After: relationAfter.Clone(),
				Targets: []RelationMigrationTarget{{SourceField: relationField.Clone(), TargetModel: wantTarget.TargetModel.Clone(), TargetKey: wantTarget.TargetKey.Clone()}},
			},
			{
				OperationIndex: 2, Kind: RelationMigrationRemoveField, Before: relationAfter.Clone(), After: scalarAfter.Clone(),
				Targets: []RelationMigrationTarget{{SourceField: relationField.Clone(), TargetModel: wantTarget.TargetModel.Clone(), TargetKey: wantTarget.TargetKey.Clone()}},
			},
		}}
		wantApplyTransition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: stepKey.App, Name: stepKey.Name},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		wantApplyPlan := lifecycleClonePreparedPlan(lifecyclePreparedPlan{
			definitions: preflightProductDefinitionGraph(input.Definitions),
			applied:     input.PlanApplied,
			targets:     input.PlanTargets,
			expected: migrations.PlanStep{
				Key: migrations.MigrationKey{App: stepKey.App, Name: stepKey.Name}, Direction: migrations.DirectionForward,
			},
		})
		if apply.transition != wantApplyTransition || !reflect.DeepEqual(apply.intent, wantApply) {
			t.Fatalf("apply adapter = %#v, want transition:%#v intent:%#v", apply, wantApplyTransition, wantApply)
		}
		if apply.binding == nil || apply.binding.key != (migrations.MigrationKey{App: stepKey.App, Name: stepKey.Name}) ||
			apply.binding.direction != migrations.DirectionForward || apply.binding.transition != wantApplyTransition ||
			!reflect.DeepEqual(apply.binding.intent, wantApply) ||
			!reflect.DeepEqual(apply.plan, wantApplyPlan) ||
			!reflect.DeepEqual(apply.plan, apply.binding.plan) {
			t.Fatalf("apply adapter binding = %#v plan:%#v, want exact key/direction/transition/intent/full graph/applied/targets/step", apply.binding, apply.plan)
		}
		if err := lifecycleValidatePreparedRelationBinding(apply); err != nil {
			t.Fatalf("exact apply adapter rejected by lifecycle: %v", err)
		}
		preparedApplyDecision, err := lifecyclePrepareSealedStepPure(apply)
		if err != nil {
			t.Fatalf("exact apply handoff rejected by lifecycle decision preparation: prepared:%#v error:%v", preparedApplyDecision, err)
		}
		if err := lifecycleValidatePreparedRelationBinding(preparedApplyDecision); err != nil {
			t.Fatalf("prepared apply decision lost sealed handoff: %v", err)
		}

		applyDefinitionIndex := -1
		for index := range apply.plan.definitions {
			if apply.plan.definitions[index].Key() == (migrations.MigrationKey{App: stepKey.App, Name: stepKey.Name}) {
				applyDefinitionIndex = index
				break
			}
		}
		if applyDefinitionIndex < 0 {
			t.Fatalf("authoritative handoff lacks current graph definition: %#v", apply.plan.definitions)
		}
		apply.plan.definitions[applyDefinitionIndex].Dependencies = nil
		apply.binding.plan.targets = nil
		freshAuthoritative, exists := snapshot.preflightHandoff(stepKey)
		if !exists || !reflect.DeepEqual(freshAuthoritative, preparedApplyDecision) {
			t.Fatalf("authoritative handoff accessor retained mutation: exists=%t handoff=%#v", exists, freshAuthoritative)
		}
		apply = freshAuthoritative

		backward := preflightCloneInput(input)
		backward.PlanStart = input.State.stateClone()
		backward.PlanTarget = planStart.stateClone()
		backward.PlanApplied = append(
			append([]migrations.MigrationKey(nil), input.PlanApplied...),
			migrations.MigrationKey{App: stepKey.App, Name: stepKey.Name},
		)
		backward.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(input.Definitions[1].Key)}
		backwardSnapshot, backwardMetrics, backwardErr := preflightValidate(backward)
		if backwardErr != nil || backwardMetrics != (preflightIOMetrics{}) {
			t.Fatalf("transient prepared unapply preflight = snapshot:%#v metrics:%+v error:%v", backwardSnapshot, backwardMetrics, backwardErr)
		}
		unapply, hasRelation := backwardSnapshot.preflightHandoff(stepKey)
		if !hasRelation {
			t.Fatal("unapply snapshot did not publish its authoritative relation handoff")
		}
		wantUnapply := RelationMigrationIntent{Operations: []RelationMigrationOperation{
			{
				OperationIndex: 2, Kind: RelationMigrationAddField, Before: scalarAfter.Clone(), After: relationAfter.Clone(),
				Targets: []RelationMigrationTarget{{SourceField: relationField.Clone(), TargetModel: wantTarget.TargetModel.Clone(), TargetKey: wantTarget.TargetKey.Clone()}},
			},
			{
				OperationIndex: 1, Kind: RelationMigrationRemoveField, Before: relationAfter.Clone(), After: scalarAfter.Clone(),
				Targets: []RelationMigrationTarget{{SourceField: relationField.Clone(), TargetModel: wantTarget.TargetModel.Clone(), TargetKey: wantTarget.TargetKey.Clone()}},
			},
			{OperationIndex: 0, Kind: RelationMigrationRemoveField, Before: scalarAfter.Clone(), After: originalRelation.Before.Clone()},
		}}
		wantUnapplyTransition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: stepKey.App, Name: stepKey.Name},
			Kind:      migrationbackend.HistoryTransitionUnapply,
		}
		wantUnapplyPlan := lifecycleClonePreparedPlan(lifecyclePreparedPlan{
			definitions: preflightProductDefinitionGraph(backward.Definitions),
			applied:     backward.PlanApplied,
			targets:     backward.PlanTargets,
			expected: migrations.PlanStep{
				Key: migrations.MigrationKey{App: stepKey.App, Name: stepKey.Name}, Direction: migrations.DirectionBackward,
			},
		})
		if unapply.transition != wantUnapplyTransition || !reflect.DeepEqual(unapply.intent, wantUnapply) {
			t.Fatalf("unapply adapter = %#v, want transition:%#v intent:%#v", unapply, wantUnapplyTransition, wantUnapply)
		}
		if unapply.binding == nil || unapply.binding.key != (migrations.MigrationKey{App: stepKey.App, Name: stepKey.Name}) ||
			unapply.binding.direction != migrations.DirectionBackward || unapply.binding.transition != wantUnapplyTransition ||
			!reflect.DeepEqual(unapply.binding.intent, wantUnapply) ||
			!reflect.DeepEqual(unapply.plan, wantUnapplyPlan) ||
			!reflect.DeepEqual(unapply.plan, unapply.binding.plan) {
			t.Fatalf("unapply adapter binding = %#v plan:%#v, want exact key/direction/transition/intent/full graph/applied/targets/step", unapply.binding, unapply.plan)
		}
		if err := lifecycleValidatePreparedRelationBinding(unapply); err != nil {
			t.Fatalf("exact unapply adapter rejected by lifecycle: %v", err)
		}
		preparedUnapplyDecision, err := lifecyclePrepareSealedStepPure(unapply)
		if err != nil {
			t.Fatalf("exact unapply handoff rejected by lifecycle decision preparation: prepared:%#v error:%v", preparedUnapplyDecision, err)
		}
		if err := lifecycleValidatePreparedRelationBinding(preparedUnapplyDecision); err != nil {
			t.Fatalf("prepared unapply decision lost sealed handoff: %v", err)
		}

		forgedTransition := lifecycleClonePreparedRelationStep(apply)
		forgedTransition.transition.Migration.Name = "0002_relation"
		if err := lifecycleValidatePreparedRelationBinding(forgedTransition); !errors.Is(err, lifecycleRelationErrIntent) {
			t.Fatalf("re-paired prepared transition error = %v, want relation intent", err)
		}
		forgedIntent := lifecycleClonePreparedRelationStep(apply)
		forgedIntent.intent.Operations[1].After.Fields[0].Name = "forged_successor"
		if err := lifecycleValidatePreparedRelationBinding(forgedIntent); !errors.Is(err, lifecycleRelationErrIntent) {
			t.Fatalf("re-paired prepared intent error = %v, want relation intent", err)
		}

		// Every accessor and adapter returns fresh nested IR. Mutating source,
		// prepared, and adapted values cannot alter the published snapshot.
		input.Definitions[0].Operations[0].ModelState.Fields[0].Name = "source_alias"
		prepared.Operations[1].Targets[0].TargetKey.Name = "prepared_alias"
		apply.intent.Operations[1].Targets[0].SourceField.Relation.Target.AppLabel = "adapter_alias"
		if apply.binding.intent.Operations[1].Targets[0].TargetKey.Name != "id" ||
			apply.binding.intent.Operations[1].Targets[0].SourceField.Relation.Target.AppLabel != "authors" {
			t.Fatalf("prepared handoff binding retained intent alias: %#v", apply.binding)
		}
		var fresh preflightPreparedStep
		for _, candidate := range snapshot.preflightSteps() {
			if candidate.Key == stepKey {
				fresh = candidate
			}
		}
		if fresh.Operations[1].Targets[0].TargetKey.Name != "id" ||
			fresh.Operations[1].Targets[0].SourceField.Relation.Target.AppLabel != "authors" {
			t.Fatalf("prepared sequence retained alias: %#v", fresh)
		}
	})

	t.Run("relation-bearing create targets retain normalized model field order", func(t *testing.T) {
		input := preflightFixture()
		article := input.Definitions[2].Operations[0].After.Clone()
		editor := article.Fields[len(article.Fields)-1].Clone()
		editor.Name = "editor"
		editor.GoName = "EditorID"
		editor.Column = "editor_id"
		editor.Relation.Reverse.Name = "edited_articles"
		article.Fields = append(article.Fields, editor)
		input.Definitions[1].Dependencies = []preflightMigrationKey{input.Definitions[0].Key}
		input.Definitions[1].Operations[0].ModelState = article.Clone()
		input.Definitions[2].Operations = nil
		blog := input.State.apps["blog"]
		blog.Models[0] = article.Clone()
		input.State.apps["blog"] = blog
		planStart, planErr := stateNewProject(stateFormatRelation, input.State.apps["authors"])
		if planErr != nil {
			t.Fatalf("relation create field-order plan start: %v", planErr)
		}
		input.PlanStart = planStart
		input.PlanTarget = input.State.stateClone()
		input.PlanApplied = []migrations.MigrationKey{{
			App: input.Definitions[0].Key.App, Name: input.Definitions[0].Key.Name,
		}}
		input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(input.Definitions[1].Key)}
		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) {
			t.Fatalf("relation create field-order preflight = metrics:%+v error:%v", metrics, err)
		}
		steps := snapshot.preflightSteps()
		var create preflightPreparedOperation
		for _, step := range steps {
			if step.Key == input.Definitions[1].Key {
				create = step.Operations[0]
			}
		}
		if len(create.Targets) != 2 || create.Targets[0].SourceField.Name != "author" ||
			create.Targets[1].SourceField.Name != "editor" ||
			!reflect.DeepEqual(create.After.Fields, article.Fields) {
			t.Fatalf("relation create target/model field order = %#v", create)
		}
	})

	t.Run("already satisfied product target is rejected without an exact current step", func(t *testing.T) {
		input := preflightFixture()
		input.PlanStart = input.State.stateClone()
		input.PlanTarget = input.State.stateClone()
		input.PlanApplied = make([]migrations.MigrationKey, len(input.Definitions))
		for index, definitionValue := range input.Definitions {
			input.PlanApplied[index] = migrations.MigrationKey{App: definitionValue.Key.App, Name: definitionValue.Key.Name}
		}
		last := input.Definitions[len(input.Definitions)-1].Key
		input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(last)}
		snapshot, metrics, err := preflightValidate(input)
		if !preflightErrorCode(err, "plan_step_invalid") || metrics != (preflightIOMetrics{}) ||
			!reflect.DeepEqual(snapshot, preflightSnapshot{}) {
			t.Fatalf("already-satisfied Planner request = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}
	})

	t.Run("transient self and reverse collisions cannot be hidden by a later remove", func(t *testing.T) {
		for _, test := range []struct {
			name     string
			field    ir.Field
			target   stateModelIdentity
			reverse  string
			wantCode string
		}{
			{
				name: "self", target: stateModelIdentity{App: "blog", Model: "article"}, reverse: "children",
				field:    ir.Field{Name: "parent", GoName: "ParentID", Column: "parent_id", Kind: ir.FieldForeignKey},
				wantCode: "self_relation_unsupported",
			},
			{
				name: "reverse", target: stateModelIdentity{App: "authors", Model: "author"}, reverse: "articles",
				field:    ir.Field{Name: "editor", GoName: "EditorID", Column: "editor_id", Kind: ir.FieldForeignKey},
				wantCode: "reverse_namespace_collision",
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				input := preflightFixture()
				before := input.State.apps["blog"].Models[0].Clone()
				field := test.field.Clone()
				field.Relation = &ir.ForeignKeyRelation{
					Target: test.target.stateIRIdentity(), Cardinality: ir.RelationManyToOne,
					Reverse: ir.ReverseRelation{Name: test.reverse}, OnDelete: ir.DeleteProtect,
				}
				after := before.Clone()
				after.Fields = append(after.Fields, field.Clone())
				declaration := preflightRelationDeclaration{
					Source: stateModelIdentity{App: "blog", Model: "article"}, Field: field.Name,
					Target:        test.target,
					DeclaredTable: before.DBTable, DeclaredColumn: field.Column,
					Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Name: test.reverse}, OnDelete: ir.DeleteProtect,
				}
				input.Definitions = append(input.Definitions, preflightDefinition{
					Key:          preflightMigrationKey{App: "blog", Name: "0003_transient_" + test.name},
					Dependencies: []preflightMigrationKey{input.Definitions[2].Key},
					Operations: []preflightOperation{
						{Kind: preflightAddRelation, Before: before.Clone(), After: after.Clone(), Relation: declaration},
						{Kind: preflightRemoveRelation, Before: after.Clone(), After: before.Clone(), Relation: declaration},
					},
				})
				assertFailure(t, input, test.wantCode)
			})
		}

		t.Run("scalar target field collision", func(t *testing.T) {
			input := preflightFixture()
			relationKey := input.Definitions[2].Key
			addRelation := input.Definitions[2].Operations[0]
			authorBefore := input.Definitions[0].Operations[0].ModelState.Clone()
			authorAfter := authorBefore.Clone()
			authorAfter.Fields = append(authorAfter.Fields, ir.Field{
				Name: "articles", GoName: "Articles", Column: "articles", Kind: ir.FieldChar, MaxLength: 32,
			})
			authorScalarKey := preflightMigrationKey{App: "authors", Name: "0002_articles"}
			input.Definitions = append(input.Definitions,
				preflightDefinition{
					Key:          authorScalarKey,
					Dependencies: []preflightMigrationKey{relationKey},
					Operations: []preflightOperation{{
						Kind: preflightAddScalar, Before: authorBefore.Clone(), After: authorAfter.Clone(),
					}},
				},
				preflightDefinition{
					Key:          preflightMigrationKey{App: "blog", Name: "0003_remove_article_author"},
					Dependencies: []preflightMigrationKey{authorScalarKey},
					Operations: []preflightOperation{{
						Kind: preflightRemoveRelation, Before: addRelation.After.Clone(), After: addRelation.Before.Clone(),
						Relation: addRelation.Relation,
					}},
				},
			)
			authors := input.State.apps["authors"]
			authors.Models[0] = authorAfter.Clone()
			input.State.apps["authors"] = authors
			blog := input.State.apps["blog"]
			blog.Models[0] = addRelation.Before.Clone()
			input.State.apps["blog"] = blog

			// Make the product Planner ordering request deliberately empty. The
			// candidate-local chronological replay must detect the transient collision
			// at AddScalar before the later RemoveRelation can erase the offending edge.
			input.PlanStart = input.State.stateClone()
			input.PlanTarget = input.State.stateClone()
			input.PlanApplied = make([]migrations.MigrationKey, len(input.Definitions))
			for index, definitionValue := range input.Definitions {
				input.PlanApplied[index] = migrations.MigrationKey{
					App: definitionValue.Key.App, Name: definitionValue.Key.Name,
				}
			}
			last := input.Definitions[len(input.Definitions)-1].Key
			input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(last)}
			assertFailure(t, input, "reverse_namespace_collision")
		})
	})

	t.Run("profile-tied operation cap rejects before operation inspection", func(t *testing.T) {
		input := preflightFixture()
		input.Definitions[0].Operations = make([]preflightOperation, definition.MaxOperationsPerMigration+1)
		input.Definitions[0].Operations[0].Relation.DeclaredTable = strings.Repeat("late", definition.MaxSourceIDBytes)
		_, _, err := preflightValidate(input)
		var failure *preflightCandidateError
		if !errors.As(err, &failure) || failure.Code != "resource_limit_exceeded" ||
			failure.Reason != "operation_count_exceeds_profile_limit" {
			t.Fatalf("structural operation cap precedence = %#v", err)
		}
	})

	t.Run("profile-tied field and state caps reject before deep clone", func(t *testing.T) {
		input := preflightFixture()
		input.Definitions[0].Operations[0].ModelState.Fields = make(
			[]ir.Field,
			definition.MaxFieldsPerCreateModel+1,
		)
		assertFailure(t, input, "resource_limit_exceeded")

		input = preflightFixture()
		input.State.apps = make(map[string]ir.Schema, definition.MaxSources+1)
		for index := 0; index <= definition.MaxSources; index++ {
			app := fmt.Sprintf("app_%04d", index)
			input.State.apps[app] = ir.Schema{AppLabel: app}
		}
		assertFailure(t, input, "resource_limit_exceeded")

		input = preflightFixture()
		schema := input.State.apps["blog"]
		oversized := strings.Repeat("x", definition.MaxDocumentBytes+1)
		schema.Models[0].Fields[1].Default = &ir.ScalarDefault{
			Kind:   ir.ScalarString,
			String: oversized,
		}
		input.State.apps["blog"] = schema
		assertFailure(t, input, "resource_limit_exceeded")

		input = preflightFixture()
		input.PlanStart = input.State.stateClone()
		input.PlanTarget = input.State.stateClone()
		planSchema := input.PlanStart.apps["blog"]
		planSchema.Models[0].Fields[1].Default = &ir.ScalarDefault{
			Kind:   ir.ScalarString,
			String: oversized,
		}
		input.PlanStart.apps["blog"] = planSchema
		last := input.Definitions[len(input.Definitions)-1].Key
		input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(last)}
		assertFailure(t, input, "resource_limit_exceeded")
	})

	t.Run("aggregate definition node budget rejects shared large arms before clone", func(t *testing.T) {
		input := preflightFixture()
		shared := make([]ir.Field, 128)
		operations := make([]preflightOperation, definition.MaxOperationsPerMigration)
		for index := range operations {
			operations[index].Kind = preflightCreateModel
			operations[index].ModelState.Fields = shared
		}
		input.Definitions[0].Operations = operations
		assertFailure(t, input, "resource_limit_exceeded")
	})

	t.Run("aggregate node exhaustion canonically outranks an earlier field count failure", func(t *testing.T) {
		input := preflightFixture()
		stateSchema := input.State.apps["blog"]
		stateSchema.Models[0].Fields = make([]ir.Field, definition.MaxFieldsPerCreateModel+1)
		input.State.apps["blog"] = stateSchema
		input.PlanStart = stateProjectState{
			formatVersion: stateFormatRelation,
			apps: map[string]ir.Schema{
				"nodes": {
					AppLabel: "nodes",
					Models:   make([]ir.Model, definition.MaxJSONValues+1),
				},
			},
		}
		input.PlanTarget = input.State.stateClone()
		last := input.Definitions[len(input.Definitions)-1].Key
		input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(last)}

		_, metrics, err := preflightValidate(input)
		var failure *preflightCandidateError
		if !errors.As(err, &failure) || failure.Code != "resource_limit_exceeded" ||
			failure.Reason != "aggregate_structural_nodes_exceed_profile_limit" ||
			metrics != (preflightIOMetrics{}) {
			t.Fatalf("compound structural precedence = metrics:%+v failure:%#v", metrics, err)
		}
	})

	t.Run("every key dependency relation string and transient model arm is scanned before clone", func(t *testing.T) {
		oversizedID := strings.Repeat("i", definition.MaxSourceIDBytes+1)
		oversizedDefault := strings.Repeat("d", definition.MaxDocumentBytes+1)
		tests := []struct {
			name   string
			mutate func(*preflightInput)
		}{
			{name: "migration key", mutate: func(input *preflightInput) { input.Definitions[2].Key.Name = oversizedID }},
			{name: "dependency key", mutate: func(input *preflightInput) { input.Definitions[2].Dependencies[0].Name = oversizedID }},
			{name: "plan target key", mutate: func(input *preflightInput) {
				input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(preflightMigrationKey{App: "blog", Name: oversizedID})}
			}},
			{name: "invalid plan target arm", mutate: func(input *preflightInput) {
				input.PlanTargets = []preflightPlanTarget{{
					Kind: preflightPlanTargetKind(255),
					Key:  preflightMigrationKey{App: "blog", Name: oversizedID},
					App:  oversizedID,
				}}
			}},
			{name: "relation declaration", mutate: func(input *preflightInput) { input.Definitions[2].Operations[0].Relation.DeclaredTable = oversizedID }},
			{name: "model state default", mutate: func(input *preflightInput) {
				input.Definitions[0].Operations[0].ModelState.Fields[1].Default.String = oversizedDefault
			}},
			{name: "before relation string", mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Before.Fields[1].GoName = oversizedID
			}},
			{name: "after default", mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].After.Fields[1].Default = &ir.ScalarDefault{
					Kind: ir.ScalarString, String: oversizedDefault,
				}
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				input := preflightFixture()
				test.mutate(&input)
				assertFailure(t, input, "resource_limit_exceeded")
			})
		}
	})

	t.Run("transient operation arms share the batch byte budget before clone", func(t *testing.T) {
		input := preflightFixture()
		payload := strings.Repeat("p", definition.MaxDocumentBytes/2)
		large := input.Definitions[1].Operations[0].ModelState.Clone()
		large.Fields[1].Default = &ir.ScalarDefault{Kind: ir.ScalarString, String: payload}
		operations := make([]preflightOperation, 12)
		for index := range operations {
			operations[index] = preflightOperation{
				Kind:       preflightCreateModel,
				ModelState: large,
				Before:     large,
				After:      large,
			}
		}
		input.Definitions[1].Operations = operations
		assertFailure(t, input, "resource_limit_exceeded")
		if large.Fields[1].Default == nil || large.Fields[1].Default.String != payload {
			t.Fatal("transient resource scan mutated caller-owned default")
		}
	})

	t.Run("every plan request arm is explicit before clone", func(t *testing.T) {
		oversizedStart := stateProjectState{
			formatVersion: stateFormatRelation,
			apps: map[string]ir.Schema{
				"oversized": {
					FormatVersion: ir.RelationFormatVersion,
					AppLabel:      "oversized",
					Models: []ir.Model{{
						Name:   "entry",
						GoName: "Entry",
						Fields: make([]ir.Field, definition.MaxJSONValues+1),
					}},
				},
			},
		}
		for _, test := range []struct {
			name   string
			mutate func(*preflightInput)
		}{
			{
				name: "all request arms absent",
				mutate: func(input *preflightInput) {
					input.PlanStart = stateProjectState{}
					input.PlanTarget = stateProjectState{}
					input.PlanApplied = nil
					input.PlanTargets = nil
				},
			},
			{
				name: "targets absent even with oversized start",
				mutate: func(input *preflightInput) {
					input.PlanStart = oversizedStart
					input.PlanTargets = nil
				},
			},
			{
				name: "start absent",
				mutate: func(input *preflightInput) {
					input.PlanStart = stateProjectState{}
				},
			},
			{
				name: "target absent",
				mutate: func(input *preflightInput) {
					input.PlanTarget = stateProjectState{}
				},
			},
			{
				name: "applied absent",
				mutate: func(input *preflightInput) {
					input.PlanApplied = nil
				},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				input := preflightFixture()
				test.mutate(&input)
				assertFailure(t, input, "plan_request_required")
			})
		}
	})
}

func TestPreflightDefinitionReplayOrderIsDeterministicTopologicalAndZeroIO(t *testing.T) {
	t.Parallel()

	base := preflightFixture()
	base.Definitions[1].Dependencies = []preflightMigrationKey{base.Definitions[0].Key}
	base.Definitions[2].Dependencies = []preflightMigrationKey{base.Definitions[1].Key}
	for permutation := 0; permutation < 6; permutation++ {
		input := preflightCloneInput(base)
		switch permutation {
		case 1:
			input.Definitions[0], input.Definitions[1] = input.Definitions[1], input.Definitions[0]
		case 2:
			input.Definitions[1], input.Definitions[2] = input.Definitions[2], input.Definitions[1]
		case 3:
			input.Definitions[0], input.Definitions[2] = input.Definitions[2], input.Definitions[0]
		case 4:
			input.Definitions = []preflightDefinition{input.Definitions[1], input.Definitions[2], input.Definitions[0]}
		case 5:
			input.Definitions = []preflightDefinition{input.Definitions[2], input.Definitions[0], input.Definitions[1]}
		}
		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 2 || len(snapshot.relations) != 1 {
			t.Fatalf("permutation %d topological replay = snapshot:%#v metrics:%+v error:%v", permutation, snapshot, metrics, err)
		}
		if snapshot.relations[0].Owner != base.Definitions[2].Key {
			t.Fatalf("permutation %d relation owner = %#v", permutation, snapshot.relations[0])
		}
	}

	t.Run("product graph validation and lexicographic ready order stay aligned", func(t *testing.T) {
		keys := map[string]preflightMigrationKey{
			"a_child": {App: "a", Name: "child"},
			"b_root":  {App: "b", Name: "root"},
			"z_root":  {App: "z", Name: "root"},
			"z_final": {App: "z", Name: "final"},
		}
		definitions := []preflightDefinition{
			{Key: keys["z_final"], Dependencies: []preflightMigrationKey{keys["z_root"]}},
			{Key: keys["a_child"], Dependencies: []preflightMigrationKey{keys["b_root"]}},
			{Key: keys["z_root"]},
			{Key: keys["b_root"]},
		}
		_, ordered, failure := preflightDefinitionGraph(definitions)
		if failure != nil {
			t.Fatalf("preflightDefinitionGraph(fork): %v", failure)
		}
		got := make([]preflightMigrationKey, len(ordered))
		for index := range ordered {
			got[index] = ordered[index].Key
		}
		want := []preflightMigrationKey{keys["b_root"], keys["a_child"], keys["z_root"], keys["z_final"]}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("lexicographic ready order = %#v, want %#v", got, want)
		}

		definitions[0].Dependencies = []preflightMigrationKey{{App: "", Name: "invalid"}}
		_, _, failure = preflightDefinitionGraph(definitions)
		if failure == nil || failure.Code != string(migrations.CodeInvalidDependency) {
			t.Fatalf("invalid dependency failure = %#v, want product invalid_dependency precedence", failure)
		}
	})
}

func TestPreflightCreatorIndexAndRelationsAreImmutableSnapshots(t *testing.T) {
	t.Parallel()

	input := preflightFixture()
	snapshot, metrics, err := preflightValidate(input)
	if err != nil || metrics != (preflightIOMetrics{}) {
		t.Fatalf("preflightValidate = metrics:%+v error:%v", metrics, err)
	}
	authorIdentity := stateModelIdentity{App: "authors", Model: "author"}
	input.State.apps["authors"].Models[0].Fields[1].Default.String = "mutated_input"
	input.Definitions[0].Operations[0].Model.Model = "mutated_input"
	input.Definitions[0].Operations[0].ModelState.Fields[1].Default.String = "mutated_operation_input"
	input.Definitions[2].Dependencies[0].Name = "mutated_input"
	input.Definitions[2].Operations[0].Relation.Reverse.Name = "mutated_input"

	first, exists := snapshot.preflightCreator(authorIdentity)
	if !exists {
		t.Fatal("author creator missing")
	}
	first.Creator.Name = "mutated_accessor"
	first.Model.Fields[0].Name = "mutated_accessor"
	first.Model.Fields[1].Default.String = "mutated_accessor"
	relations := snapshot.preflightRelations()
	relations[0].Declaration.Reverse.Name = "mutated_accessor"
	relations[0].SourceModel.Fields[2].Relation.Reverse.Name = "mutated_accessor"

	fresh, exists := snapshot.preflightCreator(authorIdentity)
	freshRelations := snapshot.preflightRelations()
	if !exists || fresh.Creator != (preflightMigrationKey{App: "authors", Name: "0001_initial"}) ||
		fresh.Model.Fields[0].Name != "id" || fresh.Model.Fields[1].Default == nil || fresh.Model.Fields[1].Default.String != "anonymous" ||
		freshRelations[0].Declaration.Reverse.Name != "articles" || freshRelations[0].SourceModel.Fields[2].Relation.Reverse.Name != "articles" {
		t.Fatalf("preflight snapshot retained alias: creator=%+v relations=%+v", fresh, freshRelations)
	}
}

func preflightErrorCode(err error, code string) bool {
	var failure *preflightCandidateError
	return errors.As(err, &failure) && failure.Category == "migration_relation_preflight_candidate_error" &&
		failure.Stage == "preflight" && failure.Code == code
}

func preflightFixture() preflightInput {
	authorsRoot := preflightMigrationKey{App: "authors", Name: "0001_initial"}
	blogRoot := preflightMigrationKey{App: "blog", Name: "0001_initial"}
	blogRelation := preflightMigrationKey{App: "blog", Name: "0002_article_author"}
	authorIdentity := stateModelIdentity{App: "authors", Model: "author"}
	articleIdentity := stateModelIdentity{App: "blog", Model: "article"}

	authors := ir.Schema{
		FormatVersion: ir.RelationFormatVersion,
		AppLabel:      "authors",
		Models: []ir.Model{{
			Name:    "author",
			GoName:  "Author",
			DBTable: "authors_author",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 100,
					Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: "anonymous"},
				},
			},
		}},
	}
	blog := ir.Schema{
		FormatVersion: ir.RelationFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:    "article",
			GoName:  "Article",
			DBTable: "blog_article",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
				{
					Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      authorIdentity.stateIRIdentity(),
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "articles"},
						OnDelete:    ir.DeleteProtect,
					},
				},
			},
		}},
	}
	authorCreate := authors.Models[0].Clone()
	articleFinal := blog.Models[0].Clone()
	articleCreate := articleFinal.Clone()
	articleCreate.Fields = articleCreate.Fields[:2]
	state, err := stateNewProject(stateFormatRelation, authors, blog)
	if err != nil {
		panic(err)
	}
	planBlog := blog.Clone()
	planBlog.Models[0] = articleCreate.Clone()
	planStart, err := stateNewProject(stateFormatRelation, authors, planBlog)
	if err != nil {
		panic(err)
	}
	return preflightInput{
		State:       state,
		PlanStart:   planStart,
		PlanTarget:  state.stateClone(),
		PlanApplied: []migrations.MigrationKey{{App: authorsRoot.App, Name: authorsRoot.Name}, {App: blogRoot.App, Name: blogRoot.Name}},
		PlanTargets: []preflightPlanTarget{preflightNamedPlanTarget(blogRelation)},
		Definitions: []preflightDefinition{
			{
				Key: authorsRoot,
				Operations: []preflightOperation{{
					Kind:       preflightCreateModel,
					Model:      authorIdentity,
					ModelState: authorCreate.Clone(),
				}},
			},
			{
				Key: blogRoot,
				Operations: []preflightOperation{{
					Kind:       preflightCreateModel,
					Model:      articleIdentity,
					ModelState: articleCreate.Clone(),
				}},
			},
			{
				Key:          blogRelation,
				Dependencies: []preflightMigrationKey{authorsRoot, blogRoot},
				Operations: []preflightOperation{{
					Kind:   preflightAddRelation,
					Before: articleCreate.Clone(),
					After:  articleFinal.Clone(),
					Relation: preflightRelationDeclaration{
						Source:           articleIdentity,
						Field:            "author",
						Target:           authorIdentity,
						DeclaredTable:    "blog_article",
						DeclaredColumn:   "author_id",
						DeclaredNullable: false,
						Cardinality:      ir.RelationManyToOne,
						Reverse:          ir.ReverseRelation{Name: "articles"},
						OnDelete:         ir.DeleteProtect,
					},
				}},
			},
		},
		Capability: preflightCapabilityDescriptor{RelationEditor: true},
	}
}

// preflightMoveTargetCreatorAfterRelation keeps every operation in its owning
// app while constructing a same-app target whose CreateModel appears after the
// relation operation. Cross-app targeting remains covered by preflightFixture;
// this helper isolates only same-migration operation chronology.
func preflightMoveTargetCreatorAfterRelation(input *preflightInput) {
	articleCreate := input.Definitions[1].Operations[0]
	relation := input.Definitions[2].Operations[0]
	categoryIdentity := stateModelIdentity{App: "blog", Model: "category"}
	categoryModel := input.Definitions[0].Operations[0].ModelState.Clone()
	categoryModel.Name = "category"
	categoryModel.GoName = "Category"
	categoryModel.DBTable = "blog_category"
	categoryCreate := preflightOperation{
		Kind:       preflightCreateModel,
		Model:      categoryIdentity,
		ModelState: categoryModel.Clone(),
	}
	relation.Relation.Target = categoryIdentity
	for index := range relation.After.Fields {
		field := &relation.After.Fields[index]
		if field.Name == relation.Relation.Field && field.Relation != nil {
			field.Relation.Target = categoryIdentity.stateIRIdentity()
		}
	}

	blog := input.State.apps["blog"]
	blog.Models[0] = relation.After.Clone()
	blog.Models = append(blog.Models, categoryModel.Clone())
	input.State.apps["blog"] = blog
	input.Definitions = []preflightDefinition{
		input.Definitions[0],
		{
			Key:        preflightMigrationKey{App: "blog", Name: "0001_combined"},
			Operations: []preflightOperation{articleCreate, relation, categoryCreate},
		},
	}
}
