package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestMultipleRelationProjectionsCanonicalizeWithoutAliasing(t *testing.T) {
	root := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	person := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	authorKey := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	reviewerKey := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	makeProjection := func(source ir.ModelIdentity, table string, key query.FieldRef, columns []query.FieldRef) query.RelationProjection {
		p, err := query.NewForwardRelationProjection(source, table, key, person, "authors_author", id, columns)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	columns := []query.FieldRef{id, name}
	author := makeProjection(root, "blog_post", authorKey, columns)
	reviewer := makeProjection(root, "blog_post", reviewerKey, columns)
	base := query.NewPlan("blog_post", []query.FieldRef{id, authorKey, reviewerKey}).WithOrderings(query.NewOrdering(id, query.Ascending)).WithDistinct()
	base, err := base.WithOffset(2)
	if err != nil {
		t.Fatal(err)
	}
	base, err = base.WithLimit(3)
	if err != nil {
		t.Fatal(err)
	}
	inputs := []query.RelationProjection{reviewer, author, reviewer}
	selected, err := base.WithRelationProjections(inputs...)
	if err != nil {
		t.Fatal(err)
	}
	permuted, err := base.WithRelationProjections(author, reviewer)
	if err != nil || !selected.Equal(permuted) {
		t.Fatal("permutations changed immutable plan", err)
	}
	inputs[0] = query.RelationProjection{}
	columns[0] = name
	returned := selected.RelationProjections()
	if len(returned) != 2 || returned[0].TerminalHop().Field() != "author" || returned[1].TerminalHop().Field() != "reviewer" {
		t.Fatal("canonical ordering lost")
	}
	returned[0] = query.RelationProjection{}
	if !selected.Equal(permuted) || !selected.WithoutRelationProjections().Equal(base) || len(base.RelationProjections()) != 0 {
		t.Fatal("projection storage aliases or source changed")
	}
	onlyAuthor, err := base.WithRelationProjections(author)
	if err != nil {
		t.Fatal(err)
	}
	appended, err := onlyAuthor.WithRelationProjections(reviewer, author)
	if err != nil || !appended.Equal(selected) || len(onlyAuthor.RelationProjections()) != 1 {
		t.Fatal("selection derivation corrupted source", err)
	}
	conflicts := map[string][]query.RelationProjection{
		"empty":                           nil,
		"invalid after valid":             {author, {}},
		"different root":                  {author, makeProjection(ir.ModelIdentity{AppLabel: "archive", ModelName: "post"}, "blog_post", reviewerKey, []query.FieldRef{id, name})},
		"different table":                 {author, makeProjection(root, "archive_post", reviewerKey, []query.FieldRef{id, name})},
		"same FK different nullable":      {author, makeProjection(root, "blog_post", query.NewFieldRef("author", "author_id", query.FieldInteger, true), []query.FieldRef{id, name})},
		"same FK different column":        {author, makeProjection(root, "blog_post", query.NewFieldRef("author", "other_id", query.FieldInteger, false), []query.FieldRef{id, name})},
		"same FK different order":         {author, makeProjection(root, "blog_post", authorKey, []query.FieldRef{name, id})},
		"same FK different target fields": {author, makeProjection(root, "blog_post", authorKey, []query.FieldRef{id})},
	}
	for label, values := range conflicts {
		t.Run(label, func(t *testing.T) {
			result, err := base.WithRelationProjections(values...)
			if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || !result.Equal(query.Plan{}) {
				t.Fatalf("partial or accepted invalid collection: %v", err)
			}
		})
	}
	shape, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := base.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = aggregate.WithRelationProjections(author, reviewer); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("accepted aggregate projection", err)
	}
}
