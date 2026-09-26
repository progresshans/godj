package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"example.com/godj-json/models"
	"example.com/godj-json/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed forward_projection_sqlite_reference.json
var forwardJSONSQLite []byte

//go:embed forward_projection_postgres_reference.json
var forwardJSONPostgres []byte

func verifyForwardJSONProjection(t *testing.T, backend jsonBackend, native bool) {
	t.Helper()
	raw := forwardJSONSQLite
	if native {
		raw = forwardJSONPostgres
	}
	var reference struct {
		Documents []struct {
			Label string
			Raw   *string
		}
		Shelves []struct {
			Primary   int
			Secondary *int
		}
		Entries []struct {
			Label     string
			Primary   int
			Secondary *int
		}
		Paths   []string
		Absence []struct {
			Path         string
			Missing      []int64
			TargetAbsent []int64 `json:"target_absent"`
			SourceNull   []int64 `json:"source_sql_null_or_absent"`
		}
		Observations []relatedProjectionCase
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if len(reference.Documents) != 7 || len(reference.Shelves) != 5 || len(reference.Entries) != 8 || len(reference.Paths) != 4 || len(reference.Observations) != 144 || len(reference.Absence) != 4 {
		t.Fatal("incomplete forward JSON reference")
	}
	documents := make([]models.Document, len(reference.Documents))
	for i, input := range reference.Documents {
		create := models.NewDocumentCreate(input.Label)
		if input.Raw != nil {
			create = create.WithPayload(document(t, *input.Raw))
		}
		row, err := models.DocumentObjects.Create(t.Context(), backend, create)
		if err != nil {
			t.Fatal(err)
		}
		documents[i] = row
	}
	shelves := make([]models.Shelf, len(reference.Shelves))
	for i, input := range reference.Shelves {
		create := models.NewShelfCreate(documents[input.Primary].ID)
		if input.Secondary != nil {
			create = create.WithSecondaryID(documents[*input.Secondary].ID)
		}
		row, err := models.ShelfObjects.Create(t.Context(), backend, create)
		if err != nil {
			t.Fatal(err)
		}
		shelves[i] = row
	}
	numbers := map[int64]int64{}
	for i, input := range reference.Entries {
		create := models.NewEntryCreate(input.Label, shelves[input.Primary].ID)
		if input.Secondary != nil {
			create = create.WithSecondaryID(shelves[*input.Secondary].ID)
		}
		row, err := models.EntryObjects.Create(t.Context(), backend, create)
		if err != nil {
			t.Fatal(err)
		}
		numbers[row.ID] = int64(i + 1)
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	entry, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "jsonref", ModelName: "entry"}, models.EntryDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	shelf, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "jsonref", ModelName: "shelf"}, models.ShelfDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "jsonref", ModelName: "document"}, models.DocumentDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	entryFirst, err := orm.BindForward(entry, "primary", shelf)
	if err != nil {
		t.Fatal(err)
	}
	entrySecond, err := orm.BindForward(entry, "secondary", shelf)
	if err != nil {
		t.Fatal(err)
	}
	shelfFirst, err := orm.BindForward(shelf, "primary", doc)
	if err != nil {
		t.Fatal(err)
	}
	shelfSecond, err := orm.BindForward(shelf, "secondary", doc)
	if err != nil {
		t.Fatal(err)
	}
	routes := []orm.QueryRelation[models.Entry, models.Document]{orm.ChainRelations(entryFirst, shelfFirst), orm.ChainRelations(entrySecond, shelfFirst), orm.ChainRelations(entryFirst, shelfSecond), orm.ChainRelations(entrySecond, shelfSecond)}
	paths := map[string]orm.JSONPathField[models.Entry]{}
	fields := make([]orm.RelatedJSONField[models.Entry], 4)
	for i, route := range routes {
		field, err := route.JSON(models.DocumentFields.Payload)
		if err != nil {
			t.Fatal(err)
		}
		fields[i] = field
		paths[reference.Paths[i]] = field.At(query.JSONKey("a"))
	}
	firstLabel, err := routes[0].String(models.DocumentFields.Label)
	if err != nil {
		t.Fatal(err)
	}
	optionalLabel, err := routes[1].String(models.DocumentFields.Label)
	if err != nil {
		t.Fatal(err)
	}
	typed := map[string]orm.Predicate[models.Entry]{
		"primary_one":             firstLabel.Exact("one"),
		"not_primary_one":         orm.Not(firstLabel.Exact("one")),
		"optional_parent_one":     optionalLabel.Exact("one"),
		"not_optional_parent_one": orm.Not(optionalLabel.Exact("one")),
		"optional_or_local":       orm.Or(optionalLabel.Exact("one"), models.EntryFields.Label.Exact("e0")),
		"nested_or":               orm.Or(fields[1].HasKey("a"), fields[3].IsNull(true)),
		"empty":                   orm.And(firstLabel.Exact("one"), firstLabel.Exact("null")),
	}
	dynamicLeaf := func(key string, value any) orm.Predicate[models.Entry] {
		parsed, err := orm.ParseDynamicRelations(entry, nil, []orm.LookupInput{{Key: key, Value: value}})
		if err != nil || len(parsed) != 1 {
			t.Fatal("dynamic route", err)
		}
		return parsed[0]
	}
	firstDynamic := dynamicLeaf("primary__primary__label", "one")
	optionalDynamic := dynamicLeaf("secondary__primary__label", "one")
	rootLabel, err := orm.ParseDynamic(models.EntryDescriptor{}, nil, []orm.LookupInput{{Key: "label", Value: "e0"}})
	if err != nil || len(rootLabel) != 1 {
		t.Fatal("dynamic root field", err)
	}
	dynamic := map[string]orm.Predicate[models.Entry]{
		"primary_one": firstDynamic, "not_primary_one": orm.Not(firstDynamic), "optional_parent_one": optionalDynamic, "not_optional_parent_one": orm.Not(optionalDynamic),
		"optional_or_local": orm.Or(optionalDynamic, rootLabel[0]),
		"nested_or":         orm.Or(dynamicLeaf("secondary__primary__payload__has_key", "a"), dynamicLeaf("secondary__secondary__payload__isnull", true)),
		"empty":             orm.And(firstDynamic, dynamicLeaf("primary__primary__label", "null")),
	}
	base := models.EntryObjects.Using(backend)
	for _, tc := range reference.Observations {
		sources := []orm.QuerySet[models.Entry]{base, base}
		if tc.Name != "all" {
			p, ok := typed[tc.Name]
			if !ok {
				t.Fatal("unknown reference filter")
			}
			sources[0] = base.Filter(p)
			sources[1] = base.Filter(dynamic[tc.Name])
		}
		if !sources[0].Plan().Equal(sources[1].Plan()) {
			t.Fatal("typed/dynamic forward result source diverged")
		}
		for i, source := range sources {
			t.Run(fmt.Sprintf("%s/%s/%v/%v/%d", tc.Name, tc.Selection, tc.Distinct, tc.Sliced, i), func(t *testing.T) {
				if tc.Selection != "paths_only" {
					path, ok := paths[tc.Selection]
					if !ok {
						t.Fatal("unknown reference path")
					}
					single := tc
					single.Name += "/" + tc.Selection
					single.Selection = "id_label_path"
					compareRelatedProjection(t, source, models.EntryFields.ID, models.EntryFields.Label, path, numbers, 3, single)
					return
				}
				if tc.Distinct {
					source = source.Distinct()
				}
				decode := func(v *jsonvalue.Value) any {
					if v == nil {
						return nil
					}
					value, err := v.Decode()
					if err != nil {
						t.Fatal(err)
					}
					return value
				}
				projection := orm.Project4(paths[reference.Paths[0]], paths[reference.Paths[1]], paths[reference.Paths[2]], paths[reference.Paths[3]], func(a, b, c, d *jsonvalue.Value) []any { return []any{decode(a), decode(b), decode(c), decode(d)} })
				rows, err := orm.SelectInto(t.Context(), source, projection)
				if err != nil {
					t.Fatal(err)
				}
				got, want := []string{}, []string{}
				for _, row := range rows {
					raw, err := json.Marshal(row)
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, string(raw))
				}
				for _, row := range tc.Rows {
					want = append(want, document(t, string(row)).Text)
				}
				slices.Sort(got)
				slices.Sort(want)
				if len(rows) != tc.Count || !slices.Equal(got, want) {
					t.Fatal("selected route values/distinct differ", got, want)
				}
			})
		}
	}
	for _, absence := range reference.Absence {
		type row struct {
			ID    int64
			Value *jsonvalue.Value
		}
		projection := orm.Project2(models.EntryFields.ID, paths[absence.Path], func(id int64, v *jsonvalue.Value) row { return row{id, v} })
		rows, err := orm.SelectInto(t.Context(), base.OrderBy(models.EntryFields.ID.Asc()), projection)
		if err != nil || len(rows) != 8 {
			t.Fatal("forward nullable result", err)
		}
		for _, row := range rows {
			missing := slices.Contains(absence.Missing, numbers[row.ID])
			if (row.Value == nil) != missing {
				t.Fatal("missing and JSON null collapsed", absence.Path, numbers[row.ID], row.Value)
			}
		}
	}
	// A single-hop generated selector uses the same result expression path.
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	direct := relations.ModelsShelf.Primary.Payload.At(query.JSONKey("a"))
	directRows, err := orm.SelectInto(t.Context(), models.ShelfObjects.Using(backend).OrderBy(models.ShelfFields.ID.Asc()), orm.Project1(direct, func(v *jsonvalue.Value) *jsonvalue.Value { return v }))
	if err != nil || len(directRows) != 5 || directRows[0] == nil || directRows[0].Text != "1" || directRows[1] == nil || *directRows[1] != jsonvalue.Null() || directRows[2] != nil || directRows[4] != nil {
		t.Fatal("direct generated JSON result", err)
	}
	verifyForwardJSONResultLifetime(t, backend, native, base, paths[reference.Paths[0]], fields[0], documents)
}

func verifyForwardJSONResultLifetime(t *testing.T, backend jsonBackend, native bool, source orm.QuerySet[models.Entry], path orm.JSONPathField[models.Entry], field orm.RelatedJSONField[models.Entry], documents []models.Document) {
	t.Helper()
	projection := orm.Project1(path, func(v *jsonvalue.Value) *jsonvalue.Value { return v })
	empty, err := source.Limit(0)
	if err != nil {
		t.Fatal(err)
	}
	var before uint64
	if !native {
		before = backend.(*sqlite.Backend).QueryCount()
	}
	for _, candidate := range []orm.QuerySet[models.Entry]{empty, source.Filter(models.EntryFields.ID.In())} {
		rows, err := orm.SelectInto(t.Context(), candidate, projection)
		if err != nil || rows == nil || len(rows) != 0 {
			t.Fatal("empty related value projection", err)
		}
		bad := orm.Project2(path, path, func(a, b *jsonvalue.Value) int { return 0 })
		if _, err := orm.SelectInto(t.Context(), candidate, bad); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("empty source hid duplicate route", err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := orm.SelectInto(canceled, source, projection); !errors.Is(err, context.Canceled) {
		t.Fatal("related result cancellation", err)
	}
	if !native && backend.(*sqlite.Backend).QueryCount() != before {
		t.Fatal("empty/invalid/canceled related result performed I/O")
	}
	if native {
		nul := orm.Project1(field.At(query.JSONKey("\x00")), func(v *jsonvalue.Value) *jsonvalue.Value { return v })
		for _, candidate := range []orm.QuerySet[models.Entry]{source, empty, source.Filter(models.EntryFields.ID.In())} {
			if rows, err := orm.SelectInto(t.Context(), candidate, nul); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) || rows != nil {
				t.Fatal("native selected route NUL preflight", err)
			}
		}
	}
	rollback := errors.New("related JSON result rollback")
	var escaped db.Session
	err = backend.Atomic(t.Context(), func(session db.Session) error {
		escaped = session
		if _, err := models.DocumentObjects.Update(t.Context(), session, documents[0], models.DocumentPatch{}.WithPayload(document(t, `{"a":777}`))); err != nil {
			return err
		}
		rows, err := orm.SelectInto(t.Context(), models.EntryObjects.Using(session).OrderBy(models.EntryFields.ID.Asc()), projection)
		if err != nil {
			return err
		}
		if len(rows) != 8 || rows[0] == nil || rows[0].Text != "777" {
			return errors.New("transaction selected JSON target incorrectly")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal("related JSON transaction", err)
	}
	if rows, err := orm.SelectInto(t.Context(), models.EntryObjects.Using(escaped), projection); err == nil || rows != nil {
		t.Fatal("selected relation escaped its session")
	}
	rolledBack, err := orm.SelectInto(t.Context(), source.OrderBy(models.EntryFields.ID.Asc()), projection)
	if err != nil || len(rolledBack) != 8 || rolledBack[0] == nil || rolledBack[0].Text != "1" {
		t.Fatal("related JSON target rollback lost original value", err)
	}
	cached, err := source.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	value := document(t, `{"a":999}`)
	documents[0].Payload = &value
	if err := models.DocumentObjects.Save(t.Context(), backend, &documents[0], models.DocumentUpdateFieldNames("payload")); err != nil {
		t.Fatal(err)
	}
	rows, err := orm.SelectInto(t.Context(), source.OrderBy(models.EntryFields.ID.Asc()), projection)
	if err != nil || len(rows) != 8 || rows[0] == nil || rows[0].Text != "999" || rows[5] == nil || rows[5].Text != "999" || rows[0] == rows[5] {
		t.Fatal("selected target reused cache or shared row storage", err)
	}
	*rows[0] = jsonvalue.Null()
	if rows[5].Text != "999" {
		t.Fatal("selected row pointer escaped")
	}
	again, err := source.All(t.Context())
	if err != nil || !reflect.DeepEqual(cached, again) {
		t.Fatal("target projection replaced root model cache", err)
	}
	if !native {
		sqliteBackend := backend.(*sqlite.Backend)
		table := (models.DocumentDescriptor{}).Metadata().DBTable
		if _, err := sqliteBackend.ExecContext(t.Context(), `UPDATE "`+table+`" SET payload=? WHERE id=?`, `{"a":1,"a":2}`, documents[2].ID); err != nil {
			t.Fatal(err)
		}
		if rows, err := orm.SelectInto(t.Context(), source.OrderBy(models.EntryFields.ID.Asc()), projection); err == nil || rows != nil {
			t.Fatal("target projection returned a partial failed result")
		}
		if err := models.DocumentObjects.Save(t.Context(), backend, &documents[2], models.DocumentUpdateFieldNames("payload")); err != nil {
			t.Fatal(err)
		}
		if rows, err := orm.SelectInto(t.Context(), source.OrderBy(models.EntryFields.ID.Asc()), projection); err != nil || len(rows) != 8 {
			t.Fatal("target projection cannot retry after repair", err)
		}
	}
}
