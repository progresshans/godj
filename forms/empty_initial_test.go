package forms_test

import (
	"fmt"
	"testing"

	"github.com/progresshans/godj/forms"
)

// External rule: Django Field.has_changed normalizes None/empty only for the
// comparison, independently of field cleaning and required validation.
// https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/forms/fields.py
// BSD-3-Clause; attribution and license are in conformance/runners/django/.
func TestEmptyTextInitialHasNoChangeWithoutRewritingCleanedValue(t *testing.T) {
	for _, kind := range []string{"char", "email", "url", "slug"} {
		for _, nullEmpty := range []bool{false, true} {
			for _, required := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s_null_%t_required_%t", kind, nullEmpty, required), func(t *testing.T) {
					empty, initial := forms.String(""), forms.Null()
					if nullEmpty {
						empty, initial = forms.Null(), forms.String("")
					}
					options := []forms.FieldOption{forms.WithNullable(), forms.WithRequired(required), forms.WithEmptyValue(empty)}
					var field forms.Field
					var err error
					switch kind {
					case "char":
						field, err = forms.CharField("value", options...)
					case "email":
						field, err = forms.EmailField("value", options...)
					case "url":
						field, err = forms.URLField("value", options...)
					case "slug":
						field, err = forms.SlugField("value", options...)
					}
					if err != nil {
						t.Fatal(err)
					}
					spec, err := forms.NewSpec([]forms.Field{field})
					if err != nil {
						t.Fatal(err)
					}
					for _, raw := range []map[string][]string{nil, {"value": {""}}, {"value": {"  "}}} {
						bound, err := spec.Bind(t.Context(), forms.NewData(raw), map[string]forms.Value{"value": initial})
						if err != nil || bound.Valid() == required || len(bound.Changed()) != 0 {
							t.Fatal("empty comparison changed or skipped required validation", err, bound.Changed())
						}
						retained, _ := bound.Initial().Get("value")
						if !retained.Equal(initial) {
							t.Fatal("comparison rewrote initial")
						}
						value, present := bound.Cleaned().Get("value")
						if !required && (!present || !value.Equal(empty)) || required && present {
							t.Fatal("comparison rewrote cleaned/error result")
						}
					}
					bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"value": {"changed"}}), map[string]forms.Value{"value": initial})
					if err != nil || len(bound.Changed()) != 1 {
						t.Fatal("nonempty edit disappeared", err)
					}
				})
			}
		}
	}
}
