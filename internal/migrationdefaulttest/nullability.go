package migrationdefaulttest

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/migrationautodetect"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

type fieldMigration struct {
	Fixture
	loaded          migrations.LoadedDefinitionSet
	initial, change migrations.MigrationKey
	constraint      func(error) string
}

func newFieldMigration(t *testing.T, f Fixture, constraint func(error) string, fieldName string, oldUnique, newUnique bool, fill *ir.Scalar) fieldMigration {
	t.Helper()
	const app = "tickets"
	category := schema.ForeignKey("category", "Category", schema.Target(app, "category"), schema.RelatedName("tickets"), schema.Protect)
	category.Nullable = fieldName == "category"
	external := schema.CharField("external_id", "ExternalID", 80, schema.Nullable(), schema.DBIndex(true))
	external.Unique = oldUnique
	s, err := schema.Build(schema.Definition{AppLabel: app, Models: []schema.Model{
		{Name: "category", GoName: "Category", DBTable: "m1_category", Fields: []schema.Field{schema.CharField("legacy_code", "LegacyCode", 32)}},
		{Name: "ticket", GoName: "Ticket", DBTable: "m1_ticket", Fields: []schema.Field{category, external}},
		{Name: "bookmark", GoName: "Bookmark", DBTable: "m1_bookmark", Fields: []schema.Field{schema.ForeignKey("ticket", "Ticket", schema.Target(app, "ticket"), schema.RelatedName("bookmarks"), schema.Cascade)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m := detectFieldMigration(t, f, constraint, s, fieldName, newUnique, fill)
	m.apply(t, m.initial)
	m.exec(t, "INSERT INTO "+f.table("m1_category")+" (legacy_code) VALUES ('한''글'), ('β')")
	m.exec(t, "INSERT INTO "+f.table("m1_ticket")+" (category_id, external_id) VALUES (1,'한''글/1'), (1,NULL), (2,NULL), (2,'discard')")
	m.exec(t, "DELETE FROM "+f.table("m1_ticket")+" WHERE id=4")
	m.exec(t, "INSERT INTO "+f.table("m1_bookmark")+" (ticket_id) VALUES (1),(2),(3)")
	return m
}

func detectFieldMigration(t *testing.T, f Fixture, constraint func(error) string, s ir.Schema, fieldName string, unique bool, fill *ir.Scalar) fieldMigration {
	t.Helper()
	initial := migrations.Migration{App: s.AppLabel, Name: "0001_initial"}
	for _, model := range s.Models {
		initial.Operations = append(initial.Operations, migrations.CreateModel{AppLabel: s.AppLabel, Model: model})
	}
	desired := s.Clone()
	for _, model := range desired.Models {
		if model.Name == "ticket" {
			for i := range model.Fields {
				field := &model.Fields[i]
				if field.Name == fieldName {
					field.Nullable, field.Unique, field.Default = false, unique, fill
				}
			}
		}
	}
	state, err := migrations.NewProjectState(desired)
	if err != nil {
		t.Fatal(err)
	}
	request := migrationautodetect.Request{Definitions: loadChanges(t, initial), Desired: state, ManagedApps: []string{s.AppLabel}}
	plan, err := migrationautodetect.Detect(request)
	changes := plan.Migrations()
	if err != nil || len(changes) != 1 || len(changes[0].Operations) != 1 {
		t.Fatal("model change did not produce one migration operation", changes, err)
	}
	change := changes[0]
	alter, ok := change.Operations[0].(migrations.AlterField)
	if !ok || alter.ModelName != "ticket" || alter.After.Name != fieldName || !alter.Before.Nullable || alter.After.Nullable || alter.After.Unique != unique {
		t.Fatal("unexpected generated field transition", change)
	}
	loaded := loadChanges(t, initial, change)
	return fieldMigration{Fixture: f, loaded: loaded, initial: initial.Key(), change: change.Key(), constraint: constraint}
}

func (m fieldMigration) exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	if _, err := m.SQL.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(statement, err)
	}
}

func (m fieldMigration) apply(t *testing.T, target migrations.MigrationKey) {
	t.Helper()
	_, err := (migrations.Executor{Backend: m.Backend}).Migrate(t.Context(), m.loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(target)))
	if err != nil {
		t.Fatal("migrate to", target, err)
	}
	want := []mb.AppliedMigration{{App: m.initial.App, Name: m.initial.Name}}
	if target == m.change {
		want = append(want, mb.AppliedMigration{App: m.change.App, Name: m.change.Name})
	}
	current, err := m.Backend.ReadAppliedMigrations(t.Context())
	if err != nil || !reflect.DeepEqual(current, want) {
		t.Fatal("unexpected recorded history", current, err)
	}
}

func (m fieldMigration) state(t *testing.T) [][]string {
	t.Helper()
	// Each snapshot is read through a new query outside the migration transaction.
	tables := []string{"godj_migration_revision", "m1_ticket", "m1_category", "m1_bookmark"}
	if m.SQLite {
		tables = append(tables, "sqlite_sequence")
	}
	var result [][]string
	for _, table := range tables {
		result = append(result, snapshot(t, m.Fixture, table))
	}
	return result
}

func (m fieldMigration) fail(t *testing.T, target migrations.MigrationKey, direction migrations.Direction, constraint string) {
	t.Helper()
	before := m.state(t)
	recorded, err := m.Backend.ReadAppliedMigrations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = (migrations.Executor{Backend: m.Backend}).Migrate(t.Context(), m.loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(target)))
	var owner *migrations.Error
	if !errors.As(err, &owner) || owner.Category != migrations.CategoryExecution || owner.Code != migrations.CodeOperationFailed ||
		owner.App != m.change.App || owner.Migration != m.change.Name || owner.OperationIndex != 0 || owner.Direction != direction ||
		owner.Operation != "AlterField" || owner.RollbackCause != nil || m.constraint(err) != constraint {
		t.Fatalf("expected %s AlterField %s failure, got %v", direction, constraint, err)
	}
	current, err := m.Backend.ReadAppliedMigrations(t.Context())
	if err != nil || !reflect.DeepEqual(current, recorded) || !reflect.DeepEqual(before, m.state(t)) {
		t.Fatal("failure changed durable rows, keys, recorded history or revision", err)
	}
}

func (m fieldMigration) rejectWrite(t *testing.T, statement, kind string) {
	t.Helper()
	_, err := m.SQL.ExecContext(t.Context(), statement)
	if m.constraint(err) != kind {
		t.Fatalf("expected native %s rejection for %s, got %v", kind, statement, err)
	}
}

func (m fieldMigration) column(t *testing.T, column string, required bool) {
	t.Helper()
	statement := "SELECT is_nullable = 'NO', column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name='m1_ticket' AND column_name=$2"
	args := []any{m.Namespace, column}
	if m.SQLite {
		statement = "SELECT \"notnull\", dflt_value FROM pragma_table_info('m1_ticket') WHERE name=?"
		args = []any{column}
	}
	var gotRequired bool
	var persistentDefault sql.NullString
	if err := m.SQL.QueryRowContext(t.Context(), statement, args...).Scan(&gotRequired, &persistentDefault); err != nil || gotRequired != required || persistentDefault.Valid {
		t.Fatalf("physical %s: required=%v default=%v: %v", column, gotRequired, persistentDefault, err)
	}
}

// RunFieldNullability uses the file or schema returned by open again to check
// the durable result on a fresh pool. SQL writes bypass model validation.
func RunFieldNullability(t *testing.T, database func(*testing.T) func() Fixture, constraint func(error) string) {
	t.Run("legacy_definition_history", func(t *testing.T) {
		checkLegacyDefinitionHistory(t, database(t), constraint)
	})
	t.Run("bound_bootstrap", func(t *testing.T) {
		checkBoundBootstrap(t, database(t)())
	})
	t.Run("definition_history", func(t *testing.T) {
		checkDefinitionHistory(t, database(t), constraint)
	})
	t.Run("populated_contract", func(t *testing.T) {
		open := database(t)
		m := newFieldMigration(t, open(), constraint, "external_id", false, true, nil)
		table := m.table("m1_ticket")
		links := snapshot(t, m.Fixture, "m1_bookmark")
		statements, err := migrations.RenderMigrationSQL(t.Context(), m.loaded, m.change, migrations.DirectionForward, m.Renderer)
		body := strings.Join(statements, "\n")
		if err != nil || !strings.Contains(body, "NOT NULL") || !strings.Contains(body, "UNIQUE") {
			t.Fatal("missing physical constraints in SQL projection", body, err)
		}
		m.fail(t, m.change, migrations.DirectionForward, "not_null")
		m.column(t, "external_id", false)
		m.exec(t, "UPDATE "+table+" SET external_id='한''글/1' WHERE id=2")
		m.exec(t, "UPDATE "+table+" SET external_id='β/3' WHERE id=3")
		m.fail(t, m.change, migrations.DirectionForward, "unique")
		m.exec(t, "UPDATE "+table+" SET external_id='한''글/2' WHERE id=2")
		rows := snapshot(t, m.Fixture, "m1_ticket")
		m.apply(t, m.change)
		if err := m.SQL.Close(); err != nil {
			t.Fatal(err)
		}
		m.Fixture = open()
		state := m.state(t)
		m.apply(t, m.change)
		if !reflect.DeepEqual(state, m.state(t)) || !reflect.DeepEqual(rows, snapshot(t, m.Fixture, "m1_ticket")) || !reflect.DeepEqual(links, snapshot(t, m.Fixture, "m1_bookmark")) {
			t.Fatal("contract or repeated migrate changed rows, inbound links or revision")
		}
		m.column(t, "external_id", true)
		m.rejectWrite(t, "UPDATE "+table+" SET external_id=NULL WHERE id=2", "not_null")
		m.rejectWrite(t, "UPDATE "+table+" SET external_id='한''글/1' WHERE id=2", "unique")
		m.rejectWrite(t, "UPDATE "+table+" SET category_id=999 WHERE id=2", "foreign_key")
		var id int64
		if err := m.SQL.QueryRowContext(t.Context(), "INSERT INTO "+table+" (category_id,external_id) VALUES (2,'fresh') RETURNING id").Scan(&id); err != nil || id != 5 {
			t.Fatal("deleted ID was reused or next insert failed", id, err)
		}
		m.apply(t, m.initial)
		m.column(t, "external_id", false)
		m.exec(t, "UPDATE "+table+" SET external_id=NULL WHERE id=2")
		m.exec(t, "UPDATE "+table+" SET external_id='한''글/1' WHERE id=3")
		if !reflect.DeepEqual(links, snapshot(t, m.Fixture, "m1_bookmark")) {
			t.Fatal("reverse lost inbound rows")
		}
	})
	const value = "채움';\nβ"
	fill := &ir.Scalar{Kind: ir.ScalarString, String: value}
	t.Run("replace_unique_with_default", func(t *testing.T) {
		m := newFieldMigration(t, database(t)(), constraint, "external_id", true, false, fill)
		table := m.table("m1_ticket")
		prefix := m.state(t)
		projection, err := migrations.RenderMigrationSQL(t.Context(), m.loaded, m.change, migrations.DirectionForward, m.Renderer)
		if err != nil {
			t.Fatal("render literal backfill", err)
		}
		checkProjectedFill(t, m, projection, value)
		if !reflect.DeepEqual(prefix, m.state(t)) {
			t.Fatal("rolled-back projection changed the migration prefix")
		}
		m.apply(t, m.change)
		m.column(t, "external_id", true)
		var count int
		if err := m.SQL.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE (id IN (2,3) AND external_id=$1) OR (id=1 AND external_id='한''글/1')", value).Scan(&count); err != nil || count != 3 {
			t.Fatal("literal backfill did not preserve populated values", count, err)
		}
		m.fail(t, m.initial, migrations.DirectionBackward, "unique")
		m.column(t, "external_id", true)
		m.exec(t, "UPDATE "+table+" SET external_id='β/3' WHERE id=3")
		m.apply(t, m.initial)
		m.column(t, "external_id", false)
		m.exec(t, "UPDATE "+table+" SET external_id=NULL WHERE id=2")
		m.rejectWrite(t, "UPDATE "+table+" SET external_id='한''글/1' WHERE id=3", "unique")
	})
	t.Run("unique_default_collision", func(t *testing.T) {
		m := newFieldMigration(t, database(t)(), constraint, "external_id", false, true, fill)
		m.fail(t, m.change, migrations.DirectionForward, "unique")
		m.column(t, "external_id", false)
		m.exec(t, "UPDATE "+m.table("m1_ticket")+" SET external_id='β/3' WHERE id=3")
		m.apply(t, m.change)
		m.column(t, "external_id", true)
		var filled string
		if err := m.SQL.QueryRowContext(t.Context(), "SELECT external_id FROM "+m.table("m1_ticket")+" WHERE id=2").Scan(&filled); err != nil || filled != value {
			t.Fatal("retry failed to fill the remaining NULL", filled, err)
		}
	})
	t.Run("required_foreign_key", func(t *testing.T) {
		m := newFieldMigration(t, database(t)(), constraint, "category", false, false, nil)
		table := m.table("m1_ticket")
		links := snapshot(t, m.Fixture, "m1_bookmark")
		m.exec(t, "UPDATE "+table+" SET category_id=NULL WHERE id=2")
		m.fail(t, m.change, migrations.DirectionForward, "not_null")
		m.exec(t, "UPDATE "+table+" SET category_id=2 WHERE id=2")
		rows := snapshot(t, m.Fixture, "m1_ticket")
		m.apply(t, m.change)
		m.column(t, "category_id", true)
		m.rejectWrite(t, "UPDATE "+table+" SET category_id=NULL WHERE id=2", "not_null")
		m.rejectWrite(t, "UPDATE "+table+" SET category_id=999 WHERE id=2", "foreign_key")
		m.apply(t, m.initial)
		if !reflect.DeepEqual(rows, snapshot(t, m.Fixture, "m1_ticket")) || !reflect.DeepEqual(links, snapshot(t, m.Fixture, "m1_bookmark")) {
			t.Fatal("round trip changed FK values or incoming references")
		}
		m.column(t, "category_id", false)
		m.exec(t, "UPDATE "+table+" SET category_id=NULL WHERE id=2")
		m.rejectWrite(t, "UPDATE "+table+" SET category_id=999 WHERE id=2", "foreign_key")
	})
	for _, test := range []struct {
		kind  string
		field schema.Field
		kept  any
	}{
		{"text", schema.TextField("value", "Value", schema.Nullable(), schema.Blank()), "한'글"},
		{"binary", schema.BinaryField("value", "Value", schema.Nullable(), schema.Blank()), []byte{0, 255}},
		{"integer", schema.IntegerField("value", "Value", schema.Nullable(), schema.Blank()), int64(7)},
	} {
		t.Run("blank_"+test.kind, func(t *testing.T) {
			checkBlankTightening(t, database(t)(), constraint, test.kind, test.field, test.kept)
		})
	}
}

func checkBlankTightening(t *testing.T, f Fixture, constraint func(error) string, kind string, field schema.Field, kept any) {
	t.Helper()
	s, err := schema.Build(schema.Definition{AppLabel: "tickets", Models: []schema.Model{
		{Name: "ticket", GoName: "Ticket", DBTable: "m1_ticket", Fields: []schema.Field{field}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m := detectFieldMigration(t, f, constraint, s, "value", false, nil)
	m.apply(t, m.initial)
	table := f.table("m1_ticket")
	m.exec(t, "INSERT INTO "+table+" (value) VALUES (NULL), ($1)", kept)
	beforeRevision := snapshot(t, f, "godj_migration_revision")
	outcome := "applied"
	_, err = (migrations.Executor{Backend: f.Backend}).Migrate(t.Context(), m.loaded, migrations.LatestLifecycleRequest())
	if err != nil {
		if constraint(err) != "not_null" || !reflect.DeepEqual(beforeRevision, snapshot(t, f, "godj_migration_revision")) {
			t.Fatal("unexpected blank field failure or changed revision", err)
		}
		outcome = "IntegrityError"
	}
	m.column(t, "value", err == nil)
	values := make([]any, 2)
	for i := range values {
		if err := f.SQL.QueryRowContext(t.Context(), "SELECT value FROM "+table+" WHERE id=$1", i+1).Scan(&values[i]); err != nil {
			t.Fatal(err)
		}
		if bytes, ok := values[i].([]byte); ok {
			values[i] = hex.EncodeToString(bytes)
		}
	}
	backend := "postgresql"
	if f.SQLite {
		backend = "sqlite"
	}
	data, err := references.ReadFile("testdata/blank-tightening-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference map[string]map[string]any
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(map[string]any{"outcome": outcome, "nullable": outcome == "IntegrityError", "default": nil, "values": values})
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(actual, &decoded); err != nil || !reflect.DeepEqual(decoded, reference[backend][kind]) {
		t.Fatalf("native blank field observation %s/%s: %s != %v (%v)", backend, kind, actual, reference[backend][kind], err)
	}
}

func checkProjectedFill(t *testing.T, m fieldMigration, statements []string, value string) {
	t.Helper()
	ctx := t.Context()
	conn, err := m.SQL.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if m.SQLite {
		// sqlmigrate emits the operation body. The lifecycle owns the transaction
		// and temporarily suspends SQLite FK actions for the sealed table remake.
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
				t.Error(err)
			}
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatal("execute SQL projection", statement, err)
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+m.table("m1_ticket")+" WHERE external_id=$1", value).Scan(&count); err != nil || count != 2 {
		t.Fatal("projection did not apply the new literal to both NULL rows", count, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
