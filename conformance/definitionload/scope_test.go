package definitionload

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDefinitionLoadProofRemainsTestOnlyAndOutsideProductImports(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not resolve the proof source")
	}
	proofDirectory := filepath.Dir(currentFile)
	repositoryRoot := filepath.Dir(filepath.Dir(proofDirectory))
	plannerCallSites := 0
	executorMigrateCallSites := 0

	err := filepath.WalkDir(proofDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			t.Errorf("definitionload Go source %s is importable; every proof source must end in _test.go", path)
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			selector, isSelector := call.Fun.(*ast.SelectorExpr)
			if !isSelector {
				return true
			}
			if packageName, isIdentifier := selector.X.(*ast.Ident); isIdentifier && packageName.Name == "migrations" && selector.Sel.Name == "NewPlanner" {
				plannerCallSites++
			}
			if selector.Sel.Name == "Migrate" {
				executorMigrateCallSites++
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk definitionload proof: %v", err)
	}
	if plannerCallSites != 1 {
		t.Errorf("direct migrations.NewPlanner call sites = %d, want exactly 1", plannerCallSites)
	}
	if executorMigrateCallSites != 1 {
		t.Errorf("public Executor.Migrate call sites = %d, want exactly 1", executorMigrateCallSites)
	}

	const forbiddenImport = "github.com/progresshans/godj/conformance/definitionload"
	fileSet := token.NewFileSet()
	err = filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || filepath.Clean(filepath.Dir(path)) == filepath.Clean(proofDirectory) {
			return nil
		}
		parsed, parseErr := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imported := range parsed.Imports {
			importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if importPath == forbiddenImport || strings.HasPrefix(importPath, forbiddenImport+"/") {
				t.Errorf("%s imports test-only definitionload proof %q", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository imports: %v", err)
	}
}
