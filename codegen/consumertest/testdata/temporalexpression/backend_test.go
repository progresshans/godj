package consumer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/planning"
	"example.com/godj-project-bundle/records"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

type temporalBackend interface {
	db.Session
	db.Atomic
	db.QueryUpdater
	migrationbackend.RevisionFencedBackend
	Close() error
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type temporalPhysical struct {
	Update   func(table, column, expression string, id int64) error
	Snapshot func() string
}

func withTemporalBackends(t *testing.T, run func(*testing.T, temporalBackend, string, temporalPhysical)) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "temporal.db"))
		backend, err := sqlite.Open(t.Context(), dsn)
		check(t, err)
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateTemporal(t, backend)
		raw, err := sql.Open("sqlite", dsn)
		check(t, err)
		t.Cleanup(func() { check(t, raw.Close()) })
		run(t, backend, "sqlite", temporalPhysical{
			Update: func(table, column, expression string, id int64) error {
				_, err := raw.ExecContext(t.Context(), `UPDATE main.`+pgx.Identifier{"gdj_temporal_" + table}.Sanitize()+` SET `+pgx.Identifier{column}.Sanitize()+` = `+expression+` WHERE id = ?`, id)
				return err
			},
			Snapshot: func() string { return sqliteTemporalSnapshot(t, raw) },
		})
	})
	url := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if url == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), url)
		check(t, err)
		schema := fmt.Sprintf("godj_temporal_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
		created := false
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var cleanup error
			if created {
				_, cleanup = connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
				if cleanup == nil {
					var exists bool
					cleanup = connection.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", schema).Scan(&exists)
					if exists {
						cleanup = errors.Join(cleanup, errors.New("owned temporal namespace survived cleanup"))
					}
				}
			}
			check(t, errors.Join(cleanup, connection.Close(ctx)))
		})
		_, err = connection.Exec(t.Context(), "CREATE SCHEMA "+quoted)
		check(t, err)
		created = true
		backend, err := postgres.Open(t.Context(), postgres.Config{URL: url, Schema: schema})
		check(t, err)
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateTemporal(t, backend)
		run(t, backend, "postgres", temporalPhysical{
			Update: func(table, column, expression string, id int64) error {
				_, err := connection.Exec(t.Context(), `UPDATE `+quoted+`.`+pgx.Identifier{"gdj_temporal_" + table}.Sanitize()+` SET `+pgx.Identifier{column}.Sanitize()+` = `+expression+` WHERE id = $1`, id)
				return err
			},
			Snapshot: func() string {
				var tables [][]string
				for _, table := range []string{"schedule", "event", "detail"} {
					rows, err := connection.Query(t.Context(), `SELECT row_to_json(t)::text FROM (SELECT * FROM `+quoted+`.`+pgx.Identifier{"gdj_temporal_" + table}.Sanitize()+` ORDER BY id) t`)
					check(t, err)
					var records []string
					for rows.Next() {
						var value string
						check(t, rows.Scan(&value))
						records = append(records, value)
					}
					check(t, rows.Err())
					rows.Close()
					tables = append(tables, records)
				}
				data, err := json.Marshal(tables)
				check(t, err)
				return string(data)
			},
		})
	})
}

func migrateTemporal(t *testing.T, backend temporalBackend) {
	t.Helper()
	first := migrations.Migration{App: "planning", Name: "0001_initial"}
	second := migrations.Migration{App: "records", Name: "0001_initial", Dependencies: []migrations.MigrationKey{first.Key()}}
	appendModels := func(migration *migrations.Migration, schema ir.Schema, names ...string) {
		for _, name := range names {
			found := false
			for _, model := range schema.Models {
				if model.Name == name {
					migration.Operations = append(migration.Operations, migrations.CreateModel{AppLabel: schema.AppLabel, Model: model})
					found = true
					break
				}
			}
			if !found {
				t.Fatal("missing temporal model", name)
			}
		}
	}
	appendModels(&first, planning.GoDjRelationSchema(), "schedule")
	appendModels(&second, records.GoDjRelationSchema(), "event", "detail")
	var sources []definition.Source
	for _, migration := range []migrations.Migration{first, second} {
		wire, err := definition.Encode(definition.Producer{Name: "temporal-consumer", Version: "1"}, migration)
		check(t, err)
		sources = append(sources, definition.Source{SourceID: migration.App, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	check(t, err)
	_, err = (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest())
	check(t, err)
}

// Inspect SQLite storage classes and SQL literals directly. Declared DATETIME
// driver conversion must not normalize away a physical value change.
func sqliteTemporalSnapshot(t *testing.T, raw *sql.DB) string {
	t.Helper()
	var tables []any
	for _, name := range []string{"schedule", "event", "detail"} {
		table := "main." + pgx.Identifier{"gdj_temporal_" + name}.Sanitize()
		metadata, err := raw.QueryContext(t.Context(), "SELECT * FROM "+table+" LIMIT 0")
		check(t, err)
		columns, err := metadata.Columns()
		check(t, err)
		check(t, metadata.Close())
		expressions := make([]string, len(columns))
		for index, column := range columns {
			quoted := pgx.Identifier{column}.Sanitize()
			expressions[index] = "typeof(" + quoted + ") || ':' || quote(" + quoted + ")"
		}
		rows, err := raw.QueryContext(t.Context(), "SELECT "+strings.Join(expressions, ",")+" FROM "+table+" ORDER BY id")
		check(t, err)
		var records [][]string
		for rows.Next() {
			values := make([]string, len(columns))
			destinations := make([]any, len(columns))
			for index := range values {
				destinations[index] = &values[index]
			}
			check(t, rows.Scan(destinations...))
			records = append(records, values)
		}
		check(t, rows.Err())
		check(t, rows.Close())
		tables = append(tables, records)
	}
	document, err := json.Marshal(tables)
	check(t, err)
	return string(document)
}
