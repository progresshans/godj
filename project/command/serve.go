package command

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/progresshans/godj/project/compatibility"
)

// Serve is a runtime command entrypoint. list/help are resource-free; check
// performs pure project checks. run parses argv before invoking a handler.
// Call compatibility.Serve before constructing project configuration as well.
func (r Registry) Serve(ctx context.Context, argv []string, streams Streams) error {
	if handled, err := compatibility.Serve(ctx, argv, streams.Stdout); handled {
		return err
	}
	if ctx == nil || r.byName == nil || streams.Stdout == nil || streams.Stderr == nil || streams.Stdin == nil {
		return failed("invalid_invocation")
	}
	if err := ctx.Err(); err != nil {
		return failed("canceled", err)
	}
	args := append([]string(nil), argv...)
	if len(args) == 1 && args[0] == "list" {
		var output strings.Builder
		for _, d := range r.definitions {
			fmt.Fprintf(&output, "%s\t%s\n", d.Name, d.Summary)
		}
		return writeHelp(streams.Stdout, output.String())
	}
	if len(args) == 2 && args[0] == "help" {
		i, ok := r.byName[args[1]]
		if !ok {
			return failed("unknown_command")
		}
		d := r.definitions[i]
		var output strings.Builder
		fmt.Fprintf(&output, "%s: %s\n", d.Name, d.Summary)
		for _, a := range d.Arguments {
			fmt.Fprintf(&output, "  %s (%s; required=%t; many=%t): %s\n", a.Name, a.Kind, a.Required, a.Many, a.Help)
		}
		for _, o := range d.Options {
			fmt.Fprintf(&output, "  --%s (%s; required=%t): %s\n", o.Name, o.Kind, o.Required, o.Help)
		}
		return writeHelp(streams.Stdout, output.String())
	}
	if len(args) == 1 && args[0] == "check" {
		return r.Check(ctx)
	}
	if len(args) >= 2 && args[0] == "run" {
		if len(args) == 3 && args[2] == "--help" {
			return r.Serve(ctx, []string{"help", args[1]}, streams)
		}
		_, err := r.Execute(ctx, args[1], args[2:], streams)
		return err
	}
	return failed("invalid_arguments")
}

func writeHelp(writer io.Writer, text string) error {
	n, err := io.WriteString(writer, text)
	if err != nil || n != len(text) {
		return failed("output_failed", err)
	}
	return nil
}
