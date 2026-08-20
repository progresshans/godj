package relationreverseproduct

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/relationreverseproduct/fixture"
)

func TestCheckedInGeneratedReverseRelationProjectMatchesEightDeterministicCandidates(t *testing.T) {
	t.Parallel()

	authorsSchema, err := fixture.AuthorsSchema()
	if err != nil {
		t.Fatal(err)
	}
	blogSchema, err := fixture.BlogSchema()
	if err != nil {
		t.Fatal(err)
	}
	const rootImport = "github.com/progresshans/godj/conformance/relationreverseproduct/"
	candidates := []struct {
		path string
		data []byte
	}{
		{path: "authors/zz_godj_generated.go", data: generated(t, func() ([]byte, error) { return codegen.Generate("authors", authorsSchema) })},
		{path: "authors/zz_godj_relation.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationMetadata("authors", authorsSchema) })},
		{path: "authors/zz_godj_relation_object.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationObject("authors", authorsSchema) })},
		{path: "blog/zz_godj_generated.go", data: generated(t, func() ([]byte, error) { return codegen.Generate("blog", blogSchema) })},
		{path: "blog/zz_godj_relation.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationMetadata("blog", blogSchema) })},
		{path: "blog/zz_godj_relation_object.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationObject("blog", blogSchema) })},
		{path: "project/zz_godj_bindings.go", data: generated(t, func() ([]byte, error) {
			return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
				{Alias: "authors", ImportPath: rootImport + "authors"},
				{Alias: "blog", ImportPath: rootImport + "blog"},
			})
		})},
		{path: "project/zz_godj_relation_reverse.go", data: generated(t, func() ([]byte, error) {
			return codegen.GenerateProjectRelationReverse("project", []codegen.RelationReversePackage{
				{Alias: "authors", ImportPath: rootImport + "authors", Schema: authorsSchema},
				{Alias: "blog", ImportPath: rootImport + "blog", Schema: blogSchema},
			})
		})},
	}

	root := relationReverseProductDirectory(t)
	for _, candidate := range candidates {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(candidate.path)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contents, candidate.data) {
			t.Fatalf("checked-in generated file %s differs from deterministic candidate", candidate.path)
		}
	}

	generatedFiles := []string{}
	for _, directory := range []string{"authors", "blog", "project"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "zz_godj_") && strings.HasSuffix(entry.Name(), ".go") {
				generatedFiles = append(generatedFiles, filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(generatedFiles)
	wantFiles := make([]string, len(candidates))
	for index := range candidates {
		wantFiles[index] = candidates[index].path
	}
	slices.Sort(wantFiles)
	if !reflect.DeepEqual(generatedFiles, wantFiles) {
		t.Fatalf("generated file inventory = %#v, want exact eight %#v", generatedFiles, wantFiles)
	}
}

func TestObserveExecutesExactREL005AccessorLookupAndDatabaseState(t *testing.T) {
	t.Parallel()

	got, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantAccessor := QueryMetrics{
		QueryCount:     1,
		StatementKinds: []string{"SELECT"},
		JoinKinds:      []string{},
	}
	wantLookup := QueryMetrics{
		QueryCount:         1,
		StatementKinds:     []string{"SELECT"},
		JoinKinds:          []string{"INNER"},
		InnerJoinCount:     1,
		LeftOuterJoinCount: 0,
	}
	if !reflect.DeepEqual(got.AccessorPostIDs, []int64{10, 11}) ||
		!reflect.DeepEqual(got.LookupAuthorIDs, []int64{1}) ||
		!reflect.DeepEqual(got.Accessor, wantAccessor) ||
		!reflect.DeepEqual(got.Lookup, wantLookup) {
		t.Fatalf("REL-005 result/metrics = %#v", got)
	}
	reviewer := int64(2)
	wantState := DatabaseState{
		Authors: []AuthorRow{{ID: 1, Name: "Ada"}, {ID: 2, Name: "Bob"}, {ID: 3, Name: "Cleo"}},
		Posts: []PostRow{
			{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewer},
			{ID: 11, Title: "Beta", AuthorID: 1},
			{ID: 12, Title: "Gamma", AuthorID: 3, ReviewerID: &reviewer},
		},
	}
	if !reflect.DeepEqual(got.DBState, wantState) {
		t.Fatalf("database state = %#v, want %#v", got.DBState, wantState)
	}
}

func TestObservationChangesForEveryOwnedREL005ResultStateAndMetricMutation(t *testing.T) {
	t.Parallel()

	base, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*fixtureConfig)
		check  func(*testing.T, Observation)
	}{
		{
			name: "accessor source key",
			mutate: func(config *fixtureConfig) {
				config.posts[1].authorID = 3
			},
			check: func(t *testing.T, got Observation) {
				if !reflect.DeepEqual(got.AccessorPostIDs, []int64{10}) {
					t.Fatalf("accessor IDs = %v, want [10]", got.AccessorPostIDs)
				}
			},
		},
		{
			name: "lookup terminal value",
			mutate: func(config *fixtureConfig) {
				config.posts[0].title = "Mutation Alpha"
			},
			check: func(t *testing.T, got Observation) {
				if len(got.LookupAuthorIDs) != 0 {
					t.Fatalf("lookup IDs = %v, want empty", got.LookupAuthorIDs)
				}
			},
		},
		{
			name: "accessor ordering",
			mutate: func(config *fixtureConfig) {
				config.accessorDescending = true
			},
			check: func(t *testing.T, got Observation) {
				if !reflect.DeepEqual(got.AccessorPostIDs, []int64{11, 10}) {
					t.Fatalf("accessor IDs = %v, want [11 10]", got.AccessorPostIDs)
				}
			},
		},
		{
			name: "database state",
			mutate: func(config *fixtureConfig) {
				config.authors[0].name = "Mutation Ada"
			},
			check: func(t *testing.T, got Observation) {
				if got.DBState.Authors[0].Name != "Mutation Ada" ||
					!reflect.DeepEqual(got.AccessorPostIDs, base.AccessorPostIDs) ||
					!reflect.DeepEqual(got.LookupAuthorIDs, base.LookupAuthorIDs) {
					t.Fatalf("state-only mutation = %#v", got)
				}
			},
		},
		{
			name: "lookup metrics",
			mutate: func(config *fixtureConfig) {
				config.repeatLookup = true
			},
			check: func(t *testing.T, got Observation) {
				if got.Lookup.QueryCount != 2 || got.Lookup.InnerJoinCount != 2 ||
					!reflect.DeepEqual(got.AccessorPostIDs, base.AccessorPostIDs) ||
					!reflect.DeepEqual(got.LookupAuthorIDs, base.LookupAuthorIDs) ||
					!reflect.DeepEqual(got.DBState, base.DBState) {
					t.Fatalf("metrics-only mutation = %#v", got)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			mutated, err := observe(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, mutated)
			if reflect.DeepEqual(mutated, base) {
				t.Fatal("owned REL-005 mutation produced an unchanged observation")
			}
		})
	}
}

func TestGeneratedAppsHaveNoAppToAppEdgesAndObserverIsOracleBlind(t *testing.T) {
	t.Parallel()

	root := relationReverseProductDirectory(t)
	for _, directory := range []string{"authors", "blog"} {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "zz_godj_") || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			for _, imported := range parsedImports(t, filepath.Join(root, directory, entry.Name())) {
				if strings.Contains(imported, "/relationreverseproduct/authors") || strings.Contains(imported, "/relationreverseproduct/blog") {
					t.Fatalf("generated app file %s/%s has app-to-app import %q", directory, entry.Name(), imported)
				}
			}
		}
	}
	for _, imported := range parsedImports(t, filepath.Join(root, "observer.go")) {
		for _, forbidden := range []string{"/oracles/", "/fixtures/", "relation-oracle", "not-implemented"} {
			if strings.Contains(imported, forbidden) {
				t.Fatalf("REL-005 observer imports expected artifact %q", imported)
			}
		}
	}

	const rootImport = "github.com/progresshans/godj/conformance/relationreverseproduct/"
	command := exec.Command("go", "list", "-json", rootImport+"authors", rootImport+"blog", rootImport+"project")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list reverse-relation project: %v", err)
	}
	type listedPackage struct {
		ImportPath string
		Imports    []string
		Deps       []string
	}
	listed := make(map[string]listedPackage, 3)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var candidate listedPackage
		if err := decoder.Decode(&candidate); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		listed[candidate.ImportPath] = candidate
	}
	authorsPackage := listed[rootImport+"authors"]
	blogPackage := listed[rootImport+"blog"]
	projectPackage := listed[rootImport+"project"]
	if slices.Contains(authorsPackage.Imports, rootImport+"blog") || slices.Contains(authorsPackage.Deps, rootImport+"blog") {
		t.Fatalf("authors app reaches blog: imports=%#v deps=%#v", authorsPackage.Imports, authorsPackage.Deps)
	}
	if slices.Contains(blogPackage.Imports, rootImport+"authors") || slices.Contains(blogPackage.Deps, rootImport+"authors") {
		t.Fatalf("blog app reaches authors: imports=%#v deps=%#v", blogPackage.Imports, blogPackage.Deps)
	}
	for _, app := range []string{rootImport + "authors", rootImport + "blog"} {
		if !slices.Contains(projectPackage.Imports, app) || !slices.Contains(projectPackage.Deps, app) {
			t.Fatalf("project companion does not own app edge %q: imports=%#v deps=%#v", app, projectPackage.Imports, projectPackage.Deps)
		}
	}
}

func parsedImports(t *testing.T, path string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	imports := make([]string, len(parsed.Imports))
	for index, imported := range parsed.Imports {
		imports[index] = strings.Trim(imported.Path.Value, `"`)
	}
	return imports
}

func generated(t *testing.T, generate func() ([]byte, error)) []byte {
	t.Helper()
	contents, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func relationReverseProductDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate reverse relation product test source")
	}
	return filepath.Dir(source)
}
