package consumer_test

import (
	"context"
	_ "embed"
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

	"example.com/godj-nullable-boolean/models"
	"example.com/godj-nullable-boolean/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed reference.json
var rawReference []byte

var (
	_ orm.WritableField[models.Record]      = models.RecordFields.Flag
	_ orm.BooleanLookupField[models.Record] = models.RecordFields.Flag
)

func TestNullableBooleanGeneratedDefaults(t *testing.T) {
	for _, test := range []struct {
		input         models.RecordCreate
		flag, no, yes query.Value
	}{
		{models.NewRecordCreate("default"), query.Null(), query.Boolean(false), query.Boolean(true)},
		{models.NewRecordCreate("explicit").WithFlag(false).WithFallbackFalseNull().WithFallbackTrue(false), query.Boolean(false), query.Null(), query.Boolean(false)},
		{models.NewRecordCreate("null").WithFlagNull().WithFallbackFalse(true).WithFallbackTrueNull(), query.Null(), query.Boolean(true), query.Null()},
	} {
		mutation := test.input.BuildCreate()
		if err := mutation.Err(); err != nil {
			t.Fatal(err)
		}
		values := map[string]query.Value{}
		for _, assignment := range mutation.Assignments() {
			values[assignment.Field().Name()] = assignment.Value()
		}
		for name, expected := range map[string]query.Value{"flag": test.flag, "fallback_false": test.no, "fallback_true": test.yes} {
			actual, found := values[name]
			if !found || !actual.Equal(expected) {
				t.Fatalf("generated %s changed presence/default", name)
			}
		}
	}
	if _, writable := any(models.RecordFields.ID).(orm.WritableField[models.Record]); writable {
		t.Fatal("primary key became writable")
	}
}

type countedMutator struct {
	db.Mutator
	writes int
	fail   bool
}

func (b *countedMutator) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	b.writes++
	if b.fail {
		return 0, errors.New("injected insert failure")
	}
	return b.Mutator.Insert(ctx, plan)
}
func (b *countedMutator) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	b.writes++
	if b.fail {
		return 0, errors.New("injected update failure")
	}
	return b.Mutator.Update(ctx, plan)
}

type nullableBackend interface {
	db.Queryer
	db.Mutator
	migrationbackend.RevisionFencedBackend
	Close() error
}

func TestNullableBooleanStorageQueryAndOwnership(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "nullable.sqlite3")) + "?mode=rwc"
		runNullableBooleanStorage(t, func(ctx context.Context) (nullableBackend, error) { return sqlite.Open(ctx, dsn) })
	})
	databaseURL := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL connection is absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), databaseURL)
		if err != nil {
			t.Fatal("connect generated PostgreSQL consumer")
		}
		name := fmt.Sprintf("godj_nullable_bool_%d_%d", os.Getpid(), time.Now().UnixNano())
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
		runNullableBooleanStorage(t, func(ctx context.Context) (nullableBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: name})
		})
	})
}

func runNullableBooleanStorage(t *testing.T, open func(context.Context) (nullableBackend, error)) {
	t.Helper()
	ctx := t.Context()
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
	current := (models.RecordDescriptor{}).Metadata()
	original := current
	original.Fields = slices.Delete(slices.Clone(current.Fields), 2, 3)
	var sources []definition.Source
	for _, migration := range []migrations.Migration{
		{App: "nullablebool", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "nullablebool", Model: original}}},
		{App: "nullablebool", Name: "0002_flag", Dependencies: []migrations.MigrationKey{{App: "nullablebool", Name: "0001_initial"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "nullablebool", ModelName: "record", Field: current.Fields[2], BeforeField: "fallback_false"},
			migrations.CreateModel{AppLabel: "nullablebool", Model: (models.LinkDescriptor{}).Metadata()},
		}},
	} {
		wire, err := definition.Encode(definition.Producer{Name: "nullable-boolean-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	initialTarget := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "nullablebool", Name: "0001_initial"}))
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initialTarget); err != nil {
		t.Fatal(err)
	}
	_, err = backend.Insert(ctx, query.NewInsertPlanReturningKey(current.DBTable, []query.Assignment{
		orm.NewAssignment(current.Fields[1], query.String("existing")), orm.NewAssignment(current.Fields[3], query.Boolean(false)), orm.NewAssignment(current.Fields[4], query.Boolean(true)),
	}, query.NewFieldRef("id", "id", query.FieldInteger, false)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, DRF string
		Database    struct {
			AfterAdd    [][]any `json:"after_add"`
			Reopened    [][]any
			Queries     map[string][]string
			Relations   map[string][]string
			AfterUpdate [][]any  `json:"after_update"`
			AfterRemove []string `json:"after_remove"`
		}
	}
	if err := json.Unmarshal(rawReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" || len(reference.Database.Queries) != 6 || len(reference.Database.Relations) != 6 {
		t.Fatal("incomplete independent reference")
	}
	observe := func() [][]any {
		rows, err := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := make([][]any, len(rows))
		for index, row := range rows {
			var value any
			if row.Flag != nil {
				value = *row.Flag
			}
			result[index] = []any{row.Label, value}
		}
		return result
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterAdd) {
		t.Fatal("nonempty migration invented false")
	}
	for _, input := range []models.RecordCreate{models.NewRecordCreate("false").WithFlag(false), models.NewRecordCreate("true").WithFlag(true), models.NewRecordCreate("null").WithFlagNull()} {
		row, err := models.RecordObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		if row.FallbackFalse == nil || *row.FallbackFalse || row.FallbackTrue == nil || !*row.FallbackTrue {
			t.Fatal("stored defaults differ")
		}
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend, err = open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observe(), reference.Database.Reopened) {
		t.Fatal("fresh connection lost Boolean presence")
	}
	for name, predicate := range map[string]orm.Predicate[models.Record]{
		"false": models.RecordFields.Flag.Exact(false), "true": models.RecordFields.Flag.Exact(true), "null": models.RecordFields.Flag.IsNull(true),
		"not_false": orm.Not(models.RecordFields.Flag.Exact(false)), "in_false_null": models.RecordFields.Flag.In(false),
		"not_in_false_null": orm.And(orm.Not(models.RecordFields.Flag.In(false)), models.RecordFields.Flag.IsNull(false)),
	} {
		key, value, negated := "flag", any(false), false
		switch name {
		case "true":
			value = true
		case "null":
			key, value = "flag__isnull", true
		case "not_false":
			negated = true
		case "in_false_null":
			key, value = "flag__in", []any{false, nil}
		case "not_in_false_null":
			key, value, negated = "flag__in", []any{false, nil}, true
		}
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: key, Value: value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic Boolean: %v", err)
		}
		if negated {
			dynamic[0] = orm.Not(dynamic[0])
		}
		for _, filter := range []orm.Predicate[models.Record]{predicate, dynamic[0]} {
			rows, err := models.RecordObjects.Using(backend).Filter(filter).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			labels := make([]string, len(rows))
			for index, row := range rows {
				labels[index] = row.Label
			}
			if !slices.Equal(labels, reference.Database.Queries[name]) {
				t.Fatalf("%s query = %v, want %v", name, labels, reference.Database.Queries[name])
			}
		}
	}
	for _, value := range []any{0, "false", []string{"true"}} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "flag", Value: value}}); err == nil {
			t.Fatal("dynamic Boolean coerced another scalar type")
		}
	}
	all := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	*rows[1].Flag = true
	*rows[1].FallbackFalse = true
	again, err := all.All(ctx)
	if err != nil || again[1].Flag == nil || *again[1].Flag || *again[1].FallbackFalse {
		t.Fatal("caller Boolean pointer mutated query cache")
	}
	projected, err := orm.SelectInto(ctx, all, orm.Project1(models.RecordFields.Flag, func(value *bool) *bool { return value }))
	if err != nil || len(projected) != 4 || projected[0] != nil || projected[1] == nil || *projected[1] || projected[2] == nil || !*projected[2] || projected[3] != nil {
		t.Fatal("nullable scalar projection lost false or null")
	}
	*projected[1] = true
	if *again[1].Flag {
		t.Fatal("projection aliases a model Boolean")
	}
	verifyNullableBooleanRelations(t, backend, again, reference.Database.Relations)
	falseRow, err := models.RecordObjects.Update(ctx, backend, again[1], models.RecordPatch{}.WithFlagNull())
	if err != nil || falseRow.Flag != nil || again[1].Flag == nil || *again[1].Flag {
		t.Fatal("patch mutated caller or lost null")
	}
	nullRow, err := models.RecordObjects.Update(ctx, backend, again[3], models.RecordPatch{}.WithFlag(false))
	if err != nil || nullRow.Flag == nil || *nullRow.Flag {
		t.Fatal("false patch became null")
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterUpdate) {
		t.Fatal("updates differ from Django")
	}
	nullRow.Flag = new(true)
	mutator := &countedMutator{Mutator: backend, fail: true}
	before := (models.RecordDescriptor{}).CloneWriteModel(nullRow)
	if err := models.RecordObjects.Save(ctx, mutator, &nullRow, models.RecordUpdateFields(models.RecordFields.Flag)); err == nil || !reflect.DeepEqual(before, nullRow) {
		t.Fatal("failed Save changed the caller")
	}
	mutator.fail = false
	if err := models.RecordObjects.Save(ctx, mutator, &nullRow, models.RecordUpdateFields(models.RecordFields.Flag)); err != nil {
		t.Fatal(err)
	}
	nullRow.Flag = nil
	if err := models.RecordObjects.Save(ctx, mutator, &nullRow, models.RecordUpdateFields(models.RecordFields.Label)); err != nil {
		t.Fatal(err)
	}
	stored, _, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.Exact(nullRow.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
	if err != nil || stored.Flag == nil || !*stored.Flag {
		t.Fatal("Save mask cleared an omitted Boolean")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	writes := mutator.writes
	if _, err := models.RecordObjects.Create(canceled, mutator, models.NewRecordCreate("canceled")); !errors.Is(err, context.Canceled) || mutator.writes != writes {
		t.Fatal("canceled create reached I/O")
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initialTarget); err != nil {
		t.Fatal(err)
	}
	label := query.NewFieldRef("label", "label", query.FieldString, false)
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan := query.NewPlan(current.DBTable, []query.FieldRef{id, label}).WithOrderings(query.NewOrdering(id, query.Ascending))
	result, err := backend.Query(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	labels := []string{}
	for result.Next() {
		var id int64
		var text string
		if err := result.Scan(&id, &text); err != nil {
			t.Fatal(err)
		}
		labels = append(labels, text)
	}
	if result.Err() != nil || !slices.Equal(labels, reference.Database.AfterRemove) {
		t.Fatal("reverse nullable migration changed existing rows")
	}
	if err := result.Close(); err != nil {
		t.Fatal(err)
	}
}

func verifyNullableBooleanRelations(t *testing.T, backend nullableBackend, records []models.Record, reference map[string][]string) {
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
	related, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	bound, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	link, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "nullablebool", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		typed   orm.Predicate[models.Link]
		key     string
		value   any
		negated bool
	}{
		{"false", related.ModelsLink.Record.Flag.Exact(false), "record__flag", false, false},
		{"true", related.ModelsLink.Record.Flag.Exact(true), "record__flag", true, false},
		{"null", related.ModelsLink.Record.Flag.IsNull(true), "record__flag__isnull", true, false},
		{"not_false", orm.Not(related.ModelsLink.Record.Flag.Exact(false)), "record__flag", false, true},
		{"in_false_null", related.ModelsLink.Record.Flag.In(false), "record__flag__in", []any{false, nil}, false},
		{"not_in_false_null", orm.And(orm.Not(related.ModelsLink.Record.Flag.In(false)), related.ModelsLink.Record.Flag.IsNull(false)), "record__flag__in", []any{false, nil}, true},
	} {
		dynamic, err := orm.ParseDynamicRelations(link, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic Boolean relation: %v", err)
		}
		if test.negated {
			dynamic[0] = orm.Not(dynamic[0])
		}
		for _, filter := range []orm.Predicate[models.Link]{test.typed, dynamic[0]} {
			rows, err := facade.ModelsLink.Filter(filter).OrderBy(models.LinkFields.ID.Asc()).SelectRelated(facade.ModelsLink.Related.Record).All(ctx)
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
					t.Fatal("eager Boolean owner presence differs")
				}
				if present && record.Label == "false" {
					if record.Flag == nil || *record.Flag {
						t.Fatal("eager nullable target lost false")
					}
					// Related facade objects preserve pointer identity. Unwrap is
					// the detached model snapshot boundary for caller-owned edits.
					snapshot, err := record.Unwrap()
					if err != nil {
						t.Fatal(err)
					}
					*snapshot.Flag = true
					again, _, err := row.Record(ctx)
					if err != nil || again != record || again.Flag == nil || *again.Flag {
						t.Fatal("eager target Boolean aliases cache")
					}
				}
			}
			if !slices.Equal(labels, reference[test.name]) {
				t.Fatalf("related nullable Boolean = %v, want %v", labels, reference[test.name])
			}
		}
	}
}
