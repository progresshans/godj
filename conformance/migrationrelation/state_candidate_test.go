package migrationrelation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// These values are a test-only candidate for the historical-state boundary.
// The candidate deliberately stores the complete normalized Schema IR value;
// it does not invent a smaller migration-only model or recover omitted scalar
// meaning from a current runtime model.

const (
	stateFormatScalar   = 1
	stateFormatRelation = 2
)

type stateModelIdentity struct {
	App   string
	Model string
}

func stateIdentity(value ir.ModelIdentity) stateModelIdentity {
	return stateModelIdentity{App: value.AppLabel, Model: value.ModelName}
}

func (value stateModelIdentity) stateIRIdentity() ir.ModelIdentity {
	return ir.ModelIdentity{AppLabel: value.App, ModelName: value.Model}
}

type stateCandidateError struct {
	Category string
	Code     string
	Stage    string
	Reason   string
	App      string
	Model    string
	Field    string
	Path     string
}

func (e *stateCandidateError) Error() string {
	if e == nil {
		return "migration relation state candidate error"
	}
	return fmt.Sprintf(
		"%s/%s stage=%s reason=%s app=%s model=%s field=%s path=%s",
		e.Category,
		e.Code,
		e.Stage,
		e.Reason,
		e.App,
		e.Model,
		e.Field,
		e.Path,
	)
}

type stateProjectState struct {
	formatVersion int
	apps          map[string]ir.Schema
}

type stateStepDirection uint8

const (
	stateStepForward stateStepDirection = iota + 1
	stateStepBackward
)

type stateStepOperationKind uint8

const (
	stateStepOperationScalar stateStepOperationKind = iota
	stateStepOperationRelationAdd
	stateStepOperationRelationRemove
)

// stateStepOperation carries only the exact whole-project snapshots on the two
// sides of one decoded historical operation. Operation kind and relation
// bearing are derived during binding; callers have no classification knob.
type stateStepOperation struct {
	Before stateProjectState
	After  stateProjectState
}

type statePreparedOperation struct {
	before stateProjectState
	after  stateProjectState
	kind   stateStepOperationKind
}

type statePreparedGraphToken struct {
	marker byte
}

// statePreparedMigrationDefinition is the only authoritative replay input. It
// is bound from one loader-published record and its canonical forward
// predecessor; profile, source format, operation classification, definition
// identity, and snapshots cannot be supplied independently at replay time.
type statePreparedMigrationDefinition struct {
	sourceID        string
	producer        profileProducer
	profile         profileCompatibility
	key             migrations.MigrationKey
	definition      profileDefinition
	definitionHash  string
	provenanceSeal  string
	setDigest       string
	graphToken      *statePreparedGraphToken
	sourceFormat    int
	relationBearing bool
	predecessor     stateProjectState
	forwardResult   stateProjectState
	operations      []statePreparedOperation
	verify          func(statePreparedMigrationDefinition) bool
}

// stateStepTrace is test-only transition evidence. Formats records the input,
// the one optional pre-step promotion, every replayed operation boundary, and
// the one optional post-step demotion. It deliberately has no backend/session
// counters: this candidate proves historical-state behavior only.
type stateStepTrace struct {
	SourceID        string
	Key             migrations.MigrationKey
	RelationProfile bool
	Promotions      int
	Demotions       int
	Operations      int
	Formats         []int
}

func stateNewProject(formatVersion int, schemas ...ir.Schema) (stateProjectState, error) {
	if failure := stateValidateSchemaSliceResources(schemas); failure != nil {
		return stateProjectState{}, failure
	}
	candidate := stateProjectState{
		formatVersion: formatVersion,
		apps:          make(map[string]ir.Schema, len(schemas)),
	}
	snapshots := make([]ir.Schema, len(schemas))
	for index := range schemas {
		snapshots[index] = schemas[index].Clone()
	}
	sort.SliceStable(snapshots, func(left, right int) bool {
		return snapshots[left].AppLabel < snapshots[right].AppLabel
	})
	for _, schema := range snapshots {
		if _, exists := candidate.apps[schema.AppLabel]; exists {
			return stateProjectState{}, stateFailure(
				"duplicate_app",
				"duplicate_app",
				schema.AppLabel,
				"",
				"",
				"app_label",
			)
		}
		candidate.apps[schema.AppLabel] = schema.Clone()
	}
	if failure := stateValidate(candidate); failure != nil {
		return stateProjectState{}, failure
	}
	return candidate.stateClone(), nil
}

func (s stateProjectState) stateFormatVersion() int {
	return s.formatVersion
}

func (s stateProjectState) stateApps() []string {
	apps := make([]string, 0, len(s.apps))
	for app := range s.apps {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	return apps
}

func (s stateProjectState) stateSchema(app string) (ir.Schema, bool) {
	schema, exists := s.apps[app]
	if !exists {
		return ir.Schema{}, false
	}
	return schema.Clone(), true
}

func (s stateProjectState) stateModel(app, model string) (ir.Model, bool) {
	schema, exists := s.apps[app]
	if !exists {
		return ir.Model{}, false
	}
	for _, candidate := range schema.Models {
		if candidate.Name == model {
			return candidate.Clone(), true
		}
	}
	return ir.Model{}, false
}

func (s stateProjectState) stateClone() stateProjectState {
	clone := stateProjectState{
		formatVersion: s.formatVersion,
		apps:          make(map[string]ir.Schema, len(s.apps)),
	}
	for app, schema := range s.apps {
		clone.apps[app] = schema.Clone()
	}
	return clone
}

func statePromote(value stateProjectState) (stateProjectState, error) {
	if failure := stateValidate(value); failure != nil {
		return stateProjectState{}, failure
	}
	if value.formatVersion != stateFormatScalar {
		return stateProjectState{}, stateFailure(
			"promotion_source_version",
			"promotion_requires_state_1",
			"",
			"",
			"",
			"format_version",
		)
	}
	promoted := value.stateClone()
	promoted.formatVersion = stateFormatRelation
	for app, schema := range promoted.apps {
		schema.FormatVersion = ir.RelationFormatVersion
		promoted.apps[app] = schema
	}
	if failure := stateValidate(promoted); failure != nil {
		return stateProjectState{}, failure
	}
	return promoted.stateClone(), nil
}

func stateDemote(value stateProjectState) (stateProjectState, error) {
	if failure := stateValidate(value); failure != nil {
		return stateProjectState{}, failure
	}
	if value.formatVersion != stateFormatRelation {
		return stateProjectState{}, stateFailure(
			"demotion_source_version",
			"demotion_requires_state_2",
			"",
			"",
			"",
			"format_version",
		)
	}
	if app, model, field, exists := stateCanonicalFirstRelation(value); exists {
		return stateProjectState{}, stateFailure(
			"relation_state_demotion_rejected",
			"relation_present",
			app,
			model,
			field,
			"relation",
		)
	}
	demoted := value.stateClone()
	demoted.formatVersion = stateFormatScalar
	for app, schema := range demoted.apps {
		schema.FormatVersion = ir.FormatVersion
		demoted.apps[app] = schema
	}
	if failure := stateValidate(demoted); failure != nil {
		return stateProjectState{}, failure
	}
	return demoted.stateClone(), nil
}

// statePreparedBuilder is created only after stateNewPreparedReconstructor has
// resource-checked and privately cloned the loader set and the complete
// snapshot graph. Its derive method accepts only a planned key and the
// canonical predecessor produced by replaying the preceding Planner steps; no
// caller-visible published record can be supplied independently. It returns an
// unsealed value; the verifier closure is minted only inside the constructor.
type statePreparedBuilder struct {
	records   map[migrations.MigrationKey]profilePublishedDefinition
	snapshots map[migrations.MigrationKey][]stateStepOperation
}

func (builder statePreparedBuilder) derive(
	key migrations.MigrationKey,
	predecessor stateProjectState,
) (statePreparedMigrationDefinition, *stateCandidateError) {
	published, exists := builder.records[key]
	if !exists {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_definition_missing", "planned_definition_missing", key.App, "", "", "definition")
	}
	operations, exists := builder.snapshots[key]
	if !exists {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_snapshot_graph_mismatch", "complete_definition_snapshot_graph_required", key.App, "", "", "snapshots")
	}
	if len(published.SourceID) == 0 || !utf8.ValidString(published.SourceID) || len(published.SourceID) > definition.MaxSourceIDBytes {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_source_invalid", "loader_source_provenance_required", "", "", "", "source_id")
	}
	if published.Producer.Name == "" || published.Producer.Version == "" ||
		len(published.Producer.Name) > definition.MaxSourceIDBytes || len(published.Producer.Version) > definition.MaxSourceIDBytes {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_producer_invalid", "loader_producer_provenance_required", "", "", "", "producer")
	}
	decoder, profileFailure := profileDispatch(published.Profile)
	if profileFailure != nil {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_profile_invalid", "exact_loader_profile_required", "", "", "", "profile")
	}
	if published.Definition.App == "" || published.Definition.Name == "" {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_key_invalid", "migration_key_required", "", "", "", "definition")
	}
	if (migrations.MigrationKey{App: published.Definition.App, Name: published.Definition.Name}) != key {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_key_mismatch", "planned_definition_key_mismatch", key.App, "", "", "definition")
	}
	canonicalDefinition, _, canonicalFailure := profileCanonicalDefinition(published.Definition, decoder)
	if canonicalFailure != nil {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_definition_invalid", "loader_definition_semantics_invalid", "", "", "", "definition")
	}
	provenanceSeal, sealErr := profilePublishedProvenanceSeal(
		published.SourceID,
		published.Producer,
		published.Profile,
		canonicalDefinition,
	)
	if sealErr != nil || published.provenanceSeal == "" || provenanceSeal != published.provenanceSeal {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_loader_record_invalid", "loader_record_provenance_mismatch", "", "", "", "definition")
	}
	if len(published.Definition.Operations) > definition.MaxOperationsPerMigration || len(operations) > definition.MaxOperationsPerMigration {
		return statePreparedMigrationDefinition{}, stateResourceFailure("operation_count_exceeds_profile_limit", "", "", "", "operations")
	}
	if len(operations) != len(published.Definition.Operations) {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_operation_count_mismatch", "definition_snapshot_count_mismatch", "", "", "", "operations")
	}
	if failure := stateValidatePublishedDefinitionResources(published); failure != nil {
		return statePreparedMigrationDefinition{}, failure
	}
	if failure := stateValidateProjectResources(predecessor); failure != nil {
		failure.Path = "predecessor." + failure.Path
		return statePreparedMigrationDefinition{}, failure
	}
	if failure := stateValidateStepResources(predecessor, operations); failure != nil {
		failure.Path = "operations." + failure.Path
		return statePreparedMigrationDefinition{}, failure
	}
	if failure := stateValidate(predecessor); failure != nil {
		failure.Path = "predecessor." + failure.Path
		return statePreparedMigrationDefinition{}, failure
	}
	for index := range operations {
		if failure := stateValidate(operations[index].Before); failure != nil {
			failure.Path = fmt.Sprintf("operations[%d].before.%s", index, failure.Path)
			return statePreparedMigrationDefinition{}, failure
		}
		if failure := stateValidate(operations[index].After); failure != nil {
			failure.Path = fmt.Sprintf("operations[%d].after.%s", index, failure.Path)
			return statePreparedMigrationDefinition{}, failure
		}
	}

	preparedOperations := make([]statePreparedOperation, len(operations))
	relationBearing := false
	for index := range operations {
		kind, app, model, field, valid := stateDerivedOperationKind(operations[index].Before, operations[index].After)
		if !valid {
			return statePreparedMigrationDefinition{}, stateFailure(
				"step_relation_delta_unsupported", "relation_delta_is_not_one_directional",
				app, model, field, fmt.Sprintf("operations[%d]", index),
			)
		}
		relationBearing = relationBearing || kind != stateStepOperationScalar
		preparedOperations[index] = statePreparedOperation{
			before: operations[index].Before.stateClone(), after: operations[index].After.stateClone(), kind: kind,
		}
	}
	if relationBearing && published.Profile != profileRelationTuple {
		return statePreparedMigrationDefinition{}, stateFailure(
			"step_profile_incompatible", "relation_arm_requires_relation_profile", "", "", "", "profile",
		)
	}
	for index := range operations {
		if failure := stateDefinitionOperationMatchesSnapshot(
			published.Definition.App, published.Definition.Operations[index], decoder, operations[index], index,
		); failure != nil {
			return statePreparedMigrationDefinition{}, failure
		}
	}

	working := predecessor.stateClone()
	if relationBearing && working.stateFormatVersion() == stateFormatScalar {
		promoted, err := statePromote(working)
		if err != nil {
			return statePreparedMigrationDefinition{}, stateFailure("prepared_promotion_failed", "canonical_predecessor_promotion_failed", "", "", "", "predecessor")
		}
		working = promoted
	}
	for index := range operations {
		if operations[index].Before.stateFormatVersion() != working.stateFormatVersion() || operations[index].After.stateFormatVersion() != working.stateFormatVersion() {
			return statePreparedMigrationDefinition{}, stateFailure(
				"step_operation_format_mismatch", "operation_snapshot_format_mismatch", "", "", "", fmt.Sprintf("operations[%d].format_version", index),
			)
		}
		if !stateProjectEqual(working, operations[index].Before) {
			return statePreparedMigrationDefinition{}, stateFailure(
				"prepared_predecessor_mismatch", "canonical_forward_predecessor_mismatch", "", "", "", fmt.Sprintf("operations[%d].before", index),
			)
		}
		working = operations[index].After.stateClone()
	}
	if relationBearing && working.stateFormatVersion() == stateFormatRelation && stateRelationFieldCount(working) == 0 &&
		predecessor.stateFormatVersion() == stateFormatScalar {
		demoted, err := stateDemote(working)
		if err != nil {
			return statePreparedMigrationDefinition{}, stateFailure("prepared_demotion_failed", "canonical_forward_result_demotion_failed", "", "", "", "operations")
		}
		working = demoted
	}

	definitionHash, hashFailure := statePreparedDefinitionHash(published.Profile, published.Definition, decoder)
	if hashFailure != nil {
		return statePreparedMigrationDefinition{}, hashFailure
	}
	prepared := statePreparedMigrationDefinition{
		sourceID: published.SourceID, producer: published.Producer, profile: published.Profile,
		key:        migrations.MigrationKey{App: published.Definition.App, Name: published.Definition.Name},
		definition: profileCloneDefinition(published.Definition), definitionHash: definitionHash, provenanceSeal: provenanceSeal,
		sourceFormat: predecessor.stateFormatVersion(), relationBearing: relationBearing,
		predecessor: predecessor.stateClone(), forwardResult: working.stateClone(), operations: preparedOperations,
	}
	return prepared, nil
}

// stateReplayMigrationStep accepts no profile, source-format, operation-kind,
// or snapshot pairing knobs. Forward and backward reuse the same sealed
// prepared definition.
func stateReplayMigrationStep(
	incoming stateProjectState,
	prepared statePreparedMigrationDefinition,
	direction stateStepDirection,
) (stateProjectState, stateStepTrace, error) {
	trace := stateStepTrace{
		SourceID: prepared.sourceID, Key: prepared.key,
		RelationProfile: prepared.profile == profileRelationTuple,
		Formats:         []int{incoming.stateFormatVersion()},
	}
	if direction != stateStepForward && direction != stateStepBackward {
		return stateProjectState{}, trace, stateFailure("step_direction_invalid", "step_direction_invalid", "", "", "", "direction")
	}
	if failure := stateValidatePreparedDefinition(prepared); failure != nil {
		return stateProjectState{}, trace, failure
	}
	if failure := stateValidateProjectResources(incoming); failure != nil {
		failure.Path = "incoming." + failure.Path
		return stateProjectState{}, trace, failure
	}
	if failure := stateValidate(incoming); failure != nil {
		failure.Path = "incoming." + failure.Path
		return stateProjectState{}, trace, failure
	}
	expectedIncoming := prepared.predecessor
	if direction == stateStepBackward {
		expectedIncoming = prepared.forwardResult
	}
	if !stateProjectEqual(incoming, expectedIncoming) {
		return stateProjectState{}, trace, stateFailure(
			"step_boundary_state_mismatch", "sealed_migration_boundary_mismatch", "", "", "", "incoming",
		)
	}

	current := incoming.stateClone()
	if prepared.relationBearing && current.stateFormatVersion() == stateFormatScalar {
		promoted, err := statePromote(current)
		if err != nil {
			return stateProjectState{}, trace, err
		}
		current = promoted
		trace.Promotions++
		trace.Formats = append(trace.Formats, current.stateFormatVersion())
	}

	for offset := range prepared.operations {
		index := offset
		if direction == stateStepBackward {
			index = len(prepared.operations) - 1 - offset
		}
		expected := prepared.operations[index].before
		next := prepared.operations[index].after
		if direction == stateStepBackward {
			expected, next = next, expected
		}
		if !stateProjectEqual(current, expected) {
			return stateProjectState{}, trace, stateFailure(
				"step_before_state_mismatch",
				"exact_operation_before_state_mismatch",
				"",
				"",
				"",
				fmt.Sprintf("operations[%d]", index),
			)
		}
		// Each arm was already resource-checked and normalized in its declared
		// state version. Clone again so neither caller nor adjacent operations
		// can alias the published historical state.
		current = next.stateClone()
		trace.Operations++
		trace.Formats = append(trace.Formats, current.stateFormatVersion())
	}

	if prepared.relationBearing && current.stateFormatVersion() == stateFormatRelation && stateRelationFieldCount(current) == 0 &&
		prepared.sourceFormat == stateFormatScalar {
		demoted, err := stateDemote(current)
		if err != nil {
			return stateProjectState{}, trace, err
		}
		current = demoted
		trace.Demotions++
		trace.Formats = append(trace.Formats, current.stateFormatVersion())
	}
	return current.stateClone(), trace, nil
}

func stateValidatePublishedDefinitionResources(published profilePublishedDefinition) *stateCandidateError {
	check := func(value, path string) *stateCandidateError {
		if len(value) > definition.MaxSourceIDBytes {
			return stateResourceFailure("identifier_bytes_exceed_profile_limit", "", "", "", path)
		}
		return nil
	}
	for _, value := range []struct{ text, path string }{
		{published.SourceID, "source_id"}, {published.Producer.Name, "producer.name"},
		{published.Producer.Version, "producer.version"}, {published.Definition.App, "definition.app"},
		{published.Definition.Name, "definition.name"},
	} {
		if failure := check(value.text, value.path); failure != nil {
			return failure
		}
	}
	if len(published.Definition.Dependencies) > definition.MaxDependenciesPerMigration {
		return stateResourceFailure("dependency_count_exceeds_profile_limit", "", "", "", "definition.dependencies")
	}
	for index, dependency := range published.Definition.Dependencies {
		if failure := check(dependency.App, fmt.Sprintf("definition.dependencies[%d].app", index)); failure != nil {
			return failure
		}
		if failure := check(dependency.Name, fmt.Sprintf("definition.dependencies[%d].name", index)); failure != nil {
			return failure
		}
	}
	for operationIndex, operation := range published.Definition.Operations {
		for _, value := range []struct{ text, path string }{
			{operation.Kind, "kind"}, {operation.AppLabel, "app_label"}, {operation.ModelName, "model_name"},
		} {
			if failure := check(value.text, fmt.Sprintf("definition.operations[%d].%s", operationIndex, value.path)); failure != nil {
				return failure
			}
		}
		if operation.Model != nil {
			if len(operation.Model.Fields) > definition.MaxFieldsPerCreateModel {
				return stateResourceFailure("model_field_count_exceeds_profile_limit", published.Definition.App, operation.Model.Name, "", fmt.Sprintf("definition.operations[%d].model.fields", operationIndex))
			}
			for fieldIndex := range operation.Model.Fields {
				if failure := stateValidatePublishedFieldResources(operation.Model.Fields[fieldIndex], fmt.Sprintf("definition.operations[%d].model.fields[%d]", operationIndex, fieldIndex)); failure != nil {
					return failure
				}
			}
		}
		if operation.Field != nil {
			if failure := stateValidatePublishedFieldResources(*operation.Field, fmt.Sprintf("definition.operations[%d].field", operationIndex)); failure != nil {
				return failure
			}
		}
	}
	if nodes, bytes, nodeOverflow, byteOverflow := statePublishedDefinitionAggregate(published); nodeOverflow || nodes > definition.MaxJSONValues {
		return stateResourceFailure("published_definition_nodes_exceed_profile_limit", "", "", "", "definition")
	} else if byteOverflow || bytes > definition.MaxDocumentBytes {
		return stateResourceFailure("published_definition_bytes_exceed_profile_limit", "", "", "", "definition")
	}
	return nil
}

func statePublishedDefinitionAggregate(published profilePublishedDefinition) (int, int, bool, bool) {
	nodes := 4 // source, producer, profile, definition
	bytes := 0
	nodeOverflow := false
	byteOverflow := false
	consumeNodes := func(count int) {
		if count < 0 || nodes > definition.MaxJSONValues-count {
			nodeOverflow = true
			return
		}
		nodes += count
	}
	consumeString := func(value string) {
		if bytes > definition.MaxDocumentBytes-len(value) {
			byteOverflow = true
			return
		}
		bytes += len(value)
	}
	consumeString(published.SourceID)
	consumeString(published.Producer.Name)
	consumeString(published.Producer.Version)
	consumeString(published.Definition.App)
	consumeString(published.Definition.Name)
	consumeNodes(len(published.Definition.Dependencies) + len(published.Definition.Operations))
	for _, dependency := range published.Definition.Dependencies {
		consumeString(dependency.App)
		consumeString(dependency.Name)
	}
	consumeField := func(field profileField) {
		consumeNodes(1)
		consumeString(field.Name)
		consumeString(field.GoName)
		consumeString(field.Column)
		consumeString(field.Kind)
		if field.Default != nil {
			consumeNodes(1)
			consumeString(field.Default.Kind)
			if field.Default.String != nil {
				consumeString(*field.Default.String)
			}
		}
		if field.Relation != nil {
			consumeNodes(3) // relation, target, reverse
			consumeString(field.Relation.Target.App)
			consumeString(field.Relation.Target.Model)
			consumeString(field.Relation.Cardinality)
			consumeString(field.Relation.Reverse.Name)
			consumeString(field.Relation.OnDelete)
		}
	}
	for _, operation := range published.Definition.Operations {
		consumeString(operation.Kind)
		consumeString(operation.AppLabel)
		consumeString(operation.ModelName)
		if operation.Model != nil {
			consumeNodes(1)
			consumeString(operation.Model.Name)
			consumeString(operation.Model.GoName)
			consumeString(operation.Model.DBTable)
			for _, field := range operation.Model.Fields {
				consumeField(field)
			}
		}
		if operation.Field != nil {
			consumeField(*operation.Field)
		}
	}
	return nodes, bytes, nodeOverflow, byteOverflow
}

func stateValidatePublishedFieldResources(field profileField, path string) *stateCandidateError {
	for _, value := range []struct{ text, suffix string }{
		{field.Name, "name"}, {field.GoName, "go_name"}, {field.Column, "column"}, {field.Kind, "kind"},
	} {
		if len(value.text) > definition.MaxSourceIDBytes {
			return stateResourceFailure("identifier_bytes_exceed_profile_limit", "", "", field.Name, path+"."+value.suffix)
		}
	}
	if field.Default != nil && field.Default.String != nil && len(*field.Default.String) > definition.MaxDocumentBytes {
		return stateResourceFailure("default_payload_bytes_exceed_profile_limit", "", "", field.Name, path+".default.string")
	}
	if field.Relation != nil {
		for _, value := range []struct{ text, suffix string }{
			{field.Relation.Target.App, "target.app_label"}, {field.Relation.Target.Model, "target.model_name"},
			{field.Relation.Cardinality, "cardinality"}, {field.Relation.Reverse.Name, "reverse.name"}, {field.Relation.OnDelete, "on_delete"},
		} {
			if len(value.text) > definition.MaxSourceIDBytes {
				return stateResourceFailure("identifier_bytes_exceed_profile_limit", "", "", field.Name, path+".relation."+value.suffix)
			}
		}
	}
	return nil
}

func stateDefinitionOperationMatchesSnapshot(
	definitionApp string,
	operation profileOperation,
	decoder profileDecoder,
	snapshot stateStepOperation,
	index int,
) *stateCandidateError {
	_, decoded, failure := profileCanonicalOperationValue(operation, definitionApp, decoder)
	if failure != nil {
		return stateFailure("prepared_definition_invalid", "loader_definition_operation_invalid", "", "", "", fmt.Sprintf("definition.operations[%d]", index))
	}
	next := snapshot.Before.stateClone()
	switch current := decoded.(type) {
	case migrations.CreateModel:
		schema, exists := next.apps[current.AppLabel]
		if !exists {
			format := ir.FormatVersion
			if next.stateFormatVersion() == stateFormatRelation {
				format = ir.RelationFormatVersion
			}
			schema = ir.Schema{FormatVersion: format, AppLabel: current.AppLabel, Models: []ir.Model{}}
		}
		for _, model := range schema.Models {
			if model.Name == current.Model.Name {
				return stateFailure("prepared_operation_cross_pair", "definition_snapshot_semantic_mismatch", current.AppLabel, current.Model.Name, "", fmt.Sprintf("operations[%d]", index))
			}
		}
		schema.Models = append(schema.Models, current.Model.Clone())
		next.apps[current.AppLabel] = schema
	case migrations.AddField:
		schema, exists := next.apps[current.AppLabel]
		if !exists {
			return stateFailure("prepared_operation_cross_pair", "definition_snapshot_semantic_mismatch", current.AppLabel, current.ModelName, current.Field.Name, fmt.Sprintf("operations[%d]", index))
		}
		found := false
		for modelIndex := range schema.Models {
			if schema.Models[modelIndex].Name != current.ModelName {
				continue
			}
			for _, field := range schema.Models[modelIndex].Fields {
				if field.Name == current.Field.Name {
					return stateFailure("prepared_operation_cross_pair", "definition_snapshot_semantic_mismatch", current.AppLabel, current.ModelName, current.Field.Name, fmt.Sprintf("operations[%d]", index))
				}
			}
			schema.Models[modelIndex].Fields = append(schema.Models[modelIndex].Fields, current.Field.Clone())
			found = true
			break
		}
		if !found {
			return stateFailure("prepared_operation_cross_pair", "definition_snapshot_semantic_mismatch", current.AppLabel, current.ModelName, current.Field.Name, fmt.Sprintf("operations[%d]", index))
		}
		next.apps[current.AppLabel] = schema
	default:
		return stateFailure("prepared_definition_invalid", "loader_definition_operation_invalid", "", "", "", fmt.Sprintf("definition.operations[%d]", index))
	}
	if !stateProjectEqual(next, snapshot.After) {
		return stateFailure("prepared_operation_cross_pair", "definition_snapshot_semantic_mismatch", "", "", "", fmt.Sprintf("operations[%d]", index))
	}
	return nil
}

func statePreparedDefinitionHash(profile profileCompatibility, value profileDefinition, decoder profileDecoder) (string, *stateCandidateError) {
	canonical, _, failure := profileCanonicalDefinition(value, decoder)
	if failure != nil {
		return "", stateFailure("prepared_definition_invalid", "loader_definition_semantics_invalid", "", "", "", "definition")
	}
	encoded, err := profileCanonicalJSON(map[string]any{
		"definition": canonical, "profile": profileCompatibilityValue(profile), "domain": "godj:migration-state-prepared-definition:v1",
	})
	if err != nil {
		return "", stateFailure("prepared_definition_invalid", "definition_identity_failed", "", "", "", "definition")
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func stateClonePreparedMigrationDefinition(prepared statePreparedMigrationDefinition) statePreparedMigrationDefinition {
	clone := statePreparedMigrationDefinition{
		sourceID: prepared.sourceID, producer: prepared.producer, profile: prepared.profile,
		key: prepared.key, definition: profileCloneDefinition(prepared.definition),
		definitionHash: prepared.definitionHash, provenanceSeal: prepared.provenanceSeal,
		setDigest: prepared.setDigest, graphToken: prepared.graphToken,
		sourceFormat: prepared.sourceFormat, relationBearing: prepared.relationBearing,
		predecessor: prepared.predecessor.stateClone(), forwardResult: prepared.forwardResult.stateClone(),
		operations: make([]statePreparedOperation, len(prepared.operations)), verify: prepared.verify,
	}
	for index := range prepared.operations {
		clone.operations[index] = statePreparedOperation{
			before: prepared.operations[index].before.stateClone(),
			after:  prepared.operations[index].after.stateClone(),
			kind:   prepared.operations[index].kind,
		}
	}
	return clone
}

func statePreparedDefinitionEqual(left, right statePreparedMigrationDefinition) bool {
	if left.sourceID != right.sourceID || left.producer != right.producer || left.profile != right.profile ||
		left.key != right.key || !reflect.DeepEqual(left.definition, right.definition) ||
		left.definitionHash != right.definitionHash || left.provenanceSeal != right.provenanceSeal ||
		left.setDigest != right.setDigest || left.graphToken == nil || left.graphToken != right.graphToken ||
		left.sourceFormat != right.sourceFormat || left.relationBearing != right.relationBearing ||
		!stateProjectEqual(left.predecessor, right.predecessor) || !stateProjectEqual(left.forwardResult, right.forwardResult) ||
		len(left.operations) != len(right.operations) {
		return false
	}
	for index := range left.operations {
		if left.operations[index].kind != right.operations[index].kind ||
			!stateProjectEqual(left.operations[index].before, right.operations[index].before) ||
			!stateProjectEqual(left.operations[index].after, right.operations[index].after) {
			return false
		}
	}
	return true
}

func stateValidatePreparedDefinition(prepared statePreparedMigrationDefinition) *stateCandidateError {
	if len(prepared.sourceID) == 0 || !utf8.ValidString(prepared.sourceID) || len(prepared.sourceID) > definition.MaxSourceIDBytes {
		return stateFailure("prepared_source_invalid", "loader_source_provenance_required", "", "", "", "source_id")
	}
	if prepared.producer.Name == "" || prepared.producer.Version == "" ||
		len(prepared.producer.Name) > definition.MaxSourceIDBytes || len(prepared.producer.Version) > definition.MaxSourceIDBytes {
		return stateFailure("prepared_producer_invalid", "loader_producer_provenance_required", "", "", "", "producer")
	}
	decoder, profileFailure := profileDispatch(prepared.profile)
	if profileFailure != nil {
		return stateFailure("prepared_profile_invalid", "exact_loader_profile_required", "", "", "", "profile")
	}
	if prepared.key.App == "" || prepared.key.Name == "" || prepared.definition.App != prepared.key.App || prepared.definition.Name != prepared.key.Name {
		return stateFailure("prepared_key_invalid", "migration_key_required", "", "", "", "definition")
	}
	if len(prepared.operations) > definition.MaxOperationsPerMigration {
		return stateResourceFailure("operation_count_exceeds_profile_limit", "", "", "", "operations")
	}
	if prepared.setDigest == "" || prepared.graphToken == nil {
		return stateFailure("prepared_definition_seal_mismatch", "prepared_definition_identity_mismatch", "", "", "", "prepared_definition")
	}
	published := profilePublishedDefinition{
		SourceID: prepared.sourceID, Producer: prepared.producer, Profile: prepared.profile,
		Definition: prepared.definition, provenanceSeal: prepared.provenanceSeal,
	}
	if failure := stateValidatePublishedDefinitionResources(published); failure != nil {
		return failure
	}
	snapshots := make([]stateStepOperation, len(prepared.operations))
	for index := range prepared.operations {
		snapshots[index] = stateStepOperation{
			Before: prepared.operations[index].before, After: prepared.operations[index].after,
		}
	}
	if failure := stateValidateStepResources(prepared.predecessor, snapshots); failure != nil {
		failure.Path = "prepared." + failure.Path
		return failure
	}
	if failure := stateValidateProjectResources(prepared.forwardResult); failure != nil {
		failure.Path = "prepared.forward_result." + failure.Path
		return failure
	}
	definitionHash, hashFailure := statePreparedDefinitionHash(prepared.profile, prepared.definition, decoder)
	if hashFailure != nil {
		return hashFailure
	}
	canonicalDefinition, _, canonicalFailure := profileCanonicalDefinition(prepared.definition, decoder)
	if canonicalFailure != nil {
		return stateFailure("prepared_definition_invalid", "loader_definition_semantics_invalid", "", "", "", "definition")
	}
	provenanceSeal, sealErr := profilePublishedProvenanceSeal(
		prepared.sourceID,
		prepared.producer,
		prepared.profile,
		canonicalDefinition,
	)
	if sealErr != nil || definitionHash != prepared.definitionHash || provenanceSeal != prepared.provenanceSeal ||
		prepared.verify == nil || !prepared.verify(prepared) {
		return stateFailure("prepared_definition_seal_mismatch", "prepared_definition_identity_mismatch", "", "", "", "prepared_definition")
	}
	return nil
}

type statePreparedReconstructor struct {
	planner     migrations.Planner
	definitions map[migrations.MigrationKey]statePreparedMigrationDefinition
	order       []migrations.MigrationKey
	empty       stateProjectState
	setDigest   string
}

func stateNewPreparedReconstructor(
	set profileSet,
	snapshots map[migrations.MigrationKey][]stateStepOperation,
) (statePreparedReconstructor, error) {
	if len(set.definitions) > definition.MaxSources {
		return statePreparedReconstructor{}, stateResourceFailure("definition_count_exceeds_profile_limit", "", "", "", "definitions")
	}
	// Loader-owned definitions and caller-owned snapshots are both resource
	// bounded before either graph is cloned. All later work uses only these
	// private snapshots, so caller mutation cannot race semantic pairing or mint
	// a different handoff.
	for index := range set.definitions {
		if failure := stateValidatePublishedDefinitionResources(set.definitions[index]); failure != nil {
			failure.Path = fmt.Sprintf("definitions[%d].%s", index, failure.Path)
			return statePreparedReconstructor{}, failure
		}
	}
	if failure := stateValidateReconstructorSnapshotResources(snapshots); failure != nil {
		failure.Path = "snapshots." + failure.Path
		return statePreparedReconstructor{}, failure
	}
	records := make([]profilePublishedDefinition, len(set.definitions))
	for index := range set.definitions {
		records[index] = profileClonePublishedDefinition(set.definitions[index])
	}
	privateSnapshots := stateCloneReconstructorSnapshots(snapshots)

	migrationValues := make([]migrations.Migration, len(records))
	byKey := make(map[migrations.MigrationKey]profilePublishedDefinition, len(records))
	children := make(map[migrations.MigrationKey]int, len(records))
	canonicalItems := make([]any, len(records))
	canonicalDefinitions := make([]any, len(records))
	allLegacy := true
	for index := range records {
		decoder, failure := profileDispatch(records[index].Profile)
		if failure != nil {
			return statePreparedReconstructor{}, stateFailure("prepared_profile_invalid", "exact_loader_profile_required", "", "", "", "profile")
		}
		canonicalDefinition, migrationValue, semanticFailure := profileCanonicalDefinition(records[index].Definition, decoder)
		if semanticFailure != nil {
			return statePreparedReconstructor{}, stateFailure("prepared_definition_invalid", "loader_definition_semantics_invalid", "", "", "", "definition")
		}
		provenanceSeal, sealErr := profilePublishedProvenanceSeal(
			records[index].SourceID,
			records[index].Producer,
			records[index].Profile,
			canonicalDefinition,
		)
		if sealErr != nil || records[index].provenanceSeal == "" || provenanceSeal != records[index].provenanceSeal {
			return statePreparedReconstructor{}, stateFailure("prepared_loader_record_invalid", "loader_record_provenance_mismatch", "", "", "", "definition")
		}
		canonicalDefinitions[index] = canonicalDefinition
		canonicalItems[index] = map[string]any{
			"definition": canonicalDefinition,
			"profile":    profileCompatibilityValue(records[index].Profile),
		}
		allLegacy = allLegacy && records[index].Profile == profileLegacy
		key := migrationValue.Key()
		if _, exists := byKey[key]; exists {
			return statePreparedReconstructor{}, stateFailure("prepared_key_duplicate", "loader_definition_key_duplicate", key.App, "", "", "definition")
		}
		byKey[key] = records[index]
		migrationValues[index] = migrationValue
		if len(migrationValue.Dependencies) > 1 {
			return statePreparedReconstructor{}, stateFailure("prepared_graph_shape_unsupported", "mini_reconstructor_requires_linear_graph", key.App, "", "", "definition.dependencies")
		}
		for _, dependency := range migrationValue.Dependencies {
			children[dependency]++
			if children[dependency] > 1 {
				return statePreparedReconstructor{}, stateFailure("prepared_graph_shape_unsupported", "mini_reconstructor_requires_linear_graph", dependency.App, "", "", "definition.dependencies")
			}
		}
	}
	canonicalValue := map[string]any{"definitions": canonicalItems, "domain": profileMixedDigestDomain}
	if allLegacy {
		canonicalValue = map[string]any{
			"compatibility": profileCompatibilityValue(profileLegacy),
			"definitions":   canonicalDefinitions,
			"domain":        profileLegacyDigestDomain,
		}
	}
	canonical, canonicalErr := profileCanonicalJSON(canonicalValue)
	if canonicalErr != nil {
		return statePreparedReconstructor{}, canonicalErr
	}
	canonicalSum := sha256.Sum256(canonical)
	canonicalDigest := "sha256:" + hex.EncodeToString(canonicalSum[:])
	if !reflect.DeepEqual(canonical, set.canonical) || canonicalDigest != set.digest {
		return statePreparedReconstructor{}, stateFailure("prepared_set_invalid", "loader_set_semantic_seal_mismatch", "", "", "", "definitions")
	}
	if len(privateSnapshots) != len(byKey) {
		return statePreparedReconstructor{}, stateFailure("prepared_snapshot_graph_mismatch", "complete_definition_snapshot_graph_required", "", "", "", "snapshots")
	}
	for key := range privateSnapshots {
		if _, exists := byKey[key]; !exists {
			return statePreparedReconstructor{}, stateFailure("prepared_snapshot_graph_mismatch", "complete_definition_snapshot_graph_required", key.App, "", "", "snapshots")
		}
	}
	planner, err := migrations.NewPlanner(migrationValues...)
	if err != nil {
		return statePreparedReconstructor{}, err
	}
	emptyApplied, err := migrations.NewAppliedState()
	if err != nil {
		return statePreparedReconstructor{}, err
	}
	if err := planner.CheckHistory(emptyApplied); err != nil {
		return statePreparedReconstructor{}, err
	}
	leaves := make([]migrations.MigrationKey, 0)
	for key := range byKey {
		if children[key] == 0 {
			leaves = append(leaves, key)
		}
	}
	if len(records) != 0 && len(leaves) != 1 {
		return statePreparedReconstructor{}, stateFailure("prepared_graph_shape_unsupported", "mini_reconstructor_requires_linear_graph", "", "", "", "definition")
	}
	sort.Slice(leaves, func(left, right int) bool {
		if leaves[left].App != leaves[right].App {
			return leaves[left].App < leaves[right].App
		}
		return leaves[left].Name < leaves[right].Name
	})
	targets := make([]migrations.Target, len(leaves))
	for index := range leaves {
		targets[index] = migrations.NamedTarget(leaves[index])
	}
	plan, err := planner.Plan(emptyApplied, targets...)
	if err != nil {
		return statePreparedReconstructor{}, err
	}
	empty, failure := stateNewProject(stateFormatScalar)
	if failure != nil {
		return statePreparedReconstructor{}, failure
	}
	reconstructor := statePreparedReconstructor{
		planner: planner, definitions: make(map[migrations.MigrationKey]statePreparedMigrationDefinition, len(records)),
		empty: empty, setDigest: set.profileDigest(),
	}
	builder := statePreparedBuilder{records: byKey, snapshots: privateSnapshots}
	graphToken := &statePreparedGraphToken{marker: 1}
	current := empty
	for _, step := range plan {
		if step.Direction != migrations.DirectionForward {
			return statePreparedReconstructor{}, stateFailure("prepared_plan_invalid", "canonical_definition_plan_not_forward", step.Key.App, "", "", "plan")
		}
		prepared, prepareFailure := builder.derive(step.Key, current)
		if prepareFailure != nil {
			return statePreparedReconstructor{}, prepareFailure
		}
		prepared.setDigest = canonicalDigest
		prepared.graphToken = graphToken
		binding := stateClonePreparedMigrationDefinition(prepared)
		binding.verify = nil
		prepared.verify = func(candidate statePreparedMigrationDefinition) bool {
			return statePreparedDefinitionEqual(candidate, binding)
		}
		next, _, replayErr := stateReplayMigrationStep(current, prepared, stateStepForward)
		if replayErr != nil {
			return statePreparedReconstructor{}, replayErr
		}
		reconstructor.definitions[step.Key] = prepared
		reconstructor.order = append(reconstructor.order, step.Key)
		current = next
	}
	if len(reconstructor.definitions) != len(records) {
		return statePreparedReconstructor{}, stateFailure("prepared_graph_incomplete", "canonical_definition_graph_incomplete", "", "", "", "plan")
	}
	return reconstructor, nil
}

func stateCloneReconstructorSnapshots(
	snapshots map[migrations.MigrationKey][]stateStepOperation,
) map[migrations.MigrationKey][]stateStepOperation {
	cloned := make(map[migrations.MigrationKey][]stateStepOperation, len(snapshots))
	for key, operations := range snapshots {
		cloned[key] = make([]stateStepOperation, len(operations))
		for index := range operations {
			cloned[key][index] = stateStepOperation{
				Before: operations[index].Before.stateClone(),
				After:  operations[index].After.stateClone(),
			}
		}
	}
	return cloned
}

func (r statePreparedReconstructor) preparedByKey(key migrations.MigrationKey) (statePreparedMigrationDefinition, error) {
	prepared, exists := r.definitions[key]
	if !exists {
		return statePreparedMigrationDefinition{}, stateFailure("prepared_definition_missing", "prepared_definition_key_missing", key.App, "", "", "definition")
	}
	return stateClonePreparedMigrationDefinition(prepared), nil
}

func stateValidateReconstructorSnapshotResources(snapshots map[migrations.MigrationKey][]stateStepOperation) *stateCandidateError {
	budget := stateResourceBudget{}
	stateResourceConsumeNodes(&budget, len(snapshots))
	for _, operations := range snapshots {
		if len(operations) > definition.MaxOperationsPerMigration {
			return stateResourceFailure("operation_count_exceeds_profile_limit", "", "", "", "operations")
		}
		stateResourceConsumeNodes(&budget, 1+2*len(operations))
		for index := range operations {
			stateResourceScanProject(&budget, operations[index].Before)
			stateResourceScanProject(&budget, operations[index].After)
		}
	}
	return stateResourceBudgetFailure(&budget)
}

func (r statePreparedReconstructor) stateAtApplied(keys ...migrations.MigrationKey) (stateProjectState, error) {
	applied, err := migrations.NewAppliedState(keys...)
	if err != nil {
		return stateProjectState{}, err
	}
	if err := r.planner.CheckHistory(applied); err != nil {
		return stateProjectState{}, err
	}
	wanted := make(map[migrations.MigrationKey]struct{}, len(keys))
	for _, key := range keys {
		if _, exists := r.definitions[key]; !exists {
			return stateProjectState{}, stateFailure("prepared_definition_missing", "applied_definition_missing", key.App, "", "", "applied")
		}
		wanted[key] = struct{}{}
	}
	current := r.empty.stateClone()
	gap := false
	for _, key := range r.order {
		if _, exists := wanted[key]; !exists {
			gap = true
			continue
		}
		if gap {
			return stateProjectState{}, stateFailure("prepared_applied_not_prefix", "mini_reconstructor_requires_linear_history", key.App, "", "", "applied")
		}
		prepared, exists := r.definitions[key]
		if !exists {
			return stateProjectState{}, stateFailure("prepared_definition_missing", "applied_definition_missing", key.App, "", "", "applied")
		}
		current, _, err = stateReplayMigrationStep(current, prepared, stateStepForward)
		if err != nil {
			return stateProjectState{}, err
		}
	}
	return current.stateClone(), nil
}

func (r statePreparedReconstructor) planAndReplay(
	appliedKeys []migrations.MigrationKey,
	targets ...migrations.Target,
) (stateProjectState, []migrations.PlanStep, error) {
	applied, err := migrations.NewAppliedState(appliedKeys...)
	if err != nil {
		return stateProjectState{}, nil, err
	}
	if err := r.planner.CheckHistory(applied); err != nil {
		return stateProjectState{}, nil, err
	}
	plan, err := r.planner.Plan(applied, targets...)
	if err != nil {
		return stateProjectState{}, nil, err
	}
	current, err := r.stateAtApplied(appliedKeys...)
	if err != nil {
		return stateProjectState{}, nil, err
	}
	for _, step := range plan {
		prepared, exists := r.definitions[step.Key]
		if !exists {
			return stateProjectState{}, nil, stateFailure("prepared_definition_missing", "planned_definition_missing", step.Key.App, "", "", "plan")
		}
		direction := stateStepForward
		if step.Direction == migrations.DirectionBackward {
			direction = stateStepBackward
		}
		current, _, err = stateReplayMigrationStep(current, prepared, direction)
		if err != nil {
			return stateProjectState{}, nil, err
		}
	}
	return current.stateClone(), append([]migrations.PlanStep(nil), plan...), nil
}

func stateReopen(value stateProjectState) (stateProjectState, error) {
	if failure := stateValidate(value); failure != nil {
		return stateProjectState{}, failure
	}
	return value.stateClone(), nil
}

func stateProjectEqual(left, right stateProjectState) bool {
	return left.formatVersion == right.formatVersion && reflect.DeepEqual(left.apps, right.apps)
}

func stateRelationFieldCount(value stateProjectState) int {
	count := 0
	for _, schema := range value.apps {
		for _, model := range schema.Models {
			for _, field := range model.Fields {
				if field.Kind == ir.FieldForeignKey || field.Relation != nil {
					count++
				}
			}
		}
	}
	return count
}

type stateRelationFieldIdentity struct {
	app   string
	model string
	field string
}

func stateDerivedOperationKind(
	before stateProjectState,
	after stateProjectState,
) (stateStepOperationKind, string, string, string, bool) {
	beforeRelations := stateRelationFields(before)
	afterRelations := stateRelationFields(after)
	added := make([]stateRelationFieldIdentity, 0)
	removed := make([]stateRelationFieldIdentity, 0)
	changed := make([]stateRelationFieldIdentity, 0)
	for identity, beforeField := range beforeRelations {
		afterField, exists := afterRelations[identity]
		if !exists {
			removed = append(removed, identity)
			continue
		}
		if !reflect.DeepEqual(beforeField, afterField) {
			changed = append(changed, identity)
		}
	}
	for identity := range afterRelations {
		if _, exists := beforeRelations[identity]; !exists {
			added = append(added, identity)
		}
	}
	sort.Slice(added, func(left, right int) bool { return stateRelationIdentityLess(added[left], added[right]) })
	sort.Slice(removed, func(left, right int) bool { return stateRelationIdentityLess(removed[left], removed[right]) })
	sort.Slice(changed, func(left, right int) bool { return stateRelationIdentityLess(changed[left], changed[right]) })
	first := func(groups ...[]stateRelationFieldIdentity) stateRelationFieldIdentity {
		all := make([]stateRelationFieldIdentity, 0)
		for _, group := range groups {
			all = append(all, group...)
		}
		sort.Slice(all, func(left, right int) bool { return stateRelationIdentityLess(all[left], all[right]) })
		if len(all) == 0 {
			return stateRelationFieldIdentity{}
		}
		return all[0]
	}
	switch {
	case len(added) == 0 && len(removed) == 0 && len(changed) == 0:
		return stateStepOperationScalar, "", "", "", true
	case len(added) != 0 && len(removed) == 0 && len(changed) == 0:
		location := first(added)
		return stateStepOperationRelationAdd, location.app, location.model, location.field, true
	case len(removed) != 0 && len(added) == 0 && len(changed) == 0:
		location := first(removed)
		return stateStepOperationRelationRemove, location.app, location.model, location.field, true
	default:
		location := first(added, removed, changed)
		return stateStepOperationScalar, location.app, location.model, location.field, false
	}
}

func stateRelationFields(value stateProjectState) map[stateRelationFieldIdentity]ir.Field {
	relations := make(map[stateRelationFieldIdentity]ir.Field)
	for app, schema := range value.apps {
		for _, model := range schema.Models {
			for _, field := range model.Fields {
				if field.Kind != ir.FieldForeignKey && field.Relation == nil {
					continue
				}
				relations[stateRelationFieldIdentity{app: app, model: model.Name, field: field.Name}] = field
			}
		}
	}
	return relations
}

func stateRelationIdentityLess(left, right stateRelationFieldIdentity) bool {
	if left.app != right.app {
		return left.app < right.app
	}
	if left.model != right.model {
		return left.model < right.model
	}
	return left.field < right.field
}

func stateValidate(value stateProjectState) *stateCandidateError {
	expectedIRVersion := 0
	switch value.formatVersion {
	case stateFormatScalar:
		expectedIRVersion = ir.FormatVersion
	case stateFormatRelation:
		expectedIRVersion = ir.RelationFormatVersion
	default:
		return stateFailure(
			"state_format_incompatible",
			"format_version",
			"",
			"",
			"",
			"format_version",
		)
	}
	// Resource shape is checked before stateApps allocates and sorts keys, and
	// before Normalize clones and walks nested caller-owned IR values.
	if failure := stateValidateProjectResources(value); failure != nil {
		return failure
	}

	for _, app := range value.stateApps() {
		schema := value.apps[app]
		if app == "" || schema.AppLabel != app {
			return stateFailure(
				"invalid_app_identity",
				"app_identity",
				app,
				"",
				"",
				"app_label",
			)
		}
		if schema.FormatVersion != expectedIRVersion {
			return stateFailure(
				"schema_ir_version_mismatch",
				"schema_ir_version",
				app,
				"",
				"",
				"format_version",
			)
		}
		normalized, err := ir.Normalize(schema)
		if err != nil {
			var validation *ir.ValidationError
			if errors.As(err, &validation) {
				model, field := stateLocation(schema, validation.Path)
				return stateFailure(
					"schema_invalid",
					validation.Code,
					app,
					model,
					field,
					validation.Path,
				)
			}
			return stateFailure("schema_invalid", "schema_invalid", app, "", "", "schema")
		}
		if !reflect.DeepEqual(normalized, schema) {
			return stateFailure(
				"schema_not_normalized",
				"normalization_would_change_state",
				app,
				"",
				"",
				"schema",
			)
		}
	}
	return nil
}

type stateResourceBudget struct {
	nodes         int
	batchBytes    int
	nodeOverflow  bool
	batchOverflow bool
	countFailure  *stateCandidateError
	valueFailure  *stateCandidateError
	docFailure    *stateCandidateError
}

func stateValidateSchemaSliceResources(schemas []ir.Schema) *stateCandidateError {
	if len(schemas) > definition.MaxSources {
		return stateResourceFailure("app_count_exceeds_profile_limit", "", "", "", "apps")
	}
	budget := stateResourceBudget{}
	stateResourceConsumeNodes(&budget, len(schemas))
	for index := range schemas {
		stateResourceScanSchema(&budget, schemas[index].AppLabel, schemas[index])
	}
	return stateResourceBudgetFailure(&budget)
}

func stateValidateProjectResources(value stateProjectState) *stateCandidateError {
	if len(value.apps) > definition.MaxSources {
		return stateResourceFailure("app_count_exceeds_profile_limit", "", "", "", "apps")
	}
	budget := stateResourceBudget{}
	stateResourceScanProject(&budget, value)
	return stateResourceBudgetFailure(&budget)
}

// stateResourceScanProject adds one caller-owned project snapshot to an
// existing budget without cloning or sorting its map. Keeping the budget
// caller-owned lets a migration-step preflight charge every Before/After arm
// to one aggregate node/byte ceiling instead of granting each arm a fresh
// allowance.
func stateResourceScanProject(budget *stateResourceBudget, value stateProjectState) {
	if budget == nil || budget.nodeOverflow {
		return
	}
	if len(value.apps) > definition.MaxSources {
		stateResourceConsider(
			&budget.countFailure,
			stateResourceFailure("app_count_exceeds_profile_limit", "", "", "", "apps"),
		)
		return
	}
	stateResourceConsumeNodes(budget, len(value.apps))
	for app, schema := range value.apps {
		if budget.nodeOverflow {
			return
		}
		// The map key is caller-owned state too. Validate it individually, but
		// do not double-charge it to the serialized schema budget: AppLabel is
		// the single persisted identity representation.
		if len(app) > definition.MaxSourceIDBytes {
			stateResourceConsider(
				&budget.valueFailure,
				stateResourceFailure("identifier_bytes_exceed_profile_limit", app, "", "", "app_key"),
			)
		}
		stateResourceScanSchema(budget, app, schema)
	}
}

func stateValidateStepResources(
	incoming stateProjectState,
	operations []stateStepOperation,
) *stateCandidateError {
	budget := stateResourceBudget{}
	// The incoming state and every transient operation arm are one caller-owned
	// step request. Charge them to one budget before stateValidate can Normalize
	// (and therefore clone) any individual snapshot.
	stateResourceConsumeNodes(&budget, 1+len(operations))
	stateResourceScanProject(&budget, incoming)
	for index := range operations {
		stateResourceConsumeNodes(&budget, 2)
		stateResourceScanProject(&budget, operations[index].Before)
		stateResourceScanProject(&budget, operations[index].After)
	}
	return stateResourceBudgetFailure(&budget)
}

func stateResourceScanSchema(budget *stateResourceBudget, app string, schema ir.Schema) {
	if budget == nil || budget.nodeOverflow {
		return
	}
	locationApp := app
	if locationApp == "" {
		locationApp = schema.AppLabel
	}
	documentBytes := 0
	stateResourceConsumeString(
		budget,
		&documentBytes,
		schema.AppLabel,
		definition.MaxSourceIDBytes,
		"identifier_bytes_exceed_profile_limit",
		locationApp,
		"",
		"",
		-1,
		-1,
		"app_label",
	)
	stateResourceConsumeNodes(budget, len(schema.Models))
	if budget.nodeOverflow {
		return
	}
	for modelIndex := range schema.Models {
		model := &schema.Models[modelIndex]
		if len(model.Fields) > definition.MaxFieldsPerCreateModel {
			stateResourceConsider(
				&budget.countFailure,
				stateResourceFailure(
					"model_field_count_exceeds_profile_limit",
					locationApp,
					model.Name,
					"",
					fmt.Sprintf("models[%d].fields", modelIndex),
				),
			)
		}
		stateResourceConsumeNodes(budget, len(model.Fields))
		// A count or aggregate-node failure outranks all byte failures. Keep
		// scanning model headers to select the canonical count failure, but do
		// not walk a caller-provided over-limit field slice.
		if len(model.Fields) > definition.MaxFieldsPerCreateModel || budget.countFailure != nil {
			continue
		}
		if budget.nodeOverflow {
			return
		}
		stateResourceConsumeString(
			budget, &documentBytes, model.Name, definition.MaxSourceIDBytes,
			"identifier_bytes_exceed_profile_limit", locationApp, model.Name, "", modelIndex, -1, "name",
		)
		stateResourceConsumeString(
			budget, &documentBytes, model.GoName, definition.MaxSourceIDBytes,
			"identifier_bytes_exceed_profile_limit", locationApp, model.Name, "", modelIndex, -1, "go_name",
		)
		stateResourceConsumeString(
			budget, &documentBytes, model.DBTable, definition.MaxSourceIDBytes,
			"identifier_bytes_exceed_profile_limit", locationApp, model.Name, "", modelIndex, -1, "db_table",
		)
		for fieldIndex := range model.Fields {
			field := &model.Fields[fieldIndex]
			stateResourceConsumeString(
				budget, &documentBytes, field.Name, definition.MaxSourceIDBytes,
				"identifier_bytes_exceed_profile_limit", locationApp, model.Name, field.Name, modelIndex, fieldIndex, "name",
			)
			stateResourceConsumeString(
				budget, &documentBytes, field.GoName, definition.MaxSourceIDBytes,
				"identifier_bytes_exceed_profile_limit", locationApp, model.Name, field.Name, modelIndex, fieldIndex, "go_name",
			)
			stateResourceConsumeString(
				budget, &documentBytes, field.Column, definition.MaxSourceIDBytes,
				"identifier_bytes_exceed_profile_limit", locationApp, model.Name, field.Name, modelIndex, fieldIndex, "column",
			)
			stateResourceConsumeString(
				budget, &documentBytes, string(field.Kind), definition.MaxSourceIDBytes,
				"identifier_bytes_exceed_profile_limit", locationApp, model.Name, field.Name, modelIndex, fieldIndex, "kind",
			)
			if field.Default != nil {
				stateResourceConsumeNodes(budget, 1)
				stateResourceConsumeString(
					budget, &documentBytes, string(field.Default.Kind), definition.MaxSourceIDBytes,
					"identifier_bytes_exceed_profile_limit", locationApp, model.Name, field.Name,
					modelIndex, fieldIndex, "default.kind",
				)
				stateResourceConsumeString(
					budget, &documentBytes, field.Default.String, definition.MaxDocumentBytes,
					"default_payload_bytes_exceed_profile_limit", locationApp, model.Name, field.Name,
					modelIndex, fieldIndex, "default.string",
				)
			}
			if field.Relation != nil {
				stateResourceConsumeNodes(budget, 1)
				stateResourceConsumeString(
					budget, &documentBytes, field.Relation.Target.AppLabel, definition.MaxSourceIDBytes,
					"identifier_bytes_exceed_profile_limit", locationApp, model.Name, field.Name,
					modelIndex, fieldIndex, "relation.target.app_label",
				)
				stateResourceConsumeString(
					budget, &documentBytes, field.Relation.Target.ModelName, definition.MaxSourceIDBytes,
					"identifier_bytes_exceed_profile_limit", locationApp, model.Name, field.Name,
					modelIndex, fieldIndex, "relation.target.model_name",
				)
				stateResourceConsumeString(
					budget, &documentBytes, string(field.Relation.Cardinality), definition.MaxSourceIDBytes,
					"identifier_bytes_exceed_profile_limit", locationApp, model.Name, field.Name,
					modelIndex, fieldIndex, "relation.cardinality",
				)
				stateResourceConsumeString(
					budget, &documentBytes, field.Relation.Reverse.Name, definition.MaxSourceIDBytes,
					"identifier_bytes_exceed_profile_limit", locationApp, model.Name, field.Name,
					modelIndex, fieldIndex, "relation.reverse.name",
				)
				stateResourceConsumeString(
					budget, &documentBytes, string(field.Relation.OnDelete), definition.MaxSourceIDBytes,
					"identifier_bytes_exceed_profile_limit", locationApp, model.Name, field.Name,
					modelIndex, fieldIndex, "relation.on_delete",
				)
			}
		}
	}
	if documentBytes > definition.MaxDocumentBytes {
		stateResourceConsider(
			&budget.docFailure,
			stateResourceFailure(
				"schema_document_bytes_exceed_profile_limit",
				locationApp,
				"",
				"",
				"schema",
			),
		)
	}
}

func stateResourceConsumeNodes(budget *stateResourceBudget, count int) {
	if budget == nil || budget.nodeOverflow || count < 0 {
		return
	}
	if count > definition.MaxJSONValues-budget.nodes {
		budget.nodeOverflow = true
		return
	}
	budget.nodes += count
}

func stateResourceConsumeString(
	budget *stateResourceBudget,
	documentBytes *int,
	value string,
	maximum int,
	reason string,
	app, model, field string,
	modelIndex, fieldIndex int,
	member string,
) {
	if budget == nil {
		return
	}
	if len(value) > maximum {
		stateResourceConsider(
			&budget.valueFailure,
			stateResourceFailure(reason, app, model, field, stateResourceValuePath(modelIndex, fieldIndex, member)),
		)
	}
	if documentBytes != nil {
		if *documentBytes > definition.MaxDocumentBytes || len(value) > definition.MaxDocumentBytes-*documentBytes {
			*documentBytes = definition.MaxDocumentBytes + 1
		} else {
			*documentBytes += len(value)
		}
	}
	if !budget.batchOverflow {
		if len(value) > definition.MaxBatchBytes-budget.batchBytes {
			budget.batchOverflow = true
		} else {
			budget.batchBytes += len(value)
		}
	}
}

func stateResourceBudgetFailure(budget *stateResourceBudget) *stateCandidateError {
	if budget == nil {
		return stateResourceFailure("resource_budget_missing", "", "", "", "state")
	}
	// Aggregate exhaustion is canonical before any location-bearing count
	// failure. Scanners stop as soon as this fixed ceiling is crossed, so this
	// precedence cannot depend on which caller-owned map member happened to be
	// visited before the overflow.
	if budget.nodeOverflow {
		return stateResourceFailure("aggregate_node_count_exceeds_profile_limit", "", "", "", "state")
	}
	if budget.countFailure != nil {
		return budget.countFailure
	}
	if budget.valueFailure != nil {
		return budget.valueFailure
	}
	if budget.docFailure != nil {
		return budget.docFailure
	}
	if budget.batchOverflow {
		return stateResourceFailure("aggregate_bytes_exceed_profile_limit", "", "", "", "state")
	}
	return nil
}

func stateResourceConsider(current **stateCandidateError, candidate *stateCandidateError) {
	if candidate == nil || current == nil {
		return
	}
	if *current == nil || stateResourceErrorLess(candidate, *current) {
		*current = candidate
	}
}

func stateResourceErrorLess(left, right *stateCandidateError) bool {
	if left.App != right.App {
		return left.App < right.App
	}
	if left.Model != right.Model {
		return left.Model < right.Model
	}
	if left.Field != right.Field {
		return left.Field < right.Field
	}
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	return left.Reason < right.Reason
}

func stateResourceValuePath(modelIndex, fieldIndex int, member string) string {
	if modelIndex < 0 {
		return member
	}
	if fieldIndex < 0 {
		return fmt.Sprintf("models[%d].%s", modelIndex, member)
	}
	return fmt.Sprintf("models[%d].fields[%d].%s", modelIndex, fieldIndex, member)
}

func stateResourceFailure(reason, app, model, field, path string) *stateCandidateError {
	return stateFailure("resource_limit_exceeded", reason, app, model, field, path)
}

func stateCanonicalFirstRelation(value stateProjectState) (string, string, string, bool) {
	type relationIdentity struct {
		app   string
		model string
		field string
	}
	relations := make([]relationIdentity, 0)
	for app, schema := range value.apps {
		for _, model := range schema.Models {
			for _, field := range model.Fields {
				if field.Kind == ir.FieldForeignKey || field.Relation != nil {
					relations = append(relations, relationIdentity{app: app, model: model.Name, field: field.Name})
				}
			}
		}
	}
	if len(relations) == 0 {
		return "", "", "", false
	}
	sort.Slice(relations, func(left, right int) bool {
		if relations[left].app != relations[right].app {
			return relations[left].app < relations[right].app
		}
		if relations[left].model != relations[right].model {
			return relations[left].model < relations[right].model
		}
		return relations[left].field < relations[right].field
	})
	return relations[0].app, relations[0].model, relations[0].field, true
}

func stateLocation(schema ir.Schema, path string) (string, string) {
	model := ""
	field := ""
	var modelIndex int
	if _, err := fmt.Sscanf(path, "models[%d]", &modelIndex); err == nil && modelIndex >= 0 && modelIndex < len(schema.Models) {
		model = schema.Models[modelIndex].Name
		var fieldIndex int
		if _, err := fmt.Sscanf(path, fmt.Sprintf("models[%d].fields[%%d]", modelIndex), &fieldIndex); err == nil &&
			fieldIndex >= 0 && fieldIndex < len(schema.Models[modelIndex].Fields) {
			field = schema.Models[modelIndex].Fields[fieldIndex].Name
		}
	}
	return model, field
}

func stateFailure(code, reason, app, model, field, path string) *stateCandidateError {
	return &stateCandidateError{
		Category: "migration_relation_state_candidate_error",
		Code:     code,
		Stage:    "state",
		Reason:   reason,
		App:      app,
		Model:    model,
		Field:    field,
		Path:     path,
	}
}
