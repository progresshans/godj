package sqlite

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalstorage"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteDecimalExactParametersAndWriteDomain(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	field := query.NewDecimalFieldRef("cost", "cost", true, 5, 2)
	for _, raw := range []string{"0", "-0", "999.99", "-999.99", "1.001", "1000", "123456789012345678.123456789012"} {
		number, err := decimal.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupExact, query.Decimal(number)))
		if err != nil {
			t.Fatal(err)
		}
		_, args, err := Compile(plan)
		if err != nil || len(args) != 1 {
			t.Fatalf("exact comparison %s: %v", raw, err)
		}
		key, err := decimalstorage.Encode(number)
		if err != nil {
			t.Fatal(err)
		}
		actual, ok := args[0].([]byte)
		if !ok || !bytes.Equal(actual, key) {
			t.Fatal("comparison coerced decimal to another storage class")
		}
		_, args, err = CompileInsert(query.NewInsertPlanReturningKey("records", []query.Assignment{query.NewAssignment(field, query.Decimal(number))}, id))
		if (err == nil) != number.Fits(5, 2) {
			t.Fatalf("write silently rounded %s: %v", raw, err)
		}
		if err == nil {
			actual, ok = args[0].([]byte)
			if !ok || !bytes.Equal(actual, key) {
				t.Fatal("write changed key")
			}
		}
	}
	metadata := ir.Field{Name: "cost", GoName: "Cost", Column: "cost", Kind: ir.FieldDecimal, Nullable: true, Decimal: &ir.DecimalSpec{MaxDigits: 5, DecimalPlaces: 2}}
	ddl, err := compileMigrationColumn(metadata)
	if err != nil || ddl != `"cost" BLOB NULL` {
		t.Fatalf("decimal physical declaration: %s %v", ddl, err)
	}
	if declared, err := sqliteRelationDeclaredType(metadata); err != nil || declared != "BLOB" {
		t.Fatal("preflight accepts NUMERIC affinity")
	}
	invalid := query.NewFieldRef("cost", "cost", query.FieldDecimal, true)
	if _, _, err := Compile(query.NewPlan("records", []query.FieldRef{invalid})); err == nil {
		t.Fatal("read accepted missing precision")
	}
	if _, _, err := CompileInsert(query.NewInsertPlanReturningKey("records", []query.Assignment{query.NewAssignment(invalid, query.Null())}, id)); err == nil {
		t.Fatal("NULL write bypassed missing precision")
	}
}

func TestSQLiteDecimalRejectsExternalStorageAndPrecision(t *testing.T) {
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE records (id INTEGER PRIMARY KEY, cost BLOB NULL)`); err != nil {
		t.Fatal(err)
	}
	overflow, _ := decimal.Parse("1000")
	overflowKey, _ := decimalstorage.Encode(overflow)
	overscale, _ := decimal.Parse("1.001")
	overscaleKey, _ := decimalstorage.Encode(overscale)
	for _, raw := range []any{"0", int64(0), float64(1.5), []byte("0"), []byte{2}, overflowKey, overscaleKey} {
		if _, err := backend.ExecContext(t.Context(), `INSERT INTO records (cost) VALUES (?)`, raw); err != nil {
			t.Fatal(err)
		}
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	cost := query.NewDecimalFieldRef("cost", "cost", true, 5, 2)
	rows, err := backend.Query(t.Context(), query.NewPlan("records", []query.FieldRef{id, cost}).WithOrderings(query.NewOrdering(id, query.Ascending)))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		scanner := orm.NewNullableDecimalScanner(5, 2)
		scanner.Decimal = decimal.Decimal{Coefficient: "1"}
		scanner.Valid = true
		if err := rows.Scan(&id, &scanner); err == nil || scanner.Valid || scanner.Decimal != (decimal.Decimal{}) {
			t.Fatalf("external SQLite value %d accepted or leaked prior row", count)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != 7 {
		t.Fatalf("external row coverage %d: %v", count, err)
	}
}
