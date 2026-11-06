//go:build darwin || linux

package runserverproduct_test

import (
	"encoding/json"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/testprocess"
)

func exerciseStartedApp(t *testing.T, global, project, workspace string, environment []string, username, password string) {
	t.Helper()
	run := func(args ...string) testprocess.CommandResult {
		t.Helper()
		result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}, project, environment, global, args...)
		assertRunserverWorkspaceEmpty(t, workspace)
		if result.ExitCode != 0 || result.Stderr != "" {
			t.Fatalf("app command %s: exit=%d stderr=%q", args[0], result.ExitCode, result.Stderr)
		}
		return result
	}
	before := snapshotRunserverProjectTree(t, project)
	created := run("startapp", "line_items", "apps/line_items")
	var outcome struct {
		Status    string `json:"status"`
		AppLabel  string `json:"app_label"`
		Installed bool   `json:"installed"`
	}
	if json.Unmarshal([]byte(created.Stdout), &outcome) != nil || outcome.Status != "created" || outcome.AppLabel != "line_items" || outcome.Installed {
		t.Fatal("app source creation was confused with project installation")
	}
	after := snapshotRunserverProjectTree(t, project)
	for name, hash := range before {
		if hash.Mode.IsDir() {
			current := after[name]
			if current.Mode != hash.Mode || current.Device != hash.Device || current.Inode != hash.Inode {
				t.Fatal("startapp replaced an existing directory", name)
			}
			continue // Adding the new app changes the parent's directory timestamps.
		}
		if after[name] != hash {
			t.Fatal("startapp changed an existing project file", name)
		}
	}
	for name := range after {
		if _, existed := before[name]; !existed && !strings.HasPrefix(name, "apps/line_items/") {
			t.Fatal("startapp wrote outside its new app directory", name)
		}
	}
	run("generate", "--check") // Creating unregistered source is not model drift.
	writeStartedAppSource(t, project, "modeldef/apps.go", `package modeldef
import (
 "github.com/progresshans/godj/schema"
 appdef "example.com/started-site/apps/line_items/modeldef"
)
func Applications() []schema.Definition {
 return []schema.Definition{{AppLabel: "site"}, appdef.Definition()}
}
`)
	run("generate")
	run("migrations", "check")
	writeStartedAppSource(t, project, "cmd/appcheck/main.go", `package main
import (
 "fmt"
 "os"
 "example.com/started-site/project"
 "example.com/started-site/apps/line_items/models"
)
func main() {
 count := 0
 for _, app := range project.InstalledApps() { if app.Label == "line_items" { count++ } }
 if count != 1 || len(models.GoDjRelationSchema().Models) != 0 { os.Exit(1) }
 fmt.Println("empty-app-installed")
}
`)
	runStartedAppProgram(t, project, workspace, environment, "empty-app-installed\n")
	writeStartedAppSource(t, project, "apps/line_items/modeldef/schema.go", startedAppDefinition(false))
	for _, args := range [][]string{{"generate"}, {"makemigrations"}, {"migrations", "check"}, {"migrate"}} {
		run(args...)
	}
	writeStartedAppSource(t, project, "cmd/appcheck/main.go", startedAppProgram(false))
	runStartedAppProgram(t, project, workspace, environment, "first-model-stored\n")
	writeStartedAppSource(t, project, "apps/line_items/modeldef/schema.go", startedAppDefinition(true))
	for _, args := range [][]string{{"generate"}, {"makemigrations"}, {"migrations", "check"}, {"migrate"}, {"generate", "--check"}} {
		run(args...)
	}
	writeStartedAppSource(t, project, "cmd/appcheck/main.go", startedAppProgram(true))
	runStartedAppProgram(t, project, workspace, environment, "existing-model-preserved\n")
	exerciseStartedAppCommands(t, global, project, workspace, environment)
	address := reserveRunserverLoopbackAddress(t, "")
	sensitive := []string{password}
	runGlobalProjectServer(t, global, project, filepath.Join(project, "godj.toml"), address, environment,
		"site listening on http://", &sensitive, func(address string) error {
			return exerciseStartedIdentity(address, username, password, false, &sensitive)
		})
	assertRunserverWorkspaceEmpty(t, workspace)
}

func writeStartedAppSource(t *testing.T, project, relative, source string) {
	t.Helper()
	data, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal("consumer source did not format", relative, err)
	}
	name := filepath.Join(project, relative)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func runStartedAppProgram(t *testing.T, project, workspace string, environment []string, expected string) {
	t.Helper()
	result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 3 * time.Minute, Output: maximumRunserverOutput},
		project, environment, "go", "run", "-mod=readonly", "./cmd/appcheck")
	assertRunserverWorkspaceEmpty(t, workspace)
	if result.ExitCode != 0 || result.Stdout != expected || result.Stderr != "" {
		t.Fatalf("public generated app consumer: exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
}

func startedAppDefinition(changed bool) string {
	extra := ""
	if changed {
		extra = `schema.TextField("summary", "Summary", schema.Nullable()),`
	}
	return `package modeldef
import "github.com/progresshans/godj/schema"
func Definition() schema.Definition {
 return schema.Definition{AppLabel: "line_items", Models: []schema.Model{{Name: "note", GoName: "Note", Fields: []schema.Field{
  schema.CharField("title", "Title", 80),` + extra + `
 }}}}
}
`
}

func startedAppProgram(changed bool) string {
	body := `
 existing, err := models.NoteObjects.Using(backend).Count(ctx)
 if err != nil || existing != 0 { return errors.New("initial model state differs") }
 row, err := models.NoteObjects.Create(ctx, backend, models.NewNoteCreate("before field change"))
 if err != nil || row.ID <= 0 { return errors.New("first model write failed") }
 fmt.Println("first-model-stored")
`
	if changed {
		body = `
 rows, err := models.NoteObjects.Using(backend).All(ctx)
 if err != nil || len(rows) != 1 || rows[0].Title != "before field change" || rows[0].Summary != nil {
  return errors.New("schema change did not preserve existing data")
 }
 row, err := models.NoteObjects.Create(ctx, backend, models.NewNoteCreate("after field change").WithSummary("new field"))
 if err != nil || row.Summary == nil || *row.Summary != "new field" { return errors.New("changed model write failed") }
 count, err := models.NoteObjects.Using(backend).Count(ctx)
 if err != nil || count != 2 { return errors.New("changed model count differs") }
 fmt.Println("existing-model-preserved")
`
	}
	return `package main
import (
 "context"
 "errors"
 "fmt"
 "os"
 "github.com/progresshans/godj/db/sqlite"
 "example.com/started-site/apps/line_items/models"
)
func main() { if err := run(); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) } }
func run() (result error) {
 ctx := context.Background()
 backend, err := sqlite.Open(ctx, "site.sqlite3")
 if err != nil { return err }
 defer func() { result = errors.Join(result, backend.Close()) }()
` + body + `
 return nil
}
`
}
