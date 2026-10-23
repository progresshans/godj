package command

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/internal/callback"
	"github.com/progresshans/godj/internal/diagnosticlog"
)

// ResourceCheck declares an optional I/O check. Run owns and closes every
// resource it opens, including on error, cancellation, panic and Goexit. Checks
// must not repair state or invoke business commands. Registries never retry them.
type ResourceCheck struct {
	Name, AppLabel, Summary string
	Run                     func(context.Context) error
}

// CheckError explicitly exposes a configuration field name while keeping the
// original cause private in diagnostics. Field is a public schema/environment
// identifier, never a value, path, credential or arbitrary error message.
type CheckError struct {
	Field string
	Cause error
}

func (e *CheckError) Error() string {
	if e != nil && validCheckField(e.Field) {
		return "command: invalid configuration field " + e.Field
	}
	return "command: invalid configuration"
}
func (e *CheckError) Format(s fmt.State, _ rune) { fmt.Fprint(s, e.Error()) }
func (e *CheckError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func validCheckField(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	indexed, digits := false, 0
	for index, c := range []byte(value) {
		if indexed {
			if c >= '0' && c <= '9' {
				digits++
				continue
			}
			if c == ']' && digits != 0 {
				indexed = false
				continue
			}
			return false
		}
		if c == '[' && index > 0 {
			indexed, digits = true, 0
			continue
		}
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
		if !letter && !(index > 0 && (c >= '0' && c <= '9' || c == '.' || c == '-')) {
			return false
		}
	}
	return !indexed
}

type resourceCheckFailure struct {
	name  string
	cause error
}

func (*resourceCheckFailure) Error() string                { return "command: resource check failed" }
func (e *resourceCheckFailure) Unwrap() error              { return e.cause }
func (e *resourceCheckFailure) Format(s fmt.State, _ rune) { fmt.Fprint(s, e.Error()) }

func (r *Registry) setResourceChecks(installed apps.Registry, checks []ResourceCheck) error {
	r.resources = append([]ResourceCheck(nil), checks...)
	r.resourceByName = make(map[string]int, len(checks))
	for _, check := range r.resources {
		if !validName(check.Name) || !validText(check.Summary, 2048) || check.Run == nil {
			return failed("invalid_definition")
		}
		if _, exists := r.resourceByName[check.Name]; exists {
			return failed("duplicate_check")
		}
		if check.AppLabel != "" {
			if _, exists := installed.Lookup(check.AppLabel); !exists {
				return failed("uninstalled_app")
			}
		}
		r.resourceByName[check.Name] = 0
	}
	sort.Slice(r.resources, func(i, j int) bool { return r.resources[i].Name < r.resources[j].Name })
	for i, check := range r.resources {
		r.resourceByName[check.Name] = i
	}
	return nil
}

// ResourceDescriptions returns a sorted, caller-owned inventory without Run.
func (r Registry) ResourceDescriptions() []ResourceCheck {
	result := append([]ResourceCheck(nil), r.resources...)
	for i := range result {
		result[i].Run = nil
	}
	return result
}

// CheckResources validates the complete selection before any callback. Pure
// configuration checks precede selected I/O checks, which run in request order.
// Failure or cancellation prevents the remaining checks; started callbacks are
// joined through their defers. An empty selection runs pure checks only.
func (r Registry) CheckResources(ctx context.Context, names ...string) error {
	if ctx == nil || r.byName == nil || r.resourceByName == nil {
		return failed("invalid_invocation")
	}
	if len(names) > MaxCommands {
		return failed("invalid_arguments")
	}
	selected := make([]ResourceCheck, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !validName(name) || seen[name] {
			return failed("invalid_arguments")
		}
		i, exists := r.resourceByName[name]
		if !exists {
			return failed("unknown_check")
		}
		seen[name] = true
		selected = append(selected, r.resources[i])
	}
	if err := r.Check(ctx); err != nil {
		return err
	}
	for _, check := range selected {
		if err := ctx.Err(); err != nil {
			return failed("canceled", err)
		}
		_, err := callback.Invoke(func() (struct{}, error) { return struct{}{}, check.Run(ctx) }, failed("callback_aborted"))
		diagnosticlog.End(ctx, r.logger, "command", "check_resource", check.Name, err)
		if err != nil {
			return failed("resource_check_failed", &resourceCheckFailure{name: check.Name, cause: err})
		}
	}
	if err := ctx.Err(); err != nil {
		return failed("canceled", err)
	}
	return nil
}

func (r Registry) serveCheck(ctx context.Context, args []string, streams Streams) error {
	if len(args) == 0 {
		return r.Check(ctx)
	}
	if len(args) == 1 && args[0] == "--list" {
		var output strings.Builder
		for _, check := range r.resources {
			fmt.Fprintf(&output, "%s\t%s\n", check.Name, check.Summary)
		}
		return writeHelp(streams.Stdout, output.String())
	}
	if len(args)%2 != 0 || len(args) > MaxArguments {
		return failed("invalid_arguments")
	}
	names := make([]string, 0, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		if args[i] != "--resource" {
			return failed("invalid_arguments")
		}
		names = append(names, args[i+1])
	}
	return r.CheckResources(ctx, names...)
}
