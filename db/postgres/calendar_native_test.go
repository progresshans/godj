package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/queryupdatetest"
	"github.com/progresshans/godj/query"
)

func TestPostgresCalendarFunctionsAndUpdates(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	queryupdatetest.CheckCalendar(t, backend, backend.database, func(name string) string {
		quoted, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, true, func(t *testing.T, err error) {
		t.Helper()
		var native *pgconn.PgError
		if !errors.As(err, &native) || !strings.HasPrefix(native.Code, "22") {
			t.Fatal("calendar failure lost its native data-exception SQLSTATE", err)
		}
	})
}

func TestPostgresCalendarExtractionIgnoresSessionTimezone(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	const name = "calendar_session_values"
	table, err := quoteTable(namespace, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(t.Context(), "CREATE TABLE "+table+" (id BIGINT PRIMARY KEY, at TIMESTAMPTZ NOT NULL, day DATE, wall TIME, part BIGINT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(t.Context(), "INSERT INTO "+table+" VALUES (1, TIMESTAMPTZ '2021-01-01 00:30:59.999999+00', NULL, NULL, NULL)"); err != nil {
		t.Fatal(err)
	}
	at := query.NewFieldRef("at", "at", query.FieldDateTime, false)
	day := query.NewFieldRef("day", "day", query.FieldDate, true)
	wall := query.NewFieldRef("wall", "wall", query.FieldTime, true)
	part := query.NewFieldRef("part", "part", query.FieldInteger, true)
	source := query.NewPlan(name, []query.FieldRef{queryupdatetest.ID, at, day, wall, part})
	value := queryupdatetest.Field(t, at)
	functions := make([]query.ScalarExpression, 7)
	selections := make([]query.ResultExpression, len(functions))
	for index, function := range []query.FunctionKind{query.FunctionDate, query.FunctionTime, query.FunctionYear, query.FunctionSecond} {
		functions[index], err = query.FunctionExpression(function, value)
		if err != nil {
			t.Fatal(err)
		}
		selections[index], err = query.ScalarResult(functions[index])
		if err != nil {
			t.Fatal(err)
		}
	}
	functions[4], err = query.FunctionExpression(query.FunctionTruncDay, value)
	if err != nil {
		t.Fatal(err)
	}
	functions[5], err = query.FunctionExpression(query.FunctionTruncWeek, functions[0])
	if err != nil {
		t.Fatal(err)
	}
	functions[6], err = query.FunctionExpression(query.FunctionTruncMinute, functions[1])
	if err != nil {
		t.Fatal(err)
	}
	for index := 4; index < len(functions); index++ {
		selections[index], err = query.ScalarResult(functions[index])
		if err != nil {
			t.Fatal(err)
		}
	}
	shape, err := query.NewProjectionResult(selections...)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := source.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	err = backend.Atomic(t.Context(), func(borrowed db.Session) error {
		session := borrowed.(*transactionSession)
		if _, err := session.transaction.ExecContext(t.Context(), "SET LOCAL TIME ZONE 'America/New_York'"); err != nil {
			return err
		}
		rows, err := session.Query(t.Context(), projection)
		if err != nil {
			return err
		}
		if !rows.Next() {
			return errors.Join(errors.New("calendar row missing"), rows.Err(), rows.Close())
		}
		var date time.Time
		var clock string
		var year, second int64
		var truncatedInstant, truncatedDate time.Time
		var truncatedClock string
		if err := rows.Scan(&date, &clock, &year, &second, &truncatedInstant, &truncatedDate, &truncatedClock); err != nil {
			return errors.Join(err, rows.Close())
		}
		if rows.Next() {
			return errors.Join(errors.New("calendar row duplicated"), rows.Close())
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if date.Format("2006-01-02") != "2021-01-01" || clock != "00:30:59.999999" || year != 2021 || second != 59 {
			t.Fatal("calendar extraction used session-local components", date, clock, year, second)
		}
		if truncatedInstant.UTC().Format(time.RFC3339Nano) != "2021-01-01T00:00:00Z" || truncatedDate.Format("2006-01-02") != "2020-12-28" || truncatedClock != "00:30:00" {
			t.Fatal("truncation used the session-local calendar or changed its result domain", truncatedInstant, truncatedDate, truncatedClock)
		}
		update := queryupdatetest.Plan(t, source, queryupdatetest.Assignment(t, day, functions[0]), queryupdatetest.Assignment(t, wall, functions[1]), queryupdatetest.Assignment(t, part, functions[2]))
		if count, err := session.QueryUpdate(t.Context(), update); err != nil || count != 1 {
			t.Fatal("calendar update failed in an altered session", count, err)
		}
		var storedDate, storedClock, zone string
		var storedYear int64
		if err := session.transaction.QueryRowContext(t.Context(), "SELECT day::text,wall::text,part,current_setting('TimeZone') FROM "+table).Scan(&storedDate, &storedClock, &storedYear, &zone); err != nil {
			return err
		}
		if storedDate != "2021-01-01" || storedClock != clock || storedYear != year || zone != "America/New_York" {
			t.Fatal("calendar write changed its domain or the caller's zone", storedDate, storedClock, storedYear, zone)
		}
		truncatedUpdate := queryupdatetest.Plan(t, source, queryupdatetest.Assignment(t, at, functions[4]), queryupdatetest.Assignment(t, day, functions[5]), queryupdatetest.Assignment(t, wall, functions[6]))
		if count, err := session.QueryUpdate(t.Context(), truncatedUpdate); err != nil || count != 1 {
			t.Fatal("truncation write failed in an altered session", count, err)
		}
		var storedInstant time.Time
		if err := session.transaction.QueryRowContext(t.Context(), "SELECT at,day::text,wall::text,current_setting('TimeZone') FROM "+table).Scan(&storedInstant, &storedDate, &storedClock, &zone); err != nil {
			return err
		}
		if !storedInstant.Equal(truncatedInstant) || storedDate != "2020-12-28" || storedClock != "00:30:00" || zone != "America/New_York" {
			t.Fatal("truncation write changed the caller's zone or lost the UTC period", storedInstant, storedDate, storedClock, zone)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
