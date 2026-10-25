package model

import (
	"errors"
	"testing"

	fixture "github.com/progresshans/godj/conformance/accountdeletefixture"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestCollectionDeletionCannotSkipOtherMembershipOwners(t *testing.T) {
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
			catalogErr := validateCatalogCollectionTopology(binding, *spec.Account)
			roleErr := validateRoleDeleteTopology(binding, *spec.Account, []string{spec.Account.Groups})
			catalogRejected := mode == "cascade"
			roleRejected := mode == "reverse_cascade" || mode == "self_cascade"
			if errors.Is(catalogErr, &query.Error{Code: query.CodeUnsupported}) != catalogRejected || !catalogRejected && catalogErr != nil {
				t.Fatal("catalog indirect membership effect escaped its owner", catalogErr)
			}
			if errors.Is(roleErr, &query.Error{Code: query.CodeUnsupported}) != roleRejected || !roleRejected && roleErr != nil {
				t.Fatal("account indirect membership effect escaped its owner", roleErr)
			}
			if err := validateRoleDeleteTopology(binding, *spec.Account, []string{spec.Account.Staff.Field}); err != nil {
				t.Fatal("unselected collection restricted scalar editing", err)
			}
		})
	}
}
