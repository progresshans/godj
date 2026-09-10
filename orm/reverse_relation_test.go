package orm

import (
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestBindReverseBuildsCanonicalRequiredAndNullablePaths(t *testing.T) {
	post, author, _, _ := bindRelationObjectTestFixture(t)
	posts, err := BindReverse(author, "posts", post)
	if err != nil {
		t.Fatalf("BindReverse(posts) error = %v", err)
	}
	title, err := posts.String(NewStringField[relationObjectTestPost](relationObjectTestPostField("title")))
	if err != nil {
		t.Fatalf("ReverseRelation.String(title) error = %v", err)
	}
	id, err := posts.Integer(NewIntegerField[relationObjectTestPost](relationObjectTestPostField("id")))
	if err != nil {
		t.Fatalf("ReverseRelation.Integer(id) error = %v", err)
	}
	plan := NewManager[relationObjectTestAuthor](relationObjectTestAuthorDescriptor{}).
		Using(nil).
		Filter(title.Exact("Alpha"), id.Exact(10)).
		Plan()
	conditions := plan.Conditions()
	if len(conditions) != 2 {
		t.Fatalf("condition count = %d, want 2", len(conditions))
	}
	left, leftOK := conditions[0].RelationPath()
	right, rightOK := conditions[1].RelationPath()
	if !leftOK || !rightOK || !left.Hops()[0].Equal(right.Hops()[0]) {
		t.Fatalf("typed reverse predicates do not share one edge: %#v %#v", left, right)
	}
	hop := left.Hops()[0]
	if hop.Source() != (ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}) ||
		hop.SourceTable() != "blog_post" || hop.Field() != "author" || hop.SourceColumn() != "author_id" ||
		hop.Target() != (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) ||
		hop.TargetTable() != "authors_author" || hop.TargetPrimaryKeyColumn() != "id" ||
		hop.ReverseName() != "posts" || hop.Direction() != query.RelationReverse ||
		hop.Cardinality() != ir.RelationOneToMany || hop.Nullable() {
		t.Fatalf("required reverse hop = %#v", hop)
	}
	if left.Terminal().Name() != "title" || left.Terminal().Kind() != query.FieldString ||
		right.Terminal().Name() != "id" || right.Terminal().Kind() != query.FieldInteger {
		t.Fatalf("reverse terminals = (%#v, %#v)", left.Terminal(), right.Terminal())
	}

	reviewed, err := BindReverse(author, "reviewed_posts", post)
	if err != nil {
		t.Fatalf("BindReverse(reviewed_posts) error = %v", err)
	}
	reviewedTitle, err := reviewed.String(NewStringField[relationObjectTestPost](relationObjectTestPostField("title")))
	if err != nil {
		t.Fatalf("nullable ReverseRelation.String(title) error = %v", err)
	}
	reviewedPlan := NewManager[relationObjectTestAuthor](relationObjectTestAuthorDescriptor{}).
		Using(nil).
		Filter(reviewedTitle.Exact("Gamma")).
		Plan()
	reviewedPath, ok := reviewedPlan.Conditions()[0].RelationPath()
	if !ok || !reviewedPath.Hops()[0].Nullable() || reviewedPath.Hops()[0].Field() != "reviewer" ||
		reviewedPath.Hops()[0].ReverseName() != "reviewed_posts" {
		t.Fatalf("nullable reverse path = (%#v, %v)", reviewedPath, ok)
	}
}

func TestReverseRelationTerminalValidationAndStateAreFailClosed(t *testing.T) {
	post, author, _, _ := bindRelationObjectTestFixture(t)
	posts, err := BindReverse(author, "posts", post)
	if err != nil {
		t.Fatalf("BindReverse(posts) error = %v", err)
	}
	_, err = posts.String(NewStringField[relationObjectTestPost](ir.Field{
		Name: "missing", GoName: "Missing", Column: "missing", Kind: ir.FieldChar, MaxLength: 10,
	}))
	assertRelationObjectQueryError(t, err, query.CategoryField, query.CodeUnknownRelatedField)
	_, err = posts.Integer(NewIntegerField[relationObjectTestPost](ir.Field{
		Name: "fake", GoName: "Fake", Column: "fake", Kind: ir.FieldAuto, PrimaryKey: true,
	}))
	assertRelationObjectQueryError(t, err, query.CategoryField, query.CodeUnknownRelatedField)

	var zero ReverseRelation[relationObjectTestAuthor, relationObjectTestPost]
	_, err = zero.String(NewStringField[relationObjectTestPost](relationObjectTestPostField("title")))
	assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	poisoned := posts
	poisoned.state.reverse.Name = "reviewed_posts"
	_, err = poisoned.String(NewStringField[relationObjectTestPost](relationObjectTestPostField("title")))
	assertRelationObjectQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
}
