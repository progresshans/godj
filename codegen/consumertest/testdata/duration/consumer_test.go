package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"example.com/godj-duration/models"
	"example.com/godj-duration/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed reference.json
var rawReference []byte
var minimum = duration.FromMicroseconds(math.MinInt64)
var fraction = duration.Duration{Days: 1, Microseconds: 7384123456}
var maximum = duration.FromMicroseconds(math.MaxInt64)

func TestDurationGeneratedDefaults(t *testing.T) {
	// A model and member named Time cannot shadow generated package imports.
	if mutation := models.NewDurationCreate().WithDuration(duration.Duration{}).BuildCreate(); mutation.Err() != nil {
		t.Fatal(mutation.Err())
	}

	if err := (models.RecordCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("missing required time accepted")
	}
	for _, test := range []struct {
		input       models.RecordCreate
		day, origin query.Value
	}{
		{models.NewRecordCreate("default", minimum), query.Null(), query.Duration(duration.Duration{})},
		{models.NewRecordCreate("explicit", maximum).WithElapsed(fraction).WithOriginNull(), query.Duration(fraction), query.Null()},
		{models.NewRecordCreate("null", fraction).WithElapsedNull().WithOrigin(maximum), query.Null(), query.Duration(maximum)},
	} {
		mutation := test.input.BuildCreate()
		if err := mutation.Err(); err != nil {
			t.Fatal(err)
		}
		values := map[string]query.Value{}
		for _, assignment := range mutation.Assignments() {
			values[assignment.Field().Name()] = assignment.Value()
		}
		for name, want := range map[string]query.Value{"elapsed": test.day, "origin": test.origin, "scheduled": query.Duration(fraction)} {
			if got, ok := values[name]; !ok || !got.Equal(want) {
				t.Fatalf("%s default/presence changed", name)
			}
		}
	}
	for _, invalid := range []duration.Duration{{Microseconds: -1}, {Microseconds: duration.MicrosecondsPerDay}, {Days: 1000000000}} {
		if models.NewRecordCreate("bad", invalid).BuildCreate().Err() == nil || models.NewRecordCreate("bad", minimum).WithElapsed(invalid).BuildCreate().Err() == nil || (models.RecordPatch{}).WithElapsed(invalid).BuildPatch(models.Record{}).Err() == nil {
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

func TestDurationStorageQueryAndOwnership(t *testing.T) {
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
		{App: "durationref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "durationref", Model: original}}},
		{App: "durationref", Name: "0002_day", Dependencies: []migrations.MigrationKey{{App: "durationref", Name: "0001_initial"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "durationref", ModelName: "record", Field: current.Fields[2], BeforeField: "required"},
			migrations.CreateModel{AppLabel: "durationref", Model: (models.LinkDescriptor{}).Metadata()},
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
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "durationref", Name: "0001_initial"}))
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(current.DBTable, []query.Assignment{
		orm.NewAssignment(current.Fields[1], query.String("existing")), orm.NewAssignment(current.Fields[3], query.Duration(minimum)), orm.NewAssignment(current.Fields[4], query.Duration(minimum)), orm.NewAssignment(current.Fields[5], query.Duration(fraction)),
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
			if row.Elapsed != nil {
				day = row.Elapsed.String()
			}
			result[i] = []any{row.Label, day}
		}
		return result
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterAdd) {
		t.Fatal("time addition changed existing row")
	}
	for _, input := range []models.RecordCreate{models.NewRecordCreate("minimum", minimum).WithElapsed(minimum), models.NewRecordCreate("fraction", fraction).WithElapsed(fraction), models.NewRecordCreate("maximum", maximum).WithElapsed(maximum), models.NewRecordCreate("null", fraction)} {
		row, err := models.RecordObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		if row.Origin == nil || *row.Origin != (duration.Duration{}) || row.Scheduled != fraction {
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
		{"exact", "elapsed", fraction, models.RecordFields.Elapsed.Exact(fraction), false},
		{"not_exact", "elapsed", fraction, orm.Not(models.RecordFields.Elapsed.Exact(fraction)), true},
		{"null", "elapsed__isnull", true, models.RecordFields.Elapsed.IsNull(true), false},
		{"gt", "elapsed__gt", fraction, models.RecordFields.Elapsed.GreaterThan(fraction), false},
		{"gte", "elapsed__gte", fraction, models.RecordFields.Elapsed.GreaterThanOrEqual(fraction), false},
		{"lt", "elapsed__lt", fraction, models.RecordFields.Elapsed.LessThan(fraction), false},
		{"lte", "elapsed__lte", fraction, models.RecordFields.Elapsed.LessThanOrEqual(fraction), false},
		{"in_null", "elapsed__in", []any{fraction, nil}, models.RecordFields.Elapsed.In(fraction), false},
		{"not_in_null", "elapsed__in", []any{fraction, nil}, orm.And(orm.Not(models.RecordFields.Elapsed.In(fraction)), models.RecordFields.Elapsed.IsNull(false)), true},
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
	for _, value := range []any{"12:34:56.123456", time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC), duration.Duration{Days: 1000000000}, []string{"12:34:56.123456"}} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "elapsed", Value: value}}); err == nil {
			t.Fatal("dynamic time coerced invalid input")
		}
	}
	all := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	*rows[1].Elapsed = maximum
	*rows[1].Origin = maximum
	again, err := all.All(ctx)
	if err != nil || again[1].Elapsed == nil || *again[1].Elapsed != minimum || *again[1].Origin != (duration.Duration{}) {
		t.Fatal("time pointer escaped query cache")
	}
	projected, err := orm.SelectInto(ctx, all, orm.Project1(models.RecordFields.Elapsed, func(value *duration.Duration) *duration.Duration { return value }))
	if err != nil || len(projected) != 5 || projected[0] != nil || projected[1] == nil || *projected[1] != minimum || projected[4] != nil {
		t.Fatal("time projection lost presence")
	}
	*projected[1] = maximum
	if *again[1].Elapsed != minimum {
		t.Fatal("projection aliases model")
	}
	type bounds struct {
		Min, Max orm.Optional[duration.Duration]
	}
	rangeValue, err := orm.AggregateInto(ctx, all, orm.Aggregate2(orm.Min(models.RecordFields.Elapsed), orm.Max(models.RecordFields.Elapsed), func(a, b orm.Optional[duration.Duration]) bounds { return bounds{a, b} }))
	if err != nil {
		t.Fatal(err)
	}
	for name, result := range map[string]orm.Optional[duration.Duration]{"minimum": rangeValue.Min, "maximum": rangeValue.Max} {
		if value, ok := result.Get(); !ok || value.String() != reference.Database.Aggregates[name] {
			t.Fatal("time aggregate differs from independent DB")
		}
	}
	empty, err := orm.AggregateInto(ctx, all.Filter(models.RecordFields.ID.Exact(-1)), orm.Aggregate1(orm.Max(models.RecordFields.Elapsed), func(v orm.Optional[duration.Duration]) orm.Optional[duration.Duration] { return v }))
	if err != nil || empty.Valid() {
		t.Fatal("empty time aggregate invented a day")
	}
	if count, err := all.Filter(models.RecordFields.Elapsed.ExactField(orm.F[models.Record, duration.Duration](models.RecordFields.Required))).Count(ctx); err != nil || count != 3 {
		t.Fatalf("time F comparison: %d %v", count, err)
	}
	if count, err := all.Filter(models.RecordFields.Required.GreaterThanField(orm.F[models.Record, duration.Duration](models.RecordFields.Origin))).Count(ctx); err != nil || count != 3 {
		t.Fatalf("time ordered F comparison: %d %v", count, err)
	}
	verifyRelations(t, backend, again, reference.Database.Relations)
	changed, err := models.RecordObjects.Update(ctx, backend, again[2], models.RecordPatch{}.WithElapsedNull())
	if err != nil || changed.Elapsed != nil || again[2].Elapsed == nil || *again[2].Elapsed != fraction {
		t.Fatal("null patch mutated caller")
	}
	changed, err = models.RecordObjects.Update(ctx, backend, again[4], models.RecordPatch{}.WithElapsed(fraction))
	if err != nil || changed.Elapsed == nil || *changed.Elapsed != fraction {
		t.Fatal("time patch lost value")
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterUpdate) {
		t.Fatal("time update differs from independent DB")
	}
	*changed.Elapsed = maximum
	before := (models.RecordDescriptor{}).CloneWriteModel(changed)
	mutator := &countedMutator{Mutator: backend, fail: true}
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Elapsed)); err == nil || !reflect.DeepEqual(before, changed) {
		t.Fatal("failed Save changed caller")
	}
	mutator.fail = false
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Elapsed)); err != nil {
		t.Fatal(err)
	}
	changed.Elapsed = nil
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Label)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.Exact(changed.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Elapsed == nil || *stored.Elapsed != maximum {
		t.Fatal("Save mask erased unselected day")
	}
	writes := mutator.writes
	if _, err := models.RecordObjects.Update(ctx, mutator, stored, models.RecordPatch{}.WithElapsed(duration.Duration{Days: 1000000000})); err == nil || mutator.writes != writes {
		t.Fatal("invalid time update reached I/O")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := models.RecordObjects.Create(canceled, mutator, models.NewRecordCreate("canceled", minimum)); !errors.Is(err, context.Canceled) || mutator.writes != writes {
		t.Fatal("canceled time create reached I/O")
	}
	// Model values beyond SQLite's integer range stay valid and are accepted by
	// PostgreSQL. Roll back this probe to preserve the independently observed rows.
	beforeWide := observe()
	_, native := backend.(*postgres.Backend)
	rollback := errors.New("duration range probe rollback")
	err = backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
		for _, text := range []string{"106751991 04:00:54.775808", "-106751992 19:59:05.224191", "999999999 23:59:59.999999", "-999999999 00:00:00"} {
			elapsed, err := duration.Parse(text)
			if err != nil {
				return err
			}
			row, err := models.RecordObjects.Create(ctx, session, models.NewRecordCreate("wide", elapsed).WithElapsed(elapsed))
			if (err == nil) != native {
				return errors.New("backend did not enforce its duration range")
			}
			if native {
				stored, found, err := models.RecordObjects.Using(session).Filter(models.RecordFields.ID.Exact(row.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
				if err != nil || !found || stored.Elapsed == nil || *stored.Elapsed != elapsed {
					return errors.New("wide native duration lost precision")
				}
			}
		}
		return rollback
	})
	if !errors.Is(err, rollback) || !reflect.DeepEqual(beforeWide, observe()) {
		t.Fatalf("duration range transaction changed rows: %v", err)
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
	link, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "durationref", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	day := related.ModelsLink.Record.Elapsed
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Link]
		not       bool
	}{
		{"exact", "record__elapsed", fraction, day.Exact(fraction), false},
		{"not_exact", "record__elapsed", fraction, orm.Not(day.Exact(fraction)), true},
		{"null", "record__elapsed__isnull", true, day.IsNull(true), false},
		{"gt", "record__elapsed__gt", fraction, day.GreaterThan(fraction), false},
		{"gte", "record__elapsed__gte", fraction, day.GreaterThanOrEqual(fraction), false},
		{"lt", "record__elapsed__lt", fraction, day.LessThan(fraction), false},
		{"lte", "record__elapsed__lte", fraction, day.LessThanOrEqual(fraction), false},
		{"in_null", "record__elapsed__in", []any{fraction, nil}, day.In(fraction), false},
		{"not_in_null", "record__elapsed__in", []any{fraction, nil}, orm.And(orm.Not(day.In(fraction)), day.IsNull(false)), true},
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
					if record.Elapsed == nil || *record.Elapsed != fraction {
						t.Fatal("eager scan lost time")
					}
					snapshot, err := record.Unwrap()
					if err != nil {
						t.Fatal(err)
					}
					*snapshot.Elapsed = maximum
					again, _, err := row.Record(ctx)
					if err != nil || again != record || again.Elapsed == nil || *again.Elapsed != fraction {
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
