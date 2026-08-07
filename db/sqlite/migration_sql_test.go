package sqlite

import (
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestCompileMigrationSQL(t *testing.T) {
	t.Parallel()

	model := migrationTestModel(false)
	create, err := compileMigrationCreateModel(model)
	if err != nil {
		t.Fatalf("compileMigrationCreateModel() error = %v", err)
	}
	wantCreate := `CREATE TABLE "godj_migration_article" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "title" VARCHAR(200) NOT NULL, "published" BOOLEAN NOT NULL)`
	if create != wantCreate {
		t.Fatalf("CreateModel SQL = %q, want %q", create, wantCreate)
	}

	summary := ir.Field{Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200}
	add, err := compileMigrationAddField(model, summary)
	if err != nil {
		t.Fatalf("compileMigrationAddField() error = %v", err)
	}
	wantAdd := `ALTER TABLE "godj_migration_article" ADD COLUMN "summary" VARCHAR(200) NULL`
	if add != wantAdd {
		t.Fatalf("AddField SQL = %q, want %q", add, wantAdd)
	}

	remove, err := compileMigrationRemoveField(model, summary)
	if err != nil {
		t.Fatalf("compileMigrationRemoveField() error = %v", err)
	}
	if want := `ALTER TABLE "godj_migration_article" DROP COLUMN "summary"`; remove != want {
		t.Fatalf("RemoveField SQL = %q, want %q", remove, want)
	}

	removeModel, err := compileMigrationDeleteModel(model)
	if err != nil {
		t.Fatalf("compileMigrationDeleteModel() error = %v", err)
	}
	if want := `DROP TABLE "godj_migration_article"`; removeModel != want {
		t.Fatalf("DeleteModel SQL = %q, want %q", removeModel, want)
	}
}

func TestCompileMigrationColumnRejectsUnsupportedShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field ir.Field
	}{
		{name: "empty column", field: ir.Field{Kind: ir.FieldChar, MaxLength: 1}},
		{name: "invalid char length", field: ir.Field{Column: "value", Kind: ir.FieldChar}},
		{name: "nullable boolean", field: ir.Field{Column: "value", Kind: ir.FieldBoolean, Nullable: true}},
		{name: "invalid auto", field: ir.Field{Column: "id", Kind: ir.FieldAuto}},
		{name: "unknown kind", field: ir.Field{Column: "value", Kind: ir.FieldKind("unknown")}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := compileMigrationColumn(test.field); err == nil {
				t.Fatal("compileMigrationColumn() error = nil")
			}
		})
	}
}

func migrationTestModel(withSummary bool) ir.Model {
	fields := []ir.Field{
		{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
		{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
		{Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean},
	}
	if withSummary {
		fields = append(fields, ir.Field{Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200})
	}
	return ir.Model{Name: "article", GoName: "Article", DBTable: "godj_migration_article", Fields: fields}
}
