package consumer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/notes"
	"github.com/progresshans/godj/project/command"
	"github.com/progresshans/godj/settings/database"
)

// The logger really runs during generated-model I/O in an external module. A
// returned error, panic or Goexit at the sink must not report the committed write
// as a failed command. A fresh connection independently reads the durable result.
func TestGeneratedCommandCommitSurvivesEveryLoggerFailure(t *testing.T) {
	for _, mode := range []string{"error", "panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			selected, err := database.SQLite(filepath.Join(t.TempDir(), "logs.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			prepareDatabase(t, selected)
			sink := &failingSink{mode: mode}
			logger := slog.New(slog.NewJSONHandler(sink, nil)).With("project", "external-notes", "invocation", mode)
			var closed atomic.Int32
			registry, err := command.New(command.Config{Logger: logger, Commands: []command.Definition{{Name: "save", Summary: "Save a generated note",
				Handle: command.Using(func(ctx context.Context) (command.Resource[database.Backend], error) {
					backend, err := database.Open(ctx, selected)
					if err != nil {
						return command.Resource[database.Backend]{}, err
					}
					return command.Resource[database.Backend]{Value: backend, Close: func(context.Context) error { closed.Add(1); return backend.Close() }}, nil
				}, func(ctx context.Context, backend database.Backend, _ command.Values, _ command.Streams) error {
					_, err := notes.NoteObjects.Create(ctx, backend, notes.NewNoteCreate("confirmed private note"))
					return err
				}),
			}}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := registry.Execute(t.Context(), "save", nil, command.Streams{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
			if err != nil || !result.Executed || !result.Completed || result.OutputFailed || closed.Load() != 1 || sink.calls.Load() != 4 || sink.defers.Load() != 4 {
				t.Fatal("logging changed command/cleanup result", result, err, closed.Load(), sink.calls.Load(), sink.defers.Load())
			}
			if sink.private.Load() {
				t.Fatal("generated field or credential sent to log sink")
			}
			backend, err := database.Open(t.Context(), selected)
			if err != nil {
				t.Fatal(err)
			}
			rows, readErr := notes.NoteObjects.Using(backend).All(t.Context())
			closeErr := backend.Close()
			if readErr != nil || closeErr != nil || len(rows) != 1 || rows[0].Title != "confirmed private note" {
				t.Fatal("confirmed command data did not survive logger failure", readErr, closeErr)
			}
		})
	}
}

type failingSink struct {
	mode          string
	calls, defers atomic.Int32
	private       atomic.Bool
}

func (s *failingSink) Write(data []byte) (int, error) {
	s.calls.Add(1)
	defer s.defers.Add(1)
	if strings.Contains(string(data), "confirmed private note") || strings.Contains(string(data), "credential") {
		s.private.Store(true)
	}
	switch s.mode {
	case "panic":
		panic("private-sink-credential")
	case "goexit":
		runtime.Goexit()
	}
	return 0, errors.New("private-sink-credential")
}
