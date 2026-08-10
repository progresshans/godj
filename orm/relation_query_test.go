package orm

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type relationObjectTestAuthor struct {
	ID   int64
	Name string
}

type relationObjectTestPost struct {
	ID         int64
	Title      string
	AuthorID   int64
	ReviewerID *int64
}

type relationObjectTestAuthorDescriptor struct{}

func (relationObjectTestAuthorDescriptor) Metadata() ir.Model {
	model, _ := relationObjectTestModels()
	return model
}

func (relationObjectTestAuthorDescriptor) Scan(row db.Row) (relationObjectTestAuthor, error) {
	var value relationObjectTestAuthor
	err := row.Scan(&value.ID, &value.Name)
	return value, err
}

func (relationObjectTestAuthorDescriptor) CloneModel(value relationObjectTestAuthor) relationObjectTestAuthor {
	return value
}

func (relationObjectTestAuthorDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestAuthor] {
	return relationObjectTestAuthorDescriptor{}
}

func (relationObjectTestAuthorDescriptor) BindRelationStorage(ir.Field) (RelationStorage[relationObjectTestAuthor], bool) {
	return nil, false
}

type relationObjectTestPostDescriptor struct{}

func (relationObjectTestPostDescriptor) Metadata() ir.Model {
	_, model := relationObjectTestModels()
	return model
}

func (relationObjectTestPostDescriptor) Scan(row db.Row) (relationObjectTestPost, error) {
	var value relationObjectTestPost
	err := row.Scan(&value.ID, &value.Title, &value.AuthorID, &value.ReviewerID)
	return value, err
}

func (relationObjectTestPostDescriptor) CloneModel(value relationObjectTestPost) relationObjectTestPost {
	clone := value
	if value.ReviewerID != nil {
		reviewerID := *value.ReviewerID
		clone.ReviewerID = &reviewerID
	}
	return clone
}

func (relationObjectTestPostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return relationObjectTestPostDescriptor{}
}

func (relationObjectTestPostDescriptor) BindRelationStorage(field ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	switch field.Name {
	case "author":
		return relationObjectTestAuthorStorage{}, true
	case "reviewer":
		return relationObjectTestReviewerStorage{}, true
	default:
		return nil, false
	}
}

type relationObjectTestAuthorStorage struct{}

func (relationObjectTestAuthorStorage) Field() ir.Field {
	return relationObjectTestPostField("author")
}

func (relationObjectTestAuthorStorage) Value(value relationObjectTestPost) (query.Value, bool) {
	return query.Integer(value.AuthorID), true
}

type relationObjectTestReviewerStorage struct{}

func (relationObjectTestReviewerStorage) Field() ir.Field {
	return relationObjectTestPostField("reviewer")
}

func (relationObjectTestReviewerStorage) Value(value relationObjectTestPost) (query.Value, bool) {
	if value.ReviewerID == nil {
		return query.Null(), true
	}
	return query.Integer(*value.ReviewerID), true
}

type relationObjectMutablePostDescriptor struct {
	snapshotCalls *atomic.Int64
	poisoned      *atomic.Bool
}

func (d *relationObjectMutablePostDescriptor) Metadata() ir.Model {
	return relationObjectTestPostDescriptor{}.Metadata()
}

func (d *relationObjectMutablePostDescriptor) Scan(row db.Row) (relationObjectTestPost, error) {
	return relationObjectTestPostDescriptor{}.Scan(row)
}

func (d *relationObjectMutablePostDescriptor) CloneModel(value relationObjectTestPost) relationObjectTestPost {
	if d.poisoned.Load() {
		value.AuthorID = 999
	}
	return value
}

func (d *relationObjectMutablePostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	d.snapshotCalls.Add(1)
	return relationObjectTestPostDescriptor{}
}

func (d *relationObjectMutablePostDescriptor) BindRelationStorage(ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	return relationObjectPoisonedStorage{}, true
}

type relationObjectPoisonedStorage struct{}

func (relationObjectPoisonedStorage) Field() ir.Field {
	return relationObjectTestPostField("author")
}

func (relationObjectPoisonedStorage) Value(relationObjectTestPost) (query.Value, bool) {
	return query.Integer(999), true
}

type relationObjectPointerSnapshotPostDescriptor struct{}

func (relationObjectPointerSnapshotPostDescriptor) Metadata() ir.Model {
	return relationObjectTestPostDescriptor{}.Metadata()
}

func (relationObjectPointerSnapshotPostDescriptor) Scan(row db.Row) (relationObjectTestPost, error) {
	return relationObjectTestPostDescriptor{}.Scan(row)
}

func (relationObjectPointerSnapshotPostDescriptor) CloneModel(value relationObjectTestPost) relationObjectTestPost {
	return relationObjectTestPostDescriptor{}.CloneModel(value)
}

func (relationObjectPointerSnapshotPostDescriptor) SnapshotRelationObjectDescriptor() RelationObjectDescriptor[relationObjectTestPost] {
	return &relationObjectPointerSnapshotPostDescriptor{}
}

func (relationObjectPointerSnapshotPostDescriptor) BindRelationStorage(ir.Field) (RelationStorage[relationObjectTestPost], bool) {
	return nil, false
}

type relationObjectPlainPostDescriptor struct{}

func (relationObjectPlainPostDescriptor) Metadata() ir.Model {
	return relationObjectTestPostDescriptor{}.Metadata()
}

func (relationObjectPlainPostDescriptor) Scan(row db.Row) (relationObjectTestPost, error) {
	return relationObjectTestPostDescriptor{}.Scan(row)
}

func (relationObjectPlainPostDescriptor) CloneModel(value relationObjectTestPost) relationObjectTestPost {
	return relationObjectTestPostDescriptor{}.CloneModel(value)
}

func TestBindModelSealsRelationObjectDescriptorExactlyOnce(t *testing.T) {
	binding := relationObjectTestBinding(t)
	var snapshotCalls atomic.Int64
	var poisoned atomic.Bool
	original := &relationObjectMutablePostDescriptor{snapshotCalls: &snapshotCalls, poisoned: &poisoned}

	bound, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		original,
	)
	if err != nil {
		t.Fatalf("BindModel() error = %v", err)
	}
	if got := snapshotCalls.Load(); got != 1 {
		t.Fatalf("SnapshotRelationObjectDescriptor() calls = %d, want 1", got)
	}
	poisoned.Store(true)
	if _, ok := bound.objectDescriptor.(relationObjectTestPostDescriptor); !ok {
		t.Fatalf("bound object descriptor = %T, want sealed value descriptor", bound.objectDescriptor)
	}
	cloned := bound.objectDescriptor.CloneModel(relationObjectTestPost{AuthorID: 1})
	if cloned.AuthorID != 1 {
		t.Fatalf("bound descriptor retained poisoned original: %#v", cloned)
	}
	author, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		relationObjectTestAuthorDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(author) error = %v", err)
	}
	relation, err := BindRequiredForwardObject(bound, "author", author)
	if err != nil {
		t.Fatalf("BindRequiredForwardObject() error = %v", err)
	}
	backend := &relationObjectAuthorBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
		identifier := relationObjectPlanIdentifier(t, plan)
		return &relationObjectAuthorRows{values: []relationObjectTestAuthor{{ID: identifier, Name: "Ada"}}}, nil
	}}
	loaded, err := relation.From(backend, relationObjectTestPost{AuthorID: 1})
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}
	value, ok, err := loaded.Get(context.Background())
	if err != nil || !ok || value.ID != 1 {
		t.Fatalf("sealed storage Get() = (%#v, %v, %v)", value, ok, err)
	}
}

func TestBindModelRejectsMutableRelationObjectSnapshotShape(t *testing.T) {
	binding := relationObjectTestBinding(t)
	bound, err := BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		relationObjectPointerSnapshotPostDescriptor{},
	)
	if err == nil || bound.snapshot != nil {
		t.Fatalf("BindModel() = (%#v, %v), want zero/error", bound, err)
	}
	assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
}

func relationObjectTestBinding(t *testing.T) ProjectBinding {
	t.Helper()
	authors, blog := relationObjectTestSchemas()
	binding, err := BindProject(authors, blog)
	if err != nil {
		t.Fatalf("BindProject() error = %v", err)
	}
	return binding
}

func relationObjectTestModels() (ir.Model, ir.Model) {
	authors, blog := relationObjectTestSchemas()
	authors, err := ir.Normalize(authors)
	if err != nil {
		panic(err)
	}
	blog, err = ir.Normalize(blog)
	if err != nil {
		panic(err)
	}
	return authors.Models[0].Clone(), blog.Models[0].Clone()
}

func relationObjectTestPostField(name string) ir.Field {
	_, post := relationObjectTestModels()
	field, ok := findField(post.Fields, name)
	if !ok {
		panic("missing relation object test field: " + name)
	}
	return field.Clone()
}

func relationObjectTestSchemas() (ir.Schema, ir.Schema) {
	authors := ir.Schema{
		FormatVersion: ir.FormatVersion,
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
		FormatVersion: ir.RelationFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:   "post",
			GoName: "Post",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 100},
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

func assertRelationObjectQueryError(t *testing.T, err error, category, code string) {
	t.Helper()
	queryError, ok := err.(*query.Error)
	if !ok || queryError.Category != category || queryError.Code != code {
		t.Fatalf("error = %T %v, want %s/%s", err, err, category, code)
	}
}
