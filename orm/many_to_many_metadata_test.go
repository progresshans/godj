package orm_test

import (
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	identitymodels "github.com/progresshans/godj/identity/models"
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

func TestManyToManyCapabilityRequiresCompleteHostSource(t *testing.T) {
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	owner, field, err := collections.ModelsTicketLabels.Declaration()
	if err != nil || field.Through == nil {
		t.Fatal("missing fixture declaration", err)
	}
	identity := ir.ModelIdentity{AppLabel: models.GoDjRelationSchema().AppLabel, ModelName: owner.Name}
	if err := collections.ModelsTicketLabels.ValidateSource(binding, identity, field.Name); err != nil {
		t.Fatal("independently bound identical source rejected", err)
	}
	// The original owner declaration is unchanged. The host now protects a
	// physical through row, so its policy must govern collection replacement.
	host := ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "host", Models: []ir.Model{{Name: "link_guard", GoName: "LinkGuard", Fields: []ir.Field{{
		Name: "link", GoName: "LinkID", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{Target: field.Through.Model, Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}, OnDelete: ir.DeleteProtect},
	}}}}}
	withHost, err := orm.BindProject(models.GoDjRelationSchema(), identitymodels.GoDjRelationSchema(), host)
	if err != nil {
		t.Fatal(err)
	}
	if current, found := withHost.Model(identity); !found || !current.Equal(owner) {
		t.Fatal("negative control changed owner model")
	}
	for _, test := range []struct {
		source orm.ProjectBinding
		owner  ir.ModelIdentity
		field  string
	}{
		{orm.ProjectBinding{}, identity, field.Name},
		{withHost, identity, field.Name},
		{binding, field.Target, field.Name},
		{binding, identity, "missing"},
	} {
		if err := collections.ModelsTicketLabels.ValidateSource(test.source, test.owner, test.field); err == nil {
			t.Fatal("foreign/unbound collection source admitted")
		}
	}
	if err := collections.ModelsLabelTickets.ValidateSource(binding, field.Target, "tickets"); err == nil {
		t.Fatal("reverse accessor became a forward field capability")
	}
	if err := (orm.ManyToMany[models.Ticket, models.Label, models.TicketLabel]{}).ValidateSource(binding, identity, field.Name); err == nil {
		t.Fatal("zero collection became a capability")
	}
}

func TestManyToManyDeclarationOwnsForwardPolicyAndRejectsReverse(t *testing.T) {
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	owner, field, err := collections.ModelsTicketLabels.Declaration()
	if err != nil {
		t.Fatal(err)
	}
	if !owner.Equal((models.TicketDescriptor{}).Metadata()) || field.Name != "labels" || field.Through == nil {
		t.Fatal("wrong declared field metadata")
	}
	owner.Fields[1].Name = "changed"
	field.Through.SourceField = "changed"
	owner.ManyToMany[0].Target.ModelName = "changed"
	again, againField, err := collections.ModelsTicketLabels.Declaration()
	if err != nil || againField.Through.SourceField == "changed" || !again.Equal((models.TicketDescriptor{}).Metadata()) {
		t.Fatal("declaration borrowed metadata", err)
	}
	if _, _, err := collections.ModelsLabelTickets.Declaration(); err == nil {
		t.Fatal("reverse accessor became a declared field")
	}
	var zero orm.ManyToMany[models.Ticket, models.Label, models.TicketLabel]
	if _, _, err := zero.Declaration(); err == nil {
		t.Fatal("zero factory published declaration")
	}
}
