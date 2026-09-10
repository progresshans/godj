package templates

import "fmt"

// Error is a structured, secret-free template construction or render error.
type Error struct {
	Phase    string
	Code     string
	Template string
	Line     int
	Column   int
	Cause    error
}

func (e *Error) Error() string {
	location := e.Template
	if e.Line > 0 {
		location = fmt.Sprintf("%s:%d:%d", location, e.Line, e.Column)
	}
	if e.Cause != nil {
		return fmt.Sprintf("templates: %s %s at %s: %v", e.Phase, e.Code, location, e.Cause)
	}
	return fmt.Sprintf("templates: %s %s at %s", e.Phase, e.Code, location)
}

func (e *Error) Unwrap() error { return e.Cause }
