package admin

import (
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
	bound, err := spec.Bind(forms.NewData(submitted), nil)
	if err != nil || bound.Valid() {
		t.Fatal("wrong parent accepted", err)
	}
	if text, _ := renderedFieldValue(field, bound, submitted); text != "7" {
		t.Fatal("hidden parent replayed an invalid submitted value")
	}
}
