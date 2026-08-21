//go:build darwin || linux

package projectcheck

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateWorkspaceRealModuleDownloadRemainsCleanupWritable(t *testing.T) {
	fixture := newGlobalFixture(t, 0)
	ambientBytes, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("locate ambient module cache: %v", err)
	}
	ambientCache, err := filepath.EvalSymlinks(strings.TrimSpace(string(ambientBytes)))
	if err != nil {
		t.Fatalf("resolve ambient module cache: %v", err)
	}
	downloadProxy := filepath.Join(ambientCache, "cache", "download")
	if info, err := os.Stat(downloadProxy); err != nil || !info.IsDir() {
		t.Fatalf("ambient module download proxy unavailable at %s: %v", downloadProxy, err)
	}
	ambient := append([]string(nil), fixture.environment...)
	ambient = append(ambient,
		"GOMODCACHE="+ambientCache,
		"GOPROXY=off",
		"GOSUMDB=off",
	)

	report := Report{}
	selected, failure := selectProject(fixture.project, commandArguments{}, &report)
	if failure != nil {
		t.Fatalf("select project: %+v", *failure)
	}
	workspace, failure := createPrivateWorkspaceWithHooks(selected, ambient, &report, workspaceHooks{})
	if failure != nil {
		_ = selected.close()
		t.Fatalf("create workspace: %+v", *failure)
	}
	workspaceRoot := workspace.root
	values := environmentValues(workspace.environment)
	if values["GOFLAGS"] != "-modcacherw" {
		t.Fatalf("GOFLAGS=%q, want exact -modcacherw", values["GOFLAGS"])
	}
	if values["GOMODCACHE"] != filepath.Join(workspaceRoot, "gomodcache") {
		t.Fatalf("GOMODCACHE=%q is not private", values["GOMODCACHE"])
	}
	if !strings.HasPrefix(values["GOPROXY"], "file://") || !strings.HasSuffix(values["GOPROXY"], ",off") {
		t.Fatalf("GOPROXY=%q does not expose the safe ambient download cache before offline fallback", values["GOPROXY"])
	}
	result := processBackend{}.Execute(context.Background(), nil, BuildStage, Command{
		Dir:  fixture.project,
		Argv: []string{"go", "mod", "download", "golang.org/x/sys@v0.47.0"},
		Env:  workspace.environment,
	})
	if !result.Started || result.ExitCode != 0 || result.DirectReaps != 1 || result.CleanupFailed || result.Failure != nil {
		_ = workspace.cleanup()
		_ = selected.close()
		t.Fatalf("real private module download=%+v", result)
	}
	downloaded := filepath.Join(values["GOMODCACHE"], "golang.org", "x", "sys@v0.47.0")
	info, err := os.Stat(downloaded)
	if err != nil {
		_ = workspace.cleanup()
		_ = selected.close()
		t.Fatalf("downloaded module missing: %v", err)
	}
	if info.Mode().Perm()&0o200 == 0 {
		_ = workspace.cleanup()
		_ = selected.close()
		t.Fatalf("downloaded module mode=%04o is not cleanup-writable", info.Mode().Perm())
	}
	if err := selected.close(); err != nil {
		_ = workspace.cleanup()
		t.Fatalf("close selected project: %v", err)
	}
	report.TempCleanupAttempts++
	if err := workspace.cleanup(); err != nil {
		t.Fatalf("cleanup real downloaded module cache: %v", err)
	}
	if _, err := os.Lstat(workspaceRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private workspace remained after cleanup: %v", err)
	}
	if report.TempCreated != 1 || report.TempCleanupAttempts != 1 {
		t.Fatalf("workspace observations=%+v", report)
	}
}

func TestPrivateWorkspaceRejectsAmbientModuleCacheResolvingInsideProject(t *testing.T) {
	fixture := newGlobalFixture(t, 0)
	insideCache := filepath.Join(fixture.project, "unsafe-modcache")
	if err := os.MkdirAll(filepath.Join(insideCache, "cache", "download"), 0o755); err != nil {
		t.Fatalf("create inside-project module cache: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "modcache")
	if err := os.Symlink(insideCache, alias); err != nil {
		t.Fatalf("symlink module cache into project: %v", err)
	}
	ambient := append([]string(nil), fixture.environment...)
	ambient = append(ambient, "GOMODCACHE="+alias, "GOPROXY=off")
	report := Report{}
	selected, failure := selectProject(fixture.project, commandArguments{}, &report)
	if failure != nil {
		t.Fatalf("select project: %+v", *failure)
	}
	workspace, failure := createPrivateWorkspaceWithHooks(selected, ambient, &report, workspaceHooks{})
	if failure != nil {
		_ = selected.close()
		t.Fatalf("create workspace: %+v", *failure)
	}
	values := environmentValues(workspace.environment)
	if values["GOPROXY"] != "off" {
		_ = workspace.cleanup()
		_ = selected.close()
		t.Fatalf("GOPROXY=%q exposed an inside-project module cache", values["GOPROXY"])
	}
	if err := selected.close(); err != nil {
		_ = workspace.cleanup()
		t.Fatalf("close selected project: %v", err)
	}
	if err := workspace.cleanup(); err != nil {
		t.Fatalf("cleanup workspace: %v", err)
	}
}
