package postgres

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

func TestPostgresUUIDNativeParametersCatalogAndDriver(t *testing.T) {
	for _, raw := range []string{"00000000-0000-0000-0000-000000000000", "12345678-9abc-4def-8123-456789abcdef", "ffffffff-ffff-ffff-ffff-ffffffffffff"} {
		value, err := uuid.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		parameter, err := postgresValue(query.UUID(value))
		if native, ok := parameter.(pgtype.UUID); err != nil || !ok || !native.Valid || native.Bytes != value.Bytes() {
			t.Fatal("UUID native parameter changed", err)
		}
		for _, input := range []any{raw, []byte(raw)} {
			scanner := orm.NullableUUIDScanner{}
			if err := (uuidDestination{destination: &scanner}).Scan(input); err != nil || !scanner.Valid || scanner.UUID != value {
				t.Fatal("native UUID driver text changed", err)
			}
			var cell any
			if err := (uuidDestination{destination: &cell}).Scan(input); err != nil || cell != value {
				t.Fatal("native UUID mixed-row cell lost type", err)
			}
		}
	}
	for _, raw := range []any{"", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF", "00000000000000000000000000000000", int64(0), uuid.UUID{}, []byte("bad")} {
		scanner := orm.NullableUUIDScanner{UUID: uuid.UUID{1}, Valid: true}
		if err := (uuidDestination{destination: &scanner}).Scan(raw); err == nil || scanner.Valid || scanner.UUID != (uuid.UUID{}) {
			t.Fatal("invalid UUID driver text retained scanner")
		}
		var cell any = "old"
		if err := (uuidDestination{destination: &cell}).Scan(raw); err == nil || cell != nil {
			t.Fatal("invalid UUID driver text retained mixed cell")
		}
	}
	scanner := orm.NullableUUIDScanner{UUID: uuid.UUID{1}, Valid: true}
	if err := (uuidDestination{destination: &scanner}).Scan(nil); err != nil || scanner.Valid || scanner.UUID != (uuid.UUID{}) {
		t.Fatal("native UUID NULL did not reset scanner")
	}
	metadata := ir.Field{Name: "reference", GoName: "Reference", Column: "reference", Kind: ir.FieldUUID, Nullable: true}
	if ddl, err := compilePostgresMigrationColumn(metadata); err != nil || ddl != `"reference" UUID NULL` {
		t.Fatal("native UUID declaration", ddl, err)
	}
	column := postgresMigrationColumnCatalog{attributeNumber: 1, name: "reference", typeSchema: "pg_catalog", typeName: "uuid", typeModifier: -1, defaultCollation: true}
	if err := assertPostgresMigrationColumnCatalog(column, metadata); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*postgresMigrationColumnCatalog){
		func(c *postgresMigrationColumnCatalog) { c.typeName = "text" }, func(c *postgresMigrationColumnCatalog) { c.typeModifier = 32 },
		func(c *postgresMigrationColumnCatalog) { c.typeSchema = "foreign" }, func(c *postgresMigrationColumnCatalog) { c.notNull = true },
		func(c *postgresMigrationColumnCatalog) { c.hasDefault = true }, func(c *postgresMigrationColumnCatalog) { c.defaultCollation = false },
	} {
		bad := column
		change(&bad)
		if err := assertPostgresMigrationColumnCatalog(bad, metadata); err == nil {
			t.Fatal("UUID catalog drift accepted")
		}
	}
}
