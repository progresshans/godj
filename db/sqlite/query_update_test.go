package sqlite

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/queryupdatetest"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

func TestSQLiteQueryUpdateCompiler(t *testing.T) {
	queryupdatetest.CheckCompiler(t, CompileQueryUpdate, false)
}

func TestSQLiteTemporalReadsAndUpdates(t *testing.T) {
	backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "temporal-expressions.sqlite"), "")
	queryupdatetest.CheckTemporal(t, backend, backend.database, func(name string) string {
		quoted, err := quoteIdentifier(name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, false, func(t *testing.T, err error) {
		t.Helper()
		var native *modernsqlite.Error
		if !errors.As(err, &native) || (!errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue}) && native.Error() != "SQL logic error: integer overflow (1)") {
			t.Fatal("temporal failure lost its field classification or native cause", err)
		}
	})
}
func TestSQLiteNativeQueryUpdateAndExpressions(t *testing.T) {
	backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "query-update.sqlite"), "&_pragma=busy_timeout(5000)")
	queryupdatetest.Check(t, backend, backend.database, func(name string) string {
		quoted, err := quoteIdentifier(name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, false)
}
func TestSQLiteQueryUpdateScalarCodecs(t *testing.T) {
	backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "query-update-codecs.sqlite"), "")
	queryupdatetest.CheckCodecs(t, backend, backend.database, func(name string) string {
		quoted, err := quoteIdentifier(name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, false)
}
