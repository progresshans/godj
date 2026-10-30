package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func TestCompileGroupedResults(t *testing.T) { querytest.GroupCompiler(t, Compile, false) }

func TestGroupedPageCannotShadowItsPhysicalSourceAndKeepsZeroPageTotal(t *testing.T) {
	backend, err := Open(t.Context(), "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "groups.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE "godj_groups" ("id" INTEGER PRIMARY KEY, "rank" INTEGER); INSERT INTO "godj_groups" VALUES (1, NULL), (2, 0), (3, 0)`); err != nil {
		t.Fatal(err)
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	rank := query.NewFieldRef("rank", "rank", query.FieldInteger, true)
	shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(rank)}, []query.ResultExpression{query.CountAllResult()})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("godj_groups", []query.FieldRef{id, rank}).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithGroupMode(query.GroupPage)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := backend.Query(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var total int64
	var present, key, count sql.NullInt64
	if !rows.Next() {
		t.Fatal("no total row", rows.Err())
	}
	if err := rows.Scan(&total, &present, &key, &count); err != nil {
		t.Fatal(err)
	}
	if total != 2 || present.Valid || key.Valid || count.Valid || rows.Next() {
		t.Fatal("zero page lost total or fabricated a group", total, present, key, count)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
