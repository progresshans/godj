package sqlite

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteJSONStorageCheckAndCanonicalCatalog(t *testing.T) {
	metadata := ir.Field{Name: "payload", GoName: "Payload", Column: "payload", Kind: ir.FieldJSON, Nullable: true}
	ddl, err := compileMigrationColumn(metadata)
	if err != nil || ddl != `"payload" TEXT NULL CHECK(JSON_VALID("payload") OR "payload" IS NULL)` {
		t.Fatal("JSON declaration", ddl, err)
	}
	if declared, err := sqliteRelationDeclaredType(metadata); err != nil || declared != "TEXT" {
		t.Fatal("JSON catalog declaration", declared, err)
	}
	model := ir.Model{Name: "record", GoName: "Record", DBTable: "records", Fields: []ir.Field{metadata}}
	create, err := compileMigrationCreateModel(model)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := matchesSQLiteRelationCanonicalTableSQL(create, model, nil); err != nil || !ok {
		t.Fatal("JSON check not recognized in sealed table", err)
	}
	for _, altered := range []string{strings.Replace(create, "JSON_VALID", "OTHER_VALID", 1), strings.Replace(create, ` CHECK(JSON_VALID("payload") OR "payload" IS NULL)`, "", 1), strings.Replace(create, " IS NULL", " IS NOT NULL", 1)} {
		if ok, err := matchesSQLiteRelationCanonicalTableSQL(altered, model, nil); err != nil || ok {
			t.Fatal("JSON integrity check drift accepted", err)
		}
	}
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := backend.ExecContext(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", `NaN`, `Infinity`, `{"a":}`, `true false`} {
		if _, err := backend.ExecContext(t.Context(), `INSERT INTO records(payload) VALUES (?)`, raw); err == nil {
			t.Fatal("invalid external JSON bypassed SQLite check", raw)
		}
	}
	// The DB accepts syntax the strict model codec deliberately rejects. Reads
	// must fail without retaining a previous JSON value or treating it as NULL.
	for _, raw := range []any{[]byte(`null`), `{"a":1,"a":2}`, `"\ud800"`} {
		var scanner orm.NullableJSONScanner
		if _, err := backend.ExecContext(t.Context(), `DELETE FROM records`); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.ExecContext(t.Context(), `INSERT INTO records(payload) VALUES (?)`, raw); err != nil {
			t.Fatal(err)
		}
		rows, err := backend.Query(t.Context(), query.NewPlan("records", []query.FieldRef{query.NewFieldRef("payload", "payload", query.FieldJSON, true)}))
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("external JSON row missing")
		}
		scanner.JSON, scanner.Valid = jsonvalue.Null(), true
		if err := rows.Scan(&scanner); err == nil || scanner.Valid || scanner.JSON != (jsonvalue.Value{}) {
			t.Fatal("foreign JSON retained a usable model value")
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLiteJSONParametersRemainTextWithNativeOrdering(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	field := query.NewFieldRef("payload", "payload", query.FieldJSON, true)
	for _, raw := range []string{`null`, `false`, `1.0`, `1e400`, `340282366920938463463374607431768211455`, `{"b":2,"a":1}`, `"\u0000"`} {
		value, err := jsonvalue.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupExact, query.JSON(value)))
		if err != nil {
			t.Fatal(err)
		}
		_, args, err := Compile(plan)
		if err != nil || len(args) != 1 || args[0] != value.Text {
			t.Fatal("JSON predicate lost exact text", err)
		}
		_, args, err = CompileInsert(query.NewInsertPlanReturningKey("records", []query.Assignment{query.NewAssignment(field, query.JSON(value))}, id))
		if err != nil || len(args) != 1 || args[0] != value.Text {
			t.Fatal("JSON mutation was encoded as a Go object", err)
		}
	}
	if _, _, err := Compile(query.NewPlan("records", []query.FieldRef{field}).WithOrderings(query.NewOrdering(field, query.Ascending))); err != nil {
		t.Fatal("JSON whole-field ordering", err)
	}
}

func TestSQLiteJSONOrderingValidatesBeforeAggregateElision(t *testing.T) {
	field := query.NewFieldRef("payload", "payload", query.FieldJSON, true)
	model := query.NewPlan("records", []query.FieldRef{field}).WithOrderings(query.NewOrdering(field, query.Ascending))
	result, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
	}
	count, err := model.WithResultShape(result)
	if err != nil {
		t.Fatal(err)
	}
	limited, err := count.WithLimit(1)
	if err != nil {
		t.Fatal(err)
	}
	emptyCondition, err := query.NewInCondition(field, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := count.WithConditions(emptyCondition)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []query.Plan{model, count, count.WithDistinct(), limited, empty} {
		if _, _, err := Compile(plan); err != nil {
			t.Fatal("valid JSON ordering rejected", err)
		}
		plan = plan.WithOrderings(query.NewOrdering(field, query.Direction("sideways")))
		if _, _, err := Compile(plan); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("invalid ordering escaped aggregate/empty validation", err)
		}
	}
}
