package diagnosticlog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type hostileError struct{}

func (hostileError) Error() string { panic("error formatting must never run") }
func (hostileError) Is(error) bool { panic("error inspection must never run") }
func (hostileError) Unwrap() error { panic("error traversal must never run") }

func TestExplicitLoggerScopeAndClosedErrorObservations(t *testing.T) {
	var one, two bytes.Buffer
	first := slog.New(slog.NewJSONHandler(&one, nil)).With("project", "one", "invocation", "command-1")
	second := slog.New(slog.NewJSONHandler(&two, nil)).With("project", "two", "invocation", "command-2")
	ctx := context.Background()
	End(ctx, first, "command", "execute", "write", hostileError{})
	End(ctx, second, "command", "execute", "write", nil)
	End(ctx, nil, "command", "execute", "never", errors.New("private"))
	for _, check := range []struct{ text, project, outcome string }{{one.String(), "one", "failed"}, {two.String(), "two", "completed"}} {
		if !strings.Contains(check.text, `"project":"`+check.project+`"`) || !strings.Contains(check.text, `"outcome":"`+check.outcome+`"`) || strings.Count(check.text, "\n") != 1 {
			t.Fatal(check.text)
		}
	}
}
