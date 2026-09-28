package forms_test

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func TestSubmittedRetainsPresenceAndRepeatedValuesWithoutAliasing(t *testing.T) {
	field, err := forms.CharField("password", forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	source := map[string][]string{"password": {"private-one", "private-two"}, "empty": {}}
	data := forms.NewData(source)
	bound, err := spec.Bind(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	source["password"][0] = "changed"
	got, present := bound.Submitted().Get("password")
	if !present || !reflect.DeepEqual(got, []string{"private-one", "private-two"}) {
		t.Fatal("submission was normalized or retained caller slice")
	}
	got[0] = "changed again"
	if unchanged, _ := bound.Submitted().Get("password"); unchanged[0] != "private-one" {
		t.Fatal("getter exposed input slice")
	}
	if _, ok := bound.Submitted().Get("empty"); !ok {
		t.Fatal("present empty list was omitted")
	}
	if _, ok := bound.Submitted().Get("omitted"); ok {
		t.Fatal("absent key was synthesized")
	}
	withErrors, err := bound.WithErrors(validation.NewErrors(validation.New(validation.NonField, "later")))
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := withErrors.Submitted().Get("password"); !reflect.DeepEqual(raw, []string{"private-one", "private-two"}) {
		t.Fatal("post-clean dropped input")
	}
}

func TestCrossErrorsLeaveCleanedBeforeFollowingValidator(t *testing.T) {
	field, err := forms.CharField("name")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	spec, err := forms.NewSpec([]forms.Field{field},
		forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
			if _, ok := values.Get("name"); !ok {
				t.Fatal("first validator missed value")
			}
			return validation.NewErrors(validation.New("name", "first"))
		}),
		forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
			calls++
			if _, ok := values.Get("name"); ok {
				t.Fatal("rejected value remained in next validator")
			}
			return validation.NewErrors(validation.New(validation.NonField, "second"))
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{"name": {"value"}}), nil)
	if err != nil || calls != 1 || bound.Errors().Len() != 2 || len(bound.Cleaned().All()) != 0 {
		t.Fatal("cross validation stages changed", err)
	}
}

func TestNewValuesOwnsItsSortedSnapshot(t *testing.T) {
	source := map[string]forms.Value{"z": forms.Integer(1), "a": forms.String("private")}
	values := forms.NewValues(source)
	delete(source, "a")
	source["z"] = forms.Integer(2)
	entries := values.All()
	if len(entries) != 2 || entries[0].Name() != "a" || entries[1].Name() != "z" {
		t.Fatal("snapshot lost lexical order")
	}
	entries[0] = forms.Entry{}
	if value, ok := values.Integer("z"); !ok || value != 1 {
		t.Fatal("snapshot borrowed input map")
	}
	if values.All()[0].Name() != "a" {
		t.Fatal("snapshot exposed order slice")
	}
}
