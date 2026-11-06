package command

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCommandLoggingPreservesCompletedAndPrivateInputs(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil)).With("project", "one", "invocation", "command-1")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registry, err := New(Config{Logger: logger, Commands: []Definition{{Name: "save", Summary: "Save a value", Arguments: []Argument{{Name: "title", Help: "private input", Kind: String, Required: true}}, Handle: func(context.Context, Values, Streams) error { cancel(); return nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := registry.Execute(ctx, "save", []string{"private-value"}, testStreams())
	if err != nil || !result.Completed || result.OutputFailed {
		t.Fatal(result, err)
	}
	if strings.Contains(output.String(), "private-value") || !strings.Contains(output.String(), `"canceled":true`) || !strings.Contains(output.String(), `"action":"execute","outcome":"completed"`) {
		t.Fatal(output.String())
	}
}

type abnormalLogHandler struct {
	stage, mode string
	completed   atomic.Int32
}

func (h *abnormalLogHandler) fail() {
	defer h.completed.Add(1)
	if h.mode == "panic" {
		panic("private-log-data")
	}
	runtime.Goexit()
}
func (h *abnormalLogHandler) Enabled(context.Context, slog.Level) bool {
	if h.stage == "enabled" {
		h.fail()
	}
	return true
}
func (h *abnormalLogHandler) Handle(context.Context, slog.Record) error {
	h.fail()
	return errors.New("private-log-data")
}
func (h *abnormalLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *abnormalLogHandler) WithGroup(string) slog.Handler      { return h }

func TestCommandJoinsAbnormalLoggerAndRetainsBusinessFailure(t *testing.T) {
	for _, stage := range []string{"enabled", "handle"} {
		for _, mode := range []string{"panic", "goexit"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				h := &abnormalLogHandler{stage: stage, mode: mode}
				cause := errors.New("private-business-value")
				var calls atomic.Int32
				r, err := New(Config{Logger: slog.New(h), Commands: []Definition{{Name: "fail", Summary: "Failure", Handle: func(context.Context, Values, Streams) error { calls.Add(1); return cause }}}})
				if err != nil {
					t.Fatal(err)
				}
				result, err := r.Execute(t.Context(), "fail", nil, testStreams())
				if !errors.Is(err, cause) || !result.Executed || result.Completed || result.OutputFailed || calls.Load() != 1 || h.completed.Load() != 2 {
					t.Fatal(result, err, calls.Load(), h.completed.Load())
				}
			})
		}
	}
}
