package consumer_test

import (
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"example.com/godj-json/models"
	"example.com/godj-json/project"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed comparison_sqlite_reference.json
var comparisonSQLiteReference []byte

//go:embed comparison_postgres_reference.json
var comparisonPostgresReference []byte

type orderingCase struct {
	Scope                                 string
	Related, Descending, Distinct, Sliced bool
	Count                                 int64
	Rows, Projected                       []string
}

type orderedJSONField[M any] interface {
	orm.ScalarField[M, *jsonvalue.Value]
	Asc() orm.Ordering[M]
	Desc() orm.Ordering[M]
}

type comparisonCase struct {
	Name, Scope, Lookup, RHS, Mode, Exception string
	Related                                   bool
	Count                                     int64
	Rows                                      []string
}

type comparisonField[M any] interface {
	GreaterThan(jsonvalue.Value) orm.Predicate[M]
	GreaterThanOrEqual(jsonvalue.Value) orm.Predicate[M]
	LessThan(jsonvalue.Value) orm.Predicate[M]
	LessThanOrEqual(jsonvalue.Value) orm.Predicate[M]
}

func jsonComparison[M any](t *testing.T, field comparisonField[M], tc comparisonCase) orm.Predicate[M] {
	t.Helper()
	value := document(t, tc.RHS)
	switch tc.Lookup {
	case "gt":
		return field.GreaterThan(value)
	case "gte":
		return field.GreaterThanOrEqual(value)
	case "lt":
		return field.LessThan(value)
	case "lte":
		return field.LessThanOrEqual(value)
	default:
		t.Fatal("unknown comparison reference lookup")
		return orm.Predicate[M]{}
	}
}

func verifyJSONComparisons(t *testing.T, backend jsonBackend, native bool) {
	t.Helper()
	ctx := t.Context()
	raw := comparisonSQLiteReference
	if native {
		raw = comparisonPostgresReference
	}
	var reference struct {
		Storage string
		Samples []struct {
			Label string
			Raw   *string
		}
		Observations  []comparisonCase
		Compositions  []comparisonCase
		OrderingCases []orderingCase `json:"ordering_cases"`
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if len(reference.Samples) != 37 || len(reference.Observations) != 448 || len(reference.Compositions) != 4 || native && reference.Storage != "django_default" || !native && reference.Storage != "godj_canonical" {
		t.Fatal("wrong JSON comparison reference profile")
	}
	var recordNames, linkNames []string
	for _, sample := range reference.Samples {
		name := "comparison_" + sample.Label
		recordNames = append(recordNames, name)
		create := models.NewRecordCreate(name, jsonvalue.Null())
		if sample.Raw != nil {
			create = create.WithPayload(document(t, *sample.Raw))
		}
		row, err := models.RecordObjects.Create(ctx, backend, create)
		if err != nil {
			t.Fatal(err)
		}
		label := "comparison_l_" + sample.Label
		linkNames = append(linkNames, label)
		if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate(label).WithRecordID(row.ID)); err != nil {
			t.Fatal(err)
		}
	}
	linkNames = append(linkNames, "comparison_l_absent")
	if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate("comparison_l_absent")); err != nil {
		t.Fatal(err)
	}
	records := models.RecordObjects.Using(backend).Filter(models.RecordFields.Label.In(recordNames...)).OrderBy(models.RecordFields.ID.Asc())
	links := models.LinkObjects.Using(backend).Filter(models.LinkFields.Label.In(linkNames...)).OrderBy(models.LinkFields.ID.Asc())
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	linkBinding, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "jsonref", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range reference.Observations {
		var path []query.JSONPathSegment
		if tc.Scope == "x" {
			path = []query.JSONPathSegment{query.JSONKey("x")}
		} else if tc.Scope != "root" {
			t.Fatal("unknown comparison scope")
		}
		input := orm.LookupInput{Key: "payload__" + tc.Lookup, Value: document(t, tc.RHS), JSONPath: path}
		if tc.Related {
			var field comparisonField[models.Link] = relations.ModelsLink.Record.Payload
			if path != nil {
				field = relations.ModelsLink.Record.Payload.At(path...)
			}
			typed := jsonComparison(t, field, tc)
			input.Key = "record__" + input.Key
			dynamic, err := orm.ParseDynamicRelations(linkBinding, nil, []orm.LookupInput{input})
			if err != nil || len(dynamic) != 1 {
				t.Fatal(tc.Name, err)
			}
			if !links.Filter(typed).Plan().Equal(links.Filter(dynamic[0]).Plan()) {
				t.Fatal("related typed/dynamic comparison AST differs")
			}
			for _, predicate := range []orm.Predicate[models.Link]{typed, dynamic[0]} {
				if tc.Mode == "exclude" {
					predicate = orm.Not(predicate)
				} else if tc.Mode != "filter" {
					t.Fatal("unknown comparison mode")
				}
				checkJSONComparison(t, backend, native, tc, links.Filter(predicate), models.LinkFields.ID.In(), models.LinkFields.Label, func(row models.Link) string { return row.Label })
			}
		} else {
			var field comparisonField[models.Record] = models.RecordFields.Payload
			if path != nil {
				field = models.RecordFields.Payload.At(path...)
			}
			typed := jsonComparison(t, field, tc)
			dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{input})
			if err != nil || len(dynamic) != 1 {
				t.Fatal(tc.Name, err)
			}
			if !records.Filter(typed).Plan().Equal(records.Filter(dynamic[0]).Plan()) {
				t.Fatal("root typed/dynamic comparison AST differs")
			}
			for _, predicate := range []orm.Predicate[models.Record]{typed, dynamic[0]} {
				if tc.Mode == "exclude" {
					predicate = orm.Not(predicate)
				} else if tc.Mode != "filter" {
					t.Fatal("unknown comparison mode")
				}
				checkJSONComparison(t, backend, native, tc, records.Filter(predicate), models.RecordFields.ID.In(), models.RecordFields.Label, func(row models.Record) string { return row.Label })
			}
		}
	}
	pathField := relations.ModelsLink.Record.Payload.At(query.JSONKey("x"))
	gt := pathField.GreaterThan(document(t, "1"))
	absent := relations.ModelsLink.Record.IsNull(true)
	compositions := map[string]orm.Predicate[models.Link]{
		"forward_gt_or_absent":     orm.Or(gt, absent),
		"forward_between":          orm.And(pathField.GreaterThanOrEqual(document(t, "0")), pathField.LessThanOrEqual(document(t, "1"))),
		"forward_not_gt_or_absent": orm.Not(orm.Or(gt, absent)),
		"forward_gt_or_label":      orm.Or(gt, models.LinkFields.Label.Exact("comparison_l_absent")),
	}
	for _, tc := range reference.Compositions {
		predicate, ok := compositions[tc.Name]
		if !ok {
			t.Fatal("unknown comparison composition", tc.Name)
		}
		checkJSONComparison(t, backend, native, tc, links.Filter(predicate), models.LinkFields.ID.In(), models.LinkFields.Label, func(row models.Link) string { return row.Label })
	}
	for _, input := range []orm.LookupInput{{Key: "payload__gt", Value: int64(1)}, {Key: "payload__lte", Value: nil}, {Key: "payload__lt", Value: jsonvalue.Value{}}} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{input}); err == nil {
			t.Fatal("comparison coerced an untyped/invalid JSON value")
		}
	}
	if _, err := orm.ParseDynamic(models.RecordDescriptor{}, func(ir.Field, query.Lookup) bool { return false }, []orm.LookupInput{{Key: "payload__gt", Value: jsonvalue.Null()}}); !errors.Is(err, &query.Error{Code: query.CodeDisallowedLookup}) {
		t.Fatal("comparison bypassed lookup policy", err)
	}
	t.Run("ordering", func(t *testing.T) {
		if len(reference.OrderingCases) != 32 {
			t.Fatal("incomplete ordering reference")
		}
		beforeRecords, beforeLinks := records.Plan(), links.Plan()
		for _, tc := range reference.OrderingCases {
			if tc.Related {
				var field orderedJSONField[models.Link] = relations.ModelsLink.Record.Payload
				if tc.Scope == "x" {
					field = relations.ModelsLink.Record.Payload.At(query.JSONKey("x"))
				}
				ordering := field.Asc()
				if tc.Descending {
					ordering = field.Desc()
				}
				checkJSONOrdering(t, tc, links.OrderBy(ordering, models.LinkFields.ID.Asc()), models.LinkFields.ID, models.LinkFields.Label, field, func(row models.Link) string { return row.Label })
			} else {
				var field orderedJSONField[models.Record] = models.RecordFields.Payload
				if tc.Scope == "x" {
					field = models.RecordFields.Payload.At(query.JSONKey("x"))
				}
				ordering := field.Asc()
				if tc.Descending {
					ordering = field.Desc()
				}
				checkJSONOrdering(t, tc, records.OrderBy(ordering, models.RecordFields.ID.Asc()), models.RecordFields.ID, models.RecordFields.Label, field, func(row models.Record) string { return row.Label })
			}
		}
		facade, err := project.Using(backend)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range reference.OrderingCases {
			if !tc.Related || tc.Scope != "x" || !tc.Distinct || tc.Descending || tc.Sliced {
				continue
			}
			rows, err := facade.ModelsLink.Filter(models.LinkFields.Label.In(linkNames...)).OrderBy(relations.ModelsLink.Record.Payload.At(query.JSONKey("x")).Asc(), models.LinkFields.ID.Asc()).Distinct().SelectRelated(facade.ModelsLink.Related.Record).All(t.Context())
			if err != nil {
				t.Fatal("eager hidden path ordering", err)
			}
			got := make([]string, len(rows))
			for i, row := range rows {
				model, err := row.Unwrap()
				if err != nil {
					t.Fatal(err)
				}
				got[i] = strings.TrimPrefix(model.Label, "comparison_")
				record, present, err := row.Record(t.Context())
				if err != nil || present != (model.RecordID != nil) {
					t.Fatal("ordering changed eager presence", err)
				}
				if present && record.ID != *model.RecordID {
					t.Fatal("ordering changed eager target")
				}
			}
			if !slices.Equal(got, tc.Rows) {
				t.Fatal("eager ordering differs", got, tc.Rows)
			}
		}
		verifyJSONOrderingPrecision(t, backend)
		if !records.Plan().Equal(beforeRecords) || !links.Plan().Equal(beforeLinks) {
			t.Fatal("ordering mutated its source")
		}
		verifyJSONOrderingBoundaries(t, backend, native, records, links, relations.ModelsLink.Record.Payload)
	})
	verifyJSONComparisonBoundaries(t, backend, native)
}

func checkJSONComparison[M any](t *testing.T, backend jsonBackend, native bool, tc comparisonCase, source orm.QuerySet[M], empty orm.Predicate[M], label orm.ScalarField[M, string], name func(M) string) {
	t.Helper()
	ctx := t.Context()
	if tc.Exception != "" {
		if native || tc.Scope != "x" || tc.Exception != "ProgrammingError" || (tc.RHS != "[]" && tc.RHS != "{}" && tc.RHS != "[0]") {
			t.Fatal("unexpected reference exception", tc.Name, tc.Exception)
		}
		before := backend.(*sqlite.Backend).QueryCount()
		zero, err := source.Limit(0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range []orm.QuerySet[M]{source, zero, source.Filter(empty)} {
			want := &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Lookup: tc.Lookup}
			if _, err := candidate.Count(ctx); !errors.Is(err, want) {
				t.Fatal("unsupported comparison Count", tc.Name, err)
			}
			if rows, err := candidate.All(ctx); !errors.Is(err, want) || rows != nil {
				t.Fatal("unsupported comparison All", tc.Name, err)
			}
			if rows, err := orm.SelectInto(ctx, candidate, orm.Project1(label, func(s string) string { return s })); !errors.Is(err, want) || rows != nil {
				t.Fatal("unsupported comparison projection", tc.Name, err)
			}
		}
		if backend.(*sqlite.Backend).QueryCount() != before {
			t.Fatal("unsupported comparison performed I/O")
		}
		return
	}
	count, err := source.Count(ctx)
	if err != nil || count != tc.Count {
		t.Fatal("comparison cold count", tc.Name, count, tc.Count, err)
	}
	rows, err := source.All(ctx)
	if err != nil {
		t.Fatal("comparison rows", tc.Name, err)
	}
	got := make([]string, len(rows))
	for i, row := range rows {
		got[i] = strings.TrimPrefix(name(row), "comparison_")
	}
	if !slices.Equal(got, tc.Rows) {
		t.Fatal("comparison differs from reference", tc.Name, got, tc.Rows)
	}
	selected, err := orm.SelectInto(ctx, source, orm.Project1(label, func(s string) string { return strings.TrimPrefix(s, "comparison_") }))
	if err != nil || !slices.Equal(selected, tc.Rows) {
		t.Fatal("comparison DTO differs", tc.Name, selected, tc.Rows, err)
	}
}

func verifyJSONComparisonBoundaries(t *testing.T, backend jsonBackend, native bool) {
	t.Helper()
	ctx := t.Context()
	// These exact-number and literal-key cases use GoDj's existing strict JSON
	// policy rather than SQLite's lossy integer/REAL extraction.
	inputs := []string{`{"x":9007199254740993.0000000000000001,"":2,"\u0000":0}`, `{"x":9007199254740993,"":0,"\u0000":2}`, `{"x":-0.0000}`}
	if native {
		inputs = []string{`{"x":9007199254740993.0000000000000001,"":2}`, `{"x":9007199254740993,"":0}`, `{"x":-0.0000}`}
	}
	var ids []int64
	for i, input := range inputs {
		row, err := models.RecordObjects.Create(ctx, backend, models.NewRecordCreate("comparison_precision", jsonvalue.Null()).WithPayload(document(t, input)))
		if err != nil {
			t.Fatal(i, err)
		}
		ids = append(ids, row.ID)
	}
	base := models.RecordObjects.Using(backend).Filter(models.RecordFields.ID.In(ids...)).OrderBy(models.RecordFields.ID.Asc())
	field := models.RecordFields.Payload.At(query.JSONKey("x"))
	for _, tc := range []struct {
		predicate orm.Predicate[models.Record]
		ids       []int64
	}{
		{field.GreaterThan(document(t, "9007199254740993")), ids[:1]},
		{field.LessThanOrEqual(document(t, "9007199254740993")), ids[1:]},
		{models.RecordFields.Payload.At(query.JSONKey("")).GreaterThan(document(t, "1")), ids[:1]},
	} {
		rows, err := base.Filter(tc.predicate).All(ctx)
		got := make([]int64, len(rows))
		for i, row := range rows {
			got[i] = row.ID
		}
		if err != nil || !slices.Equal(got, tc.ids) {
			t.Fatal("exact numeric/literal path comparison", got, tc.ids, err)
		}
	}
	if !native {
		rows, err := base.Filter(models.RecordFields.Payload.At(query.JSONKey("\x00")).GreaterThan(document(t, "1"))).All(ctx)
		if err != nil || len(rows) != 1 || rows[0].ID != ids[1] {
			t.Fatal("NUL key comparison aliased empty key", err)
		}
	}
	zero, err := base.Limit(0)
	if err != nil {
		t.Fatal(err)
	}
	if native {
		for _, predicate := range []orm.Predicate[models.Record]{field.GreaterThan(document(t, `"\u0000"`)), field.GreaterThan(document(t, "1e4096")), models.RecordFields.Payload.At(query.JSONKey("\x00")).GreaterThan(document(t, "1"))} {
			for _, candidate := range []orm.QuerySet[models.Record]{base, zero, base.Filter(models.RecordFields.ID.In())} {
				if _, err := candidate.Filter(predicate).All(ctx); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
					t.Fatal("native comparison preflight bypassed", err)
				}
			}
		}
	}
	if rows, err := zero.Filter(field.GreaterThan(document(t, "0"))).All(ctx); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("valid empty comparison", err)
	}
	for _, predicate := range []orm.Predicate[models.Record]{field.GreaterThan(jsonvalue.Value{}), models.RecordFields.Payload.LessThan(jsonvalue.Value{})} {
		if _, err := base.Filter(predicate).All(ctx); err == nil {
			t.Fatal("invalid comparison value accepted")
		}
	}
	back, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.RecordObjects.Using(backend).Filter(back.ModelsRecord.Links.Token.GreaterThan(jsonvalue.Null())).All(ctx); !errors.Is(err, &query.Error{Code: query.CodeUnsupportedLookup}) {
		t.Fatal("reverse comparison widened", err)
	}
}

func checkJSONOrdering[M any](t *testing.T, tc orderingCase, source orm.QuerySet[M], id orm.ScalarField[M, int64], label orm.ScalarField[M, string], field orderedJSONField[M], name func(M) string) {
	t.Helper()
	var err error
	if tc.Distinct {
		source = source.Distinct()
	}
	if tc.Sliced {
		source, err = source.Offset(1)
		if err != nil {
			t.Fatal(err)
		}
		source, err = source.Limit(7)
		if err != nil {
			t.Fatal(err)
		}
	}
	count, err := source.Count(t.Context())
	if err != nil || count != tc.Count {
		t.Fatal("ordering cold count", tc, count, err)
	}
	rows, err := source.All(t.Context())
	if err != nil {
		t.Fatal("ordered model", tc, err)
	}
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = strings.TrimPrefix(name(row), "comparison_")
	}
	if !slices.Equal(names, tc.Rows) {
		t.Fatal("model ordering differs from reference", tc, names)
	}
	selected, err := orm.SelectInto(t.Context(), source, orm.Project3(id, label, field, func(_ int64, label string, value *jsonvalue.Value) string {
		if value != nil {
			if _, err := value.Decode(); err != nil {
				t.Fatal("ordering changed projected JSON", err)
			}
		}
		return strings.TrimPrefix(label, "comparison_")
	}))
	if err != nil || !slices.Equal(selected, tc.Projected) {
		t.Fatal("projected ordering differs from reference", tc, selected, err)
	}
}

func verifyJSONOrderingBoundaries(t *testing.T, backend jsonBackend, native bool, records orm.QuerySet[models.Record], links orm.QuerySet[models.Link], related orm.RelatedJSONField[models.Link]) {
	t.Helper()
	// A target field with the same id metadata must not satisfy root ordering.
	for _, source := range []orm.QuerySet[models.Record]{records, records.Distinct()} {
		zero, err := source.OrderBy(models.RecordFields.Payload.At(query.JSONKey("x")).Desc()).Limit(0)
		if err != nil {
			t.Fatal(err)
		}
		if rows, err := zero.All(t.Context()); err != nil || rows == nil || len(rows) != 0 {
			t.Fatal("valid empty JSON ordering", err)
		}
	}
	strict := links.OrderBy(related.At(query.JSONKey("x")).Asc()).Distinct()
	if rows, err := orm.SelectInto(t.Context(), strict, orm.Project1(models.LinkFields.Label, func(s string) string { return s })); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || rows != nil {
		t.Fatal("DISTINCT accepted unselected ordering expression", err)
	}
	if native {
		invalid := records.OrderBy(models.RecordFields.Payload.At(query.JSONKey("a\x00b")).Asc())
		zero, err := invalid.Limit(0)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []orm.QuerySet[models.Record]{invalid, zero, invalid.Filter(models.RecordFields.ID.In())} {
			if _, err := source.Count(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
				t.Fatal("native omitted order path was not validated", err)
			}
			if _, err := source.All(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
				t.Fatal("native empty order path was not validated", err)
			}
		}
	}
	var unbound orm.RelatedJSONField[models.Link]
	if _, err := links.OrderBy(unbound.Asc()).Count(t.Context()); err == nil {
		t.Fatal("unbound ordering accepted")
	}
	cause := errors.New("ordering binding failed")
	if _, err := links.OrderBy(related.WithConfigurationError(cause).Desc()).Count(t.Context()); !errors.Is(err, cause) {
		t.Fatal("ordering lost configuration error", err)
	}
}

func verifyJSONOrderingPrecision(t *testing.T, backend jsonBackend) {
	t.Helper()
	// These values are ordered mathematically, independently of binary64 and
	// lexicographic JSON token spelling. Equal spellings retain the ID tie-break.
	numbers := []string{"-1e400", "-1.201", "-1.2", "-1e-400", "-0", "0.0", "1e-400", "1", "1.0", "9007199254740993", "9007199254740993.000001", "340282366920938463463374607431768211455", "1e400"}
	labels := make([]string, len(numbers))
	for i, number := range numbers {
		labels[i] = "ordered_precision_" + number
		if _, err := models.RecordObjects.Create(t.Context(), backend, models.NewRecordCreate(labels[i], jsonvalue.Null()).WithPayload(document(t, `{"n":`+number+`}`))); err != nil {
			t.Fatal(err)
		}
	}
	base := models.RecordObjects.Using(backend).Filter(models.RecordFields.Label.In(labels...))
	for _, distinct := range []bool{false, true} {
		source := base.OrderBy(models.RecordFields.Payload.At(query.JSONKey("n")).Asc(), models.RecordFields.ID.Asc())
		if distinct {
			source = source.Distinct()
		}
		if count, err := source.Count(t.Context()); err != nil || count != int64(len(labels)) {
			t.Fatal("precision count", count, err)
		}
		rows, err := source.All(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(rows))
		for i, row := range rows {
			got[i] = row.Label
		}
		if !slices.Equal(got, labels) {
			t.Fatal("exact numeric ordering", got, labels)
		}
	}
	// The derived source must preserve root column names for scalar aggregates.
	source, err := base.OrderBy(models.RecordFields.Payload.At(query.JSONKey("n")).Asc(), models.RecordFields.ID.Asc()).Distinct().Limit(3)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := source.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := orm.AggregateInto(t.Context(), source, orm.Aggregate1(orm.Max(models.RecordFields.ID), func(v orm.Optional[int64]) orm.Optional[int64] { return v }))
	value, valid := maximum.Get()
	if err != nil || !valid || value != rows[2].ID {
		t.Fatal("ordered derived aggregate", maximum, err)
	}
}
