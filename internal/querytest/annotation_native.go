package querytest

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// AnnotationNative uses independently seeded SQL rows. Expected multiplicities
// and arithmetic below come from those rows, not from the compiler's SQL text.
func AnnotationNative(t *testing.T, backend db.Queryer, database *sql.DB, quote func(string) string, postgres bool) {
	t.Helper()
	ctx := t.Context()
	exec := func(statement string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	spanType := "BIGINT"
	span := func(value string) string { return value }
	if postgres {
		spanType = "INTERVAL"
		span = func(value string) string { return "INTERVAL '" + value + " microseconds'" }
	}
	owners, items, labels := quote("annotation_owners"), quote("annotation_items"), quote("annotation_labels")
	exec("CREATE TABLE " + owners + " (id BIGINT PRIMARY KEY, kind TEXT NOT NULL, amount BIGINT NOT NULL, optional BIGINT)")
	exec("CREATE TABLE " + items + " (id BIGINT PRIMARY KEY, owner_id BIGINT NOT NULL REFERENCES " + owners + "(id), value BIGINT, span " + spanType + ")")
	exec("CREATE TABLE " + labels + " (id BIGINT PRIMARY KEY, owner_id BIGINT NOT NULL REFERENCES " + owners + "(id), value BIGINT NOT NULL)")
	exec("INSERT INTO " + owners + " VALUES (1,'x',5,2),(2,'x',-7,NULL),(3,'y',3,0),(4,'z',5,2),(5,'y',0,-2)")
	exec("INSERT INTO " + items + " VALUES (1,1,2," + span("3") + "),(2,1,5," + span("4") + "),(3,2,7,NULL),(4,3,NULL," + span("0") + "),(5,3,11," + span("1") + ")")
	exec("INSERT INTO " + labels + " VALUES (1,1,10),(2,1,20),(3,3,30)")
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	kind := query.NewFieldRef("kind", "kind", query.FieldString, false)
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	optional := query.NewFieldRef("optional", "optional", query.FieldInteger, true)
	value := query.NewFieldRef("value", "value", query.FieldInteger, true)
	period := query.NewFieldRef("span", "span", query.FieldDuration, true)
	base := query.NewPlan("annotation_owners", []query.FieldRef{id, kind, amount, optional})
	ordered := base.WithOrderings(query.NewOrdering(id, query.Ascending))
	row := func(t *testing.T, value query.ScalarExpression) query.ResultExpression {
		t.Helper()
		result, err := query.ScalarResult(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	field := func(t *testing.T, value query.FieldRef) query.ScalarExpression {
		t.Helper()
		result, err := query.FieldExpression(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	literal := func(t *testing.T, value query.Value) query.ScalarExpression {
		t.Helper()
		result, err := query.LiteralExpression(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	arithmetic := func(t *testing.T, op query.ArithmeticOperator, left, right query.ScalarExpression) query.ScalarExpression {
		t.Helper()
		result, err := query.Arithmetic(op, left, right)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	bind := func(t *testing.T, plan query.Plan, name string, value query.ResultExpression, selected bool) (query.Plan, query.ScalarExpression) {
		t.Helper()
		plan, ref, err := plan.WithAnnotation(name, value, selected)
		if err != nil {
			t.Fatal(err)
		}
		scalar, err := query.AnnotationExpression(ref)
		if err != nil {
			t.Fatal(err)
		}
		return plan, scalar
	}
	project := func(t *testing.T, plan query.Plan, values ...query.ResultExpression) query.Plan {
		t.Helper()
		shape, err := query.NewProjectionResult(values...)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	predicate := func(t *testing.T, value query.ScalarExpression, lookup query.Lookup, n int64) query.Expression {
		t.Helper()
		condition, err := query.NewScalarCondition(value, lookup, query.Integer(n))
		if err != nil {
			t.Fatal(err)
		}
		result, err := query.NewExpression(condition)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	filter := func(t *testing.T, plan query.Plan, value query.Expression) query.Plan {
		t.Helper()
		result, err := plan.WithWhere(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	read := func(t *testing.T, plan query.Plan) [][]any {
		t.Helper()
		rows, err := backend.Query(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result [][]any
		for rows.Next() {
			values := make([]any, len(plan.ResultShape().Expressions()))
			targets := make([]any, len(values))
			for index := range values {
				targets[index] = &values[index]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			result = append(result, values)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		return result
	}
	check := func(t *testing.T, plan query.Plan, want [][]any) {
		t.Helper()
		if got := read(t, plan); !reflect.DeepEqual(got, want) {
			t.Fatalf("named native rows = %#v; want %#v", got, want)
		}
	}
	route := func(t *testing.T, table string, terminal query.FieldRef) query.RelationPath {
		t.Helper()
		path, err := query.NewReverseRelationPath(ir.ModelIdentity{AppLabel: "annotations", ModelName: table}, table,
			"owner", "owner_id", ir.ModelIdentity{AppLabel: "annotations", ModelName: "owner"}, "annotation_owners", "id", table, false, id, ir.RelationOneToMany)
		if err != nil {
			t.Fatal(err)
		}
		path, err = query.NewRelationChain(path.Hops(), []query.FieldRef{id, id}, terminal, query.RelationTerminalRelatedField)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	itemID, itemValue, itemSpan, labelID := route(t, "annotation_items", id), route(t, "annotation_items", value), route(t, "annotation_items", period), route(t, "annotation_labels", id)
	aggregate := func(t *testing.T, op query.ResultExpressionKind, path query.RelationPath) query.ResultExpression {
		t.Helper()
		value, err := query.RelatedAggregateResult(op, path)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	countItems := aggregate(t, query.ResultCount, itemID)
	greaterThanThree, err := query.NewExpression(query.NewRelatedCondition(itemValue, query.LookupGreaterThan, query.Integer(3)))
	if err != nil {
		t.Fatal(err)
	}
	one, two := literal(t, query.Integer(1)), literal(t, query.Integer(2))

	t.Run("scalar_dependencies_and_rebinding", func(t *testing.T) {
		first, adjusted := bind(t, ordered, "adjusted", row(t, arithmetic(t, query.ArithmeticAdd, field(t, amount), one)), true)
		second, doubled := bind(t, first, "doubled", row(t, arithmetic(t, query.ArithmeticMultiply, adjusted, two)), true)
		final, latest := bind(t, second, "adjusted", row(t, literal(t, query.Integer(100))), true)
		check(t, project(t, final, query.FieldResult(id), row(t, latest), row(t, doubled)), [][]any{
			{int64(1), int64(100), int64(12)}, {int64(2), int64(100), int64(-12)}, {int64(3), int64(100), int64(8)}, {int64(4), int64(100), int64(12)}, {int64(5), int64(100), int64(2)},
		})
		check(t, project(t, first, row(t, adjusted)), [][]any{{int64(6)}, {int64(-6)}, {int64(4)}, {int64(6)}, {int64(1)}})
	})
	t.Run("predicates_keep_their_definition_and_null_semantics", func(t *testing.T) {
		first, adjusted := bind(t, ordered, "adjusted", row(t, arithmetic(t, query.ArithmeticAdd, field(t, optional), one)), true)
		filtered := filter(t, first, predicate(t, adjusted, query.LookupGreaterThan, 1))
		final, latest := bind(t, filtered, "adjusted", row(t, literal(t, query.Integer(-100))), true)
		check(t, project(t, final, query.FieldResult(id), row(t, latest)), [][]any{{int64(1), int64(-100)}, {int64(4), int64(-100)}})
		not, err := query.NotExpression(predicate(t, adjusted, query.LookupGreaterThan, 1))
		if err != nil {
			t.Fatal(err)
		}
		check(t, project(t, filter(t, first, not), query.FieldResult(id), row(t, adjusted)), [][]any{{int64(3), int64(1)}, {int64(5), int64(-1)}})
	})
	t.Run("filter_before_aggregate_reuses_filtered_rows", func(t *testing.T) {
		plan, count := bind(t, filter(t, ordered, greaterThanThree), "counted", countItems, true)
		plan, sum := bind(t, plan, "summed", aggregate(t, query.ResultSum, itemValue), true)
		check(t, project(t, plan, query.FieldResult(id), row(t, count), row(t, sum)), [][]any{{int64(1), int64(1), int64(5)}, {int64(2), int64(1), int64(7)}, {int64(3), int64(1), int64(11)}})
	})
	t.Run("filter_after_aggregate_cannot_rebind_earlier_join", func(t *testing.T) {
		plan, count := bind(t, ordered, "counted", countItems, true)
		plan = filter(t, plan, greaterThanThree)
		plan, sum := bind(t, plan, "summed", aggregate(t, query.ResultSum, itemValue), true)
		check(t, project(t, plan, query.FieldResult(id), row(t, count), row(t, sum)), [][]any{{int64(1), int64(2), int64(10)}, {int64(2), int64(1), int64(7)}, {int64(3), int64(2), int64(22)}})
	})
	t.Run("multiple_collections_keep_row_products_and_distinct", func(t *testing.T) {
		plan, count := bind(t, ordered, "items", countItems, true)
		plan, labels := bind(t, plan, "labels", aggregate(t, query.ResultCount, labelID), true)
		check(t, project(t, plan, query.FieldResult(id), row(t, count), row(t, labels)), [][]any{{int64(1), int64(4), int64(4)}, {int64(2), int64(1), int64(0)}, {int64(3), int64(2), int64(2)}, {int64(4), int64(0), int64(0)}, {int64(5), int64(0), int64(0)}})
		distinctItems, err := countItems.WithDistinct()
		if err != nil {
			t.Fatal(err)
		}
		distinctLabels, err := aggregate(t, query.ResultCount, labelID).WithDistinct()
		if err != nil {
			t.Fatal(err)
		}
		plan, count = bind(t, ordered, "items", distinctItems, true)
		plan, labels = bind(t, plan, "labels", distinctLabels, true)
		check(t, project(t, plan, query.FieldResult(id), row(t, count), row(t, labels)), [][]any{{int64(1), int64(2), int64(2)}, {int64(2), int64(1), int64(0)}, {int64(3), int64(2), int64(1)}, {int64(4), int64(0), int64(0)}, {int64(5), int64(0), int64(0)}})
	})
	t.Run("aggregate_dependencies_and_mixed_or_having", func(t *testing.T) {
		plan, count := bind(t, ordered, "counted", countItems, true)
		plan, doubled := bind(t, plan, "doubled", row(t, arithmetic(t, query.ArithmeticMultiply, count, two)), true)
		mixed, err := query.OrExpressions(predicate(t, doubled, query.LookupGreaterThanOrEqual, 4), predicate(t, field(t, id), query.LookupExact, 4))
		if err != nil {
			t.Fatal(err)
		}
		check(t, project(t, filter(t, plan, mixed), query.FieldResult(id), row(t, count), row(t, doubled)), [][]any{{int64(1), int64(2), int64(4)}, {int64(3), int64(2), int64(4)}, {int64(4), int64(0), int64(0)}})
	})
	t.Run("hidden_alias_requires_promotion_and_names_are_not_sql_aliases", func(t *testing.T) {
		name := strings.Repeat("a", 127)
		plan, hidden := bind(t, ordered, name+"x", row(t, arithmetic(t, query.ArithmeticAdd, field(t, amount), one)), false)
		shape, err := query.NewProjectionResult(row(t, hidden))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.WithResultShape(shape); err == nil {
			t.Fatal("hidden alias was selectable without promotion")
		}
		plan, visible := bind(t, plan, name+"y", row(t, hidden), true)
		check(t, project(t, filter(t, plan, predicate(t, hidden, query.LookupGreaterThan, 4)), query.FieldResult(id), row(t, visible)), [][]any{{int64(1), int64(6)}, {int64(4), int64(6)}})
	})
	t.Run("projection_before_grouping_retains_hidden_keys", func(t *testing.T) {
		input := project(t, base, query.FieldResult(kind)).WithOrderings(query.NewOrdering(kind, query.Ascending))
		sum, err := query.SumResult(query.FieldResult(amount))
		if err != nil {
			t.Fatal(err)
		}
		plan, total := bind(t, input, "total", sum, true)
		check(t, project(t, plan, row(t, total)), [][]any{{int64(-2)}, {int64(3)}, {int64(5)}})
	})
	t.Run("explicit_result_stage_preserves_slice_and_reaggregation", func(t *testing.T) {
		plan, count := bind(t, ordered, "counted", countItems, true)
		input := project(t, plan, query.FieldResult(id), row(t, count))
		input, err = input.WithOffset(1)
		if err != nil {
			t.Fatal(err)
		}
		input, err = input.WithLimit(3)
		if err != nil {
			t.Fatal(err)
		}
		stage, scope, err := query.NewDerivedPlan(input)
		if err != nil {
			t.Fatal(err)
		}
		column := DerivedColumn(t, scope, 1)
		stage, doubled := bind(t, stage, "doubled", row(t, arithmetic(t, query.ArithmeticMultiply, column, two)), false)
		sum, err := query.ScalarAggregateResult(query.ResultSum, doubled)
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewAggregateResult(sum)
		if err != nil {
			t.Fatal(err)
		}
		result, err := stage.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		check(t, result, [][]any{{int64(6)}})
	})
	t.Run("exact_duration_aggregate_dependencies_keep_fractions", func(t *testing.T) {
		periodValue, err := query.RelatedExpression(itemSpan)
		if err != nil {
			t.Fatal(err)
		}
		identifier, err := query.RelatedExpression(itemID)
		if err != nil {
			t.Fatal(err)
		}
		fraction := arithmetic(t, query.ArithmeticDivide, periodValue, arithmetic(t, query.ArithmeticAdd, identifier, one))
		sum, err := query.ScalarAggregateResult(query.ResultSum, fraction)
		if err != nil {
			t.Fatal(err)
		}
		plan, total := bind(t, ordered, "fraction_total", sum, true)
		plan, ratio := bind(t, plan, "ratio", row(t, arithmetic(t, query.ArithmeticDivide, total, literal(t, query.Duration(duration.FromMicroseconds(1))))), true)
		check(t, project(t, plan, query.FieldResult(id), row(t, ratio)), [][]any{{int64(1), float64(17.0 / 6.0)}, {int64(2), nil}, {int64(3), float64(1.0 / 6.0)}, {int64(4), nil}, {int64(5), nil}})
	})
	t.Run("native_duration_mean_is_preserved_behind_a_name", func(t *testing.T) {
		plan, mean := bind(t, ordered, "native_mean", aggregate(t, query.ResultAvg, itemSpan), true)
		plan, ratio := bind(t, plan, "ratio", row(t, arithmetic(t, query.ArithmeticDivide, mean, literal(t, query.Duration(duration.FromMicroseconds(1))))), true)
		native := "SELECT AVG(span) FROM " + items + " WHERE owner_id = 1"
		if postgres {
			native = "SELECT CAST(EXTRACT(EPOCH FROM AVG(span)) * 1000000 AS double precision) FROM " + items + " WHERE owner_id = 1"
		}
		var expected float64
		if err := database.QueryRowContext(ctx, native).Scan(&expected); err != nil {
			t.Fatal(err)
		}
		check(t, project(t, filter(t, plan, predicate(t, field(t, id), query.LookupExact, 1)), row(t, ratio)), [][]any{{expected}})
	})
}
