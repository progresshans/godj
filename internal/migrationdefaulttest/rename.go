package migrationdefaulttest

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func checkFieldRename(t *testing.T, open func() Fixture, constraint func(error) string) {
	t.Helper()
	f := open()
	const app = "rename_fixture"
	models, err := schema.Build(schema.Definition{AppLabel: app, Models: []schema.Model{
		{Name: "category", GoName: "Category", DBTable: "r_category", Fields: []schema.Field{schema.CharField("legacy_code", "LegacyCode", 64, schema.Unique())}},
		{Name: "tag", GoName: "Tag", DBTable: "r_tag"},
		{Name: "ticket", GoName: "Ticket", DBTable: "r_ticket", Fields: []schema.Field{
			schema.ForeignKey("category", "Category", schema.Target(app, "category"), schema.RelatedName("tickets"), schema.Protect, schema.DBIndex(true)),
			schema.ForeignKey("tag", "Tag", schema.Target(app, "tag"), schema.RelatedName("tickets"), schema.Protect),
			schema.CharField("note", "Note", 64),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "ticket_note", Fields: []string{"category", "note"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	watchSchema, err := schema.Build(schema.Definition{AppLabel: "watches", Models: []schema.Model{{Name: "watch", GoName: "Watch", DBTable: "r_watch", Fields: []schema.Field{
		schema.ForeignKey("category", "Category", schema.Target(app, "category"), schema.RelatedName("watches"), schema.Protect),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.Migration{App: app, Name: "0001_rename_base"}
	for _, model := range models.Models {
		initial.Operations = append(initial.Operations, migrations.CreateModel{AppLabel: app, Model: model})
	}
	tags := schema.ManyToMany("tags", "Tags", schema.Target(app, "tag"), schema.RelatedName("categories"), schema.Through(schema.Target(app, "ticket"), "category", "tag"))
	tags, err = ir.NormalizeManyToManyField(app, "category", tags)
	if err != nil {
		t.Fatal(err)
	}
	initial.Operations = append(initial.Operations, migrations.AddManyToMany{AppLabel: app, ModelName: "category", Field: tags})
	watch := migrations.Migration{App: "watches", Name: "0001_watch", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "watches", Model: watchSchema.Models[0]}}}
	renamed := migrations.Migration{App: app, Name: "0002_rename", Dependencies: []migrations.MigrationKey{initial.Key(), watch.Key()}}
	add := func(model ir.Model, from, to, goName, column string) migrations.RenameField {
		i := slices.IndexFunc(model.Fields, func(f ir.Field) bool { return f.Name == from })
		if i < 0 {
			t.Fatal("missing source field", from)
		}
		before := model.Fields[i]
		after := before.Clone()
		after.Name, after.GoName, after.Column = to, goName, column
		op := migrations.RenameField{AppLabel: app, ModelName: model.Name, Before: before, After: after}
		renamed.Operations = append(renamed.Operations, op)
		return op
	}
	add(models.Models[0], "id", "category_key", "CategoryKey", "category_key")
	add(models.Models[0], "legacy_code", "code", "Code", "code")
	add(models.Models[2], "category", "project", "Project", "project_id")
	note := add(models.Models[2], "note", "memo", "Memo", "note")
	load := func(change migrations.Migration) migrations.LoadedDefinitionSet {
		return loadChanges(t, initial, watch, change)
	}
	run := func(set migrations.LoadedDefinitionSet, target migrations.Migration) (migrations.ProjectState, error) {
		return (migrations.Executor{Backend: f.Backend}).Migrate(t.Context(), set, dataRequest(target))
	}
	sql := func(stmt string) {
		t.Helper()
		if _, err := f.SQL.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	stored := func() [][]string {
		t.Helper()
		var state [][]string
		for _, table := range []string{"r_category", "r_ticket", "r_tag", "r_watch", "godj_migrations", "godj_migration_revision"} {
			rows := snapshot(t, f, table)
			if table == "godj_migrations" {
				slices.Sort(rows)
			}
			state = append(state, rows)
		}
		return state
	}
	set := load(renamed)
	if _, err := run(set, watch); err != nil {
		t.Fatal(err)
	}
	category, ticket, tag, watchTable := f.table("r_category"), f.table("r_ticket"), f.table("r_tag"), f.table("r_watch")
	sql("INSERT INTO " + category + " (legacy_code) VALUES ('a'),('b'),('discard')")
	sql("DELETE FROM " + category + " WHERE id=3")
	sql("INSERT INTO " + tag + " DEFAULT VALUES")
	sql("INSERT INTO " + ticket + " (category_id,tag_id,note) VALUES (1,1,'retained'),(2,1,'retained')")
	sql("INSERT INTO " + watchTable + " (category_id) VALUES (2)")
	before := stored()
	statements, err := migrations.RenderMigrationSQL(t.Context(), set, renamed.Key(), f.Renderer)
	rendered := strings.Join(statements, "\n")
	if err != nil || strings.Count(rendered, " RENAME COLUMN ") != 3 || strings.Contains(rendered, `RENAME COLUMN "note"`) || !reflect.DeepEqual(stored(), before) {
		t.Fatal("pure field rename SQL", statements, err)
	}
	// A physical incoming FK outside the declared graph cannot be changed.
	referencedTable := category
	if f.SQLite {
		referencedTable = "r_category"
	}
	sql("CREATE TABLE " + f.table("r_unmanaged") + " (ptr BIGINT REFERENCES " + referencedTable + "(id))")
	if _, err := run(set, renamed); err == nil || !reflect.DeepEqual(stored(), before) {
		t.Fatal("rewrote an unmanaged incoming key", err)
	}
	sql("DROP TABLE " + f.table("r_unmanaged"))
	// Fail after all the renames. A retry must see the exact old columns,
	// indexes, relation rows, recorder and revision.
	failing := renamed
	unique := note.After.Clone()
	unique.Unique = true
	failing.Operations = append(slices.Clone(renamed.Operations), migrations.AlterField{AppLabel: app, ModelName: "ticket", Before: note.After, After: unique})
	_, err = run(load(failing), failing)
	var owner *migrations.Error
	if !errors.As(err, &owner) || owner.OperationIndex != 4 || constraint(err) != "unique" || !reflect.DeepEqual(stored(), before) {
		t.Fatal("late failure did not roll back renamed storage", err)
	}
	state, err := run(set, renamed)
	if err != nil {
		t.Fatal(err)
	}
	model, ok := state.Model(app, "category")
	if !ok || len(model.ManyToMany) != 1 || model.ManyToMany[0].Through.SourceField != "project" {
		t.Fatal("explicit through source still uses old field", model)
	}
	link, _ := state.Model(app, "ticket")
	if len(link.UniqueConstraints) != 1 || !slices.Equal(link.UniqueConstraints[0].Fields, []string{"project", "memo"}) {
		t.Fatal("renamed named key members", link.UniqueConstraints)
	}
	checkLinks := func(pk, fk, code string) {
		t.Helper()
		var codes string
		if err := f.SQL.QueryRowContext(t.Context(), "SELECT c."+code+" FROM "+category+" c JOIN "+ticket+" t ON t."+fk+"=c."+pk+" JOIN "+watchTable+" w ON w.category_id=c."+pk+" WHERE t.note='retained'").Scan(&codes); err != nil || codes != "b" {
			t.Fatal("inbound or through row changed", codes, err)
		}
	}
	checkLinks("category_key", "project_id", "code")
	var next int64
	if err := f.SQL.QueryRowContext(t.Context(), "INSERT INTO "+category+" (code) VALUES ('fresh') RETURNING category_key").Scan(&next); err != nil || next != 4 {
		t.Fatal("renamed identity sequence", next, err)
	}
	for stmt, want := range map[string]string{
		"INSERT INTO " + category + " (code) VALUES ('a')":                            "unique",
		"INSERT INTO " + ticket + " (project_id,tag_id,note) VALUES (1,1,'retained')": "unique",
		"UPDATE " + ticket + " SET project_id=999 WHERE id=1":                         "foreign_key",
		"UPDATE " + watchTable + " SET category_id=999":                               "foreign_key",
	} {
		if _, err := f.SQL.ExecContext(t.Context(), stmt); constraint(err) != want {
			t.Fatal("renamed constraint", want, err)
		}
	}
	if err := f.SQL.Close(); err != nil {
		t.Fatal(err)
	}
	f = open()
	accepted := stored()
	if _, err := run(set, renamed); err != nil || !reflect.DeepEqual(accepted, stored()) {
		t.Fatal("reopened rename changed the database", err)
	}
	if _, err := run(set, initial); err != nil {
		t.Fatal(err)
	}
	checkLinks("id", "category_id", "legacy_code")
	var persisted string
	if err := f.SQL.QueryRowContext(t.Context(), "SELECT legacy_code FROM "+category+" WHERE id=4").Scan(&persisted); err != nil || persisted != "fresh" {
		t.Fatal("reverse rename lost new row", persisted, err)
	}
	if _, err := run(set, renamed); err != nil {
		t.Fatal("reapply after inverse", err)
	}
}
