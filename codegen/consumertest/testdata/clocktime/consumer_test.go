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

	"example.com/godj-clock-time/models"
	"example.com/godj-clock-time/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/clock"
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
var minimum = clock.Time{}
var fraction = clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: 123456}
var maximum = clock.Time{Hour: 23, Minute: 59, Second: 59, Microsecond: 999999}

func TestClockTimeGeneratedDefaults(t *testing.T) {
	// A model and member named Time cannot shadow generated package imports.
	if mutation := models.NewTimeCreate().WithTime(clock.Time{}).BuildCreate(); mutation.Err() != nil {
		t.Fatal(mutation.Err())
	}

	if err := (models.RecordCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("missing required time accepted")
	}
	for _, test := range []struct {
		input       models.RecordCreate
		day, origin query.Value
	}{
		{models.NewRecordCreate("default", minimum), query.Null(), query.Time(minimum)},
		{models.NewRecordCreate("explicit", maximum).WithAt(fraction).WithOriginNull(), query.Time(fraction), query.Null()},
		{models.NewRecordCreate("null", fraction).WithAtNull().WithOrigin(maximum), query.Null(), query.Time(maximum)},
	} {
		mutation := test.input.BuildCreate()
		if err := mutation.Err(); err != nil {
			t.Fatal(err)
		}
		values := map[string]query.Value{}
		for _, assignment := range mutation.Assignments() {
			values[assignment.Field().Name()] = assignment.Value()
		}
		for name, want := range map[string]query.Value{"at": test.day, "origin": test.origin, "scheduled": query.Time(fraction)} {
			if got, ok := values[name]; !ok || !got.Equal(want) {
				t.Fatalf("%s default/presence changed", name)
			}
		}
	}
	for _, invalid := range []clock.Time{{Minute: -1}, {Second: 60}, {Hour: 24}} {
		if models.NewRecordCreate("bad", invalid).BuildCreate().Err() == nil || models.NewRecordCreate("bad", minimum).WithAt(invalid).BuildCreate().Err() == nil || (models.RecordPatch{}).WithAt(invalid).BuildPatch(models.Record{}).Err() == nil {
			t.Fatal("invalid time reached a valid mutation")
		}
	}
}

type dateBackend interface {
	db.Queryer
	db.Mutator
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

func TestClockTimeStorageQueryAndOwnership(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "clock.sqlite3")) + "?mode=rwc"
		runStorage(t, func(ctx context.Context) (dateBackend, error) { return sqlite.Open(ctx, dsn) })
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
			t.Fatal("connect time consumer PostgreSQL")
		}
		name := fmt.Sprintf("godj_clock_time_%d_%d", os.Getpid(), time.Now().UnixNano())
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
		runStorage(t, func(ctx context.Context) (dateBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: name})
		})
	})
}

func runStorage(t *testing.T, open func(context.Context) (dateBackend, error)) {
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
		{App: "clocktime", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "clocktime", Model: original}}},
		{App: "clocktime", Name: "0002_day", Dependencies: []migrations.MigrationKey{{App: "clocktime", Name: "0001_initial"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "clocktime", ModelName: "record", Field: current.Fields[2], BeforeField: "required"},
			migrations.CreateModel{AppLabel: "clocktime", Model: (models.LinkDescriptor{}).Metadata()},
		}},
	} {
		wire, err := definition.Encode(definition.Producer{Name: "time-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "clocktime", Name: "0001_initial"}))
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(current.DBTable, []query.Assignment{
		orm.NewAssignment(current.Fields[1], query.String("existing")), orm.NewAssignment(current.Fields[3], query.Time(minimum)), orm.NewAssignment(current.Fields[4], query.Time(minimum)), orm.NewAssignment(current.Fields[5], query.Time(fraction)),
	}, query.NewFieldRef("id", "id", query.FieldInteger, false))); err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, DRF string
		Database    struct {
			AfterAdd           [][]any `json:"after_add"`
			Reopened           [][]any
			Queries, Relations map[string][]string
			Aggregates         map[string]string
			AfterUpdate        [][]any  `json:"after_update"`
			AfterRemove        []string `json:"after_remove"`
		}
	}
	if err := json.Unmarshal(rawReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" || len(reference.Database.Queries) != 9 || len(reference.Database.Relations) != 9 {
		t.Fatal("incomplete time reference")
	}
	observe := func() [][]any {
		rows, err := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := make([][]any, len(rows))
		for i, row := range rows {
			var day any
			if row.At != nil {
				day = row.At.String()
			}
			result[i] = []any{row.Label, day}
		}
		return result
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterAdd) {
		t.Fatal("time addition changed existing row")
	}
	for _, input := range []models.RecordCreate{models.NewRecordCreate("minimum", minimum).WithAt(minimum), models.NewRecordCreate("fraction", fraction).WithAt(fraction), models.NewRecordCreate("maximum", maximum).WithAt(maximum), models.NewRecordCreate("null", fraction)} {
		row, err := models.RecordObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		if row.Origin == nil || *row.Origin != minimum || row.Scheduled != fraction {
			t.Fatal("time defaults changed")
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
		t.Fatal("time changed across fresh connection")
	}
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Record]
		not       bool
	}{
		{"exact", "at", fraction, models.RecordFields.At.Exact(fraction), false},
		{"not_exact", "at", fraction, orm.Not(models.RecordFields.At.Exact(fraction)), true},
		{"null", "at__isnull", true, models.RecordFields.At.IsNull(true), false},
		{"gt", "at__gt", fraction, models.RecordFields.At.GreaterThan(fraction), false},
		{"gte", "at__gte", fraction, models.RecordFields.At.GreaterThanOrEqual(fraction), false},
		{"lt", "at__lt", fraction, models.RecordFields.At.LessThan(fraction), false},
		{"lte", "at__lte", fraction, models.RecordFields.At.LessThanOrEqual(fraction), false},
		{"in_null", "at__in", []any{fraction, nil}, models.RecordFields.At.In(fraction), false},
		{"not_in_null", "at__in", []any{fraction, nil}, orm.And(orm.Not(models.RecordFields.At.In(fraction)), models.RecordFields.At.IsNull(false)), true},
	} {
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic time: %v", err)
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
				t.Fatalf("%s: %v want %v", test.name, labels, reference.Database.Queries[test.name])
			}
		}
	}
	for _, value := range []any{"12:34:56.123456", time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC), clock.Time{Hour: 24}, []string{"12:34:56.123456"}} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "at", Value: value}}); err == nil {
			t.Fatal("dynamic time coerced invalid input")
		}
	}
	all := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	*rows[1].At = maximum
	*rows[1].Origin = maximum
	again, err := all.All(ctx)
	if err != nil || again[1].At == nil || *again[1].At != minimum || *again[1].Origin != minimum {
		t.Fatal("time pointer escaped query cache")
	}
	projected, err := orm.SelectInto(ctx, all, orm.Project1(models.RecordFields.At, func(value *clock.Time) *clock.Time { return value }))
	if err != nil || len(projected) != 5 || projected[0] != nil || projected[1] == nil || *projected[1] != minimum || projected[4] != nil {
		t.Fatal("time projection lost presence")
	}
	*projected[1] = maximum
	if *again[1].At != minimum {
		t.Fatal("projection aliases model")
	}
	type bounds struct{ Min, Max orm.Optional[clock.Time] }
	rangeValue, err := orm.AggregateInto(ctx, all, orm.Aggregate2(orm.Min(models.RecordFields.At), orm.Max(models.RecordFields.At), func(a, b orm.Optional[clock.Time]) bounds { return bounds{a, b} }))
	if err != nil {
		t.Fatal(err)
	}
	for name, result := range map[string]orm.Optional[clock.Time]{"minimum": rangeValue.Min, "maximum": rangeValue.Max} {
		if value, ok := result.Get(); !ok || value.String() != reference.Database.Aggregates[name] {
			t.Fatal("time aggregate differs from independent DB")
		}
	}
	empty, err := orm.AggregateInto(ctx, all.Filter(models.RecordFields.ID.Exact(-1)), orm.Aggregate1(orm.Max(models.RecordFields.At), func(v orm.Optional[clock.Time]) orm.Optional[clock.Time] { return v }))
	if err != nil || empty.Valid() {
		t.Fatal("empty time aggregate invented a day")
	}
	if count, err := all.Filter(models.RecordFields.At.ExactField(orm.F[models.Record, clock.Time](models.RecordFields.Required))).Count(ctx); err != nil || count != 3 {
		t.Fatalf("time F comparison: %d %v", count, err)
	}
	if count, err := all.Filter(models.RecordFields.Required.GreaterThanField(orm.F[models.Record, clock.Time](models.RecordFields.Origin))).Count(ctx); err != nil || count != 3 {
		t.Fatalf("time ordered F comparison: %d %v", count, err)
	}
	verifyRelations(t, backend, again, reference.Database.Relations)
	changed, err := models.RecordObjects.Update(ctx, backend, again[2], models.RecordPatch{}.WithAtNull())
	if err != nil || changed.At != nil || again[2].At == nil || *again[2].At != fraction {
		t.Fatal("null patch mutated caller")
	}
	changed, err = models.RecordObjects.Update(ctx, backend, again[4], models.RecordPatch{}.WithAt(fraction))
	if err != nil || changed.At == nil || *changed.At != fraction {
		t.Fatal("time patch lost value")
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterUpdate) {
		t.Fatal("time update differs from independent DB")
	}
	*changed.At = maximum
	before := (models.RecordDescriptor{}).CloneWriteModel(changed)
	mutator := &countedMutator{Mutator: backend, fail: true}
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.At)); err == nil || !reflect.DeepEqual(before, changed) {
		t.Fatal("failed Save changed caller")
	}
	mutator.fail = false
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.At)); err != nil {
		t.Fatal(err)
	}
	changed.At = nil
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Label)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.Exact(changed.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.At == nil || *stored.At != maximum {
		t.Fatal("Save mask erased unselected day")
	}
	writes := mutator.writes
	if _, err := models.RecordObjects.Update(ctx, mutator, stored, models.RecordPatch{}.WithAt(clock.Time{Hour: 24})); err == nil || mutator.writes != writes {
		t.Fatal("invalid time update reached I/O")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := models.RecordObjects.Create(canceled, mutator, models.NewRecordCreate("canceled", minimum)); !errors.Is(err, context.Canceled) || mutator.writes != writes {
		t.Fatal("canceled time create reached I/O")
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
	labels := []string{}
	for result.Next() {
		var id int64
		var label string
		if err := result.Scan(&id, &label); err != nil {
			t.Fatal(err)
		}
		labels = append(labels, label)
	}
	if result.Err() != nil || !slices.Equal(labels, reference.Database.AfterRemove) {
		t.Fatal("reverse migration changed rows")
	}
	if err := result.Close(); err != nil {
		t.Fatal(err)
	}
}

func verifyRelations(t *testing.T, backend dateBackend, records []models.Record, reference map[string][]string) {
	t.Helper()
	ctx := t.Context()
	for _, record := range records {
		if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate(record.Label).WithRecordID(record.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate("fraction_again").WithRecordID(records[2].ID)); err != nil {
		t.Fatal(err)
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
	link, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "clocktime", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	day := related.ModelsLink.Record.At
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Link]
		not       bool
	}{
		{"exact", "record__at", fraction, day.Exact(fraction), false},
		{"not_exact", "record__at", fraction, orm.Not(day.Exact(fraction)), true},
		{"null", "record__at__isnull", true, day.IsNull(true), false},
		{"gt", "record__at__gt", fraction, day.GreaterThan(fraction), false},
		{"gte", "record__at__gte", fraction, day.GreaterThanOrEqual(fraction), false},
		{"lt", "record__at__lt", fraction, day.LessThan(fraction), false},
		{"lte", "record__at__lte", fraction, day.LessThanOrEqual(fraction), false},
		{"in_null", "record__at__in", []any{fraction, nil}, day.In(fraction), false},
		{"not_in_null", "record__at__in", []any{fraction, nil}, orm.And(orm.Not(day.In(fraction)), day.IsNull(false)), true},
	} {
		dynamic, err := orm.ParseDynamicRelations(link, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic time relation: %v", err)
		}
		if test.not {
			dynamic[0] = orm.Not(dynamic[0])
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
					t.Fatal("eager time owner presence differs")
				}
				if present && record.Label == "fraction" {
					if record.At == nil || *record.At != fraction {
						t.Fatal("eager scan lost time")
					}
					snapshot, err := record.Unwrap()
					if err != nil {
						t.Fatal(err)
					}
					*snapshot.At = maximum
					again, _, err := row.Record(ctx)
					if err != nil || again != record || again.At == nil || *again.At != fraction {
						t.Fatal("eager snapshot aliases cached time")
					}
				}
			}
			if !slices.Equal(labels, reference[test.name]) {
				t.Fatalf("related %s=%v want %v", test.name, labels, reference[test.name])
			}
		}
	}
}
