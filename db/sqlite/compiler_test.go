package sqlite_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

func TestCompilePredicatesOrderingAndLimit(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	plan := query.NewPlan("news_article", []query.FieldRef{id, title, published}).
		WithConditions(
			query.NewCondition(title, query.LookupIContains, query.String(`50%_Go\SQL`)),
			query.NewCondition(published, query.LookupExact, query.Boolean(true)),
		).
		WithOrderings(query.NewOrdering(title, query.Descending), query.NewOrdering(id, query.Ascending))
	plan, err := plan.WithLimit(2)
	if err != nil {
		t.Fatalf("WithLimit() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT "id", "title", "published" FROM "news_article" WHERE "title" LIKE ? ESCAPE '\' AND "published" = ? ORDER BY "title" DESC, "id" ASC LIMIT ?`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	wantArguments := []any{`%50\%\_Go\\SQL%`, true, int64(2)}
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileIsNullHasNoBoundArgument(t *testing.T) {
	t.Parallel()

	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	plan := query.NewPlan("news_article", []query.FieldRef{summary}).WithConditions(
		query.NewCondition(summary, query.LookupIsNull, query.Boolean(false)),
	)
	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if statement != `SELECT "summary" FROM "news_article" WHERE "summary" IS NOT NULL` {
		t.Fatalf("SQL = %q", statement)
	}
	if len(arguments) != 0 {
		t.Fatalf("arguments = %#v, want empty", arguments)
	}
}

func TestCompileRejectsConditionFromOtherModel(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	otherID := query.NewFieldRef("id", "other_id", query.FieldInteger, false)
	plan := query.NewPlan("news_article", []query.FieldRef{id}).WithConditions(
		query.NewCondition(otherID, query.LookupExact, query.Integer(1)),
	)
	_, _, err := sqlite.Compile(plan)
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Code != query.CodeInvalidPlan {
		t.Fatalf("error = %v, want invalid_plan", err)
	}
}
