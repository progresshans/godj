package consumer_test

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"example.com/godj-membership/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed reference.json
var reference []byte

func fixture(t *testing.T) *sqlite.Backend {
	t.Helper()
	backend, err := sqlite.OpenMemory(t.Context(), "membership-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	encoded, err := definition.Encode(definition.Producer{Name: "membership-consumer", Version: "1"}, migrations.Migration{
		App: "membership", Name: "0001_initial", Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "membership", Model: (models.EntryDescriptor{}).Metadata()},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "membership-consumer", Document: encoded})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 9, 19, 3, 34, 56, 123456000, time.UTC)
	maximum := time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	for index, input := range []models.EntryCreate{
		models.NewEntryCreate("alpha"),
		models.NewEntryCreate("beta").WithNote("beta").WithRank(-1).WithAt(instant).WithActive(true),
		models.NewEntryCreate("gamma").WithNote("").WithRank(0).WithAt(time.Time{}),
		models.NewEntryCreate("delta").WithNote("delta").WithRank(math.MaxInt64).WithAt(maximum).WithActive(true),
	} {
		created, err := models.EntryObjects.Create(t.Context(), backend, input)
		if err != nil || created.ID != int64(index+1) {
			t.Fatalf("seed identity: %v", err)
		}
	}
	return backend
}

type inputValue struct {
	Kind     string
	Integer  int64
	String   string
	Boolean  bool
	DateTime string
}

func (value inputValue) raw(t *testing.T) any {
	t.Helper()
	switch value.Kind {
	case "null":
		return nil
	case "integer":
		return value.Integer
	case "string":
		return value.String
	case "boolean":
		return value.Boolean
	case "datetime":
		instant, err := time.Parse(time.RFC3339Nano, value.DateTime)
		if err != nil {
			t.Fatal(err)
		}
		return instant
	default:
		t.Fatal("unknown independent reference input kind")
	}
	return nil
}

func compose(t *testing.T, predicate orm.Predicate[models.Entry], operation string) orm.Predicate[models.Entry] {
	t.Helper()
	switch operation {
	case "plain":
		return predicate
	case "not":
		return orm.Not(predicate)
	case "or":
		return orm.Or(predicate, models.EntryFields.Name.Exact("alpha"))
	case "and":
		return orm.And(predicate, models.EntryFields.Active.Exact(true))
	default:
		t.Fatal("unknown reference composition")
	}
	return predicate
}

func typedMembership(t *testing.T, field string, values []any) orm.Predicate[models.Entry] {
	t.Helper()
	switch field {
	case "id", "rank":
		items := make([]int64, len(values))
		for i, value := range values {
			items[i] = value.(int64)
		}
		if field == "id" {
			return models.EntryFields.ID.In(items...)
		}
		return models.EntryFields.Rank.In(items...)
	case "name", "note":
		items := make([]string, len(values))
		for i, value := range values {
			items[i] = value.(string)
		}
		if field == "name" {
			return models.EntryFields.Name.In(items...)
		}
		return models.EntryFields.Note.In(items...)
	case "active":
		items := make([]bool, len(values))
		for i, value := range values {
			items[i] = value.(bool)
		}
		return models.EntryFields.Active.In(items...)
	case "at":
		items := make([]time.Time, len(values))
		for i, value := range values {
			items[i] = value.(time.Time)
		}
		return models.EntryFields.At.In(items...)
	default:
		t.Fatal("unknown reference field")
	}
	return orm.Predicate[models.Entry]{}
}

func TestGeneratedMembershipMatchesReference(t *testing.T) {
	backend := fixture(t)
	var observations struct {
		Django, Timezone, Backend string
		Cases                     []struct {
			Name, Field, Composition string
			Values                   []inputValue
			IDs                      []int64
			Queries                  uint64
		}
	}
	if err := json.Unmarshal(reference, &observations); err != nil {
		t.Fatal(err)
	}
	if observations.Django != "6.1" || observations.Timezone != "UTC" || observations.Backend != "sqlite" || len(observations.Cases) != 120 {
		t.Fatal("reference roster is incomplete")
	}
	seen := make(map[string]bool)
	typedCases := 0
	for _, observation := range observations.Cases {
		if seen[observation.Name] {
			t.Fatal("duplicate reference case")
		}
		seen[observation.Name] = true
		t.Run(observation.Name, func(t *testing.T) {
			values := make([]any, len(observation.Values))
			hasNull := false
			for index, input := range observation.Values {
				values[index] = input.raw(t)
				hasNull = hasNull || values[index] == nil
			}
			predicates, err := orm.ParseDynamic(models.EntryDescriptor{}, nil, []orm.LookupInput{{Key: observation.Field + "__in", Value: values}})
			if err != nil || len(predicates) != 1 {
				t.Fatalf("dynamic membership: %v", err)
			}
			check := func(predicate orm.Predicate[models.Entry]) {
				t.Helper()
				before := backend.QueryCount()
				entries, err := models.EntryObjects.Using(backend).Filter(compose(t, predicate, observation.Composition)).OrderBy(models.EntryFields.ID.Asc()).All(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				ids := make([]int64, len(entries))
				for index, entry := range entries {
					ids[index] = entry.ID
				}
				if !reflect.DeepEqual(ids, observation.IDs) || backend.QueryCount()-before != observation.Queries {
					t.Fatalf("membership = %v, queries %d; want %v, queries %d", ids, backend.QueryCount()-before, observation.IDs, observation.Queries)
				}
			}
			check(predicates[0])
			// Concrete typed lists cannot contain NULL. Dynamic []any retains
			// that explicit input; the remaining cases also exercise FieldSet.
			if !hasNull {
				check(typedMembership(t, observation.Field, values))
				typedCases++
			}
		})
	}
	if typedCases != 84 {
		t.Fatalf("typed reference cases = %d, want 84", typedCases)
	}
}

func TestGeneratedMembershipEvaluationAndOwnership(t *testing.T) {
	backend := fixture(t)
	ctx := t.Context()
	empty := func() orm.QuerySet[models.Entry] {
		return models.EntryObjects.Using(backend).Filter(models.EntryFields.ID.In())
	}
	before := backend.QueryCount()
	if rows, err := empty().All(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("empty All: %v", err)
	}
	if count, err := empty().Count(ctx); err != nil || count != 0 {
		t.Fatalf("empty Count: %v", err)
	}
	if exists, err := empty().Exists(ctx); err != nil || exists {
		t.Fatalf("empty Exists: %v", err)
	}
	if _, found, err := empty().OrderBy(models.EntryFields.ID.Asc()).First(ctx); err != nil || found {
		t.Fatalf("empty First: %v", err)
	}
	if _, _, err := empty().First(ctx); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnorderedQuery}) {
		t.Fatal("empty First bypassed ordering validation")
	}
	if _, _, err := empty().At(ctx, -1); err == nil {
		t.Fatal("empty At bypassed index validation")
	}
	called := false
	if err := empty().Iterate(ctx, func(models.Entry) error { called = true; return nil }); err != nil || called {
		t.Fatal("empty iterator called its callback")
	}
	if err := empty().Iterate(ctx, nil); err == nil {
		t.Fatal("empty iterator accepted nil callback")
	}
	projected, err := orm.SelectInto(ctx, empty(), orm.Project1(models.EntryFields.Name, func(value string) string { return value }))
	if err != nil || len(projected) != 0 {
		t.Fatalf("empty projection: %v", err)
	}
	type summary struct {
		Count int64
		Min   orm.Optional[int64]
	}
	stats, err := orm.AggregateInto(ctx, empty(), orm.Aggregate2(orm.CountRows[models.Entry](), orm.Min(models.EntryFields.Rank), func(count int64, min orm.Optional[int64]) summary { return summary{count, min} }))
	if err != nil || stats.Count != 0 || stats.Min.Valid() {
		t.Fatalf("empty aggregate invented a value: %v", err)
	}
	maximum, err := orm.AggregateInto(ctx, empty(), orm.Aggregate1(orm.Max(models.EntryFields.At), func(value orm.Optional[time.Time]) orm.Optional[time.Time] { return value }))
	if err != nil || maximum.Valid() {
		t.Fatalf("empty time aggregate invented an epoch: %v", err)
	}
	if backend.QueryCount() != before {
		t.Fatal("empty evaluations executed SQL")
	}
	var saved db.Session
	if err := backend.Atomic(ctx, func(session db.Session) error {
		saved = session
		count, err := models.EntryObjects.Using(session).Filter(models.EntryFields.ID.In()).Count(ctx)
		if err != nil || count != 0 {
			return errors.New("empty transaction count failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if backend.QueryCount() != before {
		t.Fatal("empty transaction query executed SELECT")
	}
	if _, err := saved.Query(ctx, empty().Plan()); err == nil {
		t.Fatal("empty query bypassed expired session validation")
	}

	names := []string{"beta"}
	cached := models.EntryObjects.Using(backend).Filter(models.EntryFields.Name.In(names...))
	names[0] = "gamma"
	first, err := cached.All(ctx)
	if err != nil || len(first) != 1 || first[0].ID != 2 {
		t.Fatal("typed IN retained caller slice storage")
	}
	*first[0].Note = "caller mutation"
	before = backend.QueryCount()
	again, err := cached.All(ctx)
	if err != nil || len(again) != 1 || *again[0].Note != "beta" || backend.QueryCount() != before {
		t.Fatal("membership cache lost ownership or repeated SQL")
	}
	if _, err := cached.Filter(models.EntryFields.Active.Exact(true)).All(ctx); err != nil || backend.QueryCount() != before+1 {
		t.Fatal("derived membership query reused the old cache")
	}
	raw := []any{"delta"}
	predicates, err := orm.ParseDynamic(models.EntryDescriptor{}, nil, []orm.LookupInput{{Key: "name__in", Value: raw}})
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = "alpha"
	rows, err := models.EntryObjects.Using(backend).Filter(predicates...).All(ctx)
	if err != nil || len(rows) != 1 || rows[0].ID != 4 {
		t.Fatal("dynamic IN retained caller slice storage")
	}
	for _, predicate := range []orm.Predicate[models.Entry]{models.EntryFields.Rank.In(math.MinInt64, math.MaxInt64), models.EntryFields.At.In(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC))} {
		rows, err := models.EntryObjects.Using(backend).Filter(predicate).All(ctx)
		if err != nil || len(rows) != 1 || rows[0].ID != 4 {
			t.Fatal("scalar boundary membership failed")
		}
	}
	before = backend.QueryCount()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := empty().All(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("empty result bypassed cancellation")
	}
	if _, err := empty().All(nil); err == nil {
		t.Fatal("empty result accepted nil context")
	}
	if backend.QueryCount() != before {
		t.Fatal("invalid context executed SQL")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := empty().All(ctx); err == nil {
		t.Fatal("empty result bypassed closed backend")
	}
}

func TestGeneratedMembershipSessionLifetime(t *testing.T) {
	for _, mode := range []string{"atomic", "coordinated", "relation"} {
		t.Run(mode, func(t *testing.T) {
			backend := fixture(t)
			run := backend.Atomic
			if mode == "coordinated" {
				run = backend.CoordinatedAtomic
			} else if mode == "relation" {
				run = func(ctx context.Context, callback func(db.Session) error) error {
					return backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
				}
			}
			shape, err := query.NewAggregateResult(query.CountAllResult())
			if err != nil {
				t.Fatal(err)
			}
			plan, err := models.EntryObjects.Using(backend).Filter(models.EntryFields.ID.In()).Plan().WithResultShape(shape)
			if err != nil {
				t.Fatal(err)
			}
			var retained db.Rows
			var retainedSession db.Session
			before := backend.QueryCount()
			if err := run(t.Context(), func(session db.Session) error {
				retainedSession = session
				var err error
				retained, err = session.Query(context.Background(), plan)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if retained.Next() || !errors.Is(retained.Err(), sql.ErrTxDone) {
				t.Fatal("synthetic aggregate outlived its transaction")
			}
			if err := retained.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := retainedSession.Query(t.Context(), plan); err == nil {
				t.Fatal("expired transaction accepted empty query")
			}
			transactionContext, cancel := context.WithCancel(t.Context())
			defer cancel()
			err = run(transactionContext, func(session db.Session) error {
				rows, err := session.Query(context.Background(), plan)
				if err != nil {
					return err
				}
				defer rows.Close()
				if !rows.Next() {
					return errors.New("empty aggregate lost its row")
				}
				cancel()
				var count int64
				if err := rows.Scan(&count); !errors.Is(err, context.Canceled) {
					return errors.New("synthetic cursor ignored transaction cancellation")
				}
				if _, err := session.Query(context.Background(), plan); !errors.Is(err, context.Canceled) {
					return errors.New("detached query context bypassed transaction cancellation")
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || backend.QueryCount() != before {
				t.Fatalf("empty transaction cancellation/I/O: %v", err)
			}
		})
	}
}

func TestGeneratedMembershipRejectsInvalidInputs(t *testing.T) {
	backend := fixture(t)
	for _, test := range []struct {
		field string
		value any
	}{
		{"name", []int{}}, {"rank", []string{}}, {"active", []time.Time{}}, {"at", []bool{}},
		{"id", nil}, {"id", int64(1)}, {"id", []uint64{1}}, {"id", []any{int64(1), "2"}},
		{"at", []any{time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}}, {"note", []*string{nil}},
	} {
		_, err := orm.ParseDynamic(models.EntryDescriptor{}, nil, []orm.LookupInput{{Key: test.field + "__in", Value: test.value}})
		if !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue}) {
			t.Fatalf("invalid dynamic membership accepted or misclassified: %v", err)
		}
	}
	seen := false
	_, err := orm.ParseDynamic(models.EntryDescriptor{}, func(field ir.Field, lookup query.Lookup) bool {
		seen = field.Name == "rank" && lookup == query.LookupIn
		return false
	}, []orm.LookupInput{{Key: "rank__in", Value: "invalid"}})
	if !seen || !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeDisallowedLookup}) {
		t.Fatal("membership policy did not precede value parsing")
	}
	before := backend.QueryCount()
	bad := orm.NewIntegerField[models.Entry](ir.Field{Name: "missing", GoName: "Missing", Column: "missing", Kind: ir.FieldInteger})
	for _, predicate := range []orm.Predicate[models.Entry]{
		orm.And(models.EntryFields.ID.In(), bad.In()),
		models.EntryFields.At.In(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)),
	} {
		if _, err := models.EntryObjects.Using(backend).Filter(predicate).All(t.Context()); err == nil {
			t.Fatal("empty or invalid membership hid a field/value error")
		}
	}
	if backend.QueryCount() != before {
		t.Fatal("invalid membership executed SQL")
	}
}
