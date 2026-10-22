//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"io"

	"github.com/progresshans/godj/version"
)

func runVersion(ctx context.Context, args []string, stdout, stderr io.Writer, interrupt <-chan struct{}) int {
	if len(args) > 2 || len(args) == 2 && args[1] != "--json" {
		writeVersionDiagnostic(stderr, "invalid_arguments")
		return 2
	}
	select {
	case <-interrupt:
		writeVersionDiagnostic(stderr, "interrupted")
		return 130
	default:
	}
	if ctx != nil && ctx.Err() != nil {
		writeVersionDiagnostic(stderr, "canceled")
		return 130
	}
	current := version.Current()
	var document []byte
	if len(args) == 2 {
		// Every field is a value, and encoding has no user callbacks.
		document, _ = json.Marshal(struct {
			FormatVersion int          `json:"format_version"`
			Framework     version.Info `json:"framework"`
		}{1, current})
		document = append(document, '\n')
	} else {
		label := string(current.Origin)
		switch current.Origin {
		case version.ModuleVersion:
			label = current.Version
		case version.LocalReplacement:
			label = "local replacement"
		case version.ModuleReplacement:
			label = current.ReplacementModule + "@" + current.Version + " (module replacement)"
		}
		if current.Revision != "" {
			label += " " + current.Revision
		}
		if current.Modified {
			label += " (modified)"
		}
		document = []byte("godj " + label + "\n")
	}
	if stdout == nil {
		writeVersionDiagnostic(stderr, "output_failed")
		return 1
	}
	if count, err := stdout.Write(document); err != nil || count != len(document) {
		writeVersionDiagnostic(stderr, "output_failed")
		return 1
	}
	return 0
}

func writeVersionDiagnostic(writer io.Writer, code string) {
	if writer != nil {
		_, _ = io.WriteString(writer, "version_error/"+code+"\n")
	}
}
