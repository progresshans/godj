package consumer

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"example.com/godj-project-bundle/catalog"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/uuid"
)

type collectionInput struct {
	Categories []string
	Labels     []struct {
		Code     string
		Category *string
	}
	Tickets []struct {
		Code     string
		Category *string
		Amount   *int64
		Enabled  *bool
		Score    *float64
		Price    *string
		Elapsed  *int64
	}
	Links   []struct{ Ticket, Label *string }
	Details []struct {
		Ticket string
		Bonus  *int64
	}
}
type collectionSeed struct {
	categories map[string]catalog.Category
	labels     map[string]catalog.Label
	tickets    map[string]records.Ticket
}

func seedCollection(t *testing.T, backend db.Mutator) collectionSeed {
	t.Helper()
	data, err := os.ReadFile("input.json")
	check(t, err)
	var input collectionInput
	check(t, json.Unmarshal(data, &input))
	result := collectionSeed{map[string]catalog.Category{}, map[string]catalog.Label{}, map[string]records.Ticket{}}
	for _, code := range input.Categories {
		row, err := catalog.CategoryObjects.Create(t.Context(), backend, catalog.NewCategoryCreate(code))
		check(t, err)
		result.categories[code] = row
	}
	for _, row := range input.Labels {
		value := catalog.NewLabelCreate(row.Code)
		if row.Category != nil {
			value = value.WithCategoryID(result.categories[*row.Category].ID)
		}
		saved, err := catalog.LabelObjects.Create(t.Context(), backend, value)
		check(t, err)
		result.labels[row.Code] = saved
	}
	for _, row := range input.Tickets {
		value := records.NewTicketCreate(row.Code)
		if row.Category != nil {
			value = value.WithCategoryID(result.categories[*row.Category].ID)
		}
		if row.Amount != nil {
			value = value.WithAmount(*row.Amount)
		}
		if row.Enabled != nil {
			value = value.WithEnabled(*row.Enabled)
		}
		if row.Score != nil {
			value = value.WithScore(*row.Score)
		}
		if row.Price != nil {
			price, err := decimal.Parse(*row.Price)
			check(t, err)
			value = value.WithPrice(price)
		}
		if row.Elapsed != nil {
			value = value.WithElapsed(duration.FromMicroseconds(*row.Elapsed))
		}
		// These additional nullable fields exercise Go-native ordered decoders.
		// The independent Django observer does not consume them or their outputs.
		if row.Code == "a" || row.Code == "d" {
			day := 1
			if row.Code == "d" {
				day = 2
			}
			date, err := calendar.New(2026, time.January, day)
			check(t, err)
			clockTime, err := clock.New(12, 30, day, 123456)
			check(t, err)
			binary, err := binaryvalue.FromBytes([]byte{byte(day), 0, 255})
			check(t, err)
			identity, err := uuid.Parse(map[int]string{1: "00000000-0000-0000-0000-000000000001", 2: "00000000-0000-0000-0000-000000000002"}[day])
			check(t, err)
			value = value.WithDate(date).WithTime(clockTime).WithStamp(time.Date(2026, time.January, day, 12, 30, 0, 0, time.UTC)).WithBinary(binary).WithIdentity(identity)
		}
		saved, err := records.TicketObjects.Create(t.Context(), backend, value)
		check(t, err)
		result.tickets[row.Code] = saved
	}
	for _, row := range input.Links {
		value := records.NewLinkCreate()
		if row.Ticket != nil {
			value = value.WithTicketID(result.tickets[*row.Ticket].ID)
		}
		if row.Label != nil {
			value = value.WithLabelID(result.labels[*row.Label].ID)
		}
		_, err := records.LinkObjects.Create(t.Context(), backend, value)
		check(t, err)
	}
	for _, row := range input.Details {
		value := records.NewDetailCreate(result.tickets[row.Ticket].ID)
		if row.Bonus != nil {
			value = value.WithBonus(*row.Bonus)
		}
		_, err := records.DetailObjects.Create(t.Context(), backend, value)
		check(t, err)
	}
	return result
}

func collectionValue(t *testing.T, value query.Value) any {
	t.Helper()
	switch value.Kind() {
	case query.ValueNull:
		return nil
	case query.ValueInteger:
		n, _ := value.Integer()
		return n
	case query.ValueFloat:
		n, _ := value.Float()
		return n
	case query.ValueString:
		n, _ := value.String()
		return n
	case query.ValueBoolean:
		n, _ := value.Boolean()
		return n
	case query.ValueDecimal:
		n, _ := value.Decimal()
		return n.String()
	case query.ValueDuration:
		n, _ := value.Duration()
		micros, err := n.TotalMicroseconds()
		check(t, err)
		return micros
	case query.ValueDate:
		n, _ := value.Date()
		return n.String()
	case query.ValueTime:
		n, _ := value.Time()
		return n.String()
	case query.ValueDateTime:
		n, _ := value.DateTime()
		return n.UTC().Format(time.RFC3339Nano)
	case query.ValueBinary:
		n, _ := value.Binary()
		return hex.EncodeToString(n.Bytes())
	case query.ValueUUID:
		n, _ := value.UUID()
		return n.String()
	default:
		t.Fatalf("unowned collection reference value: %s", value.Kind())
		return nil
	}
}
func optionalValue[V any](value orm.Optional[V]) any {
	if n, present := value.Get(); present {
		return n
	}
	return nil
}
func collectionCanonical(t *testing.T, rows []orm.GroupRow, names []string) []map[string]any {
	t.Helper()
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		cells := append(append([]query.Value{}, row.Keys...), row.Aggregates...)
		if len(cells) != len(names) {
			t.Fatal("collection column inventory", len(cells), len(names))
		}
		resultRow := map[string]any{}
		for i, cell := range cells {
			resultRow[names[i]] = collectionValue(t, cell)
		}
		result = append(result, resultRow)
	}
	return result
}
func collectionRows[M any](t *testing.T, grouped orm.GroupedQuery[M, orm.GroupRow], names ...string) []map[string]any {
	t.Helper()
	rows, err := grouped.All(t.Context())
	check(t, err)
	return collectionCanonical(t, rows, names)
}
func collectionGroup(t *testing.T, source project.CatalogLabelQuery, inputs ...orm.DynamicAggregateInput) orm.GroupedQuery[catalog.Label, orm.GroupRow] {
	t.Helper()
	grouped, err := source.GroupValues([]string{"code"}, inputs)
	check(t, err)
	return grouped.OrderBy(catalog.LabelFields.Code.Asc())
}
func typedCollectionGroup[R any](t *testing.T, source project.CatalogLabelQuery, aggregate orm.Aggregate[catalog.Label, R]) orm.GroupedQuery[catalog.Label, R] {
	t.Helper()
	grouped, err := project.GroupCatalogLabelBy(source, orm.Project1(catalog.LabelFields.Code, func(code string) string { return code }), aggregate, func(_ string, row R) R { return row })
	check(t, err)
	return grouped.OrderBy(catalog.LabelFields.Code.Asc())
}
func namedCollectionAggregate(t *testing.T, source project.CatalogLabelQuery, aggregate orm.Aggregate[catalog.Label, map[string]any]) orm.GroupedQuery[catalog.Label, map[string]any] {
	t.Helper()
	grouped, err := project.GroupCatalogLabelBy(source, orm.Project1(catalog.LabelFields.Code, func(code string) string { return code }), aggregate, func(code string, row map[string]any) map[string]any { row["code"] = code; return row })
	check(t, err)
	return grouped.OrderBy(catalog.LabelFields.Code.Asc())
}
func collectionCountQuery(t *testing.T, reader project.Backend) orm.GroupedQuery[catalog.Label, orm.GroupRow] {
	t.Helper()
	api, err := project.Using(reader)
	check(t, err)
	return collectionGroup(t, api.CatalogLabel, orm.DynamicAggregateInput{Kind: query.ResultCount, Field: "tickets__id"})
}

func observeCollection(t *testing.T, reader collectionBackend, name string) any {
	t.Helper()
	api, err := project.Using(reader)
	check(t, err)
	relations, err := project.BindRelations()
	check(t, err)
	base := api.CatalogLabel
	target, f := relations.CatalogLabel.Tickets, catalog.LabelFields
	agg := func(kind query.ResultExpressionKind, field string) orm.DynamicAggregateInput {
		return orm.DynamicAggregateInput{Kind: kind, Field: field}
	}
	filtered := func(source project.CatalogLabelQuery) any {
		return collectionRows(t, collectionGroup(t, source, agg(query.ResultCount, "tickets__id"), agg(query.ResultSum, "tickets__amount"), agg(query.ResultAvg, "tickets__amount"),
			orm.DynamicAggregateInput{Kind: query.ResultCount, Field: "tickets__id", Filter: []orm.LookupInput{{Key: "tickets__enabled", Value: true}}}), "code", "total", "amount", "average", "opened")
	}
	counts := func(value orm.AggregateExpression[catalog.Label, int64]) orm.GroupedQuery[catalog.Label, map[string]any] {
		return namedCollectionAggregate(t, base, orm.Aggregate1(value, func(total int64) map[string]any { return map[string]any{"total": total} }))
	}
	switch name {
	case "count_and_distinct":
		return collectionRows(t, collectionGroup(t, base, agg(query.ResultCount, "tickets__id"), orm.DynamicAggregateInput{Kind: query.ResultCount, Field: "tickets__id", Distinct: true}, agg(query.ResultCount, "tickets__amount")), "code", "total", "unique", "present")
	case "numeric_aggregates":
		var inputs []orm.DynamicAggregateInput
		for _, field := range []string{"amount", "price", "score", "elapsed"} {
			inputs = append(inputs, agg(query.ResultSum, "tickets__"+field), agg(query.ResultAvg, "tickets__"+field))
		}
		return collectionRows(t, collectionGroup(t, base, inputs...), "code", "amount", "average", "cost", "mean_cost", "effort", "mean_effort", "elapsed", "mean_elapsed")
	case "min_max":
		return collectionRows(t, collectionGroup(t, base, agg(query.ResultMin, "tickets__amount"), agg(query.ResultMax, "tickets__amount"), agg(query.ResultMin, "tickets__code"), agg(query.ResultMax, "tickets__code")), "code", "minimum", "maximum", "first", "last")
	case "distinct_numeric":
		inputs := []orm.DynamicAggregateInput{agg(query.ResultSum, "tickets__amount"), agg(query.ResultAvg, "tickets__amount"), agg(query.ResultSum, "tickets__price")}
		for i := range inputs {
			inputs[i].Distinct = true
		}
		return collectionRows(t, collectionGroup(t, base, inputs...), "code", "amount", "average", "cost")
	case "conditional_counts":
		grouped := namedCollectionAggregate(t, base, orm.Aggregate3(orm.Count(target.ID).Where(target.Enabled.Exact(true)), orm.Count(target.ID).Where(target.Category().Code.Exact("a")), orm.Sum(target.Amount).Where(target.Enabled.Exact(true)),
			func(opened, scoped int64, amount orm.Optional[int64]) map[string]any {
				return map[string]any{"opened": opened, "scoped": scoped, "amount": optionalValue(amount)}
			}))
		rows, err := grouped.All(t.Context())
		check(t, err)
		return rows
	case "negated_count", "negated_root_count", "nullable_or_condition", "source_key_isnull":
		value := orm.Count(target.ID).Where(orm.Not(target.Enabled.Exact(true)))
		if name == "negated_root_count" {
			value = orm.Count(f.ID).Where(orm.Not(target.Enabled.Exact(true)))
		}
		if name == "nullable_or_condition" {
			value = orm.Count(target.ID).Where(orm.Or(target.Amount.IsNull(true), target.Category().Code.Exact("b")))
		}
		if name == "source_key_isnull" {
			value = orm.Count(f.ID).Where(target.Category().IsNull(true))
		}
		rows, err := counts(value).All(t.Context())
		check(t, err)
		return rows
	case "optional_ancestor":
		return collectionRows(t, collectionGroup(t, base, agg(query.ResultCount, "category__tickets__id"), agg(query.ResultSum, "category__tickets__amount")), "code", "total", "amount")
	case "source_filter_before_group":
		return filtered(base.Filter(target.Enabled.Exact(true)))
	case "chained_filters":
		return filtered(base.Filter(target.Enabled.Exact(true)).Filter(target.Amount.GreaterThan(0)))
	case "chained_filters_reversed":
		return filtered(base.Filter(target.Amount.GreaterThan(0)).Filter(target.Enabled.Exact(true)))
	case "root_exclusion":
		return filtered(base.Filter(orm.Not(target.Enabled.Exact(true))))
	case "root_exclusion_then_filter":
		return filtered(base.Filter(orm.Not(target.Amount.GreaterThan(4))).Filter(target.Enabled.Exact(true)))
	case "root_or_condition":
		return filtered(base.Filter(orm.Or(target.Enabled.Exact(true), f.Code.Exact("none"))))
	case "independent_collection_counts", "independent_distinct_aggregates":
		inputs := []orm.DynamicAggregateInput{agg(query.ResultCount, "labels__id"), agg(query.ResultCount, "tickets__id"), agg(query.ResultSum, "tickets__amount")}
		if name == "independent_distinct_aggregates" {
			for i := range inputs {
				inputs[i].Distinct = true
			}
		}
		grouped, err := api.CatalogCategory.GroupValues([]string{"code"}, inputs)
		check(t, err)
		return collectionRows(t, grouped.OrderBy(catalog.CategoryFields.Code.Asc()), "code", "labels", "tickets", "amount")
	case "nested_collections":
		return collectionRows(t, collectionGroup(t, base, agg(query.ResultCount, "tickets__labels__id"), orm.DynamicAggregateInput{Kind: query.ResultCount, Field: "tickets__labels__id", Distinct: true}), "code", "total", "unique")
	case "reverse_one_to_one_leaf":
		return collectionRows(t, collectionGroup(t, base, agg(query.ResultCount, "tickets__detail__bonus"), agg(query.ResultSum, "tickets__detail__bonus")), "code", "total", "amount")
	case "global_aggregates":
		row, err := orm.AggregateInto(t.Context(), catalog.LabelObjects.Using(reader), orm.Aggregate5(orm.Count(target.ID), orm.Sum(target.Amount), orm.Avg(target.Amount), orm.Min(target.Amount), orm.Max(target.Amount),
			func(total int64, amount orm.Optional[int64], average orm.Optional[float64], minimum, maximum orm.Optional[int64]) map[string]any {
				return map[string]any{"total": total, "amount": optionalValue(amount), "average": optionalValue(average), "minimum": optionalValue(minimum), "maximum": optionalValue(maximum)}
			}))
		check(t, err)
		return row
	case "empty_global":
		row, err := orm.AggregateInto(t.Context(), catalog.LabelObjects.Using(reader).Filter(f.ID.In()), orm.Aggregate3(orm.Count(target.ID), orm.Sum(target.Amount), orm.Avg(target.Amount),
			func(total int64, amount orm.Optional[int64], average orm.Optional[float64]) map[string]any {
				return map[string]any{"total": total, "amount": optionalValue(amount), "average": optionalValue(average)}
			}))
		check(t, err)
		return row
	case "empty_groups":
		return filtered(base.Filter(f.Code.Exact("missing")))
	case "having_zero":
		rows, err := counts(orm.Count(target.ID)).Having(orm.Count(target.ID).Exact(0)).All(t.Context())
		check(t, err)
		return rows
	case "having_filtered":
		value := orm.Count(target.ID).Where(target.Enabled.Exact(true))
		grouped := namedCollectionAggregate(t, base, orm.Aggregate1(value, func(n int64) map[string]any { return map[string]any{"opened": n} }))
		rows, err := grouped.Having(value.GreaterThanOrEqual(2)).All(t.Context())
		check(t, err)
		return rows
	case "duration_average_having":
		grouped := collectionGroup(t, base, agg(query.ResultAvg, "tickets__elapsed"))
		return collectionRows(t, grouped.HavingDynamic(orm.GroupLookupInput{Column: 1, Lookup: query.LookupGreaterThanOrEqual, Value: duration.FromMicroseconds(4)}), "code", "elapsed")
	case "order_slice":
		grouped := counts(orm.Count(target.ID)).OrderBy(orm.Count(target.ID).Desc(), f.Code.Asc())
		grouped, err = grouped.Offset(1)
		check(t, err)
		grouped, err = grouped.Limit(3)
		check(t, err)
		rows, err := grouped.All(t.Context())
		check(t, err)
		return rows
	case "page_past_end":
		page, err := counts(orm.Count(target.ID)).Page(t.Context(), 2, 50)
		check(t, err)
		return map[string]any{"total": page.Total, "rows": page.Rows}
	case "derived_aggregate":
		source, err := orm.Derive(counts(orm.Count(target.ID)))
		check(t, err)
		selected := orm.SelectedAggregate(source, orm.Count(target.ID))
		result, err := orm.AggregateResults(source, orm.Aggregate2(orm.Sum(orm.Numeric(selected)), orm.Max(orm.Numeric(selected)), func(sum, maximum orm.Optional[int64]) map[string]any {
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
