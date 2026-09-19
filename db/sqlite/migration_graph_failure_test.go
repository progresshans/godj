package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/migrationgraphtest"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

var errSQLiteGraphInjected = errors.New("injected relation graph migration failure")

type sqliteGraphFaultBackend struct {
	*Backend
	fault *sqliteGraphFaultConnection
}

func (backend *sqliteGraphFaultBackend) OpenRevisionFencedSession(ctx context.Context) (migrationbackend.RevisionFencedSession, error) {
	session, err := backend.Backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		return nil, err
	}
	session.(*sqliteRevisionFencedSession).relationConnectionHook = func(connection migrationPinnedConnection) migrationPinnedConnection {
		backend.fault.migrationPinnedConnection = connection
		return backend.fault
	}
	return session, nil
}

type sqliteGraphFaultConnection struct {
	migrationPinnedConnection
	point        string
	discardFails bool
	off          bool
	fired        bool
	restoreRead  bool
	copies       int
	renames      int
	rawCalls     int
	closeCalls   int
	cancel       context.CancelFunc
}

func (connection *sqliteGraphFaultConnection) ExecContext(ctx context.Context, statement string, arguments ...any) (sql.Result, error) {
	trimmed := strings.TrimSpace(statement)
	suspend := trimmed == "PRAGMA foreign_keys = OFF"
	restore := trimmed == "PRAGMA foreign_keys = ON" && connection.off
	copyRows := strings.HasPrefix(trimmed, `INSERT INTO "main"."__godj_relation_`)
	rename := strings.HasPrefix(trimmed, `ALTER TABLE "main"."__godj_relation_`)
	if copyRows {
		connection.copies++
	}
	if rename {
		connection.renames++
	}
	before := !connection.fired && (connection.point == "suspend before" && suspend ||
		connection.point == "restore write" && restore ||
		connection.point == "begin" && trimmed == "BEGIN IMMEDIATE" ||
		connection.point == "first copy" && copyRows && connection.copies == 1 ||
		connection.point == "second copy" && copyRows && connection.copies == 2 ||
		connection.point == "drop" && strings.HasPrefix(trimmed, `DROP TABLE "main"."graph_a"`) ||
		connection.point == "recorder" && strings.HasPrefix(trimmed, "DELETE FROM") && strings.Contains(trimmed, "godj_migrations") ||
		connection.point == "commit before" && trimmed == "COMMIT")
	if before {
		connection.fired = true
		return nil, errSQLiteGraphInjected
	}
	result, err := connection.migrationPinnedConnection.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return result, err
	}
	if suspend {
		connection.off = true
	}
	if restore {
		connection.off = false
		connection.restoreRead = true
	}
	if !connection.fired {
		switch {
		case connection.point == "suspend after" && suspend,
			connection.point == "rename after" && rename,
			connection.point == "commit after" && trimmed == "COMMIT":
			connection.fired = true
			return nil, errSQLiteGraphInjected
		case connection.point == "cancel suspended" && suspend,
			connection.point == "cancel after copy" && copyRows:
			connection.fired = true
			connection.cancel()
		case connection.point == "invalid final FK" && rename && connection.renames == 2:
			connection.fired = true
			_, err := connection.migrationPinnedConnection.ExecContext(ctx, `UPDATE "graph_a" SET "parent_id"=999999 WHERE "id"=1`)
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func (connection *sqliteGraphFaultConnection) QueryRowContext(ctx context.Context, statement string, arguments ...any) *sql.Row {
	if statement == "PRAGMA foreign_keys" && !connection.fired && (connection.point == "suspend read" && connection.off || connection.point == "restore read" && connection.restoreRead) {
		connection.fired = true
		return connection.migrationPinnedConnection.QueryRowContext(ctx, `SELECT missing_graph_fault_column`)
	}
	return connection.migrationPinnedConnection.QueryRowContext(ctx, statement, arguments...)
}

func (connection *sqliteGraphFaultConnection) Raw(callback func(any) error) error {
	connection.rawCalls++
	if connection.discardFails {
		return fmt.Errorf("discard unavailable: %w", errSQLiteGraphInjected)
	}
	return connection.migrationPinnedConnection.Raw(callback)
}

func (connection *sqliteGraphFaultConnection) Close() error {
	connection.closeCalls++
	return connection.migrationPinnedConnection.Close()
}

func TestSQLiteHistoricalRelationGraphFailureRestoresOrQuarantinesConnection(t *testing.T) {
	for _, point := range []string{"suspend before", "suspend after", "suspend read", "begin", "first copy", "second copy", "drop", "rename after", "invalid final FK", "recorder", "commit before", "commit after", "restore write", "restore read", "cancel suspended", "cancel after copy"} {
		for _, discardFails := range []bool{false, true} {
			if discardFails && point != "restore write" && point != "suspend after" && point != "commit after" {
				continue
			}
			t.Run(fmt.Sprintf("%s/discard_failure_%t", point, discardFails), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				path := filepath.Join(t.TempDir(), "graph-failure.sqlite3")
				dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=foreign_keys(1)"}).String()
				backend, err := Open(ctx, dsn)
				if err != nil {
					t.Fatal(err)
				}
				backend.database.SetMaxOpenConns(1)
				t.Cleanup(func() { _ = backend.Close() })
				loaded, keys := migrationgraphtest.Definitions(t)
				before, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[1])))
				if err != nil {
					t.Fatal(err)
				}
				for _, statement := range []string{
					`INSERT INTO "graph_a" ("label") VALUES ('alpha')`,
					`UPDATE "graph_a" SET "parent_id"=1 WHERE "id"=1`,
					`INSERT INTO "graph_b" ("label", "a_id") VALUES ('beta', 1)`,
					`INSERT INTO "graph_c" ("label", "b_id") VALUES ('gamma', 1)`,
					`UPDATE "graph_a" SET "peer_id"=1, "tertiary_id"=1 WHERE "id"=1`,
				} {
					if _, err := backend.ExecContext(ctx, statement); err != nil {
						t.Fatal(err)
					}
				}
				attempt, cancelAttempt := context.WithCancel(ctx)
				defer cancelAttempt()
				fault := &sqliteGraphFaultConnection{point: point, discardFails: discardFails, cancel: cancelAttempt}
				wrapped := &sqliteGraphFaultBackend{Backend: backend, fault: fault}
				state, err := (migrations.Executor{Backend: wrapped}).Migrate(attempt, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[0])))
				if err == nil || !fault.fired {
					t.Fatalf("migration failure was not exercised: fired=%t error=%v", fault.fired, err)
				}
				committed := point == "restore write" || point == "restore read" || point == "commit after"
				if point != "restore write" && point != "restore read" && !state.Equal(before) {
					t.Fatal("unconfirmed/rolled-back result claimed an advanced durable state")
				}
				if point == "restore write" || point == "restore read" {
					model, ok := state.Model("graph", "a")
					if !ok || len(model.Fields) != 3 {
						t.Fatal("confirmed commit with cleanup failure lost its durable state")
					}
				}
				if discardFails {
					if fault.rawCalls != 1 || fault.closeCalls != 0 || backend.relationRetention.availabilityError() == nil {
						t.Fatal("unconfirmed cleanup returned the connection instead of quarantining it")
					}
				} else {
					var enabled int
					if err := backend.database.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
						t.Fatalf("application pool FK mode = %d: %v", enabled, err)
					}
				}
				if err := backend.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := Open(ctx, dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = reopened.Close() })
				var records, columns, validRows int
				if err := reopened.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "godj_migrations"`).Scan(&records); err != nil {
					t.Fatal(err)
				}
				if err := reopened.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('graph_a')`).Scan(&columns); err != nil {
					t.Fatal(err)
				}
				if err := reopened.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "graph_a" WHERE "id"=1 AND "label"='alpha' AND "parent_id"=1`).Scan(&validRows); err != nil {
					t.Fatal(err)
				}
				wantRecords, wantColumns := 2, 5
				if committed {
					wantRecords, wantColumns = 1, 3
				}
				if records != wantRecords || columns != wantColumns || validRows != 1 {
					t.Fatalf("reconciled state: records=%d columns=%d valid rows=%d, committed=%t", records, columns, validRows, committed)
				}
				if !committed {
					var links int
					if err := reopened.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "graph_a" WHERE "peer_id"=1 AND "tertiary_id"=1`).Scan(&links); err != nil || links != 1 {
						t.Fatal("failed migration changed cyclic relation values")
					}
				}
			})
		}
	}
}
