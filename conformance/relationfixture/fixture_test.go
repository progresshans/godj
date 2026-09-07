package relationfixture_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	fixture "github.com/progresshans/godj/conformance/relationfixture"
	"github.com/progresshans/godj/internal/projectgenerate"
	projectgenerateprotocol "github.com/progresshans/godj/internal/projectgenerate/protocol"
)

const fixtureImport = "github.com/progresshans/godj/conformance/relationfixture/"

func TestGeneratedFixtureMatchesDeclaration(t *testing.T) {
	t.Parallel()
	bundle := generatedFixture(t)
	report, err := projectgenerate.Check(t.Context(), fixtureDirectory(t), bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("generated fixture drift: report=%+v, error=%v", report, err)
	}
}

func TestGeneratedAppsKeepIndependentDependencyGraphs(t *testing.T) {
	t.Parallel()
	command := fixtureGoCommand(t, fixtureDirectory(t), "list", "-json", fixtureImport+"authors", fixtureImport+"blog", fixtureImport+"project")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list shared fixture dependencies: %v", err)
	}
	type packageInfo struct {
		ImportPath string
		Imports    []string
		Deps       []string
	}
	listed := make(map[string]packageInfo)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var info packageInfo
		if err := decoder.Decode(&info); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if _, duplicate := listed[info.ImportPath]; duplicate {
			t.Fatalf("duplicate package in dependency output: %s", info.ImportPath)
		}
		listed[info.ImportPath] = info
	}
	for _, name := range []string{"authors", "blog", "project"} {
		if _, ok := listed[fixtureImport+name]; !ok {
			t.Fatalf("dependency output omitted %s", name)
		}
	}
	for _, edge := range [][2]string{{"authors", "blog"}, {"blog", "authors"}} {
		from := listed[fixtureImport+edge[0]]
		to := fixtureImport + edge[1]
		if slices.Contains(from.Imports, to) || slices.Contains(from.Deps, to) {
			t.Fatalf("generated app %s reaches %s", from.ImportPath, to)
		}
	}
	project := listed[fixtureImport+"project"]
	for _, app := range []string{"authors", "blog"} {
		if !slices.Contains(project.Imports, fixtureImport+app) || !slices.Contains(project.Deps, fixtureImport+app) {
			t.Fatalf("project companion does not own the %s app edge", app)
		}
	}
}

func TestRelationObserversRemainOracleBlind(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join(fixtureDirectory(t), "..", "relation*product", "observer.go"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("discover relation observers: paths=%v error=%v", paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := observerSourceBoundary(path, source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestObserverBoundaryRejectsExpectedDataAndFileReaders(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`package observer; import "os"`,
		`package observer; import _ "embed"`,
		`package observer; import expected "example.com/oracles/relation"`,
		`package observer; import expected "example.com/\x6fracles/relation"`,
		`package observer; const source = "relation-oracle.json"`,
		`package observer; const source = "/fixtures/expected.json"`,
		`package observer; const source = "relation-\x6fracle.json"`,
		`package observer; func notImplemented() {}`,
		`package observer; func broken( {`,
	} {
		if err := observerSourceBoundary("observer.go", []byte(source)); err == nil {
			t.Fatalf("unsafe observer source was accepted: %s", source)
		}
	}
	if err := observerSourceBoundary("observer.go", []byte("// Reads no not-implemented artifacts.\npackage observer; import \"context\"")); err != nil {
		t.Fatalf("safe source rejected: %v", err)
	}
}

func observerSourceBoundary(path string, source []byte) error {
	forbidden := []string{"/oracles/", "/static/", "/fixtures/", "relation-oracle", "not-implemented", "notimplemented", "not_implemented"}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return err
	}
	// Inspect decoded code literals and identifiers; explanatory comments are
	// not artifact dependencies. Decoding also catches escaped import paths.
	ast.Inspect(file, func(node ast.Node) bool {
		var value string
		switch node := node.(type) {
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				value, _ = strconv.Unquote(node.Value)
			}
		case *ast.Ident:
			value = strings.ToLower(node.Name)
		}
		for _, name := range forbidden {
			if strings.Contains(value, name) {
				err = fmt.Errorf("observer %s names expected artifact %q", path, name)
				return false
			}
		}
		return err == nil
	})
	if err != nil {
		return err
	}
	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return err
		}
		if slices.Contains([]string{"embed", "io/fs", "os", "path/filepath"}, imported) {
			return fmt.Errorf("observer %s imports file-reading package %q", path, imported)
		}
	}
	return nil
}

func TestDeclarationRunnerBootstrapsWithoutGeneratedOutputs(t *testing.T) {
	t.Parallel()
	root := fixtureDirectory(t)
	repository := filepath.Clean(filepath.Join(root, "..", ".."))
	bundle := generatedFixture(t)
	for _, mode := range []string{"missing", "broken"} {
		t.Run(mode, func(t *testing.T) {
			replacements := make(map[string]string)
			for _, file := range bundle.Files() {
				replacements[filepath.Join(root, filepath.FromSlash(file.Path))] = ""
			}
			if mode == "broken" {
				broken := filepath.Join(t.TempDir(), "broken.go")
				if err := os.WriteFile(broken, []byte("package authors\nfunc broken( {\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				replacements = map[string]string{filepath.Join(root, "authors", "zz_godj_generated.go"): broken}
			}
			document, err := json.Marshal(struct{ Replace map[string]string }{replacements})
			if err != nil {
				t.Fatal(err)
			}
			overlay := filepath.Join(t.TempDir(), "overlay.json")
			if err := os.WriteFile(overlay, document, 0o600); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(t.TempDir(), "projectrunner")
			build := fixtureGoCommand(t, repository, "build", "-overlay="+overlay, "-o", binary, fixtureImport+"cmd/projectrunner")
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build declaration runner with %s generated files: %v\n%s", mode, err, output)
			}
			command := exec.CommandContext(t.Context(), binary, projectgenerateprotocol.PrivateArgument)
			command.Stdin = bytes.NewReader(projectgenerateprotocol.RequestDocument())
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err != nil || stderr.Len() != 0 {
				t.Fatalf("run declaration runner: %v stderr=%q", err, stderr.String())
			}
			response, failure, failed := projectgenerateprotocol.ParseResponse(stdout.Bytes(), true)
			if failed || failure != (projectgenerateprotocol.Failure{}) || !response.OK {
				t.Fatalf("declaration response = %+v failure=%+v failed=%v", response, failure, failed)
			}
			actual, err := codegen.GenerateProject(response.ProjectSpec)
			if err != nil || !bytes.Equal(actual.Manifest(), bundle.Manifest()) {
				t.Fatalf("declaration runner returned a different project: %v", err)
			}
		})
	}
}

func generatedFixture(t *testing.T) codegen.GeneratedBundle {
	t.Helper()
	spec, err := fixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func fixtureDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate shared relation fixture")
	}
	return filepath.Dir(source)
}

func fixtureGoCommand(t *testing.T, directory string, args ...string) *exec.Cmd {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", args...)
	command.Dir = directory
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "GOWORK" && name != "GOPROXY" && name != "GOTOOLCHAIN" {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
	return command
}
