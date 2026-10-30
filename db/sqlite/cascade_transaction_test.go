package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	sqlitedriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestSQLiteCascadeDeferredCommitFailureDoesNotReturnPendingWritesToPool(t *testing.T) {
	for _, mode := range []string{"ordinary", "relation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			backend := openMigrationHistoryFileBackend(t, filepath.Join(t.TempDir(), "deferred.sqlite"))
			backend.database.SetMaxOpenConns(1)
			for _, statement := range []string{
				`CREATE TABLE cascade_parent (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
				`CREATE TABLE cascade_child (id INTEGER PRIMARY KEY AUTOINCREMENT, parent_id INTEGER NOT NULL REFERENCES cascade_parent(id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED)`,
			} {
				if _, err := backend.database.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			key := query.NewFieldRef("id", "id", query.FieldInteger, false)
			parent := query.NewFieldRef("parent", "parent_id", query.FieldInteger, false)
			insert := query.NewInsertPlanReturningKey("cascade_child", []query.Assignment{
				query.NewAssignment(parent, query.Integer(404)),
			}, key)
			callback := func(session db.Session) error {
				id, err := session.Insert(ctx, insert)
				if err == nil && id <= 0 {
					return errors.New("deferred insert did not execute before COMMIT")
				}
				return err
			}
			var err error
			if mode == "ordinary" {
				err = backend.Atomic(ctx, callback)
			} else {
				err = backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
			}
			var failure *query.Error
			if !errors.As(err, &failure) || failure.Code != query.CodeCommitOutcomeUnknown {
				t.Fatal("deferred native commit failure lost outcome ownership", err)
			}
			var native *sqlitedriver.Error
			if !errors.As(err, &native) || native.Code() != sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
				t.Fatal("deferred commit error lost its native FK cause", err)
			}
			var count int
			if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM cascade_child`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed COMMIT returned a connection with pending writes", count, err)
			}
			if err := backend.Atomic(ctx, func(session db.Session) error {
				_, err := session.Insert(ctx, query.NewInsertPlanReturningKey("cascade_parent", nil, key))
				return err
			}); err != nil {
				t.Fatal("failed COMMIT prevented a fresh independent transaction", err)
			}
		})
	}
}
