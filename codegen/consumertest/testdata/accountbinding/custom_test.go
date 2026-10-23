package consumer

import (
	"testing"

	"example.com/godj-project-bundle/access"
	"example.com/godj-project-bundle/people"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/tickets"
	"github.com/progresshans/godj/schema/ir"
)

func TestCustomAccountBindingOwnsTypedRowsAndHostGraph(t *testing.T) {
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	role, err := binding.Role()
	if err != nil || role.Model != (ir.ModelIdentity{AppLabel: "people", ModelName: "member"}) || role.Login != "contact" || role.Email != "contact" || role.Staff.Constant == nil || *role.Staff.Constant {
		t.Fatal("custom account selection was replaced by defaults", err)
	}
	*role.Staff.Constant = true
	again, err := binding.Role()
	if err != nil || *again.Staff.Constant {
		t.Fatal("role accessor changed the binding", err)
	}
	graph, err := binding.Project()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := graph.Model(ir.ModelIdentity{AppLabel: "godj_identity", ModelName: "user"}); exists {
		t.Fatal("custom account introduced a hidden built-in user model")
	}
	if edge, exists := graph.Relation(ir.ModelIdentity{AppLabel: "tickets", ModelName: "ticket"}, "owner"); !exists || edge.Target != again.Model {
		t.Fatal("binding omitted the host's account relation")
	}
	backend := accountDatabase(t, access.GoDjRelationSchema(), people.GoDjRelationSchema(), tickets.GoDjRelationSchema())
	objects, err := binding.Objects()
	if err != nil {
		t.Fatal(err)
	}
	row, err := objects.Create(t.Context(), backend, people.NewMemberCreate("member-1", "member@example.test", "!", "KR"))
	if err != nil {
		t.Fatal(err)
	}
	found, exists, err := objects.Using(backend).Filter(people.MemberFields.Contact.Exact("member@example.test")).First(t.Context())
	if err != nil || !exists || found.ID != row.ID || found.Region != "KR" || found.Subject != "member-1" || !found.Enabled || found.Version != 1 {
		t.Fatal("selected typed account failed to preserve its model fields", err)
	}
}
