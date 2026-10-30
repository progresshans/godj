package sqlite

import (
	"database/sql/driver"
	"errors"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

// Stateless and deterministic, registered before connections are opened.
var jsonKeysRegistrationError = modernsqlite.RegisterDeterministicScalarFunction("godj_json_has_keys", 3, sqliteJSONHasKeys)

func sqliteJSONHasKeys(_ *modernsqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	invalid := errors.New("invalid SQLite JSON key presence arguments")
	if len(args) != 3 {
		return nil, invalid
	}
	encoded, ok := args[1].(string)
	if !ok || len(encoded) > 32<<10 {
		return nil, invalid
	}
	mode, ok := args[2].(int64)
	if !ok || mode < 0 || mode > 1 {
		return nil, invalid
	}
	decoded, err := (jsonvalue.Value{Text: encoded}).Decode()
	if err != nil {
		return nil, invalid
	}
	items, ok := decoded.([]any)
	if !ok || len(items) > query.MaxJSONKeys {
		return nil, invalid
	}
	keys := make([]string, len(items))
	for i, item := range items {
		key, ok := item.(string)
		if !ok {
			return nil, invalid
		}
		keys[i] = key
	}
	if _, err := query.NewJSONKeyList(keys...); err != nil {
		return nil, invalid
	}
	if args[0] == nil {
		// Empty-list predicates adopt native PostgreSQL's unknown result for
		// an absent JSON document; they are not empty-IN Boolean constants.
		if len(keys) == 0 {
			return nil, nil
		}
		// Nonempty SQLite presence is Boolean, including a missing path.
		return int64(0), nil
	}
	document, ok := args[0].(string)
	if !ok {
		return nil, invalid
	}
	if len(document) > jsonvalue.MaxDocumentBytes {
		return nil, jsonvalue.ErrLimit
	}
	value, err := (jsonvalue.Value{Text: document}).Decode()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return mode, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return int64(0), nil
	}
	for _, key := range keys {
		_, present := object[key]
		if mode == 1 && !present {
			return int64(0), nil
		}
		if mode == 0 && present {
			return int64(1), nil
		}
	}
	return mode, nil
}
