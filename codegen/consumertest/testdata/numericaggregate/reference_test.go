package consumer

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

var (
	_ orm.AggregateExpression[records.Measure, orm.Optional[int64]]             = orm.Sum(records.MeasureFields.Amount)
	_ orm.AggregateExpression[records.Measure, orm.Optional[float64]]           = orm.Avg(records.MeasureFields.Amount)
	_ orm.AggregateExpression[records.Measure, orm.Optional[float64]]           = orm.Sum(records.MeasureFields.Score)
	_ orm.AggregateExpression[records.Measure, orm.Optional[decimal.Decimal]]   = orm.Avg(records.MeasureFields.Price)
	_ orm.AggregateExpression[records.Measure, orm.Optional[duration.Duration]] = orm.Avg(records.MeasureFields.Elapsed)
)

type numericReference struct{ Cases []numericCase }
type numericCase struct {
	Name    string
	Input   []map[string]any
	Actions []numericAction
}
type numericAction struct {
	Name   string
	Result any
	Error  map[string]any
	SQL    []string
}
type numericCanonical struct{ Kind, Text string }

func readNumericReference(t *testing.T, backend string) numericReference {
	t.Helper()
	raw, err := os.ReadFile("django61-" + backend + ".json")
	check(t, err)
	var value numericReference
	check(t, json.Unmarshal(raw, &value))
	raw, err = os.ReadFile("django61-duration-" + backend + ".json")
	check(t, err)
	var boundaries numericReference
	check(t, json.Unmarshal(raw, &boundaries))
	for _, record := range boundaries.Cases {
		record.Name = "boundary_" + record.Name
		value.Cases = append(value.Cases, record)
	}
	return value
}

func TestNumericReference(t *testing.T) {
	withNumericBackends(t, func(t *testing.T, backend numericBackend, vendor string, _ numericPhysicalWriter) {
		fixture := readNumericReference(t, vendor)
		postgres := readNumericReference(t, "postgres")
		var previous []records.Measure
		for caseIndex, record := range fixture.Cases {
			t.Run(record.Name, func(t *testing.T) {
				for _, old := range previous {
					_, err := records.MeasureObjects.Delete(t.Context(), backend, &old)
					check(t, err)
				}
				for _, input := range record.Input {
					createNumericInput(t, backend, input)
				}
				query := records.MeasureObjects.Using(backend).OrderBy(records.MeasureFields.ID.Asc())
				before, err := query.All(t.Context())
				check(t, err)
				if len(before) != len(record.Input) {
					t.Fatal("numeric input cardinality")
				}
				for actionIndex, action := range record.Actions {
					t.Run(action.Name, func(t *testing.T) {
						_, field, variant := splitNumericAction(t, action.Name)
						want := action
						// Exact SQLite BLOB arithmetic deliberately follows the
						// independent native NUMERIC result, not affinity loss in
						// Django's SQLite Decimal storage. Raw bytes stay unchanged.
						if vendor == "sqlite" && strings.Contains(field, "price") {
							other := postgres.Cases[caseIndex]
							if other.Name != record.Name || other.Actions[actionIndex].Name != action.Name {
								t.Fatal("reference alignment")
							}
							want = other.Actions[actionIndex]
							want.Result = numericReferenceOrder(t, action.Result, want.Result)
						}
						actual, failure := evaluateNumericAction(t, backend, action.Name)
						expected, domainFailure := canonicalNumeric(want.Result)
						// A native arbitrary Python int or Decimal outside GoDj's
						// explicit result domain must fail, never wrap or coerce.
						if want.Error != nil || domainFailure != nil {
							if failure == nil {
								t.Fatalf("native/domain failure became result: %#v", actual)
							}
							return
						}
						check(t, failure)
						got, err := canonicalNumeric(actual)
						check(t, err)
						if !reflect.DeepEqual(got, expected) {
							t.Fatalf("numeric %s: %#v want %#v", action.Name, got, expected)
						}
						if variant == "folded_empty" && len(action.SQL) != 0 {
							t.Fatal("independent folded empty unexpectedly performed I/O")
						}
					})
				}
				after, err := query.Fresh().All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("numeric reads changed stored input")
				}
				previous = after
			})
		}
	})
}

// Keep the backend's independently observed key ordering while taking exact
// Decimal values from native NUMERIC. In particular, default ASC NULL ordering
// differs between SQLite and PostgreSQL.
func numericReferenceOrder(t *testing.T, nativeOrder, exactValues any) any {
	t.Helper()
	order, ordered := nativeOrder.([]any)
	values, grouped := exactValues.([]any)
	if !ordered || !grouped {
		return exactValues
	}
	if len(order) != len(values) {
		t.Fatal("independent numeric group cardinality differs")
	}
	byKey := make(map[any]any, len(values))
	for _, row := range values {
		key := row.(map[string]any)["group_key"]
		if _, duplicate := byKey[key]; duplicate {
			t.Fatal("independent numeric group repeated")
		}
		byKey[key] = row
	}
	result := make([]any, len(order))
	for index, row := range order {
		key := row.(map[string]any)["group_key"]
		value, present := byKey[key]
		if !present {
			t.Fatal("independent numeric group key differs")
		}
		result[index] = value
		delete(byKey, key)
	}
	return result
}

func createNumericInput(t *testing.T, backend numericBackend, row map[string]any) records.Measure {
	t.Helper()
	enabled := true
	if value, present := row["enabled"]; present {
		enabled = value.(bool)
	}
	input := records.NewMeasureCreate(enabled)
	for field, raw := range row {
		if raw == nil || field == "enabled" {
			continue
		}
		if field == "group_key" {
			input = input.WithGroupKey(raw.(string))
			continue
		}
		value := raw.(map[string]any)
		switch field {
		case "amount":
			number, err := strconv.ParseInt(value["text"].(string), 10, 64)
			check(t, err)
			input = input.WithAmount(number)
		case "score":
			number, err := numericFloat(value["text"].(string))
			check(t, err)
			input = input.WithScore(number)
		case "elapsed":
			number, err := strconv.ParseInt(value["microseconds"].(string), 10, 64)
			check(t, err)
			input = input.WithElapsed(duration.FromMicroseconds(number))
		case "price", "precise_price", "wide_price", "full_price", "full_whole_price":
			number, err := decimal.Parse(value["text"].(string))
			check(t, err)
			switch field {
			case "price":
				input = input.WithPrice(number)
			case "precise_price":
				input = input.WithPrecisePrice(number)
			case "wide_price":
				input = input.WithWidePrice(number)
			case "full_price":
				input = input.WithFullPrice(number)
			case "full_whole_price":
				input = input.WithFullWholePrice(number)
			}
		default:
			t.Fatal("unknown numeric input field", field)
		}
	}
	value, err := records.MeasureObjects.Create(t.Context(), backend, input)
	check(t, err)
	return value
}

func splitNumericAction(t *testing.T, name string) (kind, field, variant string) {
	t.Helper()
	if strings.HasPrefix(name, "Sum_") {
		kind = "sum"
		name = strings.TrimPrefix(name, "Sum_")
	} else if strings.HasPrefix(name, "Avg_") {
		kind = "avg"
		name = strings.TrimPrefix(name, "Avg_")
	} else {
		t.Fatal("unknown numeric action", name)
	}
	for _, suffix := range []string{"plain", "distinct", "filtered", "filter_empty", "native_empty", "folded_empty", "groups", "having_null", "having_gte_1", "having_gte_2", "having_lte_1", "having_lte_2"} {
		if value, found := strings.CutSuffix(name, "_"+suffix); found {
			return kind, value, suffix
		}
	}
	t.Fatal("unknown numeric action variant", name)
	return
}

func evaluateNumericAction(t *testing.T, backend numericBackend, name string) (any, error) {
	kind, field, variant := splitNumericAction(t, name)
	source := records.MeasureObjects.Using(backend)
	f := records.MeasureFields
	switch field {
	case "amount":
		if kind == "sum" {
			return evaluateNumeric(t, source, orm.Sum(f.Amount), variant)
		}
		return evaluateNumeric(t, source, orm.Avg(f.Amount), variant)
	case "score":
		if kind == "sum" {
			return evaluateNumeric(t, source, orm.Sum(f.Score), variant)
		}
		return evaluateNumeric(t, source, orm.Avg(f.Score), variant)
	case "price":
		if kind == "sum" {
			return evaluateNumeric(t, source, orm.Sum(f.Price), variant)
		}
		return evaluateNumeric(t, source, orm.Avg(f.Price), variant)
	case "elapsed":
		if kind == "sum" {
			return evaluateNumeric(t, source, orm.Sum(f.Elapsed), variant)
		}
		return evaluateNumeric(t, source, orm.Avg(f.Elapsed), variant)
	case "precise_price":
		if kind == "sum" {
			return evaluateNumeric(t, source, orm.Sum(f.PrecisePrice), variant)
		}
		return evaluateNumeric(t, source, orm.Avg(f.PrecisePrice), variant)
	case "wide_price":
		if kind == "sum" {
			return evaluateNumeric(t, source, orm.Sum(f.WidePrice), variant)
		}
		return evaluateNumeric(t, source, orm.Avg(f.WidePrice), variant)
	case "full_price":
		if kind == "sum" {
			return evaluateNumeric(t, source, orm.Sum(f.FullPrice), variant)
		}
		return evaluateNumeric(t, source, orm.Avg(f.FullPrice), variant)
	case "full_whole_price":
		if kind == "sum" {
			return evaluateNumeric(t, source, orm.Sum(f.FullWholePrice), variant)
		}
		return evaluateNumeric(t, source, orm.Avg(f.FullWholePrice), variant)
	default:
		t.Fatal("unknown numeric field", field)
		return nil, nil
	}
}

func nullableNumeric[V any](value orm.Optional[V]) any {
	if raw, ok := value.Get(); ok {
		return raw
	}
	return nil
}

func evaluateNumeric[V any](t *testing.T, source orm.QuerySet[records.Measure], expression orm.AggregateExpression[records.Measure, orm.Optional[V]], variant string) (any, error) {
	f := records.MeasureFields
	switch variant {
	case "distinct":
		expression = expression.Distinct()
	case "filtered":
		expression = expression.Where(f.Enabled.Exact(true))
	case "filter_empty":
		expression = expression.Where(f.ID.LessThan(0))
	case "native_empty":
		source = source.Filter(f.ID.LessThan(0))
	case "folded_empty":
		source = source.Filter(f.ID.In())
	}
	keys := orm.Project1(f.GroupKey, func(value *string) *string { return value })
	if variant == "groups" {
		grouped, err := orm.GroupBy(source, keys, orm.Aggregate3(expression, expression.Where(f.Enabled.Exact(true)), expression.Distinct(), func(a, b, c orm.Optional[V]) [3]orm.Optional[V] { return [3]orm.Optional[V]{a, b, c} }), func(key *string, values [3]orm.Optional[V]) map[string]any {
			var k any
			if key != nil {
				k = *key
			}
			return map[string]any{"group_key": k, "total": nullableNumeric(values[0]), "opened": nullableNumeric(values[1]), "unique": nullableNumeric(values[2])}
		})
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(orm.GroupKey(f.GroupKey).Asc()).All(t.Context())
	}
	if strings.HasPrefix(variant, "having_") {
		grouped, err := orm.GroupBy(source, keys, orm.Aggregate1(expression, func(value orm.Optional[V]) orm.Optional[V] { return value }), func(key *string, value orm.Optional[V]) map[string]any {
			var k any
			if key != nil {
				k = *key
			}
			return map[string]any{"group_key": k, "total": nullableNumeric(value)}
		})
		if err != nil {
			return nil, err
		}
		if variant == "having_null" {
			grouped = grouped.Having(expression.IsNull(true))
		} else {
			parts := strings.Split(variant, "_")
			bound, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil {
				return nil, err
			}
			lookup := query.LookupGreaterThanOrEqual
			if parts[1] == "lte" {
				lookup = query.LookupLessThanOrEqual
			}
			grouped = grouped.HavingDynamic(orm.GroupLookupInput{Column: 1, Lookup: lookup, Value: duration.FromMicroseconds(bound)})
		}
		return grouped.OrderBy(orm.GroupKey(f.GroupKey).Asc()).All(t.Context())
	}
	value, err := orm.AggregateInto(t.Context(), source, orm.Aggregate1(expression, func(value orm.Optional[V]) orm.Optional[V] { return value }))
	if err != nil {
		return nil, err
	}
	return map[string]any{"value": nullableNumeric(value)}, nil
}

func numericFloat(text string) (float64, error) {
	switch strings.ToLower(text) {
	case "inf":
		return math.Inf(1), nil
	case "-inf":
		return math.Inf(-1), nil
	case "nan":
		return math.NaN(), nil
	}
	return strconv.ParseFloat(text, 64)
}

func canonicalNumeric(value any) (any, error) {
	switch raw := value.(type) {
	case nil, string, bool:
		return raw, nil
	case int64:
		return numericCanonical{"int", strconv.FormatInt(raw, 10)}, nil
	case float64:
		if math.IsNaN(raw) {
			return numericCanonical{"float", "NaN"}, nil
		}
		return numericCanonical{"float", fmt.Sprintf("%016x", math.Float64bits(raw))}, nil
	case decimal.Decimal:
		canonical, err := raw.Canonical()
		if err != nil {
			return nil, err
		}
		return numericCanonical{"Decimal", canonical.String()}, nil
	case duration.Duration:
		if !raw.Valid() {
			return nil, duration.ErrRange
		}
		total := big.NewInt(int64(raw.Days))
		total.Mul(total, big.NewInt(duration.MicrosecondsPerDay))
		total.Add(total, big.NewInt(raw.Microseconds))
		return numericCanonical{"timedelta", total.String()}, nil
	case map[string]any:
		if kind, ok := raw["type"].(string); ok {
			switch kind {
			case "int":
				value, err := strconv.ParseInt(raw["text"].(string), 10, 64)
				if err != nil {
					return nil, err
				}
				return canonicalNumeric(value)
			case "float":
				value, err := numericFloat(raw["text"].(string))
				if err != nil {
					return nil, err
				}
				return canonicalNumeric(value)
			case "Decimal":
				value, err := decimal.Parse(raw["text"].(string))
				if err != nil {
					return nil, err
				}
				return canonicalNumeric(value)
			case "timedelta":
				text := raw["microseconds"].(string)
				value, ok := new(big.Int).SetString(text, 10)
				if !ok {
					return nil, fmt.Errorf("invalid reference duration")
				}
				var days, remainder big.Int
				days.DivMod(value, big.NewInt(duration.MicrosecondsPerDay), &remainder)
				if !days.IsInt64() {
					return nil, duration.ErrRange
				}
				elapsed, err := duration.New(days.Int64(), remainder.Int64())
				if err != nil {
					return nil, err
				}
				return canonicalNumeric(elapsed)
			default:
				return nil, fmt.Errorf("unknown numeric reference type %s", kind)
			}
		}
		result := make(map[string]any, len(raw))
		for key, value := range raw {
			canonical, err := canonicalNumeric(value)
			if err != nil {
				return nil, err
			}
			result[key] = canonical
		}
		return result, nil
	case []any:
		result := make([]any, len(raw))
		for index, value := range raw {
			canonical, err := canonicalNumeric(value)
			if err != nil {
				return nil, err
			}
			result[index] = canonical
		}
		return result, nil
	case []map[string]any:
		result := make([]any, len(raw))
		for index, value := range raw {
			canonical, err := canonicalNumeric(value)
			if err != nil {
				return nil, err
			}
			result[index] = canonical
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unknown numeric result type %T", value)
	}
}
