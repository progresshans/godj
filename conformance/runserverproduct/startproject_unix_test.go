//go:build darwin || linux

package runserverproduct_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/testprocess"
	"github.com/progresshans/godj/internal/testenv"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestGlobalStartProjectUsesPublicBootstrapAndAuthenticatedRuntime(t *testing.T) {
	repository := runserverRepositoryRoot(t)
	global := buildGlobalGodj(t, repository)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(parent, "new-site")
	workspace := t.TempDir()
	values := testenv.Map(runserverEnvironment(t, "unused-article.sqlite3", workspace))
	for _, key := range []string{articleSQLiteDatabaseEnv, "GODJ_SQLITE_DATABASE", "GODJ_POSTGRES_URL", "GODJ_POSTGRES_SCHEMA"} {
		delete(values, key)
	}
	environment := testenv.Sorted(values)
	run := func(t *testing.T, directory string, args ...string) testprocess.CommandResult {
		t.Helper()
		result := runProjectCLI(t, directory, environment, global, args...)
		assertRunserverWorkspaceEmpty(t, workspace)
		return result
	}
	created := run(t, parent, "startproject", "example.com/started-site", "new-site", "--framework-source", repository)
	var result struct {
		FormatVersion int    `json:"format_version"`
		Status        string `json:"status"`
		Directory     string `json:"directory"`
	}
	if created.ExitCode != 0 || created.Stderr != "" || json.Unmarshal([]byte(created.Stdout), &result) != nil || result.FormatVersion != 1 || result.Status != "created" || result.Directory != project {
		t.Fatalf("public startproject result: exit=%d stdout=%q stderr=%q", created.ExitCode, created.Stdout, created.Stderr)
	}
	for _, name := range []string{"README.md", "go.mod", "go.sum", "godj.toml", "modeldef/apps.go", "cmd/projectrunner/main.go", "cmd/site/main.go", "project/zz_godj_bindings.go", ".godj/generated-manifest.json"} {
		if _, err := os.Stat(filepath.Join(project, name)); err != nil {
			t.Fatal("scaffold omitted required source", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(project, "site.sqlite3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("startproject performed database I/O", err)
	}
	t.Run("compatibility before configuration and data I/O", func(t *testing.T) {
		exerciseStartupCompatibilityBeforeIO(t, global, project, workspace, environment)
	})
	for _, args := range [][]string{{"commands"}, {"help", "count-groups"}, {"check"}} {
		checked := run(t, project, args...)
		if checked.ExitCode != 0 || checked.Stderr != "" {
			t.Fatalf("resource-free %s: %d %s", args[0], checked.ExitCode, checked.Stderr)
		}
		if args[0] != "check" && !strings.Contains(checked.Stdout, "count-groups") {
			t.Fatal("command help omitted declared action")
		}
	}
	if _, err := os.Lstat(filepath.Join(project, "site.sqlite3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("list, help or pure check opened the database", err)
	}
	t.Run("explicit checks preserve an unopened database", func(t *testing.T) {
		exerciseStartedChecksBeforeDatabase(t, global, project, workspace, environment)
	})
	before := snapshotRunserverProjectTree(t, project)
	conflict := run(t, parent, "startproject", "example.com/other", "new-site", "--framework-source", repository)
	if conflict.ExitCode != 1 || conflict.Stdout != "" || conflict.Stderr != "startproject_error/destination_exists\n" || !reflect.DeepEqual(before, snapshotRunserverProjectTree(t, project)) {
		t.Fatal("conflicting scaffold changed the existing project", conflict.ExitCode)
	}
	for _, args := range [][]string{{"generate", "--check"}, {"migrations", "check"}, {"migrate"}} {
		result := run(t, project, args...)
		if result.ExitCode != 0 || result.Stderr != "" {
			t.Fatalf("public command %s failed: %d %s", args[0], result.ExitCode, result.Stderr)
		}
	}
	assertStartedDatabaseCheckPreservesTree(t, global, project, workspace, environment)
	descriptor := filepath.Join(project, "godj.toml")
	address := reserveRunserverLoopbackAddress(t, "")
	runGlobalProjectServer(t, global, project, descriptor, address, environment, "site listening on http://", nil, func(address string) error {
		client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		defer client.CloseIdleConnections()
		home, err := requestAuthenticatedRunserverEventually(client, http.MethodGet, "http://"+address+"/", "", "")
		if err != nil || home.status != 200 {
			return fmt.Errorf("unprovisioned project home: %d %w", home.status, err)
		}
		admin, err := requestAuthenticatedRunserver(client, http.MethodGet, "http://"+address+"/admin/", "", "")
		if err != nil || admin.status != 404 {
			return fmt.Errorf("unprovisioned Admin publication: %d %w", admin.status, err)
		}
		return nil
	})
	assertRunserverWorkspaceEmpty(t, workspace)
	const username, password = "starter-admin", "Scaffold-Admin-734!private"
	provisionRunserverOperator(t, global, project, descriptor, environment, username, password)
	assertRunserverWorkspaceEmpty(t, workspace)
	loggedRuntime := testenv.With(environment, map[string]string{"GODJ_LOG_LEVEL": "info"})
	var previousInvocation string
	for attempt := range 2 {
		address = reserveRunserverLoopbackAddress(t, address)
		sensitive := []string{password}
		invocation := runGlobalProjectServer(t, global, project, descriptor, address, loggedRuntime, "site listening on http://", &sensitive, func(address string) error {
			return exerciseStartedIdentity(address, username, password, attempt == 0, &sensitive)
		})
		if invocation == "" || invocation == previousInvocation {
			t.Fatal("server restart reused logging invocation identity")
		}
		previousInvocation = invocation
		assertRunserverWorkspaceEmpty(t, workspace)
	}
	counted := run(t, project, "command", "count-groups")
	if counted.ExitCode != 0 || counted.Stdout != "1\n" || counted.Stderr != "" {
		t.Fatalf("generated-model management command: %d %q %q", counted.ExitCode, counted.Stdout, counted.Stderr)
	}
	assertStartedDatabaseCheckPreservesTree(t, global, project, workspace, environment)
	after := snapshotRunserverProjectTree(t, project)
	for name, original := range before {
		if strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum" || name == "godj.toml" || name == ".godj/generated-manifest.json" {
			if current := after[name]; current != original {
				t.Fatal("runtime rewrote scaffold source", name)
			}
		}
	}
	t.Run("explicit_app_registration_and_existing_data", func(t *testing.T) {
		exerciseStartedApp(t, global, project, workspace, environment, username, password)
	})
	t.Run("published_output_failure_preserves_complete_project", func(t *testing.T) {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		failed := testprocess.RunWithStdout(t, testprocess.CommandLimits{Timeout: runserverScaffoldTimeout, Output: maximumRunserverOutput},
			parent, environment, writer,
			global, "startproject", "example.com/output-failed-site", "output-failed", "--framework-source", repository)
		assertRunserverWorkspaceEmpty(t, workspace)
		if failed.ExitCode != 1 || failed.Stdout != "" || failed.Stderr != "startproject_error/published_output_failed\n" {
			t.Fatalf("published output failure: exit=%d stdout=%q stderr=%q", failed.ExitCode, failed.Stdout, failed.Stderr)
		}
		created := filepath.Join(parent, "output-failed")
		check := run(t, created, "generate", "--check")
		if check.ExitCode != 0 || check.Stderr != "" {
			t.Fatal("output failure removed or invalidated the committed bundle", check.ExitCode, check.Stderr)
		}
		if _, err := os.Lstat(filepath.Join(created, "site.sqlite3")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("output failure path performed database I/O", err)
		}
		original := snapshotRunserverProjectTree(t, created)
		retry := run(t, parent, "startproject", "example.com/output-retry", "output-failed", "--framework-source", repository)
		if retry.ExitCode != 1 || retry.Stderr != "startproject_error/destination_exists\n" || !reflect.DeepEqual(original, snapshotRunserverProjectTree(t, created)) {
			t.Fatal("retry replaced a known published project")
		}
	})
}

func exerciseStartedIdentity(address, username, password string, create bool, sensitive *[]string) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	base := "http://" + address
	groups := base + "/api/identity/groups/"
	anonymous, err := requestAuthenticatedRunserverEventually(client, http.MethodGet, groups, "application/json", "")
	if err != nil || anonymous.status != 403 {
		return fmt.Errorf("anonymous identity API: %d %w", anonymous.status, err)
	}
	loginPage, err := requestAuthenticatedRunserver(client, http.MethodGet, base+"/admin/login/", "", "")
	if err != nil || loginPage.status != 200 {
		return fmt.Errorf("scaffold login page: %d %w", loginPage.status, err)
	}
	match := runserverAdminLoginCSRFPattern.FindSubmatch(loginPage.body)
	if len(match) != 2 {
		return errors.New("scaffold login omitted CSRF")
	}
	*sensitive = append(*sensitive, string(match[1]))
	login, err := requestAuthenticatedRunserver(client, http.MethodPost, base+"/admin/login/", "", url.Values{
		"username": {username}, "password": {password}, "csrfmiddlewaretoken": {string(match[1])}, "next": {"/admin/"},
	}.Encode())
	if err != nil || login.status != 302 || login.header.Get("Location") != "/admin/" {
		return fmt.Errorf("scaffold login: %d %w", login.status, err)
	}
	for _, cookie := range login.cookies {
		*sensitive = append(*sensitive, cookie.Value)
	}
	admin, err := requestAuthenticatedRunserver(client, http.MethodGet, base+"/admin/", "", "")
	if err != nil || admin.status != 200 || !bytes.Contains(admin.body, []byte(`href="/admin/groups/"`)) {
		return fmt.Errorf("scaffold identity Admin: %d %w", admin.status, err)
	}
	list, err := requestAuthenticatedRunserver(client, http.MethodGet, groups, "application/json", "")
	if err != nil || list.status != 200 {
		return fmt.Errorf("scaffold group read: %d %w", list.status, err)
	}
	if create {
		csrf := list.header.Get(websessionauth.DefaultCSRFHeader)
		if !runserverMaskedCSRFPattern.MatchString(csrf) {
			return errors.New("scaffold API omitted masked CSRF")
		}
		*sensitive = append(*sensitive, csrf)
		for _, valid := range []bool{false, true} {
			request, err := http.NewRequest(http.MethodPost, groups, strings.NewReader(`{"name":"Starter operators","permissions":[]}`))
			if err != nil {
				return err
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json")
			if valid {
				request.Header.Set(websessionauth.DefaultCSRFHeader, csrf)
			}
			response, err := client.Do(request)
			if err != nil {
				return err
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 65537))
			closeErr := response.Body.Close()
			if err := errors.Join(readErr, closeErr); err != nil {
				return err
			}
			expected := 403
			if valid {
				expected = 201
			}
			if response.StatusCode != expected || len(body) > 65536 {
				return fmt.Errorf("scaffold group write status: %d, want %d", response.StatusCode, expected)
			}
		}
		list, err = requestAuthenticatedRunserver(client, http.MethodGet, groups, "application/json", "")
		if err != nil || list.status != 200 {
			return fmt.Errorf("scaffold stored group read: %d %w", list.status, err)
		}
	}
	var page struct {
		Count int64 `json:"count"`
		Items []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.body, &page); err != nil || page.Count != 1 || len(page.Items) != 1 || page.Items[0].ID <= 0 || page.Items[0].Name != "Starter operators" {
		return errors.New("scaffold API write or persisted restart data differs")
	}
	adminGroups, err := requestAuthenticatedRunserver(client, http.MethodGet, base+"/admin/groups/", "", "")
	if err != nil || adminGroups.status != 200 || !bytes.Contains(adminGroups.body, []byte("Starter operators")) {
		return fmt.Errorf("scaffold Admin did not expose the stored group: status=%d, error=%v", adminGroups.status, err)
	}
	return nil
}
