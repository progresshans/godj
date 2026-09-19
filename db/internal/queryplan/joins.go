package queryplan

import (
	"fmt"
	"slices"

	"github.com/progresshans/godj/query"
)

// Join contains unquoted SQL-independent names. Compilers own qualification,
// quoting, placeholders and the final SQL text.
type Join struct {
	Table, RootColumn, Column, Alias string
	LeftOuter                        bool
}

type Joins struct {
	Keys          []RelationKey
	ByKey         map[RelationKey]Join
	ProjectionKey RelationKey
}

// PrepareJoins consumes the compiler's private edge inventory. Predicate
// capabilities and duplicate-hop equality must already have been checked.
// It validates projection provenance and owns deterministic alias allocation.
func PrepareJoins(plan query.Plan, edges map[RelationKey]query.RelationHop, sourceKeys []query.RelationHop, backendName string) (Joins, error) {
	projection, selected := plan.RelationProjection()
	var projectionKey RelationKey
	if selected {
		var err error
		projectionKey, err = RelationProjection(plan, projection, backendName)
		if err != nil {
			return Joins{}, err
		}
		hop := projection.Hop()
		if previous, exists := edges[projectionKey]; exists && !previous.Equal(hop) {
			return Joins{}, invalidPlan(fmt.Sprintf("relation edge %s.%s.%s has inconsistent predicate and projection metadata", projectionKey.SourceApp, projectionKey.SourceModel, projectionKey.Field))
		}
		edges[projectionKey] = hop
		if err := projectionJoinProvenance(plan.SourceFields(), hop, edges, sourceKeys); err != nil {
			return Joins{}, err
		}
	}

	// Source-key presence can prove that a joined target exists only for the
	// exact same edge. A matching logical identity alone is insufficient when
	// callers construct AST paths with conflicting physical metadata.
	for _, sourceKey := range sourceKeys {
		if hop, exists := edges[KeyForRelation(sourceKey)]; exists && !sourceKey.Equal(hop) {
			return Joins{}, invalidPlan("relation source-key provenance does not match the predicate edge")
		}
	}

	keys := make([]RelationKey, 0, len(edges))
	for key := range edges {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, CompareRelationKey)
	required := map[RelationKey]bool(nil)
	if where, ok := plan.Where(); ok {
		required = requiredForwardJoins(where, false)
	}
	joins := make(map[RelationKey]Join, len(keys))
	for index, key := range keys {
		hop := edges[key]
		join := Join{
			Table:      hop.TargetTable(),
			RootColumn: hop.SourceColumn(),
			Column:     hop.TargetPrimaryKeyColumn(),
			Alias:      fmt.Sprintf("t%d", index+1),
			LeftOuter:  hop.Direction() == query.RelationForward && hop.Nullable() && !required[key],
		}
		if hop.Direction() == query.RelationReverse {
			join.Table = hop.SourceTable()
			join.RootColumn = hop.TargetPrimaryKeyColumn()
			join.Column = hop.SourceColumn()
		}
		joins[key] = join
	}
	return Joins{Keys: keys, ByKey: joins, ProjectionKey: projectionKey}, nil
}

// A projection anchors the logical root identity. Other filter joins may use
// different edges, but a forward FK declaration must still describe exactly
// one target. RelationKey alone cannot establish this: its target identity
// differs when two paths forge different targets for the same source field.
func projectionJoinProvenance(fields []query.FieldRef, selected query.RelationHop, edges map[RelationKey]query.RelationHop, sourceKeys []query.RelationHop) error {
	declarations := make(map[string]query.RelationHop, len(edges)+len(sourceKeys))
	check := func(hop query.RelationHop) error {
		if hop.Direction() == query.RelationReverse {
			if hop.Target() != selected.Source() {
				return invalidPlan("reverse filter root identity does not match the relation projection")
			}
			if hop.Source() != selected.Source() {
				return nil
			}
			// A self-reference is another view of a FK owned by this root,
			// not an independent declaration on an unrelated source model.
			field := query.NewFieldRef(hop.Field(), hop.SourceColumn(), query.FieldInteger, hop.Nullable())
			if hop.SourceTable() != selected.SourceTable() || !ContainsField(fields, field) {
				return invalidPlan("self-reference source key does not match the selected root metadata")
			}
		} else if hop.Source() != selected.Source() {
			return invalidPlan("forward filter root identity does not match the relation projection")
		}
		if previous, exists := declarations[hop.Field()]; exists && !sameForeignKeyDeclaration(previous, hop) {
			return invalidPlan("source-key provenance conflicts across projection and filter edges")
		}
		declarations[hop.Field()] = hop
		return nil
	}
	for _, hop := range edges {
		if err := check(hop); err != nil {
			return err
		}
	}
	for _, hop := range sourceKeys {
		if err := check(hop); err != nil {
			return err
		}
	}
	return nil
}

// Direction, reverse accessor and traversal cardinality describe the view of
// an edge; the physical FK declaration is the same from either direction.
func sameForeignKeyDeclaration(left, right query.RelationHop) bool {
	return left.Source() == right.Source() && left.SourceTable() == right.SourceTable() &&
		left.Field() == right.Field() && left.SourceColumn() == right.SourceColumn() &&
		left.Target() == right.Target() && left.TargetTable() == right.TargetTable() &&
		left.TargetPrimaryKeyColumn() == right.TargetPrimaryKeyColumn() && left.Nullable() == right.Nullable()
}

// requiredForwardJoins finds edges whose joined row must exist for the
// predicate to be true. It owns only join presence, not SQL rendering. Odd
// negation swaps AND/OR and nullable negated leaves can match an absent row.
// Keeping an optional edge outer is conservative when no proof is available.
func requiredForwardJoins(expression query.Expression, negated bool) map[RelationKey]bool {
	switch expression.Kind() {
	case query.ExpressionLeaf:
		condition, ok := expression.Condition()
		if !ok {
			return nil
		}
		path, related := condition.RelationPath()
		if !related {
			return nil
		}
		hops := path.Hops()
		if len(hops) != 1 || hops[0].Direction() != query.RelationForward {
			return nil
		}
		if path.TerminalScope() == query.RelationTerminalSourceKey && condition.Lookup() == query.LookupIsNull {
			isNull, valid := condition.Value().Boolean()
			if valid && isNull == negated {
				// A present FK implies its target exists under the declared FK
				// constraint. This can promote an edge used elsewhere in the tree.
				return map[RelationKey]bool{KeyForRelation(hops[0]): true}
			}
			return nil
		}
		if path.TerminalScope() != query.RelationTerminalRelatedField {
			return nil
		}
		if condition.Lookup() == query.LookupIsNull {
			isNull, valid := condition.Value().Boolean()
			if valid && isNull == negated {
				return map[RelationKey]bool{KeyForRelation(hops[0]): true}
			}
			return nil
		}
		if !negated {
			switch condition.Lookup() {
			case query.LookupExact, query.LookupGreaterThan, query.LookupGreaterThanOrEqual,
				query.LookupLessThan, query.LookupLessThanOrEqual, query.LookupIContains, query.LookupIn:
				return map[RelationKey]bool{KeyForRelation(hops[0]): true}
			}
		}
		return nil
	case query.ExpressionNot:
		children := expression.Children()
		if len(children) != 1 {
			return nil
		}
		return requiredForwardJoins(children[0], !negated)
	case query.ExpressionAnd, query.ExpressionOr:
		union := (expression.Kind() == query.ExpressionAnd) != negated
		var required map[RelationKey]bool
		for index, child := range expression.Children() {
			current := requiredForwardJoins(child, negated)
			if index == 0 {
				required = current
				continue
			}
			if union {
				if required == nil {
					required = make(map[RelationKey]bool, len(current))
				}
				for key := range current {
					required[key] = true
				}
			} else {
				for key := range required {
					if !current[key] {
						delete(required, key)
					}
				}
			}
		}
		return required
	default:
		return nil
	}
}
