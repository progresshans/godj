// Package migrationautodetect computes the bounded, declaration-owned
// migration changes supported by the current GoDj migration operation set.
// It performs no I/O and deliberately remains internal until the operation
// domain and public migration authoring API are broader than additive changes.
package migrationautodetect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

// ErrorCode is the closed internal failure vocabulary consumed by the
// project-linked makemigrations boundary. Details are diagnostic only; the
// global CLI publishes a separate bounded category/code pair.
type ErrorCode string

const (
	CodeInvalidRequest         ErrorCode = "invalid_request"
	CodeUnsupportedChange      ErrorCode = "unsupported_change"
	CodeAmbiguousHistory       ErrorCode = "ambiguous_history"
	CodeInvalidRelation        ErrorCode = "invalid_relation"
	CodeInvalidGeneratedPlan   ErrorCode = "invalid_generated_plan"
	CodeCandidateResourceLimit ErrorCode = "candidate_resource_limit_exceeded"

	// MaxCandidates bounds prefix planning and per-file publication CAS work.
	MaxCandidates = 64

	compositeMigrationNameDigestDomain = "godj/migration-name/v1\x00"
)

// Error identifies the canonical app/model/field at which detection stopped.
// Cause is retained for internal diagnostics and error inspection only.
type Error struct {
	Code  ErrorCode
	App   string
	Model string
	Field string
	Cause error
}

func (e *Error) Error() string {
	if e == nil {
		return "migration autodetection error"
	}
	message := "migration autodetection " + string(e.Code)
	if e.App != "" {
		message += " app=" + e.App
	}
	if e.Model != "" {
		message += " model=" + e.Model
	}
	if e.Field != "" {
		message += " field=" + e.Field
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Request is snapshotted by Detect. Definitions must be a successful
// definition.Load result, including the initialized empty set. Desired may
// contain only managed apps. ManagedApps is the explicit union of currently
// declared apps and filesystem-owned historical apps; programmatic-only apps
// remain outside this set and are preserved from history.
type Request struct {
	Definitions migrations.LoadedDefinitionSet
	Desired     migrations.ProjectState
	ManagedApps []string
}

// Plan is an immutable-by-contract ordered candidate set. Its zero value is a
// valid empty plan, but Detect never returns a non-zero plan with an error.
type Plan struct {
	migrations []migrations.Migration
	baseState  migrations.ProjectState
}

// Empty reports whether the declared schema already matches current history.
func (p Plan) Empty() bool {
	return len(p.migrations) == 0
}

// Migrations returns a fresh deep copy in deterministic topological order,
// with app/key order as the tie-break among ready candidates.
func (p Plan) Migrations() []migrations.Migration {
	return cloneMigrations(p.migrations)
}

// BaseState returns the history already validated by Detect. Publication
// callers reuse it for final-state checks instead of reconstructing the same
// immutable input catalog a second time. Empty successful plans retain it too.
func (p Plan) BaseState() migrations.ProjectState { return p.baseState.Clone() }

type appChange struct {
	operations []migrations.Operation
	relations  []relationReference
}

type relationReference struct {
	sourceModel  string
	field        string
	targetApp    string
	targetModel  string
	targetFields []string
}

// Detect computes supported field and named-constraint changes to make every
// managed historical app exactly equal Desired. Existing model order and the
// relative order of retained fields are preserved. Deferred relations may be
// inserted between retained fields without changing their metadata.
func Detect(request Request) (Plan, error) {
	if request.Definitions.Digest() == "" {
		return Plan{}, detectionError(CodeInvalidRequest, "", "", "", fmt.Errorf("definition set is not initialized"))
	}

	desired, err := snapshotState(request.Desired)
	if err != nil {
		return Plan{}, detectionError(CodeInvalidRequest, "", "", "", fmt.Errorf("desired state: %w", err))
	}
	existingDefinitions := request.Definitions.Definitions()
	reconstructor, err := request.Definitions.Reconstructor(context.Background())
	if err != nil {
		return Plan{}, detectionError(CodeInvalidRequest, "", "", "", fmt.Errorf("historical definitions: %w", err))
	}
	current, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		return Plan{}, detectionError(CodeInvalidRequest, "", "", "", fmt.Errorf("historical state: %w", err))
	}

	managed, managedOrder, err := snapshotManagedApps(request.ManagedApps, desired, existingDefinitions)
	if err != nil {
		return Plan{}, err
	}
	for _, app := range desired.Apps() {
		if _, exists := managed[app]; !exists {
			return Plan{}, detectionError(CodeInvalidRequest, app, "", "", fmt.Errorf("desired app is not managed"))
		}
	}

	expected, err := expectedProjectState(current, desired, managed)
	if err != nil {
		return Plan{}, detectionError(CodeInvalidRequest, "", "", "", fmt.Errorf("expected state: %w", err))
	}
	base := current
	var candidates []migrations.Migration
	for {
		leaves := migrationLeaves(existingDefinitions)
		changes := make(map[string]appChange, len(managedOrder))
		for _, app := range managedOrder {
			if len(leaves[app]) > 1 {
				return Plan{}, detectionError(CodeAmbiguousHistory, app, "", "", fmt.Errorf("managed app has %d migration leaves", len(leaves[app])))
			}
			change, changed, err := detectAppChange(app, current, desired)
			if err != nil {
				return Plan{}, err
			}
			if changed {
				changes[app] = change
			}
		}
		if len(changes) == 0 {
			if !current.Equal(expected) {
				return Plan{}, detectionError(CodeInvalidGeneratedPlan, "", "", "", fmt.Errorf("generated latest state differs from desired managed state"))
			}
			return Plan{migrations: cloneMigrations(candidates), baseState: base}, nil
		}
		if len(changes) > MaxCandidates-len(candidates) {
			return Plan{}, detectionError(CodeCandidateResourceLimit, "", "", "", fmt.Errorf("candidate count exceeds %d", MaxCandidates))
		}
		candidate, err := nextCandidate(changes, current, desired, leaves, existingDefinitions)
		if err != nil {
			return Plan{}, err
		}
		existingDefinitions = append(existingDefinitions, candidate)
		// Each next candidate is computed from the exact durable prefix that
		// will precede it. Re-running detection after any published prefix must
		// produce the same remaining bytes, including partial-temp recovery.
		generated, err := migrations.NewStateReconstructor(existingDefinitions...)
		if err != nil {
			return Plan{}, detectionError(CodeInvalidGeneratedPlan, "", "", "", err)
		}
		next, err := generated.Reconstruct(migrations.LatestStateRequest())
		if err != nil {
			return Plan{}, detectionError(CodeInvalidGeneratedPlan, "", "", "", err)
		}
		if next.Equal(current) {
			return Plan{}, detectionError(CodeInvalidGeneratedPlan, "", "", "", fmt.Errorf("candidate made no historical progress"))
		}
		current = next
		candidates = append(candidates, candidate)
	}
}

func sortMigrationKeys(values []migrations.MigrationKey) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].App != values[right].App {
			return values[left].App < values[right].App
		}
		return values[left].Name < values[right].Name
	})
}

func snapshotState(input migrations.ProjectState) (migrations.ProjectState, error) {
	apps := input.Apps()
	schemas := make([]ir.Schema, 0, len(apps))
	for _, app := range apps {
		schema, exists := input.Schema(app)
		if !exists {
			return migrations.ProjectState{}, fmt.Errorf("app %q disappeared during snapshot", app)
		}
		schemas = append(schemas, schema)
	}
	snapshot, err := migrations.NewProjectState(schemas...)
	if err != nil {
		return migrations.ProjectState{}, err
	}
	if !snapshot.Equal(input) {
		return migrations.ProjectState{}, fmt.Errorf("state is not an exact normalized snapshot")
	}
	return snapshot, nil
}

func snapshotManagedApps(
	input []string,
	desired migrations.ProjectState,
	definitions []migrations.Migration,
) (map[string]struct{}, []string, error) {
	known := make(map[string]struct{})
	for _, app := range desired.Apps() {
		known[app] = struct{}{}
	}
	for _, migration := range definitions {
		known[migration.App] = struct{}{}
	}
	order := append([]string(nil), input...)
	sort.Strings(order)
	managed := make(map[string]struct{}, len(order))
	for index, app := range order {
		if app == "" {
			return nil, nil, detectionError(CodeInvalidRequest, "", "", "", fmt.Errorf("managed app is empty"))
		}
		if index > 0 && app == order[index-1] {
			return nil, nil, detectionError(CodeInvalidRequest, app, "", "", fmt.Errorf("managed app is duplicated"))
		}
		if _, exists := known[app]; !exists {
			return nil, nil, detectionError(CodeInvalidRequest, app, "", "", fmt.Errorf("managed app has no declared or historical source"))
		}
		managed[app] = struct{}{}
	}
	return managed, order, nil
}

func migrationLeaves(definitions []migrations.Migration) map[string][]migrations.MigrationKey {
	leaves := make(map[string]map[migrations.MigrationKey]struct{})
	for _, migration := range definitions {
		if leaves[migration.App] == nil {
			leaves[migration.App] = make(map[migrations.MigrationKey]struct{})
		}
		leaves[migration.App][migration.Key()] = struct{}{}
	}
	for _, migration := range definitions {
		for _, dependency := range migration.Dependencies {
			if dependency.App == migration.App {
				delete(leaves[migration.App], dependency)
			}
		}
	}
	result := make(map[string][]migrations.MigrationKey, len(leaves))
	for app, values := range leaves {
		keys := make([]migrations.MigrationKey, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(left, right int) bool {
			if keys[left].App != keys[right].App {
				return keys[left].App < keys[right].App
			}
			return keys[left].Name < keys[right].Name
		})
		result[app] = keys
	}
	return result
}

func detectAppChange(app string, current, desired migrations.ProjectState) (appChange, bool, error) {
	before, beforeExists := current.Schema(app)
	after, afterExists := desired.Schema(app)
	if beforeExists && !afterExists {
		return appChange{}, false, detectionError(CodeUnsupportedChange, app, "", "", fmt.Errorf("managed app removal is unsupported"))
	}
	if !afterExists {
		return appChange{}, false, nil
	}
	for _, schema := range []ir.Schema{before, after} {
		for _, model := range schema.Models {
			for _, field := range model.ManyToMany {
				if field.Through == nil {
					return appChange{}, false, detectionError(CodeUnsupportedChange, app, model.Name, field.Name, fmt.Errorf("automatic ManyToMany storage migration is not implemented"))
				}
			}
		}
	}
	if !beforeExists {
		before = ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: app}
	}
	if len(after.Models) < len(before.Models) {
		return appChange{}, false, detectionError(CodeUnsupportedChange, app, "", "", fmt.Errorf("model removal is unsupported"))
	}

	change := appChange{}
	addedFields := make([]migrations.Operation, 0)
	var removedConstraints, addedConstraints, manyOperations []migrations.Operation
	for index := range before.Models {
		oldModel := before.Models[index]
		newModel := after.Models[index]
		if oldModel.Name != newModel.Name || oldModel.GoName != newModel.GoName || oldModel.DBTable != newModel.DBTable {
			return appChange{}, false, detectionError(CodeUnsupportedChange, app, newModel.Name, "", fmt.Errorf("existing model identity/order changed at index %d", index))
		}
		if len(newModel.Fields) < len(oldModel.Fields) {
			return appChange{}, false, detectionError(CodeUnsupportedChange, app, newModel.Name, "", fmt.Errorf("field removal is unsupported"))
		}
		many, err := manyChanges(app, oldModel, newModel)
		if err != nil {
			return appChange{}, false, err
		}
		manyOperations = append(manyOperations, many...)
		removed, added := constraintChanges(app, oldModel, newModel)
		removedConstraints = append(removedConstraints, removed...)
		addedConstraints = append(addedConstraints, added...)
		oldNames := make(map[string]int, len(oldModel.Fields))
		for index, field := range oldModel.Fields {
			oldNames[field.Name] = index
		}
		retained := 0
		for _, field := range newModel.Fields {
			if oldIndex, exists := oldNames[field.Name]; exists {
				if oldIndex != retained {
					return appChange{}, false, detectionError(CodeUnsupportedChange, app, newModel.Name, field.Name, fmt.Errorf("existing field order changed"))
				}
				before := oldModel.Fields[oldIndex]
				if !reflect.DeepEqual(before, field) {
					if _, err := ir.ClassifyFieldChange(before, field); err != nil {
						return appChange{}, false, detectionError(CodeUnsupportedChange, app, newModel.Name, field.Name, err)
					}
					addedFields = append(addedFields, migrations.AlterField{AppLabel: app, ModelName: newModel.Name, Before: before.Clone(), After: field.Clone()})
				}
				retained++
				continue
			}
			if !safeExistingAddField(field) {
				return appChange{}, false, detectionError(CodeUnsupportedChange, app, newModel.Name, field.Name, fmt.Errorf("existing-table AddField requires a supported nullable field or an empty-table ForeignKey, with no default"))
			}
			if err := validateAddedRelation(app, newModel.Name, field, after); err != nil {
				return appChange{}, false, err
			}
			collectRelationReference(&change.relations, app, newModel.Name, field)
			addedFields = append(addedFields, migrations.AddField{AppLabel: app, ModelName: newModel.Name, Field: field.Clone()})
		}
		if retained != len(oldModel.Fields) {
			return appChange{}, false, detectionError(CodeUnsupportedChange, app, newModel.Name, "", fmt.Errorf("field removal is unsupported"))
		}
	}

	for modelIndex := len(before.Models); modelIndex < len(after.Models); modelIndex++ {
		model := after.Models[modelIndex].Clone()
		for _, field := range model.Fields {
			if err := validateAddedRelation(app, model.Name, field, after); err != nil {
				return appChange{}, false, err
			}
			collectRelationReference(&change.relations, app, model.Name, field)
		}
		many, err := manyChanges(app, ir.Model{}, model)
		if err != nil {
			return appChange{}, false, err
		}
		manyOperations = append(manyOperations, many...)
		model.ManyToMany = nil
		change.operations = append(change.operations, migrations.CreateModel{AppLabel: app, Model: model})
	}
	change.operations = append(removedConstraints, change.operations...)
	change.operations = append(change.operations, addedFields...)
	change.operations = append(change.operations, addedConstraints...)
	change.operations = append(change.operations, manyOperations...)
	for _, operation := range manyOperations {
		collectManyOperationReferences(&change.relations, operation)
	}
	return change, len(change.operations) != 0, nil
}

func safeExistingAddField(field ir.Field) bool {
	if field.Default != nil || field.PrimaryKey {
		return false
	}
	if field.Kind == ir.FieldForeignKey && field.Relation != nil {
		// Required relations are executable only after the backend proves the
		// source table empty. This also resumes an interrupted cyclic Create
		// prefix; no guessed key, default, or populated-table backfill is used.
		return field.Relation.OnDelete.Valid() && (field.Nullable || field.Relation.OnDelete != ir.DeleteSetNull)
	}
	return field.Nullable && (field.Kind == ir.FieldChar || field.Kind == ir.FieldText || field.Kind == ir.FieldDateTime || field.Kind == ir.FieldDate || (field.Kind == ir.FieldTime || field.Kind == ir.FieldDuration || field.Kind == ir.FieldFloat || field.Kind == ir.FieldDecimal || field.Kind == ir.FieldUUID || field.Kind == ir.FieldJSON) || field.Kind == ir.FieldInteger || field.Kind == ir.FieldBoolean)
}

func validateAddedRelation(
	app, model string,
	field ir.Field,
	desired ir.Schema,
) error {
	if field.Kind != ir.FieldForeignKey || field.Relation == nil {
		return nil
	}
	target := field.Relation.Target
	if target.AppLabel != app {
		return nil
	}
	targetExists := false
	for _, candidate := range desired.Models {
		if candidate.Name == target.ModelName {
			targetExists = true
			break
		}
	}
	if !targetExists {
		return detectionError(CodeInvalidRelation, app, model, field.Name, fmt.Errorf("same-app relation target %q does not exist", target.ModelName))
	}
	return nil
}

func collectRelationReference(targets *[]relationReference, app, model string, field ir.Field) {
	if field.Kind == ir.FieldForeignKey && field.Relation != nil && field.Relation.Target.AppLabel != app {
		*targets = append(*targets, relationReference{
			sourceModel: model,
			field:       field.Name,
			targetApp:   field.Relation.Target.AppLabel,
			targetModel: field.Relation.Target.ModelName,
		})
	}
}

func expectedProjectState(current, desired migrations.ProjectState, managed map[string]struct{}) (migrations.ProjectState, error) {
	schemas := make([]ir.Schema, 0, len(current.Apps())+len(desired.Apps()))
	for _, app := range current.Apps() {
		if _, replaced := managed[app]; replaced {
			continue
		}
		schema, _ := current.Schema(app)
		schemas = append(schemas, schema)
	}
	for _, app := range desired.Apps() {
		schema, _ := desired.Schema(app)
		schemas = append(schemas, schema)
	}
	return migrations.NewProjectState(schemas...)
}

func nextMigrationName(
	app string,
	leaves []migrations.MigrationKey,
	operations []migrations.Operation,
	existing []migrations.Migration,
) (string, error) {
	if len(leaves) == 0 {
		return "0001_initial", nil
	}
	if len(leaves) != 1 {
		return "", detectionError(CodeAmbiguousHistory, app, "", "", fmt.Errorf("cannot name successor for %d leaves", len(leaves)))
	}
	leaf := leaves[0].Name
	if len(leaf) < 5 || leaf[4] != '_' {
		return "", detectionError(CodeUnsupportedChange, app, "", "", fmt.Errorf("leaf %q has no canonical four-digit prefix", leaf))
	}
	sequence, err := strconv.Atoi(leaf[:4])
	if err != nil || sequence <= 0 || sequence >= 9999 {
		return "", detectionError(CodeUnsupportedChange, app, "", "", fmt.Errorf("leaf %q cannot produce a bounded successor", leaf))
	}
	name := fmt.Sprintf("%04d_%s", sequence+1, operationSlug(operations))
	key := migrations.MigrationKey{App: app, Name: name}
	for _, migration := range existing {
		if migration.Key() == key {
			return "", detectionError(CodeAmbiguousHistory, app, "", "", fmt.Errorf("generated identity %s already exists", name))
		}
	}
	return name, nil
}

func operationSlug(operations []migrations.Operation) string {
	if len(operations) == 1 {
		switch operation := operations[0].(type) {
		case migrations.CreateModel:
			return operation.Model.Name
		case *migrations.CreateModel:
			if operation != nil {
				return operation.Model.Name
			}
		case migrations.AddField:
			return operation.ModelName + "_" + operation.Field.Name
		case migrations.AddManyToMany:
			return "add_" + operation.ModelName + "_" + operation.Field.Name
		case migrations.RemoveManyToMany:
			return "remove_" + operation.ModelName + "_" + operation.Field.Name
		case migrations.RenameManyToMany:
			return "rename_" + operation.ModelName + "_" + operation.After.Name
		case migrations.AddConstraint:
			return "add_" + operation.ModelName + "_" + operation.Constraint.Name
		case migrations.RemoveConstraint:
			return "remove_" + operation.ModelName + "_" + operation.Constraint.Name
		case migrations.AlterField:
			return "alter_" + operation.ModelName + "_" + operation.After.Name
		case *migrations.AlterField:
			if operation != nil {
				return "alter_" + operation.ModelName + "_" + operation.After.Name
			}
		case *migrations.AddField:
			if operation != nil {
				return operation.ModelName + "_" + operation.Field.Name
			}
		}
	}
	type change struct {
		Kind        string               `json:"kind"`
		App         string               `json:"app"`
		Model       string               `json:"model"`
		Value       *ir.Model            `json:"value,omitempty"`
		Field       *ir.Field            `json:"field,omitempty"`
		Before      *ir.Field            `json:"before,omitempty"`
		After       *ir.Field            `json:"after,omitempty"`
		BeforeField string               `json:"before_field,omitempty"`
		Constraint  *ir.UniqueConstraint `json:"constraint,omitempty"`
		Many        *ir.ManyToManyField  `json:"many,omitempty"`
		ManyBefore  *ir.ManyToManyField  `json:"many_before,omitempty"`
	}
	values := make([]change, 0, len(operations))
	for _, operation := range operations {
		switch value := operation.(type) {
		case migrations.AddManyToMany:
			field := value.Field.Clone()
			values = append(values, change{Kind: "add_many_to_many", App: value.AppLabel, Model: value.ModelName, Many: &field, BeforeField: value.BeforeField})
		case migrations.RemoveManyToMany:
			field := value.Field.Clone()
			values = append(values, change{Kind: "remove_many_to_many", App: value.AppLabel, Model: value.ModelName, Many: &field, BeforeField: value.BeforeField})
		case migrations.RenameManyToMany:
			before, after := value.Before.Clone(), value.After.Clone()
			values = append(values, change{Kind: "rename_many_to_many", App: value.AppLabel, Model: value.ModelName, Many: &after, ManyBefore: &before})
		case migrations.CreateModel:
			model := value.Model.Clone()
			values = append(values, change{Kind: "create_model", App: value.AppLabel, Model: value.Model.Name, Value: &model})
		case migrations.AddField:
			field := value.Field.Clone()
			values = append(values, change{Kind: "add_field", App: value.AppLabel, Model: value.ModelName, Field: &field, BeforeField: value.BeforeField})
		case migrations.AddConstraint:
			constraint := value.Constraint.Clone()
			values = append(values, change{Kind: "add_constraint", App: value.AppLabel, Model: value.ModelName, Constraint: &constraint})
		case migrations.RemoveConstraint:
			constraint := value.Constraint.Clone()
			values = append(values, change{Kind: "remove_constraint", App: value.AppLabel, Model: value.ModelName, Constraint: &constraint})
		case migrations.AlterField:
			before, after := value.Before.Clone(), value.After.Clone()
			values = append(values, change{Kind: "alter_field", App: value.AppLabel, Model: value.ModelName, Before: &before, After: &after})
		}
	}
	document, _ := json.Marshal(values)
	digest := sha256.New()
	_, _ = digest.Write([]byte(compositeMigrationNameDigestDomain))
	_, _ = digest.Write(document)
	return "auto_" + hex.EncodeToString(digest.Sum(nil)[:6])
}

func canonicalDependencies(input []migrations.MigrationKey) []migrations.MigrationKey {
	values := append([]migrations.MigrationKey(nil), input...)
	sort.Slice(values, func(left, right int) bool {
		if values[left].App != values[right].App {
			return values[left].App < values[right].App
		}
		return values[left].Name < values[right].Name
	})
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func cloneMigrations(input []migrations.Migration) []migrations.Migration {
	result := make([]migrations.Migration, len(input))
	for index, migration := range input {
		result[index] = migrations.Migration{
			App:          migration.App,
			Name:         migration.Name,
			Dependencies: append([]migrations.MigrationKey(nil), migration.Dependencies...),
			Operations:   cloneOperations(migration.Operations),
		}
	}
	return result
}

func cloneOperations(input []migrations.Operation) []migrations.Operation {
	result := make([]migrations.Operation, len(input))
	for index, operation := range input {
		switch value := operation.(type) {
		case migrations.AddManyToMany:
			copy := value
			copy.Field = copy.Field.Clone()
			result[index] = copy
		case *migrations.AddManyToMany:
			if value != nil {
				copy := *value
				copy.Field = copy.Field.Clone()
				result[index] = &copy
			}
		case migrations.RemoveManyToMany:
			copy := value
			copy.Field = copy.Field.Clone()
			result[index] = copy
		case *migrations.RemoveManyToMany:
			if value != nil {
				copy := *value
				copy.Field = copy.Field.Clone()
				result[index] = &copy
			}
		case migrations.RenameManyToMany:
			copy := value
			copy.Before, copy.After = copy.Before.Clone(), copy.After.Clone()
			result[index] = copy
		case *migrations.RenameManyToMany:
			if value != nil {
				copy := *value
				copy.Before, copy.After = copy.Before.Clone(), copy.After.Clone()
				result[index] = &copy
			}
		case migrations.CreateModel:
			result[index] = migrations.CreateModel{AppLabel: value.AppLabel, Model: value.Model.Clone()}
		case *migrations.CreateModel:
			if value != nil {
				copy := migrations.CreateModel{AppLabel: value.AppLabel, Model: value.Model.Clone()}
				result[index] = &copy
			}
		case migrations.AddField:
			value.Field = value.Field.Clone()
			result[index] = value
		case *migrations.AddField:
			if value != nil {
				copy := *value
				copy.Field = value.Field.Clone()
				result[index] = &copy
			}
		case migrations.AddConstraint:
			value.Constraint = value.Constraint.Clone()
			result[index] = value
		case *migrations.AddConstraint:
			if value != nil {
				copy := *value
				copy.Constraint = value.Constraint.Clone()
				result[index] = &copy
			}
		case migrations.RemoveConstraint:
			value.Constraint = value.Constraint.Clone()
			result[index] = value
		case *migrations.RemoveConstraint:
			if value != nil {
				copy := *value
				copy.Constraint = value.Constraint.Clone()
				result[index] = &copy
			}
		case migrations.AlterField:
			value.Before, value.After = value.Before.Clone(), value.After.Clone()
			result[index] = value
		case *migrations.AlterField:
			if value != nil {
				copy := *value
				copy.Before, copy.After = value.Before.Clone(), value.After.Clone()
				result[index] = &copy
			}
		default:
			result[index] = operation
		}
	}
	return result
}

func detectionError(code ErrorCode, app, model, field string, cause error) *Error {
	return &Error{Code: code, App: app, Model: model, Field: field, Cause: cause}
}
