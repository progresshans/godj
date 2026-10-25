package model

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestAccountMembershipDeletionChecksExplicitThroughRoot(t *testing.T) {
	identity := func(name string) ir.ModelIdentity {
		return ir.ModelIdentity{AppLabel: "godj_identity", ModelName: name}
	}
	role := ir.AccountRole{Model: identity("user"), Groups: "groups", Permissions: "permissions", Catalog: ir.AccountCatalog{Group: identity("group"), Permission: identity("permission"), GroupPermissions: "permissions"}}
	key := ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}
	reference := func(name, goName string, target ir.ModelIdentity, policy ir.DeletePolicy) ir.Field {
		return ir.Field{Name: name, GoName: goName, Column: name + "_id", Kind: ir.FieldForeignKey, Nullable: true, Relation: &ir.ForeignKeyRelation{Target: target, Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}, OnDelete: policy}}
	}
	for _, critical := range []string{"user", "group", "permission"} {
		for _, policy := range []ir.DeletePolicy{ir.DeleteCascade, ir.DeleteSetNull, ir.DeleteProtect} {
			t.Run(critical+"_"+string(policy), func(t *testing.T) {
				source := models.GoDjRelationSchema()
				member := reference("member", "MemberID", role.Model, ir.DeleteProtect)
				member.Nullable = false
				group := reference("team", "TeamID", role.Catalog.Group, ir.DeleteProtect)
				group.Nullable = false
				source.Models = append(source.Models, ir.Model{Name: "membership", GoName: "Membership", DBTable: "godj_identity_membership", Fields: []ir.Field{key, member, group}})
				for i := range source.Models {
					model := &source.Models[i]
					if model.Name == "user" {
						for j := range model.ManyToMany {
							if model.ManyToMany[j].Name == role.Groups {
								model.ManyToMany[j].Through = &ir.ThroughModel{Model: identity("membership"), SourceField: "member", TargetField: "team"}
							}
						}
					}
					if model.Name == critical {
						model.Fields = append(model.Fields, reference("membership", "MembershipID", identity("membership"), policy))
					}
				}
				project, err := orm.BindProject(source)
				if err != nil {
					t.Fatal(err)
				}
				if err := validateCatalogCollectionTopology(project, role); err != nil {
					t.Fatal("PROTECT endpoint should not reach explicit through owner", err)
				}
				err = validateRoleDeleteTopology(project, role, []string{"groups"})
				if policy == ir.DeleteProtect {
					if err != nil {
						t.Fatal("protected host effect should remain supported", err)
					}
				} else if !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
					t.Fatal("critical through lifecycle silently accepted", err)
				}
				if err := validateRoleDeleteTopology(project, role, []string{"staff"}); err != nil {
					t.Fatal("unselected collection restricted scalar roles", err)
				}
			})
		}
	}
	// The same full host graph supports ordinary CASCADE/SET_NULL references;
	// they do not imply another account/catalog lifecycle owner.
	for _, policy := range []ir.DeletePolicy{ir.DeleteCascade, ir.DeleteSetNull} {
		source := models.GoDjRelationSchema()
		project, err := orm.BindProject(source)
		if err != nil {
			t.Fatal(err)
		}
		var through ir.ModelIdentity
		for _, relation := range project.ManyToManyRelations() {
			if relation.Source == role.Model && relation.Field == role.Groups {
				through = relation.Through.Model
			}
		}
		source.Models = append(source.Models, ir.Model{Name: "note", GoName: "Note", DBTable: "godj_identity_note", Fields: []ir.Field{key, reference("membership", "MembershipID", through, policy)}})
		project, err = orm.BindProject(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRoleDeleteTopology(project, role, []string{"groups"}); err != nil {
			t.Fatal("ordinary host deletion effect rejected", err)
		}
	}
}
