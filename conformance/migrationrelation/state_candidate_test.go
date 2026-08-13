package migrationrelation

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

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
	stateResourceConsumeNodes(&budget, len(value.apps))
	for app, schema := range value.apps {
		// The map key is caller-owned state too. Validate it individually, but
		// do not double-charge it to the serialized schema budget: AppLabel is
		// the single persisted identity representation.
		if len(app) > definition.MaxSourceIDBytes {
			stateResourceConsider(
				&budget.valueFailure,
				stateResourceFailure("identifier_bytes_exceed_profile_limit", app, "", "", "app_key"),
			)
		}
		stateResourceScanSchema(&budget, app, schema)
	}
	return stateResourceBudgetFailure(&budget)
}

func stateResourceScanSchema(budget *stateResourceBudget, app string, schema ir.Schema) {
	if budget == nil {
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
		if len(model.Fields) > definition.MaxFieldsPerCreateModel ||
			budget.countFailure != nil || budget.nodeOverflow {
			continue
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
	if budget.countFailure != nil {
		return budget.countFailure
	}
	if budget.nodeOverflow {
		return stateResourceFailure("aggregate_node_count_exceeds_profile_limit", "", "", "", "state")
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
