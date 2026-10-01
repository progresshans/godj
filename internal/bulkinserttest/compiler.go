// Package bulkinserttest checks native batch contracts with shared authored
// inputs. Raw SQL state is observed separately from the product's plans.
package bulkinserttest

import (
	"errors"
	"reflect"
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

func Plan(t *testing.T, rows [][]query.Value, policy query.BulkConflict) query.BulkInsertPlan {
	t.Helper()
	plan, err := query.NewBulkInsertPlan("bulk_items", []query.FieldRef{Name, Amount, Parent, Note}, rows, ID, policy)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func Row(name string, amount int64) []query.Value {
	return []query.Value{query.String(name), query.Integer(amount), query.Integer(1), query.Null()}
}

func Policy(t *testing.T, mode query.BulkConflictMode) query.BulkConflict {
	t.Helper()
	var target, update []query.FieldRef
	if mode == query.BulkConflictUpdate {
		target, update = []query.FieldRef{Name}, []query.FieldRef{Amount}
	}
	policy, err := query.NewBulkConflict(mode, target, update)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func CheckCompiler(t *testing.T, compile func(query.BulkInsertPlan) (string, []any, error), base, auto string, parameterLimit int) {
	t.Helper()
	for _, mode := range []query.BulkConflictMode{query.BulkConflictError, query.BulkConflictIgnore, query.BulkConflictUpdate} {
		t.Run("policy_"+string(mode), func(t *testing.T) {
			plan := Plan(t, [][]query.Value{Row("one'); DROP TABLE bulk_items; --", 1), Row("two", 2)}, Policy(t, mode))
			statement, arguments, err := compile(plan)
			want := base
			switch mode {
			case query.BulkConflictIgnore:
				want += " ON CONFLICT DO NOTHING"
			case query.BulkConflictUpdate:
				want += ` ON CONFLICT ("name") DO UPDATE SET "amount" = EXCLUDED."amount"`
			}
			if mode != query.BulkConflictIgnore {
				want += ` RETURNING "id"`
			}
			if err != nil || statement != want || !reflect.DeepEqual(arguments, []any{"one'); DROP TABLE bulk_items; --", int64(1), int64(1), nil, "two", int64(2), int64(1), nil}) {
				t.Fatal("native bulk SQL/argument contract", statement, arguments, err)
			}
		})
	}
	plan, err := query.NewBulkInsertPlan("bulk_auto", nil, [][]query.Value{nil, nil}, ID, query.BulkConflict{})
	if err != nil {
		t.Fatal(err)
	}
	if statement, arguments, err := compile(plan); err != nil || statement != auto || len(arguments) != 0 {
		t.Fatal("auto-only statement", statement, arguments, err)
	}
	for _, kind := range []string{"zero", "invalid_value", "required_null", "invalid_late_value", "key_type_alias", "parameter_budget"} {
		t.Run(kind, func(t *testing.T) {
			plan := query.BulkInsertPlan{}
			if kind != "zero" {
				rows := [][]query.Value{Row("first", 1), Row("second", 2)}
				fields := []query.FieldRef{Name, Amount, Parent, Note}
				switch kind {
				case "invalid_value":
					rows[0][1] = query.String("2")
				case "required_null":
					rows[0][0] = query.Null()
				case "invalid_late_value":
					rows[1][3] = query.Integer(7)
				case "key_type_alias":
					fields[0] = query.NewFieldRef("name", "id", query.FieldString, false)
				case "parameter_budget":
					fields = []query.FieldRef{Name}
					rows = make([][]query.Value, parameterLimit+1)
					for index := range rows {
						rows[index] = []query.Value{query.String("x")}
					}
				}
				var err error
				plan, err = query.NewBulkInsertPlan("bulk_items", fields, rows, ID, query.BulkConflict{})
				if err != nil {
					if kind == "key_type_alias" || kind == "parameter_budget" && parameterLimit == query.MaximumBulkValues {
						return
					}
					t.Fatal(err)
				}
			}
			statement, arguments, err := compile(plan)
			if err == nil || statement != "" || arguments != nil {
				t.Fatal("invalid bulk insert reached a statement", statement, arguments, err)
			}
		})
	}
	if _, _, err := compile(query.BulkInsertPlan{}); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("zero-plan error classification", err)
	}
	if strings.Contains(base, "DROP TABLE") {
		t.Fatal("unsafe authored expected SQL")
	}
}
