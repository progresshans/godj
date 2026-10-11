package migrationgraphtest

import (
	"context"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// The two apps intentionally use the same model name. Their migration
// dependencies form a DAG while their eventual model references form a cycle.
func crossApp(t *testing.T, ctx context.Context, binding Binding) {
	t.Helper()
	model := func(app, target string) ir.Model {
		fields := []ir.Field{{Name: "label", GoName: "Label", Kind: ir.FieldChar, MaxLength: 20}}
		if target != "" {
			fields = append(fields, ir.Field{Name: "peer", GoName: "PeerID", Kind: ir.FieldForeignKey, Nullable: true,
				Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: target, ModelName: "item"},
					Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}})
		}
		schema, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: app,
			Models: []ir.Model{{Name: "item", GoName: "Item", Fields: fields}}})
		if err != nil {
			t.Fatal(err)
		}
		return schema.Models[0]
	}
	left, right, complete := model("left", ""), model("right", "left"), model("left", "right")
	definitions := []migrations.Migration{
		{App: "left", Name: "0001_item", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "left", Model: left}}},
		{App: "right", Name: "0001_item", Dependencies: []migrations.MigrationKey{{App: "left", Name: "0001_item"}},
			Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "right", Model: right}}},
		{App: "left", Name: "0002_peer", Dependencies: []migrations.MigrationKey{{App: "left", Name: "0001_item"}, {App: "right", Name: "0001_item"}},
			Operations: []migrations.Operation{migrations.AddField{AppLabel: "left", ModelName: "item", Field: complete.Fields[2]}}},
	}
	sources := make([]definition.Source, len(definitions))
	for index, migration := range definitions {
		encoded, err := definition.Encode(definition.Producer{Name: "cross-app-graph-product", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources[index] = definition.Source{SourceID: migration.App + "/" + migration.Name, Document: encoded}
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	migrate := func(request migrations.LifecycleRequest) migrations.ProjectState {
		t.Helper()
		state, err := (migrations.Executor{Backend: binding.Backend}).Migrate(ctx, loaded, request)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := binding.Database.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	leftTable, rightTable := binding.Table("left_item"), binding.Table("right_item")
	latest := migrate(migrations.LatestLifecycleRequest())
	exec("INSERT INTO " + leftTable + " (label) VALUES ('left')")
	exec("INSERT INTO " + rightTable + " (label, peer_id) VALUES ('right', 1)")
	exec("UPDATE " + leftTable + " SET peer_id=1")
	verify := func() {
		t.Helper()
		var label string
		if err := binding.Database.QueryRowContext(ctx, "SELECT l.label FROM "+rightTable+" r JOIN "+leftTable+" l ON r.peer_id=l.id").Scan(&label); err != nil || label != "left" {
			t.Fatalf("cross-app incoming edge was lost: label=%q err=%v", label, err)
		}
	}
	verify()
	previous := migrate(migrations.TargetedLifecycleRequest(migrations.NamedTarget(definitions[0].Key())))
	if source, ok := previous.Model("left", "item"); !ok || len(source.Fields) != 2 {
		t.Fatal("cross-app reverse did not remove only the back edge")
	}
	verify() // SQLite remakes left while right still references its existing row.
	if reapplied := migrate(migrations.LatestLifecycleRequest()); !reapplied.Equal(latest) {
		t.Fatal("cross-app reapply changed the historical graph")
	}
	verify()
	if empty := migrate(migrations.TargetedLifecycleRequest(migrations.ZeroTarget("left"))); len(empty.Apps()) != 0 {
		t.Fatal("cross-app reverse left a dependent model behind")
	}
}
