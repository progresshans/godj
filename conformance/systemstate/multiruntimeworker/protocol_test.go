package multiruntimeworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseConfigIsOpaqueRedactedAndBounded(t *testing.T) {
	sqlitePath := filepath.Join(t.TempDir(), "sqlite-secret-marker.sqlite3")
	sqliteConfig, err := NewSQLiteDatabase(sqlitePath)
	if err != nil {
		t.Fatalf("NewSQLiteDatabase(): %v", err)
	}
	postgresURL := "postgresql://worker_user:postgres-password-marker@127.0.0.1:5432/worker_db"
	postgresConfig, err := NewPostgresDatabase(postgresURL, "worker_schema")
	if err != nil {
		t.Fatalf("NewPostgresDatabase(): %v", err)
	}
	for name, config := range map[string]DatabaseConfig{
		"sqlite":   sqliteConfig,
		"postgres": postgresConfig,
	} {
		invalidWrapVerb := "%w"
		rendered := strings.Join([]string{
			fmt.Sprintf("%v", config),
			fmt.Sprintf("%+v", config),
			fmt.Sprintf("%#v", config),
			fmt.Sprintf("%s", config),
			fmt.Sprintf("%q", config),
			fmt.Sprintf("%d", config),
			fmt.Sprintf("%x", config),
			fmt.Sprintf("%o", config),
			fmt.Sprintf("%p", config),
			fmt.Sprintf(invalidWrapVerb, config),
		}, "\n")
		encoded, marshalErr := json.Marshal(config)
		if marshalErr != nil {
			t.Fatalf("json.Marshal(%s config): %v", name, marshalErr)
		}
		rendered += string(encoded)
		for _, marker := range []string{sqlitePath, "sqlite-secret-marker", postgresURL, "postgres-password-marker", "worker_schema"} {
			if strings.Contains(rendered, marker) {
				t.Fatalf("%s config diagnostics exposed marker %q", name, marker)
			}
		}
	}
	if _, err := NewSQLiteDatabase("relative.sqlite3"); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
		t.Fatalf("relative SQLite path error = %#v, want invalid_config", err)
	}
	if _, err := NewPostgresDatabase("", "schema"); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
		t.Fatalf("empty PostgreSQL URL error = %#v, want invalid_config", err)
	}
	if (DatabaseConfig{}).valid() {
		t.Fatal("zero DatabaseConfig is valid")
	}
}

func TestWireConfigStrictBoundedDecodeAndRedaction(t *testing.T) {
	database, err := NewSQLiteDatabase(filepath.Join(t.TempDir(), "strict.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := newWireConfig(database, roleHolder, "wire-password-marker", 42)
	if err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeWireConfig(bytes.NewReader(document))
	if err != nil || decoded.Role != roleHolder || decoded.Password != "wire-password-marker" || decoded.ObjectID != 42 {
		t.Fatalf("decodeWireConfig(valid) = (%v, %v)", decoded, err)
	}
	if rendered := fmt.Sprintf("%v %#v", config, config); strings.Contains(rendered, "wire-password-marker") {
		t.Fatalf("wire config diagnostics exposed password: %q", rendered)
	}

	unknown := append(bytes.TrimSuffix(document, []byte("}")), []byte(`,"unknown":true}`)...)
	duplicate := append(bytes.TrimSuffix(document, []byte("}")), []byte(`,"role":"holder"}`)...)
	for name, input := range map[string][]byte{
		"unknown":             unknown,
		"duplicate":           duplicate,
		"trailing":            append(append([]byte(nil), document...), []byte(` {}`)...),
		"oversize":            []byte(`{"format_version":1,"role":"holder","backend":"sqlite","sqlite_data_source":"` + strings.Repeat("x", maximumConfigBytes) + `"}`),
		"oversize whitespace": append(append([]byte(nil), document...), bytes.Repeat([]byte(" "), maximumConfigBytes)...),
		"invalid utf8":        append(append([]byte(nil), document...), 0xff),
	} {
		if _, err := decodeWireConfig(bytes.NewReader(input)); !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
			t.Fatalf("decodeWireConfig(%s) error = %#v, want invalid_config", name, err)
		}
	}
}

func TestRunWorkerFailureResponseExcludesPrivateConfig(t *testing.T) {
	const (
		passwordMarker = "private-worker-password-marker"
		urlMarker      = "postgresql://private_user:private-url-password@invalid.invalid/private_db"
	)
	database, err := NewPostgresDatabase(urlMarker, "private_schema")
	if err != nil {
		t.Fatal(err)
	}
	config, err := newWireConfig(database, roleProbe, passwordMarker, 91)
	if err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err = RunWorker(context.Background(), bytes.NewReader(document), &bytes.Buffer{}, &bytes.Buffer{}, &stdout)
	if !errors.Is(err, &Error{Code: CodeDatabase}) {
		t.Fatalf("RunWorker(database failure) error = %#v, want database_failure", err)
	}
	if strings.Contains(stdout.String(), passwordMarker) || strings.Contains(stdout.String(), urlMarker) ||
		strings.Contains(stdout.String(), "private-url-password") || strings.Contains(stdout.String(), "private_schema") {
		t.Fatalf("failure response exposed private config: %q", stdout.String())
	}
	response, err := decodeWorkerResponse(bytes.NewReader(stdout.Bytes()))
	if err != nil || response.OK || response.ErrorCode != CodeDatabase || response.Role != roleProbe {
		t.Fatalf("database failure response = (%+v, %v)", response, err)
	}
}

func TestRunWorkerInvalidInputEmitsOneBoundedClosedFailure(t *testing.T) {
	private := "invalid-config-secret-marker"
	input := []byte(`{"format_version":1,"role":"probe","backend":"sqlite","password":"` + private + `","unknown":true}`)
	var stdout bytes.Buffer
	err := RunWorker(context.Background(), bytes.NewReader(input), &bytes.Buffer{}, &bytes.Buffer{}, &stdout)
	if !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
		t.Fatalf("RunWorker(invalid) error = %#v, want invalid_config", err)
	}
	if stdout.Len() == 0 || stdout.Len() > maximumOutputBytes || bytes.Count(stdout.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("invalid response size/envelopes = %d/%d", stdout.Len(), bytes.Count(stdout.Bytes(), []byte("\n")))
	}
	if strings.Contains(stdout.String(), private) {
		t.Fatalf("invalid response exposed private input: %q", stdout.String())
	}
	response, decodeErr := decodeWorkerResponse(bytes.NewReader(stdout.Bytes()))
	if decodeErr != nil || response.OK || response.ErrorCode != CodeInvalidConfig || response.Role != "" || response.Backend != "" {
		t.Fatalf("strict invalid-config response = (%+v, %v)", response, decodeErr)
	}
}
