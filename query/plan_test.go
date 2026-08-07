package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestPlanDerivationDoesNotMutateSource(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	base := query.NewPlan("news_article", []query.FieldRef{title})
	filtered := base.WithConditions(query.NewCondition(title, query.LookupExact, query.String("Django")))
	ordered := filtered.WithOrderings(query.NewOrdering(title, query.Descending))

	if len(base.Conditions()) != 0 || len(base.Orderings()) != 0 {
		t.Fatalf("base plan mutated: conditions=%v orderings=%v", base.Conditions(), base.Orderings())
	}
	if len(filtered.Conditions()) != 1 || len(filtered.Orderings()) != 0 {
		t.Fatalf("filtered plan changed unexpectedly: conditions=%v orderings=%v", filtered.Conditions(), filtered.Orderings())
	}
	if len(ordered.Conditions()) != 1 || len(ordered.Orderings()) != 1 {
		t.Fatalf("ordered plan = conditions=%v orderings=%v", ordered.Conditions(), ordered.Orderings())
	}

	copyOfConditions := filtered.Conditions()
	copyOfConditions[0] = query.NewCondition(title, query.LookupIContains, query.String("mutated"))
	if filtered.Conditions()[0].Lookup() != query.LookupExact {
		t.Fatal("Conditions returned mutable plan storage")
	}
}

func TestPlanLimitValidation(t *testing.T) {
	t.Parallel()

	_, err := query.NewPlan("news_article", nil).WithLimit(-1)
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Code != query.CodeInvalidLimit {
		t.Fatalf("error = %v, want invalid_limit", err)
	}
}
