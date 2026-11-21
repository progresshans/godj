package sqlite_test

import (
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/internal/querytest"
)

func TestSQLitePrefetchOwnerWindowsMatchDjango(t *testing.T) {
	backend, err := sqlite.OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	querytest.CreatePrefetchWindowFixture(t, func(name string) string { return name }, "TEXT", func(statement string) error {
		_, err := backend.ExecContext(t.Context(), statement)
		return err
	})
	querytest.CheckPrefetchWindows(t, backend, sqlite.Compile, func() (uint64, uint64) { n := backend.QueryCount(); return n, n })
}
