package forms

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/validation"
)

func TestFormWithErrorsRetainsInitialChangesAndDetachesCleanedFields(t *testing.T) {
	first, err := CharField("first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CharField("second")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := NewSpec([]Field{first, second})
	if err != nil {
		t.Fatal(err)
	}
	form, err := spec.Bind(NewData(map[string][]string{"first": {"one"}, "second": {"two"}}), map[string]Value{"first": String("before")})
	if err != nil || !form.Valid() {
		t.Fatal(err)
	}
	with, err := form.WithErrors(validation.NewErrors(validation.New("first", validation.CodeUnique)))
	if err != nil || !with.Bound() || with.Valid() || with.Errors().Len() != 1 {
		t.Fatal("post-clean field errors", with.Errors().All(), err)
	}
	if _, ok := with.Cleaned().Get("first"); ok {
		t.Fatal("rejected field remains in cleaned data")
	}
	if value, ok := with.Cleaned().Get("second"); !ok || !value.Equal(String("two")) {
		t.Fatal("unrelated cleaned field was lost")
	}
	if value, ok := with.Initial().Get("first"); !ok || !value.Equal(String("before")) || !reflect.DeepEqual(with.Changed(), form.Changed()) {
		t.Fatal("initial/change data was modified")
	}
	if !form.Valid() || !form.Errors().Empty() {
		t.Fatal("WithErrors mutated its source form")
	}
	if value, ok := form.Cleaned().Get("first"); !ok || !value.Equal(String("one")) {
		t.Fatal("source cleaned map was borrowed")
	}
	nonField, err := with.WithErrors(validation.NewErrors(validation.New(validation.NonField, "conflict")))
	if err != nil || nonField.Errors().Len() != 2 || !reflect.DeepEqual(nonField.Cleaned().All(), with.Cleaned().All()) {
		t.Fatal("non-field diagnostic discarded cleaned input", err)
	}
	if with.Errors().Len() != 1 {
		t.Fatal("appending diagnostics changed an earlier form")
	}
	if _, err := form.WithErrors(validation.NewErrors(validation.New("hidden", "unique"))); err == nil {
		t.Fatal("unknown error field accepted")
	}
	unbound, err := spec.Unbound(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unbound.WithErrors(validation.Errors{}); err == nil {
		t.Fatal("unbound form accepted post-clean errors")
	}
	unchanged, err := form.WithErrors(validation.Errors{})
	if err != nil || !reflect.DeepEqual(form, unchanged) {
		t.Fatal("empty diagnostics changed bound form", err)
	}
}

func TestFormCompoundRejectionRemovesOnlyNamedCleanedValues(t *testing.T) {
	first, err := CharField("first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CharField("second")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := NewSpec([]Field{first, second})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(NewData(map[string][]string{"first": {"private-one"}, "second": {"private-two"}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := validation.NewErrors(validation.New(validation.NonField, "unique"))
	rejected, err := bound.WithErrors(diagnostic, "first")
	if err != nil || rejected.Valid() || !bound.Valid() || rejected.Errors().Len() != 1 {
		t.Fatal("compound rejection mutated input or lost non-field error", err)
	}
	if _, present := rejected.Cleaned().Get("first"); present {
		t.Fatal("rejected member retained")
	}
	if value, present := rejected.Cleaned().String("second"); !present || value != "private-two" {
		t.Fatal("unrelated member removed")
	}
	if raw, present := rejected.Submitted().Get("first"); !present || len(raw) != 1 || raw[0] != "private-one" || !reflect.DeepEqual(rejected.Changed(), bound.Changed()) {
		t.Fatal("original submission/change detection lost")
	}
	if _, err := bound.WithErrors(diagnostic, "unknown"); err == nil {
		t.Fatal("unknown compound member accepted")
	}
	if _, err := bound.WithErrors(validation.Errors{}, "first"); err == nil {
		t.Fatal("cleaned data removed without a diagnostic")
	}
}
