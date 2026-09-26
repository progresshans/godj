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

	"example.com/godj-forward-scalar/models"
	"example.com/godj-forward-scalar/project"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed sqlite_reference.json
var sqliteReference []byte

//go:embed postgres_reference.json
var postgresReference []byte

type resultBackend interface {
	db.Queryer
	db.Mutator
	db.Atomic
	migrationbackend.RevisionFencedBackend
	Close() error
}
type reference struct {
	Fields, Routes, Kinds []string
	Inputs                []struct {
		Label    string
		Required []string
		Nullable []*string
	}
	Holders []struct {
		Primary   int
		Secondary *int
	}
	Entries []struct {
		Label     string
		Primary   int
		Secondary *int
	}
	Observations []struct {
		Name, Route      string
		Distinct, Sliced bool
		Count            int
		Rows             [][]json.RawMessage
	}
	Orderings []struct {
		Route, Field                 string
		Descending, Distinct, Sliced bool
		Count                        int64
		Rows, Projected              []int64
	}
	Distinct []struct {
		Route, Field string
		Values       []json.RawMessage
	}
	JSONNulls []struct {
		Route, Field string
		Nulls        []int64 `json:"sql_null_or_absent"`
	} `json:"json_nulls"`
}
type projectedRow struct {
	ID    int64
	Value any
}
type scalarSelection struct {
	ascending, descending orm.Ordering[models.Entry]
	rows                  func(context.Context, orm.QuerySet[models.Entry]) ([]projectedRow, error)
	values                func(context.Context, orm.QuerySet[models.Entry]) ([]any, error)
}

func TestForwardScalarSelections(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		backend, err := sqlite.Open(t.Context(), "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "results.sqlite3"))+"?mode=rwc")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := backend.Close(); err != nil {
				t.Error(err)
			}
		})
		runResults(t, backend, false)
	})
	url := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if url == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL is absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), url)
		if err != nil {
			t.Fatal("connect scalar result PostgreSQL")
		}
		schema := fmt.Sprintf("godj_scalar_results_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{schema}.Sanitize()
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
		backend, err := postgres.Open(t.Context(), postgres.Config{URL: url, Schema: schema})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := backend.Close(); err != nil {
				t.Error(err)
			}
		})
		runResults(t, backend, true)
	})
}

func runResults(t *testing.T, backend resultBackend, native bool) {
	t.Helper()
	ctx := t.Context()
	raw := sqliteReference
	if native {
		raw = postgresReference
	}
	var expected reference
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected.Kinds) != 11 || len(expected.Fields) != 22 || len(expected.Routes) != 4 || len(expected.Observations) != 96 || len(expected.Distinct) != 88 || len(expected.JSONNulls) != 8 || len(expected.Orderings) != 704 {
		t.Fatal("incomplete scalar reference")
	}
	migration := migrations.Migration{App: "scalarref", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "scalarref", Model: (models.DatumDescriptor{}).Metadata()}, migrations.CreateModel{AppLabel: "scalarref", Model: (models.HolderDescriptor{}).Metadata()}, migrations.CreateModel{AppLabel: "scalarref", Model: (models.EntryDescriptor{}).Metadata()},
	}}
	wire, err := definition.Encode(definition.Producer{Name: "scalar-results", Version: "1"}, migration)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "scalarref/0001_initial.json", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	data := make([]models.Datum, len(expected.Inputs))
	for i, input := range expected.Inputs {
		row, err := models.DatumObjects.Create(ctx, backend, datumCreate(t, input.Label, input.Required, input.Nullable))
		if err != nil {
			t.Fatal(err)
		}
		data[i] = row
	}
	holders := make([]models.Holder, len(expected.Holders))
	for i, input := range expected.Holders {
		create := models.NewHolderCreate(data[input.Primary].ID)
		if input.Secondary != nil {
			create = create.WithSecondaryID(data[*input.Secondary].ID)
		}
		row, err := models.HolderObjects.Create(ctx, backend, create)
		if err != nil {
			t.Fatal(err)
		}
		holders[i] = row
	}
	numbers := map[int64]int64{}
	for i, input := range expected.Entries {
		create := models.NewEntryCreate(input.Label, holders[input.Primary].ID)
		if input.Secondary != nil {
			create = create.WithSecondaryID(holders[*input.Secondary].ID)
		}
		row, err := models.EntryObjects.Create(ctx, backend, create)
		if err != nil {
			t.Fatal(err)
		}
		numbers[row.ID] = int64(i + 1)
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	entry, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "scalarref", ModelName: "entry"}, models.EntryDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	holder, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "scalarref", ModelName: "holder"}, models.HolderDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	datum, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "scalarref", ModelName: "datum"}, models.DatumDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := orm.BindForward(entry, "primary", holder)
	if err != nil {
		t.Fatal(err)
	}
	second, err := orm.BindForward(entry, "secondary", holder)
	if err != nil {
		t.Fatal(err)
	}
	innerFirst, err := orm.BindForward(holder, "primary", datum)
	if err != nil {
		t.Fatal(err)
	}
	innerSecond, err := orm.BindForward(holder, "secondary", datum)
	if err != nil {
		t.Fatal(err)
	}
	routes := []orm.QueryRelation[models.Entry, models.Datum]{orm.ChainRelations(first, innerFirst), orm.ChainRelations(second, innerFirst), orm.ChainRelations(first, innerSecond), orm.ChainRelations(second, innerSecond)}
	selectors := map[string]map[string]scalarSelection{}
	for i, route := range routes {
		selectors[expected.Routes[i]] = bindSelections(t, route)
	}
	optionalLabel, err := routes[1].String(models.DatumFields.Label)
	if err != nil {
		t.Fatal(err)
	}
	rootLabel, err := routes[0].String(models.DatumFields.Label)
	if err != nil {
		t.Fatal(err)
	}
	optionalNumber, err := routes[1].Integer(models.DatumFields.NInteger)
	if err != nil {
		t.Fatal(err)
	}
	nestedNumber, err := routes[3].Integer(models.DatumFields.NInteger)
	if err != nil {
		t.Fatal(err)
	}
	predicates := map[string]orm.Predicate[models.Entry]{"optional_a": optionalLabel.Exact("A"), "not_optional_a": orm.Not(optionalLabel.Exact("A")), "optional_or_root": orm.Or(optionalLabel.Exact("A"), models.EntryFields.Label.Exact("e0")), "nested_or": orm.Or(optionalNumber.Exact(0), nestedNumber.IsNull(true)), "empty": orm.And(rootLabel.Exact("A"), rootLabel.Exact("B"))}
	dynamicLeaf := func(key string, value any) orm.Predicate[models.Entry] {
		p, err := orm.ParseDynamicRelations(entry, nil, []orm.LookupInput{{Key: key, Value: value}})
		if err != nil || len(p) != 1 {
			t.Fatal("dynamic reference route", err)
		}
		return p[0]
	}
	da := dynamicLeaf("secondary__primary__label", "A")
	dynamic := map[string]orm.Predicate[models.Entry]{"optional_a": da, "not_optional_a": orm.Not(da), "optional_or_root": orm.Or(da, models.EntryFields.Label.Exact("e0")), "nested_or": orm.Or(dynamicLeaf("secondary__primary__n_integer", int64(0)), dynamicLeaf("secondary__secondary__n_integer__isnull", true)), "empty": orm.And(dynamicLeaf("primary__primary__label", "A"), dynamicLeaf("primary__primary__label", "B"))}
	source := models.EntryObjects.Using(backend)
	for _, test := range expected.Observations {
		candidates := []orm.QuerySet[models.Entry]{source, source}
		if test.Name != "all" {
			p, ok := predicates[test.Name]
			if !ok {
				t.Fatal("unknown filter")
			}
			candidates[0] = source.Filter(p)
			candidates[1] = source.Filter(dynamic[test.Name])
		}
		if !candidates[0].Plan().Equal(candidates[1].Plan()) {
			t.Fatal("typed/dynamic scalar result source differ")
		}
		for _, candidate := range candidates {
			candidate = candidate.OrderBy(models.EntryFields.ID.Asc())
			if test.Distinct {
				candidate = candidate.Distinct()
			}
			if test.Sliced {
				candidate, err = candidate.Offset(1)
				if err != nil {
					t.Fatal(err)
				}
				candidate, err = candidate.Limit(3)
				if err != nil {
					t.Fatal(err)
				}
			}
			for index, name := range expected.Fields {
				selection, ok := selectors[test.Route][name]
				if !ok {
					t.Fatal("missing selected field", test.Route, name)
				}
				rows, err := selection.rows(ctx, candidate)
				if err != nil {
					t.Fatal(test.Name, test.Route, name, err)
				}
				if len(rows) != test.Count {
					t.Fatal("selected row count", test.Name, test.Route, name, len(rows), test.Count)
				}
				for rowIndex, row := range rows {
					var id int64
					if err := json.Unmarshal(test.Rows[rowIndex][0], &id); err != nil {
						t.Fatal(err)
					}
					observed, err := json.Marshal(row.Value)
					if err != nil {
						t.Fatal(err)
					}
					if numbers[row.ID] != id || string(observed) != canonicalWire(t, test.Rows[rowIndex][index+1]) {
						t.Fatalf("%s/%s/%s row %d: %d/%s want %d/%s", test.Name, test.Route, name, rowIndex, numbers[row.ID], observed, id, test.Rows[rowIndex][index+1])
					}
				}
			}
		}
	}
	for _, test := range expected.Distinct {
		values, err := selectors[test.Route][test.Field].values(ctx, source.Distinct())
		if err != nil {
			t.Fatal(test.Route, test.Field, err)
		}
		got, want := []string{}, []string{}
		for _, value := range values {
			b, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, string(b))
		}
		for _, value := range test.Values {
			want = append(want, canonicalWire(t, value))
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatal("selected value DISTINCT", test.Route, test.Field, got, want)
		}
	}
	for _, tc := range expected.Orderings {
		selected := selectors[tc.Route][tc.Field]
		ordering := selected.ascending
		if tc.Descending {
			ordering = selected.descending
		}
		candidate := source.OrderBy(ordering, models.EntryFields.ID.Asc())
		if tc.Distinct {
			candidate = candidate.Distinct()
		}
		if tc.Sliced {
			candidate, err = candidate.Offset(1)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err = candidate.Limit(3)
			if err != nil {
				t.Fatal(err)
			}
		}
		count, err := candidate.Count(ctx)
		if err != nil || count != tc.Count {
			t.Fatal("ordered scalar count", tc, count, err)
		}
		rows, err := candidate.All(ctx)
		if err != nil {
			t.Fatal("ordered scalar model", tc, err)
		}
		got := make([]int64, len(rows))
		for i, row := range rows {
			got[i] = numbers[row.ID]
		}
		if !slices.Equal(got, tc.Rows) {
			t.Fatal("scalar ordering differs from reference", tc, got)
		}
		projected, err := selected.rows(ctx, candidate)
		if err != nil {
			t.Fatal("ordered scalar projection", tc, err)
		}
		got = make([]int64, len(projected))
		for i, row := range projected {
			got[i] = numbers[row.ID]
		}
		if !slices.Equal(got, tc.Projected) {
			t.Fatal("projected scalar ordering differs from reference", tc, got)
		}
	}
	verifyResultBoundaries(t, backend, native, source, routes, data, expected, numbers)
	// No model cache is populated or rewritten by DTO terminals.
	cached, err := source.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := selectors[expected.Routes[0]]["v_text"].rows(ctx, source); err != nil {
		t.Fatal(err)
	}
	again, err := source.All(ctx)
	if err != nil || !reflect.DeepEqual(cached, again) {
		t.Fatal("DTO changed model cache", err)
	}
	// Generated direct selection has the same optional result type as chained selection.
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	direct := orm.Project2(models.HolderFields.ID, relations.ModelsHolder.Secondary.VText, func(id int64, v *string) projectedRow {
		var value any
		if v != nil {
			value = *v
		}
		return projectedRow{id, value}
	})
	rows, err := orm.SelectInto(ctx, models.HolderObjects.Using(backend).OrderBy(models.HolderFields.ID.Asc()), direct)
	if err != nil || len(rows) != 4 || rows[0].Value != "" || rows[1].Value != nil || rows[2].Value != expected.Inputs[0].Required[1] {
		t.Fatal("direct generated scalar selection", rows, err)
	}
	// Bounds stay explicit even when a valid plan is statically empty.
	empty, err := source.Limit(0)
	if err != nil {
		t.Fatal(err)
	}
	var before uint64
	if !native {
		before = backend.(*sqlite.Backend).QueryCount()
	}
	for _, candidate := range []orm.QuerySet[models.Entry]{empty, source.Filter(models.EntryFields.ID.In())} {
		values, err := selectors[expected.Routes[0]]["v_text"].values(ctx, candidate)
		if err != nil || values == nil || len(values) != 0 {
			t.Fatal("empty selected result", err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := selectors[expected.Routes[0]]["v_text"].values(canceled, source); !errors.Is(err, context.Canceled) {
		t.Fatal("selected scalar cancellation", err)
	}
	if !native && backend.(*sqlite.Backend).QueryCount() != before {
		t.Fatal("empty/canceled selected scalar performed I/O")
	}
}

func canonicalWire(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
