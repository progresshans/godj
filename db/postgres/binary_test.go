package postgres

import (
	"testing"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestPostgresBinaryNativeParametersAndCatalog(t *testing.T) {
	for _, data := range []string{"", "\x00\xffa\x80", "'\\;"} {
		parameter, err := postgresValue(query.Binary(binaryvalue.Value{Data: data}))
		if bytes, ok := parameter.([]byte); err != nil || !ok || bytes == nil || string(bytes) != data {
			t.Fatal("binary parameter became text or NULL", err)
		}
	}
	metadata := ir.Field{Name: "payload", GoName: "Payload", Column: "payload", Kind: ir.FieldBinary, Nullable: true, MaxLength: 4}
	if ddl, err := compilePostgresMigrationColumn(metadata); err != nil || ddl != `"payload" BYTEA NULL` {
		t.Fatal(ddl, err)
	}
	column := postgresMigrationColumnCatalog{attributeNumber: 1, name: "payload", typeSchema: "pg_catalog", typeName: "bytea", typeModifier: -1, defaultCollation: true}
	if err := assertPostgresMigrationColumnCatalog(column, metadata); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*postgresMigrationColumnCatalog){
		func(c *postgresMigrationColumnCatalog) { c.typeName = "text" }, func(c *postgresMigrationColumnCatalog) { c.typeModifier = 4 },
		func(c *postgresMigrationColumnCatalog) { c.typeSchema = "foreign" }, func(c *postgresMigrationColumnCatalog) { c.notNull = true },
		func(c *postgresMigrationColumnCatalog) { c.hasDefault = true }, func(c *postgresMigrationColumnCatalog) { c.defaultCollation = false },
	} {
		bad := column
		mutate(&bad)
		if err := assertPostgresMigrationColumnCatalog(bad, metadata); err == nil {
			t.Fatal("binary catalog drift accepted")
		}
	}
	metadata.Default = &ir.Scalar{Kind: ir.ScalarBinary, Binary: "AP8="}
	if literal, err := compilePostgresMigrationDefault(metadata); err != nil || literal != `decode('00ff', 'hex')` {
		t.Fatal("binary default literal changed", literal, err)
	}
	metadata.Default.Binary = ""
	if literal, err := compilePostgresMigrationDefault(metadata); err != nil || literal != `decode('', 'hex')` {
		t.Fatal("empty binary default became NULL", literal, err)
	}
}
