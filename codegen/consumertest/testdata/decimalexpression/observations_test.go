package consumer

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"example.com/godj-project-bundle/ledger"
	"example.com/godj-project-bundle/pricing"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func d(text string) decimal.Decimal {
	value, err := decimal.Parse(text)
	if err != nil {
		panic(err)
	} // Only fixed, test-owned literals call this helper.
	return value
}

func optionalDecimal(value orm.Optional[decimal.Decimal]) any {
	if number, present := value.Get(); present {
		return number.String()
	}
	return nil
}
func pointerDecimal(value *decimal.Decimal) any {
	if value != nil {
		return value.String()
	}
	return nil
}
func dynamicDecimal(value query.Value) any {
	if value.Kind() == query.ValueNull {
		return nil
	}
	if number, valid := value.Decimal(); valid {
		return number.String()
	}
	panic("non-decimal fixture result")
}

type decimalSeed struct {
	rates   map[string]pricing.Rate
	entries map[string]ledger.Entry
}

func seedDecimal(t *testing.T, backend db.Mutator) decimalSeed {
	t.Helper()
	data, err := os.ReadFile("input.json")
	check(t, err)
	var input struct {
		Rates []struct {
			Code         string
			Factor, Flat *string
		}
		Entries []struct {
			Code, Bucket, Balance    string
			Enabled                  bool
			Amount, Fine, Wide, Rate *string
		}
		Adjustments []struct {
			Entry string
			Bonus *string
		}
	}
	check(t, json.Unmarshal(data, &input))
	seed := decimalSeed{rates: map[string]pricing.Rate{}, entries: map[string]ledger.Entry{}}
	for _, row := range input.Rates {
		value := pricing.NewRateCreate(row.Code)
		if row.Factor != nil {
			value = value.WithFactor(d(*row.Factor))
		}
		if row.Flat != nil {
			value = value.WithFlat(d(*row.Flat))
		}
		saved, err := pricing.RateObjects.Create(t.Context(), backend, value)
		check(t, err)
		seed.rates[row.Code] = saved
	}
	for _, row := range input.Entries {
		value := ledger.NewEntryCreate(row.Code, row.Bucket, row.Enabled, d(row.Balance))
		if row.Amount != nil {
			value = value.WithAmount(d(*row.Amount))
		}
		if row.Fine != nil {
			value = value.WithFine(d(*row.Fine))
		}
		if row.Wide != nil {
			value = value.WithWide(d(*row.Wide))
		}
		if row.Rate != nil {
			value = value.WithRateID(seed.rates[*row.Rate].ID)
		}
		saved, err := ledger.EntryObjects.Create(t.Context(), backend, value)
		check(t, err)
		seed.entries[row.Code] = saved
	}
	for _, row := range input.Adjustments {
		value := ledger.NewAdjustmentCreate(seed.entries[row.Entry].ID)
		if row.Bonus != nil {
			value = value.WithBonus(d(*row.Bonus))
		}
		_, err := ledger.AdjustmentObjects.Create(t.Context(), backend, value)
		check(t, err)
	}
	return seed
}

func decimalStorage(t *testing.T, backend db.Queryer) any {
	t.Helper()
	rates, err := pricing.RateObjects.Using(backend).OrderBy(pricing.RateFields.ID.Asc()).All(t.Context())
	check(t, err)
	entries, err := ledger.EntryObjects.Using(backend).OrderBy(ledger.EntryFields.ID.Asc()).All(t.Context())
	check(t, err)
	adjustments, err := ledger.AdjustmentObjects.Using(backend).OrderBy(ledger.AdjustmentFields.ID.Asc()).All(t.Context())
	check(t, err)
	return []any{rates, entries, adjustments}
}

func decimalProjection(value orm.DecimalExpression[ledger.Entry]) orm.Projection[ledger.Entry, map[string]any] {
	return orm.Project2(ledger.EntryFields.Code, value, func(code string, value orm.Optional[decimal.Decimal]) map[string]any {
		return map[string]any{"code": code, "value": optionalDecimal(value)}
	})
}

func observeDecimal(t *testing.T, reader db.Session, name string) (any, error) {
	t.Helper()
	relations, err := project.BindRelations()
	if err != nil {
		return nil, err
	}
	f := ledger.EntryFields
	base := ledger.EntryObjects.Using(reader).OrderBy(f.Code.Asc())
	amount := orm.Decimal(orm.F(f.Amount))
	cost := amount.Multiply(d("1.25")).Add(d("0.5"))
	rows := func(value orm.DecimalExpression[ledger.Entry]) (any, error) {
		return orm.SelectInto(t.Context(), base, decimalProjection(value))
	}
	summary := func(source orm.QuerySet[ledger.Entry]) (any, error) {
		return orm.AggregateInto(t.Context(), source,
			orm.Aggregate5(orm.Sum(cost), orm.Avg(cost), orm.Min(cost), orm.Max(cost), orm.Count(cost),
				func(total, average, minimum, maximum orm.Optional[decimal.Decimal], present int64) map[string]any {
					return map[string]any{"total": optionalDecimal(total), "average": optionalDecimal(average), "minimum": optionalDecimal(minimum), "maximum": optionalDecimal(maximum), "present": present}
				}))
	}
	update := func(assignments ...orm.UpdateAssignment[ledger.Entry]) (any, error) {
		count, err := base.Update(t.Context(), assignments...)
		if err != nil {
			return nil, err
		}
		entries, err := base.Fresh().All(t.Context())
		if err != nil {
			return nil, err
		}
		result := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			result = append(result, map[string]any{"code": entry.Code, "amount": pointerDecimal(entry.Amount), "balance": entry.Balance.String()})
		}
		return map[string]any{"count": count, "rows": result}, nil
	}
	switch name {
	case "add":
		return rows(amount.Add(d("0.25")))
	case "subtract":
		return rows(amount.SubtractExpression(orm.F(f.Balance)))
	case "multiply":
		return rows(cost)
	case "negate":
		return rows(amount.Negate())
	case "case_null":
		return rows(orm.Decimal(orm.Case(cost, orm.When(f.Amount.IsNull(true), orm.Value[ledger.Entry](d("7.5"))))))
	case "related_arithmetic":
		return rows(amount.MultiplyExpression(relations.LedgerEntry.Rate.Factor).AddExpression(relations.LedgerEntry.Rate.Flat))
	case "reverse_arithmetic":
		return rows(amount.AddExpression(relations.LedgerEntry.Adjustment.Bonus))
	case "comparison_rhs":
		return orm.SelectInto(t.Context(), base.Filter(orm.GreaterThanExpressions(amount.Add(d("1")), orm.Decimal(orm.F(f.Balance)).Multiply(d("2")))), orm.Project1(f.Code, func(code string) string { return code }))
	case "order_page":
		selected, err := orm.SelectQuery(base.Filter(cost.GreaterThanOrEqual(d("0"))).OrderBy(cost.Desc(), f.Code.Asc()), decimalProjection(cost))
		if err != nil {
			return nil, err
		}
		page, err := selected.Page(t.Context(), 2, 1)
		return page.Rows, err
	case "computed_aggregate":
		return summary(base)
	case "empty_aggregate":
		return summary(base.Filter(f.ID.In()))
	case "all_null_aggregate":
		return summary(base.Filter(f.Code.Exact("c")))
	case "distinct_filtered_aggregate":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate2(orm.Sum(cost).Distinct().Where(f.Enabled.Exact(true)), orm.Avg(cost).Distinct().Where(f.Enabled.Exact(true)),
			func(total, average orm.Optional[decimal.Decimal]) map[string]any {
				return map[string]any{"total": optionalDecimal(total), "average": optionalDecimal(average)}
			}))
	case "groups_having":
		total := orm.Sum(cost)
		grouped, err := orm.GroupBy(base.Filter(f.Enabled.Exact(true)).OrderBy(), orm.Project1(f.Bucket, func(value string) string { return value }),
			orm.Aggregate2(total, orm.Avg(cost), func(total, average orm.Optional[decimal.Decimal]) map[string]any {
				return map[string]any{"total": optionalDecimal(total), "average": optionalDecimal(average)}
			}),
			func(bucket string, values map[string]any) map[string]any { values["bucket"] = bucket; return values })
		if err != nil {
			return nil, err
		}
		return grouped.Having(total.GreaterThanOrEqual(orm.Some(d("1")))).OrderBy(f.Bucket.Asc()).All(t.Context())
	case "derived_aggregate":
		selected, err := orm.SelectQuery(base, decimalProjection(cost))
		if err != nil {
			return nil, err
		}
		stage, err := orm.Derive(selected)
		if err != nil {
			return nil, err
		}
		value := orm.Decimal(orm.OptionalOperand(orm.SelectedField(stage, cost)))
		aggregate, err := orm.AggregateResults(stage, orm.Aggregate2(orm.Sum(value), orm.Max(value),
			func(total, maximum orm.Optional[decimal.Decimal]) map[string]any {
				return map[string]any{"total": optionalDecimal(total), "maximum": optionalDecimal(maximum)}
			}))
		if err != nil {
			return nil, err
		}
		return aggregate.One(t.Context())
	case "update_add":
		return update(orm.AssignExpression(f.Amount, amount.Add(d("0.25"))))
	case "update_old_row":
		return update(orm.AssignExpression(f.Amount, orm.Decimal(orm.F(f.Balance)).Add(d("1"))), orm.AssignExpression(f.Balance, orm.Decimal(orm.F(f.Balance)).Multiply(d("2"))))
	case "update_null":
		return update(orm.AssignExpression(f.Amount, amount.AddExpression(orm.NullValue[ledger.Entry, decimal.Decimal]())))
	case "divide":
		return rows(amount.Divide(d("3")))
	case "divide_integer":
		base = base.Filter(f.Code.Exact("a"))
		return rows(orm.Decimal(orm.Value[ledger.Entry](d("100"))).Divide(d("3")))
	case "divide_decimal_spelling":
		base = base.Filter(f.Code.Exact("a"))
		return rows(orm.Decimal(orm.Value[ledger.Entry](d("100.0"))).Divide(d("3.0")))
	case "divide_native_scale":
		base = base.Filter(f.Code.Exact("a"))
		return rows(orm.Decimal(orm.F(f.Wide)).Divide(d("3")))
	case "remainder":
		return rows(amount.Remainder(d("0.3")))
	case "divide_zero", "remainder_zero":
		base = base.Filter(f.Code.Exact("a"))
		if name == "divide_zero" {
			return rows(amount.Divide(d("0")))
		}
		return rows(amount.Remainder(d("0")))
	case "large_literal":
		base = base.Filter(f.Code.Exact("a"))
		return rows(orm.Decimal(orm.Value[ledger.Entry](d("9007199254740993"))).Add(d("0.1")))
	case "update_rounding":
		return update(orm.AssignExpression(f.Amount, amount.Multiply(d("1.5"))))
	case "update_overflow":
		return update(orm.AssignExpression(f.Amount, amount.Multiply(d("1000000000000"))))
	case "case_unselected_zero":
		return rows(orm.Decimal(orm.Case(amount.Add(d("1")), orm.When(f.Code.Exact("never"), amount.Divide(d("0"))))))
	case "wide_product":
		base = base.Filter(f.Code.Exact("a"))
		wide := orm.Decimal(orm.F(f.Wide))
		square := wide.MultiplyExpression(wide)
		return rows(square.MultiplyExpression(square).MultiplyExpression(wide))
	default:
		return nil, fmt.Errorf("unowned decimal observation %q", name)
	}
}
