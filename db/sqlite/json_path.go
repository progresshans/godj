package sqlite

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

// Register once during package initialization, before connections are opened.
// The driver owns this stateless deterministic function; no query/document
// state is cached globally. Open returns registration failures without panic.
var jsonPathRegistrationError = modernsqlite.RegisterDeterministicScalarFunction("godj_json_at", 2, sqliteJSONAt)

func sqliteJSONPathArgument(path query.JSONPath) string {
	segments := path.Segments()
	values := make([]any, len(segments))
	for i, segment := range segments {
		if key, ok := segment.Key(); ok {
			values[i] = key
		} else {
			values[i], _ = segment.Index()
		}
	}
	data, _ := json.Marshal(values) // Validated strings and integers only.
	return string(data)
}

// SQLite's native -> compares NUL-containing object keys as prefixes (even an
// empty key can match a NUL key). Evaluate the bounded document inside SQLite
// instead, retaining exact numbers, arbitrary Unicode keys and JSON null.
func sqliteJSONAt(_ *modernsqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	invalid := errors.New("invalid SQLite JSON path arguments")
	if len(args) != 2 {
		return nil, invalid
	}
	encoded, ok := args[1].(string)
	if !ok || len(encoded) > 32<<10 {
		return nil, invalid
	}
	decoded, err := (jsonvalue.Value{Text: encoded}).Decode()
	if err != nil {
		return nil, invalid
	}
	items, ok := decoded.([]any)
	if !ok || len(items) == 0 || len(items) > 64 {
		return nil, invalid
	}
	segments := make([]query.JSONPathSegment, len(items))
	for i, item := range items {
		switch item := item.(type) {
		case string:
			segments[i] = query.JSONKey(item)
		case json.Number:
			index, err := strconv.ParseInt(item.String(), 10, 32)
			if err != nil {
				return nil, invalid
			}
			segments[i] = query.JSONIndex(int(index))
		default:
			return nil, invalid
		}
	}
	if _, err := query.NewJSONPath(segments...); err != nil {
		return nil, invalid
	}
	if args[0] == nil {
		return nil, nil
	}
	document, ok := args[0].(string)
	if !ok {
		return nil, invalid
	}
	value, err := (jsonvalue.Value{Text: document}).Decode()
	if err != nil {
		return nil, err
	}
	for _, segment := range segments {
		if key, ok := segment.Key(); ok {
			object, ok := value.(map[string]any)
			if !ok {
				return nil, nil
			}
			value, ok = object[key]
			if !ok {
				return nil, nil
			}
		} else {
			index, _ := segment.Index()
			array, ok := value.([]any)
			if !ok || index >= len(array) {
				return nil, nil
			}
			value = array[index]
		}
	}
	result, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(result), nil
}
