package consumer_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"example.com/godj-json/models"
	"example.com/godj-json/project"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func verifyJSONPaths(t *testing.T, backend jsonBackend, native bool) {
	t.Helper()
	ctx := t.Context()
	samples := []struct{ label, raw string }{
		{"sql_null", ""}, {"json_null", "null"}, {"missing", "{}"}, {"array", "[null,false,1,{\"a\":\"last\"}]"}, {"scalar", "1"},
		{"null", `{"a":null}`}, {"false", `{"a":false}`}, {"true", `{"a":true}`}, {"zero", `{"a":0}`}, {"one", `{"a":1}`}, {"float", `{"a":1.0}`},
		{"string_null", `{"a":"null"}`}, {"string_false", `{"a":"false"}`}, {"string_true", `{"a":"true"}`},
		{"huge_prev", `{"a":340282366920938463463374607431768211454}`}, {"huge", `{"a":340282366920938463463374607431768211455}`}, {"huge_next", `{"a":340282366920938463463374607431768211456}`},
		{"overflow", `{"a":1e400}`}, {"underflow", `{"a":1e-400}`}, {"object", `{"a":{"x":1,"y":2}}`}, {"list", `{"a":[1,false,null]}`},
		{"nested", `{"a":{"b":[null,false,1,{"c":"last"}]}}`},
	}
	keys := []string{"", "0", "01", "-1", "a.b", `a"b`, `a\b`, "a[0]", "한글😀", "__proto__", "a') OR 1=1 --", "*", "a\n\t<>&\u2028"}
	if !native {
		keys = append(keys, "\x00")
	}
	for i, key := range keys {
		data, err := json.Marshal(map[string]string{key: "found"})
		if err != nil {
			t.Fatal(err)
		}
		samples = append(samples, struct{ label, raw string }{fmt.Sprintf("key_%d", i), string(data)})
	}
	var names []string
	var created []models.Record
	for _, sample := range samples {
		names = append(names, "path_"+sample.label)
		value := jsonvalue.Null()
		if sample.raw != "" {
			value = document(t, sample.raw)
		}
		input := models.NewRecordCreate("path_"+sample.label, value)
		if sample.raw != "" {
			input = input.WithPayload(value)
		}
		row, err := models.RecordObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, row)
	}
	base := models.RecordObjects.Using(backend).Filter(models.RecordFields.Label.In(names...))
	check := func(name string, predicate orm.Predicate[models.Record], want ...string) {
		t.Helper()
		rows, err := base.Filter(predicate).OrderBy(models.RecordFields.ID.Asc()).All(ctx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := []string{}
		for _, row := range rows {
			got = append(got, strings.TrimPrefix(row.Label, "path_"))
		}
		slices.Sort(got)
		want = slices.Clone(want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
		count, err := base.Filter(predicate).Count(ctx)
		if err != nil || count != int64(len(want)) {
			t.Fatalf("%s count %d: %v", name, count, err)
		}
	}
	a := []query.JSONPathSegment{query.JSONKey("a")}
	field := models.RecordFields.Payload.At(a...)
	one := []string{"one"}
	if native {
		one = append(one, "float")
	}
	cases := []struct {
		name, raw string
		want      []string
	}{
		{"null", "null", []string{"null"}}, {"false", "false", []string{"false"}}, {"true", "true", []string{"true"}}, {"one", "1", one},
		{"string_null", `"null"`, []string{"string_null"}}, {"string_false", `"false"`, []string{"string_false"}},
		{"huge", "340282366920938463463374607431768211455", []string{"huge"}},
		{"overflow", "1e400", []string{"overflow"}}, {"underflow", "1e-400", []string{"underflow"}},
		{"object", `{"y":2,"x":1}`, []string{"object"}}, {"list", `[1,false,null]`, []string{"list"}},
	}
	for _, tc := range cases {
		value := document(t, tc.raw)
		inputs := []orm.LookupInput{{Key: "payload__exact", Value: value, JSONPath: a}}
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, inputs)
		if err != nil {
			t.Fatal(err)
		}
		typed := field.Exact(value)
		if !base.Filter(typed).Plan().Equal(base.Filter(dynamic[0]).Plan()) {
			t.Fatal("typed/dynamic JSON paths diverged")
		}
		check(tc.name, typed, tc.want...)
		check(tc.name+" dynamic", dynamic[0], tc.want...)
	}
	present := []string{"null", "false", "true", "zero", "one", "float", "string_null", "string_false", "string_true", "huge_prev", "huge", "huge_next", "overflow", "underflow", "object", "list", "nested"}
	missing := []string{}
	for _, sample := range samples {
		if !slices.Contains(present, sample.label) {
			missing = append(missing, sample.label)
		}
	}
	check("missing", field.IsNull(true), missing...)
	check("present", field.IsNull(false), present...)
	check("not missing", orm.Not(field.IsNull(true)), present...)
	nonNull := []string{"sql_null"}
	for _, label := range present {
		if label != "null" {
			nonNull = append(nonNull, label)
		}
	}
	check("exclude null", orm.Not(field.Exact(jsonvalue.Null())), nonNull...)
	requiredNonNull := slices.DeleteFunc(slices.Clone(nonNull), func(label string) bool { return label == "sql_null" })
	check("required exclude null", orm.Not(models.RecordFields.Required.At(a...).Exact(jsonvalue.Null())), requiredNonNull...)
	check("double NOT", orm.Not(orm.Not(field.Exact(jsonvalue.Null()))), "null")
	check("null or missing", orm.Or(field.Exact(jsonvalue.Null()), field.IsNull(true)), append(slices.Clone(missing), "null")...)
	check("IN", field.In(jsonvalue.Null(), document(t, "false")), "null", "false")
	check("empty IN", field.In())
	all := []string{}
	for _, sample := range samples {
		all = append(all, sample.label)
	}
	check("exclude empty IN", orm.Not(field.In()), all...)
	check("nested", models.RecordFields.Payload.At(query.JSONKey("a"), query.JSONKey("b"), query.JSONIndex(3), query.JSONKey("c")).Exact(document(t, `"last"`)), "nested")
	check("array scalar index", models.RecordFields.Payload.At(query.JSONIndex(0)).Exact(jsonvalue.Null()), "array")
	check("array false", models.RecordFields.Payload.At(query.JSONIndex(1)).Exact(document(t, "false")), "array")
	check("array objects not implicitly unwrapped", models.RecordFields.Payload.At(query.JSONKey("a")).Exact(document(t, `"last"`)))
	check("out of range", models.RecordFields.Payload.At(query.JSONIndex(2147483647)).IsNull(false))
	for i, key := range keys {
		check("literal key "+key, models.RecordFields.Payload.At(query.JSONKey(key)).Exact(document(t, `"found"`)), fmt.Sprintf("key_%d", i))
	}
	for _, input := range []orm.LookupInput{
		{Key: "payload__a__exact", Value: jsonvalue.Null()},
		{Key: "label", Value: "x", JSONPath: a},
		{Key: "payload", Value: jsonvalue.Null(), JSONPath: []query.JSONPathSegment{}},
		{Key: "payload__in", Value: []any{nil, jsonvalue.Null()}, JSONPath: a},
	} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{input}); err == nil {
			t.Fatal("unsupported dynamic JSON path accepted", input.Key)
		}
	}
	if _, err := orm.ParseDynamic(models.RecordDescriptor{}, func(ir.Field, query.Lookup) bool { return false }, []orm.LookupInput{{Key: "payload", Value: jsonvalue.Null(), JSONPath: a}}); !errors.Is(err, &query.Error{Code: query.CodeDisallowedLookup}) {
		t.Fatal("JSON path bypassed policy", err)
	}
	if native {
		for _, predicate := range []orm.Predicate[models.Record]{models.RecordFields.Payload.At(query.JSONKey("\x00")).IsNull(false), models.RecordFields.Payload.At(query.JSONKey("\x00")).In()} {
			if _, err := base.Filter(predicate).All(ctx); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
				t.Fatal("PostgreSQL NUL path reached query or was hidden by empty IN", err)
			}
		}
	}
	for _, predicate := range []orm.Predicate[models.Record]{models.RecordFields.Payload.At().Exact(jsonvalue.Null()), models.RecordFields.Payload.At(query.JSONIndex(-1)).IsNull(true), (orm.JSONPathField[models.Record]{}).Exact(jsonvalue.Null())} {
		if _, err := base.Filter(predicate).All(ctx); err == nil {
			t.Fatal("invalid typed path accepted")
		}
	}
	// Both construction input and returned AST slices are detached from cached queries.
	owned := []query.JSONPathSegment{query.JSONKey("a")}
	dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "payload", Value: jsonvalue.Null(), JSONPath: owned}})
	if err != nil {
		t.Fatal(err)
	}
	cached := base.Filter(dynamic...)
	before, err := cached.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owned[0] = query.JSONKey("changed")
	path, _ := cached.Plan().Conditions()[1].JSONPath()
	detached := path.Segments()
	detached[0] = query.JSONKey("changed")
	after, err := cached.All(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("path mutation changed cache", err)
	}
	verifyJSONPathRelations(t, backend, created)
}

func verifyJSONPathRelations(t *testing.T, backend jsonBackend, records []models.Record) {
	t.Helper()
	ctx := t.Context()
	for _, row := range records {
		if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate(row.Label).WithRecordID(row.ID).WithToken(row.Required)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate("path_absent")); err != nil {
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
	link, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "jsonref", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	segments := []query.JSONPathSegment{query.JSONKey("a")}
	field := related.ModelsLink.Record.Payload.At(segments...)
	for _, tc := range []struct {
		lookup string
		value  any
		typed  orm.Predicate[models.Link]
		want   []string
	}{
		{"exact", jsonvalue.Null(), field.Exact(jsonvalue.Null()), []string{"path_null"}},
		{"in", []jsonvalue.Value{jsonvalue.Null(), document(t, "false")}, field.In(jsonvalue.Null(), document(t, "false")), []string{"path_null", "path_false"}},
	} {
		parsed, err := orm.ParseDynamicRelations(link, nil, []orm.LookupInput{{Key: "record__payload__" + tc.lookup, Value: tc.value, JSONPath: segments}})
		if err != nil {
			t.Fatal(err)
		}
		for _, predicate := range []orm.Predicate[models.Link]{tc.typed, parsed[0]} {
			rows, err := facade.ModelsLink.Filter(predicate).SelectRelated(facade.ModelsLink.Related.Record).All(ctx)
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
				if _, present, err := row.Record(ctx); err != nil || !present {
					t.Fatal("path join lost target", err)
				}
			}
			want := slices.Clone(tc.want)
			slices.Sort(labels)
			slices.Sort(want)
			if !slices.Equal(labels, want) {
				t.Fatalf("path forward %v want %v", labels, want)
			}
		}
	}
	// Optional forward presence and root SQL NULL compensation retain absent targets.
	rows, err := facade.ModelsLink.Filter(orm.And(models.LinkFields.Label.In("path_absent", "path_sql_null", "path_missing", "path_null", "path_false"), orm.Not(field.Exact(jsonvalue.Null())))).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{}
	for _, row := range rows {
		labels = append(labels, row.Label)
	}
	slices.Sort(labels)
	if !slices.Equal(labels, []string{"path_absent", "path_false", "path_sql_null"}) {
		t.Fatalf("forward missing/null negation %v", labels)
	}
	// Direct reverse exact keeps the existing conjunction-only relation domain.
	reverseFields, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	reverse := reverseFields.ModelsRecord.Links.Token.At(query.JSONKey("a")).Exact(jsonvalue.Null())
	roots, err := models.RecordObjects.Using(backend).Filter(reverse).All(ctx)
	if err != nil || len(roots) != 1 || roots[0].Label != "path_null" {
		t.Fatal("reverse JSON path", err)
	}
}
