package queryplan_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestNullableForwardJoinRejectsConflictingPresenceProof(t *testing.T) {
	post := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	author := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	reviewer := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	path, err := query.NewForwardRelationPath(post, "blog_post", "reviewer", "reviewer_id", author, "authors_author", "id", true, query.NewFieldRef("name", "name", query.FieldString, false))
	if err != nil {
		t.Fatal(err)
	}
	presencePath, err := query.NewNullableForwardRelationIsNullPath(post, "blog_post", reviewer, author, "archived_author", "id")
	if err != nil {
		t.Fatal(err)
	}
	match, err := query.NewExpression(query.NewRelatedCondition(path, query.LookupExact, query.String("Ada")))
	if err != nil {
		t.Fatal(err)
	}
	presence, err := query.NewExpression(query.NewRelatedCondition(presencePath, query.LookupIsNull, query.Boolean(false)))
	if err != nil {
		t.Fatal(err)
	}
	where, err := query.OrExpressions(match, presence)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("blog_post", []query.FieldRef{reviewer}).WithWhere(where)
	if err != nil {
		t.Fatal(err)
	}
	if statement, args, err := sqlite.Compile(plan); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || statement != "" || len(args) != 0 {
		t.Fatalf("conflicting presence proof compiled: %q %v, error=%v", statement, args, err)
	}
}

func TestNullableForwardJoinCompilationOwnsItsWorkingState(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	reviewer := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	path, err := query.NewForwardRelationPath(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, "blog_post", "reviewer", "reviewer_id", ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, "authors_author", "id", true, query.NewFieldRef("name", "name", query.FieldString, false))
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := query.NewExpression(query.NewRelatedCondition(path, query.LookupExact, query.String("Ada")))
	if err != nil {
		t.Fatal(err)
	}
	negated, err := query.NotExpression(leaf)
	if err != nil {
		t.Fatal(err)
	}
	root, err := query.NewExpression(query.NewCondition(title, query.LookupExact, query.String("keep")))
	if err != nil {
		t.Fatal(err)
	}
	where, err := query.OrExpressions(negated, root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("blog_post", []query.FieldRef{id, title, reviewer}).WithWhere(where)
	if err != nil {
		t.Fatal(err)
	}
	// Selecting a different edge must retain deterministic aliases and private
	// compilation state while the nullable filter still chooses an outer join.
	author := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	plan, err = query.NewPlan("blog_post", []query.FieldRef{id, title, author, reviewer}).WithWhere(where)
	if err != nil {
		t.Fatal(err)
	}
	targetID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	targetName := query.NewFieldRef("name", "name", query.FieldString, false)
	projection, err := query.NewForwardRelationProjection(ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}, "blog_post", author, ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, "authors_author", targetID, []query.FieldRef{targetID, targetName})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithRelationProjection(projection)
	if err != nil {
		t.Fatal(err)
	}
	compile := sqlite.Compile
	t.Run("sqlite", func(t *testing.T) {
		statement, args, err := compile(plan)
		if err != nil {
			t.Fatal(err)
		}
		before := plan
		var wait sync.WaitGroup
		for range 12 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				for range 30 {
					got, values, err := compile(plan)
					if err != nil || got != statement || !reflect.DeepEqual(values, args) {
						t.Errorf("shared plan compile changed: %v", err)
						return
					}
					if len(values) > 0 {
						values[0] = "caller mutation"
					}
				}
			}()
		}
		wait.Wait()
		if !plan.Equal(before) {
			t.Fatal("compilation mutated source plan")
		}
	})
}
