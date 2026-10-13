package query

import "slices"

// ValidateScalar binds both original row leaves and related routes to this
// plan, or binds selected cells to its owned result scope. A field list alone
// cannot authorize relation traversal.
func (p Plan) ValidateScalar(expression ScalarExpression) error {
	if p.derived != nil {
		return expression.ValidateResultSource(p.derived.scope)
	}
	if err := expression.validateSourceFields(p.sourceFields); err != nil {
		return err
	}
	for _, path := range expression.RelationPaths() {
		if err := p.validateValueRelation(path); err != nil {
			return err
		}
	}
	return nil
}

func (p Plan) validateValueRelation(path RelationPath) error {
	if err := p.validateRelationSource(path); err != nil {
		return err
	}
	if !path.SingleValued() {
		return groupUnsupported("scalar values require single-valued relation paths")
	}
	return nil
}

// Source authority is common to row values and aggregate fields; cardinality
// belongs to their separate expression contexts.
func (p Plan) validateRelationSource(path RelationPath) error {
	if err := path.Validate(); err != nil {
		return err
	}
	if len(path.keys) != 0 && !slices.Contains(p.sourceFields, path.keys[0]) {
		return invalidPlanError("value relation root primary key differs from source metadata")
	}
	root := path.hops[0]
	if root.Direction() == RelationReverse {
		if root.TargetTable() != p.table || !containsPlanIntegerColumn(p.sourceFields, root.TargetPrimaryKeyColumn()) {
			return invalidPlanError("value reverse relation root key is not part of the plan source metadata")
		}
	} else if root.SourceTable() != p.table || !slices.Contains(p.sourceFields, NewFieldRef(root.Field(), root.SourceColumn(), FieldInteger, root.Nullable())) {
		return invalidPlanError("value relation source key is not part of the plan source metadata")
	}
	return nil
}

func (p RelationPath) validateValueSelection() error {
	if err := p.Validate(); err != nil {
		return err
	}
	if !p.SingleValued() || p.scope != RelationTerminalRelatedField {
		return groupUnsupported("scalar selection requires a single-valued related-field route")
	}
	return nil
}
