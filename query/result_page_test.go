package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
)

func TestResultPageKeepsPrivateOrderingAndInputOwnership(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	value := query.NewFieldRef("value", "value", query.FieldInteger, true)
	shape, err := query.NewProjectionResult(query.FieldResult(value))
	if err != nil {
		t.Fatal(err)
	}
	input, err := query.NewPlan("page_items", []query.FieldRef{id, value}).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	input = input.WithOrderings(query.NewOrdering(id, query.Descending))
	page, err := query.NewPagePlan(input, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	captured, order, present := page.PageInput()
	if !present || len(captured.ResultShape().Expressions()) != 2 || len(page.ResultShape().Expressions()) != 1 || len(order) != 2 ||
		order[0].Index() != 1 || order[0].Direction() != query.Descending || order[1].Index() != 0 || order[1].Nulls() != query.NullsLast {
		t.Fatal("page lost private order cell or deterministic ties")
	}
	order[0] = order[1]
	_, retained, _ := page.PageInput()
	if retained[0].Index() != 1 || len(input.ResultShape().Expressions()) != 1 || input.ResultStageDepth() != 1 || page.ResultStageDepth() != 2 {
		t.Fatal("page or source shares mutable ordering")
	}
	zero, err := query.NewPagePlan(input, 0, 0)
	if err != nil || zero.EmptyResult() {
		t.Fatal("zero-sized page must retain its total", err)
	}
	if _, _, err := query.NewDerivedPlan(page); err == nil {
		t.Fatal("page envelope became scalar input")
	}
	if _, err := query.NewPagePlan(input.WithDistinct(), 2, 0); err == nil {
		t.Fatal("hidden order cell changed DISTINCT cardinality")
	}
	if _, err := query.NewPagePlan(query.NewPlan("page_items", []query.FieldRef{id, value}), 2, 0); err == nil {
		t.Fatal("implicit model page has no explicit selected cells")
	}
	sliced, err := input.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewPagePlan(sliced, 2, 0); err == nil {
		t.Fatal("page silently replaced an input slice")
	}
	for _, bounds := range [][2]int{{-1, 0}, {1, -1}} {
		if _, err := query.NewPagePlan(input, bounds[0], bounds[1]); err == nil {
			t.Fatal("invalid page bounds accepted")
		}
	}
	changed, err := page.WithResultShape(shape)
	if err == nil && changed.ValidatePage() == nil {
		t.Fatal("page envelope can be changed into an ordinary projection")
	}
	if _, err := query.NewQueryUpdatePlan(page, id, nil); err == nil {
		t.Fatal("page envelope became an update source")
	}
}
