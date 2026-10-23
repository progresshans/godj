//go:build darwin || linux

package runserverproduct_test

import (
	"archive/zip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/testprocess"
	"github.com/progresshans/godj/internal/testenv"
	"github.com/progresshans/godj/project/compatibility"
	"github.com/progresshans/godj/version"
)

// These versions are private test-proxy fixtures made from the complete module
// source under test, not published releases. Both have the same source bytes so
// mismatches exercise fixed module identity independently of contract equality.
func TestGlobalFixedModuleInstallationCompatibility(t *testing.T) {
	repository := runserverRepositoryRoot(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspaces")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	environment := runserverEnvironment(t, "unused-article.sqlite3", workspace)
	environment = testenv.Without(environment, articleSQLiteDatabaseEnv, "GODJ_SQLITE_DATABASE", "GODJ_POSTGRES_URL", "GODJ_POSTGRES_SCHEMA")
	runGo := func(directory string, env []string, args ...string) testprocess.CommandResult {
		t.Helper()
		result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}, directory, env, "go", args...)
		if result.ExitCode != 0 {
			t.Fatalf("module fixture go %s: exit=%d stderr=%s", args[0], result.ExitCode, result.Stderr)
		}
		assertRunserverWorkspaceEmpty(t, workspace)
		return result
	}
	hostCaches := runGo(repository, environment, "env", "-json", "GOMODCACHE", "GOCACHE")
	var caches struct{ GOMODCACHE, GOCACHE string }
	if json.Unmarshal([]byte(hostCaches.Stdout), &caches) != nil || !filepath.IsAbs(caches.GOMODCACHE) || !filepath.IsAbs(caches.GOCACHE) {
		t.Fatal("module fixture requires explicit host cache paths")
	}
	proxy := filepath.Join(root, "proxy")
	versions := []string{"v0.0.0-godj-compatibility-test.1", "v0.0.0-godj-compatibility-test.2"}
	source := fixedModuleSources(t, repository, environment)
	for _, release := range versions {
		fixedModuleArchive(t, repository, proxy, release, source)
	}
	fixedModuleWrite(t, filepath.Join(proxy, filepath.FromSlash(version.Module), "@v/list"), []byte(strings.Join(versions, "\n")+"\n"))
	fileURL := func(path string) string { return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String() }
	flags := "-modcacherw"
	if fixedModuleRaceBuild() {
		flags += " -race"
	}
	environment = testenv.With(environment, map[string]string{
		"GOPROXY":    fileURL(proxy) + "," + fileURL(filepath.Join(caches.GOMODCACHE, "cache", "download")),
		"GOMODCACHE": filepath.Join(root, "module-cache"), "GOCACHE": caches.GOCACHE,
		"GOPATH": filepath.Join(root, "gopath"), "GOFLAGS": flags,
	})
	tools := make([]string, len(versions))
	for i, release := range versions {
		bin := filepath.Join(root, fmt.Sprintf("install-%d", i))
		if err := os.Mkdir(bin, 0700); err != nil {
			t.Fatal(err)
		}
		runGo(root, testenv.With(environment, map[string]string{"GOBIN": bin}), "install", version.Module+"/cmd/godj@"+release)
		tools[i] = filepath.Join(bin, "godj")
		fixedModuleBuildIdentity(t, tools[i], release, true)
		output := testprocess.Run(t, testprocess.CommandLimits{Timeout: 30 * time.Second, Output: maximumRunserverOutput}, root, environment, tools[i], "version", "--json")
		var doc struct {
			FormatVersion int          `json:"format_version"`
			Framework     version.Info `json:"framework"`
		}
		if output.ExitCode != 0 || output.Stderr != "" || json.Unmarshal([]byte(output.Stdout), &doc) != nil || doc.FormatVersion != 1 || doc.Framework.Origin != version.ModuleVersion || doc.Framework.Version != release || doc.Framework.ReplacementModule != "" || doc.Framework.RequestedVersion != "" {
			t.Fatal("installed tool does not identify its fixed module", output.ExitCode, output.Stderr, doc)
		}
	}
	project := filepath.Join(root, "site")
	run := func(binary, directory string, args ...string) testprocess.CommandResult {
		t.Helper()
		result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}, directory, environment, binary, args...)
		assertRunserverWorkspaceEmpty(t, workspace)
		return result
	}
	mustRun := func(binary, directory string, args ...string) testprocess.CommandResult {
		t.Helper()
		result := run(binary, directory, args...)
		if result.ExitCode != 0 || result.Stderr != "" {
			t.Fatalf("fixed module %s: exit=%d stderr=%s", args[0], result.ExitCode, result.Stderr)
		}
		return result
	}
	// No source replacement or explicit dependency option: the installed CLI's
	// own fixed module version selects the starter dependency.
	mustRun(tools[0], root, "startproject", "example.com/started-site", "site")
	fixedModuleProjectDependency(t, project, repository, versions[0])
	for _, args := range [][]string{{"generate", "--check"}, {"migrations", "check"}, {"check"}, {"commands"}, {"migrate"}, {"showmigrations"}} {
		mustRun(tools[0], project, args...)
	}
	sql := mustRun(tools[0], project, "sqlmigrate", "godj_identity", "0001_initial")
	if !strings.Contains(sql.Stdout, "CREATE TABLE") {
		t.Fatal("fixed module SQL command produced no schema")
	}
	count := mustRun(tools[0], project, "command", "count-groups")
	if count.Stdout != "0\n" {
		t.Fatal("fresh fixed module project has unexpected groups", count.Stdout)
	}
	const username, password = "fixed-module-admin", "Fixed-Module-734!private"
	descriptor := filepath.Join(project, "godj.toml")
	provisionRunserverOperator(t, tools[0], project, descriptor, environment, username, password)
	assertRunserverWorkspaceEmpty(t, workspace)
	exerciseRuntime := func(binary string, create bool) {
		t.Helper()
		address := reserveRunserverLoopbackAddress(t, "")
		sensitive := []string{password}
		runGlobalProjectServer(t, binary, project, descriptor, address, environment, "site listening on http://", &sensitive, func(address string) error {
			return exerciseStartedIdentity(address, username, password, create, &sensitive)
		})
		assertRunserverWorkspaceEmpty(t, workspace)
	}
	exerciseRuntime(tools[0], true)
	if count := mustRun(tools[0], project, "command", "count-groups"); count.Stdout != "1\n" {
		t.Fatal("fixed module command did not read stored HTTP data", count.Stdout)
	}
	assertBinaries := func(release string) {
		t.Helper()
		var prior compatibility.Contract
		for i, target := range []string{"./cmd/projectrunner", "./cmd/site", "./cmd/commands"} {
			binary := filepath.Join(root, fmt.Sprintf("linked-%d", i))
			runGo(project, environment, "build", "-mod=readonly", "-o", binary, target)
			fixedModuleBuildIdentity(t, binary, release, false)
			probe := run(binary, project, compatibility.PrivateArgument)
			info, err := compatibility.Parse([]byte(probe.Stdout))
			if err != nil || probe.ExitCode != 0 || probe.Stderr != "" || info.Framework.Origin != version.ModuleVersion || info.Framework.Version != release {
				t.Fatalf("fixed linked metadata %s: exit=%d err=%v", target, probe.ExitCode, err)
			}
			if i > 0 && info.Contract != prior {
				t.Fatal("independently built fixed runtimes have different contracts")
			}
			prior = info.Contract
		}
	}
	assertBinaries(versions[0])
	reject := func(binary string) {
		t.Helper()
		before := snapshotRunserverProjectTree(t, project)
		for _, args := range [][]string{{"generate"}, {"migrate"}, {"createsuperuser"}, {"runserver", "--addr", "127.0.0.1:0"}, {"commands"}} {
			result := run(binary, project, args...)
			category, exit := "project_compatibility_error", 2
			if args[0] == "commands" {
				category, exit = "project_command_error", 1
			}
			if result.ExitCode != exit || result.Stdout != "" || result.Stderr != category+"/framework_incompatible\n" {
				t.Fatalf("fixed mismatch %s was not rejected before I/O: %+v", args[0], result)
			}
		}
		if !reflect.DeepEqual(before, snapshotRunserverProjectTree(t, project)) {
			t.Fatal("mismatched fixed tool changed source or stored data")
		}
	}
	reject(tools[1])
	// The project owner explicitly changes the dependency. No global command is
	// permitted to perform this change on its own. This tests the two fixture
	// versions, not a blanket support claim for future framework upgrades.
	runGo(project, environment, "mod", "edit", "-require="+version.Module+"@"+versions[1])
	runGo(project, environment, "mod", "tidy")
	fixedModuleProjectDependency(t, project, repository, versions[1])
	mustRun(tools[1], project, "generate", "--check")
	mustRun(tools[1], project, "check")
	assertBinaries(versions[1])
	exerciseRuntime(tools[1], false)
	if count := mustRun(tools[1], project, "command", "count-groups"); count.Stdout != "1\n" {
		t.Fatal("explicit dependency change lost stored data", count.Stdout)
	}
	reject(tools[0])
	runGo(project, environment, "mod", "verify")
	t.Logf("fixed module compatibility: installed=%d linked_per_version=3 mismatched_commands_per_direction=5 stored_groups=1", len(versions))
}

func fixedModuleProjectDependency(t *testing.T, project, repository, release string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(project, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), version.Module+" "+release) || strings.Contains(string(data), "replace") || strings.Contains(string(data), repository) {
		t.Fatal("fixed project depends on an unversioned or replaced framework")
	}
}

func fixedModuleBuildIdentity(t *testing.T, binary, release string, main bool) {
	t.Helper()
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	race := false
	for _, setting := range info.Settings {
		if setting.Key == "-race" {
			race = setting.Value == "true"
		}
	}
	if race != fixedModuleRaceBuild() {
		t.Fatal("installed or linked executable lost the native race coordinate")
	}
	if main {
		if info.Main.Path != version.Module || info.Main.Version != release || info.Main.Replace != nil {
			t.Fatal("tool was not installed from the versioned module", info.Main)
		}
		return
	}
	matches := 0
	for _, dependency := range info.Deps {
		if dependency.Path == version.Module {
			matches++
			if dependency.Version != release || dependency.Replace != nil {
				t.Fatal("linked executable used a checkout or wrong version", dependency.Version)
			}
		}
	}
	if matches != 1 {
		t.Fatal("linked executable omitted framework build metadata")
	}
}

func fixedModuleSources(t *testing.T, repository string, environment []string) []string {
	t.Helper()
	result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 30 * time.Second, Output: maximumRunserverOutput}, repository, environment, "git", "ls-files", "-z")
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatal("cannot inventory module source", result.Stderr)
	}
	names := strings.Split(strings.TrimSuffix(result.Stdout, "\x00"), "\x00")
	nested := []string{}
	for _, name := range names {
		if name != "go.mod" && filepath.Base(name) == "go.mod" {
			nested = append(nested, filepath.ToSlash(filepath.Dir(name))+"/")
		}
	}
	selected := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, "../") {
			t.Fatal("invalid tracked module member")
		}
		omit := strings.Contains("/"+name+"/", "/vendor/")
		for _, prefix := range nested {
			omit = omit || strings.HasPrefix(name, prefix)
		}
		if omit {
			continue
		}
		info, err := os.Lstat(filepath.Join(repository, filepath.FromSlash(name)))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatal("module source is unavailable or irregular", name, err)
		}
		selected = append(selected, name)
	}
	sort.Strings(selected)
	if len(selected) == 0 {
		t.Fatal("empty module source inventory")
	}
	return selected
}

func fixedModuleArchive(t *testing.T, repository, proxy, release string, names []string) {
	t.Helper()
	directory := filepath.Join(proxy, filepath.FromSlash(version.Module), "@v")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, release+".zip")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	writer := zip.NewWriter(io.MultiWriter(file, hash))
	total := int64(0)
	for _, name := range names {
		input, err := os.Open(filepath.Join(repository, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		header := &zip.FileHeader{Name: version.Module + "@" + release + "/" + name, Method: zip.Deflate}
		header.SetMode(0644)
		member, err := writer.CreateHeader(header)
		if err != nil {
			_ = input.Close()
			t.Fatal(err)
		}
		size, copyErr := io.Copy(member, io.LimitReader(input, int64(500<<20)+1))
		closeErr := input.Close()
		total += size
		if copyErr != nil || closeErr != nil || total > 500<<20 {
			t.Fatal("module archive exceeds its source bounds", copyErr, closeErr)
		}
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	fixedModuleWrite(t, filepath.Join(directory, release+".mod"), module)
	fixedModuleWrite(t, filepath.Join(directory, release+".info"), []byte(`{"Version":"`+release+`","Time":"2026-10-23T00:00:00Z"}`))
	t.Logf("module proxy fixture: version=%s files=%d bytes=%d zip_sha256=%s", release, len(names), total, hex.EncodeToString(hash.Sum(nil)))
}

func fixedModuleWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func fixedModuleRaceBuild() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, setting := range info.Settings {
		if setting.Key == "-race" {
			return setting.Value == "true"
		}
	}
	return false
}
