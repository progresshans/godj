//go:build darwin || linux

package projectgenerate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRenameNoReplacePreservesConcurrentDestination(t *testing.T) {
	for _, directory := range []bool{false, true} {
		name := "file"
		if directory {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			sourceParentPath := filepath.Join(root, "source")
			targetParentPath := filepath.Join(root, "target")
			if err := os.Mkdir(sourceParentPath, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(targetParentPath, 0o700); err != nil {
				t.Fatal(err)
			}
			sourcePath := filepath.Join(sourceParentPath, "entry")
			targetPath := filepath.Join(targetParentPath, "entry")
			if directory {
				if err := os.Mkdir(sourcePath, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(targetPath, 0o711); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(sourcePath, []byte("generated"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(targetPath, []byte("user"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			sourceParent, err := os.Open(sourceParentPath)
			if err != nil {
				t.Fatal(err)
			}
			defer sourceParent.Close()
			targetParent, err := os.Open(targetParentPath)
			if err != nil {
				t.Fatal(err)
			}
			defer targetParent.Close()
			err = renameNoReplace(int(sourceParent.Fd()), "entry", int(targetParent.Fd()), "entry")
			if !errors.Is(err, unix.EEXIST) {
				t.Fatalf("renameNoReplace() error = %v, want EEXIST", err)
			}
			if _, err := os.Lstat(sourcePath); err != nil {
				t.Fatalf("source was lost: %v", err)
			}
			if directory {
				info, err := os.Stat(targetPath)
				if err != nil || !info.IsDir() || info.Mode().Perm() != 0o711 {
					t.Fatalf("destination directory changed: info=%v error=%v", info, err)
				}
				return
			}
			contents, err := os.ReadFile(targetPath)
			if err != nil || string(contents) != "user" {
				t.Fatalf("destination file=%q error=%v", contents, err)
			}
		})
	}
}
