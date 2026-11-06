package sqlite

import (
	"errors"
	"github.com/progresshans/godj/internal/queryupdatetest"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
	"path/filepath"
	"testing"
)

func TestSQLiteDurationScaling(t *testing.T) {
	backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "duration-scaling.sqlite"), "")
	queryupdatetest.CheckDurationScaling(t, backend, backend.database, func(name string) string {
		quoted, err := quoteIdentifier(name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, false, func(t *testing.T, err error) {
		t.Helper()
		var native *modernsqlite.Error
		if !errors.As(err, &native) || !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue}) {
			t.Fatal("invalid arithmetic lost its field classification or native cause", err)
		}
	})
}
