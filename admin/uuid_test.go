package admin

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/uuid"
)

func TestUUIDAdminTypedSnapshotCanonicalRenderingAndRevalidation(t *testing.T) {
	built, err := schema.Build(schema.Definition{AppLabel: "refs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.UUIDField("reference", "Reference", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := built.Models[0]
	projector, err := NewModelProjector(metadata, func(value uuid.UUID, field ir.Field) (query.Value, bool) { return query.UUID(value), true }, "reference")
	if err != nil {
		t.Fatal(err)
	}
	sample, err := uuid.Parse("12345678-9abc-4def-8123-456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	for _, identifier := range []uuid.UUID{{}, sample, {0: 0xff, 15: 1}} {
		object, err := projector.Project(identifier, 1, "Reference")
		if err != nil {
			t.Fatal(err)
		}
		value, _ := object.Values().Member("reference")
		if text, ok := value.AsString(); !ok || text != identifier.String() {
			t.Fatal("UUID admin snapshot changed canonical value")
		}
		if !initialMatchesSnapshot(forms.UUID(identifier), value) {
			t.Fatal("UUID initial and snapshot disagree")
		}
		for _, wrong := range []templates.Value{templates.Integer(0), templates.Null(), templates.String(identifier.Hex()), templates.String(identifier.String() + " ")} {
			if initialMatchesSnapshot(forms.UUID(identifier), wrong) {
				t.Fatal("UUID snapshot matched a different representation or type")
			}
		}
	}
	for _, text := range []string{sample.Hex(), strings.ToUpper(sample.String()), "urn:uuid:" + sample.String(), "invalid"} {
		if validSnapshotValue(templates.String(text), metadata.Fields[1], 1) {
			t.Fatal("noncanonical UUID snapshot admitted")
		}
	}
	wrong, err := NewModelProjector(metadata, func(uuid.UUID, ir.Field) (query.Value, bool) { return query.String(sample.String()), true }, "reference")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Project(sample, 1, "Reference"); err == nil {
		t.Fatal("text bypassed typed UUID admin value")
	}
	field, err := forms.UUIDField("reference", forms.WithNullable(), forms.WithRequired(false))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	raw := "  {" + strings.ToUpper(sample.String()) + "}  "
	bound, err := spec.Bind(forms.NewData(map[string][]string{"reference": {raw}}), map[string]forms.Value{"reference": forms.UUID(sample)})
	if err != nil || !bound.Valid() || len(bound.Changed()) != 0 {
		t.Fatal("UUID alias produced a changed model value", err)
	}
	validated, err := validateBoundForm(bound, spec, spec.Fields())
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := validated.UUID("reference"); !ok || got != sample {
		t.Fatal("UUID canonical revalidation lost value")
	}
	if text, _ := renderedFieldValue(field, bound, map[string][]string{"reference": {raw}}); text != raw {
		t.Fatal("bound UUID lost submitted spelling")
	}
	unbound, err := spec.Unbound(map[string]forms.Value{"reference": forms.UUID(sample)})
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := renderedFieldValue(field, unbound, nil); text != sample.String() {
		t.Fatal("unbound UUID did not render canonical text")
	}
	null, err := spec.Bind(forms.NewData(map[string][]string{"reference": {""}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	cleaned, err := validateBoundForm(null, spec, spec.Fields())
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := cleaned.Get("reference"); !ok || !value.IsNull() {
		t.Fatal("UUID empty revalidation invented zero")
	}
}
