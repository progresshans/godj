package consumer

import (
	"errors"
	"reflect"
	"testing"

	"example.com/godj-project-bundle/access"
	"example.com/godj-project-bundle/people"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/tickets"
	"github.com/progresshans/godj/auth"
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
	hasher, encoded := accountPassword(t)
	objects, err := binding.Objects()
	if err != nil {
		t.Fatal(err)
	}
	row := createBoundAccount(t, backend, binding, people.NewMemberCreate("member-1", "member@example.test", encoded, "KR"))
	found, exists, err := objects.Using(backend).Filter(people.MemberFields.Contact.Exact("member@example.test")).OrderBy(people.MemberFields.ID.Asc()).First(t.Context())
	if err != nil || !exists || found.ID != row.ID || found.Region != "KR" || found.Subject != "member-1" || !found.Enabled || found.Version != 1 {
		t.Fatal("selected typed account failed to preserve its model fields", err)
	}
	// Direct, group-only and overlapping grants must form one bounded set.
	store, err := project.NewAccountCredentialStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	grants := make([]access.Grant, 3)
	for i, code := range []string{"account.direct", "account.shared", "account.team"} {
		grants[i], err = access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate(code, code))
		if err != nil {
			t.Fatal(err)
		}
	}
	team, err := access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate("operators"))
	if err != nil {
		t.Fatal(err)
	}
	direct, err := people.MemberGrantsLinkObjects.Create(t.Context(), backend, people.NewMemberGrantsLinkCreate(row.ID, grants[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := people.MemberGrantsLinkObjects.Create(t.Context(), backend, people.NewMemberGrantsLinkCreate(row.ID, grants[1].ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := people.MemberTeamsLinkObjects.Create(t.Context(), backend, people.NewMemberTeamsLinkCreate(row.ID, team.ID)); err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants[1:] {
		if _, err := access.TeamGrantsLinkObjects.Create(t.Context(), backend, access.NewTeamGrantsLinkCreate(team.ID, grant.ID)); err != nil {
			t.Fatal(err)
		}
	}
	before, foundAccount, err := store.CredentialByUsername(t.Context(), "member@example.test")
	expected := []auth.Permission{"account.direct", "account.shared", "account.team"}
	if err != nil || !foundAccount || !reflect.DeepEqual(before.Principal().Permissions(), expected) {
		t.Fatal("effective permission union", err)
	}
	if count, err := people.MemberGrantsLinkObjects.Delete(t.Context(), backend, &direct); err != nil || count != 1 {
		t.Fatal("direct permission removal", err)
	}
	after, foundAccount, err := store.CredentialByID(t.Context(), "member-1")
	if err != nil || !foundAccount || !reflect.DeepEqual(after.Principal().Permissions(), expected[1:]) || !reflect.DeepEqual(before.Principal().Permissions(), expected) {
		t.Fatal("fresh permissions or detached snapshot", err)
	}
	// Hidden reverse accessors remain hidden while the store uses the declared forward capabilities.
	for _, relation := range graph.ManyToManyRelations() {
		if !relation.Reverse.Disabled {
			t.Fatal("account catalog published a hidden reverse accessor")
		}
	}
	scope := &trackedSnapshots{backend: backend}
	authenticator, err := project.NewAccountAuthenticator(t.Context(), scope, outsideSnapshotHasher{hasher, &scope.reading})
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := authenticator.Authenticate(t.Context(), "ｍｅｍｂｅｒ@example.test", accountPasswordText)
	if again.AuthenticateNormalization == ir.AccountUnicode16NFKC {
		if err != nil || normalized.Principal().ID() != "member-1" {
			t.Fatal("selected custom normalization did not authenticate", err)
		}
	} else if !errors.Is(err, auth.ErrInvalidCredentials) || normalized.SessionStamp() != "" {
		t.Fatal("exact custom policy normalized input", err)
	}
	credential := assertStoredAccount(t, backend, binding, authenticator, "member@example.test", "member-1")
	if credential.Principal().Staff() || credential.Principal().Superuser() {
		t.Fatal("custom account constants and renamed role fields were not respected")
	}
}
