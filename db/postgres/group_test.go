package postgres

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func TestCompileGroupedResults(t *testing.T) {
	querytest.GroupCompiler(t, func(p query.Plan) (string, []any, error) { return compilePlan("groups_schema", p) }, true)
}

func TestGroupedOrderedPhysicalCodecsAndPrecisionStayInCompiler(t *testing.T) {
	key := query.NewFieldRef("id", "id", query.FieldInteger, false)
	binary := query.NewFieldRef("binary", "binary", query.FieldBinary, false)
	uuid := query.NewFieldRef("identity", "identity", query.FieldUUID, false)
	amount := query.NewDecimalFieldRef("amount", "amount", true, 12, 4)
	enabled := query.NewFieldRef("enabled", "enabled", query.FieldBoolean, false)
	filter, err := query.NewExpression(query.NewCondition(enabled, query.LookupExact, query.Boolean(true)))
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := query.MinResult(binary).WithFilter(filter)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(key)}, []query.ResultExpression{minimum, query.MaxResult(uuid), query.MinResult(amount)})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("values", []query.FieldRef{key, binary, uuid, amount, enabled}).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	statement, args, err := compilePlan("grouped_codec", plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`decode(MIN(encode("t0"."binary", 'hex') COLLATE "C") FILTER (WHERE `, `), 'hex') AS "c1"`, `MAX("t0"."identity"::text COLLATE "C")::uuid`, "/* godj:decimal(12,4) */", `FROM "grouped_codec"."values"`} {
		if !strings.Contains(statement, part) {
			t.Fatal("missing physical codec boundary", part, statement)
		}
	}
	if len(args) != 1 || args[0] != true {
		t.Fatal("filter argument", args)
	}
	if statement, args, err := compilePlan("a\"b", plan); err == nil || statement != "" || args != nil {
		t.Fatal("invalid schema escaped preflight", statement, args, err)
	}
}
