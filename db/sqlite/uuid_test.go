package sqlite

import (
	"testing"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

func TestSQLiteUUIDParametersAndForeignStorage(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	field := query.NewFieldRef("reference", "reference", query.FieldUUID, true)
	for _, raw := range []string{"00000000-0000-0000-0000-000000000000", "12345678-9abc-4def-8123-456789abcdef", "ffffffff-ffff-ffff-ffff-ffffffffffff"} {
		value, err := uuid.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupExact, query.UUID(value)))
		if err != nil {
			t.Fatal(err)
		}
		_, args, err := Compile(plan)
		if err != nil || len(args) != 1 || args[0] != value.Hex() {
			t.Fatal("UUID predicate changed physical encoding", args, err)
		}
		_, args, err = CompileInsert(query.NewInsertPlanReturningKey("records", []query.Assignment{query.NewAssignment(field, query.UUID(value))}, id))
		if err != nil || len(args) != 1 || args[0] != value.Hex() {
			t.Fatal("UUID mutation changed physical encoding", args, err)
		}
	}
	metadata := ir.Field{Name: "reference", GoName: "Reference", Column: "reference", Kind: ir.FieldUUID, Nullable: true}
	if ddl, err := compileMigrationColumn(metadata); err != nil || ddl != `"reference" CHAR(32) NULL` {
		t.Fatal("UUID physical declaration", ddl, err)
	}
	if declared, err := sqliteRelationDeclaredType(metadata); err != nil || declared != "CHAR(32)" {
		t.Fatal("UUID preflight declaration", declared, err)
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
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE records(id INTEGER PRIMARY KEY,reference CHAR(32) NULL)`); err != nil {
		t.Fatal(err)
	}
	foreign := []any{"", "00000000-0000-0000-0000-000000000000", "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", "0000000000000000000000000000000g", []byte("00000000000000000000000000000000"), int64(0), float64(1.5)}
	for _, raw := range append(foreign, "00000000000000000000000000000000", nil) {
		if _, err := backend.ExecContext(t.Context(), `INSERT INTO records(reference) VALUES (?)`, raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := backend.Query(t.Context(), query.NewPlan("records", []query.FieldRef{id, field}).WithOrderings(query.NewOrdering(id, query.Ascending)))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var key int64
		scanner := orm.NullableUUIDScanner{UUID: uuid.UUID{1}, Valid: true}
		err := rows.Scan(&key, &scanner)
		if count < len(foreign) {
			if err == nil || scanner.Valid || scanner.UUID != (uuid.UUID{}) {
				t.Fatalf("foreign UUID %d accepted or retained prior value", count)
			}
		} else if err != nil || scanner.UUID != (uuid.UUID{}) || scanner.Valid != (count == len(foreign)) {
			t.Fatal("UUID zero and NULL collapsed", err)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != len(foreign)+2 {
		t.Fatal("UUID foreign storage coverage", count, err)
	}
}
