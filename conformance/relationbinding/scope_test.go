package relationbinding

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRelationBindingProofRemainsTestOnlyProductFreeAndArtifactBlind(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not resolve relation-binding proof")
	}
	proofDirectory := filepath.Dir(currentFile)
	repositoryRoot := filepath.Dir(filepath.Dir(proofDirectory))
	productModule := "github.com/progresshans/" + "godj"
	proofImport := productModule + "/conformance/" + "relationbinding"
	forbiddenArtifactNames := []string{
		"conformance/" + "contracts",
		"conformance/" + "oracles",
		"conformance/" + "fixtures",
		"relation-" + "manifest.json",
		"relation-" + "oracle.json",
		"godj-relation-" + "not-implemented.json",
	}

	fileSet := token.NewFileSet()
	err := filepath.WalkDir(proofDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		relative, err := filepath.Rel(proofDirectory, path)
		if err != nil {
			return err
		}
		inExternalFixture := strings.HasPrefix(filepath.ToSlash(relative), "testdata/external/")
		if !inExternalFixture && !strings.HasSuffix(entry.Name(), "_test.go") {
			t.Errorf("relation-binding candidate %s is importable; root proof Go files must end in _test.go", relative)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(contents)
		for _, forbidden := range forbiddenArtifactNames {
			if strings.Contains(text, forbidden) {
				t.Errorf("relation-binding source %s names forbidden artifact path %q", relative, forbidden)
			}
		}
		parsed, err := parser.ParseFile(fileSet, path, contents, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if importPath == productModule || strings.HasPrefix(importPath, productModule+"/") {
				t.Errorf("relation-binding source %s imports product package %q", relative, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan relation-binding proof: %v", err)
	}

	err = filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || path == proofDirectory {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if importPath == proofImport || strings.HasPrefix(importPath, proofImport+"/") {
				t.Errorf("repository source %s imports test-only proof %q", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository product imports: %v", err)
	}

	moduleDocument, err := os.ReadFile(filepath.Join(proofDirectory, "testdata", "external", "go.mod"))
	if err != nil {
		t.Fatalf("read external fixture module: %v", err)
	}
	moduleText := string(moduleDocument)
	if strings.Contains(moduleText, "require") || strings.Contains(moduleText, "replace") || strings.Contains(moduleText, productModule) {
		t.Fatalf("external compile fixture is not self-contained:\n%s", moduleText)
	}
}
