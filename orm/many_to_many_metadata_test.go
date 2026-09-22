package orm_test

import (
	"testing"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

func TestManyToManyBindingOwnsColumnlessAndAutomaticStorageMetadata(t *testing.T) {
	input := ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "app", Models: []ir.Model{{Name: "owner", GoName: "Owner", ManyToMany: []ir.ManyToManyField{{Name: "labels", GoName: "Labels", Target: ir.ModelIdentity{AppLabel: "app", ModelName: "label"}}}}, {Name: "label", GoName: "Label"}}}
	binding, err := orm.BindProject(input)
	if err != nil {
		t.Fatal(err)
	}
	relations := binding.ManyToManyRelations()
	if len(relations) != 1 || !relations[0].Automatic {
		t.Fatal("collection missing")
	}
	link, ok := binding.Model(relations[0].Through.Model)
	if !ok || len(link.Fields) != 3 {
		t.Fatal("automatic storage unavailable")
	}
	if len(binding.ForwardRelations()) != 2 || len(binding.ReverseRelations()) != 0 {
		t.Fatal("hidden physical FK edges changed")
	}
	owner, ok := binding.Model(ir.ModelIdentity{AppLabel: "app", ModelName: "owner"})
	if !ok || len(owner.Fields) != 1 || len(owner.ManyToMany) != 1 {
		t.Fatal("owner column layout changed")
	}
	input.Models[0].ManyToMany[0].Target.ModelName = "changed"
	owner.ManyToMany[0].Name = "changed"
	relations[0].Through.SourceField = "changed"
	fresh, _ := binding.Model(ir.ModelIdentity{AppLabel: "app", ModelName: "owner"})
	if fresh.ManyToMany[0].Name != "labels" || binding.ManyToManyRelations()[0].Through.SourceField != "source" {
		t.Fatal("mutable binding leak")
	}
	if got, err := orm.BindProject(input); err == nil || len(got.ManyToManyRelations()) != 0 {
		t.Fatal("failed binding published partial result")
	}
}
