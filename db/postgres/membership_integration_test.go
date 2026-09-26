package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type membershipTrace struct {
	table string
	total atomic.Uint64
	reads atomic.Uint64
}

func (trace *membershipTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	trace.total.Add(1)
	if strings.HasPrefix(data.SQL, "SELECT ") && strings.Contains(data.SQL, trace.table) {
		trace.reads.Add(1)
	}
	return ctx
}
func (*membershipTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestPostgreSQLScalarMembershipMatchesReferenceAndSkipsEmptyIO(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect membership database: %v", redactConnectionError(err))
	}
	schema := fmt.Sprintf("godj_membership_%d", time.Now().UnixNano())
	quoted, err := quoteIdentifier(schema)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := admin.Close(ctx); err != nil {
			t.Error(redactConnectionError(err))
		}
	})
	table := quoted + `."entries"`
	if _, err := admin.Exec(ctx, "CREATE TABLE "+table+` (id BIGINT PRIMARY KEY, name VARCHAR(20) NOT NULL, note TEXT NULL, rank BIGINT NULL, at TIMESTAMP WITH TIME ZONE NULL, active BOOLEAN NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "INSERT INTO "+table+` VALUES
		(1, 'alpha', NULL, NULL, NULL, false),
		(2, 'beta', 'beta', -1, '2026-09-19 03:34:56.123456+00', true),
		(3, 'gamma', '', 0, '0001-01-01 00:00:00+00', false),
		(4, 'delta', 'delta', 9223372036854775807, '9999-12-31 23:59:59.999999+00', true)`); err != nil {
		t.Fatal(err)
	}
	backend, err := Open(ctx, Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	// Use the production connection profile and its physical-session guards.
	// The tracer only counts calls; no credentials or statements are retained.
	config, err := currentConnectionConfig(databaseURL)
	if err != nil {
		t.Fatal("prepare traced PostgreSQL connection")
	}
	trace := &membershipTrace{table: table}
	config.Tracer = trace
	if err := backend.database.Close(); err != nil {
		t.Fatal(err)
	}
	backend.database = stdlib.OpenDB(*config,
		stdlib.OptionAfterConnect(validateAndCloseInvalidPostgresPhysicalSession),
		stdlib.OptionResetSession(func(ctx context.Context, connection *pgx.Conn) error {
			if err := validateCurrentPostgresPhysicalSession(ctx, connection); err != nil {
				return errors.Join(driver.ErrBadConn, err)
			}
			return nil
		}),
	)

	fields := []query.FieldRef{
		query.NewFieldRef("id", "id", query.FieldInteger, false),
		query.NewFieldRef("name", "name", query.FieldString, false),
		query.NewFieldRef("note", "note", query.FieldString, true),
		query.NewFieldRef("rank", "rank", query.FieldInteger, true),
		query.NewFieldRef("at", "at", query.FieldDateTime, true),
		query.NewFieldRef("active", "active", query.FieldBoolean, false),
	}
	byName := make(map[string]query.FieldRef)
	for _, field := range fields {
		byName[field.Name()] = field
	}
	projection, err := query.NewProjectionResult(query.FieldResult(fields[0]))
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, Backend string
		Cases           []struct {
			Name, Field, Composition string
			Values                   []struct {
				Kind     string
				Integer  int64
				String   string
				Boolean  bool
				DateTime string
			}
			IDs     []int64
			Queries uint64
		}
	}
	data, err := os.ReadFile("../../orm/testdata/in-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Backend != "sqlite" || len(reference.Cases) != 120 {
		t.Fatal("independent reference roster incomplete")
	}
	must := func(value query.Expression, err error) query.Expression {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, test := range reference.Cases {
		t.Run(test.Name, func(t *testing.T) {
			values := make([]query.Value, len(test.Values))
			for index, value := range test.Values {
				switch value.Kind {
				case "null":
					values[index] = query.Null()
				case "integer":
					values[index] = query.Integer(value.Integer)
				case "string":
					values[index] = query.String(value.String)
				case "boolean":
					values[index] = query.Boolean(value.Boolean)
				case "datetime":
					instant, err := time.Parse(time.RFC3339Nano, value.DateTime)
					if err != nil {
						t.Fatal(err)
					}
					values[index] = query.DateTime(instant)
				default:
					t.Fatal("unknown independent input kind")
				}
			}
			condition, err := query.NewInCondition(byName[test.Field], values)
			if err != nil {
				t.Fatal(err)
			}
			expression := must(query.NewExpression(condition))
			switch test.Composition {
			case "plain":
			case "not":
				expression = must(query.NotExpression(expression))
			case "or":
				expression = must(query.OrExpressions(expression, must(query.NewExpression(query.NewCondition(byName["name"], query.LookupExact, query.String("alpha"))))))
			case "and":
				expression = must(query.AndExpressions(expression, must(query.NewExpression(query.NewCondition(byName["active"], query.LookupExact, query.Boolean(true))))))
			default:
				t.Fatal("unknown reference composition")
			}
			plan, err := query.NewPlan("entries", fields).WithWhere(expression)
			if err != nil {
				t.Fatal(err)
			}
			plan, err = plan.WithResultShape(projection)
			if err != nil {
				t.Fatal(err)
			}
			plan = plan.WithOrderings(query.NewOrdering(fields[0], query.Ascending))
			before, beforeTotal := trace.reads.Load(), trace.total.Load()
			rows, err := backend.Query(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, 0)
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			rowErr, closeErr := rows.Err(), rows.Close()
			if rowErr != nil || closeErr != nil {
				t.Fatalf("read membership: %v / %v", rowErr, closeErr)
			}
			if !reflect.DeepEqual(ids, test.IDs) || trace.reads.Load()-before != test.Queries {
				t.Fatalf("PostgreSQL membership = %v, model queries %d; want %v, queries %d", ids, trace.reads.Load()-before, test.IDs, test.Queries)
			}
			if test.Queries == 0 && trace.total.Load() != beforeTotal {
				t.Fatal("empty PostgreSQL query acquired a connection or executed SQL")
			}
		})
	}
	condition, err := query.NewInCondition(fields[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := query.NewPlan("entries", fields).WithConditions(condition)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := query.NewAggregateResult(query.CountAllResult(), query.MinResult(byName["rank"]), query.MaxResult(byName["at"]))
	if err != nil {
		t.Fatal(err)
	}
	empty, err = empty.WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	before := trace.total.Load()
	rows, err := backend.Query(ctx, empty)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	var minimum sql.NullInt64
	var maximum sql.NullTime
	if !rows.Next() {
		t.Fatal("empty PostgreSQL aggregate lost its row")
	}
	if err := rows.Scan(&count, &minimum, &maximum); err != nil {
		t.Fatal(err)
	}
	if count != 0 || minimum.Valid || maximum.Valid || rows.Next() || rows.Err() != nil {
		t.Fatal("empty aggregate invented a scalar or epoch")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if trace.total.Load() != before {
		t.Fatal("empty aggregate executed PostgreSQL SQL")
	}
	var retained db.Session
	if err := backend.Atomic(ctx, func(session db.Session) error {
		retained = session
		before := trace.total.Load()
		rows, err := session.Query(ctx, empty)
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			return errors.New("empty transaction aggregate lost its row")
		}
		if err := rows.Scan(&count, &minimum, &maximum); err != nil {
			return err
		}
		if trace.total.Load() != before {
			return errors.New("empty transaction query executed SQL")
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := retained.Query(ctx, empty); err == nil {
		t.Fatal("empty query accepted expired PostgreSQL session")
	}
	for _, mode := range []string{"atomic", "coordinated"} {
		t.Run("lifetime_"+mode, func(t *testing.T) {
			run := backend.Atomic
			if mode == "coordinated" {
				run = backend.CoordinatedAtomic
			}
			var retainedRows db.Rows
			if err := run(ctx, func(session db.Session) error {
				before := trace.total.Load()
				var err error
				retainedRows, err = session.Query(context.Background(), empty)
				if trace.total.Load() != before {
					return errors.New("empty session query executed SQL")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if retainedRows.Next() || !errors.Is(retainedRows.Err(), sql.ErrTxDone) {
				t.Fatal("synthetic PostgreSQL cursor outlived transaction")
			}
			if err := retainedRows.Close(); err != nil {
				t.Fatal(err)
			}
			transactionContext, cancelTransaction := context.WithCancel(ctx)
			defer cancelTransaction()
			before := trace.reads.Load()
			err := run(transactionContext, func(session db.Session) error {
				rows, err := session.Query(context.Background(), empty)
				if err != nil {
					return err
				}
				defer rows.Close()
				if !rows.Next() {
					return errors.New("empty aggregate lost its row")
				}
				cancelTransaction()
				if err := rows.Scan(&count, &minimum, &maximum); !errors.Is(err, context.Canceled) {
					return errors.New("synthetic PostgreSQL cursor ignored transaction cancellation")
				}
				if _, err := session.Query(context.Background(), empty); !errors.Is(err, context.Canceled) {
					return errors.New("detached query context bypassed PostgreSQL transaction cancellation")
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || trace.reads.Load() != before {
				t.Fatalf("canceled transaction result/model I/O: %v", err)
			}
		})
	}
	before = trace.total.Load()
	invalid := empty.WithOrderings(query.NewOrdering(query.NewFieldRef("absent", "absent", query.FieldInteger, false), query.Ascending))
	if _, err := backend.Query(ctx, invalid); err == nil {
		t.Fatal("empty query bypassed PostgreSQL ordering metadata")
	}
	canceled, cancelQuery := context.WithCancel(ctx)
	cancelQuery()
	if _, err := backend.Query(canceled, empty); !errors.Is(err, context.Canceled) {
		t.Fatal("empty PostgreSQL query bypassed cancellation")
	}
	if trace.total.Load() != before {
		t.Fatal("invalid PostgreSQL query executed SQL")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Query(ctx, empty); err == nil {
		t.Fatal("empty PostgreSQL query accepted closed backend")
	}
}
