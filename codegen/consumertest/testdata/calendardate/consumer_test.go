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

	"example.com/godj-calendar-date/models"
	"example.com/godj-calendar-date/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/calendar"
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
var minimum = calendar.Date{Year: 1, Month: 1, Day: 1}
var leap = calendar.Date{Year: 2000, Month: 2, Day: 29}
var maximum = calendar.Date{Year: 9999, Month: 12, Day: 31}

func TestCalendarDateGeneratedDefaults(t *testing.T) {
	if err := (models.RecordCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("missing required date accepted")
	}
	for _, test := range []struct {
		input       models.RecordCreate
		day, origin query.Value
	}{
		{models.NewRecordCreate("default", minimum), query.Null(), query.Date(minimum)},
		{models.NewRecordCreate("explicit", maximum).WithDay(leap).WithOriginNull(), query.Date(leap), query.Null()},
		{models.NewRecordCreate("null", leap).WithDayNull().WithOrigin(maximum), query.Null(), query.Date(maximum)},
	} {
		mutation := test.input.BuildCreate()
		if err := mutation.Err(); err != nil {
			t.Fatal(err)
		}
		values := map[string]query.Value{}
		for _, assignment := range mutation.Assignments() {
			values[assignment.Field().Name()] = assignment.Value()
		}
		for name, want := range map[string]query.Value{"day": test.day, "origin": test.origin, "scheduled": query.Date(leap)} {
			if got, ok := values[name]; !ok || !got.Equal(want) {
				t.Fatalf("%s default/presence changed", name)
			}
		}
	}
	for _, invalid := range []calendar.Date{{}, {Year: 1900, Month: 2, Day: 29}, {Year: 10000, Month: 1, Day: 1}} {
		if models.NewRecordCreate("bad", invalid).BuildCreate().Err() == nil || models.NewRecordCreate("bad", minimum).WithDay(invalid).BuildCreate().Err() == nil || (models.RecordPatch{}).WithDay(invalid).BuildPatch(models.Record{}).Err() == nil {
			t.Fatal("invalid date reached a valid mutation")
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

func TestCalendarDateStorageQueryAndOwnership(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "calendar.sqlite3")) + "?mode=rwc"
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
			t.Fatal("connect date consumer PostgreSQL")
		}
		name := fmt.Sprintf("godj_calendar_date_%d_%d", os.Getpid(), time.Now().UnixNano())
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
		{App: "calendardate", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "calendardate", Model: original}}},
		{App: "calendardate", Name: "0002_day", Dependencies: []migrations.MigrationKey{{App: "calendardate", Name: "0001_initial"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "calendardate", ModelName: "record", Field: current.Fields[2], BeforeField: "required"},
			migrations.CreateModel{AppLabel: "calendardate", Model: (models.LinkDescriptor{}).Metadata()},
		}},
	} {
		wire, err := definition.Encode(definition.Producer{Name: "date-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "calendardate", Name: "0001_initial"}))
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(current.DBTable, []query.Assignment{
		orm.NewAssignment(current.Fields[1], query.String("existing")), orm.NewAssignment(current.Fields[3], query.Date(minimum)), orm.NewAssignment(current.Fields[4], query.Date(minimum)), orm.NewAssignment(current.Fields[5], query.Date(leap)),
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
		t.Fatal("incomplete date reference")
	}
	observe := func() [][]any {
		rows, err := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := make([][]any, len(rows))
		for i, row := range rows {
			var day any
			if row.Day != nil {
				day = row.Day.String()
			}
			result[i] = []any{row.Label, day}
		}
		return result
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterAdd) {
		t.Fatal("date addition changed existing row")
	}
	for _, input := range []models.RecordCreate{models.NewRecordCreate("minimum", minimum).WithDay(minimum), models.NewRecordCreate("leap", leap).WithDay(leap), models.NewRecordCreate("maximum", maximum).WithDay(maximum), models.NewRecordCreate("null", leap)} {
		row, err := models.RecordObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		if row.Origin == nil || *row.Origin != minimum || row.Scheduled != leap {
			t.Fatal("date defaults changed")
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
		t.Fatal("date changed across fresh connection")
	}
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Record]
		not       bool
	}{
		{"exact", "day", leap, models.RecordFields.Day.Exact(leap), false},
		{"not_exact", "day", leap, orm.Not(models.RecordFields.Day.Exact(leap)), true},
		{"null", "day__isnull", true, models.RecordFields.Day.IsNull(true), false},
		{"gt", "day__gt", leap, models.RecordFields.Day.GreaterThan(leap), false},
		{"gte", "day__gte", leap, models.RecordFields.Day.GreaterThanOrEqual(leap), false},
		{"lt", "day__lt", leap, models.RecordFields.Day.LessThan(leap), false},
		{"lte", "day__lte", leap, models.RecordFields.Day.LessThanOrEqual(leap), false},
		{"in_null", "day__in", []any{leap, nil}, models.RecordFields.Day.In(leap), false},
		{"not_in_null", "day__in", []any{leap, nil}, orm.And(orm.Not(models.RecordFields.Day.In(leap)), models.RecordFields.Day.IsNull(false)), true},
	} {
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic date: %v", err)
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
	for _, value := range []any{"2000-02-29", time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC), calendar.Date{}, []string{"2000-02-29"}} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "day", Value: value}}); err == nil {
			t.Fatal("dynamic date coerced invalid input")
		}
	}
	all := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	*rows[1].Day = maximum
	*rows[1].Origin = maximum
	again, err := all.All(ctx)
	if err != nil || again[1].Day == nil || *again[1].Day != minimum || *again[1].Origin != minimum {
		t.Fatal("date pointer escaped query cache")
	}
	projected, err := orm.SelectInto(ctx, all, orm.Project1(models.RecordFields.Day, func(value *calendar.Date) *calendar.Date { return value }))
	if err != nil || len(projected) != 5 || projected[0] != nil || projected[1] == nil || *projected[1] != minimum || projected[4] != nil {
		t.Fatal("date projection lost presence")
	}
	*projected[1] = maximum
	if *again[1].Day != minimum {
		t.Fatal("projection aliases model")
	}
	type bounds struct{ Min, Max orm.Optional[calendar.Date] }
	rangeValue, err := orm.AggregateInto(ctx, all, orm.Aggregate2(orm.Min(models.RecordFields.Day), orm.Max(models.RecordFields.Day), func(a, b orm.Optional[calendar.Date]) bounds { return bounds{a, b} }))
	if err != nil {
		t.Fatal(err)
	}
	for name, result := range map[string]orm.Optional[calendar.Date]{"minimum": rangeValue.Min, "maximum": rangeValue.Max} {
		if value, ok := result.Get(); !ok || value.String() != reference.Database.Aggregates[name] {
			t.Fatal("date aggregate differs from independent DB")
		}
	}
	empty, err := orm.AggregateInto(ctx, all.Filter(models.RecordFields.ID.Exact(-1)), orm.Aggregate1(orm.Max(models.RecordFields.Day), func(v orm.Optional[calendar.Date]) orm.Optional[calendar.Date] { return v }))
	if err != nil || empty.Valid() {
		t.Fatal("empty date aggregate invented a day")
	}
	if count, err := all.Filter(models.RecordFields.Day.ExactField(orm.F[models.Record, calendar.Date](models.RecordFields.Required))).Count(ctx); err != nil || count != 3 {
		t.Fatalf("date F comparison: %d %v", count, err)
	}
	if count, err := all.Filter(models.RecordFields.Required.GreaterThanField(orm.F[models.Record, calendar.Date](models.RecordFields.Origin))).Count(ctx); err != nil || count != 3 {
		t.Fatalf("date ordered F comparison: %d %v", count, err)
	}
	verifyRelations(t, backend, again, reference.Database.Relations)
	changed, err := models.RecordObjects.Update(ctx, backend, again[2], models.RecordPatch{}.WithDayNull())
	if err != nil || changed.Day != nil || again[2].Day == nil || *again[2].Day != leap {
		t.Fatal("null patch mutated caller")
	}
	changed, err = models.RecordObjects.Update(ctx, backend, again[4], models.RecordPatch{}.WithDay(leap))
	if err != nil || changed.Day == nil || *changed.Day != leap {
		t.Fatal("date patch lost value")
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterUpdate) {
		t.Fatal("date update differs from independent DB")
	}
	*changed.Day = maximum
	before := (models.RecordDescriptor{}).CloneWriteModel(changed)
	mutator := &countedMutator{Mutator: backend, fail: true}
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Day)); err == nil || !reflect.DeepEqual(before, changed) {
		t.Fatal("failed Save changed caller")
	}
	mutator.fail = false
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Day)); err != nil {
		t.Fatal(err)
	}
	changed.Day = nil
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Label)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.Exact(changed.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Day == nil || *stored.Day != maximum {
		t.Fatal("Save mask erased unselected day")
	}
	writes := mutator.writes
	if _, err := models.RecordObjects.Update(ctx, mutator, stored, models.RecordPatch{}.WithDay(calendar.Date{})); err == nil || mutator.writes != writes {
		t.Fatal("invalid date update reached I/O")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := models.RecordObjects.Create(canceled, mutator, models.NewRecordCreate("canceled", minimum)); !errors.Is(err, context.Canceled) || mutator.writes != writes {
		t.Fatal("canceled date create reached I/O")
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
	if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate("leap_again").WithRecordID(records[2].ID)); err != nil {
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
	link, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "calendardate", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	day := related.ModelsLink.Record.Day
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Link]
		not       bool
	}{
		{"exact", "record__day", leap, day.Exact(leap), false},
		{"not_exact", "record__day", leap, orm.Not(day.Exact(leap)), true},
		{"null", "record__day__isnull", true, day.IsNull(true), false},
		{"gt", "record__day__gt", leap, day.GreaterThan(leap), false},
		{"gte", "record__day__gte", leap, day.GreaterThanOrEqual(leap), false},
		{"lt", "record__day__lt", leap, day.LessThan(leap), false},
		{"lte", "record__day__lte", leap, day.LessThanOrEqual(leap), false},
		{"in_null", "record__day__in", []any{leap, nil}, day.In(leap), false},
		{"not_in_null", "record__day__in", []any{leap, nil}, orm.And(orm.Not(day.In(leap)), day.IsNull(false)), true},
	} {
		dynamic, err := orm.ParseDynamicRelations(link, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic date relation: %v", err)
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
					t.Fatal("eager date owner presence differs")
				}
				if present && record.Label == "leap" {
					if record.Day == nil || *record.Day != leap {
						t.Fatal("eager scan lost date")
					}
					snapshot, err := record.Unwrap()
					if err != nil {
						t.Fatal(err)
					}
					*snapshot.Day = maximum
					again, _, err := row.Record(ctx)
					if err != nil || again != record || again.Day == nil || *again.Day != leap {
						t.Fatal("eager snapshot aliases cached date")
					}
				}
			}
			if !slices.Equal(labels, reference[test.name]) {
				t.Fatalf("related %s=%v want %v", test.name, labels, reference[test.name])
			}
		}
	}
}
