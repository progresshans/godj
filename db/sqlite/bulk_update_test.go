package sqlite

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/bulkupdatetest"
	"github.com/progresshans/godj/query"
)

func TestSQLiteBulkUpdateCompiler(t *testing.T) {
	bulkupdatetest.CheckCompiler(t, CompileBulkUpdate, `"update_items"`, `SELECT "id" FROM "update_items" WHERE "name" = ?`, false, sqliteBulkParameters)
	upper := query.NewFieldRef("upper", "AMOUNT", query.FieldInteger, false)
	source := query.NewPlan("update_items", []query.FieldRef{bulkupdatetest.ID, bulkupdatetest.Amount, upper})
	plan := bulkupdatetest.Plan(t, source, []query.FieldRef{upper}, []int64{1}, [][]query.Value{{query.Integer(2)}})
	if sql, args, err := CompileBulkUpdate(plan); sql != "" || args != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("SQLite equivalent columns escaped validation", sql, args, err)
	}
}

func TestSQLiteNativeBulkUpdateAndScope(t *testing.T) {
	backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "bulk-update.sqlite"), "&_pragma=busy_timeout(5000)")
	for _, statement := range []string{
		`CREATE TABLE godj_bulk_targets (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO godj_bulk_targets VALUES (1,'allowed'),(2,'denied')`,
		`CREATE TABLE godj_bulk_targets_0 (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO godj_bulk_targets_0 VALUES (1,'red'),(2,'blue')`,
		`CREATE TABLE update_items (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, amount INTEGER NOT NULL CHECK(amount>=0), parent_id INTEGER REFERENCES godj_bulk_targets(id) DEFERRABLE INITIALLY DEFERRED, note TEXT)`,
		`CREATE TABLE update_links (id INTEGER PRIMARY KEY, owner_id INTEGER NOT NULL REFERENCES update_items(id), label_id INTEGER NOT NULL REFERENCES godj_bulk_targets_0(id))`,
		`CREATE TRIGGER update_skip BEFORE UPDATE ON update_items WHEN NEW.name='skip-native' BEGIN SELECT RAISE(IGNORE); END`,
	} {
		if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	bulkupdatetest.Check(t, backend, backend.database, `"update_items"`, `"update_links"`, false)
}

func TestSQLiteNativeBulkUpdateScalarCodecs(t *testing.T) {
	backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "bulk-update-types.sqlite"), "")
	bulkupdatetest.CheckCodecs(t, backend, backend.database, func(name string) string {
		quoted, err := quoteIdentifier(name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, false)
}
