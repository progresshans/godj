package consumer_test

import (
	"context"
	"errors"
	"example.com/godj-datetime/models"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"testing"
	"time"
)

func scheduled() time.Time { return time.Date(2026, 9, 19, 3, 34, 56, 123456000, time.UTC) }

func TestDateTimeGeneratedDefaults(t *testing.T) {
	if err := (models.EventCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("required time silently became the Go zero time")
	}
	mutation := models.NewEventCreate(time.Time{}).BuildCreate()
	if err := mutation.Err(); err != nil {
		t.Fatal(err)
	}
	values := map[string]query.Value{}
	for _, assignment := range mutation.Assignments() {
		values[assignment.Field().Name()] = assignment.Value()
	}
	for _, name := range []string{"time", "origin"} {
		value, ok := values[name].DateTime()
		if !ok || !value.IsZero() {
			t.Fatal("explicit or default year 1 became null")
		}
	}
	if !values["optional"].IsNull() {
		t.Fatal("omitted nullable datetime acquired an epoch")
	}
	value, ok := values["scheduled"].DateTime()
	if !ok || value != scheduled() {
		t.Fatal("default instant or precision changed")
	}
	invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := models.NewEventCreate(invalid).BuildCreate().Err(); err == nil {
		t.Fatal("out-of-range required datetime accepted")
	}
	if err := models.NewEventCreate(time.Time{}).WithOptional(invalid).BuildCreate().Err(); err == nil {
		t.Fatal("out-of-range nullable datetime accepted")
	}
}

type countedBackend struct {
	*sqlite.Backend
	writes int
}

func (b *countedBackend) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	b.writes++
	return b.Backend.Insert(ctx, plan)
}
func (b *countedBackend) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	b.writes++
	return b.Backend.Update(ctx, plan)
}

func TestDateTimeStorageAndQuery(t *testing.T) {
	ctx := t.Context()
	database, err := sqlite.OpenMemory(ctx, "datetime-consumer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	backend := &countedBackend{Backend: database}
	encoded, err := definition.Encode(definition.Producer{Name: "datetime-consumer", Version: "1"}, migrations.Migration{App: "events", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "events", Model: (models.EventDescriptor{}).Metadata()}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "datetime-consumer", Document: encoded})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	minimum := time.Time{}
	beforeEpoch := time.Date(1969, 12, 31, 23, 59, 59, 999999000, time.UTC)
	offset := time.Date(2026, 9, 19, 12, 34, 56, 123456789, time.FixedZone("caller", 9*3600))
	maximum := time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	var created []models.Event
	for _, value := range []time.Time{maximum, offset, minimum, beforeEpoch} {
		event, err := models.EventObjects.Create(ctx, backend, models.NewEventCreate(value))
		if err != nil {
			t.Fatal(err)
		}
		if event.Time != value.UTC().Truncate(time.Microsecond) || event.Scheduled != scheduled() || event.Optional != nil || event.Origin == nil || !event.Origin.IsZero() {
			t.Fatal("Create returned noncanonical values or lost default presence")
		}
		created = append(created, event)
	}
	all := models.EventObjects.Using(backend).OrderBy(models.EventFields.Time.Asc())
	rows, err := all.All(ctx)
	if err != nil || len(rows) != 4 {
		t.Fatalf("datetime read: %v", err)
	}
	for i, want := range []time.Time{minimum, beforeEpoch, scheduled(), maximum} {
		if rows[i].Time != want {
			t.Fatal("datetime ordering or full range changed")
		}
	}
	projectedRows, err := backend.Query(ctx, all.Plan())
	if err != nil {
		t.Fatal(err)
	}
	if !projectedRows.Next() {
		t.Fatal("missing projection row")
	}
	scan := (models.EventDescriptor{}).NewProjectionScan()
	if err := projectedRows.Scan(scan.Destinations()...); err != nil {
		t.Fatal(err)
	}
	joined, _, presence := scan.Decode()
	if presence != orm.ProjectionPresent || joined.Time != minimum || joined.Origin == nil || !joined.Origin.IsZero() {
		t.Fatal("eager datetime scan lost presence or precision")
	}
	if err := projectedRows.Close(); err != nil {
		t.Fatal(err)
	}
	*rows[0].Origin = maximum
	again, err := all.All(ctx)
	if err != nil || !again[0].Origin.IsZero() {
		t.Fatal("nullable datetime escaped result cache")
	}
	typed := models.EventObjects.Using(backend).Filter(models.EventFields.Time.GreaterThanOrEqual(offset))
	dynamic, err := orm.ParseDynamic(models.EventDescriptor{}, nil, []orm.LookupInput{{Key: "time__gte", Value: offset}})
	if err != nil || !typed.Plan().Equal(models.EventObjects.Using(backend).Filter(dynamic...).Plan()) {
		t.Fatal("typed/dynamic datetime plans differ")
	}
	if count, err := typed.Count(ctx); err != nil || count != 2 {
		t.Fatalf("datetime comparison: %d %v", count, err)
	}
	if _, err := orm.ParseDynamic(models.EventDescriptor{}, nil, []orm.LookupInput{{Key: "time", Value: "2026-09-19T03:34:56Z"}}); err == nil {
		t.Fatal("dynamic query implicitly parsed a string as time")
	}
	changed, err := models.EventObjects.Update(ctx, backend, created[1], models.EventPatch{}.WithOptional(offset))
	if err != nil || changed.Optional == nil || *changed.Optional != scheduled() {
		t.Fatalf("nullable datetime patch: %v", err)
	}
	if count, err := models.EventObjects.Using(backend).Filter(models.EventFields.Time.ExactField(orm.F[models.Event, time.Time](models.EventFields.Optional))).Count(ctx); err != nil || count != 1 {
		t.Fatal("datetime F comparison failed")
	}
	projected, err := orm.SelectInto(ctx, models.EventObjects.Using(backend).OrderBy(models.EventFields.ID.Asc()), orm.Project1(models.EventFields.Optional, func(v *time.Time) *time.Time { return v }))
	if err != nil || len(projected) != 4 || projected[0] != nil || projected[1] == nil || *projected[1] != scheduled() {
		t.Fatal("datetime projection null/value mismatch")
	}
	type bounds struct{ Min, Max orm.Optional[time.Time] }
	result, err := orm.AggregateInto(ctx, models.EventObjects.Using(backend), orm.Aggregate2(orm.Min(models.EventFields.Time), orm.Max(models.EventFields.Optional), func(a, b orm.Optional[time.Time]) bounds { return bounds{a, b} }))
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := result.Min.Get(); !ok || value != minimum {
		t.Fatal("MIN time lost valid year 1")
	}
	if value, ok := result.Max.Get(); !ok || value != scheduled() {
		t.Fatal("nullable MAX time failed")
	}
	empty, err := orm.AggregateInto(ctx, models.EventObjects.Using(backend).Filter(models.EventFields.ID.Exact(-1)), orm.Aggregate1(orm.Max(models.EventFields.Time), func(v orm.Optional[time.Time]) orm.Optional[time.Time] { return v }))
	if err != nil || empty.Valid() {
		t.Fatal("empty time aggregate acquired an epoch")
	}
	changed.Time = beforeEpoch
	*changed.Optional = maximum
	if err := models.EventObjects.Save(ctx, backend, &changed, models.EventUpdateFields(models.EventFields.Time)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.EventObjects.Using(backend).Filter(models.EventFields.ID.Exact(changed.ID)).OrderBy(models.EventFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Time != beforeEpoch || stored.Optional == nil || *stored.Optional != scheduled() {
		t.Fatal("Save wrote an unselected time field")
	}
	before := backend.writes
	invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := models.EventObjects.Update(ctx, backend, stored, models.EventPatch{}.WithTime(invalid)); err == nil || backend.writes != before {
		t.Fatal("invalid datetime update reached I/O")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := models.EventObjects.Create(canceled, backend, models.NewEventCreate(minimum)); !errors.Is(err, context.Canceled) || backend.writes != before {
		t.Fatal("canceled datetime Create reached storage")
	}
	cleared, err := models.EventObjects.Update(ctx, backend, stored, models.EventPatch{}.WithOptionalNull())
	if err != nil || cleared.Optional != nil {
		t.Fatal("datetime null patch failed")
	}
}
