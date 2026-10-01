package queryplan_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
	_ "modernc.org/sqlite"
)

func emptyAggregateShape(t *testing.T) query.ResultShape {
	t.Helper()
	field := query.NewFieldRef("value", "value", query.FieldInteger, true)
	shape, err := query.NewAggregateResult(query.CountAllResult(), query.MinResult(field))
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

func TestEmptyAggregateScanMatchesDatabaseSQLForZeroAndNull(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	type integer int64
	type text string
	type bytes []byte
	type boolean bool
	type emptyInterface interface{}
	for _, test := range []struct {
		name string
		make func() (any, any)
	}{
		{"integer and nullable", func() (any, any) { return new(int64), new(sql.NullInt64) }},
		{"named integer and any", func() (any, any) { return new(integer), new(any) }},
		{"numeric widths", func() (any, any) { return new(uint8), new(*int32) }},
		{"float and pointer", func() (any, any) { return new(float64), new(*string) }},
		{"boolean and nullable", func() (any, any) { return new(bool), new(sql.NullBool) }},
		{"text and bytes", func() (any, any) { return new(string), new([]byte) }},
		{"bytes and raw bytes", func() (any, any) { return new([]byte), new(sql.RawBytes) }},
		{"pointer and nullable string", func() (any, any) { return new(*int64), new(sql.NullString) }},
		{"invalid nonnullable NULL", func() (any, any) { return new(int64), new(int64) }},
		{"unsupported destination", func() (any, any) { return new([]string), new(any) }},
		{"named text conversion", func() (any, any) { return new(text), new(any) }},
		{"named byte conversion", func() (any, any) { return new(bytes), new(any) }},
		{"named bool conversion", func() (any, any) { return new(boolean), new(any) }},
		{"named interface NULL", func() (any, any) { return new(emptyInterface), new(emptyInterface) }},
		{"nonempty interface NULL", func() (any, any) { return new(int64), new(io.Reader) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := queryplan.EmptyRows(t.Context(), emptyAggregateShape(t))
			if err != nil {
				t.Fatal(err)
			}
			defer actual.Close()
			reference, err := database.QueryContext(t.Context(), "SELECT 0, NULL")
			if err != nil {
				t.Fatal(err)
			}
			defer reference.Close()
			if !actual.Next() || !reference.Next() {
				t.Fatal("empty aggregate lost its single row")
			}
			a, b := test.make()
			x, y := test.make()
			actualErr, referenceErr := actual.Scan(a, b), reference.Scan(x, y)
			if (actualErr != nil) != (referenceErr != nil) {
				t.Fatalf("scan error presence differs: %v / %v", actualErr, referenceErr)
			}
			if actualErr == nil && (!reflect.DeepEqual(a, x) || !reflect.DeepEqual(b, y)) {
				t.Fatal("scan conversion differs from database/sql")
			}
			if actual.Next() || actual.Err() != nil {
				t.Fatal("empty aggregate returned an extra row or terminal error")
			}
		})
	}
}

func TestEmptyRowsKeepCursorCancellationAndCloseBoundaries(t *testing.T) {
	shape := emptyAggregateShape(t)
	ctx, cancel := context.WithCancel(t.Context())
	rows, err := queryplan.EmptyRows(ctx, shape)
	if err != nil {
		t.Fatal(err)
	}
	var value int64
	var minimum sql.NullInt64
	if err := rows.Scan(&value, &minimum); err == nil {
		t.Fatal("scan before Next accepted")
	}
	if !rows.Next() {
		t.Fatal("missing aggregate row")
	}
	if err := rows.Scan(&value); err == nil {
		t.Fatal("wrong destination count accepted")
	}
	var nilScanner *sql.NullInt64
	if err := rows.Scan(nilScanner, &minimum); err == nil {
		t.Fatal("nil Scanner accepted")
	}
	cancel()
	if err := rows.Scan(&value, &minimum); !errors.Is(err, context.Canceled) {
		t.Fatal("scan lost cancellation")
	}
	if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal("rows lost cancellation")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := queryplan.EmptyRows(ctx, shape); !errors.Is(err, context.Canceled) {
		t.Fatal("creation lost cancellation")
	}
	if _, err := queryplan.EmptyRows(nil, shape); err == nil {
		t.Fatal("nil context accepted")
	}

	empty, err := queryplan.EmptyRows(t.Context(), query.NewPlan("entries", nil).ResultShape())
	if err != nil {
		t.Fatal(err)
	}
	var done sync.WaitGroup
	done.Go(func() { _ = empty.Close() })
	if empty.Next() {
		t.Fatal("empty model source produced a row")
	}
	_ = empty.Err()
	done.Wait()
	if err := empty.Scan(&value); err == nil {
		t.Fatal("closed empty source accepted Scan")
	}
}
