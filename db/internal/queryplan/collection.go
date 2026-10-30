package queryplan

import (
	"github.com/progresshans/godj/query"
	"strings"
)

// CollectionExists is a correlated scalar predicate, never an outer row
// multiplier. Its identifiers come from a separately validated positive plan.
// Dialects own quoting, scalar expressions, placeholders and SQL rendering.
type CollectionExists struct {
	Table, RootAlias, TerminalAlias   string
	InnerAlias, OuterAlias, KeyColumn string
	Joins                             []Join
}

type conditionLeaf struct {
	condition query.Condition
	negated   bool
}

func conditionLeaves(plan query.Plan) []conditionLeaf {
	where, present := plan.Where()
	if !present {
		return nil
	}
	var leaves []conditionLeaf
	var walk func(query.Expression, bool)
	walk = func(expression query.Expression, negated bool) {
		if condition, leaf := expression.Condition(); leaf {
			leaves = append(leaves, conditionLeaf{condition, negated})
			return
		}
		if expression.Kind() == query.ExpressionNot {
			negated = !negated
		}
		for _, child := range expression.Children() {
			walk(child, negated)
		}
	}
	walk(where, false)
	return leaves
}

func prepareCollectionExists(plan query.Plan, condition query.Condition, depth int, outer Joins, backendName string) (CollectionExists, error) {
	path, _ := condition.RelationPath()
	keys := path.PrimaryKeys()
	if depth < 0 || depth >= len(keys) {
		return CollectionExists{}, invalidPlan("collection correlation has no model row identity")
	}
	probe, err := query.NewPlan(plan.Table(), plan.SourceFields()).WithConditions(condition)
	if err != nil {
		return CollectionExists{}, err
	}
	inner, err := PrepareJoins(probe, backendName)
	if err != nil {
		return CollectionExists{}, err
	}
	probePath, _ := probe.Conditions()[0].RelationPath()
	exists := CollectionExists{Table: plan.Table(), RootAlias: "et0", InnerAlias: "et0", OuterAlias: "t0", TerminalAlias: "et0", KeyColumn: keys[depth].Column()}
	for _, key := range inner.Keys {
		join := inner.ByKey[key]
		join.Alias = "e" + join.Alias
		join.FromAlias = "e" + join.FromAlias
		exists.Joins = append(exists.Joins, join)
	}
	if key, joined := ConditionJoinKey(probePath); joined {
		join, found := inner.ByKey[key]
		if !found {
			return CollectionExists{}, invalidPlan("collection existence terminal was not materialized")
		}
		exists.TerminalAlias = "e" + join.Alias
	}
	if depth > 0 {
		innerJoin, innerFound := inner.ByKey[KeyForPath(probePath.Hops()[:depth])]
		outerJoin, outerFound := outer.ByKey[KeyForPath(path.Hops()[:depth])]
		if !innerFound || !outerFound {
			return CollectionExists{}, invalidPlan("collection correlation row was not materialized")
		}
		exists.InnerAlias, exists.OuterAlias = "e"+innerJoin.Alias, outerJoin.Alias
	}
	return exists, nil
}

// CollectionExistsPrefix renders only portable correlation/join syntax.
// The dialect validates and quotes every physical table, alias and column.
// The caller appends its scalar predicate followed by ") LIMIT 1)".
func CollectionExistsPrefix(exists CollectionExists, table, identifier func(string) (string, error), qualified func(string, string) (string, error)) (string, error) {
	rootTable, err := table(exists.Table)
	if err != nil {
		return "", err
	}
	rootAlias, err := identifier(exists.RootAlias)
	if err != nil {
		return "", err
	}
	var sql strings.Builder
	sql.WriteString("EXISTS(SELECT 1 FROM ")
	sql.WriteString(rootTable)
	sql.WriteString(" AS ")
	sql.WriteString(rootAlias)
	for _, join := range exists.Joins {
		joinedTable, err := table(join.Table)
		if err != nil {
			return "", err
		}
		alias, err := identifier(join.Alias)
		if err != nil {
			return "", err
		}
		from, err := qualified(join.FromAlias, join.FromColumn)
		if err != nil {
			return "", err
		}
		to, err := qualified(join.Alias, join.Column)
		if err != nil {
			return "", err
		}
		if join.LeftOuter {
			sql.WriteString(" LEFT OUTER JOIN ")
		} else {
			sql.WriteString(" INNER JOIN ")
		}
		sql.WriteString(joinedTable)
		sql.WriteString(" AS ")
		sql.WriteString(alias)
		sql.WriteString(" ON ")
		sql.WriteString(from)
		sql.WriteString(" = ")
		sql.WriteString(to)
	}
	inner, err := qualified(exists.InnerAlias, exists.KeyColumn)
	if err != nil {
		return "", err
	}
	outer, err := qualified(exists.OuterAlias, exists.KeyColumn)
	if err != nil {
		return "", err
	}
	sql.WriteString(" WHERE ")
	sql.WriteString(inner)
	sql.WriteString(" = ")
	sql.WriteString(outer)
	sql.WriteString(" AND (")
	return sql.String(), nil
}
