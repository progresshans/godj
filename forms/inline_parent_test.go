package forms_test

import (
	"testing"

	"github.com/progresshans/godj/forms"
)

func TestInlineParentFieldKeepsServerIdentityAndNeverCountsAsChange(t *testing.T) {
	for _, parent := range []forms.Value{forms.Null(), forms.Integer(0), forms.Integer(17)} {
		field, err := forms.InlineParentField("parent", parent)
		if err != nil || field.Widget() != forms.HiddenInput || field.Required() {
			t.Fatal("invalid parent field policy", err)
		}
		spec, err := forms.NewSpec([]forms.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range [][]string{nil, {""}, {"0"}, {"17"}, {"017"}, {"None"}, {"17", "17"}} {
			input := map[string][]string{}
			if raw != nil {
				input["parent"] = raw
			}
			bound, err := spec.Bind(forms.NewData(input), nil)
			if err != nil {
				t.Fatal(err)
			}
			key, saved := parent.AsInteger()
			want := len(raw) == 0 || len(raw) == 1 && (raw[0] == "" || saved && (key == 0 && raw[0] == "0" || key == 17 && raw[0] == "17"))
			if bound.Valid() != want || len(bound.Changed()) != 0 {
				t.Fatal("client changed parent identity or blank-extra status")
			}
			if want {
				value, present := bound.Cleaned().Get("parent")
				if !present || !value.Equal(parent) {
					t.Fatal("server parent was not supplied on omission/empty input")
				}
			}
		}
		if _, err := spec.Unbound(map[string]forms.Value{"parent": forms.Integer(99)}); err == nil {
			t.Fatal("display snapshot replaced fixed parent")
		}
	}
	if _, err := forms.InlineParentField("parent", forms.String("17")); err == nil {
		t.Fatal("untyped parent identity accepted")
	}
}
