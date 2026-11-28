// Package diagnosticlog delivers closed framework observations to an explicit
// caller-owned slog logger. It never selects a global logger or owns the sink.
package diagnosticlog

import (
	"context"
	"log/slog"

	"github.com/progresshans/godj/internal/callback"
)

type loggingKey struct{}
type loggerKey struct{}

// WithLogger carries the invocation's explicit logger to resource helpers.
// Logger and its handler must support concurrent use. The owner closes its sink
// only after the framework invocation and all application work have returned.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

func Logger(ctx context.Context) *slog.Logger {
	logger, _ := ctx.Value(loggerKey{}).(*slog.Logger)
	return logger
}

// Emit only receives framework constants and validated declaration names. Never
// pass arguments, payloads, error text, credentials, addresses or protocol data.
// A handler's errors, panic and Goexit do not affect the product result. Handler
// calls are joined, including their defers; a nonreturning handler can delay the
// caller. There is no asynchronous queue, retry, fallback sink or hidden worker.
// Recursive logging with the supplied context is suppressed.
func Emit(ctx context.Context, logger *slog.Logger, component, action, name, outcome string) {
	if logger == nil || ctx == nil || ctx.Value(loggingKey{}) != nil {
		return
	}
	ctx = context.WithValue(ctx, loggingKey{}, true)
	_, _ = callback.Invoke(func() (struct{}, error) {
		level := slog.LevelInfo
		if outcome == "failed" {
			level = slog.LevelError
		} else if outcome == "canceled" {
			level = slog.LevelWarn
		}
		attrs := []slog.Attr{slog.String("component", component), slog.String("action", action), slog.String("outcome", outcome), slog.Bool("canceled", ctx.Err() != nil)}
		if name != "" {
			attrs = append(attrs, slog.String("name", name))
		}
		logger.LogAttrs(ctx, level, "godj", attrs...)
		return struct{}{}, nil
	}, nil)
}

// End deliberately inspects no user error methods and never formats a cause.
func End(ctx context.Context, logger *slog.Logger, component, action, name string, err error) {
	outcome := "completed"
	if err != nil {
		outcome = "failed"
	}
	Emit(ctx, logger, component, action, name, outcome)
}
