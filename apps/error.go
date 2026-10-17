package apps

import "fmt"

// ErrorCode identifies a stable app-registry configuration failure.
type ErrorCode string

const (
	CodeInvalidConfig  ErrorCode = "invalid_config"
	CodeDuplicateName  ErrorCode = "duplicate_name"
	CodeDuplicateLabel ErrorCode = "duplicate_label"
)

// Error reports an invalid app registry definition.
type Error struct {
	Code   ErrorCode
	Field  string
	Detail string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Field == "" {
		return fmt.Sprintf("apps: %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("apps: %s: %s: %s", e.Code, e.Field, e.Detail)
}

func (e *Error) Is(target error) bool {
	want, ok := target.(*Error)
	if !ok || e == nil || want == nil {
		return false
	}
	return (want.Code == "" || e.Code == want.Code) &&
		(want.Field == "" || e.Field == want.Field)
}
