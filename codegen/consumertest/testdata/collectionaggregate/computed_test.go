package consumer

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"example.com/godj-project-bundle/catalog"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func testCollectionComputed(t *testing.T, backend collectionBackend, vendor string, physical collectionPhysicalWriter, seed collectionSeed) {
	t.Helper()
	relations, err := project.BindRelations()
	check(t, err)
	target, fields := relations.CatalogLabel.Tickets, catalog.LabelFields
	typedValue := orm.Add(orm.Multiply(target.Amount, int64(2)), int64(1))
	dynamicValue := orm.DynamicF("tickets__amount").Multiply(int64(2)).Add(int64(1))
	t.Run("computed_typed_dynamic_scope_and_values", func(t *testing.T) {
		probe := &collectionReadProbe{collectionBackend: backend}
		api, err := project.Using(probe)
		check(t, err)
		typed := namedCollectionAggregate(t, api.CatalogLabel, orm.Aggregate3(orm.Count(typedValue), orm.Sum(typedValue), orm.Avg(typedValue),
			func(total int64, sum orm.Optional[int64], average orm.Optional[float64]) map[string]any {
				return map[string]any{"total": total, "amount": optionalValue(sum), "average": optionalValue(average)}
			}))
		dynamic := collectionGroup(t, api.CatalogLabel,
			orm.DynamicAggregateInput{Kind: query.ResultCount, Expression: dynamicValue},
			orm.DynamicAggregateInput{Kind: query.ResultSum, Expression: dynamicValue},
			orm.DynamicAggregateInput{Kind: query.ResultAvg, Expression: dynamicValue})
		if !typed.Plan().Equal(dynamic.Plan()) {
			t.Fatal("computed typed/dynamic AST differs")
		}
		rows, err := typed.All(t.Context())
		check(t, err)
		want := `[{"code":"blue","total":2,"amount":0,"average":0},{"code":"foreign","total":0,"amount":null,"average":null},{"code":"none","total":0,"amount":null,"average":null},{"code":"nullonly","total":0,"amount":null,"average":null},{"code":"orphan","total":1,"amount":19,"average":19},{"code":"red","total":3,"amount":21,"average":7}]`
		assertCollectionJSON(t, rows, want)
		assertCollectionJSON(t, collectionRows(t, dynamic, "code", "total", "amount", "average"), want)
		bound, err := api.CatalogLabel.AggregateExpression(query.ResultSum, dynamicValue)
		check(t, err)
		sum, err := orm.AggregateInto(t.Context(), catalog.LabelObjects.Using(probe), orm.Aggregate1(bound, func(v query.Value) query.Value { return v }))
		check(t, err)
		if n, ok := sum.Integer(); !ok || n != 40 {
			t.Fatal("generated aggregate expression changed global multiplicity", sum)
		}
		calls := probe.calls()
		if _, err := api.CatalogLabel.Expression(dynamicValue); err == nil {
			t.Fatal("collection computation became a row scalar")
		}
		if _, err := api.CatalogLabel.ExpressionPredicate(dynamicValue.GreaterThan(int64(0))); err == nil {
			t.Fatal("collection computation became a row predicate")
		}
		if _, err := orm.SelectQuery(catalog.LabelObjects.Using(probe), orm.Project1(typedValue, func(v orm.Optional[int64]) orm.Optional[int64] { return v })); err == nil {
			t.Fatal("typed collection computation became a row projection")
		}
		if _, err := orm.BindAggregateExpression(catalog.LabelObjects.Using(probe), query.ResultSum, dynamicValue); err == nil {
			t.Fatal("dynamic aggregate invented project authority")
		}
		if _, err := orm.BindAggregateExpressionIn(catalog.LabelObjects.Using(probe), orm.BoundModel[catalog.Label]{}, query.ResultSum, dynamicValue); err == nil {
			t.Fatal("dynamic aggregate accepted an unbound model")
		}
		for _, kind := range []query.ResultExpressionKind{query.ResultCountAll, query.ResultField, "unknown"} {
			if _, err := api.CatalogLabel.AggregateExpression(kind, dynamicValue); err == nil {
				t.Fatal("dynamic aggregate accepted an invalid kind", kind)
			}
		}
		if rows, err := catalog.LabelObjects.Using(probe).Filter(typedValue.GreaterThan(int64(0))).All(t.Context()); err == nil || rows != nil {
			t.Fatal("typed computed collection escaped into WHERE")
		}
		if changed, err := catalog.LabelObjects.Using(probe).Update(t.Context(), orm.AssignExpression(fields.ID, typedValue)); err == nil || changed != 0 {
			t.Fatal("collection computation escaped into UPDATE")
		}
		if probe.calls() != calls {
			t.Fatal("invalid computed source performed read I/O")
		}
	})
	t.Run("computed_case_filter_and_nulls", func(t *testing.T) {
		api, err := project.Using(backend)
		check(t, err)
		value := orm.Numeric(orm.Case(orm.Value[catalog.Label](int64(1)), orm.When(target.Enabled.Exact(true), orm.Add(target.Amount, int64(3)))))
		grouped := namedCollectionAggregate(t, api.CatalogLabel, orm.Aggregate2(orm.Sum(value), orm.Count(value), func(sum orm.Optional[int64], count int64) map[string]any {
			return map[string]any{"amount": optionalValue(sum), "total": count}
		}))
		rows, err := grouped.All(t.Context())
		check(t, err)
		assertCollectionJSON(t, rows, `[{"code":"blue","amount":5,"total":2},{"code":"foreign","amount":1,"total":1},{"code":"none","amount":1,"total":1},{"code":"nullonly","amount":null,"total":0},{"code":"orphan","amount":1,"total":1},{"code":"red","amount":13,"total":5}]`)
		filtered := orm.Sum(typedValue).Where(orm.LessThanExpressions(target.Amount, target.Detail().Bonus))
		typed := namedCollectionAggregate(t, api.CatalogLabel, orm.Aggregate1(filtered, func(v orm.Optional[int64]) map[string]any { return map[string]any{"amount": optionalValue(v)} }))
		dynamic := collectionGroup(t, api.CatalogLabel, orm.DynamicAggregateInput{Kind: query.ResultSum, Expression: dynamicValue, Predicate: orm.DynamicF("tickets__amount").LessThan(orm.DynamicF("tickets__detail__bonus"))})
		if !typed.Plan().Equal(dynamic.Plan()) {
			t.Fatal("computed FILTER lost typed/dynamic identity")
		}
		rows, err = typed.All(t.Context())
		check(t, err)
		want := `[{"code":"blue","amount":5},{"code":"foreign","amount":null},{"code":"none","amount":null},{"code":"nullonly","amount":null},{"code":"orphan","amount":null},{"code":"red","amount":10}]`
		assertCollectionJSON(t, rows, want)
		assertCollectionJSON(t, collectionRows(t, dynamic, "code", "amount"), want)
		for _, enabled := range []bool{true, false} {
			// Conditional COUNT names its contributing value. COUNT(*) FILTER
			// remains rejected by the common aggregate contract.
			count := orm.Count(target.ID).Where(target.Enabled.Exact(enabled)).Where(orm.LessThanExpressions(target.Amount, target.Detail().Bonus))
			typed := namedCollectionAggregate(t, api.CatalogLabel, orm.Aggregate1(count, func(v int64) map[string]any { return map[string]any{"total": v} }))
			dynamic := collectionGroup(t, api.CatalogLabel, orm.DynamicAggregateInput{Kind: query.ResultCount, Field: "tickets__id",
				Filter: []orm.LookupInput{{Key: "tickets__enabled", Value: enabled}}, Predicate: orm.DynamicF("tickets__amount").LessThan(orm.DynamicF("tickets__detail__bonus"))})
			if !typed.Plan().Equal(dynamic.Plan()) {
				t.Fatal("conditional COUNT lost the conjunction of field and computed FILTER")
			}
			want := `[{"code":"blue","total":0},{"code":"foreign","total":0},{"code":"none","total":0},{"code":"nullonly","total":0},{"code":"orphan","total":0},{"code":"red","total":0}]`
			if enabled {
				want = `[{"code":"blue","total":1},{"code":"foreign","total":0},{"code":"none","total":0},{"code":"nullonly","total":0},{"code":"orphan","total":0},{"code":"red","total":2}]`
			}
			rows, err := typed.All(t.Context())
			check(t, err)
			assertCollectionJSON(t, rows, want)
			assertCollectionJSON(t, collectionRows(t, dynamic, "code", "total"), want)
		}
	})
	t.Run("computed_read_lifetime_and_failures", func(t *testing.T) {
		probe := &collectionReadProbe{collectionBackend: backend}
		api, err := project.Using(probe)
		check(t, err)
		input := orm.DynamicAggregateInput{Kind: query.ResultSum, Expression: dynamicValue}
		grouped := collectionGroup(t, api.CatalogLabel, input)
		rows, err := grouped.All(t.Context())
		check(t, err)
		rows[0].Aggregates[0] = query.Integer(999)
		copy := grouped
		again, err := copy.All(t.Context())
		check(t, err)
		if probe.calls() != 1 || again[0].Aggregates[0] == query.Integer(999) {
			t.Fatal("computed cache either reran or published caller mutation")
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if result, err := grouped.All(cancelled); !errors.Is(err, context.Canceled) || result != nil || probe.calls() != 1 {
			t.Fatal("warm computed cache ignored cancellation", err)
		}
		for _, fault := range []string{"query", "scan", "iteration", "close"} {
			candidate := grouped.Fresh()
			probe.fault = fault
			if result, err := candidate.All(t.Context()); err == nil || result != nil {
				t.Fatal("computed failure published rows", fault)
			}
			probe.fault = ""
			calls := probe.calls()
			result, err := candidate.All(t.Context())
			check(t, err)
			if !reflect.DeepEqual(result, again) || probe.calls() != calls+1 {
				t.Fatal("computed failure polluted a retry", fault)
			}
		}
		for _, commit := range []bool{false, true} {
			var warm orm.GroupedQuery[catalog.Label, orm.GroupRow]
			var sessionProbe *collectionSessionProbe
			rollback := errors.New("computed session rollback")
			err := backend.Atomic(t.Context(), func(session db.Session) error {
				validator, ok := session.(db.SessionValidator)
				if !ok {
					return errors.New("computed session lacks lifetime authority")
				}
				sessionProbe = &collectionSessionProbe{Session: session, validator: validator}
				api, err := project.UsingSession(sessionProbe)
				if err != nil {
					return err
				}
				warm = collectionGroup(t, api.CatalogLabel, input)
				if _, err := warm.All(t.Context()); err != nil {
					return err
				}
				if !commit {
					return rollback
				}
				return nil
			})
			if commit {
				check(t, err)
			} else if !errors.Is(err, rollback) {
				t.Fatal("computed session rollback lost its cause", err)
			}
			calls := sessionProbe.calls
			for _, candidate := range []orm.GroupedQuery[catalog.Label, orm.GroupRow]{warm, warm.Fresh()} {
				if rows, err := candidate.All(t.Context()); err == nil || rows != nil || sessionProbe.calls != calls {
					t.Fatal("computed result escaped session lifetime", commit, err)
				}
			}
		}
	})
	t.Run("computed_physical_values_and_rollback", func(t *testing.T) {
		zero, err := decimal.Parse("0")
		check(t, err)
		value := orm.Decimal(target.Price).Multiply(zero)
		invalid := "'not-a-decimal'"
		if vendor == "postgres" {
			invalid = "'NaN'::numeric"
		}
		check(t, physical("ticket", "price", invalid, seed.tickets["c"].ID))
		func() {
			defer func() {
				_, err := records.TicketObjects.Using(backend).Filter(records.TicketFields.ID.Exact(seed.tickets["c"].ID)).Update(t.Context(), orm.AssignNull(records.TicketFields.Price))
				check(t, err)
			}()
			source := catalog.LabelObjects.Using(backend).Filter(fields.Code.Exact("red"))
			for _, aggregate := range []orm.AggregateExpression[catalog.Label, orm.Optional[decimal.Decimal]]{orm.Sum(value), orm.Avg(value), orm.Min(value), orm.Max(value)} {
				built := 0
				_, err := orm.AggregateInto(t.Context(), source, orm.Aggregate1(aggregate, func(v orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { built++; return v }))
				if err == nil || built != 0 {
					t.Fatal("multiplication by zero hid corrupt collection data", err)
				}
				scoped, err := orm.AggregateInto(t.Context(), source, orm.Aggregate1(aggregate.Where(target.Category().Code.Exact("a")), func(v orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return v }))
				check(t, err)
				if n, ok := scoped.Get(); !ok || !n.Equal(zero) {
					t.Fatal("FILTER evaluated excluded corrupt collection data", scoped)
				}
			}
			conditional := orm.Decimal(orm.Case(orm.Value[catalog.Label](zero), orm.When(target.Category().Code.Exact("a"), value)))
			result, err := orm.AggregateInto(t.Context(), source, orm.Aggregate1(orm.Sum(conditional), func(v orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return v }))
			check(t, err)
			if n, ok := result.Get(); !ok || !n.Equal(zero) {
				t.Fatal("CASE evaluated an unselected corrupt collection value")
			}
		}()
		rollback := errors.New("computed integer rollback")
		err = backend.Atomic(t.Context(), func(session db.Session) error {
			_, err := records.TicketObjects.Using(session).Filter(records.TicketFields.Code.Exact("a")).Update(t.Context(), orm.Assign(records.TicketFields.Amount, int64(math.MaxInt64)))
			if err != nil {
				return err
			}
			built := 0
			_, err = orm.AggregateInto(t.Context(), catalog.LabelObjects.Using(session), orm.Aggregate1(orm.Sum(orm.Add(target.Amount, int64(1))), func(v orm.Optional[int64]) orm.Optional[int64] { built++; return v }))
			if err == nil || built != 0 {
				return errors.New("collection integer computation overflow published a value")
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal("computed numeric rollback failed", err)
		}
		price := orm.Decimal(target.Price)
		denominator := price.SubtractExpression(price)
		for _, value := range []orm.DecimalExpression[catalog.Label]{price.DivideExpression(denominator), price.RemainderExpression(denominator)} {
			built := 0
			result, err := orm.AggregateInto(t.Context(), catalog.LabelObjects.Using(backend).Filter(fields.Code.Exact("red")), orm.Aggregate1(orm.Sum(value).Where(target.Code.Exact("a")), func(v orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { built++; return v }))
			if vendor == "postgres" {
				if err == nil || built != 0 {
					t.Fatal("PostgreSQL collection zero divisor published a value")
				}
			} else if err != nil || result.Valid() || built != 1 {
				t.Fatal("SQLite collection zero divisor lost NULL", err, result)
			}
		}
	})
	t.Run("computed_resource_limits", func(t *testing.T) {
		probe := &collectionReadProbe{collectionBackend: backend}
		api, err := project.Using(probe)
		check(t, err)
		wide := dynamicValue
		for index := 0; index <= query.MaximumScalarDepth; index++ {
			wide = wide.Add(int64(1))
		}
		if _, err := api.CatalogLabel.AggregateExpression(query.ResultSum, wide); err == nil {
			t.Fatal("computed collection bypassed expression depth")
		}
		inputs := []orm.DynamicAggregateInput{
			{Kind: query.ResultSum, Expression: dynamicValue, Predicate: wide.Exact(int64(0))},
			{Kind: query.ResultCountAll, Expression: dynamicValue},
			{Kind: query.ResultSum, Field: "tickets__amount", Expression: dynamicValue},
		}
		for _, input := range inputs {
			if _, err := api.CatalogLabel.GroupValues([]string{"code"}, []orm.DynamicAggregateInput{input}); err == nil {
				t.Fatal("invalid computed aggregate was admitted")
			}
		}
		if probe.calls() != 0 {
			t.Fatal("invalid computed aggregate performed I/O")
		}
	})
}

func observeComputedCollection(t *testing.T, reader collectionBackend, name string) any {
	t.Helper()
	api, err := project.Using(reader)
	check(t, err)
	relations, err := project.BindRelations()
	check(t, err)
	base, target := api.CatalogLabel, relations.CatalogLabel.Tickets
	amount := orm.DynamicF("tickets__amount")
	adjusted := amount.Multiply(int64(2)).Add(int64(1))
	aggregate := func(kind query.ResultExpressionKind, value orm.DynamicExpression) orm.DynamicAggregateInput {
		return orm.DynamicAggregateInput{Kind: kind, Expression: value}
	}
	groups := func(source project.CatalogLabelQuery) any {
		return collectionRows(t, collectionGroup(t, source, aggregate(query.ResultCount, adjusted), aggregate(query.ResultSum, adjusted)), "code", "total", "amount")
	}
	switch name {
	case "computed_integer", "computed_float", "computed_decimal", "computed_same_row":
		value := adjusted
		kinds := []query.ResultExpressionKind{query.ResultSum, query.ResultAvg, query.ResultMin, query.ResultMax}
		names := []string{"code", "amount", "average", "minimum", "maximum"}
		switch name {
		case "computed_same_row":
			value = amount.Multiply(amount)
			kinds, names = kinds[:2], names[:3]
		case "computed_float":
			value = orm.DynamicF("tickets__score").Multiply(float64(1.5)).Subtract(float64(0.25))
			kinds, names = kinds[:2], names[:3]
		case "computed_decimal":
			factor, err := decimal.Parse("1.25")
			check(t, err)
			delta, err := decimal.Parse("0.25")
			check(t, err)
			value = orm.DynamicF("tickets__price").Multiply(factor).Add(delta)
		}
		var inputs []orm.DynamicAggregateInput
		for _, kind := range kinds {
			inputs = append(inputs, aggregate(kind, value))
		}
		return collectionRows(t, collectionGroup(t, base, inputs...), names...)
	case "computed_distinct":
		value := amount.Remainder(int64(3))
		var inputs []orm.DynamicAggregateInput
		for _, kind := range []query.ResultExpressionKind{query.ResultSum, query.ResultAvg, query.ResultCount} {
			input := aggregate(kind, value)
			input.Distinct = true
			inputs = append(inputs, input)
		}
		return collectionRows(t, collectionGroup(t, base, inputs...), "code", "amount", "average", "total")
	case "computed_case", "computed_case_comparison":
		value := orm.DynamicCase(int64(1), orm.DynamicWhen(orm.DynamicF("tickets__enabled").Exact(true), amount.Add(int64(3))))
		kinds := []query.ResultExpressionKind{query.ResultSum, query.ResultCount, query.ResultMin, query.ResultMax}
		names := []string{"code", "amount", "total", "minimum", "maximum"}
		if name == "computed_case_comparison" {
			bonus := orm.DynamicF("tickets__detail__bonus")
			value = orm.DynamicCase(int64(-1), orm.DynamicWhen(amount.LessThan(bonus), amount.Add(bonus)))
			kinds, names = kinds[:2], names[:3]
		}
		var inputs []orm.DynamicAggregateInput
		for _, kind := range kinds {
			inputs = append(inputs, aggregate(kind, value))
		}
		return collectionRows(t, collectionGroup(t, base, inputs...), names...)
	case "computed_filter", "computed_filter_comparison":
		input := aggregate(query.ResultSum, adjusted)
		if name == "computed_filter" {
			input.Filter = []orm.LookupInput{{Key: "tickets__enabled", Value: true}, {Key: "tickets__category__code", Value: "a"}}
		} else {
			input.Predicate = amount.LessThan(orm.DynamicF("tickets__detail__bonus"))
		}
		return collectionRows(t, collectionGroup(t, base, input), "code", "amount")
	case "computed_chained_filters":
		return groups(base.Filter(target.Enabled.Exact(true)).Filter(target.Amount.GreaterThan(0)))
	case "computed_chained_reversed":
		return groups(base.Filter(target.Amount.GreaterThan(0)).Filter(target.Enabled.Exact(true)))
	case "computed_root_exclusion":
		return groups(base.Filter(orm.Not(target.Enabled.Exact(true))))
	case "computed_independent_collections", "computed_nested_collections":
		value := orm.DynamicF("labels__id").Add(orm.DynamicF("tickets__amount"))
		if name == "computed_nested_collections" {
			value = orm.DynamicF("tickets__labels__id").Add(amount)
		}
		inputs := []orm.DynamicAggregateInput{aggregate(query.ResultSum, value), aggregate(query.ResultSum, value)}
		inputs[1].Distinct = true
		if name == "computed_nested_collections" {
			return collectionRows(t, collectionGroup(t, base, inputs...), "code", "amount", "unique")
		}
		grouped, err := api.CatalogCategory.GroupValues([]string{"code"}, inputs)
		check(t, err)
		return collectionRows(t, grouped.OrderBy(catalog.CategoryFields.Code.Asc()), "code", "amount", "unique")
	case "computed_optional_ancestor":
		value := orm.DynamicF("category__tickets__amount").Multiply(int64(2)).Add(int64(1))
		return collectionRows(t, collectionGroup(t, base, aggregate(query.ResultCount, value), aggregate(query.ResultSum, value)), "code", "total", "amount")
	case "computed_global", "computed_empty_global":
		value := orm.Add(orm.Multiply(target.Amount, int64(2)), int64(1))
		source := catalog.LabelObjects.Using(reader)
		if name == "computed_empty_global" {
			source = source.Filter(catalog.LabelFields.ID.In())
		}
		row, err := orm.AggregateInto(t.Context(), source, orm.Aggregate3(orm.Count(value), orm.Sum(value), orm.Avg(value),
			func(total int64, sum orm.Optional[int64], average orm.Optional[float64]) map[string]any {
				return map[string]any{"total": total, "amount": optionalValue(sum), "average": optionalValue(average)}
			}))
		check(t, err)
		return row
	case "computed_having_page", "computed_derived":
		value := orm.Sum(orm.Add(orm.Multiply(target.Amount, int64(2)), int64(1)))
		grouped := namedCollectionAggregate(t, base, orm.Aggregate1(value, func(sum orm.Optional[int64]) map[string]any { return map[string]any{"amount": optionalValue(sum)} }))
		if name == "computed_having_page" {
			page, err := grouped.Having(value.GreaterThan(orm.Some(int64(0)))).Page(t.Context(), 3, 1)
			check(t, err)
			return map[string]any{"total": page.Total, "rows": page.Rows}
		}
		source, err := orm.Derive(grouped)
		check(t, err)
		selected := orm.Numeric(orm.OptionalOperand(orm.SelectedAggregate(source, value)))
		result, err := orm.AggregateResults(source, orm.Aggregate2(orm.Sum(selected), orm.Max(selected), func(sum, maximum orm.Optional[int64]) map[string]any {
			return map[string]any{"amount": optionalValue(sum), "maximum": optionalValue(maximum)}
		}))
		check(t, err)
		row, err := result.One(t.Context())
		check(t, err)
		return row
	default:
		t.Fatal("unknown collection observation", name)
		return nil
	}
}
