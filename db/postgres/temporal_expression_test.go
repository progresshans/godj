package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/queryupdatetest"
	"github.com/progresshans/godj/query"
)

func TestPostgresTemporalShiftsIgnoreSessionDST(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	const name = "temporal_session_values"
	table, err := quoteTable(namespace, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(t.Context(), "CREATE TABLE "+table+" (id BIGINT PRIMARY KEY, at TIMESTAMPTZ NOT NULL, span INTERVAL NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.database.ExecContext(t.Context(), "INSERT INTO "+table+" VALUES (1, TIMESTAMPTZ '2026-03-08 06:30:00.000001+00', INTERVAL '1 day')"); err != nil {
		t.Fatal(err)
	}
	at := query.NewFieldRef("at", "at", query.FieldDateTime, false)
	span := query.NewFieldRef("span", "span", query.FieldDuration, false)
	fields := []query.FieldRef{queryupdatetest.ID, at, span}
	source := query.NewPlan(name, fields)
	left, right := queryupdatetest.Field(t, at), queryupdatetest.Field(t, span)
	shift := queryupdatetest.Arithmetic(t, query.ArithmeticAdd, left, right)
	reverse := queryupdatetest.Arithmetic(t, query.ArithmeticAdd, right, left)
	previous := queryupdatetest.Arithmetic(t, query.ArithmeticSubtract, left, right)
	values := make([]query.ResultExpression, 3)
	for index, scalar := range []query.ScalarExpression{shift, reverse, previous} {
		values[index], err = query.ScalarResult(scalar)
		if err != nil {
			t.Fatal(err)
		}
	}
	shape, err := query.NewProjectionResult(values...)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := source.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	wantNext := time.Date(2026, 3, 9, 6, 30, 0, 1000, time.UTC)
	wantPrevious := time.Date(2026, 3, 7, 6, 30, 0, 1000, time.UTC)
	err = backend.Atomic(t.Context(), func(borrowed db.Session) error {
		session := borrowed.(*transactionSession)
		if _, err := session.transaction.ExecContext(t.Context(), "SET LOCAL TIME ZONE 'America/New_York'"); err != nil {
			return err
		}
		rows, err := borrowed.Query(t.Context(), projection)
		if err != nil {
			return err
		}
		var next, reverseNext, previous time.Time
		if !rows.Next() {
			return errors.Join(errors.New("temporal row missing"), rows.Err(), rows.Close())
		}
		if err := rows.Scan(&next, &reverseNext, &previous); err != nil {
			return errors.Join(err, rows.Close())
		}
		if rows.Next() {
			return errors.Join(errors.New("temporal row duplicated"), rows.Close())
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if !next.Equal(wantNext) || !reverseNext.Equal(wantNext) || !previous.Equal(wantPrevious) {
			t.Fatal("elapsed Duration used the session's DST calendar", next, reverseNext, previous)
		}
		update := queryupdatetest.Plan(t, source, queryupdatetest.Assignment(t, at, shift))
		if count, err := session.QueryUpdate(t.Context(), update); err != nil || count != 1 {
			t.Fatal("temporal assignment failed in an altered session", count, err)
		}
		var stored time.Time
		var zone string
		if err := session.transaction.QueryRowContext(t.Context(), "SELECT at, current_setting('TimeZone') FROM "+table).Scan(&stored, &zone); err != nil {
			return err
		}
		if !stored.Equal(wantNext) || zone != "America/New_York" {
			t.Fatal("update changed its instant or the caller's session setting", stored, zone)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
