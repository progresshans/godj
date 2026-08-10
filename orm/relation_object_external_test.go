package orm_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type publicAuthorObjectDescriptor struct{}

func (publicAuthorObjectDescriptor) Metadata() ir.Model {
	author, _ := publicRelationObjectModels()
	return author
}

func (publicAuthorObjectDescriptor) Scan(row db.Row) (relationQueryAuthor, error) {
	var value relationQueryAuthor
	err := row.Scan(&value.ID, &value.Name)
	return value, err
}

func (publicAuthorObjectDescriptor) CloneModel(value relationQueryAuthor) relationQueryAuthor {
	return value
}

func (publicAuthorObjectDescriptor) SnapshotRelationObjectDescriptor() orm.RelationObjectDescriptor[relationQueryAuthor] {
	return publicAuthorObjectDescriptor{}
}

func (publicAuthorObjectDescriptor) BindRelationStorage(ir.Field) (orm.RelationStorage[relationQueryAuthor], bool) {
	return nil, false
}

type publicPostObjectDescriptor struct{}

func (publicPostObjectDescriptor) Metadata() ir.Model {
	_, post := publicRelationObjectModels()
	return post
}

func (publicPostObjectDescriptor) Scan(row db.Row) (relationQueryPost, error) {
	var value relationQueryPost
	err := row.Scan(&value.ID, &value.AuthorID, &value.ReviewerID)
	return value, err
}

func (publicPostObjectDescriptor) CloneModel(value relationQueryPost) relationQueryPost {
	clone := value
	if value.ReviewerID != nil {
		reviewerID := *value.ReviewerID
		clone.ReviewerID = &reviewerID
	}
	return clone
}

func (publicPostObjectDescriptor) SnapshotRelationObjectDescriptor() orm.RelationObjectDescriptor[relationQueryPost] {
	return publicPostObjectDescriptor{}
}

func (publicPostObjectDescriptor) BindRelationStorage(field ir.Field) (orm.RelationStorage[relationQueryPost], bool) {
	switch field.Name {
	case "author":
		return publicAuthorStorage{}, true
	case "reviewer":
		return publicReviewerStorage{}, true
	default:
		return nil, false
	}
}

type publicAuthorStorage struct{}

func (publicAuthorStorage) Field() ir.Field { return publicPostField("author") }
func (publicAuthorStorage) Value(value relationQueryPost) (query.Value, bool) {
	return query.Integer(value.AuthorID), true
}

type publicReviewerStorage struct{}

func (publicReviewerStorage) Field() ir.Field { return publicPostField("reviewer") }
func (publicReviewerStorage) Value(value relationQueryPost) (query.Value, bool) {
	if value.ReviewerID == nil {
		return query.Null(), true
	}
	return query.Integer(*value.ReviewerID), true
}

func TestPublicRelationObjectSurfaceCompilesAndLoads(t *testing.T) {
	authors, blog := relationSchemas()
	binding, err := orm.BindProject(authors, blog)
	if err != nil {
		t.Fatalf("BindProject() error = %v", err)
	}
	author, err := orm.BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		publicAuthorObjectDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(author) error = %v", err)
	}
	post, err := orm.BindModel(
		binding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		publicPostObjectDescriptor{},
	)
	if err != nil {
		t.Fatalf("BindModel(post) error = %v", err)
	}
	required, err := orm.BindRequiredForwardObject(post, "author", author)
	if err != nil {
		t.Fatalf("BindRequiredForwardObject() error = %v", err)
	}
	nullable, err := orm.BindNullableForwardObject(post, "reviewer", author)
	if err != nil {
		t.Fatalf("BindNullableForwardObject() error = %v", err)
	}

	backend := publicAuthorBackend{}
	related, err := required.From(backend, relationQueryPost{ID: 10, AuthorID: 1})
	if err != nil {
		t.Fatalf("required From() error = %v", err)
	}
	value, ok, err := related.Get(context.Background())
	if err != nil || !ok || value != (relationQueryAuthor{ID: 1, Name: "Ada"}) {
		t.Fatalf("required Get() = (%#v, %v, %v)", value, ok, err)
	}
	if _, err := related.Fresh(); err != nil {
		t.Fatalf("Fresh() error = %v", err)
	}

	absent, err := nullable.From(backend, relationQueryPost{ID: 11, AuthorID: 1})
	if err != nil {
		t.Fatalf("nullable From() error = %v", err)
	}
	if _, ok, err := absent.Get(context.Background()); err != nil || ok {
		t.Fatalf("nullable Get() = (ok=%v, err=%v)", ok, err)
	}
	predicates, err := orm.ParseDynamicRelationObjects(post, nil, []orm.LookupInput{{Key: "reviewer__isnull", Value: true}})
	if err != nil || len(predicates) != 1 {
		t.Fatalf("ParseDynamicRelationObjects() = (%#v, %v)", predicates, err)
	}
	typed := orm.NewManager[relationQueryPost](publicPostObjectDescriptor{}).Using(nil).Filter(nullable.IsNull(true)).Plan()
	dynamic := orm.NewManager[relationQueryPost](publicPostObjectDescriptor{}).Using(nil).Filter(predicates...).Plan()
	if !typed.Equal(dynamic) {
		t.Fatalf("typed/dynamic plans differ")
	}
}

func publicRelationObjectModels() (ir.Model, ir.Model) {
	authors, blog := relationSchemas()
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

func publicPostField(name string) ir.Field {
	_, post := publicRelationObjectModels()
	for _, field := range post.Fields {
		if field.Name == name {
			return field.Clone()
		}
	}
	panic("missing public post field: " + name)
}

type publicAuthorBackend struct{}

func (publicAuthorBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	return &publicAuthorRows{}, nil
}

type publicAuthorRows struct {
	position int
}

func (rows *publicAuthorRows) Next() bool {
	if rows.position != 0 {
		return false
	}
	rows.position++
	return true
}

func (rows *publicAuthorRows) Scan(destinations ...any) error {
	if len(destinations) != 2 {
		return fmt.Errorf("destination count = %d", len(destinations))
	}
	id, idOK := destinations[0].(*int64)
	name, nameOK := destinations[1].(*string)
	if !idOK || !nameOK {
		return fmt.Errorf("destination types = (%T, %T)", destinations[0], destinations[1])
	}
	*id = 1
	*name = "Ada"
	return nil
}

func (*publicAuthorRows) Err() error   { return nil }
func (*publicAuthorRows) Close() error { return nil }
