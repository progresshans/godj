//go:build darwin || linux

package projectgenerate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSealedProjectRootRejectsReboundPathBeforeReadVerifyOrPublish(t *testing.T) {
	root := t.TempDir()
	var stat unix.Stat_t
	if err := unix.Lstat(root, &stat); err != nil {
		t.Fatal(err)
	}
	sealed, err := SealProjectRoot(root, uint64(stat.Dev), uint64(stat.Ino))
	if err != nil {
		t.Fatalf("SealProjectRoot() error = %v", err)
	}
	retained := root + ".selected"
	if err := os.Rename(root, retained); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "replacement.marker")
	if err := os.WriteFile(marker, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle := projectGenerateTestBundle(t)
	if _, err := CheckRoot(context.Background(), sealed, bundle); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("CheckRoot(rebound) error = %v, want conflict", err)
	}
	if _, err := NewGoCandidateVerifierRoot(sealed, bundle); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("NewGoCandidateVerifierRoot(rebound) error = %v, want conflict", err)
	}
	if err := PublishRoot(context.Background(), sealed, bundle, CandidateVerifyFunc(func(context.Context, string) error {
		t.Fatal("verifier called for rebound root")
		return nil
	})); !errors.Is(err, ErrGeneratedConflict) {
		t.Fatalf("PublishRoot(rebound) error = %v, want conflict", err)
	}
	contents, err := os.ReadFile(marker)
	if err != nil || string(contents) != "replacement" {
		t.Fatalf("replacement marker=%q error=%v", contents, err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".godj")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rebound root mutated: .godj error=%v", err)
	}
}
