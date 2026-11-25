package migrationdefaulttest

import (
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/model-deletion-django61.json
var modelDeletionReference []byte

func checkModelDeletion(t *testing.T, open func() Fixture, constraint func(error) string) {
	t.Helper()
	const app = "retire"
	s, err := schema.Build(schema.Definition{AppLabel: app, Models: []schema.Model{
		{Name: "tag", GoName: "Tag"},
		{Name: "archive", GoName: "Archive", Fields: []schema.Field{schema.CharField("code", "Code", 32, schema.Unique())}, ManyToMany: []schema.ManyToManyField{
			schema.ManyToMany("tags", "Tags", schema.Target(app, "tag"), schema.RelatedName("archives")),
		}},
		{Name: "keeper", GoName: "Keeper", Fields: []schema.Field{schema.TextField("note", "Note")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.Migration{App: app, Name: "0001_initial"}
	for _, model := range s.Models {
		initial.Operations = append(initial.Operations, migrations.CreateModel{AppLabel: app, Model: model})
	}
	drop := migrations.Migration{App: app, Name: "0002_delete_archive", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		migrations.DeleteModel{AppLabel: app, Model: s.Models[1], BeforeModel: "keeper"},
	}}
	loaded := loadChanges(t, initial, drop)
	f := open()
	run := func(set migrations.LoadedDefinitionSet, target migrations.Migration) (migrations.ProjectState, error) {
		return (migrations.Executor{Backend: f.Backend}).Migrate(t.Context(), set, dataRequest(target))
	}
	apply := func(target migrations.Migration) migrations.ProjectState {
		t.Helper()
		state, err := run(loaded, target)
		if err != nil {
			t.Fatal(target.Key(), err)
		}
		return state
	}
	exec := func(stmt string) {
		t.Helper()
		if _, err := f.SQL.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	apply(initial)
	tag, archive, links, keeper := f.table("retire_tag"), f.table("retire_archive"), f.table("retire_archive_tags"), f.table("retire_keeper")
	exec("INSERT INTO " + tag + " DEFAULT VALUES")
	exec("INSERT INTO " + keeper + " (note) VALUES ('kept'),('kept')")
	exec("INSERT INTO " + archive + " (code) VALUES ('old')")
	exec("INSERT INTO " + links + " (source_id,target_id) VALUES (1,1)")
	observe := func() map[string]any {
		t.Helper()
		var entries [][]any
		if f.SQLite {
			entries = f.readRows(t, "SELECT name FROM sqlite_schema WHERE type='table' AND name LIKE 'retire\\_%' ESCAPE '\\' ORDER BY name")
		} else {
			entries = f.readRows(t, "SELECT table_name::text FROM information_schema.tables WHERE table_schema=$1 AND table_name LIKE 'retire\\_%' ESCAPE '\\' ORDER BY table_name", f.Namespace)
		}
		tables := make([]string, 0, len(entries))
		for _, row := range entries {
			tables = append(tables, row[0].(string))
		}
		var archives, relations [][]any
		if slices.Contains(tables, "retire_archive") {
			archives = f.readRows(t, "SELECT id,code FROM "+archive+" ORDER BY id")
		}
		if slices.Contains(tables, "retire_archive_tags") {
			relations = f.readRows(t, "SELECT source_id,target_id FROM "+links+" ORDER BY id")
		}
		tags := []any{}
		for _, row := range f.readRows(t, "SELECT id FROM "+tag+" ORDER BY id") {
			tags = append(tags, row[0])
		}
		return map[string]any{"tables": tables, "archives": archives, "links": relations, "tags": tags, "keepers": f.readRows(t, "SELECT id,note FROM "+keeper+" ORDER BY id")}
	}
	actual := map[string]any{"before": observe()}
	history := func() [][]string {
		t.Helper()
		records := snapshot(t, f, "godj_migrations")
		slices.Sort(records)
		return [][]string{records, snapshot(t, f, "godj_migration_revision")}
	}
	beforeHistory := history()
	for _, preview := range []struct {
		key, verb string
		direction migrations.Direction
	}{
		{"drop_order", "DROP", migrations.DirectionForward}, {"restore_order", "CREATE", migrations.DirectionBackward},
	} {
		statements, err := migrations.RenderMigrationSQL(t.Context(), loaded, drop.Key(), preview.direction, f.Renderer)
		if err != nil {
			t.Fatal(err)
		}
		var tables []string
		for _, stmt := range statements {
			fields := strings.Fields(stmt)
			if len(fields) < 3 || fields[0] != preview.verb || fields[1] != "TABLE" {
				continue
			}
			name := fields[2][strings.LastIndexByte(fields[2], '.')+1:]
			tables = append(tables, strings.Trim(name, "\""))
		}
		actual[preview.key] = tables
	}
	if !reflect.DeepEqual(actual["before"], observe()) || !reflect.DeepEqual(history(), beforeHistory) {
		t.Fatal("SQL preview changed schema, rows or recorder")
	}
	failed := drop
	failed.Operations = append(slices.Clone(drop.Operations), migrations.AddConstraint{AppLabel: app, ModelName: "keeper", Constraint: ir.UniqueConstraint{Name: "retire_note_unique", Fields: []string{"note"}}})
	_, err = run(loadChanges(t, initial, failed), failed)
	var failure *migrations.Error
	if !errors.As(err, &failure) || failure.OperationIndex != 1 || constraint(err) != "unique" || failure.RollbackCause != nil || !reflect.DeepEqual(history(), beforeHistory) {
		t.Fatal("failed deletion did not roll back the schema and recorder", err)
	}
	actual["rollback"] = observe()
	refTarget := archive
	if f.SQLite {
		refTarget = "\"retire_archive\""
	}
	exec("CREATE TABLE " + f.table("unmanaged_ref") + " (ref BIGINT REFERENCES " + refTarget + "(id))")
	if _, err := run(loaded, drop); err == nil || !reflect.DeepEqual(history(), beforeHistory) || !reflect.DeepEqual(observe(), actual["before"]) {
		t.Fatal("unmanaged incoming FK failed to protect the table", err)
	}
	exec("DROP TABLE " + f.table("unmanaged_ref"))
	exec("CREATE VIEW " + f.table("unmanaged_view") + " AS SELECT code FROM " + refTarget)
	if _, err := run(loaded, drop); err == nil || !reflect.DeepEqual(history(), beforeHistory) || !reflect.DeepEqual(observe(), actual["before"]) {
		t.Fatal("unmanaged view failed to protect the table", err)
	}
	exec("DROP VIEW " + f.table("unmanaged_view"))
	after := apply(drop)
	if _, ok := after.Model(app, "archive"); ok {
		t.Fatal("deleted model remains in history")
	}
	if err := f.SQL.Close(); err != nil {
		t.Fatal(err)
	}
	f = open()
	actual["removed"] = observe()
	var raw []byte
	if err := f.SQL.QueryRowContext(t.Context(), "SELECT definition_digest FROM "+f.table("godj_migrations")+" WHERE app='retire' AND name='0002_delete_archive'").Scan(&raw); err != nil || !slices.Equal(raw, expectedHistoryDigest(t, loaded, drop.Key())) {
		t.Fatal("missing bound deletion", err)
	}
	removedHistory := history()
	_, err = run(loadChanges(t, initial, failed), initial)
	if !errors.As(err, &failure) || failure.Code != migrations.CodeDefinitionHistoryMismatch || failure.App != app || failure.Migration != drop.Name ||
		!reflect.DeepEqual(history(), removedHistory) || !reflect.DeepEqual(observe(), actual["removed"]) {
		t.Fatal("altered deletion history authorized restoration", err)
	}
	restored := apply(initial)
	got, _ := restored.Schema(app)
	if !slices.EqualFunc(got.Models, s.Models, ir.Model.Equal) {
		t.Fatal("restored model declarations or order differ", got.Models)
	}
	actual["restored"] = observe()
	exec("INSERT INTO " + archive + " (code) VALUES ('fresh')")
	exec("INSERT INTO " + links + " (source_id,target_id) VALUES (1,1)")
	_, err = f.SQL.ExecContext(t.Context(), "INSERT INTO "+archive+" (code) VALUES ('fresh')")
	actual["unique"] = constraint(err) == "unique"
	if _, err = f.SQL.ExecContext(t.Context(), "INSERT INTO "+links+" (source_id,target_id) VALUES (1,99)"); constraint(err) != "foreign_key" {
		t.Fatal("missing restored foreign key", err)
	}
	actual["refilled"] = observe()
	var reference map[string]any
	if err := json.Unmarshal(modelDeletionReference, &reference); err != nil {
		t.Fatal(err)
	}
	compareObservation(t, "model deletion and empty recreation", reference, actual)
}
