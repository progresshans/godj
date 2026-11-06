//go:build darwin || linux

package runserverproduct_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/testprocess"
)

func exerciseStartupCompatibilityBeforeIO(t *testing.T, global, project, workspace string, environment []string) {
	t.Helper()
	entry := filepath.Join(project, "cmd/projectrunner/main.go")
	before, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.WriteFile(entry, before, 0644); err != nil {
			t.Error(err)
		}
	}()
	source := `package main
import("encoding/json";"os";"github.com/progresshans/godj/project/compatibility")
func main(){
 if len(os.Args)==2&&os.Args[1]==compatibility.PrivateArgument{
  info:=compatibility.Current();info.Contract.Schema++
  if json.NewEncoder(os.Stdout).Encode(info)!=nil{os.Exit(1)};return
 }
 if err:=os.WriteFile("unexpected-project-entry",[]byte("entered"),0600);err!=nil{os.Exit(2)}
}
`
	if err := os.WriteFile(entry, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotRunserverProjectTree(t, project)
	for _, args := range [][]string{{"migrations", "check"}, {"generate"}, {"makemigrations"}, {"migrate"}, {"showmigrations"}, {"sqlmigrate", "site", "0001_initial"}, {"createsuperuser"}, {"runserver", "--addr", "127.0.0.1:0"}, {"commands"}} {
		result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}, project, environment, global, args...)
		category, exit := "project_compatibility_error", 2
		if args[0] == "commands" {
			category, exit = "project_command_error", 1
		}
		wantStderr := category + "/framework_incompatible\n"
		if args[0] == "createsuperuser" {
			exit = 3
			wantStderr = `{"operation_id":"","status":"not_created","code":"framework_incompatible","cleanup_failed":false,"file_report_failed":false}` + "\n"
		}
		if result.ExitCode != exit || result.Stdout != "" || result.Stderr != wantStderr {
			t.Fatalf("%s bypassed compatibility: exit=%d stdout=%q stderr=%q", args[0], result.ExitCode, result.Stdout, result.Stderr)
		}
		assertRunserverWorkspaceEmpty(t, workspace)
		if _, err := os.Stat(filepath.Join(project, "unexpected-project-entry")); !os.IsNotExist(err) {
			t.Fatal("incompatible declaration entered business branch", err)
		}
		if _, err := os.Stat(filepath.Join(project, "site.sqlite3")); !os.IsNotExist(err) {
			t.Fatal("incompatible declaration opened database", err)
		}
	}
	after := snapshotRunserverProjectTree(t, project)
	if len(snapshot) != len(after) {
		t.Fatal("compatibility rejection changed project files")
	}
	for key, value := range snapshot {
		if after[key] != value {
			t.Fatal("compatibility rejection changed project", key)
		}
	}
	// A malformed child response stays private and cannot trigger regeneration.
	invalid := strings.Replace(source, "info:=compatibility.Current();info.Contract.Schema++\n  if json.NewEncoder(os.Stdout).Encode(info)!=nil{os.Exit(1)};return", "_ = json.NewEncoder(os.Stdout)\n  _,_ = os.Stdout.WriteString(\"private-metadata-canary\");return", 1)
	if invalid == source {
		t.Fatal("malformed response control did not change source")
	}
	if err := os.WriteFile(entry, []byte(invalid), 0644); err != nil {
		t.Fatal(err)
	}
	result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}, project, environment, global, "generate")
	if result.ExitCode != 3 || result.Stdout != "" || result.Stderr != "project_compatibility_error/compatibility_response_invalid\n" {
		t.Fatalf("malformed probe escaped closed diagnostics: %+v", result)
	}
	assertRunserverWorkspaceEmpty(t, workspace)
}
