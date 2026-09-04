package codegen_test

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/examples/article/modeldef"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratorVersionTracksCloneModelABI(t *testing.T) {
	t.Parallel()

	const want = "godj-codegen-current-v1"
	if codegen.GeneratorVersion != want {
		t.Fatalf("GeneratorVersion = %q, want %q", codegen.GeneratorVersion, want)
	}
}

func TestCurrentGeneratorPublishesCompleteRelationModelSurface(t *testing.T) {
	t.Parallel()

	_, blog := relationGenerationSchemas()
	first, err := codegen.Generate("models", blog)
	if err != nil {
		t.Fatalf("Generate() relation error = %v", err)
	}
	second, err := codegen.Generate("models", blog)
	if err != nil {
		t.Fatalf("Generate() relation second error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("relation main generation is not byte deterministic")
	}

	for _, fragment := range [][]byte{
		[]byte(`const GoDjGeneratorVersion = "godj-codegen-current-v1"`),
		[]byte("const GoDjSchemaSHA256 ="),
		[]byte("type Post struct"),
		[]byte("ID                    int64"),
		[]byte("AuthorID              int64"),
		[]byte("ReviewerID            *int64"),
		[]byte("godjPrimaryKeyPresent bool"),
		[]byte("var _ orm.ModelDescriptor[Post]"),
		[]byte("var _ orm.WriteDescriptor[Post]"),
		[]byte("func (PostDescriptor) Scan(row db.Row) (Post, error)"),
		[]byte("var scanReviewerID sql.NullInt64"),
		[]byte("func (PostDescriptor) CloneModel(value Post) Post"),
		[]byte("func (PostDescriptor) WriteFieldValue(value Post, field ir.Field)"),
		[]byte("Relation: &ir.ForeignKeyRelation{"),
		[]byte("var PostObjects = orm.NewManager[Post](PostDescriptor{})"),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("relation main source does not contain %q:\n%s", fragment, first)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte("GoDjRelationQueryGeneratorVersion"),
		[]byte("GoDjRelationQuerySchemaSHA256"),
	} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("relation main source contains forbidden fragment %q:\n%s", forbidden, first)
		}
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), "generated.go", first, 0)
	if err != nil {
		t.Fatalf("parse relation main: %v", err)
	}
	exported := make([]string, 0)
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				switch specification := specification.(type) {
				case *ast.ValueSpec:
					for _, name := range specification.Names {
						if name.IsExported() {
							exported = append(exported, name.Name)
						}
					}
				case *ast.TypeSpec:
					if specification.Name.IsExported() {
						exported = append(exported, specification.Name.Name)
					}
				}
			}
		case *ast.FuncDecl:
			if declaration.Name.IsExported() {
				exported = append(exported, declaration.Name.Name)
			}
		}
	}
	slices.Sort(exported)
	wantExported := []string{
		"BuildCreate",
		"BuildPatch",
		"ClearPrimaryKey",
		"CloneModel",
		"CloneWriteModel",
		"GoDjGeneratorVersion",
		"GoDjSchemaSHA256",
		"Metadata",
		"NewPostCreate",
		"NewPostWithID",
		"Post",
		"PostCreate",
		"PostDescriptor",
		"PostFields",
		"PostFieldSet",
		"PostForceInsert",
		"PostForceUpdate",
		"PostObjects",
		"PostPatch",
		"PostUpdateFieldNames",
		"PostUpdateFields",
		"PrimaryKey",
		"Scan",
		"SetPrimaryKey",
		"WithAuthorID",
		"WithAuthorID",
		"WithReviewerID",
		"WithReviewerID",
		"WithReviewerIDNull",
		"WithReviewerIDNull",
		"WriteFieldValue",
	}
	slices.Sort(wantExported)
	if !slices.Equal(exported, wantExported) {
		t.Fatalf("relation main exported declarations = %v, want %v", exported, wantExported)
	}
}

func TestGenerateRelationMainSnapshotsInputAndRejectsFixedNameCollision(t *testing.T) {
	t.Parallel()

	_, blog := relationGenerationSchemas()
	before, err := codegen.Generate("models", blog)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	blog.Models[0].Fields[1].Relation.Target.AppLabel = "mutated"
	after, err := codegen.Generate("models", func() ir.Schema {
		_, fresh := relationGenerationSchemas()
		return fresh
	}())
	if err != nil {
		t.Fatalf("Generate() fresh error = %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("post-generation caller mutation changed prior candidate bytes")
	}

	collision := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "collision",
		Models:        []ir.Model{{Name: "record", GoName: "GoDjSchemaSHA256"}},
	}
	if _, err := codegen.Generate("models", collision); err == nil {
		t.Fatal("Generate() accepted relation model colliding with provenance symbol")
	}
}

func TestGenerateIsDeterministicAndContainsProvenance(t *testing.T) {
	t.Parallel()

	irSchema, err := modeldef.Schema()
	if err != nil {
		t.Fatalf("Schema() error = %v", err)
	}
	first, err := codegen.Generate("models", irSchema)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	second, err := codegen.Generate("models", irSchema)
	if err != nil {
		t.Fatalf("Generate() second error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("generation is not byte deterministic")
	}
	for _, fragment := range [][]byte{
		[]byte("Code generated by GoDj"),
		[]byte(codegen.GeneratorVersion),
		[]byte(`const GoDjSchemaSHA256 = "3e6ec104d26c21665690e9d4a20f547ae2f7212b2eb35f5e741d38a85274647d"`),
		[]byte("type Article struct"),
		[]byte("var _ orm.ModelDescriptor[Article]"),
		[]byte("var _ orm.WriteDescriptor[Article]"),
		[]byte("CloneModel(value Article) Article"),
		[]byte("CloneWriteModel(value Article) Article"),
		[]byte("return descriptor.CloneModel(value)"),
		[]byte("func NewArticleWithID(key int64) Article"),
		[]byte("return Article{ID: key, godjPrimaryKeyPresent: true}"),
		[]byte("func ArticleUpdateFields(fields ...orm.WritableField[Article]) orm.SaveOption[Article]"),
		[]byte("func ArticleUpdateFieldNames(names ...string) orm.SaveOption[Article]"),
		[]byte("func ArticleForceInsert() orm.SaveOption[Article]"),
		[]byte("func ArticleForceUpdate() orm.SaveOption[Article]"),
		[]byte("type ArticleCreate struct"),
		[]byte("type ArticlePatch struct"),
		[]byte("WithSummaryNull"),
		[]byte("BuildCreate() orm.Mutation[Article]"),
		[]byte("Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: false}"),
		[]byte("*string"),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("generated source does not contain %q", fragment)
		}
	}
	if bytes.Contains(first, []byte(") Save(")) {
		t.Fatal("generated source unexpectedly contains an instance Save method")
	}
}

func TestGeneratedCloneModelDeepClonesNullableFields(t *testing.T) {
	t.Parallel()

	irSchema, err := modeldef.Schema()
	if err != nil {
		t.Fatalf("Schema() error = %v", err)
	}
	source, err := codegen.Generate("models", irSchema)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	for _, fragment := range [][]byte{
		[]byte("func (ArticleDescriptor) CloneModel(value Article) Article"),
		[]byte("clonedSummary := *value.Summary"),
		[]byte("clone.Summary = &clonedSummary"),
		[]byte("func (descriptor ArticleDescriptor) CloneWriteModel(value Article) Article"),
		[]byte("return descriptor.CloneModel(value)"),
	} {
		if !bytes.Contains(source, fragment) {
			t.Fatalf("generated source does not contain %q", fragment)
		}
	}
}

func TestCommittedDescriptorCloneModelIsolatesNullablePointers(t *testing.T) {
	t.Parallel()

	summary := "source"
	source := models.Article{Title: "article", Summary: &summary}
	descriptor := models.ArticleDescriptor{}

	clone := descriptor.CloneModel(source)
	if clone.Summary == source.Summary {
		t.Fatal("CloneModel reused nullable pointer")
	}
	*clone.Summary = "clone"
	if got, want := *source.Summary, "source"; got != want {
		t.Fatalf("source Summary = %q after clone mutation, want %q", got, want)
	}

	writeClone := descriptor.CloneWriteModel(source)
	if writeClone.Summary == source.Summary {
		t.Fatal("CloneWriteModel reused nullable pointer")
	}
	*writeClone.Summary = "write clone"
	if got, want := *source.Summary, "source"; got != want {
		t.Fatalf("source Summary = %q after write clone mutation, want %q", got, want)
	}
}

func TestGeneratedArticleMatchesCommittedGolden(t *testing.T) {
	t.Parallel()

	spec, err := modeldef.ProjectSpec(context.Background())
	if err != nil {
		t.Fatalf("ProjectSpec() error = %v", err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	var want []byte
	for _, file := range bundle.Files() {
		if file.Path == "models/zz_godj_generated.go" {
			want = file.Source()
			break
		}
	}
	if len(want) == 0 {
		t.Fatal("GenerateProject() omitted models/zz_godj_generated.go")
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), ".."))
	path := filepath.Join(root, "examples", "article", "models", "zz_godj_generated.go")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed generated source: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("committed generated Article differs from codegen output; run go run ./cmd/godj generate --project examples/article/godj.toml")
	}
}

func TestGenerateDerivesExplicitKeyConstructorFromPrimaryKeyGoName(t *testing.T) {
	t.Parallel()

	irSchema, err := schema.Build(schema.Definition{
		AppLabel: "custom_key",
		Models: []schema.Model{{
			Name:   "record",
			GoName: "Record",
			Fields: []schema.Field{
				schema.AutoField("record_key", "RecordKey"),
				schema.CharField("title", "Title", 20),
			},
		}},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	source, err := codegen.Generate("models", irSchema)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	for _, fragment := range [][]byte{
		[]byte("func NewRecordWithRecordKey(key int64) Record"),
		[]byte("return Record{RecordKey: key, godjPrimaryKeyPresent: true}"),
	} {
		if !bytes.Contains(source, fragment) {
			t.Fatalf("generated source does not contain %q", fragment)
		}
	}
}

func TestWriteFileCreatesFirstOutput(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "generated.go")
	source := []byte("package fixture\n\nconst Generated = true\n")
	verified := false
	options := codegen.WriteOptions{Verify: func(_ context.Context, candidate string) error {
		verified = true
		got, err := os.ReadFile(candidate)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, source) {
			return errors.New("candidate bytes differ")
		}
		return nil
	}}
	if err := codegen.WriteFile(context.Background(), target, source, options); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if !verified {
		t.Fatal("candidate verifier was not called")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read generated output: %v", err)
	}
	if !bytes.Equal(got, source) {
		t.Fatalf("generated output = %q, want %q", got, source)
	}
}

func TestWriteFilePreservesLastGoodOnInvalidCandidate(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	target := filepath.Join(directory, "generated.go")
	lastGood := []byte("package fixture\n")
	if err := os.WriteFile(target, lastGood, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := codegen.WriteFile(context.Background(), target, []byte("package"), codegen.WriteOptions{}); err == nil {
		t.Fatal("WriteFile() accepted malformed source")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read last-good output: %v", err)
	}
	if !bytes.Equal(got, lastGood) {
		t.Fatalf("last-good output changed: %q", got)
	}
}

func TestWriteFileCheckDetectsDrift(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	target := filepath.Join(directory, "generated.go")
	if err := os.WriteFile(target, []byte("package fixture\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	err := codegen.WriteFile(context.Background(), target, []byte("package fixture\n\nconst Changed = true\n"), codegen.WriteOptions{Check: true})
	if !errors.Is(err, codegen.ErrDrift) {
		t.Fatalf("error = %v, want ErrDrift", err)
	}
}

func TestWriteFilePreservesLastGoodWhenCandidateVerificationFails(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	target := filepath.Join(directory, "generated.go")
	lastGood := []byte("package fixture\n")
	if err := os.WriteFile(target, lastGood, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	verificationFailure := errors.New("candidate does not compile with target")
	err := codegen.WriteFile(
		context.Background(),
		target,
		[]byte("package fixture\n\nconst Candidate = true\n"),
		codegen.WriteOptions{Verify: func(context.Context, string) error { return verificationFailure }},
	)
	if !errors.Is(err, verificationFailure) {
		t.Fatalf("WriteFile() error = %v, want verification failure", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read last-good output: %v", err)
	}
	if !bytes.Equal(got, lastGood) {
		t.Fatalf("last-good output changed: %q", got)
	}
}

func TestGenerateRejectsInvalidSchemaBeforeWrite(t *testing.T) {
	t.Parallel()

	_, err := schema.Build(schema.Definition{AppLabel: "bad-label"})
	if err == nil {
		t.Fatal("Build() accepted invalid schema")
	}
}

func TestGenerateRejectsBlankPackageIdentifier(t *testing.T) {
	t.Parallel()

	irSchema, err := schema.Build(schema.Definition{
		AppLabel: "valid",
		Models: []schema.Model{{
			Name:   "record",
			GoName: "Record",
			Fields: []schema.Field{schema.CharField("title", "Title", 20)},
		}},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := codegen.Generate("_", irSchema); err == nil {
		t.Fatal("Generate() accepted blank package identifier")
	}
}

func TestGenerateRejectsDerivedWriteNameCollisions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields []schema.Field
	}{
		{
			name: "nullable null method",
			fields: []schema.Field{
				schema.CharField("foo", "Foo", 20, schema.Nullable()),
				schema.CharField("foo_null", "FooNull", 20),
			},
		},
		{
			name: "private keyword storage",
			fields: []schema.Field{
				schema.CharField("type", "Type", 20),
				schema.CharField("type_value", "TypeValue", 20),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			irSchema, err := schema.Build(schema.Definition{
				AppLabel: "collision",
				Models:   []schema.Model{{Name: "record", GoName: "Record", Fields: test.fields}},
			})
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if _, err := codegen.Generate("models", irSchema); err == nil {
				t.Fatal("Generate() accepted derived write name collision")
			}
		})
	}
}

func TestGenerateRejectsFixedPackageSymbolCollisions(t *testing.T) {
	t.Parallel()

	for _, modelName := range []string{"GoDjGeneratorVersion", "GoDjSchemaSHA256"} {
		t.Run(modelName, func(t *testing.T) {
			irSchema, err := schema.Build(schema.Definition{
				AppLabel: "collision",
				Models: []schema.Model{{
					Name:   "record",
					GoName: modelName,
					Fields: []schema.Field{schema.CharField("title", "Title", 20)},
				}},
			})
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if _, err := codegen.Generate("models", irSchema); err == nil {
				t.Fatalf("Generate() accepted model name %s that collides with a fixed package symbol", modelName)
			}
		})
	}
}

func TestGenerateRejectsSaveBindingSymbolCollisions(t *testing.T) {
	t.Parallel()

	for _, modelName := range []string{
		"ArticleUpdateFields",
		"ArticleUpdateFieldNames",
		"ArticleForceInsert",
		"ArticleForceUpdate",
		"NewArticleWithID",
	} {
		t.Run(modelName, func(t *testing.T) {
			irSchema, err := schema.Build(schema.Definition{
				AppLabel: "collision",
				Models: []schema.Model{
					{Name: "article", GoName: "Article", Fields: []schema.Field{schema.CharField("title", "Title", 20)}},
					{Name: "other", GoName: modelName, Fields: []schema.Field{schema.CharField("title", "Title", 20)}},
				},
			})
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if _, err := codegen.Generate("models", irSchema); err == nil {
				t.Fatalf("Generate() accepted model name %s that collides with a Save binding", modelName)
			}
		})
	}
}

func relationGenerationSchemas() (ir.Schema, ir.Schema) {
	authors := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "authors",
		Models: []ir.Model{{
			Name:   "author",
			GoName: "Author",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 100},
			},
		}},
	}
	blog := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:   "post",
			GoName: "Post",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name:   "author",
					GoName: "AuthorID",
					Kind:   ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "posts"},
						OnDelete:    ir.DeleteProtect,
					},
				},
				{
					Name:     "reviewer",
					GoName:   "ReviewerID",
					Kind:     ir.FieldForeignKey,
					Nullable: true,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "reviewed_posts"},
						OnDelete:    ir.DeleteSetNull,
					},
				},
			},
		}},
	}
	return authors, blog
}
