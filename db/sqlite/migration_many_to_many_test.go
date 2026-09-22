package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/progresshans/godj/internal/manytomanytest"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestSQLiteExplicitManyToManyPreservesRowsCatalogSequenceAndReopens(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		for _, unique := range []bool{false, true} {
			for _, self := range []bool{false, true} {
				for _, cross := range []bool{false, true} {
					t.Run(fmt.Sprintf("nullable_%t_unique_%t_self_%t_cross_%t", nullable, unique, self, cross), func(t *testing.T) {
						ctx := t.Context()
						path := filepath.Join(t.TempDir(), "many.sqlite")
						database := openMigrationHistoryFileBackend(t, path)
						history, models, _ := manytomanytest.History(t, nullable, unique, self)
						if cross {
							history, models, _ = manytomanytest.CrossHistory(t, nullable, unique, self)
						}
						loaded := sqliteUniqueHistory(t, history...)
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
						sqliteUniqueExec(t, database, `INSERT INTO manyhistory_owner(id) VALUES(1),(2)`)
						sqliteUniqueExec(t, database, `INSERT INTO manyhistory_label(id) VALUES(1),(2)`)
						sqliteUniqueExec(t, database, `INSERT INTO manyhistory_membership(id,owner_id,label_id,note) VALUES(200,1,1,'past')`)
						sqliteUniqueExec(t, database, `DELETE FROM manyhistory_membership`)
						sqliteUniqueExec(t, database, `INSERT INTO manyhistory_membership(id,owner_id,label_id,note) VALUES(1,1,1,'kept'),(2,2,1,'kept')`)
						if nullable {
							sqliteUniqueExec(t, database, `INSERT INTO manyhistory_membership(owner_id,label_id,note) VALUES(NULL,NULL,'null')`)
						}
						if !unique {
							sqliteUniqueExec(t, database, `INSERT INTO manyhistory_membership(owner_id,label_id,note) VALUES(1,1,'duplicate')`)
						}
						before := sqliteUniqueReadSnapshot(t, database, models...)
						for _, index := range []int{1, 2, 3, 2, 1, 0, 1, 2} {
							bodies, err := migrations.RenderMigrationSQL(ctx, loaded, history[index].Key(), NewMigrationSQLRenderer())
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
							after := sqliteUniqueReadSnapshot(t, database, models...)
							after.revision = before.revision
							if !reflect.DeepEqual(before, after) {
								t.Fatal("columnless change modified rows, physical schema, FK state or sequence", index)
							}
						}
						if err := database.Close(); err != nil {
							t.Fatal(err)
						}
						database = openMigrationHistoryFileBackend(t, path)
						executor.Backend = database
						if _, err := executor.Migrate(ctx, loaded, target(0)); err != nil {
							t.Fatal("reopened reverse", err)
						}
						after := sqliteUniqueReadSnapshot(t, database, models...)
						after.revision = before.revision
						if !reflect.DeepEqual(before, after) {
							t.Fatal("reopened reverse rewrote storage")
						}
					})
				}
			}
		}
	}
}

func TestSQLiteExplicitManyToManyLateFailureAndPhysicalDriftAreAtomic(t *testing.T) {
	for _, mode := range []string{"late_unique", "through_drift", "endpoint_drift", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			database := openMigrationHistoryFileBackend(t, filepath.Join(t.TempDir(), "many.sqlite"))
			history, models, _ := manytomanytest.History(t, false, true, false)
			history = history[:2]
			if mode == "late_unique" {
				history[1].Operations = append(history[1].Operations, migrations.AddConstraint{AppLabel: "manyhistory", ModelName: "membership", Constraint: ir.UniqueConstraint{Name: "unique_note", Fields: []string{"note"}}})
			}
			loaded := sqliteUniqueHistory(t, history...)
			executor := migrations.Executor{Backend: database}
			initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(history[0].Key()))
			if _, err := executor.Migrate(ctx, loaded, initial); err != nil {
				t.Fatal(err)
			}
			sqliteUniqueExec(t, database, `INSERT INTO manyhistory_owner(id) VALUES(1),(2)`)
			sqliteUniqueExec(t, database, `INSERT INTO manyhistory_label(id) VALUES(1)`)
			sqliteUniqueExec(t, database, `INSERT INTO manyhistory_membership(owner_id,label_id,note) VALUES(1,1,'same'),(2,1,'same')`)
			if mode == "through_drift" {
				sqliteUniqueExec(t, database, `ALTER TABLE manyhistory_membership ADD COLUMN forged TEXT`)
			}
			if mode == "endpoint_drift" {
				sqliteUniqueExec(t, database, `ALTER TABLE manyhistory_label ADD COLUMN forged TEXT`)
			}
			before := sqliteUniqueReadSnapshot(t, database, models...)
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
				assertSQLiteNativeUniqueError(t, err)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if mode != "canceled" {
				owner, _ := state.Model("manyhistory", "owner")
				if len(owner.ManyToMany) != 0 {
					t.Fatal("failed change published new relation")
				}
			}
			if after := sqliteUniqueReadSnapshot(t, database, models...); !reflect.DeepEqual(before, after) {
				t.Fatal("failed migration changed durable state")
			}
		})
	}
}

func TestSQLiteExplicitManyToManySealBindsNestedMetadata(t *testing.T) {
	_, models, field := manytomanytest.History(t, false, true, false)
	owner := models[0].Clone()
	owner.ManyToMany = []ir.ManyToManyField{field}
	intent := mb.MigrationIntent{Operations: []mb.MigrationOperation{{OperationIndex: 0, Kind: mb.MigrationAlterManyToMany, Before: models[0], After: owner, RelatedModels: []mb.MigrationModel{{AppLabel: "manyhistory", Model: models[1]}, {AppLabel: "manyhistory", Model: models[2]}}}}}
	seal, err := validateAndSealSQLiteRelationIntent(mb.HistoryTransition{Migration: mb.AppliedMigration{App: "manyhistory", Name: "0002_labels"}, Kind: mb.HistoryTransitionApply}, intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.Operations[0].After.ManyToMany[0].Through.SourceField = "external"
	if err := verifySQLiteRelationIntentSeal(&seal); err != nil {
		t.Fatal("seal retained caller alias", err)
	}
	seal.intent.Operations[0].After.ManyToMany[0].Through.SourceField = "tampered"
	if err := verifySQLiteRelationIntentSeal(&seal); err == nil {
		t.Fatal("seal omitted nested relation metadata")
	}
}
