package postgres

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestPostgresJSONParametersBoundNativeExpansionWithoutRounding(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `0`, `-0.0`, `1.0`, `1e400`, `1e4095`, `-1e4094`, `1e-4094`, `0.00e2`, `1.2300e2`, `{"big":340282366920938463463374607431768211455,"text":"한글"}`} {
		value, err := jsonvalue.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		parameter, err := postgresValue(query.JSON(value))
		wire, ok := parameter.(json.RawMessage)
		if err != nil || !ok || string(wire) != value.Text {
			t.Fatal("native JSON parameter was re-encoded or rounded", raw, err)
		}
		wire[0] = 'x'
		if !value.Valid() {
			t.Fatal("parameter aliases JSON value")
		}
	}
	for _, raw := range []string{`1e4096`, `-1e4095`, `1e-4095`, `1e2147483647`, `1e-2147483648`, `1e99999999999999999999999999999999`, `"\u0000"`, `{"\u0000":1}`, `{"nested":["\u0000"]}`} {
		value, err := jsonvalue.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if parameter, err := postgresValue(query.JSON(value)); err == nil || parameter != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
			t.Fatal("unreadable native JSON parameter accepted", raw, err)
		}
	}
	// Input fits the shared codec; jsonb's expanded numbers and separators must
	// also fit as a whole document, independently of each numeric token's bound.
	for _, raw := range []string{`[` + strings.Repeat(`1e4095,`, 256) + `0]`, `{"a":` + `"` + strings.Repeat("x", jsonvalue.MaxDocumentBytes-8) + `"}`} {
		value, err := jsonvalue.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := postgresValue(query.JSON(value)); !errors.Is(err, jsonvalue.ErrLimit) {
			t.Fatal("native document expansion bypassed limit", err)
		}
	}
}

func TestPostgresJSONDriverAndCatalogKeepSQLNullSeparate(t *testing.T) {
	for _, raw := range []string{`null`, `1.00`, `340282366920938463463374607431768211455`, `{"b": 2, "a": [false, null]}`} {
		want, err := jsonvalue.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range []any{raw, []byte(raw)} {
			var optional orm.NullableJSONScanner
			if err := (jsonDestination{destination: &optional}).Scan(input); err != nil || !optional.Valid || optional.JSON != want {
				t.Fatal("native JSON driver lost value", err)
			}
			var mixed any
			if err := (jsonDestination{destination: &mixed}).Scan(input); err != nil || mixed != want {
				t.Fatal("joined JSON cell lost its type", err)
			}
		}
	}
	for _, input := range []any{"", `{"a":1,"a":2}`, `"\ud800"`, `NaN`, int64(0), jsonvalue.Null()} {
		optional := orm.NullableJSONScanner{JSON: jsonvalue.Null(), Valid: true}
		if err := (jsonDestination{destination: &optional}).Scan(input); err == nil || optional.Valid || optional.JSON != (jsonvalue.Value{}) {
			t.Fatal("bad native JSON retained scanner")
		}
		var mixed any = "prior"
		if err := (jsonDestination{destination: &mixed}).Scan(input); err == nil || mixed != nil {
			t.Fatal("bad native JSON retained joined cell")
		}
	}
	optional := orm.NullableJSONScanner{JSON: jsonvalue.Null(), Valid: true}
	if err := (jsonDestination{destination: &optional}).Scan(nil); err != nil || optional.Valid {
		t.Fatal("native SQL NULL retained JSON", err)
	}
	metadata := ir.Field{Name: "payload", GoName: "Payload", Column: "payload", Kind: ir.FieldJSON, Nullable: true}
	if ddl, err := compilePostgresMigrationColumn(metadata); err != nil || ddl != `"payload" JSONB NULL` {
		t.Fatal("JSONB declaration", ddl, err)
	}
	column := postgresMigrationColumnCatalog{attributeNumber: 1, name: "payload", typeSchema: "pg_catalog", typeName: "jsonb", typeModifier: -1, defaultCollation: true}
	if err := assertPostgresMigrationColumnCatalog(column, metadata); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*postgresMigrationColumnCatalog){
		func(c *postgresMigrationColumnCatalog) { c.typeName = "json" }, func(c *postgresMigrationColumnCatalog) { c.typeName = "text" },
		func(c *postgresMigrationColumnCatalog) { c.typeSchema = "foreign" }, func(c *postgresMigrationColumnCatalog) { c.notNull = true },
		func(c *postgresMigrationColumnCatalog) { c.hasDefault = true }, func(c *postgresMigrationColumnCatalog) { c.typeModifier = 1 },
	} {
		bad := column
		change(&bad)
		if err := assertPostgresMigrationColumnCatalog(bad, metadata); err == nil {
			t.Fatal("JSONB catalog drift accepted")
		}
	}
}

func TestPostgresJSONOrderingValidatesBeforeAggregateElision(t *testing.T) {
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
		if _, _, err := compilePlan("jsonref", plan); err != nil {
			t.Fatal("valid JSON ordering rejected", err)
		}
		plan = plan.WithOrderings(query.NewOrdering(field, query.Direction("sideways")))
		if _, _, err := compilePlan("jsonref", plan); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("invalid ordering escaped aggregate/empty validation", err)
		}
	}
}
