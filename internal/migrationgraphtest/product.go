// Package migrationgraphtest exercises historical relation graphs through the
// public definition, SQL projection, and revision-fenced lifecycle boundaries.
package migrationgraphtest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

type Binding struct {
	Backend  migrationbackend.RevisionFencedBackend
	Database *sql.DB
	Table    func(string) string
	Renderer migrationbackend.MigrationSQLRenderer
	Close    func() error
}

func Definitions(t *testing.T) (migrations.LoadedDefinitionSet, []migrations.MigrationKey) {
	t.Helper()
	fk := func(name, target string) ir.Field {
		return ir.Field{Name: name, GoName: "Field" + name, Kind: ir.FieldForeignKey, Nullable: true,
			Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "graph", ModelName: target},
				Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}}
	}
	model := func(name string, fields ...ir.Field) ir.Model {
		value, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "graph",
			Models: []ir.Model{{Name: name, GoName: "Model" + name, Fields: fields}}})
		if err != nil {
			t.Fatal(err)
		}
		return value.Models[0]
	}
	label := ir.Field{Name: "label", GoName: "Label", Kind: ir.FieldChar, MaxLength: 20}
	a := model("a", label, fk("parent", "a"))
	b := model("b", label, fk("a", "a"))
	c := model("c", label, fk("b", "b"))
	aComplete := model("a", label, fk("parent", "a"), fk("peer", "b"), fk("tertiary", "c"))
	cComplete := model("c", label, fk("b", "b"), fk("loop", "c"))
	choices := b.Fields[1].Clone()
	choices.Choices = []ir.Choice{{Value: ir.Scalar{Kind: ir.ScalarString, String: "beta"}, Label: "Beta"}}
	definitions := []migrations.Migration{
		{App: "graph", Name: "0001_base", Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "graph", Model: a}, migrations.CreateModel{AppLabel: "graph", Model: b}, migrations.CreateModel{AppLabel: "graph", Model: c},
		}},
		{App: "graph", Name: "0002_links", Dependencies: []migrations.MigrationKey{{App: "graph", Name: "0001_base"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "graph", ModelName: "a", Field: aComplete.Fields[3]}, migrations.AddField{AppLabel: "graph", ModelName: "a", Field: aComplete.Fields[4]},
		}},
		{App: "graph", Name: "0003_self", Dependencies: []migrations.MigrationKey{{App: "graph", Name: "0002_links"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "graph", ModelName: "c", Field: cComplete.Fields[3]},
		}},
		{App: "graph", Name: "0004_choices", Dependencies: []migrations.MigrationKey{{App: "graph", Name: "0003_self"}}, Operations: []migrations.Operation{
			migrations.AlterField{AppLabel: "graph", ModelName: "b", Before: b.Fields[1], After: choices},
		}},
	}
	sources := make([]definition.Source, len(definitions))
	keys := make([]migrations.MigrationKey, len(definitions))
	for index, migration := range definitions {
		encoded, err := definition.Encode(definition.Producer{Name: "migration-graph-product", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources[index] = definition.Source{SourceID: migration.Name, Document: encoded}
		keys[index] = migration.Key()
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return loaded, keys
}

// Run creates its tables exclusively through public migration execution.
// Raw SQL only seeds and observes rows after that schema exists.
func Run(t *testing.T, ctx context.Context, open func() Binding) {
	t.Helper()
	loaded, keys := Definitions(t)
	binding := open()
	var observations []graphObservation
	observePhase := func(phase string, state migrations.ProjectState) {
		observations = append(observations, observe(t, ctx, binding, phase, state))
	}
	expand := func(statement string) string {
		return strings.NewReplacer("{a}", binding.Table("graph_a"), "{b}", binding.Table("graph_b"),
			"{c}", binding.Table("graph_c"), "{history}", binding.Table("godj_migrations")).Replace(statement)
	}
	exec := func(statement string) {
		t.Helper()
		if _, err := binding.Database.ExecContext(ctx, expand(statement)); err != nil {
			t.Fatalf("SQL %s: %v", statement, err)
		}
	}
	count := func(statement string, want int64) {
		t.Helper()
		var got int64
		if err := binding.Database.QueryRowContext(ctx, expand(statement)).Scan(&got); err != nil || got != want {
			t.Fatalf("SQL %s = %d, want %d: %v", statement, got, want, err)
		}
	}
	migrate := func(index int) migrations.ProjectState {
		t.Helper()
		request := migrations.TargetedLifecycleRequest(migrations.ZeroTarget("graph"))
		if index >= 0 {
			request = migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[index]))
		}
		state, err := (migrations.Executor{Backend: binding.Backend}).Migrate(ctx, loaded, request)
		if err != nil {
			t.Fatalf("migrate target %d: %v", index, err)
		}
		count(`SELECT COUNT(*) FROM {history}`, int64(index+1))
		return state
	}
	for index, key := range keys {
		statements, err := migrations.RenderMigrationSQL(ctx, loaded, key, binding.Renderer)
		want := []int{3, 2, 1, 0}[index]
		if err != nil || len(statements) != want {
			t.Fatalf("SQL projection %s: %d statements, want %d: %v", key.Name, len(statements), want, err)
		}
	}
	initial := migrate(0)
	exec(`INSERT INTO {a} ("label") VALUES ('alpha'), ('second')`)
	exec(`UPDATE {a} SET "parent_id"=2 WHERE "id"=1`)
	exec(`UPDATE {a} SET "parent_id"=1 WHERE "id"=2`)
	exec(`WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<98) INSERT INTO {a} ("label") SELECT 'spare' FROM seq`)
	exec(`DELETE FROM {a} WHERE "id">2`)
	exec(`INSERT INTO {b} ("label", "a_id") VALUES ('beta', 2)`)
	exec(`INSERT INTO {c} ("label", "b_id") VALUES ('gamma', 1)`)
	observePhase("base", initial)
	linked := migrate(1)
	exec(`UPDATE {a} SET "peer_id"=1, "tertiary_id"=1 WHERE "id"=1`)
	exec(`UPDATE {a} SET "peer_id"=1 WHERE "id"=2`)
	observePhase("links", linked)
	// A is only transitive authority for the next C self AddField. A full
	// historical target check must reject drift outside its PK without
	// publishing C's schema/recorder successor.
	exec(`ALTER TABLE {a} ADD COLUMN "unrecorded" BIGINT NULL`)
	if _, err := (migrations.Executor{Backend: binding.Backend}).Migrate(ctx, loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(keys[2]))); err == nil {
		t.Fatal("accepted physical drift in a transitive historical target")
	}
	count(`SELECT COUNT(*) FROM {history}`, 2)
	exec(`ALTER TABLE {a} DROP COLUMN "unrecorded"`)
	self := migrate(2)
	exec(`UPDATE {c} SET "loop_id"=1 WHERE "id"=1`)
	observePhase("self", self)
	latest := migrate(3)
	observePhase("choices", latest)
	if model, ok := latest.Model("graph", "b"); !ok || len(model.Fields[1].Choices) != 1 {
		t.Fatal("choices state was not materialized through the cycle")
	}
	count(`SELECT COUNT(*) FROM {a} WHERE ("id"=1 AND "label"='alpha' AND "parent_id"=2 AND "peer_id"=1 AND "tertiary_id"=1) OR ("id"=2 AND "label"='second' AND "parent_id"=1 AND "peer_id"=1 AND "tertiary_id" IS NULL)`, 2)
	count(`SELECT COUNT(*) FROM {c} WHERE "id"=1 AND "b_id"=1 AND "loop_id"=1`, 1)
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
	binding = open()
	if again := migrate(3); !again.Equal(latest) {
		t.Fatal("reopen/no-op changed the latest historical graph")
	}
	observePhase("reopen", latest)
	observePhase("reverse_self", migrate(1)) // Reverse choices and a populated self AddField/remake.
	count(`SELECT COUNT(*) FROM {c} WHERE "id"=1 AND "label"='gamma' AND "b_id"=1`, 1)
	base := migrate(0) // Two remakes of A while B and A itself still reference A.
	if model, ok := base.Model("graph", "a"); !ok || len(model.Fields) != 3 {
		t.Fatal("reverse links did not restore exact source fields")
	}
	count(`SELECT COUNT(*) FROM {a} WHERE ("id"=1 AND "label"='alpha' AND "parent_id"=2) OR ("id"=2 AND "label"='second' AND "parent_id"=1)`, 2)
	count(`SELECT COUNT(*) FROM {b} WHERE "label"='beta' AND "a_id"=2`, 1)
	exec(`INSERT INTO {a} ("label") VALUES ('next')`)
	count(`SELECT "id" FROM {a} WHERE "label"='next'`, 101)
	if _, err := binding.Database.ExecContext(ctx, expand(`UPDATE {a} SET "parent_id"=999999 WHERE "id"=1`)); err == nil {
		t.Fatal("FK enforcement was not restored after a remake")
	}
	observePhase("reverse_links", base)
	empty := migrate(-1)
	observePhase("zero", empty)
	if len(empty.Apps()) != 0 {
		t.Fatal("full reverse did not produce empty historical state")
	}
	observePhase("reapply", migrate(3))
	for _, table := range []string{"{a}", "{b}", "{c}"} {
		count(fmt.Sprintf("SELECT COUNT(*) FROM %s", table), 0)
	}
	empty = migrate(-1)
	observePhase("second_zero", empty)
	if len(empty.Apps()) != 0 {
		t.Fatal("second full reverse did not produce empty state")
	}
	assertDjangoReference(t, observations)
	t.Run("cross_app_back_edge", func(t *testing.T) { crossApp(t, ctx, binding) })
}
