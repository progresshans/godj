package migrationrelation

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
	_ "modernc.org/sqlite"
)

func TestLifecycleIntegrationCandidateUsesOneExistingFenceForMixedWork(t *testing.T) {
	t.Run("public decision shape has one identity owner and existing fenced ports", func(t *testing.T) {
		capabilitiesType := reflect.TypeOf(RelationMigrationCapabilities{})
		wantCapabilityFields := []string{
			"CreateModelForeignKeys",
			"AddNullableForeignKey",
			"AddRequiredForeignKeyToEmptyTable",
			"RemoveForeignKeyByTableRemake",
		}
		if capabilitiesType.NumField() != len(wantCapabilityFields) {
			t.Fatalf("RelationMigrationCapabilities field count = %d, want %d", capabilitiesType.NumField(), len(wantCapabilityFields))
		}
		for index, want := range wantCapabilityFields {
			field := capabilitiesType.Field(index)
			if field.Name != want || field.Type.Kind() != reflect.Bool {
				t.Fatalf("RelationMigrationCapabilities field[%d] = %s %s, want %s bool", index, field.Name, field.Type, want)
			}
		}
		intentType := reflect.TypeOf(RelationMigrationIntent{})
		if intentType.NumField() != 1 || intentType.Field(0).Name != "Operations" ||
			intentType.Field(0).Type != reflect.TypeOf([]RelationMigrationOperation(nil)) {
			t.Fatalf("RelationMigrationIntent fields = %v, want Operations only", reflect.VisibleFields(intentType))
		}
		for _, forbidden := range []string{"App", "Name", "Direction"} {
			if _, exists := intentType.FieldByName(forbidden); exists {
				t.Fatalf("RelationMigrationIntent duplicates HistoryTransition field %q", forbidden)
			}
		}
		preparedType := reflect.TypeOf(lifecyclePreparedRelationStep{})
		if preparedType.NumField() != 4 {
			t.Fatalf("prepared lifecycle handoff field count = %d, want exact opaque transition/intent/plan/binding", preparedType.NumField())
		}
		for index := 0; index < preparedType.NumField(); index++ {
			if preparedType.Field(index).PkgPath == "" {
				t.Fatalf("prepared lifecycle handoff field %q is exported", preparedType.Field(index).Name)
			}
		}
		operationType := reflect.TypeOf(RelationMigrationOperation{})
		wantOperationFields := []string{"OperationIndex", "Kind", "Before", "After", "Targets"}
		wantOperationTypes := []reflect.Type{
			reflect.TypeOf(int(0)),
			reflect.TypeOf(RelationMigrationOperationKind(0)),
			reflect.TypeOf(ir.Model{}),
			reflect.TypeOf(ir.Model{}),
			reflect.TypeOf([]RelationMigrationTarget(nil)),
		}
		if operationType.NumField() != len(wantOperationFields) {
			t.Fatalf("RelationMigrationOperation field count = %d, want %d", operationType.NumField(), len(wantOperationFields))
		}
		for index, want := range wantOperationFields {
			field := operationType.Field(index)
			if field.Name != want || field.Type != wantOperationTypes[index] {
				t.Fatalf("RelationMigrationOperation field[%d] = %s %s, want %s %s", index, field.Name, field.Type, want, wantOperationTypes[index])
			}
		}
		targetType := reflect.TypeOf(RelationMigrationTarget{})
		wantTargetFields := []string{"SourceField", "TargetModel", "TargetKey"}
		wantTargetTypes := []reflect.Type{reflect.TypeOf(ir.Field{}), reflect.TypeOf(ir.Model{}), reflect.TypeOf(ir.Field{})}
		if targetType.NumField() != len(wantTargetFields) {
			t.Fatalf("RelationMigrationTarget field count = %d, want %d", targetType.NumField(), len(wantTargetFields))
		}
		for index, want := range wantTargetFields {
			field := targetType.Field(index)
			if field.Name != want || field.Type != wantTargetTypes[index] {
				t.Fatalf("RelationMigrationTarget field[%d] = %s %s, want %s %s", index, field.Name, field.Type, want, wantTargetTypes[index])
			}
		}
		wantKinds := []RelationMigrationOperationKind{
			RelationMigrationCreateModel,
			RelationMigrationDeleteModel,
			RelationMigrationAddField,
			RelationMigrationRemoveField,
		}
		if reflect.TypeOf(RelationMigrationOperationKind(0)).Kind() != reflect.Uint8 ||
			!reflect.DeepEqual(wantKinds, []RelationMigrationOperationKind{1, 2, 3, 4}) {
			t.Fatalf("relation operation kinds = %#v, want stable 1..4", wantKinds)
		}
		backendPort := reflect.TypeOf((*RelationRevisionFencedBackend)(nil)).Elem()
		capabilityMethod, exists := backendPort.MethodByName("RelationMigrationCapabilities")
		if backendPort.NumMethod() != 2 || !exists || capabilityMethod.Type.NumIn() != 0 || capabilityMethod.Type.NumOut() != 1 ||
			capabilityMethod.Type.Out(0) != capabilitiesType {
			t.Fatalf("RelationMigrationCapabilities method = %#v, want exact zero-argument capability value signature", capabilityMethod)
		}
		sessionPort := reflect.TypeOf((*RelationRevisionFencedSession)(nil)).Elem()
		beginMethod, exists := sessionPort.MethodByName("BeginRelationFencedMigration")
		if sessionPort.NumMethod() != 4 || !exists || beginMethod.Type.NumIn() != 3 || beginMethod.Type.NumOut() != 2 ||
			beginMethod.Type.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() ||
			beginMethod.Type.In(1) != reflect.TypeOf(migrationbackend.HistoryTransition{}) ||
			beginMethod.Type.In(2) != reflect.TypeOf(RelationMigrationIntent{}) ||
			beginMethod.Type.Out(0) != reflect.TypeOf((*migrationbackend.RevisionFencedTransaction)(nil)).Elem() ||
			beginMethod.Type.Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
			t.Fatalf("BeginRelationFencedMigration method = %#v, want exact existing fenced transaction signature", beginMethod)
		}
		var _ migrationbackend.RevisionFencedBackend = (RelationRevisionFencedBackend)(nil)
		var _ migrationbackend.RevisionFencedSession = (RelationRevisionFencedSession)(nil)
	})

	t.Run("additive session port leaves scalar-only implementation compatible without legacy begin fallback", func(t *testing.T) {
		session := &lifecycleScalarOnlySession{}
		intent := lifecycleNullableEditorIntent()
		_, err := lifecycleBeginRelationFenced(
			context.Background(),
			session,
			lifecycleRelationTransition(migrationbackend.HistoryTransitionApply),
			intent,
		)
		if !errors.Is(err, lifecycleRelationErrCapability) {
			t.Fatalf("lifecycleBeginRelationFenced() error = %v, want capability", err)
		}
		if session.beginCalls != 0 {
			t.Fatalf("legacy BeginFencedMigration() calls = %d, want 0", session.beginCalls)
		}
	})

	t.Run("scalar-only intent routes out before relation capability or session", func(t *testing.T) {
		mixed := lifecyclePublishedAndEditorIntent()
		scalarOnly := RelationMigrationIntent{Operations: []RelationMigrationOperation{mixed.Operations[0]}}
		trace := &lifecycleDecisionTrace{}
		session := &lifecycleTraceSession{}
		backend := &lifecycleDecisionBackend{
			trace: trace, session: session, capabilities: lifecycleAllRelationCapabilities(),
		}
		result, err := lifecycleExecuteDecisionPath(
			context.Background(),
			backend,
			lifecycleTestForgedHandoff(
				lifecycleRelationTransition(migrationbackend.HistoryTransitionApply),
				scalarOnly,
			),
			trace,
		)
		if result != (lifecycleMixedResult{}) || !errors.Is(err, lifecycleRelationErrIntent) {
			t.Fatalf("scalar-only relation route = result:%+v error:%v", result, err)
		}
		if backend.capabilityCalls != 0 || backend.openCalls != 0 || session.readCalls != 0 ||
			session.relationBeginCalls != 0 || session.legacyBeginCalls != 0 || session.closeCalls != 0 ||
			session.lastTransaction != nil || len(session.snapshotEvents()) != 0 {
			t.Fatalf(
				"scalar-only route touched relation lifecycle: capability=%d open=%d read=%d relation=%d legacy=%d close=%d transaction=%#v events=%#v",
				backend.capabilityCalls, backend.openCalls, session.readCalls, session.relationBeginCalls,
				session.legacyBeginCalls, session.closeCalls, session.lastTransaction, session.snapshotEvents(),
			)
		}
	})

	t.Run("backend capability rejection occurs before session open", func(t *testing.T) {
		trace := &lifecycleDecisionTrace{}
		session := &lifecycleTraceSession{}
		backend := &lifecycleDecisionBackend{trace: trace, session: session}
		result, err := lifecycleExecuteDecisionPath(
			context.Background(),
			backend,
			lifecycleTestAdaptedHandoff(
				t,
				migrations.MigrationKey{App: "blog", Name: "0002_article_author"},
				migrations.DirectionForward,
				lifecycleNullableEditorIntent(),
			),
			trace,
		)
		if result != (lifecycleMixedResult{}) || !errors.Is(err, lifecycleRelationErrCapability) {
			t.Fatalf("capability rejection = (%+v, %v), want zero/capability", result, err)
		}
		metrics, _ := trace.snapshot()
		if metrics != (lifecyclePreflightMetrics{StaticPreSession: 1}) ||
			backend.capabilityCalls != 1 || backend.openCalls != 0 || session.readCalls != 0 ||
			session.relationBeginCalls != 0 || session.legacyBeginCalls != 0 || session.closeCalls != 0 {
			t.Fatalf(
				"capability rejection touched lifecycle: metrics=%+v capability=%d open=%d snapshot=%d relation=%d legacy=%d close=%d",
				metrics, backend.capabilityCalls, backend.openCalls, session.readCalls,
				session.relationBeginCalls, session.legacyBeginCalls, session.closeCalls,
			)
		}
	})

	t.Run("prepared handoff rejects transition intent re-pairing before capability or session", func(t *testing.T) {
		base := lifecycleTestAdaptedHandoff(
			t,
			migrations.MigrationKey{App: "blog", Name: "0002_article_author"},
			migrations.DirectionForward,
			lifecycleNullableEditorIntent(),
		)
		wrongApp := lifecycleClonePreparedRelationStep(base)
		wrongApp.transition.Migration.App = "other"
		wrongName := lifecycleClonePreparedRelationStep(base)
		wrongName.transition.Migration.Name = "0002_relation"
		wrongDirection := lifecycleClonePreparedRelationStep(base)
		wrongDirection.transition.Kind = migrationbackend.HistoryTransitionUnapply
		wrongSuccessor := lifecycleClonePreparedRelationStep(base)
		wrongSuccessor.intent.Operations[0].After.Fields[0].Name = "wrong_successor"
		wrongFenceBinding := lifecycleClonePreparedRelationStep(base)
		wrongFenceBinding.binding.transition.Migration.Name = "0002_relation"
		unsealed, err := lifecyclePrepareMixedStep(
			migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: "blog", Name: "0002_relation"},
				Kind:      migrationbackend.HistoryTransitionApply,
			},
			lifecycleNullableEditorIntent(),
		)
		if err != nil {
			t.Fatalf("construct adversarial unsealed pair: %v", err)
		}

		for _, test := range []struct {
			name string
			step lifecyclePreparedRelationStep
		}{
			{name: "wrong app", step: wrongApp},
			{name: "wrong migration name", step: wrongName},
			{name: "wrong direction", step: wrongDirection},
			{name: "wrong from-to successor", step: wrongSuccessor},
			{name: "wrong sealed fence transition", step: wrongFenceBinding},
			{name: "unsealed arbitrary revision-fence pair", step: unsealed},
		} {
			t.Run(test.name, func(t *testing.T) {
				trace := &lifecycleDecisionTrace{}
				session := &lifecycleTraceSession{}
				backend := &lifecycleDecisionBackend{
					trace: trace, session: session, capabilities: lifecycleAllRelationCapabilities(),
				}
				result, err := lifecycleExecuteDecisionPath(
					context.Background(), backend, test.step, trace,
				)
				if result != (lifecycleMixedResult{}) || !errors.Is(err, lifecycleRelationErrIntent) {
					t.Fatalf("forged prepared handoff = result:%+v error:%v", result, err)
				}
				metrics, events := trace.snapshot()
				if metrics != (lifecyclePreflightMetrics{StaticPreSession: 1}) || len(events) != 1 ||
					events[0] != "static_preflight" || backend.capabilityCalls != 0 || backend.openCalls != 0 ||
					session.readCalls != 0 || session.relationBeginCalls != 0 || session.legacyBeginCalls != 0 ||
					session.closeCalls != 0 || session.lastTransaction != nil {
					t.Fatalf(
						"forged handoff touched I/O: metrics=%+v events=%#v capability=%d open=%d read=%d relation=%d legacy=%d close=%d transaction=%#v",
						metrics, events, backend.capabilityCalls, backend.openCalls, session.readCalls,
						session.relationBeginCalls, session.legacyBeginCalls, session.closeCalls, session.lastTransaction,
					)
				}
			})
		}
	})

	t.Run("product history and Planner validation reject unsafe current steps before relation begin", func(t *testing.T) {
		baseIntent := lifecycleNullableEditorIntent()
		parent := migrations.Migration{App: "blog", Name: "0001_parent"}
		child := migrations.Migration{
			App: "blog", Name: "0002_child",
			Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_parent"}},
		}
		alternateParent := migrations.Migration{App: "other", Name: "0001_parent"}
		parentApplied := lifecycleTestParentChildPreflightInput(
			t, parent, alternateParent, child, baseIntent, "parent_applied",
		)
		parentSnapshot, metrics, err := preflightValidate(parentApplied)
		if err != nil || metrics != (preflightIOMetrics{}) {
			t.Fatalf("parent-applied graph preflight = metrics:%+v error:%v", metrics, err)
		}
		base, ok := parentSnapshot.preflightHandoff(preflightMigrationKey{App: child.App, Name: child.Name})
		if !ok {
			t.Fatal("parent-applied graph did not publish the child handoff")
		}
		childDefinitionIndex := -1
		for index := range base.plan.definitions {
			if base.plan.definitions[index].Key() == child.Key() {
				childDefinitionIndex = index
				break
			}
		}
		if childDefinitionIndex < 0 || len(base.plan.definitions[childDefinitionIndex].Dependencies) != 1 {
			t.Fatalf("sealed child definition/dependencies = %#v", base.plan.definitions)
		}

		removedDependency := lifecycleClonePreparedRelationStep(base)
		removedDependency.plan.definitions[childDefinitionIndex].Dependencies = nil
		swappedDependency := lifecycleClonePreparedRelationStep(base)
		swappedDependency.plan.definitions[childDefinitionIndex].Dependencies[0] = alternateParent.Key()
		mutatedTarget := lifecycleClonePreparedRelationStep(base)
		mutatedTarget.plan.targets[0] = preflightNamedPlanTarget(preflightMigrationKey{App: parent.App, Name: parent.Name})
		mutatedApplied := lifecycleClonePreparedRelationStep(base)
		mutatedApplied.plan.applied = nil
		mutatedExpected := lifecycleClonePreparedRelationStep(base)
		mutatedExpected.plan.expected = migrations.PlanStep{Key: parent.Key(), Direction: migrations.DirectionForward}
		mutatedBindingGraph := lifecycleClonePreparedRelationStep(base)
		mutatedBindingGraph.binding.plan.definitions[childDefinitionIndex].Dependencies = nil
		unsealedSingleton, err := lifecyclePrepareMixedStep(
			migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: child.App, Name: child.Name},
				Kind:      migrationbackend.HistoryTransitionApply,
			},
			baseIntent,
		)
		if err != nil {
			t.Fatalf("construct unsealed singleton: %v", err)
		}

		for _, test := range []struct {
			name string
			step lifecyclePreparedRelationStep
		}{
			{name: "removed dependency", step: removedDependency},
			{name: "swapped dependency", step: swappedDependency},
			{name: "mutated target", step: mutatedTarget},
			{name: "mutated applied snapshot", step: mutatedApplied},
			{name: "mutated expected step", step: mutatedExpected},
			{name: "mutated binding graph", step: mutatedBindingGraph},
			{name: "unsealed singleton graph", step: unsealedSingleton},
		} {
			t.Run(test.name, func(t *testing.T) {
				trace := &lifecycleDecisionTrace{}
				session := &lifecycleTraceSession{records: []migrationbackend.AppliedMigration{{App: parent.App, Name: parent.Name}}}
				backend := &lifecycleDecisionBackend{trace: trace, session: session, capabilities: lifecycleAllRelationCapabilities()}
				result, err := lifecycleExecuteDecisionPath(context.Background(), backend, test.step, trace)
				if result != (lifecycleMixedResult{}) || !errors.Is(err, lifecycleRelationErrIntent) {
					t.Fatalf("forged sealed handoff = result:%+v error:%v", result, err)
				}
				metrics, events := trace.snapshot()
				if metrics != (lifecyclePreflightMetrics{StaticPreSession: 1}) || backend.capabilityCalls != 0 ||
					backend.openCalls != 0 || session.readCalls != 0 || session.relationBeginCalls != 0 ||
					session.legacyBeginCalls != 0 || session.closeCalls != 0 || session.lastTransaction != nil ||
					!reflect.DeepEqual(events, []string{"static_preflight"}) {
					t.Fatalf("forged provenance reached I/O: metrics=%+v events=%#v capability=%d open=%d read=%d begin=%d",
						metrics, events, backend.capabilityCalls, backend.openCalls, session.readCalls, session.relationBeginCalls)
				}
			})
		}

		t.Run("child is not current with empty history", func(t *testing.T) {
			emptyInput := lifecycleTestParentChildPreflightInput(
				t, parent, alternateParent, child, baseIntent, "empty_history",
			)
			emptySnapshot, emptyMetrics, err := preflightValidate(emptyInput)
			if err != nil || emptyMetrics != (preflightIOMetrics{}) {
				t.Fatalf("empty-history graph preflight = metrics:%+v error:%v", emptyMetrics, err)
			}
			steps := emptySnapshot.preflightSteps()
			var forged *preflightPreparedStep
			for index := range steps {
				if steps[index].Key == (preflightMigrationKey{App: child.App, Name: child.Name}) {
					forged = &steps[index]
					break
				}
			}
			if forged == nil || forged.plan == nil {
				t.Fatal("empty-history diagnostic child step missing")
			}
			emptyPlan := lifecycleClonePreparedPlan(*forged.plan)
			if err := lifecycleValidateSealedPlan(emptyPlan); err != nil {
				t.Fatalf("empty-history parent-first product plan rejected: %v", err)
			}
			// A caller can forge its diagnostic clone into a parentless singleton,
			// but the snapshot never accepts it back or reseals it as authority.
			forged.Dependencies = nil
			forged.plan.definitions = []migrations.Migration{{App: child.App, Name: child.Name}}
			forged.plan.applied = []migrations.MigrationKey{}
			forged.plan.targets = []preflightPlanTarget{
				preflightNamedPlanTarget(preflightMigrationKey{App: child.App, Name: child.Name}),
			}
			forged.plan.expected = migrations.PlanStep{Key: child.Key(), Direction: migrations.DirectionForward}
			blocked, exists := emptySnapshot.preflightHandoff(preflightMigrationKey{App: child.App, Name: child.Name})
			if exists || !reflect.DeepEqual(blocked, lifecyclePreparedRelationStep{}) {
				t.Fatalf("forged singleton child escaped snapshot = handoff:%#v exists:%t", blocked, exists)
			}
			trace := &lifecycleDecisionTrace{}
			session := &lifecycleTraceSession{}
			backend := &lifecycleDecisionBackend{trace: trace, session: session, capabilities: lifecycleAllRelationCapabilities()}
			result, err := lifecycleExecuteDecisionPath(context.Background(), backend, blocked, trace)
			blockedMetrics, blockedEvents := trace.snapshot()
			if result != (lifecycleMixedResult{}) || !errors.Is(err, lifecycleRelationErrIntent) ||
				blockedMetrics != (lifecyclePreflightMetrics{StaticPreSession: 1}) ||
				!reflect.DeepEqual(blockedEvents, []string{"static_preflight"}) ||
				backend.capabilityCalls != 0 || backend.openCalls != 0 || session.readCalls != 0 ||
				session.relationBeginCalls != 0 || session.legacyBeginCalls != 0 || session.lastTransaction != nil {
				t.Fatalf(
					"forged singleton child touched lifecycle: result:%+v error:%v metrics:%+v events:%#v backend:%+v session:%+v",
					result, err, blockedMetrics, blockedEvents, backend, session,
				)
			}
		})

		t.Run("applied parent admits child and exact session snapshot", func(t *testing.T) {
			trace := &lifecycleDecisionTrace{}
			session := &lifecycleTraceSession{records: []migrationbackend.AppliedMigration{{App: parent.App, Name: parent.Name}}}
			backend := &lifecycleDecisionBackend{trace: trace, session: session, capabilities: lifecycleAllRelationCapabilities()}
			result, err := lifecycleExecuteDecisionPath(context.Background(), backend, base, trace)
			if err != nil || !result.Committed || session.relationBeginCalls != 1 ||
				!reflect.DeepEqual(session.records, []migrationbackend.AppliedMigration{
					{App: parent.App, Name: parent.Name}, {App: child.App, Name: child.Name},
				}) {
				t.Fatalf("parent-applied child execution = result:%+v error:%v records:%#v begin:%d", result, err, session.records, session.relationBeginCalls)
			}
		})

		t.Run("unapply planner selects child before parent", func(t *testing.T) {
			unapplyInput := lifecycleTestParentChildPreflightInput(
				t, parent, alternateParent, child, baseIntent, "unapply_child",
			)
			unapplySnapshot, unapplyMetrics, err := preflightValidate(unapplyInput)
			if err != nil || unapplyMetrics != (preflightIOMetrics{}) {
				t.Fatalf("unapply graph preflight = metrics:%+v error:%v", unapplyMetrics, err)
			}
			handoff, ok := unapplySnapshot.preflightHandoff(preflightMigrationKey{App: child.App, Name: child.Name})
			if !ok {
				t.Fatal("unapply graph did not publish child-first handoff")
			}
			trace := &lifecycleDecisionTrace{}
			session := &lifecycleTraceSession{records: []migrationbackend.AppliedMigration{
				{App: parent.App, Name: parent.Name}, {App: child.App, Name: child.Name},
			}}
			backend := &lifecycleDecisionBackend{trace: trace, session: session, capabilities: lifecycleAllRelationCapabilities()}
			result, err := lifecycleExecuteDecisionPath(context.Background(), backend, handoff, trace)
			if err != nil || !result.Committed || session.relationBeginCalls != 1 ||
				!reflect.DeepEqual(session.records, []migrationbackend.AppliedMigration{{App: parent.App, Name: parent.Name}}) {
				t.Fatalf("child-first unapply = result:%+v error:%v records:%#v begin:%d", result, err, session.records, session.relationBeginCalls)
			}
		})

		t.Run("session history divergence is rejected before begin", func(t *testing.T) {
			trace := &lifecycleDecisionTrace{}
			session := &lifecycleTraceSession{records: []migrationbackend.AppliedMigration{
				{App: parent.App, Name: parent.Name}, {App: parent.App, Name: parent.Name},
			}}
			backend := &lifecycleDecisionBackend{trace: trace, session: session, capabilities: lifecycleAllRelationCapabilities()}
			result, err := lifecycleExecuteDecisionPath(context.Background(), backend, base, trace)
			var planningError *migrations.PlanningError
			if result != (lifecycleMixedResult{}) || !errors.Is(err, lifecycleRelationErrIntent) ||
				!errors.As(err, &planningError) || planningError.Code != migrations.CodeDuplicateApplied ||
				session.relationBeginCalls != 0 || session.lastTransaction != nil {
				t.Fatalf("duplicate session history = result:%+v error:%v planning:%#v begin:%d", result, err, planningError, session.relationBeginCalls)
			}
		})
	})

	t.Run("partial begin failure and post-begin cancellation roll back with a live bounded context", func(t *testing.T) {
		intent := lifecycleNullableEditorIntent()
		transition := lifecycleRelationTransition(migrationbackend.HistoryTransitionApply)
		tests := []struct {
			name      string
			beginErr  error
			cancelCtx bool
		}{
			{name: "partial begin error", beginErr: errors.New("partial begin sentinel")},
			{name: "post begin cancellation", cancelCtx: true},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				session := &lifecycleTraceSession{beginErr: test.beginErr}
				if test.cancelCtx {
					session.cancelBegin = cancel
				}
				transaction, err := lifecycleBeginRelationFenced(ctx, session, transition, intent)
				wantCause := test.beginErr
				if test.cancelCtx {
					wantCause = context.Canceled
				}
				if transaction != nil || !errors.Is(err, wantCause) {
					t.Fatalf("begin boundary = transaction:%#v error:%v, want nil/%v", transaction, err, wantCause)
				}
				partial := session.lastTransaction
				if partial == nil {
					t.Fatal("partial begin did not publish its cleanup handle")
				}
				if partial.rollbackCalls != 1 || partial.rollbackCtx != nil || !partial.rollbackLimit || session.closeCalls != 0 {
					t.Fatalf(
						"partial begin cleanup = transaction:%#v rollback:%d ctx:%v bounded:%t close:%d",
						partial, partial.rollbackCalls, partial.rollbackCtx, partial.rollbackLimit, session.closeCalls,
					)
				}
			})
		}
	})

	t.Run("three preflight stages and mixed work share one existing transaction recorder and commit", func(t *testing.T) {
		intent := lifecyclePublishedAndEditorIntent()
		transition := lifecycleRelationTransition(migrationbackend.HistoryTransitionApply)
		key := migrations.MigrationKey{App: transition.Migration.App, Name: transition.Migration.Name}
		wantScalarModel := intent.Operations[0].Before.Clone()
		wantScalarField, err := lifecycleSingleFieldDelta(intent.Operations[0].Before, intent.Operations[0].After)
		if err != nil {
			t.Fatal(err)
		}
		stagedBeforeCommit := false
		trace := &lifecycleDecisionTrace{}
		session := &lifecycleTraceSession{records: lifecycleTestRootRecords(key)}
		backend := &lifecycleDecisionBackend{
			trace:        trace,
			session:      session,
			capabilities: lifecycleAllRelationCapabilities(),
		}
		session.beginHook = func() {
			// Begin runs only after the complete mixed sequence was cloned. Mutating
			// every caller-owned arm here must not affect the prepared transaction.
			intent.Operations[0].Before.Fields[0].Name = "mutated_model"
			intent.Operations[0].After.Fields[2].Name = "mutated_field"
			intent.Operations[1].Targets[0].SourceField.Relation.Reverse.Name = "mutated_relation"
		}
		session.preCommitHook = func(transaction *lifecycleTraceTransaction) {
			stagedBeforeCommit = transaction.recordStaged &&
				reflect.DeepEqual(transaction.stagedRecords, append(lifecycleTestRootRecords(key), transition.Migration)) &&
				reflect.DeepEqual(session.records, lifecycleTestRootRecords(key))
		}

		result, err := lifecycleExecuteDecisionPath(
			context.Background(),
			backend,
			lifecycleTestAdaptedHandoff(
				t,
				key,
				migrations.DirectionForward,
				intent,
			),
			trace,
		)
		if err != nil {
			t.Fatalf("lifecycleExecuteMixedStep() error = %v", err)
		}
		if !result.Committed || result.Outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("mixed result = %+v, want committed", result)
		}
		wantEvents := []string{"begin_relation", "scalar_add", "relation_change", "record", "commit_fenced"}
		wantEvents = append(wantEvents, "close")
		if got := session.snapshotEvents(); !reflect.DeepEqual(got, wantEvents) {
			t.Fatalf("mixed events = %#v, want %#v", got, wantEvents)
		}
		metrics, decisionEvents := trace.snapshot()
		wantDecisionEvents := []string{
			"static_preflight",
			"open_session",
			"session_snapshot",
			"history_plan_preflight",
			"relation_begin",
			"pragma_foreign_keys",
			"begin_immediate",
			"sqlite_physical_preflight",
			"revision_claim",
			"user_ddl",
			"user_ddl",
		}
		if metrics != (lifecyclePreflightMetrics{StaticPreSession: 1, HistoryPlan: 1, SQLitePhysical: 1}) ||
			!reflect.DeepEqual(decisionEvents, wantDecisionEvents) {
			t.Fatalf("preflight proof = metrics:%+v events:%#v, want exact three-stage order %#v", metrics, decisionEvents, wantDecisionEvents)
		}
		transaction := session.lastTransaction
		if backend.capabilityCalls != 1 || backend.openCalls != 1 || session.readCalls != 1 ||
			session.relationBeginCalls != 1 || session.legacyBeginCalls != 0 ||
			transaction.recorderCalls != 1 || transaction.commitCalls != 1 || transaction.rollbackCalls != 0 {
			t.Fatalf(
				"calls capability=%d open=%d snapshot=%d relation_begin=%d legacy_begin=%d record=%d commit=%d rollback=%d, want 1/1/1/1/0/1/1/0",
				backend.capabilityCalls, backend.openCalls, session.readCalls,
				session.relationBeginCalls, session.legacyBeginCalls,
				transaction.recorderCalls, transaction.commitCalls, transaction.rollbackCalls,
			)
		}
		if got, want := session.records, append(lifecycleTestRootRecords(key), transition.Migration); !reflect.DeepEqual(got, want) {
			t.Fatalf("generic fenced records = %#v, want %#v", got, want)
		}
		if !stagedBeforeCommit || !transaction.recordStaged || !transaction.stagePublished || transaction.stageDiscarded {
			t.Fatalf(
				"history staging = before_commit:%t staged:%t published:%t discarded:%t",
				stagedBeforeCommit, transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded,
			)
		}
		if !reflect.DeepEqual(transaction.lastScalarModel, wantScalarModel) ||
			!reflect.DeepEqual(transaction.lastScalarField, wantScalarField) {
			t.Fatalf("prepared scalar operation retained caller aliases: model=%+v field=%+v", transaction.lastScalarModel, transaction.lastScalarField)
		}
		if session.closeCalls != 1 {
			t.Fatalf("outer decision path Close() calls = %d, want 1", session.closeCalls)
		}
	})

	t.Run("unapply uses reverse source membership order and the same existing fence", func(t *testing.T) {
		transition := lifecycleRelationTransition(migrationbackend.HistoryTransitionUnapply)
		key := migrations.MigrationKey{App: transition.Migration.App, Name: transition.Migration.Name}
		trace := &lifecycleDecisionTrace{}
		session := &lifecycleTraceSession{records: append(lifecycleTestRootRecords(key), transition.Migration)}
		backend := &lifecycleDecisionBackend{
			trace: trace, session: session, capabilities: lifecycleAllRelationCapabilities(),
		}
		result, err := lifecycleExecuteDecisionPath(
			context.Background(),
			backend,
			lifecycleTestAdaptedHandoff(
				t,
				key,
				migrations.DirectionBackward,
				lifecyclePublishedAndEditorIntent(),
			),
			trace,
		)
		if err != nil || !result.Committed || result.Outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("unapply decision path = (%+v, %v), want committed", result, err)
		}
		wantEvents := []string{"begin_relation", "relation_change", "scalar_remove", "record", "commit_fenced", "close"}
		if got := session.snapshotEvents(); !reflect.DeepEqual(got, wantEvents) {
			t.Fatalf("unapply events = %#v, want %#v", got, wantEvents)
		}
		if !reflect.DeepEqual(session.records, lifecycleTestRootRecords(key)) || backend.capabilityCalls != 1 || backend.openCalls != 1 ||
			session.readCalls != 1 || session.relationBeginCalls != 1 || session.legacyBeginCalls != 0 ||
			session.lastTransaction.recorderCalls != 1 || session.lastTransaction.commitCalls != 1 {
			t.Fatalf(
				"unapply ownership = records:%#v capability:%d open:%d snapshot:%d relation:%d legacy:%d record:%d commit:%d",
				session.records, backend.capabilityCalls, backend.openCalls, session.readCalls,
				session.relationBeginCalls, session.legacyBeginCalls,
				session.lastTransaction.recorderCalls, session.lastTransaction.commitCalls,
			)
		}
	})

	t.Run("rolled-back and both unknown commit observations are terminal and never retried", func(t *testing.T) {
		tests := []struct {
			name           string
			durability     migrationbackend.CommitDurability
			unknownDurable bool
			wantRecords    int
		}{
			{name: "definite rollback", durability: migrationbackend.CommitRolledBack},
			{name: "unknown nondurable", durability: migrationbackend.CommitUnknown},
			{name: "unknown durable", durability: migrationbackend.CommitUnknown, unknownDurable: true, wantRecords: 1},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				cause := errors.New(test.name + " sentinel")
				intent := lifecycleNullableEditorIntent()
				session := &lifecycleTraceSession{
					outcome:        migrationbackend.CommitOutcome{Durability: test.durability},
					commitErr:      cause,
					unknownDurable: test.unknownDurable,
				}
				result, err := lifecycleExecuteMixedStep(
					context.Background(),
					session,
					lifecycleRelationTransition(migrationbackend.HistoryTransitionApply),
					intent,
				)
				if !errors.Is(err, cause) || result.Committed || result.Outcome.Durability != test.durability {
					t.Fatalf("lifecycleExecuteMixedStep() = (%+v, %v), want terminal %d", result, err, test.durability)
				}
				transaction := session.lastTransaction
				if session.relationBeginCalls != 1 || transaction.commitCalls != 1 || transaction.rollbackCalls != 0 {
					t.Fatalf(
						"terminal outcome calls begin=%d commit=%d rollback=%d, want 1/1/0",
						session.relationBeginCalls, transaction.commitCalls, transaction.rollbackCalls,
					)
				}
				if len(session.records) != test.wantRecords {
					t.Fatalf("terminal outcome durable records = %#v, want %d", session.records, test.wantRecords)
				}
				if !transaction.recordStaged || transaction.stagePublished != (test.wantRecords == 1) ||
					transaction.stageDiscarded != (test.wantRecords == 0) {
					t.Fatalf(
						"terminal staging = staged:%t published:%t discarded:%t",
						transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded,
					)
				}
				if session.closeCalls != 0 {
					t.Fatalf("terminal outcome closed outer-owned session %d times", session.closeCalls)
				}
			})
		}
	})

	t.Run("committed cleanup error remains durable and is not retried", func(t *testing.T) {
		cause := errors.New("committed cleanup sentinel")
		intent := lifecycleNullableEditorIntent()
		session := &lifecycleTraceSession{
			outcome:   migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted},
			commitErr: cause,
		}
		result, err := lifecycleExecuteMixedStep(
			context.Background(),
			session,
			lifecycleRelationTransition(migrationbackend.HistoryTransitionApply),
			intent,
		)
		if !errors.Is(err, cause) || !result.Committed || result.Outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("lifecycleExecuteMixedStep() = (%+v, %v), want durable cleanup error", result, err)
		}
		transaction := session.lastTransaction
		if session.relationBeginCalls != 1 || transaction.commitCalls != 1 || transaction.rollbackCalls != 0 {
			t.Fatalf(
				"committed cleanup calls begin=%d commit=%d rollback=%d, want 1/1/0",
				session.relationBeginCalls, transaction.commitCalls, transaction.rollbackCalls,
			)
		}
		if got, want := session.records, []migrationbackend.AppliedMigration{{App: "blog", Name: "0002_article_author"}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("durable cleanup records = %#v, want %#v", got, want)
		}
		if !transaction.recordStaged || !transaction.stagePublished || transaction.stageDiscarded || session.closeCalls != 0 {
			t.Fatalf(
				"committed cleanup staging/ownership = staged:%t published:%t discarded:%t close:%d",
				transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded, session.closeCalls,
			)
		}
	})

	t.Run("operation failure rolls back with detached bounded context", func(t *testing.T) {
		cause := errors.New("relation operation sentinel")
		ctx, cancel := context.WithCancel(context.Background())
		intent := lifecycleNullableEditorIntent()
		session := &lifecycleTraceSession{relationErr: cause, cancelRelation: cancel}
		_, err := lifecycleExecuteMixedStep(
			ctx,
			session,
			lifecycleRelationTransition(migrationbackend.HistoryTransitionApply),
			intent,
		)
		if !errors.Is(err, cause) {
			t.Fatalf("lifecycleExecuteMixedStep() error = %v, want operation cause", err)
		}
		transaction := session.lastTransaction
		if transaction.rollbackCalls != 1 || transaction.rollbackCtx != nil || !transaction.rollbackLimit {
			t.Fatalf(
				"rollback calls=%d context_err=%v bounded=%t, want 1/nil/true",
				transaction.rollbackCalls, transaction.rollbackCtx, transaction.rollbackLimit,
			)
		}
		if transaction.recorderCalls != 0 || transaction.commitCalls != 0 {
			t.Fatalf("failed operation calls record=%d commit=%d, want 0/0", transaction.recorderCalls, transaction.commitCalls)
		}
		if transaction.recordStaged || transaction.stagePublished || transaction.stageDiscarded || session.closeCalls != 0 {
			t.Fatalf(
				"failed operation staging/ownership = staged:%t published:%t discarded:%t close:%d",
				transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded, session.closeCalls,
			)
		}
	})

	t.Run("successful final operation cannot hide request cancellation before recorder", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		intent := lifecycleNullableEditorIntent()
		session := &lifecycleTraceSession{cancelRelation: cancel}
		_, err := lifecycleExecuteMixedStep(
			ctx,
			session,
			lifecycleRelationTransition(migrationbackend.HistoryTransitionApply),
			intent,
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lifecycleExecuteMixedStep() error = %v, want cancellation", err)
		}
		transaction := session.lastTransaction
		if transaction.rollbackCalls != 1 || transaction.rollbackCtx != nil || !transaction.rollbackLimit ||
			transaction.recorderCalls != 0 || transaction.commitCalls != 0 {
			t.Fatalf(
				"post-operation cancellation calls rollback=%d record=%d commit=%d context_err=%v bounded=%t",
				transaction.rollbackCalls, transaction.recorderCalls, transaction.commitCalls,
				transaction.rollbackCtx, transaction.rollbackLimit,
			)
		}
		if transaction.recordStaged || transaction.stagePublished || transaction.stageDiscarded || session.closeCalls != 0 {
			t.Fatalf(
				"post-operation cancellation staging/ownership = staged:%t published:%t discarded:%t close:%d",
				transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded, session.closeCalls,
			)
		}
	})

	t.Run("successful recorder cannot hide request cancellation before commit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		intent := lifecycleNullableEditorIntent()
		session := &lifecycleTraceSession{cancelRecorder: cancel}
		_, err := lifecycleExecuteMixedStep(
			ctx,
			session,
			lifecycleRelationTransition(migrationbackend.HistoryTransitionApply),
			intent,
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lifecycleExecuteMixedStep() error = %v, want cancellation", err)
		}
		transaction := session.lastTransaction
		if transaction.rollbackCalls != 1 || transaction.rollbackCtx != nil || !transaction.rollbackLimit ||
			transaction.recorderCalls != 1 || transaction.commitCalls != 0 {
			t.Fatalf(
				"post-recorder cancellation calls rollback=%d record=%d commit=%d context_err=%v bounded=%t",
				transaction.rollbackCalls, transaction.recorderCalls, transaction.commitCalls,
				transaction.rollbackCtx, transaction.rollbackLimit,
			)
		}
		if !transaction.recordStaged || transaction.stagePublished || !transaction.stageDiscarded ||
			len(session.records) != 0 || session.closeCalls != 0 {
			t.Fatalf(
				"post-recorder cancellation staging/ownership = staged:%t published:%t discarded:%t records:%#v close:%d",
				transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded,
				session.records, session.closeCalls,
			)
		}
	})

	t.Run("invalid and over-budget intents fail before either begin path", func(t *testing.T) {
		baseIntent := lifecycleNullableEditorIntent()
		baseTransition := lifecycleRelationTransition(migrationbackend.HistoryTransitionApply)
		invalidKind := lifecycleCloneRelationIntent(baseIntent)
		invalidKind.Operations[0].Kind = RelationMigrationOperationKind(99)
		missingTarget := lifecycleCloneRelationIntent(baseIntent)
		missingTarget.Operations[0].Targets = nil
		duplicateIndex := lifecyclePublishedAndEditorIntent()
		duplicateIndex.Operations[1].OperationIndex = duplicateIndex.Operations[0].OperationIndex
		oversized := lifecyclePublishedAndEditorIntent()
		oversized.Operations[0].After.Fields[2].Default = &ir.ScalarDefault{
			Kind: ir.ScalarString, String: strings.Repeat("x", migrationdefinition.MaxDocumentBytes+1),
		}
		aggregate := lifecyclePublishedAndEditorIntent()
		aggregateDefault := strings.Repeat("x", migrationdefinition.MaxDocumentBytes/2+1)
		for _, field := range []*ir.Field{
			&aggregate.Operations[0].After.Fields[2],
			&aggregate.Operations[1].Before.Fields[2],
			&aggregate.Operations[1].After.Fields[2],
		} {
			field.Kind = ir.FieldChar
			field.MaxLength = len(aggregateDefault)
			field.Default = &ir.ScalarDefault{Kind: ir.ScalarString, String: aggregateDefault}
		}
		reordered := lifecyclePublishedAndEditorIntent()
		reordered.Operations[0], reordered.Operations[1] = reordered.Operations[1], reordered.Operations[0]
		discontinuous := lifecyclePublishedAndEditorIntent()
		discontinuous.Operations[1].Before.Fields[1].MaxLength++
		discontinuous.Operations[1].After.Fields[1].MaxLength++
		wrongUnapplyOrder := lifecyclePublishedAndEditorIntent()
		oversizedSourceField := lifecycleNullableEditorIntent()
		oversizedSourceField.Operations[0].Targets[0].SourceField.Default = &ir.ScalarDefault{
			Kind: ir.ScalarString, String: strings.Repeat("s", migrationdefinition.MaxDocumentBytes+1),
		}
		oversizedTargetModel := lifecycleNullableEditorIntent()
		oversizedTargetModel.Operations[0].Targets[0].TargetModel.Fields = make(
			[]ir.Field,
			migrationdefinition.MaxFieldsPerCreateModel+1,
		)
		oversizedTargets := lifecycleNullableEditorIntent()
		oversizedTargets.Operations[0].Targets = make(
			[]RelationMigrationTarget,
			migrationdefinition.MaxFieldsPerCreateModel+1,
		)
		charTargetKey := lifecycleNullableEditorIntent()
		charTargetKey.Operations[0].Targets[0].TargetKey.Kind = ir.FieldChar
		charTargetKey.Operations[0].Targets[0].TargetKey.MaxLength = 32
		charTargetKey.Operations[0].Targets[0].TargetModel.Fields[0] = charTargetKey.Operations[0].Targets[0].TargetKey.Clone()
		nullableTargetKey := lifecycleNullableEditorIntent()
		nullableTargetKey.Operations[0].Targets[0].TargetKey.Nullable = true
		nullableTargetKey.Operations[0].Targets[0].TargetModel.Fields[0] = nullableTargetKey.Operations[0].Targets[0].TargetKey.Clone()
		aggregateStructural := RelationMigrationIntent{
			Operations: make([]RelationMigrationOperation, 256),
		}
		sharedDefault := &ir.ScalarDefault{Kind: ir.ScalarString}
		sharedRelation := &ir.ForeignKeyRelation{}
		sharedFields := make([]ir.Field, 512)
		for index := range sharedFields {
			sharedFields[index].Default = sharedDefault
			sharedFields[index].Relation = sharedRelation
		}
		for index := range aggregateStructural.Operations {
			aggregateStructural.Operations[index] = RelationMigrationOperation{
				OperationIndex: index,
				Kind:           RelationMigrationAddField,
				Before:         ir.Model{Name: "article", Fields: sharedFields},
				After:          ir.Model{Name: "article", Fields: sharedFields},
			}
		}
		tests := []struct {
			name       string
			intent     RelationMigrationIntent
			transition migrationbackend.HistoryTransition
		}{
			{name: "empty", intent: RelationMigrationIntent{}, transition: baseTransition},
			{name: "invalid kind", intent: invalidKind, transition: baseTransition},
			{name: "missing target", intent: missingTarget, transition: baseTransition},
			{name: "duplicate operation index", intent: duplicateIndex, transition: baseTransition},
			{name: "reordered apply membership", intent: reordered, transition: baseTransition},
			{name: "discontinuous same-model before state", intent: discontinuous, transition: baseTransition},
			{name: "forward order supplied to unapply", intent: wrongUnapplyOrder, transition: lifecycleRelationTransition(migrationbackend.HistoryTransitionUnapply)},
			{name: "oversized scalar string", intent: oversized, transition: baseTransition},
			{name: "aggregate scalar bytes", intent: aggregate, transition: baseTransition},
			{name: "oversized target source field", intent: oversizedSourceField, transition: baseTransition},
			{name: "oversized target model fields", intent: oversizedTargetModel, transition: baseTransition},
			{name: "oversized target count", intent: oversizedTargets, transition: baseTransition},
			{name: "aggregate pointer-aware structural nodes", intent: aggregateStructural, transition: baseTransition},
			{name: "forged Char target key", intent: charTargetKey, transition: baseTransition},
			{name: "forged nullable target key", intent: nullableTargetKey, transition: baseTransition},
			{name: "empty transition identity", intent: baseIntent, transition: migrationbackend.HistoryTransition{Kind: migrationbackend.HistoryTransitionApply}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				trace := &lifecycleDecisionTrace{}
				session := &lifecycleTraceSession{}
				backend := &lifecycleDecisionBackend{
					trace: trace, session: session, capabilities: lifecycleAllRelationCapabilities(),
				}
				result, err := lifecycleExecuteDecisionPath(
					context.Background(),
					backend,
					lifecycleTestForgedHandoff(test.transition, test.intent),
					trace,
				)
				if !errors.Is(err, lifecycleRelationErrIntent) || result != (lifecycleMixedResult{}) {
					t.Fatalf("invalid mixed sequence = result:%+v error:%v", result, err)
				}
				if backend.capabilityCalls != 0 || backend.openCalls != 0 || session.readCalls != 0 ||
					session.relationBeginCalls != 0 || session.legacyBeginCalls != 0 || session.closeCalls != 0 ||
					session.lastTransaction != nil || len(session.snapshotEvents()) != 0 {
					t.Fatalf(
						"invalid mixed sequence touched lifecycle: capability=%d open=%d snapshot=%d relation=%d legacy=%d close=%d transaction=%#v events=%#v",
						backend.capabilityCalls, backend.openCalls, session.readCalls,
						session.relationBeginCalls, session.legacyBeginCalls, session.closeCalls,
						session.lastTransaction, session.snapshotEvents(),
					)
				}
			})
		}
	})

	t.Run("cancellation after every pure stage gates the next I O boundary", func(t *testing.T) {
		tests := []struct {
			name           string
			stage          string
			wantCapability int
			wantOpen       int
			wantSnapshot   int
			wantHistory    int
			wantClose      int
		}{
			{name: "after complete pre-session preparation", stage: "static"},
			{name: "after capability selection", stage: "capability", wantCapability: 1},
			{name: "after session open", stage: "open", wantCapability: 1, wantOpen: 1, wantClose: 1},
			{name: "after history snapshot", stage: "snapshot", wantCapability: 1, wantOpen: 1, wantSnapshot: 1, wantClose: 1},
			{name: "after history plan validation", stage: "history", wantCapability: 1, wantOpen: 1, wantSnapshot: 1, wantHistory: 1, wantClose: 1},
			{name: "after begin intent snapshot", stage: "begin preparation", wantCapability: 1, wantOpen: 1, wantSnapshot: 1, wantHistory: 1, wantClose: 1},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				trace := &lifecycleDecisionTrace{}
				key := migrations.MigrationKey{App: "blog", Name: "0002_article_author"}
				session := &lifecycleTraceSession{records: lifecycleTestRootRecords(key)}
				backend := &lifecycleDecisionBackend{
					trace: trace, session: session, capabilities: lifecycleAllRelationCapabilities(),
				}
				switch test.stage {
				case "static":
					trace.cancelStatic = cancel
				case "capability":
					backend.cancelCapabilities = cancel
				case "open":
					backend.cancelOpen = cancel
				case "snapshot":
					session.cancelSnapshot = cancel
				case "history":
					trace.cancelHistoryPlan = cancel
				case "begin preparation":
					trace.cancelBeginPreparation = cancel
				default:
					t.Fatalf("unknown cancellation stage %q", test.stage)
				}

				result, err := lifecycleExecuteDecisionPath(
					ctx,
					backend,
					lifecycleTestAdaptedHandoff(
						t,
						key,
						migrations.DirectionForward,
						lifecycleNullableEditorIntent(),
					),
					trace,
				)
				if result != (lifecycleMixedResult{}) || !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled decision path = (%+v, %v), want zero/context.Canceled", result, err)
				}
				metrics, _ := trace.snapshot()
				if metrics != (lifecyclePreflightMetrics{StaticPreSession: 1, HistoryPlan: test.wantHistory}) {
					t.Fatalf("canceled metrics = %+v, want static=1 history=%d physical=0", metrics, test.wantHistory)
				}
				if backend.capabilityCalls != test.wantCapability || backend.openCalls != test.wantOpen ||
					session.readCalls != test.wantSnapshot || session.relationBeginCalls != 0 ||
					session.legacyBeginCalls != 0 || session.closeCalls != test.wantClose ||
					session.lastTransaction != nil {
					t.Fatalf(
						"canceled calls capability=%d open=%d snapshot=%d relation=%d legacy=%d close=%d transaction=%#v, want %d/%d/%d/0/0/%d/nil",
						backend.capabilityCalls, backend.openCalls, session.readCalls,
						session.relationBeginCalls, session.legacyBeginCalls, session.closeCalls,
						session.lastTransaction, test.wantCapability, test.wantOpen,
						test.wantSnapshot, test.wantClose,
					)
				}
				if test.wantClose == 1 && (session.closeCtx != nil || !session.closeLimit) {
					t.Fatalf("canceled Close context = err:%v bounded:%t, want live/bounded", session.closeCtx, session.closeLimit)
				}
			})
		}
	})

	t.Run("legacy Apply Unapply and ExecutePlan reject relation input before BeginMigration", func(t *testing.T) {
		initial := lifecycleHistoryContinuityDefinitions()[0]
		reconstructor, err := migrations.NewStateReconstructor(initial)
		if err != nil {
			t.Fatal(err)
		}
		before, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
		if err != nil {
			t.Fatal(err)
		}
		relationMigration := lifecycleUnsupportedRelationDefinition()
		tests := []struct {
			name string
			run  func(migrations.Executor) error
		}{
			{
				name: "Apply",
				run: func(executor migrations.Executor) error {
					_, err := executor.Apply(context.Background(), before, relationMigration)
					return err
				},
			},
			{
				name: "Unapply",
				run: func(executor migrations.Executor) error {
					_, err := executor.Unapply(context.Background(), before, relationMigration)
					return err
				},
			},
			{
				name: "ExecutePlan",
				run: func(executor migrations.Executor) error {
					_, err := executor.ExecutePlan(
						context.Background(), before, []migrations.Migration{relationMigration},
						[]migrations.PlanStep{{Key: relationMigration.Key(), Direction: migrations.DirectionForward}},
					)
					return err
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				backend := &lifecycleLegacyCountingBackend{}
				err := test.run(migrations.Executor{Backend: backend})
				if err == nil {
					t.Fatalf("%s relation input unexpectedly succeeded", test.name)
				}
				if backend.beginCalls != 0 {
					t.Fatalf("%s BeginMigration calls = %d, want 0", test.name, backend.beginCalls)
				}
			})
		}
	})
}

func TestLifecycleIntegrationCandidateRealSQLiteHistoryContinuityAndBlocker(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lifecycle-integration.sqlite3")
	dsn := "file:" + path + "?mode=rwc"
	definitions := lifecycleHistoryContinuityDefinitions()
	initialOnly := definitions[:1]

	backend := lifecycleOpenSQLiteBackend(t, ctx, dsn)
	executor := migrations.Executor{Backend: backend}
	state, err := executor.Migrate(ctx, initialOnly, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Migrate(0001) error = %v", err)
	}
	if _, exists := state.Model("blog", "author"); !exists {
		t.Fatal("Migrate(0001) state lacks blog.author")
	}
	if _, exists := state.Model("blog", "article"); !exists {
		t.Fatal("Migrate(0001) state lacks blog.article")
	}
	if metadata := lifecycleReadRevisionMetadata(t, dsn); metadata.Revision != 1 {
		t.Fatalf("revision after 0001 = %d, want 1", metadata.Revision)
	}
	if _, ok := any(backend).(RelationRevisionFencedBackend); ok {
		t.Fatal("actual SQLite backend unexpectedly implements the decision-only relation lifecycle port")
	}

	// The real session implements the existing fenced contract, but its
	// unexported concrete transaction exposes neither the pinned connection nor
	// the optional relation port. Capability selection must stop before calling
	// legacy BeginFencedMigration; a wrapper cannot safely bolt DDL onto it.
	realSession, err := backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(blocker): %v", err)
	}
	records, err := realSession.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("ReadAppliedMigrations(blocker): %v", err)
	}
	if want := []migrationbackend.AppliedMigration{{App: "blog", Name: "0001_initial"}}; !reflect.DeepEqual(records, want) {
		t.Fatalf("history before relation blocker = %#v, want %#v", records, want)
	}
	if _, ok := realSession.(RelationRevisionFencedSession); ok {
		t.Fatal("actual SQLite session unexpectedly implements the decision-only relation begin port")
	}
	intent := lifecycleNullableEditorIntent()
	_, err = lifecycleBeginRelationFenced(
		ctx,
		realSession,
		lifecycleRelationTransition(migrationbackend.HistoryTransitionApply),
		intent,
	)
	if !errors.Is(err, lifecycleRelationErrCapability) {
		t.Fatalf("real SQLite relation begin error = %v, want explicit capability blocker", err)
	}
	if err := realSession.Close(ctx); err != nil {
		t.Fatalf("Close(blocker session): %v", err)
	}
	if metadata := lifecycleReadRevisionMetadata(t, dsn); metadata.Revision != 1 {
		t.Fatalf("capability rejection advanced revision to %d", metadata.Revision)
	}

	// Current StateReconstructor rejects the actual relation-bearing operation.
	// The empty 0002 definition below is therefore intentionally only a history
	// continuity stand-in; it is not evidence that relation state or DDL works.
	relationDefinition := lifecycleUnsupportedRelationDefinition()
	if _, err := migrations.NewStateReconstructor(initialOnly[0], relationDefinition); err == nil {
		t.Fatal("StateReconstructor unexpectedly accepted relation-bearing 0002")
	} else {
		var migrationErr *migrations.Error
		if !errors.As(err, &migrationErr) || migrationErr.Category != migrations.CategoryState ||
			migrationErr.Code != migrations.CodeInvalidState || migrationErr.Direction != migrations.DirectionForward ||
			migrationErr.App != "blog" || migrationErr.Migration != "0002_article_author" ||
			migrationErr.OperationIndex != 0 || migrationErr.Operation != "AddField" || migrationErr.Cause == nil ||
			!strings.Contains(migrationErr.Cause.Error(), "Schema IR v2 migration state cannot represent relation-bearing field") {
			t.Fatalf("StateReconstructor blocker error = %#v (%v), want exact relation-bearing state error", migrationErr, err)
		}
	}

	state, err = executor.Migrate(ctx, definitions, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Migrate(history-only 0002) error = %v", err)
	}
	if _, exists := state.Model("blog", "article"); !exists {
		t.Fatal("history-only 0002 lost reconstructed 0001 state")
	}
	metadataAtTwo := lifecycleReadRevisionMetadata(t, dsn)
	if metadataAtTwo.Revision != 2 || len(metadataAtTwo.Epoch) != 16 || len(metadataAtTwo.Fingerprint) != 32 {
		t.Fatalf("metadata after 0002 = %+v, want revision 2 and 16/32-byte fence", metadataAtTwo)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close(first backend): %v", err)
	}

	reopened := lifecycleOpenSQLiteBackend(t, ctx, dsn)
	reopenedExecutor := migrations.Executor{Backend: reopened}
	staleSession, err := reopened.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(reopened): %v", err)
	}
	reopenedRecords, err := staleSession.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("ReadAppliedMigrations(reopened): %v", err)
	}
	wantBoth := []migrationbackend.AppliedMigration{
		{App: "blog", Name: "0001_initial"},
		{App: "blog", Name: "0002_article_author"},
	}
	if !reflect.DeepEqual(reopenedRecords, wantBoth) {
		t.Fatalf("reopened history = %#v, want %#v", reopenedRecords, wantBoth)
	}

	reconstructor, err := migrations.NewStateReconstructor(definitions...)
	if err != nil {
		t.Fatalf("NewStateReconstructor(history continuity): %v", err)
	}
	applied, err := migrations.NewAppliedState(
		migrations.MigrationKey{App: "blog", Name: "0001_initial"},
		migrations.MigrationKey{App: "blog", Name: "0002_article_author"},
	)
	if err != nil {
		t.Fatalf("NewAppliedState(): %v", err)
	}
	reconstructed, err := reconstructor.Reconstruct(migrations.AppliedStateRequest(applied))
	if err != nil {
		t.Fatalf("Reconstruct(reopened history): %v", err)
	}
	if !reconstructed.Equal(state) {
		t.Fatalf("reopened reconstructed state differs from pre-close state")
	}
	article, exists := reconstructed.Model("blog", "article")
	if !exists {
		t.Fatal("candidate-local history restart lost blog.article")
	}
	for _, field := range article.Fields {
		if field.Name == "editor" || field.Relation != nil {
			t.Fatalf("candidate-local history restart unexpectedly reconstructed relation field %+v", field)
		}
	}
	// This restart path replays an empty history identity through the actual
	// reconstructor. Its deliberate lack of relation state makes it blocker
	// evidence only; it is not relation-capable restart evidence.

	state, err = reopenedExecutor.Migrate(
		ctx,
		definitions,
		migrations.TargetedLifecycleRequest(
			migrations.NamedTarget(migrations.MigrationKey{App: "blog", Name: "0001_initial"}),
		),
	)
	if err != nil {
		t.Fatalf("Migrate(unapply 0002) error = %v", err)
	}
	if _, exists := state.Model("blog", "article"); !exists {
		t.Fatal("unapply 0002 did not preserve 0001 state")
	}
	metadataAtThree := lifecycleReadRevisionMetadata(t, dsn)
	if metadataAtThree.Revision != 3 || !bytes.Equal(metadataAtThree.Epoch, metadataAtTwo.Epoch) ||
		bytes.Equal(metadataAtThree.Fingerprint, metadataAtTwo.Fingerprint) {
		t.Fatalf("metadata after unapply = %+v, want revision 3, same epoch, changed fingerprint", metadataAtThree)
	}

	// staleSession still owns the revision-2 snapshot. The real backend must
	// reject its claim after revision 3 became durable.
	_, staleErr := staleSession.BeginFencedMigration(
		ctx,
		migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "blog", Name: "0003_stale_probe"},
			Kind:      migrationbackend.HistoryTransitionApply,
		},
	)
	var fenceErr *migrationbackend.RevisionFenceError
	if !errors.As(staleErr, &fenceErr) || fenceErr == nil || fenceErr.Kind != migrationbackend.RevisionFenceFailureStale {
		t.Fatalf("stale BeginFencedMigration() error = %#v, want stale fence", staleErr)
	}
	if err := staleSession.Close(ctx); err != nil {
		t.Fatalf("Close(stale session): %v", err)
	}

	remaining := lifecycleReadApplied(t, ctx, reopened)
	wantInitial := []migrationbackend.AppliedMigration{{App: "blog", Name: "0001_initial"}}
	if !reflect.DeepEqual(remaining, wantInitial) {
		t.Fatalf("history after unapply = %#v, want %#v", remaining, wantInitial)
	}

	state, err = reopenedExecutor.Migrate(ctx, definitions, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Migrate(reapply history-only 0002) error = %v", err)
	}
	if _, exists := state.Model("blog", "article"); !exists {
		t.Fatal("reapply 0002 lost 0001 state")
	}
	metadataAtFour := lifecycleReadRevisionMetadata(t, dsn)
	if metadataAtFour.Revision != 4 || !bytes.Equal(metadataAtFour.Epoch, metadataAtTwo.Epoch) ||
		!bytes.Equal(metadataAtFour.Fingerprint, metadataAtTwo.Fingerprint) {
		t.Fatalf("metadata after reapply = %+v, want revision 4 and restored history fingerprint", metadataAtFour)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close(second backend): %v", err)
	}

	finalBackend := lifecycleOpenSQLiteBackend(t, ctx, dsn)
	if got := lifecycleReadApplied(t, ctx, finalBackend); !reflect.DeepEqual(got, wantBoth) {
		t.Fatalf("final reopened history = %#v, want %#v", got, wantBoth)
	}
	if lifecycleSQLiteColumnExists(t, dsn, "article", "editor_id") {
		t.Fatal("history-only 0002 unexpectedly created relation column")
	}
	if count := lifecycleSQLiteForeignKeyCount(t, dsn, "article"); count != 0 {
		t.Fatalf("history-only article foreign keys = %d, want 0 (physical relation integration remains blocked)", count)
	}
}

// lifecycleTestAdaptedHandoff routes every valid decision fixture through a
// complete preflight snapshot. The snapshot seals its opaque handoff before
// any prepared-step clone is observable; tests cannot supply a prepared step
// back to an adapter or invent a singleton planning request.
func lifecycleTestAdaptedHandoff(
	t *testing.T,
	key migrations.MigrationKey,
	direction migrations.Direction,
	forward RelationMigrationIntent,
) lifecyclePreparedRelationStep {
	t.Helper()
	input, _ := lifecycleTestPreflightInput(t, key, direction, forward)
	snapshot, metrics, err := preflightValidate(input)
	if err != nil || metrics != (preflightIOMetrics{}) {
		t.Fatalf("valid lifecycle preflight = metrics:%+v error:%v", metrics, err)
	}
	handoff, ok := snapshot.preflightHandoff(preflightMigrationKey{App: key.App, Name: key.Name})
	if !ok {
		t.Fatalf("valid lifecycle preflight did not publish relation handoff for %s.%s", key.App, key.Name)
	}
	return handoff
}

func lifecycleTestPreflightInput(
	t *testing.T,
	key migrations.MigrationKey,
	direction migrations.Direction,
	forward RelationMigrationIntent,
) (preflightInput, migrations.MigrationKey) {
	t.Helper()
	if len(forward.Operations) == 0 {
		t.Fatal("lifecycle preflight fixture requires an explicit forward operation sequence")
	}
	root := migrations.MigrationKey{App: key.App, Name: "0001_lifecycle_fixture"}
	if root == key {
		t.Fatal("lifecycle preflight fixture root collides with current migration")
	}
	sourceInitial := forward.Operations[0].Before.Clone()
	sourceFinal := forward.Operations[len(forward.Operations)-1].After.Clone()
	var targetModel ir.Model
	for _, operation := range forward.Operations {
		if len(operation.Targets) == 0 {
			continue
		}
		if len(operation.Targets) != 1 {
			t.Fatalf("lifecycle preflight fixture target count = %d, want 1", len(operation.Targets))
		}
		candidate := operation.Targets[0]
		if candidate.SourceField.Relation == nil || candidate.SourceField.Relation.Target.AppLabel != key.App {
			t.Fatalf("lifecycle preflight fixture requires an exact same-app relation target: %+v", candidate.SourceField)
		}
		if preflightModelIsZero(targetModel) {
			targetModel = candidate.TargetModel.Clone()
		} else if !reflect.DeepEqual(targetModel, candidate.TargetModel) {
			t.Fatal("lifecycle preflight fixture has more than one target model")
		}
	}
	if preflightModelIsZero(targetModel) {
		t.Fatal("lifecycle preflight fixture requires a relation-bearing intent")
	}

	rootState := lifecycleTestProjectState(t, key.App, sourceInitial, targetModel)
	finalState := lifecycleTestProjectState(t, key.App, sourceFinal, targetModel)
	current := preflightMigrationKey{App: key.App, Name: key.Name}
	rootKey := preflightMigrationKey{App: root.App, Name: root.Name}
	input := preflightInput{
		State:      finalState,
		PlanStart:  rootState,
		PlanTarget: finalState.stateClone(),
		PlanApplied: []migrations.MigrationKey{
			root,
		},
		PlanTargets: []preflightPlanTarget{preflightNamedPlanTarget(current)},
		Definitions: []preflightDefinition{
			{
				Key: rootKey,
				Operations: []preflightOperation{
					{
						Kind:       preflightCreateModel,
						Model:      stateModelIdentity{App: key.App, Model: targetModel.Name},
						ModelState: targetModel.Clone(),
					},
					{
						Kind:       preflightCreateModel,
						Model:      stateModelIdentity{App: key.App, Model: sourceInitial.Name},
						ModelState: sourceInitial.Clone(),
					},
				},
			},
			{
				Key:          current,
				Dependencies: []preflightMigrationKey{rootKey},
				Operations:   lifecycleTestPreflightOperations(t, key.App, forward),
			},
		},
		Capability: preflightCapabilityDescriptor{RelationEditor: true},
	}
	if direction == migrations.DirectionBackward {
		input.PlanStart = finalState.stateClone()
		input.PlanTarget = rootState.stateClone()
		input.PlanApplied = []migrations.MigrationKey{root, key}
		input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(rootKey)}
	} else if direction != migrations.DirectionForward {
		t.Fatalf("lifecycle preflight fixture direction = %v", direction)
	}
	return input, root
}

func lifecycleTestParentChildPreflightInput(
	t *testing.T,
	parent migrations.Migration,
	alternateParent migrations.Migration,
	child migrations.Migration,
	forward RelationMigrationIntent,
	mode string,
) preflightInput {
	t.Helper()
	input, _ := lifecycleTestPreflightInput(t, child.Key(), migrations.DirectionForward, forward)
	rootState := input.PlanStart.stateClone()
	parentKey := preflightMigrationKey{App: parent.App, Name: parent.Name}
	childKey := preflightMigrationKey{App: child.App, Name: child.Name}
	rootDefinition := preflightCloneDefinition(input.Definitions[0])
	rootDefinition.Key = parentKey
	childDefinition := preflightCloneDefinition(input.Definitions[1])
	childDefinition.Key = childKey
	childDefinition.Dependencies = []preflightMigrationKey{parentKey}
	input.Definitions = []preflightDefinition{
		rootDefinition,
		{Key: preflightMigrationKey{App: alternateParent.App, Name: alternateParent.Name}},
		childDefinition,
	}
	input.PlanApplied = []migrations.MigrationKey{parent.Key()}
	input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(childKey)}

	switch mode {
	case "parent_applied":
		return input
	case "empty_history":
		empty, err := stateNewProject(stateFormatRelation)
		if err != nil {
			t.Fatalf("empty lifecycle graph state: %v", err)
		}
		input.PlanStart = empty
		input.PlanTarget = input.State.stateClone()
		input.PlanApplied = []migrations.MigrationKey{}
		return input
	case "unapply_child":
		input.PlanStart = input.State.stateClone()
		input.PlanTarget = rootState
		input.PlanApplied = []migrations.MigrationKey{parent.Key(), child.Key()}
		input.PlanTargets = []preflightPlanTarget{preflightNamedPlanTarget(parentKey)}
		return input
	default:
		t.Fatalf("unknown lifecycle parent-child fixture mode %q", mode)
		return preflightInput{}
	}
}

func lifecycleTestPreflightOperations(
	t *testing.T,
	app string,
	forward RelationMigrationIntent,
) []preflightOperation {
	t.Helper()
	operations := make([]preflightOperation, len(forward.Operations))
	for index, operation := range forward.Operations {
		candidate := preflightOperation{
			Before: operation.Before.Clone(),
			After:  operation.After.Clone(),
		}
		source := stateModelIdentity{App: app, Model: operation.Before.Name}
		switch operation.Kind {
		case RelationMigrationAddField:
			field, err := lifecycleSingleFieldDelta(operation.Before, operation.After)
			if err != nil {
				t.Fatalf("lifecycle preflight add-field delta: %v", err)
			}
			candidate.Kind = preflightAddScalar
			if len(operation.Targets) != 0 {
				if len(operation.Targets) != 1 || !reflect.DeepEqual(operation.Targets[0].SourceField, field) || field.Relation == nil {
					t.Fatalf("lifecycle preflight relation target does not match exact added field: %+v", operation)
				}
				candidate.Kind = preflightAddRelation
				candidate.Relation = preflightDeclarationFromField(source, operation.After, field)
			}
		case RelationMigrationRemoveField:
			if len(operation.Targets) != 1 || operation.Targets[0].SourceField.Relation == nil {
				t.Fatalf("lifecycle preflight remove-field target is not exact: %+v", operation)
			}
			candidate.Kind = preflightRemoveRelation
			candidate.Relation = preflightDeclarationFromField(source, operation.Before, operation.Targets[0].SourceField)
		default:
			t.Fatalf("lifecycle preflight fixture operation kind = %q", operation.Kind)
		}
		operations[index] = candidate
	}
	return operations
}

func lifecycleTestProjectState(t *testing.T, app string, models ...ir.Model) stateProjectState {
	t.Helper()
	cloned := make([]ir.Model, len(models))
	for index := range models {
		cloned[index] = models[index].Clone()
	}
	sort.Slice(cloned, func(left, right int) bool { return cloned[left].Name < cloned[right].Name })
	state, err := stateNewProject(stateFormatRelation, ir.Schema{
		FormatVersion: ir.RelationFormatVersion,
		AppLabel:      app,
		Models:        cloned,
	})
	if err != nil {
		t.Fatalf("lifecycle preflight fixture state: %v", err)
	}
	return state
}

func lifecycleTestRootRecords(key migrations.MigrationKey) []migrationbackend.AppliedMigration {
	return []migrationbackend.AppliedMigration{{App: key.App, Name: "0001_lifecycle_fixture"}}
}

// lifecycleTestForgedHandoff exists only to inject malformed defensive inputs
// that the real prepared-step adapter correctly refuses to publish.
func lifecycleTestForgedHandoff(
	transition migrationbackend.HistoryTransition,
	intent RelationMigrationIntent,
) lifecyclePreparedRelationStep {
	direction := migrations.DirectionForward
	if transition.Kind == migrationbackend.HistoryTransitionUnapply {
		direction = migrations.DirectionBackward
	}
	clonedIntent := lifecycleCloneRelationIntent(intent)
	key := migrations.MigrationKey{App: transition.Migration.App, Name: transition.Migration.Name}
	plan := lifecycleTestForgedSingletonPlan(key, direction)
	return lifecyclePreparedRelationStep{
		transition: transition,
		intent:     clonedIntent,
		plan:       lifecycleClonePreparedPlan(plan),
		binding: &lifecyclePreparedRelationBinding{
			key:        key,
			direction:  direction,
			transition: transition,
			intent:     lifecycleCloneRelationIntent(intent),
			plan:       lifecycleClonePreparedPlan(plan),
		},
	}
}

func lifecycleTestForgedSingletonPlan(
	key migrations.MigrationKey,
	direction migrations.Direction,
) lifecyclePreparedPlan {
	plan := lifecyclePreparedPlan{
		definitions: lifecycleCloneHistoryGraph([]migrations.Migration{{App: key.App, Name: key.Name}}),
		expected:    migrations.PlanStep{Key: key, Direction: direction},
	}
	if direction == migrations.DirectionBackward {
		plan.applied = []migrations.MigrationKey{key}
		plan.targets = []preflightPlanTarget{preflightZeroPlanTarget(key.App)}
	} else {
		plan.targets = []preflightPlanTarget{
			preflightNamedPlanTarget(preflightMigrationKey{App: key.App, Name: key.Name}),
		}
	}
	return plan
}

func lifecycleHistoryContinuityDefinitions() []migrations.Migration {
	initial := migrations.Migration{
		App: "blog", Name: "0001_initial",
		Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "blog", Model: ir.Model{
				Name: "author", GoName: "Author", DBTable: "author",
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 200},
				},
			}},
			migrations.CreateModel{AppLabel: "blog", Model: lifecycleMixedScalarModel()},
		},
	}
	return []migrations.Migration{
		initial,
		{
			App: "blog", Name: "0002_article_author",
			Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_initial"}},
			// Deliberately empty: this proves only that the real recorder,
			// revision token, planner, and reconstructor can carry the identity.
			// The relation-bearing equivalent is rejected below.
		},
	}
}

func lifecycleUnsupportedRelationDefinition() migrations.Migration {
	return migrations.Migration{
		App: "blog", Name: "0002_article_author",
		Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_initial"}},
		Operations: []migrations.Operation{migrations.AddField{
			AppLabel: "blog", ModelName: "article",
			Field: ir.Field{
				Name: "editor", GoName: "EditorID", Column: "editor_id", Kind: ir.FieldForeignKey,
				Nullable: true,
				Relation: &ir.ForeignKeyRelation{
					Target:      ir.ModelIdentity{AppLabel: "blog", ModelName: "author"},
					Cardinality: ir.RelationManyToOne,
					Reverse:     ir.ReverseRelation{Name: "edited_articles"},
					OnDelete:    ir.DeleteSetNull,
				},
			},
		}},
	}
}

type lifecycleRevisionMetadata struct {
	Epoch       []byte
	Revision    int64
	Fingerprint []byte
}

func lifecycleOpenSQLiteBackend(t *testing.T, ctx context.Context, dsn string) *sqlite.Backend {
	t.Helper()
	backend, err := sqlite.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("sqlite.Open(%q): %v", dsn, err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("SQLite backend cleanup: %v", err)
		}
	})
	return backend
}

func lifecycleReadApplied(
	t *testing.T,
	ctx context.Context,
	backend *sqlite.Backend,
) []migrationbackend.AppliedMigration {
	t.Helper()
	session, err := backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(): %v", err)
	}
	records, readErr := session.ReadAppliedMigrations(ctx)
	closeErr := session.Close(ctx)
	if readErr != nil || closeErr != nil {
		t.Fatalf("revision-fenced history read/close = (%v, %v)", readErr, closeErr)
	}
	return records
}

func lifecycleReadRevisionMetadata(t *testing.T, dsn string) lifecycleRevisionMetadata {
	t.Helper()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open(metadata): %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("metadata database Close(): %v", err)
		}
	}()
	var metadata lifecycleRevisionMetadata
	if err := database.QueryRow(
		`SELECT "epoch", "revision", "history_fingerprint" FROM "godj_migration_revision" WHERE "singleton" = 1`,
	).Scan(&metadata.Epoch, &metadata.Revision, &metadata.Fingerprint); err != nil {
		t.Fatalf("read migration revision metadata: %v", err)
	}
	metadata.Epoch = append([]byte(nil), metadata.Epoch...)
	metadata.Fingerprint = append([]byte(nil), metadata.Fingerprint...)
	return metadata
}

func lifecycleSQLiteColumnExists(t *testing.T, dsn, table, column string) bool {
	t.Helper()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(fmt.Sprintf(`PRAGMA main.table_xinfo(%q)`, table))
	if err != nil {
		t.Fatalf("inspect columns for %q: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func lifecycleSQLiteForeignKeyCount(t *testing.T, dsn, table string) int {
	t.Helper()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(fmt.Sprintf(`PRAGMA main.foreign_key_list(%q)`, table))
	if err != nil {
		t.Fatalf("inspect foreign keys for %q: %v", table, err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}
