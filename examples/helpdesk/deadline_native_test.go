package helpdesk_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
)

// These capabilities belong to the owned test database, never the application.
// Inspect native storage classes and every column, including fields not read by
// the preview. Session tables can change during authenticated reads and are not
// application data; durable object audit is checked separately through Runtime.
type deadlineNative struct {
	snapshot            func(context.Context) (string, error)
	setDueAt            func(context.Context, int64, time.Time) error
	corruptDueAt        func(context.Context, int64) error
	setServiceValues    func(context.Context, int64, *calendar.Date, *clock.Time, *duration.Duration) error
	corruptServiceValue func(context.Context, int64, string) error
}

func (native deadlineNative) Snapshot(t *testing.T) string {
	t.Helper()
	value, err := native.snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func sqliteDeadlineNative(connection *sql.DB) deadlineNative {
	return deadlineNative{
		snapshot: func(ctx context.Context) (string, error) {
			tables, err := connection.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type='table' AND name LIKE 'helpdesk_%' ORDER BY name")
			if err != nil {
				return "", err
			}
			var names []string
			for tables.Next() {
				var name string
				if err := tables.Scan(&name); err != nil {
					return "", errors.Join(err, tables.Close())
				}
				names = append(names, name)
			}
			if err := errors.Join(tables.Err(), tables.Close()); err != nil {
				return "", err
			}
			if len(names) == 0 {
				return "", errors.New("owned Helpdesk tables are missing")
			}
			var snapshot []any
			for _, name := range names {
				table := pgx.Identifier{"main", name}.Sanitize()
				metadata, err := connection.QueryContext(ctx, "SELECT * FROM "+table+" LIMIT 0")
				if err != nil {
					return "", err
				}
				columns, err := metadata.Columns()
				if err = errors.Join(err, metadata.Close()); err != nil {
					return "", err
				}
				expressions := make([]string, len(columns))
				for index, column := range columns {
					quoted := pgx.Identifier{column}.Sanitize()
					expressions[index] = "typeof(" + quoted + ") || ':' || quote(" + quoted + ")"
				}
				rows, err := connection.QueryContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM "+table+" ORDER BY id")
				if err != nil {
					return "", err
				}
				var records [][]string
				for rows.Next() {
					values := make([]string, len(columns))
					destinations := make([]any, len(columns))
					for index := range values {
						destinations[index] = &values[index]
					}
					if err := rows.Scan(destinations...); err != nil {
						return "", errors.Join(err, rows.Close())
					}
					records = append(records, values)
				}
				if err := errors.Join(rows.Err(), rows.Close()); err != nil {
					return "", err
				}
				snapshot = append(snapshot, []any{name, columns, records})
			}
			encoded, err := json.Marshal(snapshot)
			return string(encoded), err
		},
		setDueAt: func(ctx context.Context, id int64, value time.Time) error {
			_, err := connection.ExecContext(ctx, "UPDATE helpdesk_ticket SET due_at=? WHERE id=?", value.UTC().Format("2006-01-02 15:04:05.000000"), id)
			return err
		},
		corruptDueAt: func(ctx context.Context, id int64) error {
			_, err := connection.ExecContext(ctx, "UPDATE helpdesk_ticket SET due_at='not-a-time' WHERE id=?", id)
			return err
		},
		setServiceValues: func(ctx context.Context, id int64, day *calendar.Date, wall *clock.Time, period *duration.Duration) error {
			var dateValue, clockValue, elapsed any
			if day != nil {
				dateValue = day.String()
			}
			if wall != nil {
				clockValue = wall.String()
			}
			if period != nil {
				value, err := period.TotalMicroseconds()
				if err != nil {
					return err
				}
				elapsed = value
			}
			_, err := connection.ExecContext(ctx, "UPDATE helpdesk_ticket SET service_on=?,service_at=?,elapsed=? WHERE id=?", dateValue, clockValue, elapsed, id)
			return err
		},
		corruptServiceValue: func(ctx context.Context, id int64, field string) error {
			value, ok := map[string]string{"service_on": "'not-a-date'", "service_at": "'24:00:00'", "elapsed": "'not-a-period'"}[field]
			if !ok {
				return errors.New("unknown synthetic service field")
			}
			_, err := connection.ExecContext(ctx, "UPDATE helpdesk_ticket SET "+field+"="+value+" WHERE id=?", id)
			return err
		},
	}
}

func postgresDeadlineNative(connection *pgx.Conn, namespace string) deadlineNative {
	ticket := pgx.Identifier{namespace, "helpdesk_ticket"}.Sanitize()
	return deadlineNative{
		snapshot: func(ctx context.Context) (string, error) {
			tables, err := connection.Query(ctx, "SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname=$1 AND tablename LIKE 'helpdesk_%' ORDER BY tablename", namespace)
			if err != nil {
				return "", err
			}
			var names []string
			for tables.Next() {
				var name string
				if err := tables.Scan(&name); err != nil {
					tables.Close()
					return "", err
				}
				names = append(names, name)
			}
			tables.Close()
			if err := tables.Err(); err != nil {
				return "", err
			}
			if len(names) == 0 {
				return "", errors.New("owned Helpdesk tables are missing")
			}
			var snapshot []any
			for _, name := range names {
				rows, err := connection.Query(ctx, "SELECT row_to_json(t)::text FROM (SELECT * FROM "+pgx.Identifier{namespace, name}.Sanitize()+" ORDER BY id) t")
				if err != nil {
					return "", err
				}
				var records []string
				for rows.Next() {
					var value string
					if err := rows.Scan(&value); err != nil {
						rows.Close()
						return "", err
					}
					records = append(records, value)
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					return "", err
				}
				snapshot = append(snapshot, []any{name, records})
			}
			encoded, err := json.Marshal(snapshot)
			return string(encoded), err
		},
		setDueAt: func(ctx context.Context, id int64, value time.Time) error {
			_, err := connection.Exec(ctx, "UPDATE "+ticket+" SET due_at=$1 WHERE id=$2", value, id)
			return err
		},
		corruptDueAt: func(ctx context.Context, id int64) error {
			_, err := connection.Exec(ctx, "UPDATE "+ticket+" SET due_at=TIMESTAMPTZ 'infinity' WHERE id=$1", id)
			return err
		},
		setServiceValues: func(ctx context.Context, id int64, day *calendar.Date, wall *clock.Time, period *duration.Duration) error {
			var dateValue, clockValue, elapsed any
			if day != nil {
				dateValue = day.String()
			}
			if wall != nil {
				clockValue = wall.String()
			}
			if period != nil {
				elapsed = period.String()
			}
			_, err := connection.Exec(ctx, "UPDATE "+ticket+" SET service_on=$1::date,service_at=$2::time,elapsed=$3::interval WHERE id=$4", dateValue, clockValue, elapsed, id)
			return err
		},
		corruptServiceValue: func(ctx context.Context, id int64, field string) error {
			value, ok := map[string]string{"service_on": "DATE '10000-01-01'", "service_at": "TIME '24:00:00'", "elapsed": "INTERVAL '1 month'"}[field]
			if !ok {
				return errors.New("unknown synthetic service field")
			}
			_, err := connection.Exec(ctx, "UPDATE "+ticket+" SET "+field+"="+value+" WHERE id=$1", id)
			return err
		},
	}
}
