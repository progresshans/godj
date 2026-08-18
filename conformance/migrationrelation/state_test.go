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

func TestStatePromotionAndScalarDemotionAreLosslessAndExplicit(t *testing.T) {
	t.Parallel()

	input := stateScalarFixture()
	scalar, err := stateNewProject(stateFormatScalar, input)
	if err != nil {
		t.Fatalf("stateNewProject scalar: %v", err)
	}
	promoted, err := statePromote(scalar)
	if err != nil {
		t.Fatalf("statePromote: %v", err)
	}
	demoted, err := stateDemote(promoted)
	if err != nil || !stateProjectEqual(demoted, scalar) {
		t.Fatalf("scalar promotion round trip = state:%#v error:%v", demoted, err)
	}
	promotedSchema, exists := promoted.stateSchema("blog")
	if !exists {
		t.Fatal("promoted blog schema missing")
	}
	wantPromoted := input.Clone()
	wantPromoted.FormatVersion = ir.RelationFormatVersion
	if !reflect.DeepEqual(promotedSchema, wantPromoted) {
		t.Fatalf("promotion changed scalar IR meaning:\n got=%#v\nwant=%#v", promotedSchema, wantPromoted)
	}
	if got, want := stateFieldOrder(input.Models[0].Fields), []string{"id", "title", "subtitle", "published"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("field order = %v, want %v", got, want)
	}
	if input.Models[0].Fields[1].Default == nil || input.Models[0].Fields[1].Default.String != "" ||
		input.Models[0].Fields[2].Default == nil || input.Models[0].Fields[2].Default.String != "draft" ||
		input.Models[0].Fields[3].Default == nil || input.Models[0].Fields[3].Default.Boolean {
		t.Fatalf("scalar defaults changed: %#v", input.Models[0].Fields)
	}
	if _, err := statePromote(promoted); !stateErrorHas(err, "promotion_source_version", "promotion_requires_state_1") {
		t.Fatalf("statePromote(state2) = %#v", err)
	}
	if _, err := stateDemote(scalar); !stateErrorHas(err, "demotion_source_version", "demotion_requires_state_2") {
		t.Fatalf("stateDemote(state1) = %#v", err)
	}

	t.Run("one loader record seals relation replay in both directions", func(t *testing.T) {
		incoming, beforeV2, afterV2 := stateStepRelationFixture(t)
		field := stateMustField(t, afterV2, "blog", "article", "author")
		operations := []stateStepOperation{{Before: beforeV2, After: afterV2}}
		set, snapshots, _, stepKey := stateAddFieldGraph(
			t, profileRelationTuple, "relation-state-step", "0002_author", "blog", "article", field, incoming, operations,
		)
		reconstructor, prepared := stateMustPreparedFromGraph(t, set, snapshots, stepKey)
		if prepared.sourceID != "relation-state-step" || prepared.producer != profileTestProducer() ||
			prepared.profile != profileRelationTuple || prepared.key != (migrations.MigrationKey{App: "blog", Name: "0002_author"}) ||
			prepared.sourceFormat != stateFormatScalar || !prepared.relationBearing ||
			len(prepared.operations) != 1 || prepared.operations[0].kind != stateStepOperationRelationAdd ||
			prepared.definitionHash == "" || prepared.provenanceSeal == "" || prepared.setDigest != set.profileDigest() ||
			prepared.graphToken == nil || prepared.verify == nil {
			t.Fatalf("prepared definition = %#v", prepared)
		}

		forward, trace, err := stateReplayMigrationStep(incoming, prepared, stateStepForward)
		if err != nil || !stateProjectEqual(forward, afterV2) || trace.SourceID != "relation-state-step" ||
			trace.Key != prepared.key || !trace.RelationProfile || trace.Promotions != 1 || trace.Demotions != 0 ||
			trace.Operations != 1 || !reflect.DeepEqual(trace.Formats, []int{stateFormatScalar, stateFormatRelation, stateFormatRelation}) {
			t.Fatalf("forward sealed replay = state:%#v trace:%+v error:%v", forward, trace, err)
		}
		backward, trace, err := stateReplayMigrationStep(forward, prepared, stateStepBackward)
		if err != nil || !stateProjectEqual(backward, incoming) || trace.Promotions != 0 || trace.Demotions != 1 ||
			trace.Operations != 1 || !reflect.DeepEqual(trace.Formats, []int{stateFormatRelation, stateFormatRelation, stateFormatScalar}) {
			t.Fatalf("backward sealed replay = state:%#v trace:%+v error:%v", backward, trace, err)
		}

		published := set.profileDefinitions()
		published[0].SourceID = "mutated-source"
		published[0].Producer.Name = "mutated-producer"
		published[0].Definition.Name = "mutated-definition"
		published = published[:1]
		mutatedAfter := snapshots[stepKey][0].After.stateClone()
		mutated := mutatedAfter.apps["blog"]
		mutated.Models[0].Fields[len(mutated.Models[0].Fields)-1].Relation.Target.AppLabel = "mutated-snapshot"
		mutatedAfter.apps["blog"] = mutated
		snapshots[stepKey][0].After = mutatedAfter
		delete(snapshots, stepKey)
		immutable, err := reconstructor.preparedByKey(stepKey)
		if err != nil {
			t.Fatalf("prepared accessor after caller mutation: %v", err)
		}
		replayed, _, err := stateReplayMigrationStep(incoming, immutable, stateStepForward)
		model, _ := replayed.stateModel("blog", "article")
		if err != nil || model.Fields[len(model.Fields)-1].Relation.Target.AppLabel != "authors" ||
			immutable.sourceID != "relation-state-step" || immutable.producer.Name != profileTestProducer().Name {
			t.Fatalf("caller mutation crossed prepared boundary: state=%#v prepared=%#v error=%v", replayed, immutable, err)
		}

		mutatedSource, err := reconstructor.preparedByKey(stepKey)
		if err != nil {
			t.Fatalf("prepared source mutation fixture: %v", err)
		}
		mutatedSource.sourceID = "valid-mutated-source"
		if got, trace, replayErr := stateReplayMigrationStep(incoming, mutatedSource, stateStepForward); !stateErrorHas(replayErr, "prepared_definition_seal_mismatch", "prepared_definition_identity_mismatch") ||
			got.apps != nil || trace.Operations != 0 {
			t.Fatalf("one-sided source mutation = state:%#v trace:%+v error:%#v", got, trace, replayErr)
		}
		mutatedProducer, err := reconstructor.preparedByKey(stepKey)
		if err != nil {
			t.Fatalf("prepared producer mutation fixture: %v", err)
		}
		mutatedProducer.producer = profileProducer{Name: "valid-mutated-producer", Version: "9"}
		if got, trace, replayErr := stateReplayMigrationStep(incoming, mutatedProducer, stateStepForward); !stateErrorHas(replayErr, "prepared_definition_seal_mismatch", "prepared_definition_identity_mismatch") ||
			got.apps != nil || trace.Operations != 0 {
			t.Fatalf("one-sided producer mutation = state:%#v trace:%+v error:%#v", got, trace, replayErr)
		}
	})

	t.Run("scalar-only legacy and relation profiles remain on their predecessor format", func(t *testing.T) {
		beforeSchema := stateScalarFixture()
		before, err := stateNewProject(stateFormatScalar, beforeSchema)
		if err != nil {
			t.Fatalf("scalar before: %v", err)
		}
		afterSchema := beforeSchema.Clone()
		afterSchema.Models[0].Fields = append(afterSchema.Models[0].Fields, ir.Field{
			Name: "archived", GoName: "Archived", Column: "archived", Kind: ir.FieldBoolean,
		})
		after, err := stateNewProject(stateFormatScalar, afterSchema)
		if err != nil {
			t.Fatalf("scalar after: %v", err)
		}
		field := stateMustField(t, after, "blog", "article", "archived")
		for _, tuple := range []profileCompatibility{profileLegacy, profileRelationTuple} {
			tuple := tuple
			t.Run(fmt.Sprintf("tuple_%d_%d_%d_%d", tuple.DefinitionFormat, tuple.LoaderABI, tuple.OperationCodec, tuple.SchemaIR), func(t *testing.T) {
				set, snapshots, _, stepKey := stateAddFieldGraph(
					t, tuple, "scalar-profile", "0002_archived", "blog", "article", field, before,
					[]stateStepOperation{{Before: before, After: after}},
				)
				_, prepared := stateMustPreparedFromGraph(t, set, snapshots, stepKey)
				got, trace, err := stateReplayMigrationStep(before, prepared, stateStepForward)
				if err != nil || !stateProjectEqual(got, after) || trace.Promotions != 0 || trace.Demotions != 0 ||
					trace.RelationProfile != (tuple == profileRelationTuple) ||
					!reflect.DeepEqual(trace.Formats, []int{stateFormatScalar, stateFormatScalar}) {
					t.Fatalf("scalar profile replay = state:%#v trace:%+v error:%v", got, trace, err)
				}
			})
		}
	})

	t.Run("preparation rejects missing provenance and every cross-pair", func(t *testing.T) {
		incoming, beforeV2, afterV2 := stateStepRelationFixture(t)
		field := stateMustField(t, afterV2, "blog", "article", "author")
		operations := []stateStepOperation{{Before: beforeV2, After: afterV2}}
		set, snapshots, rootKey, stepKey := stateAddFieldGraph(
			t, profileRelationTuple, "relation-reject", "0002_author", "blog", "article", field, incoming, operations,
		)

		// profileDefinitions is deliberately caller-visible, but it is only a
		// clone. Mutating and shrinking it before construction cannot affect the
		// loader set's private records or mint a different handoff.
		visible := set.profileDefinitions()
		visible[0].SourceID = "mutated-source"
		visible[0].Producer = profileProducer{Name: "mutated", Version: "9"}
		visible[0].Definition.Name = "mutated-definition"
		visible = visible[:1]
		reconstructor, prepared := stateMustPreparedFromGraph(t, set, snapshots, stepKey)
		if prepared.sourceID != "relation-reject" || prepared.key != stepKey || len(visible) != 1 {
			t.Fatalf("visible record mutation crossed loader boundary: visible=%#v prepared=%#v", visible, prepared)
		}

		forgedRecords := set.profileDefinitions()
		forgedRecords[0].SourceID = "forged-valid-source"
		forgedSet := profileSet{
			canonical: append([]byte(nil), set.canonical...), digest: set.digest,
			definitions: forgedRecords, hasLegacy: set.hasLegacy,
		}
		if _, err := stateNewPreparedReconstructor(forgedSet, stateCloneReconstructorSnapshots(snapshots)); !stateErrorHas(
			err, "prepared_loader_record_invalid", "loader_record_provenance_mismatch",
		) {
			t.Fatalf("hand-built/re-sourced published records = %#v", err)
		}

		missingGraph := stateCloneReconstructorSnapshots(snapshots)
		delete(missingGraph, stepKey)
		if _, err := stateNewPreparedReconstructor(set, missingGraph); !stateErrorHas(
			err, "prepared_snapshot_graph_mismatch", "complete_definition_snapshot_graph_required",
		) {
			t.Fatalf("shrunken snapshot graph = %#v", err)
		}
		rekeyedGraph := stateCloneReconstructorSnapshots(snapshots)
		rekeyedGraph[migrations.MigrationKey{App: "blog", Name: "9999_forged"}] = rekeyedGraph[stepKey]
		delete(rekeyedGraph, stepKey)
		if _, err := stateNewPreparedReconstructor(set, rekeyedGraph); !stateErrorHas(
			err, "prepared_snapshot_graph_mismatch", "complete_definition_snapshot_graph_required",
		) {
			t.Fatalf("re-keyed snapshot graph = %#v", err)
		}

		wrongSnapshot := stateCloneReconstructorSnapshots(snapshots)
		wrongAfter := wrongSnapshot[stepKey][0].After.stateClone()
		blog := wrongAfter.apps["blog"]
		blog.Models[0].Fields[len(blog.Models[0].Fields)-1].Name = "editor"
		blog.Models[0].Fields[len(blog.Models[0].Fields)-1].GoName = "Editor"
		blog.Models[0].Fields[len(blog.Models[0].Fields)-1].Column = "editor_id"
		wrongAfter.apps["blog"] = blog
		wrongSnapshot[stepKey][0].After = wrongAfter
		if _, err := stateNewPreparedReconstructor(set, wrongSnapshot); !stateErrorHas(
			err, "prepared_operation_cross_pair", "definition_snapshot_semantic_mismatch",
		) {
			t.Fatalf("definition/snapshot cross-pair = %#v", err)
		}

		countMismatch := stateCloneReconstructorSnapshots(snapshots)
		countMismatch[stepKey] = nil
		if _, err := stateNewPreparedReconstructor(set, countMismatch); !stateErrorHas(
			err, "prepared_operation_count_mismatch", "definition_snapshot_count_mismatch",
		) {
			t.Fatalf("operation count cross-pair = %#v", err)
		}

		unsupported := stateCloneReconstructorSnapshots(snapshots)
		changed := afterV2.stateClone()
		blog = changed.apps["blog"]
		blog.Models[0].Fields[len(blog.Models[0].Fields)-1].Relation.Reverse.Name = "edited"
		changed.apps["blog"] = blog
		unsupported[stepKey] = []stateStepOperation{{Before: afterV2, After: changed}}
		if _, err := stateNewPreparedReconstructor(set, unsupported); !stateErrorHas(
			err, "step_relation_delta_unsupported", "relation_delta_is_not_one_directional",
		) {
			t.Fatalf("unsupported relation delta = %#v", err)
		}

		wrongPredecessor := stateCloneReconstructorSnapshots(snapshots)
		wrongBefore := beforeV2.stateClone()
		blog = wrongBefore.apps["blog"]
		blog.Models[0].Fields = append(blog.Models[0].Fields, ir.Field{
			Name: "other", GoName: "Other", Column: "other", Kind: ir.FieldBoolean,
		})
		wrongBefore.apps["blog"] = blog
		wrongAfter = wrongBefore.stateClone()
		blog = wrongAfter.apps["blog"]
		blog.Models[0].Fields = append(blog.Models[0].Fields, field.Clone())
		wrongAfter.apps["blog"] = blog
		wrongPredecessor[stepKey] = []stateStepOperation{{Before: wrongBefore, After: wrongAfter}}
		if _, err := stateNewPreparedReconstructor(set, wrongPredecessor); !stateErrorHas(
			err, "prepared_predecessor_mismatch", "canonical_forward_predecessor_mismatch",
		) {
			t.Fatalf("predecessor cross-pair = %#v", err)
		}

		if got, trace, err := stateReplayMigrationStep(incoming, statePreparedMigrationDefinition{}, stateStepForward); !stateErrorHas(err, "prepared_source_invalid", "loader_source_provenance_required") ||
			got.apps != nil || trace.Operations != 0 {
			t.Fatalf("zero prepared replay = state:%#v trace:%+v error:%#v", got, trace, err)
		}
		other := incoming.stateClone()
		blog = other.apps["blog"]
		blog.Models[0].Fields = append(blog.Models[0].Fields, ir.Field{
			Name: "other", GoName: "Other", Column: "other", Kind: ir.FieldBoolean,
		})
		other.apps["blog"] = blog
		if failure := stateValidate(other); failure != nil {
			t.Fatalf("other incoming fixture: %v", failure)
		}
		if got, trace, err := stateReplayMigrationStep(other, prepared, stateStepForward); !stateErrorHas(err, "step_boundary_state_mismatch", "sealed_migration_boundary_mismatch") ||
			got.apps != nil || trace.Operations != 0 {
			t.Fatalf("re-paired incoming replay = state:%#v trace:%+v error:%#v", got, trace, err)
		}

		editorField := field.Clone()
		editorField.Name, editorField.GoName, editorField.Column = "editor", "Editor", "editor_id"
		editorAfter := beforeV2.stateClone()
		blog = editorAfter.apps["blog"]
		blog.Models[0].Fields = append(blog.Models[0].Fields, editorField.Clone())
		editorAfter.apps["blog"] = blog
		editorSet, editorSnapshots, _, editorKey := stateAddFieldGraph(
			t, profileRelationTuple, "relation-editor", "0002_editor", "blog", "article", editorField, incoming,
			[]stateStepOperation{{Before: beforeV2, After: editorAfter}},
		)
		_, editorPrepared := stateMustPreparedFromGraph(t, editorSet, editorSnapshots, editorKey)
		coherentReplacement := stateClonePreparedMigrationDefinition(editorPrepared)
		coherentReplacement.verify = prepared.verify
		if got, trace, err := stateReplayMigrationStep(incoming, coherentReplacement, stateStepForward); !stateErrorHas(err, "prepared_definition_seal_mismatch", "prepared_definition_identity_mismatch") ||
			got.apps != nil || trace.Operations != 0 {
			t.Fatalf("coherent prepared cross-pair = state:%#v trace:%+v error:%#v", got, trace, err)
		}

		if _, err := reconstructor.preparedByKey(rootKey); err != nil {
			t.Fatalf("root prepared accessor: %v", err)
		}
	})

	t.Run("actual planner drives applied latest before after and last relation demotion", func(t *testing.T) {
		reconstructor, rootKey, relationKey, rootAfter, latest, records, snapshots := stateMiniReconstructorFixture(t)
		if reconstructor.setDigest == profileEmptyDigest || len(reconstructor.definitions) != 2 {
			t.Fatalf("prepared reconstructor seal = digest %q definitions %d", reconstructor.setDigest, len(reconstructor.definitions))
		}
		before, err := reconstructor.stateAtApplied(rootKey)
		if err != nil || !stateProjectEqual(before, rootAfter) {
			t.Fatalf("before relation = state:%#v error:%v", before, err)
		}
		after, err := reconstructor.stateAtApplied(rootKey, relationKey)
		if err != nil || !stateProjectEqual(after, latest) {
			t.Fatalf("after/latest relation = state:%#v error:%v", after, err)
		}
		applied, applyPlan, err := reconstructor.planAndReplay(nil, migrations.NamedTarget(relationKey))
		if err != nil || !stateProjectEqual(applied, latest) ||
			!reflect.DeepEqual(applyPlan, []migrations.PlanStep{
				{Key: rootKey, Direction: migrations.DirectionForward},
				{Key: relationKey, Direction: migrations.DirectionForward},
			}) {
			t.Fatalf("parent-child apply = state:%#v plan:%+v error:%v", applied, applyPlan, err)
		}
		unapplied, unapplyPlan, err := reconstructor.planAndReplay(
			[]migrations.MigrationKey{rootKey, relationKey}, migrations.NamedTarget(rootKey),
		)
		if err != nil || !stateProjectEqual(unapplied, rootAfter) || unapplied.stateFormatVersion() != stateFormatScalar ||
			!reflect.DeepEqual(unapplyPlan, []migrations.PlanStep{{Key: relationKey, Direction: migrations.DirectionBackward}}) {
			t.Fatalf("child-first unapply/last relation demotion = state:%#v plan:%+v error:%v", unapplied, unapplyPlan, err)
		}

		records[0].SourceID = "mutated"
		records[0].Definition.Name = "mutated"
		records = records[:1]
		for key := range snapshots {
			if len(snapshots[key]) != 0 {
				snapshots[key][0].After.apps = nil
			}
		}
		delete(snapshots, relationKey)
		replayed, _, err := reconstructor.planAndReplay(nil, migrations.NamedTarget(relationKey))
		if err != nil || !stateProjectEqual(replayed, latest) {
			t.Fatalf("reconstructor retained caller aliases: state=%#v error=%v", replayed, err)
		}

		root := profileRelationCreateFixture()
		independent := profileCloneSource(root)
		independent.SourceID = "independent-root"
		independent.Definition.App = "archive"
		independent.Definition.Operations[0].AppLabel = "archive"
		independent.Definition.Operations[0].Model.DBTable = "archive_article"
		independentSet, _, loadErr := profileLoad(root, independent)
		if loadErr != nil {
			t.Fatalf("publish independent roots: %v", loadErr)
		}
		if _, constructErr := stateNewPreparedReconstructor(independentSet, nil); !stateErrorHas(
			constructErr, "prepared_snapshot_graph_mismatch", "complete_definition_snapshot_graph_required",
		) {
			t.Fatalf("mini reconstructor accepted a shrunken snapshot graph: %#v", constructErr)
		}
		independentSnapshots := map[migrations.MigrationKey][]stateStepOperation{
			{App: root.Definition.App, Name: root.Definition.Name}:               nil,
			{App: independent.Definition.App, Name: independent.Definition.Name}: nil,
		}
		if _, constructErr := stateNewPreparedReconstructor(independentSet, independentSnapshots); !stateErrorHas(
			constructErr, "prepared_graph_shape_unsupported", "mini_reconstructor_requires_linear_graph",
		) {
			t.Fatalf("mini reconstructor accepted an unproved branching graph: %#v", constructErr)
		}
	})
}
func TestStateRelationDemotionRejectsCanonicalFirstRelationWithoutPublishing(t *testing.T) {
	t.Parallel()

	relation, err := stateNewProject(stateFormatRelation, stateRelationFixture())
	if err != nil {
		t.Fatalf("stateNewProject relation: %v", err)
	}
	demoted, err := stateDemote(relation)
	var failure *stateCandidateError
	if !errors.As(err, &failure) || failure.Code != "relation_state_demotion_rejected" || failure.Reason != "relation_present" ||
		failure.App != "blog" || failure.Model != "article" || failure.Field != "author" {
		t.Fatalf("relation demotion failure = %#v", err)
	}
	if demoted.stateFormatVersion() != 0 || len(demoted.apps) != 0 {
		t.Fatalf("failed demotion published partial state: %#v", demoted)
	}
	if relation.stateFormatVersion() != stateFormatRelation {
		t.Fatal("failed demotion mutated source state")
	}

	if _, err := stateNewProject(stateFormatScalar, stateRelationFixture()); !stateErrorHas(
		err,
		"schema_ir_version_mismatch",
		"schema_ir_version",
	) {
		t.Fatalf("state 1 relation construction = %#v", err)
	}
}

func TestStateSnapshotsAndNestedRelationsNeverAliasCallers(t *testing.T) {
	t.Parallel()

	input := stateRelationFixture()
	state, err := stateNewProject(stateFormatRelation, input)
	if err != nil {
		t.Fatalf("stateNewProject relation: %v", err)
	}
	input.Models[0].Fields[1].Name = "mutated_input"
	input.Models[0].Fields[1].Relation.Target.AppLabel = "mutated_input"
	first, exists := state.stateModel("blog", "article")
	if !exists {
		t.Fatal("relation state model missing")
	}
	first.Fields[1].Name = "mutated_accessor"
	first.Fields[1].Relation.Reverse.Name = "mutated_accessor"
	clone := state.stateClone()
	clone.apps["blog"].Models[0].Fields[1].Relation.Target.ModelName = "mutated_clone"
	fresh, exists := state.stateModel("blog", "article")
	if !exists || fresh.Name != "article" || fresh.GoName != "Article" || fresh.DBTable != "blog_article" ||
		fresh.Fields[1].Name != "author" || fresh.Fields[1].GoName != "AuthorID" ||
		fresh.Fields[1].Relation.Target != (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) ||
		fresh.Fields[1].Relation.Cardinality != ir.RelationManyToOne ||
		fresh.Fields[1].Relation.Reverse.Name != "articles" || fresh.Fields[1].Relation.OnDelete != ir.DeleteProtect {
		t.Fatalf("state retained nested relation alias: %#v", fresh)
	}

	scalarInput := stateScalarFixture()
	scalar, err := stateNewProject(stateFormatScalar, scalarInput)
	if err != nil {
		t.Fatalf("stateNewProject scalar: %v", err)
	}
	scalarInput.Models[0].Fields[1].Default.String = "mutated_input"
	promoted, err := statePromote(scalar)
	if err != nil {
		t.Fatalf("statePromote: %v", err)
	}
	promoted.apps["blog"].Models[0].Fields[1].Default.String = "mutated_promoted"
	original, exists := scalar.stateModel("blog", "article")
	if !exists || original.Fields[1].Default == nil || original.Fields[1].Default.String != "" {
		t.Fatalf("promotion/default retained source alias: %#v", original)
	}

	t.Run("prepared definition owns provenance definition predecessor and snapshots", func(t *testing.T) {
		incoming, beforeV2, afterV2 := stateStepRelationFixture(t)
		canonicalIncoming := incoming.stateClone()
		field := stateMustField(t, afterV2, "blog", "article", "author")
		operations := []stateStepOperation{{Before: beforeV2.stateClone(), After: afterV2.stateClone()}}
		set, snapshots, _, stepKey := stateAddFieldGraph(
			t, profileRelationTuple, "alias-proof", "0002_author", "blog", "article", field, incoming, operations,
		)
		reconstructor, prepared := stateMustPreparedFromGraph(t, set, snapshots, stepKey)

		published := set.profileDefinitions()
		published[0].SourceID = "mutated"
		published[0].Producer.Version = "mutated"
		published[0].Definition.Name = "mutated"
		operationAfterState := snapshots[stepKey][0].After.stateClone()
		operationAfter := operationAfterState.apps["blog"]
		operationAfter.Models[0].Fields[len(operationAfter.Models[0].Fields)-1].Relation.Target.AppLabel = "mutated"
		operationAfterState.apps["blog"] = operationAfter
		snapshots[stepKey][0].After = operationAfterState
		incoming.apps["blog"] = ir.Schema{}

		got, trace, err := stateReplayMigrationStep(canonicalIncoming, prepared, stateStepForward)
		if err != nil || !stateProjectEqual(got, afterV2) || trace.SourceID != "alias-proof" || trace.Operations != 1 {
			t.Fatalf("prepared replay after caller mutation = state:%#v trace:%+v error:%v", got, trace, err)
		}
		got.apps["blog"].Models[0].Fields[len(got.apps["blog"].Models[0].Fields)-1].Relation.Target.ModelName = "mutated-result"
		replayed, _, err := stateReplayMigrationStep(canonicalIncoming, prepared, stateStepForward)
		model, exists := replayed.stateModel("blog", "article")
		if err != nil || !exists ||
			model.Fields[len(model.Fields)-1].Relation.Target != (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) {
			t.Fatalf("prepared result retained alias: model=%#v error=%v", model, err)
		}
		fresh, err := reconstructor.preparedByKey(stepKey)
		if err != nil || fresh.sourceID != "alias-proof" || fresh.producer != profileTestProducer() {
			t.Fatalf("prepared accessor retained caller alias: prepared=%#v error=%v", fresh, err)
		}
	})
}
func TestStateValidationPrecedenceIsVersionThenStructureThenRelation(t *testing.T) {
	t.Parallel()

	valid, err := stateNewProject(stateFormatRelation, stateRelationFixture())
	if err != nil {
		t.Fatalf("stateNewProject relation: %v", err)
	}

	invalidVersion := valid.stateClone()
	invalidVersion.formatVersion = 9
	invalidVersion.apps["wrong-key"] = invalidVersion.apps["blog"]
	delete(invalidVersion.apps, "blog")
	if _, err := stateDemote(invalidVersion); !stateErrorHas(err, "state_format_incompatible", "format_version") {
		t.Fatalf("version precedence error = %#v", err)
	}

	invalidApp := valid.stateClone()
	invalidApp.apps["wrong-key"] = invalidApp.apps["blog"]
	delete(invalidApp.apps, "blog")
	if _, err := stateDemote(invalidApp); !stateErrorHas(err, "invalid_app_identity", "app_identity") {
		t.Fatalf("app precedence error = %#v", err)
	}

	invalidIRVersion := valid.stateClone()
	schema := invalidIRVersion.apps["blog"]
	schema.FormatVersion = ir.FormatVersion
	invalidIRVersion.apps["blog"] = schema
	if _, err := stateDemote(invalidIRVersion); !stateErrorHas(err, "schema_ir_version_mismatch", "schema_ir_version") {
		t.Fatalf("IR-version precedence error = %#v", err)
	}

	invalidRelation := valid.stateClone()
	schema = invalidRelation.apps["blog"]
	schema.Models[0].Fields[1].Relation.OnDelete = ir.DeleteSetNull
	invalidRelation.apps["blog"] = schema
	if _, err := stateDemote(invalidRelation); !stateErrorHas(err, "schema_invalid", "invalid_nullability") {
		t.Fatalf("relation validation error = %#v", err)
	}
}

func TestStateResourceShapeFailsClosedBeforeCloneNormalizeOrPublication(t *testing.T) {
	t.Parallel()

	assertZero := func(t *testing.T, value stateProjectState) {
		t.Helper()
		if value.formatVersion != 0 || value.apps != nil {
			t.Fatalf("resource failure published state: %#v", value)
		}
	}

	t.Run("schema count is bounded before candidate map allocation", func(t *testing.T) {
		schemas := make([]ir.Schema, definition.MaxSources+1)
		got, err := stateNewProject(stateFormatScalar, schemas...)
		if !stateErrorHas(err, "resource_limit_exceeded", "app_count_exceeds_profile_limit") {
			t.Fatalf("schema count failure = %#v", err)
		}
		assertZero(t, got)
	})

	t.Run("identifier and default payload are individually bounded", func(t *testing.T) {
		identifier := stateScalarFixture()
		identifier.Models[0].Fields[1].Name = strings.Repeat("i", definition.MaxSourceIDBytes+1)
		got, err := stateNewProject(stateFormatScalar, identifier)
		if !stateErrorHas(err, "resource_limit_exceeded", "identifier_bytes_exceed_profile_limit") {
			t.Fatalf("identifier failure = %#v", err)
		}
		assertZero(t, got)

		payload := stateScalarFixture()
		ownedDefault := payload.Models[0].Fields[1].Default
		ownedDefault.String = strings.Repeat("d", definition.MaxDocumentBytes+1)
		got, err = stateNewProject(stateFormatScalar, payload)
		if !stateErrorAt(
			err,
			"resource_limit_exceeded",
			"default_payload_bytes_exceed_profile_limit",
			"blog",
			"article",
			"title",
		) {
			t.Fatalf("default payload failure = %#v", err)
		}
		assertZero(t, got)
		if payload.Models[0].Fields[1].Default != ownedDefault || len(ownedDefault.String) != definition.MaxDocumentBytes+1 {
			t.Fatal("failed construction replaced or mutated caller-owned default")
		}
	})

	t.Run("one schema document has an aggregate byte budget", func(t *testing.T) {
		schema := stateScalarFixture()
		payload := strings.Repeat("p", definition.MaxDocumentBytes/3+1)
		schema.Models[0].Fields[1].Default.String = payload
		schema.Models[0].Fields[2].Default.String = payload
		schema.Models[0].Fields = append(schema.Models[0].Fields, ir.Field{
			Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, MaxLength: 1,
			Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: payload},
		})
		got, err := stateNewProject(stateFormatScalar, schema)
		if !stateErrorHas(err, "resource_limit_exceeded", "schema_document_bytes_exceed_profile_limit") {
			t.Fatalf("schema document failure = %#v", err)
		}
		assertZero(t, got)
	})

	t.Run("project batch has an aggregate byte budget", func(t *testing.T) {
		payload := strings.Repeat("b", definition.MaxDocumentBytes/2)
		schemaCount := definition.MaxBatchBytes/(definition.MaxDocumentBytes/2) + 1
		schemas := make([]ir.Schema, schemaCount)
		for index := range schemas {
			schemas[index] = stateScalarFixture()
			schemas[index].AppLabel = fmt.Sprintf("app_%03d", index)
			schemas[index].Models[0].Fields[1].Default.String = payload
		}
		got, err := stateNewProject(stateFormatScalar, schemas...)
		if !stateErrorHas(err, "resource_limit_exceeded", "aggregate_bytes_exceed_profile_limit") {
			t.Fatalf("aggregate bytes failure = %#v", err)
		}
		assertZero(t, got)
	})

	t.Run("shared caller slices cannot bypass aggregate node budget", func(t *testing.T) {
		defaultValue := &ir.ScalarDefault{}
		relationValue := &ir.ForeignKeyRelation{}
		fields := make([]ir.Field, definition.MaxFieldsPerCreateModel)
		for index := range fields {
			fields[index].Default = defaultValue
			fields[index].Relation = relationValue
		}
		nodesPerModel := 1 + 3*definition.MaxFieldsPerCreateModel
		models := make([]ir.Model, definition.MaxJSONValues/nodesPerModel+1)
		for index := range models {
			models[index].Fields = fields
		}
		schema := ir.Schema{FormatVersion: ir.FormatVersion, AppLabel: "nodes", Models: models}
		got, err := stateNewProject(stateFormatScalar, schema)
		if !stateErrorHas(err, "resource_limit_exceeded", "aggregate_node_count_exceeds_profile_limit") {
			t.Fatalf("aggregate nodes failure = %#v", err)
		}
		assertZero(t, got)
	})

	t.Run("aggregate node exhaustion stops model traversal immediately", func(t *testing.T) {
		budget := stateResourceBudget{nodes: definition.MaxJSONValues - 1}
		stateResourceScanSchema(&budget, "bounded", ir.Schema{
			AppLabel: "bounded",
			Models: []ir.Model{
				{},
				{Name: strings.Repeat("late", definition.MaxSourceIDBytes)},
			},
		})
		if !budget.nodeOverflow || budget.countFailure != nil || budget.valueFailure != nil ||
			budget.docFailure != nil || budget.batchOverflow {
			t.Fatalf("post-exhaustion scanner state = %+v, want node overflow only", budget)
		}
	})

	t.Run("one model cannot exceed the create-model field budget", func(t *testing.T) {
		schema := ir.Schema{
			FormatVersion: ir.FormatVersion,
			AppLabel:      "fields",
			Models: []ir.Model{{
				Name:   "entry",
				Fields: make([]ir.Field, definition.MaxFieldsPerCreateModel+1),
			}},
		}
		got, err := stateNewProject(stateFormatScalar, schema)
		if !stateErrorAt(
			err,
			"resource_limit_exceeded",
			"model_field_count_exceeds_profile_limit",
			"fields",
			"entry",
			"",
		) {
			t.Fatalf("field count failure = %#v", err)
		}
		assertZero(t, got)
	})

	t.Run("prepared operation count is bounded before snapshot cloning", func(t *testing.T) {
		incoming, beforeV2, afterV2 := stateStepRelationFixture(t)
		field := stateMustField(t, afterV2, "blog", "article", "author")
		set, snapshots, _, stepKey := stateAddFieldGraph(
			t, profileRelationTuple, "oversized-operations", "0002_many", "blog", "article", field, incoming,
			[]stateStepOperation{{Before: beforeV2, After: afterV2}},
		)
		snapshots[stepKey] = make([]stateStepOperation, definition.MaxOperationsPerMigration+1)
		reconstructor, failure := stateNewPreparedReconstructor(set, snapshots)
		if !stateErrorHas(failure, "resource_limit_exceeded", "operation_count_exceeds_profile_limit") {
			t.Fatalf("prepared operation count failure = %#v", failure)
		}
		if reconstructor.definitions != nil {
			t.Fatalf("resource failure published prepared graph: %#v", reconstructor)
		}
	})

	t.Run("published definition aggregate is bounded before snapshot cloning", func(t *testing.T) {
		incoming, beforeV2, afterV2 := stateStepRelationFixture(t)
		field := stateMustField(t, afterV2, "blog", "article", "author")
		set, snapshots, _, stepKey := stateAddFieldGraph(
			t, profileRelationTuple, "resource-definition", "0002_author", "blog", "article", field, incoming,
			[]stateStepOperation{{Before: beforeV2, After: afterV2}},
		)
		payloadA := strings.Repeat("a", definition.MaxDocumentBytes/2+1)
		payloadB := strings.Repeat("b", definition.MaxDocumentBytes/2+1)
		stepIndex := -1
		for index := range set.definitions {
			if set.definitions[index].Definition.App == stepKey.App && set.definitions[index].Definition.Name == stepKey.Name {
				stepIndex = index
				break
			}
		}
		if stepIndex < 0 {
			t.Fatal("resource definition step missing")
		}
		first := profileCloneOperation(set.definitions[stepIndex].Definition.Operations[0])
		first.Field = &profileField{
			Name: "payload_a", GoName: "PayloadA", Column: "payload_a", Kind: string(ir.FieldChar), MaxLength: 1,
			Default: &profileDefault{Kind: string(ir.ScalarString), String: &payloadA},
		}
		second := profileCloneOperation(first)
		second.Field.Name, second.Field.GoName, second.Field.Column = "payload_b", "PayloadB", "payload_b"
		second.Field.Default.String = &payloadB
		set.definitions[stepIndex].Definition.Operations = []profileOperation{first, second}
		reconstructor, failure := stateNewPreparedReconstructor(set, snapshots)
		if !stateErrorHas(failure, "resource_limit_exceeded", "published_definition_bytes_exceed_profile_limit") || reconstructor.definitions != nil {
			t.Fatalf("published definition aggregate = reconstructor:%#v failure:%#v", reconstructor, failure)
		}
	})

	t.Run("preparation scans every caller snapshot before semantic pairing", func(t *testing.T) {
		incoming, beforeV2, afterV2 := stateStepRelationFixture(t)
		field := stateMustField(t, afterV2, "blog", "article", "author")
		set, snapshots, _, stepKey := stateAddFieldGraph(
			t, profileRelationTuple, "resource-arm", "0002_author", "blog", "article", field, incoming,
			[]stateStepOperation{{Before: beforeV2, After: afterV2}},
		)
		owned := afterV2.apps["blog"].Models[0].Fields[0].GoName
		oversizedAfter := afterV2.stateClone()
		oversized := oversizedAfter.apps["blog"]
		oversized.Models[0].Fields[0].GoName = strings.Repeat("g", definition.MaxSourceIDBytes+1)
		oversizedAfter.apps["blog"] = oversized
		snapshots[stepKey][0].After = oversizedAfter
		reconstructor, failure := stateNewPreparedReconstructor(set, snapshots)
		var resourceFailure *stateCandidateError
		if !stateErrorHas(failure, "resource_limit_exceeded", "identifier_bytes_exceed_profile_limit") ||
			!errors.As(failure, &resourceFailure) || !strings.HasPrefix(resourceFailure.Path, "snapshots.") {
			t.Fatalf("snapshot resource failure = %#v", failure)
		}
		if reconstructor.definitions != nil || beforeV2.apps["blog"].Models[0].Fields[0].GoName != owned ||
			afterV2.apps["blog"].Models[0].Fields[0].GoName != owned ||
			len(snapshots[stepKey][0].After.apps["blog"].Models[0].Fields[0].GoName) != definition.MaxSourceIDBytes+1 {
			t.Fatalf("failed preparation cloned or mutated caller state: reconstructor=%#v", reconstructor)
		}
	})

	t.Run("predecessor resource failure retains its exact path", func(t *testing.T) {
		incoming, beforeV2, afterV2 := stateStepRelationFixture(t)
		field := stateMustField(t, afterV2, "blog", "article", "author")
		set, snapshots, rootKey, _ := stateAddFieldGraph(
			t, profileRelationTuple, "resource-predecessor", "0002_author", "blog", "article", field, incoming,
			[]stateStepOperation{{Before: beforeV2, After: afterV2}},
		)
		oversizedPredecessor := snapshots[rootKey][0].After.stateClone()
		blog := oversizedPredecessor.apps["blog"]
		blog.Models[0].Fields[0].GoName = strings.Repeat("i", definition.MaxSourceIDBytes+1)
		oversizedPredecessor.apps["blog"] = blog
		snapshots[rootKey][0].After = oversizedPredecessor
		reconstructor, failure := stateNewPreparedReconstructor(set, snapshots)
		var resourceFailure *stateCandidateError
		if !stateErrorHas(failure, "resource_limit_exceeded", "identifier_bytes_exceed_profile_limit") ||
			!errors.As(failure, &resourceFailure) || !strings.HasPrefix(resourceFailure.Path, "snapshots.") || reconstructor.definitions != nil {
			t.Fatalf("predecessor resource path = reconstructor:%#v failure:%#v", reconstructor, failure)
		}
	})

	t.Run("one aggregate budget spans predecessor and every snapshot arm", func(t *testing.T) {
		incoming, beforeV2, afterV2 := stateStepRelationFixture(t)
		large := beforeV2.stateClone()
		blog := large.apps["blog"]
		payload := strings.Repeat("b", definition.MaxDocumentBytes/2)
		blog.Models[0].Fields = append(blog.Models[0].Fields, ir.Field{
			Name: "payload", GoName: "Payload", Column: "payload", Kind: ir.FieldChar,
			MaxLength: len(payload), Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: payload},
		})
		large.apps["blog"] = blog
		if failure := stateValidate(large); failure != nil {
			t.Fatalf("individual large snapshot must remain valid: %v", failure)
		}
		field := stateMustField(t, afterV2, "blog", "article", "author")
		set, snapshots, _, stepKey := stateAddFieldGraph(
			t, profileRelationTuple, "resource-aggregate", "0002_payload", "blog", "article", field, incoming,
			[]stateStepOperation{{Before: beforeV2, After: afterV2}},
		)
		operations := make([]stateStepOperation, 17)
		for index := range operations {
			operations[index] = stateStepOperation{Before: large, After: large}
		}
		snapshots[stepKey] = operations
		reconstructor, failure := stateNewPreparedReconstructor(set, snapshots)
		if !stateErrorHas(failure, "resource_limit_exceeded", "aggregate_bytes_exceed_profile_limit") ||
			reconstructor.definitions != nil {
			t.Fatalf("aggregate preparation failure = reconstructor:%#v failure:%#v", reconstructor, failure)
		}
		if large.apps["blog"].Models[0].Fields[1].Default.String != payload {
			t.Fatal("aggregate preclone scan mutated caller-owned state")
		}
	})
}
func TestStatePromotionAndDemotionScanCallerOwnedMapBeforeCloning(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		version int
		promote bool
	}{
		{name: "promotion source", version: stateFormatScalar, promote: true},
		{name: "demotion source", version: stateFormatRelation},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema := stateScalarFixture()
			if test.version == stateFormatRelation {
				schema.FormatVersion = ir.RelationFormatVersion
			}
			ownedDefault := schema.Models[0].Fields[1].Default
			ownedDefault.String = strings.Repeat("s", definition.MaxDocumentBytes+1)
			source := stateProjectState{
				formatVersion: test.version,
				apps:          map[string]ir.Schema{"blog": schema},
			}
			var got stateProjectState
			var err error
			if test.promote {
				got, err = statePromote(source)
			} else {
				got, err = stateDemote(source)
			}
			if !stateErrorHas(err, "resource_limit_exceeded", "default_payload_bytes_exceed_profile_limit") {
				t.Fatalf("source resource failure = %#v", err)
			}
			if got.formatVersion != 0 || got.apps != nil {
				t.Fatalf("resource failure published state: %#v", got)
			}
			if source.apps["blog"].Models[0].Fields[1].Default != ownedDefault ||
				len(ownedDefault.String) != definition.MaxDocumentBytes+1 {
				t.Fatal("failed transition cloned over or mutated caller-owned source")
			}
		})
	}
}

func TestStateMapResourceFailureSelectionIsDeterministicWithoutSorting(t *testing.T) {
	t.Parallel()

	a := stateRelationFixture()
	a.AppLabel = "a"
	a.Models[0].Fields[1].Relation.Reverse.Name = strings.Repeat("r", definition.MaxSourceIDBytes+1)
	z := stateRelationFixture()
	z.AppLabel = "z"
	z.Models[0].Fields[1].Relation.Reverse.Name = strings.Repeat("z", definition.MaxSourceIDBytes+1)
	value := stateProjectState{
		formatVersion: stateFormatRelation,
		apps:          map[string]ir.Schema{"z": z, "a": a},
	}
	for attempt := 0; attempt < 64; attempt++ {
		failure := stateValidate(value)
		if failure == nil || failure.Code != "resource_limit_exceeded" ||
			failure.Reason != "identifier_bytes_exceed_profile_limit" ||
			failure.App != "a" || failure.Model != "article" || failure.Field != "author" ||
			failure.Path != "models[0].fields[1].relation.reverse.name" {
			t.Fatalf("attempt %d resource failure = %#v", attempt, failure)
		}
	}

	// A fixed aggregate ceiling must outrank an earlier location-bearing count
	// failure. Otherwise the same map can report either member depending on its
	// iteration start point once the scanner stops at node exhaustion.
	countFailure := stateResourceFailure(
		"model_field_count_exceeds_profile_limit", "a", "entry", "", "models[0].fields",
	)
	if failure := stateResourceBudgetFailure(&stateResourceBudget{
		nodeOverflow: true,
		countFailure: countFailure,
	}); failure == nil || failure.Reason != "aggregate_node_count_exceeds_profile_limit" {
		t.Fatalf("compound resource precedence = %#v", failure)
	}
	aCount := ir.Schema{
		AppLabel: "a",
		Models: []ir.Model{{
			Name: "entry", Fields: make([]ir.Field, definition.MaxFieldsPerCreateModel+1),
		}},
	}
	zNodes := ir.Schema{
		AppLabel: "z",
		Models:   make([]ir.Model, definition.MaxJSONValues+1),
	}
	compound := stateProjectState{
		formatVersion: stateFormatRelation,
		apps:          map[string]ir.Schema{"z": zNodes, "a": aCount},
	}
	for attempt := 0; attempt < 64; attempt++ {
		failure := stateValidate(compound)
		if failure == nil || failure.Code != "resource_limit_exceeded" ||
			failure.Reason != "aggregate_node_count_exceeds_profile_limit" {
			t.Fatalf("attempt %d compound resource failure = %#v", attempt, failure)
		}
	}
}

func TestStateFieldKindsAndPrimaryKeyShapeFailClosedBeforePromotion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*ir.Schema)
		wantReason string
		wantField  string
	}{
		{
			name: "unknown kind", wantReason: "unsupported_field_kind", wantField: "title",
			mutate: func(schema *ir.Schema) { schema.Models[0].Fields[1].Kind = ir.FieldKind("mystery") },
		},
		{
			name: "auto must be primary key", wantReason: "required", wantField: "id",
			mutate: func(schema *ir.Schema) { schema.Models[0].Fields[0].PrimaryKey = false },
		},
		{
			name: "char max length is exact", wantReason: "invalid", wantField: "title",
			mutate: func(schema *ir.Schema) { schema.Models[0].Fields[1].MaxLength = 0 },
		},
		{
			name: "char default kind is exact", wantReason: "type_mismatch", wantField: "title",
			mutate: func(schema *ir.Schema) {
				schema.Models[0].Fields[1].Default = &ir.ScalarDefault{Kind: ir.ScalarBoolean}
			},
		},
		{
			name: "boolean cannot be nullable", wantReason: "unsupported", wantField: "published",
			mutate: func(schema *ir.Schema) { schema.Models[0].Fields[3].Nullable = true },
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema := stateScalarFixture()
			test.mutate(&schema)
			if _, err := stateNewProject(stateFormatScalar, schema); !stateErrorAt(
				err,
				"schema_invalid",
				test.wantReason,
				"blog",
				"article",
				test.wantField,
			) {
				t.Fatalf("stateNewProject invalid field = %#v", err)
			}

			invalid := stateProjectState{
				formatVersion: stateFormatScalar,
				apps:          map[string]ir.Schema{"blog": schema.Clone()},
			}
			promoted, err := statePromote(invalid)
			if !stateErrorHas(err, "schema_invalid", test.wantReason) {
				t.Fatalf("statePromote invalid field = %#v", err)
			}
			if promoted.stateFormatVersion() != 0 || len(promoted.apps) != 0 {
				t.Fatalf("failed promotion published partial state: %#v", promoted)
			}
		})
	}
}

func TestStateValidationAndDemotionChooseCanonicalFieldAcrossPermutations(t *testing.T) {
	t.Parallel()

	t.Run("explicit Auto and field order are preserved", func(t *testing.T) {
		schema := stateScalarFixture()
		state, err := stateNewProject(stateFormatScalar, schema)
		if err != nil {
			t.Fatalf("explicit normalized Auto rejected: %v", err)
		}
		model, _ := state.stateModel("blog", "article")
		if !reflect.DeepEqual(stateFieldOrder(model.Fields), []string{"id", "title", "subtitle", "published"}) {
			t.Fatalf("explicit field order changed: %#v", model.Fields)
		}
	})

	t.Run("implicit Auto is rejected rather than silently normalized", func(t *testing.T) {
		schema := stateScalarFixture()
		schema.Models[0].Fields = append([]ir.Field(nil), schema.Models[0].Fields[1:]...)
		if _, err := stateNewProject(stateFormatScalar, schema); !stateErrorHas(
			err,
			"schema_not_normalized",
			"normalization_would_change_state",
		) {
			t.Fatalf("implicit Auto input = %#v", err)
		}
	})

	t.Run("empty derived table and column are rejected as unnormalized", func(t *testing.T) {
		schema := stateScalarFixture()
		schema.Models[0].DBTable = ""
		schema.Models[0].Fields[1].Column = ""
		if _, err := stateNewProject(stateFormatScalar, schema); !stateErrorHas(
			err,
			"schema_not_normalized",
			"normalization_would_change_state",
		) {
			t.Fatalf("derived identities were silently accepted: %#v", err)
		}
	})

	relationSchema := stateRelationFixture()
	relationSchema.Models[0].Fields = append(relationSchema.Models[0].Fields, ir.Field{
		Name: "editor", GoName: "EditorID", Column: "editor_id", Kind: ir.FieldForeignKey, Nullable: true,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Disabled: true},
			OnDelete:    ir.DeleteSetNull,
		},
	})
	for permutation := 0; permutation < 2; permutation++ {
		schema := relationSchema.Clone()
		if permutation != 0 {
			fields := schema.Models[0].Fields
			for left, right := 0, len(fields)-1; left < right; left, right = left+1, right-1 {
				fields[left], fields[right] = fields[right], fields[left]
			}
		}
		state, err := stateNewProject(stateFormatRelation, schema)
		if err != nil {
			t.Fatalf("permutation %d relation state: %v", permutation, err)
		}
		if _, err := stateDemote(state); !stateErrorAt(
			err,
			"relation_state_demotion_rejected",
			"relation_present",
			"blog",
			"article",
			"author",
		) {
			t.Fatalf("permutation %d demotion failure = %#v", permutation, err)
		}
	}
}

func stateErrorHas(err error, code, reason string) bool {
	var failure *stateCandidateError
	return errors.As(err, &failure) && failure.Category == "migration_relation_state_candidate_error" &&
		failure.Stage == "state" && failure.Code == code && failure.Reason == reason
}

func stateErrorAt(err error, code, reason, app, model, field string) bool {
	var failure *stateCandidateError
	return errors.As(err, &failure) && failure.Category == "migration_relation_state_candidate_error" &&
		failure.Stage == "state" && failure.Code == code && failure.Reason == reason &&
		failure.App == app && failure.Model == model && failure.Field == field
}

func stateFieldOrder(fields []ir.Field) []string {
	names := make([]string, len(fields))
	for index, field := range fields {
		names[index] = field.Name
	}
	return names
}

func stateMustField(t *testing.T, value stateProjectState, app, model, field string) ir.Field {
	t.Helper()
	current, exists := value.stateModel(app, model)
	if !exists {
		t.Fatalf("state model %s.%s missing", app, model)
	}
	for _, candidate := range current.Fields {
		if candidate.Name == field {
			return candidate.Clone()
		}
	}
	t.Fatalf("state field %s.%s.%s missing", app, model, field)
	return ir.Field{}
}

func stateAddFieldGraph(
	t *testing.T,
	tuple profileCompatibility,
	sourceID string,
	name string,
	app string,
	model string,
	field ir.Field,
	predecessor stateProjectState,
	operations []stateStepOperation,
) (profileSet, map[migrations.MigrationKey][]stateStepOperation, migrations.MigrationKey, migrations.MigrationKey) {
	t.Helper()
	if _, exists := predecessor.stateModel(app, model); !exists || predecessor.stateFormatVersion() != stateFormatScalar {
		t.Fatalf("state add-field predecessor %s.%s must be a canonical scalar project", app, model)
	}
	empty, emptyErr := stateNewProject(stateFormatScalar)
	if emptyErr != nil {
		t.Fatalf("state add-field empty predecessor: %v", emptyErr)
	}
	sources := make([]profileSource, 0, len(predecessor.apps)+1)
	snapshots := make(map[migrations.MigrationKey][]stateStepOperation, len(predecessor.apps)+1)
	current := empty
	rootKey := migrations.MigrationKey{}
	for appIndex, predecessorApp := range predecessor.stateApps() {
		schema, exists := predecessor.stateSchema(predecessorApp)
		if !exists {
			t.Fatalf("state add-field predecessor app %s missing", predecessorApp)
		}
		rootName := fmt.Sprintf("0000_state_predecessor_%03d", appIndex)
		key := migrations.MigrationKey{App: predecessorApp, Name: rootName}
		dependencies := []profileIdentity{}
		if rootKey != (migrations.MigrationKey{}) {
			dependencies = append(dependencies, profileIdentity{App: rootKey.App, Name: rootKey.Name})
		}
		profileOperations := make([]profileOperation, len(schema.Models))
		stateOperations := make([]stateStepOperation, len(schema.Models))
		for modelIndex := range schema.Models {
			converted := profileModelFromIR(schema.Models[modelIndex])
			profileOperations[modelIndex] = profileOperation{
				AppLabel: predecessorApp, Kind: "create_model", Model: &converted,
			}
			next := current.stateClone()
			currentSchema, exists := next.apps[predecessorApp]
			if !exists {
				currentSchema = ir.Schema{
					FormatVersion: ir.FormatVersion, AppLabel: predecessorApp, Models: []ir.Model{},
				}
			}
			currentSchema.Models = append(currentSchema.Models, schema.Models[modelIndex].Clone())
			next.apps[predecessorApp] = currentSchema
			stateOperations[modelIndex] = stateStepOperation{Before: current, After: next}
			current = next
		}
		sources = append(sources, profileSource{
			SourceID: fmt.Sprintf("%s-predecessor-%03d", sourceID, appIndex),
			Producer: profileTestProducer(), Profile: tuple,
			Definition: profileDefinition{
				App: predecessorApp, Dependencies: dependencies, Name: rootName, Operations: profileOperations,
			},
		})
		snapshots[key] = stateOperations
		rootKey = key
	}
	if !stateProjectEqual(current, predecessor) {
		t.Fatalf("state add-field root graph did not reconstruct predecessor: got=%#v want=%#v", current, predecessor)
	}
	if rootKey == (migrations.MigrationKey{}) {
		t.Fatal("state add-field predecessor graph is empty")
	}
	stepKey := migrations.MigrationKey{App: app, Name: name}
	converted := profileFieldFromIR(field)
	stepSource := profileSource{
		SourceID: sourceID,
		Producer: profileTestProducer(),
		Profile:  tuple,
		Definition: profileDefinition{
			App: app, Dependencies: []profileIdentity{{App: rootKey.App, Name: rootKey.Name}}, Name: name,
			Operations: []profileOperation{{
				AppLabel: app, Kind: "add_field", ModelName: model, Field: &converted,
			}},
		},
	}
	sources = append(sources, stepSource)
	set, report, err := profileLoad(sources...)
	if err != nil || report.SetsPublished != 1 {
		t.Fatalf("publish state definition graph: report=%+v error=%v", report, err)
	}
	snapshots[stepKey] = operations
	return set, snapshots, rootKey, stepKey
}

func stateMustPreparedFromGraph(
	t *testing.T,
	set profileSet,
	snapshots map[migrations.MigrationKey][]stateStepOperation,
	key migrations.MigrationKey,
) (statePreparedReconstructor, statePreparedMigrationDefinition) {
	t.Helper()
	reconstructor, err := stateNewPreparedReconstructor(set, snapshots)
	if err != nil {
		t.Fatalf("prepare loader definition graph: %v", err)
	}
	prepared, err := reconstructor.preparedByKey(key)
	if err != nil {
		t.Fatalf("prepared definition %s: %v", key, err)
	}
	return reconstructor, prepared
}

func stateMiniReconstructorFixture(t *testing.T) (
	statePreparedReconstructor,
	migrations.MigrationKey,
	migrations.MigrationKey,
	stateProjectState,
	stateProjectState,
	[]profilePublishedDefinition,
	map[migrations.MigrationKey][]stateStepOperation,
) {
	t.Helper()
	rootSource := profileRelationCreateFixture()
	relationSource := profileRelationFixture()
	relationSource.Definition.Dependencies = []profileIdentity{{
		App: rootSource.Definition.App, Name: rootSource.Definition.Name,
	}}
	set, report, err := profileLoad(relationSource, rootSource)
	if err != nil || report.PlannerConstruction != 1 || report.SetsPublished != 1 {
		t.Fatalf("publish mini graph: report=%+v error=%v", report, err)
	}
	rootKey := migrations.MigrationKey{App: rootSource.Definition.App, Name: rootSource.Definition.Name}
	relationKey := migrations.MigrationKey{App: relationSource.Definition.App, Name: relationSource.Definition.Name}

	empty, err := stateNewProject(stateFormatScalar)
	if err != nil {
		t.Fatalf("mini empty state: %v", err)
	}
	rootModel, _, failure := profileModelIR(*rootSource.Definition.Operations[0].Model, rootSource.Definition.App, profileDecoderRelation)
	if failure != nil {
		t.Fatalf("mini root model: %v", failure)
	}
	rootAfter, err := stateNewProject(stateFormatScalar, ir.Schema{
		FormatVersion: ir.FormatVersion, AppLabel: rootSource.Definition.App, Models: []ir.Model{rootModel},
	})
	if err != nil {
		t.Fatalf("mini root state: %v", err)
	}
	relationBefore, err := statePromote(rootAfter)
	if err != nil {
		t.Fatalf("mini relation predecessor: %v", err)
	}
	relationAfter := relationBefore.stateClone()
	field, _, fieldFailure := profileFieldIR(*relationSource.Definition.Operations[0].Field, profileDecoderRelation)
	if fieldFailure != nil {
		t.Fatalf("mini relation field: %v", fieldFailure)
	}
	schema := relationAfter.apps[relationSource.Definition.App]
	schema.Models[0].Fields = append(schema.Models[0].Fields, field.Clone())
	relationAfter.apps[relationSource.Definition.App] = schema
	if validation := stateValidate(relationAfter); validation != nil {
		t.Fatalf("mini relation result: %v", validation)
	}
	snapshots := map[migrations.MigrationKey][]stateStepOperation{
		rootKey:     {{Before: empty, After: rootAfter}},
		relationKey: {{Before: relationBefore, After: relationAfter}},
	}
	reconstructor, constructErr := stateNewPreparedReconstructor(set, snapshots)
	if constructErr != nil {
		t.Fatalf("new prepared reconstructor: %v", constructErr)
	}
	records := set.profileDefinitions()
	return reconstructor, rootKey, relationKey, rootAfter, relationAfter, records, snapshots
}

func stateStepRelationFixture(t *testing.T) (stateProjectState, stateProjectState, stateProjectState) {
	t.Helper()
	authorsSchema := ir.Schema{
		FormatVersion: ir.FormatVersion,
		AppLabel:      "authors",
		Models: []ir.Model{{
			Name:    "author",
			GoName:  "Author",
			DBTable: "authors_author",
			Fields:  []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	scalarSchema := ir.Schema{
		FormatVersion: ir.FormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:    "article",
			GoName:  "Article",
			DBTable: "blog_article",
			Fields:  []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	incoming, err := stateNewProject(stateFormatScalar, authorsSchema, scalarSchema)
	if err != nil {
		t.Fatalf("state step incoming: %v", err)
	}
	beforeSchema := scalarSchema.Clone()
	beforeSchema.FormatVersion = ir.RelationFormatVersion
	authorsV2 := authorsSchema.Clone()
	authorsV2.FormatVersion = ir.RelationFormatVersion
	beforeV2, err := stateNewProject(stateFormatRelation, authorsV2, beforeSchema)
	if err != nil {
		t.Fatalf("state step before v2: %v", err)
	}
	afterSchema := beforeSchema.Clone()
	afterSchema.Models[0].Fields = append(afterSchema.Models[0].Fields, ir.Field{
		Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "articles"},
			OnDelete:    ir.DeleteProtect,
		},
	})
	afterV2, err := stateNewProject(stateFormatRelation, authorsV2, afterSchema)
	if err != nil {
		t.Fatalf("state step after v2: %v", err)
	}
	return incoming, beforeV2, afterV2
}

func stateScalarFixture() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.FormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:    "article",
			GoName:  "Article",
			DBTable: "blog_article",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200,
					Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: ""},
				},
				{
					Name: "subtitle", GoName: "Subtitle", Column: "subtitle", Kind: ir.FieldChar, Nullable: true, MaxLength: 80,
					Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: "draft"},
				},
				{
					Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean,
					Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false},
				},
			},
		}},
	}
}

func stateRelationFixture() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.RelationFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:    "article",
			GoName:  "Article",
			DBTable: "blog_article",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "articles"},
						OnDelete:    ir.DeleteProtect,
					},
				},
			},
		}},
	}
}
