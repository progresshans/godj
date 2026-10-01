package forms_test

import (
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/internal/slugtest"
)

func TestSlugChangedAgainstPinnedDjango(t *testing.T) {
	reference, _ := slugtest.Load(t, "sqlite")
	field, err := forms.SlugField("address", forms.WithAllowUnicode(true))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range reference.Changed {
		// Native None means no initial value. Go's nullable Value is an
		// explicit model NULL, so leave the ordinary Form initial absent.
		initial := map[string]forms.Value{}
		if observation.Initial != nil {
			initial["address"] = forms.String(*observation.Initial)
		}
		bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"address": {observation.Submitted}}), initial)
		if err != nil || (len(bound.Changed()) != 0) != observation.Changed {
			t.Fatalf("changed Slug differs from native: %v %v", bound.Changed(), err)
		}
	}
}

func TestSlugUnicodeOptionDoesNotLeakToOtherInputKinds(t *testing.T) {
	for _, allow := range []bool{true, false} {
		if _, err := forms.CharField("address", forms.WithAllowUnicode(allow)); err == nil {
			t.Fatal("slug option accepted on Char")
		}
		field, err := forms.SlugField("address", forms.WithAllowUnicode(allow))
		if err != nil || field.AllowUnicode() != allow {
			t.Fatal("slug option lost", err)
		}
	}
	if _, err := forms.SlugField("address", forms.WithAssumeScheme("https")); err == nil {
		t.Fatal("URL normalization admitted to slug")
	}
}
