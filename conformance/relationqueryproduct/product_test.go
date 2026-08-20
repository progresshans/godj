package relationqueryproduct

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/relationqueryproduct/blog"
	"github.com/progresshans/godj/conformance/relationqueryproduct/fixture"
	"github.com/progresshans/godj/conformance/relationqueryproduct/project"
	"github.com/progresshans/godj/orm"
)

func TestCheckedInGeneratedRelationQueryProjectMatchesDeterministicCandidates(t *testing.T) {
	t.Parallel()

	authorsSchema, err := fixture.AuthorsSchema()
	if err != nil {
		t.Fatal(err)
	}
	blogSchema, err := fixture.BlogSchema()
	if err != nil {
		t.Fatal(err)
	}
	const rootImport = "github.com/progresshans/godj/conformance/relationqueryproduct/"
	candidates := []struct {
		path string
		data []byte
	}{
		{path: "authors/zz_godj_generated.go", data: generated(t, func() ([]byte, error) { return codegen.Generate("authors", authorsSchema) })},
		{path: "authors/zz_godj_relation.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationMetadata("authors", authorsSchema) })},
		{path: "blog/zz_godj_generated.go", data: generated(t, func() ([]byte, error) { return codegen.Generate("blog", blogSchema) })},
		{path: "blog/zz_godj_relation.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationMetadata("blog", blogSchema) })},
		{path: "project/zz_godj_bindings.go", data: generated(t, func() ([]byte, error) {
			return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
				{Alias: "authors", ImportPath: rootImport + "authors"},
				{Alias: "blog", ImportPath: rootImport + "blog"},
			})
		})},
		{path: "project/zz_godj_relation_query.go", data: generated(t, func() ([]byte, error) {
			return codegen.GenerateProjectRelationQuery("project", []codegen.RelationQueryPackage{
				{Alias: "authors", ImportPath: rootImport + "authors", Schema: authorsSchema},
				{Alias: "blog", ImportPath: rootImport + "blog", Schema: blogSchema},
			})
		})},
	}
	root := relationQueryProductDirectory(t)
	for _, candidate := range candidates {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(candidate.path)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contents, candidate.data) {
			t.Fatalf("checked-in generated file %s differs from deterministic candidate", candidate.path)
		}
	}
}

func TestObserveExecutesExactREL004CasesAndDatabaseState(t *testing.T) {
	t.Parallel()

	got, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantMetric := QueryMetrics{
		QueryCount:         1,
		StatementKinds:     []string{"SELECT"},
		JoinKinds:          []string{"INNER"},
		InnerJoinCount:     1,
		LeftOuterJoinCount: 0,
	}
	wantConstruction := QueryMetrics{StatementKinds: []string{}, JoinKinds: []string{}}
	wantCases := []CaseObservation{
		{Name: "one_predicate", PostIDs: []int64{10, 11}, Construction: wantConstruction, Evaluation: wantMetric},
		{Name: "two_predicates", PostIDs: []int64{10, 11}, Construction: wantConstruction, Evaluation: wantMetric},
	}
	if !reflect.DeepEqual(got.Cases, wantCases) {
		t.Fatalf("cases = %#v, want %#v", got.Cases, wantCases)
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

func TestObservationChangesForEachOwnedREL004Mutation(t *testing.T) {
	t.Parallel()

	base, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		mutate      func(*fixtureConfig)
		wantPostIDs [][]int64
	}{
		{
			name: "author name",
			mutate: func(config *fixtureConfig) {
				config.authors[0].name = "Adele"
			},
			wantPostIDs: [][]int64{{}, {}},
		},
		{
			name: "foreign key identity",
			mutate: func(config *fixtureConfig) {
				config.posts[2].authorID = 1
			},
			wantPostIDs: [][]int64{{10, 11, 12}, {10, 11, 12}},
		},
		{
			name: "terminal target field",
			mutate: func(config *fixtureConfig) {
				config.firstPredicateTerminalID = true
			},
			wantPostIDs: [][]int64{{}, {}},
		},
		{
			name: "row ordering",
			mutate: func(config *fixtureConfig) {
				config.descending = true
			},
			wantPostIDs: [][]int64{{11, 10}, {11, 10}},
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
			if len(mutated.Cases) != len(test.wantPostIDs) {
				t.Fatalf("mutated case count = %d, want %d", len(mutated.Cases), len(test.wantPostIDs))
			}
			for index, want := range test.wantPostIDs {
				if !reflect.DeepEqual(mutated.Cases[index].PostIDs, want) {
					t.Fatalf("mutated case %d IDs = %v, want %v", index, mutated.Cases[index].PostIDs, want)
				}
				if reflect.DeepEqual(mutated.Cases[index].PostIDs, base.Cases[index].PostIDs) {
					t.Fatalf("mutated case %d IDs remained %v; DBState differences cannot satisfy the gate", index, mutated.Cases[index].PostIDs)
				}
			}
		})
	}
}

func TestGeneratedTypedAndDynamicRelationSelectorsBuildEqualPlans(t *testing.T) {
	t.Parallel()

	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	typed := blog.PostObjects.Using(nil).
		Filter(relations.BlogPost.Author.Name.Exact("Ada"), relations.BlogPost.Author.ID.Exact(1)).
		OrderBy(blog.PostFields.ID.Asc()).
		Plan()
	dynamicPredicates, err := relations.BlogPost.ParseDynamic(nil, []orm.LookupInput{
		{Key: "author__name", Value: "Ada"},
		{Key: "author__id", Value: int64(1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	dynamic := blog.PostObjects.Using(nil).
		Filter(dynamicPredicates...).
		OrderBy(blog.PostFields.ID.Asc()).
		Plan()
	if !typed.Equal(dynamic) {
		t.Fatalf("typed plan %#v differs from dynamic plan %#v", typed, dynamic)
	}
}

func TestGeneratedAppsHaveNoAppToAppImportsAndObserverIsOracleBlind(t *testing.T) {
	t.Parallel()

	root := relationQueryProductDirectory(t)
	for _, relative := range []string{
		"authors/zz_godj_generated.go",
		"authors/zz_godj_relation.go",
		"blog/zz_godj_generated.go",
		"blog/zz_godj_relation.go",
	} {
		imports := parsedImports(t, filepath.Join(root, relative))
		for _, imported := range imports {
			if strings.Contains(imported, "/relationqueryproduct/authors") || strings.Contains(imported, "/relationqueryproduct/blog") {
				t.Fatalf("generated app file %s has app-to-app import %q", relative, imported)
			}
		}
	}
	for _, imported := range parsedImports(t, filepath.Join(root, "observer.go")) {
		for _, forbidden := range []string{"/oracles/", "/fixtures/", "relation-oracle", "not-implemented"} {
			if strings.Contains(imported, forbidden) {
				t.Fatalf("REL-004 observer imports expected artifact %q", imported)
			}
		}
	}
}

func TestGeneratedAppsHaveZeroAppToAppImportsAndDependencies(t *testing.T) {
	t.Parallel()

	const rootImport = "github.com/progresshans/godj/conformance/relationqueryproduct/"
	command := exec.Command("go", "list", "-json", rootImport+"authors", rootImport+"blog", rootImport+"project")
	command.Dir = relationQueryProductDirectory(t)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list relation-query project: %v", err)
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
	authors := listed[rootImport+"authors"]
	blogPackage := listed[rootImport+"blog"]
	projectPackage := listed[rootImport+"project"]
	if slices.Contains(authors.Imports, rootImport+"blog") || slices.Contains(authors.Deps, rootImport+"blog") {
		t.Fatalf("authors app reaches blog: imports=%#v deps=%#v", authors.Imports, authors.Deps)
	}
	if slices.Contains(blogPackage.Imports, rootImport+"authors") || slices.Contains(blogPackage.Deps, rootImport+"authors") {
		t.Fatalf("blog app reaches authors: imports=%#v deps=%#v", blogPackage.Imports, blogPackage.Deps)
	}
	for _, app := range []string{rootImport + "authors", rootImport + "blog"} {
		if !slices.Contains(projectPackage.Imports, app) || !slices.Contains(projectPackage.Deps, app) {
			t.Fatalf("project bridge does not own app edge %q: imports=%#v deps=%#v", app, projectPackage.Imports, projectPackage.Deps)
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

func relationQueryProductDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate relation query product test source")
	}
	return filepath.Dir(source)
}
