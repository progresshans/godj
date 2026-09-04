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

func (publicAuthorObjectDescriptor) PrimaryKey(value relationQueryAuthor) (query.Value, bool) {
	return query.Integer(value.ID), value.ID != 0
}

var _ orm.PrimaryKeyObjectDescriptor[relationQueryAuthor] = publicAuthorObjectDescriptor{}

func TestPublicReverseObjectAndRelatedSetSurfaceCompilesAndEvaluates(t *testing.T) {
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
	reverse, err := orm.BindReverseObject(author, "posts", post)
	if err != nil {
		t.Fatalf("BindReverseObject() error = %v", err)
	}
	backend := &publicReversePostBackend{}
	set, err := reverse.From(backend, relationQueryAuthor{ID: 1, Name: "Ada"})
	if err != nil {
		t.Fatalf("ReverseObject.From() error = %v", err)
	}
	values, err := set.All(context.Background())
	if err != nil || len(values) != 2 || values[0].ID != 10 || values[1].ID != 11 {
		t.Fatalf("RelatedSet.All() = (%#v, %v)", values, err)
	}
	if backend.calls != 1 {
		t.Fatalf("backend calls = %d, want 1", backend.calls)
	}
	if _, err := set.All(context.Background()); err != nil || backend.calls != 1 {
		t.Fatalf("warm RelatedSet.All() = (calls=%d, err=%v)", backend.calls, err)
	}
	postMetadata, _ := binding.Model(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"})
	ordered, err := set.OrderBy(orm.NewIntegerField[relationQueryPost](postMetadata.Fields[0]).Desc())
	if err != nil {
		t.Fatalf("RelatedSet.OrderBy() error = %v", err)
	}
	if _, err := ordered.All(context.Background()); err != nil || backend.calls != 2 {
		t.Fatalf("ordered RelatedSet.All() = (calls=%d, err=%v)", backend.calls, err)
	}
	if fresh, err := set.Fresh(); err != nil {
		t.Fatalf("RelatedSet.Fresh() error = %v", err)
	} else if _, err := fresh.All(context.Background()); err != nil || backend.calls != 3 {
		t.Fatalf("fresh RelatedSet.All() = (calls=%d, err=%v)", backend.calls, err)
	}
}

type publicReversePostBackend struct {
	calls int
}

func (backend *publicReversePostBackend) Query(_ context.Context, plan query.Plan) (db.Rows, error) {
	backend.calls++
	conditions := plan.Conditions()
	if len(conditions) != 1 || conditions[0].Field().Name() != "author" || conditions[0].Lookup() != query.LookupExact {
		return nil, fmt.Errorf("conditions = %#v", conditions)
	}
	identifier, ok := conditions[0].Value().Integer()
	if !ok || identifier != 1 {
		return nil, fmt.Errorf("identifier = (%d, %v)", identifier, ok)
	}
	return &publicReversePostRows{values: []relationQueryPost{
		{ID: 10, AuthorID: 1},
		{ID: 11, AuthorID: 1},
	}}, nil
}

type publicReversePostRows struct {
	values   []relationQueryPost
	position int
}

func (rows *publicReversePostRows) Next() bool {
	if rows.position >= len(rows.values) {
		return false
	}
	rows.position++
	return true
}

func (rows *publicReversePostRows) Scan(destinations ...any) error {
	if len(destinations) != 3 {
		return fmt.Errorf("destination count = %d", len(destinations))
	}
	id, idOK := destinations[0].(*int64)
	authorID, authorOK := destinations[1].(*int64)
	reviewerID, reviewerOK := destinations[2].(**int64)
	if !idOK || !authorOK || !reviewerOK {
		return fmt.Errorf("destination types = (%T, %T, %T)", destinations[0], destinations[1], destinations[2])
	}
	value := rows.values[rows.position-1]
	*id = value.ID
	*authorID = value.AuthorID
	*reviewerID = value.ReviewerID
	return nil
}

func (*publicReversePostRows) Err() error   { return nil }
func (*publicReversePostRows) Close() error { return nil }
