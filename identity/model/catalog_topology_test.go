package model

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestCatalogDeleteTopologyCannotSilentlySkipOtherLifecycleOwners(t *testing.T) {
	account := ir.ModelIdentity{AppLabel: "app", ModelName: "account"}
	group := ir.ModelIdentity{AppLabel: "app", ModelName: "group"}
	permission := ir.ModelIdentity{AppLabel: "app", ModelName: "permission"}
	role := ir.AccountRole{Model: account, Catalog: ir.AccountCatalog{Group: group, Permission: permission}}
	for _, test := range []struct {
		name               string
		source, target     ir.ModelIdentity
		policy             ir.DeletePolicy
		indirect, rejected bool
	}{
		{"ordinary_cascade", ir.ModelIdentity{AppLabel: "app", ModelName: "host"}, group, ir.DeleteCascade, false, false},
		{"ordinary_set_null", ir.ModelIdentity{AppLabel: "app", ModelName: "host"}, permission, ir.DeleteSetNull, false, false},
		{"protected_account", account, group, ir.DeleteProtect, false, false},
		{"account_cascade", account, group, ir.DeleteCascade, false, true},
		{"account_set_null", account, permission, ir.DeleteSetNull, false, true},
		{"another_catalog", group, permission, ir.DeleteCascade, false, true},
		{"self_catalog", group, group, ir.DeleteCascade, false, true},
		{"indirect_account", account, ir.ModelIdentity{AppLabel: "app", ModelName: "host"}, ir.DeleteSetNull, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition := ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "app", Models: []ir.Model{{Name: "account", GoName: "Account"}, {Name: "group", GoName: "Group"}, {Name: "permission", GoName: "Permission"}, {Name: "host", GoName: "Host"}}}
			add := func(source, target ir.ModelIdentity, policy ir.DeletePolicy) {
				for index := range definition.Models {
					if definition.Models[index].Name == source.ModelName {
						definition.Models[index].Fields = append(definition.Models[index].Fields, ir.Field{Name: "reference", GoName: "ReferenceID", Kind: ir.FieldForeignKey, Nullable: policy == ir.DeleteSetNull,
							Relation: &ir.ForeignKeyRelation{Target: target, OnDelete: policy, Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}}})
					}
				}
			}
			add(test.source, test.target, test.policy)
			if test.indirect {
				add(test.target, permission, ir.DeleteCascade)
			}
			binding, err := orm.BindProject(definition)
			if err != nil {
				t.Fatal(err)
			}
			err = validateCatalogDeleteTopology(binding, role)
			if test.rejected != errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || !test.rejected && err != nil {
				t.Fatal("catalog lifecycle topology classification", err)
			}
		})
	}
}
