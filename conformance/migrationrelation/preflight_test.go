package migrationrelation

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

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
			name: "target_primary_key_wrapper_mismatch", wantCode: "target_autofield_required",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.TargetField.Name = "legacy_id"
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
				t.Fatalf("preflight performed I/O: %+v", metrics)
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
		{
			name: "target name is separate wrapper metadata", wantCode: "target_autofield_required",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.TargetField.Name = "pk"
			},
		},
		{
			name: "target column is separate wrapper metadata", wantCode: "target_autofield_required",
			mutate: func(input *preflightInput) {
				input.Definitions[2].Operations[0].Relation.TargetField.Column = "pk_id"
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
				t.Fatalf("metadata mismatch performed I/O: %+v", metrics)
			}
			if !preflightErrorCode(err, test.wantCode) {
				t.Fatalf("metadata mismatch failure = %#v, want %s", err, test.wantCode)
			}
			if len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
				t.Fatalf("metadata mismatch published partial snapshot: %#v", snapshot)
			}
		})
	}

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
		duplicate.Relation.TargetField = preflightTargetField{Name: "missing", Column: "missing"}
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
				t.Fatalf("permutation %d duplicate published/performed I/O: snapshot=%#v metrics=%+v", permutation, snapshot, metrics)
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
		relation.TargetField = preflightTargetField{Name: "id", Column: "id"}
		snapshot, metrics, err := preflightValidate(input)
		var failure *preflightCandidateError
		if !errors.As(err, &failure) || failure.Code != "self_relation_unsupported" ||
			failure.Reason != "self_relation_unsupported" || failure.Source != failure.Target ||
			failure.Field != "author" {
			t.Fatalf("self relation failure = %#v", err)
		}
		if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
			t.Fatalf("self relation published/performed I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
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
					TargetField:   preflightTargetField{Name: "id", Column: "id"},
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
				t.Fatalf("permutation %d cycle published/performed I/O: snapshot=%#v metrics=%+v", permutation, snapshot, metrics)
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
				t.Fatalf("permutation %d published/performed I/O: snapshot=%#v metrics=%+v", permutation, snapshot, metrics)
			}
		}
	})

	t.Run("duplicate dependency is canonical and zero IO", func(t *testing.T) {
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
				t.Fatalf("permutation %d duplicate dependency published/performed I/O: snapshot=%#v metrics=%+v", permutation, snapshot, metrics)
			}
		}
	})

	t.Run("duplicate creators are derived canonically from operation records", func(t *testing.T) {
		base := preflightFixture()
		base.Definitions = append(base.Definitions, preflightDefinition{
			Key: preflightMigrationKey{App: "zeta", Name: "0001_duplicate"},
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
				failure.Owner != (preflightMigrationKey{App: "zeta", Name: "0001_duplicate"}) {
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
			Key:        preflightMigrationKey{App: "unrelated", Name: "0001"},
			Operations: []preflightOperation{relationOperation},
		})
		snapshot, metrics, err := preflightValidate(input)
		if !preflightErrorCode(err, "source_creator_not_ancestor") {
			t.Fatalf("unrelated source creator failure = %#v", err)
		}
		if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
			t.Fatalf("unrelated owner published/performed I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
		}
	})

	t.Run("same migration chronology comes only from ordered operations", func(t *testing.T) {
		input := preflightFixture()
		author := input.Definitions[0].Operations[0]
		article := input.Definitions[1].Operations[0]
		relation := input.Definitions[2].Operations[0]
		input.Definitions = []preflightDefinition{{
			Key:        preflightMigrationKey{App: "combined", Name: "0001"},
			Operations: []preflightOperation{author, article, relation},
		}}
		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 2 || len(snapshot.relations) != 1 {
			t.Fatalf("ordered creators = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}
		for identity, wantIndex := range map[stateModelIdentity]int{
			{App: "authors", Model: "author"}: 0,
			{App: "blog", Model: "article"}:   1,
		} {
			creator, exists := snapshot.preflightCreator(identity)
			if !exists || creator.CreatorOperation != wantIndex || creator.Creator != (preflightMigrationKey{App: "combined", Name: "0001"}) {
				t.Fatalf("creator %v = %#v, want operation %d", identity, creator, wantIndex)
			}
		}

		input.Definitions[0].Operations = []preflightOperation{author, relation, article}
		snapshot, metrics, err = preflightValidate(input)
		if !preflightErrorCode(err, "source_creator_not_ancestor") {
			t.Fatalf("later source creator accepted: %#v", err)
		}
		if metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 0 || len(snapshot.relations) != 0 {
			t.Fatalf("later creator published/performed I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
		}

		input.Definitions[0].Operations = []preflightOperation{article, relation, author}
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
			t.Fatalf("invalid state published/performed I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
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
		input := preflightInput{State: scalar, Capability: preflightCapabilityDescriptor{RelationEditor: true}}
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
			t.Fatalf("missing relation owner published/performed I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
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
			t.Fatalf("chronological replay failure published/performed I/O: snapshot=%#v metrics=%+v", snapshot, metrics)
		}
	}

	t.Run("relation-bearing create is its actual owner", func(t *testing.T) {
		input := preflightFixture()
		authorsRoot := input.Definitions[0].Key
		articleFinal := input.Definitions[2].Operations[0].After.Clone()
		input.Definitions[1].Dependencies = []preflightMigrationKey{authorsRoot}
		input.Definitions[1].Operations[0].ModelState = articleFinal
		input.Definitions[2].Operations = nil

		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.creators) != 2 || len(snapshot.relations) != 1 {
			t.Fatalf("relation-bearing CreateModel replay = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}
		relation := snapshot.relations[0]
		if relation.Owner != input.Definitions[1].Key || relation.OwnerOperation != 0 || relation.Declaration.Field != "author" {
			t.Fatalf("relation-bearing CreateModel owner = %#v", relation)
		}
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
		remove.Relation.TargetField.Name = "forged_pk"
		input.Definitions = append(input.Definitions, preflightDefinition{
			Key:          preflightMigrationKey{App: "blog", Name: "0003_remove_article_author"},
			Dependencies: []preflightMigrationKey{input.Definitions[2].Key},
			Operations:   []preflightOperation{remove},
		})
		assertFailure(t, input, "target_autofield_required")
	})

	t.Run("product planner replays one mixed scalar relation step forward and backward", func(t *testing.T) {
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
		mixed.PlanTargets = []migrations.Target{migrations.NamedTarget(migrations.MigrationKey{App: relationKey.App, Name: relationKey.Name})}
		snapshot, metrics, err := preflightValidate(mixed)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.relations) != 1 {
			t.Fatalf("mixed forward product plan = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}

		backward := preflightCloneInput(mixed)
		backward.PlanStart = mixed.State.stateClone()
		backward.PlanTarget = start.stateClone()
		backward.PlanApplied = append(backward.PlanApplied, migrations.MigrationKey{App: relationKey.App, Name: relationKey.Name})
		backward.PlanTargets = []migrations.Target{migrations.NamedTarget(migrations.MigrationKey{App: blogRoot.App, Name: blogRoot.Name})}
		snapshot, metrics, err = preflightValidate(backward)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.relations) != 1 {
			t.Fatalf("mixed backward product plan = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
		}
	})

	t.Run("already satisfied product target is a valid empty plan", func(t *testing.T) {
		input := preflightFixture()
		input.PlanStart = input.State.stateClone()
		input.PlanTarget = input.State.stateClone()
		input.PlanApplied = make([]migrations.MigrationKey, len(input.Definitions))
		for index, definitionValue := range input.Definitions {
			input.PlanApplied[index] = migrations.MigrationKey{App: definitionValue.Key.App, Name: definitionValue.Key.Name}
		}
		last := input.Definitions[len(input.Definitions)-1].Key
		input.PlanTargets = []migrations.Target{migrations.NamedTarget(migrations.MigrationKey{App: last.App, Name: last.Name})}
		snapshot, metrics, err := preflightValidate(input)
		if err != nil || metrics != (preflightIOMetrics{}) || len(snapshot.relations) != 1 {
			t.Fatalf("already-satisfied product plan = snapshot:%#v metrics:%+v error:%v", snapshot, metrics, err)
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
					Target: test.target, TargetField: preflightTargetField{Name: "id", Column: "id"},
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
	})

	t.Run("profile-tied operation cap rejects before operation inspection", func(t *testing.T) {
		input := preflightFixture()
		input.Definitions[0].Operations = make([]preflightOperation, definition.MaxOperationsPerMigration+1)
		assertFailure(t, input, "resource_limit_exceeded")
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
		input.PlanTargets = []migrations.Target{
			migrations.NamedTarget(migrations.MigrationKey{App: last.App, Name: last.Name}),
		}
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

	t.Run("plan state and applied arms require a target before clone", func(t *testing.T) {
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
				name: "plan_start",
				mutate: func(input *preflightInput) {
					input.PlanStart = oversizedStart
				},
			},
			{
				name: "plan_target",
				mutate: func(input *preflightInput) {
					input.PlanTarget = input.State.stateClone()
				},
			},
			{
				name: "plan_applied",
				mutate: func(input *preflightInput) {
					input.PlanApplied = []migrations.MigrationKey{{App: "blog", Name: "0001_initial"}}
				},
			},
			{
				name: "plan_applied_empty_non_nil",
				mutate: func(input *preflightInput) {
					input.PlanApplied = []migrations.MigrationKey{}
				},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				input := preflightFixture()
				test.mutate(&input)
				assertFailure(t, input, "conflicting_plan_arms")
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
	return preflightInput{
		State: state,
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
						TargetField:      preflightTargetField{Name: "id", Column: "id"},
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
