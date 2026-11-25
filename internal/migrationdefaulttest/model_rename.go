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
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/model-rename-django61.json
var modelRenameReference []byte

const renameApp = "renamehub"
const renamePeer = "renamepeer"

type renameModelFixture struct {
	base, peer, binding, rename, through migrations.Migration
}

func makeRenameModelFixture(t *testing.T, fixed bool) renameModelFixture {
	t.Helper()
	build := func(app string, models ...schema.Model) []ir.Model {
		t.Helper()
		value, err := schema.Build(schema.Definition{AppLabel: app, Models: models})
		if err != nil {
			t.Fatal(err)
		}
		return value.Models
	}
	record := func(name, goName string, curated bool) schema.Model {
		target := schema.Target(renameApp, name)
		tag := schema.Target(renameApp, "tag")
		model := schema.Model{Name: name, GoName: goName, Fields: []schema.Field{
			schema.CharField("code", "Code", 32, schema.Unique()),
			schema.ForeignKey("parent", "Parent", target, schema.RelatedName("children"), schema.Protect, schema.Nullable(), schema.DBIndex(true)),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "entry_pair", Fields: []string{"code", "parent"}}},
			ManyToMany: []schema.ManyToManyField{
				schema.ManyToMany("tags", "Tags", tag, schema.RelatedName("tagged_entries")),
				schema.ManyToMany("peers", "Peers", target, schema.RelatedName("peer_of"), schema.Directed()),
			}}
		if fixed {
			model.DBTable = "renamehub_ledger"
		}
		if curated {
			model.ManyToMany = append(model.ManyToMany, schema.ManyToMany("curated", "Curated", tag, schema.RelatedName("curated_entries"), schema.Through(schema.Target(renamePeer, "bond"), "entry", "tag")))
		}
		return model
	}
	base := migrations.Migration{App: renameApp, Name: "0001_initial"}
	for _, model := range build(renameApp, schema.Model{Name: "tag", GoName: "Tag", Fields: []schema.Field{schema.CharField("note", "Note", 32)}}, record("entry", "Entry", false)) {
		base.Operations = append(base.Operations, migrations.CreateModel{AppLabel: renameApp, Model: model})
	}
	box := schema.Model{Name: "box", GoName: "Box", Fields: []schema.Field{
		schema.OneToOne("featured", "Featured", schema.Target(renameApp, "entry"), schema.RelatedName("feature_box"), schema.Protect),
	}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("entries", "Entries", schema.Target(renameApp, "entry"), schema.RelatedName("boxes"))}}
	bond := func(name, goName, target string) schema.Model {
		return schema.Model{Name: name, GoName: goName, Fields: []schema.Field{
			schema.ForeignKey("entry", "Entry", schema.Target(renameApp, target), schema.NoReverse(), schema.Protect),
			schema.ForeignKey("tag", "Tag", schema.Target(renameApp, "tag"), schema.NoReverse(), schema.Protect),
			schema.IntegerField("weight", "Weight"),
		}}
	}
	peer := migrations.Migration{App: renamePeer, Name: "0001_refs", Dependencies: []migrations.MigrationKey{base.Key()}}
	witness := schema.Model{Name: "witness", GoName: "Witness", DBTable: "retained_link_witness", Fields: []schema.Field{
		schema.ForeignKey("link", "Link", schema.Target(renameApp, "entry_tags"), schema.NoReverse(), schema.Protect),
	}}
	for _, model := range build(renamePeer, box, bond("bond", "Bond", "entry"), witness) {
		peer.Operations = append(peer.Operations, migrations.CreateModel{AppLabel: renamePeer, Model: model})
	}
	old := build(renameApp, record("entry", "Entry", true))[0]
	next := build(renameApp, record("publication", "Publication", true))[0]
	binding := migrations.Migration{App: renameApp, Name: "0002_curated", Dependencies: []migrations.MigrationKey{base.Key(), peer.Key()}, Operations: []migrations.Operation{
		migrations.AddManyToMany{AppLabel: renameApp, ModelName: "entry", Field: old.ManyToMany[2]},
	}}
	rename := migrations.Migration{App: renameApp, Name: "0003_rename", Dependencies: []migrations.MigrationKey{binding.Key()}, Operations: []migrations.Operation{
		migrations.RenameModel{AppLabel: renameApp, Before: old, After: next},
	}}
	through := migrations.Migration{App: renamePeer, Name: "0002_rename_bond", Dependencies: []migrations.MigrationKey{peer.Key(), rename.Key()}, Operations: []migrations.Operation{
		migrations.RenameModel{AppLabel: renamePeer, Before: build(renamePeer, bond("bond", "Bond", "publication"))[0], After: build(renamePeer, bond("grant", "Grant", "publication"))[0]},
	}}
	return renameModelFixture{base, peer, binding, rename, through}
}

func observeRenamedModel(t *testing.T, f Fixture, state migrations.ProjectState, name, through string) map[string]any {
	t.Helper()
	root, exists := state.Model(renameApp, name)
	owner, owns := state.Model(renamePeer, "box")
	bridge, bound := state.Model(renamePeer, through)
	id := ir.ModelIdentity{AppLabel: renameApp, ModelName: name}
	if !exists || !owns || !bound || root.Fields[2].Relation.Target != id || owner.Fields[1].Relation.Target != id ||
		owner.ManyToMany[0].Target != id || bridge.Fields[1].Relation.Target != id ||
		root.ManyToMany[2].Through.Model != (ir.ModelIdentity{AppLabel: renamePeer, ModelName: through}) {
		t.Fatal("incorrect historical graph", name, through, root, owner, bridge)
	}
	other := "entry"
	if name == "entry" {
		other = "publication"
	}
	if _, ok := state.Model(renameApp, other); ok {
		t.Fatal("previous logical identity still visible", other)
	}
	witness, found := state.Model(renamePeer, "witness")
	if !found || witness.Fields[1].Relation.Target != (ir.ModelIdentity{AppLabel: renameApp, ModelName: name + "_tags"}) {
		t.Fatal("foreign key still targets the old intermediary", witness)
	}
	var link, source, target int64
	if err := f.SQL.QueryRowContext(t.Context(), "SELECT w.link_id, l.source_id, l.target_id FROM "+f.table(witness.DBTable)+" AS w JOIN "+f.table(root.DBTable+"_tags")+" AS l ON l.id=w.link_id").Scan(&link, &source, &target); err != nil || link != 1 || source != 1 || target != 1 {
		t.Fatal("populated external reference to owned intermediary", link, source, target, err)
	}
	var catalog [][]any
	if f.SQLite {
		catalog = f.readRows(t, "SELECT name FROM sqlite_schema WHERE type='table' AND (name GLOB 'renamehub_*' OR name GLOB 'renamepeer_*') ORDER BY name")
	} else {
		catalog = f.readRows(t, "SELECT table_name::text FROM information_schema.tables WHERE table_schema=$1 AND (left(table_name,10)='renamehub_' OR left(table_name,11)='renamepeer_') ORDER BY table_name", f.Namespace)
	}
	tables := make([]string, 0, len(catalog))
	for _, row := range catalog {
		tables = append(tables, row[0].(string))
	}
	rows := func(table, columns string) [][]any {
		return f.readRows(t, "SELECT "+columns+" FROM "+f.table(table)+" ORDER BY id")
	}
	return map[string]any{"model": name, "tables": tables, "entries": rows(root.DBTable, "id,code,parent_id"), "tags": rows("renamehub_tag", "id,note"),
		"featured": rows(owner.DBTable, "id,featured_id"), "owned_links": rows(root.DBTable+"_tags", "id,source_id,target_id"),
		"self_links": rows(root.DBTable+"_peers", "id,source_id,target_id"), "incoming_links": rows(owner.DBTable+"_entries", "id,source_id,target_id"),
		"explicit_links": rows(bridge.DBTable, "id,entry_id,tag_id,weight")}
}

func checkModelRename(t *testing.T, open func() Fixture, fixed bool, classify func(error) string) {
	t.Helper()
	m := makeRenameModelFixture(t, fixed)
	loaded := loadChanges(t, m.base, m.peer, m.binding, m.rename, m.through)
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
	reopen := func() {
		t.Helper()
		if err := f.SQL.Close(); err != nil {
			t.Fatal(err)
		}
		f = open()
	}
	history := func() [][]string {
		records := snapshot(t, f, "godj_migrations")
		slices.Sort(records)
		return [][]string{records, snapshot(t, f, "godj_migration_revision")}
	}
	initial := apply(m.binding)
	move := m.rename.Operations[0].(migrations.RenameModel)
	old, next := move.Before.DBTable, move.After.DBTable
	edition := move.After.Fields[1].Clone()
	edition.Name, edition.GoName, edition.Column, edition.Unique = "edition", "Edition", "edition", false
	withRequired := m.rename
	withRequired.Operations = append(slices.Clone(m.rename.Operations), migrations.AddField{AppLabel: renameApp, ModelName: move.After.Name, Field: edition})
	mixed := loadChanges(t, m.base, m.peer, m.binding, withRequired)
	if _, err := run(mixed, withRequired); err != nil {
		t.Fatal("rename then add required column to empty table", err)
	}
	var count int
	if err := f.SQL.QueryRowContext(t.Context(), "SELECT COUNT(edition) FROM "+f.table(next)).Scan(&count); err != nil || count != 0 {
		t.Fatal("missing required column after rename", count, err)
	}
	if _, err := run(mixed, m.binding); err != nil {
		t.Fatal("reverse same-step column addition and model rename", err)
	}
	reopen()
	exec("INSERT INTO " + f.table("renamehub_tag") + " (note) VALUES ('same'),('same')")
	exec("INSERT INTO " + f.table(old) + " (code) VALUES ('original')")
	exec("INSERT INTO " + f.table(old) + " (code,parent_id) VALUES ('child',1)")
	exec("INSERT INTO " + f.table(old+"_tags") + " (source_id,target_id) VALUES (1,1),(2,2)")
	exec("INSERT INTO " + f.table("retained_link_witness") + " (link_id) VALUES (1)")
	exec("INSERT INTO " + f.table(old+"_peers") + " (source_id,target_id) VALUES (1,2)")
	exec("INSERT INTO " + f.table("renamepeer_box") + " (featured_id) VALUES (2)")
	exec("INSERT INTO " + f.table("renamepeer_box_entries") + " (source_id,target_id) VALUES (1,1)")
	exec("INSERT INTO " + f.table("renamepeer_bond") + " (entry_id,tag_id,weight) VALUES (2,1,17)")
	observed := map[string]any{"before": observeRenamedModel(t, f, initial, "entry", "bond")}
	prefix := history()
	var capability *backend.CapabilityError
	if _, err := run(mixed, withRequired); !errors.As(err, &capability) || !strings.Contains(capability.Detail, "contains rows") || !reflect.DeepEqual(prefix, history()) {
		t.Fatal("required column after rename must reject the populated source before DDL", err)
	}
	if !reflect.DeepEqual(observed["before"], observeRenamedModel(t, f, initial, "entry", "bond")) {
		t.Fatal("failed column preflight changed retained rows or model storage")
	}
	for _, direction := range []migrations.Direction{migrations.DirectionForward, migrations.DirectionBackward} {
		statements, err := migrations.RenderMigrationSQL(t.Context(), loaded, m.rename.Key(), direction, f.Renderer)
		if err != nil {
			t.Fatal(err)
		}
		renames := 0
		for _, sql := range statements {
			if strings.HasPrefix(sql, "DROP TABLE") || strings.HasPrefix(sql, "CREATE TABLE") {
				t.Fatal("destructive rename", sql)
			}
			if strings.HasPrefix(sql, "ALTER TABLE") && strings.Contains(sql, " RENAME TO ") {
				renames++
			}
		}
		if fixed && len(statements) != 0 || !fixed && renames != 3 {
			t.Fatal("unexpected table rename SQL", direction, statements)
		}
	}
	if !reflect.DeepEqual(prefix, history()) || !reflect.DeepEqual(observed["before"], observeRenamedModel(t, f, initial, "entry", "bond")) {
		t.Fatal("SQL projection changed durable state")
	}
	failed := m.rename
	failed.Operations = append(slices.Clone(m.rename.Operations), migrations.AddConstraint{AppLabel: renameApp, ModelName: "tag", Constraint: ir.UniqueConstraint{Name: "renamehub_unique_note", Fields: []string{"note"}}})
	_, err := run(loadChanges(t, m.base, m.peer, m.binding, failed), failed)
	var detail *migrations.Error
	if !errors.As(err, &detail) || detail.OperationIndex != 1 || classify(err) != "unique" || detail.RollbackCause != nil || !reflect.DeepEqual(history(), prefix) {
		t.Fatal("late rename failure was not atomic", err)
	}
	reopen()
	observed["rollback"] = observeRenamedModel(t, f, initial, "entry", "bond")
	if !fixed {
		exec("CREATE TABLE " + f.table(next) + " (marker INTEGER PRIMARY KEY)")
		exec("INSERT INTO " + f.table(next) + " VALUES (31)")
		if _, err := run(loaded, m.rename); err == nil || !reflect.DeepEqual(history(), prefix) {
			t.Fatal("claimed occupied destination", err)
		}
		var marker int
		if err := f.SQL.QueryRowContext(t.Context(), "SELECT marker FROM "+f.table(next)).Scan(&marker); err != nil || marker != 31 {
			t.Fatal("lost occupied destination", err)
		}
		exec("DROP TABLE " + f.table(next))
		observed["collision"] = observeRenamedModel(t, f, initial, "entry", "bond")
		for _, blocker := range []struct{ create, drop string }{
			{"CREATE VIEW " + f.table("unmanaged_view") + " AS SELECT id FROM " + f.table(old+"_tags"), "DROP VIEW " + f.table("unmanaged_view")},
			{"CREATE TABLE " + f.table("unmanaged_ref") + " (x TEXT REFERENCES " + renameReferenceTable(f, old) + "(code))", "DROP TABLE " + f.table("unmanaged_ref")},
		} {
			exec(blocker.create)
			if _, err := run(loaded, m.rename); err == nil || !reflect.DeepEqual(prefix, history()) || !reflect.DeepEqual(observed["before"], observeRenamedModel(t, f, initial, "entry", "bond")) {
				t.Fatal("unowned rename dependency", err)
			}
			exec(blocker.drop)
		}
	}
	if fixed {
		exec("CREATE VIEW " + f.table("unmanaged_view") + " AS SELECT code FROM " + f.table(old))
		exec("CREATE TABLE " + f.table("unmanaged_ref") + " (x TEXT REFERENCES " + renameReferenceTable(f, old) + "(code))")
		exec("INSERT INTO " + f.table("unmanaged_ref") + " VALUES ('original')")
	}
	renamed := apply(m.rename)
	reopen()
	observed["renamed"] = observeRenamedModel(t, f, renamed, "publication", "bond")
	if fixed {
		var value string
		if err := f.SQL.QueryRowContext(t.Context(), "SELECT x FROM "+f.table("unmanaged_ref")+" JOIN "+f.table("unmanaged_view")+" ON x=code").Scan(&value); err != nil || value != "original" {
			t.Fatal("fixed table lost an external dependency", err)
		}
		exec("DROP TABLE " + f.table("unmanaged_ref"))
		exec("DROP VIEW " + f.table("unmanaged_view"))
	}
	changed := apply(m.through)
	reopen()
	observed["renamed_through"] = observeRenamedModel(t, f, changed, "publication", "grant")
	back := apply(m.peer)
	observed["restored_through"] = observeRenamedModel(t, f, back, "publication", "bond")
	exec("INSERT INTO " + f.table(next) + " (code,parent_id) VALUES ('later',1)")
	exec("INSERT INTO " + f.table(next+"_tags") + " (source_id,target_id) VALUES (3,1)")
	constraints := map[string]bool{}
	for _, check := range []struct{ name, sql, category string }{
		{"self_foreign_key", "UPDATE " + f.table(next) + " SET parent_id=999 WHERE id=3", "foreign_key"},
		{"incoming_foreign_key", "UPDATE " + f.table("renamepeer_box") + " SET featured_id=999", "foreign_key"},
		{"one_to_one", "INSERT INTO " + f.table("renamepeer_box") + " (featured_id) VALUES (2)", "unique"},
		{"unique_code", "UPDATE " + f.table(next) + " SET code='original' WHERE id=3", "unique"},
	} {
		_, err := f.SQL.ExecContext(t.Context(), check.sql)
		constraints[check.name] = classify(err) == check.category
	}
	observed["constraints"] = constraints
	renamedHistory := history()
	_, err = run(loadChanges(t, m.base, m.peer, m.binding, failed), m.binding)
	if !errors.As(err, &detail) || detail.Code != migrations.CodeDefinitionHistoryMismatch || detail.App != renameApp || detail.Migration != m.rename.Name || !reflect.DeepEqual(renamedHistory, history()) {
		t.Fatal("changed rename definition authorized reverse", err)
	}
	restored := apply(m.binding)
	reopen()
	observed["reversed"] = observeRenamedModel(t, f, restored, "entry", "bond")
	again := apply(m.rename)
	observed["reapplied"] = observeRenamedModel(t, f, again, "publication", "bond")
	var reference map[string]any
	if err := json.Unmarshal(modelRenameReference, &reference); err != nil {
		t.Fatal(err)
	}
	mode := "derived_table"
	if fixed {
		mode = "fixed_table"
	}
	compareObservation(t, mode, reference[mode], observed)
	var digest []byte
	if err := f.SQL.QueryRowContext(t.Context(), "SELECT definition_digest FROM "+f.table("godj_migrations")+" WHERE app='renamehub' AND name='0003_rename'").Scan(&digest); err != nil || !slices.Equal(digest, expectedHistoryDigest(t, loaded, m.rename.Key())) {
		t.Fatal("rename definition binding", err)
	}
	id := func(sql string) int64 {
		t.Helper()
		var key int64
		if err := f.SQL.QueryRowContext(t.Context(), sql+" RETURNING id").Scan(&key); err != nil {
			t.Fatal(err)
		}
		return key
	}
	newRoot := "INSERT INTO " + f.table(next) + " (code) VALUES ('gap')"
	newLink := "INSERT INTO " + f.table(next+"_tags") + " (source_id,target_id) VALUES (2,1)"
	rootID, linkID := id(newRoot), id(newLink)
	exec("DELETE FROM " + f.table(next) + " WHERE code='gap'")
	exec("DELETE FROM " + f.table(next+"_tags") + " WHERE source_id=2 AND target_id=1")
	apply(m.binding)
	apply(m.rename)
	if id(newRoot) <= rootID || id(newLink) <= linkID {
		t.Fatal("rename reset a previously allocated key")
	}
}

func renameReferenceTable(f Fixture, name string) string {
	if f.SQLite {
		return `"` + name + `"`
	}
	return f.table(name)
}
