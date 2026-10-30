package orm

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type manyRowsSession struct {
	db.RelationSession
	rows  db.Rows
	err   error
	calls int
}

func (s *manyRowsSession) Query(context.Context, query.Plan) (db.Rows, error) {
	s.calls++
	return s.rows, s.err
}

type manyRowsProbe struct {
	values                    [][]any
	index, closed             int
	scanErr, rowErr, closeErr error
	next                      func()
}

func (r *manyRowsProbe) Next() bool {
	if r.next != nil {
		r.next()
	}
	r.index++
	return r.index <= len(r.values)
}
func (r *manyRowsProbe) Scan(destinations ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	for index, value := range r.values[r.index-1] {
		*destinations[index].(*any) = value
	}
	return nil
}
func (r *manyRowsProbe) Err() error   { return r.rowErr }
func (r *manyRowsProbe) Close() error { r.closed++; return r.closeErr }

func TestManyToManyRowsPreserveErrorsAndRejectUnownedRows(t *testing.T) {
	state := &manyToManyState{model: ir.Model{DBTable: "links"}, key: ir.Field{Name: "id", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}, source: ir.Field{Name: "source", Column: "source_id", Kind: ir.FieldForeignKey, Nullable: true}, target: ir.Field{Name: "target", Column: "target_id", Kind: ir.FieldForeignKey, Nullable: true}}
	queryErr, scanErr, rowErr, closeErr := errors.New("query"), errors.New("scan"), errors.New("rows"), errors.New("close")
	for _, mode := range []string{"nil", "typed_nil", "query", "scan", "rows", "close", "wrong_key", "wrong_source", "wrong_target", "outside_owner", "duplicate_key", "cancel_next", "cancel_before", "nullable_zero"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rows := &manyRowsProbe{values: [][]any{{int64(0), int64(0), int64(2)}}}
			session := &manyRowsSession{rows: rows}
			var expected []error
			switch mode {
			case "nil":
				session.rows = nil
			case "typed_nil":
				session.rows = (*manyRowsProbe)(nil)
			case "query":
				session.err = queryErr
				rows.closeErr = closeErr
				expected = []error{queryErr, closeErr}
			case "scan":
				rows.scanErr = scanErr
				rows.rowErr = rowErr
				rows.closeErr = closeErr
				expected = []error{scanErr, rowErr, closeErr}
			case "rows":
				rows.rowErr = rowErr
				expected = []error{rowErr}
			case "close":
				rows.closeErr = closeErr
				expected = []error{closeErr}
			case "wrong_key":
				rows.values[0][0] = nil
			case "wrong_source":
				rows.values[0][1] = "0"
			case "wrong_target":
				rows.values[0][2] = "2"
			case "outside_owner":
				rows.values[0][1] = int64(3)
			case "duplicate_key":
				rows.values = append(rows.values, rows.values[0])
			case "cancel_next":
				rows.next = cancel
				expected = []error{context.Canceled}
			case "cancel_before":
				cancel()
				expected = []error{context.Canceled}
			case "nullable_zero":
				rows.values[0][2] = nil
			}
			result, err := state.rows(ctx, session, 0)
			if mode == "nullable_zero" {
				if err != nil || len(result) != 1 || result[0].key != 0 || !result[0].sourcePresent || result[0].targetPresent {
					t.Fatal(result, err)
				}
			} else if err == nil || result != nil {
				t.Fatal("malformed/error rows published", result, err)
			}
			for _, cause := range expected {
				if !errors.Is(err, cause) {
					t.Fatal("cause lost", cause, err)
				}
			}
			if mode == "nil" || mode == "typed_nil" || mode == "cancel_before" {
				if rows.closed != 0 {
					t.Fatal("closed absent/unowned rows")
				}
			} else if rows.closed != 1 {
				t.Fatal("rows not closed exactly once", rows.closed)
			}
			if mode == "cancel_before" && session.calls != 0 {
				t.Fatal("canceled input reached backend")
			}
		})
	}
}
