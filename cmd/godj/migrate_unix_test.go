//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
)

func TestActualGodjMigrateProcessUsesSeparatePrivateRunner(t *testing.T) {
	fixture := newProcessFixture(t)
	before := snapshotProject(t, fixture.project)
	wantFailure := migrateprotocol.CategoryBackend + "/" + migrateprotocol.CodeInvalidBackend + "\n"
	tests := []struct {
		name string
		cwd  string
		args []string
	}{
		{name: "implicit execute latest", cwd: fixture.nested, args: []string{"migrate"}},
		{name: "explicit execute latest", cwd: filepath.Dir(fixture.project), args: []string{"migrate", "--project", filepath.Join(fixture.project, "godj.toml")}},
		{name: "implicit plan latest", cwd: fixture.nested, args: []string{"migrate", "--plan"}},
		{name: "explicit plan named", cwd: filepath.Dir(fixture.project), args: []string{"migrate", "content", "0001_initial", "--plan", "--project", filepath.Join(fixture.project, "godj.toml")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := fixture.run(t, test.cwd, nil, test.args...)
			if result.exit != 3 || result.stdout != "" || result.stderr != wantFailure {
				t.Fatalf("migrate = %+v", result)
			}
		})
	}
	after := snapshotProject(t, fixture.project)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed migrate variants rewrote project tree\nbefore=%v\nafter=%v", before, after)
	}
}

func TestExecuteDispatchesMigrateBeforeProjectSelection(t *testing.T) {
	for _, arguments := range [][]string{
		{"migrate", "--project"},
		{"migrate", "--project", "godj.toml", "--plan"},
		{"migrate", "--plan", "content", "0001_initial"},
	} {
		var stdout, stderr bytes.Buffer
		exit := execute(
			context.Background(),
			filepath.Join(t.TempDir(), "missing"),
			arguments,
			os.Environ(),
			&stdout,
			&stderr,
			nil,
		)
		if exit != 2 || stdout.Len() != 0 || stderr.String() != migrateprotocol.CategoryCommand+"/"+migrateprotocol.CodeInvalidArguments+"\n" {
			t.Fatalf("migrate dispatch args=%q exit=%d stdout=%q stderr=%q", arguments, exit, stdout.String(), stderr.String())
		}
	}
}

func TestActualGodjMigrateInvalidArgumentsPrecedeDeletedCWD(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux deleted-cwd process regression")
	}
	fixture := newProcessFixture(t)
	deleted := filepath.Join(fixture.universe, "deleted-migrate-cwd")
	if err := os.Mkdir(deleted, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		"/bin/sh",
		"-c",
		`cd "$1" && rmdir "$1" && exec "$2" migrate --project godj.toml --plan`,
		"godj-deleted-cwd",
		deleted,
		fixture.godj,
	)
	command.Env = fixture.environment(nil)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 2 || stdout.Len() != 0 || stderr.String() != migrateprotocol.CategoryCommand+"/"+migrateprotocol.CodeInvalidArguments+"\n" {
		t.Fatalf("deleted-cwd migrate args err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func TestGlobalMainObservesTerminationSignal(t *testing.T) {
	source, err := os.ReadFile("main_unix.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "signal.Notify(signals, os.Interrupt, syscall.SIGTERM)") {
		t.Fatal("global main does not register SIGTERM with SIGINT")
	}
}
