package relationbinding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

type deleteMutationMetrics struct {
	Transactions     int
	UpdateStatements int
	UpdatedRows      int64
	DeleteStatements int
	DeletedRows      int64
	Order            []string
	Committed        bool
}

func setNullDeleteCandidate(ctx context.Context, database *sql.DB, targetID int64, afterUpdate func(context.Context, *sql.Tx) error) (metrics deleteMutationMetrics, returnedError error) {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return metrics, fmt.Errorf("begin SET_NULL candidate: %w", err)
	}
	metrics.Transactions = 1
	defer func() {
		if !metrics.Committed {
			_ = transaction.Rollback()
		}
	}()

	result, err := transaction.ExecContext(ctx, `UPDATE blog_post SET reviewer_id = NULL WHERE reviewer_id = ?`, targetID)
	if err != nil {
		return metrics, fmt.Errorf("update SET_NULL source rows: %w", err)
	}
	metrics.UpdateStatements = 1
	metrics.Order = append(metrics.Order, "UPDATE")
	metrics.UpdatedRows, err = result.RowsAffected()
	if err != nil {
		return metrics, fmt.Errorf("read SET_NULL affected rows: %w", err)
	}
	if afterUpdate != nil {
		if err := afterUpdate(ctx, transaction); err != nil {
			return metrics, err
		}
	}

	result, err = transaction.ExecContext(ctx, `DELETE FROM authors_author WHERE id = ?`, targetID)
	if err != nil {
		return metrics, fmt.Errorf("delete relation target: %w", err)
	}
	metrics.DeleteStatements = 1
	metrics.Order = append(metrics.Order, "DELETE")
	metrics.DeletedRows, err = result.RowsAffected()
	if err != nil {
		return metrics, fmt.Errorf("read delete affected rows: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return metrics, fmt.Errorf("commit SET_NULL candidate: %w", err)
	}
	metrics.Committed = true
	return metrics, nil
}

func TestSetNullDeleteFaultRollsBackUpdateAndDeleteTogether(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := newRelationSQLite(t)
	fault := errors.New("injected target delete fault")
	metrics, err := setNullDeleteCandidate(ctx, database, 2, func(ctx context.Context, transaction *sql.Tx) error {
		var nullReviewers int
		if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM blog_post WHERE reviewer_id IS NULL`).Scan(&nullReviewers); err != nil {
			return fmt.Errorf("observe interim update: %w", err)
		}
		if nullReviewers != 3 {
			return fmt.Errorf("interim null reviewer rows = %d, want 3", nullReviewers)
		}
		var targetRows int
		if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM authors_author WHERE id = 2`).Scan(&targetRows); err != nil {
			return fmt.Errorf("observe target before injected delete fault: %w", err)
		}
		if targetRows != 1 {
			return fmt.Errorf("target rows before fault = %d, want 1", targetRows)
		}
		return fault
	})
	if !errors.Is(err, fault) {
		t.Fatalf("fault result = %v, want injected fault", err)
	}
	if metrics.Transactions != 1 || metrics.UpdateStatements != 1 || metrics.UpdatedRows != 2 || metrics.DeleteStatements != 0 || metrics.DeletedRows != 0 || metrics.Committed {
		t.Fatalf("fault metrics = %#v", metrics)
	}
	if got, want := metrics.Order, []string{"UPDATE"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fault mutation order = %v, want %v", got, want)
	}
	assertRelationDatabaseState(t, database, []int64{1, 2, 3}, map[int64]*int64{10: int64Pointer(2), 11: nil, 12: int64Pointer(2)})

	success, err := setNullDeleteCandidate(ctx, database, 2, nil)
	if err != nil {
		t.Fatalf("successful SET_NULL candidate: %v", err)
	}
	if success.Transactions != 1 || success.UpdateStatements != 1 || success.UpdatedRows != 2 || success.DeleteStatements != 1 || success.DeletedRows != 1 || !success.Committed {
		t.Fatalf("success metrics = %#v", success)
	}
	if got, want := success.Order, []string{"UPDATE", "DELETE"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("success mutation order = %v, want %v", got, want)
	}
	assertRelationDatabaseState(t, database, []int64{1, 3}, map[int64]*int64{10: nil, 11: nil, 12: nil})
}

func newRelationSQLite(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "relationbinding.sqlite"))
	if err != nil {
		t.Fatalf("open relation SQLite: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close relation SQLite: %v", err)
		}
	})
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE authors_author (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE blog_post (id INTEGER PRIMARY KEY, title TEXT NOT NULL, author_id INTEGER NOT NULL REFERENCES authors_author(id), reviewer_id INTEGER NULL REFERENCES authors_author(id))`,
		`INSERT INTO authors_author(id, name) VALUES (1, 'Ada'), (2, 'Bob'), (3, 'Cleo')`,
		`INSERT INTO blog_post(id, title, author_id, reviewer_id) VALUES (10, 'Alpha', 1, 2), (11, 'Beta', 1, NULL), (12, 'Gamma', 3, 2)`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("execute relation fixture %q: %v", statement, err)
		}
	}
	var foreignKeys int
	if err := database.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("SQLite foreign keys = %d, err=%v", foreignKeys, err)
	}
	if _, err := database.Exec(`INSERT INTO blog_post(id, title, author_id) VALUES (99, 'Orphan', 999)`); err == nil {
		t.Fatal("actual SQLite foreign key accepted orphan row")
	}
	return database
}

func assertRelationDatabaseState(t *testing.T, database *sql.DB, wantAuthors []int64, wantReviewers map[int64]*int64) {
	t.Helper()
	rows, err := database.Query(`SELECT id FROM authors_author ORDER BY id`)
	if err != nil {
		t.Fatalf("query authors state: %v", err)
	}
	var authors []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan author state: %v", err)
		}
		authors = append(authors, id)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close author state: %v", err)
	}
	if !reflect.DeepEqual(authors, wantAuthors) {
		t.Fatalf("authors state = %v, want %v", authors, wantAuthors)
	}

	rows, err = database.Query(`SELECT id, reviewer_id FROM blog_post ORDER BY id`)
	if err != nil {
		t.Fatalf("query post state: %v", err)
	}
	gotReviewers := make(map[int64]*int64)
	for rows.Next() {
		var id int64
		var reviewer sql.NullInt64
		if err := rows.Scan(&id, &reviewer); err != nil {
			rows.Close()
			t.Fatalf("scan post state: %v", err)
		}
		if reviewer.Valid {
			value := reviewer.Int64
			gotReviewers[id] = &value
		} else {
			gotReviewers[id] = nil
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close post state: %v", err)
	}
	if !equalNullableIDs(gotReviewers, wantReviewers) {
		t.Fatalf("reviewer state = %s, want %s", formatNullableIDs(gotReviewers), formatNullableIDs(wantReviewers))
	}
}

func equalNullableIDs(left, right map[int64]*int64) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		rightValue, exists := right[key]
		if !exists || (leftValue == nil) != (rightValue == nil) {
			return false
		}
		if leftValue != nil && *leftValue != *rightValue {
			return false
		}
	}
	return true
}

func formatNullableIDs(values map[int64]*int64) string {
	return fmt.Sprint(values)
}

func int64Pointer(value int64) *int64 {
	return &value
}
