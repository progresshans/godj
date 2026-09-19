package queryplan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// EmptyRows supplies the result of a compiled query whose source is provably
// empty. It must be called only after full backend plan/context validation.
// Aggregate cells are limited to the implemented COUNT/MIN/MAX algebra.
func EmptyRows(ctx context.Context, shape query.ResultShape) (db.Rows, error) {
	return emptyRows(ctx, nil, shape)
}

// EmptyRowsInSession also binds a synthetic cursor to the transaction lifetime.
// The query context may be detached from the transaction's caller context.
func EmptyRowsInSession(ctx, lifetime context.Context, shape query.ResultShape) (db.Rows, error) {
	if lifetime == nil {
		return nil, invalidPlan("empty session rows require a transaction lifetime")
	}
	return emptyRows(ctx, lifetime, shape)
}

func emptyRows(ctx, lifetime context.Context, shape query.ResultShape) (db.Rows, error) {
	if ctx == nil {
		return nil, invalidPlan("empty rows require a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := &emptySourceRows{ctx: ctx, lifetime: lifetime}
	if err := rows.contextError(); err != nil {
		return nil, err
	}
	switch shape.Kind() {
	case query.ResultModel, query.ResultProjection:
		return rows, nil
	case query.ResultAggregate:
		for _, expression := range shape.Expressions() {
			switch expression.Kind() {
			case query.ResultCountAll:
				rows.values = append(rows.values, int64(0))
			case query.ResultMin, query.ResultMax:
				rows.values = append(rows.values, nil)
			default:
				return nil, invalidPlan("empty source has an unsupported aggregate")
			}
		}
		if len(rows.values) == 0 {
			return nil, invalidPlan("empty source has no aggregate expressions")
		}
		return rows, nil
	default:
		return nil, invalidPlan("empty source has an unsupported result shape")
	}
}

type emptySourceRows struct {
	mu       sync.Mutex
	ctx      context.Context
	lifetime context.Context
	values   []any
	active   bool
	seen     bool
	closed   bool
	err      error
}

func (rows *emptySourceRows) Next() bool {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	if rows.closed {
		return false
	}
	if rows.err = rows.contextError(); rows.err != nil {
		rows.closeLocked()
		return false
	}
	if !rows.seen && len(rows.values) != 0 {
		rows.seen, rows.active = true, true
		return true
	}
	rows.closeLocked()
	return false
}

func (rows *emptySourceRows) Scan(destinations ...any) error {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	if !rows.active || rows.closed {
		return errors.New("scan requires an active result row")
	}
	if err := rows.contextError(); err != nil {
		rows.err = err
		return err
	}
	if len(destinations) != len(rows.values) {
		return errors.New("scan destination count differs from aggregate columns")
	}
	for index, destination := range destinations {
		if err := scanEmptyAggregate(destination, rows.values[index]); err != nil {
			return fmt.Errorf("scan empty aggregate column %d: %w", index, err)
		}
	}
	return nil
}

func (rows *emptySourceRows) Err() error {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	if !rows.closed && rows.err == nil {
		rows.err = rows.contextError()
	}
	return rows.err
}

func (rows *emptySourceRows) Close() error {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	rows.closeLocked()
	return nil
}

func (rows *emptySourceRows) closeLocked() { rows.active, rows.closed = false, true }

func (rows *emptySourceRows) contextError() error {
	if err := rows.ctx.Err(); err != nil {
		return err
	}
	if rows.lifetime != nil {
		return context.Cause(rows.lifetime)
	}
	return nil
}

// This conversion only receives int64(0) or nil. Preserve database/sql's
// Scanner, pointer, numeric, textual and NULL behavior for those two values
// without creating a second SQL connection merely to scan a known result.
func scanEmptyAggregate(destination, value any) error {
	pointer := reflect.ValueOf(destination)
	if !pointer.IsValid() || pointer.Kind() == reflect.Pointer && pointer.IsNil() {
		return errors.New("scan destination must be non-nil")
	}
	if scanner, ok := destination.(interface{ Scan(any) error }); ok {
		return scanner.Scan(value)
	}
	switch target := destination.(type) {
	case *any:
		*target = value
		return nil
	case *[]byte:
		*target = nil
		if value != nil {
			*target = []byte("0")
		}
		return nil
	case *sql.RawBytes:
		*target = nil
		if value != nil {
			*target = []byte("0")
		}
		return nil
	case *string:
		if value != nil {
			*target = "0"
			return nil
		}
	case *bool:
		if value != nil {
			*target = false
			return nil
		}
	}
	if pointer.Kind() != reflect.Pointer || pointer.IsNil() {
		return errors.New("scan destination must be a non-nil pointer")
	}
	target := pointer.Elem()
	if value == nil {
		switch target.Kind() {
		case reflect.Pointer:
			target.SetZero()
			return nil
		}
		return errors.New("cannot scan NULL into a non-nullable destination")
	}
	switch target.Kind() {
	case reflect.Interface:
		if reflect.TypeOf(value).AssignableTo(target.Type()) {
			target.Set(reflect.ValueOf(value))
			return nil
		}
	case reflect.Pointer:
		target.Set(reflect.New(target.Type().Elem()))
		return scanEmptyAggregate(target.Interface(), value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		target.SetInt(0)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		target.SetUint(0)
		return nil
	case reflect.Float32, reflect.Float64:
		target.SetFloat(0)
		return nil
	}
	return errors.New("unsupported empty aggregate scan destination")
}
