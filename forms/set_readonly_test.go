package forms_test

import (
	"slices"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func TestReadOnlySetInitialIgnoresEditableInputButKeepsExplicitControls(t *testing.T) {
	for _, controls := range []bool{false, true} {
		calls := 0
		name, err := forms.CharField("name", forms.WithMaxLength(3), forms.WithValidators(forms.FieldValidatorFunc(func(forms.Value) validation.Errors {
			calls++
			return validation.Errors{}
		})))
		if err != nil {
			t.Fatal(err)
		}
		row, err := forms.NewSpec([]forms.Field{name}, forms.CrossValidatorFunc(func(forms.Values) validation.Errors {
			calls++
			return validation.Errors{}
		}))
		if err != nil {
			t.Fatal(err)
		}
		config := forms.DefaultSetConfig()
		config.Prefix, config.ExtraForms, config.ReadOnlyInitial = "items", 1, true
		config.CanDelete, config.CanOrder = controls, controls
		spec, err := forms.NewSetSpec(row, config)
		if err != nil {
			t.Fatal(err)
		}
		initial := []map[string]forms.Value{{"name": forms.String("long existing value")}}
		unbound, err := spec.Unbound(initial)
		if err != nil || !unbound.Forms()[0].Form().ReadOnly() || unbound.Forms()[1].Form().ReadOnly() || calls != 0 {
			t.Fatal("unbound row policy differs", err)
		}
		data := map[string][]string{"items-TOTAL_FORMS": {"2"}, "items-INITIAL_FORMS": {"1"}, "items-0-name": {"forged", "repeated"}, "items-1-name": {"new"}}
		if controls {
			data["items-0-DELETE"], data["items-0-ORDER"] = []string{"on"}, []string{"3"}
		}
		bound, err := spec.Bind(forms.NewData(data), initial)
		if err != nil || !bound.Valid() || calls != 2 {
			t.Fatal("read-only input ran field/cross validation or blocked new input", err, calls)
		}
		first := bound.Forms()[0].Form()
		value, _ := first.Cleaned().String("name")
		if value != "long existing value" || !first.ReadOnly() || slices.Contains(first.Changed(), "name") {
			t.Fatal("read-only row lost the server snapshot")
		}
		raw, _ := first.Submitted().Get("name")
		if !slices.Equal(raw, []string{"forged", "repeated"}) {
			t.Fatal("original submission lost")
		}
		deleted, err := bound.DeletedForms()
		if err != nil || (len(deleted) == 1) != controls {
			t.Fatal("explicit deletion control was ignored", err)
		}
		initial[0]["name"] = forms.String("caller changed")
		if value, _ := first.Initial().String("name"); value != "long existing value" {
			t.Fatal("initial snapshot aliased the caller")
		}
		if controls {
			data["items-0-DELETE"], data["items-0-ORDER"] = []string{"false"}, []string{"invalid"}
			bad, err := spec.Bind(forms.NewData(data), initial)
			if err != nil || bad.Valid() || bad.Forms()[0].Form().Errors().ByField("ORDER").Empty() {
				t.Fatal("read-only row bypassed control validation", err)
			}
		}
	}
}
