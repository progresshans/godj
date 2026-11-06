package consumer

import (
	"fmt"
	"math"
	"strconv"
	"testing"

	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

type computedReference struct {
	Input []map[string]any
	Cases []struct {
		Name   string
		Result any
		Error  *struct {
			Type, Message string
			SQLState      *string `json:"sqlstate"`
		}
	}
}

func seedComputed(t *testing.T, backend db.Mutator, inputs []map[string]any) {
	t.Helper()
	for _, input := range inputs {
		integer := func(raw any) int64 {
			value, err := strconv.ParseInt(raw.(map[string]any)["text"].(string), 10, 64)
			check(t, err)
			return value
		}
		create := records.NewMeasureCreate(input["code"].(string), input["enabled"].(bool), integer(input["amount"]))
		if value := input["group_key"]; value != nil {
			create = create.WithGroupKey(value.(string))
		}
		if value := input["other"]; value != nil {
			create = create.WithOther(integer(value))
		}
		if value := input["score"]; value != nil {
			number, err := strconv.ParseFloat(value.(map[string]any)["text"].(string), 64)
			check(t, err)
			create = create.WithScore(number)
		}
		if value := input["price"]; value != nil {
			number, err := decimal.Parse(value.(map[string]any)["text"].(string))
			check(t, err)
			create = create.WithPrice(number)
		}
		if value := input["elapsed"]; value != nil {
			micros, err := strconv.ParseInt(value.(map[string]any)["microseconds"].(string), 10, 64)
			check(t, err)
			create = create.WithElapsed(duration.FromMicroseconds(micros))
		}
		_, err := records.MeasureObjects.Create(t.Context(), backend, create)
		check(t, err)
	}
}

func canonicalComputed(raw any) (any, error) {
	if value, ok := raw.(query.Value); ok {
		switch value.Kind() {
		case query.ValueNull:
			return nil, nil
		case query.ValueInteger:
			n, _ := value.Integer()
			return map[string]any{"type": "int", "text": strconv.FormatInt(n, 10)}, nil
		case query.ValueFloat:
			n, _ := value.Float()
			return computedFloat(n), nil
		case query.ValueString:
			s, _ := value.String()
			return s, nil
		case query.ValueBoolean:
			b, _ := value.Boolean()
			return b, nil
		case query.ValueDecimal:
			n, _ := value.Decimal()
			return map[string]any{"type": "Decimal", "text": n.String()}, nil
		case query.ValueDuration:
			n, _ := value.Duration()
			micros, err := n.TotalMicroseconds()
			return map[string]any{"type": "timedelta", "microseconds": strconv.FormatInt(micros, 10)}, err
		default:
			return nil, fmt.Errorf("unowned computed scalar kind %s", value.Kind())
		}
	}
	switch value := raw.(type) {
	case nil, string, bool:
		return raw, nil
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			v, err := canonicalComputed(child)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case map[string]any:
		if kind, ok := value["type"].(string); ok {
			switch kind {
			case "float":
				n, err := strconv.ParseFloat(value["text"].(string), 64)
				if err != nil {
					return nil, err
				}
				return computedFloat(n), nil
			case "Decimal":
				n, err := decimal.Parse(value["text"].(string))
				if err != nil {
					return nil, err
				}
				return map[string]any{"type": kind, "text": n.String()}, nil
			case "int", "timedelta":
				return value, nil
			}
		}
		out := map[string]any{}
		for key, child := range value {
			v, err := canonicalComputed(child)
			if err != nil {
				return nil, err
			}
			out[key] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unowned computed result %T", raw)
	}
}
func computedFloat(value float64) any {
	text := strconv.FormatUint(math.Float64bits(value), 16)
	if math.IsNaN(value) {
		text = "NaN"
	}
	return map[string]any{"type": "float", "bits": text}
}
