package querytest

import (
	"encoding/json"
	"errors"
	"github.com/progresshans/godj/conformance/nullableforwardproduct"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"os"
	"reflect"
	"strings"
	"testing"
)

func CheckNestedEagerReference(t *testing.T, backend db.Queryer, compile func(query.Plan) (string, []any, error), counts func() (uint64, uint64)) {
	t.Helper()
	data, err := os.ReadFile("../../orm/testdata/nested-eager-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference nullableforwardproduct.NestedEagerReference
	if err = json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 440 {
		t.Fatal("nested eager reference roster incomplete")
	}
	seen := map[string]bool{}
	for _, observation := range reference.Observations {
		if seen[observation.Name] {
			t.Fatal("duplicate observation", observation.Name)
		}
		seen[observation.Name] = true
		t.Run(observation.Name, func(t *testing.T) {
			plan, err := nullableforwardproduct.NestedEagerPlan(reference.Leaves, observation.NestedInput)
			if err != nil {
				t.Fatal(err)
			}
			beforeReads, beforeTotal := counts()
			count, first, rows, err := nullableforwardproduct.EvaluateNestedEager(t.Context(), backend, plan)
			if err != nil {
				t.Fatal(err)
			}
			if count != observation.Count || !reflect.DeepEqual(first, observation.First) || !reflect.DeepEqual(rows, observation.Rows) {
				actual, _ := json.Marshal(struct {
					Count int64
					First *nullableforwardproduct.NestedEagerRow
					Rows  []nullableforwardproduct.NestedEagerRow
				}{count, first, rows})
				t.Fatalf("nested graph differs: %s", actual)
			}
			wantReads := uint64(len(observation.AllSQL) + len(observation.FirstSQL) + len(observation.CountSQL))
			reads, total := counts()
			if reads-beforeReads != wantReads || wantReads == 0 && total != beforeTotal {
				t.Fatalf("reads=%d total=%d want=%d", reads-beforeReads, total-beforeTotal, wantReads)
			}
			statement, _, err := compile(plan)
			if err != nil {
				t.Fatal(err)
			}
			if len(observation.AllSQL) > 0 && strings.Count(statement, "LEFT OUTER JOIN") != strings.Count(observation.AllSQL[0], "LEFT OUTER JOIN") {
				t.Fatalf("outer JOIN semantics differ:\n%s\n%s", statement, observation.AllSQL[0])
			}
		})
	}
}

// Raw AST callers must receive the same pre-I/O failure even when LIMIT 0
// would make the logical result empty. These plans preserve valid selected
// prefixes but contradict their source or a shared filter edge.
func CheckNestedEagerInvalidPlans(t *testing.T, backend db.Queryer, compile func(query.Plan) (string, []any, error), counts func() (uint64, uint64)) {
	t.Helper()
	data, err := os.ReadFile("../../orm/testdata/nested-eager-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference nullableforwardproduct.NestedEagerReference
	if err = json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	input := reference.Observations[0].NestedInput
	input.Selected = []string{"author__team__organization"}
	selected, err := nullableforwardproduct.NestedEagerPlan(reference.Leaves, input)
	if err != nil {
		t.Fatal(err)
	}
	projections := selected.RelationProjections()
	base := query.NewPlan(selected.Table(), selected.SourceFields())
	invalid := map[string]query.Plan{}
	for _, kind := range []string{"column", "nullability", "type", "missing"} {
		fields := base.SourceFields()
		for i, f := range fields {
			if f.Name() == "author" {
				switch kind {
				case "column":
					fields[i] = query.NewFieldRef(f.Name(), "wrong_id", f.Kind(), f.Nullable())
				case "nullability":
					fields[i] = query.NewFieldRef(f.Name(), f.Column(), f.Kind(), true)
				case "type":
					fields[i] = query.NewFieldRef(f.Name(), f.Column(), query.FieldString, f.Nullable())
				case "missing":
					fields = append(fields[:i], fields[i+1:]...)
				}
				break
			}
		}
		plan, err := query.NewPlan(base.Table(), fields).WithRelationProjections(projections...)
		if err != nil {
			t.Fatal(err)
		}
		invalid["source "+kind] = plan
	}
	base, err = base.WithRelationProjections(projections...)
	if err != nil {
		t.Fatal(err)
	}
	for _, depth := range []int{1, 2} {
		hops := projections[depth-1].Path().Hops()
		last := hops[len(hops)-1]
		terminal := query.NewFieldRef("id", "id", query.FieldInteger, false)
		targetTable, nullable := last.TargetTable(), !last.Nullable()
		if depth == 1 {
			targetTable, nullable = targetTable+"_other", last.Nullable()
		}
		changed, err := query.NewForwardRelationPath(last.Source(), last.SourceTable(), last.Field(), last.SourceColumn(), last.Target(), targetTable, last.TargetPrimaryKeyColumn(), nullable, terminal)
		if err != nil {
			t.Fatal(err)
		}
		hops[len(hops)-1] = changed.Hops()[0]
		path, err := query.NewForwardRelationChain(hops, terminal, query.RelationTerminalRelatedField)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := base.WithConditions(query.NewRelatedCondition(path, query.LookupExact, query.Integer(1)))
		if err != nil {
			t.Fatal(err)
		}
		invalid["filter prefix "+string(rune('0'+depth))] = plan
	}
	for name, plan := range invalid {
		for _, empty := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/normal", true: "/empty"}[empty], func(t *testing.T) {
				if empty {
					plan, err = plan.WithLimit(0)
					if err != nil {
						t.Fatal(err)
					}
				}
				_, before := counts()
				statement, args, err := compile(plan)
				if statement != "" || len(args) != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatalf("invalid graph compile=%q,%v,%v", statement, args, err)
				}
				rows, err := backend.Query(t.Context(), plan)
				if rows != nil {
					_ = rows.Close()
					t.Fatal("invalid graph returned rows")
				}
				_, after := counts()
				if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || after != before {
					t.Fatalf("invalid graph I/O=%d: %v", after-before, err)
				}
			})
		}
	}
}
