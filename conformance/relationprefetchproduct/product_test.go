package relationprefetchproduct

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
	"github.com/progresshans/godj/conformance/relationprefetchproduct/fixture"
)

func TestCheckedInGeneratedReversePrefetchProjectMatchesNineDeterministicCandidates(t *testing.T) {
	t.Parallel()

	authorsSchema, err := fixture.AuthorsSchema()
	if err != nil {
		t.Fatal(err)
	}
	blogSchema, err := fixture.BlogSchema()
	if err != nil {
		t.Fatal(err)
	}
	const rootImport = "github.com/progresshans/godj/conformance/relationprefetchproduct/"
	reversePackages := []codegen.RelationReversePackage{
		{Alias: "authors", ImportPath: rootImport + "authors", Schema: authorsSchema},
		{Alias: "blog", ImportPath: rootImport + "blog", Schema: blogSchema},
	}
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
			return codegen.GenerateProjectRelationReverse("project", reversePackages)
		})},
		{path: "project/zz_godj_relation_prefetch.go", data: generated(t, func() ([]byte, error) {
			return codegen.GenerateProjectRelationPrefetch("project", reversePackages)
		})},
	}

	root := relationPrefetchProductDirectory(t)
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
		t.Fatalf("generated file inventory = %#v, want exact nine %#v", generatedFiles, wantFiles)
	}
}

func TestObserveExecutesExactREL012PrefetchAndDatabaseState(t *testing.T) {
	t.Parallel()

	got, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantAuthors := []AuthorPosts{
		{AuthorID: 1, PostIDs: []int64{10, 11}},
		{AuthorID: 2, PostIDs: []int64{}},
		{AuthorID: 3, PostIDs: []int64{12}},
	}
	wantMetrics := QueryMetrics{
		QueryCount:                2,
		StatementKinds:            []string{"SELECT", "SELECT"},
		JoinKinds:                 []string{},
		PrimaryQueryCount:         1,
		BatchQueryCount:           1,
		BatchPredicateColumn:      "author_id",
		BatchKeyCount:             3,
		RelatedAccessExtraQueries: 0,
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
	if !reflect.DeepEqual(got.Authors, wantAuthors) ||
		!reflect.DeepEqual(got.Metrics, wantMetrics) ||
		!reflect.DeepEqual(got.DBState, wantState) {
		t.Fatalf("REL-012 observation = %#v, want authors=%#v metrics=%#v state=%#v", got, wantAuthors, wantMetrics, wantState)
	}
}

func TestREL012ObservationAndInternalGatesRejectFalseGreens(t *testing.T) {
	t.Parallel()

	base, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Run("membership and state mutation changes payload", func(t *testing.T) {
		config := defaultFixtureConfig()
		config.posts[1].authorID = 3
		mutated, err := observe(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(mutated, base) || !reflect.DeepEqual(mutated.Authors[0].PostIDs, []int64{10}) ||
			!reflect.DeepEqual(mutated.Authors[2].PostIDs, []int64{11, 12}) {
			t.Fatalf("membership mutation did not change REL-012 payload: %#v", mutated)
		}
	})
	t.Run("owner ordering mutation changes payload", func(t *testing.T) {
		config := defaultFixtureConfig()
		config.ownersDescending = true
		mutated, err := observe(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(mutated, base) || mutated.Authors[0].AuthorID != 3 || mutated.Authors[2].AuthorID != 1 {
			t.Fatalf("owner-order mutation did not change REL-012 payload: %#v", mutated.Authors)
		}
	})
	t.Run("database state mutation changes payload", func(t *testing.T) {
		config := defaultFixtureConfig()
		config.authors[0].name = "Mutation Ada"
		mutated, err := observe(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(mutated, base) || mutated.DBState.Authors[0].Name != "Mutation Ada" ||
			!reflect.DeepEqual(mutated.Authors, base.Authors) || !reflect.DeepEqual(mutated.Metrics, base.Metrics) {
			t.Fatalf("state-only mutation = %#v", mutated)
		}
	})
	for _, test := range []struct {
		name   string
		mutate func(*fixtureConfig)
	}{
		{name: "extra primary query", mutate: func(config *fixtureConfig) { config.repeatPrimary = true }},
		{name: "cold related access", mutate: func(config *fixtureConfig) { config.forceColdAccess = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			if _, err := observe(context.Background(), config); err == nil {
				t.Fatal("trace mutation published a successful REL-012 observation")
			}
		})
	}
}

func TestGeneratedAppsHaveNoAppToAppEdgesAndObserverIsOracleBlind(t *testing.T) {
	t.Parallel()

	root := relationPrefetchProductDirectory(t)
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
				if strings.Contains(imported, "/relationprefetchproduct/authors") || strings.Contains(imported, "/relationprefetchproduct/blog") {
					t.Fatalf("generated app file %s/%s has app-to-app import %q", directory, entry.Name(), imported)
				}
			}
		}
	}
	for _, imported := range parsedImports(t, filepath.Join(root, "observer.go")) {
		for _, forbidden := range []string{"/oracles/", "/fixtures/", "relation-oracle", "not-implemented"} {
			if strings.Contains(imported, forbidden) {
				t.Fatalf("REL-012 observer imports expected artifact %q", imported)
			}
		}
	}

	const rootImport = "github.com/progresshans/godj/conformance/relationprefetchproduct/"
	command := exec.Command("go", "list", "-json", rootImport+"authors", rootImport+"blog", rootImport+"project")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list reverse-prefetch project: %v", err)
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

func relationPrefetchProductDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate reverse-prefetch product test source")
	}
	return filepath.Dir(source)
}
