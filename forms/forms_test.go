package forms_test

import (
	"sync"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func articleSpec(t *testing.T, cross ...forms.CrossValidator) forms.Spec {
	t.Helper()
	title, err := forms.CharField("title", forms.WithLabel("Title"), forms.WithMaxLength(8))
	if err != nil {
		t.Fatal(err)
	}
	published, err := forms.BooleanField("published", forms.WithDefault(forms.Boolean(false)))
	if err != nil {
		t.Fatal(err)
	}
	summary, err := forms.CharField(
		"summary",
		forms.WithRequired(false),
		forms.WithNullable(),
		forms.WithMaxLength(20),
	)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{title, published, summary}, cross...)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestUnboundAndBoundEmptyAreDistinct(t *testing.T) {
	spec := articleSpec(t)
	unbound, err := spec.Unbound(nil)
	if err != nil {
		t.Fatal(err)
	}
	if unbound.Bound() || unbound.Valid() || !unbound.Errors().Empty() || len(unbound.Cleaned().All()) != 0 {
		t.Fatalf("unbound state = bound %v valid %v errors %d cleaned %d",
			unbound.Bound(), unbound.Valid(), unbound.Errors().Len(), len(unbound.Cleaned().All()))
	}
	initial := unbound.Initial()
	if title, ok := initial.String("title"); !ok || title != "" {
		t.Fatalf("initial title = %q, %v", title, ok)
	}
	if published, ok := initial.Boolean("published"); !ok || published {
		t.Fatalf("initial published = %v, %v", published, ok)
	}
	if summary, ok := initial.Get("summary"); !ok || !summary.IsNull() {
		t.Fatalf("initial summary = %#v, %v", summary, ok)
	}

	bound, err := spec.Bind(forms.NewData(map[string][]string{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bound.Bound() || bound.Valid() {
		t.Fatalf("bound empty = bound %v valid %v", bound.Bound(), bound.Valid())
	}
	errors := bound.Errors().All()
	if len(errors) != 1 || errors[0].Field() != "title" || errors[0].Code() != "required" {
		t.Fatalf("bound empty errors = %#v", errors)
	}
}

func TestBindCleansTypedValuesAndTracksChangedInFieldOrder(t *testing.T) {
	spec := articleSpec(t)
	source := map[string][]string{
		"title":     {"  GoDj  "},
		"published": {"on"},
		"summary":   {"   "},
	}
	data := forms.NewData(source)
	source["title"][0] = "mutated"
	initial := map[string]forms.Value{"title": forms.String("Old")}
	form, err := spec.Bind(data, initial)
	if err != nil {
		t.Fatal(err)
	}
	initial["title"] = forms.String("mutated")
	if !form.Valid() || !form.Errors().Empty() {
		t.Fatalf("form valid = %v, errors = %#v", form.Valid(), form.Errors().All())
	}
	cleaned := form.Cleaned()
	if title, ok := cleaned.String("title"); !ok || title != "GoDj" {
		t.Fatalf("cleaned title = %q, %v", title, ok)
	}
	if published, ok := cleaned.Boolean("published"); !ok || !published {
		t.Fatalf("cleaned published = %v, %v", published, ok)
	}
	if summary, ok := cleaned.Get("summary"); !ok || !summary.IsNull() {
		t.Fatalf("cleaned summary = %#v, %v", summary, ok)
	}
	if got := form.Changed(); len(got) != 2 || got[0] != "title" || got[1] != "published" {
		t.Fatalf("changed = %#v", got)
	}
	changed := form.Changed()
	changed[0] = "mutated"
	if form.Changed()[0] != "title" {
		t.Fatal("Changed returned shared storage")
	}
	if got, _ := form.Initial().String("title"); got != "Old" {
		t.Fatalf("initial title = %q", got)
	}
}

func TestFieldErrorsHaveStableOrderCodesAndParameters(t *testing.T) {
	title, err := forms.CharField(
		"title",
		forms.WithMaxLength(3),
		forms.WithValidators(forms.FieldValidatorFunc(func(forms.Value) validation.Errors {
			return validation.NewErrors(validation.New("title", "custom"))
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{title})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		raw  string
		code validation.Code
	}{
		{name: "required", raw: "", code: "required"},
		{name: "nul", raw: "a\x00", code: "null_characters_not_allowed"},
		{name: "max", raw: "abcd", code: "max_length"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			form, err := spec.Bind(forms.NewData(map[string][]string{"title": {test.raw}}), nil)
			if err != nil {
				t.Fatal(err)
			}
			errors := form.Errors().All()
			wantCount := 2
			if test.code == "required" {
				wantCount = 1
			}
			if len(errors) != wantCount || errors[0].Code() != test.code {
				t.Fatalf("errors = %#v", errors)
			}
			if wantCount == 2 && errors[1].Code() != "custom" {
				t.Fatalf("custom error order = %#v", errors)
			}
			if test.code == "max_length" {
				params := errors[0].Params()
				if len(params) != 2 || params[0].Key() != "limit" || params[0].Value() != "3" ||
					params[1].Key() != "actual" || params[1].Value() != "4" {
					t.Fatalf("params = %#v", params)
				}
			}
		})
	}
}

func TestCrossValidatorReceivesOnlySuccessfullyCleanedFields(t *testing.T) {
	var seen forms.Values
	cross := forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		seen = values
		return validation.NewErrors(validation.New(validation.NonField, "title_publish_conflict"))
	})
	spec := articleSpec(t, cross)
	form, err := spec.Bind(forms.NewData(map[string][]string{
		"title":     {"too-long-title"},
		"published": {"true"},
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen.Get("title"); ok {
		t.Fatal("invalid title was present in cross-field cleaned data")
	}
	if published, ok := seen.Boolean("published"); !ok || !published {
		t.Fatalf("published = %v, %v", published, ok)
	}
	errors := form.Errors().All()
	if len(errors) != 2 || errors[0].Field() != "title" || errors[0].Code() != "max_length" ||
		errors[1].Field() != validation.NonField || errors[1].Code() != "title_publish_conflict" {
		t.Fatalf("errors = %#v", errors)
	}
}

func TestSpecAndDataAreSafeForConcurrentEvaluation(t *testing.T) {
	spec := articleSpec(t)
	data := forms.NewData(map[string][]string{
		"title":     {"GoDj"},
		"published": {"true"},
	})
	const workers = 32
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			form, err := spec.Bind(data, nil)
			if err != nil || !form.Valid() {
				t.Errorf("Bind = valid %v, err %v", form.Valid(), err)
			}
		}()
	}
	wait.Wait()
}

func TestConfigurationFailuresAreFailClosed(t *testing.T) {
	if _, err := forms.CharField("_private"); err == nil {
		t.Fatal("private field name accepted")
	}
	if _, err := forms.BooleanField("flag", forms.WithNullable()); err == nil {
		t.Fatal("nullable boolean accepted")
	}
	field, err := forms.CharField("title")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := forms.NewSpec([]forms.Field{field, field}); err == nil {
		t.Fatal("duplicate field accepted")
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spec.Unbound(map[string]forms.Value{"unknown": forms.String("x")}); err == nil {
		t.Fatal("unknown initial field accepted")
	}
	if _, err := spec.Unbound(map[string]forms.Value{"title": forms.String(string([]byte{0xff}))}); err == nil {
		t.Fatal("invalid UTF-8 initial value accepted")
	}
	if _, err := (forms.Spec{}).Bind(forms.NewData(nil), nil); err == nil {
		t.Fatal("zero Spec accepted")
	}
	var fieldValidator forms.FieldValidatorFunc
	if _, err := forms.CharField("typed_nil_field", forms.WithValidators(fieldValidator)); err == nil {
		t.Fatal("typed-nil field validator accepted")
	}
	var crossValidator forms.CrossValidatorFunc
	if _, err := forms.NewSpec([]forms.Field{field}, crossValidator); err == nil {
		t.Fatal("typed-nil cross validator accepted")
	}
}
