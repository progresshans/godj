//go:build darwin || linux

package projectoperatorproduct_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	operatorMaximumSecretScanRows  = 10_000
	operatorMaximumSecretScanBytes = 16 << 20
)

// Enumerate every text-bearing account, ownership, audit, session, catalog and
// article sink used by this product. Both databases scan the same closed list.
var operatorSecretSinks = []struct {
	table   string
	columns []string
}{
	{"godj_identity_user", []string{"principal_id", "username", "encoded_password", "email", "first_name", "last_name"}},
	{"godj_identity_group", []string{"name"}},
	{"godj_identity_permission", []string{"code", "name"}},
	{"godj_account_domain_mapping", []string{"payload"}},
	{"godj_account_domain_transition", []string{"operation_id", "role_fingerprint", "generation", "evidence_sha256", "mapping_sha256"}},
	{"godj_sessions_session", []string{"namespace", "digest", "payload"}},
	{"godj_account_audit_event", []string{"domain", "actor_id", "model", "object_id", "action", "changed_fields"}},
	{"godj_conformance_article", []string{"title", "summary", "slug"}},
}

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
	queries := make([]string, 0, len(operatorSecretSinks))
	for _, sink := range operatorSecretSinks {
		columns := make([]string, len(sink.columns))
		for i, column := range sink.columns {
			columns[i] = `"` + column + `"`
		}
		queries = append(queries, `SELECT `+strings.Join(columns, ",")+` FROM "`+sink.table+`"`)
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
	var scans []string
	for _, sink := range operatorSecretSinks {
		for _, column := range sink.columns {
			scans = append(scans, `SELECT COALESCE("`+column+`"::text,'') AS "value" FROM `+prefix+`"`+sink.table+`"`)
		}
	}
	query := strings.Join(scans, " UNION ALL ")

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
