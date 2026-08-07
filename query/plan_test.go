package query_test

import (
	"errors"
	"reflect"
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

func TestMutationPlansDoNotExposeMutableAssignmentStorage(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	assignments := []query.Assignment{
		query.NewAssignment(title, query.String("before")),
		query.NewAssignment(published, query.Boolean(false)),
	}
	insert := query.NewInsertPlan("news_article", assignments)
	update := query.NewUpdatePlan("news_article", assignments, id, query.Integer(7))
	assignments[0] = query.NewAssignment(title, query.String("source-mutated"))

	returned := insert.Assignments()
	returned[0] = query.NewAssignment(title, query.String("getter-mutated"))
	if got, _ := insert.Assignments()[0].Value().String(); got != "before" {
		t.Fatalf("insert plan was mutated through external slice: %q", got)
	}
	if got, _ := update.Assignments()[0].Value().String(); got != "before" {
		t.Fatalf("update plan shared source assignments: %q", got)
	}
	if !query.NewDeletePlan("news_article", id, query.Integer(7)).Equal(
		query.NewDeletePlan("news_article", id, query.Integer(7)),
	) {
		t.Fatal("equal delete plans differ")
	}
}

func TestNullValueBindsAsUntypedSQLNull(t *testing.T) {
	t.Parallel()

	value := query.Null()
	got, err := value.DatabaseValue()
	if err != nil {
		t.Fatalf("DatabaseValue() error = %v", err)
	}
	if !value.IsNull() || !reflect.DeepEqual(got, nil) {
		t.Fatalf("null value = kind %q database %#v", value.Kind(), got)
	}
}
