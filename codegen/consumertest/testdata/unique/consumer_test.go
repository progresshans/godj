package consumer_test

import (
	"reflect"
	"testing"

	"example.com/godj-unique/models"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedUniqueMetadataOwnsCompleteSchema(t *testing.T) {
	schema := models.GoDjRelationSchema()
	metadata := (models.EntryDescriptor{}).Metadata()
	if len(schema.Models) != 1 || !reflect.DeepEqual(schema.Models[0], metadata) {
		t.Fatal("descriptor and relation schema disagree")
	}
	hash, err := ir.Hash(schema)
	if err != nil || hash != models.GoDjRelationSchemaSHA256 {
		t.Fatal("generated schema lost declaration semantics", err)
	}
	seen := make(map[ir.FieldKind]bool)
	for index, field := range metadata.Fields {
		if field.PrimaryKey || field.Name == "note" {
			if field.Unique {
				t.Fatal("generator added an undeclared constraint")
			}
			continue
		}
		if !field.Unique || !field.Nullable {
			t.Fatalf("unique nullable %s lost metadata", field.Name)
		}
		seen[field.Kind] = true
		metadata.Fields[index].Unique = false
		schema.Models[0].Fields[index].Unique = false
		if !(models.EntryDescriptor{}).Metadata().Fields[index].Unique || !models.GoDjRelationSchema().Models[0].Fields[index].Unique {
			t.Fatal("metadata retains caller storage")
		}
		if field.Kind == ir.FieldForeignKey && (field.Relation == nil || field.Relation.Cardinality != ir.RelationManyToOne || field.Relation.Reverse.Name != "children") {
			t.Fatal("unique FK changed its collection relation API")
		}
	}
	for _, kind := range []ir.FieldKind{ir.FieldChar, ir.FieldText, ir.FieldInteger, ir.FieldBoolean, ir.FieldFloat, ir.FieldDecimal, ir.FieldUUID, ir.FieldJSON, ir.FieldDate, ir.FieldDateTime, ir.FieldTime, ir.FieldDuration, ir.FieldForeignKey} {
		if !seen[kind] {
			t.Fatalf("generated unique field kind %s absent", kind)
		}
	}
}
