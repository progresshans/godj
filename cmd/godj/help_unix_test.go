//go:build darwin || linux

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuiltinHelpDoesNotLoadProjectOrEnvironment(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"help", "generate"}} {
		var stdout, stderr bytes.Buffer
		exit := execute(t.Context(), "/missing/private-project", args, []string{"PATH=/unavailable", "GODJ_POSTGRES_URL=private-secret"}, nil, &stdout, &stderr, nil)
		if exit != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "godj generate") || strings.Contains(stdout.String(), "private") {
			t.Fatal(exit, stdout.String(), stderr.String())
		}
	}
	var stderr bytes.Buffer
	if exit := execute(t.Context(), "", []string{"help"}, nil, nil, versionFailedWriter{partial: true}, &stderr, nil); exit != 1 || stderr.String() != "help_error/output_failed\n" {
		t.Fatal("short help write accepted", exit)
	}
}
