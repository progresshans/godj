package sqlite

import (
	"fmt"
	"strings"

	"github.com/progresshans/godj/query"
)

const (
	sqliteMaximumWhereDepth = 64
	sqliteMaximumWhereNodes = 1024
)

// sqliteWhereAnalysis is the backend-owned, validated view of Plan.Where.
// The compiler never falls back to Plan.Conditions: every result shape and
// relation path renders this same recursive tree.
type sqliteWhereAnalysis struct {
	root         *sqliteWhereNode
	leaves       []*sqliteWhereNode
	hasRelations bool
}

type sqliteWhereNode struct {
	kind        query.ExpressionKind
	condition   query.Condition
	children    []*sqliteWhereNode
	fieldSQL    string
	rhsFieldSQL string
}

func analyzeWhere(plan query.Plan) (*sqliteWhereAnalysis, error) {
	expression, ok := plan.Where()
	if !ok {
		return &sqliteWhereAnalysis{}, nil
	}
	analysis := &sqliteWhereAnalysis{}
	visited := 0
	root, hasRelations, err := analyzeWhereExpression(expression, 1, true, &visited, analysis)
	if err != nil {
		return nil, err
	}
	analysis.root = root
	analysis.hasRelations = hasRelations
	return analysis, nil
}

func analyzeWhereExpression(
	expression query.Expression,
	depth int,
	relationAtRootConjunction bool,
	visited *int,
	analysis *sqliteWhereAnalysis,
) (*sqliteWhereNode, bool, error) {
	if depth > sqliteMaximumWhereDepth {
		return nil, false, invalidPlan("query expression exceeds the maximum depth of 64")
	}
	(*visited)++
	if *visited > sqliteMaximumWhereNodes {
		return nil, false, invalidPlan("query expression exceeds the maximum node count of 1024")
	}

	node := &sqliteWhereNode{kind: expression.Kind()}
	switch node.kind {
	case query.ExpressionLeaf:
		condition, ok := expression.Condition()
		if !ok || len(expression.Children()) != 0 {
			return nil, false, invalidPlan("query expression leaf is zero or malformed")
		}
		node.condition = condition
		_, related := condition.RelationPath()
		if related && !relationAtRootConjunction {
			return nil, false, unsupportedRelatedCondition(
				condition,
				"SQLite relation predicates under OR or NOT are not supported",
			)
		}
		if expression.HasRelations() != related {
			return nil, false, invalidPlan("query expression relation metadata is malformed")
		}
		analysis.leaves = append(analysis.leaves, node)
		return node, related, nil
	case query.ExpressionAnd, query.ExpressionOr:
		children := expression.Children()
		if len(children) < 2 {
			return nil, false, invalidPlan("AND and OR query expressions require at least two children")
		}
		node.children = make([]*sqliteWhereNode, len(children))
		childRelationAtRoot := relationAtRootConjunction && node.kind == query.ExpressionAnd
		hasRelations := false
		for index, child := range children {
			analyzed, childRelations, err := analyzeWhereExpression(
				child,
				depth+1,
				childRelationAtRoot,
				visited,
				analysis,
			)
			if err != nil {
				return nil, false, err
			}
			node.children[index] = analyzed
			hasRelations = hasRelations || childRelations
		}
		if expression.HasRelations() != hasRelations {
			return nil, false, invalidPlan("query expression relation metadata is malformed")
		}
		return node, hasRelations, nil
	case query.ExpressionNot:
		children := expression.Children()
		if len(children) != 1 {
			return nil, false, invalidPlan("NOT query expressions require exactly one child")
		}
		child, hasRelations, err := analyzeWhereExpression(
			children[0],
			depth+1,
			false,
			visited,
			analysis,
		)
		if err != nil {
			return nil, false, err
		}
		node.children = []*sqliteWhereNode{child}
		if expression.HasRelations() != hasRelations {
			return nil, false, invalidPlan("query expression relation metadata is malformed")
		}
		return node, hasRelations, nil
	default:
		return nil, false, invalidPlan("query expression is zero or malformed")
	}
}

func bindScalarWhere(analysis *sqliteWhereAnalysis, sourceFields []query.FieldRef) error {
	for _, leaf := range analysis.leaves {
		condition := leaf.condition
		if _, related := condition.RelationPath(); related {
			return invalidPlan("scalar query contains relation predicate metadata")
		}
		if !containsField(sourceFields, condition.Field()) {
			return invalidPlan(fmt.Sprintf("condition field %q is not selected model metadata", condition.Field().Name()))
		}
		field, err := quoteIdentifier(condition.Field().Column())
		if err != nil {
			return err
		}
		leaf.fieldSQL = field
		if right, ok := condition.RHSField(); ok {
			if !containsField(sourceFields, right) {
				return invalidPlan(fmt.Sprintf("condition right-hand-side field %q is not selected model metadata", right.Name()))
			}
			rhsField, err := quoteIdentifier(right.Column())
			if err != nil {
				return err
			}
			leaf.rhsFieldSQL = rhsField
		}
	}
	return nil
}

func appendWhere(sql *strings.Builder, analysis *sqliteWhereAnalysis) ([]any, error) {
	if analysis == nil || analysis.root == nil {
		return []any{}, nil
	}
	for _, leaf := range analysis.leaves {
		if leaf.fieldSQL == "" {
			return nil, invalidPlan("query expression field binding is missing")
		}
		if _, ok := leaf.condition.RHSField(); ok && leaf.rhsFieldSQL == "" {
			return nil, invalidPlan("query expression right-hand-side field binding is missing")
		}
	}

	sql.WriteString(" WHERE ")
	arguments := make([]any, 0, len(analysis.leaves))
	if err := appendWhereNode(sql, analysis.root, false, false, &arguments); err != nil {
		return nil, err
	}
	return arguments, nil
}

func appendWhereNode(
	sql *strings.Builder,
	node *sqliteWhereNode,
	oddNegation bool,
	alreadyGrouped bool,
	arguments *[]any,
) error {
	switch node.kind {
	case query.ExpressionLeaf:
		guards := nullableNegationGuards(node, oddNegation)
		if len(guards) > 0 && !alreadyGrouped {
			sql.WriteByte('(')
		}
		sql.WriteString(node.fieldSQL)
		conditionArguments, err := compileCondition(sql, node.condition, node.rhsFieldSQL)
		if err != nil {
			return err
		}
		*arguments = append(*arguments, conditionArguments...)
		for _, guard := range guards {
			sql.WriteString(" AND ")
			sql.WriteString(guard)
			sql.WriteString(" IS NOT NULL")
		}
		if len(guards) > 0 && !alreadyGrouped {
			sql.WriteByte(')')
		}
		return nil
	case query.ExpressionAnd, query.ExpressionOr:
		if len(node.children) < 2 {
			return invalidPlan("AND and OR query expressions require at least two children")
		}
		if !alreadyGrouped {
			sql.WriteByte('(')
		}
		operator := " AND "
		if node.kind == query.ExpressionOr {
			operator = " OR "
		}
		for index, child := range node.children {
			if index > 0 {
				sql.WriteString(operator)
			}
			if err := appendWhereNode(sql, child, oddNegation, false, arguments); err != nil {
				return err
			}
		}
		if !alreadyGrouped {
			sql.WriteByte(')')
		}
		return nil
	case query.ExpressionNot:
		if len(node.children) != 1 {
			return invalidPlan("NOT query expressions require exactly one child")
		}
		sql.WriteString("NOT (")
		if err := appendWhereNode(sql, node.children[0], !oddNegation, true, arguments); err != nil {
			return err
		}
		sql.WriteByte(')')
		return nil
	default:
		return invalidPlan("query expression is zero or malformed")
	}
}

func nullableNegationGuards(node *sqliteWhereNode, oddNegation bool) []string {
	if !oddNegation || !nullableNegationLookup(node.condition.Lookup()) {
		return nil
	}
	guards := make([]string, 0, 2)
	left := node.condition.Field()
	if left.Nullable() {
		guards = append(guards, node.fieldSQL)
	}
	if right, ok := node.condition.RHSField(); ok && right.Nullable() && !right.Equal(left) {
		guards = append(guards, node.rhsFieldSQL)
	}
	return guards
}

func nullableNegationLookup(lookup query.Lookup) bool {
	switch lookup {
	case query.LookupExact,
		query.LookupGreaterThan,
		query.LookupGreaterThanOrEqual,
		query.LookupLessThan,
		query.LookupLessThanOrEqual,
		query.LookupIContains,
		query.LookupIn:
		return true
	default:
		return false
	}
}
