package sqlite

import (
	"strings"
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
	defaultValue := &ir.Scalar{Kind: ir.ScalarBoolean, Boolean: false}
	defaultAdd, err := compileMigrationAddField(model, ir.Field{
		Name: "featured", GoName: "Featured", Column: "featured",
		Kind: ir.FieldBoolean, Default: defaultValue,
	})
	if err != nil {
		t.Fatalf("compile default-bearing AddField: %v", err)
	}
	if strings.Contains(strings.ToUpper(defaultAdd), "DEFAULT") {
		t.Fatalf("default-bearing AddField SQL contains persistent DEFAULT: %q", defaultAdd)
	}
	if want := `ALTER TABLE "godj_migration_article" ADD COLUMN "featured" BOOLEAN NOT NULL`; defaultAdd != want {
		t.Fatalf("default-bearing AddField SQL = %q, want %q", defaultAdd, want)
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
		{name: "boolean wrong default", field: ir.Field{Column: "value", Kind: ir.FieldBoolean, Nullable: true, Default: &ir.Scalar{Kind: ir.ScalarInteger}}},
		{name: "invalid auto", field: ir.Field{Column: "id", Kind: ir.FieldAuto}},
		{name: "integer primary key", field: ir.Field{Column: "value", Kind: ir.FieldInteger, PrimaryKey: true}},
		{name: "integer length", field: ir.Field{Column: "value", Kind: ir.FieldInteger, MaxLength: 1}},
		{name: "integer wrong default", field: ir.Field{Column: "value", Kind: ir.FieldInteger, Default: &ir.Scalar{Kind: ir.ScalarBoolean}}},
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

func TestIntegerMigrationColumnIsSignedStorageWithoutPersistentDefault(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		field := ir.Field{Name: "amount", GoName: "Amount", Column: "amount", Kind: ir.FieldInteger, Nullable: nullable, Default: &ir.Scalar{Kind: ir.ScalarInteger, Integer: -9223372036854775808}}
		statement, err := compileMigrationColumn(field)
		want := `"amount" BIGINT NOT NULL`
		if nullable {
			want = `"amount" BIGINT NULL`
		}
		if err != nil || statement != want {
			t.Fatalf("integer column = %q, %v; want %q", statement, err, want)
		}
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

func TestTextMigrationColumnHasNoLengthOrPersistentDefault(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		field := ir.Field{Name: "body", GoName: "Body", Column: "body", Kind: ir.FieldText, Nullable: nullable, Default: &ir.Scalar{Kind: ir.ScalarString, String: "line one\nline two"}}
		statement, err := compileMigrationColumn(field)
		want := `"body" TEXT NOT NULL`
		if nullable {
			want = `"body" TEXT NULL`
		}
		if err != nil || statement != want {
			t.Fatalf("Text DDL = %q, %v; want %q", statement, err, want)
		}
		for _, mutate := range []func(*ir.Field){
			func(f *ir.Field) { f.MaxLength = 10 },
			func(f *ir.Field) { f.PrimaryKey = true },
			func(f *ir.Field) { f.Default = &ir.Scalar{Kind: ir.ScalarInteger} },
		} {
			invalid := field
			mutate(&invalid)
			if _, err := compileMigrationColumn(invalid); err == nil {
				t.Fatal("invalid Text metadata accepted by migration compiler")
			}
		}
	}
}

func TestBooleanMigrationColumnKeepsNullabilityWithoutPersistentDefault(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		for _, value := range []bool{false, true} {
			field := ir.Field{Name: "reviewed", GoName: "Reviewed", Column: "reviewed", Kind: ir.FieldBoolean, Nullable: nullable, Default: &ir.Scalar{Kind: ir.ScalarBoolean, Boolean: value}}
			statement, err := compileMigrationColumn(field)
			want := `"reviewed" BOOLEAN NOT NULL`
			if nullable {
				want = `"reviewed" BOOLEAN NULL`
			}
			if err != nil || statement != want {
				t.Fatalf("Boolean column=%q err=%v want=%q", statement, err, want)
			}
		}
	}
}
