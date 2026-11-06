package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRequireCompleteProductRowsAllowsSequenceGaps(t *testing.T) {
	t.Parallel()
	rows := []productRow{
		{id: 1, title: "prepared"},
		{id: 33, title: "resumed", summary: sql.NullString{String: "after-restart", Valid: true}},
	}
	if err := requireCompleteProductRows(rows); err != nil {
		t.Fatalf("require complete rows with a sequence gap: %v", err)
	}
	rows[1].id = 1
	if err := requireCompleteProductRows(rows); err == nil {
		t.Fatal("require complete rows accepted a reused identity key")
	}
}

func TestValidateRunnerSchema(t *testing.T) {
	t.Parallel()
	t.Run("supported modes", func(t *testing.T) {
		for _, mode := range []string{"prepare", "probe", "resume", "verify", "cleanup"} {
			if got := modeFromArguments([]string{mode}); got != mode {
				t.Fatalf("modeFromArguments(%q) = %q", mode, got)
			}
		}
		for _, arguments := range [][]string{nil, {}, {"unknown"}, {"prepare", "resume"}} {
			if got := modeFromArguments(arguments); got != "" {
				t.Fatalf("modeFromArguments(%q) = %q, want empty", arguments, got)
			}
		}
	})
	tests := []struct {
		name   string
		schema string
		valid  bool
	}{
		{name: "valid digits", schema: postgresSchemaPrefix + "20260823", valid: true},
		{name: "valid lowercase", schema: postgresSchemaPrefix + "restartabc123", valid: true},
		{name: "missing prefix", schema: "public", valid: false},
		{name: "short suffix", schema: postgresSchemaPrefix + "short", valid: false},
		{name: "uppercase suffix", schema: postgresSchemaPrefix + "Restart123", valid: false},
		{name: "separator in suffix", schema: postgresSchemaPrefix + "restart_123", valid: false},
		{name: "quoted suffix", schema: postgresSchemaPrefix + `restart"123`, valid: false},
		{name: "too long", schema: postgresSchemaPrefix + strings.Repeat("a", maximumSchemaSuffix+1), valid: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			err := validateRunnerSchema(test.schema)
			if (err == nil) != test.valid {
				t.Fatalf("validateRunnerSchema(%q) error = %v, valid=%t", test.schema, err, test.valid)
			}
		})
	}
}

func TestFailureOutputDoesNotExposeDatabaseURL(t *testing.T) {
	t.Parallel()
	const secretURL = "postgresql://runner:do-not-print@127.0.0.1:5432/database"
	var output bytes.Buffer
	if err := writeFailure(&output, "prepare", errors.New("connect "+secretURL)); err != nil {
		t.Fatalf("write failure output: %v", err)
	}
	if strings.Contains(output.String(), secretURL) || strings.Contains(output.String(), "do-not-print") {
		t.Fatalf("failure output exposed PostgreSQL URL: %s", output.String())
	}
	var got failureResult
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode failure output: %v", err)
	}
	if got != (failureResult{Mode: "prepare", Status: "error", Error: "operation_failed"}) {
		t.Fatalf("failure output = %+v", got)
	}
}

func TestProjectRunnerSameServerLifecycle(t *testing.T) {
	databaseURL := os.Getenv(postgresURLEnv)
	if strings.TrimSpace(databaseURL) == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatalf("%s is required when GODJ_REQUIRE_POSTGRES=1", postgresURLEnv)
		}
		t.Skipf("%s is not set", postgresURLEnv)
	}
	schema := postgresSchemaPrefix + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Setenv(postgresSchemaEnv, schema)

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var output bytes.Buffer
		if err := run(ctx, []string{"cleanup"}, &output); err != nil {
			t.Errorf("cleanup PostgreSQL product schema: %v", err)
		}
	}
	t.Cleanup(cleanup)

	ctx, cancel := context.WithTimeout(context.Background(), runnerTimeout)
	defer cancel()
	requireModeResult(t, ctx, "prepare", modeResult{Mode: "prepare", Status: "ok", History: 1, Rows: 1})
	requireModeResult(t, ctx, "probe", modeResult{Mode: "probe", Status: "ok", History: 1, Rows: 1})
	requireModeResult(t, ctx, "resume", modeResult{Mode: "resume", Status: "ok", History: 2, Rows: 2})
	requireModeResult(t, ctx, "verify", modeResult{Mode: "verify", Status: "ok", History: 2, Rows: 2})
	requireModeResult(t, ctx, "cleanup", modeResult{Mode: "cleanup", Status: "ok"})
}

func requireModeResult(t *testing.T, ctx context.Context, mode string, want modeResult) {
	t.Helper()
	var output bytes.Buffer
	if err := run(ctx, []string{mode}, &output); err != nil {
		t.Fatalf("run PostgreSQL product mode %q: %v", mode, err)
	}
	var got modeResult
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode PostgreSQL product mode %q output %q: %v", mode, output.String(), err)
	}
	if got != want {
		t.Fatalf("PostgreSQL product mode %q result = %+v, want %+v", mode, got, want)
	}
}
