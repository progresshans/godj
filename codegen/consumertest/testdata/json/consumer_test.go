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

	"example.com/godj-json/models"
	"example.com/godj-json/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed reference.json
var rawReference []byte

func document(t *testing.T, raw string) jsonvalue.Value {
	t.Helper()
	value, err := jsonvalue.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestJSONGeneratedDefaults(t *testing.T) {
	if err := (models.RecordCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("missing required JSON accepted")
	}
	sample := document(t, `{"a":1,"b":2}`)
	mutation := models.NewJSONCreate().WithJSON(jsonvalue.Null()).BuildCreate()
	if mutation.Err() != nil {
		t.Fatal(mutation.Err())
	}
	seen := map[string]query.Value{}
	for _, assignment := range mutation.Assignments() {
		seen[assignment.Field().Name()] = assignment.Value()
	}
	if !seen["json"].Equal(query.JSON(jsonvalue.Null())) || !seen["json_value"].Equal(query.JSON(sample)) {
		t.Fatal("JSON import/default name collision")
	}
	for _, test := range []struct {
		input           models.RecordCreate
		payload, origin query.Value
	}{
		{models.NewRecordCreate("default", sample), query.Null(), query.JSON(jsonvalue.Null())},
		{models.NewRecordCreate("json_null", sample).WithPayload(jsonvalue.Null()).WithOriginNull(), query.JSON(jsonvalue.Null()), query.Null()},
		{models.NewRecordCreate("sql_null", sample).WithPayloadNull().WithOrigin(sample), query.Null(), query.JSON(sample)},
	} {
		mutation := test.input.BuildCreate()
		if mutation.Err() != nil {
			t.Fatal(mutation.Err())
		}
		values := map[string]query.Value{}
		for _, assignment := range mutation.Assignments() {
			values[assignment.Field().Name()] = assignment.Value()
		}
		for name, want := range map[string]query.Value{"payload": test.payload, "origin": test.origin, "scheduled": query.JSON(sample)} {
			if got, ok := values[name]; !ok || !got.Equal(want) {
				t.Fatalf("JSON %s default/presence changed", name)
			}
		}
	}
}

type jsonBackend interface {
	db.Queryer
	db.Mutator
	db.Atomic
	migrationbackend.RevisionFencedBackend
	Close() error
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

func TestJSONStorageQueryAndOwnership(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "json.sqlite3")) + "?mode=rwc"
		runStorage(t, func(ctx context.Context) (jsonBackend, error) { return sqlite.Open(ctx, dsn) })
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
			t.Fatal("connect JSON consumer PostgreSQL")
		}
		name := fmt.Sprintf("godj_json_%d_%d", os.Getpid(), time.Now().UnixNano())
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
		runStorage(t, func(ctx context.Context) (jsonBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: name})
		})
	})
}

func runStorage(t *testing.T, open func(context.Context) (jsonBackend, error)) {
	t.Helper()
	ctx := t.Context()
	sample := document(t, `{"a":1,"b":2}`)
	changedValue := document(t, `{"changed":[340282366920938463463374607431768211455,null]}`)
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
	_, native := backend.(*postgres.Backend)
	current := (models.RecordDescriptor{}).Metadata()
	original := current.Clone()
	original.Fields = slices.Delete(original.Fields, 2, 3)
	var sources []definition.Source
	for _, migration := range []migrations.Migration{
		{App: "jsonref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "jsonref", Model: original}}},
		{App: "jsonref", Name: "0002_payload", Dependencies: []migrations.MigrationKey{{App: "jsonref", Name: "0001_initial"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "jsonref", ModelName: "record", Field: current.Fields[2], BeforeField: "required"},
			migrations.CreateModel{AppLabel: "jsonref", Model: (models.LinkDescriptor{}).Metadata()},
			migrations.CreateModel{AppLabel: "jsonref", Model: (models.DocumentDescriptor{}).Metadata()},
			migrations.CreateModel{AppLabel: "jsonref", Model: (models.ShelfDescriptor{}).Metadata()},
			migrations.CreateModel{AppLabel: "jsonref", Model: (models.EntryDescriptor{}).Metadata()},
		}},
	} {
		wire, err := definition.Encode(definition.Producer{Name: "json-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "jsonref", Name: "0001_initial"}))
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(current.DBTable, []query.Assignment{
		orm.NewAssignment(current.Fields[1], query.String("existing")), orm.NewAssignment(current.Fields[3], query.JSON(jsonvalue.Null())),
		orm.NewAssignment(current.Fields[4], query.JSON(jsonvalue.Null())), orm.NewAssignment(current.Fields[5], query.JSON(sample)),
	}, id)); err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, DRF string
		Database    struct {
			Type        string
			Physical    [][]json.RawMessage
			Queries     map[string]json.RawMessage
			AfterRemove []string `json:"after_remove"`
		}
	}
	if err := json.Unmarshal(rawReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" || reference.Database.Type != "text" || len(reference.Database.Physical) != 12 || len(reference.Database.Queries) != 10 {
		t.Fatal("incomplete JSON independent reference")
	}
	wantRows := make([][2]string, len(reference.Database.Physical))
	for index, observed := range reference.Database.Physical {
		if len(observed) != 4 {
			t.Fatal("incomplete JSON physical row")
		}
		var label string
		var raw *string
		if err := json.Unmarshal(observed[0], &label); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(observed[1], &raw); err != nil {
			t.Fatal(err)
		}
		value := jsonvalue.Null()
		wantRows[index] = [2]string{label, "<sql-null>"}
		if raw != nil {
			value = document(t, *raw)
			wantRows[index][1] = value.Text
		}
		if index == 0 {
			continue
		}
		create := models.NewRecordCreate(label, value)
		if raw != nil {
			create = create.WithPayload(value)
		}
		row, err := models.RecordObjects.Create(ctx, backend, create)
		if err != nil {
			t.Fatal(err)
		}
		if row.Origin == nil || *row.Origin != jsonvalue.Null() || row.Scheduled != sample {
			t.Fatal("JSON defaults lost presence")
		}
	}
	observe := func() [][2]string {
		rows, err := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := make([][2]string, len(rows))
		for i, row := range rows {
			result[i] = [2]string{row.Label, "<sql-null>"}
			if row.Payload != nil {
				result[i][1] = row.Payload.Text
			}
		}
		return result
	}
	if !slices.Equal(observe(), wantRows) {
		t.Fatal("JSON add/storage disagrees with explicit SQL and JSON null or precise reference values")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend, err = open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(observe(), wantRows) {
		t.Fatal("JSON changed after reopening")
	}
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Record]
	}{
		{"json_null", "payload", jsonvalue.Null(), models.RecordFields.Payload.Exact(jsonvalue.Null())},
		{"sql_null", "payload__isnull", true, models.RecordFields.Payload.IsNull(true)},
		{"not_sql_null", "payload__isnull", false, models.RecordFields.Payload.IsNull(false)},
		{"false", "payload", document(t, `false`), models.RecordFields.Payload.Exact(document(t, `false`))},
		{"zero", "payload", document(t, `0`), models.RecordFields.Payload.Exact(document(t, `0`))},
		{"one", "payload", document(t, `1`), models.RecordFields.Payload.Exact(document(t, `1`))},
		{"float", "payload", document(t, `1.0`), models.RecordFields.Payload.Exact(document(t, `1.0`))},
		{"object", "payload", sample, models.RecordFields.Payload.Exact(sample)},
	} {
		var want []string
		if err := json.Unmarshal(reference.Database.Queries[test.name], &want); err != nil {
			t.Fatal(err)
		}
		// GoDj canonicalizes object keys before SQLite storage. PostgreSQL's
		// numeric equality additionally treats 1 and 1.0 as equal.
		if test.name == "object" {
			want = []string{"object", "reordered"}
		}
		if native && (test.name == "one" || test.name == "float") {
			want = []string{"one", "float"}
		}
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatal("dynamic JSON", err)
		}
		for _, predicate := range []orm.Predicate[models.Record]{test.typed, dynamic[0]} {
			rows, err := models.RecordObjects.Using(backend).Filter(predicate).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			labels := []string{}
			for _, row := range rows {
				labels = append(labels, row.Label)
			}
			if !slices.Equal(labels, want) {
				t.Fatalf("JSON %s=%v want %v", test.name, labels, want)
			}
		}
	}
	for _, input := range []orm.LookupInput{{Key: "payload", Value: nil}, {Key: "payload", Value: "null"}, {Key: "payload", Value: map[string]any{"a": 1}}, {Key: "payload", Value: jsonvalue.Value{}}, {Key: "payload__range", Value: sample}, {Key: "payload__icontains", Value: "a"}} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{input}); err == nil {
			t.Fatal("JSON dynamic lookup coerced an unsupported value or operation")
		}
	}
	dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "payload__in", Value: []any{jsonvalue.Null(), document(t, `false`), nil}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, predicate := range []orm.Predicate[models.Record]{dynamic[0], models.RecordFields.Payload.In(jsonvalue.Null(), document(t, `false`))} {
		rows, err := models.RecordObjects.Using(backend).Filter(predicate).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
		if err != nil || len(rows) != 2 || rows[0].Label != "json_null" || rows[1].Label != "false" {
			t.Fatal("JSON IN confused SQL NULL and JSON null", err)
		}
	}
	all := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	*rows[2].Payload = changedValue
	again, err := all.All(ctx)
	if err != nil || again[2].Payload == nil || *again[2].Payload != jsonvalue.Null() {
		t.Fatal("JSON pointer aliases cached models", err)
	}
	projected, err := orm.SelectInto(ctx, all, orm.Project1(models.RecordFields.Payload, func(value *jsonvalue.Value) *jsonvalue.Value { return value }))
	if err != nil || len(projected) != 12 || projected[0] != nil || projected[2] == nil || *projected[2] != jsonvalue.Null() || projected[11].Text != "340282366920938463463374607431768211455" {
		t.Fatal("JSON projection lost null or precision", err)
	}
	*projected[2] = changedValue
	if *again[2].Payload != jsonvalue.Null() {
		t.Fatal("JSON projection aliases model")
	}
	equal, err := all.Filter(models.RecordFields.Payload.ExactField(orm.F[models.Record, jsonvalue.Value](models.RecordFields.Required))).All(ctx)
	if err != nil || len(equal) != 10 {
		t.Fatal("JSON F equality changed null semantics", err)
	}
	verifyRelations(t, backend, again, sample, changedValue)
	rollback := errors.New("JSON rollback")
	err = backend.Atomic(ctx, func(session db.Session) error {
		if _, err := models.RecordObjects.Update(ctx, session, again[2], models.RecordPatch{}.WithPayload(changedValue)); err != nil {
			return err
		}
		stored, found, err := models.RecordObjects.Using(session).Filter(models.RecordFields.ID.Exact(again[2].ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
		if err != nil || !found || stored.Payload == nil || *stored.Payload != changedValue {
			return errors.New("JSON transaction read changed")
		}
		return rollback
	})
	if !errors.Is(err, rollback) || !slices.Equal(observe(), wantRows) {
		t.Fatal("JSON rollback lost data", err)
	}
	changed, err := models.RecordObjects.Update(ctx, backend, again[2], models.RecordPatch{}.WithPayloadNull())
	if err != nil || changed.Payload != nil || again[2].Payload == nil || *again[2].Payload != jsonvalue.Null() {
		t.Fatal("JSON null patch mutated input", err)
	}
	changed.Payload = &changedValue
	before := (models.RecordDescriptor{}).CloneWriteModel(changed)
	mutator := &countedMutator{Mutator: backend, fail: true}
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Payload)); err == nil || !reflect.DeepEqual(changed, before) {
		t.Fatal("failed JSON Save changed caller")
	}
	mutator.fail = false
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Payload)); err != nil {
		t.Fatal(err)
	}
	changed.Payload = nil
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Label)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := all.Filter(models.RecordFields.ID.Exact(changed.ID)).First(ctx)
	if err != nil || !found || stored.Payload == nil || *stored.Payload != changedValue {
		t.Fatal("JSON save mask erased omitted field", err)
	}
	writes := mutator.writes
	if _, err := models.RecordObjects.Create(ctx, mutator, models.NewRecordCreate("invalid", jsonvalue.Value{})); err == nil || mutator.writes != writes {
		t.Fatal("invalid JSON reached I/O")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := models.RecordObjects.Create(canceled, mutator, models.NewRecordCreate("canceled", sample)); !errors.Is(err, context.Canceled) || mutator.writes != writes {
		t.Fatal("canceled JSON create reached I/O", err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	label := query.NewFieldRef("label", "label", query.FieldString, false)
	result, err := backend.Query(ctx, query.NewPlan(current.DBTable, []query.FieldRef{id, label}).WithOrderings(query.NewOrdering(id, query.Ascending)))
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{}
	for result.Next() {
		var key int64
		var name string
		if err := result.Scan(&key, &name); err != nil {
			t.Fatal(err)
		}
		labels = append(labels, name)
	}
	if result.Err() != nil || !slices.Equal(labels, reference.Database.AfterRemove) {
		t.Fatal("JSON reverse migration changed rows")
	}
	if err := result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	for _, row := range observe() {
		if row[1] != "<sql-null>" {
			t.Fatal("re-added JSON field invented lost values")
		}
	}
	verifyNativeLimits(t, backend, native)
	t.Run("paths", func(t *testing.T) { verifyJSONPaths(t, backend, native) })
	t.Run("containment", func(t *testing.T) { verifyJSONContainment(t, backend, native) })
	t.Run("keys", func(t *testing.T) { verifyJSONKeys(t, backend, native) })
	t.Run("projection", func(t *testing.T) { verifyJSONProjection(t, backend, native) })
	t.Run("related_projection", func(t *testing.T) { verifyRelatedProjection(t, backend, native) })
	t.Run("forward_projection", func(t *testing.T) { verifyForwardJSONProjection(t, backend, native) })
	t.Run("comparison", func(t *testing.T) { verifyJSONComparisons(t, backend, native) })
}

func verifyRelations(t *testing.T, backend jsonBackend, records []models.Record, sample, changed jsonvalue.Value) {
	t.Helper()
	ctx := t.Context()
	for _, record := range records[1:] {
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
	link, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "jsonref", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	field := related.ModelsLink.Record.Payload
	for _, test := range []struct {
		key   string
		value any
		typed orm.Predicate[models.Link]
		want  []string
	}{
		{"record__payload", jsonvalue.Null(), field.Exact(jsonvalue.Null()), []string{"json_null"}},
		{"record__payload", sample, field.Exact(sample), []string{"object", "reordered"}},
		{"record__payload__isnull", true, field.IsNull(true), []string{"sql_null", "missing"}},
		{"record__payload__in", []any{sample, nil}, field.In(sample), []string{"object", "reordered"}},
	} {
		dynamic, err := orm.ParseDynamicRelations(link, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatal("dynamic JSON relation", err)
		}
		for _, predicate := range []orm.Predicate[models.Link]{test.typed, dynamic[0]} {
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
					t.Fatal("joined JSON owner presence changed", err)
				}
				if present && record.Label == "object" {
					if record.Payload == nil || *record.Payload != sample {
						t.Fatal("joined JSON changed")
					}
					snapshot, err := record.Unwrap()
					if err != nil {
						t.Fatal(err)
					}
					*snapshot.Payload = changed
					again, _, err := row.Record(ctx)
					if err != nil || again != record || again.Payload == nil || *again.Payload != sample {
						t.Fatal("joined JSON snapshot aliases cache")
					}
				}
			}
			if !slices.Equal(labels, test.want) {
				t.Fatalf("JSON relation=%v want %v", labels, test.want)
			}
		}
	}
	reverse, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	if count, err := models.RecordObjects.Using(backend).Filter(reverse.ModelsRecord.Links.Token.Exact(sample)).Count(ctx); err != nil || count != 11 {
		t.Fatal("reverse JSON query changed", count, err)
	}
}

func verifyNativeLimits(t *testing.T, backend jsonBackend, native bool) {
	t.Helper()
	ctx := t.Context()
	for _, test := range []struct{ raw, nativeText string }{
		{`1e400`, "1" + strings.Repeat("0", 400)}, {`1e4095`, "1" + strings.Repeat("0", 4095)},
		{`1e-4094`, "0." + strings.Repeat("0", 4093) + "1"}, {`-0.0`, `0.0`}, {`1.2300e2`, `123.00`},
	} {
		value := document(t, test.raw)
		row, err := models.RecordObjects.Create(ctx, backend, models.NewRecordCreate("numeric", value).WithPayload(value))
		if err != nil {
			t.Fatal(err)
		}
		stored, found, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.Exact(row.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
		want := value.Text
		if native {
			want = test.nativeText
		}
		if err != nil || !found || stored.Required.Text != want || stored.Payload == nil || stored.Payload.Text != want {
			t.Fatal("native JSON numeric expansion lost precision/readability", err)
		}
	}
	before, err := models.RecordObjects.Using(backend).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`1e4096`, `1e-4095`, `"\u0000"`, `{"\u0000":1}`} {
		value := document(t, raw)
		_, err := models.RecordObjects.Create(ctx, backend, models.NewRecordCreate("boundary", value))
		if native && !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
			t.Fatal("unreadable native JSON write accepted", err)
		}
		if !native && err != nil {
			t.Fatal("SQLite inherited native JSONB limitations", err)
		}
	}
	if native {
		after, err := models.RecordObjects.Using(backend).Count(ctx)
		if err != nil || after != before {
			t.Fatal("failed native JSON writes changed rows", err)
		}
	}
}
