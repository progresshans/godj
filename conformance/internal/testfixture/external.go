// Package testfixture shares external-project preparation and assertions.
// Command budgets, output policy and process ownership remain with each test.
package testfixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func RepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	root, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(source), "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func AssertSeparateRoot(t *testing.T, repository, external string) {
	t.Helper()
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	resolvedExternal, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(resolvedRepository, resolvedExternal)
	if err != nil {
		t.Fatal(err)
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		t.Fatalf("external consumer root %q is inside repository %q", resolvedExternal, resolvedRepository)
	}
}

func AssertArtifactsRedacted(t *testing.T, root string, sensitive ...string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return nil
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range sensitive {
			if value != "" && bytes.Contains(document, []byte(value)) {
				return errors.New("external artifact contains a sensitive value")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func Environment(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func ApplicationHashes(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	result := make(map[string][sha256.Size]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("external application contains non-regular entry %s", path)
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[path] = sha256.Sum256(document)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func PostgresSafeError(err error) error {
	if err == nil {
		return nil
	}
	var structured interface{ SQLState() string }
	if errors.As(err, &structured) {
		return fmt.Errorf("PostgreSQL SQLSTATE %s", structured.SQLState())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("PostgreSQL operation timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("PostgreSQL operation was canceled")
	}
	return errors.New("PostgreSQL operation failed")
}

func UniqueCookie(cookies []*http.Cookie, name string) (*http.Cookie, error) {
	var found *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name != name {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("response published duplicate %s cookies", name)
		}
		clone := *cookie
		found = &clone
	}
	if found == nil {
		return nil, fmt.Errorf("response omitted the %s cookie", name)
	}
	return found, nil
}

func AuditApplicationSources(t *testing.T, repository, root string, allowedImports map[string]struct{}) {
	t.Helper()
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(document, []byte(resolvedRepository)) || bytes.Contains(document, []byte(filepath.ToSlash(resolvedRepository))) {
			return fmt.Errorf("application source %s contains repository absolute path", path)
		}
		parsed, err := parser.ParseFile(fileSet, path, document, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, specification := range parsed.Imports {
			importPath, err := strconv.Unquote(specification.Path.Value)
			if err != nil {
				return fmt.Errorf("decode import in %s: %w", path, err)
			}
			if !strings.HasPrefix(importPath, "github.com/progresshans/godj") {
				continue
			}
			if strings.Contains(importPath, "/internal/") || strings.Contains(importPath, "/conformance/") || strings.Contains(importPath, "/examples/") {
				return fmt.Errorf("application source %s imports forbidden GoDj package %s", path, importPath)
			}
			if _, ok := allowedImports[importPath]; !ok {
				return fmt.Errorf("application source %s imports non-allowlisted GoDj package %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func SortedEnvironment(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, len(keys))
	for index, key := range keys {
		result[index] = key + "=" + values[key]
	}
	return result
}
