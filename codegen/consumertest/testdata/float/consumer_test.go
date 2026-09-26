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

	"example.com/godj-float/models"
	"example.com/godj-float/project"
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
var minimum = -math.MaxFloat64
var fraction = float64(1.5)
var maximum = math.MaxFloat64

func TestFloatGeneratedDefaults(t *testing.T) {
	// A model and member named Float cannot shadow generated package imports.
	if mutation := models.NewFloatCreate().WithFloat(float64(0)).BuildCreate(); mutation.Err() != nil {
		t.Fatal(mutation.Err())
	}

	if err := (models.RecordCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("missing required float accepted")
	}
	for _, test := range []struct {
		input       models.RecordCreate
		day, origin query.Value
	}{
		{models.NewRecordCreate("default", minimum), query.Null(), query.Float(float64(0))},
		{models.NewRecordCreate("explicit", maximum).WithEffort(fraction).WithOriginNull(), query.Float(fraction), query.Null()},
		{models.NewRecordCreate("null", fraction).WithEffortNull().WithOrigin(maximum), query.Null(), query.Float(maximum)},
	} {
		mutation := test.input.BuildCreate()
		if err := mutation.Err(); err != nil {
			t.Fatal(err)
		}
		values := map[string]query.Value{}
		for _, assignment := range mutation.Assignments() {
			values[assignment.Field().Name()] = assignment.Value()
		}
		for name, want := range map[string]query.Value{"effort": test.day, "origin": test.origin, "scheduled": query.Float(fraction)} {
			if got, ok := values[name]; !ok || !got.Equal(want) {
				t.Fatalf("%s default/presence changed", name)
			}
		}
	}
	defaults := models.NewFloatCreate().BuildCreate()
	if defaults.Err() != nil {
		t.Fatal(defaults.Err())
	}
	seenZero, seenNaN := false, false
	for _, assignment := range defaults.Assignments() {
		value, ok := assignment.Value().Float()
		if assignment.Field().Name() == "float_value" {
			seenZero = ok && math.Float64bits(value) == 0x8000000000000000
		}
		if assignment.Field().Name() == "nan" {
			seenNaN = ok && math.Float64bits(value) == 0x7ff8000000000000
		}
	}
	if !seenZero || !seenNaN {
		t.Fatal("generated defaults changed signed zero or canonical NaN")
	}

}

type floatBackend interface {
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

func TestFloatStorageQueryAndOwnership(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "float.sqlite3")) + "?mode=rwc"
		runStorage(t, func(ctx context.Context) (floatBackend, error) { return sqlite.Open(ctx, dsn) })
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
			t.Fatal("connect float consumer PostgreSQL")
		}
		name := fmt.Sprintf("godj_float_%d_%d", os.Getpid(), time.Now().UnixNano())
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
		runStorage(t, func(ctx context.Context) (floatBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: name})
		})
	})
}

func runStorage(t *testing.T, open func(context.Context) (floatBackend, error)) {
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
	_, native := backend.(*postgres.Backend)
	current := (models.RecordDescriptor{}).Metadata()
	original := current
	original.Fields = slices.Delete(slices.Clone(current.Fields), 2, 3)
	var sources []definition.Source
	for _, migration := range []migrations.Migration{
		{App: "floatref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "floatref", Model: original}}},
		{App: "floatref", Name: "0002_day", Dependencies: []migrations.MigrationKey{{App: "floatref", Name: "0001_initial"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "floatref", ModelName: "record", Field: current.Fields[2], BeforeField: "required"},
			migrations.CreateModel{AppLabel: "floatref", Model: (models.LinkDescriptor{}).Metadata()},
		}},
	} {
		wire, err := definition.Encode(definition.Producer{Name: "float-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "floatref", Name: "0001_initial"}))
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(current.DBTable, []query.Assignment{
		orm.NewAssignment(current.Fields[1], query.String("existing")), orm.NewAssignment(current.Fields[3], query.Float(minimum)), orm.NewAssignment(current.Fields[4], query.Float(minimum)), orm.NewAssignment(current.Fields[5], query.Float(fraction)),
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
			Aggregates         map[string]struct {
				Bits string
				Repr string
			}
			AfterUpdate [][]any  `json:"after_update"`
			AfterRemove []string `json:"after_remove"`
		}
	}
	if err := json.Unmarshal(rawReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" || len(reference.Database.Queries) != 9 || len(reference.Database.Relations) != 9 {
		t.Fatal("incomplete time reference")
	}
	for _, rows := range [][][]any{reference.Database.AfterAdd, reference.Database.Reopened, reference.Database.AfterUpdate} {
		for _, row := range rows {
			if row[1] == nil {
				continue
			}
			number, ok := row[1].(map[string]any)
			if !ok || len(number) != 2 {
				t.Fatal("float reference shape changed")
			}
			bits, ok := number["bits"].(string)
			if !ok || len(bits) != 16 {
				t.Fatal("float reference bits missing")
			}
			if native && row[0] == "negative_zero" {
				if bits != "0000000000000000" {
					t.Fatal("SQLite zero selector changed")
				}
				bits = "8000000000000000"
			}
			row[1] = bits
		}
	}
	if len(reference.Database.AfterRemove) != 12 || !slices.Equal(reference.Database.AfterRemove[8:], []string{"special_nan", "special_positive_infinity", "special_negative_infinity", "special_negative_zero"}) {
		t.Fatal("independent special row roster changed")
	}
	// Specials use a rolled-back probe here because SQLite NaN must be rejected.
	// Compare the common eight-row migration lifecycle separately.
	reference.Database.AfterRemove = reference.Database.AfterRemove[:8]
	observe := func() [][]any {
		rows, err := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := make([][]any, len(rows))
		for i, row := range rows {
			var day any
			if row.Effort != nil {
				day = fmt.Sprintf("%016x", math.Float64bits(*row.Effort))
			}
			result[i] = []any{row.Label, day}
		}
		return result
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterAdd) {
		t.Fatal("time addition changed existing row")
	}
	for _, input := range []models.RecordCreate{models.NewRecordCreate("minimum", minimum).WithEffort(minimum), models.NewRecordCreate("negative_zero", math.Copysign(0, -1)).WithEffort(math.Copysign(0, -1)), models.NewRecordCreate("zero", 0).WithEffort(0), models.NewRecordCreate("subnormal", math.SmallestNonzeroFloat64).WithEffort(math.SmallestNonzeroFloat64), models.NewRecordCreate("fraction", fraction).WithEffort(fraction), models.NewRecordCreate("maximum", maximum).WithEffort(maximum), models.NewRecordCreate("null", 0)} {
		row, err := models.RecordObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		if row.Origin == nil || *row.Origin != (float64(0)) || row.Scheduled != fraction {
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
		{"exact", "effort", fraction, models.RecordFields.Effort.Exact(fraction), false},
		{"not_exact", "effort", fraction, orm.Not(models.RecordFields.Effort.Exact(fraction)), true},
		{"null", "effort__isnull", true, models.RecordFields.Effort.IsNull(true), false},
		{"gt", "effort__gt", fraction, models.RecordFields.Effort.GreaterThan(fraction), false},
		{"gte", "effort__gte", fraction, models.RecordFields.Effort.GreaterThanOrEqual(fraction), false},
		{"lt", "effort__lt", fraction, models.RecordFields.Effort.LessThan(fraction), false},
		{"lte", "effort__lte", fraction, models.RecordFields.Effort.LessThanOrEqual(fraction), false},
		{"in_null", "effort__in", []any{fraction, nil}, models.RecordFields.Effort.In(fraction), false},
		{"not_in_null", "effort__in", []any{fraction, nil}, orm.And(orm.Not(models.RecordFields.Effort.In(fraction)), models.RecordFields.Effort.IsNull(false)), true},
	} {
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic float: %v", err)
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
	for _, value := range []any{"1.5", int64(1), float32(1), []string{"1.5"}} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "effort", Value: value}}); err == nil {
			t.Fatal("dynamic float coerced invalid input")
		}
	}
	all := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	*rows[1].Effort = maximum
	*rows[1].Origin = maximum
	again, err := all.All(ctx)
	if err != nil || again[1].Effort == nil || *again[1].Effort != minimum || *again[1].Origin != (float64(0)) {
		t.Fatal("float pointer escaped query cache")
	}
	projected, err := orm.SelectInto(ctx, all, orm.Project1(models.RecordFields.Effort, func(value *float64) *float64 { return value }))
	if err != nil || len(projected) != 8 || projected[0] != nil || projected[1] == nil || *projected[1] != minimum || projected[7] != nil {
		t.Fatal("float projection lost presence")
	}
	*projected[1] = maximum
	if *again[1].Effort != minimum {
		t.Fatal("projection aliases model")
	}
	type bounds struct {
		Min, Max orm.Optional[float64]
	}
	rangeValue, err := orm.AggregateInto(ctx, all, orm.Aggregate2(orm.Min(models.RecordFields.Effort), orm.Max(models.RecordFields.Effort), func(a, b orm.Optional[float64]) bounds { return bounds{a, b} }))
	if err != nil {
		t.Fatal(err)
	}
	for name, result := range map[string]orm.Optional[float64]{"minimum": rangeValue.Min, "maximum": rangeValue.Max} {
		if value, ok := result.Get(); !ok || fmt.Sprintf("%016x", math.Float64bits(value)) != reference.Database.Aggregates[name].Bits {
			t.Fatal("float aggregate differs from independent DB")
		}
	}
	empty, err := orm.AggregateInto(ctx, all.Filter(models.RecordFields.ID.Exact(-1)), orm.Aggregate1(orm.Max(models.RecordFields.Effort), func(v orm.Optional[float64]) orm.Optional[float64] { return v }))
	if err != nil || empty.Valid() {
		t.Fatal("empty float aggregate invented a value")
	}
	if count, err := all.Filter(models.RecordFields.Effort.ExactField(orm.F[models.Record, float64](models.RecordFields.Required))).Count(ctx); err != nil || count != 6 {
		t.Fatalf("float F comparison: %d %v", count, err)
	}
	if count, err := all.Filter(models.RecordFields.Required.GreaterThanField(orm.F[models.Record, float64](models.RecordFields.Origin))).Count(ctx); err != nil || count != 3 {
		t.Fatalf("float ordered F comparison: %d %v", count, err)
	}
	verifyRelations(t, backend, again, reference.Database.Relations)
	changed, err := models.RecordObjects.Update(ctx, backend, again[5], models.RecordPatch{}.WithEffortNull())
	if err != nil || changed.Effort != nil || again[5].Effort == nil || *again[5].Effort != fraction {
		t.Fatal("null patch mutated caller")
	}
	changed, err = models.RecordObjects.Update(ctx, backend, again[7], models.RecordPatch{}.WithEffort(0.1))
	if err != nil || changed.Effort == nil || *changed.Effort != 0.1 {
		t.Fatal("float patch lost value")
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterUpdate) {
		t.Fatal("float update differs from independent DB")
	}
	*changed.Effort = maximum
	before := (models.RecordDescriptor{}).CloneWriteModel(changed)
	mutator := &countedMutator{Mutator: backend, fail: true}
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Effort)); err == nil || !reflect.DeepEqual(before, changed) {
		t.Fatal("failed Save changed caller")
	}
	mutator.fail = false
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Effort)); err != nil {
		t.Fatal(err)
	}
	changed.Effort = nil
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Label)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.Exact(changed.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Effort == nil || *stored.Effort != maximum {
		t.Fatal("Save mask erased unselected float")
	}
	writes := mutator.writes
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := models.RecordObjects.Create(canceled, mutator, models.NewRecordCreate("canceled", minimum)); !errors.Is(err, context.Canceled) || mutator.writes != writes {
		t.Fatal("canceled float create reached I/O")
	}
	beforeSpecial := observe()
	rollback := errors.New("float special probe rollback")
	err = backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
		for _, number := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			row, err := models.RecordObjects.Create(ctx, session, models.NewRecordCreate("special", number).WithEffort(number))
			if math.IsNaN(number) && !native {
				if err == nil {
					return errors.New("SQLite silently stored NaN")
				}
				if _, err := models.RecordObjects.Using(session).Filter(models.RecordFields.Effort.Exact(number)).Count(ctx); err == nil {
					return errors.New("SQLite silently compared NaN")
				}
				continue
			}
			if err != nil {
				return err
			}
			stored, found, err := models.RecordObjects.Using(session).Filter(models.RecordFields.ID.Exact(row.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
			if err != nil || !found || stored.Effort == nil {
				return errors.New("special Float missing after storage")
			}
			if math.IsNaN(number) {
				if math.Float64bits(*stored.Effort) != 0x7ff8000000000000 {
					return errors.New("NaN scan is not canonical")
				}
			} else if *stored.Effort != number {
				return errors.New("infinity changed")
			}
			if count, err := models.RecordObjects.Using(session).Filter(models.RecordFields.Effort.Exact(number)).Count(ctx); err != nil || count != 1 {
				return errors.New("native special comparison differs")
			}
		}
		queryset := models.RecordObjects.Using(session)
		if native {
			if count, err := queryset.Filter(models.RecordFields.Effort.GreaterThan(math.Inf(1))).Count(ctx); err != nil || count != 1 {
				return errors.New("native NaN ordering changed")
			}
		}
		maximum, err := orm.AggregateInto(ctx, queryset, orm.Aggregate1(orm.Max(models.RecordFields.Effort), func(value orm.Optional[float64]) orm.Optional[float64] { return value }))
		if err != nil {
			return err
		}
		top, valid := maximum.Get()
		if !valid || native && !math.IsNaN(top) || !native && !math.IsInf(top, 1) {
			return errors.New("special Float aggregate changed")
		}
		return rollback
	})
	if !errors.Is(err, rollback) || !reflect.DeepEqual(beforeSpecial, observe()) {
		t.Fatalf("special float rollback changed rows: %v", err)
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

func verifyRelations(t *testing.T, backend floatBackend, records []models.Record, reference map[string][]string) {
	t.Helper()
	ctx := t.Context()
	for _, record := range records {
		if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate(record.Label).WithRecordID(record.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate("fraction_again").WithRecordID(records[5].ID)); err != nil {
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
	link, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "floatref", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	day := related.ModelsLink.Record.Effort
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Link]
		not       bool
	}{
		{"exact", "record__effort", fraction, day.Exact(fraction), false},
		{"not_exact", "record__effort", fraction, orm.Not(day.Exact(fraction)), true},
		{"null", "record__effort__isnull", true, day.IsNull(true), false},
		{"gt", "record__effort__gt", fraction, day.GreaterThan(fraction), false},
		{"gte", "record__effort__gte", fraction, day.GreaterThanOrEqual(fraction), false},
		{"lt", "record__effort__lt", fraction, day.LessThan(fraction), false},
		{"lte", "record__effort__lte", fraction, day.LessThanOrEqual(fraction), false},
		{"in_null", "record__effort__in", []any{fraction, nil}, day.In(fraction), false},
		{"not_in_null", "record__effort__in", []any{fraction, nil}, orm.And(orm.Not(day.In(fraction)), day.IsNull(false)), true},
	} {
		dynamic, err := orm.ParseDynamicRelations(link, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic float relation: %v", err)
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
					t.Fatal("eager float owner presence differs")
				}
				if present && record.Label == "fraction" {
					if record.Effort == nil || *record.Effort != fraction {
						t.Fatal("eager scan lost float")
					}
					snapshot, err := record.Unwrap()
					if err != nil {
						t.Fatal(err)
					}
					*snapshot.Effort = maximum
					again, _, err := row.Record(ctx)
					if err != nil || again != record || again.Effort == nil || *again.Effort != fraction {
						t.Fatal("eager snapshot aliases cached float")
					}
				}
			}
			if !slices.Equal(labels, reference[test.name]) {
				t.Fatalf("related %s=%v want %v", test.name, labels, reference[test.name])
			}
		}
	}
}
