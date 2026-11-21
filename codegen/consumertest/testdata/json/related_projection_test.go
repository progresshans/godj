package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"example.com/godj-json/models"
	"example.com/godj-json/project"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed related_projection_sqlite_reference.json
var relatedProjectionSQLite []byte

//go:embed related_projection_postgres_reference.json
var relatedProjectionPostgres []byte

type relatedProjectionCase struct {
	Name, Selection  string
	Distinct, Sliced bool
	Rows             []json.RawMessage
	Count            int
}

func verifyRelatedProjection(t *testing.T, backend jsonBackend, native bool) {
	t.Helper()
	raw := relatedProjectionSQLite
	if native {
		raw = relatedProjectionPostgres
	}
	var reference struct {
		Records []struct {
			Label string
			Raw   *string
		}
		Links []struct {
			Label, Token string
			Record       *int
		}
		Observations []relatedProjectionCase
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if len(reference.Records) != 5 || len(reference.Links) != 8 || len(reference.Observations) != 48 {
		t.Fatal("related projection reference inventory changed")
	}
	var records []models.Record
	var recordIDs, linkIDs []int64
	recordNumbers, linkNumbers := map[int64]int64{}, map[int64]int64{}
	for i, input := range reference.Records {
		value := jsonvalue.Null()
		if input.Raw != nil {
			value = document(t, *input.Raw)
		}
		create := models.NewRecordCreate(input.Label, value)
		if input.Raw != nil {
			create = create.WithPayload(value)
		}
		row, err := models.RecordObjects.Create(t.Context(), backend, create)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, row)
		recordIDs = append(recordIDs, row.ID)
		recordNumbers[row.ID] = int64(i + 1)
	}
	for i, input := range reference.Links {
		create := models.NewLinkCreate(input.Label).WithToken(document(t, input.Token))
		if input.Record != nil {
			create = create.WithRecordID(records[*input.Record].ID)
		}
		row, err := models.LinkObjects.Create(t.Context(), backend, create)
		if err != nil {
			t.Fatal(err)
		}
		linkIDs = append(linkIDs, row.ID)
		linkNumbers[row.ID] = int64(i + 1)
	}
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	boundLinks, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "jsonref", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	boundRecords, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "jsonref", ModelName: "record"}, models.RecordDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	forward := map[string]orm.Predicate[models.Link]{
		"forward_one":          relations.ModelsLink.Record.Label.Exact("one"),
		"forward_not_one":      orm.Not(relations.ModelsLink.Record.Label.Exact("one")),
		"forward_or_absent":    orm.Or(relations.ModelsLink.Record.Label.Exact("one"), models.LinkFields.Label.Exact("orphan")),
		"forward_target_null":  relations.ModelsLink.Record.Payload.IsNull(true),
		"forward_json_key":     relations.ModelsLink.Record.Payload.HasKey("a"),
		"forward_not_json_key": orm.Not(relations.ModelsLink.Record.Payload.HasKey("a")),
	}
	dynamicForward := map[string]orm.Predicate[models.Link]{}
	for _, input := range []struct {
		name, key string
		value     any
	}{{"forward_one", "record__label", "one"}, {"forward_target_null", "record__payload__isnull", true}, {"forward_json_key", "record__payload__has_key", "a"}} {
		parsed, err := orm.ParseDynamicRelations(boundLinks, nil, []orm.LookupInput{{Key: input.key, Value: input.value}})
		if err != nil {
			t.Fatal(err)
		}
		dynamicForward[input.name] = parsed[0]
	}
	dynamicForward["forward_not_one"] = orm.Not(dynamicForward["forward_one"])
	dynamicForward["forward_not_json_key"] = orm.Not(dynamicForward["forward_json_key"])
	dynamicForward["forward_or_absent"] = orm.Or(dynamicForward["forward_one"], models.LinkFields.Label.Exact("orphan"))
	backward := map[string]orm.Predicate[models.Record]{"reverse_match": reverse.ModelsRecord.Links.Label.Exact("match"), "reverse_json": reverse.ModelsRecord.Links.Token.Exact(document(t, `{"a":1}`))}
	dynamicBackward := map[string]orm.Predicate[models.Record]{}
	for _, input := range []struct {
		name, key string
		value     any
	}{{"reverse_match", "links__label", "match"}, {"reverse_json", "links__token", document(t, `{"a":1}`)}} {
		parsed, err := orm.ParseDynamicRelations(boundRecords, nil, []orm.LookupInput{{Key: input.key, Value: input.value}})
		if err != nil {
			t.Fatal(err)
		}
		dynamicBackward[input.name] = parsed[0]
	}
	links := models.LinkObjects.Using(backend).Filter(models.LinkFields.ID.In(linkIDs...))
	recordSource := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.In(recordIDs...))
	for _, tc := range reference.Observations {
		if predicate, ok := forward[tc.Name]; ok {
			typed, dynamic := links.Filter(predicate), links.Filter(dynamicForward[tc.Name])
			if !typed.Plan().Equal(dynamic.Plan()) {
				t.Fatal("forward projection predicate AST diverged")
			}
			for _, source := range []orm.QuerySet[models.Link]{typed, dynamic} {
				compareRelatedProjection(t, source, models.LinkFields.ID, models.LinkFields.Label, models.LinkFields.Token.At(query.JSONKey("a")), linkNumbers, 2, tc)
			}
		} else if predicate, ok := backward[tc.Name]; ok {
			typed, dynamic := recordSource.Filter(predicate), recordSource.Filter(dynamicBackward[tc.Name])
			if !typed.Plan().Equal(dynamic.Plan()) {
				t.Fatal("reverse projection predicate AST diverged")
			}
			for _, source := range []orm.QuerySet[models.Record]{typed, dynamic} {
				compareRelatedProjection(t, source, models.RecordFields.ID, models.RecordFields.Label, models.RecordFields.Required.At(query.JSONKey("a")), recordNumbers, 2, tc)
			}
		} else {
			t.Fatal("unknown reference predicate", tc.Name)
		}
	}
	// Python None above is the reference representation. Retain Go's stronger
	// missing/JSON-null distinction explicitly after nullable JOIN predicates.
	selected := orm.Project2(models.LinkFields.ID, models.LinkFields.Token.At(query.JSONKey("a")), func(id int64, value *jsonvalue.Value) struct {
		ID    int64
		Value *jsonvalue.Value
	} {
		return struct {
			ID    int64
			Value *jsonvalue.Value
		}{id, value}
	})
	rows, err := orm.SelectInto(t.Context(), links.Filter(forward["forward_not_one"]).OrderBy(models.LinkFields.ID.Asc()), selected)
	if err != nil || len(rows) != 5 {
		t.Fatal("nullable forward projection", err)
	}
	for _, row := range rows {
		number := linkNumbers[row.ID]
		if number == 6 {
			if row.Value != nil {
				t.Fatal("missing path became JSON null")
			}
			continue
		}
		if row.Value == nil {
			t.Fatal("present path became missing")
		}
		if (number == 5 || number == 8) && *row.Value != jsonvalue.Null() {
			t.Fatal("stored JSON null lost")
		}
	}
	verifyRelatedProjectionPreflight(t, backend, native, links.Filter(forward["forward_or_absent"]))
}

func compareRelatedProjection[M any](t *testing.T, source orm.QuerySet[M], id orm.AutoField[M], label orm.ScalarField[M, string], path orm.JSONPathField[M], numbers map[int64]int64, sliceLimit int, tc relatedProjectionCase) {
	t.Helper()
	if tc.Distinct {
		source = source.Distinct()
	}
	var projection orm.Projection[M, []any]
	decode := func(value *jsonvalue.Value) any {
		if value == nil {
			return nil
		}
		decoded, err := value.Decode()
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	switch tc.Selection {
	case "id_label_path":
		source = source.OrderBy(id.Asc())
		projection = orm.Project3(id, label, path, func(id int64, label string, value *jsonvalue.Value) []any {
			number, ok := numbers[id]
			if !ok {
				t.Fatal("projection returned foreign fixture ID")
			}
			return []any{number, label, decode(value)}
		})
	case "path_only":
		projection = orm.Project1(path, func(value *jsonvalue.Value) []any { return []any{decode(value)} })
	default:
		t.Fatal("unknown reference selection")
	}
	if tc.Sliced {
		if tc.Selection != "id_label_path" {
			t.Fatal("unordered reference slice")
		}
		var err error
		source, err = source.Offset(1)
		if err != nil {
			t.Fatal(err)
		}
		source, err = source.Limit(sliceLimit)
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := orm.SelectInto(t.Context(), source, projection)
	if err != nil {
		t.Fatal(tc, err)
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
	if tc.Selection == "path_only" {
		slices.Sort(got)
		slices.Sort(want)
	}
	if len(rows) != tc.Count || !slices.Equal(got, want) {
		t.Fatalf("relation projection %s %s distinct=%v sliced=%v: %v want %v", tc.Name, tc.Selection, tc.Distinct, tc.Sliced, got, want)
	}
}

func verifyRelatedProjectionPreflight(t *testing.T, backend jsonBackend, native bool, source orm.QuerySet[models.Link]) {
	t.Helper()
	path := models.LinkFields.Token.At(query.JSONKey("a"))
	projection := orm.Project1(path, func(value *jsonvalue.Value) *jsonvalue.Value { return value })
	if _, err := orm.SelectInto(t.Context(), source.Distinct().OrderBy(models.LinkFields.ID.Asc()), projection); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("joined DISTINCT allowed unselected ordering", err)
	}
	empty, err := source.Limit(0)
	if err != nil {
		t.Fatal(err)
	}
	var before uint64
	if !native {
		before = backend.(*sqlite.Backend).QueryCount()
	}
	for _, candidate := range []orm.QuerySet[models.Link]{empty, source.Filter(models.LinkFields.ID.In())} {
		if rows, err := orm.SelectInto(t.Context(), candidate, projection); err != nil || rows == nil || len(rows) != 0 {
			t.Fatal("empty joined projection", err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := orm.SelectInto(canceled, source, projection); !errors.Is(err, context.Canceled) {
		t.Fatal("joined projection cancellation", err)
	}
	if !native && backend.(*sqlite.Backend).QueryCount() != before {
		t.Fatal("empty/canceled joined projection performed I/O")
	}
	if native {
		nul := orm.Project1(models.LinkFields.Token.At(query.JSONKey("\x00")), func(value *jsonvalue.Value) *jsonvalue.Value { return value })
		for _, candidate := range []orm.QuerySet[models.Link]{source, empty, source.Filter(models.LinkFields.ID.In())} {
			if rows, err := orm.SelectInto(t.Context(), candidate, nul); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) || rows != nil {
				t.Fatal("joined NUL projection bypassed preflight", err)
			}
		}
	}
	// The model cache remains a model rowset. Projection performs independent
	// I/O and must not replace its shape or expose its selected values as models.
	modelRows, err := source.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orm.SelectInto(t.Context(), source, projection); err != nil {
		t.Fatal(err)
	}
	again, err := source.All(t.Context())
	if err != nil || !reflect.DeepEqual(modelRows, again) {
		t.Fatal("joined projection replaced model cache", err)
	}
}
