//go:build darwin || linux

package runserverproduct_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/testprocess"
)

func exerciseStartedAppCommands(t *testing.T, global, project, workspace string, environment []string) {
	t.Helper()
	run := func(args ...string) testprocess.CommandResult {
		t.Helper()
		result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}, project, environment, global, args...)
		assertRunserverWorkspaceEmpty(t, workspace)
		return result
	}
	// This is ordinary project registration, after adding a generated model.
	// Its declaration runner remains independent of generated app imports.
	writeStartedAppSource(t, project, "internal/commands/commands.go", startedAppCommandSource)
	for _, args := range [][]string{{"check"}, {"commands"}, {"help", "add-note"}, {"command", "add-note", "--help"}} {
		result := run(args...)
		if result.ExitCode != 0 || result.Stderr != "" {
			t.Fatalf("management discovery %s: %d %q", args[0], result.ExitCode, result.Stderr)
		}
	}
	for _, args := range [][]string{{"command", "add-note"}, {"command", "add-note", "private-title", "--unknown"}, {"command", "missing"}} {
		result := run(args...)
		if result.ExitCode != 2 || result.Stdout != "" || strings.Contains(result.Stderr, "private-title") {
			t.Fatal("invalid input reached command or leaked values", result.ExitCode, result.Stderr)
		}
	}
	assertStartedCommandRows(t, project, workspace, environment, 2, "")
	created := run("command", "add-note", "created by command", "--summary", "shared model field")
	if created.ExitCode != 0 || created.Stdout != "note-created\n" || created.Stderr != "" {
		t.Fatal("custom generated command failed", created.ExitCode, created.Stderr)
	}
	assertStartedCommandRows(t, project, workspace, environment, 3, "created by command")
	failed := run("command", "add-note", "saved before command error", "--fail-after-write")
	if failed.ExitCode != 1 || failed.Stdout != "" || failed.Stderr != "command_error/execution_failed\n" {
		t.Fatal("callback error lost command exit or exposed cause", failed.ExitCode, failed.Stderr)
	}
	assertStartedCommandRows(t, project, workspace, environment, 4, "saved before command error")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	failed = testprocess.RunWithStdout(t, testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}, project, environment, writer, global, "command", "add-note", "saved before output error")
	assertRunserverWorkspaceEmpty(t, workspace)
	if failed.ExitCode == 0 || !strings.Contains(failed.Stderr, "output_failed") {
		t.Fatal("closed output was reported as success", failed.ExitCode, failed.Stderr)
	}
	assertStartedCommandRows(t, project, workspace, environment, 5, "saved before output error")
	// A genuine child probe with a mismatching contract must fail before its
	// resource-opening/command branch. This is a protocol negative control,
	// not evidence for independent fixed-module installation.
	entry := filepath.Join(project, "cmd/commands/main.go")
	before, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	writeStartedAppSource(t, project, "cmd/commands/main.go", `package main
import (
 "encoding/json"
 "os"
 "github.com/progresshans/godj/project/compatibility"
)
func main() {
 if len(os.Args) == 2 && os.Args[1] == compatibility.PrivateArgument {
  info := compatibility.Current(); info.Contract.Commands++
  if json.NewEncoder(os.Stdout).Encode(info) != nil { os.Exit(1) }; return
 }
 if os.WriteFile("unexpected-command-entry", []byte("entered"), 0600) != nil { os.Exit(2) }
}
`)
	mismatch := run("command", "add-note", "must not be saved")
	if mismatch.ExitCode != 1 || mismatch.Stdout != "" || mismatch.Stderr != "project_command_error/framework_incompatible\n" {
		t.Fatal("runtime mismatch not diagnosed", mismatch.ExitCode, mismatch.Stderr)
	}
	if _, err := os.Stat(filepath.Join(project, "unexpected-command-entry")); !os.IsNotExist(err) {
		t.Fatal("incompatible runtime entered command", err)
	}
	if err := os.WriteFile(entry, before, 0644); err != nil {
		t.Fatal(err)
	}
	declaration := filepath.Join(project, "apps/line_items/modeldef/schema.go")
	beforeDeclaration, err := os.ReadFile(declaration)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(beforeDeclaration), `"Title", 80`, `"Title", 81`, 1)
	if changed == string(beforeDeclaration) {
		t.Fatal("stale declaration control was not changed")
	}
	if err := os.WriteFile(declaration, []byte(changed), 0644); err != nil {
		t.Fatal(err)
	}
	stale := run("command", "add-note", "must not use stale models")
	if stale.ExitCode != 1 || stale.Stdout != "" || stale.Stderr != "project_command_error/generated_bundle_stale\n" {
		t.Fatal("stale bundle reached command", stale.ExitCode, stale.Stderr)
	}
	if err := os.WriteFile(declaration, beforeDeclaration, 0644); err != nil {
		t.Fatal(err)
	}
	assertStartedCommandRows(t, project, workspace, environment, 5, "saved before output error")
}

func assertStartedCommandRows(t *testing.T, project, workspace string, environment []string, expected int, title string) {
	t.Helper()
	source := fmt.Sprintf(`package main
import (
 "context"
 "errors"
 "fmt"
 "os"
 "github.com/progresshans/godj/settings"
 "github.com/progresshans/godj/settings/database"
 "example.com/started-site/internal/config"
 "example.com/started-site/apps/line_items/models"
)
func main() { if err := run(); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) } }
func run() (result error) {
 ctx := context.Background()
 environment, err := settings.NewEnvironment(os.Environ()); if err != nil { return err }
 selected, err := config.Database(environment); if err != nil { return err }
 backend, err := database.Open(ctx, selected); if err != nil { return err }
 defer func() { result = errors.Join(result, backend.Close()) }()
 rows, err := models.NoteObjects.Using(backend).All(ctx)
 if err != nil || len(rows) != %d { return errors.New("stored row count changed") }
 found := %q == ""
 for _, row := range rows { if row.Title == %q { found = true } }
 if !found { return errors.New("confirmed command write missing") }
 fmt.Println("command-data-preserved")
 return nil
}
`, expected, title, title)
	writeStartedAppSource(t, project, "cmd/appcheck/main.go", source)
	runStartedAppProgram(t, project, workspace, environment, "command-data-preserved\n")
}

const startedAppCommandSource = `package commands
import (
 "context"
 "errors"
 "fmt"
 "github.com/progresshans/godj/apps"
 "github.com/progresshans/godj/project/command"
 "github.com/progresshans/godj/settings"
 "github.com/progresshans/godj/settings/database"
 "example.com/started-site/internal/config"
 "example.com/started-site/project"
 "example.com/started-site/apps/line_items/models"
)
func Registry(environment settings.Environment) (command.Registry, error) {
 installed, err := apps.New(project.InstalledApps()); if err != nil { return command.Registry{}, err }
 selected, selectionErr := config.Database(environment)
 return command.New(command.Config{Apps: installed, Check: func(context.Context) error { return selectionErr }, Commands: []command.Definition{{
  Name: "add-note", AppLabel: "line_items", Summary: "Create a note in the current project database",
  Arguments: []command.Argument{{Name:"title", Help:"note title", Kind:command.String, Required:true}},
  Options: []command.Option{{Name:"summary", Help:"optional summary", Kind:command.String}, {Name:"fail-after-write", Help:"exercise post-commit failure", Kind:command.Boolean}},
  Handle: command.Using(func(ctx context.Context) (command.Resource[database.Backend], error) {
   backend, err := database.Open(ctx, selected); if err != nil { return command.Resource[database.Backend]{}, err }
   return command.Resource[database.Backend]{Value:backend, Close:func(context.Context) error { return backend.Close() }}, nil
  }, func(ctx context.Context, backend database.Backend, args command.Values, output command.Streams) error {
   title, _ := args.String("title")
   create := models.NewNoteCreate(title)
   if summary, provided := args.String("summary"); provided { create = create.WithSummary(summary) }
   if _, err := models.NoteObjects.Create(ctx, backend, create); err != nil { return err }
   if fail, _ := args.Boolean("fail-after-write"); fail { return errors.New("private-database-detail") }
   _, err := fmt.Fprintln(output.Stdout, "note-created")
   return err
  }),
 }}})
}
`
