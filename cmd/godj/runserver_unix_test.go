//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck"
)

func TestExecuteDispatchesRunserverBeforeProjectSelection(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	exit := execute(
		context.Background(),
		filepath.Join(t.TempDir(), "missing"),
		[]string{"runserver", "--project"},
		nil,
		nil,
		&stdout,
		&stderr,
		nil,
	)
	if exit != 2 || stdout.Len() != 0 || stderr.String() != projectcheck.RunServerCategoryCommand+"/"+projectcheck.RunServerCodeInvalidArguments+"\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}
