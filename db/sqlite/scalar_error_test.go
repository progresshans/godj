package sqlite

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

func TestSQLiteScalarReadErrorsRetainClassificationAndNativeCause(t *testing.T) {
	for _, failure := range []string{"decimal_source", "decimal_result", "integer_result"} {
		t.Run(failure, func(t *testing.T) {
			backend, err := OpenMemory(t.Context(), t.Name())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := backend.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := backend.ExecContext(t.Context(), `CREATE TABLE scalar_error_values(id INTEGER PRIMARY KEY, amount BLOB, whole INTEGER)`); err != nil {
				t.Fatal(err)
			}
			var bad any = expressionDecimalKey(t, "12.5")
			if failure == "decimal_source" {
				bad = "not-a-decimal"
			}
			if _, err := backend.ExecContext(t.Context(), `INSERT INTO scalar_error_values VALUES(1, ?, 0), (2, ?, ?)`, expressionDecimalKey(t, "0"), bad, int64(math.MaxInt64)); err != nil {
				t.Fatal(err)
			}
			id := query.NewFieldRef("id", "id", query.FieldInteger, false)
			amount := query.NewDecimalFieldRef("amount", "amount", false, 14, 2)
			whole := query.NewFieldRef("whole", "whole", query.FieldInteger, false)
			field, literal, operator := amount, query.Decimal(decimal.Decimal{}), query.ArithmeticAdd
			if failure == "decimal_result" {
				literal, operator = query.Decimal(decimal.Decimal{Coefficient: "1", Exponent: 1000}), query.ArithmeticMultiply
			} else if failure == "integer_result" {
				field, literal = whole, query.Integer(1)
			}
			left, err := query.FieldExpression(field)
			if err != nil {
				t.Fatal(err)
			}
			right, err := query.LiteralExpression(literal)
			if err != nil {
				t.Fatal(err)
			}
			value, err := query.Arithmetic(operator, left, right)
			if err != nil {
				t.Fatal(err)
			}
			computed, err := query.ScalarResult(value)
			if err != nil {
				t.Fatal(err)
			}
			shape, err := query.NewProjectionResult(query.FieldResult(id), computed)
			if err != nil {
				t.Fatal(err)
			}
			base := query.NewPlan("scalar_error_values", []query.FieldRef{id, amount, whole}).WithOrderings(query.NewOrdering(id, query.Ascending))
			late, err := base.WithResultShape(shape)
			if err != nil {
				t.Fatal(err)
			}
			initial, err := late.WithConditions(query.NewCondition(id, query.LookupExact, query.Integer(2)))
			if err != nil {
				t.Fatal(err)
			}
			ids, err := query.NewProjectionResult(query.FieldResult(id))
			if err != nil {
				t.Fatal(err)
			}
			stream, err := base.WithResultShape(ids)
			if err != nil {
				t.Fatal(err)
			}
			assertFailure := func(t *testing.T, err error) {
				t.Helper()
				var native *modernsqlite.Error
				if !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue}) || !errors.As(err, &native) {
					t.Fatal("scalar read lost its domain or native cause", err)
				}
			}
			for _, delayed := range []bool{false, true} {
				name, plan := "initial", initial
				if delayed {
					name, plan = "iteration", late
				}
				t.Run(name, func(t *testing.T) {
					read := func(reader db.Queryer) error {
						rows, err := reader.Query(t.Context(), plan)
						if !delayed {
							if rows != nil || err == nil {
								if rows != nil {
									_ = rows.Close()
								}
								t.Fatal("invalid first row did not fail query execution", err)
							}
							return err
						}
						if err != nil || rows == nil {
							t.Fatal("iteration probe failed before its valid first row", err)
						}
						defer rows.Close()
						var key int64
						var value any
						if !rows.Next() || rows.Scan(&key, &value) != nil || key != 1 {
							t.Fatal("valid first scalar row was lost", rows.Err())
						}
						if rows.Next() {
							t.Fatal("invalid later scalar row was published")
						}
						return errors.Join(rows.Err(), rows.Close())
					}
					assertFailure(t, read(backend))
					for _, atomic := range []func(context.Context, func(db.Session) error) error{
						backend.Atomic, backend.CoordinatedAtomic,
						func(ctx context.Context, work func(db.Session) error) error {
							return backend.AtomicRelation(ctx, func(session db.RelationSession) error { return work(session) })
						},
						func(ctx context.Context, work func(db.Session) error) error {
							return backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return work(session) })
						},
					} {
						assertFailure(t, atomic(t.Context(), func(session db.Session) error { return read(session) }))
					}
					// A batch source can fail during iteration before its first full
					// batch. Its row and connection cleanup must retain the cause.
					scanned, yielded := 0, 0
					err := backend.QueryBatches(t.Context(), plan, 2, func(row db.Row) error {
						scanned++
						var key int64
						var value any
						return row.Scan(&key, &value)
					}, func(db.Queryer) (bool, error) { yielded++; return true, nil })
					assertFailure(t, err)
					wantScanned := 0
					if delayed {
						wantScanned = 1
					}
					if scanned != wantScanned || yielded != 0 {
						t.Fatal("incomplete failing scalar batch was published", scanned, yielded)
					}
					// Callback reads use a separate stream lease, including the
					// relation transaction's native-row bookkeeping wrapper.
					for _, transaction := range []bool{false, true} {
						err := backend.QueryBatches(t.Context(), stream, 1, func(row db.Row) error { var key int64; return row.Scan(&key) }, func(reader db.Queryer) (bool, error) {
							if transaction {
								return false, reader.(db.RelationAtomic).AtomicRelation(t.Context(), func(session db.RelationSession) error { return read(session) })
							}
							return false, read(reader)
						})
						assertFailure(t, err)
					}
					var count int
					if err := backend.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM scalar_error_values`).Scan(&count); err != nil || count != 2 {
						t.Fatal("failed reads changed rows or leaked their connection", count, err)
					}
				})
			}
		})
	}
}

func TestSQLiteScalarErrorClassificationRejectsUnstructuredLookalikes(t *testing.T) {
	for _, err := range []error{nil, context.Canceled, errors.New(invalidIntegerExpression), errors.New(invalidDecimalExpression), errors.New(invalidTemporalExpression), errCalendarOperand, errors.New(errDecimalAggregateInput.Error())} {
		if got := classifySQLiteScalarError(err); got != err {
			t.Fatal("unstructured text was classified or cancellation was replaced", got)
		}
	}
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, message := range []string{invalidIntegerExpression, invalidDecimalExpression, invalidTemporalExpression, errCalendarOperand.Error(), errDecimalAggregateInput.Error()} {
		name, err := quoteIdentifier(message)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		err = backend.database.QueryRowContext(t.Context(), "SELECT * FROM "+name).Scan(&value)
		var native *modernsqlite.Error
		if !errors.As(err, &native) {
			t.Fatal("missing-table control did not produce a native error", err)
		}
		if got := classifySQLiteScalarError(err); errors.Is(got, &query.Error{Code: query.CodeInvalidValue}) || !errors.Is(got, err) {
			t.Fatal("native identifier text impersonated a scalar function error", got)
		}
	}
}
