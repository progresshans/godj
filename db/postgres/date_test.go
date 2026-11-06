package postgres

import (
	"github.com/progresshans/godj/calendar"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestDateSQLUsesCanonicalArgumentsAndDateColumns(t *testing.T) {
	field := query.NewFieldRef("at", "at", query.FieldDate, true)
	value := query.Date(calendar.Date{Year: 2026, Month: 9, Day: 19})
	want := "2026-09-19"
	plan, err := query.NewPlan("events", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupGreaterThanOrEqual, value))
	if err != nil {
		t.Fatal(err)
	}
	_, readArgs, err := compilePlan("public", plan)
	if err != nil || len(readArgs) != 1 || readArgs[0] != want {
		t.Fatalf("date predicate argument: %v %v", readArgs, err)
	}
	_, writeArgs, err := compileInsert("public", query.NewInsertPlanReturningKey("events", []query.Assignment{query.NewAssignment(field, value)}, query.NewFieldRef("id", "id", query.FieldInteger, false)))
	if err != nil || len(writeArgs) != 1 || writeArgs[0] != want {
		t.Fatalf("date write argument: %v %v", writeArgs, err)
	}
	for _, nullable := range []bool{false, true} {
		field := ir.Field{Name: "at", GoName: "At", Column: "at", Kind: ir.FieldDate, Nullable: nullable, Default: &ir.Scalar{Kind: ir.ScalarDate, Date: "0001-01-01"}}
		ddl, err := compilePostgresMigrationColumn(field)
		want := `"at" DATE NOT NULL`
		if nullable {
			want = `"at" DATE NULL`
		}
		if err != nil || ddl != want {
			t.Fatalf("date DDL added a persistent default or lost time zone: %s %v", ddl, err)
		}
	}
}

func TestDateCatalogRejectsTimeAndNullDrift(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		model := postgresMigrationTestAuthorModel()
		model.Fields = append(model.Fields, ir.Field{Name: "at", GoName: "At", Column: "at", Kind: ir.FieldDate, Nullable: nullable})
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
				t.Fatalf("date drift was not capability error: %v", err)
			}
		}
	}
}
