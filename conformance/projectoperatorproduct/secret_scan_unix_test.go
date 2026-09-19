//go:build darwin || linux

package projectoperatorproduct_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	operatorMaximumSecretScanRows  = 10_000
	operatorMaximumSecretScanBytes = 16 << 20
)

func operatorSQLiteRawSecretOccurrences(t *testing.T, path string, secret []byte) int64 {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal("open external operator SQLite secret scan")
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	queries := []string{
		`SELECT "principal_id", "username", "encoded_password", "permissions", "definition_digest" FROM "godj_system_credential"`,
		`SELECT "digest", "payload" FROM "godj_system_session"`,
		`SELECT "actor_id", "model", "object_id", "action", "changed_fields", "display_label" FROM "godj_system_audit"`,
		`SELECT "title", COALESCE("summary", '') FROM "godj_conformance_article"`,
	}
	var fields [][]byte
	for _, query := range queries {
		rows, err := database.QueryContext(ctx, query)
		if err != nil {
			t.Fatal("query external operator SQLite forbidden secret sink")
		}
		fields = append(fields, operatorReadSQLTextRows(t, rows)...)
	}
	return operatorCountRawSecretOccurrences(t, fields, secret)
}

func operatorReadSQLTextRows(t *testing.T, rows *sql.Rows) [][]byte {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil {
		rows.Close()
		t.Fatal("read SQLite forbidden secret sink columns")
	}
	var result [][]byte
	rowCount := 0
	retained := 0
	for rows.Next() {
		rowCount++
		if rowCount > operatorMaximumSecretScanRows {
			rows.Close()
			t.Fatal("SQLite forbidden secret sink scan exceeded its row limit")
		}
		values := make([]sql.NullString, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			rows.Close()
			t.Fatal("scan SQLite forbidden secret sink")
		}
		for _, value := range values {
			if value.Valid {
				if len(value.String) > operatorMaximumSecretScanBytes-retained {
					rows.Close()
					t.Fatal("SQLite forbidden secret sink scan exceeded its byte limit")
				}
				retained += len(value.String)
				result = append(result, []byte(value.String))
			}
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal("finish SQLite forbidden secret sink scan")
	}
	return result
}

func operatorPostgresRawSecretOccurrences(t *testing.T, databaseURL, schema string, secret []byte) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect external operator PostgreSQL secret scan")
	}
	defer connection.Close(ctx)
	prefix := pgx.Identifier{schema}.Sanitize() + "."
	query := fmt.Sprintf(`
		SELECT "value" FROM (
			SELECT "principal_id"::text AS "value" FROM %[1]s"godj_system_credential"
			UNION ALL SELECT "username"::text FROM %[1]s"godj_system_credential"
			UNION ALL SELECT "encoded_password"::text FROM %[1]s"godj_system_credential"
			UNION ALL SELECT "permissions"::text FROM %[1]s"godj_system_credential"
			UNION ALL SELECT "definition_digest"::text FROM %[1]s"godj_system_credential"
			UNION ALL SELECT "digest"::text FROM %[1]s"godj_system_session"
			UNION ALL SELECT "payload"::text FROM %[1]s"godj_system_session"
			UNION ALL SELECT "actor_id"::text FROM %[1]s"godj_system_audit"
			UNION ALL SELECT "model"::text FROM %[1]s"godj_system_audit"
			UNION ALL SELECT "object_id"::text FROM %[1]s"godj_system_audit"
			UNION ALL SELECT "action"::text FROM %[1]s"godj_system_audit"
			UNION ALL SELECT "changed_fields"::text FROM %[1]s"godj_system_audit"
			UNION ALL SELECT "display_label"::text FROM %[1]s"godj_system_audit"
			UNION ALL SELECT "title"::text FROM %[1]s"godj_conformance_article"
			UNION ALL SELECT COALESCE("summary", '')::text FROM %[1]s"godj_conformance_article"
		) AS "forbidden_secret_sinks"
	`, prefix)
	rows, err := connection.Query(ctx, query)
	if err != nil {
		t.Fatal("query external operator PostgreSQL forbidden secret sinks")
	}
	fields := make([][]byte, 0, 64)
	retained := 0
	for rows.Next() {
		if len(fields) >= operatorMaximumSecretScanRows {
			rows.Close()
			t.Fatal("PostgreSQL forbidden secret sink scan exceeded its row limit")
		}
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			t.Fatal("scan external operator PostgreSQL forbidden secret sink")
		}
		if len(value) > operatorMaximumSecretScanBytes-retained {
			rows.Close()
			t.Fatal("PostgreSQL forbidden secret sink scan exceeded its byte limit")
		}
		retained += len(value)
		fields = append(fields, []byte(value))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal("finish external operator PostgreSQL forbidden secret sink scan")
	}
	rows.Close()
	return operatorCountRawSecretOccurrences(t, fields, secret)
}

func operatorCountRawSecretOccurrences(t *testing.T, fields [][]byte, secret []byte) int64 {
	t.Helper()
	if len(secret) == 0 {
		t.Fatal("forbidden secret scan received an empty marker")
	}
	var scanned int
	var occurrences int64
	for _, field := range fields {
		if len(field) > operatorMaximumSecretScanBytes-scanned {
			t.Fatal("forbidden secret sink scan exceeded its byte limit")
		}
		scanned += len(field)
		occurrences += int64(bytes.Count(field, secret))
	}
	return occurrences
}

func TestOperatorCountRawSecretOccurrencesDetectsAuditMarker(t *testing.T) {
	secret := []byte("raw-operator-secret")
	fields := [][]byte{
		[]byte("credential-hash"),
		[]byte(`{"audit":"raw-operator-secret"}`),
		[]byte("raw-operator-secret/raw-operator-secret"),
	}
	if got := operatorCountRawSecretOccurrences(t, fields, secret); got != 3 {
		t.Fatalf("raw secret occurrence count = %d, want 3", got)
	}
}
