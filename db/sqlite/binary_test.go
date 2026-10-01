package sqlite

import (
	"testing"

	"github.com/progresshans/godj/binaryvalue"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteBinaryParametersAndForeignStorage(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	field := query.NewFieldRef("payload", "payload", query.FieldBinary, true)
	for _, data := range []string{"", "\x00\xffa\x80", "'\\;"} {
		value := binaryvalue.Value{Data: data}
		plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupExact, query.Binary(value)))
		if err != nil {
			t.Fatal(err)
		}
		_, args, err := Compile(plan)
		if err != nil || len(args) != 1 {
			t.Fatal(err)
		}
		if bytes, ok := args[0].([]byte); !ok || bytes == nil || string(bytes) != data {
			t.Fatal("binary predicate became text or NULL")
		}
		_, args, err = CompileInsert(query.NewInsertPlanReturningKey("records", []query.Assignment{query.NewAssignment(field, query.Binary(value))}, id))
		if err != nil || len(args) != 1 {
			t.Fatal(err)
		}
		if bytes, ok := args[0].([]byte); !ok || bytes == nil || string(bytes) != data {
			t.Fatal("binary write became text or NULL")
		}
	}
	metadata := ir.Field{Name: "payload", GoName: "Payload", Column: "payload", Kind: ir.FieldBinary, Nullable: true, MaxLength: 4}
	if ddl, err := compileMigrationColumn(metadata); err != nil || ddl != `"payload" BLOB NULL` {
		t.Fatal(ddl, err)
	}
	if declared, err := sqliteRelationDeclaredType(metadata); err != nil || declared != "BLOB" {
		t.Fatal(declared, err)
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
	if _, err := backend.ExecContext(t.Context(), `CREATE TABLE records(id INTEGER PRIMARY KEY,payload BLOB NULL)`); err != nil {
		t.Fatal(err)
	}
	foreign := []any{"", "AP8=", int64(1), float64(1.5)}
	for _, raw := range append(foreign, []byte{}, []byte{0, 255}, nil) {
		if _, err := backend.ExecContext(t.Context(), `INSERT INTO records(payload) VALUES (?)`, raw); err != nil {
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
		scanner := orm.NullableBinaryScanner{Binary: binaryvalue.Value{Data: "old"}, Valid: true}
		err := rows.Scan(&key, &scanner)
		if count < len(foreign) {
			if err == nil || scanner.Valid || scanner.Binary != (binaryvalue.Value{}) {
				t.Fatal("foreign storage accepted or old value retained")
			}
		} else {
			want := ""
			if count == len(foreign)+1 {
				want = "\x00\xff"
			}
			if err != nil || scanner.Binary.Data != want || scanner.Valid != (count < len(foreign)+2) {
				t.Fatal("binary presence changed", err)
			}
		}
		count++
	}
	if err := rows.Err(); err != nil || count != len(foreign)+3 {
		t.Fatal("foreign storage inventory", count, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteBinaryInputPolicyAndDefaultAreSealed(t *testing.T) {
	model := ir.Model{Name: "packet", GoName: "Packet", DBTable: "packet", Fields: []ir.Field{
		{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
		{Name: "payload", GoName: "Payload", Column: "payload", Kind: ir.FieldBinary, NonEditable: true, MaxLength: 4, Default: &ir.Scalar{Kind: ir.ScalarBinary, Binary: "AP8="}},
	}}
	for _, mutate := range []func(*ir.Field){func(f *ir.Field) { f.NonEditable = false }, func(f *ir.Field) { f.MaxLength = 8 }, func(f *ir.Field) { f.Default.Binary = "" }} {
		seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{Migration: migrationbackend.AppliedMigration{App: "binaryref", Name: "0001_initial"}, Kind: migrationbackend.HistoryTransitionApply}, migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{Kind: migrationbackend.MigrationCreateModel, After: model}}})
		if err != nil {
			t.Fatal(err)
		}
		mutate(&seal.intent.Operations[0].After.Fields[1])
		if err := verifySQLiteRelationIntentSeal(&seal); err == nil {
			t.Fatal("sealed binary input policy/default changed")
		}
	}
}
