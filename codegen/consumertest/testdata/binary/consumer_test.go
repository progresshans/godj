package consumer_test

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"example.com/godj-binary/models"
	"example.com/godj-binary/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed reference-*.json
var references embed.FS

type binaryBackend interface {
	db.Queryer
	db.Mutator
	db.Atomic
	migrationbackend.RevisionFencedBackend
	Close() error
}

type packed struct{ Kind, Base64 string }
type nativeReference struct {
	Django, DRF, Backend string
	Storage              struct {
		Rows     [][]json.RawMessage
		Queries  map[string][]string
		Min, Max struct {
			Value     packed
			Exception string
		}
	}
}

func parse(t *testing.T, text string) binaryvalue.Value {
	t.Helper()
	value, err := binaryvalue.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func nativeRows(t *testing.T, rows [][]json.RawMessage) [][]string {
	t.Helper()
	result := make([][]string, len(rows))
	for i, row := range rows {
		if len(row) != 2 {
			t.Fatal("native binary row shape changed")
		}
		var label string
		var value packed
		if err := json.Unmarshal(row[0], &label); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(row[1], &value); err != nil {
			t.Fatal(err)
		}
		text := "<null>"
		if value.Kind == "binary" {
			text = parse(t, value.Base64).Base64()
		} else if value.Kind != "null" {
			t.Fatal("native binary type changed")
		}
		result[i] = []string{label, text}
	}
	return result
}

func TestBinaryGeneratedDefaults(t *testing.T) {
	sample := binaryvalue.Value{Data: "\x00\xffa\x80"}
	if err := (models.RecordCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("missing required binary accepted")
	}
	mutation := models.NewValueCreate().WithBinary(binaryvalue.Value{}).BuildCreate()
	if mutation.Err() != nil {
		t.Fatal(mutation.Err())
	}
	values := map[string]query.Value{}
	for _, assignment := range mutation.Assignments() {
		values[assignment.Field().Name()] = assignment.Value()
	}
	if !values["binary"].Equal(query.Binary(binaryvalue.Value{})) || !values["value"].Equal(query.Binary(sample)) {
		t.Fatal("binary package/model/member collision")
	}
	for _, test := range []struct {
		input             models.RecordCreate
		reference, origin query.Value
	}{
		{models.NewRecordCreate("default", sample), query.Null(), query.Binary(binaryvalue.Value{})},
		{models.NewRecordCreate("empty", sample).WithReference(binaryvalue.Value{}).WithOriginNull(), query.Binary(binaryvalue.Value{}), query.Null()},
		{models.NewRecordCreate("null", sample).WithReferenceNull().WithOrigin(sample), query.Null(), query.Binary(sample)},
	} {
		mutation := test.input.BuildCreate()
		if mutation.Err() != nil {
			t.Fatal(mutation.Err())
		}
		values := map[string]query.Value{}
		for _, assignment := range mutation.Assignments() {
			values[assignment.Field().Name()] = assignment.Value()
		}
		for name, want := range map[string]query.Value{"reference": test.reference, "origin": test.origin, "scheduled": query.Binary(sample)} {
			if got, ok := values[name]; !ok || !got.Equal(want) {
				t.Fatal("binary default/presence lost", name)
			}
		}
	}
}

func TestBinaryStorageQueryAndOwnership(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "binary.sqlite3")) + "?mode=rwc"
		runStorage(t, "sqlite", func(ctx context.Context) (binaryBackend, error) { return sqlite.Open(ctx, dsn) })
	})
	databaseURL := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL connection absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), databaseURL)
		if err != nil {
			t.Fatal("connect binary consumer PostgreSQL")
		}
		name := fmt.Sprintf("godj_binary_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{name}.Sanitize()
		if _, err := connection.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
			_ = connection.Close(context.Background())
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
				t.Error(err)
			}
			if err := connection.Close(ctx); err != nil {
				t.Error(err)
			}
		})
		runStorage(t, "postgres", func(ctx context.Context) (binaryBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: name})
		})
	})
}

func history(t *testing.T) (migrations.LoadedDefinitionSet, []migrations.MigrationKey) {
	t.Helper()
	current := (models.RecordDescriptor{}).Metadata()
	original := current.Clone()
	original.Fields = slices.DeleteFunc(original.Fields, func(field ir.Field) bool { return field.Name == "reference" || field.Name == "scheduled" })
	initial := migrations.Migration{App: "binaryref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "binaryref", Model: original}}}
	addition := migrations.Migration{App: "binaryref", Name: "0002_binary", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		migrations.AddField{AppLabel: "binaryref", ModelName: "record", Field: current.Fields[2], BeforeField: "required"},
		migrations.AddField{AppLabel: "binaryref", ModelName: "record", Field: current.Fields[5], BeforeField: "internal_note"},
		migrations.CreateModel{AppLabel: "binaryref", Model: (models.LinkDescriptor{}).Metadata()},
	}}
	all := []migrations.Migration{initial, addition}
	before := current.Fields[2].Clone()
	for _, mutate := range []func(*ir.Field){
		func(f *ir.Field) { f.MaxLength = 8 }, func(f *ir.Field) { f.NonEditable = true }, func(f *ir.Field) { f.DBIndex = true }, func(f *ir.Field) { f.Unique = true },
		func(f *ir.Field) { f.Unique = false }, func(f *ir.Field) { f.DBIndex = false }, func(f *ir.Field) { f.NonEditable = false }, func(f *ir.Field) { f.MaxLength = 4 },
	} {
		after := before.Clone()
		mutate(&after)
		next := migrations.Migration{App: "binaryref", Name: fmt.Sprintf("%04d_policy", len(all)+1), Dependencies: []migrations.MigrationKey{all[len(all)-1].Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "binaryref", ModelName: "record", Before: before, After: after}}}
		all = append(all, next)
		before = after
	}
	var sources []definition.Source
	var targets []migrations.MigrationKey
	for _, migration := range all {
		wire, err := definition.Encode(definition.Producer{Name: "binary-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
		targets = append(targets, migration.Key())
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return loaded, targets
}

func runStorage(t *testing.T, name string, open func(context.Context) (binaryBackend, error)) {
	t.Helper()
	ctx := t.Context()
	sample := binaryvalue.Value{Data: "\x00\xffa\x80"}
	empty := binaryvalue.Value{}
	ordinary := binaryvalue.Value{Data: "abc"}
	backend, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if backend != nil {
			if err := backend.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	raw, err := references.ReadFile("reference-" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var native nativeReference
	if err := json.Unmarshal(raw, &native); err != nil {
		t.Fatal(err)
	}
	if native.Django != "6.1" || native.DRF != "3.18.0" || len(native.Storage.Rows) != 7 || len(native.Storage.Queries) != 10 {
		t.Fatal("incomplete native binary observation")
	}
	expected := nativeRows(t, native.Storage.Rows)
	loaded, targets := history(t)
	migrate := func(target migrations.MigrationKey) migrations.ProjectState {
		t.Helper()
		state, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(target)))
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	migrate(targets[0])
	current := (models.RecordDescriptor{}).Metadata()
	if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(current.DBTable, []query.Assignment{
		orm.NewAssignment(current.Fields[1], query.String("null")), orm.NewAssignment(current.Fields[3], query.Binary(empty)), orm.NewAssignment(current.Fields[4], query.Binary(empty)), orm.NewAssignment(current.Fields[6], query.String("server")),
	}, query.NewFieldRef("id", "id", query.FieldInteger, false))); err != nil {
		t.Fatal(err)
	}
	migrate(targets[1])
	for _, row := range expected[1:] {
		value := empty
		create := models.NewRecordCreate(row[0], value)
		if row[1] != "<null>" {
			value = parse(t, row[1])
			create = models.NewRecordCreate(row[0], value).WithReference(value)
		}
		if _, err := models.RecordObjects.Create(ctx, backend, create); err != nil {
			t.Fatal(err)
		}
	}
	observe := func() ([]models.Record, [][]string) {
		t.Helper()
		rows, err := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		values := make([][]string, len(rows))
		for i, row := range rows {
			text := "<null>"
			if row.Reference != nil {
				text = row.Reference.Base64()
			}
			values[i] = []string{row.Label, text}
			if row.Origin == nil || *row.Origin != empty || row.Scheduled != sample || row.InternalNote != "server" {
				t.Fatal("binary backfill/default or server scalar lost")
			}
		}
		return rows, values
	}
	if _, values := observe(); !reflect.DeepEqual(values, expected) {
		t.Fatal("binary storage differs from native", values)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend, err = open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, renderer := range []migrationbackend.MigrationSQLRenderer{sqlite.NewMigrationSQLRenderer(), postgres.NewMigrationSQLRenderer(postgres.MigrationSQLConfig{Schema: "binary_sql"})} {
		for _, index := range []int{2, 3, 8, 9} {
			statements, err := migrations.RenderMigrationSQL(ctx, loaded, targets[index], renderer)
			if err != nil || statements == nil || len(statements) != 0 {
				t.Fatal("binary input policy produced storage DDL", index, statements, err)
			}
		}
	}
	staleBackend, err := open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := staleBackend.OpenRevisionFencedSession(ctx)
	if err != nil {
		_ = staleBackend.Close()
		t.Fatal(err)
	}
	staleClosed := false
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !staleClosed {
			if err := stale.Close(cleanup); err != nil {
				t.Error(err)
			}
		}
		if err := staleBackend.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := stale.ReadAppliedMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	policies := []struct {
		maximum                 int
		hidden, indexed, unique bool
	}{{8, false, false, false}, {8, true, false, false}, {8, true, true, false}, {8, true, true, true}, {8, true, true, false}, {8, true, false, false}, {8, false, false, false}, {4, false, false, false}}
	for i, target := range targets[2:] {
		if i == 3 {
			duplicate, err := models.RecordObjects.Create(ctx, backend, models.NewRecordCreate("migration duplicate", ordinary).WithReference(ordinary))
			if err != nil {
				t.Fatal(err)
			}
			beforeHistory := binaryHistorySnapshot(t, backend)
			if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(target))); err == nil {
				t.Fatal("duplicate bytes accepted a unique migration")
			}
			if !reflect.DeepEqual(beforeHistory, binaryHistorySnapshot(t, backend)) {
				t.Fatal("failed binary DDL advanced history")
			}
			if count, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.Reference.Exact(ordinary)).Count(ctx); err != nil || count != 2 {
				t.Fatal("failed binary DDL lost duplicate rows", err)
			}
			if count, err := backend.Delete(ctx, query.NewDeletePlan(current.DBTable, query.NewFieldRef("id", "id", query.FieldInteger, false), query.Integer(duplicate.ID))); err != nil || count != 1 {
				t.Fatal("remove owned duplicate", err)
			}
		}
		state := migrate(target)
		model, found := state.Model("binaryref", "record")
		policy := policies[i]
		if !found || model.Fields[2].Kind != ir.FieldBinary || model.Fields[2].MaxLength != policy.maximum || model.Fields[2].NonEditable != policy.hidden || model.Fields[2].DBIndex != policy.indexed || model.Fields[2].Unique != policy.unique {
			t.Fatal("binary logical history lost input/index policy")
		}
		if i == 0 {
			after := model.Clone()
			after.Fields[2].NonEditable = true
			tx, err := stale.BeginMigration(ctx, migrationbackend.HistoryTransition{Migration: migrationbackend.AppliedMigration{App: "binaryref", Name: targets[3].Name}, Kind: migrationbackend.HistoryTransitionApply}, migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{Kind: migrationbackend.MigrationAlterField, Before: model, After: after}}})
			if tx != nil {
				_ = tx.Rollback(ctx)
			}
			closeErr := stale.Close(ctx)
			staleClosed = true
			var fence *migrationbackend.RevisionFenceError
			if !errors.As(err, &fence) || fence.Kind != migrationbackend.RevisionFenceFailureStale || closeErr != nil {
				t.Fatal("metadata-only binary change did not advance revision", err, closeErr)
			}
		}
		if _, values := observe(); !reflect.DeepEqual(values, expected) {
			t.Fatal("binary policy/index transition changed bytes or NULL")
		}
		if i == 3 {
			if _, err := models.RecordObjects.Create(ctx, backend, models.NewRecordCreate("duplicate", ordinary).WithReference(ordinary)); err == nil {
				t.Fatal("unique binary bytes accepted")
			}
			if _, values := observe(); !reflect.DeepEqual(values, expected) {
				t.Fatal("failed unique write changed rows")
			}
		}
	}
	for _, probe := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Record]
		exclude   bool
	}{
		{"exact", "reference", sample, models.RecordFields.Reference.Exact(sample), false},
		{"empty", "reference", empty, models.RecordFields.Reference.Exact(empty), false},
		{"null", "reference__isnull", true, models.RecordFields.Reference.IsNull(true), false},
		{"in", "reference__in", []any{empty, ordinary, nil}, models.RecordFields.Reference.In(empty, ordinary), false},
		{"greater", "reference__gt", binaryvalue.Value{Data: "\x00"}, models.RecordFields.Reference.GreaterThan(binaryvalue.Value{Data: "\x00"}), false},
		{"less", "reference__lt", ordinary, models.RecordFields.Reference.LessThan(ordinary), false},
		{"empty_in", "reference__in", []binaryvalue.Value{}, models.RecordFields.Reference.In(), false},
		{"exclude", "reference", ordinary, orm.Not(models.RecordFields.Reference.Exact(ordinary)), true},
	} {
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: probe.key, Value: probe.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatal("dynamic binary lookup", err)
		}
		if probe.exclude {
			dynamic[0] = orm.Not(dynamic[0])
		}
		for _, predicate := range []orm.Predicate[models.Record]{probe.typed, dynamic[0]} {
			rows, err := models.RecordObjects.Using(backend).Filter(predicate).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			labels := []string{}
			for _, row := range rows {
				labels = append(labels, row.Label)
			}
			if !slices.Equal(labels, native.Storage.Queries[probe.name]) {
				t.Fatal("binary query differs from native", probe.name, labels)
			}
		}
	}
	for _, value := range []any{sample.Base64(), sample.Bytes(), int64(1), []string{sample.Base64()}} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "reference", Value: value}}); err == nil {
			t.Fatal("dynamic binary coerced another type")
		}
	}
	all := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 7 || rows[1].Reference == nil || *rows[1].Reference != empty {
		t.Fatal("empty binary lost presence")
	}
	*rows[1].Reference = ordinary
	*rows[1].Origin = ordinary
	again, err := all.All(ctx)
	if err != nil || *again[1].Reference != empty || *again[1].Origin != empty {
		t.Fatal("binary pointer escaped query cache", err)
	}
	projected, err := orm.SelectInto(ctx, all, orm.Project1(models.RecordFields.Reference, func(v *binaryvalue.Value) *binaryvalue.Value { return v }))
	if err != nil || len(projected) != 7 || projected[0] != nil || projected[1] == nil || *projected[1] != empty || projected[6] != nil {
		t.Fatal("binary projection presence", err)
	}
	*projected[1] = ordinary
	if *again[1].Reference != empty {
		t.Fatal("binary projection aliases model")
	}
	for _, source := range []orm.QuerySet[models.Record]{all, all.Distinct()} {
		type bounds struct {
			Min, Max orm.Optional[binaryvalue.Value]
		}
		got, err := orm.AggregateInto(ctx, source, orm.Aggregate2(orm.Min(models.RecordFields.Reference), orm.Max(models.RecordFields.Reference), func(a, b orm.Optional[binaryvalue.Value]) bounds { return bounds{a, b} }))
		if err != nil {
			t.Fatal(err)
		}
		minimum, minOK := got.Min.Get()
		maximum, maxOK := got.Max.Get()
		if !minOK || !maxOK || minimum != empty || maximum != ordinary {
			t.Fatal("binary aggregate byte order changed")
		}
	}
	if name == "postgres" {
		if native.Storage.Min.Exception != "ProgrammingError" || native.Storage.Max.Exception != "ProgrammingError" {
			t.Fatal("native bytea aggregate limitation changed")
		}
	} else if native.Storage.Min.Value.Base64 != empty.Base64() || native.Storage.Max.Value.Base64 != ordinary.Base64() {
		t.Fatal("SQLite aggregate differs from native")
	}
	limited, err := all.Limit(3)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := orm.AggregateInto(ctx, limited, orm.Aggregate1(orm.Max(models.RecordFields.Reference), func(v orm.Optional[binaryvalue.Value]) orm.Optional[binaryvalue.Value] { return v }))
	if value, ok := bound.Get(); err != nil || !ok || value != sample {
		t.Fatal("sliced binary aggregate escaped source", err)
	}
	for _, source := range []orm.QuerySet[models.Record]{all.Filter(models.RecordFields.ID.Exact(-1)), all.Filter(models.RecordFields.Reference.IsNull(true))} {
		value, err := orm.AggregateInto(ctx, source, orm.Aggregate1(orm.Max(models.RecordFields.Reference), func(v orm.Optional[binaryvalue.Value]) orm.Optional[binaryvalue.Value] { return v }))
		if err != nil || value.Valid() {
			t.Fatal("empty aggregate invented bytes", err)
		}
	}
	equal, err := all.Filter(models.RecordFields.Reference.ExactField(orm.F(models.RecordFields.Required))).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{}
	for _, row := range equal {
		labels = append(labels, row.Label)
	}
	if !slices.Equal(labels, native.Storage.Queries["same_field"]) {
		t.Fatal("binary F comparison changed")
	}
	ordered, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.Reference.IsNull(false)).OrderBy(models.RecordFields.Reference.Asc()).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	labels = nil
	for _, row := range ordered {
		labels = append(labels, row.Label)
	}
	if !slices.Equal(labels, native.Storage.Queries["order"]) {
		t.Fatal("binary ordering changed", labels)
	}
	verifyRelations(t, backend, again, sample, native.Storage.Queries["order"])
	rollback := errors.New("binary rollback")
	err = backend.Atomic(ctx, func(session db.Session) error {
		if _, err := models.RecordObjects.Update(ctx, session, again[2], models.RecordPatch{}.WithReference(ordinary)); err != nil {
			return err
		}
		stored, found, err := models.RecordObjects.Using(session).Filter(models.RecordFields.ID.Exact(again[2].ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
		if err != nil || !found || stored.Reference == nil || *stored.Reference != ordinary {
			return errors.New("transaction binary read changed")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, values := observe(); !reflect.DeepEqual(values, expected) {
		t.Fatal("binary rollback changed stored values")
	}
	verifyBinaryDeferredForm(t, backend, again[2])
	verifyWrites(t, backend, again[2], sample, ordinary)
	migrate(targets[0])
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	label := query.NewFieldRef("label", "label", query.FieldString, false)
	remaining, err := backend.Query(ctx, query.NewPlan(current.DBTable, []query.FieldRef{id, label}).WithOrderings(query.NewOrdering(id, query.Ascending)))
	if err != nil {
		t.Fatal(err)
	}
	defer remaining.Close()
	labels = nil
	for remaining.Next() {
		var id int64
		var label string
		if err := remaining.Scan(&id, &label); err != nil {
			t.Fatal(err)
		}
		labels = append(labels, label)
	}
	if err := remaining.Err(); err != nil || len(labels) != len(expected) {
		t.Fatal("reverse migration lost rows", err)
	}
	if err := remaining.Close(); err != nil {
		t.Fatal(err)
	}
	for i, label := range labels {
		if label != expected[i][0] {
			t.Fatal("reverse migration changed labels")
		}
	}
	migrate(targets[1])
	for _, row := range func() []models.Record { rows, _ := observe(); return rows }() {
		if row.Reference != nil {
			t.Fatal("binary re-add invented removed bytes")
		}
	}
}

func verifyRelations(t *testing.T, backend binaryBackend, records []models.Record, sample binaryvalue.Value, expectedOrder []string) {
	t.Helper()
	ctx := t.Context()
	for _, record := range records {
		if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate(record.Label).WithRecordID(record.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate("missing")); err != nil {
		t.Fatal(err)
	}
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	bound, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	link, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "binaryref", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	field := relations.ModelsLink.Record.Reference
	for _, probe := range []struct {
		key   string
		value any
		typed orm.Predicate[models.Link]
		want  []string
	}{
		{"record__reference", sample, field.Exact(sample), []string{"binary"}},
		{"record__reference__isnull", true, field.IsNull(true), []string{"null", "null_again", "missing"}},
		{"record__reference__in", []binaryvalue.Value{sample}, field.In(sample), []string{"binary"}},
	} {
		dynamic, err := orm.ParseDynamicRelations(link, nil, []orm.LookupInput{{Key: probe.key, Value: probe.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatal(err)
		}
		for _, predicate := range []orm.Predicate[models.Link]{probe.typed, dynamic[0]} {
			rows, err := facade.ModelsLink.Filter(predicate).OrderBy(models.LinkFields.ID.Asc()).SelectRelated(facade.ModelsLink.Related.Record).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			labels := []string{}
			for _, row := range rows {
				raw, err := row.Unwrap()
				if err != nil {
					t.Fatal(err)
				}
				labels = append(labels, raw.Label)
				record, present, err := row.Record(ctx)
				if err != nil || present != (raw.RecordID != nil) {
					t.Fatal("binary eager owner presence", err)
				}
				if present && record.Label == "binary" {
					snapshot, err := record.Unwrap()
					if err != nil {
						t.Fatal(err)
					}
					if snapshot.Reference == nil || *snapshot.Reference != sample {
						t.Fatal("eager binary value changed")
					}
					snapshot.Reference.Data = "mutated"
					again, _, err := row.Record(ctx)
					if err != nil || again.Reference == nil || *again.Reference != sample {
						t.Fatal("eager binary snapshot aliases cache", err)
					}
				}
			}
			if !slices.Equal(labels, probe.want) {
				t.Fatal("binary relation labels", labels, probe.want)
			}
		}
	}
	ordered, err := models.LinkObjects.Using(backend).Filter(field.IsNull(false)).OrderBy(field.Asc()).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{}
	for _, row := range ordered {
		labels = append(labels, row.Label)
	}
	if !slices.Equal(labels, expectedOrder) {
		t.Fatal("related binary ordering differs from native", labels)
	}
	if count, err := models.RecordObjects.Using(backend).Filter(relations.ModelsRecord.Links.Token.Exact(sample)).Count(ctx); err != nil || count != 7 {
		t.Fatal("reverse binary query", count, err)
	}
	values, err := orm.SelectInto(ctx, models.LinkObjects.Using(backend).OrderBy(models.LinkFields.ID.Asc()), orm.Project1(field, func(v *binaryvalue.Value) *binaryvalue.Value { return v }))
	if err != nil || len(values) != 8 || values[1] == nil || values[1].Data != "" || values[2] == nil || *values[2] != sample || values[7] != nil {
		t.Fatal("related binary scalar projection lost bytes/NULL", err)
	}
}

type countedMutator struct {
	db.Mutator
	writes int
	fail   bool
}

func (m *countedMutator) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	m.writes++
	if m.fail {
		return 0, errors.New("injected insert failure")
	}
	return m.Mutator.Insert(ctx, plan)
}
func (m *countedMutator) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	m.writes++
	if m.fail {
		return 0, errors.New("injected update failure")
	}
	return m.Mutator.Update(ctx, plan)
}

func verifyWrites(t *testing.T, backend binaryBackend, row models.Record, sample, other binaryvalue.Value) {
	t.Helper()
	ctx := t.Context()
	mutator := &countedMutator{Mutator: backend, fail: true}
	changed := models.RecordDescriptor{}.CloneWriteModel(row)
	changed.Reference = &other
	before := models.RecordDescriptor{}.CloneWriteModel(changed)
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Reference)); err == nil || !reflect.DeepEqual(changed, before) {
		t.Fatal("failed binary Save mutated caller")
	}
	mutator.fail = false
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Reference)); err != nil {
		t.Fatal(err)
	}
	changed.Reference = nil
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Label)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.Exact(row.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Reference == nil || *stored.Reference != other || row.Reference == nil || *row.Reference != sample {
		t.Fatal("Save mask or caller ownership changed", err)
	}
	if _, err := models.RecordObjects.Update(ctx, backend, stored, models.RecordPatch{}.WithReferenceNull()); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	beforeWrites := mutator.writes
	if _, err := models.RecordObjects.Create(canceled, mutator, models.NewRecordCreate("canceled", sample)); !errors.Is(err, context.Canceled) || mutator.writes != beforeWrites {
		t.Fatal("canceled binary create reached I/O", err)
	}
}

func binaryHistorySnapshot(t *testing.T, backend binaryBackend) []migrationbackend.AppliedMigration {
	t.Helper()
	session, err := backend.OpenRevisionFencedSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	records, readErr := session.ReadAppliedMigrations(t.Context())
	closeErr := session.Close(t.Context())
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	return records
}

func verifyBinaryDeferredForm(t *testing.T, backend binaryBackend, current models.Record) {
	t.Helper()
	ctx := t.Context()
	spec, err := formmodel.NewSpecForFields((models.RecordDescriptor{}).Metadata(), []string{"label", "reference"})
	if err != nil {
		t.Fatal(err)
	}
	data := map[string][]string{"label": {"Deferred binary"}, "reference": {"AP8="}, "required": {"attacker"}, "internal_note": {"attacker"}}
	bound, err := formmodel.BindInstance(ctx, models.RecordObjects, spec, forms.NewData(data), &current, formmodel.PostClean{})
	if err != nil || !bound.BoundForm().Form().Valid() {
		t.Fatal("deferred binary form", err)
	}
	prepared, err := bound.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := prepared.Model()
	if err != nil || candidate.Reference == nil || candidate.Reference.Data != "\x00\xff" || candidate.Required != current.Required || candidate.InternalNote != current.InternalNote {
		t.Fatal("binary form lost typed/excluded fields", err)
	}
	read := func() models.Record {
		t.Helper()
		row, found, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.Exact(current.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal(err)
		}
		return row
	}
	if !reflect.DeepEqual(read(), current) {
		t.Fatal("deferred binary form persisted before Save")
	}
	stop := errors.New("deferred binary rollback")
	err = backend.Atomic(ctx, func(session db.Session) error {
		if err := prepared.Save(ctx, session, &candidate); err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) || !reflect.DeepEqual(read(), current) {
		t.Fatal("deferred binary Save escaped rollback", err)
	}
	if err := backend.Atomic(ctx, func(session db.Session) error { return prepared.Save(ctx, session, &candidate) }); err != nil {
		t.Fatal(err)
	}
	stored := read()
	if !reflect.DeepEqual(stored, candidate) {
		t.Fatal("explicit deferred binary Save changed bytes")
	}
	if _, err := models.RecordObjects.Update(ctx, backend, stored, models.RecordPatch{}.WithLabel(current.Label).WithReference(*current.Reference)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read(), current) {
		t.Fatal("deferred binary fixture restore failed")
	}
}
