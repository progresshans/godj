//go:build darwin || linux

package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

func TestActualGodjMigrationCheckProcess(t *testing.T) {
	fixture := newProcessFixture(t)

	t.Run("implicit success", func(t *testing.T) {
		before := snapshotProject(t, fixture.project)
		result := fixture.run(t, fixture.nested, nil, "migrations", "check")
		want := `{"source_count":0,"definition_count":0,"definition_set_digest":"` + protocol.EmptySetDigest + `"}` + "\n"
		if result.exit != 0 || result.stdout != want || result.stderr != "" {
			t.Fatalf("implicit success = %+v", result)
		}
		after := snapshotProject(t, fixture.project)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("successful check rewrote project tree\nbefore=%v\nafter=%v", before, after)
		}
	})

	t.Run("explicit success", func(t *testing.T) {
		result := fixture.run(t, fixture.universe, nil, "migrations", "check", "--project", filepath.Join(fixture.project, "godj.toml"))
		if result.exit != 0 || !strings.Contains(result.stdout, protocol.EmptySetDigest) || result.stderr != "" {
			t.Fatalf("explicit success = %+v", result)
		}
	})

	t.Run("invalid arguments precede deleted cwd resolution", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("Linux deleted-cwd process regression")
		}
		deleted := filepath.Join(fixture.universe, "deleted-cwd")
		if err := os.Mkdir(deleted, 0o700); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("/bin/sh", "-c", `cd "$1" && rmdir "$1" && exec "$2" invalid`, "godj-deleted-cwd", deleted, fixture.godj)
		command.Env = fixture.environment(nil)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 2 || stdout.Len() != 0 || stderr.String() != protocol.CategoryCommand+"/"+protocol.CodeInvalidArguments+"\n" {
			t.Fatalf("deleted-cwd invalid arguments err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
	})

	t.Run("invalid descriptor", func(t *testing.T) {
		fixture.writeDescriptor(t, "format_version = 1\n[project]\npackage = \"./cmd/site\"")
		defer fixture.writeDescriptor(t, canonicalE2EDescriptor)
		result := fixture.run(t, fixture.project, nil, "migrations", "check")
		if result.exit != 2 || result.stdout != "" || result.stderr != protocol.CategorySelection+"/"+protocol.CodeInvalidProjectDescriptor+"\n" {
			t.Fatalf("invalid descriptor = %+v", result)
		}
	})

	t.Run("syntax build failure is atomic", func(t *testing.T) {
		before := snapshotProject(t, fixture.project)
		mainPath := filepath.Join(fixture.project, "cmd", "site", "main.go")
		beforeMain, err := os.Lstat(mainPath)
		if err != nil {
			t.Fatal(err)
		}
		fixture.writeMain(t, "package main\nfunc main( {\n")
		defer fixture.writeMain(t, e2eProjectMain)
		result := fixture.run(t, fixture.project, nil, "migrations", "check")
		if result.exit != 3 || result.stdout != "" || result.stderr != protocol.CategoryBuild+"/"+protocol.CodeProjectBuildFailed+"\n" {
			t.Fatalf("build failure = %+v", result)
		}
		after := snapshotProject(t, fixture.project)
		afterMain, err := os.Lstat(mainPath)
		if err != nil {
			t.Fatal(err)
		}
		if beforeMain.Mode() != afterMain.Mode() {
			t.Fatalf("intentional syntax fixture mode changed: before=%v after=%v", beforeMain.Mode(), afterMain.Mode())
		}
		delete(before, "cmd/site/main.go")
		delete(after, "cmd/site/main.go")
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("project metadata changed across -mod=readonly build\nbefore=%v\nafter=%v", before, after)
		}
	})

	t.Run("definition failure", func(t *testing.T) {
		migrations := filepath.Join(fixture.project, "migrations")
		if err := os.Mkdir(migrations, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(migrations, "broken.godj.json"), []byte(`{"migration":{"name":"duplicate","name":"duplicate"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		result := fixture.run(t, fixture.project, map[string]string{"GODJ_E2E_USE_MIGRATIONS": "1"}, "migrations", "check")
		if result.exit != 1 || result.stdout != "" || result.stderr != protocol.CategorySource+"/invalid_definition_document\n" {
			t.Fatalf("definition failure = %+v", result)
		}
	})

	t.Run("invalid runner response", func(t *testing.T) {
		result := fixture.run(t, fixture.project, map[string]string{"GODJ_E2E_INVALID_RESPONSE": "1"}, "migrations", "check")
		if result.exit != 3 || result.stdout != "" || result.stderr != protocol.CategoryProtocol+"/"+protocol.CodeInvalidProjectRunnerResponse+"\n" {
			t.Fatalf("invalid runner response = %+v", result)
		}
	})

	t.Run("handled SIGINT reaps runner and cleans private workspace", func(t *testing.T) {
		ready := filepath.Join(fixture.universe, "runner-ready")
		command := exec.Command(fixture.godj, "migrations", "check")
		command.Dir = fixture.project
		command.Env = fixture.environment(map[string]string{
			"GODJ_E2E_HANG":  "1",
			"GODJ_E2E_READY": ready,
		})
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		command.WaitDelay = 5 * time.Second
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		waited := make(chan error, 1)
		go func() { waited <- command.Wait() }()
		if err := waitForE2EFile(command, waited, ready); err != nil {
			t.Fatalf("runner readiness: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
		if signalErr := command.Process.Signal(os.Interrupt); signalErr != nil {
			t.Fatalf("signal runner: %v wait=%v", signalErr, <-waited)
		}
		err := <-waited
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 130 || stdout.Len() != 0 || stderr.String() != protocol.CategoryProcess+"/"+protocol.CodeProjectInterrupted+"\n" {
			t.Fatalf("SIGINT err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
		entries, err := os.ReadDir(fixture.scratch)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "godj-projectcheck-") {
				t.Fatalf("private workspace remained: %s", entry.Name())
			}
		}
	})
}

func TestActualGodjGenerateProcess(t *testing.T) {
	fixture := newProcessFixture(t)
	if strings.Contains(e2eProjectMain, "\n\t\"example.com/godj-e2e/generated/") {
		t.Fatal("declaration runner source imports generated packages")
	}

	t.Run("invalid generation arguments precede deleted cwd resolution", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("Linux deleted-cwd process regression")
		}
		deleted := filepath.Join(fixture.universe, "deleted-generate-cwd")
		if err := os.Mkdir(deleted, 0o700); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("/bin/sh", "-c", `cd "$1" && rmdir "$1" && exec "$2" generate --project`, "godj-deleted-cwd", deleted, fixture.godj)
		command.Env = fixture.environment(nil)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 2 || stdout.Len() != 0 || stderr.String() != "project_generation_command_error/invalid_arguments\n" {
			t.Fatalf("deleted-cwd generation args err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
	})

	t.Run("read-only drift from nested cwd", func(t *testing.T) {
		before := snapshotProject(t, fixture.project)
		result := fixture.run(t, fixture.nested, map[string]string{"GOPROXY": "off"}, "generate", "--check")
		if result.exit != 1 || result.stderr != "" || !strings.Contains(result.stdout, `"status":"drift"`) || !strings.Contains(result.stdout, `"actual_snapshot_sha256":""`) {
			t.Fatalf("initial check=%+v", result)
		}
		after := snapshotProject(t, fixture.project)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("generate --check mutated tree\nbefore=%v\nafter=%v", before, after)
		}
	})

	t.Run("generate with isolated offline build", func(t *testing.T) {
		initMarker := filepath.Join(fixture.universe, "user-init-ran")
		testMainMarker := filepath.Join(fixture.universe, "user-testmain-ran")
		result := fixture.run(t, fixture.nested, map[string]string{
			"GOPROXY": "off", "GODJ_E2E_INIT_MARKER": initMarker, "GODJ_E2E_TESTMAIN_MARKER": testMainMarker,
		}, "generate")
		if result.exit != 0 || result.stderr != "" || !strings.Contains(result.stdout, `"status":"generated"`) || !strings.Contains(result.stdout, `"file_count":16`) {
			t.Fatalf("generate=%+v", result)
		}
		for _, marker := range []string{initMarker, testMainMarker} {
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("candidate compilation executed user code %s: %v", marker, err)
			}
		}
		if _, err := os.Stat(filepath.Join(fixture.project, ".godj", "generated-manifest.json")); err != nil {
			t.Fatalf("manifest missing: %v", err)
		}
	})

	t.Run("explicit clean check", func(t *testing.T) {
		result := fixture.run(t, fixture.universe, map[string]string{"GOPROXY": "off"}, "generate", "--check", "--project", filepath.Join(fixture.project, "godj.toml"))
		if result.exit != 0 || result.stderr != "" || !strings.Contains(result.stdout, `"status":"clean"`) || !strings.Contains(result.stdout, `"file_count":16`) {
			t.Fatalf("clean check=%+v", result)
		}
	})

	generated := filepath.Join(fixture.project, "generated", "blog", "zz_godj_generated.go")
	t.Run("missing old generated file is reported and publication fails closed", func(t *testing.T) {
		if err := os.Remove(generated); err != nil {
			t.Fatal(err)
		}
		before := snapshotProject(t, fixture.project)
		result := fixture.run(t, fixture.project, map[string]string{"GOPROXY": "off"}, "generate", "--check")
		if result.exit != 1 || result.stderr != "" || !strings.Contains(result.stdout, `"path":"generated/blog/zz_godj_generated.go","kind":"missing"`) {
			t.Fatalf("missing check=%+v", result)
		}
		if after := snapshotProject(t, fixture.project); !reflect.DeepEqual(before, after) {
			t.Fatalf("missing check mutated tree\nbefore=%v\nafter=%v", before, after)
		}
		result = fixture.run(t, fixture.project, map[string]string{"GOPROXY": "off"}, "generate")
		if result.exit != 1 || result.stdout != "" || result.stderr != generationCategoryGenerationForE2E+"/project_generate_publish_failed\n" {
			t.Fatalf("missing publication=%+v", result)
		}
		if _, err := os.Stat(generated); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing target was guessed/recreated: %v", err)
		}
	})

	t.Run("broken old generated source stays outside declaration runner dependencies and fails publish CAS", func(t *testing.T) {
		if err := os.WriteFile(generated, []byte("package blog\nfunc broken( {\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		result := fixture.run(t, fixture.nested, map[string]string{"GOPROXY": "off"}, "generate")
		if result.exit != 1 || result.stdout != "" || result.stderr != generationCategoryGenerationForE2E+"/project_generate_publish_failed\n" {
			t.Fatalf("broken publication=%+v", result)
		}
		contents, err := os.ReadFile(generated)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(contents, []byte("func broken")) {
			t.Fatal("broken generated source was unexpectedly replaced")
		}
	})
}

func TestActualMainDoesNotResolveCWDBeforeGlobalArgumentParsing(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate main source")
	}
	document, err := os.ReadFile(filepath.Join(filepath.Dir(source), "main_unix.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(document)
	if strings.Contains(text, "os.Getwd(") || !strings.Contains(text, `execute(context.Background(), "", os.Args[1:]`) {
		t.Fatalf("main must delegate empty CWD after argument capture: %s", text)
	}
}

type processFixture struct {
	universe string
	project  string
	nested   string
	scratch  string
	godj     string
	baseEnv  map[string]string
}

type commandResult struct {
	exit   int
	stdout string
	stderr string
}

func newProcessFixture(t *testing.T) processFixture {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	repository, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(source), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	universe, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture := processFixture{
		universe: universe,
		project:  filepath.Join(universe, "project"),
		nested:   filepath.Join(universe, "project", "nested"),
		scratch:  filepath.Join(universe, "scratch"),
		godj:     filepath.Join(universe, "godj"),
		baseEnv:  environmentMap(os.Environ()),
	}
	for _, directory := range []string{
		fixture.project,
		fixture.nested,
		filepath.Join(fixture.project, "cmd", "site"),
		filepath.Join(fixture.project, "sentinel"),
		fixture.scratch,
		filepath.Join(universe, "home"),
		filepath.Join(universe, "xdg-config"),
		filepath.Join(universe, "xdg-cache"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	moduleCacheBytes, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	xsys := filepath.Join(strings.TrimSpace(string(moduleCacheBytes)), "golang.org", "x", "sys@v0.47.0")
	if info, err := os.Stat(xsys); err != nil || !info.IsDir() {
		t.Fatalf("x/sys module cache unavailable at %s: %v", xsys, err)
	}
	goMod := fmt.Sprintf("module example.com/godj-e2e\n\ngo 1.26.0\n\ntoolchain go1.26.5\n\nrequire (\n\tgithub.com/progresshans/godj v0.0.0\n\tgolang.org/x/sys v0.47.0\n)\n\nreplace github.com/progresshans/godj => %s\nreplace golang.org/x/sys => %s\n", repository, xsys)
	if err := os.WriteFile(filepath.Join(fixture.project, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.writeDescriptor(t, canonicalE2EDescriptor)
	fixture.writeMain(t, e2eProjectMain)
	if err := os.WriteFile(filepath.Join(fixture.project, "sentinel", "sentinel.go"), []byte(e2eSentinelSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.project, "sentinel", "sentinel_test.go"), []byte(e2eSentinelTestSource), 0o600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", fixture.godj, "./cmd/godj")
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build godj: %v\n%s", err, output)
	}
	fixture.baseEnv["HOME"] = filepath.Join(universe, "home")
	fixture.baseEnv["XDG_CONFIG_HOME"] = filepath.Join(universe, "xdg-config")
	fixture.baseEnv["XDG_CACHE_HOME"] = filepath.Join(universe, "xdg-cache")
	fixture.baseEnv["TMPDIR"] = fixture.scratch
	// The product still gives every child an empty writable GOMODCACHE. The
	// candidate verifier may expose this ambient cache's immutable download
	// subtree as a file:// module proxy, including while GOPROXY=off.
	fixture.baseEnv["GOMODCACHE"] = strings.TrimSpace(string(moduleCacheBytes))
	return fixture
}

func (fixture processFixture) run(t *testing.T, cwd string, extra map[string]string, args ...string) commandResult {
	t.Helper()
	command := exec.Command(fixture.godj, args...)
	command.Dir = cwd
	command.Env = fixture.environment(extra)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exit := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("run godj: %v", err)
		}
		exit = exitError.ExitCode()
	}
	return commandResult{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

func (fixture processFixture) environment(extra map[string]string) []string {
	values := make(map[string]string, len(fixture.baseEnv)+len(extra))
	for key, value := range fixture.baseEnv {
		values[key] = value
	}
	delete(values, "GODJ_E2E_HANG")
	delete(values, "GODJ_E2E_READY")
	delete(values, "GODJ_E2E_USE_MIGRATIONS")
	delete(values, "GODJ_E2E_INVALID_RESPONSE")
	delete(values, "GODJ_E2E_INIT_MARKER")
	delete(values, "GODJ_E2E_TESTMAIN_MARKER")
	for key, value := range extra {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, len(keys))
	for index, key := range keys {
		result[index] = key + "=" + values[key]
	}
	return result
}

func (fixture processFixture) writeDescriptor(t *testing.T, document string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.project, "godj.toml"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture processFixture) writeMain(t *testing.T, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.project, "cmd", "site", "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func environmentMap(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	return values
}

func snapshotProject(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[key+"/"] = fmt.Sprintf("type=%v perm=%04o", info.Mode().Type(), info.Mode().Perm())
			return nil
		}
		var contents []byte
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			contents = []byte(target)
		} else {
			contents, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		digest := sha256.Sum256(contents)
		result[key] = fmt.Sprintf("type=%v perm=%04o sha256=%x", info.Mode().Type(), info.Mode().Perm(), digest)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func waitForE2EFile(command *exec.Cmd, waited <-chan error, path string) error {
	// Readiness includes an intentionally cold project build because each
	// invocation receives a fresh private GOCACHE and GOMODCACHE. Keep a finite
	// deadline so a stuck build or runner still fails the process test.
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return stopE2EProcess(command, waited, fmt.Errorf("inspect readiness file: %w", err))
		}
		select {
		case waitErr := <-waited:
			return fmt.Errorf("process exited before readiness file: %v", waitErr)
		case <-timer.C:
			return stopE2EProcess(command, waited, fmt.Errorf("timed out waiting for %s", path))
		case <-ticker.C:
		}
	}
}

func stopE2EProcess(command *exec.Cmd, waited <-chan error, cause error) error {
	interruptErr := command.Process.Signal(os.Interrupt)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case waitErr := <-waited:
		return fmt.Errorf("%w; interrupt=%v wait=%v", cause, interruptErr, waitErr)
	case <-timer.C:
		killErr := command.Process.Kill()
		waitErr := <-waited
		return fmt.Errorf("%w; interrupt=%v kill=%v wait=%v", cause, interruptErr, killErr, waitErr)
	}
}

const canonicalE2EDescriptor = "format_version = 1\n\n[project]\npackage = \"./cmd/site\"\n"

const generationCategoryGenerationForE2E = "project_generation_error"

const e2eSentinelSource = `package sentinel

import "os"

func init() {
	if marker := os.Getenv("GODJ_E2E_INIT_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("init ran"), 0o600)
	}
}
`

const e2eSentinelTestSource = `package sentinel

import (
	"os"
	"testing"
)

func TestMain(testingMain *testing.M) {
	if marker := os.Getenv("GODJ_E2E_TESTMAIN_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("TestMain ran"), 0o600)
	}
	os.Exit(testingMain.Run())
}
`

const e2eProjectMain = `package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/project"
	"github.com/progresshans/godj/schema/ir"
)

func main() {
	if os.Getenv("GODJ_E2E_INVALID_RESPONSE") == "1" && len(os.Args) == 2 && os.Args[1] == "__godj_project_runner_v1" {
		_, _ = fmt.Print("{\"protocol_version\":1,\"status\":\"ok\",\"status\":\"ok\",\"result\":{\"source_count\":0,\"definition_count\":0,\"definition_set_digest\":\"sha256:1412c48d7da2299b6f2be7a614c5bb9ce510027328f6baed72ae05cbecc9b494\"}}")
		return
	}
	if os.Getenv("GODJ_E2E_HANG") == "1" && len(os.Args) == 2 && os.Args[1] == "__godj_project_runner_v1" {
		signal.Ignore(os.Interrupt)
		if ready := os.Getenv("GODJ_E2E_READY"); ready != "" {
			_ = os.WriteFile(ready, []byte("ready"), 0o600)
		}
		for { time.Sleep(time.Second) }
	}
	roots := []string(nil)
	if os.Getenv("GODJ_E2E_USE_MIGRATIONS") == "1" {
		roots = []string{"migrations"}
	}
	config := project.Config{
		MigrationDefinitionRoots: roots,
		LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) {
			authors := ir.Schema{
				FormatVersion: ir.CurrentFormatVersion,
				AppLabel: "authors",
				Models: []ir.Model{{Name: "author", GoName: "Author", Fields: []ir.Field{
					{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 100},
				}}},
			}
			blog := ir.Schema{
				FormatVersion: ir.CurrentFormatVersion,
				AppLabel: "blog",
				Models: []ir.Model{{Name: "blog_post", GoName: "BlogPost", Fields: []ir.Field{
					{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
					{Name: "author", GoName: "AuthorID", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{
						Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse: ir.ReverseRelation{Name: "blog_posts"},
						OnDelete: ir.DeleteProtect,
					}},
				}}},
			}
			return codegen.ProjectSpec{
				Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-e2e/generated/project", Directory: "generated/project"},
				Apps: []codegen.AppSpec{
					{Alias: "blog", Package: codegen.PackageSpec{PackageName: "blog", ImportPath: "example.com/godj-e2e/generated/blog", Directory: "generated/blog"}, Schema: blog},
					{Alias: "authors", Package: codegen.PackageSpec{PackageName: "authors", ImportPath: "example.com/godj-e2e/generated/authors", Directory: "generated/authors"}, Schema: authors},
				},
			}, nil
		},
	}
	if err := project.Run(context.Background(), config, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		os.Exit(1)
	}
}
`
