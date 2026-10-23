// Package command provides explicit, invocation-owned management commands.
// Registries are immutable. Argument parsing and help never open resources.
package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/internal/callback"
	"github.com/progresshans/godj/internal/diagnosticlog"
)

const (
	MaxCommands      = 1024
	MaxArguments     = 256
	MaxArgumentBytes = 64 << 10
	MaxInputBytes    = 1 << 20
)

// Error prints a closed diagnostic; values, callback errors and panic payloads
// are never interpolated. Unwrap is for programmatic inspection by the owner.
type Error struct {
	Code   string
	causes []error
}

func (e *Error) Error() string {
	if e == nil {
		return "command: invalid error"
	}
	return "command: " + e.Code
}
func (e *Error) Unwrap() []error {
	if e == nil {
		return nil
	}
	return append([]error(nil), e.causes...)
}
func (e *Error) Format(s fmt.State, _ rune) { fmt.Fprint(s, e.Error()) }

func failed(code string, causes ...error) *Error {
	failure := &Error{Code: code}
	for _, cause := range causes {
		if cause != nil {
			failure.causes = append(failure.causes, cause)
		}
	}
	return failure
}

type Streams struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}
type Handler func(context.Context, Values, Streams) error

type Definition struct {
	Name string
	// AppLabel is empty for a project command, otherwise an installed app label.
	AppLabel  string
	Summary   string
	Arguments []Argument
	Options   []Option
	Handle    Handler
}

type Config struct {
	Apps     apps.Registry
	Commands []Definition
	// Logger is optional and caller-owned. Use With to identify the project and
	// invocation. Logs omit arguments, output and error causes. Sink errors,
	// panic and Goexit never alter Completed, OutputFailed or the command error.
	Logger *slog.Logger
	// Check is pure configuration validation, called after argument validation
	// and before Handle or an explicitly selected resource check.
	Check func(context.Context) error
	// ResourceChecks run only when selected by CheckResources or check --resource.
	ResourceChecks []ResourceCheck
}

type Registry struct {
	definitions    []Definition
	byName         map[string]int
	check          func(context.Context) error
	resources      []ResourceCheck
	resourceByName map[string]int
	logger         *slog.Logger
}

// New snapshots declarations and rejects ambiguity instead of overriding an
// earlier app or a built-in command. No callbacks execute during construction.
func New(config Config) (Registry, error) {
	if len(config.Commands) > MaxCommands || len(config.ResourceChecks) > MaxCommands {
		return Registry{}, failed("invalid_definition")
	}
	r := Registry{byName: make(map[string]int), check: config.Check, logger: config.Logger}
	for _, input := range config.Commands {
		if !validName(input.Name) || Reserved(input.Name) || input.Handle == nil || !validText(input.Summary, 2048) {
			return Registry{}, failed("invalid_definition")
		}
		if _, exists := r.byName[input.Name]; exists {
			return Registry{}, failed("duplicate_command")
		}
		if input.AppLabel != "" {
			if _, installed := config.Apps.Lookup(input.AppLabel); !installed {
				return Registry{}, failed("uninstalled_app")
			}
		}
		input.Arguments = append([]Argument(nil), input.Arguments...)
		input.Options = append([]Option(nil), input.Options...)
		if !validParameters(input) {
			return Registry{}, failed("invalid_definition")
		}
		r.byName[input.Name] = len(r.definitions)
		r.definitions = append(r.definitions, input)
	}
	sort.Slice(r.definitions, func(i, j int) bool { return r.definitions[i].Name < r.definitions[j].Name })
	for i, d := range r.definitions {
		r.byName[d.Name] = i
	}
	if err := r.setResourceChecks(config.Apps, config.ResourceChecks); err != nil {
		return Registry{}, err
	}
	return r, nil
}

// Descriptions returns independent, sorted declarations without callbacks.
func (r Registry) Descriptions() []Definition {
	result := make([]Definition, len(r.definitions))
	for i, d := range r.definitions {
		d.Arguments = append([]Argument(nil), d.Arguments...)
		d.Options = append([]Option(nil), d.Options...)
		d.Handle = nil
		result[i] = d
	}
	return result
}

// Check validates project configuration only. It never invokes a command.
func (r Registry) Check(ctx context.Context) (result error) {
	if ctx == nil || r.byName == nil {
		return failed("invalid_invocation")
	}
	defer func() { diagnosticlog.End(ctx, r.logger, "command", "check", "", result) }()
	if err := ctx.Err(); err != nil {
		return failed("canceled", err)
	}
	if r.check != nil {
		_, err := callback.Invoke(func() (struct{}, error) { return struct{}{}, r.check(ctx) }, failed("callback_aborted"))
		if err != nil {
			return failed("check_failed", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return failed("canceled", err)
	}
	return nil
}

type Result struct {
	// Executed means Handle was entered; an error never implies rollback.
	Executed bool
	// Completed means Handle returned nil, even if output delivery failed.
	Completed    bool
	OutputFailed bool
}

// Execute validates every input before Check/Handle. Callbacks are joined even
// after cancellation, panic or Goexit. They must finish all owned work before
// returning; the framework never retries a command or infers its DB outcome.
func (r Registry) Execute(ctx context.Context, name string, argv []string, streams Streams) (result Result, err error) {
	if ctx == nil || r.byName == nil || streams.Stdin == nil || streams.Stdout == nil || streams.Stderr == nil {
		return result, failed("invalid_invocation")
	}
	index, exists := r.byName[name]
	if !exists {
		return result, failed("unknown_command")
	}
	definition := r.definitions[index]
	ctx = diagnosticlog.WithLogger(ctx, r.logger)
	defer func() { diagnosticlog.End(ctx, r.logger, "command", "execute", definition.Name, err) }()
	values, err := parse(definition, argv)
	if err != nil {
		return result, err
	}
	if err := r.Check(ctx); err != nil {
		return result, err
	}
	// Both proxies share the mutex, including when the caller supplied the same
	// writer. Delivery errors are retained even if a handler ignores Write.
	var mu sync.Mutex
	stdout := &observedWriter{mu: &mu, writer: streams.Stdout}
	stderr := &observedWriter{mu: &mu, writer: streams.Stderr}
	result.Executed = true
	_, callErr := callback.Invoke(func() (struct{}, error) {
		return struct{}{}, definition.Handle(ctx, values, Streams{Stdin: streams.Stdin, Stdout: stdout, Stderr: stderr})
	}, failed("callback_aborted"))
	result.Completed = callErr == nil
	mu.Lock()
	writeErr := errors.Join(stdout.err, stderr.err)
	mu.Unlock()
	result.OutputFailed = writeErr != nil
	if callErr != nil {
		return result, failed("execution_failed", callErr, writeErr)
	}
	if writeErr != nil {
		return result, failed("output_failed", writeErr)
	}
	// A late cancellation cannot turn a completed mutation into a failed one.
	return result, nil
}

type observedWriter struct {
	mu     *sync.Mutex
	writer io.Writer
	err    error
}

func (w *observedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.writer.Write(data)
	if n < 0 || n > len(data) {
		n = 0
		err = io.ErrShortWrite
	}
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}

// Resource transfers one invocation's value and cleanup callback. A non-nil
// Close is required, including when Open returns both a resource and an error.
// Until Open returns, it retains cleanup responsibility (also on panic/Goexit).
type Resource[T any] struct {
	Value T
	Close func(context.Context) error
}

// Using opens resources only after Execute has parsed all input and completed
// pure checks. It joins Handle before Close and never replaces its result with
// a cleanup result. Cleanup receives an independent cooperative deadline.
func Using[T any](open func(context.Context) (Resource[T], error), handle func(context.Context, T, Values, Streams) error) Handler {
	return func(ctx context.Context, values Values, streams Streams) error {
		if open == nil || handle == nil {
			return failed("invalid_definition")
		}
		resource, openErr := callback.Invoke(func() (Resource[T], error) { return open(ctx) }, failed("callback_aborted"))
		diagnosticlog.End(ctx, diagnosticlog.Logger(ctx), "command", "open_resource", "", openErr)
		if resource.Close == nil {
			if openErr != nil {
				return failed("resource_open_failed", openErr)
			}
			return failed("invalid_resource")
		}
		var runErr error
		if openErr == nil {
			if err := ctx.Err(); err != nil {
				runErr = err
			} else {
				_, runErr = callback.Invoke(func() (struct{}, error) { return struct{}{}, handle(ctx, resource.Value, values, streams) }, failed("callback_aborted"))
			}
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, closeErr := callback.Invoke(func() (struct{}, error) { return struct{}{}, resource.Close(cleanup) }, failed("callback_aborted"))
		diagnosticlog.End(cleanup, diagnosticlog.Logger(ctx), "command", "close_resource", "", closeErr)
		if openErr != nil {
			return failed("resource_open_failed", openErr, closeErr)
		}
		if runErr != nil {
			return failed("execution_failed", runErr, closeErr)
		}
		if closeErr != nil {
			return failed("resource_close_failed", closeErr)
		}
		return nil
	}
}

// ExitCode maps a command error to a stable process result. It makes no claim
// about a transaction: any command that was entered may have written data.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var failure *Error
	if errors.As(err, &failure) && failure != nil {
		switch failure.Code {
		case "unknown_command", "unknown_check", "invalid_arguments", "invalid_invocation", "invalid_definition", "duplicate_command", "duplicate_check", "uninstalled_app":
			return 2
		}
	}
	return 1
}

// Diagnostic is safe for a process entrypoint's stderr, including arbitrary
// errors returned by project code. It never formats the underlying cause.
func Diagnostic(err error) string {
	if err == nil {
		return ""
	}
	var failure *Error
	if errors.As(err, &failure) && failure != nil {
		switch failure.Code {
		case "check_failed", "resource_check_failed":
			result := "command_error/" + failure.Code
			var resource *resourceCheckFailure
			if errors.As(err, &resource) && resource != nil && validName(resource.name) {
				result += "/" + resource.name
			}
			var configuration *CheckError
			if errors.As(err, &configuration) && configuration != nil && validCheckField(configuration.Field) {
				result += "/" + configuration.Field
			}
			return result
		case "unknown_command", "unknown_check", "invalid_arguments", "invalid_invocation", "invalid_definition", "duplicate_command", "duplicate_check", "uninstalled_app", "canceled", "output_failed", "execution_failed", "resource_open_failed", "resource_close_failed", "invalid_resource", "callback_aborted":
			return "command_error/" + failure.Code
		}
	}
	var configuration *CheckError
	if errors.As(err, &configuration) && configuration != nil {
		if validCheckField(configuration.Field) {
			return "command_error/check_failed/" + configuration.Field
		}
		return "command_error/check_failed"
	}
	return "command_error/execution_failed"
}

func validText(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func validName(value string) bool {
	if len(value) == 0 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func Reserved(name string) bool {
	switch name {
	case "startproject", "startapp", "version", "help", "check", "generate", "migrations", "makemigrations", "migrate", "showmigrations", "sqlmigrate", "createsuperuser", "runserver", "command", "commands", "serve":
		return true
	}
	return false
}
