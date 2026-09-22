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

	"example.com/godj-uuid/models"
	"example.com/godj-uuid/project"
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
	"github.com/progresshans/godj/uuid"
)

//go:embed reference.json
var rawReference []byte

func identifier(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	value, err := uuid.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestUUIDGeneratedDefaults(t *testing.T) {
	sample := identifier(t, "12345678-9abc-4def-8123-456789abcdef")
	if err := (models.RecordCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("missing required UUID accepted")
	}
	// Model/member names cannot shadow the value package or erase literal defaults.
	mutation := models.NewUUIDCreate().WithUUID(uuid.UUID{}).BuildCreate()
	if mutation.Err() != nil {
		t.Fatal(mutation.Err())
	}
	seen := map[string]query.Value{}
	for _, assignment := range mutation.Assignments() {
		seen[assignment.Field().Name()] = assignment.Value()
	}
	if !seen["uuid"].Equal(query.UUID(uuid.UUID{})) || !seen["uuid_value"].Equal(query.UUID(sample)) {
		t.Fatal("UUID import/default collision")
	}
	for _, test := range []struct {
		input             models.RecordCreate
		reference, origin query.Value
	}{
		{models.NewRecordCreate("default", sample), query.Null(), query.UUID(uuid.UUID{})},
		{models.NewRecordCreate("zero", sample).WithReference(uuid.UUID{}).WithOriginNull(), query.UUID(uuid.UUID{}), query.Null()},
		{models.NewRecordCreate("null", sample).WithReferenceNull().WithOrigin(sample), query.Null(), query.UUID(sample)},
	} {
		mutation := test.input.BuildCreate()
		if mutation.Err() != nil {
			t.Fatal(mutation.Err())
		}
		values := map[string]query.Value{}
		for _, assignment := range mutation.Assignments() {
			values[assignment.Field().Name()] = assignment.Value()
		}
		for name, want := range map[string]query.Value{"reference": test.reference, "origin": test.origin, "scheduled": query.UUID(sample)} {
			if got, ok := values[name]; !ok || !got.Equal(want) {
				t.Fatalf("UUID %s default/presence changed", name)
			}
		}
	}
}

type uuidBackend interface {
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

func TestUUIDStorageQueryAndOwnership(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "uuid.sqlite3")) + "?mode=rwc"
		runStorage(t, func(ctx context.Context) (uuidBackend, error) { return sqlite.Open(ctx, dsn) })
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
			t.Fatal("connect UUID consumer PostgreSQL")
		}
		name := fmt.Sprintf("godj_uuid_%d_%d", os.Getpid(), time.Now().UnixNano())
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
		runStorage(t, func(ctx context.Context) (uuidBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: name})
		})
	})
}

type referenceValue struct{ Text, Hex string }
type databaseReference struct {
	AfterAdd           [][]json.RawMessage `json:"after_add"`
	Reopened           [][]json.RawMessage
	BeforeRollback     [][]json.RawMessage `json:"before_rollback"`
	AfterRollback      [][]json.RawMessage `json:"after_rollback"`
	Queries, Relations map[string][]string
	Aggregates         map[string]referenceValue
	AfterRemove        []string `json:"after_remove"`
	Type               string
}

func referenceRows(t *testing.T, input [][]json.RawMessage) [][]string {
	t.Helper()
	rows := make([][]string, len(input))
	for i, row := range input {
		if len(row) < 2 {
			t.Fatal("incomplete UUID reference row")
		}
		var label string
		if err := json.Unmarshal(row[0], &label); err != nil {
			t.Fatal(err)
		}
		text := "<null>"
		if string(row[1]) != "null" {
			var value referenceValue
			if err := json.Unmarshal(row[1], &value); err != nil {
				t.Fatal(err)
			}
			parsed := identifier(t, value.Text)
			if parsed.Hex() != value.Hex {
				t.Fatal("reference canonical UUID disagrees")
			}
			text = value.Text
		}
		rows[i] = []string{label, text}
	}
	return rows
}

func runStorage(t *testing.T, open func(context.Context) (uuidBackend, error)) {
	t.Helper()
	ctx := t.Context()
	sample := identifier(t, "12345678-9abc-4def-8123-456789abcdef")
	maximum := identifier(t, "ffffffff-ffff-ffff-ffff-ffffffffffff")
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
		{App: "uuidref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "uuidref", Model: original}}},
		{App: "uuidref", Name: "0002_reference", Dependencies: []migrations.MigrationKey{{App: "uuidref", Name: "0001_initial"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "uuidref", ModelName: "record", Field: current.Fields[2], BeforeField: "required"},
			migrations.CreateModel{AppLabel: "uuidref", Model: (models.LinkDescriptor{}).Metadata()},
		}},
	} {
		wire, err := definition.Encode(definition.Producer{Name: "uuid-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "uuidref", Name: "0001_initial"}))
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(current.DBTable, []query.Assignment{
		orm.NewAssignment(current.Fields[1], query.String("existing")), orm.NewAssignment(current.Fields[3], query.UUID(uuid.UUID{})), orm.NewAssignment(current.Fields[4], query.UUID(uuid.UUID{})), orm.NewAssignment(current.Fields[5], query.UUID(sample)),
	}, query.NewFieldRef("id", "id", query.FieldInteger, false))); err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, DRF string
		Database    databaseReference
	}
	if err := json.Unmarshal(rawReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" || reference.Database.Type != "char(32)" || len(reference.Database.Queries) != 10 || len(reference.Database.Relations) != 7 {
		t.Fatal("incomplete UUID reference")
	}
	observe := func() [][]string {
		rows, err := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := make([][]string, len(rows))
		for i, row := range rows {
			value := "<null>"
			if row.Reference != nil {
				value = row.Reference.String()
			}
			result[i] = []string{row.Label, value}
		}
		return result
	}
	if !reflect.DeepEqual(observe(), referenceRows(t, reference.Database.AfterAdd)) {
		t.Fatal("UUID add changed existing row")
	}
	for _, input := range []struct{ label, raw string }{
		{"zero", "00000000-0000-0000-0000-000000000000"}, {"one", "00000000-0000-0000-0000-000000000001"}, {"sample", sample.String()},
		{"lower_half", "7fffffff-ffff-ffff-ffff-ffffffffffff"}, {"upper_half", "80000000-0000-0000-0000-000000000000"}, {"maximum", maximum.String()}, {"null", ""},
	} {
		value := uuid.UUID{}
		if input.raw != "" {
			value = identifier(t, input.raw)
		}
		create := models.NewRecordCreate(input.label, value)
		if input.raw != "" {
			create = create.WithReference(value)
		}
		row, err := models.RecordObjects.Create(ctx, backend, create)
		if err != nil {
			t.Fatal(err)
		}
		if row.Origin == nil || *row.Origin != (uuid.UUID{}) || row.Scheduled != sample {
			t.Fatal("UUID defaults changed")
		}
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	backend, err = open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observe(), referenceRows(t, reference.Database.Reopened)) {
		t.Fatal("UUID changed across fresh connection")
	}
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Record]
		not       bool
	}{
		{"exact", "reference", sample, models.RecordFields.Reference.Exact(sample), false},
		{"not_exact", "reference", sample, orm.Not(models.RecordFields.Reference.Exact(sample)), true},
		{"null", "reference__isnull", true, models.RecordFields.Reference.IsNull(true), false},
		{"gt", "reference__gt", sample, models.RecordFields.Reference.GreaterThan(sample), false},
		{"gte", "reference__gte", sample, models.RecordFields.Reference.GreaterThanOrEqual(sample), false},
		{"lt", "reference__lt", sample, models.RecordFields.Reference.LessThan(sample), false},
		{"lte", "reference__lte", sample, models.RecordFields.Reference.LessThanOrEqual(sample), false},
		{"in_null", "reference__in", []any{sample, nil}, models.RecordFields.Reference.In(sample), false},
	} {
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic UUID: %v", err)
		}
		if test.not {
			dynamic[0] = orm.Not(dynamic[0])
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
			if !slices.Equal(labels, reference.Database.Queries[test.name]) {
				t.Fatalf("%s=%v want %v", test.name, labels, reference.Database.Queries[test.name])
			}
		}
	}
	for _, value := range []any{sample.String(), int64(1), sample.Bytes(), []byte(sample.Hex()), []string{sample.String()}} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "reference", Value: value}}); err == nil {
			t.Fatal("dynamic UUID coerced foreign type")
		}
	}
	all := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	*rows[1].Reference, *rows[1].Origin = maximum, maximum
	again, err := all.All(ctx)
	if err != nil || again[1].Reference == nil || *again[1].Reference != (uuid.UUID{}) || *again[1].Origin != (uuid.UUID{}) {
		t.Fatal("UUID pointer escaped query cache")
	}
	projected, err := orm.SelectInto(ctx, all, orm.Project1(models.RecordFields.Reference, func(value *uuid.UUID) *uuid.UUID { return value }))
	if err != nil || len(projected) != 8 || projected[0] != nil || projected[1] == nil || *projected[1] != (uuid.UUID{}) || projected[7] != nil {
		t.Fatal("UUID projection lost presence", err)
	}
	*projected[1] = maximum
	if *again[1].Reference != (uuid.UUID{}) {
		t.Fatal("UUID projection aliases model")
	}
	type bounds struct{ Min, Max orm.Optional[uuid.UUID] }
	for _, source := range []orm.QuerySet[models.Record]{all, all.Distinct()} {
		got, err := orm.AggregateInto(ctx, source, orm.Aggregate2(orm.Min(models.RecordFields.Reference), orm.Max(models.RecordFields.Reference), func(a, b orm.Optional[uuid.UUID]) bounds { return bounds{a, b} }))
		if err != nil {
			t.Fatal(err)
		}
		for name, result := range map[string]orm.Optional[uuid.UUID]{"minimum": got.Min, "maximum": got.Max} {
			if value, ok := result.Get(); !ok || value.String() != reference.Database.Aggregates[name].Text {
				t.Fatal("UUID aggregate changed unsigned order")
			}
		}
	}
	limited, err := all.Limit(3)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := orm.AggregateInto(ctx, limited, orm.Aggregate1(orm.Max(models.RecordFields.Reference), func(v orm.Optional[uuid.UUID]) orm.Optional[uuid.UUID] { return v }))
	if value, ok := bound.Get(); err != nil || !ok || value != identifier(t, "00000000-0000-0000-0000-000000000001") {
		t.Fatal("sliced UUID aggregate escaped source", err)
	}
	for _, source := range []orm.QuerySet[models.Record]{all.Filter(models.RecordFields.ID.Exact(-1)), all.Filter(models.RecordFields.Reference.IsNull(true))} {
		empty, err := orm.AggregateInto(ctx, source, orm.Aggregate1(orm.Max(models.RecordFields.Reference), func(v orm.Optional[uuid.UUID]) orm.Optional[uuid.UUID] { return v }))
		if err != nil || empty.Valid() {
			t.Fatal("empty UUID aggregate invented zero", err)
		}
	}
	equal, err := all.Filter(models.RecordFields.Reference.ExactField(orm.F[models.Record, uuid.UUID](models.RecordFields.Required))).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{}
	for _, row := range equal {
		labels = append(labels, row.Label)
	}
	if !slices.Equal(labels, reference.Database.Queries["field_equal"]) {
		t.Fatal("UUID F comparison changed")
	}
	ordered, err := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.Reference.Asc(), models.RecordFields.ID.Asc()).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	labels = labels[:0]
	for _, row := range ordered {
		labels = append(labels, row.Label)
	}
	wantOrder := slices.Clone(reference.Database.Queries["order"])
	if native {
		wantOrder = append(wantOrder[2:], wantOrder[:2]...)
	} // Preserve the backend's native NULL ordering.
	if !slices.Equal(labels, wantOrder) {
		t.Fatal("UUID ordering changed", labels)
	}
	verifyRelations(t, backend, again, sample, maximum, reference.Database.Relations)
	beforeRollback := observe()
	if !reflect.DeepEqual(beforeRollback, referenceRows(t, reference.Database.BeforeRollback)) {
		t.Fatal("UUID rollback fixture changed")
	}
	rollback := errors.New("UUID transaction rollback")
	err = backend.Atomic(ctx, func(session db.Session) error {
		if _, err := models.RecordObjects.Update(ctx, session, again[3], models.RecordPatch{}.WithReference(maximum)); err != nil {
			return err
		}
		stored, found, err := models.RecordObjects.Using(session).Filter(models.RecordFields.ID.Exact(again[3].ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
		if err != nil || !found || stored.Reference == nil || *stored.Reference != maximum {
			return errors.New("transaction UUID read changed")
		}
		return rollback
	})
	if !errors.Is(err, rollback) || !reflect.DeepEqual(observe(), referenceRows(t, reference.Database.AfterRollback)) {
		t.Fatal("UUID rollback changed stored values", err)
	}
	changed, err := models.RecordObjects.Update(ctx, backend, again[3], models.RecordPatch{}.WithReferenceNull())
	if err != nil || changed.Reference != nil || again[3].Reference == nil || *again[3].Reference != sample {
		t.Fatal("UUID null patch mutated caller", err)
	}
	changed.Reference = &maximum
	before := (models.RecordDescriptor{}).CloneWriteModel(changed)
	mutator := &countedMutator{Mutator: backend, fail: true}
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Reference)); err == nil || !reflect.DeepEqual(before, changed) {
		t.Fatal("failed UUID Save mutated caller")
	}
	mutator.fail = false
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Reference)); err != nil {
		t.Fatal(err)
	}
	changed.Reference = nil
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Label)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := all.Filter(models.RecordFields.ID.Exact(changed.ID)).First(ctx)
	if err != nil || !found || stored.Reference == nil || *stored.Reference != maximum {
		t.Fatal("UUID Save mask erased unselected field")
	}
	writes := mutator.writes
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := models.RecordObjects.Create(canceled, mutator, models.NewRecordCreate("canceled", sample)); !errors.Is(err, context.Canceled) || mutator.writes != writes {
		t.Fatal("canceled UUID create reached I/O")
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	label := query.NewFieldRef("label", "label", query.FieldString, false)
	result, err := backend.Query(ctx, query.NewPlan(current.DBTable, []query.FieldRef{id, label}).WithOrderings(query.NewOrdering(id, query.Ascending)))
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	labels = labels[:0]
	for result.Next() {
		var id int64
		var name string
		if err := result.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		labels = append(labels, name)
	}
	if result.Err() != nil || !slices.Equal(labels, reference.Database.AfterRemove) {
		t.Fatal("UUID reverse migration changed rows")
	}
	if err := result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	for _, row := range observe() {
		if row[1] != "<null>" {
			t.Fatal("UUID re-add invented removed values")
		}
	}
}

func verifyRelations(t *testing.T, backend uuidBackend, records []models.Record, sample, maximum uuid.UUID, reference map[string][]string) {
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
	link, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "uuidref", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	field := related.ModelsLink.Record.Reference
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Link]
	}{
		{"exact", "record__reference", sample, field.Exact(sample)}, {"null", "record__reference__isnull", true, field.IsNull(true)},
		{"gt", "record__reference__gt", sample, field.GreaterThan(sample)}, {"gte", "record__reference__gte", sample, field.GreaterThanOrEqual(sample)},
		{"lt", "record__reference__lt", sample, field.LessThan(sample)}, {"lte", "record__reference__lte", sample, field.LessThanOrEqual(sample)},
		{"in_null", "record__reference__in", []any{sample, nil}, field.In(sample)},
	} {
		dynamic, err := orm.ParseDynamicRelations(link, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatal("dynamic UUID relation", err)
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
					t.Fatal("eager UUID owner presence changed")
				}
				if present && record.Label == "sample" {
					if record.Reference == nil || *record.Reference != sample {
						t.Fatal("eager UUID value changed")
					}
					snapshot, err := record.Unwrap()
					if err != nil {
						t.Fatal(err)
					}
					*snapshot.Reference = maximum
					again, _, err := row.Record(ctx)
					if err != nil || again != record || again.Reference == nil || *again.Reference != sample {
						t.Fatal("eager UUID snapshot aliases cache")
					}
				}
			}
			if !slices.Equal(labels, reference[test.name]) {
				t.Fatalf("related %s=%v want %v", test.name, labels, reference[test.name])
			}
		}
	}
	reverse, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	if count, err := models.RecordObjects.Using(backend).Filter(reverse.ModelsRecord.Links.Token.Exact(sample)).Count(ctx); err != nil || count != 7 {
		t.Fatal("reverse UUID scalar query changed", count, err)
	}
}
