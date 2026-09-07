package codegen_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testfixture"
	"github.com/progresshans/godj/codegen/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedProjectRelationFacadeBroadUniversesCompile(t *testing.T) {
	authors, blog := testschema.QueryRelation()

	t.Run("unrelated multi-app multi-model", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-facade-broad"
		multiAuthors := authors.Clone()
		multiAuthors.Models = append(multiAuthors.Models, ir.Model{
			Name: "profile", GoName: "Profile", DBTable: "authors_profile",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "label", GoName: "Label", Column: "label", Kind: ir.FieldChar, MaxLength: 80},
			},
		})
		tags := ir.Schema{
			FormatVersion: ir.CurrentFormatVersion,
			AppLabel:      "tags",
			Models: []ir.Model{{
				Name: "tag", GoName: "Tag", DBTable: "tags_tag",
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 80},
				},
			}},
		}
		packages := []codegen.RelationObjectPackage{
			{Alias: "tags", ImportPath: modulePath + "/tags", Schema: tags},
			{Alias: "blog", ImportPath: modulePath + "/blog", Schema: blog},
			{Alias: "authors", ImportPath: modulePath + "/authors", Schema: multiAuthors},
		}
		directory, facade := writeGeneratedRelationFacadeUniverse(
			t,
			modulePath,
			packages,
			nil,
			generatedRelationFacadeTypedResultCompileTest(modulePath),
		)
		for _, fragment := range [][]byte{
			[]byte("AuthorsAuthor  AuthorsAuthorQuery"),
			[]byte("AuthorsProfile AuthorsProfileQuery"),
			[]byte("BlogPost       BlogPostQuery"),
			[]byte("TagsTag        TagsTagQuery"),
		} {
			if !bytes.Contains(facade, fragment) {
				t.Fatalf("broad facade does not contain %q:\n%s", fragment, facade)
			}
		}
		compileGeneratedRelationFacadeUniverse(t, directory)
	})

	t.Run("target also source", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-facade-mutual"
		mutualAuthors, mutualBlog := mutualRelationGenerationSchemas()
		packages := testfixture.FacadePackages(modulePath, mutualAuthors, mutualBlog)
		directory, _ := writeGeneratedRelationFacadeUniverse(
			t,
			modulePath,
			packages,
			nil,
			generatedRelationFacadeCrossSelectorTest(modulePath),
		)
		compileGeneratedRelationFacadeUniverse(t, directory)
	})

	t.Run("self edge", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-facade-self"
		nodes := relationFacadeSelfSchema()
		packages := []codegen.RelationObjectPackage{{Alias: "nodes", ImportPath: modulePath + "/nodes", Schema: nodes}}
		directory, facade := writeGeneratedRelationFacadeUniverse(t, modulePath, packages, nil, nil)
		for _, fragment := range [][]byte{
			[]byte("NodesNode NodesNodeQuery"),
			[]byte("func (_model *NodesNode) Parent(_ctx context.Context) (*NodesNode, bool, error)"),
		} {
			if !bytes.Contains(facade, fragment) {
				t.Fatalf("self facade does not contain %q:\n%s", fragment, facade)
			}
		}
		compileGeneratedRelationFacadeUniverse(t, directory)
	})
}

func TestGeneratedProjectRelationFacadePromotesApplicationSurfaceAndRejectsDirectJSON(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-facade-promotion"
	directory, _ := writeGeneratedRelationFacadeUniverse(
		t,
		modulePath,
		testfixture.FacadePackages(modulePath, authors, blog),
		nil,
		nil,
	)
	writeGeneratedTestFile(t, directory, "blog/model_methods.go", []byte(`package blog

import "strings"

func (_post Post) NormalizedTitle() string {
	return strings.TrimSpace(_post.Title)
}

func (_post *Post) NormalizeTitle() {
	_post.Title = strings.TrimSpace(_post.Title)
}
`))
	writeGeneratedTestFile(t, directory, "project/relation_facade_promotion_external_test.go", []byte(fmt.Sprintf(`package project_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	project %q
	"github.com/progresshans/godj/query"
)

type normalizedTitle interface {
	NormalizedTitle() string
}

type normalizeTitle interface {
	NormalizeTitle()
}

var _ normalizedTitle = (*project.BlogPost)(nil)
var _ normalizeTitle = (*project.BlogPost)(nil)

func compilePromotedApplicationSurface(_post *project.BlogPost) {
	_post.Title = " current "
	_post.NormalizeTitle()
	_ = _post.NormalizedTitle()
}

func TestDirectJSONIsFailClosed(t *testing.T) {
	value := project.BlogPost{}
	value.Title = "secret"
	assertRejected := func(label string, data []byte, err error, detail string) {
		t.Helper()
		if len(data) != 0 || !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) ||
			!strings.Contains(err.Error(), detail) {
			t.Fatalf("%%s = (%%q, %%v), want deterministic query/invalid_plan", label, data, err)
		}
	}

	data, err := json.Marshal(value)
	assertRejected("value marshal", data, err, "direct JSON marshal")
	data, err = json.Marshal(&value)
	assertRejected("pointer marshal", data, err, "direct JSON marshal")

	before := value.Title
	err = json.Unmarshal([]byte("{\"Title\":\"mutated\"}"), &value)
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) ||
		!strings.Contains(err.Error(), "direct JSON unmarshal") || value.Title != before {
		t.Fatalf("pointer unmarshal = (%%v, %%q), want deterministic rejection and unchanged receiver", err, value.Title)
	}

	var nilPost *project.BlogPost
	data, err = json.Marshal(nilPost)
	if err != nil || string(data) != "null" {
		t.Fatalf("nil pointer marshal = (%%q, %%v), want null/nil", data, err)
	}
}
`, modulePath+"/project")))
	compileGeneratedRelationFacadeUniverse(t, directory)
}

func TestGeneratedProjectRelationFacadeRejectsCrossModelTypedResultsAtCompileTime(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-facade-cross-result"
	directory, _ := writeGeneratedRelationFacadeUniverse(
		t,
		modulePath,
		testfixture.FacadePackages(modulePath, authors, blog),
		nil,
		generatedRelationFacadeCrossModelResultCompileFailure(modulePath),
	)
	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("cross-model typed results unexpectedly compiled")
	}
	if !bytes.Contains(output, []byte("Projection[")) || !bytes.Contains(output, []byte("SelectBlogPostInto")) {
		t.Fatalf("cross-model compile failure did not identify the typed projection boundary: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte("Aggregate[")) || !bytes.Contains(output, []byte("AggregateBlogPostInto")) {
		t.Fatalf("cross-model compile failure did not identify the typed aggregate boundary: %v\n%s", err, output)
	}
}

func TestProjectRelationFacadeRuntime(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	reordered := blog.Clone()
	reordered.Models[0].Fields[2], reordered.Models[0].Fields[3] = reordered.Models[0].Fields[3], reordered.Models[0].Fields[2]
	for _, test := range []struct {
		name string
		blog ir.Schema
	}{{name: "declared", blog: blog}, {name: "permuted fields", blog: reordered}} {
		t.Run(test.name, func(t *testing.T) {
			modulePath := "example.com/godj-relation-facade-write-" + strings.ReplaceAll(test.name, " ", "-")
			directory, facade := writeGeneratedRelationFacadeUniverse(
				t,
				modulePath,
				testfixture.FacadePackages(modulePath, authors, test.blog),
				nil,
				generatedProjectRelationFacadeRuntimeTest(modulePath),
			)
			if !bytes.Contains(facade, []byte("func (_model *BlogPost) WithReviewerID(_key int64) (*BlogPost, error)")) ||
				bytes.Contains(facade, []byte("WithReviewerID(_key *int64)")) {
				t.Fatalf("nullable scalar setter did not use the non-pointer key contract:\n%s", facade)
			}
			compileGeneratedRelationFacadeUniverse(t, directory)
			writeGeneratedTestFile(t, directory, "project/pointer_key_compile_negative_test.go", []byte(`package project

func pointerKeyMustNotCompile(_model *BlogPost) {
	_key := int64(1)
	_, _ = _model.WithReviewerID(&_key)
}
`))
			command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./project")
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatal("nullable pointer scalar setter unexpectedly compiled")
			}
			if !bytes.Contains(output, []byte("*int64")) || !bytes.Contains(output, []byte("int64")) {
				t.Fatalf("pointer scalar compile-negative did not identify the key types: %v\n%s", err, output)
			}
		})
	}
}

func TestProjectRelationFacadeEagerCOW(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-facade-write-eager"
	directory, _ := writeGeneratedRelationFacadeUniverse(
		t,
		modulePath,
		testfixture.FacadePackages(modulePath, authors, blog),
		nil,
		generatedProjectRelationFacadeEagerCOWTest(modulePath),
	)
	compileGeneratedRelationFacadeUniverse(t, directory)
}

func TestProjectRelationFacadeEmptyUniverseCompiles(t *testing.T) {
	const modulePath = "example.com/godj-relation-facade-write-empty"
	directory, _ := writeGeneratedRelationFacadeUniverse(t, modulePath, nil, nil, nil)
	compileGeneratedRelationFacadeUniverse(t, directory)
}

func TestGeneratedProjectRelationFacadeInvalidStatesAndBindingPrecedence(t *testing.T) {
	authors, blog := testschema.QueryRelation()

	t.Run("nil zero copied", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-facade-invalid-state"
		directory, _ := writeGeneratedRelationFacadeUniverse(
			t,
			modulePath,
			testfixture.FacadePackages(modulePath, authors, blog),
			nil,
			generatedRelationFacadeInvalidStateTest(modulePath),
		)
		compileGeneratedRelationFacadeUniverse(t, directory)
	})

	t.Run("binding cause precedes nil backend", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-facade-binding-precedence"
		binding := []byte(`package project

import (
	"errors"

	"github.com/progresshans/godj/orm"
)

var relationFacadeBinderSentinel = errors.New("relation facade binder sentinel")

func Bind() (orm.ProjectBinding, error) {
	return orm.ProjectBinding{}, relationFacadeBinderSentinel
}
`)
		directory, _ := writeGeneratedRelationFacadeUniverse(
			t,
			modulePath,
			testfixture.FacadePackages(modulePath, authors, blog),
			binding,
			[]byte(`package project

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

var bindingPrecedenceIO int

type bindingPrecedenceBackend struct{}

func (*bindingPrecedenceBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	bindingPrecedenceIO++
	return nil, nil
}

func (*bindingPrecedenceBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	bindingPrecedenceIO++
	return 0, nil
}

func (*bindingPrecedenceBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	bindingPrecedenceIO++
	return 0, nil
}

func (*bindingPrecedenceBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	bindingPrecedenceIO++
	return 0, nil
}

func TestBindingCausePrecedesNilBackend(t *testing.T) {
	_, err := Using(nil)
	if !errors.Is(err, relationFacadeBinderSentinel) {
		t.Fatalf("Using(nil) error = %v, want exact binder cause", err)
	}
	var typedNil *bindingPrecedenceBackend
	_, err = Using(typedNil)
	if !errors.Is(err, relationFacadeBinderSentinel) {
		t.Fatalf("Using(typed nil) error = %v, want exact binder cause", err)
	}
	if bindingPrecedenceIO != 0 {
		t.Fatalf("binding-precedence backend I/O = %d, want 0", bindingPrecedenceIO)
	}
}
`),
		)
		compileGeneratedRelationFacadeUniverse(t, directory)
	})
}

func relationFacadeSelfSchema() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "nodes",
		Models: []ir.Model{{
			Name: "node", GoName: "Node", DBTable: "nodes_node",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "parent", GoName: "ParentID", Column: "parent_id", Kind: ir.FieldForeignKey, Nullable: true,
					Relation: &ir.ForeignKeyRelation{
						Target: ir.ModelIdentity{AppLabel: "nodes", ModelName: "node"}, Cardinality: ir.RelationManyToOne,
						Reverse: ir.ReverseRelation{Name: "children"}, OnDelete: ir.DeleteSetNull,
					},
				},
			},
		}},
	}
}

func writeGeneratedRelationFacadeUniverse(
	t *testing.T,
	modulePath string,
	packages []codegen.RelationObjectPackage,
	bindingOverride, projectTest []byte,
) (string, []byte) {
	t.Helper()
	directory := newGeneratedModule(t, modulePath)

	bridgePackages := make([]codegen.BridgePackage, len(packages))
	for index, candidate := range packages {
		directoryName := strings.TrimPrefix(candidate.ImportPath, modulePath+"/")
		if directoryName == candidate.ImportPath || directoryName == "" || strings.Contains(directoryName, "..") {
			t.Fatalf("fixture import path %q is not confined to module %q", candidate.ImportPath, modulePath)
		}
		bridgePackages[index] = codegen.BridgePackage{Alias: candidate.Alias, ImportPath: candidate.ImportPath}
		main := testfixture.Generate(t, candidate.Alias+" main", func() ([]byte, error) {
			return codegen.Generate(candidate.Alias, candidate.Schema)
		})
		metadata := testfixture.Generate(t, candidate.Alias+" metadata", func() ([]byte, error) {
			return codegen.GenerateRelationMetadata(candidate.Alias, candidate.Schema)
		})
		object := testfixture.Generate(t, candidate.Alias+" object", func() ([]byte, error) {
			return codegen.GenerateRelationObject(candidate.Alias, candidate.Schema)
		})
		projection := testfixture.Generate(t, candidate.Alias+" projection", func() ([]byte, error) {
			return codegen.GenerateRelationProjection(candidate.Alias, candidate.Schema)
		})
		writeGeneratedTestFile(t, directory, directoryName+"/zz_godj_generated.go", main)
		writeGeneratedTestFile(t, directory, directoryName+"/zz_godj_relation.go", metadata)
		writeGeneratedTestFile(t, directory, directoryName+"/zz_godj_relation_object.go", object)
		writeGeneratedTestFile(t, directory, directoryName+"/zz_godj_relation_projection.go", projection)
	}

	binding := bindingOverride
	if binding == nil {
		binding = testfixture.Generate(t, "project binding", func() ([]byte, error) {
			return codegen.GenerateProjectBridge("project", bridgePackages)
		})
	}
	object := testfixture.Generate(t, "project object", func() ([]byte, error) {
		return codegen.GenerateProjectRelationObject("project", packages)
	})
	selectRelated := testfixture.Generate(t, "project select related", func() ([]byte, error) {
		return codegen.GenerateProjectRelationSelectRelated("project", packages)
	})
	facade := testfixture.Generate(t, "project relation facade", func() ([]byte, error) {
		return codegen.GenerateProjectRelationFacade("project", packages)
	})
	writeGeneratedTestFile(t, directory, "project/zz_godj_binding.go", binding)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_object.go", object)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_select_related.go", selectRelated)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_facade.go", facade)
	if projectTest != nil {
		writeGeneratedTestFile(t, directory, "project/relation_facade_test.go", projectTest)
	}
	return directory, facade
}

func compileGeneratedRelationFacadeUniverse(t *testing.T, directory string) {
	t.Helper()
	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated relation facade universe did not compile: %v\n%s", err, output)
	}
}

func generatedProjectRelationFacadeRuntimeTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"
	"errors"
	"reflect"
	"testing"

	authors %q
	blog %q
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type relationFacadeRow struct { values []any }

func (row relationFacadeRow) Scan(destinations ...any) error {
	if len(destinations) != len(row.values) { return errors.New("scan width mismatch") }
	for index, destination := range destinations {
		reflected := reflect.ValueOf(destination)
		if reflected.Kind() != reflect.Pointer { return errors.New("scan destination is not a pointer") }
		value := row.values[index]
		if value == nil { continue }
		reflected.Elem().Set(reflect.ValueOf(value).Convert(reflected.Elem().Type()))
	}
	return nil
}

type relationFacadeRows struct { rows [][]any; index int }

func (rows *relationFacadeRows) Next() bool { return rows.index < len(rows.rows) }
func (rows *relationFacadeRows) Scan(destinations ...any) error {
	if rows.index >= len(rows.rows) { return errors.New("scan after rows end") }
	err := (relationFacadeRow{values: rows.rows[rows.index]}).Scan(destinations...)
	rows.index++
	return err
}
func (*relationFacadeRows) Err() error { return nil }
func (*relationFacadeRows) Close() error { return nil }

type relationFacadeBackend struct {
	queries int
	inserts int
	updates int
	deletes int
	nextID int64
	authors map[int64]string
	posts map[int64]blog.Post
}

var relationFacadeForeignKeyFailure = errors.New("relation facade foreign key sentinel")

func newRelationFacadeBackend() *relationFacadeBackend {
	reviewer := int64(2)
	return &relationFacadeBackend{
		nextID: 40,
		authors: map[int64]string{0: "Zero", 1: "Ada", 2: "Bob", 3: "Cleo"},
		posts: map[int64]blog.Post{10: {ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewer}},
	}
}

func (backend *relationFacadeBackend) io() int { return backend.queries + backend.inserts + backend.updates + backend.deletes }

func (backend *relationFacadeBackend) Query(_ context.Context, plan query.Plan) (db.Rows, error) {
	backend.queries++
	switch plan.Table() {
	case "authors_author":
		for key, name := range backend.authors {
			return &relationFacadeRows{rows: [][]any{{key, name}}}, nil
		}
		return &relationFacadeRows{}, nil
	case "blog_post":
		for _, post := range backend.posts {
			return &relationFacadeRows{rows: [][]any{{post.ID, post.Title, post.AuthorID, post.ReviewerID}}}, nil
		}
		return &relationFacadeRows{}, nil
	default:
		return nil, errors.New("unexpected query table")
	}
}

func (backend *relationFacadeBackend) Insert(_ context.Context, plan query.InsertPlan) (int64, error) {
	backend.inserts++
	if plan.Table() == "authors_author" {
		backend.nextID++
		backend.authors[backend.nextID] = "saved"
		return backend.nextID, nil
	}
	if plan.Table() != "blog_post" { return 0, errors.New("unexpected insert table") }
	var post blog.Post
	for _, assignment := range plan.Assignments() {
		switch assignment.Field().Name() {
		case "title": post.Title, _ = assignment.Value().String()
		case "author": post.AuthorID, _ = assignment.Value().Integer()
		case "reviewer":
			if !assignment.Value().IsNull() { value, _ := assignment.Value().Integer(); post.ReviewerID = &value }
		}
	}
	if _, ok := backend.authors[post.AuthorID]; !ok { return 0, relationFacadeForeignKeyFailure }
	backend.nextID++
	post.ID = backend.nextID
	backend.posts[post.ID] = post
	return post.ID, nil
}

func (backend *relationFacadeBackend) Update(context.Context, query.UpdatePlan) (int64, error) { backend.updates++; return 1, nil }
func (backend *relationFacadeBackend) Delete(context.Context, query.DeletePlan) (int64, error) { backend.deletes++; return 1, nil }

func TestProjectRelationFacadePendingNoPKLaterKeyAndManualKey(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, err := Using(backend)
	if err != nil { t.Fatal(err) }
	unset, err := models.BlogPost.New(blog.Post{Title: "required unset"})
	if err != nil { t.Fatal(err) }
	beforeIO := backend.io()
	if _, err := unset.Author(ctx); !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeRequiredField, Field: "author"}) || backend.io() != beforeIO { t.Fatalf("required-unset accessor = %%v, io=%%d want %%d", err, backend.io(), beforeIO) }
	if reviewer, present, err := unset.Reviewer(ctx); err != nil || present || reviewer != nil || backend.io() != beforeIO { t.Fatalf("new nullable-absent accessor = (%%p,%%v,%%v), io=%%d want %%d", reviewer, present, err, backend.io(), beforeIO) }
	if _, err := unset.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeRequiredField, Field: "author"}) { t.Fatalf("required-unset Unwrap = %%v", err) }
	beforePosts := len(backend.posts)
	if err := unset.Save(ctx); !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeRequiredField, Field: "author"}) || backend.io() != beforeIO || len(backend.posts) != beforePosts { t.Fatalf("required-unset Save = %%v, io=%%d posts=%%d", err, backend.io(), len(backend.posts)) }
	rawPresent, err := models.BlogPost.New(blog.Post{Title: "raw present", AuthorID: 1})
	if err != nil { t.Fatal(err) }
	if raw, err := rawPresent.Unwrap(); err != nil || raw.AuthorID != 1 { t.Fatalf("new raw nonzero presence = %%#v, %%v", raw, err) }
	loadedZero, err := models.BlogPost.state.wrapBlogPost(blog.Post{ID: 12, Title: "loaded zero", AuthorID: 0})
	if err != nil { t.Fatal(err) }
	if raw, err := loadedZero.Unwrap(); err != nil || raw.AuthorID != 0 { t.Fatalf("loaded zero presence = %%#v, %%v", raw, err) }
	beforeLoadedZero := backend.queries
	if _, err := loadedZero.Author(ctx); err != nil || backend.queries != beforeLoadedZero+1 { t.Fatalf("loaded zero accessor = %%v, queries=%%d want %%d", err, backend.queries, beforeLoadedZero+1) }
	explicitZero, err := unset.WithAuthorID(0)
	if err != nil { t.Fatal(err) }
	if err := explicitZero.Save(ctx); err != nil { t.Fatalf("explicit FK zero Save = %%v", err) }
	explicitZeroRaw, err := explicitZero.Unwrap()
	if err != nil || explicitZeroRaw.ID == 0 || explicitZeroRaw.AuthorID != 0 { t.Fatalf("explicit FK zero raw = %%#v, %%v", explicitZeroRaw, err) }
	zeroTarget, err := models.AuthorsAuthor.New(authors.NewAuthorWithID(0))
	if err != nil { t.Fatal(err) }
	zeroObjectSource, _ := models.BlogPost.New(blog.Post{Title: "object PK zero"})
	zeroObjectDerived, err := zeroObjectSource.WithAuthor(zeroTarget)
	if err != nil { t.Fatal(err) }
	beforeZeroTarget := backend.io()
	if got, err := zeroObjectDerived.Author(ctx); err != nil || got != zeroTarget || backend.io() != beforeZeroTarget { t.Fatalf("object PK zero accessor = %%p, %%v, io=%%d want %%d", got, err, backend.io(), beforeZeroTarget) }
	if !zeroObjectDerived.authorScalarPresent { t.Fatal("object PK zero did not establish source scalar presence") }
	beforeZeroInserts := backend.inserts
	if err := zeroObjectDerived.Save(ctx); err != nil || backend.inserts != beforeZeroInserts+1 { t.Fatalf("object PK zero Save = %%v, inserts=%%d want %%d", err, backend.inserts, beforeZeroInserts+1) }
	author, err := models.AuthorsAuthor.New(authors.Author{Name: "new"})
	if err != nil { t.Fatal(err) }
	post, err := models.BlogPost.New(blog.Post{Title: "draft"})
	if err != nil { t.Fatal(err) }
	derived, err := post.WithAuthor(author)
	if err != nil { t.Fatal(err) }
	beforePending := backend.io()
	got, err := derived.Author(ctx)
	if err != nil || got != author || backend.io() != beforePending { t.Fatalf("warm pending accessor = (%%p, %%v), io=%%d want %%d", got, err, backend.io(), beforePending) }
	if _, err := derived.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) { t.Fatalf("pending Unwrap error = %%v", err) }
	if err := derived.Save(ctx); !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) || backend.io() != beforePending { t.Fatalf("pending Save = %%v, io=%%d want %%d", err, backend.io(), beforePending) }
	if err := author.Save(ctx); err != nil { t.Fatal(err) }
	if !author.primaryKeySnapshotPresent || author.primaryKeySnapshot != author.ID {
		t.Fatalf("successful target Save did not refresh primary snapshot: key=%%d present=%%v raw=%%d", author.primaryKeySnapshot, author.primaryKeySnapshotPresent, author.ID)
	}
	author.Name = "saved and mutable"
	if err := author.Save(ctx); err != nil { t.Fatalf("ordinary scalar mutation after primary refresh = %%v", err) }
	if err := derived.Save(ctx); err != nil { t.Fatal(err) }
	raw, err := derived.Unwrap()
	if err != nil || raw.ID == 0 || raw.AuthorID == 0 { t.Fatalf("reconciled source = %%#v, %%v", raw, err) }

	manual, err := models.AuthorsAuthor.New(authors.NewAuthorWithID(999))
	if err != nil { t.Fatal(err) }
	manualPost, _ := models.BlogPost.New(blog.Post{Title: "manual"})
	manualDerived, _ := manualPost.WithAuthor(manual)
	beforePosts = len(backend.posts)
	beforeInserts := backend.inserts
	err = manualDerived.Save(ctx)
	if !errors.Is(err, relationFacadeForeignKeyFailure) || errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) || backend.inserts != beforeInserts+1 || len(backend.posts) != beforePosts { t.Fatalf("manual-key DB reach = %%v, inserts=%%d want %%d posts=%%d", err, backend.inserts, beforeInserts+1, len(backend.posts)) }
}

func TestProjectRelationFacadeSameChangedScalarAndUnrelatedCacheCOW(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	author, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	reviewer, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(2))
	post, _ := models.BlogPost.New(blog.Post{Title: "cache"})
	post, _ = post.WithAuthor(author)
	post, _ = post.WithReviewer(reviewer)
	same, _ := post.WithAuthorID(1)
	got, _ := same.Author(ctx)
	if got != author || backend.queries != 0 { t.Fatalf("same scalar lost cache: got=%%p want=%%p queries=%%d", got, author, backend.queries) }
	changed, _ := same.WithAuthorID(3)
	before := backend.queries
	if _, err := changed.Author(ctx); err != nil { t.Fatal(err) }
	if backend.queries != before+1 { t.Fatalf("changed scalar queries=%%d want %%d", backend.queries, before+1) }
	before = backend.queries
	gotReviewer, present, err := changed.Reviewer(ctx)
	if err != nil || !present || gotReviewer != reviewer || backend.queries != before { t.Fatalf("unrelated reviewer cache = (%%p,%%v,%%v) queries=%%d", gotReviewer, present, err, backend.queries) }
	originalReviewer, present, err := post.Reviewer(ctx)
	if err != nil || !present || originalReviewer != reviewer { t.Fatalf("original reviewer changed = (%%p,%%v,%%v)", originalReviewer, present, err) }
	if post.authorCache == changed.authorCache || post.reviewerCache == changed.reviewerCache { t.Fatal("derived wrapper shared a mutable cache cell") }
	cleared, err := changed.ClearReviewer()
	if err != nil { t.Fatal(err) }
	before = backend.queries
	gotReviewer, present, err = cleared.Reviewer(ctx)
	if err != nil || present || gotReviewer != nil || backend.queries != before { t.Fatalf("cleared reviewer = (%%p,%%v,%%v), queries=%%d", gotReviewer, present, err, backend.queries) }
	raw, err := cleared.Unwrap()
	if err != nil || raw.ReviewerID != nil { t.Fatalf("cleared raw = %%#v, %%v", raw, err) }
	if err := cleared.Save(ctx); err != nil { t.Fatal(err) }
	unsavedReviewer, _ := models.AuthorsAuthor.New(authors.Author{Name: "pending reviewer"})
	pendingReviewer, _ := post.WithReviewer(unsavedReviewer)
	if _, err := pendingReviewer.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) { t.Fatalf("nullable pending Unwrap error = %%v", err) }
	before = backend.io()
	if err := pendingReviewer.Save(ctx); !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}) || backend.io() != before { t.Fatalf("nullable pending Save = %%v, io=%%d want %%d", err, backend.io(), before) }
	clearedPending, err := pendingReviewer.ClearReviewer()
	if err != nil { t.Fatal(err) }
	before = backend.queries
	gotReviewer, present, err = clearedPending.Reviewer(ctx)
	if err != nil || present || gotReviewer != nil || backend.queries != before { t.Fatalf("pending then clear reviewer = (%%p,%%v,%%v), queries=%%d", gotReviewer, present, err, backend.queries) }
	clearedPendingRaw, err := clearedPending.Unwrap()
	if err != nil || clearedPendingRaw.ReviewerID != nil { t.Fatalf("cleared pending Unwrap = %%#v, %%v", clearedPendingRaw, err) }
}

func TestProjectRelationFacadeDirectForeignKeyMutationReconcilesAtEveryBoundary(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	author, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	reviewer, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(2))
	base, _ := models.BlogPost.New(blog.Post{Title: "direct relation"})
	base, _ = base.WithAuthor(author)
	base, _ = base.WithReviewer(reviewer)
	branch, _ := base.WithAuthorID(1)
	baseAuthorCache := base.authorCache
	baseReviewerCache := base.reviewerCache
	branchReviewerCache := branch.reviewerCache

	branch.AuthorID = 3
	before := backend.queries
	gotAuthor, err := branch.Author(ctx)
	if err != nil || gotAuthor == author || backend.queries != before+1 {
		t.Fatalf("direct required FK accessor = (%%p,%%v), queries=%%d want %%d", gotAuthor, err, backend.queries, before+1)
	}
	if branch.authorScalarSnapshot != 3 || !branch.authorScalarPresent || branch.authorCache == baseAuthorCache {
		t.Fatalf("direct required FK snapshot/cache = (%%d,%%v,shared=%%v)", branch.authorScalarSnapshot, branch.authorScalarPresent, branch.authorCache == baseAuthorCache)
	}
	if branch.reviewerCache != branchReviewerCache {
		t.Fatal("unchanged nullable edge cache cell was replaced")
	}
	before = backend.queries
	gotReviewer, present, err := branch.Reviewer(ctx)
	if err != nil || !present || gotReviewer != reviewer || backend.queries != before {
		t.Fatalf("unchanged nullable edge = (%%p,%%v,%%v), queries=%%d want %%d", gotReviewer, present, err, backend.queries, before)
	}
	if base.AuthorID != 1 || base.authorCache != baseAuthorCache || base.reviewerCache != baseReviewerCache {
		t.Fatal("direct mutation on derived wrapper changed the original wrapper")
	}

	directReviewer := int64(3)
	branch.ReviewerID = &directReviewer
	authorCacheBefore := branch.authorCache
	before = backend.queries
	gotReviewer, present, err = branch.Reviewer(ctx)
	if err != nil || !present || gotReviewer == reviewer || backend.queries != before+1 {
		t.Fatalf("direct nullable FK accessor = (%%p,%%v,%%v), queries=%%d want %%d", gotReviewer, present, err, backend.queries, before+1)
	}
	if branch.reviewerScalarSnapshot != 3 || !branch.reviewerScalarPresent || branch.authorCache != authorCacheBefore {
		t.Fatalf("nullable snapshot/unchanged required cache = (%%d,%%v,same=%%v)", branch.reviewerScalarSnapshot, branch.reviewerScalarPresent, branch.authorCache == authorCacheBefore)
	}
	branch.ReviewerID = nil
	before = backend.queries
	gotReviewer, present, err = branch.Reviewer(ctx)
	if err != nil || present || gotReviewer != nil || backend.queries != before || branch.reviewerScalarPresent {
		t.Fatalf("direct nullable clear = (%%p,%%v,%%v), queries=%%d want %%d scalar=%%v", gotReviewer, present, err, backend.queries, before, branch.reviewerScalarPresent)
	}

	unset, _ := models.BlogPost.New(blog.Post{Title: "direct zero remains unset"})
	unset.AuthorID = 0
	before = backend.io()
	if _, err := unset.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeRequiredField, Field: "author"}) || backend.io() != before {
		t.Fatalf("unchanged required raw zero = %%v, io=%%d want %%d", err, backend.io(), before)
	}
	unset.AuthorID = 3
	raw, err := unset.Unwrap()
	if err != nil || raw.AuthorID != 3 || !unset.authorScalarPresent || unset.authorScalarSnapshot != 3 {
		t.Fatalf("direct required nonzero Unwrap = %%#v, %%v presence=%%v snapshot=%%d", raw, err, unset.authorScalarPresent, unset.authorScalarSnapshot)
	}

	withBoundary, _ := base.WithAuthorID(1)
	withBoundary.AuthorID = 3
	withResult, err := withBoundary.WithReviewerID(2)
	if err != nil || withResult.AuthorID != 3 || withResult.authorScalarSnapshot != 3 {
		t.Fatalf("With boundary reconciliation = (%%#v,%%v)", withResult, err)
	}
	withState, withTarget, withPending, _ := withResult.authorCache.Snapshot()
	if withState != orm.RelationUnassigned || withTarget != nil || withPending {
		t.Fatalf("With boundary retained stale author cache = (%%d,%%p,%%v)", withState, withTarget, withPending)
	}
	clearBoundary, _ := base.WithAuthorID(1)
	clearBoundary.AuthorID = 3
	clearResult, err := clearBoundary.ClearReviewer()
	if err != nil || clearResult.AuthorID != 3 || clearResult.authorScalarSnapshot != 3 {
		t.Fatalf("Clear boundary reconciliation = (%%#v,%%v)", clearResult, err)
	}
	saveBoundary, _ := models.BlogPost.New(blog.Post{Title: "direct save", AuthorID: 1})
	saveBoundary.AuthorID = 3
	saveReviewerID := int64(2)
	saveBoundary.ReviewerID = &saveReviewerID
	if err := saveBoundary.Save(ctx); err != nil || saveBoundary.authorScalarSnapshot != 3 || saveBoundary.reviewerScalarSnapshot != 2 || !saveBoundary.primaryKeySnapshotPresent || saveBoundary.primaryKeySnapshot != saveBoundary.ID {
		t.Fatalf("Save boundary reconciliation/snapshot refresh = %%v wrapper=%%#v", err, saveBoundary)
	}
}

func TestProjectRelationFacadeDirectScalarOverridesPendingTargets(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	unsavedAuthor, _ := models.AuthorsAuthor.New(authors.Author{Name: "pending author"})
	unsavedReviewer, _ := models.AuthorsAuthor.New(authors.Author{Name: "pending reviewer"})
	base, _ := models.BlogPost.New(blog.Post{Title: "pending override"})
	pending, _ := base.WithAuthor(unsavedAuthor)
	pending, _ = pending.WithReviewer(unsavedReviewer)
	reviewerID := int64(2)
	pending.AuthorID = 3
	pending.ReviewerID = &reviewerID
	before := backend.io()
	raw, err := pending.Unwrap()
	if err != nil || raw.AuthorID != 3 || raw.ReviewerID == nil || *raw.ReviewerID != 2 || backend.io() != before {
		t.Fatalf("direct pending override Unwrap = %%#v, %%v io=%%d want %%d", raw, err, backend.io(), before)
	}
	authorState, authorTarget, authorPending, _ := pending.authorCache.Snapshot()
	reviewerState, reviewerTarget, reviewerPending, _ := pending.reviewerCache.Snapshot()
	if authorState != orm.RelationUnassigned || authorTarget != nil || authorPending ||
		reviewerState != orm.RelationUnassigned || reviewerTarget != nil || reviewerPending {
		t.Fatalf("pending caches survived direct scalar override: author=(%%d,%%p,%%v) reviewer=(%%d,%%p,%%v)", authorState, authorTarget, authorPending, reviewerState, reviewerTarget, reviewerPending)
	}
	if err := pending.Save(ctx); err != nil { t.Fatalf("direct pending override Save = %%v", err) }
}

func TestProjectRelationFacadeMultiEdgeReconcileFailurePublishesNothing(t *testing.T) {
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	author, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	reviewer, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(2))
	candidate, _ := models.BlogPost.New(blog.Post{Title: "atomic direct reconcile"})
	candidate, _ = candidate.WithAuthor(author)
	candidate, _ = candidate.WithReviewer(reviewer)
	objectBefore := candidate.object
	authorCacheBefore := candidate.authorCache
	reviewerCacheBefore := candidate.reviewerCache
	authorSnapshotBefore := candidate.authorScalarSnapshot
	reviewerSnapshotBefore := candidate.reviewerScalarSnapshot
	reviewerPresentBefore := candidate.reviewerScalarPresent
	candidate.AuthorID = 3
	reviewerID := int64(3)
	candidate.ReviewerID = &reviewerID
	candidate.state.objects.BlogPost = BlogPostObjectFactory{}
	before := backend.io()
	_, err := candidate.Unwrap()
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != before {
		t.Fatalf("multi-edge reconcile failure = %%v, io=%%d want %%d", err, backend.io(), before)
	}
	if candidate.AuthorID != 3 || candidate.ReviewerID == nil || *candidate.ReviewerID != 3 || candidate.object != objectBefore ||
		candidate.authorCache != authorCacheBefore || candidate.reviewerCache != reviewerCacheBefore ||
		candidate.authorScalarSnapshot != authorSnapshotBefore || candidate.reviewerScalarSnapshot != reviewerSnapshotBefore ||
		candidate.reviewerScalarPresent != reviewerPresentBefore {
		t.Fatal("failed multi-edge reconciliation partially published wrapper state")
	}
}

func TestProjectRelationFacadeDirectPrimaryKeyMutationFailsBeforeIO(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	target, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	source, _ := models.BlogPost.New(blog.Post{Title: "pk mutation"})
	derived, _ := source.WithAuthor(target)
	(authors.AuthorDescriptor{}).SetPrimaryKey(&target.authorsAuthorModel, 3)
	before := backend.io()
	want := &query.Error{Category: query.CategoryModelState, Code: query.CodePrimaryKeyUpdateField, Field: "id"}
	if err := target.Save(ctx); !errors.Is(err, want) || backend.io() != before { t.Fatalf("target Save = %%v, io=%%d want %%d", err, backend.io(), before) }
	if _, err := target.Unwrap(); !errors.Is(err, want) || backend.io() != before { t.Fatalf("target Unwrap = %%v, io=%%d want %%d", err, backend.io(), before) }
	if err := derived.Save(ctx); !errors.Is(err, want) || backend.io() != before { t.Fatalf("source Save through mutated target = %%v, io=%%d want %%d", err, backend.io(), before) }
	if _, err := derived.Author(ctx); !errors.Is(err, want) || backend.io() != before { t.Fatalf("source accessor through mutated target = %%v, io=%%d want %%d", err, backend.io(), before) }
	if derived.blogPostModel.AuthorID != 1 { t.Fatalf("source scalar = %%d, want unchanged 1", derived.blogPostModel.AuthorID) }
	loaded := blog.NewPostWithID(10)
	loaded.Title = "source PK mutation"
	loaded.AuthorID = 1
	sourcePK, _ := models.BlogPost.New(loaded)
	sourcePK.ID = 11
	before = backend.io()
	if err := sourcePK.Save(ctx); !errors.Is(err, want) || backend.io() != before { t.Fatalf("source PK Save = %%v, io=%%d want %%d", err, backend.io(), before) }
	if _, err := sourcePK.Unwrap(); !errors.Is(err, want) || backend.io() != before { t.Fatalf("source PK Unwrap = %%v, io=%%d want %%d", err, backend.io(), before) }
	if _, err := sourcePK.WithAuthorID(1); !errors.Is(err, want) || backend.io() != before { t.Fatalf("source PK With = %%v, io=%%d want %%d", err, backend.io(), before) }
	if _, err := sourcePK.ClearReviewer(); !errors.Is(err, want) || backend.io() != before { t.Fatalf("source PK Clear = %%v, io=%%d want %%d", err, backend.io(), before) }
	if _, err := sourcePK.Author(ctx); !errors.Is(err, want) || backend.io() != before { t.Fatalf("source PK accessor = %%v, io=%%d want %%d", err, backend.io(), before) }
}

func TestProjectRelationFacadeCanonicalRelationPreflightOrder(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	source, _ := models.BlogPost.New(blog.Post{Title: "two pending edges"})
	author, _ := models.AuthorsAuthor.New(authors.Author{Name: "pending author"})
	reviewer, _ := models.AuthorsAuthor.New(authors.Author{Name: "pending reviewer"})
	derived, _ := source.WithReviewer(reviewer)
	derived, _ = derived.WithAuthor(author)
	before := backend.io()
	err := derived.Save(ctx)
	if !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject, Field: "author"}) || backend.io() != before {
		t.Fatalf("canonical preflight = %%v, io=%%d want %%d", err, backend.io(), before)
	}

	requiredUnset, _ := models.BlogPost.New(blog.Post{Title: "required unset reviewer pending"})
	reviewerOnly, _ := requiredUnset.WithReviewer(reviewer)
	before = backend.io()
	err = reviewerOnly.Save(ctx)
	if !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject, Field: "reviewer"}) || backend.io() != before {
		t.Fatalf("pending target did not precede required unset: %%v, io=%%d want %%d", err, backend.io(), before)
	}
	if _, err := reviewerOnly.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject, Field: "reviewer"}) {
		t.Fatalf("pending Unwrap did not precede required unset: %%v", err)
	}

	lateAuthor, _ := models.AuthorsAuthor.New(authors.Author{Name: "late author"})
	laterReviewer, _ := models.AuthorsAuthor.New(authors.Author{Name: "later reviewer"})
	staged, _ := requiredUnset.WithAuthor(lateAuthor)
	staged, _ = staged.WithReviewer(laterReviewer)
	if err := lateAuthor.Save(ctx); err != nil { t.Fatal(err) }
	objectBefore := staged.object
	authorStateBefore, authorTargetBefore, authorPendingBefore, _ := staged.authorCache.Snapshot()
	reviewerStateBefore, reviewerTargetBefore, reviewerPendingBefore, _ := staged.reviewerCache.Snapshot()
	before = backend.io()
	err = staged.Save(ctx)
	if !errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject, Field: "reviewer"}) || backend.io() != before {
		t.Fatalf("later pending validation = %%v, io=%%d want %%d", err, backend.io(), before)
	}
	authorStateAfter, authorTargetAfter, authorPendingAfter, _ := staged.authorCache.Snapshot()
	reviewerStateAfter, reviewerTargetAfter, reviewerPendingAfter, _ := staged.reviewerCache.Snapshot()
	if staged.blogPostModel.AuthorID != 0 || staged.authorScalarPresent || staged.object != objectBefore ||
		authorStateAfter != authorStateBefore || authorTargetAfter != authorTargetBefore || authorPendingAfter != authorPendingBefore ||
		reviewerStateAfter != reviewerStateBefore || reviewerTargetAfter != reviewerTargetBefore || reviewerPendingAfter != reviewerPendingBefore {
		t.Fatalf("failed preflight partially published author: raw=%%d scalar=%%v", staged.blogPostModel.AuthorID, staged.authorScalarPresent)
	}
}

func TestProjectRelationFacadeAllCacheTuplesPrecedeUnsavedTargets(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	unsavedAuthor, _ := models.AuthorsAuthor.New(authors.Author{Name: "unsaved author"})
	unsavedReviewer, _ := models.AuthorsAuthor.New(authors.Author{Name: "unsaved reviewer"})
	savedAuthor, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))

	assertStructuralFirst := func(label string, candidate *BlogPost) {
		t.Helper()
		modelBefore := (blog.PostDescriptor{}).CloneWriteModel(candidate.blogPostModel)
		_, primaryBefore := (blog.PostDescriptor{}).PrimaryKey(modelBefore)
		objectBefore := candidate.object
		authorCacheBefore := candidate.authorCache
		reviewerCacheBefore := candidate.reviewerCache
		authorScalarBefore := candidate.authorScalarPresent
		authorStateBefore, authorTargetBefore, authorPendingBefore, _ := candidate.authorCache.Snapshot()
		reviewerStateBefore, reviewerTargetBefore, reviewerPendingBefore, _ := candidate.reviewerCache.Snapshot()
		beforeIO := backend.io()
		err := candidate.Save(ctx)
		if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != beforeIO {
			t.Fatalf("%%s Save = %%v, io=%%d want structural invalid_plan/I/O %%d", label, err, backend.io(), beforeIO)
		}
		_, primaryAfter := (blog.PostDescriptor{}).PrimaryKey(candidate.blogPostModel)
		authorStateAfter, authorTargetAfter, authorPendingAfter, _ := candidate.authorCache.Snapshot()
		reviewerStateAfter, reviewerTargetAfter, reviewerPendingAfter, _ := candidate.reviewerCache.Snapshot()
		if candidate.blogPostModel.ID != modelBefore.ID || candidate.blogPostModel.Title != modelBefore.Title ||
			candidate.blogPostModel.AuthorID != modelBefore.AuthorID ||
			(candidate.blogPostModel.ReviewerID == nil) != (modelBefore.ReviewerID == nil) ||
			(candidate.blogPostModel.ReviewerID != nil && *candidate.blogPostModel.ReviewerID != *modelBefore.ReviewerID) ||
			primaryAfter != primaryBefore || candidate.authorScalarPresent != authorScalarBefore ||
			candidate.object != objectBefore || candidate.authorCache != authorCacheBefore || candidate.reviewerCache != reviewerCacheBefore ||
			authorStateAfter != authorStateBefore || authorTargetAfter != authorTargetBefore || authorPendingAfter != authorPendingBefore ||
			reviewerStateAfter != reviewerStateBefore || reviewerTargetAfter != reviewerTargetBefore || reviewerPendingAfter != reviewerPendingBefore {
			t.Fatalf("%%s structural preflight failure partially published source", label)
		}
	}

	source, _ := models.BlogPost.New(blog.Post{Title: "earlier unsaved later corrupt"})
	earlierUnsaved, _ := source.WithAuthor(unsavedAuthor)
	earlierUnsaved.reviewerCache = nil
	assertStructuralFirst("earlier unsaved author, later corrupt reviewer", earlierUnsaved)

	selfCorruptTarget := &AuthorsAuthor{state: source.state}
	selfCorrupt, _ := source.WithAuthor(unsavedAuthor)
	if err := selfCorrupt.reviewerCache.Store(orm.RelationAssignedPresent, selfCorruptTarget, false); err != nil { t.Fatal(err) }
	assertStructuralFirst("earlier unsaved author, later corrupt reviewer self", selfCorrupt)

	otherModels, _ := Using(newRelationFacadeBackend())
	foreignReviewer, _ := otherModels.AuthorsAuthor.New(authors.NewAuthorWithID(2))
	originCorrupt, _ := source.WithAuthor(unsavedAuthor)
	if err := originCorrupt.reviewerCache.Store(orm.RelationAssignedPresent, foreignReviewer, false); err != nil { t.Fatal(err) }
	assertStructuralFirst("earlier unsaved author, later foreign reviewer origin", originCorrupt)

	reverseSource, _ := models.BlogPost.New(blog.Post{Title: "earlier corrupt later unsaved"})
	reverse, _ := reverseSource.WithAuthor(savedAuthor)
	reverse, _ = reverse.WithReviewer(unsavedReviewer)
	reverse.authorCache = nil
	assertStructuralFirst("earlier corrupt author, later unsaved reviewer", reverse)
}

func TestProjectRelationFacadeRebuildConstructionFailureDoesNotPublish(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	source, _ := models.BlogPost.New(blog.Post{Title: "rebuild failure"})
	author, _ := models.AuthorsAuthor.New(authors.Author{Name: "later key"})
	staged, _ := source.WithAuthor(author)
	if err := author.Save(ctx); err != nil { t.Fatal(err) }
	modelBefore := (blog.PostDescriptor{}).CloneWriteModel(staged.blogPostModel)
	_, primaryBefore := (blog.PostDescriptor{}).PrimaryKey(modelBefore)
	objectBefore := staged.object
	cacheBefore := staged.authorCache
	stateBefore, targetBefore, pendingBefore, _ := cacheBefore.Snapshot()
	scalarBefore := staged.authorScalarPresent
	staged.state.objects.BlogPost = BlogPostObjectFactory{}
	beforeIO := backend.io()
	err := staged.Save(ctx)
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != beforeIO {
		t.Fatalf("rebuild construction failure = %%v, io=%%d want %%d", err, backend.io(), beforeIO)
	}
	stateAfter, targetAfter, pendingAfter, _ := staged.authorCache.Snapshot()
	_, primaryAfter := (blog.PostDescriptor{}).PrimaryKey(staged.blogPostModel)
	if staged.blogPostModel.ID != modelBefore.ID || staged.blogPostModel.Title != modelBefore.Title || staged.blogPostModel.AuthorID != modelBefore.AuthorID ||
		primaryAfter != primaryBefore || staged.object != objectBefore || staged.authorCache != cacheBefore ||
		staged.authorScalarPresent != scalarBefore || stateAfter != stateBefore || targetAfter != targetBefore || pendingAfter != pendingBefore {
		t.Fatalf("rebuild construction failure partially published source")
	}
}

func TestProjectRelationFacadeMissingCacheFailsBeforeIO(t *testing.T) {
	ctx := context.Background()
	backend := newRelationFacadeBackend()
	models, _ := Using(backend)
	author, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	source, _ := models.BlogPost.New(blog.Post{Title: "corrupt cache"})
	source, _ = source.WithAuthor(author)
	assertCorrupt := func(label string, cache *orm.RelationCache[AuthorsAuthor]) {
		t.Helper()
		candidate, _ := source.relationFacadeDerived(source.blogPostModel)
		candidate.authorCache = cache
		before := backend.io()
		if _, err := candidate.Unwrap(); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != before {
			t.Fatalf("%%s Unwrap = %%v, io=%%d want %%d", label, err, backend.io(), before)
		}
		if err := candidate.Save(ctx); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != before {
			t.Fatalf("%%s Save = %%v, io=%%d want %%d", label, err, backend.io(), before)
		}
		if _, err := candidate.Author(ctx); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || backend.io() != before {
			t.Fatalf("%%s Author = %%v, io=%%d want %%d", label, err, backend.io(), before)
		}
	}
	assertCorrupt("missing cache", nil)
}
`, modulePath+"/authors", modulePath+"/blog"))
}

func generatedProjectRelationFacadeEagerCOWTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	authors %q
	blog %q
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type eagerRows struct { index int; empty, absent bool }

func (rows *eagerRows) Next() bool { return !rows.empty && rows.index == 0 }
func (rows *eagerRows) Scan(destinations ...any) error {
	rows.index++
	values := []any{int64(10), "Alpha", int64(1), int64(2), int64(2), "Bob"}
	for index, destination := range destinations {
		if rows.absent && index >= 3 { continue }
		switch typed := destination.(type) {
		case *int64: *typed = values[index].(int64)
		case *string: *typed = values[index].(string)
		case *sql.NullInt64: typed.Int64, typed.Valid = values[index].(int64), true
		case *sql.NullString: typed.String, typed.Valid = values[index].(string), true
		default: return errors.New("unsupported eager scan destination")
		}
	}
	return nil
}
func (*eagerRows) Err() error { return nil }
func (*eagerRows) Close() error { return nil }

type eagerBackend struct { queries int; plan query.Plan; empty, absent bool }
func (backend *eagerBackend) Query(_ context.Context, plan query.Plan) (db.Rows, error) { backend.queries++; backend.plan = plan; return &eagerRows{empty: backend.empty, absent: backend.absent}, nil }
func (*eagerBackend) Insert(context.Context, query.InsertPlan) (int64, error) { return 0, nil }
func (*eagerBackend) Update(context.Context, query.UpdatePlan) (int64, error) { return 0, nil }
func (*eagerBackend) Delete(context.Context, query.DeletePlan) (int64, error) { return 0, nil }

func TestProjectRelationFacadeEagerSelectedCacheHasIndependentCOWCell(t *testing.T) {
	ctx := context.Background()
	backend := &eagerBackend{}
	models, err := Using(backend)
	if err != nil { t.Fatal(err) }
	posts, err := models.BlogPost.SelectRelated(models.BlogPost.Related.Reviewer).All(ctx)
	if err != nil || len(posts) != 1 || backend.queries != 1 { t.Fatalf("eager All = %%d, %%v queries=%%d", len(posts), err, backend.queries) }
	loaded := posts[0]
	state, reviewer, pending, err := loaded.reviewerCache.Snapshot()
	if err != nil || state != orm.RelationAssignedPresent || reviewer == nil || pending { t.Fatalf("eager cache = %%d, %%p, %%v, %%v", state, reviewer, pending, err) }
	author, _ := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	derived, err := loaded.WithAuthor(author)
	if err != nil { t.Fatal(err) }
	before := backend.queries
	got, present, err := derived.Reviewer(ctx)
	if err != nil || !present || got != reviewer || backend.queries != before { t.Fatalf("derived eager reviewer = (%%p,%%v,%%v), queries=%%d", got, present, err, backend.queries) }
	if derived.reviewerCache == loaded.reviewerCache { t.Fatal("derived eager cache cell was shared") }
}
func TestProjectRelationFacadeEagerDerivationPreservesSourceAndCache(t *testing.T) {
	ctx := context.Background()
	backend := &eagerBackend{}
	models, err := Using(backend)
	if err != nil { t.Fatal(err) }
	source := models.BlogPost.SelectRelated(models.BlogPost.Related.Reviewer)
	if _, err := source.All(ctx); err != nil { t.Fatal(err) }
	derived, err := source.Distinct().Offset(1)
	if err != nil { t.Fatal(err) }
	derived, err = derived.Limit(2)
	if err != nil { t.Fatal(err) }
	if backend.queries != 1 { t.Fatal("query derivation performed I/O") }
	if _, err := derived.All(ctx); err != nil { t.Fatal(err) }
	offset, hasOffset := backend.plan.Offset()
	limit, hasLimit := backend.plan.Limit()
	projection, hasProjection := backend.plan.RelationProjection()
	if !hasOffset || offset != 1 || !hasLimit || limit != 2 || !backend.plan.Distinct() || !hasProjection || projection.Hop().Field() != "reviewer" {
		t.Fatalf("derived eager plan lost pagination/distinct/relation: %%#v", backend.plan)
	}
	if _, err := source.All(ctx); err != nil || backend.queries != 2 { t.Fatalf("source cache changed: %%v queries=%%d", err, backend.queries) }
	if _, err := derived.All(ctx); err != nil || backend.queries != 2 { t.Fatalf("derived cache changed: %%v queries=%%d", err, backend.queries) }
	if _, err := derived.Fresh().All(ctx); err != nil || backend.queries != 3 { t.Fatalf("fresh eager query did not reevaluate: %%v queries=%%d", err, backend.queries) }
	if _, err := source.Offset(-1); err == nil || backend.queries != 3 { t.Fatalf("invalid offset did not fail before I/O: %%v", err) }
}

func TestProjectRelationFacadeAndDynamicFirst(t *testing.T) {
	ctx := context.Background()
	for _, absent := range []bool{false, true} {
		backend := &eagerBackend{absent: absent}
		models, err := Using(backend)
		if err != nil { t.Fatal(err) }
		ordered := models.BlogPost.OrderBy(blog.PostFields.ID.Asc())
		eager := ordered.SelectRelated(models.BlogPost.Related.Reviewer)
		first, found, err := eager.First(ctx)
		if err != nil || !found || first.ID != 10 { t.Fatalf("First = %%v, %%v, %%v", first, found, err) }
		if limit, _ := backend.plan.Limit(); limit != 1 { t.Fatalf("First limit = %%d", limit) }
		_, present, err := first.Reviewer(ctx)
		if err != nil || present == absent || backend.queries != 1 { t.Fatalf("ready nullable relation = %%v, %%v, queries=%%d", present, err, backend.queries) }
		all, err := eager.All(ctx)
		if err != nil || len(all) != 1 || backend.queries != 2 { t.Fatalf("First populated All cache: %%v", err) }
		all[0].Title = "mutated"
		warm, found, err := eager.First(ctx)
		if err != nil || !found || warm.Title != "Alpha" || warm == all[0] || warm.reviewerCache == all[0].reviewerCache || backend.queries != 2 { t.Fatalf("warm First ownership/cache: %%v", err) }
		dynamic, err := models.BlogPost.state.objects.BlogPost.SelectRelated(ordered.query).ParseDynamic("reviewer")
		if err != nil { t.Fatal(err) }
		object, found, err := dynamic.First(ctx)
		if err != nil || !found || object == nil || backend.queries != 3 { t.Fatalf("dynamic First = %%v, %%v", found, err) }
		if _, _, err := models.BlogPost.SelectRelated(models.BlogPost.Related.Reviewer).First(ctx); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnorderedQuery}) || backend.queries != 3 { t.Fatalf("unordered First = %%v", err) }
		backend.empty = true
		if value, found, err := eager.Fresh().First(ctx); value != nil || found || err != nil { t.Fatalf("empty First = %%v, %%v, %%v", value, found, err) }
	}
}

`, modulePath+"/authors", modulePath+"/blog"))
}

func generatedRelationFacadeInvalidStateTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"
	"errors"
	"testing"

	authors %q
	blog %q
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type facadeBackend struct{ io int }

func (backend *facadeBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.io++
	return nil, errors.New("unexpected facade query")
}
func (backend *facadeBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	backend.io++
	return 0, errors.New("unexpected facade insert")
}
func (backend *facadeBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	backend.io++
	return 0, errors.New("unexpected facade update")
}
func (backend *facadeBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	backend.io++
	return 0, errors.New("unexpected facade delete")
}

var _ Backend = (*facadeBackend)(nil)
var _ Backend = (db.Session)(nil)

func assertFacadeError(t *testing.T, err error, category string) {
	t.Helper()
	if !errors.Is(err, &query.Error{Category: category, Code: query.CodeInvalidPlan}) {
		t.Fatalf("error = %%v, want %%s/invalid_plan", err, category)
	}
}

func TestGeneratedFacadeInvalidStates(t *testing.T) {
	_, err := Using(nil)
	assertFacadeError(t, err, query.CategoryBackend)
	var typedNil *facadeBackend
	_, err = Using(typedNil)
	assertFacadeError(t, err, query.CategoryBackend)

	backend := &facadeBackend{}
	models, err := Using(backend)
	if err != nil {
		t.Fatalf("Using() error = %%v", err)
	}
	if backend.io != 0 {
		t.Fatalf("Using() I/O = %%d, want 0", backend.io)
	}

	var zero Models
	_, err = zero.BlogPost.All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	var zeroEager BlogPostEagerQuery
	_, err = zeroEager.All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	var nilSelector BlogPostRelationSelector
	_, err = models.BlogPost.SelectRelated(nilSelector).All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	var typedNilSelector *blogPostRelationSelector
	_, err = models.BlogPost.SelectRelated(typedNilSelector).All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	zeroSelector := blogPostRelationSelector{}
	_, err = models.BlogPost.SelectRelated(zeroSelector).All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	corruptSelector := blogPostRelationSelector{state: models.BlogPost.state}
	_, err = models.BlogPost.SelectRelated(corruptSelector).All(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)

	var nilPost *BlogPost
	_, err = nilPost.Unwrap()
	assertFacadeError(t, err, query.CategoryQuery)
	_, err = new(BlogPost).Unwrap()
	assertFacadeError(t, err, query.CategoryQuery)

	reviewerID := int64(2)
	wrapped, err := models.BlogPost.state.wrapBlogPost(blog.Post{ID: 1, Title: "post", AuthorID: 1, ReviewerID: &reviewerID})
	if err != nil {
		t.Fatalf("wrapBlogPost() error = %%v", err)
	}
	copyValue := *wrapped
	_, err = (&copyValue).Unwrap()
	assertFacadeError(t, err, query.CategoryQuery)
	before := backend.io
	err = (&copyValue).Save(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)
	if backend.io != before { t.Fatalf("copied wrapper Save I/O = %%d, want %%d", backend.io, before) }
	err = nilPost.Save(context.Background())
	assertFacadeError(t, err, query.CategoryQuery)

	created, err := models.BlogPost.New(blog.Post{Title: "new", AuthorID: 1})
	if err != nil { t.Fatal(err) }
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	before = backend.io
	if err := created.Save(cancelled); !errors.Is(err, context.Canceled) || backend.io != before { t.Fatalf("cancelled Save = %%v, I/O=%%d want %%d", err, backend.io, before) }

	other, err := Using(backend)
	if err != nil { t.Fatal(err) }
	target, err := models.AuthorsAuthor.New(authors.NewAuthorWithID(1))
	if err != nil { t.Fatal(err) }
	otherSource, err := other.BlogPost.New(blog.Post{Title: "origin"})
	if err != nil { t.Fatal(err) }
	before = backend.io
	_, err = otherSource.WithAuthor(target)
	assertFacadeError(t, err, query.CategoryQuery)
	if backend.io != before { t.Fatalf("cross-origin assignment I/O = %%d, want %%d", backend.io, before) }
	_, err = otherSource.WithAuthor(nil)
	assertFacadeError(t, err, query.CategoryQuery)
	if backend.io != before { t.Fatalf("nil-target assignment I/O = %%d, want %%d", backend.io, before) }
	first, err := wrapped.Unwrap()
	if err != nil {
		t.Fatalf("first Unwrap() error = %%v", err)
	}
	*first.ReviewerID = 99
	second, err := wrapped.Unwrap()
	if err != nil || second.ReviewerID == nil || *second.ReviewerID != 2 {
		t.Fatalf("second Unwrap() = %%#v, err=%%v", second, err)
	}
	if backend.io != 0 {
		t.Fatalf("invalid facade boundaries performed %%d I/O calls", backend.io)
	}
}
`, modulePath+"/authors", modulePath+"/blog"))
}

func generatedRelationFacadeCrossSelectorTest(_ string) []byte {
	return []byte(`package project

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type crossSelectorBackend struct{ io int }

func (backend *crossSelectorBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.io++
	return nil, errors.New("unexpected query")
}

func (*crossSelectorBackend) Insert(context.Context, query.InsertPlan) (int64, error) { return 0, nil }
func (*crossSelectorBackend) Update(context.Context, query.UpdatePlan) (int64, error) { return 0, nil }
func (*crossSelectorBackend) Delete(context.Context, query.DeletePlan) (int64, error) { return 0, nil }

func TestCrossModelSelectorFailsBeforeIO(t *testing.T) {
	backend := &crossSelectorBackend{}
	models, err := Using(backend)
	if err != nil {
		t.Fatalf("Using() error = %v", err)
	}
	_, err = models.BlogPost.selectRelated(models.AuthorsAuthor.Related.Manager).All(context.Background())
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("cross selector error = %v", err)
	}
	if backend.io != 0 {
		t.Fatalf("cross selector I/O = %d, want 0", backend.io)
	}
}
`)
}

func generatedRelationFacadeTypedResultCompileTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"

	blog %q
	"github.com/progresshans/godj/orm"
)

type typedPostRow struct {
	ID int64
	Title string
}

type typedPostReport struct {
	Count int64
	LatestID orm.Optional[int64]
}

func compileTypedPostResultSurface(ctx context.Context, source BlogPostQuery) error {
	projection := orm.Project2(blog.PostFields.ID, blog.PostFields.Title, func(id int64, title string) typedPostRow {
		return typedPostRow{ID: id, Title: title}
	})
	if _, err := SelectBlogPostInto(ctx, source.Distinct(), projection); err != nil {
		return err
	}
	offset, err := source.Offset(1)
	if err != nil {
		return err
	}
	if _, err := offset.Count(ctx); err != nil {
		return err
	}
	aggregate := orm.Aggregate2(orm.CountRows[blog.Post](), orm.Max(blog.PostFields.ID), func(count int64, latestID orm.Optional[int64]) typedPostReport {
		return typedPostReport{Count: count, LatestID: latestID}
	})
	_, err = AggregateBlogPostInto(ctx, source, aggregate)
	return err
}
`, modulePath+"/blog"))
}

func generatedRelationFacadeCrossModelResultCompileFailure(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"

	authors %q
	"github.com/progresshans/godj/orm"
)

func crossModelTypedResultMustNotCompile(ctx context.Context, source BlogPostQuery) {
	projection := orm.Project1(authors.AuthorFields.ID, func(id int64) int64 { return id })
	_, _ = SelectBlogPostInto(ctx, source, projection)
	aggregate := orm.Aggregate1(orm.CountRows[authors.Author](), func(count int64) int64 { return count })
	_, _ = AggregateBlogPostInto(ctx, source, aggregate)
}
`, modulePath+"/authors"))
}
