package consumer_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"example.com/godj-integers/models"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

var (
	_ orm.WritableField[models.Number]         = models.NumberFields.Amount
	_ orm.WritableField[models.Number]         = models.NumberFields.Optional
	_ orm.ReferenceField[models.Number, int64] = models.NumberFields.ID
	_ orm.ReferenceField[models.Number, int64] = models.NumberFields.Optional
)

func TestIntegerGeneratedDefaults(t *testing.T) {
	if err := (models.NumberCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("required integer was silently defaulted")
	}
	defaults := models.NewNumberCreate(0).BuildCreate()
	if err := defaults.Err(); err != nil {
		t.Fatal(err)
	}
	values := map[string]query.Value{}
	for _, assignment := range defaults.Assignments() {
		values[assignment.Field().Name()] = assignment.Value()
	}
	for name, want := range map[string]int64{"amount": 0, "quota": 0, "minimum": math.MinInt64, "maximum": math.MaxInt64} {
		if value, ok := values[name].Integer(); !ok || value != want {
			t.Fatalf("default %s was rounded or omitted", name)
		}
	}
	if !values["optional"].IsNull() {
		t.Fatal("omitted nullable integer became zero")
	}
	for _, assignment := range models.NewNumberCreate(-1).WithQuota(-2).WithOptional(0).WithMinimumNull().WithMaximum(0).BuildCreate().Assignments() {
		switch assignment.Field().Name() {
		case "minimum":
			if !assignment.Value().IsNull() {
				t.Fatal("explicit null did not override default")
			}
		case "optional", "maximum":
			if value, ok := assignment.Value().Integer(); !ok || value != 0 {
				t.Fatal("explicit zero did not override default/null")
			}
		}
	}
	if _, writable := any(models.NumberFields.ID).(orm.WritableField[models.Number]); writable {
		t.Fatal("automatic primary key acquired the writable capability")
	}
}

type countedBackend struct {
	*sqlite.Backend
	inserts int
}

func (backend *countedBackend) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	backend.inserts++
	return backend.Backend.Insert(ctx, plan)
}

func TestIntegerStorageAndQuery(t *testing.T) {
	ctx := t.Context()
	database, err := sqlite.OpenMemory(ctx, "generated_integer_consumer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	backend := &countedBackend{Backend: database}
	document, err := definition.Encode(definition.Producer{Name: "integer-consumer", Version: "1"}, migrations.Migration{
		App: "integers", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "integers", Model: (models.NumberDescriptor{}).Metadata()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "integer-consumer", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	if _, err := models.NumberObjects.Create(ctx, backend, models.NumberCreate{}); err == nil || backend.inserts != 0 {
		t.Fatal("invalid integer create reached I/O")
	}
	minimum, err := models.NumberObjects.Create(ctx, backend, models.NewNumberCreate(math.MinInt64))
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := models.NumberObjects.Create(ctx, backend, models.NewNumberCreate(math.MaxInt64).WithOptional(math.MaxInt64))
	if err != nil {
		t.Fatal(err)
	}
	if minimum.Quota != 0 || minimum.Optional != nil || minimum.Minimum == nil || *minimum.Minimum != math.MinInt64 || minimum.Maximum == nil || *minimum.Maximum != math.MaxInt64 {
		t.Fatal("stored generated defaults changed")
	}
	all := models.NumberObjects.Using(backend).OrderBy(models.NumberFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil || len(rows) != 2 || rows[0].Amount != math.MinInt64 || rows[1].Amount != math.MaxInt64 {
		t.Fatalf("signed range roundtrip failed: %v", err)
	}
	*rows[0].Minimum = 123
	*rows[1].Optional = 123
	again, err := all.All(ctx)
	if err != nil || *again[0].Minimum != math.MinInt64 || *again[1].Optional != math.MaxInt64 {
		t.Fatal("integer pointer escaped the result cache")
	}
	for _, predicate := range []orm.Predicate[models.Number]{models.NumberFields.Amount.LessThan(0), models.NumberFields.Optional.IsNull(true)} {
		count, err := models.NumberObjects.Using(backend).Filter(predicate).Count(ctx)
		if err != nil || count != 1 {
			t.Fatalf("typed integer predicate failed: %v", err)
		}
	}
	dynamic, err := orm.ParseDynamic(models.NumberDescriptor{}, nil, []orm.LookupInput{{Key: "amount__gte", Value: int64(0)}})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := models.NumberObjects.Using(backend).Filter(dynamic...).Count(ctx); err != nil || count != 1 {
		t.Fatal("dynamic integer predicate diverged")
	}
	if _, err := orm.ParseDynamic(models.NumberDescriptor{}, nil, []orm.LookupInput{{Key: "amount", Value: float64(1)}}); err == nil {
		t.Fatal("dynamic integer accepted a floating-point value")
	}
	if count, err := models.NumberObjects.Using(backend).Filter(models.NumberFields.Amount.ExactField(orm.F[models.Number, int64](models.NumberFields.Optional))).Count(ctx); err != nil || count != 1 {
		t.Fatal("nullable integer F comparison diverged")
	}
	projected, err := orm.SelectInto(ctx, models.NumberObjects.Using(backend).OrderBy(models.NumberFields.ID.Asc()), orm.Project1(models.NumberFields.Optional, func(value *int64) *int64 { return value }))
	if err != nil || len(projected) != 2 || projected[0] != nil || projected[1] == nil || *projected[1] != math.MaxInt64 {
		t.Fatalf("nullable integer projection lost null: %v", err)
	}
	type bounds struct{ Minimum, Maximum orm.Optional[int64] }
	aggregate, err := orm.AggregateInto(ctx, models.NumberObjects.Using(backend), orm.Aggregate2(orm.Min(models.NumberFields.Amount), orm.Max(models.NumberFields.Optional), func(low, high orm.Optional[int64]) bounds { return bounds{low, high} }))
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := aggregate.Minimum.Get(); !ok || value != math.MinInt64 {
		t.Fatal("minimum was rounded")
	}
	if value, ok := aggregate.Maximum.Get(); !ok || value != math.MaxInt64 {
		t.Fatal("nullable maximum was rounded")
	}
	maximum, err = models.NumberObjects.Update(ctx, backend, maximum, models.NumberPatch{}.WithAmount(0).WithOptionalNull())
	if err != nil || maximum.Amount != 0 || maximum.Optional != nil {
		t.Fatalf("integer patch failed: %v", err)
	}
	maximum.Amount = -42
	dirty := int64(123)
	maximum.Optional = &dirty
	if err := models.NumberObjects.Save(ctx, backend, &maximum, models.NumberUpdateFields(models.NumberFields.Amount)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.NumberObjects.Using(backend).Filter(models.NumberFields.ID.Exact(maximum.ID)).OrderBy(models.NumberFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Amount != -42 || stored.Optional != nil {
		t.Fatal("integer update mask changed an omitted field")
	}
	if err := models.NumberObjects.Save(ctx, backend, &maximum, models.NumberUpdateFieldNames("id")); err == nil {
		t.Fatal("dynamic update mask allowed the primary key")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	before := backend.inserts
	if _, err := models.NumberObjects.Create(canceled, backend, models.NewNumberCreate(1)); !errors.Is(err, context.Canceled) || backend.inserts != before {
		t.Fatal("canceled integer creation reached storage")
	}
	if count, err := models.NumberObjects.Using(backend).Count(ctx); err != nil || count != 2 {
		t.Fatal("failed operations changed integer rows")
	}
}
