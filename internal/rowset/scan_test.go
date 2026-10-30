package rowset_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/rowset"
	"github.com/progresshans/godj/query"
)

type rows struct {
	db.Rows
	iteration, close error
	closed, checks   int
	next             bool
}

func (rows *rows) Next() bool   { result := rows.next; rows.next = false; return result }
func (rows *rows) Err() error   { rows.checks++; return rows.iteration }
func (rows *rows) Close() error { rows.closed++; return rows.close }

type reader struct {
	rows    db.Rows
	failure error
}

func (reader reader) Query(context.Context, query.Plan) (db.Rows, error) {
	return reader.rows, reader.failure
}

func TestBoundedScanRetainsIterationAndCleanupAfterEarlyFailure(t *testing.T) {
	iteration, close, primary := errors.New("iteration"), errors.New("close"), errors.New("primary")
	for _, mode := range []string{"query", "scan", "short", "cancel", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			value := &rows{iteration: iteration, close: close, next: true}
			owner := reader{rows: value}
			if mode == "query" {
				owner.failure = primary
			}
			var result error
			var panicValue any
			func() {
				defer func() { panicValue = recover() }()
				result = rowset.Scan(ctx, owner, query.Plan{}, func(err error) error { return err }, func(db.Rows) (bool, error) {
					switch mode {
					case "scan":
						return false, primary
					case "cancel":
						cancel()
					case "panic":
						panic(primary)
					}
					return false, nil
				})
			}()
			if value.closed != 1 || value.checks != 1 {
				t.Fatal("early scan missed cleanup", value.closed, value.checks)
			}
			if mode == "panic" {
				if panicValue != primary {
					t.Fatal("scan panic changed")
				}
				return
			}
			if !errors.Is(result, iteration) || !errors.Is(result, close) {
				t.Fatal("iteration/cleanup cause lost", result)
			}
			if (mode == "query" || mode == "scan") && !errors.Is(result, primary) {
				t.Fatal("primary cause lost", result)
			}
			if mode == "cancel" && !errors.Is(result, context.Canceled) {
				t.Fatal("cancellation cause lost", result)
			}
		})
	}
}
