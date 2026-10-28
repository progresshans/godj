package model

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestTransitionCreationRejectsAccountAsThrough(t *testing.T) {
	identity := func(name string) ir.ModelIdentity {
		return ir.ModelIdentity{AppLabel: "godj_identity", ModelName: name}
	}
	for _, name := range []string{"ordinary", "catalog", "host"} {
		t.Run(name, func(t *testing.T) {
			source := models.GoDjRelationSchema()
			if name != "ordinary" {
				owner := "group"
				if name == "host" {
					owner = "department"
					source.Models = append(source.Models, ir.Model{Name: owner, GoName: "Department", DBTable: "transition_department", Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}}, ManyToMany: []ir.ManyToManyField{{Name: "permissions", GoName: "Permissions", Target: identity("permission"), Reverse: ir.ReverseRelation{Disabled: true}}}})
				}
				for i := range source.Models {
					model := &source.Models[i]
					if model.Name == "user" {
						for _, field := range []struct{ name, goName, target string }{{"transition_owner", "TransitionOwnerID", owner}, {"transition_permission", "TransitionPermissionID", "permission"}} {
							model.Fields = append(model.Fields, ir.Field{Name: field.name, GoName: field.goName, Column: field.name + "_id", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{Target: identity(field.target), Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}, OnDelete: ir.DeleteProtect}})
						}
					}
					if model.Name == owner {
						for j := range model.ManyToMany {
							if model.ManyToMany[j].Name == "permissions" {
								model.ManyToMany[j].Through = &ir.ThroughModel{Model: identity("user"), SourceField: "transition_owner", TargetField: "transition_permission"}
							}
						}
					}
				}
			}
			project, err := orm.BindProject(source)
			if err != nil {
				t.Fatal(err)
			}
			err = validateTransitionTargetStorage(project, identity("user"))
			if name == "ordinary" {
				if err != nil {
					t.Fatal("ordinary account collections were rejected", err)
				}
			} else if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
				t.Fatal("account INSERT could change another collection without its lifecycle owner", err)
			}
		})
	}
}
