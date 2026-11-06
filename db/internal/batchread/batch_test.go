package batchread

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
)

type faultRows struct {
	next, limit, closes  int
	rowError, closeError error
	beforeNext           func()
}

func (rows *faultRows) Next() bool {
	if rows.beforeNext != nil {
		rows.beforeNext()
	}
	rows.next++
	return rows.next <= rows.limit
}
func (rows *faultRows) Scan(...any) error { return nil }
func (rows *faultRows) Err() error {
	if rows.next > rows.limit {
		return rows.rowError
	}
	return nil
}
func (rows *faultRows) Close() error { rows.closes++; return rows.closeError }

func TestStreamDoesNotPublishFailedFinalBatchOrReadAhead(t *testing.T) {
	for _, failure := range []string{"late_row", "late_close", "stopped_close", "scan", "cancel_next", "panic"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			signal := errors.New("injected native failure")
			rows := &faultRows{limit: 3}
			scans, yields := 0, 0
			switch failure {
			case "late_row":
				rows.rowError = signal
			case "late_close", "stopped_close":
				rows.closeError = signal
			case "cancel_next":
				rows.beforeNext = cancel
			}
			var err error
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				err = Stream(ctx, nil, rows, 2, func(db.Row) error {
					scans++
					if scans == 3 && failure == "scan" {
						return signal
					}
					return nil
				}, func(db.Queryer) (bool, error) {
					yields++
					if rows.next != 2 {
						t.Fatal("read ahead before first yield", rows.next)
					}
					if failure == "panic" {
						panic(signal)
					}
					return failure != "stopped_close", nil
				}, func(ctx context.Context) error { return ctx.Err() })
			}()
			if rows.closes != 1 {
				t.Fatal("row close count", rows.closes)
			}
			switch failure {
			case "cancel_next":
				if !errors.Is(err, context.Canceled) || scans != 0 || yields != 0 {
					t.Fatal(scans, yields, err)
				}
			case "panic":
				if recovered != signal || scans != 2 || yields != 1 {
					t.Fatal(scans, yields, recovered)
				}
			default:
				if !errors.Is(err, signal) || yields != 1 {
					t.Fatal(scans, yields, err)
				}
			}
		})
	}
}
