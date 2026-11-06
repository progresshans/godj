package admin

import (
	"net/url"
	"testing"

	"github.com/progresshans/godj/forms"
)

func TestHiddenInlineParentRedisplayUsesServerValue(t *testing.T) {
	field, err := forms.InlineParentField("parent", forms.Integer(7))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	submitted := map[string][]string{"parent": {"99<script>"}}
	bound, err := spec.Bind(t.Context(), forms.NewData(submitted), nil)
	if err != nil || bound.Valid() {
		t.Fatal("wrong parent accepted", err)
	}
	if text, _ := renderedFieldValue(field, bound, submitted); text != "7" {
		t.Fatal("hidden parent replayed an invalid submitted value")
	}
}

func TestReadOnlyInlineRedisplayRetainsInitialAndPasswordPrivacy(t *testing.T) {
	name, err := forms.CharField("name")
	if err != nil {
		t.Fatal(err)
	}
	flag, err := forms.BooleanField("flag", forms.WithRequired(false), forms.WithNullable())
	if err != nil {
		t.Fatal(err)
	}
	password, err := forms.CharField("password", forms.WithWidget(forms.PasswordInput))
	if err != nil {
		t.Fatal(err)
	}
	row, err := forms.NewSpec([]forms.Field{name, flag, password})
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix, config.ReadOnlyInitial = "items", true
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	set, err := spec.Bind(t.Context(), forms.NewData(map[string][]string{"items-TOTAL_FORMS": {"1"}, "items-INITIAL_FORMS": {"1"}, "items-0-name": {"forged"}, "items-0-flag": {"false"}, "items-0-password": {"submitted-private"}}), []map[string]forms.Value{{"name": forms.String("kept"), "flag": forms.Boolean(true), "password": forms.String("server-private")}})
	if err != nil || !set.Valid() {
		t.Fatal("read-only row failed", err)
	}
	form := set.Forms()[0].Form()
	submitted := url.Values{"name": {"forged"}, "flag": {"false"}, "password": {"submitted-private"}}
	for _, check := range []struct {
		field forms.Field
		want  string
	}{{name, "kept"}, {flag, "true"}, {password, ""}} {
		if text, _ := renderedFieldValue(check.field, form, submitted); text != check.want {
			t.Fatal("read-only display used submitted/secret data", check.field.Name())
		}
	}
}
