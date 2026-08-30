//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"
)

func TestActualGodjShowMigrationsUsesSeparatePrivateRunner(t *testing.T) {
	fixture := newProcessFixture(t)
	before := snapshotProject(t, fixture.project)
	wantFailure := showmigrationsprotocol.CategoryBackend + "/" + showmigrationsprotocol.CodeInvalidBackend + "\n"

	implicit := fixture.run(t, fixture.nested, nil, "showmigrations")
	if implicit.exit != 3 || implicit.stdout != "" || implicit.stderr != wantFailure {
		t.Fatalf("implicit showmigrations = %+v", implicit)
	}
	if after := snapshotProject(t, fixture.project); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed showmigrations rewrote project tree\nbefore=%v\nafter=%v", before, after)
	}

	explicit := fixture.run(
		t,
		filepath.Dir(fixture.project),
		nil,
		"showmigrations", "--project", filepath.Join(fixture.project, "godj.toml"),
	)
	if explicit.exit != 3 || explicit.stdout != "" || explicit.stderr != wantFailure {
		t.Fatalf("explicit showmigrations = %+v", explicit)
	}
}

func TestExecuteDispatchesShowMigrationsBeforeProjectSelection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := execute(
		context.Background(),
		filepath.Join(t.TempDir(), "missing"),
		[]string{"showmigrations", "--project"},
		os.Environ(),
		&stdout,
		&stderr,
		nil,
	)
	if exit != 2 || stdout.Len() != 0 ||
		stderr.String() != showmigrationsprotocol.CategoryCommand+"/"+showmigrationsprotocol.CodeInvalidArguments+"\n" {
		t.Fatalf("showmigrations dispatch exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}
