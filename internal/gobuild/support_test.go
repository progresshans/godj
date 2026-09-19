package gobuild

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnvironmentReusesOnlySafeExternalBuildCache(t *testing.T) {
	project := t.TempDir()
	cache := t.TempDir()
	private := []string{"HOME=/private/home", "TMPDIR=/private/tmp", "GOCACHE=/private/cache", "GOMODCACHE=/private/modules", "GOFLAGS=-modcacherw", "GOWORK=off", ColdBuildEnvironment + "=1"}
	before := append([]string(nil), private...)
	got := environmentValues(Environment(private, []string{"GOCACHE=" + cache}, project))
	physical, err := filepath.EvalSymlinks(cache)
	if err != nil {
		t.Fatal(err)
	}
	if got["GOCACHE"] != physical || got["GOFLAGS"] != "-modcacherw -trimpath" {
		t.Fatalf("reusable build environment = %v", got)
	}
	for key, want := range environmentValues(private) {
		if key != "GOCACHE" && key != "GOFLAGS" && key != ColdBuildEnvironment && got[key] != want {
			t.Errorf("%s changed from %q to %q", key, want, got[key])
		}
	}
	if _, exists := got[ColdBuildEnvironment]; exists || !reflect.DeepEqual(before, private) {
		t.Fatalf("host control leaked or input mutated: got=%v input=%v", got, private)
	}
	for _, ambient := range [][]string{
		{"GOCACHE=" + cache, ColdBuildEnvironment + "=1"},
		{"GOCACHE=off"},
	} {
		cold := environmentValues(Environment(private, ambient, project))
		if cold["GOCACHE"] != "/private/cache" || cold["GOFLAGS"] != "-modcacherw" {
			t.Fatalf("cold build reused cache: %v", cold)
		}
	}
}

func TestEnvironmentRejectsCacheWithinProjectThroughSymlinkOrUnsafePermissions(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	inside := filepath.Join(project, "cache")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "cache-alias")
	if err := os.Symlink(inside, alias); err != nil {
		t.Fatal(err)
	}
	shared := t.TempDir()
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	readonly := t.TempDir()
	if err := os.Chmod(readonly, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readonly, 0o700) })
	for _, candidate := range []string{project, parent, inside, alias, shared, readonly, "relative-cache", filepath.Join(parent, "missing")} {
		got := environmentValues(Environment([]string{"GOCACHE=/private/cache"}, []string{"GOCACHE=" + candidate}, project))
		if got["GOCACHE"] != "/private/cache" {
			t.Errorf("unsafe candidate %q reused: %v", candidate, got)
		}
	}
}

func TestSummaryPreservesBuildCauseAndRedactsSensitiveOutput(t *testing.T) {
	diagnostic := strings.Join([]string{
		"arbitrary private runner text must not be published",
		"/Users/private/project/main.go:12:4: undefined: MissingSymbol",
		`main.go:15:1: cannot use "literal-password" as string; password=loose-secret api_key: another-secret`,
		"go: module download https://name:password@private.example/repo: 403 Forbidden",
		"go: git failed: secret-env-value",
		"main.go:16:1: undefined: tiny",
		"\x1b[31mgo: fatal error: compiler failed\x1b[0m",
	}, "\n")
	got := Summary(nil, []byte(diagnostic), []string{"API_TOKEN=secret-env-value", "PASSWORD=tiny"})
	for _, wanted := range []string{"undefined: MissingSymbol", ":12:4:", "cannot use", "403 Forbidden", "fatal error: compiler failed"} {
		if !strings.Contains(got, wanted) {
			t.Errorf("missing useful cause %q in %q", wanted, got)
		}
	}
	for _, forbidden := range []string{"/Users/private", "literal-password", "loose-secret", "another-secret", "name:password", "private.example", "secret-env-value", "tiny", "private runner", "\x1b"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("sensitive output %q survived: %q", forbidden, got)
		}
	}
}

func TestSummaryAndCaptureStayBounded(t *testing.T) {
	var output strings.Builder
	for index := 0; index < 100; index++ {
		fmt.Fprintf(&output, "main.go:%d:1: undefined: Symbol%d\n", index+1, index)
	}
	got := Summary(nil, []byte(output.String()), nil)
	if len(got) > MaxDiagnosticBytes || len(strings.Split(got, "\n")) != maxDiagnosticLines {
		t.Fatalf("summary limits: bytes=%d lines=%d", len(got), len(strings.Split(got, "\n")))
	}
	oversized := []byte("go: " + strings.Repeat("x", MaxDiagnosticBytes*2))
	if got := Summary(nil, oversized, nil); got == "" || len(got) > MaxDiagnosticBytes {
		t.Fatalf("oversized diagnostic = %q", got)
	}
	if got := Summary(nil, []byte("private output"), nil); strings.Contains(got, "private output") || got == "" {
		t.Fatalf("unknown diagnostic = %q", got)
	}
	var capture Capture
	payload := []byte(strings.Repeat("x", 256<<10))
	if written, err := capture.Write(payload); err != nil || written != len(payload) || capture.Len() != len(payload) || len(capture.Bytes()) != 128<<10 {
		t.Fatalf("bounded drain: written=%d err=%v total=%d retained=%d", written, err, capture.Len(), len(capture.Bytes()))
	}
}

func TestBuildErrorPreservesClassificationWithoutPublishingRawCause(t *testing.T) {
	cause := errors.New("private raw cause")
	err := fmt.Errorf("candidate verification: %w", &Error{Cause: cause, Diagnostic: "main.go:1:1: undefined: Missing"})
	if !errors.Is(err, cause) || strings.Contains(err.Error(), cause.Error()) || Diagnostic(err) == "" {
		t.Fatalf("build error = %v", err)
	}
}
