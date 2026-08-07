package query

import "fmt"

const (
	CategoryField   = "field_error"
	CategoryQuery   = "query_error"
	CategoryBackend = "backend_error"
)

const (
	CodeUnknownField      = "unknown_field"
	CodeUnsupportedLookup = "unsupported_lookup"
	CodeDisallowedLookup  = "disallowed_lookup"
	CodeInvalidValue      = "invalid_value"
	CodeInvalidLimit      = "invalid_limit"
	CodeInvalidPlan       = "invalid_plan"
	CodeRequiredField     = "required_field"
	CodeEmptyPatch        = "empty_patch"
	CodeMissingPrimaryKey = "missing_primary_key"
	CodeUnexpectedRows    = "unexpected_rows_affected"
	CodeUnsupported       = "unsupported_feature"
)

// Error is the stable error taxonomy shared by dynamic lookup validation and
// backend compilation. Detail is diagnostic text, not a compatibility
// contract.
type Error struct {
	Category string
	Code     string
	Field    string
	Lookup   string
	Detail   string
}

func (e *Error) Error() string {
	location := ""
	if e.Field != "" {
		location = fmt.Sprintf(" field=%q", e.Field)
	}
	if e.Lookup != "" {
		location += fmt.Sprintf(" lookup=%q", e.Lookup)
	}
	if e.Detail == "" {
		return fmt.Sprintf("%s/%s%s", e.Category, e.Code, location)
	}
	return fmt.Sprintf("%s/%s%s: %s", e.Category, e.Code, location, e.Detail)
}

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	if !ok {
		return false
	}
	return (other.Category == "" || e.Category == other.Category) &&
		(other.Code == "" || e.Code == other.Code) &&
		(other.Field == "" || e.Field == other.Field) &&
		(other.Lookup == "" || e.Lookup == other.Lookup)
}
