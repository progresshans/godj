package admin

import (
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
)

func TestJSONAdminSnapshotNullAndRawTextRevalidation(t *testing.T) {
	built, err := schema.Build(schema.Definition{AppLabel: "refs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.JSONField("payload", "Payload", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := built.Models[0]
	projector, err := NewModelProjector(metadata, func(value jsonvalue.Value, field ir.Field) (query.Value, bool) { return query.JSON(value), true }, "payload")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `false`, `1.00`, `{"": [340282366920938463463374607431768211455,null]}`, `{"text":"</textarea><script>alert(1)</script>"}`} {
		value, err := jsonvalue.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		object, err := projector.Project(value, 1, "JSON")
		if err != nil {
			t.Fatal(err)
		}
		snapshot, _ := object.Values().Member("payload")
		text, ok := snapshot.AsString()
		if !ok || text != value.Text || !initialMatchesSnapshot(forms.JSON(value), snapshot) {
			t.Fatal("JSON admin snapshot lost canonical value")
		}
		for _, wrong := range []templates.Value{templates.Null(), templates.Integer(0), templates.String(value.Text + " ")} {
			if initialMatchesSnapshot(forms.JSON(value), wrong) {
				t.Fatal("JSON snapshot matched different presence or representation")
			}
		}
		if !validSnapshotValue(snapshot, metadata.Fields[1], 1) || validSnapshotValue(templates.String(value.Text+" "), metadata.Fields[1], 1) {
			t.Fatal("JSON snapshot canonical validation changed")
		}
	}
	wrong, err := NewModelProjector(metadata, func(jsonvalue.Value, ir.Field) (query.Value, bool) { return query.String("null"), true }, "payload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Project(jsonvalue.Null(), 1, "JSON"); err == nil {
		t.Fatal("text bypassed JSON model snapshot")
	}
	field, err := forms.JSONField("payload", forms.WithNullable(), forms.WithRequired(false))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := jsonvalue.Parse([]byte(`{"a":1.0,"b":2}`))
	if err != nil {
		t.Fatal(err)
	}
	raw := ` {"b": 2, "a": 1e0} `
	bound, err := spec.Bind(forms.NewData(map[string][]string{"payload": {raw}}), map[string]forms.Value{"payload": forms.JSON(initial)})
	if err != nil || !bound.Valid() || len(bound.Changed()) != 0 {
		t.Fatal("JSON spelling caused false change", err)
	}
	validated, err := validateBoundForm(bound, spec, spec.Fields())
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := validated.JSON("payload"); !ok || value.Text != `{"a":1e0,"b":2}` {
		t.Fatal("JSON revalidation lost numeric token")
	}
	if text, _ := renderedFieldValue(field, bound, map[string][]string{"payload": {raw}}); text != raw {
		t.Fatal("bound JSON lost input text")
	}
	for _, value := range []forms.Value{forms.Null(), forms.JSON(jsonvalue.Null())} {
		form, err := spec.Unbound(map[string]forms.Value{"payload": value})
		if err != nil {
			t.Fatal(err)
		}
		if text, _ := renderedFieldValue(field, form, nil); text != "null" {
			t.Fatal("JSON initial null rendered as an empty document")
		}
	}
}
