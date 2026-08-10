package compiletest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const modulePath = "github.com/progresshans/godj"

func TestExternalConsumerCompiles(t *testing.T) {
	for _, fixture := range []string{"external_consumer.go.txt", "write_external_consumer.go.txt", "save_external_consumer.go.txt", "migration_external_consumer.go.txt", "migration_definition_external_consumer.go.txt", "project_external_consumer.go.txt", "relation_project/external_consumer.go.txt", "relation_query/external_consumer.go.txt"} {
		result := compileFixture(t, fixture)
		if result.err != nil {
			t.Fatalf("external consumer %s did not compile: %v\n%s", fixture, result.err, result.output)
		}
	}
}

func TestTypedAPIMisuseDoesNotCompile(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		wantFragments []string
	}{
		{
			name:    "predicate model mismatch",
			fixture: "predicate_model_mismatch.go.txt",
			wantFragments: []string{
				"models.ArticleFields.Title.Exact",
				"orm.Predicate[Other]",
			},
		},
		{
			name:    "descriptor model mismatch",
			fixture: "descriptor_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.ModelDescriptor[Other]",
				"wrong type for method",
			},
		},
		{
			name:    "descriptor clone is required",
			fixture: "descriptor_clone_missing.go.txt",
			wantFragments: []string{
				"orm.ModelDescriptor[CustomModel]",
				"missing method CloneModel",
			},
		},
		{
			name:    "isnull requires bool",
			fixture: "isnull_string.go.txt",
			wantFragments: []string{
				"cannot use \"true\"",
				"as bool value",
			},
		},
		{
			name:    "nullable exact requires value",
			fixture: "nullable_exact_pointer.go.txt",
			wantFragments: []string{
				"cannot use (*string)(nil)",
				"as string value",
			},
		},
		{
			name:    "icontains requires string",
			fixture: "icontains_integer.go.txt",
			wantFragments: []string{
				"cannot use 123",
				"as string value",
			},
		},
		{
			name:    "non-null field has no null builder",
			fixture: "write_title_null.go.txt",
			wantFragments: []string{
				"WithTitleNull undefined",
			},
		},
		{
			name:    "write scalar type is static",
			fixture: "write_wrong_scalar.go.txt",
			wantFragments: []string{
				"cannot use \"false\"",
				"as bool value",
			},
		},
		{
			name:    "write input model mismatch",
			fixture: "write_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.CreateInput[Other]",
				"wrong type for method BuildCreate",
			},
		},
		{
			name:    "Save update field model mismatch",
			fixture: "save_field_model_mismatch.go.txt",
			wantFragments: []string{
				"cannot use orm.NewStringField[Other]",
				"orm.WritableField[models.Article]",
			},
		},
		{
			name:    "Save primary key is not writable",
			fixture: "save_primary_key_mask.go.txt",
			wantFragments: []string{
				"models.ArticleFields.ID",
				"orm.WritableField[models.Article]",
			},
		},
		{
			name:    "Save option model mismatch",
			fixture: "save_option_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.ForceInsert[Other]()",
				"orm.SaveOption[models.Article]",
			},
		},
		{
			name:    "QuerySet Iterate callback model mismatch",
			fixture: "query_iterate_model_mismatch.go.txt",
			wantFragments: []string{
				"cannot use func(Other) error",
				"func(models.Article) error",
			},
		},
		{
			name:    "QuerySet terminal result model mismatch",
			fixture: "query_terminal_result_mismatch.go.txt",
			wantFragments: []string{
				"cannot use article",
				"as Other value",
			},
		},
		{
			name:    "related predicate source model mismatch",
			fixture: "relation_query/predicate_source_mismatch.go.txt",
			wantFragments: []string{
				"relations.BlogPost.Author.Name.Exact",
				"orm.Predicate[authors.Author]",
			},
		},
		{
			name:    "forward relation target field mismatch",
			fixture: "relation_query/target_field_mismatch.go.txt",
			wantFragments: []string{
				"blog.PostFields.Title",
				"orm.StringField[authors.Author]",
			},
		},
		{
			name:    "related integer exact requires integer",
			fixture: "relation_query/integer_value_mismatch.go.txt",
			wantFragments: []string{
				"cannot use \"1\"",
				"as int64 value",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := compileFixture(t, test.fixture)
			if result.err == nil {
				t.Fatalf("fixture %s unexpectedly compiled", test.fixture)
			}
			for _, fragment := range test.wantFragments {
				if !strings.Contains(result.output, fragment) {
					t.Fatalf("compiler output for %s does not contain %q:\n%s", test.fixture, fragment, result.output)
				}
			}
		})
	}
}

func TestDirectPackageDependencyBoundaries(t *testing.T) {
	forbidden := []dependencyEdge{
		{from: modulePath + "/schema/ir", to: modulePath + "/orm"},
		{from: modulePath + "/query", to: modulePath + "/orm"},
		{from: modulePath + "/orm", to: modulePath + "/db/sqlite"},
		{from: modulePath + "/codegen", to: modulePath + "/examples/article/models"},
		{from: modulePath + "/examples/article/models", to: modulePath + "/codegen"},
		{from: modulePath + "/internal/cmd/m1generate", to: modulePath + "/examples/article/models"},
		{from: modulePath + "/migrations", to: modulePath + "/migrations/definition"},
		{from: modulePath + "/internal/projectcheck", to: modulePath + "/internal/projectcheck/linked"},
		{from: modulePath + "/internal/projectcheck/linked", to: modulePath + "/internal/projectcheck"},
		{from: modulePath + "/migrations", to: modulePath + "/internal/projectcheck"},
		{from: modulePath + "/migrations", to: modulePath + "/internal/projectcheck/linked"},
		{from: modulePath + "/migrations/definition", to: modulePath + "/internal/projectcheck/linked"},
	}

	packages := make([]string, 0, len(forbidden))
	for _, edge := range forbidden {
		if !slices.Contains(packages, edge.from) {
			packages = append(packages, edge.from)
		}
	}

	root := repositoryRoot(t)
	arguments := append([]string{"list", "-json"}, packages...)
	command := exec.Command("go", arguments...)
	command.Dir = root
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("load direct package imports: %v\n%s", err, output)
	}

	directImports := make(map[string][]string, len(packages))
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed struct {
			ImportPath string
			Imports    []string
		}
		err := decoder.Decode(&listed)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		directImports[listed.ImportPath] = listed.Imports
	}

	for _, edge := range forbidden {
		imports, ok := directImports[edge.from]
		if !ok {
			t.Errorf("go list did not return package %s", edge.from)
			continue
		}
		if slices.Contains(imports, edge.to) {
			t.Errorf("forbidden direct dependency exists: %s -> %s", edge.from, edge.to)
		}
	}
}

func TestProjectCheckDirectImportGraph(t *testing.T) {
	want := map[string][]string{
		modulePath + "/project": {
			modulePath + "/internal/projectcheck/linked",
		},
		modulePath + "/internal/projectcheck": {
			modulePath + "/internal/projectcheck/protocol",
		},
		modulePath + "/internal/projectcheck/linked": {
			modulePath + "/internal/projectcheck/protocol",
			modulePath + "/migrations",
			modulePath + "/migrations/definition",
		},
		modulePath + "/internal/projectcheck/protocol": nil,
	}

	packages := make([]string, 0, len(want))
	for packagePath := range want {
		packages = append(packages, packagePath)
	}
	slices.Sort(packages)
	command := exec.Command("go", append([]string{"list", "-json"}, packages...)...)
	command.Dir = repositoryRoot(t)
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("load project-check imports: %v\n%s", err, output)
	}

	seen := make(map[string]bool, len(want))
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed struct {
			ImportPath string
			Imports    []string
		}
		err := decoder.Decode(&listed)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode project-check package: %v", err)
		}
		required, expected := want[listed.ImportPath]
		if !expected {
			continue
		}
		seen[listed.ImportPath] = true
		for _, requiredImport := range required {
			if !slices.Contains(listed.Imports, requiredImport) {
				t.Errorf("%s does not import required package %s", listed.ImportPath, requiredImport)
			}
		}
		for _, imported := range listed.Imports {
			if !strings.HasPrefix(imported, modulePath+"/") {
				continue
			}
			if !slices.Contains(required, imported) {
				t.Errorf("%s has unexpected direct module import %s", listed.ImportPath, imported)
			}
		}
	}
	for packagePath := range want {
		if !seen[packagePath] {
			t.Errorf("go list did not return %s", packagePath)
		}
	}
}

type compileResult struct {
	output string
	err    error
}

func compileFixture(t *testing.T, fixture string) compileResult {
	t.Helper()

	root := repositoryRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "internal", "compiletest", "testdata", fixture))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}

	directory := t.TempDir()
	goMod := fmt.Sprintf(`module example.com/godj-compile-gate

go 1.26.0

require %s v0.0.0

replace %s => %s
`, modulePath, modulePath, filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write fixture go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "consumer.go"), source, 0o644); err != nil {
		t.Fatalf("write fixture source: %v", err)
	}

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}

type dependencyEdge struct {
	from string
	to   string
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve compile test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func commandEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOWORK=") || strings.HasPrefix(entry, "GOTOOLCHAIN=") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "GOWORK=off", "GOTOOLCHAIN=local")
	return environment
}
