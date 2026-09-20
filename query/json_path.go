package query

import (
	"slices"
	"unicode/utf8"
)

// JSONPathSegment is a literal object key or a zero-based array index. A key
// containing digits remains a key; it is never parsed as an index or lookup.
type JSONPathSegment struct {
	kind  uint8
	key   string
	index int
}

func JSONKey(key string) JSONPathSegment      { return JSONPathSegment{kind: 1, key: key} }
func JSONIndex(index int) JSONPathSegment     { return JSONPathSegment{kind: 2, index: index} }
func (s JSONPathSegment) Key() (string, bool) { return s.key, s.kind == 1 }
func (s JSONPathSegment) Index() (int, bool)  { return s.index, s.kind == 2 }

// JSONPath owns a bounded sequence of literal segments. The zero value is
// invalid. Missing keys, wrong container kinds and out-of-range indices yield
// SQL NULL; an existing JSON null stays a JSON value.
type JSONPath struct{ data *jsonPathData }
type jsonPathData struct{ segments []JSONPathSegment }

// NewJSONPath accepts 1..64 segments and at most 4096 UTF-8 key bytes in total.
// Indices are in 0..2147483647 on every platform. Negative indices, wildcards
// and query-language expressions are not accepted as array indices.
func NewJSONPath(segments ...JSONPathSegment) (JSONPath, error) {
	if len(segments) == 0 || len(segments) > 64 {
		return JSONPath{}, invalidPlanError("JSON path requires 1 through 64 segments")
	}
	bytes := 0
	for _, segment := range segments {
		switch segment.kind {
		case 1:
			if len(segment.key) > 4096-bytes || !utf8.ValidString(segment.key) {
				return JSONPath{}, invalidPlanError("JSON path keys require valid UTF-8 and at most 4096 bytes in total")
			}
			bytes += len(segment.key)
		case 2:
			if segment.index < 0 || int64(segment.index) > 2147483647 {
				return JSONPath{}, invalidPlanError("JSON path index is outside 0 through 2147483647")
			}
		default:
			return JSONPath{}, invalidPlanError("JSON path segment is invalid")
		}
	}
	return JSONPath{data: &jsonPathData{segments: slices.Clone(segments)}}, nil
}

func (p JSONPath) Valid() bool { return p.data != nil }
func (p JSONPath) Segments() []JSONPathSegment {
	if p.data == nil {
		return nil
	}
	return slices.Clone(p.data.segments)
}
func (p JSONPath) Equal(other JSONPath) bool {
	if p.data == other.data {
		return true
	}
	return p.data != nil && other.data != nil && slices.Equal(p.data.segments, other.data.segments)
}

// WithJSONPath selects a value inside a JSON field without replacing the
// field's identity or relation provenance. Negation still compensates root
// SQL NULL independently of a missing path. SQL NULL IN members are rejected:
// use IsNull for missing paths and JSON null as an explicit JSON value.
func (c Condition) WithJSONPath(path JSONPath) (Condition, error) {
	if !path.Valid() {
		return Condition{}, invalidPlanError("JSON condition requires a valid path")
	}
	c.jsonPath = path
	if err := validateExpressionCondition(c); err != nil {
		return Condition{}, err
	}
	return c, nil
}

func (c Condition) JSONPath() (JSONPath, bool) { return c.jsonPath, c.jsonPath.Valid() }

func validateJSONPathCondition(c Condition) error {
	if !c.jsonPath.Valid() {
		return nil
	}
	if c.field.kind != FieldJSON || c.rhs == nil || c.rhs.kind == conditionRHSField {
		return invalidPlanError("JSON paths require a JSON field and literal operands")
	}
	switch c.lookup {
	case LookupExact, LookupIsNull, LookupIn:
	default:
		return invalidPlanError("JSON paths support exact, in and isnull only")
	}
	for _, value := range c.rhs.values {
		if value.IsNull() {
			return invalidPlanError("JSON path membership requires JSON values; use isnull for missing paths")
		}
	}
	return nil
}
