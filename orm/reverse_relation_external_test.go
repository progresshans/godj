package orm_test

import (
	"context"
	"testing"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestPublicReverseRelationQuerySurfaceDoesNotRequireObjectCapability(t *testing.T) {
	t.Parallel()

	fixture := newRelationQueryFixture(t)
	postMetadata, ok := fixture.binding.Model(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"})
	if !ok {
		t.Fatal("blog.post missing from binding")
	}

	posts, err := orm.BindReverse(fixture.authorModel, "posts", fixture.postModel)
	if err != nil {
		t.Fatalf("BindReverse() with plain query descriptors error = %v", err)
	}
	postID, err := posts.Integer(orm.NewIntegerField[relationQueryPost](postMetadata.Fields[0]))
	if err != nil {
		t.Fatalf("ReverseRelation.Integer() error = %v", err)
	}
	plan := orm.NewManager[relationQueryAuthor](fixture.authorDescriptor).
		Using(nil).
		Filter(postID.Exact(10)).
		Plan()
	conditions := plan.Conditions()
	if len(conditions) != 1 || conditions[0].Lookup() != query.LookupExact {
		t.Fatalf("reverse conditions = %#v", conditions)
	}
	path, ok := conditions[0].RelationPath()
	if !ok || len(path.Hops()) != 1 || path.Hops()[0].Direction() != query.RelationReverse ||
		path.Hops()[0].ReverseName() != "posts" {
		t.Fatalf("public reverse path = (%#v, %v)", path, ok)
	}

	reviewed, err := orm.BindReverse(fixture.authorModel, "reviewed_posts", fixture.postModel)
	if err != nil {
		t.Fatalf("BindReverse(reviewed_posts) error = %v", err)
	}
	reviewedID, err := reviewed.Integer(orm.NewIntegerField[relationQueryPost](postMetadata.Fields[0]))
	if err != nil {
		t.Fatalf("nullable ReverseRelation.Integer() error = %v", err)
	}
	reviewedPlan := orm.NewManager[relationQueryAuthor](fixture.authorDescriptor).
		Using(nil).
		Filter(reviewedID.Exact(12)).
		Plan()
	reviewedPath, ok := reviewedPlan.Conditions()[0].RelationPath()
	if !ok || !reviewedPath.Hops()[0].Nullable() {
		t.Fatalf("nullable public reverse path = (%#v, %v)", reviewedPath, ok)
	}
}

func TestPublicReverseRelationBindingFailuresPublishOnlyZeroValues(t *testing.T) {
	t.Parallel()

	fixture := newRelationQueryFixture(t)
	_, err := orm.BindReverse(fixture.authorModel, "missing", fixture.postModel)
	assertRelationQueryError(t, err, query.CategoryField, query.CodeUnknownRelation)
	_, err = orm.BindReverse(fixture.authorModel, "posts", fixture.authorModel)
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	authors, blog := relationSchemas()
	otherBinding, err := orm.BindProject(authors, blog)
	if err != nil {
		t.Fatalf("second BindProject() error = %v", err)
	}
	postMetadata, _ := otherBinding.Model(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"})
	otherPost, err := orm.BindModel(
		otherBinding,
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		&relationQueryDescriptor[relationQueryPost]{metadata: postMetadata},
	)
	if err != nil {
		t.Fatalf("second BindModel(post) error = %v", err)
	}
	_, err = orm.BindReverse(fixture.authorModel, "posts", otherPost)
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	var zeroOwner orm.BoundModel[relationQueryAuthor]
	_, err = orm.BindReverse(zeroOwner, "posts", fixture.postModel)
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	var zeroRelation orm.ReverseRelation[relationQueryAuthor, relationQueryPost]
	postMetadata, _ = fixture.binding.Model(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"})
	_, err = zeroRelation.Integer(orm.NewIntegerField[relationQueryPost](postMetadata.Fields[0]))
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	_, err = zeroRelation.String(orm.NewStringField[relationQueryPost](ir.Field{
		Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 100,
	}))
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	var zeroField orm.RelatedIntegerField[relationQueryAuthor]
	_, err = orm.NewManager[relationQueryAuthor](fixture.authorDescriptor).
		Using(nil).
		Filter(zeroField.Exact(1)).
		All(context.Background())
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
}
