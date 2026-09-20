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

//go:embed containment_reference.json
var containmentReference []byte

type containmentCase struct {
	Scope, Lookup, RHS, Mode string
	Rows                     []string
	Exception                string
}
type containmentField[M any] interface {
	Contains(jsonvalue.Value) orm.Predicate[M]
	ContainedBy(jsonvalue.Value) orm.Predicate[M]
}

func containmentPredicate[M any](t *testing.T, field containmentField[M], lookup string, value jsonvalue.Value) orm.Predicate[M] {
	t.Helper()
	switch lookup {
	case "contains":
		return field.Contains(value)
	case "contained_by":
		return field.ContainedBy(value)
	default:
		t.Fatalf("unknown containment lookup %q", lookup)
		return orm.Predicate[M]{}
	}
}

func verifyJSONContainment(t *testing.T, backend jsonBackend, native bool) {
	t.Helper()
	ctx := t.Context()
	var reference struct {
		Containment struct {
			Supported bool
			Samples   []struct {
				Label string
				Raw   *string
			}
			Queries []containmentCase
		}
	}
	if err := json.Unmarshal(containmentReference, &reference); err != nil {
		t.Fatal(err)
	}
	if !reference.Containment.Supported || len(reference.Containment.Queries) != 168 || len(reference.Containment.Samples) != 33 {
		t.Fatal("independent containment reference missing")
	}
	var names []string
	var records []models.Record
	for _, sample := range reference.Containment.Samples {
		name := "contain_" + sample.Label
		names = append(names, name)
		value := jsonvalue.Null()
		if sample.Raw != nil {
			value = document(t, *sample.Raw)
		}
		input := models.NewRecordCreate(name, value)
		if sample.Raw != nil {
			input = input.WithPayload(value)
		}
		row, err := models.RecordObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, row)
	}
	base := models.RecordObjects.Using(backend).Filter(models.RecordFields.Label.In(names...))
	for _, tc := range reference.Containment.Queries {
		if tc.Exception != "" {
			t.Fatal("unexpected reference containment exception", tc.Exception)
		}
		var field containmentField[models.Record] = models.RecordFields.Payload
		var path []query.JSONPathSegment
		if tc.Scope == "a" {
			path = []query.JSONPathSegment{query.JSONKey("a")}
			field = models.RecordFields.Payload.At(path...)
		} else if tc.Scope != "root" {
			t.Fatal("unknown reference scope")
		}
		value := document(t, tc.RHS)
		typed := containmentPredicate(t, field, tc.Lookup, value)
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "payload__" + tc.Lookup, Value: value, JSONPath: path}})
		if err != nil || len(dynamic) != 1 {
			t.Fatal(err)
		}
		if !base.Filter(typed).Plan().Equal(base.Filter(dynamic[0]).Plan()) {
			t.Fatal("typed/dynamic containment diverged")
		}
		for _, predicate := range []orm.Predicate[models.Record]{typed, dynamic[0]} {
			if tc.Mode == "exclude" {
				predicate = orm.Not(predicate)
			} else if tc.Mode != "filter" {
				t.Fatal("unknown reference mode")
			}
			qs := base.Filter(predicate).OrderBy(models.RecordFields.ID.Asc())
			var before uint64
			if !native {
				before = backend.(*sqlite.Backend).QueryCount()
			}
			rows, err := qs.All(ctx)
			if !native {
				want := &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Lookup: tc.Lookup}
				if !errors.Is(err, want) || rows != nil {
					t.Fatal("SQLite containment did not reject capability", err)
				}
				if _, err := qs.Count(ctx); !errors.Is(err, want) {
					t.Fatal("SQLite COUNT bypassed capability", err)
				}
				empty, err := qs.Limit(0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := empty.All(ctx); !errors.Is(err, want) {
					t.Fatal("LIMIT 0 hid unsupported containment", err)
				}
				if _, err := qs.Filter(models.RecordFields.ID.In()).All(ctx); !errors.Is(err, want) {
					t.Fatal("empty IN hid unsupported containment", err)
				}
				if backend.(*sqlite.Backend).QueryCount() != before {
					t.Fatal("unsupported containment performed I/O")
				}
				continue
			}
			if err != nil {
				t.Fatal(tc, err)
			}
			labels := []string{}
			for _, row := range rows {
				labels = append(labels, strings.TrimPrefix(row.Label, "contain_"))
			}
			if !slices.Equal(labels, tc.Rows) {
				t.Fatalf("containment %s %s %s %s = %v want %v", tc.Scope, tc.Lookup, tc.Mode, tc.RHS, labels, tc.Rows)
			}
			count, err := qs.Count(ctx)
			if err != nil || count != int64(len(tc.Rows)) {
				t.Fatal("containment Count", tc, count, err)
			}
		}
		if native {
			// Required mirrors each JSON document; only the SQL NULL fixture becomes
			// JSON null. Its expected membership must therefore match json_null.
			var required containmentField[models.Record] = models.RecordFields.Required
			if tc.Scope == "a" {
				required = models.RecordFields.Required.At(path...)
			}
			predicate := containmentPredicate(t, required, tc.Lookup, value)
			if tc.Mode == "exclude" {
				predicate = orm.Not(predicate)
			}
			got, err := base.Filter(predicate).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := slices.DeleteFunc(slices.Clone(tc.Rows), func(s string) bool { return s == "sql_null" })
			if slices.Contains(tc.Rows, "json_null") {
				want = append(want, "sql_null")
			}
			labels := []string{}
			for _, row := range got {
				labels = append(labels, strings.TrimPrefix(row.Label, "contain_"))
			}
			slices.Sort(labels)
			slices.Sort(want)
			if !slices.Equal(labels, want) {
				t.Fatal("non-nullable containment", tc, labels, want)
			}
		}
	}
	for _, lookup := range []query.Lookup{query.LookupContains, query.LookupContainedBy} {
		for _, raw := range []any{nil, "{}", map[string]any{"a": 1}, jsonvalue.Value{}} {
			if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "payload__" + string(lookup), Value: raw}}); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
				t.Fatal("containment accepted untyped input", err)
			}
		}
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, func(ir.Field, query.Lookup) bool { return false }, []orm.LookupInput{{Key: "payload__" + string(lookup), Value: jsonvalue.Null()}}); !errors.Is(err, &query.Error{Code: query.CodeDisallowedLookup}) {
			t.Fatal("containment bypassed policy", err)
		}
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "label__" + string(lookup), Value: "x"}}); !errors.Is(err, &query.Error{Code: query.CodeUnsupportedLookup}) {
			t.Fatal("JSON containment widened string lookups", err)
		}
		if native {
			for _, raw := range []string{`"\u0000"`, `{"a":"\u0000"}`, `1e1000000`} {
				predicate := containmentPredicate(t, containmentField[models.Record](models.RecordFields.Payload), string(lookup), document(t, raw))
				for _, qs := range []orm.QuerySet[models.Record]{base.Filter(predicate), base.Filter(predicate, models.RecordFields.ID.In())} {
					if _, err := qs.All(ctx); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
						t.Fatal("containment bypassed native parameter limits", err)
					}
				}
			}
		}
	}
	verifyJSONContainmentRelations(t, backend, native, records, reference.Containment.Queries)
}

func verifyJSONContainmentRelations(t *testing.T, backend jsonBackend, native bool, records []models.Record, cases []containmentCase) {
	t.Helper()
	ctx := t.Context()
	names := []string{"contain_absent"}
	for _, record := range records {
		names = append(names, record.Label)
		if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate(record.Label).WithRecordID(record.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.LinkObjects.Create(ctx, backend, models.NewLinkCreate("contain_absent")); err != nil {
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
	model, err := orm.BindModel(bound, ir.ModelIdentity{AppLabel: "jsonref", ModelName: "link"}, models.LinkDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	facade, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if tc.RHS != `{"a":1}` && tc.RHS != "null" {
			continue
		}
		var field containmentField[models.Link] = related.ModelsLink.Record.Payload
		var path []query.JSONPathSegment
		if tc.Scope == "a" {
			path = []query.JSONPathSegment{query.JSONKey("a")}
			field = related.ModelsLink.Record.Payload.At(path...)
		}
		value := document(t, tc.RHS)
		typed := containmentPredicate(t, field, tc.Lookup, value)
		parsed, err := orm.ParseDynamicRelations(model, nil, []orm.LookupInput{{Key: "record__payload__" + tc.Lookup, Value: value, JSONPath: path}})
		if err != nil {
			t.Fatal(err)
		}
		for _, predicate := range []orm.Predicate[models.Link]{typed, parsed[0]} {
			if tc.Mode == "exclude" {
				predicate = orm.Not(predicate)
			}
			qs := facade.ModelsLink.Filter(models.LinkFields.Label.In(names...), predicate)
			rows, err := qs.SelectRelated(facade.ModelsLink.Related.Record).All(ctx)
			if !native {
				if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported}) {
					t.Fatal("SQLite forward containment", err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(tc.Rows)
			if tc.Mode == "exclude" {
				want = append(want, "absent")
			}
			slices.Sort(want)
			labels := []string{}
			for _, row := range rows {
				raw, err := row.Unwrap()
				if err != nil {
					t.Fatal(err)
				}
				labels = append(labels, strings.TrimPrefix(raw.Label, "contain_"))
				_, present, err := row.Record(ctx)
				if err != nil || present != (raw.RecordID != nil) {
					t.Fatal("containment eager presence", err)
				}
			}
			slices.Sort(labels)
			if !slices.Equal(labels, want) {
				t.Fatal("optional forward containment", tc, labels, want)
			}
			count, err := qs.Count(ctx)
			if err != nil || count != int64(len(want)) {
				t.Fatal("forward containment count", err)
			}
		}
	}

	if native {
		for _, predicate := range []orm.Predicate[models.Link]{
			orm.Not(related.ModelsLink.Record.Required.Contains(jsonvalue.Null())),
			orm.Or(related.ModelsLink.Record.Required.Contains(document(t, `{"a":1}`)), models.LinkFields.Label.Exact("contain_absent")),
		} {
			rows, err := facade.ModelsLink.Filter(models.LinkFields.Label.In("contain_absent", "contain_sql_null", "contain_json_null", "contain_object"), predicate).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			labels := []string{}
			for _, row := range rows {
				labels = append(labels, row.Label)
			}
			slices.Sort(labels)
			if !slices.Equal(labels, []string{"contain_absent", "contain_object"}) {
				t.Fatal("optional non-null target and OR/NOT", labels)
			}
		}
	}
	reverse, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	for _, predicate := range []orm.Predicate[models.Record]{reverse.ModelsRecord.Links.Token.Contains(jsonvalue.Null()), reverse.ModelsRecord.Links.Token.At(query.JSONKey("a")).ContainedBy(jsonvalue.Null())} {
		if _, err := models.RecordObjects.Using(backend).Filter(predicate).All(ctx); !errors.Is(err, &query.Error{Code: query.CodeUnsupportedLookup}) {
			t.Fatal("containment silently widened reverse traversal", err)
		}
	}
}
