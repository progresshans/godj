package query

import "slices"

// QueryUpdatePlan applies one assignment set to all rows selected by its
// original predicate. Ordering, distinct, eager projections and read locks do
// not narrow UPDATE. Slices are rejected even when assignments are empty.
type QueryUpdatePlan struct {
	selection   Plan
	key         FieldRef
	assignments []ScalarAssignment
}

func NewQueryUpdatePlan(source Plan, key FieldRef, assignments []ScalarAssignment) (QueryUpdatePlan, error) {
	selection, err := updateSelection(source, key)
	if err != nil {
		return QueryUpdatePlan{}, err
	}
	plan := QueryUpdatePlan{selection: selection, key: key, assignments: assignments}
	if err := plan.Validate(); err != nil {
		return QueryUpdatePlan{}, err
	}
	plan.selection.sourceFields = slices.Clone(source.sourceFields)
	plan.assignments = slices.Clone(assignments)
	return plan, nil
}

// All native update forms retain the same predicate and collection scopes.
func updateSelection(source Plan, key FieldRef) (Plan, error) {
	if source.limit != nil || source.offset != nil && *source.offset != 0 || source.prefetchWindow != nil {
		return Plan{}, &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: "update cannot use a sliced or windowed source"}
	}
	selection := source
	selection.offset = nil
	selection.orderings, selection.relationProjections, selection.rowLock = nil, nil, nil
	selection.distinct, selection.reuseCollectionFilter = false, false
	selection.result = ResultShape{kind: ResultProjection, expressions: []ResultExpression{FieldResult(key)}}
	return selection, nil
}

func (plan QueryUpdatePlan) Selection() Plan                 { return plan.selection }
func (plan QueryUpdatePlan) Key() FieldRef                   { return plan.key }
func (plan QueryUpdatePlan) Assignments() []ScalarAssignment { return slices.Clone(plan.assignments) }
func (plan QueryUpdatePlan) NoOp() bool {
	return len(plan.assignments) == 0 || plan.selection.EmptyResult()
}
func (plan QueryUpdatePlan) Equal(other QueryUpdatePlan) bool {
	return plan.key == other.key && plan.selection.Equal(other.selection) && slices.EqualFunc(plan.assignments, other.assignments, ScalarAssignment.Equal)
}

func (plan QueryUpdatePlan) Validate() error {
	if plan.selection.table == "" || !validScalarField(plan.key) || plan.key.Kind() != FieldInteger || plan.key.Nullable() {
		return invalidPlanError("query update requires a table and a non-null integer primary key")
	}
	if len(plan.selection.sourceFields) == 0 || len(plan.selection.sourceFields) > MaximumBulkValues || len(plan.assignments) > MaximumScalarNodes {
		return invalidPlanError("query update requires bounded source fields and assignments")
	}
	columns, names := make(map[string]FieldRef), make(map[string]bool)
	for _, field := range plan.selection.sourceFields {
		if !validScalarField(field) || names[field.Name()] {
			return invalidPlanError("query update source has an invalid or repeated field")
		}
		if _, found := columns[field.Column()]; found {
			return invalidPlanError("query update source repeats a column")
		}
		columns[field.Column()], names[field.Name()] = field, true
	}
	if columns[plan.key.Column()] != plan.key {
		return invalidPlanError("query update key differs from source metadata")
	}
	seen, nodes := map[string]bool{}, 0
	for _, assignment := range plan.assignments {
		if err := assignment.Validate(); err != nil {
			return err
		}
		field := assignment.Field()
		if columns[field.Column()] != field || seen[field.Column()] {
			return invalidPlanError("query update assigns a foreign or repeated field")
		}
		seen[field.Column()] = true
		nodes += assignment.expression.NodeCount()
		if nodes > MaximumScalarNodes {
			return invalidPlanError("query update exceeds its total scalar node budget")
		}
		if err := assignment.expression.validateSource(columns); err != nil {
			return err
		}
	}
	if plan.selection.where.node != nil {
		if err := plan.selection.where.validate(); err != nil {
			return err
		}
		if err := plan.selection.validateWhereSource(plan.selection.where); err != nil {
			return err
		}
	}
	return nil
}
