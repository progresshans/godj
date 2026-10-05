package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/queryupdatetest"
)

func TestSQLiteQueryUpdateCompiler(t *testing.T) {
	queryupdatetest.CheckCompiler(t, CompileQueryUpdate, false)
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
