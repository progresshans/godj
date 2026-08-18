package migrationrelation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const lifecycleRelationCleanupTimeout = 2 * time.Second

var (
	lifecycleRelationErrCapability = errors.New("revision-fenced session has no relation migration capability")
	lifecycleRelationErrIntent     = errors.New("relation lifecycle intent is invalid")
)

// RelationMigrationCapabilities is the additive, backend-advertised relation
// surface. The profile is deliberately explicit: supporting a nullable add
// does not silently imply support for a populated required add or a remake.
type RelationMigrationCapabilities struct {
	CreateModelForeignKeys            bool
	AddNullableForeignKey             bool
	AddRequiredForeignKeyToEmptyTable bool
	RemoveForeignKeyByTableRemake     bool
}

// RelationRevisionFencedBackend embeds the existing lifecycle port. It opens
// the existing revision-fenced session; it does not create a parallel relation
// session with a second history snapshot or owner.
type RelationRevisionFencedBackend interface {
	migrationbackend.RevisionFencedBackend
	RelationMigrationCapabilities() RelationMigrationCapabilities
}

// RelationRevisionFencedSession is an optional extension of the already-open
// session. Its begin method returns the existing transaction contract. Relation
// schema calls, scalar schema calls, the recorder, CommitFenced, and Rollback
// therefore cannot acquire separate transaction identities.
type RelationRevisionFencedSession interface {
	migrationbackend.RevisionFencedSession
	BeginRelationFencedMigration(
		context.Context,
		migrationbackend.HistoryTransition,
		RelationMigrationIntent,
	) (migrationbackend.RevisionFencedTransaction, error)
}

type RelationMigrationOperationKind uint8

const (
	RelationMigrationCreateModel RelationMigrationOperationKind = iota + 1
	RelationMigrationDeleteModel
	RelationMigrationAddField
	RelationMigrationRemoveField
)

// RelationMigrationTarget freezes all physical target material resolved while
// history and plan state are available. TargetKey is explicit because the
// relation arm identifies a model but does not itself identify its key field.
type RelationMigrationTarget struct {
	SourceField ir.Field
	TargetModel ir.Model
	TargetKey   ir.Field
}

// RelationMigrationOperation carries the normalized before/after model states
// and the original operation index. No app, migration name, or direction lives
// here; those already have one authoritative owner in HistoryTransition.
type RelationMigrationOperation struct {
	OperationIndex int
	Kind           RelationMigrationOperationKind
	Before         ir.Model
	After          ir.Model
	Targets        []RelationMigrationTarget
}

type RelationMigrationIntent struct {
	Operations []RelationMigrationOperation
}

type lifecycleMixedResult struct {
	Outcome   migrationbackend.CommitOutcome
	Committed bool
}

// lifecyclePreparedRelationBinding seals the exact preflight-owned migration
// key, direction, transition, and operation sequence. Keeping an independent
// intent snapshot lets the lifecycle reject a same-package forged copy before
// capability selection or session I/O; callers outside this package cannot
// construct or alter the binding at all.
type lifecyclePreparedRelationBinding struct {
	key        migrations.MigrationKey
	direction  migrations.Direction
	transition migrationbackend.HistoryTransition
	intent     RelationMigrationIntent
	plan       lifecyclePreparedPlan
}

// lifecyclePreparedPlan is the immutable graph/plan provenance produced by
// whole-project preflight. Candidate targets remain inspectable until their
// complete resource shape has been bounded; the lifecycle converts them to
// product targets only while validating this sealed value.
type lifecyclePreparedPlan struct {
	definitions []migrations.Migration
	applied     []migrations.MigrationKey
	targets     []preflightPlanTarget
	expected    migrations.PlanStep
}

// lifecyclePreparedRelationStep is the one immutable handoff from the
// prepared preflight adapter to the revision-fenced lifecycle. Its transition
// is derived from the prepared step key and direction, and travels together
// with the adapter-supplied execution-order intent. Neither session nor backend
// code can observe caller aliases or accept a separately re-paired transition.
type lifecyclePreparedRelationStep struct {
	transition migrationbackend.HistoryTransition
	intent     RelationMigrationIntent
	plan       lifecyclePreparedPlan
	binding    *lifecyclePreparedRelationBinding
}

// lifecycleHistoryPlanRequest is created only after the existing session has
// returned its applied-history snapshot. It makes every product-shaped input to
// NewAppliedState, CheckHistory, and Planner.Plan explicit and immutable.
type lifecycleHistoryPlanRequest struct {
	definitions   []migrations.Migration
	records       []migrationbackend.AppliedMigration
	sealedApplied []migrations.MigrationKey
	targets       []preflightPlanTarget
	expected      migrations.PlanStep
}

func lifecyclePrepareMixedStep(
	transition migrationbackend.HistoryTransition,
	intent RelationMigrationIntent,
) (lifecyclePreparedRelationStep, error) {
	if err := lifecycleStaticPreflight(transition, intent); err != nil {
		return lifecyclePreparedRelationStep{}, err
	}

	prepared := lifecycleCloneRelationIntent(intent)
	operationIndices := make(map[int]struct{}, len(prepared.Operations))
	for index, operation := range prepared.Operations {
		if operation.OperationIndex < 0 {
			return lifecyclePreparedRelationStep{}, fmt.Errorf(
				"%w: mixed operation %d has invalid source index %d",
				lifecycleRelationErrIntent, index, operation.OperationIndex,
			)
		}
		if _, exists := operationIndices[operation.OperationIndex]; exists {
			return lifecyclePreparedRelationStep{}, fmt.Errorf(
				"%w: mixed operation %d duplicates source index %d",
				lifecycleRelationErrIntent, index, operation.OperationIndex,
			)
		}
		operationIndices[operation.OperationIndex] = struct{}{}
		if err := lifecycleValidateRelationOperation(operation); err != nil {
			return lifecyclePreparedRelationStep{}, fmt.Errorf(
				"%w: mixed operation %d: %w", lifecycleRelationErrIntent, index, err,
			)
		}
	}
	if err := lifecycleValidateOperationSequence(transition, prepared.Operations); err != nil {
		return lifecyclePreparedRelationStep{}, err
	}
	return lifecyclePreparedRelationStep{transition: transition, intent: prepared}, nil
}

func lifecyclePreparedHistoryTransition(
	key migrations.MigrationKey,
	direction migrations.Direction,
) (migrationbackend.HistoryTransition, error) {
	if key.App == "" || key.Name == "" {
		return migrationbackend.HistoryTransition{}, fmt.Errorf(
			"%w: prepared migration key is empty",
			lifecycleRelationErrIntent,
		)
	}
	kind := migrationbackend.HistoryTransitionApply
	switch direction {
	case migrations.DirectionForward:
	case migrations.DirectionBackward:
		kind = migrationbackend.HistoryTransitionUnapply
	default:
		return migrationbackend.HistoryTransition{}, fmt.Errorf(
			"%w: prepared migration direction %q is invalid",
			lifecycleRelationErrIntent, direction,
		)
	}
	return migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: key.App, Name: key.Name},
		Kind:      kind,
	}, nil
}

func lifecycleClonePreparedRelationStep(step lifecyclePreparedRelationStep) lifecyclePreparedRelationStep {
	cloned := lifecyclePreparedRelationStep{
		transition: step.transition,
		intent:     lifecycleCloneRelationIntent(step.intent),
		plan:       lifecycleClonePreparedPlan(step.plan),
	}
	if step.binding != nil {
		cloned.binding = &lifecyclePreparedRelationBinding{
			key:        step.binding.key,
			direction:  step.binding.direction,
			transition: step.binding.transition,
			intent:     lifecycleCloneRelationIntent(step.binding.intent),
			plan:       lifecycleClonePreparedPlan(step.binding.plan),
		}
	}
	return cloned
}

func lifecycleClonePreparedPlan(plan lifecyclePreparedPlan) lifecyclePreparedPlan {
	return lifecyclePreparedPlan{
		definitions: lifecycleCloneHistoryGraph(plan.definitions),
		applied:     append([]migrations.MigrationKey(nil), plan.applied...),
		targets:     append([]preflightPlanTarget(nil), plan.targets...),
		expected:    plan.expected,
	}
}

func lifecycleValidatePreparedRelationBinding(step lifecyclePreparedRelationStep) error {
	if step.binding == nil {
		return fmt.Errorf(
			"%w: relation step lacks the prepared preflight binding",
			lifecycleRelationErrIntent,
		)
	}
	expected, err := lifecyclePreparedHistoryTransition(step.binding.key, step.binding.direction)
	if err != nil {
		return err
	}
	if step.binding.transition != expected || step.transition != expected {
		return fmt.Errorf(
			"%w: relation step transition was re-paired after preflight",
			lifecycleRelationErrIntent,
		)
	}
	if !reflect.DeepEqual(step.intent, step.binding.intent) {
		return fmt.Errorf(
			"%w: relation step intent was re-paired after preflight",
			lifecycleRelationErrIntent,
		)
	}
	if !reflect.DeepEqual(step.plan, step.binding.plan) {
		return fmt.Errorf(
			"%w: relation step graph/plan provenance was re-paired after preflight",
			lifecycleRelationErrIntent,
		)
	}
	if step.plan.expected.Key != step.binding.key || step.plan.expected.Direction != step.binding.direction {
		return fmt.Errorf(
			"%w: relation step expected plan step does not own the history transition",
			lifecycleRelationErrIntent,
		)
	}
	return nil
}

// lifecycleValidateOperationSequence validates the operation sequence supplied
// by the immutable preflight adapter. Apply walks source indices 0..n-1;
// unapply walks n-1..0. Whenever the same historical model is touched again,
// its exact prior after-state must be the next before-state, preventing a
// reordered or discontinuous sequence from reaching session I/O. This local
// validator does not independently prove definition-wide membership: the
// graph-only product definitions intentionally cannot carry relation operations,
// so completeness evidence remains at preflightPrepareSteps and the private
// preflightSealCurrentHandoff construction boundary.
func lifecycleValidateOperationSequence(
	transition migrationbackend.HistoryTransition,
	operations []RelationMigrationOperation,
) error {
	latestByModel := make(map[string]ir.Model)
	for position, operation := range operations {
		expectedIndex := position
		if transition.Kind == migrationbackend.HistoryTransitionUnapply {
			expectedIndex = len(operations) - 1 - position
		}
		if operation.OperationIndex != expectedIndex {
			return fmt.Errorf(
				"%w: mixed operation position %d has source index %d, want %d for transition kind %d",
				lifecycleRelationErrIntent,
				position,
				operation.OperationIndex,
				expectedIndex,
				transition.Kind,
			)
		}

		identity := operation.Before.Name
		if identity == "" {
			identity = operation.After.Name
		}
		if identity == "" {
			return fmt.Errorf("%w: mixed operation position %d has no model identity", lifecycleRelationErrIntent, position)
		}
		if prior, exists := latestByModel[identity]; exists && !reflect.DeepEqual(operation.Before, prior) {
			return fmt.Errorf(
				"%w: mixed operation position %d before-state is discontinuous for model %q",
				lifecycleRelationErrIntent,
				position,
				identity,
			)
		}
		latestByModel[identity] = operation.After.Clone()
	}
	return nil
}

// lifecycleStaticPreflight is the resource and transition portion of the
// adapter-supplied preparation. The caller then clones and validates exact
// operation, target, model, and local sequence consistency before session I/O;
// only history consistency against the applied snapshot remains session-local.
func lifecycleStaticPreflight(
	transition migrationbackend.HistoryTransition,
	intent RelationMigrationIntent,
) error {
	if err := lifecycleValidateTransition(transition); err != nil {
		return err
	}
	if len(intent.Operations) == 0 {
		return fmt.Errorf("%w: mixed operation sequence is empty", lifecycleRelationErrIntent)
	}
	if len(intent.Operations) > profileMaxOperations {
		return fmt.Errorf(
			"%w: mixed operation resource limit exceeded: %d > %d",
			lifecycleRelationErrIntent,
			len(intent.Operations),
			profileMaxOperations,
		)
	}
	relationTargetCount := 0
	structuralNodes := len(intent.Operations)
	consumeStructuralNodes := func(count int) error {
		if count < 0 || count > migrationdefinition.MaxJSONValues-structuralNodes {
			return fmt.Errorf("aggregate mixed structural nodes exceed %d", migrationdefinition.MaxJSONValues)
		}
		structuralNodes += count
		return nil
	}
	// Complete the cheap count-only pass before reading any caller-owned string,
	// default payload, or relation metadata. Every per-model/per-operation cap and
	// the aggregate target/node budget therefore wins over a later deep scan.
	for index, operation := range intent.Operations {
		for _, candidate := range []struct {
			label string
			model ir.Model
		}{
			{label: "before", model: operation.Before},
			{label: "after", model: operation.After},
		} {
			if lifecycleModelIsZero(candidate.model) {
				continue
			}
			if len(candidate.model.Fields) > profileMaxFields {
				return fmt.Errorf(
					"%w: mixed operation %d %s field count %d exceeds %d",
					lifecycleRelationErrIntent, index, candidate.label, len(candidate.model.Fields), profileMaxFields,
				)
			}
			modelNodes := 1 + len(candidate.model.Fields)
			for fieldIndex := range candidate.model.Fields {
				if candidate.model.Fields[fieldIndex].Default != nil {
					modelNodes++
				}
				if candidate.model.Fields[fieldIndex].Relation != nil {
					modelNodes++
				}
			}
			if err := consumeStructuralNodes(modelNodes); err != nil {
				return fmt.Errorf("%w: mixed operation %d %s: %v", lifecycleRelationErrIntent, index, candidate.label, err)
			}
		}
		if len(operation.Targets) > profileMaxFields {
			return fmt.Errorf(
				"%w: mixed operation %d relation target count %d exceeds %d",
				lifecycleRelationErrIntent, index, len(operation.Targets), profileMaxFields,
			)
		}
		if len(operation.Targets) > migrationdefinition.MaxJSONValues-relationTargetCount {
			return fmt.Errorf("%w: aggregate relation target count exceeds %d", lifecycleRelationErrIntent, migrationdefinition.MaxJSONValues)
		}
		relationTargetCount += len(operation.Targets)
		for targetIndex, target := range operation.Targets {
			if len(target.TargetModel.Fields) > profileMaxFields {
				return fmt.Errorf(
					"%w: mixed operation %d target %d field count %d exceeds %d",
					lifecycleRelationErrIntent, index, targetIndex, len(target.TargetModel.Fields), profileMaxFields,
				)
			}
			targetNodes := 4 + len(target.TargetModel.Fields)
			for fieldIndex := range target.TargetModel.Fields {
				if target.TargetModel.Fields[fieldIndex].Default != nil {
					targetNodes++
				}
				if target.TargetModel.Fields[fieldIndex].Relation != nil {
					targetNodes++
				}
			}
			for _, standalone := range []ir.Field{target.SourceField, target.TargetKey} {
				if standalone.Default != nil {
					targetNodes++
				}
				if standalone.Relation != nil {
					targetNodes++
				}
			}
			if err := consumeStructuralNodes(targetNodes); err != nil {
				return fmt.Errorf("%w: mixed operation %d target %d: %v", lifecycleRelationErrIntent, index, targetIndex, err)
			}
		}
	}
	resourceBudget := lifecycleScalarResourceBudget{}
	for index, operation := range intent.Operations {
		for _, candidate := range []struct {
			label string
			model ir.Model
		}{
			{label: "before", model: operation.Before},
			{label: "after", model: operation.After},
		} {
			label, model := candidate.label, candidate.model
			if lifecycleModelIsZero(model) {
				continue
			}
			if err := lifecycleConsumeScalarResourceShape(&resourceBudget, model, ir.Field{}); err != nil {
				return fmt.Errorf(
					"%w: mixed operation %d %s resource limit: %w",
					lifecycleRelationErrIntent, index, label, err,
				)
			}
		}
		for targetIndex, target := range operation.Targets {
			if err := lifecycleConsumeScalarResourceShape(&resourceBudget, ir.Model{}, target.SourceField); err != nil {
				return fmt.Errorf(
					"%w: mixed operation %d target %d source-field resource limit: %w",
					lifecycleRelationErrIntent, index, targetIndex, err,
				)
			}
			if err := lifecycleConsumeScalarResourceShape(&resourceBudget, target.TargetModel, target.TargetKey); err != nil {
				return fmt.Errorf(
					"%w: mixed operation %d target %d resource limit: %w",
					lifecycleRelationErrIntent, index, targetIndex, err,
				)
			}
			// This independently guards the physical target-key shape before
			// lifecycleCloneRelationIntent. Membership in TargetModel is checked
			// again after cloning, but membership alone must never make a Char or
			// nullable primary key eligible for the SQLite relation path.
			if target.TargetKey.Kind != ir.FieldAuto || !target.TargetKey.PrimaryKey || target.TargetKey.Nullable {
				return fmt.Errorf(
					"%w: mixed operation %d target %d key must be a nonnullable AutoField primary key",
					lifecycleRelationErrIntent, index, targetIndex,
				)
			}
		}
	}
	if relationTargetCount == 0 {
		return fmt.Errorf("%w: scalar-only intent must use the existing scalar lifecycle", lifecycleRelationErrIntent)
	}
	return nil
}

func lifecycleCloneRelationIntent(intent RelationMigrationIntent) RelationMigrationIntent {
	clone := RelationMigrationIntent{}
	if intent.Operations == nil {
		return clone
	}
	clone.Operations = make([]RelationMigrationOperation, len(intent.Operations))
	for index, operation := range intent.Operations {
		clone.Operations[index] = operation
		clone.Operations[index].Before = operation.Before.Clone()
		clone.Operations[index].After = operation.After.Clone()
		if operation.Targets != nil {
			clone.Operations[index].Targets = make([]RelationMigrationTarget, len(operation.Targets))
			for targetIndex, target := range operation.Targets {
				clone.Operations[index].Targets[targetIndex] = RelationMigrationTarget{
					SourceField: target.SourceField.Clone(),
					TargetModel: target.TargetModel.Clone(),
					TargetKey:   target.TargetKey.Clone(),
				}
			}
		}
	}
	return clone
}

func lifecycleValidateRelationOperation(operation RelationMigrationOperation) error {
	beforeEmpty := lifecycleModelIsZero(operation.Before)
	afterEmpty := lifecycleModelIsZero(operation.After)
	switch operation.Kind {
	case RelationMigrationCreateModel:
		if !beforeEmpty || afterEmpty {
			return errors.New("CreateModel requires an empty before and a populated after")
		}
	case RelationMigrationDeleteModel:
		if beforeEmpty || !afterEmpty {
			return errors.New("DeleteModel requires a populated before and an empty after")
		}
	case RelationMigrationAddField, RelationMigrationRemoveField:
		if beforeEmpty || afterEmpty || operation.Before.Name != operation.After.Name ||
			operation.Before.GoName != operation.After.GoName || operation.Before.DBTable != operation.After.DBTable {
			return errors.New("field operation requires matching before and after model identity")
		}
	default:
		return fmt.Errorf("invalid relation migration operation kind %d", operation.Kind)
	}

	if !beforeEmpty {
		if err := lifecycleValidateNormalizedModel(operation.Before); err != nil {
			return fmt.Errorf("before model: %w", err)
		}
	}
	if !afterEmpty {
		if err := lifecycleValidateNormalizedModel(operation.After); err != nil {
			return fmt.Errorf("after model: %w", err)
		}
	}

	relationFields := make([]ir.Field, 0)
	switch operation.Kind {
	case RelationMigrationCreateModel:
		relationFields = lifecycleRelationFields(operation.After.Fields)
	case RelationMigrationDeleteModel:
		relationFields = lifecycleRelationFields(operation.Before.Fields)
	case RelationMigrationAddField:
		field, err := lifecycleSingleFieldDelta(operation.Before, operation.After)
		if err != nil {
			return err
		}
		if field.Relation != nil {
			relationFields = append(relationFields, field)
		}
	case RelationMigrationRemoveField:
		field, err := lifecycleSingleFieldDelta(operation.After, operation.Before)
		if err != nil {
			return err
		}
		if field.Relation != nil {
			relationFields = append(relationFields, field)
		}
	}
	if len(operation.Targets) != len(relationFields) {
		return fmt.Errorf("relation target count %d does not match changed relation fields %d", len(operation.Targets), len(relationFields))
	}
	for index, source := range relationFields {
		target := operation.Targets[index]
		if !reflect.DeepEqual(target.SourceField, source) || source.Relation == nil {
			return fmt.Errorf("relation target %d source field does not match normalized operation field", index)
		}
		if err := lifecycleValidateNormalizedModel(target.TargetModel); err != nil {
			return fmt.Errorf("relation target %d model: %w", index, err)
		}
		if source.Relation.Target.ModelName != target.TargetModel.Name {
			return fmt.Errorf("relation target %d model %q does not match source target %q", index, target.TargetModel.Name, source.Relation.Target.ModelName)
		}
		if target.TargetKey.Kind != ir.FieldAuto || !target.TargetKey.PrimaryKey || target.TargetKey.Nullable {
			return fmt.Errorf("relation target %d key must be a nonnullable AutoField primary key", index)
		}
		foundKey := false
		for _, field := range target.TargetModel.Fields {
			if reflect.DeepEqual(field, target.TargetKey) {
				foundKey = field.PrimaryKey
				break
			}
		}
		if !foundKey {
			return fmt.Errorf("relation target %d key is not the target model primary key", index)
		}
	}
	return nil
}

func lifecycleModelIsZero(model ir.Model) bool {
	return model.Name == "" && model.GoName == "" && model.DBTable == "" && model.Fields == nil
}

func lifecycleValidateNormalizedModel(model ir.Model) error {
	if len(model.Fields) > profileMaxFields {
		return fmt.Errorf("field resource limit exceeded: %d > %d", len(model.Fields), profileMaxFields)
	}
	version := ir.FormatVersion
	if len(lifecycleRelationFields(model.Fields)) != 0 {
		version = ir.RelationFormatVersion
	}
	normalized, err := ir.Normalize(ir.Schema{
		FormatVersion: version,
		AppLabel:      "candidate",
		Models:        []ir.Model{model.Clone()},
	})
	if err != nil {
		return err
	}
	if len(normalized.Models) != 1 || !reflect.DeepEqual(normalized.Models[0], model) {
		return errors.New("model is not normalized")
	}
	return nil
}

func lifecycleRelationFields(fields []ir.Field) []ir.Field {
	relations := make([]ir.Field, 0)
	for _, field := range fields {
		if field.Kind == ir.FieldForeignKey && field.Relation != nil {
			relations = append(relations, field.Clone())
		}
	}
	return relations
}

// lifecycleSingleFieldDelta requires the second model to equal the first plus
// exactly one field. Existing field order and values are part of the contract.
func lifecycleSingleFieldDelta(before, after ir.Model) (ir.Field, error) {
	if len(after.Fields) != len(before.Fields)+1 {
		return ir.Field{}, fmt.Errorf("field delta is %d -> %d, want exactly one addition", len(before.Fields), len(after.Fields))
	}
	for index := range before.Fields {
		if !reflect.DeepEqual(before.Fields[index], after.Fields[index]) {
			return ir.Field{}, fmt.Errorf("field delta rewrites existing field %d", index)
		}
	}
	return after.Fields[len(after.Fields)-1].Clone(), nil
}

type lifecycleScalarResourceBudget struct {
	bytes int
}

// lifecycleConsumeScalarResourceShape bounds every caller-owned scalar arm
// before Model.Clone or ir.Normalize can walk it. One shared budget spans the
// complete mixed operation sequence, matching a single migration document
// instead of incorrectly granting every operation a fresh document allowance.
func lifecycleConsumeScalarResourceShape(
	budget *lifecycleScalarResourceBudget,
	model ir.Model,
	added ir.Field,
) error {
	if budget == nil {
		return errors.New("scalar resource budget is nil")
	}
	if len(model.Fields) > profileMaxFields {
		return fmt.Errorf("model field count %d exceeds %d", len(model.Fields), profileMaxFields)
	}
	consume := func(label, value string, maximum int) error {
		if len(value) > maximum {
			return fmt.Errorf("%s bytes %d exceed %d", label, len(value), maximum)
		}
		if len(value) > migrationdefinition.MaxDocumentBytes-budget.bytes {
			return fmt.Errorf("aggregate scalar bytes exceed %d", migrationdefinition.MaxDocumentBytes)
		}
		budget.bytes += len(value)
		return nil
	}
	consumeField := func(label string, field ir.Field) error {
		for _, value := range []struct {
			label string
			text  string
		}{
			{label + ".name", field.Name},
			{label + ".go_name", field.GoName},
			{label + ".column", field.Column},
		} {
			if err := consume(value.label, value.text, migrationdefinition.MaxSourceIDBytes); err != nil {
				return err
			}
		}
		if err := consume(label+".kind", string(field.Kind), migrationdefinition.MaxSourceIDBytes); err != nil {
			return err
		}
		if field.Default != nil {
			if err := consume(label+".default.kind", string(field.Default.Kind), migrationdefinition.MaxSourceIDBytes); err != nil {
				return err
			}
			if err := consume(label+".default.string", field.Default.String, migrationdefinition.MaxDocumentBytes); err != nil {
				return err
			}
		}
		if field.Relation != nil {
			for _, value := range []struct {
				label string
				text  string
			}{
				{label + ".relation.target.app", field.Relation.Target.AppLabel},
				{label + ".relation.target.model", field.Relation.Target.ModelName},
				{label + ".relation.cardinality", string(field.Relation.Cardinality)},
				{label + ".relation.reverse.name", field.Relation.Reverse.Name},
				{label + ".relation.on_delete", string(field.Relation.OnDelete)},
			} {
				if err := consume(value.label, value.text, migrationdefinition.MaxSourceIDBytes); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, value := range []struct {
		label string
		text  string
	}{
		{"model.name", model.Name},
		{"model.go_name", model.GoName},
		{"model.db_table", model.DBTable},
	} {
		if err := consume(value.label, value.text, migrationdefinition.MaxSourceIDBytes); err != nil {
			return err
		}
	}
	for index, field := range model.Fields {
		if err := consumeField(fmt.Sprintf("model.fields[%d]", index), field); err != nil {
			return err
		}
	}
	return consumeField("added_field", added)
}

// lifecycleBeginRelationFenced is an internal boundary-unit helper. It proves
// prepared begin/rollback ownership only; it is not end-to-end evidence for
// capability selection or history-plan validation. The decision path below is
// the authoritative three-stage static -> history -> physical composition.
func lifecycleBeginRelationFenced(
	ctx context.Context,
	session migrationbackend.RevisionFencedSession,
	transition migrationbackend.HistoryTransition,
	intent RelationMigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", lifecycleRelationErrIntent)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prepared, err := lifecyclePrepareMixedStep(transition, intent)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return lifecycleBeginPreparedRelationFenced(ctx, session, prepared, nil)
}

// lifecycleBeginPreparedRelationFenced performs no definition, model, target,
// membership, or history-plan validation. Those pure phases completed exactly
// once before session I/O. This boundary only selects the additive session
// capability, snapshots the already-prepared intent for the backend, observes
// cancellation again, and enters the pinned physical begin phase.
func lifecycleBeginPreparedRelationFenced(
	ctx context.Context,
	session migrationbackend.RevisionFencedSession,
	prepared lifecyclePreparedRelationStep,
	trace *lifecycleDecisionTrace,
) (migrationbackend.RevisionFencedTransaction, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", lifecycleRelationErrIntent)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lifecycleNilInterface(session) {
		return nil, lifecycleRelationErrCapability
	}

	relationSession, ok := session.(RelationRevisionFencedSession)
	if !ok || lifecycleNilInterface(relationSession) {
		// Do not fall back to BeginFencedMigration. A relation begin must receive
		// the complete intent before PRAGMA verification, BEGIN IMMEDIATE,
		// physical preflight, the revision claim, or any user DDL can occur.
		return nil, lifecycleRelationErrCapability
	}
	beginIntent := lifecycleCloneRelationIntent(prepared.intent)
	if trace != nil {
		trace.afterBeginPreparation()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	transaction, err := relationSession.BeginRelationFencedMigration(
		ctx,
		prepared.transition,
		beginIntent,
	)
	if err != nil {
		if lifecycleNilInterface(transaction) {
			return nil, err
		}
		return nil, lifecycleRollbackMixed(ctx, transaction, err)
	}
	if lifecycleNilInterface(transaction) {
		return nil, errors.New("relation-capable session returned a nil fenced transaction")
	}
	if err := ctx.Err(); err != nil {
		return nil, lifecycleRollbackMixed(ctx, transaction, err)
	}
	return transaction, nil
}

func lifecycleValidateTransition(
	transition migrationbackend.HistoryTransition,
) error {
	if transition.Migration.App == "" || transition.Migration.Name == "" {
		return fmt.Errorf("%w: history transition identity is empty", lifecycleRelationErrIntent)
	}
	if transition.Kind != migrationbackend.HistoryTransitionApply &&
		transition.Kind != migrationbackend.HistoryTransitionUnapply {
		return fmt.Errorf("%w: history transition kind is invalid", lifecycleRelationErrIntent)
	}
	return nil
}

// lifecycleExecuteMixedStep is an internal transaction-unit helper that still
// routes through the immutable preparation stage. Tests that claim complete
// lifecycle evidence must use lifecycleExecuteDecisionPath so capability and
// history validation cannot be skipped.
func lifecycleExecuteMixedStep(
	ctx context.Context,
	session migrationbackend.RevisionFencedSession,
	transition migrationbackend.HistoryTransition,
	intent RelationMigrationIntent,
) (lifecycleMixedResult, error) {
	if ctx == nil {
		return lifecycleMixedResult{}, fmt.Errorf("%w: context is nil", lifecycleRelationErrIntent)
	}
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	prepared, err := lifecyclePrepareMixedStep(transition, intent)
	if err != nil {
		return lifecycleMixedResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	return lifecycleExecutePreparedMixedStep(ctx, session, prepared, nil)
}

func lifecycleExecutePreparedMixedStep(
	ctx context.Context,
	session migrationbackend.RevisionFencedSession,
	prepared lifecyclePreparedRelationStep,
	trace *lifecycleDecisionTrace,
) (lifecycleMixedResult, error) {
	if ctx == nil {
		return lifecycleMixedResult{}, fmt.Errorf("%w: context is nil", lifecycleRelationErrIntent)
	}
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	// Session lifetime belongs to the outer lifecycle, which may execute many
	// sequential fenced steps from one atomic history snapshot. This helper owns
	// only the transaction it begins and therefore must not close the session.
	transaction, err := lifecycleBeginPreparedRelationFenced(ctx, session, prepared, trace)
	if err != nil {
		return lifecycleMixedResult{}, err
	}

	for _, operation := range prepared.intent.Operations {
		if err := ctx.Err(); err != nil {
			return lifecycleMixedResult{}, lifecycleRollbackMixed(ctx, transaction, err)
		}
		err = lifecycleApplyPreparedOperation(ctx, transaction, operation)
		if err != nil {
			return lifecycleMixedResult{}, lifecycleRollbackMixed(ctx, transaction, err)
		}
	}
	// A context-insensitive backend operation may return nil after canceling the
	// request. Recheck before staging recorder state so the final operation
	// cannot turn cancellation into a durable migration.
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, lifecycleRollbackMixed(ctx, transaction, err)
	}

	switch prepared.transition.Kind {
	case migrationbackend.HistoryTransitionApply:
		err = transaction.RecordApplied(ctx, prepared.transition.Migration.App, prepared.transition.Migration.Name)
	case migrationbackend.HistoryTransitionUnapply:
		err = transaction.RecordUnapplied(ctx, prepared.transition.Migration.App, prepared.transition.Migration.Name)
	}
	if err != nil {
		return lifecycleMixedResult{}, lifecycleRollbackMixed(ctx, transaction, err)
	}
	// The recorder is also an external port. If it returns nil while the request
	// becomes canceled, roll the staged transition back instead of attempting a
	// commit with an already-canceled context.
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, lifecycleRollbackMixed(ctx, transaction, err)
	}

	outcome, commitErr := transaction.CommitFenced(ctx)
	result := lifecycleMixedResult{Outcome: outcome}
	switch outcome.Durability {
	case migrationbackend.CommitCommitted:
		result.Committed = true
		return result, commitErr
	case migrationbackend.CommitRolledBack:
		if commitErr == nil {
			commitErr = errors.New("relation transaction reported rollback without an error")
		}
		return result, commitErr
	case migrationbackend.CommitUnknown:
		if commitErr == nil {
			commitErr = errors.New("relation transaction reported unknown outcome without an error")
		}
		return result, commitErr
	default:
		return result, fmt.Errorf("relation transaction returned invalid commit durability %d: %w", outcome.Durability, commitErr)
	}
}

func lifecycleApplyPreparedOperation(
	ctx context.Context,
	transaction migrationbackend.RevisionFencedTransaction,
	operation RelationMigrationOperation,
) error {
	switch operation.Kind {
	case RelationMigrationCreateModel:
		return transaction.CreateModel(ctx, operation.After.Clone())
	case RelationMigrationDeleteModel:
		return transaction.DeleteModel(ctx, operation.Before.Clone())
	case RelationMigrationAddField:
		field, err := lifecycleSingleFieldDelta(operation.Before, operation.After)
		if err != nil {
			return err
		}
		return transaction.AddField(ctx, operation.Before.Clone(), field)
	case RelationMigrationRemoveField:
		field, err := lifecycleSingleFieldDelta(operation.After, operation.Before)
		if err != nil {
			return err
		}
		return transaction.RemoveField(ctx, operation.Before.Clone(), field)
	default:
		return fmt.Errorf("invalid prepared relation operation kind %d", operation.Kind)
	}
}

func lifecycleRollbackMixed(
	ctx context.Context,
	transaction migrationbackend.RevisionFencedTransaction,
	primary error,
) error {
	cleanupBase := context.Background()
	if ctx != nil {
		cleanupBase = context.WithoutCancel(ctx)
	}
	cleanupCtx, cancel := context.WithTimeout(cleanupBase, lifecycleRelationCleanupTimeout)
	defer cancel()
	return errors.Join(primary, transaction.Rollback(cleanupCtx))
}

func lifecycleNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

type lifecyclePreflightMetrics struct {
	StaticPreSession int
	HistoryPlan      int
	SQLitePhysical   int
}

type lifecycleDecisionTrace struct {
	mu                     sync.Mutex
	events                 []string
	metrics                lifecyclePreflightMetrics
	cancelStatic           context.CancelFunc
	cancelHistoryPlan      context.CancelFunc
	cancelBeginPreparation context.CancelFunc
}

func (trace *lifecycleDecisionTrace) markStaticPreSession() {
	trace.mu.Lock()
	trace.metrics.StaticPreSession++
	trace.events = append(trace.events, "static_preflight")
	cancel := trace.cancelStatic
	trace.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (trace *lifecycleDecisionTrace) markHistoryPlan() {
	trace.mu.Lock()
	trace.metrics.HistoryPlan++
	trace.events = append(trace.events, "history_plan_preflight")
	cancel := trace.cancelHistoryPlan
	trace.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (trace *lifecycleDecisionTrace) markSQLitePhysical() {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.metrics.SQLitePhysical++
	trace.events = append(trace.events, "sqlite_physical_preflight")
}

func (trace *lifecycleDecisionTrace) append(event string) {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.events = append(trace.events, event)
}

func (trace *lifecycleDecisionTrace) snapshot() (lifecyclePreflightMetrics, []string) {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return trace.metrics, append([]string(nil), trace.events...)
}

func (trace *lifecycleDecisionTrace) afterBeginPreparation() {
	trace.mu.Lock()
	cancel := trace.cancelBeginPreparation
	trace.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (capabilities RelationMigrationCapabilities) lifecycleSupports(intent RelationMigrationIntent) bool {
	for _, operation := range intent.Operations {
		if len(operation.Targets) == 0 {
			continue
		}
		switch operation.Kind {
		case RelationMigrationCreateModel, RelationMigrationDeleteModel:
			if !capabilities.CreateModelForeignKeys {
				return false
			}
		case RelationMigrationAddField:
			field, err := lifecycleSingleFieldDelta(operation.Before, operation.After)
			if err != nil || (field.Nullable && !capabilities.AddNullableForeignKey) ||
				(!field.Nullable && !capabilities.AddRequiredForeignKeyToEmptyTable) {
				return false
			}
		case RelationMigrationRemoveField:
			if !capabilities.RemoveForeignKeyByTableRemake {
				return false
			}
		default:
			return false
		}
	}
	return true
}

type lifecycleDecisionBackend struct {
	trace              *lifecycleDecisionTrace
	session            *lifecycleTraceSession
	capabilities       RelationMigrationCapabilities
	capabilityCalls    int
	openCalls          int
	cancelCapabilities context.CancelFunc
	cancelOpen         context.CancelFunc
}

var _ RelationRevisionFencedBackend = (*lifecycleDecisionBackend)(nil)

func (backend *lifecycleDecisionBackend) RelationMigrationCapabilities() RelationMigrationCapabilities {
	backend.capabilityCalls++
	if backend.cancelCapabilities != nil {
		backend.cancelCapabilities()
	}
	return backend.capabilities
}

func (backend *lifecycleDecisionBackend) OpenRevisionFencedSession(
	context.Context,
) (migrationbackend.RevisionFencedSession, error) {
	backend.openCalls++
	backend.trace.append("open_session")
	backend.session.decisionTrace = backend.trace
	if backend.cancelOpen != nil {
		backend.cancelOpen()
	}
	return backend.session, nil
}

func lifecyclePrepareSealedStep(
	step lifecyclePreparedRelationStep,
	trace *lifecycleDecisionTrace,
) (lifecyclePreparedRelationStep, error) {
	prepared, err := lifecyclePrepareSealedStepPure(step)
	trace.markStaticPreSession()
	return prepared, err
}

func lifecyclePrepareSealedStepPure(
	step lifecyclePreparedRelationStep,
) (lifecyclePreparedRelationStep, error) {
	if err := lifecycleValidateSealedStepResourceShape(step); err != nil {
		return lifecyclePreparedRelationStep{}, err
	}
	// Bound every caller-visible slice before comparing it with the independent
	// binding clone. A forged nested graph, applied key, target, expected step,
	// transition, or intent is rejected before capability discovery or Open.
	if err := lifecycleStaticPreflight(step.transition, step.intent); err != nil {
		return lifecyclePreparedRelationStep{}, err
	}
	if step.binding != nil {
		if err := lifecycleStaticPreflight(step.binding.transition, step.binding.intent); err != nil {
			return lifecyclePreparedRelationStep{}, err
		}
	}
	if err := lifecycleValidatePreparedRelationBinding(step); err != nil {
		return lifecyclePreparedRelationStep{}, err
	}
	prepared, err := lifecyclePrepareMixedStep(step.transition, step.intent)
	if err != nil {
		return lifecyclePreparedRelationStep{}, err
	}
	prepared.plan = lifecycleClonePreparedPlan(step.plan)
	prepared.binding = &lifecyclePreparedRelationBinding{
		key:        step.binding.key,
		direction:  step.binding.direction,
		transition: step.binding.transition,
		intent:     lifecycleCloneRelationIntent(step.binding.intent),
		plan:       lifecycleClonePreparedPlan(step.binding.plan),
	}
	if err := lifecycleValidateSealedPlan(prepared.plan); err != nil {
		return lifecyclePreparedRelationStep{}, err
	}
	return prepared, nil
}

func lifecycleCloneHistoryGraph(definitions []migrations.Migration) []migrations.Migration {
	cloned := make([]migrations.Migration, len(definitions))
	for index, definitionValue := range definitions {
		cloned[index] = migrations.Migration{
			App:          definitionValue.App,
			Name:         definitionValue.Name,
			Dependencies: append([]migrations.MigrationKey(nil), definitionValue.Dependencies...),
		}
	}
	return cloned
}

// lifecycleValidateSealedStepResourceShape completes a count-only pass across
// the sealed graph, applied snapshot, and targets before reading identity bytes
// or cloning nested IR. Only then may product Target values be constructed.
func lifecycleValidateSealedStepResourceShape(step lifecyclePreparedRelationStep) error {
	if step.binding != nil {
		// The independent binding copy is attacker-visible to same-package tests.
		// Bound it separately before reflect.DeepEqual can traverse either graph or
		// IR tree; recursion terminates because the synthetic step has no binding.
		if err := lifecycleValidateSealedStepResourceShape(lifecyclePreparedRelationStep{
			transition: step.binding.transition,
			plan:       step.binding.plan,
		}); err != nil {
			return err
		}
		if len(step.binding.key.App) > migrationdefinition.MaxSourceIDBytes ||
			len(step.binding.key.Name) > migrationdefinition.MaxSourceIDBytes {
			return fmt.Errorf("%w: sealed binding key bytes exceed the profile limit", lifecycleRelationErrIntent)
		}
	}
	plan := step.plan
	if len(plan.definitions) == 0 || len(plan.definitions) > migrationdefinition.MaxSources ||
		len(plan.applied) > migrationdefinition.MaxSources ||
		len(plan.targets) == 0 || len(plan.targets) > migrationdefinition.MaxSources {
		return fmt.Errorf(
			"%w: sealed history-plan graph, applied, or target count is outside the profile limit",
			lifecycleRelationErrIntent,
		)
	}
	structuralNodes := 3 + len(plan.definitions) + len(plan.applied) + len(plan.targets)
	if structuralNodes > migrationdefinition.MaxJSONValues {
		return fmt.Errorf("%w: aggregate sealed history-plan structural nodes exceed the profile limit", lifecycleRelationErrIntent)
	}
	for index, definitionValue := range plan.definitions {
		if len(definitionValue.Dependencies) > migrationdefinition.MaxDependenciesPerMigration {
			return fmt.Errorf(
				"%w: sealed history-plan definition %d dependency count exceeds the profile limit",
				lifecycleRelationErrIntent, index,
			)
		}
		if len(definitionValue.Operations) != 0 {
			return fmt.Errorf(
				"%w: sealed history-plan definition %d must be graph-only; product state reconstruction is outside this candidate",
				lifecycleRelationErrIntent, index,
			)
		}
		if len(definitionValue.Dependencies) > migrationdefinition.MaxJSONValues-structuralNodes {
			return fmt.Errorf("%w: aggregate history-plan structural nodes exceed the profile limit", lifecycleRelationErrIntent)
		}
		structuralNodes += len(definitionValue.Dependencies)
	}

	aggregateBytes := 0
	consumeIdentity := func(label, app, name string) error {
		for _, value := range []struct {
			part string
			text string
		}{
			{part: "app", text: app},
			{part: "name", text: name},
		} {
			if len(value.text) > migrationdefinition.MaxSourceIDBytes {
				return fmt.Errorf(
					"%w: %s.%s identity bytes exceed the profile limit",
					lifecycleRelationErrIntent, label, value.part,
				)
			}
			if len(value.text) > migrationdefinition.MaxDocumentBytes-aggregateBytes {
				return fmt.Errorf("%w: aggregate history-plan identity bytes exceed the profile limit", lifecycleRelationErrIntent)
			}
			aggregateBytes += len(value.text)
		}
		return nil
	}
	if err := consumeIdentity(
		"transition",
		step.transition.Migration.App,
		step.transition.Migration.Name,
	); err != nil {
		return err
	}
	if err := consumeIdentity("expected", plan.expected.Key.App, plan.expected.Key.Name); err != nil {
		return err
	}
	for appliedIndex, key := range plan.applied {
		if err := consumeIdentity(fmt.Sprintf("applied[%d]", appliedIndex), key.App, key.Name); err != nil {
			return err
		}
	}
	for targetIndex, target := range plan.targets {
		if err := consumeIdentity(
			fmt.Sprintf("targets[%d].key", targetIndex),
			target.Key.App,
			target.Key.Name,
		); err != nil {
			return err
		}
		if err := consumeIdentity(fmt.Sprintf("targets[%d].zero", targetIndex), target.App, ""); err != nil {
			return err
		}
	}
	for definitionIndex, definitionValue := range plan.definitions {
		if err := consumeIdentity(
			fmt.Sprintf("definitions[%d]", definitionIndex),
			definitionValue.App,
			definitionValue.Name,
		); err != nil {
			return err
		}
		for dependencyIndex, dependency := range definitionValue.Dependencies {
			if err := consumeIdentity(
				fmt.Sprintf("definitions[%d].dependencies[%d]", definitionIndex, dependencyIndex),
				dependency.App,
				dependency.Name,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// lifecycleValidateSealedPlan exercises the actual product planning sequence
// over only the adapter-sealed values. The first/current step is part of the
// seal and must exactly own the transition.
func lifecycleValidateSealedPlan(plan lifecyclePreparedPlan) error {
	applied, err := migrations.NewAppliedState(plan.applied...)
	if err != nil {
		return fmt.Errorf("%w: product applied-state validation failed: %w", lifecycleRelationErrIntent, err)
	}
	planner, err := migrations.NewPlanner(plan.definitions...)
	if err != nil {
		return fmt.Errorf("%w: product Planner graph validation failed: %w", lifecycleRelationErrIntent, err)
	}
	if err := planner.CheckHistory(applied); err != nil {
		return fmt.Errorf("%w: product history consistency validation failed: %w", lifecycleRelationErrIntent, err)
	}
	productTargets, targetFailure := preflightProductPlanTargets(plan.targets)
	if targetFailure != nil {
		return fmt.Errorf("%w: sealed product target validation failed: %w", lifecycleRelationErrIntent, targetFailure)
	}
	steps, err := planner.Plan(applied, productTargets...)
	if err != nil {
		return fmt.Errorf("%w: product plan request validation failed: %w", lifecycleRelationErrIntent, err)
	}
	if len(steps) == 0 || steps[0] != plan.expected {
		return fmt.Errorf(
			"%w: sealed product plan current step = %#v, want %#v",
			lifecycleRelationErrIntent,
			func() migrations.PlanStep {
				if len(steps) == 0 {
					return migrations.PlanStep{}
				}
				return steps[0]
			}(),
			plan.expected,
		)
	}
	return nil
}

func lifecycleValidatePreparedHistoryPlan(
	records []migrationbackend.AppliedMigration,
	prepared lifecyclePreparedRelationStep,
	trace *lifecycleDecisionTrace,
) error {
	if err := lifecycleValidateHistoryPlanResourceShape(records, prepared); err != nil {
		trace.markHistoryPlan()
		return err
	}
	request := lifecycleHistoryPlanRequest{
		definitions:   lifecycleCloneHistoryGraph(prepared.plan.definitions),
		records:       append([]migrationbackend.AppliedMigration(nil), records...),
		sealedApplied: append([]migrations.MigrationKey(nil), prepared.plan.applied...),
		targets:       append([]preflightPlanTarget(nil), prepared.plan.targets...),
		expected:      prepared.plan.expected,
	}
	err := lifecycleValidateHistoryPlanRequest(request)
	trace.markHistoryPlan()
	return err
}

// lifecycleValidateHistoryPlanResourceShape bounds the session-owned record
// snapshot together with the already-prepared graph before allocating the
// product MigrationKey slice. The graph is scanned again so the history-stage
// request has one aggregate budget rather than granting records a fresh one.
func lifecycleValidateHistoryPlanResourceShape(
	records []migrationbackend.AppliedMigration,
	prepared lifecyclePreparedRelationStep,
) error {
	if len(records) > migrationdefinition.MaxSources {
		return fmt.Errorf("%w: applied-history record count exceeds the profile limit", lifecycleRelationErrIntent)
	}
	plan := prepared.plan
	structuralNodes := len(records) + len(plan.applied) + len(plan.definitions) + len(plan.targets) + 1
	if structuralNodes > migrationdefinition.MaxJSONValues {
		return fmt.Errorf("%w: aggregate history-plan structural nodes exceed the profile limit", lifecycleRelationErrIntent)
	}
	for index, definitionValue := range plan.definitions {
		if len(definitionValue.Dependencies) > migrationdefinition.MaxDependenciesPerMigration {
			return fmt.Errorf(
				"%w: prepared history-plan definition %d dependency count exceeds the profile limit",
				lifecycleRelationErrIntent, index,
			)
		}
		if len(definitionValue.Dependencies) > migrationdefinition.MaxJSONValues-structuralNodes {
			return fmt.Errorf("%w: aggregate history-plan structural nodes exceed the profile limit", lifecycleRelationErrIntent)
		}
		structuralNodes += len(definitionValue.Dependencies)
	}

	aggregateBytes := 0
	consumeIdentity := func(label, app, name string) error {
		for _, value := range []struct {
			part string
			text string
		}{
			{part: "app", text: app},
			{part: "name", text: name},
		} {
			if len(value.text) > migrationdefinition.MaxSourceIDBytes {
				return fmt.Errorf(
					"%w: %s.%s identity bytes exceed the profile limit",
					lifecycleRelationErrIntent, label, value.part,
				)
			}
			if len(value.text) > migrationdefinition.MaxDocumentBytes-aggregateBytes {
				return fmt.Errorf("%w: aggregate history-plan identity bytes exceed the profile limit", lifecycleRelationErrIntent)
			}
			aggregateBytes += len(value.text)
		}
		return nil
	}
	if err := consumeIdentity("expected", plan.expected.Key.App, plan.expected.Key.Name); err != nil {
		return err
	}
	for appliedIndex, key := range plan.applied {
		if err := consumeIdentity(fmt.Sprintf("sealed_applied[%d]", appliedIndex), key.App, key.Name); err != nil {
			return err
		}
	}
	for definitionIndex, definitionValue := range plan.definitions {
		if err := consumeIdentity(
			fmt.Sprintf("definitions[%d]", definitionIndex),
			definitionValue.App,
			definitionValue.Name,
		); err != nil {
			return err
		}
		for dependencyIndex, dependency := range definitionValue.Dependencies {
			if err := consumeIdentity(
				fmt.Sprintf("definitions[%d].dependencies[%d]", definitionIndex, dependencyIndex),
				dependency.App,
				dependency.Name,
			); err != nil {
				return err
			}
		}
	}
	for recordIndex, record := range records {
		if err := consumeIdentity(
			fmt.Sprintf("records[%d]", recordIndex),
			record.App,
			record.Name,
		); err != nil {
			return err
		}
	}
	return nil
}

func lifecycleValidateHistoryPlanRequest(request lifecycleHistoryPlanRequest) error {
	keys := make([]migrations.MigrationKey, len(request.records))
	for index, record := range request.records {
		keys[index] = migrations.MigrationKey{App: record.App, Name: record.Name}
	}
	applied, err := migrations.NewAppliedState(keys...)
	if err != nil {
		return fmt.Errorf("%w: product applied-state validation failed: %w", lifecycleRelationErrIntent, err)
	}
	planner, err := migrations.NewPlanner(request.definitions...)
	if err != nil {
		return fmt.Errorf("%w: product Planner graph validation failed: %w", lifecycleRelationErrIntent, err)
	}
	if err := planner.CheckHistory(applied); err != nil {
		return fmt.Errorf("%w: product history consistency validation failed: %w", lifecycleRelationErrIntent, err)
	}
	sealedApplied, err := migrations.NewAppliedState(request.sealedApplied...)
	if err != nil {
		return fmt.Errorf("%w: sealed applied-state validation failed: %w", lifecycleRelationErrIntent, err)
	}
	if !lifecycleSameAppliedKeys(keys, request.sealedApplied) {
		return fmt.Errorf("%w: session applied history differs from the sealed preflight snapshot", lifecycleRelationErrIntent)
	}
	if err := planner.CheckHistory(sealedApplied); err != nil {
		return fmt.Errorf("%w: sealed history consistency validation failed: %w", lifecycleRelationErrIntent, err)
	}
	productTargets, targetFailure := preflightProductPlanTargets(request.targets)
	if targetFailure != nil {
		return fmt.Errorf("%w: sealed product target validation failed: %w", lifecycleRelationErrIntent, targetFailure)
	}
	plan, err := planner.Plan(applied, productTargets...)
	if err != nil {
		return fmt.Errorf("%w: product plan request validation failed: %w", lifecycleRelationErrIntent, err)
	}
	if len(plan) == 0 || plan[0] != request.expected {
		return fmt.Errorf(
			"%w: product plan current step = %#v, want %#v",
			lifecycleRelationErrIntent,
			func() migrations.PlanStep {
				if len(plan) == 0 {
					return migrations.PlanStep{}
				}
				return plan[0]
			}(),
			request.expected,
		)
	}
	return nil
}

func lifecycleSameAppliedKeys(left, right []migrations.MigrationKey) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[migrations.MigrationKey]struct{}, len(left))
	for _, key := range left {
		seen[key] = struct{}{}
	}
	if len(seen) != len(left) {
		return false
	}
	for _, key := range right {
		if _, exists := seen[key]; !exists {
			return false
		}
	}
	return true
}

func lifecycleExecuteDecisionPath(
	ctx context.Context,
	backend RelationRevisionFencedBackend,
	step lifecyclePreparedRelationStep,
	trace *lifecycleDecisionTrace,
) (result lifecycleMixedResult, resultErr error) {
	if ctx == nil {
		return lifecycleMixedResult{}, fmt.Errorf("%w: context is nil", lifecycleRelationErrIntent)
	}
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	if trace == nil {
		return lifecycleMixedResult{}, fmt.Errorf("%w: preflight trace is nil", lifecycleRelationErrIntent)
	}
	prepared, err := lifecyclePrepareSealedStep(step, trace)
	if err != nil {
		return lifecycleMixedResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	if lifecycleNilInterface(backend) {
		return lifecycleMixedResult{}, lifecycleRelationErrCapability
	}
	capabilities := backend.RelationMigrationCapabilities()
	supported := capabilities.lifecycleSupports(prepared.intent)
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	if !supported {
		return lifecycleMixedResult{}, lifecycleRelationErrCapability
	}
	// The context gate is immediately adjacent to Open: neither complete pure
	// preparation nor capability selection can conceal cancellation.
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	session, openErr := backend.OpenRevisionFencedSession(ctx)
	if !lifecycleNilInterface(session) {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lifecycleRelationCleanupTimeout)
			defer cancel()
			resultErr = errors.Join(resultErr, session.Close(cleanupCtx))
		}()
	}
	if openErr != nil {
		return lifecycleMixedResult{}, openErr
	}
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	if lifecycleNilInterface(session) {
		return lifecycleMixedResult{}, errors.New("relation revision-fenced backend returned a nil existing session")
	}
	records, err := session.ReadAppliedMigrations(ctx)
	if err != nil {
		return lifecycleMixedResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	if err := lifecycleValidatePreparedHistoryPlan(records, prepared, trace); err != nil {
		return lifecycleMixedResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return lifecycleMixedResult{}, err
	}
	return lifecycleExecutePreparedMixedStep(ctx, session, prepared, trace)
}

type lifecycleLegacyCountingBackend struct {
	beginCalls int
}

func (backend *lifecycleLegacyCountingBackend) BeginMigration(
	context.Context,
) (migrationbackend.Transaction, error) {
	backend.beginCalls++
	return lifecycleLegacyCountingTransaction{}, nil
}

type lifecycleLegacyCountingTransaction struct{}

var _ migrationbackend.Transaction = lifecycleLegacyCountingTransaction{}

func (lifecycleLegacyCountingTransaction) CreateModel(context.Context, ir.Model) error { return nil }
func (lifecycleLegacyCountingTransaction) DeleteModel(context.Context, ir.Model) error { return nil }
func (lifecycleLegacyCountingTransaction) AddField(context.Context, ir.Model, ir.Field) error {
	return nil
}
func (lifecycleLegacyCountingTransaction) RemoveField(context.Context, ir.Model, ir.Field) error {
	return nil
}
func (lifecycleLegacyCountingTransaction) RecordApplied(context.Context, string, string) error {
	return nil
}
func (lifecycleLegacyCountingTransaction) RecordUnapplied(context.Context, string, string) error {
	return nil
}
func (lifecycleLegacyCountingTransaction) Commit(context.Context) error   { return nil }
func (lifecycleLegacyCountingTransaction) Rollback(context.Context) error { return nil }

// lifecycleScalarOnlySession deliberately implements only the existing port.
// Its compile-time assertion is the source-compatibility proof for legacy
// session implementations.
type lifecycleScalarOnlySession struct {
	beginCalls int
}

var _ migrationbackend.RevisionFencedSession = (*lifecycleScalarOnlySession)(nil)

func (*lifecycleScalarOnlySession) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	return []migrationbackend.AppliedMigration{}, nil
}

func (session *lifecycleScalarOnlySession) BeginFencedMigration(
	context.Context,
	migrationbackend.HistoryTransition,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.beginCalls++
	return nil, errors.New("legacy begin should not be used for relation intent")
}

func (*lifecycleScalarOnlySession) Close(context.Context) error { return nil }

type lifecycleTraceSession struct {
	mu sync.Mutex

	records []migrationbackend.AppliedMigration
	events  []string

	legacyBeginCalls   int
	relationBeginCalls int
	readCalls          int
	closeCalls         int
	closeCtx           error
	closeLimit         bool

	outcome        migrationbackend.CommitOutcome
	commitErr      error
	unknownDurable bool
	relationErr    error
	cancelRelation context.CancelFunc
	cancelRecorder context.CancelFunc
	cancelBegin    context.CancelFunc
	cancelSnapshot context.CancelFunc
	beginErr       error
	beginHook      func()
	preCommitHook  func(*lifecycleTraceTransaction)
	decisionTrace  *lifecycleDecisionTrace

	lastTransaction *lifecycleTraceTransaction
}

var _ RelationRevisionFencedSession = (*lifecycleTraceSession)(nil)

func (session *lifecycleTraceSession) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	session.mu.Lock()
	session.readCalls++
	if session.decisionTrace != nil {
		session.decisionTrace.append("session_snapshot")
	}
	records := append([]migrationbackend.AppliedMigration(nil), session.records...)
	cancel := session.cancelSnapshot
	session.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return records, nil
}

func (session *lifecycleTraceSession) BeginFencedMigration(
	context.Context,
	migrationbackend.HistoryTransition,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.legacyBeginCalls++
	return nil, errors.New("legacy begin unexpectedly selected")
}

func (session *lifecycleTraceSession) BeginRelationFencedMigration(
	_ context.Context,
	transition migrationbackend.HistoryTransition,
	intent RelationMigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.relationBeginCalls++
	session.events = append(session.events, "begin_relation")
	if session.decisionTrace != nil {
		session.decisionTrace.append("relation_begin")
		session.decisionTrace.append("pragma_foreign_keys")
		session.decisionTrace.append("begin_immediate")
		session.decisionTrace.markSQLitePhysical()
		session.decisionTrace.append("revision_claim")
	}
	if session.beginHook != nil {
		session.beginHook()
	}
	transaction := &lifecycleTraceTransaction{
		session: session, transition: transition, intent: lifecycleCloneRelationIntent(intent),
		outcome: session.outcome, commitErr: session.commitErr, unknownDurable: session.unknownDurable,
		relationErr: session.relationErr, cancelRelation: session.cancelRelation,
		cancelRecorder: session.cancelRecorder,
		preCommitHook:  session.preCommitHook,
	}
	if transaction.outcome.Durability == 0 {
		transaction.outcome.Durability = migrationbackend.CommitCommitted
	}
	session.lastTransaction = transaction
	if session.cancelBegin != nil {
		session.cancelBegin()
	}
	return transaction, session.beginErr
}

func (session *lifecycleTraceSession) Close(ctx context.Context) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.closeCalls++
	session.closeCtx = ctx.Err()
	_, session.closeLimit = ctx.Deadline()
	session.events = append(session.events, "close")
	return nil
}

func (session *lifecycleTraceSession) appendEvent(event string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.events = append(session.events, event)
}

func (session *lifecycleTraceSession) snapshotEvents() []string {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]string(nil), session.events...)
}

type lifecycleTraceTransaction struct {
	session    *lifecycleTraceSession
	transition migrationbackend.HistoryTransition
	intent     RelationMigrationIntent
	nextChange int

	outcome        migrationbackend.CommitOutcome
	commitErr      error
	unknownDurable bool
	relationErr    error
	cancelRelation context.CancelFunc
	cancelRecorder context.CancelFunc
	preCommitHook  func(*lifecycleTraceTransaction)

	stagedRecords   []migrationbackend.AppliedMigration
	recordStaged    bool
	stagePublished  bool
	stageDiscarded  bool
	lastScalarModel ir.Model
	lastScalarField ir.Field

	recorderCalls int
	commitCalls   int
	rollbackCalls int
	rollbackErr   error
	rollbackCtx   error
	rollbackLimit bool
}

var _ migrationbackend.RevisionFencedTransaction = (*lifecycleTraceTransaction)(nil)

func (transaction *lifecycleTraceTransaction) CreateModel(_ context.Context, model ir.Model) error {
	return transaction.applySchemaOperation(RelationMigrationCreateModel, ir.Model{}, model, ir.Field{})
}

func (transaction *lifecycleTraceTransaction) DeleteModel(_ context.Context, model ir.Model) error {
	return transaction.applySchemaOperation(RelationMigrationDeleteModel, model, ir.Model{}, ir.Field{})
}

func (transaction *lifecycleTraceTransaction) AddField(_ context.Context, model ir.Model, field ir.Field) error {
	after := model.Clone()
	after.Fields = append(after.Fields, field.Clone())
	return transaction.applySchemaOperation(RelationMigrationAddField, model, after, field)
}

func (transaction *lifecycleTraceTransaction) RemoveField(_ context.Context, model ir.Model, field ir.Field) error {
	after := model.Clone()
	removed := false
	fields := make([]ir.Field, 0, len(model.Fields)-1)
	for _, candidate := range model.Fields {
		if !removed && reflect.DeepEqual(candidate, field) {
			removed = true
			continue
		}
		fields = append(fields, candidate.Clone())
	}
	after.Fields = fields
	if !removed {
		return relationBackendErrMismatch
	}
	return transaction.applySchemaOperation(RelationMigrationRemoveField, model, after, field)
}

func (transaction *lifecycleTraceTransaction) applySchemaOperation(
	kind RelationMigrationOperationKind,
	before ir.Model,
	after ir.Model,
	field ir.Field,
) error {
	if transaction.nextChange >= len(transaction.intent.Operations) {
		return relationBackendErrMismatch
	}
	want := transaction.intent.Operations[transaction.nextChange]
	if want.Kind != kind || !reflect.DeepEqual(want.Before, before) || !reflect.DeepEqual(want.After, after) {
		return relationBackendErrMismatch
	}
	relationChange := len(want.Targets) != 0
	if transaction.session.decisionTrace != nil {
		transaction.session.decisionTrace.append("user_ddl")
	}
	if relationChange {
		transaction.session.appendEvent("relation_change")
		if transaction.cancelRelation != nil {
			transaction.cancelRelation()
		}
		if transaction.relationErr != nil {
			return transaction.relationErr
		}
	} else {
		switch kind {
		case RelationMigrationCreateModel:
			transaction.session.appendEvent("scalar_create")
		case RelationMigrationDeleteModel:
			transaction.session.appendEvent("scalar_delete")
		case RelationMigrationAddField:
			transaction.session.appendEvent("scalar_add")
		case RelationMigrationRemoveField:
			transaction.session.appendEvent("scalar_remove")
		}
	}
	if kind == RelationMigrationAddField && !relationChange {
		transaction.lastScalarModel = before.Clone()
		transaction.lastScalarField = field.Clone()
	}
	transaction.nextChange++
	return nil
}

func (transaction *lifecycleTraceTransaction) RecordApplied(_ context.Context, app, name string) error {
	return transaction.record(migrationbackend.HistoryTransitionApply, app, name)
}

func (transaction *lifecycleTraceTransaction) RecordUnapplied(_ context.Context, app, name string) error {
	return transaction.record(migrationbackend.HistoryTransitionUnapply, app, name)
}

func (transaction *lifecycleTraceTransaction) record(
	kind migrationbackend.HistoryTransitionKind,
	app,
	name string,
) error {
	transaction.session.appendEvent("record")
	transaction.recorderCalls++
	if transaction.cancelRecorder != nil {
		transaction.cancelRecorder()
	}
	if transaction.recorderCalls != 1 || transaction.nextChange != len(transaction.intent.Operations) ||
		kind != transaction.transition.Kind || app != transaction.transition.Migration.App ||
		name != transaction.transition.Migration.Name {
		return errors.New("recorder transition does not match the declared fenced transition")
	}
	transaction.session.mu.Lock()
	current := append([]migrationbackend.AppliedMigration(nil), transaction.session.records...)
	transaction.session.mu.Unlock()
	staged, err := lifecycleStageHistoryTransition(current, transaction.transition)
	if err != nil {
		return err
	}
	transaction.stagedRecords = staged
	transaction.recordStaged = true
	return nil
}

func (transaction *lifecycleTraceTransaction) CommitFenced(context.Context) (migrationbackend.CommitOutcome, error) {
	transaction.session.appendEvent("commit_fenced")
	transaction.commitCalls++
	if transaction.preCommitHook != nil {
		transaction.preCommitHook(transaction)
	}
	if !transaction.recordStaged {
		return transaction.outcome, errors.Join(
			transaction.commitErr,
			errors.New("commit reached without a staged history transition"),
		)
	}
	// CommitCommitted publishes the staged history even when the returned error
	// is only terminal cleanup. CommitUnknown deliberately has two observations:
	// the same unknown result may hide either a durable or a nondurable outcome.
	publish := transaction.outcome.Durability == migrationbackend.CommitCommitted ||
		(transaction.outcome.Durability == migrationbackend.CommitUnknown && transaction.unknownDurable)
	if publish {
		transaction.session.mu.Lock()
		transaction.session.records = append(
			[]migrationbackend.AppliedMigration(nil),
			transaction.stagedRecords...,
		)
		transaction.session.mu.Unlock()
		transaction.stagePublished = true
	} else {
		transaction.stagedRecords = nil
		transaction.stageDiscarded = true
	}
	return transaction.outcome, transaction.commitErr
}

func (transaction *lifecycleTraceTransaction) Rollback(ctx context.Context) error {
	transaction.session.appendEvent("rollback")
	transaction.rollbackCalls++
	transaction.rollbackCtx = ctx.Err()
	_, transaction.rollbackLimit = ctx.Deadline()
	transaction.stagedRecords = nil
	transaction.stageDiscarded = transaction.recordStaged
	return transaction.rollbackErr
}

func lifecycleStageHistoryTransition(
	records []migrationbackend.AppliedMigration,
	transition migrationbackend.HistoryTransition,
) ([]migrationbackend.AppliedMigration, error) {
	staged := append([]migrationbackend.AppliedMigration(nil), records...)
	switch transition.Kind {
	case migrationbackend.HistoryTransitionApply:
		for _, record := range staged {
			if record == transition.Migration {
				return nil, errors.New("apply transition is already recorded")
			}
		}
		return append(staged, transition.Migration), nil
	case migrationbackend.HistoryTransitionUnapply:
		for index, record := range staged {
			if record != transition.Migration {
				continue
			}
			return append(staged[:index:index], staged[index+1:]...), nil
		}
		return nil, errors.New("unapply transition is not recorded")
	default:
		return nil, errors.New("history transition kind is invalid")
	}
}

func lifecycleMixedScalarModel() ir.Model {
	return ir.Model{
		Name: "article", GoName: "Article", DBTable: "article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
		},
	}
}

func lifecycleAuthorModel() ir.Model {
	return ir.Model{
		Name: "author", GoName: "Author", DBTable: "author",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 200},
		},
	}
}

func lifecycleEditorField() ir.Field {
	return ir.Field{
		Name: "editor", GoName: "EditorID", Column: "editor_id", Kind: ir.FieldForeignKey,
		Nullable: true,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "blog", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "edited_articles"},
			OnDelete:    ir.DeleteSetNull,
		},
	}
}

func lifecycleNullableEditorIntent() RelationMigrationIntent {
	before := lifecycleMixedScalarModel()
	after := before.Clone()
	editor := lifecycleEditorField()
	after.Fields = append(after.Fields, editor.Clone())
	author := lifecycleAuthorModel()
	return RelationMigrationIntent{Operations: []RelationMigrationOperation{{
		OperationIndex: 0,
		Kind:           RelationMigrationAddField,
		Before:         before,
		After:          after,
		Targets: []RelationMigrationTarget{{
			SourceField: editor,
			TargetModel: author,
			TargetKey:   author.Fields[0],
		}},
	}}}
}

func lifecyclePublishedAndEditorIntent() RelationMigrationIntent {
	before := lifecycleMixedScalarModel()
	published := ir.Field{
		Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean,
	}
	afterScalar := before.Clone()
	afterScalar.Fields = append(afterScalar.Fields, published.Clone())
	editor := lifecycleEditorField()
	afterRelation := afterScalar.Clone()
	afterRelation.Fields = append(afterRelation.Fields, editor.Clone())
	author := lifecycleAuthorModel()
	return RelationMigrationIntent{Operations: []RelationMigrationOperation{
		{
			OperationIndex: 0,
			Kind:           RelationMigrationAddField,
			Before:         before,
			After:          afterScalar,
		},
		{
			OperationIndex: 1,
			Kind:           RelationMigrationAddField,
			Before:         afterScalar,
			After:          afterRelation,
			Targets: []RelationMigrationTarget{{
				SourceField: editor,
				TargetModel: author,
				TargetKey:   author.Fields[0],
			}},
		},
	}}
}

func lifecycleUnpublishedAndEditorIntent() RelationMigrationIntent {
	forward := lifecyclePublishedAndEditorIntent()
	relation := lifecycleCloneRelationIntent(RelationMigrationIntent{
		Operations: []RelationMigrationOperation{forward.Operations[1]},
	}).Operations[0]
	return RelationMigrationIntent{Operations: []RelationMigrationOperation{
		{
			OperationIndex: relation.OperationIndex,
			Kind:           RelationMigrationRemoveField,
			Before:         relation.After.Clone(),
			After:          relation.Before.Clone(),
			Targets:        relation.Targets,
		},
		{
			OperationIndex: forward.Operations[0].OperationIndex,
			Kind:           RelationMigrationRemoveField,
			Before:         forward.Operations[0].After.Clone(),
			After:          forward.Operations[0].Before.Clone(),
		},
	}}
}

func lifecycleAllRelationCapabilities() RelationMigrationCapabilities {
	return RelationMigrationCapabilities{
		CreateModelForeignKeys:            true,
		AddNullableForeignKey:             true,
		AddRequiredForeignKeyToEmptyTable: true,
		RemoveForeignKeyByTableRemake:     true,
	}
}

func lifecycleRelationTransition(kind migrationbackend.HistoryTransitionKind) migrationbackend.HistoryTransition {
	return migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{App: "blog", Name: "0002_article_author"},
		Kind:      kind,
	}
}
