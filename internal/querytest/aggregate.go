package querytest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// AggregateFixtureSQL seeds independent physical rows for both backend tests.
// PostgreSQL callers qualify the two table identifiers with their private schema.
func AggregateFixtureSQL() []string {
	return []string{
		`CREATE TABLE "aggregate_author" ("id" INTEGER PRIMARY KEY, "name" TEXT NOT NULL)`,
		`CREATE TABLE "aggregate_post" ("id" INTEGER PRIMARY KEY, "title" TEXT NOT NULL, "author_id" INTEGER NOT NULL REFERENCES "aggregate_author"("id"), "reviewer_id" INTEGER REFERENCES "aggregate_author"("id"))`,
		`INSERT INTO "aggregate_author" VALUES (1, 'Ada'), (2, 'Bob'), (3, 'Empty')`,
		`INSERT INTO "aggregate_post" VALUES (10, 'Same', 1, NULL), (11, 'Same', 1, 2), (12, 'Same', 2, NULL), (13, 'Other', 2, 1)`,
	}
}

// CheckAggregateSemantics uses fixed expectations, including reverse JOIN
// multiplicity that would be lost by counting distinct root keys implicitly.
func CheckAggregateSemantics(t *testing.T, ctx context.Context, backend db.Queryer) {
	t.Helper()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorKey := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	reviewerKey := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	post := ir.ModelIdentity{AppLabel: "aggregate", ModelName: "post"}
	author := ir.ModelIdentity{AppLabel: "aggregate", ModelName: "author"}
	forward, err := query.NewForwardRelationPath(post, "aggregate_post", "author", "author_id", author, "aggregate_author", "id", false, name)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := query.NewReverseRelationPath(post, "aggregate_post", "author", "author_id", author, "aggregate_author", "id", "posts", false, title)
	if err != nil {
		t.Fatal(err)
	}
	nullable, err := query.NewNullableForwardRelationIsNullPath(post, "aggregate_post", reviewerKey, author, "aggregate_author", "id")
	if err != nil {
		t.Fatal(err)
	}
	posts := query.NewPlan("aggregate_post", []query.FieldRef{id, title, authorKey, reviewerKey})
	authors := query.NewPlan("aggregate_author", []query.FieldRef{id, name})
	matching := Conditions(t, authors, query.NewRelatedCondition(reverse, query.LookupExact, query.String("Same"))).WithOrderings(query.NewOrdering(id, query.Ascending))
	shape, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name          string
		plan          query.Plan
		offset, limit int
		want          int64
	}{
		{name: "forward", plan: Conditions(t, posts, query.NewRelatedCondition(forward, query.LookupExact, query.String("Ada"))), limit: -1, want: 2},
		{name: "reverse duplicates", plan: matching, limit: -1, want: 3},
		{name: "reverse distinct", plan: matching.WithDistinct(), limit: -1, want: 2},
		{name: "reverse slice", plan: matching, offset: 1, limit: 1, want: 1},
		{name: "distinct before slice", plan: matching.WithDistinct(), offset: 1, limit: 2, want: 1},
		{name: "offset past end", plan: matching, offset: 3, limit: -1},
		{name: "zero limit", plan: matching, limit: 0},
		{name: "nullable source", plan: Conditions(t, posts, query.NewRelatedCondition(nullable, query.LookupIsNull, query.Boolean(true))), limit: -1, want: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := test.plan.WithOffset(test.offset)
			if err != nil {
				t.Fatal(err)
			}
			if test.limit >= 0 {
				plan, err = plan.WithLimit(test.limit)
				if err != nil {
					t.Fatal(err)
				}
			}
			plan, err = plan.WithResultShape(shape)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := backend.Query(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var count int64
			if !rows.Next() {
				t.Fatalf("missing count row: %v", rows.Err())
			}
			if err := rows.Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != test.want || rows.Next() {
				t.Fatalf("count = %d, want one row with %d", count, test.want)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, limit := range []int{-1, 2, 0} {
		plan := posts.WithOrderings(query.NewOrdering(id, query.Descending))
		if limit >= 0 {
			plan, err = plan.WithLimit(limit)
			if err != nil {
				t.Fatal(err)
			}
		}
		shape, err := query.NewAggregateResult(query.MinResult(id), query.MinResult(title))
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := backend.Query(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		var minimumID sql.NullInt64
		var minimumTitle sql.NullString
		if !rows.Next() {
			_ = rows.Close()
			t.Fatalf("missing MIN row: %v", rows.Err())
		}
		err = rows.Scan(&minimumID, &minimumTitle)
		extra := rows.Next()
		iterationErr, closeErr := rows.Err(), rows.Close()
		if err != nil || extra || iterationErr != nil || closeErr != nil {
			t.Fatalf("MIN lifecycle: %v/%t/%v/%v", err, extra, iterationErr, closeErr)
		}
		wantID := int64(10)
		if limit == 2 {
			wantID = 12
		}
		if limit == 0 {
			if minimumID.Valid || minimumTitle.Valid {
				t.Fatalf("empty MIN = %v/%v", minimumID, minimumTitle)
			}
		} else if !minimumID.Valid || minimumID.Int64 != wantID || !minimumTitle.Valid || minimumTitle.String != "Other" {
			t.Fatalf("MIN(limit %d) = %v/%v", limit, minimumID, minimumTitle)
		}
	}
}
