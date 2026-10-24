package consumer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"example.com/godj-project-bundle/access"
	"example.com/godj-project-bundle/people"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/tickets"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func TestCustomAccountBindingOwnsTypedRowsAndHostGraph(t *testing.T) {
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	role, err := binding.Role()
	if err != nil || role.Model != (ir.ModelIdentity{AppLabel: "people", ModelName: "member"}) || role.Login != "contact" || role.Email != "contact" || role.Staff.Constant == nil || *role.Staff.Constant || role.CreatedAt != "" || role.WriteEmailNormalization != ir.AccountExact {
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
	var teamGrant access.TeamGrantsLink
	for _, grant := range grants[1:] {
		teamGrant, err = access.TeamGrantsLinkObjects.Create(t.Context(), backend, access.NewTeamGrantsLinkCreate(team.ID, grant.ID))
		if err != nil {
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
	actor, err := authenticator.Authenticate(t.Context(), "member@example.test", accountPasswordText)
	if err != nil {
		t.Fatal(err)
	}
	editor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, accountmodel.ProfileConfig[people.Member]{Fields: []string{"region"}, ChangePermission: "account.team"})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := project.NewAccountProfileReader(backend, auth.PrincipalAuthorizer{}, accountmodel.ProfileReadConfig{Fields: []string{"region"}, ViewPermission: "account.view", ChangePermission: "account.team"})
	if err != nil {
		t.Fatal(err)
	}
	sessionStore, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	assertProfileEditor(t, backend, binding, editor, reader, authenticator, sessionStore, actor.Principal(), row, "region", "JP", func(revoked bool) error {
		if revoked {
			count, err := access.TeamGrantsLinkObjects.Delete(t.Context(), backend, &teamGrant)
			if err == nil && count != 1 {
				return errors.New("group grant revocation did not delete one link")
			}
			return err
		}
		teamGrant, err = access.TeamGrantsLinkObjects.Create(t.Context(), backend, access.NewTeamGrantsLinkCreate(team.ID, grants[2].ID))
		return err
	})
	contactEditor, err := project.NewAccountProfileEditor(backend, auth.PrincipalAuthorizer{}, accountmodel.ProfileConfig[people.Member]{Fields: []string{"contact"}, ChangePermission: "account.team"})
	if err != nil {
		t.Fatal(err)
	}
	contact, err := objects.Input(map[string]query.Value{"contact": query.String("ｍｅｍｂｅｒ@EXAMPLE.TEST")})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := contactEditor.Update(t.Context(), actor.Principal(), row.ID, 5, contact); err != nil || changed.Revision != 6 {
		t.Fatal("custom shared login/email policy", changed, err)
	}
	edited, present, err := people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(row.ID)).OrderBy(people.MemberFields.ID.Asc()).First(t.Context())
	if err != nil || !present || edited.Region != "JP JSON" || edited.Version != 6 || edited.Subject != row.Subject || edited.Secret != row.Secret || edited.Contact != "member@EXAMPLE.TEST" || !edited.Enabled || edited.Root {
		t.Fatal("authorized typed profile mutation did not commit", err)
	}
	catalogConfig := catalogConsumerConfig[access.Team, access.Grant](role, "priority", "description")
	catalogConfig.Group.Validate = func(_ context.Context, _ db.Queryer, current *access.Team, candidate access.Team) (validation.Errors, error) {
		if candidate.Priority < 1 {
			return validation.NewErrors(validation.New("priority", "positive")), nil
		}
		if current != nil && current.ID != candidate.ID {
			return validation.Errors{}, errors.New("typed custom catalog changed identity")
		}
		return validation.Errors{}, nil
	}
	catalogConfig.Permission.Validate = func(_ context.Context, _ db.Queryer, _ *access.Grant, candidate access.Grant) (validation.Errors, error) {
		if candidate.Description == "" {
			return validation.NewErrors(validation.New("description", "required")), nil
		}
		return validation.Errors{}, nil
	}
	catalog, err := project.NewAccountCatalog(backend, auth.PrincipalAuthorizer{}, catalogConfig)
	if err != nil {
		t.Fatal(err)
	}
	assertCatalogConsumer(t, catalog, actor.Principal(), role, "priority", "description")
	roleEditor, err := project.NewAccountRoleEditor(sessionStore, auth.PrincipalAuthorizer{}, accountmodel.RoleConfig[people.Member]{Fields: roleConsumerFields(role), ChangePermission: "account.team"})
	if err != nil {
		t.Fatal(err)
	}
	roleReader, err := project.NewAccountProfileReader(backend, auth.PrincipalAuthorizer{}, accountmodel.ProfileReadConfig{Fields: roleConsumerFields(role), ViewPermission: "account.team"})
	if err != nil {
		t.Fatal(err)
	}
	roleTarget, err := people.MemberObjects.Create(t.Context(), backend, people.NewMemberCreate("role-target", "role-target@example.test", encoded, "NZ"))
	if err != nil {
		t.Fatal(err)
	}
	assertAccountRoleHTTP(t, backend, binding, authenticator, sessionStore, roleEditor, roleReader, actor.Principal(), "member@EXAMPLE.TEST", roleTarget, team.ID, grants[0].ID)
	firstConfig := accountmodel.FirstAccountConfig[people.Member]{CreationPolicy: accountmodel.CreationPolicy[people.Member]{Fields: []string{role.Login, "region"}}, EvidenceSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if owner, err := project.NewFirstAccount(backend, hasher, firstConfig); owner != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
		t.Fatal("constant staff=false was silently accepted by createsuperuser capability", err)
	}
	creationConfig := creationConsumerConfig[people.Member](role, "region")
	creationConfig.Validate = func(_ context.Context, _ db.Queryer, candidate people.Member, _ accountmodel.RoleMemberships) (validation.Errors, error) {
		if candidate.Region != "NZ" || candidate.Secret != "" {
			return validation.NewErrors(validation.New("region", "custom_policy")), nil
		}
		return validation.Errors{}, nil
	}
	missingProfile := creationConfig
	missingProfile.Fields = []string{role.Login, role.Groups, role.Permissions}
	if creator, err := project.NewAccountCreator(backend, auth.PrincipalAuthorizer{}, hasher, missingProfile); creator != nil || err == nil {
		t.Fatal("required custom profile field was silently omitted")
	}
	creator, err := project.NewAccountCreator(backend, auth.PrincipalAuthorizer{}, hasher, creationConfig)
	if err != nil {
		t.Fatal(err)
	}
	assertAccountCreationHTTP(t, backend, binding, creator, authenticator, sessionStore, actor.Principal(), "member@EXAMPLE.TEST", "region", team.ID, grants[0].ID)
	credential := assertStoredAccount(t, backend, binding, authenticator, "member@EXAMPLE.TEST", "member-1")
	if credential.Principal().Staff() || credential.Principal().Superuser() {
		t.Fatal("custom account constants and renamed role fields were not respected")
	}
}
