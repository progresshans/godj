//go:build linux && godj_starter_browser

package runserverproduct_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/progresshans/godj/conformance/internal/testprocess"
	"github.com/progresshans/godj/internal/testenv"
	_ "modernc.org/sqlite"
)

const (
	starterOperator = "operator@example.test"
	starterReader   = "reader@example.test"
	starterPassword = "Slate-Meadow-946!private"
)

// This CI owner has a locked Chromium installation and a real PostgreSQL
// service. Each model selection starts with a public startproject directory.
func TestStarterSelectedAccountBrowser(t *testing.T) {
	repo := runserverRepositoryRoot(t)
	global := buildGlobalGodj(t, repo)
	tools := os.Getenv("GODJ_STARTER_BROWSER_TOOLS")
	if !filepath.IsAbs(tools) || os.Getenv(runserverPostgresTestURLEnv) == "" {
		t.Fatal("starter browser requires locked browser tools and PostgreSQL")
	}
	for _, variant := range []string{"default", "custom"} {
		t.Run(variant, func(t *testing.T) {
			workspaces := t.TempDir()
			env := testenv.Without(runserverEnvironment(t, "", workspaces), articleSQLiteDatabaseEnv,
				"GODJ_SQLITE_DATABASE", "GODJ_POSTGRES_URL", "GODJ_POSTGRES_SCHEMA",
				runserverPostgresTestURLEnv, runserverPostgresRequiredEnv, "GODJ_STARTER_BROWSER_TOOLS", "GODJ_SOURCE_HEAD")
			parent := t.TempDir()
			project := filepath.Join(parent, "site")
			app := starterBrowserProject{global: global, root: project, workspaces: workspaces, env: env, tools: tools,
				fixture: filepath.Join(repo, "conformance/runserverproduct/testdata/starteraccount")}
			app.cli(t, parent, env, "startproject", "example.com/started-site", "site", "--framework-source", repo)
			copySource := func(from, to string) {
				t.Helper()
				source, err := os.ReadFile(from)
				if err != nil {
					t.Fatal(err)
				}
				writeStartedAppSource(t, project, to, string(source))
			}
			copySource(filepath.Join(app.fixture, "internal/site/password_reset.go"), "internal/site/password_reset.go")
			if variant == "custom" {
				copySource(filepath.Join(repo, "internal/testschema/account.go"), "modeldef/testschema/account.go")
				for _, name := range []string{"modeldef/project.go", "modeldef/migrations.go", "internal/accounts/policy.go"} {
					copySource(filepath.Join(app.fixture, name), name)
				}
				app.cli(t, project, env, "generate")
				app.cli(t, project, env, "makemigrations")
			}
			for _, args := range [][]string{{"generate", "--check"}, {"check"}, {"migrations", "check"}} {
				app.cli(t, project, env, args...)
			}
			manifest, err := os.ReadFile(filepath.Join(project, ".godj/generated-manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("starter=%s generated manifest SHA256=%x", variant, sha256.Sum256(manifest))
			for _, vendor := range []string{"sqlite", "postgres"} {
				t.Run(vendor, func(t *testing.T) {
					app.exerciseDatabase(t, variant, vendor)
				})
			}
			if _, err := os.Stat(filepath.Join(project, "site.sqlite3")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("starter opened an unselected default database")
			}
		})
	}
}

type starterBrowserProject struct {
	global, root, workspaces, fixture, tools string
	env                                      []string
}

func (app starterBrowserProject) cli(t *testing.T, directory string, environment []string, args ...string) string {
	t.Helper()
	result := runProjectCLI(t, directory, environment, app.global, args...)
	assertRunserverWorkspaceEmpty(t, app.workspaces)
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatalf("public %s failed (exit %d, stderr bytes %d)", args[0], result.ExitCode, len(result.Stderr))
	}
	return result.Stdout
}

func (app starterBrowserProject) exerciseDatabase(t *testing.T, variant, vendor string) {
	t.Helper()
	selected := map[string]string{}
	if vendor == "sqlite" {
		selected["GODJ_SQLITE_DATABASE"] = filepath.Join(t.TempDir(), "site.sqlite3")
	} else {
		dsn := runserverPostgresTestURL(t)
		selected["GODJ_POSTGRES_URL"] = dsn
		selected["GODJ_POSTGRES_SCHEMA"] = createRunserverPostgresSchema(t, dsn)
	}
	address := reserveRunserverLoopbackAddress(t, "")
	mailbox := t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal("generate reset key")
	}
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, key, 0600); err != nil {
		t.Fatal(err)
	}
	clear(key)
	selected["STARTER_MAILBOX"] = mailbox
	selected["STARTER_RESET_KEY_FILE"] = keyFile
	selected["STARTER_RESET_ORIGIN"] = "http://" + address
	environment := testenv.With(app.env, selected)
	app.cli(t, app.root, environment, "migrate")
	if app.cli(t, app.root, environment, "command", "count-groups") != "0\n" {
		t.Fatal("initial catalog contains groups")
	}
	descriptor := filepath.Join(app.root, "godj.toml")
	if variant == "custom" {
		provisionRunserverAccount(t, app.global, app.root, descriptor, environment, starterPassword,
			operatorAnswer{`"Contact": `, starterOperator}, operatorAnswer{`"Region": `, "NZ"})
	} else {
		provisionRunserverOperator(t, app.global, app.root, descriptor, environment, starterOperator, starterPassword)
	}
	assertRunserverWorkspaceEmpty(t, app.workspaces)
	for _, phase := range []string{"initial", "restart"} {
		if !t.Run(phase, func(t *testing.T) {
			secrets := []string{starterPassword, "Sunlit-Cliff-812!private", "Willow-Grove-829!private", "Linen-Forest-437!private"}
			if dsn := selected["GODJ_POSTGRES_URL"]; dsn != "" {
				secrets = append(secrets, dsn)
			}
			runGlobalProjectServer(t, app.global, app.root, descriptor, address, environment, "site listening on http://", &secrets, func(address string) error {
				if err := readStarterAccount(address, variant, phase, &secrets); err != nil {
					return err
				}
				return app.probe(t, address, variant, phase, mailbox)
			})
			assertRunserverWorkspaceEmpty(t, app.workspaces)
			checkStarterRows(t, variant, phase, selected)
			if app.cli(t, app.root, environment, "command", "count-groups") != "1\n" {
				t.Fatal("selected group count differs after shutdown")
			}
		}) {
			break
		}
	}
}

func (app starterBrowserProject) probe(t *testing.T, address, variant, phase, mailbox string) error {
	t.Helper()
	// Linux Chromium puts its profile's Unix socket below TMPDIR. A nested
	// subtest name can consume the socket path limit before Chromium starts.
	browserTemp, err := os.MkdirTemp("/tmp", "godj-chrome-")
	if err != nil {
		return err
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(browserTemp); err != nil {
			t.Errorf("remove private Chromium temporary directory: %v", err)
		}
	})
	environment := testenv.With(app.env, map[string]string{"TMPDIR": browserTemp})
	probe := testprocess.Run(t, testprocess.CommandLimits{Timeout: 3 * time.Minute, Output: 8192}, app.fixture, environment,
		"node", "browser.cjs", app.tools, "http://"+address, variant, phase, mailbox)
	var receipt struct {
		BrowserVersion string `json:"browser_version"`
		Variant        string `json:"variant"`
		Phase          string `json:"phase"`
	}
	if probe.ExitCode != 0 || json.Unmarshal([]byte(probe.Stdout), &receipt) != nil || receipt.Variant != variant || receipt.Phase != phase || receipt.BrowserVersion == "" {
		return fmt.Errorf("Chromium %s/%s exit=%d: %s", variant, phase, probe.ExitCode, probe.Stderr)
	}
	t.Logf("Chromium %s", receipt.BrowserVersion)
	return nil
}

// This client owns its cookies independently of Chromium and uses the public
// login form and JSON representation in both server processes.
func readStarterAccount(address, variant, phase string, secrets *[]string) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	base := "http://" + address
	form, err := requestAuthenticatedRunserverEventually(client, http.MethodGet, base+"/admin/login/", "", "")
	if err != nil || form.status != 200 {
		return fmt.Errorf("operator login form status=%d err=%v", form.status, err)
	}
	match := runserverAdminLoginCSRFPattern.FindSubmatch(form.body)
	if len(match) != 2 {
		return errors.New("operator login form omitted CSRF")
	}
	*secrets = append(*secrets, string(match[1]))
	login, err := loginAuthenticatedRunserver(client, base+"/admin/login/", url.Values{
		"username": {starterOperator}, "password": {starterPassword}, "csrfmiddlewaretoken": {string(match[1])}, "next": {"/admin/"},
	}.Encode())
	if err != nil || login.status != 302 || login.header.Get("Location") != "/admin/" {
		return errors.New("independent operator login failed")
	}
	for _, cookie := range login.cookies {
		*secrets = append(*secrets, cookie.Value)
	}
	list, err := requestAuthenticatedRunserver(client, http.MethodGet, base+"/api/identity/accounts/", "application/json", "")
	if err != nil || list.status != 200 {
		return errors.New("independent account listing failed")
	}
	var page struct {
		Count int `json:"count"`
		Items []struct {
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"items"`
	}
	count := 1
	if phase == "restart" {
		count = 2
	}
	if json.Unmarshal(list.body, &page) != nil || page.Count != count || len(page.Items) != count {
		return errors.New("independent account listing has unexpected members")
	}
	loginField, profileField, operatorProfile, active := "username", "first_name", "", "active"
	if variant == "custom" {
		loginField, profileField, operatorProfile, active = "contact", "region", "NZ", "enabled"
	}
	observed := make(map[string]string)
	for _, row := range page.Items {
		for _, private := range []string{"principal_id", "encoded_password", "subject", "secret"} {
			if _, found := row.Fields[private]; found {
				return errors.New("account listing exposed a private field")
			}
		}
		var name, profile string
		var enabled bool
		if json.Unmarshal(row.Fields[loginField], &name) != nil || json.Unmarshal(row.Fields[profileField], &profile) != nil ||
			json.Unmarshal(row.Fields[active], &enabled) != nil || !enabled {
			return errors.New("account listing differs from the selected profile and active state")
		}
		observed[name] = profile
	}
	if v, ok := observed[starterOperator]; !ok || v != operatorProfile || len(observed) != count ||
		phase == "restart" && observed[starterReader] != "Updated" {
		return errors.New("operator or restarted profile differs")
	}
	return nil
}

// Observe only the isolated native database after the server process exits.
func checkStarterRows(t *testing.T, variant, phase string, settings map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var database *sql.DB
	var err error
	parameter := "?"
	legacyTable := "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='godj_identity_user'"
	if path := settings["GODJ_SQLITE_DATABASE"]; path != "" {
		u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
		database, err = sql.Open("sqlite", u.String())
	} else {
		cfg, parseErr := pgx.ParseConfig(settings["GODJ_POSTGRES_URL"])
		if parseErr != nil {
			t.Fatal("invalid observer connection")
		}
		cfg.RuntimeParams["search_path"] = settings["GODJ_POSTGRES_SCHEMA"]
		database = stdlib.OpenDB(*cfg)
		parameter = "$1"
		legacyTable = "SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='godj_identity_user'"
	}
	if err != nil {
		t.Fatal("open independent database observer")
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error("close independent database observer")
		}
	}()
	table, login, profile, active, password := "godj_identity_user", "username", "first_name", "active", "encoded_password"
	teamTable, teamName, operatorProfile, wantLegacy := "godj_identity_group", "name", "", 1
	if variant == "custom" {
		table, login, profile, active, password = "people_member", "contact", "region", "enabled", "secret"
		teamTable, teamName, operatorProfile, wantLegacy = "access_team", "title", "NZ", 0
	}
	var installed int
	if err := database.QueryRowContext(ctx, legacyTable).Scan(&installed); err != nil || installed != wantLegacy {
		t.Fatal("unexpected default User table inventory")
	}
	var team string
	var teams int
	if err := database.QueryRowContext(ctx, "SELECT count(*), min("+teamName+") FROM "+teamTable).Scan(&teams, &team); err != nil || teams != 1 || team != "Schema readers" {
		t.Fatal("durable selected group differs")
	}
	groupLinks, model := "godj_identity_user_groups", "godj_identity.user"
	if variant == "custom" {
		groupLinks, model = "people_member_teams", "people.member"
	}
	wantLinks, wantDeletes := 1, 0
	if phase == "restart" {
		wantLinks, wantDeletes = 0, 1
	}
	var links, deletes int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM "+groupLinks).Scan(&links); err != nil || links != wantLinks {
		t.Fatal("durable account group membership count differs")
	}
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM godj_account_audit_event WHERE model = "+parameter+" AND action = 'delete'", model).Scan(&deletes); err != nil || deletes != wantDeletes {
		t.Fatal("durable account deletion audit differs")
	}
	rows, err := database.QueryContext(ctx, "SELECT "+login+", "+profile+", "+active+", "+password+" FROM "+table+" ORDER BY "+login)
	if err != nil {
		t.Fatal("read durable selected accounts")
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name, value, hash string
		var enabled bool
		if err := rows.Scan(&name, &value, &enabled, &hash); err != nil {
			t.Fatal("scan durable selected account")
		}
		want := operatorProfile
		if name == starterReader {
			want = "Updated"
		}
		if value != want || !enabled || !strings.HasPrefix(hash, "pbkdf2_sha256$") {
			t.Fatal("durable selected profile, status or credential differs")
		}
		names = append(names, name)
	}
	want := []string{starterOperator}
	if phase == "initial" {
		want = append(want, starterReader)
	}
	if rows.Err() != nil || !slices.Equal(names, want) {
		t.Fatal("durable account inventory differs")
	}
}
