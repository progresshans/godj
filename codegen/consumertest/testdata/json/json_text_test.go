package consumer_test

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
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

//go:embed text_sqlite_reference.json
var textSQLiteReference []byte

//go:embed text_postgres_reference.json
var textPostgresReference []byte

//go:embed text_sqlite_policy_reference.json
var textSQLitePolicyReference []byte

type jsonTextCase struct {
	Scope, Needle, Mode, Exception string
	Related                        bool
	Count                          int64
	Rows                           []string
}
type jsonTextKey struct {
	Related             bool
	Scope, Needle, Mode string
}

func (tc jsonTextCase) key() jsonTextKey {
	return jsonTextKey{tc.Related, tc.Scope, tc.Needle, tc.Mode}
}

type textJSONField[M any] interface{ IContains(string) orm.Predicate[M] }

func verifyJSONText(t *testing.T, backend jsonBackend, native bool) {
	t.Helper()
	raw := textSQLiteReference
	if native {
		raw = textPostgresReference
	}
	var reference struct {
		Storage string
		Samples []struct {
			Label string
			Raw   *string
		}
		Rejected     []struct{ Label, Exception string }
		Observations []jsonTextCase
		Compositions []struct {
			Name  string
			Count int64
			Rows  []string
		}
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if len(reference.Samples) != 53 || len(reference.Observations) != 184 || len(reference.Compositions) != 3 || native && reference.Storage != "django_default" || !native && reference.Storage != "godj_canonical" {
		t.Fatal("wrong JSON text reference")
	}
	rejected := map[string]bool{}
	for _, item := range reference.Rejected {
		if !native || item.Exception != "DataError" {
			t.Fatal("unexpected reference write failure")
		}
		rejected[item.Label] = true
	}
	type deviation struct {
		Related             bool
		Scope, Needle, Mode string
		Before, Rows        []string
	}
	deviations := map[jsonTextKey]deviation{}
	if !native {
		var policy struct{ Observations []deviation }
		if err := json.Unmarshal(textSQLitePolicyReference, &policy); err != nil {
			t.Fatal(err)
		}
		if len(policy.Observations) != 28 {
			t.Fatal("wrong literal text policy inventory")
		}
		for _, row := range policy.Observations {
			deviations[jsonTextKey{row.Related, row.Scope, row.Needle, row.Mode}] = row
		}
	}
	ctx := t.Context()
	var recordNames, linkNames []string
	for _, sample := range reference.Samples {
		name := "text_" + sample.Label
		create := models.NewRecordCreate(name, jsonvalue.Null())
		if sample.Raw != nil {
			create = create.WithPayload(document(t, *sample.Raw))
		}
		row, err := models.RecordObjects.Create(ctx, backend, create)
		if rejected[sample.Label] {
			if !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
				t.Fatal("native NUL write", sample.Label, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(sample.Label, err)
		}
		recordNames = append(recordNames, name)
		name = "text_l_" + sample.Label
		if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate(name).WithRecordID(row.ID)); err != nil {
			t.Fatal(err)
		}
		linkNames = append(linkNames, name)
	}
	linkNames = append(linkNames, "text_l_absent")
	if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate("text_l_absent")); err != nil {
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
	used := 0
	for _, tc := range reference.Observations {
		if delta, ok := deviations[tc.key()]; ok {
			if !slices.Equal(delta.Before, tc.Rows) {
				t.Fatal("policy difference no longer matches its exact Django case", tc)
			}
			tc.Rows = slices.Clone(delta.Rows)
			tc.Count = int64(len(tc.Rows))
			used++
		}
		var path []query.JSONPathSegment
		if tc.Scope == "x" {
			path = []query.JSONPathSegment{query.JSONKey("x")}
		} else if tc.Scope != "root" {
			t.Fatal("unknown text scope")
		}
		input := orm.LookupInput{Key: "payload__icontains", Value: tc.Needle, JSONPath: path}
		if tc.Related {
			var field textJSONField[models.Link] = relations.ModelsLink.Record.Payload
			if path != nil {
				field = relations.ModelsLink.Record.Payload.At(path...)
			}
			typed := field.IContains(tc.Needle)
			input.Key = "record__" + input.Key
			dynamic, err := orm.ParseDynamicRelations(linkBinding, nil, []orm.LookupInput{input})
			if err != nil || len(dynamic) != 1 {
				t.Fatal(err)
			}
			if !links.Filter(typed).Plan().Equal(links.Filter(dynamic[0]).Plan()) {
				t.Fatal("related typed/dynamic text AST differs")
			}
			for _, predicate := range []orm.Predicate[models.Link]{typed, dynamic[0]} {
				if tc.Mode == "exclude" {
					predicate = orm.Not(predicate)
				}
				checkJSONText(t, backend, native, tc, links.Filter(predicate), models.LinkFields.ID.In(), models.LinkFields.Label, func(row models.Link) string { return row.Label })
			}
		} else {
			var field textJSONField[models.Record] = models.RecordFields.Payload
			if path != nil {
				field = models.RecordFields.Payload.At(path...)
			}
			typed := field.IContains(tc.Needle)
			dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{input})
			if err != nil || len(dynamic) != 1 {
				t.Fatal(err)
			}
			if !records.Filter(typed).Plan().Equal(records.Filter(dynamic[0]).Plan()) {
				t.Fatal("root typed/dynamic text AST differs")
			}
			for _, predicate := range []orm.Predicate[models.Record]{typed, dynamic[0]} {
				if tc.Mode == "exclude" {
					predicate = orm.Not(predicate)
				}
				checkJSONText(t, backend, native, tc, records.Filter(predicate), models.RecordFields.ID.In(), models.RecordFields.Label, func(row models.Record) string { return row.Label })
			}
		}
	}
	if used != len(deviations) {
		t.Fatal("unused policy difference")
	}
	match := relations.ModelsLink.Record.Payload.At(query.JSONKey("x")).IContains("ALPHA")
	absent := relations.ModelsLink.Record.IsNull(true)
	conditions := map[string]orm.Predicate[models.Link]{"match_or_absent": orm.Or(match, absent), "not_match_or_absent": orm.Not(orm.Or(match, absent)), "match_and_label": orm.And(match, models.LinkFields.Label.IContains("path"))}
	for _, tc := range reference.Compositions {
		condition, ok := conditions[tc.Name]
		if !ok {
			t.Fatal("unknown text composition")
		}
		checkJSONText(t, backend, native, jsonTextCase{Count: tc.Count, Rows: tc.Rows}, links.Filter(condition), models.LinkFields.ID.In(), models.LinkFields.Label, func(row models.Link) string { return row.Label })
	}
	for _, value := range []any{nil, jsonvalue.Null(), int64(1), []string{"x"}, "\xff"} {
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "payload__icontains", Value: value}}); err == nil {
			t.Fatal("invalid/untyped text operand accepted", fmt.Sprintf("%T", value))
		}
	}
	if _, err := orm.ParseDynamic(models.RecordDescriptor{}, func(ir.Field, query.Lookup) bool { return false }, []orm.LookupInput{{Key: "payload__icontains", Value: "x"}}); !errors.Is(err, &query.Error{Code: query.CodeDisallowedLookup}) {
		t.Fatal("JSON text bypassed lookup policy", err)
	}
	invalid := records.Filter(models.RecordFields.Payload.At(query.JSONKey("x")).IContains("\xff"))
	if _, err := invalid.Count(ctx); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
		t.Fatal("invalid UTF-8 text reached Count", err)
	}
	// Valid empties remain distinct from invalid input and do not perform I/O.
	empty, err := records.Filter(models.RecordFields.Payload.At(query.JSONKey("x")).IContains("Alpha")).Limit(0)
	if err != nil {
		t.Fatal(err)
	}
	var before uint64
	if !native {
		before = backend.(*sqlite.Backend).QueryCount()
	}
	if rows, err := empty.All(ctx); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("valid empty JSON text query", err)
	}
	if !native && backend.(*sqlite.Backend).QueryCount() != before {
		t.Fatal("empty JSON text performed I/O")
	}
}

func checkJSONText[M any](t *testing.T, backend jsonBackend, native bool, tc jsonTextCase, source orm.QuerySet[M], empty orm.Predicate[M], label orm.ScalarField[M, string], name func(M) string) {
	t.Helper()
	if tc.Exception != "" {
		if !native || tc.Exception != "ValueError" && tc.Exception != "DataError" || !strings.ContainsRune(tc.Needle, 0) {
			t.Fatal("unexpected reference text failure", tc)
		}
		zero, err := source.Limit(0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range []orm.QuerySet[M]{source, zero, source.Filter(empty)} {
			if _, err := candidate.Count(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
				t.Fatal("native invalid text escaped Count preflight", err)
			}
			if rows, err := candidate.All(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) || rows != nil {
				t.Fatal("native invalid text escaped empty preflight", err)
			}
			if rows, err := orm.SelectInto(t.Context(), candidate, orm.Project1(label, func(s string) string { return s })); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) || rows != nil {
				t.Fatal("native invalid text escaped projection", err)
			}
		}
		return
	}
	if count, err := source.Count(t.Context()); err != nil || count != tc.Count {
		t.Fatal("JSON text cold count", tc, count, err)
	}
	rows, err := source.All(t.Context())
	if err != nil {
		t.Fatal(tc, err)
	}
	got := make([]string, len(rows))
	for i, row := range rows {
		got[i] = strings.TrimPrefix(name(row), "text_")
	}
	if !slices.Equal(got, tc.Rows) {
		t.Fatal("JSON text differs from explicit reference", tc, got)
	}
	projected, err := orm.SelectInto(t.Context(), source, orm.Project1(label, func(s string) string { return strings.TrimPrefix(s, "text_") }))
	if err != nil || !slices.Equal(projected, tc.Rows) {
		t.Fatal("JSON text DTO", tc, projected, err)
	}
}
