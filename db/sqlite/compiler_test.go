package sqlite_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
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

func TestCompileRequiredForwardRelationQualifiesAndReusesJoin(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	authorNamePath := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	authorIDPath := requiredAuthorPath(t, query.NewFieldRef("id", "id", query.FieldInteger, false))
	plan := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID}).WithConditions(
		query.NewRelatedCondition(authorNamePath, query.LookupExact, query.String("Ada")),
		query.NewRelatedCondition(authorIDPath, query.LookupExact, query.Integer(1)),
	).WithOrderings(query.NewOrdering(id, query.Ascending))

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT "t0"."id", "t0"."title", "t0"."author_id" FROM "blog_post" AS "t0" INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t1"."name" = ? AND "t1"."id" = ? ORDER BY "t0"."id" ASC`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	if want := []any{"Ada", int64(1)}; !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
}

func TestCompileRelationJoinAliasesAreCanonicalRatherThanConditionOrdered(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	author := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	editor, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "editor", "editor_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false,
		query.NewFieldRef("name", "name", query.FieldString, false),
	)
	if err != nil {
		t.Fatal(err)
	}
	left := query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(editor, query.LookupExact, query.String("Bob")),
		query.NewRelatedCondition(author, query.LookupExact, query.String("Ada")),
	)
	right := query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(author, query.LookupExact, query.String("Ada")),
		query.NewRelatedCondition(editor, query.LookupExact, query.String("Bob")),
	)
	leftSQL, _, err := sqlite.Compile(left)
	if err != nil {
		t.Fatal(err)
	}
	rightSQL, _, err := sqlite.Compile(right)
	if err != nil {
		t.Fatal(err)
	}
	wantJoins := `INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" INNER JOIN "authors_author" AS "t2" ON "t0"."editor_id" = "t2"."id"`
	if !strings.Contains(leftSQL, wantJoins) || !strings.Contains(rightSQL, wantJoins) {
		t.Fatalf("canonical joins missing:\nleft  %s\nright %s", leftSQL, rightSQL)
	}
}

func TestCompileRelationRejectsRootMismatchAndConflictingRepeatedEdge(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	terminal := query.NewFieldRef("name", "name", query.FieldString, false)
	wrongRoot, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"other_post", "author", "author_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(wrongRoot, query.LookupExact, query.String("Ada")),
	))
	assertQueryCode(t, err, query.CodeInvalidPlan)

	first := requiredAuthorPath(t, terminal)
	conflict, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "author", "writer_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(first, query.LookupExact, query.String("Ada")),
		query.NewRelatedCondition(conflict, query.LookupExact, query.String("Ada")),
	))
	assertQueryCode(t, err, query.CodeInvalidPlan)
}

func TestCompileRelationRejectsNonExactAndWrongValueKind(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	path := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	_, _, err := sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(path, query.LookupIContains, query.String("Ada")),
	))
	assertQueryCode(t, err, query.CodeUnsupported)

	_, _, err = sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(path, query.LookupExact, query.Integer(1)),
	))
	assertQueryCode(t, err, query.CodeInvalidPlan)
}

func TestCompileNullableForwardSourceKeyIsNullTrimsJoin(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)

	for _, test := range []struct {
		name      string
		value     bool
		predicate string
	}{
		{name: "null", value: true, predicate: "IS NULL"},
		{name: "not null", value: false, predicate: "IS NOT NULL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := nullableReviewerPath(t, reviewerID)
			plan := query.NewPlan("blog_post", []query.FieldRef{id, title, reviewerID}).WithConditions(
				query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(test.value)),
			).WithOrderings(query.NewOrdering(id, query.Ascending))

			statement, arguments, err := sqlite.Compile(plan)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			want := `SELECT "t0"."id", "t0"."title", "t0"."reviewer_id" FROM "blog_post" AS "t0" WHERE "t0"."reviewer_id" ` + test.predicate + ` ORDER BY "t0"."id" ASC`
			if statement != want {
				t.Fatalf("SQL = %q\nwant  %q", statement, want)
			}
			if len(arguments) != 0 {
				t.Fatalf("arguments = %#v, want empty", arguments)
			}
			if strings.Contains(statement, " JOIN ") {
				t.Fatalf("source-key isnull SQL unexpectedly contains JOIN: %s", statement)
			}
		})
	}
}

func TestCompileNullableForwardSourceKeyCanCoexistWithRequiredJoin(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	author := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	reviewer := nullableReviewerPath(t, reviewerID)
	plan := query.NewPlan("blog_post", []query.FieldRef{id, authorID, reviewerID}).WithConditions(
		query.NewRelatedCondition(reviewer, query.LookupIsNull, query.Boolean(true)),
		query.NewRelatedCondition(author, query.LookupExact, query.String("Ada")),
	)

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := `SELECT "t0"."id", "t0"."author_id", "t0"."reviewer_id" FROM "blog_post" AS "t0" INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t0"."reviewer_id" IS NULL AND "t1"."name" = ?`
	if statement != want {
		t.Fatalf("SQL = %q\nwant  %q", statement, want)
	}
	if wantArguments := []any{"Ada"}; !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileNullableForwardSourceKeyRejectsMutationBeforeIO(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	path := nullableReviewerPath(t, reviewerID)

	tests := []struct {
		name    string
		columns []query.FieldRef
		path    query.RelationPath
		lookup  query.Lookup
		value   query.Value
		code    string
	}{
		{
			name:    "source key missing from columns",
			columns: []query.FieldRef{id},
			path:    path,
			lookup:  query.LookupIsNull,
			value:   query.Boolean(true),
			code:    query.CodeInvalidPlan,
		},
		{
			name:    "source key metadata differs",
			columns: []query.FieldRef{id, query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, false)},
			path:    path,
			lookup:  query.LookupIsNull,
			value:   query.Boolean(true),
			code:    query.CodeInvalidPlan,
		},
		{
			name:    "wrong lookup",
			columns: []query.FieldRef{id, reviewerID},
			path:    path,
			lookup:  query.LookupExact,
			value:   query.Boolean(true),
			code:    query.CodeUnsupported,
		},
		{
			name:    "wrong value kind",
			columns: []query.FieldRef{id, reviewerID},
			path:    path,
			lookup:  query.LookupIsNull,
			value:   query.String("true"),
			code:    query.CodeInvalidPlan,
		},
		{
			name:    "non canonical identity",
			columns: []query.FieldRef{id, reviewerID},
			path: nullableReviewerPathWithIdentity(t, reviewerID,
				ir.ModelIdentity{AppLabel: "Blog", ModelName: "post"}),
			lookup: query.LookupIsNull,
			value:  query.Boolean(true),
			code:   query.CodeInvalidPlan,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := sqlite.Compile(query.NewPlan("blog_post", test.columns).WithConditions(
				query.NewRelatedCondition(test.path, test.lookup, test.value),
			))
			assertQueryCode(t, err, test.code)
		})
	}

	wrongRoot, err := query.NewNullableForwardRelationIsNullPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"other_post", reviewerID,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id, reviewerID}).WithConditions(
		query.NewRelatedCondition(wrongRoot, query.LookupIsNull, query.Boolean(true)),
	))
	assertQueryCode(t, err, query.CodeInvalidPlan)
}

func requiredAuthorPath(t *testing.T, terminal query.FieldRef) query.RelationPath {
	t.Helper()
	path, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "author", "author_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func nullableReviewerPath(t *testing.T, sourceKey query.FieldRef) query.RelationPath {
	t.Helper()
	return nullableReviewerPathWithIdentity(t, sourceKey, ir.ModelIdentity{AppLabel: "blog", ModelName: "post"})
}

func nullableReviewerPathWithIdentity(
	t *testing.T,
	sourceKey query.FieldRef,
	source ir.ModelIdentity,
) query.RelationPath {
	t.Helper()
	path, err := query.NewNullableForwardRelationIsNullPath(
		source,
		"blog_post",
		sourceKey,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author",
		"id",
	)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func assertQueryCode(t *testing.T, err error, code string) {
	t.Helper()
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Code != code {
		t.Fatalf("error = %v, want query error code %q", err, code)
	}
}
