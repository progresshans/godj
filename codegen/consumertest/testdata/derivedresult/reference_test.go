package consumer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func readDerivedReference(t *testing.T, vendor string) computedReference {
	t.Helper()
	data, err := os.ReadFile("django61-" + vendor + ".json")
	check(t, err)
	var fixture computedReference
	check(t, json.Unmarshal(data, &fixture))
	return fixture
}

func TestDerivedReference(t *testing.T) {
	withComputedBackends(t, func(t *testing.T, backend computedBackend, vendor string, _ computedPhysicalWriter) {
		fixture := readDerivedReference(t, vendor)
		selected := map[string]bool{
			"annotate_after_slice": true, "filter_after_slice": true, "order_after_slice": true,
			"aggregate_annotation_alias": true, "aggregate_sliced_annotation_alias": true,
			"group_then_computed_aggregate": true, "group_then_computed_having": true,
			"computed_key_then_group": true, "group_key_retained_when_hidden": true,
		}
		seen := 0
		for _, record := range fixture.Cases {
			if !selected[record.Name] {
				continue
			}
			seen++
			t.Run(record.Name, func(t *testing.T) {
				var actual any
				var failure error
				rollback := errors.New("derived reference owned rollback")
				err := backend.Atomic(t.Context(), func(session db.Session) error {
					seedComputed(t, session, fixture.Input)
					before, err := records.MeasureObjects.Using(session).OrderBy(records.MeasureFields.ID.Asc()).All(t.Context())
					check(t, err)
					actual, failure = runDerivedReference(t, session, record.Name)
					after, err := records.MeasureObjects.Using(session).OrderBy(records.MeasureFields.ID.Asc()).All(t.Context())
					check(t, err)
					if !reflect.DeepEqual(before, after) {
						t.Fatal("derived reference changed complete rows")
					}
					return rollback
				})
				if !errors.Is(err, rollback) {
					t.Fatal("reference did not confirm rollback", err)
				}
				count, err := records.MeasureObjects.Using(backend).Count(t.Context())
				check(t, err)
				if count != 0 {
					t.Fatal("reference leaked rows outside its transaction")
				}
				if record.Error != nil {
					if record.Error.Type != "TypeError" || failure == nil || actual != nil {
						t.Fatal("sliced-query rejection differs", actual, failure, record.Error)
					}
					return
				}
				check(t, failure)
				got, err := canonicalComputed(actual)
				check(t, err)
				want, err := canonicalComputed(record.Result)
				check(t, err)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("derived reference differs: %#v\nwant %#v", got, want)
				}
			})
		}
		if seen != len(selected) {
			t.Fatal("reference subset was not fully evaluated", seen)
		}
	})
}

func optionalInteger(value orm.Optional[int64]) query.Value {
	if n, valid := value.Get(); valid {
		return query.Integer(n)
	}
	return query.Null()
}
func nullableString(value *string) query.Value {
	if value != nil {
		return query.String(*value)
	}
	return query.Null()
}

func runDerivedReference(t *testing.T, backend db.Session, name string) (any, error) {
	api, err := project.UsingSession(backend)
	if err != nil {
		return nil, err
	}
	f := records.MeasureFields
	ctx := t.Context()
	switch name {
	case "aggregate_annotation_alias", "aggregate_sliced_annotation_alias":
		adjusted, doubled := orm.Add(orm.F(f.Amount), int64(1)), orm.Multiply(orm.F(f.Amount), int64(2))
		source, err := project.SelectRecordsMeasureQuery(api.RecordsMeasure.OrderBy(f.ID.Asc()), orm.Project2(adjusted, doubled, func(a, b orm.Optional[int64]) [2]orm.Optional[int64] { return [2]orm.Optional[int64]{a, b} }))
		if err != nil {
			return nil, err
		}
		if name == "aggregate_sliced_annotation_alias" {
			source, err = source.Limit(2)
			if err != nil {
				return nil, err
			}
		}
		stage, err := orm.Derive(source)
		if err != nil {
			return nil, err
		}
		value := orm.OptionalOperand(orm.SelectedField(stage, adjusted))
		result, err := orm.AggregateResults(stage, orm.Aggregate1(orm.Sum(orm.Numeric(value)), func(value orm.Optional[int64]) map[string]any { return map[string]any{"value": optionalInteger(value)} }))
		if err != nil {
			return nil, err
		}
		return result.One(ctx)
	case "annotate_after_slice", "filter_after_slice", "order_after_slice":
		source, err := project.SelectRecordsMeasureQuery(api.RecordsMeasure.OrderBy(f.ID.Asc()), orm.Project2(f.Code, f.Amount, func(code string, n int64) int64 { return n }))
		if err != nil {
			return nil, err
		}
		source, err = source.Limit(2)
		if err != nil {
			return nil, err
		}
		if name == "filter_after_slice" {
			_, err = source.Filter(f.Amount.GreaterThan(0)).All(ctx)
			return nil, err
		}
		if name == "order_after_slice" {
			_, err = source.OrderBy(f.Amount.Desc()).All(ctx)
			return nil, err
		}
		stage, err := orm.Derive(source)
		if err != nil {
			return nil, err
		}
		code, amount := orm.SelectedField(stage, f.Code), orm.SelectedField(stage, f.Amount)
		result, err := orm.SelectResults(stage, orm.Project2(code, orm.Add(amount, int64(1)), func(code string, adjusted orm.Optional[int64]) any {
			return map[string]any{"code": code, "adjusted": optionalInteger(adjusted)}
		}))
		if err != nil {
			return nil, err
		}
		return result.OrderBy(code.Asc()).All(ctx)
	case "group_then_computed_aggregate", "group_then_computed_having", "group_key_retained_when_hidden":
		sum := orm.Sum(f.Amount)
		grouped, err := project.GroupRecordsMeasureBy(api.RecordsMeasure, orm.Project1(f.GroupKey, func(key *string) *string { return key }),
			orm.Aggregate1(sum, func(value orm.Optional[int64]) orm.Optional[int64] { return value }), func(_ *string, value orm.Optional[int64]) orm.Optional[int64] { return value })
		if err != nil {
			return nil, err
		}
		stage, err := orm.Derive(grouped)
		if err != nil {
			return nil, err
		}
		key, total := orm.SelectedField(stage, f.GroupKey), orm.SelectedAggregate(stage, sum)
		doubled := orm.Multiply(orm.OptionalOperand(total), int64(2))
		if name == "group_key_retained_when_hidden" {
			result, err := orm.SelectResults(stage, orm.Project1(total, func(total orm.Optional[int64]) any { return map[string]any{"total": optionalInteger(total)} }))
			if err != nil {
				return nil, err
			}
			return result.OrderBy(key.Asc()).All(ctx)
		}
		result, err := orm.SelectResults(stage, orm.Project3(key, total, doubled, func(key *string, total, doubled orm.Optional[int64]) any {
			return map[string]any{"group_key": nullableString(key), "total": optionalInteger(total), "doubled": optionalInteger(doubled)}
		}))
		if err != nil {
			return nil, err
		}
		if name == "group_then_computed_having" {
			result = result.Filter(doubled.GreaterThanOrEqual(6))
		}
		return result.OrderBy(key.Asc()).All(ctx)
	case "computed_key_then_group":
		key := orm.Remainder(orm.F(f.Amount), int64(2))
		input, err := project.SelectRecordsMeasureQuery(api.RecordsMeasure, orm.Project1(key, func(n orm.Optional[int64]) orm.Optional[int64] { return n }))
		if err != nil {
			return nil, err
		}
		stage, err := orm.Derive(input)
		if err != nil {
			return nil, err
		}
		selected := orm.SelectedField(stage, key)
		groups, err := orm.GroupResults(stage, orm.Project1(selected, func(n orm.Optional[int64]) orm.Optional[int64] { return n }),
			orm.Aggregate1(orm.CountRows[orm.ResultOf[records.Measure]](), func(n int64) int64 { return n }), func(key orm.Optional[int64], total int64) any {
				return map[string]any{"key": optionalInteger(key), "total": query.Integer(total)}
			})
		if err != nil {
			return nil, err
		}
		return groups.OrderBy(selected.Asc()).All(ctx)
	default:
		return nil, fmt.Errorf("unowned derived reference case %s", name)
	}
}
