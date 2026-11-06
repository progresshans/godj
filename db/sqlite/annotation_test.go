package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/querytest"
)

func TestSQLiteNamedAnnotations(t *testing.T) {
	backend := openLifecycleFileBackend(t, filepath.Join(t.TempDir(), "annotations.sqlite"), "")
	querytest.AnnotationNative(t, backend, backend.database, func(name string) string {
		quoted, err := quoteIdentifier(name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, false)
}
