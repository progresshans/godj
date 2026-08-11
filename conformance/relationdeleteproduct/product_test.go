package relationdeleteproduct

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
	"github.com/progresshans/godj/conformance/relationdeleteproduct/fixture"
	"github.com/progresshans/godj/conformance/relationdeleteproduct/project"
	"github.com/progresshans/godj/query"
)

const relationDeletePolicyDigest = "eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58"

func TestCheckedInGeneratedRelationDeleteProjectMatchesExactThirteenCandidates(t *testing.T) {
	t.Parallel()

	authorsSchema, err := fixture.AuthorsSchema()
	if err != nil {
		t.Fatal(err)
	}
	blogSchema, err := fixture.BlogSchema()
	if err != nil {
		t.Fatal(err)
	}
	const rootImport = "github.com/progresshans/godj/conformance/relationdeleteproduct/"
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
		{path: "project/zz_godj_relation_delete.go", data: generated(t, func() ([]byte, error) {
			return codegen.GenerateProjectRelationDelete("project", objectPackages)
		})},
	}

	root := relationDeleteProductDirectory(t)
	for _, candidate := range candidates {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(candidate.path)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contents, candidate.data) {
			t.Fatalf("checked-in generated file %s differs from deterministic candidate", candidate.path)
		}
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
		t.Fatalf("generated file inventory = %#v, want exact thirteen %#v", generatedFiles, wantFiles)
	}

	deleteCandidate := candidates[len(candidates)-1].data
	if !bytes.Contains(deleteCandidate, []byte(relationDeletePolicyDigest)) {
		t.Fatalf("generated relation-delete aggregate omits exact policy digest %s", relationDeletePolicyDigest)
	}
	reordered, err := codegen.GenerateProjectRelationDelete("project", []codegen.RelationObjectPackage{objectPackages[1], objectPackages[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reordered, deleteCandidate) {
		t.Fatal("relation-delete regeneration changed under project package reordering")
	}
	if project.GoDjProjectRelationDeleteGeneratorVersion != codegen.ProjectRelationDeleteGeneratorVersion {
		t.Fatalf(
			"checked-in generator version = %q, want %q",
			project.GoDjProjectRelationDeleteGeneratorVersion,
			codegen.ProjectRelationDeleteGeneratorVersion,
		)
	}
	if _, err := project.BindRelationDeleters(); err != nil {
		t.Fatalf("BindRelationDeleters() error = %v", err)
	}
}

func TestObserveExecutesExactREL007AndREL008Cases(t *testing.T) {
	t.Parallel()

	got, err := Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	initial := initialDatabaseState()
	if !reflect.DeepEqual(got.Protect.Before, initial) || !reflect.DeepEqual(got.SetNull.Before, initial) {
		t.Fatalf("fresh fixture states = protect %#v set-null %#v, want %#v", got.Protect.Before, got.SetNull.Before, initial)
	}
	if got.Protect.Returned != 0 {
		t.Fatalf("REL-007 returned rows = %d, want 0", got.Protect.Returned)
	}
	var protected *query.ProtectedForeignKeyError
	if !errors.As(got.Protect.Err, &protected) || protected.ProtectedSourceRows() != 2 {
		t.Fatalf("REL-007 error = %v, want typed protected count 2", got.Protect.Err)
	}
	if !errors.Is(got.Protect.Err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}) {
		t.Fatalf("REL-007 error = %v, want integrity_error/protected_foreign_key", got.Protect.Err)
	}
	if !reflect.DeepEqual(got.Protect.CallerBefore, CallerState{ID: 1, Name: "Ada", KeyPresent: true}) ||
		!reflect.DeepEqual(got.Protect.CallerAfter, got.Protect.CallerBefore) {
		t.Fatalf("REL-007 caller before/after = %#v/%#v", got.Protect.CallerBefore, got.Protect.CallerAfter)
	}
	if !reflect.DeepEqual(got.Protect.After, got.Protect.Before) {
		t.Fatalf("REL-007 database changed from %#v to %#v", got.Protect.Before, got.Protect.After)
	}
	wantProtectMetrics := DeleteMetrics{
		TransactionCount:    1,
		QueryCount:          1,
		OperationOrder:      []string{OperationQuery},
		MutationOrder:       []string{},
		MutationRows:        []MutationRow{},
		RelationSetNullRows: []int64{},
		DeleteRows:          []int64{},
	}
	if !reflect.DeepEqual(got.Protect.Metrics, wantProtectMetrics) {
		t.Fatalf("REL-007 metrics = %#v, want %#v", got.Protect.Metrics, wantProtectMetrics)
	}

	if got.SetNull.Returned != 1 || got.SetNull.Err != nil {
		t.Fatalf("REL-008 Delete() = (%d, %v), want (1, nil)", got.SetNull.Returned, got.SetNull.Err)
	}
	if !reflect.DeepEqual(got.SetNull.CallerBefore, CallerState{ID: 2, Name: "Bob", KeyPresent: true}) ||
		!reflect.DeepEqual(got.SetNull.CallerAfter, CallerState{Name: "Bob"}) {
		t.Fatalf("REL-008 caller before/after = %#v/%#v", got.SetNull.CallerBefore, got.SetNull.CallerAfter)
	}
	if !reflect.DeepEqual(got.SetNull.After, setNullDatabaseState()) {
		t.Fatalf("REL-008 final database state = %#v, want %#v", got.SetNull.After, setNullDatabaseState())
	}
	wantSetNullMetrics := DeleteMetrics{
		TransactionCount:     1,
		QueryCount:           1,
		RelationSetNullCount: 1,
		DeleteCount:          1,
		OperationOrder:       []string{OperationQuery, OperationRelationSetNull, OperationDelete},
		MutationOrder:        []string{OperationRelationSetNull, OperationDelete},
		MutationRows: []MutationRow{
			{Kind: OperationRelationSetNull, AffectedRows: 2},
			{Kind: OperationDelete, AffectedRows: 1},
		},
		RelationSetNullRows: []int64{2},
		DeleteRows:          []int64{1},
	}
	if !reflect.DeepEqual(got.SetNull.Metrics, wantSetNullMetrics) {
		t.Fatalf("REL-008 metrics = %#v, want %#v", got.SetNull.Metrics, wantSetNullMetrics)
	}

	wantPhysical := PhysicalSchema{
		ForeignKeysEnabled: 1,
		ForeignKeys: []ForeignKeyShape{
			{From: "author_id", ToTable: "authors_author", ToColumn: "id", OnDelete: "RESTRICT"},
			{From: "reviewer_id", ToTable: "authors_author", ToColumn: "id", OnDelete: "NO ACTION"},
		},
		ReviewerNullable: true,
	}
	if !reflect.DeepEqual(got.Protect.Schema, wantPhysical) || !reflect.DeepEqual(got.SetNull.Schema, wantPhysical) {
		t.Fatalf("physical schemas = protect %#v set-null %#v, want %#v", got.Protect.Schema, got.SetNull.Schema, wantPhysical)
	}
}

func TestREL007REL008PhysicalSchemaFalseGreenMutationsAreRejected(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*fixtureConfig)
	}{
		{name: "schema-level set null", mutate: func(config *fixtureConfig) { config.reviewerDeleteAction = "SET NULL" }},
		{name: "author cascade", mutate: func(config *fixtureConfig) { config.authorDeleteAction = "CASCADE" }},
		{name: "missing author foreign key", mutate: func(config *fixtureConfig) { config.omitAuthorForeignKey = true }},
		{name: "missing reviewer foreign key", mutate: func(config *fixtureConfig) { config.omitReviewerForeignKey = true }},
		{name: "trigger side effect", mutate: func(config *fixtureConfig) { config.addBlogTrigger = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := defaultFixtureConfig()
			test.mutate(&config)
			if _, err := observeDelete(context.Background(), 2, config); err == nil {
				t.Fatal("physical-schema mutation published a successful REL-008 observation")
			}
		})
	}
}

func TestREL008AdversarialDeleteFailureRollsBackAndPreservesCaller(t *testing.T) {
	t.Parallel()

	config := defaultFixtureConfig()
	config.addExternalProtect = true
	got, err := observeDelete(context.Background(), 2, config)
	if err != nil {
		t.Fatalf("collect adversarial REL-008 observation: %v", err)
	}
	if got.Returned != 0 || got.Err == nil {
		t.Fatalf("adversarial REL-008 Delete() = (%d, %v), want (0, error)", got.Returned, got.Err)
	}
	if errors.Is(got.Err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}) {
		t.Fatalf("adversarial external DB constraint was misreported as declared PROTECT: %v", got.Err)
	}
	if !reflect.DeepEqual(got.CallerBefore, CallerState{ID: 2, Name: "Bob", KeyPresent: true}) ||
		!reflect.DeepEqual(got.CallerAfter, got.CallerBefore) {
		t.Fatalf("adversarial REL-008 caller before/after = %#v/%#v", got.CallerBefore, got.CallerAfter)
	}
	if !reflect.DeepEqual(got.Before, initialDatabaseState()) || !reflect.DeepEqual(got.After, got.Before) {
		t.Fatalf("adversarial REL-008 database before/after = %#v/%#v", got.Before, got.After)
	}
	wantMetrics := DeleteMetrics{
		TransactionCount:     1,
		QueryCount:           1,
		RelationSetNullCount: 1,
		DeleteCount:          1,
		OperationOrder:       []string{OperationQuery, OperationRelationSetNull, OperationDelete},
		MutationOrder:        []string{OperationRelationSetNull, OperationDelete},
		MutationRows: []MutationRow{
			{Kind: OperationRelationSetNull, AffectedRows: 2},
			{Kind: OperationDelete, AffectedRows: 0},
		},
		RelationSetNullRows: []int64{2},
		DeleteRows:          []int64{0},
	}
	if !reflect.DeepEqual(got.Metrics, wantMetrics) {
		t.Fatalf("adversarial REL-008 metrics = %#v, want %#v", got.Metrics, wantMetrics)
	}
}

func TestGeneratedAppsHaveNoAppEdgesAndObserverIsOracleBlind(t *testing.T) {
	t.Parallel()

	root := relationDeleteProductDirectory(t)
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
				if strings.Contains(imported, "/relationdeleteproduct/authors") || strings.Contains(imported, "/relationdeleteproduct/blog") {
					t.Fatalf("generated app file %s/%s has app-to-app import %q", directory, entry.Name(), imported)
				}
			}
		}
	}
	observerPath := filepath.Join(root, "observer.go")
	for _, imported := range parsedImports(t, observerPath) {
		for _, forbidden := range []string{"/oracles/", "/static/", "/fixtures/", "relation-oracle", "not-implemented", "notimplemented"} {
			if strings.Contains(imported, forbidden) {
				t.Fatalf("relation-delete observer imports forbidden expected artifact %q", imported)
			}
		}
		if slices.Contains([]string{"embed", "io/fs", "os", "path/filepath"}, imported) {
			t.Fatalf("relation-delete observer imports file-reading package %q", imported)
		}
	}
	observerSource, err := os.ReadFile(observerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/oracles/", "/static/", "/fixtures/", "relation-oracle", "not-implemented", "notimplemented"} {
		if bytes.Contains(observerSource, []byte(forbidden)) {
			t.Fatalf("relation-delete observer source names forbidden expected artifact %q", forbidden)
		}
	}

	const rootImport = "github.com/progresshans/godj/conformance/relationdeleteproduct/"
	command := exec.Command("go", "list", "-json", rootImport+"authors", rootImport+"blog", rootImport+"project")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list relation-delete project: %v", err)
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

func initialDatabaseState() DatabaseState {
	reviewer := int64(2)
	return DatabaseState{
		Authors: []AuthorRow{{ID: 1, Name: "Ada"}, {ID: 2, Name: "Bob"}, {ID: 3, Name: "Cleo"}},
		Posts: []PostRow{
			{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewer},
			{ID: 11, Title: "Beta", AuthorID: 1},
			{ID: 12, Title: "Gamma", AuthorID: 3, ReviewerID: &reviewer},
		},
	}
}

func setNullDatabaseState() DatabaseState {
	return DatabaseState{
		Authors: []AuthorRow{{ID: 1, Name: "Ada"}, {ID: 3, Name: "Cleo"}},
		Posts: []PostRow{
			{ID: 10, Title: "Alpha", AuthorID: 1},
			{ID: 11, Title: "Beta", AuthorID: 1},
			{ID: 12, Title: "Gamma", AuthorID: 3},
		},
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

func relationDeleteProductDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate relation-delete product test source")
	}
	return filepath.Dir(source)
}
