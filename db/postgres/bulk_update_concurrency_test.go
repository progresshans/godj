package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/bulkupdatetest"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type bulkUpdateWaitRow struct {
	ID, Amount int64
	ParentID   int64 `json:"parent_id"`
	Note       string
}

type bulkUpdateWaitCase struct {
	Blocked bool
	Count   int64
	Stored  []bulkUpdateWaitRow
}

func bulkUpdateWaitReference(t *testing.T) map[string]bulkUpdateWaitCase {
	t.Helper()
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, "codegen/consumertest/testdata/bulkupdate/bulk_update-concurrency-django61-postgres.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != "9b49fbc49cb7cf8c5dd6b34bc22a786378134e64d76fb37e6ae5eaca124c7f5c" {
		t.Fatal("bulk update lock-wait reference bytes changed")
	}
	observer, err := os.ReadFile(filepath.Join(root, "conformance/runners/django/bulk_update_concurrency_reference.py"))
	if err != nil {
		t.Fatal(err)
	}
	digest = sha256.Sum256(observer)
	if hex.EncodeToString(digest[:]) != "31b953a3e01d40441783a2d89e2878b160c16603df50ae809488f4cff610f5b1" {
		t.Fatal("bulk update lock-wait observer source changed")
	}
	var fixture struct {
		Kind, Django, Python, Postgres string
		Sources                        map[string]string
		Cases                          map[string]bulkUpdateWaitCase
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Kind != "django-bulk-update-concurrency-reference-v1" || fixture.Django != "6.1" || fixture.Python != "3.14.3" || fixture.Postgres != "17.10 (Debian 17.10-1.pgdg12+1)" || len(fixture.Cases) != 7 || !maps.Equal(fixture.Sources, map[string]string{
		"QuerySet":          "5e86af673328a1d6800342f4bec9d4e2352b7919bebef349d98962d67569eae6",
		"SQLUpdateCompiler": "38e5ed0147669e8085f00b02646e5b7436ece76f6bb399bef4acd4ae9f07feb4",
	}) {
		t.Fatal("bulk update lock-wait provenance changed")
	}
	return fixture.Cases
}

func TestPostgresBulkUpdateRechecksScalarFilterAfterNativeLockWait(t *testing.T) {
	reference := bulkUpdateWaitReference(t)
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	writer := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	updater := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	table, err := quoteTable(namespace, "update_wait_items")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := quoteTable(namespace, "update_wait_parents")
	if err != nil {
		t.Fatal(err)
	}
	child, err := quoteTable(namespace, "update_wait_children")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE " + parent + " (id BIGINT PRIMARY KEY, name TEXT NOT NULL)",
		"INSERT INTO " + parent + " VALUES (1,'allowed'),(2,'denied')",
		"CREATE TABLE " + table + " (id BIGINT PRIMARY KEY, amount BIGINT NOT NULL,note TEXT NOT NULL,parent_id BIGINT REFERENCES " + parent + "(id))",
		"CREATE TABLE " + child + " (id BIGINT PRIMARY KEY, item_id BIGINT REFERENCES " + table + "(id), name TEXT NOT NULL)",
	} {
		if _, err := writer.database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"root_changed", "root_still_matches", "root_foreign_key_changed", "joined_root_changed", "root_deleted", "trimmed_relation_root_changed", "negated_collection_root_changed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			want, found := reference[mode]
			if mode != "canceled" && (!found || !want.Blocked) {
				t.Fatal("missing independent server-confirmed lock-wait reference", mode)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			for _, statement := range []string{"DELETE FROM " + table, "INSERT INTO " + table + " VALUES (1,1,'original',1)"} {
				if _, err := writer.database.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			id, amount, parentKey, note := bulkupdatetest.ID, bulkupdatetest.Amount, bulkupdatetest.Parent, bulkupdatetest.Note
			source := query.NewPlan("update_wait_items", []query.FieldRef{id, amount, parentKey, note})
			condition := query.NewCondition(amount, query.LookupExact, query.Integer(1))
			if mode == "root_foreign_key_changed" {
				condition = query.NewCondition(parentKey, query.LookupExact, query.Integer(1))
			}
			source = bulkupdatetest.Filter(t, source, condition)
			if mode == "joined_root_changed" {
				path, err := query.NewForwardRelationPath(ir.ModelIdentity{AppLabel: "app", ModelName: "item"}, "update_wait_items", "parent", "parent_id", ir.ModelIdentity{AppLabel: "app", ModelName: "parent"}, "update_wait_parents", "id", true, bulkupdatetest.Name, ir.RelationManyToOne)
				if err != nil {
					t.Fatal(err)
				}
				source = bulkupdatetest.Filter(t, source, query.NewRelatedCondition(path, query.LookupExact, query.String("allowed")))
			}
			if mode == "trimmed_relation_root_changed" {
				path, err := query.NewForwardRelationIsNullPath(ir.ModelIdentity{AppLabel: "app", ModelName: "item"}, "update_wait_items", parentKey, ir.ModelIdentity{AppLabel: "app", ModelName: "parent"}, "update_wait_parents", "id", ir.RelationManyToOne)
				if err != nil {
					t.Fatal(err)
				}
				source = bulkupdatetest.Filter(t, source, query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(false)))
			}
			if mode == "negated_collection_root_changed" {
				path, err := query.NewReverseRelationPath(ir.ModelIdentity{AppLabel: "app", ModelName: "child"}, "update_wait_children", "item", "item_id", ir.ModelIdentity{AppLabel: "app", ModelName: "item"}, "update_wait_items", "id", "children", false, bulkupdatetest.Name, ir.RelationOneToMany)
				if err != nil {
					t.Fatal(err)
				}
				path, err = query.NewRelationChain(path.Hops(), []query.FieldRef{id, id}, bulkupdatetest.Name, query.RelationTerminalRelatedField)
				if err != nil {
					t.Fatal(err)
				}
				leaf, err := query.NewExpression(query.NewRelatedCondition(path, query.LookupExact, query.String("blocked")))
				if err != nil {
					t.Fatal(err)
				}
				not, err := query.NotExpression(leaf)
				if err != nil {
					t.Fatal(err)
				}
				source, err = source.WithWhere(not)
				if err != nil {
					t.Fatal(err)
				}
			}
			plan := bulkupdatetest.Plan(t, source, []query.FieldRef{amount}, []int64{1}, [][]query.Value{{query.Integer(11)}})
			type outcome struct {
				count int64
				err   error
			}
			started, finished := make(chan int, 1), make(chan outcome, 1)
			waitContext, stop := context.WithCancel(ctx)
			defer stop()
			running, joined := false, false
			defer func() {
				if running && !joined {
					stop()
					select {
					case <-finished:
					case <-time.After(5 * time.Second):
						t.Error("bulk update waiter did not terminate")
					}
				}
			}()
			err := writer.Atomic(ctx, func(session db.Session) error {
				tx := session.(*transactionSession).transaction
				var writerPID int
				if err := tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&writerPID); err != nil {
					return err
				}
				statement := "UPDATE " + table + " SET note='concurrent' WHERE id=1"
				switch mode {
				case "root_changed", "joined_root_changed", "trimmed_relation_root_changed", "negated_collection_root_changed":
					statement = "UPDATE " + table + " SET amount=2 WHERE id=1"
				case "root_foreign_key_changed":
					statement = "UPDATE " + table + " SET parent_id=2 WHERE id=1"
				case "root_deleted":
					statement = "DELETE FROM " + table + " WHERE id=1"
				}
				if _, err := tx.ExecContext(ctx, statement); err != nil {
					return err
				}
				running = true
				go func() {
					var count int64
					err := updater.Atomic(waitContext, func(other db.Session) error {
						var pid int
						if err := other.(*transactionSession).transaction.QueryRowContext(waitContext, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
							return err
						}
						started <- pid
						var err error
						count, err = other.(db.BulkUpdater).BulkUpdate(waitContext, plan)
						return err
					})
					finished <- outcome{count, err}
				}()
				var pid int
				select {
				case pid = <-started:
				case result := <-finished:
					joined = true
					return fmt.Errorf("waiter failed before PID: %w", result.err)
				case <-ctx.Done():
					return ctx.Err()
				}
				tick := time.NewTicker(10 * time.Millisecond)
				defer tick.Stop()
				for {
					var blocked bool
					if err := tx.QueryRowContext(ctx, "SELECT $1 = ANY(pg_blocking_pids($2))", writerPID, pid).Scan(&blocked); err != nil {
						return err
					}
					if blocked {
						if mode == "canceled" {
							stop()
						}
						return nil
					}
					select {
					case result := <-finished:
						joined = true
						return fmt.Errorf("waiter finished before server-confirmed lock wait: %w", result.err)
					case <-ctx.Done():
						return ctx.Err()
					case <-tick.C:
					}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			var result outcome
			select {
			case result = <-finished:
				joined = true
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if mode == "canceled" {
				if result.err == nil || result.count != 0 {
					t.Fatal("canceled write returned success", result)
				}
			} else if result.err != nil || result.count != want.Count {
				t.Fatal("native lock-wait count differs from Django", result.count, want.Count, result.err)
			}
			rows, err := writer.database.QueryContext(ctx, "SELECT id,amount,parent_id,note FROM "+table+" ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			stored := make([]bulkUpdateWaitRow, 0)
			for rows.Next() {
				var value bulkUpdateWaitRow
				if err := rows.Scan(&value.ID, &value.Amount, &value.ParentID, &value.Note); err != nil {
					t.Fatal(err)
				}
				stored = append(stored, value)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if mode == "canceled" {
				want.Stored = []bulkUpdateWaitRow{{ID: 1, Amount: 1, ParentID: 1, Note: "concurrent"}}
			}
			if !reflect.DeepEqual(stored, want.Stored) {
				t.Fatal("native lock-wait state differs from Django", stored, want.Stored)
			}
		})
	}
}
