package model

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// A graph allocation belongs to the same typed owner as the ordinary INSERT.
// Only a newly created identity can reserve a key. The complete scalar row is
// prepared after every required peer key in this component has been reserved.
type transitionGraphAllocation struct {
	reservation query.AutoKeyPlan
	fields      int
	prepare     func(context.Context, db.RelationSession, auth.Principal, transitionTargetMappings, int64, map[string]bool) (transitionGraphInsertion, error)
}

type transitionGraphInsertion struct {
	plan   query.InsertPlan
	values map[string]query.Value
	accept func(context.Context, db.RelationSession, int64) error
}

func newTransitionGraphAllocation(metadata ir.Model, prepare func(context.Context, db.RelationSession, auth.Principal, transitionTargetMappings, int64, map[string]bool) (transitionGraphInsertion, error)) (*transitionGraphAllocation, error) {
	key, found := primaryField(metadata)
	if !found || prepare == nil {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	plan, err := query.NewAutoKeyPlan(metadata.DBTable, orm.NewAssignment(key, query.Integer(0)).Field(), 1)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	return &transitionGraphAllocation{reservation: plan, fields: len(metadata.Fields), prepare: prepare}, nil
}

func transitionGraphCapabilities(ctx context.Context, scope db.RelationSession, plan transitionReferencePlan, allocations []transitionAllocation) (db.AutoKeyAllocator, db.GraphInserter, error) {
	if len(plan.graphs) == 0 {
		return nil, nil, nil
	}
	allocator, canReserve := scope.(db.AutoKeyAllocator)
	inserter, canInsert := scope.(db.GraphInserter)
	if !canReserve || !canInsert || nilModelValue(allocator) || nilModelValue(inserter) {
		return nil, nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported,
			Detail: "required transition cycles need native key reservation and insertion graphs"}
	}
	limits, err := inserter.InsertGraphLimits(ctx)
	if err != nil {
		return nil, nil, err
	}
	if limits.Rows < 1 || limits.Parameters < 1 {
		return nil, nil, managementError(CodeInvalidConfig, nil)
	}
	// Reject an unavailable or oversized later component before earlier rows
	// or sequences are written. A required cycle cannot be split into batches.
	for _, index := range plan.order {
		group, native := plan.graphs[index]
		if !native {
			continue
		}
		fields := 0
		if len(group) > limits.Rows {
			return nil, nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "required transition cycle exceeds the backend row bound"}
		}
		for _, member := range group {
			node := allocations[member].graph
			if node == nil || node.prepare == nil || node.reservation.Count() != 1 || node.reservation.Validate() != nil || node.fields < 1 {
				return nil, nil, managementError(CodeInvalidConfig, nil)
			}
			if node.fields > limits.Parameters-fields {
				return nil, nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "required transition cycle exceeds the backend parameter bound"}
			}
			fields += node.fields
		}
	}
	return allocator, inserter, validateAccountSessionScope(ctx, scope)
}

func cloneTransitionTargetMappings(input transitionTargetMappings) transitionTargetMappings {
	owned := make(transitionTargetMappings, len(input))
	for kind, namespace := range input {
		owned[kind] = transitionTargetNamespace{revisions: maps.Clone(namespace.revisions), targets: maps.Clone(namespace.targets)}
	}
	return owned
}

// The returned keys are accepted only after every actual INSERT result and
// every complete typed row has been checked. Provisional reserved mappings
// are confined to this component's preparation and never escape an error.
func applyTransitionGraph(ctx context.Context, scope db.RelationSession, actor auth.Principal, allocator db.AutoKeyAllocator, inserter db.GraphInserter, group []int, deferred []map[string]bool, allocations []transitionAllocation, allocated transitionTargetMappings, known map[TransitionReferenceKind]map[int64]int64, budget *transitionDigestWriter) ([]int64, error) {
	provisional := cloneTransitionTargetMappings(allocated)
	keys := make(map[int]int64, len(group))
	type nativeAllocator struct {
		table string
		key   query.FieldRef
	}
	var ordered []nativeAllocator
	batches := make(map[nativeAllocator][]int)
	for _, index := range group {
		plan := allocations[index].graph.reservation
		identity := nativeAllocator{plan.Table(), plan.Key()}
		if _, exists := batches[identity]; !exists {
			ordered = append(ordered, identity)
		}
		batches[identity] = append(batches[identity], index)
	}
	for _, identity := range ordered {
		indices := batches[identity]
		plan, err := query.NewAutoKeyPlan(identity.table, identity.key, len(indices))
		if err != nil {
			return nil, err
		}
		reserved, err := allocator.ReserveAutoKeys(ctx, plan)
		if err := errors.Join(err, validateAccountSessionScope(ctx, scope)); err != nil {
			return nil, err
		}
		if len(reserved) != len(indices) {
			return nil, managementError(CodePersistence, nil)
		}
		seen := make(map[int64]bool, len(reserved))
		for position, index := range indices {
			key := reserved[position]
			node := allocations[index]
			namespace := provisional[node.reference.kind]
			if _, exists := namespace.targets[node.reference.source.ID]; exists || seen[key] {
				return nil, managementError(CodeConflict, nil)
			}
			if _, exists := known[node.reference.kind][key]; exists {
				return nil, managementError(CodeConflict, nil)
			}
			keys[index], seen[key] = key, true
			namespace.targets[node.reference.source.ID] = key
		}
	}
	rows := make([]transitionGraphInsertion, len(group))
	inserts := make([]query.InsertPlan, len(group))
	for position, index := range group {
		node := allocations[index]
		prepared, err := node.graph.prepare(ctx, scope, actor, provisional, keys[index], maps.Clone(deferred[index]))
		if err := errors.Join(err, validateAccountSessionScope(ctx, scope)); err != nil {
			return nil, err
		}
		key, present := prepared.plan.ReturningKey()
		assignments := prepared.plan.Assignments()
		if prepared.accept == nil || !present || key != node.graph.reservation.Key() || prepared.plan.Table() != node.graph.reservation.Table() || len(assignments) != node.graph.fields || len(prepared.values) != len(assignments) {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		found := false
		for _, assignment := range assignments {
			value, present := prepared.values[assignment.Field().Name()]
			if !present || !value.Equal(assignment.Value()) {
				return nil, managementError(CodeConflict, nil)
			}
			if assignment.Field() == key {
				actual, integer := assignment.Value().Integer()
				if !integer || actual != keys[index] {
					return nil, managementError(CodeConflict, nil)
				}
				found = true
			}
		}
		if !found {
			return nil, managementError(CodeConflict, nil)
		}
		if err := writeTransitionValues(budget, prepared.values); err != nil {
			return nil, err
		}
		rows[position], inserts[position] = prepared, prepared.plan
	}
	plan, err := query.NewInsertGraphPlan(inserts)
	if err != nil {
		return nil, err
	}
	actual, err := inserter.InsertGraph(ctx, plan)
	if err := errors.Join(err, validateAccountSessionScope(ctx, scope)); err != nil {
		return nil, err
	}
	if len(actual) != len(group) {
		return nil, managementError(CodePersistence, nil)
	}
	for position, index := range group {
		if actual[position] != keys[index] {
			return nil, managementError(CodeConflict, nil)
		}
	}
	for position, index := range group {
		if err := rows[position].accept(ctx, scope, actual[position]); err != nil {
			return nil, err
		}
		if err := validateAccountSessionScope(ctx, scope); err != nil {
			return nil, err
		}
		if !maps.Equal(rows[position].values, allocations[index].values()) {
			return nil, managementError(CodeConflict, nil)
		}
	}
	return slices.Clone(actual), nil
}

func prepareTransitionGraphInsert[M any](objects orm.Manager[M], prepared orm.PreparedModel[M], key int64, publish func(context.Context, db.RelationSession, M) error) (transitionGraphInsertion, error) {
	var zero transitionGraphInsertion
	metadata, err := objects.Metadata()
	if err != nil || publish == nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	primary, found := primaryField(metadata)
	if !found {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	row, err := prepared.Model()
	if err != nil {
		return zero, err
	}
	_, present, err := objects.PrimaryKey(row)
	if err != nil || present {
		return zero, managementError(CodeInvalidInput, err)
	}
	values, err := objects.ModelValues(row)
	if err != nil || len(values) != len(metadata.Fields) || !values[primary.Name].IsNull() {
		return zero, managementError(CodeInvalidInput, err)
	}
	expected := maps.Clone(values)
	expected[primary.Name] = query.Integer(key)
	if err := validateTransitionScalarValues(metadata, expected, false, nil); err != nil {
		return zero, err
	}
	assignments := make([]query.Assignment, len(metadata.Fields))
	for index, field := range metadata.Fields {
		value, found := expected[field.Name]
		if !found {
			return zero, managementError(CodeInvalidInput, nil)
		}
		assignments[index] = orm.NewAssignment(field, value)
	}
	return transitionGraphInsertion{plan: query.NewInsertPlanReturningKey(metadata.DBTable, assignments, orm.NewAssignment(primary, query.Integer(key)).Field()), values: maps.Clone(expected),
		accept: func(ctx context.Context, scope db.RelationSession, actual int64) error {
			if err := validateAccountSessionScope(ctx, scope); err != nil {
				return err
			}
			if actual != key {
				return managementError(CodeConflict, nil)
			}
			row, err := objects.Using(scope).Filter(orm.NewAutoField[M](primary).Exact(actual)).Get(ctx)
			if err != nil {
				return err
			}
			stored, err := objects.ModelValues(row)
			if err != nil || !maps.Equal(stored, expected) {
				return managementError(CodeConflict, err)
			}
			if err := validateAccountSessionScope(ctx, scope); err != nil {
				return err
			}
			return publish(ctx, scope, row)
		}}, nil
}

func catalogTransitionGraphAllocation[M any](project orm.ProjectBinding, model transitionCatalogModel[M], candidate *catalogCreationCandidate[M], partial, complete *transitionInsertions, mapping *AccountMapping) (*transitionGraphAllocation, error) {
	if !model.created {
		return nil, nil
	}
	return newTransitionGraphAllocation(model.metadata, func(ctx context.Context, scope db.RelationSession, actor auth.Principal, references transitionTargetMappings, key int64, deferred map[string]bool) (transitionGraphInsertion, error) {
		resolved := *candidate
		if err := resolveCatalogCreationCandidate(ctx, scope, actor, &resolved, references, transitionCreationPhase[M]{allocating: true, deferred: deferred}); err != nil {
			return transitionGraphInsertion{}, err
		}
		return prepareTransitionGraphInsert(model.objects, resolved.prepared, key, func(ctx context.Context, scope db.RelationSession, row M) error {
			resolved.row = row
			actual, _, err := recordCatalogCreationCandidate(model, &resolved)
			if err != nil || actual != key {
				return managementError(CodePersistence, err)
			}
			inventory := complete
			if len(deferred) != 0 {
				inventory = partial
			}
			if _, err := addTransitionIdentity(ctx, scope, inventory, project, model.identity, model.objects, row); err != nil {
				return err
			}
			*candidate = resolved
			*mapping = AccountMapping{SourceID: resolved.source.ID, SourceRevision: resolved.source.Revision, TargetID: actual, TargetRevision: resolved.source.TargetRevision}
			return nil
		})
	})
}
