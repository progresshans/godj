package settings

import "fmt"

// Error reports an invalid settings definition.
type Error struct {
	Field  string
	Detail string
	Cause  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Field == "" {
		return "settings: " + e.Detail
	}
	return fmt.Sprintf("settings: %s: %s", e.Field, e.Detail)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) Is(target error) bool {
	want, ok := target.(*Error)
	if !ok || e == nil || want == nil {
		return false
	}
	return want.Field == "" || e.Field == want.Field
}
