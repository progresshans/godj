package migrationrelation

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// This is a pure, test-only whole-project preflight candidate. Capability is
// an inert descriptor value; this code has no database/session/backend port.

type preflightMigrationKey struct {
	App  string
	Name string
}

type preflightOperationKind string

const (
	preflightCreateModel    preflightOperationKind = "create_model"
	preflightAddScalar      preflightOperationKind = "add_scalar"
	preflightAddRelation    preflightOperationKind = "add_relation"
	preflightRemoveRelation preflightOperationKind = "remove_relation"
)

type preflightTargetField struct {
	Name   string
	Column string
}

type preflightRelationDeclaration struct {
	Source           stateModelIdentity
	Field            string
	Target           stateModelIdentity
	TargetField      preflightTargetField
	DeclaredTable    string
	DeclaredColumn   string
	DeclaredNullable bool
	Cardinality      ir.RelationCardinality
	Reverse          ir.ReverseRelation
	OnDelete         ir.DeletePolicy
}

type preflightOperation struct {
	Kind       preflightOperationKind
	Model      stateModelIdentity
	ModelState ir.Model
	Relation   preflightRelationDeclaration
	Before     ir.Model
	After      ir.Model
}

type preflightDefinition struct {
	Key          preflightMigrationKey
	Dependencies []preflightMigrationKey
	Operations   []preflightOperation
}

type preflightCapabilityDescriptor struct {
	RelationEditor bool
}

type preflightInput struct {
	State       stateProjectState
	Definitions []preflightDefinition
	Capability  preflightCapabilityDescriptor
	PlanStart   stateProjectState
	PlanTarget  stateProjectState
	PlanApplied []migrations.MigrationKey
	PlanTargets []migrations.Target
}

type preflightCandidateError struct {
	Category   string
	Code       string
	Stage      string
	Reason     string
	Source     stateModelIdentity
	Field      string
	Target     stateModelIdentity
	Owner      preflightMigrationKey
	Dependency preflightMigrationKey
}

func (e *preflightCandidateError) Error() string {
	if e == nil {
		return "migration relation preflight candidate error"
	}
	return fmt.Sprintf(
		"%s/%s stage=%s reason=%s source=%s.%s field=%s target=%s.%s owner=%s.%s",
		e.Category,
		e.Code,
		e.Stage,
		e.Reason,
		e.Source.App,
		e.Source.Model,
		e.Field,
		e.Target.App,
		e.Target.Model,
		e.Owner.App,
		e.Owner.Name,
	)
}

type preflightIOMetrics struct {
	BeginCalls     int
	ConnectionPins int
	DDLWrites      int
	RecorderWrites int
	RevisionWrites int
	SchemaReads    int
	SessionOpens   int
}

type preflightCreatorRecord struct {
	Identity         stateModelIdentity
	Model            ir.Model
	Creator          preflightMigrationKey
	CreatorOperation int
}

type preflightRelationRecord struct {
	Declaration    preflightRelationDeclaration
	Owner          preflightMigrationKey
	OwnerOperation int
	SourceModel    ir.Model
}

type preflightRelationIdentity struct {
	model stateModelIdentity
	field string
}

type preflightSnapshot struct {
	creators  map[stateModelIdentity]preflightCreatorRecord
	relations []preflightRelationRecord
}

func (s preflightSnapshot) preflightCreator(identity stateModelIdentity) (preflightCreatorRecord, bool) {
	record, exists := s.creators[identity]
	if !exists {
		return preflightCreatorRecord{}, false
	}
	record.Model = record.Model.Clone()
	return record, true
}

func (s preflightSnapshot) preflightRelations() []preflightRelationRecord {
	cloned := make([]preflightRelationRecord, len(s.relations))
	for index := range s.relations {
		cloned[index] = s.relations[index]
		cloned[index].SourceModel = s.relations[index].SourceModel.Clone()
	}
	return cloned
}

type preflightFailureCandidate struct {
	rank    int
	failure *preflightCandidateError
}

func preflightValidate(input preflightInput) (preflightSnapshot, preflightIOMetrics, error) {
	metrics := preflightIOMetrics{}
	// Resource shape is the allocation boundary. It must inspect caller-owned
	// lengths without cloning nested definitions or Schema IR first.
	if failure := preflightResourceLimits(input); failure != nil {
		return preflightSnapshot{}, metrics, failure
	}
	snapshot := preflightCloneInput(input)

	// The exact immutable state is the first gate. Preflight never normalizes a
	// malformed snapshot on the caller's behalf and never reaches I/O.
	if failure := stateValidate(snapshot.State); failure != nil {
		return preflightSnapshot{}, metrics, failure
	}
	if snapshot.State.stateFormatVersion() != stateFormatRelation {
		return preflightSnapshot{}, metrics, preflightFailure(
			"relation_state_required",
			"relation_preflight_requires_state_2",
			stateModelIdentity{},
			"",
			stateModelIdentity{},
			preflightMigrationKey{},
		)
	}
	parents, definitions, graphFailure := preflightDefinitionGraph(snapshot.Definitions)
	if graphFailure != nil {
		return preflightSnapshot{}, metrics, graphFailure
	}
	models := preflightStateModels(snapshot.State)
	creators := make(map[stateModelIdentity]preflightCreatorRecord, len(models))
	declaredAdds := make([]preflightRelationRecord, 0)
	for _, definition := range definitions {
		for operationIndex, operation := range definition.Operations {
			switch operation.Kind {
			case preflightCreateModel:
				if operation.Relation != (preflightRelationDeclaration{}) ||
					!preflightModelIsZero(operation.Before) || !preflightModelIsZero(operation.After) {
					return preflightSnapshot{}, metrics, preflightFailure(
						"conflicting_operation_arms",
						"create_model_carries_relation_state_arm",
						operation.Model,
						operation.Relation.Field,
						operation.Relation.Target,
						definition.Key,
					)
				}
				identity := operation.Model
				if failure := preflightExactOperationModel(identity, operation.ModelState, definition.Key, "create_model_snapshot_invalid"); failure != nil {
					return preflightSnapshot{}, metrics, preflightFailure(
						failure.Code,
						failure.Reason,
						identity,
						"",
						stateModelIdentity{},
						definition.Key,
					)
				}
				if _, exists := creators[identity]; exists {
					return preflightSnapshot{}, metrics, preflightFailure(
						"duplicate_model_creator",
						"duplicate_model_creator",
						identity,
						"",
						stateModelIdentity{},
						definition.Key,
					)
				}
				creators[identity] = preflightCreatorRecord{
					Identity:         identity,
					Model:            operation.ModelState.Clone(),
					Creator:          definition.Key,
					CreatorOperation: operationIndex,
				}
			case preflightAddScalar:
				if operation.Model != (stateModelIdentity{}) || !preflightModelIsZero(operation.ModelState) ||
					operation.Relation != (preflightRelationDeclaration{}) ||
					preflightModelIsZero(operation.Before) || preflightModelIsZero(operation.After) {
					return preflightSnapshot{}, metrics, preflightFailure(
						"conflicting_operation_arms", "scalar_operation_carries_non_scalar_arm",
						stateModelIdentity{}, "", stateModelIdentity{}, definition.Key,
					)
				}
				identity := stateModelIdentity{App: definition.Key.App, Model: operation.Before.Name}
				if failure := preflightExactOperationModel(identity, operation.Before, definition.Key, "scalar_before_state_invalid"); failure != nil {
					return preflightSnapshot{}, metrics, failure
				}
				if failure := preflightExactOperationModel(identity, operation.After, definition.Key, "scalar_after_state_invalid"); failure != nil {
					return preflightSnapshot{}, metrics, failure
				}
				if failure := preflightExactScalarDelta(operation, definition.Key); failure != nil {
					return preflightSnapshot{}, metrics, failure
				}
			case preflightAddRelation, preflightRemoveRelation:
				if operation.Model != (stateModelIdentity{}) || !preflightModelIsZero(operation.ModelState) {
					return preflightSnapshot{}, metrics, preflightFailure(
						"conflicting_operation_arms",
						"relation_operation_carries_model_arm",
						operation.Model,
						operation.Relation.Field,
						operation.Relation.Target,
						definition.Key,
					)
				}
				if operation.Relation.Source == (stateModelIdentity{}) || operation.Relation.Field == "" ||
					preflightModelIsZero(operation.Before) || preflightModelIsZero(operation.After) {
					return preflightSnapshot{}, metrics, preflightFailure(
						"relation_operation_snapshot_invalid",
						"relation_operation_requires_exact_before_after",
						operation.Relation.Source,
						operation.Relation.Field,
						operation.Relation.Target,
						definition.Key,
					)
				}
				if failure := preflightExactOperationModel(operation.Relation.Source, operation.Before, definition.Key, "relation_before_state_invalid"); failure != nil {
					return preflightSnapshot{}, metrics, failure
				}
				if failure := preflightExactOperationModel(operation.Relation.Source, operation.After, definition.Key, "relation_after_state_invalid"); failure != nil {
					return preflightSnapshot{}, metrics, failure
				}
				if operation.Kind == preflightAddRelation {
					declaredAdds = append(declaredAdds, preflightRelationRecord{
						Declaration:    operation.Relation,
						Owner:          definition.Key,
						OwnerOperation: operationIndex,
						SourceModel:    operation.After.Clone(),
					})
				}
			default:
				return preflightSnapshot{}, metrics, preflightFailure(
					"unsupported_operation",
					"unsupported_operation",
					operation.Model,
					operation.Relation.Field,
					operation.Relation.Target,
					definition.Key,
				)
			}
		}
	}
	sort.Slice(declaredAdds, func(left, right int) bool {
		return preflightRelationRecordLess(declaredAdds[left], declaredAdds[right])
	})
	if failure := preflightDuplicateRelationDeclaration(declaredAdds); failure != nil {
		return preflightSnapshot{}, metrics, failure
	}

	replayed := make(map[stateModelIdentity]ir.Model, len(creators))
	activeRelations := make(map[preflightRelationIdentity]preflightRelationRecord)
	relationOperationCount := 0
	for _, definition := range definitions {
		for operationIndex, operation := range definition.Operations {
			switch operation.Kind {
			case preflightCreateModel:
				identity := operation.Model
				if _, exists := replayed[identity]; exists {
					return preflightSnapshot{}, metrics, preflightFailure(
						"duplicate_model_creator", "duplicate_model_creator", identity, "", stateModelIdentity{}, definition.Key,
					)
				}
				model := operation.ModelState.Clone()
				replayed[identity] = model
				for _, field := range model.Fields {
					if field.Kind != ir.FieldForeignKey || field.Relation == nil {
						continue
					}
					relationOperationCount++
					declaration := preflightDeclarationFromField(identity, model, field, creators)
					key := preflightRelationIdentity{model: identity, field: field.Name}
					if _, exists := activeRelations[key]; exists {
						return preflightSnapshot{}, metrics, preflightFailure(
							"duplicate_relation_declaration", "duplicate_source_field_declaration",
							identity, field.Name, stateIdentity(field.Relation.Target), preflightMigrationKey{},
						)
					}
					record := preflightRelationRecord{
						Declaration: declaration, Owner: definition.Key, OwnerOperation: operationIndex, SourceModel: model.Clone(),
					}
					if failure := preflightRelationCreatorFailure(parents, creators, record); failure != nil {
						return preflightSnapshot{}, metrics, failure
					}
					activeRelations[key] = record
				}
				if failure := preflightActiveRelationShape(activeRelations, replayed); failure != nil {
					return preflightSnapshot{}, metrics, failure
				}
			case preflightAddScalar:
				identity := stateModelIdentity{App: definition.Key.App, Model: operation.Before.Name}
				current, exists := replayed[identity]
				if !exists || !reflect.DeepEqual(current, operation.Before) {
					return preflightSnapshot{}, metrics, preflightFailure(
						"operation_before_state_mismatch", "operation_before_state_mismatch",
						identity, "", stateModelIdentity{}, definition.Key,
					)
				}
				if failure := preflightExactScalarDelta(operation, definition.Key); failure != nil {
					return preflightSnapshot{}, metrics, failure
				}
				replayed[identity] = operation.After.Clone()
			case preflightAddRelation, preflightRemoveRelation:
				relationOperationCount++
				record := preflightRelationRecord{
					Declaration: operation.Relation, Owner: definition.Key, OwnerOperation: operationIndex,
				}
				presentModel := operation.After
				if operation.Kind == preflightRemoveRelation {
					presentModel = operation.Before
				}
				record.SourceModel = presentModel.Clone()
				current, exists := replayed[operation.Relation.Source]
				if !exists {
					code := "source_model_not_found"
					reason := "source_model_missing"
					if _, createdSomewhere := creators[operation.Relation.Source]; createdSomewhere {
						code = "source_creator_not_ancestor"
						reason = "source_creator_not_in_dependency_ancestry"
					}
					return preflightSnapshot{}, metrics, preflightFailure(
						code, reason, operation.Relation.Source, operation.Relation.Field,
						operation.Relation.Target, definition.Key,
					)
				}
				if !reflect.DeepEqual(current, operation.Before) {
					return preflightSnapshot{}, metrics, preflightFailure(
						"operation_before_state_mismatch", "operation_before_state_mismatch",
						operation.Relation.Source, operation.Relation.Field, operation.Relation.Target, definition.Key,
					)
				}
				declaredField, declaredFieldExists := preflightField(presentModel, operation.Relation.Field)
				if declaredFieldExists {
					if failure := preflightRelationDeclarationFailure(presentModel, declaredField, record); failure != nil {
						return preflightSnapshot{}, metrics, failure
					}
				}
				field, failure := preflightExactRelationDelta(operation, record)
				if failure != nil {
					return preflightSnapshot{}, metrics, failure
				}
				if !declaredFieldExists {
					if failure := preflightRelationDeclarationFailure(presentModel, field, record); failure != nil {
						return preflightSnapshot{}, metrics, failure
					}
				}
				if failure := preflightRelationCreatorFailure(parents, creators, record); failure != nil {
					return preflightSnapshot{}, metrics, failure
				}
				key := preflightRelationIdentity{model: operation.Relation.Source, field: operation.Relation.Field}
				switch operation.Kind {
				case preflightAddRelation:
					if _, exists := activeRelations[key]; exists {
						return preflightSnapshot{}, metrics, preflightFailure(
							"relation_already_present", "relation_already_present_before_add",
							operation.Relation.Source, operation.Relation.Field, operation.Relation.Target, definition.Key,
						)
					}
					activeRelations[key] = record
				case preflightRemoveRelation:
					active, exists := activeRelations[key]
					if !exists || active.Declaration.Target != operation.Relation.Target {
						return preflightSnapshot{}, metrics, preflightFailure(
							"relation_remove_without_active_add", "relation_remove_without_active_add",
							operation.Relation.Source, operation.Relation.Field, operation.Relation.Target, definition.Key,
						)
					}
					delete(activeRelations, key)
				}
				replayed[operation.Relation.Source] = operation.After.Clone()
				if failure := preflightActiveRelationShape(activeRelations, replayed); failure != nil {
					return preflightSnapshot{}, metrics, failure
				}
			}
		}
	}

	relations := make([]preflightRelationRecord, 0, len(activeRelations))
	for _, record := range activeRelations {
		record.SourceModel = record.SourceModel.Clone()
		relations = append(relations, record)
	}
	sort.Slice(relations, func(left, right int) bool {
		return preflightRelationRecordLess(relations[left], relations[right])
	})

	failures := make([]preflightFailureCandidate, 0)
	if failure := preflightMissingRelationOperation(models, relations); failure != nil {
		failures = append(failures, preflightFailureCandidate{rank: 2, failure: failure})
	}
	reverseNamespaces := make(map[string]preflightRelationRecord, len(relations))
	for _, record := range relations {
		relation := record.Declaration
		sourceModel := record.SourceModel.Clone()
		_, sourceExists := models[relation.Source]
		sourceCreator, sourceCreated := creators[relation.Source]
		if !sourceExists || !sourceCreated {
			failures = append(failures, preflightRankedFailure(0, "source_model_not_found", "source_model_missing", record))
			continue
		}
		targetModel, targetExists := models[relation.Target]
		targetCreator, targetCreated := creators[relation.Target]
		if !targetExists || !targetCreated {
			failures = append(failures, preflightRankedFailure(1, "target_model_not_found", "target_model_missing", record))
			continue
		}

		targetPrimaryKey, targetPrimaryKeyOK := preflightAutoPrimaryKey(targetModel)
		if !targetPrimaryKeyOK || targetPrimaryKey.Name != relation.TargetField.Name || targetPrimaryKey.Column != relation.TargetField.Column {
			failures = append(failures, preflightRankedFailure(3, "target_autofield_required", "target_primary_key_not_auto_or_mismatched", record))
		}
		if sourceModel.DBTable != relation.DeclaredTable {
			failures = append(failures, preflightRankedFailure(4, "declared_table_mismatch", "declared_table_mismatch", record))
		}
		sourceField, sourceFieldOK := preflightField(sourceModel, relation.Field)
		if !sourceFieldOK || sourceField.Column != relation.DeclaredColumn {
			failures = append(failures, preflightRankedFailure(5, "declared_column_mismatch", "declared_column_mismatch", record))
		}
		if sourceFieldOK {
			if code, reason, mismatch := preflightHistoricalRelationMismatch(sourceField, relation); mismatch {
				failures = append(failures, preflightRankedFailure(6, code, reason, record))
			}
		}

		if relation.Reverse.Name != "" {
			namespace := relation.Target.App + "\x00" + relation.Target.Model + "\x00" + relation.Reverse.Name
			_, duplicateReverse := reverseNamespaces[namespace]
			if preflightFieldNameExists(targetModel, relation.Reverse.Name) || duplicateReverse {
				failures = append(failures, preflightRankedFailure(7, "reverse_namespace_collision", "reverse_namespace_collision", record))
			} else {
				reverseNamespaces[namespace] = record
			}
		}
		if relation.OnDelete == ir.DeleteSetNull && (!sourceFieldOK || !sourceField.Nullable) {
			failures = append(failures, preflightRankedFailure(8, "set_null_requires_nullable", "set_null_not_nullable", record))
		}
		if relation.OnDelete != ir.DeleteProtect && relation.OnDelete != ir.DeleteSetNull {
			failures = append(failures, preflightRankedFailure(9, "unsupported_delete_policy", "on_delete", record))
		}
		sourceVisibleAtCreate := sourceCreator.Creator == record.Owner &&
			sourceCreator.CreatorOperation == record.OwnerOperation &&
			sourceCreator.Identity == relation.Source
		if !sourceVisibleAtCreate && !preflightCreatorVisible(
			parents,
			sourceCreator.Creator,
			sourceCreator.CreatorOperation,
			record.Owner,
			record.OwnerOperation,
		) {
			failures = append(failures, preflightRankedFailure(10, "source_creator_not_ancestor", "source_creator_not_in_dependency_ancestry", record))
		}
		if !preflightCreatorVisible(
			parents,
			targetCreator.Creator,
			targetCreator.CreatorOperation,
			record.Owner,
			record.OwnerOperation,
		) {
			failures = append(failures, preflightRankedFailure(11, "target_creator_not_ancestor", "creator_not_in_dependency_ancestry", record))
		}
	}
	if failure := preflightRelationGraph(relations); failure != nil {
		failures = append(failures, preflightFailureCandidate{rank: 12, failure: failure})
	}
	if !snapshot.Capability.RelationEditor && relationOperationCount != 0 {
		if len(relations) != 0 {
			failures = append(failures, preflightRankedFailure(13, "relation_editor_unsupported", "relation_editor_unavailable", relations[0]))
		} else {
			failures = append(failures, preflightFailureCandidate{rank: 13, failure: preflightFailure(
				"relation_editor_unsupported", "relation_editor_unavailable",
				stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
			)})
		}
	}
	if len(failures) != 0 {
		sort.SliceStable(failures, func(left, right int) bool {
			if failures[left].rank != failures[right].rank {
				return failures[left].rank < failures[right].rank
			}
			return preflightFailureLess(failures[left].failure, failures[right].failure)
		})
		return preflightSnapshot{}, metrics, failures[0].failure
	}
	if failure := preflightReplayedStateFailure(replayed, models, relations); failure != nil {
		return preflightSnapshot{}, metrics, failure
	}
	planStart := snapshot.PlanStart
	planTarget := snapshot.PlanTarget
	planApplied := snapshot.PlanApplied
	planTargets := snapshot.PlanTargets
	if len(planTargets) == 0 {
		// The actual Planner/replay path is mandatory for every successful
		// whole-project preflight. The default proof starts from an empty
		// relation-state snapshot and asks the product Planner to reach every
		// definition in canonical topological order. Explicit callers can supply
		// a different applied snapshot/target to prove backward/unapply paths.
		planStart = stateProjectState{formatVersion: stateFormatRelation, apps: map[string]ir.Schema{}}
		planTarget = snapshot.State.stateClone()
		planApplied = nil
		planTargets = make([]migrations.Target, len(definitions))
		for index, definitionValue := range definitions {
			planTargets[index] = migrations.NamedTarget(migrations.MigrationKey{
				App:  definitionValue.Key.App,
				Name: definitionValue.Key.Name,
			})
		}
	}
	if failure := preflightReplayProductPlan(
		planStart,
		planTarget,
		planApplied,
		planTargets,
		definitions,
	); failure != nil {
		return preflightSnapshot{}, metrics, failure
	}

	publishedCreators := make(map[stateModelIdentity]preflightCreatorRecord, len(creators))
	for identity, record := range creators {
		record.Model = record.Model.Clone()
		publishedCreators[identity] = record
	}
	return preflightSnapshot{
		creators:  publishedCreators,
		relations: preflightSnapshot{relations: relations}.preflightRelations(),
	}, metrics, nil
}

func preflightResourceLimits(input preflightInput) *preflightCandidateError {
	if len(input.PlanTargets) == 0 &&
		(!preflightStateIsZero(input.PlanStart) ||
			!preflightStateIsZero(input.PlanTarget) ||
			input.PlanApplied != nil) {
		return preflightFailure(
			"conflicting_plan_arms", "plan_state_or_applied_requires_targets",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	if len(input.Definitions) > definition.MaxSources {
		return preflightFailure(
			"resource_limit_exceeded", "definition_count_exceeds_profile_limit",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	if len(input.State.apps) > definition.MaxSources {
		return preflightFailure(
			"resource_limit_exceeded", "state_app_count_exceeds_profile_limit",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	if len(input.PlanApplied) > definition.MaxSources || len(input.PlanTargets) > definition.MaxSources {
		return preflightFailure(
			"resource_limit_exceeded", "plan_step_count_exceeds_profile_limit",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}

	// A published historical state is derived from one bounded definition set.
	// The shared state scanner bounds identities, defaults, per-schema and batch
	// bytes, and structural nodes before preflight clones any Schema IR. The
	// local node accounting below then composes that bounded state with the
	// definition and product-plan arms.
	if failure := preflightStateResourceFailure(input.State, "state"); failure != nil {
		return failure
	}
	stateNodes, failure := preflightStateResourceNodes(input.State, "state")
	if failure != nil {
		return failure
	}
	aggregateNodes := stateNodes + len(input.Definitions) + len(input.PlanApplied) + len(input.PlanTargets)
	if aggregateNodes > definition.MaxJSONValues {
		return preflightFailure(
			"resource_limit_exceeded", "aggregate_definition_nodes_exceed_profile_limit",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	if len(input.PlanTargets) != 0 {
		if failure := preflightStateResourceFailure(input.PlanStart, "plan_start_state"); failure != nil {
			return failure
		}
		if failure := preflightStateResourceFailure(input.PlanTarget, "plan_target_state"); failure != nil {
			return failure
		}
		planStartNodes, failure := preflightStateResourceNodes(input.PlanStart, "plan_start_state")
		if failure != nil {
			return failure
		}
		planTargetNodes, failure := preflightStateResourceNodes(input.PlanTarget, "plan_target_state")
		if failure != nil {
			return failure
		}
		if aggregateNodes > definition.MaxJSONValues-planStartNodes ||
			aggregateNodes+planStartNodes > definition.MaxJSONValues-planTargetNodes {
			return preflightFailure(
				"resource_limit_exceeded", "aggregate_state_and_plan_nodes_exceed_profile_limit",
				stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
			)
		}
		aggregateNodes += planStartNodes + planTargetNodes
	}

	type resourceCandidate struct {
		rank           int
		owner          preflightMigrationKey
		operationIndex int
		modelArm       int
		source         stateModelIdentity
		field          string
		target         stateModelIdentity
		reason         string
	}
	var winner *resourceCandidate
	consider := func(candidate resourceCandidate) {
		if winner == nil || candidate.rank < winner.rank ||
			(candidate.rank == winner.rank && preflightMigrationKeyLess(candidate.owner, winner.owner)) ||
			(candidate.rank == winner.rank && candidate.owner == winner.owner && candidate.operationIndex < winner.operationIndex) ||
			(candidate.rank == winner.rank && candidate.owner == winner.owner && candidate.operationIndex == winner.operationIndex && candidate.modelArm < winner.modelArm) {
			copy := candidate
			winner = &copy
		}
	}
	for definitionIndex := range input.Definitions {
		item := &input.Definitions[definitionIndex]
		if aggregateNodes > definition.MaxJSONValues-len(item.Dependencies) {
			return preflightFailure(
				"resource_limit_exceeded", "aggregate_definition_nodes_exceed_profile_limit",
				stateModelIdentity{}, "", stateModelIdentity{}, item.Key,
			)
		}
		aggregateNodes += len(item.Dependencies)
		if len(item.Dependencies) > definition.MaxDependenciesPerMigration {
			consider(resourceCandidate{rank: 0, owner: item.Key, operationIndex: -1, modelArm: -1, reason: "dependency_count_exceeds_profile_limit"})
		}
		if len(item.Operations) > definition.MaxOperationsPerMigration {
			consider(resourceCandidate{rank: 1, owner: item.Key, operationIndex: -1, modelArm: -1, reason: "operation_count_exceeds_profile_limit"})
			continue
		}
		if aggregateNodes > definition.MaxJSONValues-len(item.Operations) {
			return preflightFailure(
				"resource_limit_exceeded", "aggregate_definition_nodes_exceed_profile_limit",
				stateModelIdentity{}, "", stateModelIdentity{}, item.Key,
			)
		}
		aggregateNodes += len(item.Operations)
		for operationIndex := range item.Operations {
			operation := &item.Operations[operationIndex]
			models := []ir.Model{operation.ModelState, operation.Before, operation.After}
			for modelArm := range models {
				if aggregateNodes > definition.MaxJSONValues-len(models[modelArm].Fields) {
					return preflightFailure(
						"resource_limit_exceeded", "aggregate_definition_nodes_exceed_profile_limit",
						operation.Relation.Source, operation.Relation.Field, operation.Relation.Target, item.Key,
					)
				}
				aggregateNodes += len(models[modelArm].Fields)
				if len(models[modelArm].Fields) > definition.MaxFieldsPerCreateModel {
					consider(resourceCandidate{
						rank: 2, owner: item.Key, operationIndex: operationIndex, modelArm: modelArm,
						source: operation.Model, field: operation.Relation.Field, target: operation.Relation.Target,
						reason: "field_count_exceeds_profile_limit",
					})
				}
			}
		}
	}
	if winner != nil {
		return preflightFailure(
			"resource_limit_exceeded", winner.reason,
			winner.source, winner.field, winner.target, winner.owner,
		)
	}
	return nil
}

func preflightStateResourceFailure(
	state stateProjectState,
	reasonPrefix string,
) *preflightCandidateError {
	failure := stateValidateProjectResources(state)
	if failure == nil {
		return nil
	}
	reason := reasonPrefix + "_" + failure.Reason
	return preflightFailure(
		"resource_limit_exceeded",
		reason,
		stateModelIdentity{App: failure.App, Model: failure.Model},
		failure.Field,
		stateModelIdentity{},
		preflightMigrationKey{},
	)
}

func preflightStateIsZero(value stateProjectState) bool {
	return value.formatVersion == 0 && value.apps == nil
}

func preflightStateResourceNodes(
	state stateProjectState,
	reasonPrefix string,
) (int, *preflightCandidateError) {
	if len(state.apps) > definition.MaxSources {
		return 0, preflightFailure(
			"resource_limit_exceeded", reasonPrefix+"_app_count_exceeds_profile_limit",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	apps := make([]string, 0, len(state.apps))
	for app := range state.apps {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	nodes := len(apps)
	for _, app := range apps {
		schema := state.apps[app]
		if len(schema.Models) > definition.MaxJSONValues || nodes > definition.MaxJSONValues-len(schema.Models) {
			return 0, preflightFailure(
				"resource_limit_exceeded", reasonPrefix+"_model_count_exceeds_profile_limit",
				stateModelIdentity{App: app}, "", stateModelIdentity{}, preflightMigrationKey{},
			)
		}
		nodes += len(schema.Models)
		for _, model := range schema.Models {
			if len(model.Fields) > definition.MaxJSONValues || nodes > definition.MaxJSONValues-len(model.Fields) {
				return 0, preflightFailure(
					"resource_limit_exceeded", reasonPrefix+"_field_count_exceeds_profile_limit",
					stateModelIdentity{App: app, Model: model.Name}, "", stateModelIdentity{}, preflightMigrationKey{},
				)
			}
			nodes += len(model.Fields)
		}
	}
	return nodes, nil
}

func preflightModelIsZero(model ir.Model) bool {
	return reflect.DeepEqual(model, ir.Model{})
}

func preflightExactOperationModel(
	identity stateModelIdentity,
	model ir.Model,
	owner preflightMigrationKey,
	code string,
) *preflightCandidateError {
	if identity.App == "" || identity.Model == "" || model.Name != identity.Model {
		return preflightFailure(code, "operation_model_identity_mismatch", identity, "", stateModelIdentity{}, owner)
	}
	wrapper := ir.Schema{
		FormatVersion: ir.RelationFormatVersion,
		AppLabel:      identity.App,
		Models:        []ir.Model{model.Clone()},
	}
	normalized, err := ir.Normalize(wrapper)
	if err != nil || !reflect.DeepEqual(normalized, wrapper) {
		return preflightFailure(code, "operation_model_must_be_exact_normalized_ir", identity, "", stateModelIdentity{}, owner)
	}
	return nil
}

func preflightDeclarationFromField(
	identity stateModelIdentity,
	model ir.Model,
	field ir.Field,
	creators map[stateModelIdentity]preflightCreatorRecord,
) preflightRelationDeclaration {
	declaration := preflightRelationDeclaration{
		Source:           identity,
		Field:            field.Name,
		Target:           stateIdentity(field.Relation.Target),
		DeclaredTable:    model.DBTable,
		DeclaredColumn:   field.Column,
		DeclaredNullable: field.Nullable,
		Cardinality:      field.Relation.Cardinality,
		Reverse:          field.Relation.Reverse,
		OnDelete:         field.Relation.OnDelete,
	}
	if target, exists := creators[declaration.Target]; exists {
		if primaryKey, ok := preflightAutoPrimaryKey(target.Model); ok {
			declaration.TargetField = preflightTargetField{Name: primaryKey.Name, Column: primaryKey.Column}
		}
	}
	return declaration
}

func preflightExactRelationDelta(
	operation preflightOperation,
	record preflightRelationRecord,
) (ir.Field, *preflightCandidateError) {
	before := operation.Before
	after := operation.After
	if before.Name != after.Name || before.GoName != after.GoName || before.DBTable != after.DBTable {
		return ir.Field{}, preflightFailure(
			"relation_operation_delta_mismatch", "relation_operation_changed_model_identity",
			operation.Relation.Source, operation.Relation.Field, operation.Relation.Target, record.Owner,
		)
	}
	var source, retained []ir.Field
	switch operation.Kind {
	case preflightAddRelation:
		source = after.Fields
		retained = before.Fields
	case preflightRemoveRelation:
		source = before.Fields
		retained = after.Fields
	default:
		return ir.Field{}, preflightFailure(
			"relation_operation_delta_mismatch", "relation_operation_kind_invalid",
			operation.Relation.Source, operation.Relation.Field, operation.Relation.Target, record.Owner,
		)
	}
	if len(source) != len(retained)+1 {
		return ir.Field{}, preflightFailure(
			"relation_operation_delta_mismatch", "relation_operation_must_change_exactly_one_field",
			operation.Relation.Source, operation.Relation.Field, operation.Relation.Target, record.Owner,
		)
	}
	matches := 0
	var changed ir.Field
	for index := range source {
		if source[index].Name != operation.Relation.Field {
			continue
		}
		without := make([]ir.Field, 0, len(source)-1)
		for retainedIndex := range source {
			if retainedIndex != index {
				without = append(without, source[retainedIndex].Clone())
			}
		}
		if reflect.DeepEqual(without, retained) {
			matches++
			changed = source[index].Clone()
		}
	}
	if matches != 1 {
		return ir.Field{}, preflightFailure(
			"relation_operation_delta_mismatch", "relation_operation_is_not_exact_declared_field_delta",
			operation.Relation.Source, operation.Relation.Field, operation.Relation.Target, record.Owner,
		)
	}
	return changed, nil
}

func preflightExactScalarDelta(
	operation preflightOperation,
	owner preflightMigrationKey,
) *preflightCandidateError {
	before := operation.Before
	after := operation.After
	identity := stateModelIdentity{App: owner.App, Model: before.Name}
	if operation.Kind != preflightAddScalar || before.Name != after.Name || before.GoName != after.GoName ||
		before.DBTable != after.DBTable || len(after.Fields) != len(before.Fields)+1 {
		return preflightFailure(
			"scalar_operation_delta_mismatch", "scalar_operation_must_add_exactly_one_field",
			identity, "", stateModelIdentity{}, owner,
		)
	}
	matches := 0
	var added ir.Field
	for index := range after.Fields {
		without := make([]ir.Field, 0, len(after.Fields)-1)
		for retainedIndex := range after.Fields {
			if retainedIndex != index {
				without = append(without, after.Fields[retainedIndex].Clone())
			}
		}
		if reflect.DeepEqual(without, before.Fields) {
			matches++
			added = after.Fields[index].Clone()
		}
	}
	if matches != 1 || added.Kind == ir.FieldForeignKey || added.Relation != nil {
		return preflightFailure(
			"scalar_operation_delta_mismatch", "scalar_operation_is_not_exact_non_relation_field_delta",
			identity, added.Name, stateModelIdentity{}, owner,
		)
	}
	return nil
}

func preflightActiveRelationShape(
	active map[preflightRelationIdentity]preflightRelationRecord,
	models map[stateModelIdentity]ir.Model,
) *preflightCandidateError {
	relations := make([]preflightRelationRecord, 0, len(active))
	for _, record := range active {
		record.SourceModel = record.SourceModel.Clone()
		relations = append(relations, record)
	}
	sort.Slice(relations, func(left, right int) bool {
		return preflightRelationRecordLess(relations[left], relations[right])
	})
	if failure := preflightRelationGraph(relations); failure != nil {
		return failure
	}
	reverseNamespaces := make(map[string]preflightRelationRecord, len(relations))
	for _, record := range relations {
		relation := record.Declaration
		if relation.Reverse.Name == "" {
			continue
		}
		namespace := relation.Target.App + "\x00" + relation.Target.Model + "\x00" + relation.Reverse.Name
		if _, duplicate := reverseNamespaces[namespace]; duplicate ||
			preflightFieldNameExists(models[relation.Target], relation.Reverse.Name) {
			return preflightFailure(
				"reverse_namespace_collision", "reverse_namespace_collision",
				relation.Source, relation.Field, relation.Target, record.Owner,
			)
		}
		reverseNamespaces[namespace] = record
	}
	return nil
}

func preflightReplayProductPlan(
	start stateProjectState,
	target stateProjectState,
	appliedKeys []migrations.MigrationKey,
	targets []migrations.Target,
	definitions []preflightDefinition,
) *preflightCandidateError {
	if failure := stateValidate(start); failure != nil {
		return preflightFailure(
			"plan_start_state_invalid", "plan_start_state_invalid",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	if failure := stateValidate(target); failure != nil {
		return preflightFailure(
			"plan_target_state_invalid", "plan_target_state_invalid",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	if start.stateFormatVersion() != stateFormatRelation || target.stateFormatVersion() != stateFormatRelation {
		return preflightFailure(
			"plan_state_format_invalid", "plan_requires_relation_state_2",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	byKey := make(map[preflightMigrationKey]preflightDefinition, len(definitions))
	productDefinitions := make([]migrations.Migration, len(definitions))
	for _, item := range definitions {
		byKey[item.Key] = preflightCloneDefinition(item)
	}
	for index, item := range definitions {
		productDefinitions[index] = migrations.Migration{App: item.Key.App, Name: item.Key.Name}
		productDefinitions[index].Dependencies = make([]migrations.MigrationKey, len(item.Dependencies))
		for dependencyIndex, dependency := range item.Dependencies {
			productDefinitions[index].Dependencies[dependencyIndex] = migrations.MigrationKey{App: dependency.App, Name: dependency.Name}
		}
	}
	planner, err := migrations.NewPlanner(productDefinitions...)
	if err != nil {
		return preflightFailure(
			"plan_graph_invalid", "product_planner_rejected_definition_graph",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	applied, err := migrations.NewAppliedState(appliedKeys...)
	if err != nil {
		return preflightFailure(
			"plan_applied_state_invalid", "product_planner_rejected_applied_state",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	plan, err := planner.Plan(applied, targets...)
	if err != nil {
		return preflightFailure(
			"plan_target_invalid", "product_planner_rejected_target_or_history",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	if len(plan) == 0 {
		if reflect.DeepEqual(preflightStateModels(start), preflightStateModels(target)) {
			return nil
		}
		return preflightFailure(
			"plan_target_state_mismatch", "empty_product_plan_does_not_equal_target_state",
			stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
		)
	}
	models := preflightStateModels(start)
	seen := make(map[preflightMigrationKey]struct{}, len(plan))
	for _, step := range plan {
		key := preflightMigrationKey{App: step.Key.App, Name: step.Key.Name}
		definitionValue, exists := byKey[key]
		if !exists || (step.Direction != migrations.DirectionForward && step.Direction != migrations.DirectionBackward) {
			return preflightFailure(
				"plan_step_invalid", "plan_step_definition_or_direction_invalid",
				stateModelIdentity{}, "", stateModelIdentity{}, key,
			)
		}
		if _, duplicate := seen[key]; duplicate {
			return preflightFailure(
				"plan_step_duplicate", "plan_contains_duplicate_migration_step",
				stateModelIdentity{}, "", stateModelIdentity{}, key,
			)
		}
		seen[key] = struct{}{}
		for operationOffset := range definitionValue.Operations {
			operationIndex := operationOffset
			if step.Direction == migrations.DirectionBackward {
				operationIndex = len(definitionValue.Operations) - 1 - operationOffset
			}
			operation := definitionValue.Operations[operationIndex]
			identity := operation.Model
			if operation.Kind != preflightCreateModel {
				if operation.Kind == preflightAddScalar {
					identity = stateModelIdentity{App: key.App, Model: operation.Before.Name}
				} else {
					identity = operation.Relation.Source
				}
			}
			switch operation.Kind {
			case preflightCreateModel:
				if step.Direction == migrations.DirectionForward {
					if _, present := models[identity]; present {
						return preflightFailure("plan_before_state_mismatch", "plan_create_model_already_present", identity, "", stateModelIdentity{}, key)
					}
					models[identity] = operation.ModelState.Clone()
				} else {
					if current, present := models[identity]; !present || !reflect.DeepEqual(current, operation.ModelState) {
						return preflightFailure("plan_before_state_mismatch", "plan_delete_model_state_mismatch", identity, "", stateModelIdentity{}, key)
					}
					delete(models, identity)
				}
			case preflightAddScalar, preflightAddRelation, preflightRemoveRelation:
				before, after := operation.Before, operation.After
				if step.Direction == migrations.DirectionBackward {
					before, after = after, before
				}
				if current, present := models[identity]; !present || !reflect.DeepEqual(current, before) {
					return preflightFailure(
						"plan_before_state_mismatch", "plan_operation_before_state_mismatch",
						identity, operation.Relation.Field, operation.Relation.Target, key,
					)
				}
				models[identity] = after.Clone()
			}
			if failure := preflightModelRelationShape(models, key); failure != nil {
				return failure
			}
		}
	}
	if failure := preflightReplayedStateFailure(models, preflightStateModels(target), preflightRelationsFromModels(models)); failure != nil {
		failure.Code = "plan_target_state_mismatch"
		failure.Reason = "product_plan_replay_does_not_equal_target_state"
		return failure
	}
	return nil
}

func preflightRelationsFromModels(models map[stateModelIdentity]ir.Model) []preflightRelationRecord {
	relations := make([]preflightRelationRecord, 0)
	for identity, model := range models {
		for _, field := range model.Fields {
			if field.Kind != ir.FieldForeignKey || field.Relation == nil {
				continue
			}
			relations = append(relations, preflightRelationRecord{
				Declaration: preflightRelationDeclaration{
					Source: identity, Field: field.Name, Target: stateIdentity(field.Relation.Target),
					DeclaredTable: model.DBTable, DeclaredColumn: field.Column, DeclaredNullable: field.Nullable,
					Cardinality: field.Relation.Cardinality, Reverse: field.Relation.Reverse, OnDelete: field.Relation.OnDelete,
				},
				SourceModel: model.Clone(),
			})
		}
	}
	sort.Slice(relations, func(left, right int) bool { return preflightRelationRecordLess(relations[left], relations[right]) })
	return relations
}

func preflightModelRelationShape(
	models map[stateModelIdentity]ir.Model,
	owner preflightMigrationKey,
) *preflightCandidateError {
	relations := preflightRelationsFromModels(models)
	active := make(map[preflightRelationIdentity]preflightRelationRecord, len(relations))
	for _, record := range relations {
		record.Owner = owner
		relation := record.Declaration
		if _, sourceExists := models[relation.Source]; !sourceExists {
			return preflightFailure(
				"source_model_not_found", "source_model_missing_during_plan_replay",
				relation.Source, relation.Field, relation.Target, owner,
			)
		}
		target, targetExists := models[relation.Target]
		if !targetExists {
			return preflightFailure(
				"target_model_not_found", "target_model_missing_during_plan_replay",
				relation.Source, relation.Field, relation.Target, owner,
			)
		}
		if _, validTarget := preflightAutoPrimaryKey(target); !validTarget {
			return preflightFailure(
				"target_autofield_required", "target_primary_key_not_auto_during_plan_replay",
				relation.Source, relation.Field, relation.Target, owner,
			)
		}
		active[preflightRelationIdentity{model: record.Declaration.Source, field: record.Declaration.Field}] = record
	}
	return preflightActiveRelationShape(active, models)
}

func preflightRelationDeclarationFailure(
	model ir.Model,
	field ir.Field,
	record preflightRelationRecord,
) *preflightCandidateError {
	declaration := record.Declaration
	if model.DBTable != declaration.DeclaredTable {
		return preflightRankedFailure(4, "declared_table_mismatch", "declared_table_mismatch", record).failure
	}
	if field.Column != declaration.DeclaredColumn {
		return preflightRankedFailure(5, "declared_column_mismatch", "declared_column_mismatch", record).failure
	}
	if code, reason, mismatch := preflightHistoricalRelationMismatch(field, declaration); mismatch {
		return preflightRankedFailure(6, code, reason, record).failure
	}
	return nil
}

func preflightRelationCreatorFailure(
	parents map[preflightMigrationKey][]preflightMigrationKey,
	creators map[stateModelIdentity]preflightCreatorRecord,
	record preflightRelationRecord,
) *preflightCandidateError {
	declaration := record.Declaration
	sourceCreator, sourceExists := creators[declaration.Source]
	if !sourceExists {
		return preflightRankedFailure(0, "source_model_not_found", "source_model_missing", record).failure
	}
	targetCreator, targetExists := creators[declaration.Target]
	if !targetExists {
		return preflightRankedFailure(1, "target_model_not_found", "target_model_missing", record).failure
	}
	targetPrimaryKey, targetPrimaryKeyOK := preflightAutoPrimaryKey(targetCreator.Model)
	if !targetPrimaryKeyOK || targetPrimaryKey.Name != declaration.TargetField.Name ||
		targetPrimaryKey.Column != declaration.TargetField.Column {
		return preflightRankedFailure(3, "target_autofield_required", "target_primary_key_not_auto_or_mismatched", record).failure
	}
	sourceVisibleAtCreate := sourceCreator.Creator == record.Owner &&
		sourceCreator.CreatorOperation == record.OwnerOperation &&
		sourceCreator.Identity == declaration.Source
	if !sourceVisibleAtCreate && !preflightCreatorVisible(
		parents,
		sourceCreator.Creator,
		sourceCreator.CreatorOperation,
		record.Owner,
		record.OwnerOperation,
	) {
		return preflightRankedFailure(10, "source_creator_not_ancestor", "source_creator_not_in_dependency_ancestry", record).failure
	}
	if !preflightCreatorVisible(
		parents,
		targetCreator.Creator,
		targetCreator.CreatorOperation,
		record.Owner,
		record.OwnerOperation,
	) {
		return preflightRankedFailure(11, "target_creator_not_ancestor", "creator_not_in_dependency_ancestry", record).failure
	}
	return nil
}

func preflightReplayedStateFailure(
	replayed map[stateModelIdentity]ir.Model,
	declared map[stateModelIdentity]ir.Model,
	relations []preflightRelationRecord,
) *preflightCandidateError {
	if failure := preflightMissingRelationOperation(declared, relations); failure != nil {
		return failure
	}
	identities := make([]stateModelIdentity, 0, len(replayed)+len(declared))
	seen := make(map[stateModelIdentity]struct{}, len(replayed)+len(declared))
	for identity := range replayed {
		seen[identity] = struct{}{}
		identities = append(identities, identity)
	}
	for identity := range declared {
		if _, exists := seen[identity]; !exists {
			identities = append(identities, identity)
		}
	}
	sort.Slice(identities, func(left, right int) bool {
		return preflightModelIdentityLess(identities[left], identities[right])
	})
	for _, identity := range identities {
		got, gotExists := replayed[identity]
		want, wantExists := declared[identity]
		if !gotExists || !wantExists || !reflect.DeepEqual(got, want) {
			return preflightFailure(
				"historical_state_replay_mismatch", "replayed_operations_do_not_equal_declared_historical_state",
				identity, "", stateModelIdentity{}, preflightMigrationKey{},
			)
		}
	}
	return nil
}

func preflightStateModels(state stateProjectState) map[stateModelIdentity]ir.Model {
	models := make(map[stateModelIdentity]ir.Model)
	for _, app := range state.stateApps() {
		schema := state.apps[app]
		for _, model := range schema.Models {
			models[stateModelIdentity{App: app, Model: model.Name}] = model.Clone()
		}
	}
	return models
}

func preflightDuplicateRelationDeclaration(relations []preflightRelationRecord) *preflightCandidateError {
	for index := 1; index < len(relations); index++ {
		previous := relations[index-1].Declaration
		current := relations[index].Declaration
		if previous.Source == current.Source && previous.Field == current.Field {
			return preflightFailure(
				"duplicate_relation_declaration",
				"duplicate_source_field_declaration",
				current.Source,
				current.Field,
				stateModelIdentity{},
				preflightMigrationKey{},
			)
		}
	}
	return nil
}

func preflightMissingRelationOperation(
	models map[stateModelIdentity]ir.Model,
	relations []preflightRelationRecord,
) *preflightCandidateError {
	type fieldIdentity struct {
		model stateModelIdentity
		field string
	}
	declared := make(map[fieldIdentity]struct{}, len(relations))
	for _, record := range relations {
		relation := record.Declaration
		declared[fieldIdentity{model: relation.Source, field: relation.Field}] = struct{}{}
	}
	missing := make([]preflightRelationDeclaration, 0)
	for identity, model := range models {
		for _, field := range model.Fields {
			if field.Kind != ir.FieldForeignKey || field.Relation == nil {
				continue
			}
			if _, exists := declared[fieldIdentity{model: identity, field: field.Name}]; exists {
				continue
			}
			missing = append(missing, preflightRelationDeclaration{
				Source: identity,
				Field:  field.Name,
				Target: stateIdentity(field.Relation.Target),
			})
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Slice(missing, func(left, right int) bool {
		return preflightRelationRecordLess(
			preflightRelationRecord{Declaration: missing[left]},
			preflightRelationRecord{Declaration: missing[right]},
		)
	})
	first := missing[0]
	return preflightFailure(
		"relation_owner_operation_not_found",
		"relation_owner_must_be_derived_from_operation",
		first.Source,
		first.Field,
		first.Target,
		preflightMigrationKey{},
	)
}

func preflightRelationGraph(relations []preflightRelationRecord) *preflightCandidateError {
	for _, record := range relations {
		relation := record.Declaration
		if relation.Source == relation.Target {
			return preflightFailure(
				"self_relation_unsupported",
				"self_relation_unsupported",
				relation.Source,
				relation.Field,
				relation.Target,
				record.Owner,
			)
		}
	}

	adjacency := make(map[stateModelIdentity][]preflightRelationRecord)
	nodes := make(map[stateModelIdentity]struct{})
	for _, record := range relations {
		relation := record.Declaration
		adjacency[relation.Source] = append(adjacency[relation.Source], record)
		nodes[relation.Source] = struct{}{}
		nodes[relation.Target] = struct{}{}
	}
	orderedNodes := make([]stateModelIdentity, 0, len(nodes))
	for identity := range nodes {
		orderedNodes = append(orderedNodes, identity)
	}
	sort.Slice(orderedNodes, func(left, right int) bool {
		return preflightModelIdentityLess(orderedNodes[left], orderedNodes[right])
	})
	for identity := range adjacency {
		edges := adjacency[identity]
		sort.Slice(edges, func(left, right int) bool {
			return preflightRelationRecordLess(edges[left], edges[right])
		})
		adjacency[identity] = edges
	}

	const (
		preflightGraphUnvisited uint8 = iota
		preflightGraphVisiting
		preflightGraphVisited
	)
	states := make(map[stateModelIdentity]uint8, len(nodes))
	var visit func(stateModelIdentity) *preflightCandidateError
	visit = func(identity stateModelIdentity) *preflightCandidateError {
		states[identity] = preflightGraphVisiting
		for _, record := range adjacency[identity] {
			relation := record.Declaration
			switch states[relation.Target] {
			case preflightGraphVisiting:
				return preflightFailure(
					"relation_cycle_unsupported",
					"relation_cycle_unsupported",
					relation.Source,
					relation.Field,
					relation.Target,
					record.Owner,
				)
			case preflightGraphUnvisited:
				if failure := visit(relation.Target); failure != nil {
					return failure
				}
			}
		}
		states[identity] = preflightGraphVisited
		return nil
	}
	for _, identity := range orderedNodes {
		if states[identity] != preflightGraphUnvisited {
			continue
		}
		if failure := visit(identity); failure != nil {
			return failure
		}
	}
	return nil
}

func preflightDefinitionGraph(definitions []preflightDefinition) (
	map[preflightMigrationKey][]preflightMigrationKey,
	[]preflightDefinition,
	*preflightCandidateError,
) {
	normalized := make([]preflightDefinition, len(definitions))
	productDefinitions := make([]migrations.Migration, len(definitions))
	for index := range definitions {
		normalized[index] = preflightCloneDefinition(definitions[index])
		sort.Slice(normalized[index].Dependencies, func(left, right int) bool {
			return preflightMigrationKeyLess(normalized[index].Dependencies[left], normalized[index].Dependencies[right])
		})
		productDefinitions[index] = migrations.Migration{
			App:  definitions[index].Key.App,
			Name: definitions[index].Key.Name,
			Dependencies: func() []migrations.MigrationKey {
				dependencies := make([]migrations.MigrationKey, len(definitions[index].Dependencies))
				for dependencyIndex, dependency := range definitions[index].Dependencies {
					dependencies[dependencyIndex] = migrations.MigrationKey{App: dependency.App, Name: dependency.Name}
				}
				return dependencies
			}(),
		}
	}
	if _, err := migrations.NewPlanner(productDefinitions...); err != nil {
		var planning *migrations.PlanningError
		if !errors.As(err, &planning) {
			return nil, nil, preflightFailure(
				"invalid_migration_graph", "product_planner_rejected_graph",
				stateModelIdentity{}, "", stateModelIdentity{}, preflightMigrationKey{},
			)
		}
		owner := preflightMigrationKey{App: planning.Node.App, Name: planning.Node.Name}
		failure := preflightFailure(
			string(planning.Code), string(planning.Code),
			stateModelIdentity{}, "", stateModelIdentity{}, owner,
		)
		failure.Dependency = preflightMigrationKey{App: planning.Related.App, Name: planning.Related.Name}
		return nil, nil, failure
	}
	sort.Slice(normalized, func(left, right int) bool {
		return preflightMigrationKeyLess(normalized[left].Key, normalized[right].Key)
	})

	parents := make(map[preflightMigrationKey][]preflightMigrationKey, len(normalized))
	for _, definition := range normalized {
		parents[definition.Key] = append([]preflightMigrationKey(nil), definition.Dependencies...)
	}
	keys := make([]preflightMigrationKey, 0, len(parents))
	for key := range parents {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool { return preflightMigrationKeyLess(keys[left], keys[right]) })
	byKey := make(map[preflightMigrationKey]preflightDefinition, len(normalized))
	children := make(map[preflightMigrationKey][]preflightMigrationKey, len(normalized))
	indegree := make(map[preflightMigrationKey]int, len(normalized))
	for _, item := range normalized {
		byKey[item.Key] = item
		indegree[item.Key] = len(item.Dependencies)
		for _, parent := range item.Dependencies {
			children[parent] = append(children[parent], item.Key)
		}
	}
	for parent := range children {
		sort.Slice(children[parent], func(left, right int) bool {
			return preflightMigrationKeyLess(children[parent][left], children[parent][right])
		})
	}
	ordered := make([]preflightDefinition, 0, len(normalized))
	ready := make([]preflightMigrationKey, 0, len(keys))
	for _, key := range keys {
		if indegree[key] == 0 {
			ready = append(ready, key)
		}
	}
	for len(ready) != 0 {
		key := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byKey[key])
		for _, child := range children[key] {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sort.Slice(ready, func(left, right int) bool {
					return preflightMigrationKeyLess(ready[left], ready[right])
				})
			}
		}
	}
	return parents, ordered, nil
}

func preflightCreatorVisible(
	parents map[preflightMigrationKey][]preflightMigrationKey,
	creator preflightMigrationKey,
	creatorOperation int,
	owner preflightMigrationKey,
	ownerOperation int,
) bool {
	if creator == owner {
		return creatorOperation >= 0 && ownerOperation >= 0 && creatorOperation < ownerOperation
	}
	seen := make(map[preflightMigrationKey]struct{})
	stack := append([]preflightMigrationKey(nil), parents[owner]...)
	for len(stack) != 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		if current == creator {
			return true
		}
		if _, exists := seen[current]; exists {
			continue
		}
		seen[current] = struct{}{}
		stack = append(stack, parents[current]...)
	}
	return false
}

func preflightAutoPrimaryKey(model ir.Model) (ir.Field, bool) {
	var found ir.Field
	count := 0
	for _, field := range model.Fields {
		if field.PrimaryKey {
			count++
			found = field.Clone()
		}
	}
	return found, count == 1 && found.Kind == ir.FieldAuto && !found.Nullable
}

func preflightField(model ir.Model, name string) (ir.Field, bool) {
	for _, field := range model.Fields {
		if field.Name == name {
			return field.Clone(), true
		}
	}
	return ir.Field{}, false
}

func preflightFieldNameExists(model ir.Model, name string) bool {
	_, exists := preflightField(model, name)
	return exists
}

func preflightHistoricalRelationMismatch(
	field ir.Field,
	declaration preflightRelationDeclaration,
) (string, string, bool) {
	if field.Kind != ir.FieldForeignKey {
		return "source_field_not_foreign_key", "source_field_not_foreign_key", true
	}
	if field.Relation == nil {
		return "source_relation_metadata_missing", "source_relation_metadata_missing", true
	}
	if stateIdentity(field.Relation.Target) != declaration.Target {
		return "relation_target_mismatch", "relation_target_mismatch", true
	}
	if field.Relation.Cardinality != declaration.Cardinality {
		return "relation_cardinality_mismatch", "relation_cardinality_mismatch", true
	}
	if field.Relation.Reverse != declaration.Reverse {
		return "relation_reverse_mismatch", "relation_reverse_mismatch", true
	}
	if field.Relation.OnDelete != declaration.OnDelete {
		return "relation_on_delete_mismatch", "relation_on_delete_mismatch", true
	}
	if field.Nullable != declaration.DeclaredNullable {
		return "relation_nullability_mismatch", "relation_nullability_mismatch", true
	}
	return "", "", false
}

func preflightMigrationKeyLess(left, right preflightMigrationKey) bool {
	if left.App != right.App {
		return left.App < right.App
	}
	return left.Name < right.Name
}

func preflightModelIdentityLess(left, right stateModelIdentity) bool {
	if left.App != right.App {
		return left.App < right.App
	}
	return left.Model < right.Model
}

func preflightRelationRecordLess(left, right preflightRelationRecord) bool {
	leftRelation := left.Declaration
	rightRelation := right.Declaration
	if leftRelation.Source != rightRelation.Source {
		return preflightModelIdentityLess(leftRelation.Source, rightRelation.Source)
	}
	if leftRelation.Field != rightRelation.Field {
		return leftRelation.Field < rightRelation.Field
	}
	if leftRelation.Target != rightRelation.Target {
		return preflightModelIdentityLess(leftRelation.Target, rightRelation.Target)
	}
	if leftRelation.TargetField != rightRelation.TargetField {
		if leftRelation.TargetField.Name != rightRelation.TargetField.Name {
			return leftRelation.TargetField.Name < rightRelation.TargetField.Name
		}
		return leftRelation.TargetField.Column < rightRelation.TargetField.Column
	}
	if left.Owner != right.Owner {
		return preflightMigrationKeyLess(left.Owner, right.Owner)
	}
	return left.OwnerOperation < right.OwnerOperation
}

func preflightFailureLess(left, right *preflightCandidateError) bool {
	if left.Source != right.Source {
		return preflightModelIdentityLess(left.Source, right.Source)
	}
	if left.Field != right.Field {
		return left.Field < right.Field
	}
	if left.Target != right.Target {
		return preflightModelIdentityLess(left.Target, right.Target)
	}
	return preflightMigrationKeyLess(left.Owner, right.Owner)
}

func preflightRankedFailure(
	rank int,
	code string,
	reason string,
	record preflightRelationRecord,
) preflightFailureCandidate {
	relation := record.Declaration
	return preflightFailureCandidate{
		rank: rank,
		failure: preflightFailure(
			code,
			reason,
			relation.Source,
			relation.Field,
			relation.Target,
			record.Owner,
		),
	}
}

func preflightFailure(
	code string,
	reason string,
	source stateModelIdentity,
	field string,
	target stateModelIdentity,
	owner preflightMigrationKey,
) *preflightCandidateError {
	return &preflightCandidateError{
		Category: "migration_relation_preflight_candidate_error",
		Code:     code,
		Stage:    "preflight",
		Reason:   reason,
		Source:   source,
		Field:    field,
		Target:   target,
		Owner:    owner,
	}
}

func preflightDependencyFailure(owner, dependency preflightMigrationKey) *preflightCandidateError {
	failure := preflightFailure(
		"dependency_not_found",
		"dependency_not_found",
		stateModelIdentity{},
		"",
		stateModelIdentity{},
		owner,
	)
	failure.Dependency = dependency
	return failure
}

func preflightCloneDefinition(value preflightDefinition) preflightDefinition {
	clone := preflightDefinition{
		Key:          value.Key,
		Dependencies: append([]preflightMigrationKey(nil), value.Dependencies...),
		Operations:   make([]preflightOperation, len(value.Operations)),
	}
	for index := range value.Operations {
		clone.Operations[index] = value.Operations[index]
		clone.Operations[index].ModelState = value.Operations[index].ModelState.Clone()
		clone.Operations[index].Before = value.Operations[index].Before.Clone()
		clone.Operations[index].After = value.Operations[index].After.Clone()
	}
	return clone
}

func preflightCloneInput(value preflightInput) preflightInput {
	clone := preflightInput{
		State:       value.State.stateClone(),
		Definitions: make([]preflightDefinition, len(value.Definitions)),
		Capability:  value.Capability,
		PlanApplied: append([]migrations.MigrationKey(nil), value.PlanApplied...),
		PlanTargets: append([]migrations.Target(nil), value.PlanTargets...),
	}
	if !preflightStateIsZero(value.PlanStart) {
		clone.PlanStart = value.PlanStart.stateClone()
	}
	if !preflightStateIsZero(value.PlanTarget) {
		clone.PlanTarget = value.PlanTarget.stateClone()
	}
	for index := range value.Definitions {
		clone.Definitions[index] = preflightCloneDefinition(value.Definitions[index])
	}
	return clone
}
