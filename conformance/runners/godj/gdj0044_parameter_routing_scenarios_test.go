package godj

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0044ParameterizedRoutingHandlersObservePublicProduct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		scenario string
		phase    protocol.Phase
	}{
		{id: "WEB-028", scenario: "drf.parameter_routing.static_parameter_coexistence", phase: protocol.PhaseEvaluation},
		{id: "WEB-029", scenario: "drf.parameter_routing.nonnegative_int64_parameter", phase: protocol.PhaseEvaluation},
		{id: "WEB-030", scenario: "drf.parameter_routing.static_precedence_order_independent", phase: protocol.PhaseEvaluation},
		{id: "WEB-031", scenario: "drf.parameter_routing.named_reverse_boundaries", phase: protocol.PhaseConstruction},
		{id: "WEB-032", scenario: "drf.parameter_routing.ambiguous_route_rejection", phase: protocol.PhaseConstruction},
		{id: "WEB-033", scenario: "drf.parameter_routing.invalid_route_and_resource_caps", phase: protocol.PhaseConstruction},
		{id: "WEB-034", scenario: "drf.parameter_routing.trailing_slash_and_invalid_path_404", phase: protocol.PhaseEvaluation},
		{id: "WEB-035", scenario: "drf.parameter_routing.method_not_allowed_allow_header", phase: protocol.PhaseEvaluation},
	}
	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			t.Parallel()
			handler, ok := parameterRoutingScenarioHandler(test.scenario)
			if !ok {
				t.Fatalf("scenario %q is not registered in the local parameter handler", test.scenario)
			}
			observation, err := handler(context.Background(), protocol.Contract{
				ID: test.id, Scenario: test.scenario, Phase: test.phase,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ID != test.id || observation.Status != protocol.StatusObserved || observation.Phase != test.phase {
				t.Fatalf("observation envelope = %#v", observation)
			}
			if observation.Result == nil || observation.Metrics == nil || observation.DBState != nil || observation.Error != nil {
				t.Fatalf("observation dimensions = %#v", observation)
			}
			if err := observation.Result.Validate(); err != nil {
				t.Fatalf("result: %v", err)
			}
			if err := observation.Metrics.Validate(); err != nil {
				t.Fatalf("metrics: %v", err)
			}
		})
	}
}

func TestGDJ0044MethodNotAllowedObservationUsesStableSortedAllow(t *testing.T) {
	t.Parallel()

	handler, ok := parameterRoutingScenarioHandler("drf.parameter_routing.method_not_allowed_allow_header")
	if !ok {
		t.Fatal("method-not-allowed scenario is not registered")
	}
	observation, err := handler(context.Background(), protocol.Contract{
		ID: "WEB-035", Scenario: "drf.parameter_routing.method_not_allowed_allow_header", Phase: protocol.PhaseEvaluation,
	})
	if err != nil {
		t.Fatal(err)
	}
	allow := parameterRoutingTestObjectField(t, *observation.Result, "allow")
	got := make([]string, len(allow.Items))
	for index, value := range allow.Items {
		if value.Type != protocol.ValueString || value.Text == nil {
			t.Fatalf("allow[%d] = %#v", index, value)
		}
		got[index] = *value.Text
	}
	want := []string{"DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "PUT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Allow = %#v, want %#v", got, want)
	}
	response := parameterRoutingTestObjectField(t, *observation.Result, "response")
	status := parameterRoutingTestObjectField(t, response, "status")
	if status.Text == nil || *status.Text != "405" {
		t.Fatalf("status = %#v, want 405", status)
	}
}

func TestGDJ0044UnknownParameterizedRoutingScenarioStaysLocalFailClosed(t *testing.T) {
	t.Parallel()

	if handler, ok := parameterRoutingScenarioHandler("drf.parameter_routing.unknown"); ok || handler != nil {
		t.Fatalf("unknown handler = %v, %t", handler, ok)
	}
}

func TestGDJ0044LocalActualHandlersCoverBothManifestScenarioSets(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	sets := []struct {
		manifest string
		lookup   func(string) (scenarioHandler, bool)
	}{
		{manifest: "parameter-routing-manifest.json", lookup: parameterRoutingScenarioHandler},
		{manifest: "article-api-manifest.json", lookup: articleAPIScenarioHandler},
	}
	for _, set := range sets {
		manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", set.manifest))
		if err != nil {
			t.Fatal(err)
		}
		for _, contract := range manifest.Contracts {
			if _, ok := set.lookup(contract.Scenario); !ok {
				t.Fatalf("%s scenario %q has no local actual handler", contract.ID, contract.Scenario)
			}
		}
	}
}

func TestGDJ0044ActualSourcesDoNotReadReferenceOrExpectedArtifacts(t *testing.T) {
	t.Parallel()

	files := []string{
		"gdj0044_parameter_routing_scenarios.go",
		"gdj0044_article_api_fixture.go",
		"gdj0044_article_api_scenarios.go",
	}
	forbidden := []string{
		"conformance/oracles",
		"conformance/reference",
		"conformance/fixtures",
		"parameter-routing-oracle",
		"article-api-oracle",
		"not-implemented.json",
	}
	for _, file := range files {
		contents, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range forbidden {
			if strings.Contains(string(contents), value) {
				t.Fatalf("%s contains forbidden expected/reference dependency %q", file, value)
			}
		}
	}
}

func TestGDJ0044ParameterizedReverseRejectsNonInt64ExternalConsumers(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeGDJ0044CompileFile(t, filepath.Join(directory, "go.mod"), "module example.com/godj-gdj0044-admission\n\ngo 1.26.5\n\nrequire github.com/progresshans/godj v0.0.0\n\nreplace github.com/progresshans/godj => "+filepath.ToSlash(repository)+"\n")
	writeGDJ0044CompileFile(t, filepath.Join(directory, "admission_test.go"), `package admission

import "github.com/progresshans/godj/web"

var _ = web.Int64Argument("pk", int64(1))
`)
	if output, err := runGDJ0044ExternalCompile(t, directory); err != nil {
		t.Fatalf("valid int64 external consumer did not compile: %v\n%s", err, output)
	}

	writeGDJ0044CompileFile(t, filepath.Join(directory, "admission_test.go"), `package admission

import "github.com/progresshans/godj/web"

var invalidBoolean = web.Int64Argument("pk", true)
var invalidString = web.Int64Argument("pk", "1")
var invalidPathInjection = web.Int64Argument("pk", "1/../2")
var invalidOverflow = web.Int64Argument("pk", 9223372036854775808)
`)
	output, err := runGDJ0044ExternalCompile(t, directory)
	if err == nil {
		t.Fatalf("non-int64 external consumers unexpectedly compiled:\n%s", output)
	}
	for _, marker := range []string{"cannot use true", `cannot use "1"`, `cannot use "1/../2"`, "9223372036854775808", "(overflows)"} {
		if !strings.Contains(output, marker) {
			t.Fatalf("external compiler output lacks %q:\n%s", marker, output)
		}
	}
}

func TestGDJ0044ReverseArgumentExportedSurfaceRemainsClosed(t *testing.T) {
	webDirectory, err := filepath.Abs(filepath.Join("..", "..", "..", "web"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(webDirectory)
	if err != nil {
		t.Fatal(err)
	}
	typeFound := false
	constructors := make([]string, 0, 1)
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, filepath.Join(webDirectory, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.GenDecl:
				for _, specification := range typed.Specs {
					switch specification := specification.(type) {
					case *ast.TypeSpec:
						if specification.Name.Name != "ReverseArgument" {
							if gdj0044TypeExposesReverseArgument(specification.Type) {
								t.Fatalf("web type %s aliases or wraps ReverseArgument", specification.Name.Name)
							}
							continue
						}
						typeFound = true
						structure, ok := specification.Type.(*ast.StructType)
						if !ok {
							t.Fatal("web.ReverseArgument is no longer a closed struct")
						}
						for _, field := range structure.Fields.List {
							if len(field.Names) == 0 {
								t.Fatal("web.ReverseArgument exposes an embedded field")
							}
							for _, name := range field.Names {
								if name.IsExported() {
									t.Fatalf("web.ReverseArgument exposes field %s", name.Name)
								}
							}
						}
					case *ast.ValueSpec:
						if typed.Tok == token.VAR {
							for _, name := range specification.Names {
								if name.IsExported() {
									t.Fatalf("web exposes mutable package variable %s; closed ReverseArgument values cannot be inferred safely", name.Name)
								}
							}
						}
						if !gdj0044ValueSpecContainsReverseArgument(specification) {
							continue
						}
						for _, name := range specification.Names {
							if name.IsExported() {
								t.Fatalf("web exposes ReverseArgument value %s", name.Name)
							}
						}
					}
				}
			case *ast.FuncDecl:
				if !typed.Name.IsExported() {
					continue
				}
				if typed.Recv != nil && gdj0044FieldListContainsReverseArgument(typed.Recv) {
					t.Fatalf("web.ReverseArgument exposes method %s", typed.Name.Name)
				}
				if gdj0044FieldListContainsReverseArgument(typed.Type.Results) {
					constructors = append(constructors, typed.Name.Name)
				}
				if gdj0044FieldListContainsReverseArgumentPointer(typed.Type.Params) {
					t.Fatalf("web function %s can mutate ReverseArgument through a pointer", typed.Name.Name)
				}
			}
		}
	}
	if !typeFound {
		t.Fatal("web.ReverseArgument declaration is absent")
	}
	if !reflect.DeepEqual(constructors, []string{"Int64Argument"}) {
		t.Fatalf("exported ReverseArgument constructors = %#v, want only Int64Argument", constructors)
	}
}

func gdj0044FieldListContainsReverseArgument(fields *ast.FieldList) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		if gdj0044TypeContainsReverseArgument(field.Type) {
			return true
		}
	}
	return false
}

func gdj0044FieldListContainsReverseArgumentPointer(fields *ast.FieldList) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		if gdj0044TypeContainsReverseArgumentPointer(field.Type) {
			return true
		}
	}
	return false
}

func gdj0044TypeContainsReverseArgument(expression ast.Expr) bool {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name == "ReverseArgument"
	case *ast.StarExpr:
		return gdj0044TypeContainsReverseArgument(typed.X)
	case *ast.ArrayType:
		return gdj0044TypeContainsReverseArgument(typed.Elt)
	case *ast.Ellipsis:
		return gdj0044TypeContainsReverseArgument(typed.Elt)
	case *ast.MapType:
		return gdj0044TypeContainsReverseArgument(typed.Key) || gdj0044TypeContainsReverseArgument(typed.Value)
	case *ast.ChanType:
		return gdj0044TypeContainsReverseArgument(typed.Value)
	case *ast.StructType:
		return gdj0044FieldListContainsReverseArgument(typed.Fields)
	case *ast.InterfaceType:
		return gdj0044FieldListContainsReverseArgument(typed.Methods)
	case *ast.FuncType:
		return gdj0044FieldListContainsReverseArgument(typed.Params) || gdj0044FieldListContainsReverseArgument(typed.Results)
	case *ast.ParenExpr:
		return gdj0044TypeContainsReverseArgument(typed.X)
	case *ast.IndexExpr:
		return gdj0044TypeContainsReverseArgument(typed.X) || gdj0044TypeContainsReverseArgument(typed.Index)
	case *ast.IndexListExpr:
		if gdj0044TypeContainsReverseArgument(typed.X) {
			return true
		}
		for _, index := range typed.Indices {
			if gdj0044TypeContainsReverseArgument(index) {
				return true
			}
		}
		return false
	case *ast.CompositeLit:
		if gdj0044TypeContainsReverseArgument(typed.Type) {
			return true
		}
		for _, element := range typed.Elts {
			if gdj0044TypeContainsReverseArgument(element) {
				return true
			}
		}
		return false
	case *ast.CallExpr:
		if identifier, ok := typed.Fun.(*ast.Ident); ok && identifier.Name == "Int64Argument" {
			return true
		}
		for _, argument := range typed.Args {
			if gdj0044TypeContainsReverseArgument(argument) {
				return true
			}
		}
		return false
	case *ast.UnaryExpr:
		return gdj0044TypeContainsReverseArgument(typed.X)
	case *ast.KeyValueExpr:
		return gdj0044TypeContainsReverseArgument(typed.Value)
	default:
		return false
	}
}

func gdj0044TypeExposesReverseArgument(expression ast.Expr) bool {
	structure, ok := expression.(*ast.StructType)
	if !ok {
		return gdj0044TypeContainsReverseArgument(expression)
	}
	for _, field := range structure.Fields.List {
		if !gdj0044TypeContainsReverseArgument(field.Type) {
			continue
		}
		if len(field.Names) == 0 {
			return true
		}
		for _, name := range field.Names {
			if name.IsExported() {
				return true
			}
		}
	}
	return false
}

func gdj0044TypeContainsReverseArgumentPointer(expression ast.Expr) bool {
	switch typed := expression.(type) {
	case *ast.StarExpr:
		return gdj0044TypeContainsReverseArgument(typed.X)
	case *ast.ArrayType:
		return gdj0044TypeContainsReverseArgumentPointer(typed.Elt)
	case *ast.Ellipsis:
		return gdj0044TypeContainsReverseArgumentPointer(typed.Elt)
	case *ast.MapType:
		return gdj0044TypeContainsReverseArgumentPointer(typed.Key) || gdj0044TypeContainsReverseArgumentPointer(typed.Value)
	case *ast.ChanType:
		return gdj0044TypeContainsReverseArgumentPointer(typed.Value)
	case *ast.StructType:
		return gdj0044FieldListContainsReverseArgumentPointer(typed.Fields)
	case *ast.InterfaceType:
		return gdj0044FieldListContainsReverseArgumentPointer(typed.Methods)
	case *ast.FuncType:
		return gdj0044FieldListContainsReverseArgumentPointer(typed.Params) || gdj0044FieldListContainsReverseArgumentPointer(typed.Results)
	case *ast.ParenExpr:
		return gdj0044TypeContainsReverseArgumentPointer(typed.X)
	default:
		return false
	}
}

func gdj0044ValueSpecContainsReverseArgument(specification *ast.ValueSpec) bool {
	if specification.Type != nil && gdj0044TypeContainsReverseArgument(specification.Type) {
		return true
	}
	for _, value := range specification.Values {
		if gdj0044TypeContainsReverseArgument(value) {
			return true
		}
	}
	return false
}

func writeGDJ0044CompileFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runGDJ0044ExternalCompile(t *testing.T, directory string) (string, error) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = append(os.Environ(), "GOFLAGS=", "GOTOOLCHAIN=local", "GOWORK=off")
	output, err := command.CombinedOutput()
	return string(output), err
}

func parameterRoutingTestObjectField(t *testing.T, value protocol.Value, name string) protocol.Value {
	t.Helper()
	if value.Type != protocol.ValueObject {
		t.Fatalf("value = %#v, want object", value)
	}
	for _, field := range value.Fields {
		if field.Name == name {
			return field.Value
		}
	}
	t.Fatalf("object has no field %q: %#v", name, value)
	return protocol.Value{}
}
