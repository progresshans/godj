package postgres

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
)

// jsonb stores numbers as exact PostgreSQL numerics, expanding exponents on
// read. Bound that representation before sending a write or comparison so a
// successful GoDj write cannot manufacture a value its scanner cannot read.
// json.RawMessage deliberately bypasses pgx's ordinary Go-struct JSON encoder.
func postgresJSONValue(value jsonvalue.Value) (any, error) {
	decoded, err := value.Decode()
	if err != nil {
		return nil, err
	}
	if err := measureJSONB(wirejson.NewSizer(jsonvalue.MaxDocumentBytes), decoded); err != nil {
		return nil, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue,
			Detail: "JSON exceeds PostgreSQL jsonb Unicode or round-trip size limits", Cause: err}
	}
	return json.RawMessage(value.Bytes()), nil
}

func measureJSONB(size *wirejson.Sizer, value any) error {
	valid := false
	switch value := value.(type) {
	case nil:
		valid = size.Literal("null")
	case bool:
		valid = size.Boolean(value)
	case string:
		if strings.ContainsRune(value, '\x00') {
			return jsonvalue.ErrInvalid
		}
		valid = size.String(value)
	case json.Number:
		length, ok := jsonbNumberLength(value.String())
		valid = ok && size.Add(length)
	case []any:
		if !size.Add(2) {
			return jsonvalue.ErrLimit
		}
		for index, child := range value {
			// Native jsonb emits a space after each separator.
			if index > 0 && !size.Add(2) {
				return jsonvalue.ErrLimit
			}
			if err := measureJSONB(size, child); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		if !size.Add(2) {
			return jsonvalue.ErrLimit
		}
		index := 0
		for key, child := range value {
			if strings.ContainsRune(key, '\x00') {
				return jsonvalue.ErrInvalid
			}
			if index > 0 && !size.Add(2) || !size.String(key) || !size.Add(2) {
				return jsonvalue.ErrLimit
			}
			if err := measureJSONB(size, child); err != nil {
				return err
			}
			index++
		}
		return nil
	default:
		return jsonvalue.ErrInvalid
	}
	if !valid {
		return jsonvalue.ErrLimit
	}
	return nil
}

// The caller has already validated JSON number grammar. Compute expanded
// numeric length without allocating exponent-sized strings or rounding digits.
func jsonbNumberLength(token string) (int, bool) {
	negative := strings.HasPrefix(token, "-")
	if negative {
		token = token[1:]
	}
	mantissa := token
	var exponent int64
	if index := strings.IndexAny(token, "eE"); index >= 0 {
		mantissa = token[:index]
		parsed, err := strconv.ParseInt(token[index+1:], 10, 32)
		if err != nil {
			return 0, false
		}
		exponent = parsed
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := whole + fraction
	first := strings.IndexFunc(digits, func(digit rune) bool { return digit != '0' })
	integerDigits := int64(1)
	if first >= 0 {
		integerDigits = max(1, int64(len(whole))-int64(first)+exponent)
	}
	scale := max(0, int64(len(fraction))-exponent)
	length := integerDigits
	if scale > 0 {
		length += 1 + scale
	}
	if negative && first >= 0 {
		length++
	}
	if length > jsonvalue.MaxNumberBytes {
		return 0, false
	}
	return int(length), true
}
