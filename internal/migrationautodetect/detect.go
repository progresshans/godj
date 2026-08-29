// Package migrationautodetect computes the bounded, declaration-owned
// migration changes supported by the current GoDj migration operation set.
// It performs no I/O and deliberately remains internal until the operation
// domain and public migration authoring API are broader than additive changes.
package migrationautodetect

import (
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
	CodeInvalidRequest       ErrorCode = "invalid_request"
	CodeUnsupportedChange    ErrorCode = "unsupported_change"
	CodeAmbiguousHistory     ErrorCode = "ambiguous_history"
	CodeInvalidRelation      ErrorCode = "invalid_relation"
	CodeInvalidGeneratedPlan ErrorCode = "invalid_generated_plan"

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

type appChange struct {
	operations []migrations.Operation
	relations  []relationReference
}

type relationReference struct {
	sourceModel string
	field       string
	targetApp   string
	targetModel string
}

// Detect computes the additive-only migration plan required to make every
// managed historical app exactly equal Desired. Existing model and field
// order is an exact prefix contract because current operations only append.
func Detect(request Request) (Plan, error) {
	if request.Definitions.Digest() == "" {
		return Plan{}, detectionError(CodeInvalidRequest, "", "", "", fmt.Errorf("definition set is not initialized"))
	}

	desired, err := snapshotState(request.Desired)
	if err != nil {
		return Plan{}, detectionError(CodeInvalidRequest, "", "", "", fmt.Errorf("desired state: %w", err))
	}
	existingDefinitions := request.Definitions.Definitions()
	reconstructor, err := migrations.NewStateReconstructor(existingDefinitions...)
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

	leaves := migrationLeaves(existingDefinitions)
	changes := make(map[string]appChange, len(managedOrder))
	for _, app := range managedOrder {
		if len(leaves[app]) > 1 {
			return Plan{}, detectionError(CodeAmbiguousHistory, app, "", "", fmt.Errorf("managed app has %d migration leaves", len(leaves[app])))
		}
		change, changed, detectErr := detectAppChange(app, current, desired)
		if detectErr != nil {
			return Plan{}, detectErr
		}
		if changed {
			changes[app] = change
		}
	}

	if len(changes) == 0 {
		return Plan{}, nil
	}

	expected, err := expectedProjectState(current, desired, managed)
	if err != nil {
		return Plan{}, detectionError(CodeInvalidRequest, "", "", "", fmt.Errorf("expected state: %w", err))
	}

	candidates := make(map[string]migrations.Migration, len(changes))
	changeApps := make([]string, 0, len(changes))
	for app := range changes {
		changeApps = append(changeApps, app)
	}
	sort.Strings(changeApps)
	for _, app := range changeApps {
		change := changes[app]
		name, nameErr := nextMigrationName(app, leaves[app], change.operations, existingDefinitions)
		if nameErr != nil {
			return Plan{}, nameErr
		}
		dependencies := append([]migrations.MigrationKey(nil), leaves[app]...)
		candidates[app] = migrations.Migration{
			App:          app,
			Name:         name,
			Dependencies: dependencies,
			Operations:   cloneOperations(change.operations),
		}
	}

	for _, app := range changeApps {
		candidate := candidates[app]
		relations := append([]relationReference(nil), changes[app].relations...)
		sort.Slice(relations, func(left, right int) bool {
			if relations[left].targetApp != relations[right].targetApp {
				return relations[left].targetApp < relations[right].targetApp
			}
			if relations[left].targetModel != relations[right].targetModel {
				return relations[left].targetModel < relations[right].targetModel
			}
			if relations[left].sourceModel != relations[right].sourceModel {
				return relations[left].sourceModel < relations[right].sourceModel
			}
			return relations[left].field < relations[right].field
		})
		for _, relation := range relations {
			if relation.targetApp == app {
				continue
			}

			// A relation to a model already present in historical state only
			// requires that model's current app leaf. Depending on an unrelated
			// candidate for the same app would create false candidate cycles.
			if _, exists := current.Model(relation.targetApp, relation.targetModel); exists {
				targetLeaves := leaves[relation.targetApp]
				switch len(targetLeaves) {
				case 1:
					candidate.Dependencies = append(candidate.Dependencies, targetLeaves[0])
				case 0:
					return Plan{}, detectionError(CodeInvalidRelation, app, relation.sourceModel, relation.field, fmt.Errorf(
						"historical relation target %s.%s has no migration authority",
						relation.targetApp,
						relation.targetModel,
					))
				default:
					return Plan{}, detectionError(CodeAmbiguousHistory, relation.targetApp, relation.targetModel, "", fmt.Errorf(
						"relation target app has %d migration leaves",
						len(targetLeaves),
					))
				}
				continue
			}

			if _, exists := desired.Model(relation.targetApp, relation.targetModel); !exists {
				return Plan{}, detectionError(CodeInvalidRelation, app, relation.sourceModel, relation.field, fmt.Errorf(
					"relation target model %s.%s does not exist",
					relation.targetApp,
					relation.targetModel,
				))
			}
			if targetCandidate, exists := candidates[relation.targetApp]; exists {
				candidate.Dependencies = append(candidate.Dependencies, targetCandidate.Key())
				continue
			}
			return Plan{}, detectionError(CodeInvalidRelation, app, relation.sourceModel, relation.field, fmt.Errorf(
				"new relation target %s.%s has no candidate migration authority",
				relation.targetApp,
				relation.targetModel,
			))
		}
		candidate.Dependencies = canonicalDependencies(candidate.Dependencies)
		candidates[app] = candidate
	}

	ordered, err := topologicalCandidates(candidates)
	if err != nil {
		return Plan{}, err
	}
	combined := append(cloneMigrations(existingDefinitions), cloneMigrations(ordered)...)
	generated, err := migrations.NewStateReconstructor(combined...)
	if err != nil {
		return Plan{}, detectionError(CodeInvalidGeneratedPlan, "", "", "", err)
	}
	actual, err := generated.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		return Plan{}, detectionError(CodeInvalidGeneratedPlan, "", "", "", err)
	}
	if !actual.Equal(expected) {
		return Plan{}, detectionError(CodeInvalidGeneratedPlan, "", "", "", fmt.Errorf("generated latest state differs from desired managed state"))
	}
	return Plan{migrations: cloneMigrations(ordered)}, nil
}

// topologicalCandidates returns an order in which every candidate-only
// dependency is already a valid durable prefix. Existing dependencies are
// ignored because they are present in the base loaded catalog.
func topologicalCandidates(input map[string]migrations.Migration) ([]migrations.Migration, error) {
	byKey := make(map[migrations.MigrationKey]migrations.Migration, len(input))
	indegree := make(map[migrations.MigrationKey]int, len(input))
	children := make(map[migrations.MigrationKey][]migrations.MigrationKey, len(input))
	for _, migration := range input {
		key := migration.Key()
		byKey[key] = migration
		indegree[key] = 0
	}
	for _, migration := range input {
		child := migration.Key()
		for _, dependency := range migration.Dependencies {
			if _, candidate := byKey[dependency]; !candidate {
				continue
			}
			indegree[child]++
			children[dependency] = append(children[dependency], child)
		}
	}
	ready := make([]migrations.MigrationKey, 0, len(input))
	for key, count := range indegree {
		if count == 0 {
			ready = append(ready, key)
		}
	}
	sortMigrationKeys(ready)
	ordered := make([]migrations.Migration, 0, len(input))
	for len(ready) != 0 {
		key := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byKey[key])
		values := children[key]
		sortMigrationKeys(values)
		for _, child := range values {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sortMigrationKeys(ready)
			}
		}
	}
	if len(ordered) != len(input) {
		return nil, detectionError(CodeInvalidGeneratedPlan, "", "", "", fmt.Errorf("candidate dependency graph contains a cycle"))
	}
	return ordered, nil
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
	if !beforeExists {
		before = ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: app}
	}
	if len(after.Models) < len(before.Models) {
		return appChange{}, false, detectionError(CodeUnsupportedChange, app, "", "", fmt.Errorf("model removal is unsupported"))
	}

	change := appChange{}
	addedFields := make([]migrations.Operation, 0)
	for index := range before.Models {
		oldModel := before.Models[index]
		newModel := after.Models[index]
		if oldModel.Name != newModel.Name || oldModel.GoName != newModel.GoName || oldModel.DBTable != newModel.DBTable {
			return appChange{}, false, detectionError(CodeUnsupportedChange, app, newModel.Name, "", fmt.Errorf("existing model identity/order changed at index %d", index))
		}
		if len(newModel.Fields) < len(oldModel.Fields) {
			return appChange{}, false, detectionError(CodeUnsupportedChange, app, newModel.Name, "", fmt.Errorf("field removal is unsupported"))
		}
		for fieldIndex := range oldModel.Fields {
			if !reflect.DeepEqual(oldModel.Fields[fieldIndex], newModel.Fields[fieldIndex]) {
				return appChange{}, false, detectionError(CodeUnsupportedChange, app, newModel.Name, newModel.Fields[fieldIndex].Name, fmt.Errorf("existing field identity/order/metadata changed at index %d", fieldIndex))
			}
		}
		for fieldIndex := len(oldModel.Fields); fieldIndex < len(newModel.Fields); fieldIndex++ {
			field := newModel.Fields[fieldIndex].Clone()
			if !safeExistingAddField(field) {
				return appChange{}, false, detectionError(CodeUnsupportedChange, app, newModel.Name, field.Name, fmt.Errorf("existing-table AddField requires nullable CharField or ForeignKey with no default"))
			}
			if err := validateAddedRelation(app, newModel.Name, field, after, nil); err != nil {
				return appChange{}, false, err
			}
			collectRelationReference(&change.relations, app, newModel.Name, field)
			addedFields = append(addedFields, migrations.AddField{AppLabel: app, ModelName: newModel.Name, Field: field})
		}
	}

	existingModels := make(map[string]struct{}, len(before.Models))
	for _, model := range before.Models {
		existingModels[model.Name] = struct{}{}
	}
	visibleNew := make(map[string]struct{})
	for modelIndex := len(before.Models); modelIndex < len(after.Models); modelIndex++ {
		model := after.Models[modelIndex].Clone()
		for _, field := range model.Fields {
			if err := validateAddedRelation(app, model.Name, field, after, func(target string) bool {
				_, historical := existingModels[target]
				_, earlier := visibleNew[target]
				return historical || earlier
			}); err != nil {
				return appChange{}, false, err
			}
			collectRelationReference(&change.relations, app, model.Name, field)
		}
		change.operations = append(change.operations, migrations.CreateModel{AppLabel: app, Model: model})
		visibleNew[model.Name] = struct{}{}
	}
	change.operations = append(change.operations, addedFields...)
	return change, len(change.operations) != 0, nil
}

func safeExistingAddField(field ir.Field) bool {
	return field.Nullable && field.Default == nil && !field.PrimaryKey &&
		(field.Kind == ir.FieldChar || field.Kind == ir.FieldForeignKey)
}

func validateAddedRelation(
	app, model string,
	field ir.Field,
	desired ir.Schema,
	sameAppTargetVisible func(string) bool,
) error {
	if field.Kind != ir.FieldForeignKey || field.Relation == nil {
		return nil
	}
	target := field.Relation.Target
	if target.AppLabel != app {
		return nil
	}
	if target.ModelName == model {
		return detectionError(CodeInvalidRelation, app, model, field.Name, fmt.Errorf("self relation generation is outside the current operation topology"))
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
	if sameAppTargetVisible != nil && !sameAppTargetVisible(target.ModelName) {
		return detectionError(CodeInvalidRelation, app, model, field.Name, fmt.Errorf("same-app relation target %q must be created earlier", target.ModelName))
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
		case *migrations.AddField:
			if operation != nil {
				return operation.ModelName + "_" + operation.Field.Name
			}
		}
	}
	type change struct {
		Kind  string    `json:"kind"`
		App   string    `json:"app"`
		Model string    `json:"model"`
		Value *ir.Model `json:"value,omitempty"`
		Field *ir.Field `json:"field,omitempty"`
	}
	values := make([]change, 0, len(operations))
	for _, operation := range operations {
		switch value := operation.(type) {
		case migrations.CreateModel:
			model := value.Model.Clone()
			values = append(values, change{Kind: "create_model", App: value.AppLabel, Model: value.Model.Name, Value: &model})
		case migrations.AddField:
			field := value.Field.Clone()
			values = append(values, change{Kind: "add_field", App: value.AppLabel, Model: value.ModelName, Field: &field})
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
		case migrations.CreateModel:
			result[index] = migrations.CreateModel{AppLabel: value.AppLabel, Model: value.Model.Clone()}
		case *migrations.CreateModel:
			if value != nil {
				copy := migrations.CreateModel{AppLabel: value.AppLabel, Model: value.Model.Clone()}
				result[index] = &copy
			}
		case migrations.AddField:
			result[index] = migrations.AddField{AppLabel: value.AppLabel, ModelName: value.ModelName, Field: value.Field.Clone()}
		case *migrations.AddField:
			if value != nil {
				copy := migrations.AddField{AppLabel: value.AppLabel, ModelName: value.ModelName, Field: value.Field.Clone()}
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
