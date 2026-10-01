package sqlite

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/bulkinserttest"
	"github.com/progresshans/godj/query"
)

func TestSQLiteBulkInsertCompiler(t *testing.T) {
	bulkinserttest.CheckCompiler(t, CompileBulkInsert,
		`INSERT INTO "bulk_items" ("name", "amount", "parent_id", "note") VALUES (?, ?, ?, ?), (?, ?, ?, ?)`,
		`INSERT INTO "bulk_auto" ("id") VALUES (NULL), (NULL) RETURNING "id"`, sqliteBulkParameters)
	for _, fields := range [][]query.FieldRef{
		{query.NewFieldRef("upper", "NAME", query.FieldString, false), query.NewFieldRef("lower", "name", query.FieldString, false)},
		{query.NewFieldRef("not_key", "ID", query.FieldString, false)},
	} {
		row := make([]query.Value, len(fields))
		for index := range row {
			row[index] = query.String("x")
		}
		plan, err := query.NewBulkInsertPlan("bulk_items", fields, [][]query.Value{row}, bulkinserttest.ID, query.BulkConflict{})
		if err != nil {
			t.Fatal(err)
		}
		if sql, args, err := CompileBulkInsert(plan); sql != "" || args != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("SQLite equivalent columns escaped validation", sql, args, err)
		}
	}
}

func TestSQLiteNativeBulkInsertAndScope(t *testing.T) {
	backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "bulk.sqlite"), "&_pragma=busy_timeout(5000)")
	for _, statement := range []string{
		`CREATE TABLE bulk_parent (id INTEGER PRIMARY KEY)`,
		`INSERT INTO bulk_parent VALUES (1)`,
		`CREATE TABLE bulk_items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, amount INTEGER NOT NULL CHECK(amount >= 0), parent_id INTEGER REFERENCES bulk_parent(id) DEFERRABLE INITIALLY DEFERRED, note TEXT)`,
		`CREATE TABLE bulk_auto (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
		`CREATE TRIGGER bulk_skip BEFORE INSERT ON bulk_items WHEN NEW.name='skip-native' BEGIN SELECT RAISE(IGNORE); END`,
	} {
		if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	bulkinserttest.Check(t, backend, backend.database, `"bulk_items"`, `"bulk_auto"`, false)
}
