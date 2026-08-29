//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck"
)

func TestActualGodjMakemigrationsDryRunIsDeterministicAndReadOnly(t *testing.T) {
	fixture := newProcessFixture(t)
	modulePath := filepath.Join(fixture.project, "go.mod")
	moduleDocument, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatal(err)
	}
	moduleLines := strings.Split(string(moduleDocument), "\n")
	filtered := moduleLines[:0]
	for _, line := range moduleLines {
		if !strings.HasPrefix(line, "replace golang.org/x/sys => ") {
			filtered = append(filtered, line)
		}
	}
	if err := os.WriteFile(modulePath, []byte(strings.Join(filtered, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.project, "go.sum"), []byte(
		"golang.org/x/sys v0.47.0 h1:o7XGOvZQCADBQQ4Y7VNq2dRWQR7JmOUW8Kxx4ZsNgWs=\n"+
			"golang.org/x/sys v0.47.0/go.mod h1:4GL1E5IUh+htKOUEOaiffhrAeqysfVGipDYzABqnCmw=\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(fixture.project, "migrations"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := snapshotProject(t, fixture.project)
	result := fixture.run(
		t,
		fixture.nested,
		map[string]string{"GODJ_E2E_USE_MIGRATIONS": "1", "GOPROXY": "off"},
		"makemigrations",
		"--dry-run",
	)
	if result.exit != 0 || result.stderr != "" {
		t.Fatalf("dry-run = %+v", result)
	}
	var output struct {
		Status         string `json:"status"`
		CandidateCount int    `json:"candidate_count"`
		Candidates     []struct {
			App      string `json:"app"`
			Name     string `json:"name"`
			Path     string `json:"path"`
			SourceID string `json:"source_id"`
			SHA256   string `json:"sha256"`
		} `json:"candidates"`
	}
	decoder := json.NewDecoder(bytes.NewBufferString(result.stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		t.Fatalf("decode output %q: %v", result.stdout, err)
	}
	if output.Status != "pending" || output.CandidateCount != 2 || len(output.Candidates) != 2 {
		t.Fatalf("output = %+v", output)
	}
	for index, wantApp := range []string{"authors", "blog"} {
		candidate := output.Candidates[index]
		wantPath := "migrations/" + wantApp + "_0001_initial.godj.json"
		if candidate.App != wantApp || candidate.Name != "0001_initial" ||
			candidate.Path != wantPath || candidate.SourceID != wantPath || len(candidate.SHA256) != 64 {
			t.Fatalf("candidate[%d] = %+v", index, candidate)
		}
	}
	if after := snapshotProject(t, fixture.project); !reflect.DeepEqual(before, after) {
		t.Fatalf("dry-run mutated project\nbefore=%v\nafter=%v", before, after)
	}
}

func TestMakemigrationsDispatchRejectsInvalidArgumentsBeforeCWD(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := execute(
		context.Background(),
		filepath.Join(t.TempDir(), "missing"),
		[]string{"makemigrations", "--project"},
		nil,
		&stdout,
		&stderr,
		nil,
	)
	if exit != 2 || stdout.Len() != 0 || stderr.String() != projectcheck.MakemigrationsCategoryCommand+"/"+projectcheck.MakemigrationsCodeInvalidArguments+"\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}
