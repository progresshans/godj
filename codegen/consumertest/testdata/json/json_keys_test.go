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
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed keys_sqlite_reference.json
var keysSQLiteReference []byte

//go:embed keys_postgres_reference.json
var keysPostgresReference []byte

type keysCase struct {
	Scope, Lookup, Mode string
	Keys                json.RawMessage
	Rows                []string
	Exception           string
}
type keysReference struct {
	Samples []struct {
		Label string
		Raw   *string
	}
	Queries []keysCase
}
type keysField[M any] interface {
	HasKey(string) orm.Predicate[M]
	HasKeys(...string) orm.Predicate[M]
	HasAnyKeys(...string) orm.Predicate[M]
}

func keyInput(t *testing.T, tc keysCase) (any, []string) {
	t.Helper()
	if tc.Lookup == "has_key" {
		var key string
		if err := json.Unmarshal(tc.Keys, &key); err != nil {
			t.Fatal(err)
		}
		return key, []string{key}
	}
	var keys []string
	if err := json.Unmarshal(tc.Keys, &keys); err != nil || keys == nil {
		t.Fatal("invalid reference key list", err)
	}
	return keys, keys
}
func keyPredicate[M any](t *testing.T, f keysField[M], lookup string, keys []string) orm.Predicate[M] {
	t.Helper()
	switch lookup {
	case "has_key":
		return f.HasKey(keys[0])
	case "has_keys":
		return f.HasKeys(keys...)
	case "has_any_keys":
		return f.HasAnyKeys(keys...)
	default:
		t.Fatal("unknown key lookup", lookup)
		return orm.Predicate[M]{}
	}
}

// Only these exact SQLite selectors depart from the preserved Django raw.
// Empty lists extend its OperationalError with PostgreSQL's three-valued
// semantics; empty/NUL keys correct SQLite's native prefix collision.
func keyExpected(t *testing.T, tc keysCase, ref keysReference, native bool) (rows []string, invalid, deviation bool) {
	t.Helper()
	_, keys := keyInput(t, tc)
	if native {
		if slices.Contains(keys, "\x00") {
			if tc.Exception != "DataError" || tc.Rows != nil {
				t.Fatal("native NUL reference changed", tc)
			}
			return nil, true, false
		}
		if tc.Exception != "" {
			t.Fatal("unexpected native key exception", tc)
		}
		return slices.Clone(tc.Rows), false, false
	}
	if len(keys) == 0 {
		if tc.Exception != "OperationalError" || tc.Rows != nil {
			t.Fatal("SQLite empty-list reference changed", tc)
		}
		present := []string{"object", "key_null", "nested_object", "nested_array", "nested_string", "nested_empty"}
		if tc.Scope == "root" {
			present = nil
			for _, sample := range ref.Samples {
				if sample.Label != "sql_null" {
					present = append(present, sample.Label)
				}
			}
		}
		if tc.Lookup == "has_keys" && tc.Mode == "filter" {
			return present, false, true
		}
		if tc.Lookup == "has_any_keys" && tc.Mode == "exclude" {
			return append([]string{"sql_null"}, present...), false, true
		}
		if tc.Mode == "exclude" {
			return []string{"sql_null"}, false, true
		}
		return []string{}, false, true
	}
	if tc.Exception != "" {
		t.Fatal("unexpected SQLite key exception", tc)
	}
	if tc.Scope == "root" && len(keys) == 1 && (keys[0] == "" || keys[0] == "\x00") {
		matches := []string{"empty_key", "empty_and_nul"}
		if keys[0] == "\x00" {
			matches[0] = "nul_key"
		}
		if tc.Mode == "filter" {
			return matches, false, true
		}
		for _, sample := range ref.Samples {
			if !slices.Contains(matches, sample.Label) {
				rows = append(rows, sample.Label)
			}
		}
		return rows, false, true
	}
	return slices.Clone(tc.Rows), false, false
}

func verifyJSONKeys(t *testing.T, backend jsonBackend, native bool) {
	t.Helper()
	raw, sampleCount := keysSQLiteReference, 19
	if native {
		raw, sampleCount = keysPostgresReference, 17
	}
	var wire struct {
		Keys keysReference `json:"key_presence"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	ref := wire.Keys
	if len(ref.Samples) != sampleCount || len(ref.Queries) != 96 {
		t.Fatal("key reference inventory changed")
	}
	var names []string
	var records []models.Record
	for _, sample := range ref.Samples {
		name := "keys_" + sample.Label
		names = append(names, name)
		value := jsonvalue.Null()
		if sample.Raw != nil {
			value = document(t, *sample.Raw)
		}
		input := models.NewRecordCreate(name, value)
		if sample.Raw != nil {
			input = input.WithPayload(value)
		}
		row, err := models.RecordObjects.Create(t.Context(), backend, input)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, row)
	}
	base := models.RecordObjects.Using(backend).Filter(models.RecordFields.Label.In(names...))
	deviations, invalids := 0, 0
	for _, tc := range ref.Queries {
		input, keys := keyInput(t, tc)
		want, invalid, deviation := keyExpected(t, tc, ref, native)
		if deviation {
			deviations++
		}
		if invalid {
			invalids++
		}
		var f keysField[models.Record] = models.RecordFields.Payload
		var required keysField[models.Record] = models.RecordFields.Required
		var path []query.JSONPathSegment
		if tc.Scope == "a" {
			path = []query.JSONPathSegment{query.JSONKey("a")}
			f, required = models.RecordFields.Payload.At(path...), models.RecordFields.Required.At(path...)
		} else if tc.Scope != "root" {
			t.Fatal("unknown key scope")
		}
		typed := keyPredicate(t, f, tc.Lookup, keys)
		dynamic, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "payload__" + tc.Lookup, Value: input, JSONPath: path}})
		if err != nil || len(dynamic) != 1 {
			t.Fatal(err)
		}
		if !base.Filter(typed).Plan().Equal(base.Filter(dynamic[0]).Plan()) {
			t.Fatal("typed/dynamic key AST diverged")
		}
		for _, predicate := range []orm.Predicate[models.Record]{typed, dynamic[0]} {
			if tc.Mode == "exclude" {
				predicate = orm.Not(predicate)
			} else if tc.Mode != "filter" {
				t.Fatal("unknown key mode")
			}
			qs := base.Filter(predicate).OrderBy(models.RecordFields.ID.Asc())
			if invalid {
				empty, err := qs.Limit(0)
				if err != nil {
					t.Fatal(err)
				}
				for _, candidate := range []orm.QuerySet[models.Record]{qs, empty, qs.Filter(models.RecordFields.ID.In())} {
					if _, err := candidate.All(t.Context()); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidValue}) {
						t.Fatal("NUL key bypassed backend preflight", err)
					}
					if _, err := candidate.Count(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
						t.Fatal("NUL key Count bypassed preflight", err)
					}
				}
				continue
			}
			rows, err := qs.All(t.Context())
			if err != nil {
				t.Fatal(tc, err)
			}
			labels := []string{}
			for _, row := range rows {
				labels = append(labels, strings.TrimPrefix(row.Label, "keys_"))
			}
			if !slices.Equal(labels, want) {
				t.Fatalf("keys %s %s %s %s = %v want %v", tc.Scope, tc.Lookup, tc.Mode, tc.Keys, labels, want)
			}
			if count, err := qs.Count(t.Context()); err != nil || count != int64(len(want)) {
				t.Fatal("key Count", count, want, err)
			}
		}
		if !invalid {
			predicate := keyPredicate(t, required, tc.Lookup, keys)
			if tc.Mode == "exclude" {
				predicate = orm.Not(predicate)
			}
			rows, err := base.Filter(predicate).All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			labels := []string{}
			for _, row := range rows {
				labels = append(labels, strings.TrimPrefix(row.Label, "keys_"))
			}
			requiredWant := slices.DeleteFunc(slices.Clone(want), func(s string) bool { return s == "sql_null" })
			if slices.Contains(want, "json_null") {
				requiredWant = append(requiredWant, "sql_null")
			}
			slices.Sort(labels)
			slices.Sort(requiredWant)
			if !slices.Equal(labels, requiredWant) {
				t.Fatal("non-nullable key lookup", tc, labels, requiredWant)
			}
		}
	}
	if native && (invalids != 12 || deviations != 0) || !native && (invalids != 0 || deviations != 20) {
		t.Fatal("key reference/deviation coverage changed", invalids, deviations)
	}
	verifyJSONKeyInputBoundaries(t, backend, base)
	verifyJSONKeyRelations(t, backend, native, records, ref)
}

func verifyJSONKeyInputBoundaries(t *testing.T, backend jsonBackend, base orm.QuerySet[models.Record]) {
	t.Helper()
	for _, lookup := range []string{"has_key", "has_keys", "has_any_keys"} {
		invalid := []any{nil, 1, jsonvalue.Null(), map[string]any{"a": 1}}
		var valid any = []string{"a"}
		if lookup == "has_key" {
			valid = "a"
			invalid = append(invalid, []string{"a"}, []any{"a"})
		} else {
			invalid = append(invalid, "a", []any{"a", 1}, []int{1})
		}
		for _, value := range invalid {
			if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "payload__" + lookup, Value: value}}); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
				t.Fatal("invalid dynamic keys accepted", err)
			}
		}
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, func(ir.Field, query.Lookup) bool { return false }, []orm.LookupInput{{Key: "payload__" + lookup, Value: valid}}); !errors.Is(err, &query.Error{Code: query.CodeDisallowedLookup}) {
			t.Fatal("key lookup bypassed policy", err)
		}
		if _, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "label__" + lookup, Value: valid}}); !errors.Is(err, &query.Error{Code: query.CodeUnsupportedLookup}) {
			t.Fatal("JSON keys widened string lookup", err)
		}
	}
	for _, predicate := range []orm.Predicate[models.Record]{models.RecordFields.Payload.HasKeys(make([]string, query.MaxJSONKeys+1)...), models.RecordFields.Payload.HasKey(strings.Repeat("a", query.MaxJSONKeyBytes+1)), models.RecordFields.Payload.HasAnyKeys(string([]byte{255})), models.RecordFields.Payload.At().HasKey("a"), (orm.JSONPathField[models.Record]{}).HasKey("a")} {
		if _, err := base.Filter(predicate).All(t.Context()); err == nil {
			t.Fatal("invalid typed key boundary accepted")
		}
	}
	keys := []string{"a"}
	inputs := []any{"a"}
	typed := models.RecordFields.Payload.HasKeys(keys...)
	parsed, err := orm.ParseDynamic(models.RecordDescriptor{}, nil, []orm.LookupInput{{Key: "payload__has_keys", Value: inputs}})
	if err != nil {
		t.Fatal(err)
	}
	keys[0], inputs[0] = "changed", "changed"
	qs := base.Filter(typed).OrderBy(models.RecordFields.ID.Asc())
	if !qs.Plan().Equal(base.Filter(parsed[0]).OrderBy(models.RecordFields.ID.Asc()).Plan()) {
		t.Fatal("caller key mutation changed AST")
	}
	before, err := qs.All(t.Context())
	if err != nil || len(before) == 0 {
		t.Fatal("key ownership fixture", err)
	}
	owned, ok := qs.Plan().Conditions()[1].JSONKeys()
	if !ok {
		t.Fatal("key condition absent")
	}
	owned.Values()[0] = "changed"
	after, err := qs.Fresh().All(t.Context())
	if err != nil || len(after) != len(before) {
		t.Fatal("key getter mutation changed fresh query", err)
	}
	for i := range before {
		if before[i].ID != after[i].ID {
			t.Fatal("owned key query changed membership")
		}
	}
	key := "a\"\\😀') OR 1=1 --"
	encoded, _ := json.Marshal(map[string]any{key: nil, "a.b": []any{map[string]any{"0": true}}})
	value := document(t, string(encoded))
	row, err := models.RecordObjects.Create(t.Context(), backend, models.NewRecordCreate("keys_literal", value).WithPayload(value))
	if err != nil {
		t.Fatal(err)
	}
	for _, predicate := range []orm.Predicate[models.Record]{models.RecordFields.Payload.HasKey(key), models.RecordFields.Payload.At(query.JSONKey("a.b"), query.JSONIndex(0)).HasKey("0")} {
		rows, err := models.RecordObjects.Using(backend).Filter(predicate).All(t.Context())
		if err != nil || len(rows) != 1 || rows[0].ID != row.ID {
			t.Fatal("literal key/index parameter changed meaning", err)
		}
	}
}

func verifyJSONKeyRelations(t *testing.T, backend jsonBackend, native bool, records []models.Record, ref keysReference) {
	t.Helper()
	names := []string{"keys_absent"}
	for _, row := range records {
		names = append(names, row.Label)
		if _, err := models.LinkObjects.Create(t.Context(), backend, models.NewLinkCreate(row.Label).WithRecordID(row.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.LinkObjects.Create(t.Context(), backend, models.NewLinkCreate("keys_absent")); err != nil {
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
	for _, tc := range ref.Queries {
		input, keys := keyInput(t, tc)
		want, invalid, _ := keyExpected(t, tc, ref, native)
		if invalid {
			continue
		} // Root loop owns every backend preflight rejection.
		var field keysField[models.Link] = related.ModelsLink.Record.Payload
		var path []query.JSONPathSegment
		if tc.Scope == "a" {
			path = []query.JSONPathSegment{query.JSONKey("a")}
			field = related.ModelsLink.Record.Payload.At(path...)
		}
		predicate := keyPredicate(t, field, tc.Lookup, keys)
		parsed, err := orm.ParseDynamicRelations(model, nil, []orm.LookupInput{{Key: "record__payload__" + tc.Lookup, Value: input, JSONPath: path}})
		if err != nil {
			t.Fatal(err)
		}
		if !models.LinkObjects.Using(backend).Filter(predicate).Plan().Equal(models.LinkObjects.Using(backend).Filter(parsed[0]).Plan()) {
			t.Fatal("forward key AST diverged")
		}
		if tc.Mode == "exclude" {
			predicate = orm.Not(predicate)
			want = append(want, "absent")
		}
		qs := facade.ModelsLink.Filter(models.LinkFields.Label.In(names...), predicate)
		rows, err := qs.SelectRelated(facade.ModelsLink.Related.Record).All(t.Context())
		if err != nil {
			t.Fatal(tc, err)
		}
		labels := []string{}
		for _, row := range rows {
			raw, err := row.Unwrap()
			if err != nil {
				t.Fatal(err)
			}
			labels = append(labels, strings.TrimPrefix(raw.Label, "keys_"))
			_, present, err := row.Record(t.Context())
			if err != nil || present != (raw.RecordID != nil) {
				t.Fatal("key eager presence", err)
			}
		}
		slices.Sort(labels)
		slices.Sort(want)
		if !slices.Equal(labels, want) {
			t.Fatal("optional forward keys", tc, labels, want)
		}
		if count, err := qs.Count(t.Context()); err != nil || count != int64(len(want)) {
			t.Fatal("forward key Count", err)
		}
	}
	for _, predicate := range []orm.Predicate[models.Link]{orm.Not(related.ModelsLink.Record.Required.HasKey("a")), orm.Not(related.ModelsLink.Record.Required.HasKeys()), orm.Or(related.ModelsLink.Record.Required.HasAnyKeys(), models.LinkFields.Label.Exact("keys_absent")), orm.Or(orm.Not(orm.Not(related.ModelsLink.Record.Required.HasAnyKeys())), models.LinkFields.Label.Exact("keys_absent"))} {
		rows, err := facade.ModelsLink.Filter(models.LinkFields.Label.In("keys_object", "keys_absent"), predicate).All(t.Context())
		if err != nil || len(rows) != 1 || rows[0].Label != "keys_absent" {
			t.Fatal("nullable JOIN Boolean key predicate", err)
		}
	}
	if _, err := orm.ParseDynamicRelations(model, func(ir.Field, query.Lookup) bool { return false }, []orm.LookupInput{{Key: "record__payload__has_key", Value: "a"}}); !errors.Is(err, &query.Error{Code: query.CodeDisallowedLookup}) {
		t.Fatal("forward key policy bypass", err)
	}
	reverse, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	checkJSONCollectionLookup(t, backend, true, "has_key", reverse.ModelsRecord.Links.Token.HasKey("hit"), orm.LookupInput{Key: "links__token__has_key", Value: "hit"})
	checkJSONCollectionLookup(t, backend, true, "has_any_keys", reverse.ModelsRecord.Links.Token.At(query.JSONKey("a")).HasAnyKeys("match"), orm.LookupInput{Key: "links__token__has_any_keys", Value: []string{"match"}, JSONPath: []query.JSONPathSegment{query.JSONKey("a")}})
}
