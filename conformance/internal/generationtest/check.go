// Package generationtest checks independently generated candidates against the
// checked-in relation fixtures. It does not build or load reference observations.
package generationtest

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type Candidate struct {
	Path string
	Data []byte
}

func Bytes(t *testing.T, generate func() ([]byte, error)) []byte {
	t.Helper()
	contents, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func Check(t *testing.T, root string, candidates []Candidate) {
	t.Helper()
	if err := verify(t.Context(), root, candidates); err != nil {
		t.Fatal(err)
	}
}

func verify(ctx context.Context, root string, candidates []Candidate) error {
	if len(candidates) == 0 {
		return fmt.Errorf("generated candidate set is empty")
	}
	wanted := make([]string, 0, len(candidates))
	directories := map[string]bool{}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(candidate.Path)))
		if err != nil {
			return err
		}
		if !bytes.Equal(contents, candidate.Data) {
			return fmt.Errorf("checked-in generated file %s differs from deterministic candidate", candidate.Path)
		}
		wanted = append(wanted, candidate.Path)
		directories[strings.SplitN(candidate.Path, "/", 2)[0]] = true
	}
	var actual []string
	for directory := range directories {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "zz_godj_") && strings.HasSuffix(entry.Name(), ".go") {
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				actual = append(actual, filepath.ToSlash(relative))
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	slices.Sort(actual)
	slices.Sort(wanted)
	if !slices.Equal(actual, wanted) {
		return fmt.Errorf("generated file inventory = %v, want %v", actual, wanted)
	}
	return nil
}
