package migrationgraphtest

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// RunFieldPositions exercises logical insertion independently of physical
// column order, through serialized definitions and the public DB lifecycle.
func RunFieldPositions(t *testing.T, ctx context.Context, open func() Binding) {
	t.Helper()
	fk := func(name, target string) ir.Field {
		return ir.Field{Name: name, GoName: "Field" + name, Kind: ir.FieldForeignKey, Nullable: true,
			Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "position", ModelName: target}, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}}
	}
	model := func(name string, fields ...ir.Field) ir.Model {
		value, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "position", Models: []ir.Model{{Name: name, GoName: "Model" + name, Fields: fields}}})
		if err != nil {
			t.Fatal(err)
		}
		return value.Models[0]
	}
	label := ir.Field{Name: "label", GoName: "Label", Kind: ir.FieldText}
	key := ir.Field{Name: "key", GoName: "Key", Kind: ir.FieldAuto, PrimaryKey: true}
	marker := ir.Field{Name: "marker", GoName: "Marker", Kind: ir.FieldInteger, Nullable: true}
	a := model("a", label, key, fk("parent", "a"))
	b := model("b", label, fk("owner", "a"))
	linked := model("a", fk("peer", "b"), label, marker, key, fk("loop", "a"), fk("parent", "a"))
	full := model("a", fk("review", "b"), fk("peer", "b"), label, marker, key, fk("loop", "a"), fk("parent", "a"), ir.Field{Name: "tail", GoName: "Tail", Kind: ir.FieldText, Nullable: true})
	definitions := []migrations.Migration{
		{App: "position", Name: "0001_base", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "position", Model: a}, migrations.CreateModel{AppLabel: "position", Model: b}}},
		{App: "position", Name: "0002_insert", Dependencies: []migrations.MigrationKey{{App: "position", Name: "0001_base"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "position", ModelName: "a", Field: linked.Fields[0], BeforeField: "label"},
			migrations.AddField{AppLabel: "position", ModelName: "a", Field: linked.Fields[2], BeforeField: "key"},
			migrations.AddField{AppLabel: "position", ModelName: "a", Field: linked.Fields[4], BeforeField: "parent"},
		}},
		{App: "position", Name: "0003_insert", Dependencies: []migrations.MigrationKey{{App: "position", Name: "0002_insert"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "position", ModelName: "a", Field: full.Fields[0], BeforeField: "peer"},
			migrations.AddField{AppLabel: "position", ModelName: "a", Field: full.Fields[7]},
		}},
	}
	sources := make([]definition.Source, len(definitions))
	for index, migration := range definitions {
		doc, err := definition.Encode(definition.Producer{Name: "field-position-product", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources[index] = definition.Source{SourceID: migration.Name, Document: doc}
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	binding := open()
	expand := func(sql string) string {
		return strings.NewReplacer("{a}", binding.Table("position_a"), "{b}", binding.Table("position_b"), "{history}", binding.Table("godj_migrations")).Replace(sql)
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := binding.Database.ExecContext(ctx, expand(sql)); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	count := func(sql string, want int) {
		t.Helper()
		var got int
		if err := binding.Database.QueryRowContext(ctx, expand(sql)).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: %d, want %d: %v", sql, got, want, err)
		}
	}
	migrate := func(index int) {
		t.Helper()
		request := migrations.TargetedLifecycleRequest(migrations.ZeroTarget("position"))
		if index >= 0 {
			request = migrations.TargetedLifecycleRequest(migrations.NamedTarget(definitions[index].Key()))
		}
		state, err := (migrations.Executor{Backend: binding.Backend}).Migrate(ctx, loaded, request)
		if err != nil {
			t.Fatalf("field position target %d: %v", index, err)
		}
		count(`SELECT COUNT(*) FROM {history} WHERE app='position'`, index+1)
		if index >= 0 {
			actual, ok := state.Model("position", "a")
			want := []ir.Model{a, linked, full}[index]
			if !ok || !reflect.DeepEqual(actual, want) {
				t.Fatalf("target %d reordered the historical model", index)
			}
		} else if _, exists := state.Schema("position"); exists {
			t.Fatal("zero retained inserted fields")
		}
	}
	for index, migration := range definitions {
		sql, err := migrations.RenderMigrationSQL(ctx, loaded, migration.Key(), binding.Renderer)
		if err != nil || len(sql) != []int{2, 3, 2}[index] {
			t.Fatalf("position SQL %d: %v", index, err)
		}
		if index == 1 && (!strings.Contains(sql[0], `"peer_id"`) || !strings.Contains(sql[1], `"marker"`) || !strings.Contains(sql[2], `"loop_id"`)) {
			t.Fatal("SQL renderer selected a retained field instead of the inserted field")
		}
	}
	migrate(0)
	exec(`INSERT INTO {a} ("label") VALUES ('alpha'), ('beta'), ('spare'), ('spare'), ('spare')`)
	exec(`DELETE FROM {a} WHERE "key">2`)
	exec(`UPDATE {a} SET "parent_id"=2 WHERE "key"=1`)
	exec(`UPDATE {a} SET "parent_id"=1 WHERE "key"=2`)
	exec(`INSERT INTO {b} ("label", "owner_id") VALUES ('inbound', 2)`)
	migrate(1)
	exec(`UPDATE {a} SET "peer_id"=1, "marker"=42, "loop_id"=1 WHERE "key"=1`)
	exec(`ALTER TABLE {a} RENAME COLUMN "label" TO "tampered"`)
	if _, err := (migrations.Executor{Backend: binding.Backend}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(definitions[2].Key()))); err == nil {
		t.Fatal("accepted a renamed retained column despite logical insertion")
	}
	count(`SELECT COUNT(*) FROM {history} WHERE app='position'`, 2)
	exec(`ALTER TABLE {a} RENAME COLUMN "tampered" TO "label"`)
	migrate(2)
	exec(`UPDATE {a} SET "review_id"=1, "tail"='retained row' WHERE "key"=1`)
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
	binding = open()
	migrate(2)
	count(`SELECT COUNT(*) FROM {a} WHERE "key"=1 AND "label"='alpha' AND "marker"=42 AND "parent_id"=2 AND "peer_id"=1 AND "loop_id"=1 AND "review_id"=1 AND "tail"='retained row'`, 1)
	migrate(1)
	count(`SELECT COUNT(*) FROM {a} WHERE "key"=1 AND "label"='alpha' AND "marker"=42 AND "parent_id"=2 AND "peer_id"=1 AND "loop_id"=1`, 1)
	migrate(0)
	count(`SELECT COUNT(*) FROM {a} WHERE ("key"=1 AND "label"='alpha' AND "parent_id"=2) OR ("key"=2 AND "label"='beta' AND "parent_id"=1)`, 2)
	count(`SELECT COUNT(*) FROM {b} WHERE "owner_id"=2 AND "label"='inbound'`, 1)
	exec(`INSERT INTO {a} ("label") VALUES ('next')`)
	count(`SELECT "key" FROM {a} WHERE "label"='next'`, 6)
	if _, err := binding.Database.ExecContext(ctx, expand(`UPDATE {b} SET "owner_id"=999999`)); err == nil {
		t.Fatal("foreign key enforcement lost after inserted-field reversal")
	}
	migrate(-1)
	migrate(2)
	for _, table := range []string{"{a}", "{b}"} {
		count(fmt.Sprintf("SELECT COUNT(*) FROM %s", table), 0)
	}
	migrate(-1)
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
}
