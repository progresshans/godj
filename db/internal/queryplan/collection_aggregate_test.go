package queryplan_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func TestCollectionAggregateJoinsReuseOnlyTheirLatestMaterializedPrefix(t *testing.T) {
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, true)
	left := querytest.CollectionAggregateRoute(t, "left_links", amount)
	right := querytest.CollectionAggregateRoute(t, "right_links", amount)
	plan := query.NewPlan("owners", left.PrimaryKeys()[:1])
	for _, path := range []query.RelationPath{left, right, right} {
		predicate, err := query.NewExpression(query.NewRelatedCondition(path, query.LookupGreaterThan, query.Integer(1)))
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithWhere(predicate)
		if err != nil {
			t.Fatal(err)
		}
	}
	var values []query.ResultExpression
	for _, path := range []query.RelationPath{left, right} {
		value, err := query.RelatedAggregateResult(query.ResultSum, path)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	shape, err := query.NewAggregateResult(values...)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := queryplan.PrepareJoins(plan, "test")
	if err != nil || len(prepared.Keys) != 6 || len(prepared.Exists) != 0 {
		t.Fatal("aggregate introduced another source multiplier", err, prepared)
	}
	for _, test := range []struct {
		path  query.RelationPath
		scope uint32
	}{{left, 0}, {right, 2}} {
		bound, err := test.path.WithCollectionFilterScope(test.scope)
		if err != nil {
			t.Fatal(err)
		}
		selected := prepared.ResultByKey[queryplan.KeyForPath(test.path.Hops())]
		wanted := prepared.ByKey[queryplan.KeyForPath(bound.Hops())]
		if selected.Alias == "" || selected != wanted {
			t.Fatal("aggregate selected another branch or earlier filter", test.scope)
		}
	}
	before := plan
	delete(prepared.ResultByKey, queryplan.KeyForPath(left.Hops()))
	first, err := queryplan.PrepareJoins(plan, "test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := queryplan.PrepareJoins(plan, "test")
	if err != nil || !reflect.DeepEqual(first, second) || !plan.Equal(before) || !plan.ResultShape().Equal(shape) {
		t.Fatal("compiler state or result identity escaped across compilations", err)
	}
}

func TestCollectionAggregateProvenanceCannotHideBehindAnEmptyResult(t *testing.T) {
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, true)
	path := querytest.CollectionAggregateRoute(t, "links", field)
	predicate, err := query.NewExpression(query.NewRelatedCondition(path, query.LookupExact, query.Integer(1)))
	if err != nil {
		t.Fatal(err)
	}
	source, err := query.NewPlan("owners", path.PrimaryKeys()[:1]).WithWhere(predicate)
	if err != nil {
		t.Fatal(err)
	}
	keys := path.PrimaryKeys()
	keys[0] = query.NewFieldRef("wrong", "wrong", query.FieldInteger, false)
	// A reverse route's model key must agree with its physical FK target.
	if _, err := query.NewRelationChain(path.Hops(), keys, field, query.RelationTerminalRelatedField); err == nil {
		t.Fatal("aggregate route fabricated its root identity")
	}
	keys = path.PrimaryKeys()
	keys[1] = query.NewFieldRef("alternate", "alternate", query.FieldInteger, false)
	conflict, err := query.NewRelationChain(path.Hops(), keys, field, query.RelationTerminalRelatedField)
	if err != nil {
		t.Fatal(err)
	}
	value, err := query.RelatedAggregateResult(query.ResultCount, conflict)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(path.PrimaryKeys()[0])}, []query.ResultExpression{value})
	if err != nil {
		t.Fatal(err)
	}
	source, err = source.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := source.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []query.Plan{source, empty} {
		if _, err := queryplan.PrepareJoins(plan, "test"); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("aggregate hid conflicting intermediary identity", err)
		}
		if sql, args, err := sqlite.Compile(plan); sql != "" || len(args) != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("invalid aggregate published SQL", sql, args, err)
		}
	}
}
