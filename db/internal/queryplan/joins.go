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
		for _, sourceKey := range sourceKeys {
			if SameSourceEdge(sourceKey, hop) && !sourceKey.Equal(hop) {
				return Joins{}, invalidPlan("relation projection source-key provenance does not match the selected edge")
			}
		}
		if previous, exists := edges[projectionKey]; exists && !previous.Equal(hop) {
			return Joins{}, invalidPlan(fmt.Sprintf("relation edge %s.%s.%s has inconsistent predicate and projection metadata", projectionKey.SourceApp, projectionKey.SourceModel, projectionKey.Field))
		}
		edges[projectionKey] = hop
		for key := range edges {
			if key != projectionKey {
				return Joins{}, invalidPlan(backendName + " relation projection cannot combine unrelated relation joins")
			}
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
