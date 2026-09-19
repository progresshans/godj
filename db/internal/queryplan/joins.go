package queryplan

import (
	"fmt"
	"slices"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// Join contains unquoted SQL-independent names. Compilers own qualification,
// quoting, placeholders and the final SQL text.
type Join struct {
	Table, FromAlias, FromColumn, Column, Alias string
	LeftOuter                                   bool
}

type Joins struct {
	Keys  []RelationKey
	ByKey map[RelationKey]Join
}

// PrepareJoins validates the private occurrence graph and declaration inventory.
// Every compilation owns its maps; aliases and parent joins are deterministic.
func PrepareJoins(plan query.Plan, backendName string) (Joins, error) {
	type edge struct {
		hop       query.RelationHop
		parent    RelationKey
		hasParent bool
		filter    bool
	}
	edges := make(map[RelationKey]edge)
	type declarationKey struct {
		source ir.ModelIdentity
		field  string
	}
	declarations := make(map[declarationKey]query.RelationHop)
	tables := make(map[ir.ModelIdentity]string)
	primaryKeys := make(map[ir.ModelIdentity]string)
	var root ir.ModelIdentity
	anchor := func(identity ir.ModelIdentity) error {
		if root == (ir.ModelIdentity{}) {
			root = identity
			tables[root] = plan.Table()
		} else if root != identity {
			return invalidPlan("relation routes do not share one logical root")
		}
		return nil
	}
	declare := func(hop query.RelationHop) error {
		for _, model := range []struct {
			identity ir.ModelIdentity
			table    string
		}{{hop.Source(), hop.SourceTable()}, {hop.Target(), hop.TargetTable()}} {
			if previous, exists := tables[model.identity]; exists && previous != model.table {
				return invalidPlan("one relation model has conflicting table metadata")
			}
			tables[model.identity] = model.table
		}
		if previous, exists := primaryKeys[hop.Target()]; exists && previous != hop.TargetPrimaryKeyColumn() {
			return invalidPlan("one relation target has conflicting primary key metadata")
		}
		primaryKeys[hop.Target()] = hop.TargetPrimaryKeyColumn()
		if hop.Source() == root && !ContainsField(plan.SourceFields(), query.NewFieldRef(hop.Field(), hop.SourceColumn(), query.FieldInteger, hop.Nullable())) {
			return invalidPlan("root-owned relation source key disagrees with selected model metadata")
		}
		key := declarationKey{hop.Source(), hop.Field()}
		if previous, exists := declarations[key]; exists && !sameForeignKeyDeclaration(previous, hop) {
			return invalidPlan("source-key provenance conflicts across projection and filter routes")
		}
		declarations[key] = hop
		return nil
	}
	addPath := func(path query.RelationPath, filter bool) error {
		if err := path.Validate(); err != nil {
			return err
		}
		hops := path.Hops()
		identity := hops[0].Source()
		if hops[0].Direction() == query.RelationReverse {
			identity = hops[0].Target()
		}
		if err := anchor(identity); err != nil {
			return err
		}
		materialized := len(hops)
		if path.TerminalScope() == query.RelationTerminalSourceKey {
			materialized--
		}
		for index, hop := range hops {
			if err := declare(hop); err != nil {
				return err
			}
			if index >= materialized {
				continue
			}
			key := KeyForPath(hops[:index+1])
			if previous, exists := edges[key]; exists && !previous.hop.Equal(hop) {
				return invalidPlan("one relation route has conflicting hop metadata")
			}
			item := edge{hop: hop, hasParent: index > 0, filter: filter || edges[key].filter}
			if index > 0 {
				item.parent = KeyForPath(hops[:index])
			}
			edges[key] = item
		}
		return nil
	}
	for _, condition := range plan.Conditions() {
		if path, related := condition.RelationPath(); related {
			if err := RelationCondition(plan, condition, path, backendName); err != nil {
				return Joins{}, err
			}
			if err := addPath(path, true); err != nil {
				return Joins{}, err
			}
		}
	}
	for _, projection := range plan.RelationProjections() {
		if _, err := RelationProjection(plan, projection, backendName); err != nil {
			return Joins{}, err
		}
		if err := addPath(projection.Path(), false); err != nil {
			return Joins{}, err
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
	// Track declared optional ancestry separately from the final join type.
	// OR can require a shared parent while each child branch remains optional;
	// demoting that parent must not erase the child's original outer semantics.
	optionalRoutes := make(map[RelationKey]bool, len(keys))
	for index, key := range keys {
		item := edges[key]
		hop := item.hop
		fromAlias := "t0"
		parentOuter := false
		optional := hop.Nullable()
		if item.hasParent {
			parent, found := joins[item.parent]
			if !found {
				return Joins{}, invalidPlan("relation parent route was not materialized")
			}
			fromAlias = parent.Alias
			parentOuter = parent.LeftOuter
			// Filter branches retain their declared optional ancestry under OR.
			// A selection-only edge is introduced after filtering: a required
			// child of a proven-present parent can use an INNER JOIN. The
			// final parentOuter check still propagates an absent ancestor.
			if item.filter {
				optional = optional || optionalRoutes[item.parent]
			}
		}
		optionalRoutes[key] = optional
		joined := Join{Table: hop.TargetTable(), FromAlias: fromAlias, FromColumn: hop.SourceColumn(), Column: hop.TargetPrimaryKeyColumn(), Alias: fmt.Sprintf("t%d", index+1), LeftOuter: hop.Direction() == query.RelationForward && (parentOuter || optional && !required[key])}
		if hop.Direction() == query.RelationReverse {
			joined.Table = hop.SourceTable()
			joined.FromColumn = hop.TargetPrimaryKeyColumn()
			joined.Column = hop.SourceColumn()
		}
		joins[key] = joined
	}
	return Joins{Keys: keys, ByKey: joins}, nil
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
		if len(hops) == 0 || hops[0].Direction() != query.RelationForward {
			return nil
		}
		if path.TerminalScope() == query.RelationTerminalSourceKey && condition.Lookup() == query.LookupIsNull {
			isNull, valid := condition.Value().Boolean()
			if valid && isNull == negated {
				// A present FK implies its target exists under the declared FK
				// constraint. This can promote an edge used elsewhere in the tree.
				return requiredPrefixes(hops)
			}
			return nil
		}
		if path.TerminalScope() != query.RelationTerminalRelatedField {
			return nil
		}
		if condition.Lookup() == query.LookupIsNull {
			isNull, valid := condition.Value().Boolean()
			if valid && isNull == negated {
				return requiredPrefixes(hops)
			}
			return nil
		}
		if !negated {
			switch condition.Lookup() {
			case query.LookupExact, query.LookupGreaterThan, query.LookupGreaterThanOrEqual,
				query.LookupLessThan, query.LookupLessThanOrEqual, query.LookupIContains, query.LookupIn:
				return requiredPrefixes(hops)
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

func requiredPrefixes(hops []query.RelationHop) map[RelationKey]bool {
	required := make(map[RelationKey]bool, len(hops))
	for index := range hops {
		required[KeyForPath(hops[:index+1])] = true
	}
	return required
}
