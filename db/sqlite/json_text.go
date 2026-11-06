package sqlite

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/jsonvalue"
	modernsqlite "modernc.org/sqlite"
)

var jsonTextRegistrationError = modernsqlite.RegisterDeterministicScalarFunction("godj_json_icontains", 3, sqliteJSONIContains)

// Whole fields use their stored text. Paths use the decoded string or exact
// JSON subtree text: numbers do not pass through SQLite's binary64 conversion.
// Literal substring matching also preserves NUL suffixes; %, _ and backslash
// never become pattern operators. Case folding follows SQLite's ASCII scope.
func sqliteJSONIContains(_ *modernsqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	invalid := errors.New("invalid SQLite JSON text lookup arguments")
	if len(args) != 3 {
		return nil, invalid
	}
	needle, ok := args[1].([]byte)
	if !ok || !utf8.Valid(needle) || len(needle) > jsonvalue.MaxStringBytes {
		return nil, invalid
	}
	mode, ok := args[2].(int64)
	if !ok || mode < 0 || mode > 1 {
		return nil, invalid
	}
	if args[0] == nil {
		return nil, nil
	}
	text, ok := args[0].(string)
	if !ok || !utf8.ValidString(text) || len(text) > jsonvalue.MaxDocumentBytes {
		return nil, invalid
	}
	if mode == 1 {
		decoded, err := (jsonvalue.Value{Text: text}).Decode()
		if err != nil {
			return nil, err
		}
		if value, ok := decoded.(string); ok {
			text = value
		} else {
			raw, err := json.Marshal(decoded)
			if err != nil {
				return nil, err
			}
			text = string(raw)
		}
	}
	if strings.Contains(asciiLower(text), asciiLower(string(needle))) {
		return int64(1), nil
	}
	return int64(0), nil
}

func asciiLower(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, text)
}
