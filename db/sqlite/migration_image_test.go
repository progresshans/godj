package sqlite

import (
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
	"testing"
)

func TestSQLiteIntentSealOwnsImageDimensionReferences(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "photos", Models: []schema.Model{{Name: "photo", GoName: "Photo", Fields: []schema.Field{
		schema.ImageField("photo", "Photo", schema.ImageDimensions("width", "height")), schema.IntegerField("width", "Width"), schema.IntegerField("height", "Height"),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := s.Models[0]
	for _, mode := range []string{"width", "height", "swap", "blank"} {
		t.Run(mode, func(t *testing.T) {
			intent := migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{Kind: migrationbackend.MigrationCreateModel, After: model}}}
			seal, err := validateAndSealSQLiteRelationIntent(migrationbackend.HistoryTransition{Migration: migrationbackend.AppliedMigration{App: "photos", Name: "0001_initial"}, Kind: migrationbackend.HistoryTransitionApply}, intent)
			if err != nil {
				t.Fatal(err)
			}
			field := &seal.intent.Operations[0].After.Fields[1]
			switch mode {
			case "width":
				field.WidthField = ""
			case "height":
				field.HeightField = ""
			case "blank":
				field.Blank = !field.Blank
			case "swap":
				field.WidthField, field.HeightField = field.HeightField, field.WidthField
			}
			if err := verifySQLiteRelationIntentSeal(&seal); err == nil {
				t.Fatal("image reference mutation retained sealed authority")
			}
			if model.Fields[1].WidthField != "width" || model.Fields[1].HeightField != "height" {
				t.Fatal("seal borrowed caller image metadata")
			}
		})
	}
}
