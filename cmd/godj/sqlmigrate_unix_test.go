//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
)

func TestExecuteDispatchesSQLMigrateBeforeProjectSelection(t *testing.T) {
	t.Parallel()
	for _, arguments := range [][]string{
		{"sqlmigrate", "blog"},
		{"sqlmigrate", "blog", "latest"},
		{"sqlmigrate", "blog", "0001", "--project"},
		{"sqlmigrate", "blog", "0001", "--backwards"},
	} {
		var stdout, stderr bytes.Buffer
		exit := execute(
			context.Background(),
			filepath.Join(t.TempDir(), "missing"),
			arguments,
			os.Environ(),
			nil,
			&stdout,
			&stderr,
			nil,
		)
		want := sqlmigrateprotocol.CategoryCommand + "/" + sqlmigrateprotocol.CodeInvalidArguments + "\n"
		if exit != 2 || stdout.Len() != 0 || stderr.String() != want {
			t.Fatalf("sqlmigrate dispatch args=%q exit=%d stdout=%q stderr=%q", arguments, exit, stdout.String(), stderr.String())
		}
	}
}
