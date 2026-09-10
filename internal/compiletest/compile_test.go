//go:build !race

package compiletest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	fixture "github.com/progresshans/godj/conformance/relationfixture"
	"github.com/progresshans/godj/internal/projectgenerate"
)

const modulePath = "github.com/progresshans/godj"

func TestExternalConsumerCompiles(t *testing.T) {
	for _, fixture := range []string{
		"external_consumer.go.txt",
		"write_external_consumer.go.txt",
		"save_external_consumer.go.txt",
		"migration_external_consumer.go.txt",
		"migration_relation_external_consumer.go.txt",
		"migration_definition_external_consumer.go.txt",
		"project_external_consumer.go.txt",
		"relation_project/external_consumer.go.txt",
		"relation_query/external_consumer.go.txt",
		"relation_object/external_consumer.go.txt",
		"relation_reverse/external_consumer.go.txt",
		"relation_prefetch/external_consumer.go.txt",
		"relation_select_related/external_consumer.go.txt",
		"relation_delete/backend_external_consumer.go.txt",
		"relation_delete/generated_external_consumer.go.txt",
	} {
		result := compileFixture(t, fixture)
		if result.err != nil {
			t.Fatalf("external consumer %s did not compile: %v\n%s", fixture, result.err, result.output)
		}
	}

	verifyRelationFacadeProduction(t)
}

// This checks the exposed boundary, not the complete set of supported methods.
func TestRelationFacadeDoesNotExposeInternalObjects(t *testing.T) {
	for _, name := range []string{
		"codegen/testdata/relation_facade/project.golden",
		"conformance/relationfixture/project/zz_godj_relation_facade.go",
	} {
		source, err := os.ReadFile(filepath.Join(repositoryRoot(t), filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRelationFacadeProductSource(source); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func verifyRelationFacadeProduction(t *testing.T) {
	t.Helper()
	root := repositoryRoot(t)
	consumerSource, err := os.ReadFile(filepath.Join(root, "internal", "compiletest", "testdata", "relation_facade", "external_consumer.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	fixtureRoot := filepath.Join(root, "conformance", "relationfixture")
	physicalBefore := readRelationFacadeInventory(t, fixtureRoot)
	spec, err := fixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := projectgenerate.Check(t.Context(), fixtureRoot, bundle); err != nil || !report.Clean() {
		t.Fatalf("current relation facade generated drift = %#v, %v", report, err)
	}
	directory := t.TempDir()
	writeCompileModule(t, directory, "example.com/godj-relation-facade-consumer")
	consumerPath := filepath.Join(directory, "consumer.go")
	if err := os.WriteFile(consumerPath, consumerSource, 0o644); err != nil {
		t.Fatalf("write relation facade consumer: %v", err)
	}

	productOutput := filepath.Join(directory, "relationdeleteproduct.test")
	productCompile := compileRelationFacadeProduct(t, root, productOutput)
	if productCompile.err != nil {
		t.Fatalf("relation facade physical current bundle product did not compile: %v\n%s", productCompile.err, productCompile.output)
	}
	productInfo, err := os.Lstat(productOutput)
	if err != nil {
		t.Fatalf("lstat relation facade compile-only product: %v", err)
	}
	if !productInfo.Mode().IsRegular() {
		t.Fatalf("relation facade compile-only product mode = %s, want regular file", productInfo.Mode())
	}

	positive := compileRelationFacadeConsumer(t, directory, "production.test")
	if positive.err != nil {
		t.Fatalf("production relation facade consumer did not compile without overlay: %v\n%s", positive.err, positive.output)
	}

	queryerMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("func compileRelationFacade(ctx context.Context, backend project.Backend) error"),
		[]byte("func compileRelationFacade(ctx context.Context, backend db.Queryer) error"),
	))
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		queryerMutation,
		"queryer-negative.test",
		[]string{"db.Queryer", "project.Backend"},
	)

	predicateMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte(`blog.PostFields.Title.IContains("lazy")`),
		[]byte(`authors.AuthorFields.Name.IContains("lazy")`),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		predicateMutation,
		"predicate-negative.test",
		[]string{"orm.Predicate[authors.Author]", "orm.Predicate[blog.Post]"},
	)

	orderingMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("ordered := filtered.OrderBy(blog.PostFields.ID.Asc())"),
		[]byte("ordered := filtered.OrderBy(authors.AuthorFields.ID.Asc())"),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		orderingMutation,
		"ordering-negative.test",
		[]string{"orm.Ordering[authors.Author]", "orm.Ordering[blog.Post]"},
	)

	selectorMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("SelectRelated(models.BlogPost.Related.Author)"),
		[]byte("SelectRelated(blog.PostFields.ID)"),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		selectorMutation,
		"selector-negative.test",
		[]string{"project.BlogPostRelationSelector", "orm.IntegerField[blog.Post]"},
	)

	clearAuthorMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("rawPost, err := post.Unwrap()"),
		[]byte("rawPost, err := post.ClearAuthor()"),
	))
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		clearAuthorMutation,
		"clear-author-negative.test",
		[]string{"post.ClearAuthor undefined", "*project.BlogPost"},
	)

	deleteMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("rawPost, err := post.Unwrap()"),
		[]byte("rawPost, err := post.Delete(ctx)"),
	))
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		deleteMutation,
		"delete-negative.test",
		[]string{"post.Delete undefined", "*project.BlogPost"},
	)

	reverseMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("_, err = requiredTarget.Unwrap()"),
		[]byte("_, err = requiredTarget.Posts()"),
	))
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		reverseMutation,
		"reverse-negative.test",
		[]string{"requiredTarget.Posts undefined", "*project.AuthorsAuthor"},
	)

	objectMutation := formatRelationFacadeMutation(t, replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("var source *project.BlogPost = post"),
		[]byte("var source *project.BlogPostObject = post"),
	))
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		objectMutation,
		"object-negative.test",
		[]string{"*project.BlogPost", "*project.BlogPostObject"},
	)

	newValueMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte(`models.BlogPost.New(blog.Post{Title: "new post"})`),
		[]byte(`models.BlogPost.New(authors.Author{Name: "new post"})`),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		newValueMutation,
		"new-value-negative.test",
		[]string{"authors.Author", "blog.Post"},
	)

	targetMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("newPost, err = newPost.WithAuthor(newAuthor)"),
		[]byte("newPost, err = newPost.WithAuthor(newPost)"),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		targetMutation,
		"relation-target-negative.test",
		[]string{"*project.BlogPost", "*project.AuthorsAuthor"},
	)

	identifierMutation := replaceRelationFacadeToken(
		t,
		consumerSource,
		[]byte("newPost, err = newPost.WithReviewerID(2)"),
		[]byte(`newPost, err = newPost.WithReviewerID("2")`),
	)
	verifyRelationFacadeCompileNegative(
		t,
		directory,
		consumerPath,
		identifierMutation,
		"relation-id-negative.test",
		[]string{"untyped string", "int64"},
	)

	forgedSelector := append(bytes.Clone(consumerSource), []byte(`

type forgedSelector struct{}
func (forgedSelector) godjBlogPostRelationSelector() {}
var _ project.BlogPostRelationSelector = forgedSelector{}
`)...)
	verifyRelationFacadeCompileNegative(t, directory, consumerPath, forgedSelector,
		"forged-selector-negative.test", []string{"forgedSelector", "unexported method"})
	physicalAfter := readRelationFacadeInventory(t, fixtureRoot)
	if !equalRelationFacadeFiles(physicalBefore.files, physicalAfter.files) {
		t.Fatal("external compilation modified the product fixture")
	}
}

func compileRelationFacadeConsumer(t *testing.T, directory, outputName string) compileResult {
	t.Helper()

	arguments := []string{"test", "-c", "-mod=mod", "-o", filepath.Join(directory, outputName), "."}
	command := exec.CommandContext(t.Context(), "go", arguments...)
	command.Dir = directory
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}

func compileRelationFacadeProduct(t *testing.T, root, outputPath string) compileResult {
	t.Helper()

	command := exec.CommandContext(
		t.Context(),
		"go",
		"test",
		"-c",
		"-mod=readonly",
		"-o",
		outputPath,
		modulePath+"/conformance/relationdeleteproduct",
	)
	command.Dir = root
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}

func verifyRelationFacadeCompileNegative(
	t *testing.T,
	directory string,
	consumerPath string,
	source []byte,
	outputName string,
	wantFragments []string,
) {
	t.Helper()

	if err := os.WriteFile(consumerPath, source, 0o644); err != nil {
		t.Fatalf("write relation facade %s source: %v", outputName, err)
	}
	result := compileRelationFacadeConsumer(t, directory, outputName)
	if result.err == nil {
		t.Fatalf("relation facade %s unexpectedly compiled", outputName)
	}
	for _, fragment := range wantFragments {
		if !strings.Contains(result.output, fragment) {
			t.Fatalf("relation facade %s diagnostics do not contain %q:\n%s", outputName, fragment, result.output)
		}
	}
}

type relationFacadeInventory struct {
	files map[string][]byte
	names []string
}

// Discover the current fixture instead of freezing its filenames and total size.
// Publication controls do not belong to the generated product being copied.
func readRelationFacadeInventory(t *testing.T, root string) relationFacadeInventory {
	t.Helper()
	inventory := relationFacadeInventory{files: make(map[string][]byte)}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("product fixture contains symlink %s", path)
		}
		if relative == ".godj/transactions" && entry.IsDir() {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("product fixture contains non-regular file %s", path)
		}
		if relative == ".godj/generate.lock" || relative == ".godj/publication-journal.json" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		inventory.files[relative] = content
		inventory.names = append(inventory.names, relative)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(inventory.names)
	return inventory
}

func equalRelationFacadeFiles(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for name, content := range left {
		if !bytes.Equal(content, right[name]) {
			return false
		}
	}
	return true
}

func validateRelationFacadeProductSource(source []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), "facade.go", source, 0)
	if err != nil {
		return err
	}
	var surfaces []ast.Node
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if !ast.IsExported(declaration.Name.Name) {
				continue
			}
			if declaration.Recv != nil {
				receiver := declaration.Recv.List[0].Type
				if pointer, ok := receiver.(*ast.StarExpr); ok {
					receiver = pointer.X
				}
				name, ok := receiver.(*ast.Ident)
				if !ok || !ast.IsExported(name.Name) {
					continue
				}
			}
			surfaces = append(surfaces, declaration.Type)
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				if value, ok := specification.(*ast.ValueSpec); ok {
					for _, name := range value.Names {
						if name.IsExported() {
							if value.Type != nil {
								surfaces = append(surfaces, value.Type)
							}
							for _, expression := range value.Values {
								surfaces = append(surfaces, expression)
							}
						}
					}
					continue
				}
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok || !typeSpec.Name.IsExported() {
					continue
				}
				if structure, ok := typeSpec.Type.(*ast.StructType); ok {
					for _, field := range structure.Fields.List {
						if len(field.Names) == 0 {
							surfaces = append(surfaces, field.Type)
						}
						for _, name := range field.Names {
							if name.IsExported() {
								surfaces = append(surfaces, field.Type)
							}
						}
					}
				} else {
					surfaces = append(surfaces, typeSpec.Type)
				}
			}
		}
	}
	for _, surface := range surfaces {
		var forbidden string
		ast.Inspect(surface, func(node ast.Node) bool {
			if name, ok := node.(*ast.Ident); ok && (strings.HasSuffix(name.Name, "Object") || strings.HasSuffix(name.Name, "Objects")) {
				forbidden = name.Name
			}
			return forbidden == ""
		})
		if forbidden != "" {
			return fmt.Errorf("facade exports low-level %s", forbidden)
		}
	}
	return nil
}

func replaceRelationFacadeToken(t *testing.T, source, oldToken, newToken []byte) []byte {
	t.Helper()

	if count := bytes.Count(source, oldToken); count != 1 {
		t.Fatalf("relation facade mutation token %q count = %d, want exact 1", oldToken, count)
	}
	return bytes.Replace(source, oldToken, newToken, 1)
}

func formatRelationFacadeMutation(t *testing.T, source []byte) []byte {
	t.Helper()

	formatted, err := format.Source(source)
	if err != nil {
		t.Fatalf("format relation facade adversarial mutation: %v", err)
	}
	return formatted
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
			name:    "predicate composition model mismatch",
			fixture: "predicate_composition_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.Or",
				"orm.Predicate[Other]",
				"orm.Predicate[models.Article]",
			},
		},
		{
			name:    "predicate connector arity",
			fixture: "predicate_connector_arity.go.txt",
			wantFragments: []string{
				"not enough arguments in call to orm.And",
				"not enough arguments in call to orm.Or",
			},
		},
		{
			name:    "field reference model mismatch",
			fixture: "field_reference_model_mismatch.go.txt",
			wantFragments: []string{
				"orm.F(otherTitle)",
				"orm.FieldReference[models.Article, string]",
			},
		},
		{
			name:    "field reference kind mismatch",
			fixture: "field_reference_kind_mismatch.go.txt",
			wantFragments: []string{
				"orm.F(models.ArticleFields.ID)",
				"orm.FieldReference[models.Article, string]",
			},
		},
		{
			name:    "Boolean field reference is unsupported",
			fixture: "field_reference_boolean_unsupported.go.txt",
			wantFragments: []string{
				"models.ArticleFields.Published",
				"does not match orm.ReferenceField",
				"cannot infer M and V",
			},
		},
		{
			name:    "relation field reference is unsupported",
			fixture: "field_reference_relation_unsupported.go.txt",
			wantFragments: []string{
				"orm.RelatedStringField[Article]",
				"does not match orm.ReferenceField",
				"cannot infer M and V",
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
		{
			name:    "relation object predicate keeps source model",
			fixture: "relation_object/predicate_source_mismatch.go.txt",
			wantFragments: []string{
				"objects.BlogPost.Reviewer.IsNull",
				"orm.Predicate[authors.Author]",
			},
		},
		{
			name:    "relation object factory requires source model",
			fixture: "relation_object/factory_source_mismatch.go.txt",
			wantFragments: []string{
				"cannot use author",
				"as blog.Post value",
			},
		},
		{
			name:    "relation object isnull requires bool",
			fixture: "relation_object/isnull_value_mismatch.go.txt",
			wantFragments: []string{
				"cannot use \"true\"",
				"as bool value",
			},
		},
		{
			name:    "reverse relation predicate keeps owner model",
			fixture: "relation_reverse/predicate_owner_mismatch.go.txt",
			wantFragments: []string{
				"relations.AuthorsAuthor.Posts.Title.Exact",
				"orm.Predicate[blog.Post]",
			},
		},
		{
			name:    "select-related source QuerySet keeps source model",
			fixture: "relation_select_related/source_queryset_mismatch.go.txt",
			wantFragments: []string{
				"cannot use authors.AuthorObjects.Using(backend)",
				"orm.QuerySet[blog.Post]",
			},
		},
		{
			name:    "select-related remains singular",
			fixture: "relation_select_related/multiple_selection.go.txt",
			wantFragments: []string{
				"Author().Reviewer undefined",
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
		{from: modulePath + "/examples/article/modeldef", to: modulePath + "/examples/article/models"},
		{from: modulePath + "/examples/article/modeldef", to: modulePath + "/examples/article/project"},
		{from: modulePath + "/examples/article/cmd/projectrunner", to: modulePath + "/examples/article/models"},
		{from: modulePath + "/examples/article/cmd/projectrunner", to: modulePath + "/examples/article/project"},
		{from: modulePath + "/conformance/relationfixture", to: modulePath + "/conformance/relationfixture/authors"},
		{from: modulePath + "/conformance/relationfixture", to: modulePath + "/conformance/relationfixture/blog"},
		{from: modulePath + "/conformance/relationfixture", to: modulePath + "/conformance/relationfixture/project"},
		{from: modulePath + "/conformance/relationfixture/cmd/projectrunner", to: modulePath + "/conformance/relationfixture/authors"},
		{from: modulePath + "/conformance/relationfixture/cmd/projectrunner", to: modulePath + "/conformance/relationfixture/blog"},
		{from: modulePath + "/conformance/relationfixture/cmd/projectrunner", to: modulePath + "/conformance/relationfixture/project"},
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
	command := exec.CommandContext(t.Context(), "go", arguments...)
	command.Dir = root
	command.Env = commandEnvironment()
	var standardError bytes.Buffer
	command.Stderr = &standardError
	output, err := command.Output()
	if err != nil {
		t.Fatalf("load direct package imports: %v\n%s", err, standardError.Bytes())
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
			t.Fatalf("decode go list output: %v\nstderr:\n%s", err, standardError.Bytes())
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

func TestProjectToolsDoNotDependOnRuntimeBackendsOrOracle(t *testing.T) {
	patterns := []string{
		modulePath + "/project",
		modulePath + "/internal/projectcheck/...",
		modulePath + "/internal/projectgenerate/...",
		modulePath + "/internal/projectmigration/...",
		modulePath + "/internal/projectspec",
	}
	command := exec.CommandContext(t.Context(), "go", append([]string{"list", "-json"}, patterns...)...)
	command.Dir = repositoryRoot(t)
	command.Env = commandEnvironment()
	var standardError bytes.Buffer
	command.Stderr = &standardError
	output, err := command.Output()
	if err != nil {
		t.Fatalf("load project tool dependencies: %v\n%s", err, standardError.Bytes())
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed struct {
			ImportPath string
			Deps       []string
		}
		if err := decoder.Decode(&listed); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		for _, imported := range listed.Deps {
			if strings.HasPrefix(imported, modulePath+"/conformance/") ||
				imported == modulePath+"/db/sqlite" || imported == modulePath+"/db/postgres" {
				t.Errorf("project tool %s depends on runtime backend or oracle package %s", listed.ImportPath, imported)
			}
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
	writeCompileModule(t, directory, "example.com/godj-compile-gate")
	if err := os.WriteFile(filepath.Join(directory, "consumer.go"), source, 0o644); err != nil {
		t.Fatalf("write fixture source: %v", err)
	}

	command := exec.CommandContext(t.Context(), "go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = commandEnvironment()
	output, err := command.CombinedOutput()
	return compileResult{output: string(output), err: err}
}

type dependencyEdge struct {
	from string
	to   string
}

// commandEnvironment keeps every child compiler offline after the execution
// owner has downloaded the locked module graph once.
func commandEnvironment() []string {
	return compileEnvironment(os.Environ())
}

func compileEnvironment(ambient []string) []string {
	fixed := []string{"GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GONOPROXY=none"}
	blocked := make(map[string]bool, len(fixed))
	for _, entry := range fixed {
		key, _, _ := strings.Cut(entry, "=")
		blocked[key] = true
	}
	environment := make([]string, 0, len(ambient)+len(fixed))
	for _, entry := range ambient {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[key] {
			environment = append(environment, entry)
		}
	}
	return append(environment, fixed...)
}

func writeCompileModule(t *testing.T, directory, name string) {
	t.Helper()
	root := repositoryRoot(t)
	goMod := fmt.Sprintf("module %s\n\ngo 1.26.0\n\nrequire %s v0.0.0\n\nreplace %s => %s\n", name, modulePath, modulePath, filepath.ToSlash(root))
	checksums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{"go.mod": []byte(goMod), "go.sum": checksums} {
		if err := os.WriteFile(filepath.Join(directory, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
