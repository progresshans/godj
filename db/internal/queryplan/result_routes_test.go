package queryplan_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestSelectedJSONRoutesRetainJoinProvenanceAndOwnCompilerState(t *testing.T) {
	root := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	target := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	reviewer := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	author := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	payload := query.NewFieldRef("payload", "payload", query.FieldJSON, true)
	selected, err := query.NewForwardRelationPath(root, "blog_post", "reviewer", "reviewer_id", target, "authors_author", "id", true, payload)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := query.NewJSONPath(query.JSONKey("a"))
	result, err := query.RelatedJSONPathResult(selected, path)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewProjectionResult(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []struct {
		model ir.ModelIdentity
		table string
	}{{root, "archived_author"}, {ir.ModelIdentity{AppLabel: "blog", ModelName: "other"}, "authors_author"}} {
		filter, err := query.NewForwardRelationPath(foreign.model, "blog_post", "reviewer", "reviewer_id", target, foreign.table, "id", true, query.NewFieldRef("name", "name", query.FieldString, false))
		if err != nil {
			t.Fatal(err)
		}
		expression, err := query.NewExpression(query.NewRelatedCondition(filter, query.LookupExact, query.String("Ada")))
		if err != nil {
			t.Fatal(err)
		}
		source, err := query.NewPlan("blog_post", []query.FieldRef{reviewer, author}).WithWhere(expression)
		if err != nil {
			t.Fatal(err)
		}
		source, err = source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		empty, err := source.WithLimit(0)
		if err != nil {
			t.Fatal(err)
		}
		for _, plan := range []query.Plan{source, empty} {
			if statement, args, err := sqlite.Compile(plan); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || statement != "" || len(args) != 0 {
				t.Fatal("selected route hid inconsistent predicate provenance", err)
			}
		}
	}
	second, err := query.NewForwardRelationPath(root, "blog_post", "author", "author_id", target, "authors_author", "id", false, payload)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := query.RelatedJSONPathResult(second, path)
	if err != nil {
		t.Fatal(err)
	}
	both, err := query.NewProjectionResult(result, secondResult)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("blog_post", []query.FieldRef{reviewer, author}).WithResultShape(both)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := queryplan.PrepareJoins(plan, "test")
	if err != nil || len(expected.Keys) != 2 {
		t.Fatal("selection-only joins missing", err)
	}
	outer := 0
	for _, join := range expected.ByKey {
		if join.LeftOuter {
			outer++
		}
	}
	if outer != 1 {
		t.Fatal("required/optional selected routes lost presence")
	}
	before := plan
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 20 {
				got, err := queryplan.PrepareJoins(plan, "test")
				if err != nil || !reflect.DeepEqual(got, expected) {
					t.Error("shared selected route changed", err)
					return
				}
				delete(got.ByKey, got.Keys[0])
				got.Keys[0] = queryplan.RelationKey{}
			}
		}()
	}
	wait.Wait()
	if !plan.Equal(before) {
		t.Fatal("join preparation mutated selected source")
	}
	// Selection-only paths still consume SQLite's finite JOIN budget.
	self, err := query.NewForwardRelationPath(root, "blog_post", "reviewer", "reviewer_id", root, "blog_post", "id", true, payload)
	if err != nil {
		t.Fatal(err)
	}
	hops := make([]query.RelationHop, 64)
	for i := range hops {
		hops[i] = self.Hops()[0]
	}
	chain, err := query.NewForwardRelationChain(hops, payload, query.RelationTerminalRelatedField)
	if err != nil {
		t.Fatal(err)
	}
	deep, err := query.RelatedJSONPathResult(chain, path)
	if err != nil {
		t.Fatal(err)
	}
	deepShape, err := query.NewProjectionResult(deep)
	if err != nil {
		t.Fatal(err)
	}
	deepPlan, err := query.NewPlan("blog_post", []query.FieldRef{reviewer}).WithResultShape(deepShape)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sqlite.Compile(deepPlan); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("selected JSON route bypassed backend JOIN bound", err)
	}
}
