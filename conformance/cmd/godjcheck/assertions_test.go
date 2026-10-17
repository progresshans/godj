package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func assertPreHandlerFailure(
	t *testing.T,
	ctx context.Context,
	arguments []string,
	actualPath string,
	wantError string,
) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(ctx, arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), wantError) {
		t.Fatalf("stdout=%q stderr=%q, want error containing %q", stdout.String(), stderr.String(), wantError)
	}
	if strings.Contains(stderr.String(), context.Canceled.Error()) {
		t.Fatalf("stderr=%q reached actual generation instead of failing at the deviation gate", stderr.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}
