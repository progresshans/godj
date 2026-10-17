package systemstate

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
)

// This measures the real row transfer/decoding at a full retention boundary,
// independently of the sparse-capacity allocation benchmark and setup work.
func BenchmarkAuditPruneFullSQLite(b *testing.B) {
	for _, capacity := range []int{10_000, 100_000} {
		b.Run(fmt.Sprintf("rows_%d", capacity), func(b *testing.B) {
			ctx := context.Background()
			dsn := "file:" + filepath.ToSlash(filepath.Join(b.TempDir(), "audit.sqlite3"))
			seed, err := sql.Open("sqlite", dsn)
			if err != nil {
				b.Fatal(err)
			}
			defer seed.Close()
			if _, err := seed.ExecContext(ctx, `CREATE TABLE "`+auditTableName+`" ("id" INTEGER PRIMARY KEY)`); err != nil {
				b.Fatal(err)
			}
			if _, err := seed.ExecContext(ctx, `WITH RECURSIVE ids(id) AS (SELECT 1 UNION ALL SELECT id+1 FROM ids WHERE id < ?) INSERT INTO "`+auditTableName+`" (id) SELECT id FROM ids`, capacity); err != nil {
				b.Fatal(err)
			}
			backend, err := sqlite.Open(ctx, dsn)
			if err != nil {
				b.Fatal(err)
			}
			defer backend.Close()
			b.ReportAllocs()
			for b.Loop() {
				if err := pruneAuditRows(ctx, backend, capacity); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
