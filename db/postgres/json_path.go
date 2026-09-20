package postgres

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

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
