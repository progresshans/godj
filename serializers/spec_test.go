package serializers_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
)

func TestSpecFullAndPartialPresenceDefaultNullEmptySemantics(t *testing.T) {
	spec := articleSpec(t)
	fullObject := decodeObject(t, `{"title":"  Go  ","summary":null}`)
	full, err := spec.Bind(fullObject, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if !full.Valid() || !full.Errors().Empty() {
		t.Fatalf("full = valid %v errors %#v", full.Valid(), full.Errors().All())
	}
	fullEntries := full.Values().All()
	if got, want := entryNames(fullEntries), []string{"title", "published", "summary"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("full order = %v, want %v", got, want)
	}
	if title, _ := fullEntries[0].Value().AsString(); title != "Go" {
		t.Fatalf("cleaned title = %q", title)
	}
	if published, ok := fullEntries[1].Value().AsBoolean(); !ok || published {
		t.Fatalf("default published = %v, %v", published, ok)
	}
	if !fullEntries[2].Value().IsNull() {
		t.Fatal("summary null was not retained")
	}

	partialObject := decodeObject(t, `{"summary":""}`)
	partial, err := spec.Bind(partialObject, serializers.ModePartial)
	if err != nil {
		t.Fatal(err)
	}
	if !partial.Valid() {
		t.Fatalf("partial errors = %#v", partial.Errors().All())
	}
	partialEntries := partial.Values().All()
	if got := entryNames(partialEntries); !reflect.DeepEqual(got, []string{"summary"}) {
		t.Fatalf("partial order = %v", got)
	}
	if summary, ok := partialEntries[0].Value().AsString(); !ok || summary != "" {
		t.Fatalf("partial summary = %q, %v", summary, ok)
	}
	if _, present := partial.Values().Get("published"); present {
		t.Fatal("partial mode applied omitted default")
	}
}

func TestSpecOrdersDeclaredErrorsBeforeLexicallySortedUnknownFields(t *testing.T) {
	spec := articleSpec(t)
	object := decodeObject(t, `{"zeta":1,"title":false,"id":4,"alpha":2}`)
	result, err := spec.Bind(object, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() {
		t.Fatal("invalid input reported valid")
	}
	violations := result.Errors().All()
	want := []struct {
		field validation.Field
		code  validation.Code
	}{
		{field: "id", code: serializers.CodeReadOnly},
		{field: "title", code: serializers.CodeType},
		{field: "alpha", code: serializers.CodeUnknown},
		{field: "zeta", code: serializers.CodeUnknown},
	}
	if len(violations) != len(want) {
		t.Fatalf("violations = %#v", violations)
	}
	for index := range want {
		if violations[index].Field() != want[index].field || violations[index].Code() != want[index].code {
			t.Fatalf("violation[%d] = (%q,%q), want (%q,%q)", index, violations[index].Field(), violations[index].Code(), want[index].field, want[index].code)
		}
	}
	if _, present := result.Values().Get("published"); !present {
		t.Fatal("valid default was not retained alongside field errors")
	}
}

func TestSpecRequiredBlankNullLengthAndModeValidation(t *testing.T) {
	spec := articleSpec(t)
	tests := []struct {
		name string
		json string
		code validation.Code
	}{
		{name: "required", json: `{}`, code: serializers.CodeRequired},
		{name: "blank", json: `{"title":"   "}`, code: serializers.CodeBlank},
		{name: "null", json: `{"title":null}`, code: serializers.CodeNull},
		{name: "max length", json: `{"title":"123456"}`, code: serializers.CodeMaxLength},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := spec.Bind(decodeObject(t, test.json), serializers.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			violations := result.Errors().ByField("title").All()
			if len(violations) != 1 || violations[0].Code() != test.code {
				t.Fatalf("violations = %#v", violations)
			}
			if test.code == serializers.CodeMaxLength {
				params := violations[0].Params()
				if len(params) != 1 || params[0].Key() != "max_length" || params[0].Value() != "5" {
					t.Fatalf("params = %#v", params)
				}
			}
		})
	}
	if _, err := spec.Bind(decodeObject(t, `{}`), serializers.Mode(99)); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig, Field: "mode"}) {
		t.Fatalf("mode error = %v", err)
	}
}

func TestFieldAndSpecConstructionFailClosed(t *testing.T) {
	if _, err := serializers.StringField("bad-name"); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
		t.Fatalf("name error = %v", err)
	}
	var nilOption serializers.FieldOption
	if _, err := serializers.StringField("title", nilOption); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
		t.Fatalf("nil option error = %v", err)
	}
	if _, err := serializers.BooleanField("published", serializers.WithMaxLength(1)); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
		t.Fatalf("boolean string option error = %v", err)
	}
	if _, err := serializers.BooleanField("published", serializers.WithMaxLength(0)); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
		t.Fatalf("boolean no-op max length error = %v", err)
	}
	if _, err := serializers.IntegerField("id", serializers.WithTrimWhitespace(false)); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
		t.Fatalf("integer no-op trim error = %v", err)
	}
	if _, err := serializers.IntegerField("id", serializers.WithReadOnly(), serializers.WithDefault(serializers.Integer(1))); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
		t.Fatalf("read-only default error = %v", err)
	}
	title, err := serializers.StringField("title")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serializers.NewSpec([]serializers.Field{title, title}); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
		t.Fatalf("duplicate spec error = %v", err)
	}
}

func TestSpecAndResultAreDetachedFromReturnedSnapshots(t *testing.T) {
	spec := articleSpec(t)
	fields := spec.Fields()
	fields[0] = serializers.Field{}
	result, err := spec.Bind(decodeObject(t, `{"title":"Go"}`), serializers.ModeFull)
	if err != nil || !result.Valid() {
		t.Fatalf("bind = %#v, %v", result.Errors().All(), err)
	}
	entries := result.Values().All()
	entries[0] = serializers.Entry{}
	if title, ok := result.Values().Get("title"); !ok {
		t.Fatal("result mutated through entries")
	} else if value, _ := title.AsString(); value != "Go" {
		t.Fatalf("title = %q", value)
	}
}

func articleSpec(t *testing.T) serializers.Spec {
	t.Helper()
	id, err := serializers.IntegerField("id", serializers.WithReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	title, err := serializers.StringField("title", serializers.WithMaxLength(5))
	if err != nil {
		t.Fatal(err)
	}
	published, err := serializers.BooleanField("published", serializers.WithDefault(serializers.Boolean(false)))
	if err != nil {
		t.Fatal(err)
	}
	summary, err := serializers.StringField(
		"summary",
		serializers.WithRequired(false),
		serializers.WithNullable(),
		serializers.WithAllowEmpty(),
	)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{id, title, published, summary})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func decodeObject(t *testing.T, document string) serializers.Object {
	t.Helper()
	object, err := serializers.DecodeObject([]byte(document), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return object
}

func entryNames(entries []serializers.Entry) []string {
	names := make([]string, len(entries))
	for index := range entries {
		names[index] = entries[index].Name()
	}
	return names
}
