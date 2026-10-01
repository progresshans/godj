package systemstate

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/sessions"
)

// Measure the complete gated capacity scan, including actual row transfer and
// payload validation at saturation. Fixture restoration is outside the timer.
func BenchmarkSessionCapacityDatabase(b *testing.B) {
	for _, driver := range []string{"sqlite", "postgres"} {
		for _, capacity := range []int{64, 1024, 4096} {
			for _, occupancy := range []string{"quarter", "full_live", "full_expired"} {
				b.Run(fmt.Sprintf("%s/cap_%d/%s", driver, capacity, occupancy), func(b *testing.B) {
					openBackend := sessionBenchmarkDatabase(b, driver)
					backend := openBackend()
					count := capacity
					if occupancy == "quarter" {
						count /= 4
					}
					seedSessionBenchmark(b, backend, count, occupancy == "full_expired")
					gate := &Runtime{backend: backend}
					store, err := newDurableSessionStore(gate, sessions.Limits{}, capacity)
					if err != nil {
						b.Fatal(err)
					}
					expired := sessionBenchmarkRecord(b, 0, true)
					digest, _ := sessionDigest(expired.ID())
					payload, err := encodeSessionPayload(expired, sessions.Limits{})
					if err != nil {
						b.Fatal(err)
					}
					ctx := b.Context()
					b.ReportAllocs()
					for b.Loop() {
						err := gate.withAtomic(ctx, func(session db.Session) error {
							return store.ensureCapacity(ctx, session, sessionBenchmarkTime())
						})
						if occupancy == "full_live" {
							if !errors.Is(err, &sessions.Error{Code: sessions.CodeStoreFull}) {
								b.Fatalf("full capacity: %v", err)
							}
						} else if err != nil {
							b.Fatal(err)
						}
						if occupancy == "full_expired" {
							b.StopTimer()
							if err := gate.withAtomic(ctx, func(session db.Session) error {
								_, err := session.Insert(ctx, sessionInsertPlan(digest, payload))
								return err
							}); err != nil {
								b.Fatal(err)
							}
							b.StartTimer()
						}
					}
					assertSessionBenchmarkCount(b, backend, count)
				})
			}
		}
	}
}

// Each worker owns a separately opened backend and Runtime for the same
// database/schema. Create includes its matching Delete to keep occupancy
// stable; rotate alternates two identifiers and preserves the row count.
// ns/op is amortized wall time per completed cycle, not request latency.
func BenchmarkSessionOperationsDatabase(b *testing.B) {
	for _, driver := range []string{"sqlite", "postgres"} {
		for _, count := range []int{1024, 4092} {
			for _, operation := range []string{"create_delete", "rotate"} {
				for _, workers := range []int{1, 4} {
					b.Run(fmt.Sprintf("%s/rows_%d/%s/runtimes_%d", driver, count, operation, workers), func(b *testing.B) {
						openBackend := sessionBenchmarkDatabase(b, driver)
						backend := openBackend()
						seedSessionBenchmark(b, backend, count, false)
						stores := make([]*durableSessionStore, workers)
						records := make([][2]sessions.Record, workers)
						for worker := range workers {
							var err error
							stores[worker], err = newDurableSessionStore(&Runtime{backend: openBackend()}, sessions.Limits{}, 4096)
							if err != nil {
								b.Fatal(err)
							}
							records[worker] = [2]sessions.Record{
								sessionBenchmarkRecord(b, worker, false),
								sessionBenchmarkRecord(b, count+worker, false),
							}
						}
						ctx := b.Context()
						b.ReportAllocs()
						b.ResetTimer()
						var group sync.WaitGroup
						for worker := range workers {
							group.Go(func() {
								store := stores[worker]
								pair := records[worker]
								for iteration := worker; iteration < b.N; iteration += workers {
									if operation == "create_delete" {
										if created, err := store.Create(ctx, pair[1]); err != nil || !created {
											b.Errorf("Create = %v, %v", created, err)
											return
										}
										if err := store.Delete(ctx, pair[1].ID()); err != nil {
											b.Error(err)
											return
										}
									} else {
										if _, rotated, err := store.Rotate(ctx, pair[0].ID(), pair[1]); err != nil || !rotated {
											b.Errorf("Rotate = %v, %v", rotated, err)
											return
										}
										pair[0], pair[1] = pair[1], pair[0]
									}
								}
							})
						}
						group.Wait()
						b.StopTimer()
						assertSessionBenchmarkCount(b, backend, count)
					})
				}
			}
		}
	}
}

type sessionBenchmarkBackend interface {
	Backend
	migrationbackend.RevisionFencedBackend
	Close() error
}

func sessionBenchmarkDatabase(b *testing.B, driver string) func() sessionBenchmarkBackend {
	b.Helper()
	ctx := b.Context()
	// Match the supported waiting-fence profile for concurrent success costs.
	// A zero busy timeout intentionally returns SQLITE_BUSY without retrying.
	dsn := "file:" + filepath.ToSlash(filepath.Join(b.TempDir(), "sessions.sqlite3")) + "?mode=rwc&_busy_timeout=5000"
	schema := ""
	if driver == "postgres" {
		dsn = strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
		if dsn == "" {
			if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
				b.Fatal("GODJ_REQUIRE_POSTGRES=1 requires GODJ_TEST_POSTGRES_URL")
			}
			b.Skip("GODJ_TEST_POSTGRES_URL is not configured")
		}
		admin, err := pgx.Connect(ctx, dsn)
		if err != nil {
			b.Fatal("connect session benchmark PostgreSQL database failed")
		}
		schema = fmt.Sprintf("godj_session_bench_%d", time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
		if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
			_ = admin.Close(ctx)
			b.Fatal(err)
		}
		b.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
				b.Error(err)
			}
			if err := admin.Close(cleanupCtx); err != nil {
				b.Error(err)
			}
		})
	}
	return func() sessionBenchmarkBackend {
		var backend sessionBenchmarkBackend
		var err error
		if driver == "postgres" {
			backend, err = postgres.Open(ctx, postgres.Config{URL: dsn, Schema: schema})
		} else {
			backend, err = sqlite.Open(ctx, dsn)
		}
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() {
			if err := backend.Close(); err != nil {
				b.Error(err)
			}
		})
		return backend
	}
}

func seedSessionBenchmark(b *testing.B, backend sessionBenchmarkBackend, count int, expiredFirst bool) {
	b.Helper()
	ctx := b.Context()
	loaded, _, err := migrationdefinition.Load(InitialDefinitionSource())
	if err != nil {
		b.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		b.Fatal(err)
	}
	if err := backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		for index := range count {
			record := sessionBenchmarkRecord(b, index, expiredFirst && index == 0)
			digest, err := sessionDigest(record.ID())
			if err != nil {
				return err
			}
			payload, err := encodeSessionPayload(record, sessions.Limits{})
			if err != nil {
				return err
			}
			if _, err := session.Insert(ctx, sessionInsertPlan(digest, payload)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
}

func sessionBenchmarkTime() time.Time {
	return time.Date(2026, time.September, 10, 0, 0, 0, 0, time.UTC)
}

func sessionBenchmarkRecord(b *testing.B, index int, expired bool) sessions.Record {
	b.Helper()
	bytes := sha256.Sum256([]byte(fmt.Sprintf("session-benchmark/%d", index)))
	id, err := sessions.ParseID(base64.RawURLEncoding.EncodeToString(bytes[:]))
	if err != nil {
		b.Fatal(err)
	}
	created := sessionBenchmarkTime()
	if expired {
		created = created.Add(-48 * time.Hour)
	}
	record, err := sessions.RestoreRecord(sessions.RecordSnapshot{
		ID: id, Values: map[string]string{"principal_id": "benchmark-principal", "auth_marker": strings.Repeat("a", 64)},
		CreatedAt: created, AccessedAt: created, AbsoluteExpiresAt: created.Add(24 * time.Hour), IdleExpiresAt: created.Add(time.Hour),
	}, sessions.Limits{})
	if err != nil {
		b.Fatal(err)
	}
	return record
}

func assertSessionBenchmarkCount(b *testing.B, backend Backend, want int) {
	b.Helper()
	if got, err := scanSessionInventory(b.Context(), backend, want+1); err != nil || got != want {
		b.Fatalf("final session inventory = %d, %v; want %d, nil", got, err, want)
	}
}
