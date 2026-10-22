//go:build darwin || linux

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/testenv"
	"github.com/progresshans/godj/version"
)

func TestVersionCommandArgumentsCancellationAndOutput(t *testing.T) {
	for _, args := range [][]string{{"version", "--project", "private-project"}, {"version", "--json", "--json"}, {"version", "private-secret"}} {
		var stdout, stderr bytes.Buffer
		if exit := execute(t.Context(), "/missing/cwd", args, []string{"PRIVATE=secret"}, nil, &stdout, &stderr, nil); exit != 2 || stdout.Len() != 0 || stderr.String() != "version_error/invalid_arguments\n" {
			t.Fatalf("invalid args: exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	interrupt := make(chan struct{})
	close(interrupt)
	for _, test := range []struct {
		ctx       context.Context
		interrupt <-chan struct{}
		code      string
	}{{ctx, nil, "canceled"}, {t.Context(), interrupt, "interrupted"}} {
		var stdout, stderr bytes.Buffer
		if exit := execute(test.ctx, "", []string{"version"}, nil, nil, &stdout, &stderr, test.interrupt); exit != 130 || stdout.Len() != 0 || stderr.String() != "version_error/"+test.code+"\n" {
			t.Fatalf("interruption: exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
		}
	}
	for _, writer := range []io.Writer{nil, versionFailedWriter{}, versionFailedWriter{partial: true}} {
		var stderr bytes.Buffer
		if exit := execute(t.Context(), "", []string{"version", "--json"}, nil, nil, writer, &stderr, nil); exit != 1 || stderr.String() != "version_error/output_failed\n" {
			t.Fatalf("write failure: exit=%d stderr=%q", exit, stderr.String())
		}
	}
}

type versionFailedWriter struct{ partial bool }

func (writer versionFailedWriter) Write(payload []byte) (int, error) {
	if writer.partial {
		return len(payload) - 1, nil
	}
	return 0, errors.New("private-writer-error")
}

func TestActualGodjVersionNeedsNoProjectOrGoToolAtRuntime(t *testing.T) {
	repository, environment := versionBuildFixture(t)
	root := t.TempDir()
	binary := filepath.Join(root, "godj")
	versionCommand(t, repository, environment, "go", "build", "-buildvcs=true", "-trimpath", "-mod=readonly", "-o", binary, "./cmd/godj")
	compiled, err := buildinfo.ReadFile(binary)
	if err != nil || compiled.Main.Path != version.Module {
		t.Fatalf("compiled tool module: %v, %+v", err, compiled)
	}
	// A malformed descriptor and an unavailable Go executable must not affect
	// the installed tool's own version, even when run inside a project.
	versionWrite(t, filepath.Join(root, "godj.toml"), []byte("invalid private descriptor"))
	runtimeEnvironment := testenv.With(environment, map[string]string{"PATH": root, "GODJ_PRIVATE_TOKEN": "secret-marker"})
	output := versionCommand(t, root, runtimeEnvironment, binary, "version", "--json")
	var document struct {
		FormatVersion int          `json:"format_version"`
		Framework     version.Info `json:"framework"`
	}
	wantOrigin, wantVersion := version.ModuleVersion, compiled.Main.Version
	if wantVersion == "" || wantVersion == "(devel)" {
		wantOrigin, wantVersion = version.Development, ""
	}
	if err := json.Unmarshal(output, &document); err != nil || document.FormatVersion != 1 || document.Framework.Origin != wantOrigin || document.Framework.Module != version.Module || document.Framework.Version != wantVersion || document.Framework.GoVersion != compiled.GoVersion {
		t.Fatalf("tool version: %s (%v)", output, err)
	}
	revision := ""
	modified := false
	for _, setting := range compiled.Settings {
		if setting.Key == "vcs.revision" {
			revision = setting.Value
		}
		if setting.Key == "vcs.modified" {
			modified = setting.Value == "true"
		}
	}
	if revision == "" || document.Framework.Revision != revision || document.Framework.Modified != modified {
		t.Fatalf("tool VCS identity differs from compiler: %+v", document.Framework)
	}
	plain := versionCommand(t, root, runtimeEnvironment, binary, "version")
	wantLabel := wantVersion
	if wantLabel == "" {
		wantLabel = string(wantOrigin)
	}
	if !strings.HasPrefix(string(plain), "godj "+wantLabel+" "+revision) {
		t.Fatalf("plain version = %q", plain)
	}
	for _, private := range []string{repository, root, "secret-marker", "invalid private descriptor"} {
		if bytes.Contains(output, []byte(private)) || bytes.Contains(plain, []byte(private)) {
			t.Fatal("private runtime input appeared in version")
		}
	}
	if descriptor, err := os.ReadFile(filepath.Join(root, "godj.toml")); err != nil || string(descriptor) != "invalid private descriptor" {
		t.Fatal("version changed project descriptor", err)
	}
	t.Run("closed stdout reports output failure", func(t *testing.T) {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "version", "--json")
		command.Dir, command.Env, command.Stdout = root, runtimeEnvironment, writer
		var stderr bytes.Buffer
		command.Stderr = &stderr
		command.WaitDelay = time.Second
		err = command.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || stderr.String() != "version_error/output_failed\n" {
			t.Fatalf("closed pipe: %v, stderr=%q", err, stderr.String())
		}
	})
}

func TestActualFrameworkIdentityInFixedAndDevelopmentDependencies(t *testing.T) {
	repository, environment := versionBuildFixture(t)
	root := t.TempDir()
	project := filepath.Join(root, "consumer")
	binary := filepath.Join(root, "consumer-binary")
	const fixtureVersion = "v0.0.0-godj-version-test.1"
	module := "module example.com/version-consumer\n\ngo 1.26.0\n\nrequire " + version.Module + " " + fixtureVersion + "\n"
	versionWrite(t, filepath.Join(project, "go.mod"), []byte(module+fmt.Sprintf("\nreplace %s => %q\n", version.Module, repository)))
	versionWrite(t, filepath.Join(project, "main.go"), []byte(`package main
import (
 "encoding/json"
 "os"
 "github.com/progresshans/godj/version"
)
func main() { if err := json.NewEncoder(os.Stdout).Encode(version.Current()); err != nil { os.Exit(1) } }
`))
	versionCommand(t, project, environment, "git", "init", "--quiet")
	versionCommand(t, project, environment, "git", "add", "go.mod", "main.go")
	versionCommand(t, project, environment, "git", "-c", "user.name=Version Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "fixture")
	buildAndRead := func(env []string) version.Info {
		t.Helper()
		moduleMode := "-mod=mod"
		if workspace := testenv.Map(env)["GOWORK"]; workspace != "" && workspace != "off" {
			moduleMode = "-mod=readonly"
		}
		versionCommand(t, project, env, "go", "build", "-buildvcs=true", moduleMode, "-o", binary, ".")
		compiled, err := buildinfo.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		stamped := false
		for _, setting := range compiled.Settings {
			stamped = stamped || setting.Key == "vcs.revision" && setting.Value != ""
		}
		if !stamped {
			t.Fatal("consumer VCS negative control was not stamped")
		}
		output := versionCommand(t, project, env, binary)
		var identity version.Info
		if err := json.Unmarshal(output, &identity); err != nil || identity.Module != version.Module || identity.GoVersion != compiled.GoVersion || identity.Revision != "" || identity.Modified {
			t.Fatalf("dependency inherited consumer identity: %s (%v)", output, err)
		}
		if strings.Contains(string(output), repository) || strings.Contains(string(output), project) {
			t.Fatal("dependency disclosed local source path")
		}
		return identity
	}
	local := buildAndRead(environment)
	if local.Origin != version.LocalReplacement || local.Version != "" || local.RequestedVersion != fixtureVersion || local.ReplacementModule != "" {
		t.Fatalf("local replacement misreported required version: %+v", local)
	}
	// A real module proxy fixture contains this package's unmodified source.
	// It is a version-metadata test module, not a claimed GoDj release artifact.
	proxy := filepath.Join(root, "proxy", filepath.FromSlash(version.Module), "@v")
	source, err := os.ReadFile(filepath.Join(repository, "version", "version.go"))
	if err != nil {
		t.Fatal(err)
	}
	frameworkMod := []byte("module " + version.Module + "\n\ngo 1.26.0\n")
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	for _, entry := range []struct {
		path string
		data []byte
	}{{"go.mod", frameworkMod}, {"version/version.go", source}} {
		file, err := zipWriter.Create(version.Module + "@" + fixtureVersion + "/" + entry.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	versionWrite(t, filepath.Join(proxy, fixtureVersion+".zip"), archive.Bytes())
	versionWrite(t, filepath.Join(proxy, fixtureVersion+".mod"), frameworkMod)
	versionWrite(t, filepath.Join(proxy, fixtureVersion+".info"), []byte(`{"Version":"`+fixtureVersion+`","Time":"2026-10-23T00:00:00Z"}`))
	versionWrite(t, filepath.Join(project, "go.mod"), []byte(module))
	proxyURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(root, "proxy"))}).String()
	// This cache belongs to the fixture's TempDir. Go's default read-only
	// module directories cannot be removed by ordinary TempDir cleanup.
	fixedEnvironment := testenv.With(environment, map[string]string{
		"GOPROXY": proxyURL, "GOMODCACHE": filepath.Join(root, "module-cache"),
		"GOFLAGS": strings.TrimSpace(testenv.Map(environment)["GOFLAGS"] + " -modcacherw"),
	})
	fixed := buildAndRead(fixedEnvironment)
	if fixed.Origin != version.ModuleVersion || fixed.Version != fixtureVersion || fixed.RequestedVersion != "" || fixed.ReplacementModule != "" {
		t.Fatalf("fixed dependency lost effective version: %+v", fixed)
	}
	versionWrite(t, filepath.Join(root, "go.work"), []byte(fmt.Sprintf("go 1.26.0\n\nuse (\n%q\n%q\n)\n", project, repository)))
	workspace := buildAndRead(testenv.With(environment, map[string]string{"GOWORK": filepath.Join(root, "go.work")}))
	if workspace.Origin != version.Development || workspace.Version != "" || workspace.RequestedVersion != "" {
		t.Fatalf("workspace dependency misreported version: %+v", workspace)
	}
}

func versionBuildFixture(t *testing.T) (string, []string) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("version test source unavailable")
	}
	flags := ""
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			if setting.Key == "-race" && setting.Value == "true" {
				flags = "-race"
			}
		}
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..")), testenv.OfflineGo(os.Environ(), flags)
}

func versionCommand(t *testing.T, directory string, environment []string, program string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.Dir, command.Env = directory, environment
	command.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("version fixture %s %v: %v\nstdout=%s\nstderr=%s", program, args, err, stdout.Bytes(), stderr.Bytes())
	}
	return stdout.Bytes()
}

func versionWrite(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}
