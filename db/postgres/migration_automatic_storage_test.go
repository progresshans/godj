package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
	"reflect"
	"testing"
)

func TestPostgresAutomaticManyToManyHistoryPreservesIdentitySequenceAndReopens(t *testing.T) {
	for _, self := range []bool{false, true} {
		for _, cross := range []bool{false, true} {
			for _, profile := range []string{"pristine", "empty_highwater", "populated"} {
				t.Run(fmt.Sprintf("self_%t_cross_%t_%s", self, cross, profile), func(t *testing.T) {
					url := postgresIntegrationURL(t)
					namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
					database := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
					history, models, field := manytomanytest.AutomaticHistory(t, self, cross)
					loaded := postgresUniqueHistory(t, history...)
					steps := history[len(history)-4:]
					executor := migrations.Executor{Backend: database}
					migrate := func(index int) {
						t.Helper()
						if _, err := executor.Migrate(t.Context(), loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(steps[index].Key()))); err != nil {
							t.Fatal("transition", index, err)
						}
					}

					exec := func(statement string) {
						t.Helper()
						if _, err := database.database.ExecContext(t.Context(), statement); err != nil {
							t.Fatal(err)
						}
					}
					count := func(statement string) int64 {
						t.Helper()
						var n int64
						if err := database.database.QueryRowContext(t.Context(), statement).Scan(&n); err != nil {
							t.Fatal(err)
						}
						return n
					}
					quote := func(name string) string {
						table, err := quoteTable(namespace, name)
						if err != nil {
							t.Fatal(err)
						}
						return table
					}
					owner, label := quote("manyhistory_owner"), quote("manyhistory_label")
					oldTable, newTable := quote("manyhistory_owner_labels"), quote("manyhistory_owner_tags")

					exists := func(table string) bool {
						var found bool
						err := database.database.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2)`, namespace, table).Scan(&found)
						if err != nil {
							t.Fatal(err)
						}
						return found
					}
					read := func(model ir.Model) (string, string, int64, int64) {
						v := postgresManyStorage(t, database, namespace, []ir.Model{model})[0]
						return v.rows, fmt.Sprint(v.sequence, v.called), v.catalog.oid, v.catalog.sequences[0].oid
					}
					migrate(0)
					exec("INSERT INTO " + owner + "(id,note) VALUES(0,'zero'),(1,'same'),(2,'same')")
					exec("INSERT INTO " + label + "(id) VALUES(0),(1),(2)")
					migrate(1)
					if !exists("manyhistory_owner_labels") {
						t.Fatal("AddManyToMany omitted storage")
					}
					if profile != "pristine" {
						sequence, _ := postgresIdentitySequenceName("manyhistory_owner_labels", "id")
						exec("SELECT pg_catalog.setval('" + quote(sequence) + "'::regclass,200,true)")
					}
					if profile == "populated" {
						exec("INSERT INTO " + oldTable + "(id,source_id,target_id) VALUES(0,0,0),(7,1,1)")
					}
					old := manytomanytest.AutomaticModel(t, models[0], field)
					nextField := field.Clone()
					nextField.Name, nextField.GoName = "tags", "Tags"
					next := manytomanytest.AutomaticModel(t, models[0], nextField)
					rows, sequence, identity, sequenceID := read(old)
					migrate(2)
					if exists("manyhistory_owner_labels") || !exists("manyhistory_owner_tags") {
						t.Fatal("rename did not transfer owned storage")
					}
					gotRows, gotSeq, gotID, gotSequenceID := read(next)
					if rows != gotRows || sequence != gotSeq || identity != gotID || sequenceID != gotSequenceID {
						t.Fatal("rename lost rows, PK zero, sequence or physical identity", rows, gotRows, sequence, gotSeq)
					}
					if profile == "populated" {
						if _, err := database.database.ExecContext(t.Context(), "INSERT INTO "+newTable+"(id,source_id,target_id) VALUES(900,1,1)"); err == nil {
							t.Fatal("pair uniqueness lost after rename")
						}
						if _, err := database.database.ExecContext(t.Context(), "INSERT INTO "+newTable+"(id,source_id,target_id) VALUES(901,99,1)"); err == nil {
							t.Fatal("endpoint FK lost after rename")
						}
					}
					if err := database.Close(); err != nil {
						t.Fatal(err)
					}
					database = openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
					executor.Backend = database
					migrate(1)
					gotRows, gotSeq, gotID, gotSequenceID = read(old)
					if rows != gotRows || sequence != gotSeq || identity != gotID || sequenceID != gotSequenceID {
						t.Fatal("reverse rename after reopen lost storage")
					}
					migrate(3)
					if exists("manyhistory_owner_tags") || exists("manyhistory_owner_labels") {
						t.Fatal("remove retained automatic table")
					}
					if count("SELECT count(*) FROM "+owner) != 3 || count("SELECT count(*) FROM "+label) != 3 {
						t.Fatal("endpoint rows changed")
					}
					migrate(2)
					if count("SELECT count(*) FROM "+newTable) != 0 {
						t.Fatal("reverse removal restored nonexistent rows")
					}
					migrate(0)
					if exists("manyhistory_owner_tags") || exists("manyhistory_owner_labels") {
						t.Fatal("reverse add retained table")
					}
					migrate(1)
					if count("SELECT count(*) FROM "+oldTable) != 0 {
						t.Fatal("reapply is not empty")
					}
				})
			}
		}
	}
}

func TestPostgresAutomaticManyToManyRenameFailurePreservesDurableState(t *testing.T) {
	for _, mode := range []string{"late_unique", "missing_pair", "extra_index", "target_collision", "sequence_collision", "inbound_fk", "dependent_view", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			url := postgresIntegrationURL(t)
			namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
			database := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
			history, models, field := manytomanytest.AutomaticHistory(t, false, false)
			history = history[:3]
			if mode == "late_unique" {
				history[2].Operations = append(history[2].Operations, migrations.AddConstraint{AppLabel: "manyhistory", ModelName: "owner", Constraint: ir.UniqueConstraint{Name: "unique_note", Fields: []string{"note"}}})
			}
			loaded := postgresUniqueHistory(t, history...)
			executor := migrations.Executor{Backend: database}
			if _, err := executor.Migrate(t.Context(), loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(history[1].Key()))); err != nil {
				t.Fatal(err)
			}

			exec := func(statement string) {
				t.Helper()
				if _, err := database.database.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			count := func(statement string) int64 {
				t.Helper()
				var n int64
				if err := database.database.QueryRowContext(t.Context(), statement).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			quote := func(name string) string {
				table, err := quoteTable(namespace, name)
				if err != nil {
					t.Fatal(err)
				}
				return table
			}
			owner, label := quote("manyhistory_owner"), quote("manyhistory_label")
			oldTable, newTable := quote("manyhistory_owner_labels"), quote("manyhistory_owner_tags")

			_ = count
			exec("INSERT INTO " + owner + "(id,note) VALUES(1,'same'),(2,'same')")
			exec("INSERT INTO " + label + "(id) VALUES(1)")
			exec("INSERT INTO " + oldTable + "(id,source_id,target_id) VALUES(0,1,1),(7,2,1)")
			if mode == "missing_pair" {
				name, _ := postgresNamedUniqueConstraintName("manyhistory_owner_labels", "relation_pair")
				q, _ := quoteIdentifier(name)
				exec("ALTER TABLE " + oldTable + " DROP CONSTRAINT " + q)
			}
			if mode == "sequence_collision" {
				name, _ := postgresIdentitySequenceName("manyhistory_owner_tags", "id")
				exec("CREATE SEQUENCE " + quote(name))
			}
			if mode == "extra_index" {
				exec("CREATE INDEX forged ON " + oldTable + "(target_id)")
			}
			if mode == "target_collision" {
				exec("CREATE TABLE " + newTable + "(id INTEGER)")
			}
			if mode == "inbound_fk" {
				exec("CREATE TABLE " + quote("outside_ref") + "(link INTEGER REFERENCES " + oldTable + "(id))")
			}
			if mode == "dependent_view" {
				exec("CREATE VIEW " + quote("outside_view") + " AS SELECT * FROM " + oldTable)
			}
			storage := manytomanytest.AutomaticModel(t, models[0], field)
			before := postgresManyStorage(t, database, namespace, append(models, storage))
			receipt, err := readAtomicPostgresMigrationSnapshot(t.Context(), database)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if mode == "canceled" {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			_, failure := executor.Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
			if failure == nil {
				t.Fatal("invalid rename committed")
			}
			if mode == "late_unique" {
				var native *pgconn.PgError
				if !errors.As(failure, &native) || native.Code != "23505" {
					t.Fatal("late failure never reached native uniqueness", failure)
				}
			}
			if mode == "canceled" && !errors.Is(failure, context.Canceled) {
				t.Fatal("cancellation lost", failure)
			}
			if got := postgresManyStorage(t, database, namespace, append(models, storage)); !reflect.DeepEqual(before, got) {
				t.Fatal("failed rename changed catalog, rows or sequence")
			}
			after, err := readAtomicPostgresMigrationSnapshot(t.Context(), database)
			if err != nil || !reflect.DeepEqual(receipt, after) {
				t.Fatal("failed rename changed revision/history", err)
			}
		})
	}
}

func TestPostgresAutomaticManyToManyInlineCreateTransientAndMetadataRename(t *testing.T) {
	for _, mode := range []string{"inline_mixed", "inline_storage_target", "transient", "go_name", "owner_remake"} {
		t.Run(mode, func(t *testing.T) {
			url := postgresIntegrationURL(t)
			namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
			database := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
			history, models, field := manytomanytest.AutomaticHistory(t, true, false)
			history = history[:2]
			if mode == "inline_mixed" || mode == "inline_storage_target" {
				second := field.Clone()
				second.Name, second.GoName = "other", "Other"
				alias := field.Clone()
				alias.Name, alias.GoName = "alias", "Alias"
				alias.Through = &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "manyhistory", ModelName: "owner_labels"}, SourceField: "source", TargetField: "target"}
				models[0].ManyToMany = []ir.ManyToManyField{field, second, alias}
				if mode == "inline_storage_target" {
					second.Target = ir.ModelIdentity{AppLabel: "manyhistory", ModelName: "owner_labels"}
					second.Symmetry = ir.ManyToManyDirected
					models[0].ManyToMany = []ir.ManyToManyField{second, field, alias}
				}
				history = history[:1]
				history[0].Operations[0] = migrations.CreateModel{AppLabel: "manyhistory", Model: models[0]}
			}
			if mode == "transient" {
				rename := field.Clone()
				rename.Name, rename.GoName = "tags", "Tags"
				history[1].Operations = append(history[1].Operations, migrations.RenameManyToMany{AppLabel: "manyhistory", ModelName: "owner", Before: field, After: rename}, migrations.RemoveManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: rename}, migrations.AddManyToMany{AppLabel: "manyhistory", ModelName: "owner", Field: field})
			}
			if mode == "go_name" {
				rename := field.Clone()
				rename.GoName = "Renamed"
				history = append(history, migrations.Migration{App: "manyhistory", Name: "0003_go_name", Dependencies: []migrations.MigrationKey{history[1].Key()}, Operations: []migrations.Operation{migrations.RenameManyToMany{AppLabel: "manyhistory", ModelName: "owner", Before: field, After: rename}}})
			}
			if mode == "owner_remake" {
				fk := ir.Field{Name: "parent", GoName: "ParentID", Column: "parent_id", Kind: ir.FieldForeignKey, Nullable: true, Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "manyhistory", ModelName: "owner"}, Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}, OnDelete: ir.DeleteCascade}}
				history = append(history, migrations.Migration{App: "manyhistory", Name: "0003_parent", Dependencies: []migrations.MigrationKey{history[1].Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "manyhistory", ModelName: "owner", Field: fk}}})
			}
			loaded := postgresUniqueHistory(t, history...)
			executor := migrations.Executor{Backend: database}
			if mode == "go_name" {
				bodies, err := migrations.RenderMigrationSQL(t.Context(), loaded, history[2].Key(), NewMigrationSQLRenderer(MigrationSQLConfig{Schema: namespace}))
				if err != nil || len(bodies) != 0 {
					t.Fatal("Go-name-only rename changed physical storage", bodies, err)
				}
			}
			if _, err := executor.Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
				t.Fatal(err)
			}
			exists := func(table string) bool {
				var found bool
				if err := database.database.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2)`, namespace, table).Scan(&found); err != nil {
					t.Fatal(err)
				}
				return found
			}
			if !exists("manyhistory_owner_labels") || exists("manyhistory_owner_tags") {
				t.Fatal("final owned storage inventory differs")
			}
			if (mode == "inline_mixed" || mode == "inline_storage_target") && !exists("manyhistory_owner_other") {
				t.Fatal("second inline declaration omitted")
			}
			if mode == "owner_remake" {
				quote := func(name string) string {
					v, err := quoteTable(namespace, name)
					if err != nil {
						t.Fatal(err)
					}
					return v
				}
				if _, err := database.database.ExecContext(t.Context(), "INSERT INTO "+quote("manyhistory_owner")+"(id,note) VALUES(1,'kept')"); err != nil {
					t.Fatal(err)
				}
				if _, err := database.database.ExecContext(t.Context(), "INSERT INTO "+quote("manyhistory_owner_labels")+"(id,source_id,target_id) VALUES(0,1,1)"); err != nil {
					t.Fatal(err)
				}
				if _, err := executor.Migrate(t.Context(), loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(history[1].Key()))); err != nil {
					t.Fatal("owner remake with retained automatic links", err)
				}
				var id int64
				if err := database.database.QueryRowContext(t.Context(), "SELECT id FROM "+quote("manyhistory_owner_labels")).Scan(&id); err != nil || id != 0 {
					t.Fatal("owner remake lost link", err)
				}
			}
			if _, err := executor.Migrate(t.Context(), loaded, migrations.TargetedLifecycleRequest(migrations.ZeroTarget("manyhistory"))); err != nil {
				t.Fatal("reverse owned lifecycle", err)
			}
			if exists("manyhistory_owner_labels") || exists("manyhistory_owner_tags") || exists("manyhistory_owner_other") || exists("manyhistory_owner") {
				t.Fatal("reverse CreateModel left owned objects")
			}
		})
	}
}

func TestPostgresAutomaticManyToManySQLProjectionExecutesCompleteStorage(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	database := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	history, _, _ := manytomanytest.AutomaticHistory(t, false, false)
	loaded := postgresUniqueHistory(t, history...)
	quote := func(name string) string {
		v, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	exists := func(table string) bool {
		var found bool
		if err := database.database.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2)`, namespace, table).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	for i, change := range history {
		bodies, err := migrations.RenderMigrationSQL(t.Context(), loaded, change.Key(), NewMigrationSQLRenderer(MigrationSQLConfig{Schema: namespace}))
		if err != nil || len(bodies) == 0 {
			t.Fatal("missing physical SQL group", i, bodies, err)
		}
		tx, err := database.database.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, body := range bodies {
			if _, err := tx.ExecContext(t.Context(), body); err != nil {
				_ = tx.Rollback()
				t.Fatal(i, body, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			for _, sql := range []string{"INSERT INTO " + quote("manyhistory_owner") + "(id,note) VALUES(1,'kept')", "INSERT INTO " + quote("manyhistory_label") + "(id) VALUES(1)", "INSERT INTO " + quote("manyhistory_owner_labels") + "(id,source_id,target_id) VALUES(0,1,1)"} {
				if _, err := database.database.ExecContext(t.Context(), sql); err != nil {
					t.Fatal(err)
				}
			}
		}
		if i == 2 {
			var id int64
			if err := database.database.QueryRowContext(t.Context(), "SELECT id FROM "+quote("manyhistory_owner_tags")).Scan(&id); err != nil || id != 0 {
				t.Fatal("SQL projection lost retained identity", err)
			}
		}
	}
	if exists("manyhistory_owner_labels") || exists("manyhistory_owner_tags") || !exists("manyhistory_owner") || !exists("manyhistory_label") {
		t.Fatal("projection left wrong owned inventory")
	}
}
