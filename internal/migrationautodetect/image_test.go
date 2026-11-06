package migrationautodetect

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectImageDimensionsOrdersDependenciesAndKeepsFieldPositions(t *testing.T) {
	for _, alter := range []bool{false, true} {
		name := "add"
		if alter {
			name = "alter"
		}
		t.Run(name, func(t *testing.T) {
			field := testChar("photo", true, nil)
			field.Kind = ir.FieldFile
			model := testModel("photo", testChar("title", false, nil))
			if alter {
				model.Fields = append([]ir.Field{field}, model.Fields...)
			}
			base := mustProjectState(t, testSchema("photos", model))
			history := mustLoadDefinitions(t, initialMigrationsFromState(t, base)...)
			image := field
			image.Kind = ir.FieldImage
			image.WidthField = "width"
			image.HeightField = "height"
			width := ir.Field{Name: "width", GoName: "Width", Kind: ir.FieldInteger, Nullable: true}
			height := ir.Field{Name: "height", GoName: "Height", Kind: ir.FieldInteger, Nullable: true}
			// Both dimensions are declared after the image but before the existing title.
			desired := mustProjectState(t, testSchema("photos", testModel("photo", image, width, height, testChar("title", false, nil))))
			request := Request{Definitions: history, Desired: desired, ManagedApps: []string{"photos"}}
			plan, err := Detect(request)
			if err != nil {
				t.Fatal(err)
			}
			changes := plan.Migrations()
			if len(changes) != 1 || len(changes[0].Operations) != 3 {
				t.Fatal("dimension plan", changes)
			}
			for i, n := range []string{"width", "height"} {
				op, ok := changes[0].Operations[i].(migrations.AddField)
				if !ok || op.Field.Name != n {
					t.Fatal("image ordered before its dimensions", changes)
				}
			}
			if alter {
				op, ok := changes[0].Operations[2].(migrations.AlterField)
				if !ok || op.After.Kind != ir.FieldImage {
					t.Fatal("image alter missing")
				}
			} else {
				op, ok := changes[0].Operations[2].(migrations.AddField)
				if !ok || op.Field.Kind != ir.FieldImage || op.BeforeField != "width" {
					t.Fatal("image insertion anchor lost")
				}
			}
			assertGeneratedState(t, history, changes, desired)
			repeated, err := Detect(request)
			if err != nil || !reflect.DeepEqual(repeated.Migrations(), changes) {
				t.Fatal("image plan nondeterministic", err)
			}
		})
	}
}

func TestDetectImageDimensionOwnershipReleasePrecedesClaim(t *testing.T) {
	first, second := testChar("first", true, nil), testChar("second", true, nil)
	first.Kind, second.Kind = ir.FieldImage, ir.FieldImage
	second.WidthField = "width"
	width := ir.Field{Name: "width", GoName: "Width", Kind: ir.FieldInteger, Nullable: true}
	base := mustProjectState(t, testSchema("photos", testModel("photo", first, second, width)))
	history := mustLoadDefinitions(t, initialMigrationsFromState(t, base)...)
	first.WidthField, second.WidthField = "width", ""
	desired := mustProjectState(t, testSchema("photos", testModel("photo", first, second, width)))
	plan, err := Detect(Request{Definitions: history, Desired: desired, ManagedApps: []string{"photos"}})
	if err != nil {
		t.Fatal(err)
	}
	changes := plan.Migrations()
	if len(changes) != 1 || len(changes[0].Operations) != 2 {
		t.Fatal("ownership transfer plan")
	}
	release, ok := changes[0].Operations[0].(migrations.AlterField)
	if !ok || release.After.Name != "second" || release.After.WidthField != "" {
		t.Fatal("new owner precedes release")
	}
	assertGeneratedState(t, history, changes, desired)
}
