// Package migrationdefaulttest exercises populated-table default migrations
// through the public lifecycle and real backend writes, independently of the
// default-expression compilers.
package migrationdefaulttest

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/uuid"
)

type Backend interface {
	mb.RevisionFencedBackend
	mb.AppliedMigrationReader
	db.Session
}

type Fixture struct {
	Backend   Backend
	SQL       *sql.DB
	Namespace string
	Renderer  mb.MigrationSQLRenderer
	SQLite    bool
}

func (f Fixture) table(name string) string {
	return fmt.Sprintf("%q.%q", f.Namespace, name)
}

type scalarCase struct {
	name  string
	field schema.Field
	value query.Value
}

//go:embed testdata/defaults-django61-*.json
var references embed.FS

func assertReference(t *testing.T, fixture Fixture, name string, actual map[string]any) {
	t.Helper()
	if name == "json_null" || name == "text_sql_delimiters" {
		return
	} // Typed JSON null and SQL body escaping are independent Go invariants.
	backend := "postgres"
	if fixture.SQLite {
		backend = "sqlite"
	}
	data, err := references.ReadFile("testdata/defaults-django61-" + backend + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django       string
		Observations map[string]map[string]any
	}
	if err := json.Unmarshal(data, &reference); err != nil || reference.Django != "6.1" {
		t.Fatal("invalid default reference", err)
	}
	want, present := reference.Observations[name]
	if !present {
		t.Fatal("missing default reference", name)
	}
	// DEV-0013 preserves deleted IDs across SQLite remakes. Constrain the
	// adjustment to this exact observation, while retaining the raw oracle.
	if fixture.SQLite {
		if want["next_id"] != float64(3) {
			t.Fatal("changed sequence reference requires review")
		}
		want["next_id"] = float64(4)
	}
	encoded, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil || !reflect.DeepEqual(normalized, want) {
		t.Fatalf("default differs from independent reference: %v != %v (%v)", normalized, want, err)
	}
}

func fieldReference(test scalarCase, name string) query.FieldRef {
	if test.field.Decimal != nil {
		return query.NewDecimalFieldRef(name, name, test.field.Nullable, test.field.Decimal.MaxDigits, test.field.Decimal.DecimalPlaces)
	}
	return query.NewFieldRef(name, name, query.FieldKind(test.value.Kind()), test.field.Nullable)
}

func scalarCases() []scalarCase {
	number := decimal.Decimal{Coefficient: "125", Exponent: -2}
	id := uuid.UUID{0: 0x12, 6: 0x40, 8: 0x80, 15: 0xff}
	document := jsonvalue.Value{Text: `{"large":9007199254740993,"text":"한"}`}
	day := calendar.Date{Year: 2000, Month: 2, Day: 29}
	clockValue := clock.Time{Hour: 23, Minute: 59, Second: 58, Microsecond: 123456}
	elapsed := duration.Duration{Days: -1, Microseconds: 86_399_999_877}
	instant := time.Date(2026, 9, 27, 1, 2, 3, 456789000, time.FixedZone("reference", 3*3600))
	return []scalarCase{
		{"integer", schema.IntegerField("added", "Added", schema.Default(int64(1))), query.Integer(1)},
		{"integer_min", schema.IntegerField("added", "Added", schema.Default(int64(-9223372036854775808))), query.Integer(-9223372036854775808)},
		{"boolean_false", schema.BooleanField("added", "Added", schema.Default(false)), query.Boolean(false)},
		{"empty_text", schema.CharField("added", "Added", 100, schema.Default("")), query.String("")},
		{"quoted_text", schema.TextField("added", "Added", schema.Default("O'Brien\\\n한")), query.String("O'Brien\\\n한")},
		{"text_sql_delimiters", schema.TextField("added", "Added", schema.Default(";\t\r\x1b\u0085\\' --\n")), query.String(";\t\r\x1b\u0085\\' --\n")},
		{"nullable_default", schema.CharField("added", "Added", 100, schema.Nullable(), schema.Default("filled")), query.String("filled")},
		{"float", schema.FloatField("added", "Added", schema.Default(1.25)), query.Float(1.25)},
		{"decimal", schema.DecimalField("added", "Added", 9, 2, schema.Default(number)), query.Decimal(number)},
		{"uuid", schema.UUIDField("added", "Added", schema.Default(id)), query.UUID(id)},
		{"json", schema.JSONField("added", "Added", schema.Default(document)), query.JSON(document)},
		{"json_null", schema.JSONField("added", "Added", schema.Default(jsonvalue.Null())), query.JSON(jsonvalue.Null())},
		{"date", schema.DateField("added", "Added", schema.Default(day)), query.Date(day)},
		{"time", schema.TimeField("added", "Added", schema.Default(clockValue)), query.Time(clockValue)},
		{"duration", schema.DurationField("added", "Added", schema.Default(elapsed)), query.Duration(elapsed)},
		{"datetime", schema.DateTimeField("added", "Added", schema.Default(instant)), query.DateTime(instant)},
	}
}

func definitions(t *testing.T, field schema.Field) (migrations.LoadedDefinitionSet, migrations.MigrationKey, migrations.MigrationKey) {
	t.Helper()
	expected := field
	expected.Name, expected.GoName, expected.Column, expected.Default = "expected", "Expected", "expected", nil
	normalized, err := schema.Build(schema.Definition{AppLabel: "default_fixture", Models: []schema.Model{
		{Name: "parent", GoName: "Parent", Fields: []schema.Field{expected, field}},
		{Name: "child", GoName: "Child", Fields: []schema.Field{schema.ForeignKey("parent", "ParentID", schema.Target("default_fixture", "parent"), schema.RelatedName("children"), schema.Cascade)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	parent, child := normalized.Models[0].Clone(), normalized.Models[1]
	added := parent.Fields[len(parent.Fields)-1].Clone()
	parent.Fields = parent.Fields[:len(parent.Fields)-1]
	initial := migrations.Migration{App: normalized.AppLabel, Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: normalized.AppLabel, Model: parent}, migrations.CreateModel{AppLabel: normalized.AppLabel, Model: child},
	}}
	addition := migrations.Migration{App: normalized.AppLabel, Name: "0002_default", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: normalized.AppLabel, ModelName: "parent", Field: added}}}
	sources := []definition.Source{}
	for _, migration := range []migrations.Migration{initial, addition} {
		encoded, err := definition.Encode(definition.Producer{Name: "default-fixture", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: encoded})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return loaded, initial.Key(), addition.Key()
}

func insert(t *testing.T, f Fixture, table string, assignments ...query.Assignment) int64 {
	t.Helper()
	id, err := f.Backend.Insert(t.Context(), query.NewInsertPlanReturningKey(table, assignments, query.NewFieldRef("id", "id", query.FieldInteger, false)))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func snapshot(t *testing.T, f Fixture, table string) []string {
	t.Helper()
	rows, err := f.SQL.QueryContext(t.Context(), "SELECT * FROM "+f.table(table)+" ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	result := []string{}
	for rows.Next() {
		values, pointers := make([]any, len(columns)), make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		result = append(result, fmt.Sprint(values))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return result
}

func Run(t *testing.T, open func(*testing.T) Fixture) {
	for _, test := range scalarCases() {
		t.Run(test.name, func(t *testing.T) {
			f := open(t)
			loaded, initial, latest := definitions(t, test.field)
			executor := migrations.Executor{Backend: f.Backend}
			if _, err := executor.Migrate(t.Context(), loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial))); err != nil {
				t.Fatal(err)
			}
			expected := fieldReference(test, "expected")
			for range 3 {
				insert(t, f, "default_fixture_parent", query.NewAssignment(expected, test.value))
			}
			if _, err := f.Backend.Delete(t.Context(), query.NewDeletePlan("default_fixture_parent", query.NewFieldRef("id", "id", query.FieldInteger, false), query.Integer(3))); err != nil {
				t.Fatal(err)
			}
			for _, id := range []int64{1, 2} {
				insert(t, f, "default_fixture_child", query.NewAssignment(query.NewFieldRef("parent", "parent_id", query.FieldInteger, false), query.Integer(id)))
			}
			links := snapshot(t, f, "default_fixture_child")
			if statements, err := migrations.RenderMigrationSQL(t.Context(), loaded, latest, f.Renderer); err != nil || len(statements) == 0 {
				t.Fatal("missing SQL projection", err)
			}
			matched, defaults := 0, 0
			for cycle := range 2 {
				if _, err := executor.Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
					t.Fatal("default migration", err)
				}
				if err := f.SQL.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+f.table("default_fixture_parent")+` WHERE "added" = "expected"`).Scan(&matched); err != nil || matched != 2 {
					t.Fatal("backfill differs from independent ordinary write", matched, err)
				}
				if !reflect.DeepEqual(links, snapshot(t, f, "default_fixture_child")) {
					t.Fatal("parent remake changed incoming references")
				}
				defaults = assertNoDefault(t, f)
				if cycle == 0 {
					if _, err := executor.Migrate(t.Context(), loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial))); err != nil {
						t.Fatal("reverse default addition", err)
					}
				}
			}
			added := fieldReference(test, "added")
			nextID := insert(t, f, "default_fixture_parent", query.NewAssignment(expected, test.value), query.NewAssignment(added, test.value))
			if nextID != 4 {
				t.Fatal("sequence high-water was not preserved", nextID)
			}
			missing := "rejected"
			_, err := f.Backend.Insert(t.Context(), query.NewInsertPlanReturningKey("default_fixture_parent", []query.Assignment{query.NewAssignment(expected, test.value)}, query.NewFieldRef("id", "id", query.FieldInteger, false)))
			if test.field.Nullable {
				missing = "null"
				var nulls int
				if err != nil {
					t.Fatal(err)
				}
				if err := f.SQL.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+f.table("default_fixture_parent")+` WHERE "added" IS NULL`).Scan(&nulls); err != nil || nulls != 1 {
					t.Fatal("nullable raw INSERT received a persistent default", nulls, err)
				}
			} else if err == nil {
				t.Fatal("required raw INSERT received a persistent default")
			}
			assertReference(t, f, test.name, map[string]any{"matched_rows": matched, "links_preserved": reflect.DeepEqual(links, snapshot(t, f, "default_fixture_child")), "persistent_default": defaults != 0, "next_id": nextID, "raw_missing": missing})
		})
	}
	t.Run("unique_default_failure_preserves_rows_catalog_and_revision", func(t *testing.T) {
		f := open(t)
		field := schema.IntegerField("added", "Added", schema.Unique(), schema.Default(int64(7)))
		loaded, initial, _ := definitions(t, field)
		executor := migrations.Executor{Backend: f.Backend}
		if _, err := executor.Migrate(t.Context(), loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(initial))); err != nil {
			t.Fatal(err)
		}
		for _, value := range []int64{10, 20} {
			insert(t, f, "default_fixture_parent", query.NewAssignment(query.NewFieldRef("expected", "expected", query.FieldInteger, false), query.Integer(value)))
		}
		insert(t, f, "default_fixture_child", query.NewAssignment(query.NewFieldRef("parent", "parent_id", query.FieldInteger, false), query.Integer(1)))
		parents, links, revision := snapshot(t, f, "default_fixture_parent"), snapshot(t, f, "default_fixture_child"), snapshot(t, f, "godj_migration_revision")
		history, err := f.Backend.ReadAppliedMigrations(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err == nil {
			t.Fatal("duplicate constant unique default committed")
		}
		if !reflect.DeepEqual(parents, snapshot(t, f, "default_fixture_parent")) || !reflect.DeepEqual(links, snapshot(t, f, "default_fixture_child")) || !reflect.DeepEqual(revision, snapshot(t, f, "godj_migration_revision")) {
			t.Fatal("failed default changed rows, columns, links or revision")
		}
		after, err := f.Backend.ReadAppliedMigrations(t.Context())
		if err != nil || !reflect.DeepEqual(history, after) {
			t.Fatal("failed default recorded migration", err)
		}
	})
}

func assertNoDefault(t *testing.T, f Fixture) int {
	t.Helper()
	var defaults int
	statement := `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='default_fixture_parent' AND column_default IS NOT NULL AND column_name='added'`
	args := []any{f.Namespace}
	if f.SQLite {
		statement, args = `SELECT COUNT(*) FROM pragma_table_info('default_fixture_parent') WHERE "name"='added' AND "dflt_value" IS NOT NULL`, nil
	}
	if err := f.SQL.QueryRowContext(t.Context(), statement, args...).Scan(&defaults); err != nil || defaults != 0 {
		t.Fatal("persistent database default", defaults, err)
	}
	return defaults
}
