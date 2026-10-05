package consumer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type groupedReferenceCase struct {
	Case       string
	Result     json.RawMessage
	Error      string
	RowsAfter  json.RawMessage `json:"rows_after"`
	Statements []string
}

var groupedBaseNames = []string{"rank", "total", "present", "unique", "opened", "minimum", "maximum"}

func groupedInputs() []orm.DynamicAggregateInput {
	return []orm.DynamicAggregateInput{
		{Kind: query.ResultCountAll}, {Kind: query.ResultCount, Field: "note"}, {Kind: query.ResultCount, Field: "note", Distinct: true},
		{Kind: query.ResultCount, Field: "id", Filter: []orm.LookupInput{{Key: "enabled", Value: true}}}, {Kind: query.ResultMin, Field: "amount"}, {Kind: query.ResultMax, Field: "amount"},
	}
}
func groupedReferenceQuery(t *testing.T, source project.RecordsItemQuery) orm.GroupedQuery[records.Item, orm.GroupRow] {
	t.Helper()
	grouped, err := source.OrderBy().GroupValues([]string{"rank"}, groupedInputs())
	check(t, err)
	return grouped.OrderBy(records.ItemFields.Rank.Asc().NullsLast())
}
func groupedCanonicalValue(t *testing.T, value query.Value) any {
	t.Helper()
	if value.IsNull() {
		return nil
	}
	if v, ok := value.Integer(); ok {
		return v
	}
	if v, ok := value.Boolean(); ok {
		return v
	}
	if v, ok := value.String(); ok {
		return v
	}
	if v, ok := value.Float(); ok {
		return v
	}
	if v, ok := value.Decimal(); ok {
		return v.String()
	}
	if v, ok := value.UUID(); ok {
		return v.String()
	}
	if v, ok := value.Binary(); ok {
		return hex.EncodeToString(v.Bytes())
	}
	if v, ok := value.Date(); ok {
		return v.String()
	}
	if v, ok := value.Time(); ok {
		return v.String()
	}
	if v, ok := value.DateTime(); ok {
		return v.Format("2006-01-02T15:04:05.999999-07:00")
	}
	if v, ok := value.Duration(); ok {
		n, err := v.TotalMicroseconds()
		check(t, err)
		return n
	}
	t.Fatalf("unhandled result kind %s", value.Kind())
	return nil
}
func groupedCanonicalRows(t *testing.T, rows []orm.GroupRow, names []string) []map[string]any {
	t.Helper()
	result := make([]map[string]any, len(rows))
	for i, row := range rows {
		values := append(append([]query.Value{}, row.Keys...), row.Aggregates...)
		if len(values) != len(names) {
			t.Fatal("result columns", len(values), len(names))
		}
		result[i] = map[string]any{}
		for j, value := range values {
			result[i][names[j]] = groupedCanonicalValue(t, value)
		}
	}
	return result
}
func groupedResult(t *testing.T, ctx context.Context, grouped orm.GroupedQuery[records.Item, orm.GroupRow], names []string) []map[string]any {
	t.Helper()
	rows, err := grouped.All(ctx)
	check(t, err)
	return groupedCanonicalRows(t, rows, names)
}
func equalGroupedJSON(t *testing.T, actual any, expected json.RawMessage) {
	t.Helper()
	encoded, err := json.Marshal(actual)
	check(t, err)
	var got, want any
	check(t, json.Unmarshal(encoded, &got))
	check(t, json.Unmarshal(expected, &want))
	// Decimal's numeric value is the contract; SQLite/Django can print extra
	// insignificant places. Both sides undergo the same exact decimal parse.
	var canonical func(any)
	canonical = func(v any) {
		switch value := v.(type) {
		case []any:
			for _, child := range value {
				canonical(child)
			}
		case map[string]any:
			for key, child := range value {
				if key == "price" {
					if raw, ok := child.(string); ok {
						number, err := decimal.Parse(raw)
						check(t, err)
						value[key] = number.String()
					}
				} else {
					canonical(child)
				}
			}
		}
	}
	canonical(got)
	canonical(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Go=%s\nDjango=%s", encoded, expected)
	}
}

func TestGroupedReference(t *testing.T) {
	withGroupedBackends(t, func(t *testing.T, backend groupedBackend, _ func() (groupedBackend, error), postgres bool) {
		name := "sqlite"
		if postgres {
			name = "postgres"
		}
		raw, err := os.ReadFile("grouped-django61-" + name + ".json")
		check(t, err)
		var reference struct{ Cases []groupedReferenceCase }
		check(t, json.Unmarshal(raw, &reference))
		probe := &groupedProbe{groupedBackend: backend}
		api, err := project.Using(probe)
		check(t, err)
		relations, err := project.BindRelations()
		check(t, err)
		fields := records.ItemFields
		opened := orm.Count(fields.ID).Where(fields.Enabled.Exact(true))
		total := orm.CountRows[records.Item]()
		static := map[string]bool{"where_after_group": true, "distinct_fields": true, "alias_conflict": true, "eager_source": true}
		for _, native := range reference.Cases {
			if static[native.Case] {
				continue
			}
			t.Run(native.Case, func(t *testing.T) {
				ctx := t.Context()
				source := api.RecordsItem
				names := groupedBaseNames
				var observed any
				var grouped orm.GroupedQuery[records.Item, orm.GroupRow]
				var rejected error
				handled := false
				before := len(probe.plans)
				unsupported := false
				switch native.Case {
				case "retained_source_ordering":
					_, rejected = source.OrderBy(fields.Amount.Asc()).GroupValues([]string{"rank"}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll}})
					unsupported = true
				case "source_slice", "source_slice_inherited_ordering":
					source, err = source.OrderBy(fields.ID.Asc()).Limit(3)
					check(t, err)
					_, rejected = source.GroupValues([]string{"rank"}, groupedInputs())
					unsupported = true
				case "filtered_count_star":
					_, rejected = source.GroupValues([]string{"rank"}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll, Filter: []orm.LookupInput{{Key: "enabled", Value: true}}}})
					unsupported = true
				case "collection_key":
					_, rejected = source.GroupValues([]string{"peers__rank"}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll}})
					unsupported = true
				case "reverse_counts":
					_, rejected = api.RecordsGroup.GroupValues([]string{"name"}, []orm.DynamicAggregateInput{{Kind: query.ResultCount, Field: "items__id"}})
					unsupported = true
				case "row_locked_group", "row_locked_group_in_atomic":
					if native.Case == "row_locked_group_in_atomic" {
						check(t, backend.Atomic(ctx, func(session db.Session) error {
							bound, err := project.UsingSession(session)
							if err != nil {
								return err
							}
							_, rejected = bound.RecordsItem.SelectForUpdate(orm.RowLockOptions{}).GroupValues([]string{"rank"}, groupedInputs())
							return nil
						}))
					} else {
						_, rejected = source.SelectForUpdate(orm.RowLockOptions{}).GroupValues([]string{"rank"}, groupedInputs())
					}
					unsupported = true
				case "codec_key_document":
					_, rejected = source.GroupValues([]string{"document"}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll}})
					unsupported = true
				case "missing_key":
					_, rejected = source.GroupValues([]string{"missing"}, groupedInputs())
				case "missing_aggregate_field":
					_, rejected = source.GroupValues([]string{"rank"}, []orm.DynamicAggregateInput{{Kind: query.ResultCount, Field: "missing"}})
				case "where_before_group":
					source = source.Filter(fields.Enabled.Exact(true))
				case "forward_filter":
					source = source.Filter(relations.RecordsItem.Group.Name.Exact("first"))
				case "collection_filter":
					source = source.Filter(relations.RecordsItem.Peers.ID.In(2, 3))
				case "collection_negation":
					source = source.Filter(orm.Not(relations.RecordsItem.Peers.ID.Exact(2)))
				case "empty_native":
					source = source.Filter(fields.ID.GreaterThan(99))
				case "empty_folded":
					source = source.Filter(fields.ID.In())
				case "source_distinct":
					source = source.Distinct()
				case "source_ordering":
					source = source.OrderBy(fields.Amount.Asc())
				}
				if unsupported || native.Case == "missing_key" || native.Case == "missing_aggregate_field" {
					code := query.CodeUnknownField
					if unsupported {
						code = query.CodeUnsupported
					}
					if !errors.Is(rejected, &query.Error{Code: code}) || len(probe.plans) != before {
						t.Fatal("unsupported/unknown group did not reject before query", rejected)
					}
					if !unsupported && native.Error != "FieldError" {
						t.Fatal("native missing-field class changed", native.Error)
					}
					t.Logf("explicit Go boundary: %s; Django error=%q; native result retained in locked fixture", code, native.Error)
					handled = true
				} else {
					grouped = groupedReferenceQuery(t, source)
					switch native.Case {
					case "nullable_key", "where_before_group", "source_ordering", "forward_filter", "collection_filter", "collection_negation", "empty_native", "empty_folded", "source_distinct":
					case "multi_key":
						grouped, err = source.GroupValues([]string{"rank", "enabled"}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll}})
						check(t, err)
						grouped = grouped.OrderBy(fields.Rank.Asc().NullsLast(), fields.Enabled.Asc())
						names = []string{"rank", "enabled", "total"}
					case "key_order_asc_native":
						grouped = grouped.OrderBy(fields.Rank.Asc())
					case "key_order_desc_native":
						grouped = grouped.OrderBy(fields.Rank.Desc())
					case "key_order_desc_null_first":
						grouped = grouped.OrderBy(fields.Rank.Desc().NullsFirst())
					case "having_total":
						grouped = grouped.Having(total.GreaterThanOrEqual(3))
					case "having_opened", "count_groups":
						grouped = grouped.Having(opened.GreaterThanOrEqual(2))
					case "having_or":
						grouped = grouped.Having(orm.GroupOr(opened.GreaterThanOrEqual(2), total.GreaterThanOrEqual(3)))
					case "having_not":
						grouped = grouped.Having(orm.GroupNot(opened.GreaterThanOrEqual(2)))
					case "having_key_not":
						grouped = grouped.Having(orm.GroupNot(orm.GroupKey(fields.Rank).Exact(pointer(int64(1)))))
					case "having_mixed_where_or":
						grouped = grouped.Having(orm.GroupOr(opened.GreaterThanOrEqual(2), orm.GroupKey(fields.Rank).Exact(pointer(int64(7)))))
					case "having_no_groups":
						grouped = grouped.Having(total.GreaterThan(99))
					case "having_nullable_min", "having_nullable_filtered_min", "having_nullable_not", "having_nullable_note_not":
						field := "amount"
						if native.Case == "having_nullable_min" || native.Case == "having_nullable_note_not" {
							field = "note"
						}
						input := orm.DynamicAggregateInput{Kind: query.ResultMin, Field: field}
						if native.Case != "having_nullable_min" {
							input.Filter = []orm.LookupInput{{Key: "enabled", Value: false}}
						}
						grouped, err = source.GroupValues([]string{"rank"}, []orm.DynamicAggregateInput{input})
						check(t, err)
						grouped = grouped.OrderBy(fields.Rank.Asc().NullsLast())
						names = []string{"rank", "minimum"}
						switch native.Case {
						case "having_nullable_not":
							grouped = grouped.Having(orm.GroupNot(orm.Min(fields.Amount).Where(fields.Enabled.Exact(false)).GreaterThanOrEqual(orm.Some(int64(50)))))
						case "having_nullable_note_not":
							grouped = grouped.Having(orm.GroupNot(orm.Min(fields.Note).Where(fields.Enabled.Exact(false)).GreaterThanOrEqual(orm.Some("a"))))
						default:
							grouped = grouped.HavingDynamic(orm.GroupLookupInput{Column: 1, Lookup: query.LookupIsNull, Value: true})
						}
					case "empty_slice":
						grouped, err = grouped.Limit(0)
						check(t, err)
					case "page_groups":
						grouped, err = grouped.Offset(1)
						check(t, err)
						grouped, err = grouped.Limit(2)
						check(t, err)
					case "page_past_end":
						grouped, err = grouped.Offset(10)
						check(t, err)
						grouped, err = grouped.Limit(2)
						check(t, err)
					case "order_aggregate":
						grouped = grouped.OrderBy(opened.Desc(), fields.Rank.Asc().NullsLast())
					case "default_model_ordering":
						grouped, err = source.GroupValues([]string{"rank"}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll}})
						check(t, err)
						grouped = grouped.OrderBy(fields.Rank.Asc().NullsLast())
						names = []string{"rank", "total"}
					case "filtered_aggregates":
						grouped, err = source.GroupValues([]string{"rank"}, []orm.DynamicAggregateInput{{Kind: query.ResultMin, Field: "amount", Filter: []orm.LookupInput{{Key: "enabled", Value: false}}}, {Kind: query.ResultMax, Field: "note", Filter: []orm.LookupInput{{Key: "enabled", Value: false}}}, {Kind: query.ResultCount, Field: "note", Distinct: true, Filter: []orm.LookupInput{{Key: "enabled", Value: true}}}})
						check(t, err)
						grouped = grouped.OrderBy(fields.Rank.Asc().NullsLast())
						names = []string{"rank", "minimum", "maximum", "total"}
					case "count_null_and_empty":
						grouped, err = source.GroupValues([]string{"note"}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll}, {Kind: query.ResultCount, Field: "note"}})
						check(t, err)
						grouped = grouped.OrderBy(fields.Note.Asc().NullsLast())
						names = []string{"note", "total", "present"}
					case "forward_key", "forward_nullable_key":
						field := "group__name"
						if native.Case == "forward_nullable_key" {
							field = "group__region"
						}
						grouped, err = source.GroupValues([]string{field}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll}})
						check(t, err)
						grouped = grouped.OrderByDynamic(orm.GroupOrderInput{Column: 0, Direction: query.Ascending, Nulls: query.NullsLast})
						names = []string{field, "total"}
					case "forward_conditional_count", "forward_missing_conditional_count":
						filter := orm.LookupInput{Key: "group__region", Value: "east"}
						if native.Case == "forward_missing_conditional_count" {
							filter = orm.LookupInput{Key: "group__isnull", Value: true}
						}
						grouped, err = source.GroupValues([]string{"rank"}, []orm.DynamicAggregateInput{{Kind: query.ResultCount, Field: "id", Filter: []orm.LookupInput{filter}}})
						check(t, err)
						grouped = grouped.OrderBy(fields.Rank.Asc().NullsLast())
						names = []string{"rank", "total"}
					case "conditional_negation":
						value, err := project.GroupRecordsItemBy(source, orm.Project1(fields.Rank, func(rank *int64) *int64 { return rank }), orm.Aggregate1(orm.Count(fields.ID).Where(orm.Not(fields.Note.Exact("same"))), func(n int64) int64 { return n }), func(rank *int64, n int64) map[string]any { return map[string]any{"rank": rank, "total": n} })
						check(t, err)
						observed, err = value.OrderBy(fields.Rank.Asc().NullsLast()).All(ctx)
						check(t, err)
						handled = true
					case "missing_having_alias":
						_, rejected = grouped.HavingDynamic(orm.GroupLookupInput{Column: 999, Lookup: query.LookupGreaterThanOrEqual, Value: int64(1)}).All(ctx)
					case "filter_after_slice", "ordering_after_slice":
						grouped, err = grouped.Limit(2)
						check(t, err)
						if native.Case == "filter_after_slice" {
							_, rejected = grouped.Having(total.GreaterThanOrEqual(1)).All(ctx)
						} else {
							_, rejected = grouped.OrderBy(total.Desc()).All(ctx)
						}
					case "aggregate_only_empty":
						observed, err = orm.AggregateInto(ctx, records.ItemObjects.Using(probe).Filter(fields.ID.In()), orm.Aggregate2(total, orm.Min(fields.Amount), func(n int64, minimum orm.Optional[int64]) map[string]any {
							value, valid := minimum.Get()
							var min any
							if valid {
								min = value
							}
							return map[string]any{"total": n, "minimum": min}
						}))
						check(t, err)
						handled = true
					case "cached_snapshot":
						sibling := grouped.Fresh()
						first := groupedResult(t, ctx, grouped, names)
						_, err := source.Filter(fields.ID.Exact(8)).Update(ctx, orm.Assign(fields.Rank, int64(9)))
						check(t, err)
						cacheBefore := len(probe.plans)
						n, err := grouped.Count(ctx)
						check(t, err)
						held := groupedResult(t, ctx, grouped, names)
						cachedReads := len(probe.plans) - cacheBefore
						observed = map[string]any{"before": first, "cached": held, "count": n, "cached_reads": cachedReads, "fresh": groupedResult(t, ctx, grouped.Fresh(), names), "sibling": groupedResult(t, ctx, sibling, names)}
						handled = true
					default:
						if !strings.HasPrefix(native.Case, "codec_key_") {
							t.Fatal("unowned grouped reference", native.Case)
						}
						field := strings.TrimPrefix(native.Case, "codec_key_")
						grouped, err = source.GroupValues([]string{field}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll}})
						check(t, err)
						grouped = grouped.OrderByDynamic(orm.GroupOrderInput{Column: 0, Direction: query.Ascending})
						names = []string{field, "total"}
					}
					if native.Case == "missing_having_alias" || native.Case == "filter_after_slice" || native.Case == "ordering_after_slice" {
						if !errors.Is(rejected, &query.Error{Code: query.CodeInvalidPlan}) || len(probe.plans) != before {
							t.Fatal("invalid group refinement performed query", rejected)
						}
						handled = true
					}
					if !handled {
						if native.Case == "count_groups" || native.Case == "empty_slice" || native.Case == "page_groups" || native.Case == "page_past_end" {
							count, err := grouped.Count(ctx)
							check(t, err)
							rows := groupedResult(t, ctx, grouped, names)
							cached, err := grouped.Count(ctx)
							check(t, err)
							observed = map[string]any{"count": count, "rows": rows, "cached_count": cached}
						} else {
							observed = groupedResult(t, ctx, grouped, names)
						}
					}
					if rejected == nil {
						if native.Error != "" {
							t.Fatal("Go succeeded while native failed", native.Error)
						}
						equalGroupedJSON(t, observed, native.Result)
					}
				}
				stored, err := records.ItemObjects.Using(backend).OrderBy(fields.ID.Asc()).All(ctx)
				check(t, err)
				after := make([][]any, len(stored))
				for i, value := range stored {
					after[i] = []any{value.ID, value.Rank, value.Enabled}
				}
				equalGroupedJSON(t, after, native.RowsAfter)
				if native.Case == "cached_snapshot" {
					_, err := source.Filter(fields.ID.Exact(8)).Update(ctx, orm.AssignNull(fields.Rank))
					check(t, err)
				}
			})
		}
	})
}

// Keep malformed observer fixtures from becoming vacuous consumer passes.
func groupedReferenceNames(t *testing.T, backend string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("grouped-django61-%s.json", backend))
	check(t, err)
	var value struct{ Cases []groupedReferenceCase }
	check(t, json.Unmarshal(raw, &value))
	result := map[string]json.RawMessage{}
	for _, entry := range value.Cases {
		result[entry.Case] = entry.Result
	}
	return result
}
