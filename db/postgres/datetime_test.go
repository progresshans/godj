package postgres

import (
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
	"time"
)

func TestDateTimeSQLUsesNativeUTCArgumentsAndTimestampWithTimeZone(t *testing.T) {
	field := query.NewFieldRef("at", "at", query.FieldDateTime, true)
	value := query.DateTime(time.Date(2026, 9, 19, 12, 34, 56, 123456789, time.FixedZone("input", 9*3600)))
	want := time.Date(2026, 9, 19, 3, 34, 56, 123456000, time.UTC)
	plan, err := query.NewPlan("events", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupGreaterThanOrEqual, value))
	if err != nil {
		t.Fatal(err)
	}
	_, readArgs, err := compilePlan("public", plan)
	if err != nil || len(readArgs) != 1 || readArgs[0] != want {
		t.Fatalf("datetime predicate argument: %v %v", readArgs, err)
	}
	_, writeArgs, err := compileInsert("public", query.NewInsertPlanReturningKey("events", []query.Assignment{query.NewAssignment(field, value)}, query.NewFieldRef("id", "id", query.FieldInteger, false)))
	if err != nil || len(writeArgs) != 1 || writeArgs[0] != want {
		t.Fatalf("datetime write argument: %v %v", writeArgs, err)
	}
	for _, nullable := range []bool{false, true} {
		field := ir.Field{Name: "at", GoName: "At", Column: "at", Kind: ir.FieldDateTime, Nullable: nullable, Default: &ir.Scalar{Kind: ir.ScalarDateTime, DateTime: "0001-01-01T00:00:00.000000Z"}}
		ddl, err := compilePostgresMigrationColumn(field)
		want := `"at" TIMESTAMP WITH TIME ZONE NOT NULL`
		if nullable {
			want = `"at" TIMESTAMP WITH TIME ZONE NULL`
		}
		if err != nil || ddl != want {
			t.Fatalf("datetime DDL added a persistent default or lost time zone: %s %v", ddl, err)
		}
	}
}

func TestDateTimeCatalogRejectsTimezonePrecisionAndNullDrift(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		model := postgresMigrationTestAuthorModel()
		model.Fields = append(model.Fields, ir.Field{Name: "at", GoName: "At", Column: "at", Kind: ir.FieldDateTime, Nullable: nullable})
		exact := postgresMigrationTestCatalog(t, "product_schema", model, nil)
		if err := assertPostgresMigrationModelCatalog(exact, "product_schema", model, nil); err != nil {
			t.Fatal(err)
		}
		for _, change := range []func(*postgresMigrationColumnCatalog){
			func(c *postgresMigrationColumnCatalog) { c.typeName = "timestamp" }, func(c *postgresMigrationColumnCatalog) { c.typeModifier = 0 },
			func(c *postgresMigrationColumnCatalog) { c.notNull = !c.notNull }, func(c *postgresMigrationColumnCatalog) { c.hasDefault = true }, func(c *postgresMigrationColumnCatalog) { c.identity = "d" },
		} {
			catalog := clonePostgresMigrationTestCatalog(exact)
			change(&catalog.columns[2])
			if err := assertPostgresMigrationModelCatalog(catalog, "product_schema", model, nil); err == nil || !migrationbackend.IsCapabilityError(err) {
				t.Fatalf("datetime drift was not capability error: %v", err)
			}
		}
	}
}
