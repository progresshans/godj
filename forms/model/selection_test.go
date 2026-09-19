package model_test

import (
	"testing"

	"github.com/progresshans/godj/examples/helpdesk/modeldef"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
)

func TestSelectedScalarsDoNotExposeRelationOrNewFields(t *testing.T) {
	schema, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	model := schema.Models[1]
	if _, err := formmodel.NewSpec(model); err == nil {
		t.Fatal("unselected relation silently accepted")
	}
	spec, err := formmodel.NewSpecForFields(model, []string{"details", "subject"})
	if err != nil {
		t.Fatal(err)
	}
	fields := spec.Fields()
	if len(fields) != 2 || fields[0].Name() != "subject" || fields[1].Name() != "details" {
		t.Fatalf("fields: %v", fields)
	}
	valid, err := spec.Bind(forms.NewData(map[string][]string{"subject": {"Printer"}, "details": {""}}), nil)
	if err != nil || !valid.Valid() {
		t.Fatalf("selected scalar input: %v %v", valid.Errors(), err)
	}
	if _, ok := valid.Cleaned().Get("category"); ok {
		t.Fatal("omitted relationship entered cleaned input")
	}
	for _, names := range [][]string{{}, {"unknown"}, {"subject", "subject"}, {"category"}, {"id"}, {"id", "subject"}} {
		if _, err := formmodel.NewSpecForFields(model, names); err == nil {
			t.Fatalf("accepted invalid selection %v", names)
		}
	}
}
