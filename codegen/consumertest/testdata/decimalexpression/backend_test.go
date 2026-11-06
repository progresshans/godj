package consumer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/ledger"
	"example.com/godj-project-bundle/pricing"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type decimalBackend interface {
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

type decimalPhysical struct {
	Update   func(table, column, expression string, id int64) error
	SetType  func(table, column, definition string) error
	Snapshot func() string
}

func withDecimalBackends(t *testing.T, run func(*testing.T, decimalBackend, string, decimalPhysical)) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "decimal.db"))
		backend, err := sqlite.Open(t.Context(), dsn)
		check(t, err)
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateDecimal(t, backend)
		raw, err := sql.Open("sqlite", dsn)
		check(t, err)
		t.Cleanup(func() { check(t, raw.Close()) })
		run(t, backend, "sqlite", decimalPhysical{
			Update: func(table, column, expression string, id int64) error {
				_, err := raw.ExecContext(t.Context(), `UPDATE main.`+pgx.Identifier{"gdj_decimal_" + table}.Sanitize()+` SET `+pgx.Identifier{column}.Sanitize()+` = `+expression+` WHERE id = ?`, id)
				return err
			},
			SetType: func(string, string, string) error { return nil }, // SQLite BLOB columns already have no NUMERIC typmod.
			Snapshot: func() string {
				var tables []any
				for _, table := range []string{"rate", "entry", "adjustment"} {
					rows, err := raw.QueryContext(t.Context(), `SELECT * FROM main.`+pgx.Identifier{"gdj_decimal_" + table}.Sanitize()+` ORDER BY id`)
					check(t, err)
					columns, err := rows.Columns()
					check(t, err)
					var records [][]string
					for rows.Next() {
						values := make([]any, len(columns))
						destinations := make([]any, len(columns))
						for i := range values {
							destinations[i] = &values[i]
						}
						check(t, rows.Scan(destinations...))
						record := make([]string, len(values))
						for i, value := range values {
							record[i] = fmt.Sprintf("%T:%v", value, value)
						}
						records = append(records, record)
					}
					check(t, rows.Err())
					check(t, rows.Close())
					tables = append(tables, records)
				}
				data, err := json.Marshal(tables)
				check(t, err)
				return string(data)
			},
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
		schema := fmt.Sprintf("godj_decimal_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
		_, err = connection.Exec(t.Context(), "CREATE SCHEMA "+quoted)
		check(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
			check(t, err)
			check(t, connection.Close(ctx))
		})
		backend, err := postgres.Open(t.Context(), postgres.Config{URL: url, Schema: schema})
		check(t, err)
		t.Cleanup(func() { check(t, backend.Close()) })
		migrateDecimal(t, backend)
		run(t, backend, "postgres", decimalPhysical{
			Update: func(table, column, expression string, id int64) error {
				_, err := connection.Exec(t.Context(), `UPDATE `+quoted+`.`+pgx.Identifier{"gdj_decimal_" + table}.Sanitize()+` SET `+pgx.Identifier{column}.Sanitize()+` = `+expression+` WHERE id = $1`, id)
				return err
			},
			SetType: func(table, column, definition string) error {
				_, err := connection.Exec(t.Context(), `ALTER TABLE `+quoted+`.`+pgx.Identifier{"gdj_decimal_" + table}.Sanitize()+` ALTER COLUMN `+pgx.Identifier{column}.Sanitize()+` TYPE `+definition)
				return err
			},
			Snapshot: func() string {
				var tables [][]string
				for _, table := range []string{"rate", "entry", "adjustment"} {
					rows, err := connection.Query(t.Context(), `SELECT row_to_json(t)::text FROM (SELECT * FROM `+quoted+`.`+pgx.Identifier{"gdj_decimal_" + table}.Sanitize()+` ORDER BY id) t`)
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

func migrateDecimal(t *testing.T, backend decimalBackend) {
	t.Helper()
	first := migrations.Migration{App: "pricing", Name: "0001_initial"}
	second := migrations.Migration{App: "ledger", Name: "0001_initial", Dependencies: []migrations.MigrationKey{first.Key()}}
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
				t.Fatal("missing decimal model", name)
			}
		}
	}
	appendModels(&first, pricing.GoDjRelationSchema(), "rate")
	appendModels(&second, ledger.GoDjRelationSchema(), "entry", "adjustment")
	var sources []definition.Source
	for _, migration := range []migrations.Migration{first, second} {
		wire, err := definition.Encode(definition.Producer{Name: "decimal-consumer", Version: "1"}, migration)
		check(t, err)
		sources = append(sources, definition.Source{SourceID: migration.App, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	check(t, err)
	_, err = (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest())
	check(t, err)
}

type decimalProbe struct {
	decimalBackend
	plans []query.Plan
}

func (p *decimalProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.plans = append(p.plans, plan)
	return p.decimalBackend.Query(ctx, plan)
}
