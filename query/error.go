package query

import "fmt"

const (
	CategoryField      = "field_error"
	CategoryQuery      = "query_error"
	CategoryBackend    = "backend_error"
	CategoryArgument   = "argument_error"
	CategoryModelState = "model_state_error"
	CategoryNotUpdated = "not_updated"
	CategoryIntegrity  = "integrity_error"
)

const (
	CodeUnknownField                 = "unknown_field"
	CodeUnknownRelation              = "unknown_relation"
	CodeUnknownRelatedField          = "unknown_related_field"
	CodeUnsupportedLookup            = "unsupported_lookup"
	CodeDisallowedLookup             = "disallowed_lookup"
	CodeInvalidValue                 = "invalid_value"
	CodeInvalidLimit                 = "invalid_limit"
	CodeInvalidIndex                 = "invalid_index"
	CodeUnorderedQuery               = "unordered_query"
	CodeInvalidPlan                  = "invalid_plan"
	CodeMissingTable                 = "missing_table"
	CodeRequiredField                = "required_field"
	CodeEmptyPatch                   = "empty_patch"
	CodeMissingPrimaryKey            = "missing_primary_key"
	CodeUnexpectedRows               = "unexpected_rows_affected"
	CodeUnsupported                  = "unsupported_feature"
	CodePrimaryKeyUpdateField        = "primary_key_update_field"
	CodeForceUpdateWithoutPrimaryKey = "force_update_without_primary_key"
	CodeForceUpdateMissingRow        = "force_update_missing_row"
	CodeUpdateFieldsMissingRow       = "update_fields_missing_row"
	CodeMutuallyExclusiveForceFlags  = "mutually_exclusive_force_flags"
	CodeUniquePrimaryKey             = "unique_primary_key"
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
	Cause    error
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

// Unwrap preserves backend-specific causes while Error exposes a stable
// category and code to callers. Consumers should branch on the stable fields;
// errors.Is/As can still inspect the original driver error when necessary.
func (e *Error) Unwrap() error {
	return e.Cause
}
