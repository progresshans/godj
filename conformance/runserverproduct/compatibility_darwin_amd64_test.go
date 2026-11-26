package runserverproduct_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/testenv"
)

func TestNativeInstalledProjectLifecycle(t *testing.T) { nativeCompatibility(t, true) }
func TestNativeCGOFreeProjectLifecycle(t *testing.T)   { nativeCompatibility(t, false) }

func nativeCompatibility(t *testing.T, installed bool) {
	expectedCGO := "0"
	if installed {
		expectedCGO = "1"
	}
	if os.Getenv("CGO_ENABLED") != expectedCGO {
		t.Fatal("unexpected Intel compatibility mode")
	}
	repository := runserverRepositoryRoot(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, workspace := filepath.Join(root, "site"), filepath.Join(root, "workspaces")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	env := testenv.Without(runserverEnvironment(t, "unused", workspace), articleSQLiteDatabaseEnv, "GODJ_SQLITE_DATABASE", "GODJ_POSTGRES_URL", "GODJ_POSTGRES_SCHEMA")
	var global string
	phase := func(name string, f func(*testing.T)) {
		t.Helper()
		if !t.Run(name, f) {
			t.FailNow()
		}
	}
	const release = "v0.0.0-godj-native-test.1"
	var sourceArgs []string
	phase("cli_and_scaffold", func(t *testing.T) {
		if installed {
			var tools []string
			tools, env = installFixedModuleTools(t, repository, root, workspace, env, []string{release})
			global = tools[0]
		} else {
			global = filepath.Join(root, "godj")
			fixedModuleGo(t, workspace)(repository, env, "build", "-trimpath", "-o", global, "./cmd/godj")
			assertRunserverBuildCoordinate(t, global)
			sourceArgs = []string{"--framework-source", repository}
		}
		result := reloadCLI(t, global, root, env, append([]string{"startproject", "example.com/started-site", "site"}, sourceArgs...)...)
		var status struct{ Status, Directory string }
		if json.Unmarshal([]byte(result.Stdout), &status) != nil || status.Status != "created" || status.Directory != project {
			t.Fatal("unexpected public scaffold result")
		}
		if installed {
			fixedModuleProjectDependency(t, project, repository, release)
		}
		for _, name := range []string{"godj.toml", ".godj/generated-manifest.json", "cmd/site/main.go", "cmd/projectrunner/main.go", "cmd/commands/main.go"} {
			info, err := os.Stat(filepath.Join(project, name))
			if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
				t.Fatal("missing scaffold output", name)
			}
		}
		if installed {
			before := snapshotRunserverProjectTree(t, project)
			conflict := runProjectCLI(t, root, env, global, "startproject", "example.com/other", "site")
			if conflict.ExitCode != 1 || conflict.Stderr != "startproject_error/destination_exists\n" || !reflect.DeepEqual(before, snapshotRunserverProjectTree(t, project)) {
				t.Fatal("scaffold collision did not preserve destination")
			}
		}
	})
	const username, password = "native-operator", "Quartz-Canyon-962!native"
	phase("migrate_and_account", func(t *testing.T) {
		for _, args := range [][]string{{"generate", "--check"}, {"check"}, {"migrate"}, {"showmigrations"}} {
			result := reloadCLI(t, global, project, env, args...)
			if args[0] == "showmigrations" && !strings.Contains(result.Stdout, "0001_initial") {
				t.Fatal("applied migration missing")
			}
		}
		verifyNativeGroupRows(t, project, 0)
		provisionRunserverOperator(t, global, project, filepath.Join(project, "godj.toml"), env, username, password)
		assertRunserverWorkspaceEmpty(t, workspace)
	})
	phase("http_write_and_interrupt", func(t *testing.T) {
		p := startReloadProcess(t, global, project, env, "site listening on http://", false)
		sensitive := []string{password}
		if err := exerciseStartedIdentity(p.address, username, password, true, &sensitive); err != nil {
			t.Fatal(err)
		}
		p.stop(t, syscall.SIGINT)
		assertRunserverOutputExcludesSensitiveValues(t, p.stdout.String(), p.stderr.String(), &sensitive)
		assertRunserverWorkspaceEmpty(t, workspace)
		verifyNativeGroupRows(t, project, 1)
	})
	phase("command_and_reopen", func(t *testing.T) {
		command := filepath.Join(root, "commands")
		fixedModuleGo(t, workspace)(project, env, "build", "-trimpath", "-mod=readonly", "-o", command, "./cmd/commands")
		assertRunserverBuildCoordinate(t, command)
		if got := reloadCLI(t, command, project, env, "run", "count-groups"); got.Stdout != "1\n" {
			t.Fatal("compiled management command did not read HTTP write")
		}
		if got := reloadCLI(t, global, project, env, "command", "count-groups"); got.Stdout != "1\n" {
			t.Fatal("public management command lost stored row")
		}
		p := startReloadProcess(t, global, project, env, "site listening on http://", installed)
		sensitive := []string{password}
		if err := exerciseStartedIdentity(p.address, username, password, false, &sensitive); err != nil {
			t.Fatal(err)
		}
		if installed {
			const source = "internal/site/application.go"
			const marker = "Native reloaded site"
			replaceReloadSource(t, project, source, "Your GoDj project is running", marker)
			client := &http.Client{Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			active := func() bool {
				result, err := requestAuthenticatedRunserver(client, "GET", "http://"+p.address+"/", "", "")
				return err == nil && result.status == 200 && bytes.Contains(result.body, []byte(marker))
			}
			p.await(t, "new HTTP generation", active)
			p.track(t)
			before := strings.Count(p.stderr.String(), "project_runserver_reload_rejected/project_runtime_build_failed\n")
			writeReloadFile(t, project, "internal/site/native_invalid.go", "package site\nvar invalid = undefinedNativeFixtureSymbol\n")
			p.rejected(t, "project_runtime_build_failed", before)
			if !active() {
				t.Fatal("rejected generation replaced active app")
			}
			if err := exerciseStartedIdentity(p.address, username, password, false, &sensitive); err != nil {
				t.Fatal(err)
			}
		}
		p.stop(t, syscall.SIGINT)
		assertRunserverOutputExcludesSensitiveValues(t, p.stdout.String(), p.stderr.String(), &sensitive)
		assertRunserverWorkspaceEmpty(t, workspace)
		verifyNativeGroupRows(t, project, 1)
	})
}

func verifyNativeGroupRows(t *testing.T, project string, want int) {
	t.Helper()
	location := url.URL{Scheme: "file", Path: filepath.Join(project, "site.sqlite3"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM godj_identity_group").Scan(&count); err != nil || count != want {
		t.Fatal("independent SQLite group count", count, err)
	}
	if want == 1 {
		var name string
		if err := db.QueryRowContext(t.Context(), "SELECT name FROM godj_identity_group").Scan(&name); err != nil || name != "Starter operators" {
			t.Fatal("independent SQLite readback failed", err)
		}
	}
	t.Logf("GODJ_NATIVE_DB rows=%d", count)
}
