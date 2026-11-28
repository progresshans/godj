package model

import (
	"testing"

	fixture "github.com/progresshans/godj/conformance/accountdeletefixture"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

func TestCollectionStorageAcceptsCrossMembershipPolicies(t *testing.T) {
	for _, mode := range []string{"cascade", "payload_null", "protect", "reverse_cascade", "self_cascade"} {
		t.Run(mode, func(t *testing.T) {
			spec, err := fixture.ProjectSpec(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var schemas []ir.Schema
			for _, app := range spec.Apps {
				schema := app.Schema.Clone()
				if schema.AppLabel == "links" {
					for index := range schema.Models {
						model := &schema.Models[index]
						for field := range model.Fields {
							if model.Name == "membership" && model.Fields[field].Name == "depends_on" {
								if mode == "payload_null" {
									model.Fields[field].Relation.OnDelete = ir.DeleteSetNull
								}
								if mode == "protect" || mode == "reverse_cascade" || mode == "self_cascade" {
									model.Fields[field].Relation.OnDelete = ir.DeleteProtect
								}
							}
						}
						if mode == "reverse_cascade" && model.Name == "grant_link" || mode == "self_cascade" && model.Name == "membership" {
							model.Fields = append(model.Fields, ir.Field{Name: "previous_link", GoName: "PreviousLinkID", Column: "previous_link_id", Kind: ir.FieldForeignKey, Nullable: true,
								Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "links", ModelName: "membership"}, OnDelete: ir.DeleteCascade, Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}}})
						}
					}
				}
				schemas = append(schemas, schema)
			}
			binding, err := orm.BindProject(schemas...)
			if err != nil {
				t.Fatal(err)
			}
			catalogErr := validateCatalogCollectionStorage(binding, *spec.Account)
			roleErr := validateAccountCollectionStorage(binding, *spec.Account, []string{spec.Account.Groups})
			if catalogErr != nil || roleErr != nil {
				t.Fatal("cross-membership policy was rejected at construction", catalogErr, roleErr)
			}
			if err := validateAccountCollectionStorage(binding, *spec.Account, []string{spec.Account.Staff.Field}); err != nil {
				t.Fatal("unselected collection restricted scalar editing", err)
			}
		})
	}
}
