// Package bulkupdatetest checks native UPDATE behavior against independently
// seeded and read SQL state. It is shared by both supported database backends.
package bulkupdatetest

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
)

var (
	ID     = query.NewFieldRef("id", "id", query.FieldInteger, false)
	Name   = query.NewFieldRef("name", "name", query.FieldString, false)
	Amount = query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	Parent = query.NewFieldRef("parent", "parent_id", query.FieldInteger, true)
	Note   = query.NewFieldRef("note", "note", query.FieldString, true)
)

func Source() query.Plan {
	return query.NewPlan("update_items", []query.FieldRef{ID, Name, Amount, Parent, Note})
}

func Plan(t *testing.T, source query.Plan, fields []query.FieldRef, keys []int64, rows [][]query.Value) query.BulkUpdatePlan {
	t.Helper()
	spec, err := query.NewBulkUpdateSpec(source, fields, ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewBulkUpdatePlan(spec, keys, rows)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func Filter(t *testing.T, source query.Plan, conditions ...query.Condition) query.Plan {
	t.Helper()
	plan, err := source.WithConditions(conditions...)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func CheckCompiler(t *testing.T, compile func(query.BulkUpdatePlan) (string, []any, error), table, selection string, postgres bool, limit int) {
	t.Helper()
	literal := "one'); DROP TABLE update_items; --"
	source := Filter(t, Source(), query.NewCondition(Name, query.LookupExact, query.String(literal)))
	plan := Plan(t, source, []query.FieldRef{Amount, Note}, []int64{1, 9007199254740993}, [][]query.Value{{query.Integer(11), query.Null()}, {query.Integer(22), query.String(literal)}})
	p := func(index int) string {
		prefix := "?"
		if postgres {
			prefix = "$"
		}
		return prefix + strconv.Itoa(index)
	}
	want := `WITH "godj_bulk_targets" ("godj_bulk_key") AS MATERIALIZED (SELECT DISTINCT "godj_bulk_input"."id" FROM (` + selection + `) AS "godj_bulk_input" WHERE "godj_bulk_input"."id" IN (` + p(2) + `, ` + p(3) + `)) UPDATE ` + table + ` SET "amount" = CASE "id" WHEN ` + p(2) + ` THEN ` + p(4) + ` WHEN ` + p(3) + ` THEN ` + p(5) + ` ELSE "amount" END, "note" = CASE "id" WHEN ` + p(2) + ` THEN ` + p(6) + ` WHEN ` + p(3) + ` THEN ` + p(7) + ` ELSE "note" END WHERE "id" IN (SELECT "godj_bulk_key" FROM "godj_bulk_targets")`
	if postgres {
		want = `UPDATE ` + table + ` SET "amount" = CASE "id" WHEN $2 THEN $4 WHEN $3 THEN $5 ELSE "amount" END, "note" = CASE "id" WHEN $2 THEN $6 WHEN $3 THEN $7 ELSE "note" END WHERE ("name" = $1) AND "id" IN ($2, $3)`
	}
	statement, args, err := compile(plan)
	if err != nil || statement != want || !reflect.DeepEqual(args, []any{literal, int64(1), int64(9007199254740993), int64(11), int64(22), nil, literal}) || strings.Contains(statement, "DROP TABLE") {
		t.Fatal("native predicate/CASE parameter contract", statement, args, err)
	}
	for _, mode := range []string{"zero", "late_wrong_type", "required_null", "invalid_value", "predicate_budget"} {
		t.Run(mode, func(t *testing.T) {
			plan := query.BulkUpdatePlan{}
			if mode != "zero" {
				rows := [][]query.Value{{query.Integer(11)}, {query.Integer(22)}}
				keys := []int64{1, 2}
				source := Source()
				switch mode {
				case "late_wrong_type":
					rows[1][0] = query.String("22")
				case "required_null":
					rows[1][0] = query.Null()
				case "invalid_value":
					rows[1][0] = query.Value{}
				case "predicate_budget":
					// The existing read compiler packs large IN memberships into
					// one native argument. Use two real scalar arguments and a
					// matrix that fits only when their budget is incorrectly lost.
					source = Filter(t, source, query.NewCondition(Name, query.LookupExact, query.String("one")), query.NewCondition(Amount, query.LookupExact, query.Integer(1)))
					keys, rows = make([]int64, limit/2), make([][]query.Value, limit/2)
					for index := range keys {
						keys[index], rows[index] = int64(index), []query.Value{query.Integer(int64(index))}
					}
				}
				plan = Plan(t, source, []query.FieldRef{Amount}, keys, rows)
			}
			statement, args, err := compile(plan)
			if err == nil || statement != "" || args != nil {
				t.Fatal("invalid write reached a statement", len(statement), len(args), err)
			}
		})
	}
	if _, _, err := compile(query.BulkUpdatePlan{}); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("zero-plan classification", err)
	}
}
