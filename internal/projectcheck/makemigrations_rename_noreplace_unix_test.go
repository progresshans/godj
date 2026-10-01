//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMakemigrationsRenameNoReplaceUsesKernelExclusivePrimitive(t *testing.T) {
	directoryPath := t.TempDir()
	directory, err := os.Open(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	write := func(name string, document []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directoryPath, name), document, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) []byte {
		t.Helper()
		document, err := os.ReadFile(filepath.Join(directoryPath, name))
		if err != nil {
			t.Fatal(err)
		}
		return document
	}

	write("source", []byte("first"))
	if err := makemigrationsRenameNoReplace(int(directory.Fd()), "source", int(directory.Fd()), "target"); err != nil {
		t.Fatalf("exclusive rename to absent target: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(directoryPath, "source")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("renamed source still exists: %v", err)
	}
	if actual := read("target"); !bytes.Equal(actual, []byte("first")) {
		t.Fatalf("renamed target = %q", actual)
	}

	write("second-source", []byte("second"))
	if err := makemigrationsRenameNoReplace(int(directory.Fd()), "second-source", int(directory.Fd()), "target"); !errors.Is(err, unix.EEXIST) {
		t.Fatalf("exclusive rename over existing target = %v, want EEXIST", err)
	}
	if actual := read("target"); !bytes.Equal(actual, []byte("first")) {
		t.Fatalf("existing target was overwritten: %q", actual)
	}
	if actual := read("second-source"); !bytes.Equal(actual, []byte("second")) {
		t.Fatalf("failed rename consumed source: %q", actual)
	}
}
