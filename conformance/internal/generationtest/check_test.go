package generationtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedCheckDetectsDriftMissingAndExtraFiles(t *testing.T) {
	for _, test := range []struct {
		name, document, extra, want string
	}{
		{"matching", "package app\n", "", ""},
		{"byte drift", "package changed\n", "", "differs"},
		{"missing", "", "", "no such file"},
		{"extra generated", "package app\n", "zz_godj_stale.go", "inventory"},
		{"ordinary source", "package app\n", "models.go", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "app"), 0o700); err != nil {
				t.Fatal(err)
			}
			if test.document != "" {
				if err := os.WriteFile(filepath.Join(root, "app", "zz_godj_generated.go"), []byte(test.document), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.extra != "" {
				if err := os.WriteFile(filepath.Join(root, "app", test.extra), []byte("package app\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			candidate := []Candidate{{Path: "app/zz_godj_generated.go", Data: []byte("package app\n")}}
			err := verify(t.Context(), root, candidate)
			if test.name == "missing" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing file error = %v", err)
				}
			} else if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("verification = %v, want %q", err, test.want)
			}
		})
	}
}

func TestGeneratedCheckRejectsEmptyAndCanceledWork(t *testing.T) {
	if err := verify(t.Context(), t.TempDir(), nil); err == nil {
		t.Fatal("empty candidate set passed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := verify(ctx, t.TempDir(), []Candidate{{Path: "app/zz_godj_generated.go"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled verification = %v", err)
	}
}
