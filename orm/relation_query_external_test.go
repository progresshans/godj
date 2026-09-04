package orm_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type relationQueryAuthor struct {
	ID   int64
	Name string
}

type relationQueryPost struct {
	ID         int64
	AuthorID   int64
	ReviewerID *int64
}

type relationQueryDescriptor[M any] struct {
	metadata ir.Model
}

func (d *relationQueryDescriptor[M]) Metadata() ir.Model { return d.metadata.Clone() }
func (d *relationQueryDescriptor[M]) Scan(db.Row) (M, error) {
	var zero M
	return zero, nil
}
func (d *relationQueryDescriptor[M]) CloneModel(value M) M { return value }

type relationQueryFixture struct {
	binding          orm.ProjectBinding
	authorDescriptor *relationQueryDescriptor[relationQueryAuthor]
	postDescriptor   *relationQueryDescriptor[relationQueryPost]
	authorModel      orm.BoundModel[relationQueryAuthor]
	postModel        orm.BoundModel[relationQueryPost]
	author           orm.ForwardRelation[relationQueryPost, relationQueryAuthor]
	authorID         orm.RelatedIntegerField[relationQueryPost]
	authorName       orm.RelatedStringField[relationQueryPost]
}

func TestBoundForwardTypedFieldsBuildCanonicalImmutablePlans(t *testing.T) {
	t.Parallel()

	fixture := newRelationQueryFixture(t)
	typedName := orm.NewManager[relationQueryPost](fixture.postDescriptor).
		Using(nil).
		Filter(fixture.authorName.Exact("Ada")).
		Plan()
	typedBoth := orm.NewManager[relationQueryPost](fixture.postDescriptor).
		Using(nil).
		Filter(fixture.authorName.Exact("Ada"), fixture.authorID.Exact(1)).
		Plan()

	nameConditions := typedName.Conditions()
	if got, want := len(nameConditions), 1; got != want {
		t.Fatalf("name condition count = %d, want %d", got, want)
	}
	path, ok := nameConditions[0].RelationPath()
	if !ok {
		t.Fatal("typed related condition has no relation path")
	}
	hops := path.Hops()
	if got, want := len(hops), 1; got != want {
		t.Fatalf("typed path hop count = %d, want %d", got, want)
	}
	hop := hops[0]
	if hop.Source() != (ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}) ||
		hop.SourceTable() != "blog_post" || hop.Field() != "author" || hop.SourceColumn() != "author_id" ||
		hop.Target() != (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) ||
		hop.TargetTable() != "authors_author" || hop.TargetPrimaryKeyColumn() != "id" ||
		hop.Direction() != query.RelationForward || hop.Cardinality() != ir.RelationManyToOne || hop.Nullable() {
		t.Fatalf("typed relation hop = %#v", hop)
	}
	if path.Terminal().Name() != "name" || path.Terminal().Kind() != query.FieldString {
		t.Fatalf("typed terminal = %#v, want author.name string", path.Terminal())
	}

	bothConditions := typedBoth.Conditions()
	if got, want := len(bothConditions), 2; got != want {
		t.Fatalf("two-predicate condition count = %d, want %d", got, want)
	}
	left, leftOK := bothConditions[0].RelationPath()
	right, rightOK := bothConditions[1].RelationPath()
	if !leftOK || !rightOK || !left.Hops()[0].Equal(right.Hops()[0]) {
		t.Fatal("typed predicates did not share the same canonical relation edge")
	}
	if right.Terminal().Name() != "id" || right.Terminal().Kind() != query.FieldInteger {
		t.Fatalf("integer terminal = %#v, want author.id integer", right.Terminal())
	}

	// ProjectBinding.Model and bound paths retain no caller-visible aliases.
	identity := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	model, ok := fixture.binding.Model(identity)
	if !ok {
		t.Fatal("ProjectBinding.Model() did not find blog.post")
	}
	model.Fields[1].Column = "mutated"
	model.Fields[1].Relation.Target.AppLabel = "mutated"
	again, ok := fixture.binding.Model(identity)
	if !ok || again.Fields[1].Column != "author_id" || again.Fields[1].Relation.Target.AppLabel != "authors" {
		t.Fatalf("ProjectBinding.Model() retained caller mutation: %#v", again)
	}

	const readers = 24
	const iterations = 100
	var wait sync.WaitGroup
	wait.Add(readers)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				candidate := orm.NewManager[relationQueryPost](fixture.postDescriptor).
					Using(nil).
					Filter(fixture.authorName.Exact("Ada")).
					Plan()
				if !candidate.Equal(typedName) {
					t.Errorf("concurrent typed plan changed")
					return
				}
				if got, ok := fixture.binding.Model(identity); !ok || got.DBTable != "blog_post" {
					t.Errorf("concurrent model snapshot read changed")
					return
				}
			}
		}()
	}
	wait.Wait()
}

func TestRelationBindingFailuresAreStructuredAndPublishZeroValues(t *testing.T) {
	t.Parallel()

	authors, blog := relationSchemas()
	binding, err := orm.BindProject(authors, blog)
	if err != nil {
		t.Fatalf("BindProject() error = %v", err)
	}
	authorIdentity := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	postIdentity := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	authorMetadata, _ := binding.Model(authorIdentity)
	postMetadata, _ := binding.Model(postIdentity)
	authorDescriptor := &relationQueryDescriptor[relationQueryAuthor]{metadata: authorMetadata}
	postDescriptor := &relationQueryDescriptor[relationQueryPost]{metadata: postMetadata}

	var typedNil *relationQueryDescriptor[relationQueryPost]
	_, err = orm.BindModel(binding, postIdentity, typedNil)
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	_, err = orm.BindModel(orm.ProjectBinding{}, postIdentity, postDescriptor)
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	_, err = orm.BindModel(binding, postIdentity, &relationQueryDescriptor[relationQueryPost]{metadata: authorMetadata})
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
	_, err = orm.BindModel(binding, ir.ModelIdentity{AppLabel: "missing", ModelName: "post"}, postDescriptor)
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	postModel, err := orm.BindModel(binding, postIdentity, postDescriptor)
	if err != nil {
		t.Fatalf("BindModel(post) error = %v", err)
	}
	authorModel, err := orm.BindModel(binding, authorIdentity, authorDescriptor)
	if err != nil {
		t.Fatalf("BindModel(author) error = %v", err)
	}
	_, err = orm.BindForward(postModel, "missing", authorModel)
	assertRelationQueryError(t, err, query.CategoryField, query.CodeUnknownRelation)
	_, err = orm.BindForward(postModel, "reviewer", authorModel)
	assertRelationQueryError(t, err, query.CategoryField, query.CodeUnsupportedLookup)
	_, err = orm.BindForward(postModel, "author", postModel)
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	otherBinding, err := orm.BindProject(authors, blog)
	if err != nil {
		t.Fatalf("second BindProject() error = %v", err)
	}
	otherAuthor, err := orm.BindModel(otherBinding, authorIdentity, authorDescriptor)
	if err != nil {
		t.Fatalf("second BindModel(author) error = %v", err)
	}
	_, err = orm.BindForward(postModel, "author", otherAuthor)
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	relation, err := orm.BindForward(postModel, "author", authorModel)
	if err != nil {
		t.Fatalf("BindForward(author) error = %v", err)
	}
	_, err = relation.String(orm.NewStringField[relationQueryAuthor](ir.Field{
		Name: "missing", GoName: "Missing", Column: "missing", Kind: ir.FieldChar, MaxLength: 10,
	}))
	assertRelationQueryError(t, err, query.CategoryField, query.CodeUnknownRelatedField)
	var zeroRelation orm.ForwardRelation[relationQueryPost, relationQueryAuthor]
	_, err = zeroRelation.Integer(orm.NewIntegerField[relationQueryAuthor](authorMetadata.Fields[0]))
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)

	var zeroRelated orm.RelatedStringField[relationQueryPost]
	configured := orm.NewManager[relationQueryPost](postDescriptor).Using(nil).Filter(zeroRelated.Exact("Ada"))
	_, err = configured.All(context.Background())
	assertRelationQueryError(t, err, query.CategoryQuery, query.CodeInvalidPlan)
}

func newRelationQueryFixture(t *testing.T) relationQueryFixture {
	t.Helper()
	authors, blog := relationSchemas()
	binding, err := orm.BindProject(authors, blog)
	if err != nil {
		t.Fatalf("BindProject() error = %v", err)
	}
	authorIdentity := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	postIdentity := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	authorMetadata, ok := binding.Model(authorIdentity)
	if !ok {
		t.Fatal("authors.author missing from binding")
	}
	postMetadata, ok := binding.Model(postIdentity)
	if !ok {
		t.Fatal("blog.post missing from binding")
	}
	authorDescriptor := &relationQueryDescriptor[relationQueryAuthor]{metadata: authorMetadata}
	postDescriptor := &relationQueryDescriptor[relationQueryPost]{metadata: postMetadata}
	authorModel, err := orm.BindModel(binding, authorIdentity, authorDescriptor)
	if err != nil {
		t.Fatalf("BindModel(author) error = %v", err)
	}
	postModel, err := orm.BindModel(binding, postIdentity, postDescriptor)
	if err != nil {
		t.Fatalf("BindModel(post) error = %v", err)
	}
	author, err := orm.BindForward(postModel, "author", authorModel)
	if err != nil {
		t.Fatalf("BindForward(author) error = %v", err)
	}
	authorID, err := author.Integer(orm.NewIntegerField[relationQueryAuthor](authorMetadata.Fields[0]))
	if err != nil {
		t.Fatalf("ForwardRelation.Integer(id) error = %v", err)
	}
	authorName, err := author.String(orm.NewStringField[relationQueryAuthor](authorMetadata.Fields[1]))
	if err != nil {
		t.Fatalf("ForwardRelation.String(name) error = %v", err)
	}
	return relationQueryFixture{
		binding:          binding,
		authorDescriptor: authorDescriptor,
		postDescriptor:   postDescriptor,
		authorModel:      authorModel,
		postModel:        postModel,
		author:           author,
		authorID:         authorID,
		authorName:       authorName,
	}
}

func assertRelationQueryError(t *testing.T, err error, category, code string) {
	t.Helper()
	var queryError *query.Error
	if !errors.As(err, &queryError) {
		t.Fatalf("error = %T %v, want *query.Error", err, err)
	}
	if queryError.Category != category || queryError.Code != code {
		t.Fatalf("error = %#v, want category=%q code=%q", queryError, category, code)
	}
}
