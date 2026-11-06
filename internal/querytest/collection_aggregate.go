package querytest

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func CollectionAggregateRoute(t testing.TB, link string, terminal query.FieldRef) query.RelationPath {
	t.Helper()
	owner := ir.ModelIdentity{AppLabel: "catalog", ModelName: "owner"}
	through := ir.ModelIdentity{AppLabel: "records", ModelName: link}
	target := ir.ModelIdentity{AppLabel: "records", ModelName: "target"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	reverse, err := query.NewReverseRelationPath(through, link, "owner", "owner_id", owner, "owners", "id", link, false, id, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	forward, err := query.NewForwardRelationPath(through, link, "target", "target_id", target, "targets", "id", true, terminal, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	path, err := query.NewRelationChain(append(reverse.Hops(), forward.Hops()...), []query.FieldRef{id, id, id}, terminal, query.RelationTerminalRelatedField)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func CollectionAggregateCompiler(t *testing.T, compile func(query.Plan) (string, []any, error), postgres bool) {
	t.Helper()
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, true)
	path := CollectionAggregateRoute(t, "links", amount)
	id := path.PrimaryKeys()[0]
	predicate := func(lookup query.Lookup, n int64) query.Expression {
		value, err := query.NewExpression(query.NewRelatedCondition(path, lookup, query.Integer(n)))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	not, err := query.NotExpression(predicate(query.LookupExact, 2))
	if err != nil {
		t.Fatal(err)
	}
	count, err := query.CountResult(query.FieldResult(id))
	if err != nil {
		t.Fatal(err)
	}
	count, err = count.WithFilter(not)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := query.RelatedAggregateResult(query.ResultSum, path)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(id)}, []query.ResultExpression{sum, count})
	if err != nil {
		t.Fatal(err)
	}
	base := query.NewPlan("owners", []query.FieldRef{id})
	filtered, err := base.WithWhere(predicate(query.LookupGreaterThan, 1))
	if err != nil {
		t.Fatal(err)
	}
	filtered, err = filtered.WithWhere(predicate(query.LookupLessThan, 10))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := filtered.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	before := plan
	statement, arguments, err := compile(plan)
	if err != nil {
		t.Fatal(err)
	}
	latest := regexp.MustCompile(`"([^"]+)"\."amount" < `).FindAllStringSubmatch(statement, -1)
	first := regexp.MustCompile(`"([^"]+)"\."amount" > `).FindAllStringSubmatch(statement, -1)
	if len(latest) != 1 || len(first) != 1 || latest[0][1] == first[0][1] {
		t.Fatal("successive filters lost their independent collection rows", statement)
	}
	column := `"` + latest[0][1] + `"."amount"`
	wantSum := "SUM(godj_int64(" + column + "))"
	if postgres {
		wantSum = "SUM(" + column + ")"
	}
	if !strings.Contains(statement, wantSum) || strings.Count(statement, "EXISTS(") != 0 ||
		!strings.Contains(statement, column+" = ") || !reflect.DeepEqual(arguments, []any{int64(2), int64(1), int64(10)}) {
		t.Fatal("aggregate reused another row or reordered its parameters", statement, arguments)
	}
	if !plan.Equal(before) || !plan.ResultShape().Equal(shape) {
		t.Fatal("compiler rebound the immutable result identity")
	}
	negative, err := base.WithWhere(not)
	if err != nil {
		t.Fatal(err)
	}
	negative, err = negative.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	statement, arguments, err = compile(negative)
	if err != nil {
		t.Fatal(err)
	}
	filterStart := strings.Index(statement, " FILTER (WHERE ")
	if filterStart < 0 {
		t.Fatal("conditional aggregate missing", statement)
	}
	filterEnd := strings.Index(statement[filterStart:], ` AS "c2"`)
	if filterEnd < 0 || strings.Contains(statement[filterStart:filterStart+filterEnd], "EXISTS(") ||
		strings.Count(statement, "EXISTS(") != 1 || !reflect.DeepEqual(arguments, []any{int64(2), int64(2)}) {
		t.Fatal("aggregate condition inherited root collection exclusion", statement, arguments)
	}
}

func CollectionComputedCompiler(t *testing.T, compile func(query.Plan) (string, []any, error)) {
	t.Helper()
	amount := CollectionAggregateRoute(t, "links", query.NewFieldRef("amount", "amount", query.FieldInteger, true))
	weight := CollectionAggregateRoute(t, "links", query.NewFieldRef("weight", "weight", query.FieldInteger, false))
	left, err := query.RelatedExpression(amount)
	if err != nil {
		t.Fatal(err)
	}
	right, err := query.RelatedExpression(weight)
	if err != nil {
		t.Fatal(err)
	}
	computed, err := query.Arithmetic(query.ArithmeticAdd, left, right)
	if err != nil {
		t.Fatal(err)
	}
	condition, err := query.NewScalarComparison(left, query.LookupGreaterThan, right)
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := query.NewExpression(condition)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := query.WhenScalar(predicate, computed)
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := query.LiteralExpression(query.Integer(0))
	conditional, err := query.CaseScalar(zero, branch)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := query.ScalarAggregateResult(query.ResultSum, conditional)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err = aggregate.WithDistinct()
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err = aggregate.WithFilter(predicate)
	if err != nil {
		t.Fatal(err)
	}
	id := amount.PrimaryKeys()[0]
	shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(id)}, []query.ResultExpression{aggregate})
	if err != nil {
		t.Fatal(err)
	}
	base := query.NewPlan("owners", []query.FieldRef{id})
	filtered := base
	for _, item := range []struct {
		lookup query.Lookup
		value  int64
	}{{query.LookupGreaterThan, 1}, {query.LookupLessThan, 10}} {
		where, err := query.NewExpression(query.NewRelatedCondition(amount, item.lookup, query.Integer(item.value)))
		if err != nil {
			t.Fatal(err)
		}
		filtered, err = filtered.WithWhere(where)
		if err != nil {
			t.Fatal(err)
		}
	}
	plan, err := filtered.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	before := plan
	statement, arguments, err := compile(plan)
	if err != nil {
		t.Fatal(err)
	}
	whereStart := strings.LastIndex(statement, " WHERE ")
	if whereStart < 0 {
		t.Fatal("computed aggregate lost its source predicate", statement)
	}
	latest := regexp.MustCompile(`"([^"]+)"\."amount" < `).FindAllStringSubmatch(statement[whereStart:], -1)
	first := regexp.MustCompile(`"([^"]+)"\."amount" > `).FindAllStringSubmatch(statement[whereStart:], -1)
	if len(latest) != 1 || len(first) != 1 || latest[0][1] == first[0][1] {
		t.Fatal("computed aggregate collapsed independent source filters", statement)
	}
	start := strings.Index(statement, "SUM(DISTINCT ")
	end := strings.Index(statement, ` AS "c1"`)
	if start < 0 || end <= start {
		t.Fatal("computed aggregate lost DISTINCT or its selected identity", statement)
	}
	selected := statement[start:end]
	if !strings.Contains(selected, "CASE WHEN ") || !strings.Contains(selected, " FILTER (WHERE ") ||
		!strings.Contains(selected, `"`+latest[0][1]+`"."amount"`) || !strings.Contains(selected, `"`+latest[0][1]+`"."weight"`) ||
		strings.Contains(selected, `"`+first[0][1]+`".`) || strings.Contains(selected, "EXISTS(") ||
		!reflect.DeepEqual(arguments, []any{int64(0), int64(1), int64(10)}) {
		t.Fatal("value, CASE and FILTER selected different collection rows or changed parameter order", statement, arguments)
	}
	if !plan.Equal(before) || !plan.ResultShape().Equal(shape) {
		t.Fatal("computed aggregate rebound source or result identity")
	}
	outer, err := base.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	statement, _, err = compile(outer)
	if err != nil || strings.Count(statement, "LEFT OUTER JOIN ") != 2 || strings.Contains(statement, "INNER JOIN ") {
		t.Fatal("aggregate CASE or FILTER removed empty collections", err, statement)
	}
}
