package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"reflect"
	"testing"

	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

type postgresManyStorageSnapshot struct {
	catalog  postgresMigrationTableCatalog
	rows     string
	sequence int64
	called   bool
}

func postgresManyStorage(t *testing.T, database *Backend, namespace string, models []ir.Model) []postgresManyStorageSnapshot {
	t.Helper()
	ctx := t.Context()
	var result []postgresManyStorageSnapshot
	for _, model := range models {
		var value postgresManyStorageSnapshot
		catalog, exists, err := loadPostgresMigrationTableCatalog(ctx, database.database, namespace, model.DBTable)
		if err != nil || !exists {
			t.Fatal("catalog missing", err)
		}
		value.catalog = catalog
		table, err := quoteTable(namespace, model.DBTable)
		if err != nil {
			t.Fatal(err)
		}
		if err := database.database.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(entry) ORDER BY id)::text,'[]') FROM `+table+` AS entry`).Scan(&value.rows); err != nil {
			t.Fatal(err)
		}
		name, err := postgresIdentitySequenceName(model.DBTable, "id")
		if err != nil {
			t.Fatal(err)
		}
		sequence, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := database.database.QueryRowContext(ctx, `SELECT last_value,is_called FROM `+sequence).Scan(&value.sequence, &value.called); err != nil {
			t.Fatal(err)
		}
		result = append(result, value)
	}
	return result
}

func TestPostgresExplicitManyToManyPreservesRowsCatalogSequenceAndReopens(t *testing.T) {
	url := postgresIntegrationURL(t)
	for _, nullable := range []bool{false, true} {
		for _, unique := range []bool{false, true} {
			for _, self := range []bool{false, true} {
				for _, cross := range []bool{false, true} {
					t.Run(fmt.Sprintf("nullable_%t_unique_%t_self_%t_cross_%t", nullable, unique, self, cross), func(t *testing.T) {
						ctx := t.Context()
						namespace := postgresMigrationIntegrationSchema(t, ctx, url)
						database := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
						history, models, _ := manytomanytest.History(t, nullable, unique, self)
						if cross {
							history, models, _ = manytomanytest.CrossHistory(t, nullable, unique, self)
						}
						loaded := postgresUniqueHistory(t, history...)
						if cross {
							history = history[2:]
						}
						executor := migrations.Executor{Backend: database}
						target := func(index int) migrations.LifecycleRequest {
							return migrations.TargetedLifecycleRequest(migrations.NamedTarget(history[index].Key()))
						}
						if _, err := executor.Migrate(ctx, loaded, target(0)); err != nil {
							t.Fatal(err)
						}
						tables := make([]string, len(models))
						for i, model := range models {
							tables[i], _ = quoteTable(namespace, model.DBTable)
						}
						exec := func(statement string) {
							t.Helper()
							if _, err := database.database.ExecContext(ctx, statement); err != nil {
								t.Fatal(err)
							}
						}
						exec("INSERT INTO " + tables[0] + "(id) VALUES(1),(2)")
						exec("INSERT INTO " + tables[1] + "(id) VALUES(1),(2)")
						exec("INSERT INTO " + tables[2] + "(owner_id,label_id,note) VALUES(1,1,'kept'),(2,1,'kept')")
						if nullable {
							exec("INSERT INTO " + tables[2] + "(owner_id,label_id,note) VALUES(NULL,NULL,'null')")
						}
						if !unique {
							exec("INSERT INTO " + tables[2] + "(owner_id,label_id,note) VALUES(1,1,'duplicate')")
						}
						before := postgresManyStorage(t, database, namespace, models)
						for _, index := range []int{1, 2, 3, 2, 1, 0, 1, 2} {
							bodies, err := migrations.RenderMigrationSQL(ctx, loaded, history[index].Key(), NewMigrationSQLRenderer(MigrationSQLConfig{Schema: namespace}))
							if index != 0 && (err != nil || len(bodies) != 0) {
								t.Fatal("explicit through emitted DDL", bodies, err)
							}
							state, err := executor.Migrate(ctx, loaded, target(index))
							if err != nil {
								t.Fatal("transition", index, err)
							}
							owner, _ := state.Model("manyhistory", "owner")
							expected := []string{"", "labels", "tags", ""}[index]
							if expected == "" && len(owner.ManyToMany) != 0 || expected != "" && (len(owner.ManyToMany) != 1 || owner.ManyToMany[0].Name != expected) {
								t.Fatal("published state differs", index, owner)
							}
							if after := postgresManyStorage(t, database, namespace, models); !reflect.DeepEqual(before, after) {
								t.Fatal("columnless change modified rows, physical catalog or sequence", index)
							}
						}
						if err := database.Close(); err != nil {
							t.Fatal(err)
						}
						database = openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
						executor.Backend = database
						if _, err := executor.Migrate(ctx, loaded, target(0)); err != nil {
							t.Fatal("reopened reverse", err)
						}
						if after := postgresManyStorage(t, database, namespace, models); !reflect.DeepEqual(before, after) {
							t.Fatal("reopened reverse rewrote storage")
						}
					})
				}
			}
		}
	}
}

func TestPostgresExplicitManyToManyLateFailureAndPhysicalDriftAreAtomic(t *testing.T) {
	url := postgresIntegrationURL(t)
	for _, mode := range []string{"late_unique", "through_drift", "endpoint_drift", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			namespace := postgresMigrationIntegrationSchema(t, ctx, url)
			database := openPostgresMigrationIntegrationBackend(t, ctx, url, namespace)
			history, models, _ := manytomanytest.History(t, false, true, false)
			history = history[:2]
			if mode == "late_unique" {
				history[1].Operations = append(history[1].Operations, migrations.AddConstraint{AppLabel: "manyhistory", ModelName: "membership", Constraint: ir.UniqueConstraint{Name: "unique_note", Fields: []string{"note"}}})
			}
			loaded := postgresUniqueHistory(t, history...)
			executor := migrations.Executor{Backend: database}
			initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(history[0].Key()))
			if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
				t.Fatal(err)
			}
			tables := make([]string, len(models))
			for i, model := range models {
				tables[i], _ = quoteTable(namespace, model.DBTable)
			}
			exec := func(statement string) {
				t.Helper()
				if _, err := database.database.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			exec("INSERT INTO " + tables[0] + "(id) VALUES(1),(2)")
			exec("INSERT INTO " + tables[1] + "(id) VALUES(1)")
			exec("INSERT INTO " + tables[2] + "(owner_id,label_id,note) VALUES(1,1,'same'),(2,1,'same')")
			if mode == "through_drift" {
				exec("ALTER TABLE " + tables[2] + " ADD COLUMN forged TEXT")
			}
			if mode == "endpoint_drift" {
				exec("ALTER TABLE " + tables[1] + " ADD COLUMN forged TEXT")
			}
			storage := postgresManyStorage(t, database, namespace, models)
			snapshot := postgresNamedSnapshot(t, database, namespace, models[2])
			if mode == "canceled" {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			state, err := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
			if err == nil {
				t.Fatal("invalid migration reported success")
			}
			if mode == "late_unique" {
				var native *pgconn.PgError
				if !errors.As(err, &native) || native.Code != "23505" {
					t.Fatal("late unique error lost native cause", err)
				}
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if mode != "canceled" {
				owner, _ := state.Model("manyhistory", "owner")
				if len(owner.ManyToMany) != 0 {
					t.Fatal("failed migration published relation")
				}
			}
			if !reflect.DeepEqual(snapshot, postgresNamedSnapshot(t, database, namespace, models[2])) || !reflect.DeepEqual(storage, postgresManyStorage(t, database, namespace, models)) {
				t.Fatal("failed migration changed durable state")
			}
		})
	}
}
