package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/settings"
)

func TestDatabaseSelectionOwnsSnapshotDefaultsAndExplicitOverrides(t *testing.T) {
	t.Parallel()
	fallback, err := SQLite("file:default.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	override, err := SQLite("file:explicit.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		entries []string
		options Options
		kind    Kind
		value   string
	}{
		{"default", nil, Options{Default: fallback}, KindSQLite, "file:default.sqlite3"},
		{"override", nil, Options{Default: fallback, Override: override}, KindSQLite, "file:explicit.sqlite3"},
		{"sqlite_environment", []string{"GODJ_SQLITE_DATABASE= file:selected.sqlite3 "}, Options{Default: fallback}, KindSQLite, "file:selected.sqlite3"},
		{"postgres_environment", []string{"GODJ_POSTGRES_URL= postgres://user:config-secret@localhost/database ", "GODJ_POSTGRES_SCHEMA= runtime "}, Options{Default: fallback}, KindPostgres, "postgres://user:config-secret@localhost/database"},
		{"custom_keys", []string{"PROJECT_SQLITE=file:custom.sqlite3"}, Options{Names: EnvironmentNames{"PROJECT_SQLITE", "PROJECT_POSTGRES", "PROJECT_SCHEMA"}}, KindSQLite, "file:custom.sqlite3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment, err := settings.NewEnvironment(test.entries)
			if err != nil {
				t.Fatal(err)
			}
			calls := map[string]int{}
			selected, err := FromEnvironment(func(key string) (string, bool) { calls[key]++; return environment.Lookup(key) }, test.options)
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 3 {
				t.Fatalf("looked up %d keys", len(calls))
			}
			for key, count := range calls {
				if count != 1 {
					t.Fatalf("read %s %d times", key, count)
				}
			}
			for i := range test.entries {
				test.entries[i] = "changed=changed"
			}
			if selected.Kind() != test.kind {
				t.Fatalf("kind: %v", selected.Kind())
			}
			value := selected.SQLiteDatabase()
			if test.kind == KindPostgres {
				value = selected.PostgresURL()
				if selected.PostgresSchema() != "runtime" {
					t.Fatal("lost schema")
				}
			}
			if value != test.value {
				t.Fatal("selection did not preserve the frozen parsed value")
			}
			for _, format := range []string{"%v", "%+v", "%#v", "%q", "%x", "%d"} {
				if strings.Contains(fmt.Sprintf(format, selected), test.value) || strings.Contains(fmt.Sprintf(format, selected), "config-secret") {
					t.Fatal("formatting exposed database settings")
				}
			}
			encoded, err := json.Marshal(selected)
			if err != nil || string(encoded) != "{}" {
				t.Fatalf("JSON exposed selection: %s %v", encoded, err)
			}
			wantURL := ""
			if test.kind == KindPostgres {
				wantURL = test.value
			}
			var readers sync.WaitGroup
			for range 8 {
				readers.Go(func() {
					for range 100 {
						if selected.Kind() != test.kind || selected.PostgresURL() != wantURL {
							t.Error("concurrent selection changed")
						}
					}
				})
			}
			readers.Wait()
		})
	}
}

func TestDatabaseSelectionRejectsConflictsAndNeverHidesInvalidInputWithDefaults(t *testing.T) {
	t.Parallel()
	fallback, err := SQLite("file:fallback.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		entries  []string
		override Config
		field    string
	}{
		{"empty_sqlite", []string{"GODJ_SQLITE_DATABASE= "}, Config{}, "GODJ_SQLITE_DATABASE"},
		{"missing_schema", []string{"GODJ_POSTGRES_URL=postgres://user:private@localhost/db"}, Config{}, "GODJ_POSTGRES_SCHEMA"},
		{"missing_url", []string{"GODJ_POSTGRES_SCHEMA=runtime"}, Config{}, "GODJ_POSTGRES_URL"},
		{"mixed", []string{"GODJ_SQLITE_DATABASE=file:a", "GODJ_POSTGRES_SCHEMA=runtime"}, Config{}, "database"},
		{"override_with_environment", []string{"GODJ_POSTGRES_SCHEMA=runtime"}, fallback, "database"},
		{"override_with_empty_environment", []string{"GODJ_SQLITE_DATABASE="}, fallback, "GODJ_SQLITE_DATABASE"},
		{"invalid_url", []string{"GODJ_POSTGRES_URL=mysql://private@localhost/db", "GODJ_POSTGRES_SCHEMA=runtime"}, Config{}, "GODJ_POSTGRES_URL"},
		{"invalid_schema", []string{"GODJ_POSTGRES_URL=postgres://user:private@localhost/db", "GODJ_POSTGRES_SCHEMA=private-schema!"}, Config{}, "GODJ_POSTGRES_SCHEMA"},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment, err := settings.NewEnvironment(test.entries)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := FromEnvironment(environment.Lookup, Options{Default: fallback, Override: test.override})
			if !errors.Is(err, &Error{Field: test.field}) || selected.Kind() != 0 {
				t.Fatalf("invalid selection published or wrong field: %v", err)
			}
			if strings.Contains(fmt.Sprintf("%v %#v", err, err), "private") {
				t.Fatal("diagnostic exposed a value")
			}
		})
	}
	if selected, err := FromEnvironment((settings.Environment{}).Lookup, Options{}); !errors.Is(err, &Error{Field: "database"}) || selected.Kind() != 0 {
		t.Fatalf("missing selection: %v", err)
	}
}

func TestDatabaseEnvironmentNamesAreValidatedBeforeLookup(t *testing.T) {
	t.Parallel()
	for _, names := range []EnvironmentNames{
		{SQLiteDatabase: "ONE"},
		{"SAME", "SAME", "SCHEMA"},
		{"SQLITE", "PG_URL", "bad key"},
		{"SQLITE", "PG_URL", strings.Repeat("A", 129)},
		{"SQLITE", "PG_URL", "1SCHEMA"},
	} {
		calls := 0
		selected, err := FromEnvironment(func(string) (string, bool) { calls++; return "", false }, Options{Names: names})
		if !errors.Is(err, &Error{Field: "environment_names"}) || selected.Kind() != 0 || calls != 0 {
			t.Fatalf("invalid keys reached lookup: %v, calls=%d", err, calls)
		}
	}
	if _, err := FromEnvironment(nil, Options{}); !errors.Is(err, &Error{Field: "environment"}) {
		t.Fatalf("nil lookup: %v", err)
	}
}

func TestDatabaseConstructorsRejectInvalidValuesWithoutRetainingSecrets(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		make  func() (Config, error)
		field string
	}{
		{"empty_sqlite", func() (Config, error) { return SQLite(" ") }, "sqlite_database"},
		{"nul_sqlite", func() (Config, error) { return SQLite("private\x00data") }, "sqlite_database"},
		{"url_scheme", func() (Config, error) { return PostgreSQL("mysql://user:private@localhost/db", "runtime") }, "postgres_url"},
		{"missing_database", func() (Config, error) { return PostgreSQL("postgres://user:private@localhost/", "runtime") }, "postgres_url"},
		{"reserved_schema", func() (Config, error) { return PostgreSQL("postgres://user:private@localhost/db", "pg_catalog") }, "postgres_schema"},
		{"invalid_schema", func() (Config, error) { return PostgreSQL("postgres://user:private@localhost/db", "private-schema!") }, "postgres_schema"},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected, err := test.make()
			if !errors.Is(err, &Error{Field: test.field}) || selected.Kind() != 0 || strings.Contains(fmt.Sprintf("%v %#v", err, err), "private") {
				t.Fatalf("invalid constructor: %v", err)
			}
		})
	}
}
