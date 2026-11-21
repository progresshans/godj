package forms_test

import (
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/internal/urltest"
)

func TestURLChangedAgainstPinnedDjango(t *testing.T) {
	reference, _ := urltest.Load(t, "sqlite")
	field, err := forms.URLField("address")
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
			t.Fatalf("changed URL differs from native: %v %v", bound.Changed(), err)
		}
	}
}

func TestURLSchemeConfigurationAndOwnership(t *testing.T) {
	if _, err := forms.CharField("address", forms.WithAssumeScheme("https")); err == nil {
		t.Fatal("URL-only option accepted by CharField")
	}
	for _, scheme := range []string{"javascript", "https://", " https", "file"} {
		if _, err := forms.URLField("address", forms.WithAssumeScheme(scheme)); err == nil {
			t.Fatal("invalid assumed scheme accepted")
		}
	}
	field, err := forms.URLField("address", forms.WithAssumeScheme("FTP"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]string{"example.com/Path": "FTP://example.com/Path", "//Example.com": "FTP://Example.com", "https://Example.com": "https://Example.com"} {
		bound, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"address": {raw}}), nil)
		value, _ := bound.Cleaned().Get("address")
		got, ok := value.AsString()
		if err != nil || !bound.Valid() || !ok || got != want {
			t.Fatal("assumed scheme overwrote input or lost configuration", err)
		}
	}
	if spec.Fields()[0].AssumeScheme() != "FTP" {
		t.Fatal("spec lost URL scheme")
	}
}
