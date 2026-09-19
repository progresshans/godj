package migrationgraph

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/progresshans/godj/internal/irresource"
	"github.com/progresshans/godj/schema/ir"
)

const (
	migrationGraphMaxModels      = 2_048
	migrationGraphMaxFields      = 2_048
	migrationGraphMaxStringBytes = 1 << 20
	migrationGraphMaxBytes       = 16 << 20
	migrationGraphMaxNodes       = 262_144
)

// MigrationModel names one exact historical model independently of its table
// spelling. App identity is required because equal model names in different
// apps are distinct vertices of a relation graph.
type MigrationModel struct {
	AppLabel string
	Model    ir.Model
}

func (model MigrationModel) Identity() ir.ModelIdentity {
	return ir.ModelIdentity{AppLabel: model.AppLabel, ModelName: model.Model.Name}
}

// RelationGraph is a detached, closed historical graph rooted at one
// operation's relation-bearing model boundary. Models and edges occur once;
// a self edge or a cycle never recursively embeds another model snapshot.
// Its zero value is not a resolved graph. Accessors return detached IR.
type RelationGraph struct {
	root   ir.ModelIdentity
	models map[ir.ModelIdentity]ir.Model
	keys   map[ir.ModelIdentity]ir.Field
	order  []ir.ModelIdentity
	tables map[string]ir.ModelIdentity
}

// NewRelationGraph validates an exact, normalized source boundary and all
// transitively reachable target models. Related excludes the source, contains
// no unreachable models, and is ordered by app/model identity. This function
// performs no I/O and does not decide migration chronology or physical schema
// compatibility; those checks remain with the intent and backend owners.
func NewRelationGraph(source MigrationModel, related []MigrationModel) (RelationGraph, error) {
	return newRelationGraph(source, related, true)
}

func newRelationGraph(source MigrationModel, related []MigrationModel, requireReachability bool) (RelationGraph, error) {
	modelLimit := migrationGraphMaxModels
	if !requireReachability {
		modelLimit = migrationGraphMaxNodes
	}
	if len(related) >= modelLimit {
		return RelationGraph{}, fmt.Errorf("relation graph has more than %d models", modelLimit)
	}
	budget := irresource.New(irresource.Limits{
		Fields: migrationGraphMaxFields, StringBytes: migrationGraphMaxStringBytes,
		Nodes: migrationGraphMaxNodes, Bytes: migrationGraphMaxBytes,
	})
	// Boundary forests derive every source app from a transition, so its
	// immutable string is shared rather than copied per model. Count each
	// distinct app once here. Raw operation/intent scans still charge every
	// caller-supplied occurrence before constructing this compact inventory.
	apps := make(map[string]struct{})
	scan := func(path string, model MigrationModel) error {
		if requireReachability {
			return scanMigrationGraphModel(&budget, path, model)
		}
		if _, seen := apps[model.AppLabel]; !seen {
			if err := budget.ConsumeString(path+".app", model.AppLabel); err != nil {
				return err
			}
			apps[model.AppLabel] = struct{}{}
		}
		return budget.ScanModel(path+".model", model.Model)
	}
	if err := scan("source", source); err != nil {
		return RelationGraph{}, err
	}
	if err := budget.ConsumeNodes("related", len(related)); err != nil {
		return RelationGraph{}, err
	}
	for index, model := range related {
		if err := scan(fmt.Sprintf("related[%d]", index), model); err != nil {
			return RelationGraph{}, err
		}
	}

	graph := RelationGraph{
		root:   source.Identity(),
		models: make(map[ir.ModelIdentity]ir.Model, len(related)+1),
		keys:   make(map[ir.ModelIdentity]ir.Field, len(related)+1),
		order:  make([]ir.ModelIdentity, 0, len(related)+1),
	}
	tables := make(map[string]ir.ModelIdentity, len(related)+1)
	graph.tables = tables
	goNames := make(map[struct{ app, name string }]ir.ModelIdentity, len(related)+1)
	add := func(snapshot MigrationModel) error {
		identity := snapshot.Identity()
		if _, duplicate := graph.models[identity]; duplicate {
			return fmt.Errorf("relation graph repeats model %s.%s", identity.AppLabel, identity.ModelName)
		}
		normalized, err := ir.Normalize(ir.Schema{
			FormatVersion: ir.CurrentFormatVersion, AppLabel: snapshot.AppLabel,
			Models: []ir.Model{snapshot.Model},
		})
		if err != nil {
			return fmt.Errorf("relation graph model %s.%s: %w", identity.AppLabel, identity.ModelName, err)
		}
		model := normalized.Models[0]
		if !reflect.DeepEqual(model, snapshot.Model) {
			return fmt.Errorf("relation graph model %s.%s is not exact normalized IR", identity.AppLabel, identity.ModelName)
		}
		if previous, exists := tables[model.DBTable]; exists {
			return fmt.Errorf("relation graph models %s.%s and %s.%s share table %q", previous.AppLabel, previous.ModelName, identity.AppLabel, identity.ModelName, model.DBTable)
		}
		goIdentity := struct{ app, name string }{snapshot.AppLabel, model.GoName}
		if previous, exists := goNames[goIdentity]; exists {
			return fmt.Errorf("relation graph models %s.%s and %s.%s share Go name %q", previous.AppLabel, previous.ModelName, identity.AppLabel, identity.ModelName, model.GoName)
		}
		var primaryKey ir.Field
		for _, field := range model.Fields {
			if field.PrimaryKey {
				primaryKey = field
			}
		}
		if primaryKey.Kind != ir.FieldAuto || primaryKey.Nullable {
			return fmt.Errorf("relation graph model %s.%s requires a non-null AutoField primary key", identity.AppLabel, identity.ModelName)
		}
		graph.models[identity] = model
		graph.keys[identity] = primaryKey
		graph.order = append(graph.order, identity)
		tables[model.DBTable] = identity
		goNames[goIdentity] = identity
		return nil
	}
	if err := add(source); err != nil {
		return RelationGraph{}, err
	}
	for index, model := range related {
		if index > 0 && !migrationModelIdentityLess(related[index-1].Identity(), model.Identity()) {
			return RelationGraph{}, fmt.Errorf("relation graph targets are not in unique app/model order at %d", index)
		}
		if err := add(model); err != nil {
			return RelationGraph{}, err
		}
	}
	sort.Slice(graph.order, func(left, right int) bool {
		return migrationModelIdentityLess(graph.order[left], graph.order[right])
	})

	// Iterative traversal bounds work by vertices and fields, including cycles.
	// Reverse ownership is global to each target model, even when two sources
	// reach that target through different paths or different apps.
	type reverseIdentity struct {
		target ir.ModelIdentity
		name   string
	}
	type reverseOwner struct {
		source ir.ModelIdentity
		field  string
	}
	owners := make(map[reverseIdentity]reverseOwner)
	fieldNames := make(map[ir.ModelIdentity]map[string]struct{}, len(graph.models))
	for identity, model := range graph.models {
		names := make(map[string]struct{}, len(model.Fields))
		for _, field := range model.Fields {
			names[field.Name] = struct{}{}
		}
		fieldNames[identity] = names
	}
	visited := map[ir.ModelIdentity]bool{graph.root: true}
	queue := []ir.ModelIdentity{graph.root}
	if !requireReachability {
		queue = append([]ir.ModelIdentity(nil), graph.order...)
		for _, identity := range queue {
			visited[identity] = true
		}
	}
	for position := 0; position < len(queue); position++ {
		identity := queue[position]
		model := graph.models[identity]
		for _, field := range model.Fields {
			if field.Kind != ir.FieldForeignKey {
				continue
			}
			targetIdentity := field.Relation.Target
			_, exists := graph.models[targetIdentity]
			if !exists {
				return RelationGraph{}, fmt.Errorf("relation graph %s.%s.%s has missing target %s.%s", identity.AppLabel, identity.ModelName, field.Name, targetIdentity.AppLabel, targetIdentity.ModelName)
			}
			if name := field.Relation.Reverse.Name; name != "" {
				if _, collision := fieldNames[targetIdentity][name]; collision {
					return RelationGraph{}, fmt.Errorf("relation graph reverse name %q collides with %s.%s field", name, targetIdentity.AppLabel, targetIdentity.ModelName)
				}
				key := reverseIdentity{targetIdentity, name}
				if previous, exists := owners[key]; exists {
					return RelationGraph{}, fmt.Errorf("relation graph reverse name %q is owned by both %s.%s.%s and %s.%s.%s", name, previous.source.AppLabel, previous.source.ModelName, previous.field, identity.AppLabel, identity.ModelName, field.Name)
				}
				owners[key] = reverseOwner{identity, field.Name}
			}
			if !visited[targetIdentity] {
				visited[targetIdentity] = true
				queue = append(queue, targetIdentity)
			}
		}
	}
	for _, identity := range graph.order {
		if !visited[identity] {
			return RelationGraph{}, fmt.Errorf("relation graph model %s.%s is unreachable from source", identity.AppLabel, identity.ModelName)
		}
	}
	return graph, nil
}

// MigrationGraphPlan owns the chronological graphs and the exact physical
// before/after model inventories for one migration step. Its state is private
// and immutable; operations can share a target without sharing mutable IR.
type MigrationGraphPlan struct {
	initial    RelationGraph
	final      RelationGraph
	operations []RelationGraph
}

// ResolveMigrationGraphPlan checks model continuity across the entire step.
// Backend-specific deltas, identifiers, storage limits and physical catalog
// checks are additional requirements. A target is never filled from a future
// operation or from the current application's runtime registry.
func ResolveMigrationGraphPlan(app, name string, unapply bool, intent MigrationIntent) (MigrationGraphPlan, error) {
	if intent.Operations == nil || len(intent.Operations) > migrationGraphMaxModels {
		return MigrationGraphPlan{}, fmt.Errorf("migration graph operation inventory is missing or exceeds its limit")
	}
	if err := scanMigrationGraphIntent(app, name, intent); err != nil {
		return MigrationGraphPlan{}, err
	}
	initial := make(map[ir.ModelIdentity]ir.Model)
	current := make(map[ir.ModelIdentity]ir.Model)
	scheduled := make(map[ir.ModelIdentity]bool)
	for _, operation := range intent.Operations {
		model := operation.After
		if model.Name == "" {
			model = operation.Before
		}
		identity := ir.ModelIdentity{AppLabel: app, ModelName: model.Name}
		if !scheduled[identity] {
			scheduled[identity] = true
			if !reflect.DeepEqual(operation.Before, ir.Model{}) {
				initial[identity] = operation.Before
				current[identity] = operation.Before
			}
		}
	}
	plan := MigrationGraphPlan{operations: make([]RelationGraph, 0, len(intent.Operations))}
	for position, operation := range intent.Operations {
		wantIndex := position
		if unapply {
			wantIndex = len(intent.Operations) - 1 - position
		}
		if operation.OperationIndex != wantIndex {
			return MigrationGraphPlan{}, fmt.Errorf("migration graph operation index %d differs from %d", operation.OperationIndex, wantIndex)
		}
		graph, err := ResolveMigrationGraph(app, operation)
		if err != nil {
			return MigrationGraphPlan{}, fmt.Errorf("migration graph operation %d: %w", operation.OperationIndex, err)
		}
		root := graph.root
		actual, exists := current[root]
		if reflect.DeepEqual(operation.Before, ir.Model{}) {
			if exists {
				return MigrationGraphPlan{}, fmt.Errorf("migration graph creates existing model %s.%s", root.AppLabel, root.ModelName)
			}
		} else if !exists || !reflect.DeepEqual(actual, operation.Before) {
			return MigrationGraphPlan{}, fmt.Errorf("migration graph model %s.%s has a discontinuous Before snapshot", root.AppLabel, root.ModelName)
		}
		for _, identity := range graph.order {
			if identity == root {
				continue
			}
			model := graph.models[identity]
			if actual, exists := current[identity]; exists {
				if !reflect.DeepEqual(actual, model) {
					return MigrationGraphPlan{}, fmt.Errorf("migration graph target %s.%s differs from its visible historical snapshot", identity.AppLabel, identity.ModelName)
				}
			} else if scheduled[identity] {
				return MigrationGraphPlan{}, fmt.Errorf("migration graph target %s.%s is not yet visible or was removed", identity.AppLabel, identity.ModelName)
			} else {
				initial[identity], current[identity] = model, model
			}
		}
		if reflect.DeepEqual(operation.After, ir.Model{}) {
			delete(current, root)
		} else {
			current[root] = operation.After
		}
		plan.operations = append(plan.operations, graph)
	}
	var err error
	if plan.initial, err = migrationGraphBoundary(initial); err != nil {
		return MigrationGraphPlan{}, fmt.Errorf("migration graph initial boundary: %w", err)
	}
	if plan.final, err = migrationGraphBoundary(current); err != nil {
		return MigrationGraphPlan{}, fmt.Errorf("migration graph final boundary: %w", err)
	}
	return plan, nil
}

func scanMigrationGraphIntent(app, name string, intent MigrationIntent) error {
	budget := irresource.New(irresource.Limits{Fields: migrationGraphMaxFields,
		StringBytes: migrationGraphMaxStringBytes, Nodes: migrationGraphMaxNodes, Bytes: migrationGraphMaxBytes})
	if err := budget.ConsumeString("transition.app", app); err != nil {
		return err
	}
	if err := budget.ConsumeString("transition.name", name); err != nil {
		return err
	}
	if err := budget.ConsumeNodes("operations", len(intent.Operations)); err != nil {
		return err
	}
	for index, operation := range intent.Operations {
		prefix := fmt.Sprintf("operations[%d]", index)
		if err := budget.ScanModel(prefix+".before", operation.Before); err != nil {
			return err
		}
		if err := budget.ScanModel(prefix+".after", operation.After); err != nil {
			return err
		}
		if len(operation.Targets) > migrationGraphMaxFields || len(operation.RelatedModels) >= migrationGraphMaxModels {
			return fmt.Errorf("%s exceeds graph target/model limits", prefix)
		}
		if err := budget.ConsumeNodes(prefix+".targets", len(operation.Targets)); err != nil {
			return err
		}
		for _, target := range operation.Targets {
			if err := budget.ScanField(prefix+".target.source", target.SourceField); err != nil {
				return err
			}
			if err := budget.ScanModel(prefix+".target.model", target.TargetModel); err != nil {
				return err
			}
			if err := budget.ScanField(prefix+".target.key", target.TargetKey); err != nil {
				return err
			}
		}
		if err := budget.ConsumeNodes(prefix+".related_models", len(operation.RelatedModels)); err != nil {
			return err
		}
		for _, model := range operation.RelatedModels {
			if err := scanMigrationGraphModel(&budget, prefix+".related_model", model); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrationGraphBoundary(models map[ir.ModelIdentity]ir.Model) (RelationGraph, error) {
	if len(models) == 0 {
		return RelationGraph{order: []ir.ModelIdentity{}, models: map[ir.ModelIdentity]ir.Model{}, keys: map[ir.ModelIdentity]ir.Field{}}, nil
	}
	if len(models) > migrationGraphMaxNodes {
		return RelationGraph{}, fmt.Errorf("migration graph boundary exceeds %d models", migrationGraphMaxNodes)
	}
	ordered := make([]MigrationModel, 0, len(models))
	for identity, model := range models {
		ordered = append(ordered, MigrationModel{AppLabel: identity.AppLabel, Model: model})
	}
	sort.Slice(ordered, func(left, right int) bool {
		return migrationModelIdentityLess(ordered[left].Identity(), ordered[right].Identity())
	})
	graph, err := newRelationGraph(ordered[0], ordered[1:], false)
	if err != nil {
		return RelationGraph{}, err
	}
	// A compact forest may reference a wide target from many sources. Bound
	// the complete physical binding inventory before a backend clones it.
	budget := irresource.New(irresource.Limits{Fields: migrationGraphMaxFields,
		StringBytes: migrationGraphMaxStringBytes, Nodes: migrationGraphMaxNodes, Bytes: migrationGraphMaxBytes})
	for _, identity := range graph.order {
		model := graph.models[identity]
		if err := budget.ScanModel("boundary.model", model); err != nil {
			return RelationGraph{}, err
		}
		for _, field := range model.Fields {
			if field.Kind != ir.FieldForeignKey {
				continue
			}
			if err := budget.ScanField("boundary.target.source", field); err != nil {
				return RelationGraph{}, err
			}
			if err := budget.ScanModel("boundary.target.model", graph.models[field.Relation.Target]); err != nil {
				return RelationGraph{}, err
			}
			if err := budget.ScanField("boundary.target.key", graph.keys[field.Relation.Target]); err != nil {
				return RelationGraph{}, err
			}
		}
	}
	return graph, nil
}

func (plan MigrationGraphPlan) InitialModels() []MigrationModel { return plan.initial.Models() }
func (plan MigrationGraphPlan) FinalModels() []MigrationModel   { return plan.final.Models() }
func (plan MigrationGraphPlan) InitialTargets(identity ir.ModelIdentity) ([]MigrationTarget, error) {
	return plan.initial.Targets(identity)
}
func (plan MigrationGraphPlan) FinalTargets(identity ir.ModelIdentity) ([]MigrationTarget, error) {
	return plan.final.Targets(identity)
}
func (plan MigrationGraphPlan) BoundaryTargets(model ir.Model, final bool) ([]MigrationTarget, error) {
	graph := plan.initial
	if final {
		graph = plan.final
	}
	identity, exists := graph.tables[model.DBTable]
	if !exists || !reflect.DeepEqual(graph.models[identity], model) {
		return nil, fmt.Errorf("model %q is not an exact migration graph boundary", model.DBTable)
	}
	return graph.Targets(identity)
}
func (plan MigrationGraphPlan) Operation(position int) (RelationGraph, bool) {
	if position < 0 || position >= len(plan.operations) {
		return RelationGraph{}, false
	}
	return plan.operations[position], true
}

// ResolveMigrationGraph verifies the complete direct bindings and transitive
// metadata of an operation. In forward creation/addition the source boundary
// is After; deletion/removal uses Before. A self target therefore refers to
// that same exact boundary, not to a second snapshot with stale fields.
func ResolveMigrationGraph(app string, operation MigrationOperation) (RelationGraph, error) {
	source := operation.After
	if operation.Kind == MigrationDeleteModel || operation.Kind == MigrationRemoveField {
		source = operation.Before
	}
	if len(operation.Targets) > migrationGraphMaxFields || len(operation.RelatedModels) >= migrationGraphMaxModels {
		return RelationGraph{}, fmt.Errorf("relation operation graph exceeds its target/model limit")
	}
	budget := irresource.New(irresource.Limits{
		Fields: migrationGraphMaxFields, StringBytes: migrationGraphMaxStringBytes,
		Nodes: migrationGraphMaxNodes, Bytes: migrationGraphMaxBytes,
	})
	root := MigrationModel{AppLabel: app, Model: source}
	if err := scanMigrationGraphModel(&budget, "source", root); err != nil {
		return RelationGraph{}, err
	}
	for index, target := range operation.Targets {
		prefix := fmt.Sprintf("targets[%d]", index)
		if err := budget.ScanField(prefix+".source", target.SourceField); err != nil {
			return RelationGraph{}, err
		}
		if err := budget.ScanModel(prefix+".model", target.TargetModel); err != nil {
			return RelationGraph{}, err
		}
		if err := budget.ScanField(prefix+".key", target.TargetKey); err != nil {
			return RelationGraph{}, err
		}
	}
	for index, model := range operation.RelatedModels {
		if err := scanMigrationGraphModel(&budget, fmt.Sprintf("related[%d]", index), model); err != nil {
			return RelationGraph{}, err
		}
	}

	models := map[ir.ModelIdentity]ir.Model{root.Identity(): source}
	position := 0
	for _, field := range source.Fields {
		if field.Kind != ir.FieldForeignKey {
			continue
		}
		if field.Relation == nil || position >= len(operation.Targets) {
			return RelationGraph{}, fmt.Errorf("relation operation lacks target for source field %q", field.Name)
		}
		target := operation.Targets[position]
		if !reflect.DeepEqual(target.SourceField, field) || target.TargetModel.Name != field.Relation.Target.ModelName {
			return RelationGraph{}, fmt.Errorf("relation operation target %d differs from its source field", position)
		}
		identity := field.Relation.Target
		if previous, exists := models[identity]; exists && !reflect.DeepEqual(previous, target.TargetModel) {
			return RelationGraph{}, fmt.Errorf("relation operation has conflicting snapshots for %s.%s", identity.AppLabel, identity.ModelName)
		}
		models[identity] = target.TargetModel
		position++
	}
	if position != len(operation.Targets) {
		return RelationGraph{}, fmt.Errorf("relation operation carries targets without matching source fields")
	}
	for index, model := range operation.RelatedModels {
		identity := model.Identity()
		if index > 0 && !migrationModelIdentityLess(operation.RelatedModels[index-1].Identity(), identity) {
			return RelationGraph{}, fmt.Errorf("transitive relation models are not in unique app/model order")
		}
		if _, exists := models[identity]; exists {
			return RelationGraph{}, fmt.Errorf("transitive relation model %s.%s duplicates an existing vertex", identity.AppLabel, identity.ModelName)
		}
		models[identity] = model.Model
	}
	related := make([]MigrationModel, 0, len(models)-1)
	for identity, model := range models {
		if identity != root.Identity() {
			related = append(related, MigrationModel{AppLabel: identity.AppLabel, Model: model})
		}
	}
	sort.Slice(related, func(left, right int) bool {
		return migrationModelIdentityLess(related[left].Identity(), related[right].Identity())
	})
	graph, err := NewRelationGraph(root, related)
	if err != nil {
		return RelationGraph{}, err
	}
	for _, target := range operation.Targets {
		if !reflect.DeepEqual(target.TargetKey, graph.keys[target.SourceField.Relation.Target]) {
			return RelationGraph{}, fmt.Errorf("relation operation target key is not the exact historical AutoField")
		}
	}
	return graph, nil
}

func scanMigrationGraphModel(budget *irresource.Budget, path string, model MigrationModel) error {
	if err := budget.ConsumeString(path+".app", model.AppLabel); err != nil {
		return err
	}
	return budget.ScanModel(path+".model", model.Model)
}

func migrationModelIdentityLess(left, right ir.ModelIdentity) bool {
	if left.AppLabel != right.AppLabel {
		return left.AppLabel < right.AppLabel
	}
	return left.ModelName < right.ModelName
}

func (graph RelationGraph) Root() ir.ModelIdentity { return graph.root }

func (graph RelationGraph) Model(identity ir.ModelIdentity) (ir.Model, bool) {
	model, exists := graph.models[identity]
	if !exists {
		return ir.Model{}, false
	}
	return cloneIntentModel(model), true
}

// Models returns every vertex, including the source, in app/model order.
func (graph RelationGraph) Models() []MigrationModel {
	if graph.order == nil {
		return nil
	}
	models := make([]MigrationModel, len(graph.order))
	for index, identity := range graph.order {
		models[index] = MigrationModel{AppLabel: identity.AppLabel, Model: cloneIntentModel(graph.models[identity])}
	}
	return models
}

// Targets resolves the complete field-ordered relation bindings of a graph
// vertex. Count the expanded payload before cloning, so a compact graph cannot
// amplify into an unbounded set of repeated target model snapshots.
func (graph RelationGraph) Targets(identity ir.ModelIdentity) ([]MigrationTarget, error) {
	model, exists := graph.models[identity]
	if !exists {
		return nil, fmt.Errorf("relation graph has no model %s.%s", identity.AppLabel, identity.ModelName)
	}
	budget := irresource.New(irresource.Limits{
		Fields: migrationGraphMaxFields, StringBytes: migrationGraphMaxStringBytes,
		Nodes: migrationGraphMaxNodes, Bytes: migrationGraphMaxBytes,
	})
	count := 0
	for _, field := range model.Fields {
		if field.Kind != ir.FieldForeignKey {
			continue
		}
		prefix := fmt.Sprintf("targets[%d]", count)
		if err := budget.ScanField(prefix+".source", field); err != nil {
			return nil, err
		}
		if err := budget.ScanModel(prefix+".model", graph.models[field.Relation.Target]); err != nil {
			return nil, err
		}
		if err := budget.ScanField(prefix+".key", graph.keys[field.Relation.Target]); err != nil {
			return nil, err
		}
		count++
	}
	targets := make([]MigrationTarget, 0, count)
	for _, field := range model.Fields {
		if field.Kind == ir.FieldForeignKey {
			targets = append(targets, MigrationTarget{
				SourceField: field.Clone(), TargetModel: cloneIntentModel(graph.models[field.Relation.Target]),
				TargetKey: graph.keys[field.Relation.Target].Clone(),
			})
		}
	}
	return targets, nil
}
