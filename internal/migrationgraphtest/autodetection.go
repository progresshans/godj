package migrationgraphtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/migrationautodetect"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// RunAutodetection uses only generated definitions for schema mutation. It also
// verifies that a required relation deferred across files never backfills a row
// inserted into the intermediate table by another user of the database.
func RunAutodetection(t *testing.T, ctx context.Context, open func(string) Binding) {
	t.Helper()
	for _, cross := range []bool{false, true} {
		t.Run(fmt.Sprintf("cross_app_%t", cross), func(t *testing.T) {
			appA, appB := "autosame", "autosame"
			if cross {
				appA, appB = "autoalpha", "autobeta"
			}
			fk := func(name, app, model string, nullable bool) ir.Field {
				return ir.Field{Name: name, GoName: "Field" + name, Kind: ir.FieldForeignKey, Nullable: nullable,
					Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: app, ModelName: model}, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}}
			}
			label := ir.Field{Name: "label", GoName: "Label", Kind: ir.FieldText}
			key := ir.Field{Name: "key", GoName: "Key", Kind: ir.FieldAuto, PrimaryKey: true}
			a := ir.Model{Name: "a", GoName: "A", Fields: []ir.Field{fk("peer", appB, "b", !cross), label, key, fk("parent", appA, "a", true)}}
			b := ir.Model{Name: "b", GoName: "B", Fields: []ir.Field{fk("owner", appA, "a", true), label, key}}
			schemas := []ir.Schema{{FormatVersion: ir.CurrentFormatVersion, AppLabel: appA, Models: []ir.Model{a}}}
			if cross {
				schemas = append(schemas, ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: appB, Models: []ir.Model{b}})
			} else {
				schemas[0].Models = append(schemas[0].Models, b)
			}
			desired, err := migrations.NewProjectState(schemas...)
			if err != nil {
				t.Fatal(err)
			}
			empty, _, err := definition.Load()
			if err != nil {
				t.Fatal(err)
			}
			plan, err := migrationautodetect.Detect(migrationautodetect.Request{Definitions: empty, Desired: desired, ManagedApps: desired.Apps()})
			if err != nil {
				t.Fatal(err)
			}
			candidates := plan.Migrations()
			wantCandidates := 1
			if cross {
				wantCandidates = 3
			}
			if len(candidates) != wantCandidates {
				t.Fatalf("automatic candidate count=%d want=%d", len(candidates), wantCandidates)
			}
			sources := make([]definition.Source, len(candidates))
			for i, candidate := range candidates {
				doc, err := definition.Encode(definition.Producer{Name: "autodetection-product", Version: "1"}, candidate)
				if err != nil {
					t.Fatal(err)
				}
				sources[i] = definition.Source{SourceID: candidate.App + "/" + candidate.Name, Document: doc}
			}
			loaded, _, err := definition.Load(sources...)
			if err != nil {
				t.Fatal(err)
			}
			caseName := fmt.Sprintf("cross_app_%t", cross)
			binding := open(caseName)
			tableA, tableB := binding.Table(appA+"_a"), binding.Table(appB+"_b")
			exec := func(statement string) {
				t.Helper()
				if _, err := binding.Database.ExecContext(ctx, statement); err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
			}
			count := func(statement string, want int) {
				t.Helper()
				var got int
				if err := binding.Database.QueryRowContext(ctx, statement).Scan(&got); err != nil || got != want {
					t.Fatalf("%s: %d want %d: %v", statement, got, want, err)
				}
			}
			migrate := func(request migrations.LifecycleRequest) migrations.ProjectState {
				t.Helper()
				state, err := (migrations.Executor{Backend: binding.Backend}).Migrate(ctx, loaded, request)
				if err != nil {
					t.Fatal(err)
				}
				return state
			}
			latest := func() migrations.ProjectState {
				t.Helper()
				state := migrate(migrations.LatestLifecycleRequest())
				if !state.Equal(desired) {
					t.Fatal("actual automatic lifecycle changed declaration order or metadata")
				}
				return state
			}
			if cross {
				migrate(migrations.TargetedLifecycleRequest(migrations.NamedTarget(candidates[0].Key())))
				exec("INSERT INTO " + tableA + " (\"label\") VALUES ('must not backfill')")
				_, err := (migrations.Executor{Backend: binding.Backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
				var migrationError *migrations.Error
				if !errors.As(err, &migrationError) || migrationError.Category != migrations.CategoryCapability || migrationError.Code != migrations.CodeUnsupported || !strings.Contains(err.Error(), "contains rows") {
					t.Fatalf("required automatic FK did not fail at the empty-table guard: %v", err)
				}
				count("SELECT COUNT(*) FROM "+tableA+" WHERE \"label\"='must not backfill'", 1)
				count("SELECT COUNT(*) FROM "+binding.Table("godj_migrations"), 2)
				// Qualification prevents SQLite's double-quoted string fallback
				// from making a missing column look like a successful SELECT.
				rows, err := binding.Database.QueryContext(ctx, "SELECT probe.\"peer_id\" FROM "+tableA+" AS probe")
				if rows != nil {
					_ = rows.Close()
				}
				if err == nil {
					t.Fatal("failed required Add published a column")
				}
				exec("DELETE FROM " + tableA)
			}
			latest()
			var aID, bID int64
			if err := binding.Database.QueryRowContext(ctx, "INSERT INTO "+tableB+" (\"label\") VALUES ('beta') RETURNING \"key\"").Scan(&bID); err != nil {
				t.Fatal(err)
			}
			if err := binding.Database.QueryRowContext(ctx, fmt.Sprintf("INSERT INTO %s (\"label\",\"peer_id\") VALUES ('alpha',%d) RETURNING \"key\"", tableA, bID)).Scan(&aID); err != nil {
				t.Fatal(err)
			}
			wantID := int64(1)
			if cross {
				wantID = 2
			}
			if aID != wantID {
				t.Fatalf("automatic migration lost deleted-key highwater: got %d want %d", aID, wantID)
			}
			exec(fmt.Sprintf("UPDATE %s SET \"parent_id\"=%d WHERE \"key\"=%d", tableA, aID, aID))
			exec(fmt.Sprintf("UPDATE %s SET \"owner_id\"=%d WHERE \"key\"=%d", tableB, aID, bID))
			if err := binding.Close(); err != nil {
				t.Fatal(err)
			}
			binding = open(caseName)
			actual := latest()
			assertAutodetectionReference(t, ctx, binding, actual, appA, appB, cross)
			count(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE \"key\"=%d AND \"label\"='alpha' AND \"peer_id\"=%d AND \"parent_id\"=%d", tableA, aID, bID, aID), 1)
			if cross {
				migrate(migrations.TargetedLifecycleRequest(migrations.NamedTarget(candidates[0].Key())))
				count(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE \"key\"=%d AND \"label\"='alpha' AND \"parent_id\"=%d", tableA, aID, aID), 1)
				count(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE \"owner_id\"=%d", tableB, aID), 1)
			}
			if _, err := binding.Database.ExecContext(ctx, "UPDATE "+tableB+" SET \"owner_id\"=999999"); err == nil {
				t.Fatal("automatic lifecycle lost FK enforcement")
			}
			zero := migrations.TargetedLifecycleRequest(migrations.ZeroTarget(appA))
			if state := migrate(zero); len(state.Apps()) != 0 {
				t.Fatal("automatic zero retained part of the cycle")
			}
			latest()
			count("SELECT COUNT(*) FROM "+tableA, 0)
			count("SELECT COUNT(*) FROM "+tableB, 0)
			migrate(zero)
			if err := binding.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
