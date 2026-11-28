package postgres

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

func validateJSONPath(field query.FieldRef, path query.JSONPath) error {
	for _, segment := range path.Segments() {
		if key, _ := segment.Key(); strings.ContainsRune(key, 0) {
			return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidValue, Field: field.Name(), Detail: "PostgreSQL JSON paths cannot contain NUL"}
		}
	}
	return nil
}

// Strict paths avoid native integer extraction treating a scalar as an array.
// Silent handles missing/type mismatch; the literal grammar has no arithmetic.
func appendJSONPath(statement *strings.Builder, column string, path query.JSONPath, arguments *[]any) {
	statement.WriteString("jsonb_path_query_first(" + column + ", " + placeholder(len(*arguments)+1) + "::jsonpath, '{}'::jsonb, true)")
	*arguments = append(*arguments, "strict "+jsonPathArgument(path))
}

// jsonPathArgument emits PostgreSQL literal keys and array indices. The
// compiler binds it separately from source columns and comparison values.
func jsonPathArgument(path query.JSONPath) string {
	var result strings.Builder
	result.WriteByte('$')
	for _, segment := range path.Segments() {
		if key, ok := segment.Key(); ok {
			quoted, _ := json.Marshal(key) // Validated UTF-8 string; no marshal failure.
			result.WriteByte('.')
			result.Write(quoted)
		} else if index, ok := segment.Index(); ok {
			result.WriteByte('[')
			result.WriteString(strconv.Itoa(index))
			result.WriteByte(']')
		}
	}
	return result.String()
}
