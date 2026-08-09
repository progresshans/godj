//go:build darwin || linux

package projectcheck

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedDirectoryRejectsPreOpenReplacement(t *testing.T) {
	t.Parallel()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := path + "-original"
	t.Cleanup(func() { _ = os.RemoveAll(original) })
	handle, _, err := openRetainedDirectoryWithHook(path, func() {
		if renameErr := os.Rename(path, original); renameErr != nil {
			t.Fatal(renameErr)
		}
		if mkdirErr := os.Mkdir(path, 0o700); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
	})
	if handle != nil {
		_ = handle.Close()
	}
	if err == nil {
		t.Fatal("pre-open directory replacement was accepted")
	}
}

func TestImplicitSelectionRejectsReplacementAfterEnumeration(t *testing.T) {
	t.Parallel()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := path + "-original"
	t.Cleanup(func() { _ = os.RemoveAll(original) })
	replaced := false
	report := Report{}
	project, primary := selectProjectWithHooks(path, commandArguments{}, &report, selectionHooks{
		afterDirectoryScan: func(current string) {
			if replaced {
				return
			}
			replaced = true
			if renameErr := os.Rename(current, original); renameErr != nil {
				t.Fatal(renameErr)
			}
			if mkdirErr := os.Mkdir(current, 0o700); mkdirErr != nil {
				t.Fatal(mkdirErr)
			}
		},
	})
	_ = project.close()
	if primary == nil || primary.Code != "project_selection_failed" || report.AncestorDirectoriesInspected != 1 {
		t.Fatalf("post-enumeration replacement = %+v report=%+v", primary, report)
	}
}

func TestDirectoryEnumerationErrorPrecedesReturnedMarker(t *testing.T) {
	t.Parallel()
	calls := 0
	found, err := scanDirectoryForExact(func(int) ([]os.DirEntry, error) {
		calls++
		return []os.DirEntry{fakeDirEntry{name: descriptorName}}, io.ErrUnexpectedEOF
	}, descriptorName)
	if err == nil || found || calls != 1 {
		t.Fatalf("marker plus I/O = found=%v err=%v calls=%d", found, err, calls)
	}
}

func TestSelectionCaseNearestAndExplicitMetrics(t *testing.T) {
	t.Run("wrong-case marker is never accepted", func(t *testing.T) {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "GODJ.TOML"), canonicalDescriptor(), 0o600); err != nil {
			t.Fatal(err)
		}
		report := Report{}
		project, primary := selectProject(root, commandArguments{}, &report)
		_ = project.close()
		if primary == nil || primary.Code != "project_not_found" || report.DescriptorReads != 0 || report.AncestorDirectoriesInspected == 0 {
			t.Fatalf("wrong-case marker = %+v report=%+v", primary, report)
		}
	})

	t.Run("invalid nearest marker never falls back outward", func(t *testing.T) {
		outer, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outer, descriptorName), canonicalDescriptor(), 0o600); err != nil {
			t.Fatal(err)
		}
		nearest := filepath.Join(outer, "nearest")
		if err := os.Mkdir(nearest, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nearest, descriptorName), []byte("format_version = 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		report := Report{}
		project, primary := selectProject(nearest, commandArguments{}, &report)
		_ = project.close()
		if primary == nil || primary.Code != "invalid_project_descriptor" || report.AncestorDirectoriesInspected != 1 || report.DescriptorReads != 1 {
			t.Fatalf("invalid nearest marker = %+v report=%+v", primary, report)
		}
	})

	t.Run("explicit descriptor does not scan ancestors", func(t *testing.T) {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		descriptor := filepath.Join(root, descriptorName)
		if err := os.WriteFile(descriptor, canonicalDescriptor(), 0o600); err != nil {
			t.Fatal(err)
		}
		report := Report{}
		project, primary := selectProject(root, commandArguments{explicitDescriptor: descriptor}, &report)
		defer func() { _ = project.close() }()
		if primary != nil || report.AncestorDirectoriesInspected != 0 || report.DescriptorReads != 1 || project.root == nil {
			t.Fatalf("explicit descriptor = %+v report=%+v project=%+v", primary, report, project)
		}
	})

	for _, test := range []struct {
		name string
		path func(*testing.T, string) string
	}{
		{name: "absent parent", path: func(_ *testing.T, root string) string { return filepath.Join(root, "absent", descriptorName) }},
		{name: "non-directory parent", path: func(t *testing.T, root string) string {
			parent := filepath.Join(root, "file-parent")
			if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(parent, descriptorName)
		}},
	} {
		t.Run("explicit "+test.name+" is invalid without ancestor scan", func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			report := Report{}
			project, primary := selectProject(root, commandArguments{explicitDescriptor: test.path(t, root)}, &report)
			_ = project.close()
			if primary == nil || primary.Code != "invalid_project_descriptor" || report.AncestorDirectoriesInspected != 0 || report.DescriptorReads != 0 {
				t.Fatalf("explicit %s = %+v report=%+v", test.name, primary, report)
			}
		})
	}
}

func TestSelectedDescriptorReplacementIsNeverParsed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		hooks func(func(int, string)) selectionHooks
		swap  func(*testing.T, string, string)
	}{
		{
			name: "regular-to-symlink-after-stat",
			hooks: func(callback func(int, string)) selectionHooks {
				return selectionHooks{afterDescriptorStat: callback}
			},
			swap: func(t *testing.T, descriptor, original string) {
				if err := os.Rename(descriptor, original); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original, descriptor); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "identity-after-read",
			hooks: func(callback func(int, string)) selectionHooks {
				return selectionHooks{afterDescriptorRead: callback}
			},
			swap: func(t *testing.T, descriptor, original string) {
				if err := os.Rename(descriptor, original); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(descriptor, canonicalDescriptor(), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			descriptor := filepath.Join(root, descriptorName)
			original := filepath.Join(root, "original.toml")
			if err := os.WriteFile(descriptor, canonicalDescriptor(), 0o600); err != nil {
				t.Fatal(err)
			}
			swapped := false
			callback := func(int, string) {
				if swapped {
					return
				}
				swapped = true
				test.swap(t, descriptor, original)
			}
			report := Report{}
			project, primary := selectProjectWithHooks(root, commandArguments{}, &report, test.hooks(callback))
			_ = project.close()
			if primary == nil || primary.Code != "project_selection_failed" || report.DescriptorReads != 0 {
				t.Fatalf("descriptor replacement = %+v report=%+v", primary, report)
			}
		})
	}
}

type fakeDirEntry struct {
	name string
}

func (entry fakeDirEntry) Name() string         { return entry.name }
func (fakeDirEntry) IsDir() bool                { return false }
func (fakeDirEntry) Type() fs.FileMode          { return 0 }
func (fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }
