package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/progresshans/godj/decimal"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"example.com/godj-decimal/models"
	"example.com/godj-decimal/project"
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
var minimum = decimal.Decimal{Coefficient: "-999999999999", Exponent: -2}
var fraction = decimal.Decimal{Coefficient: "15", Exponent: -1}
var maximum = decimal.Decimal{Coefficient: "999999999999", Exponent: -2}

func number(t *testing.T, raw string) decimal.Decimal {
	t.Helper()
	value, err := decimal.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func fixed(t *testing.T, value decimal.Decimal) string {
	t.Helper()
	raw, err := value.Fixed(2)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecimalGeneratedDefaults(t *testing.T) {
	// A model and member named Decimal cannot shadow generated package imports.
	if mutation := models.NewDecimalCreate().WithDecimal(decimal.Decimal{}).BuildCreate(); mutation.Err() != nil {
		t.Fatal(mutation.Err())
	}

	if err := (models.RecordCreate{}).BuildCreate().Err(); err == nil {
		t.Fatal("missing required decimal accepted")
	}
	for _, test := range []struct {
		input       models.RecordCreate
		day, origin query.Value
	}{
		{models.NewRecordCreate("default", minimum), query.Null(), query.Decimal(decimal.Decimal{})},
		{models.NewRecordCreate("explicit", maximum).WithCost(fraction).WithOriginNull(), query.Decimal(fraction), query.Null()},
		{models.NewRecordCreate("null", fraction).WithCostNull().WithOrigin(maximum), query.Null(), query.Decimal(maximum)},
	} {
		mutation := test.input.BuildCreate()
		if err := mutation.Err(); err != nil {
			t.Fatal(err)
		}
		values := map[string]query.Value{}
		for _, assignment := range mutation.Assignments() {
			values[assignment.Field().Name()] = assignment.Value()
		}
		for name, want := range map[string]query.Value{"cost": test.day, "origin": test.origin, "scheduled": query.Decimal(fraction)} {
			if got, ok := values[name]; !ok || !got.Equal(want) {
				t.Fatalf("%s default/presence changed", name)
			}
		}
	}
	defaults := models.NewDecimalCreate().BuildCreate()
	if defaults.Err() != nil {
		t.Fatal(defaults.Err())
	}
	seenZero := false
	for _, assignment := range defaults.Assignments() {
		value, ok := assignment.Value().Decimal()
		if assignment.Field().Name() == "decimal_value" {
			seenZero = ok && value.Coefficient == "-0"
		}
	}
	if !seenZero {
		t.Fatal("generated decimal default lost signed zero")
	}
	for _, value := range []decimal.Decimal{number(t, "1.235"), number(t, "10000000000"), {Coefficient: "NaN"}} {
		if err := models.NewRecordCreate("bad", value).BuildCreate().Err(); err == nil {
			t.Fatal("invalid decimal accepted")
		}
	}
	normalized := decimal.Decimal{Coefficient: "001500", Exponent: -3}
	if err := models.NewRecordCreate("canonical", normalized).BuildCreate().Err(); err != nil {
		t.Fatal(err)
	}

}

type decimalBackend interface {
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

func TestDecimalStorageQueryAndOwnership(t *testing.T) {
	forEachDecimalBackend(t, runStorage)
}

func forEachDecimalBackend(t *testing.T, run func(*testing.T, func(context.Context) (decimalBackend, error))) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "decimal.sqlite3")) + "?mode=rwc"
		run(t, func(ctx context.Context) (decimalBackend, error) { return sqlite.Open(ctx, dsn) })
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
			t.Fatal("connect decimal consumer PostgreSQL")
		}
		name := fmt.Sprintf("godj_decimal_%d_%d", os.Getpid(), time.Now().UnixNano())
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
		run(t, func(ctx context.Context) (decimalBackend, error) {
			return postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: name})
		})
	})
}

func runStorage(t *testing.T, open func(context.Context) (decimalBackend, error)) {
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
		{App: "decimalref", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "decimalref", Model: original}}},
		{App: "decimalref", Name: "0002_day", Dependencies: []migrations.MigrationKey{{App: "decimalref", Name: "0001_initial"}}, Operations: []migrations.Operation{
			migrations.AddField{AppLabel: "decimalref", ModelName: "record", Field: current.Fields[2], BeforeField: "required"},
			migrations.CreateModel{AppLabel: "decimalref", Model: (models.LinkDescriptor{}).Metadata()},
			migrations.CreateModel{AppLabel: "decimalref", Model: (models.PreciseDescriptor{}).Metadata()},
			migrations.CreateModel{AppLabel: "decimalref", Model: (models.BoundaryDescriptor{}).Metadata()},
		}},
	} {
		wire, err := definition.Encode(definition.Producer{Name: "decimal-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "decimalref", Name: "0001_initial"}))
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(current.DBTable, []query.Assignment{
		orm.NewAssignment(current.Fields[1], query.String("existing")), orm.NewAssignment(current.Fields[3], query.Decimal(minimum)), orm.NewAssignment(current.Fields[4], query.Decimal(minimum)), orm.NewAssignment(current.Fields[5], query.Decimal(fraction)),
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
				Text string
			}
			AfterUpdate [][]any  `json:"after_update"`
			AfterRemove []string `json:"after_remove"`
		}
	}
	if err := json.Unmarshal(rawReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" || len(reference.Database.Queries) != 9 || len(reference.Database.Relations) != 9 {
		t.Fatal("incomplete decimal reference")
	}
	for _, rows := range [][][]any{reference.Database.AfterAdd, reference.Database.Reopened, reference.Database.AfterUpdate} {
		for _, row := range rows {
			if row[1] == nil {
				continue
			}
			object, ok := row[1].(map[string]any)
			if !ok || len(object) != 4 {
				t.Fatal("decimal reference shape changed")
			}
			text, ok := object["text"].(string)
			if !ok {
				t.Fatal("decimal reference text missing")
			}
			row[1] = text
		}
	}
	if len(reference.Database.AfterRemove) != 8 {
		t.Fatal("independent lifecycle roster changed")
	}
	observe := func() [][]any {
		rows, err := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := make([][]any, len(rows))
		for i, row := range rows {
			var day any
			if row.Cost != nil {
				day = fixed(t, *row.Cost)
			}
			result[i] = []any{row.Label, day}
		}
		return result
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterAdd) {
		t.Fatal("decimal addition changed existing row")
	}
	for _, input := range []models.RecordCreate{models.NewRecordCreate("minimum", minimum).WithCost(minimum), models.NewRecordCreate("negative_zero", decimal.Decimal{Coefficient: "-0"}).WithCost(decimal.Decimal{Coefficient: "-0"}), models.NewRecordCreate("zero", decimal.Decimal{}).WithCost(decimal.Decimal{}), models.NewRecordCreate("small", decimal.Decimal{Coefficient: "1", Exponent: -2}).WithCost(decimal.Decimal{Coefficient: "1", Exponent: -2}), models.NewRecordCreate("fraction", fraction).WithCost(fraction), models.NewRecordCreate("maximum", maximum).WithCost(maximum), models.NewRecordCreate("null", decimal.Decimal{})} {
		row, err := models.RecordObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		if row.Origin == nil || *row.Origin != (decimal.Decimal{}) || row.Scheduled != fraction {
			t.Fatal("decimal defaults changed")
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
		t.Fatal("decimal changed across fresh connection")
	}
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Record]
		not       bool
	}{
		{"exact", "cost", fraction, models.RecordFields.Cost.Exact(fraction), false},
		{"not_exact", "cost", fraction, orm.Not(models.RecordFields.Cost.Exact(fraction)), true},
		{"null", "cost__isnull", true, models.RecordFields.Cost.IsNull(true), false},
		{"gt", "cost__gt", fraction, models.RecordFields.Cost.GreaterThan(fraction), false},
		{"gte", "cost__gte", fraction, models.RecordFields.Cost.GreaterThanOrEqual(fraction), false},
		{"lt", "cost__lt", fraction, models.RecordFields.Cost.LessThan(fraction), false},
		{"lte", "cost__lte", fraction, models.RecordFields.Cost.LessThanOrEqual(fraction), false},
		{"in_null", "cost__in", []any{fraction, nil}, models.RecordFields.Cost.In(fraction), false},
		{"not_in_null", "cost__in", []any{fraction, nil}, orm.And(orm.Not(models.RecordFields.Cost.In(fraction)), models.RecordFields.Cost.IsNull(false)), true},
	} {
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic decimal: %v", err)
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
	for _, value := range []any{"1.5", int64(1), float32(1), float64(1.5), []string{"1.5"}} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "cost", Value: value}}); err == nil {
			t.Fatal("dynamic decimal coerced invalid input")
		}
	}
	all := models.RecordObjects.Using(backend).OrderBy(models.RecordFields.ID.Asc())
	rows, err := all.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	*rows[1].Cost = maximum
	*rows[1].Origin = maximum
	again, err := all.All(ctx)
	if err != nil || again[1].Cost == nil || *again[1].Cost != minimum || *again[1].Origin != (decimal.Decimal{}) {
		t.Fatal("decimal pointer escaped query cache")
	}
	projected, err := orm.SelectInto(ctx, all, orm.Project1(models.RecordFields.Cost, func(value *decimal.Decimal) *decimal.Decimal { return value }))
	if err != nil || len(projected) != 8 || projected[0] != nil || projected[1] == nil || *projected[1] != minimum || projected[7] != nil {
		t.Fatal("decimal projection lost presence")
	}
	*projected[1] = maximum
	if *again[1].Cost != minimum {
		t.Fatal("projection aliases model")
	}
	type bounds struct {
		Min, Max orm.Optional[decimal.Decimal]
	}
	rangeValue, err := orm.AggregateInto(ctx, all, orm.Aggregate2(orm.Min(models.RecordFields.Cost), orm.Max(models.RecordFields.Cost), func(a, b orm.Optional[decimal.Decimal]) bounds { return bounds{a, b} }))
	if err != nil {
		t.Fatal(err)
	}
	for name, result := range map[string]orm.Optional[decimal.Decimal]{"minimum": rangeValue.Min, "maximum": rangeValue.Max} {
		if value, ok := result.Get(); !ok || !value.Equal(number(t, reference.Database.Aggregates[name].Text)) {
			t.Fatal("decimal aggregate differs from independent DB")
		}
	}
	empty, err := orm.AggregateInto(ctx, all.Filter(models.RecordFields.ID.Exact(-1)), orm.Aggregate1(orm.Max(models.RecordFields.Cost), func(v orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return v }))
	if err != nil || empty.Valid() {
		t.Fatal("empty decimal aggregate invented a value")
	}
	if count, err := all.Filter(models.RecordFields.Cost.ExactField(orm.F[models.Record, decimal.Decimal](models.RecordFields.Required))).Count(ctx); err != nil || count != 6 {
		t.Fatalf("decimal F comparison: %d %v", count, err)
	}
	if count, err := all.Filter(models.RecordFields.Required.GreaterThanField(orm.F[models.Record, decimal.Decimal](models.RecordFields.Origin))).Count(ctx); err != nil || count != 3 {
		t.Fatalf("decimal ordered F comparison: %d %v", count, err)
	}
	verifyRelations(t, backend, again, reference.Database.Relations)
	changed, err := models.RecordObjects.Update(ctx, backend, again[5], models.RecordPatch{}.WithCostNull())
	if err != nil || changed.Cost != nil || again[5].Cost == nil || *again[5].Cost != fraction {
		t.Fatal("null patch mutated caller")
	}
	changed, err = models.RecordObjects.Update(ctx, backend, again[7], models.RecordPatch{}.WithCost(number(t, "0.1")))
	if err != nil || changed.Cost == nil || *changed.Cost != number(t, "0.1") {
		t.Fatal("decimal patch lost value")
	}
	if !reflect.DeepEqual(observe(), reference.Database.AfterUpdate) {
		t.Fatal("decimal update differs from independent DB")
	}
	*changed.Cost = maximum
	before := (models.RecordDescriptor{}).CloneWriteModel(changed)
	mutator := &countedMutator{Mutator: backend, fail: true}
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Cost)); err == nil || !reflect.DeepEqual(before, changed) {
		t.Fatal("failed Save changed caller")
	}
	mutator.fail = false
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Cost)); err != nil {
		t.Fatal(err)
	}
	changed.Cost = nil
	if err := models.RecordObjects.Save(ctx, mutator, &changed, models.RecordUpdateFields(models.RecordFields.Label)); err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.Exact(changed.ID)).OrderBy(models.RecordFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Cost == nil || *stored.Cost != maximum {
		t.Fatal("Save mask erased unselected decimal")
	}
	writes := mutator.writes
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := models.RecordObjects.Create(canceled, mutator, models.NewRecordCreate("canceled", minimum)); !errors.Is(err, context.Canceled) || mutator.writes != writes {
		t.Fatal("canceled decimal create reached I/O")
	}
	beforeRollback := observe()
	rollback := errors.New("decimal rollback probe")
	err = backend.(db.Atomic).Atomic(ctx, func(session db.Session) error {
		if _, err := models.RecordObjects.Update(ctx, session, again[1], models.RecordPatch{}.WithCost(fraction)); err != nil {
			return err
		}
		if _, err := models.RecordObjects.Create(ctx, session, models.NewRecordCreate("rollback", fraction)); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) || !reflect.DeepEqual(beforeRollback, observe()) {
		t.Fatalf("decimal rollback changed rows: %v", err)
	}
	for _, value := range []decimal.Decimal{number(t, "1.235"), number(t, "10000000000"), {Coefficient: "NaN"}} {
		if _, err := models.RecordObjects.Create(ctx, mutator, models.NewRecordCreate("invalid", value)); err == nil || mutator.writes != writes {
			t.Fatal("invalid decimal write reached I/O")
		}
	}
	if count, err := all.Filter(models.RecordFields.Cost.LessThan(number(t, "10000000000"))).Count(ctx); err != nil || count != 6 {
		t.Fatalf("comparison literal incorrectly constrained to field domain: %d %v", count, err)
	}
	verifyPrecision(t, backend)

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

func verifyRelations(t *testing.T, backend decimalBackend, records []models.Record, reference map[string][]string) {
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
	link, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "decimalref", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	day := related.ModelsLink.Record.Cost
	for _, test := range []struct {
		name, key string
		value     any
		typed     orm.Predicate[models.Link]
		not       bool
	}{
		{"exact", "record__cost", fraction, day.Exact(fraction), false},
		{"not_exact", "record__cost", fraction, orm.Not(day.Exact(fraction)), true},
		{"null", "record__cost__isnull", true, day.IsNull(true), false},
		{"gt", "record__cost__gt", fraction, day.GreaterThan(fraction), false},
		{"gte", "record__cost__gte", fraction, day.GreaterThanOrEqual(fraction), false},
		{"lt", "record__cost__lt", fraction, day.LessThan(fraction), false},
		{"lte", "record__cost__lte", fraction, day.LessThanOrEqual(fraction), false},
		{"in_null", "record__cost__in", []any{fraction, nil}, day.In(fraction), false},
		{"not_in_null", "record__cost__in", []any{fraction, nil}, orm.And(orm.Not(day.In(fraction)), day.IsNull(false)), true},
	} {
		dynamic, err := orm.ParseDynamicRelations(link, nil, []orm.LookupInput{{Key: test.key, Value: test.value}})
		if err != nil || len(dynamic) != 1 {
			t.Fatalf("dynamic decimal relation: %v", err)
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
					t.Fatal("eager decimal owner presence differs")
				}
				if present && record.Label == "fraction" {
					if record.Cost == nil || *record.Cost != fraction {
						t.Fatal("eager scan lost decimal")
					}
					snapshot, err := record.Unwrap()
					if err != nil {
						t.Fatal(err)
					}
					*snapshot.Cost = maximum
					again, _, err := row.Record(ctx)
					if err != nil || again != record || again.Cost == nil || *again.Cost != fraction {
						t.Fatal("eager snapshot aliases cached decimal")
					}
				}
			}
			if !slices.Equal(labels, reference[test.name]) {
				t.Fatalf("related %s=%v want %v", test.name, labels, reference[test.name])
			}
		}
	}
}

func verifyPrecision(t *testing.T, backend decimalBackend) {
	t.Helper()
	ctx := t.Context()
	inputs := []string{"-999999999999999999.999999999999", "-123456789012345678.123456789012", "-0.000000000001", "0", "0.000000000001", "9007199254740993", "123456789012345678.123456789012", "999999999999999999.999999999999"}
	for _, input := range inputs {
		value := number(t, input)
		if _, err := models.PreciseObjects.Create(ctx, backend, models.NewPreciseCreate(value, value)); err != nil {
			t.Fatal(err)
		}
	}
	queryset := models.PreciseObjects.Using(backend).OrderBy(models.PreciseFields.Value.Asc())
	rows, err := queryset.All(ctx)
	if err != nil || len(rows) != len(inputs) {
		t.Fatalf("precise rows: %v", err)
	}
	for i, row := range rows {
		if row.Value != number(t, inputs[i]) || row.Mirror != row.Value {
			t.Fatalf("precision loss at %s: %s / %s", inputs[i], row.Value, row.Mirror)
		}
	}
	if count, err := queryset.Filter(models.PreciseFields.Value.ExactField(orm.F[models.Precise, decimal.Decimal](models.PreciseFields.Mirror))).Count(ctx); err != nil || count != int64(len(inputs)) {
		t.Fatalf("cross-scale equality: %d %v", count, err)
	}
	literal := number(t, "123456789012345678.1234567890125")
	if count, err := queryset.Filter(models.PreciseFields.Value.GreaterThan(literal)).Count(ctx); err != nil || count != 1 {
		t.Fatalf("exact comparison between adjacent storage values: %d %v", count, err)
	}
	row := rows[4]
	if _, err := models.PreciseObjects.Update(ctx, backend, row, models.PrecisePatch{}.WithMirror(literal)); err != nil {
		t.Fatal(err)
	}
	if count, err := queryset.Filter(models.PreciseFields.Value.LessThanField(orm.F[models.Precise, decimal.Decimal](models.PreciseFields.Mirror))).Count(ctx); err != nil || count != 1 {
		t.Fatalf("cross-scale order: %d %v", count, err)
	}
	maximum, err := orm.AggregateInto(ctx, queryset, orm.Aggregate1(orm.Max(models.PreciseFields.Value), func(value orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return value }))
	if got, valid := maximum.Get(); err != nil || !valid || got != number(t, inputs[len(inputs)-1]) {
		t.Fatalf("exact numeric maximum: %s %v", got, err)
	}
	large := number(t, strings.Repeat("9", 500)+"."+strings.Repeat("9", 500))
	tiny := number(t, "1e-1000")
	created, err := models.BoundaryObjects.Create(ctx, backend, models.NewBoundaryCreate(large, tiny))
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := models.BoundaryObjects.Using(backend).Filter(models.BoundaryFields.ID.Exact(created.ID)).OrderBy(models.BoundaryFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Large != large || stored.Tiny != tiny {
		t.Fatalf("1000-digit boundary changed: %v", err)
	}
	if count, err := models.BoundaryObjects.Using(backend).Filter(models.BoundaryFields.Large.LessThan(number(t, "1e1000"))).Count(ctx); err != nil || count != 1 {
		t.Fatalf("bounded model exponent comparison: %d %v", count, err)
	}
}
