package relationselectproduct

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/progresshans/godj/conformance/relationselectproduct/blog"
	"github.com/progresshans/godj/conformance/relationselectproduct/fixture"
	"github.com/progresshans/godj/conformance/relationselectproduct/project"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

func TestCheckedInGeneratedSelectRelatedProjectMatchesTwelveDeterministicCandidates(t *testing.T) {
	t.Parallel()

	authorsSchema, err := fixture.AuthorsSchema()
	if err != nil {
		t.Fatal(err)
	}
	blogSchema, err := fixture.BlogSchema()
	if err != nil {
		t.Fatal(err)
	}
	const rootImport = "github.com/progresshans/godj/conformance/relationselectproduct/"
	objectPackages := []codegen.RelationObjectPackage{
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
		{path: "authors/zz_godj_relation_projection.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationProjection("authors", authorsSchema) })},
		{path: "blog/zz_godj_generated.go", data: generated(t, func() ([]byte, error) { return codegen.Generate("blog", blogSchema) })},
		{path: "blog/zz_godj_relation.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationMetadata("blog", blogSchema) })},
		{path: "blog/zz_godj_relation_query.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationQuery("blog", blogSchema) })},
		{path: "blog/zz_godj_relation_object.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationObject("blog", blogSchema) })},
		{path: "blog/zz_godj_relation_projection.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationProjection("blog", blogSchema) })},
		{path: "project/zz_godj_bindings.go", data: generated(t, func() ([]byte, error) {
			return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
				{Alias: "authors", ImportPath: rootImport + "authors"},
				{Alias: "blog", ImportPath: rootImport + "blog"},
			})
		})},
		{path: "project/zz_godj_relation_object.go", data: generated(t, func() ([]byte, error) {
			return codegen.GenerateProjectRelationObject("project", objectPackages)
		})},
		{path: "project/zz_godj_relation_select_related.go", data: generated(t, func() ([]byte, error) {
			return codegen.GenerateProjectRelationSelectRelated("project", objectPackages)
		})},
	}

	root := relationSelectProductDirectory(t)
	for _, candidate := range candidates {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(candidate.path)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contents, candidate.data) {
			t.Fatalf("checked-in generated file %s differs from deterministic candidate", candidate.path)
		}
	}
	selectRelatedSource, err := os.ReadFile(filepath.Join(root, "project", "zz_godj_relation_select_related.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(selectRelatedSource, []byte(`const GoDjProjectRelationSelectRelatedGeneratorVersion = "godj-codegen-rel-select-related-project-v2"`)) {
		t.Fatal("checked-in select-related companion does not expose the v2 provenance lock")
	}
	if count := bytes.Count(selectRelatedSource, []byte("configurationErr error")); count != 2 {
		t.Fatalf("typed select-related private configuration error fields = %d, want exact 2", count)
	}
	if bytes.Contains(selectRelatedSource, []byte("godj-codegen-rel-select-related-project-v1")) {
		t.Fatal("checked-in select-related companion retains stale v1 provenance")
	}

	var generatedFiles []string
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
	for index, candidate := range candidates {
		wantFiles[index] = candidate.path
	}
	slices.Sort(wantFiles)
	if !reflect.DeepEqual(generatedFiles, wantFiles) {
		t.Fatalf("generated file inventory = %#v, want exact twelve %#v", generatedFiles, wantFiles)
	}
}

func TestObserveExecutesExactREL009REL010AndREL011Cases(t *testing.T) {
	t.Parallel()

	got, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantRequired := []PostRelatedRow{
		{PostID: 10, Name: stringPointer("Ada")},
		{PostID: 11, Name: stringPointer("Ada")},
		{PostID: 12, Name: stringPointer("Cleo")},
	}
	wantNullable := []PostRelatedRow{
		{PostID: 10, Name: stringPointer("Bob")},
		{PostID: 11},
		{PostID: 12, Name: stringPointer("Bob")},
	}
	wantPlainMetrics := QueryMetrics{
		QueryCount: 4, StatementKinds: []string{"SELECT", "SELECT", "SELECT", "SELECT"},
		JoinKinds: []string{}, AccessExtraQueries: 3,
	}
	wantRequiredMetrics := QueryMetrics{
		QueryCount: 1, StatementKinds: []string{"SELECT"}, JoinKinds: []string{"INNER"}, InnerJoinCount: 1,
	}
	wantNullableMetrics := QueryMetrics{
		QueryCount: 1, StatementKinds: []string{"SELECT"}, JoinKinds: []string{"LEFT_OUTER"}, LeftOuterJoinCount: 1,
	}
	wantInvalidMetrics := QueryMetrics{StatementKinds: []string{}, JoinKinds: []string{}}
	if !equalPostRelatedRows(got.Required.Plain, wantRequired) || !equalPostRelatedRows(got.Required.Eager, wantRequired) ||
		!equalPostRelatedRows(got.Nullable.Rows, wantNullable) {
		t.Fatalf("relation select results = required %#v nullable %#v", got.Required, got.Nullable.Rows)
	}
	if !reflect.DeepEqual(got.Required.PlainMetrics, wantPlainMetrics) ||
		!reflect.DeepEqual(got.Required.EagerMetrics, wantRequiredMetrics) ||
		!reflect.DeepEqual(got.Nullable.Metrics, wantNullableMetrics) ||
		!reflect.DeepEqual(got.Invalid.Metrics, wantInvalidMetrics) {
		t.Fatalf("relation select metrics = required %#v nullable %#v invalid %#v", got.Required, got.Nullable.Metrics, got.Invalid.Metrics)
	}
	var queryError *query.Error
	if !errors.As(got.Invalid.Err, &queryError) || queryError.Category != query.CategoryField ||
		queryError.Code != query.CodeInvalidRelatedPath || queryError.Field != "posts" {
		t.Fatalf("REL-011 error = %v, want field_error/invalid_related_path posts", got.Invalid.Err)
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

func TestREL009REL010REL011FalseGreenMutations(t *testing.T) {
	t.Parallel()

	base, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*fixtureConfig)
		check  func(*testing.T, Observation)
	}{
		{
			name: "required target membership",
			mutate: func(config *fixtureConfig) {
				config.posts[0].authorID = 3
			},
			check: func(t *testing.T, got Observation) {
				if got.Required.Eager[0].Name == nil || *got.Required.Eager[0].Name != "Cleo" {
					t.Fatalf("required membership mutation = %#v", got.Required.Eager)
				}
			},
		},
		{
			name: "nullable absence",
			mutate: func(config *fixtureConfig) {
				reviewer := int64(2)
				config.posts[1].reviewerID = &reviewer
			},
			check: func(t *testing.T, got Observation) {
				if got.Nullable.Rows[1].Name == nil || *got.Nullable.Rows[1].Name != "Bob" {
					t.Fatalf("nullable mutation = %#v", got.Nullable.Rows)
				}
			},
		},
		{
			name: "root ordering",
			mutate: func(config *fixtureConfig) {
				config.postsDescending = true
			},
			check: func(t *testing.T, got Observation) {
				if got.Required.Eager[0].PostID != 12 || got.Nullable.Rows[2].PostID != 10 {
					t.Fatalf("descending mutation = required %#v nullable %#v", got.Required.Eager, got.Nullable.Rows)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			got, err := observe(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, got)
			if reflect.DeepEqual(got, base) {
				t.Fatal("owned mutation left the complete observation unchanged")
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*fixtureConfig)
	}{
		{name: "second eager query", mutate: func(config *fixtureConfig) { config.repeatRequiredEager = true }},
		{name: "required cold access", mutate: func(config *fixtureConfig) { config.forceRequiredCold = true }},
		{name: "nullable cold access", mutate: func(config *fixtureConfig) { config.forceNullableCold = true }},
		{name: "invalid path query", mutate: func(config *fixtureConfig) { config.allowInvalidPathQuery = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			if _, err := observe(context.Background(), config); err == nil {
				t.Fatal("trace mutation published a successful select-related observation")
			}
		})
	}
}

func TestTypedAndDynamicSelectRelatedConvergeOnTheSamePlan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "godj-select-related-convergence-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	if err := provision(ctx, backend, defaultFixtureConfig()); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingQueryer{backend: backend}
	source := blog.PostObjects.Using(recorder).OrderBy(blog.PostFields.ID.Asc())
	selected := objects.BlogPost.SelectRelated(source)
	typed := selected.Author()
	dynamic, err := selected.ParseDynamic("author")
	if err != nil {
		t.Fatal(err)
	}
	if recorder.mark() != 0 {
		t.Fatalf("typed/dynamic construction issued %d queries", recorder.mark())
	}
	if _, err := typed.All(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := dynamic.All(ctx); err != nil {
		t.Fatal(err)
	}
	records := recorder.recordsSince(0)
	if len(records) != 2 || !records[0].plan.Equal(records[1].plan) {
		t.Fatalf("typed/dynamic execution plans = %#v, want two equal plans", records)
	}
	beforeInvalid := recorder.mark()
	if _, err := selected.ParseDynamic("posts"); !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidRelatedPath}) {
		t.Fatalf("reverse ParseDynamic error = %v", err)
	}
	if recorder.mark() != beforeInvalid {
		t.Fatalf("reverse ParseDynamic issued %d queries", recorder.mark()-beforeInvalid)
	}
}

func TestGeneratedAppsHaveNoAppEdgesAndObserverIsOracleBlind(t *testing.T) {
	t.Parallel()

	root := relationSelectProductDirectory(t)
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
				if strings.Contains(imported, "/relationselectproduct/authors") || strings.Contains(imported, "/relationselectproduct/blog") {
					t.Fatalf("generated app file %s/%s has app-to-app import %q", directory, entry.Name(), imported)
				}
			}
		}
	}
	for _, imported := range parsedImports(t, filepath.Join(root, "observer.go")) {
		for _, forbidden := range []string{"/oracles/", "/fixtures/", "relation-oracle", "not-implemented"} {
			if strings.Contains(imported, forbidden) {
				t.Fatalf("select-related observer imports expected artifact %q", imported)
			}
		}
	}

	const rootImport = "github.com/progresshans/godj/conformance/relationselectproduct/"
	command := exec.Command("go", "list", "-json", rootImport+"authors", rootImport+"blog", rootImport+"project")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list select-related project: %v", err)
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

func relationSelectProductDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate select-related product test source")
	}
	return filepath.Dir(source)
}

func stringPointer(value string) *string {
	return &value
}
