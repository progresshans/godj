package sqlite

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math/big"
	"strings"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

var jsonComparisonRegistrationError = modernsqlite.RegisterDeterministicScalarFunction("godj_json_path_cmp", 2, sqliteJSONPathCompare)

func isJSONPathComparison(condition query.Condition) bool {
	_, path := condition.JSONPath()
	if !path || condition.Field().Kind() != query.FieldJSON {
		return false
	}
	switch condition.Lookup() {
	case query.LookupGreaterThan, query.LookupGreaterThanOrEqual, query.LookupLessThan, query.LookupLessThanOrEqual:
		return true
	default:
		return false
	}
}

// Django's SQLite key ranges bind ordinary scalar operands. A Boolean RHS
// binds as 0/1; an explicit JSONNull expression binds the text "null". Arrays
// and objects cannot bind as scalar operands. Validate this before empty-query
// elision as well as inside the function, without coercing arbitrary input.
func jsonComparisonRight(value jsonvalue.Value) (any, error) {
	decoded, err := value.Decode()
	if err != nil {
		return nil, err
	}
	switch value := decoded.(type) {
	case nil:
		return "null", nil
	case bool:
		if value {
			return json.Number("1"), nil
		}
		return json.Number("0"), nil
	case string, json.Number:
		return value, nil
	default:
		return nil, errors.New("SQLite JSON path range operand is not scalar")
	}
}

// Compare the extracted JSON using SQLite's numeric-before-text classes.
// JSON null/Booleans on the left are text, matching Django's JSON_TYPE branch;
// strings stay strings. Exact numbers never pass through binary64. SQL NULL
// and a missing path remain unknown, independently of JSON null.
func sqliteJSONPathCompare(_ *modernsqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	invalid := errors.New("invalid SQLite JSON comparison arguments")
	if len(args) != 2 {
		return nil, invalid
	}
	raw, ok := args[1].(string)
	if !ok {
		return nil, invalid
	}
	right, err := jsonComparisonRight(jsonvalue.Value{Text: raw})
	if err != nil {
		return nil, err
	}
	if args[0] == nil {
		return nil, nil
	}
	raw, ok = args[0].(string)
	if !ok {
		return nil, invalid
	}
	left, err := (jsonvalue.Value{Text: raw}).Decode()
	if err != nil {
		return nil, err
	}
	switch value := left.(type) {
	case json.Number:
		if number, ok := right.(json.Number); ok {
			result, err := compareJSONNumbers(value.String(), number.String())
			if err != nil {
				return nil, err
			}
			return int64(result), nil
		}
		return int64(-1), nil
	case nil, bool, []any, map[string]any:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		left = string(encoded)
	}
	text, ok := left.(string)
	if !ok {
		return nil, invalid
	}
	if _, numeric := right.(json.Number); numeric {
		return int64(1), nil
	}
	return int64(strings.Compare(text, right.(string))), nil
}

// Tokens have already passed the bounded JSON codec. Compare scientific
// magnitudes and padded coefficient digits, allocating by token size only.
// Even an exponent with thousands of digits never expands into decimal zeros.
func compareJSONNumbers(left, right string) (int, error) {

	l, lm, ls, err := jsonNumberParts(left)
	if err != nil {
		return 0, err
	}
	r, rm, rs, err := jsonNumberParts(right)
	if err != nil {
		return 0, err
	}
	if ls < rs {
		return -1, nil
	}
	if ls > rs {
		return 1, nil
	}
	if ls == 0 {
		return 0, nil
	}
	if magnitude := lm.Cmp(rm); magnitude != 0 {
		return ls * magnitude, nil
	}
	for i := 0; i < max(len(l), len(r)); i++ {
		a, b := byte('0'), byte('0')
		if i < len(l) {
			a = l[i]
		}
		if i < len(r) {
			b = r[i]
		}
		if a < b {
			return -ls, nil
		}
		if a > b {
			return ls, nil
		}
	}
	return 0, nil
}

func jsonNumberParts(raw string) (string, *big.Int, int, error) {
	sign := 1
	if strings.HasPrefix(raw, "-") {
		sign = -1
		raw = raw[1:]
	}
	mantissa := raw
	exponent := new(big.Int)
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		mantissa = raw[:index]
		if _, ok := exponent.SetString(raw[index+1:], 10); !ok {
			return "", nil, 0, jsonvalue.ErrInvalid
		}
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return "", exponent, 0, nil
	}
	exponent.Add(exponent, big.NewInt(int64(len(digits)-len(fraction))))
	return strings.TrimRight(digits, "0"), exponent, sign, nil
}
