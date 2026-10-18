package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
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

func TestPostgresCalendarArithmetic(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	queryupdatetest.CheckCalendarArithmetic(t, backend, backend.database, func(name string) string {
		quoted, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, true, func(t *testing.T, err error) {
		t.Helper()
		var native *pgconn.PgError
		if !errors.As(err, &native) || !strings.HasPrefix(native.Code, "22") {
			t.Fatal("calendar arithmetic lost its native data-exception SQLSTATE", err)
		}
	})
}

func TestPostgresCalendarArithmeticIgnoresSessionTimezone(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	table, err := quoteTable(namespace, "calendar_arithmetic_zone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(t.Context(), "CREATE TABLE "+table+" (id BIGINT PRIMARY KEY, day DATE, at TIMESTAMPTZ)"); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(t.Context(), "INSERT INTO "+table+" VALUES(1,DATE '2021-03-14',TIMESTAMPTZ '2021-03-14 00:30:00+00')"); err != nil {
		t.Fatal(err)
	}
	day := query.NewFieldRef("day", "day", query.FieldDate, false)
	at := query.NewFieldRef("at", "at", query.FieldDateTime, false)
	source := query.NewPlan("calendar_arithmetic_zone", []query.FieldRef{queryupdatetest.ID, day, at})
	dayValue, atValue := queryupdatetest.Field(t, day), queryupdatetest.Field(t, at)
	period := queryupdatetest.Literal(t, query.Duration(duration.Duration{Days: 1}))
	fraction := queryupdatetest.Arithmetic(t, query.ArithmeticDivide, period, queryupdatetest.Literal(t, query.Integer(2)))
	values := []query.ScalarExpression{
		queryupdatetest.Arithmetic(t, query.ArithmeticAdd, dayValue, period),
		queryupdatetest.Arithmetic(t, query.ArithmeticAdd, period, dayValue),
		queryupdatetest.Arithmetic(t, query.ArithmeticAdd, dayValue, fraction),
		queryupdatetest.Arithmetic(t, query.ArithmeticSubtract, atValue, dayValue),
		queryupdatetest.Arithmetic(t, query.ArithmeticSubtract, dayValue, atValue),
		queryupdatetest.Arithmetic(t, query.ArithmeticSubtract, dayValue, queryupdatetest.Literal(t, query.Date(calendar.Date{Year: 2021, Month: 3, Day: 13}))),
		queryupdatetest.Arithmetic(t, query.ArithmeticAdd, queryupdatetest.Literal(t, query.Time(clock.Time{Hour: 23})), period),
	}
	selections := make([]query.ResultExpression, len(values))
	for index, value := range values {
		selections[index], err = query.ScalarResult(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	shape, err := query.NewProjectionResult(selections...)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := source.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	err = backend.Atomic(t.Context(), func(borrowed db.Session) error {
		session := borrowed.(*transactionSession)
		if _, err := session.transaction.ExecContext(t.Context(), "SET LOCAL TIME ZONE 'America/New_York'"); err != nil {
			return err
		}
		rows, err := session.Query(t.Context(), plan)
		if err != nil {
			return err
		}
		if !rows.Next() {
			return errors.Join(errors.New("calendar arithmetic row missing"), rows.Err(), rows.Close())
		}
		var forward, reverse, fractional time.Time
		var first, second, third, wall string
		if err := rows.Scan(&forward, &reverse, &fractional, &first, &second, &third, &wall); err != nil {
			return errors.Join(err, rows.Close())
		}
		if rows.Next() {
			return errors.Join(errors.New("calendar arithmetic duplicated its source row"), rows.Close())
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if forward.UTC().Format(time.RFC3339Nano) != "2021-03-15T00:00:00Z" || !reverse.Equal(forward) || fractional.UTC().Format(time.RFC3339Nano) != "2021-03-14T12:00:00Z" || first != duration.FromMicroseconds(1800000000).String() || second != duration.FromMicroseconds(-1800000000).String() || third != (duration.Duration{Days: 1}).String() || wall != "23:00:00" {
			t.Fatal("calendar arithmetic used a session-local midnight or DST day", forward, reverse, fractional, first, second, third, wall)
		}
		var zone string
		if err := session.transaction.QueryRowContext(t.Context(), "SELECT current_setting('TimeZone')").Scan(&zone); err != nil {
			return err
		}
		if zone != "America/New_York" {
			t.Fatal("calendar arithmetic mutated the caller's timezone", zone)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
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
