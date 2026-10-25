package accountstoretest

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/conformance/accountdeletefixture/access"
	"github.com/progresshans/godj/conformance/accountdeletefixture/links"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

type deletionTree struct {
	root, child, survivor, copies  people.Member
	group, groupSurvivor           access.Team
	permission, permissionSurvivor access.Grant
	removed, preserved             []sessions.Record
	historical                     sessions.Store
	historicalRecord               sessions.Record
}

func deletionTreeFixture(t *testing.T, backend Backend, f *deletionFixture, prefix string) deletionTree {
	t.Helper()
	var tree deletionTree
	tree.root = deletionMember(t, backend, f, prefix+"root", nil)
	var err error
	tree.group, err = access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate(prefix+"deleted group").WithCreatorID(tree.root.ID))
	if err != nil {
		t.Fatal(err)
	}
	tree.groupSurvivor, err = access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate(prefix+"surviving group").WithReviewerID(tree.root.ID))
	if err != nil {
		t.Fatal(err)
	}
	tree.permission, err = access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate(prefix+".deleted", "Deleted grant").WithIssuerID(tree.root.ID).WithTeamID(tree.group.ID).WithReviewerID(tree.root.ID))
	if err != nil {
		t.Fatal(err)
	}
	tree.permissionSurvivor, err = access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate(prefix+".surviving", "Surviving grant").WithReviewerID(tree.root.ID))
	if err != nil {
		t.Fatal(err)
	}
	tree.child = deletionMember(t, backend, f, prefix+"child", func(value people.MemberCreate) people.MemberCreate {
		return value.WithLeaderID(tree.root.ID).WithSponsorID(tree.group.ID).WithReviewerID(tree.root.ID)
	})
	tree.survivor = deletionMember(t, backend, f, prefix+"survivor", func(value people.MemberCreate) people.MemberCreate {
		return value.WithReviewerID(tree.root.ID).WithGrantRefID(tree.permission.ID)
	})
	tree.copies = deletionMember(t, backend, f, prefix+"copies", nil)
	for _, input := range []links.MembershipCreate{
		links.NewMembershipCreate(tree.root.ID, tree.group.ID), links.NewMembershipCreate(tree.child.ID, tree.group.ID),
		links.NewMembershipCreate(tree.survivor.ID, tree.group.ID), links.NewMembershipCreate(tree.survivor.ID, tree.group.ID).WithAnchorID(tree.root.ID),
		links.NewMembershipCreate(tree.survivor.ID, tree.groupSurvivor.ID),
		links.NewMembershipCreate(tree.copies.ID, tree.groupSurvivor.ID).WithAnchorID(tree.root.ID), links.NewMembershipCreate(tree.copies.ID, tree.groupSurvivor.ID),
	} {
		if _, err := links.MembershipObjects.Create(t.Context(), backend, input); err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range []links.GrantLinkCreate{
		links.NewGrantLinkCreate(tree.group.ID, tree.permission.ID), links.NewGrantLinkCreate(tree.groupSurvivor.ID, tree.permission.ID),
		links.NewGrantLinkCreate(tree.groupSurvivor.ID, tree.permissionSurvivor.ID).WithAnchorID(tree.root.ID), links.NewGrantLinkCreate(tree.groupSurvivor.ID, tree.permissionSurvivor.ID),
	} {
		if _, err := links.GrantLinkObjects.Create(t.Context(), backend, input); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{tree.root.ID, tree.survivor.ID} {
		if _, err := people.MemberGrantsLinkObjects.Create(t.Context(), backend, people.NewMemberGrantsLinkCreate(id, tree.permission.ID)); err != nil {
			t.Fatal(err)
		}
	}
	tree.removed = []sessions.Record{deletionSession(t, f.manager, tree.root.Subject), deletionSession(t, f.manager, tree.root.Subject), deletionSession(t, f.manager, tree.child.Subject)}
	tree.preserved = []sessions.Record{deletionSession(t, f.manager, tree.survivor.Subject), deletionSession(t, f.manager, f.actorRow.Subject), deletionSession(t, f.manager, "")}
	database, err := sessiondb.New(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tree.historical, err = database.Store("historical-account-domain")
	if err != nil {
		t.Fatal(err)
	}
	history, err := sessions.NewManager(tree.historical, sessions.Config{Clock: func() time.Time { return joined }})
	if err != nil {
		t.Fatal(err)
	}
	tree.historicalRecord = deletionSession(t, history, tree.root.Subject)
	return tree
}

func assertDeletionTree(t *testing.T, backend Backend, f *deletionFixture, tree deletionTree, before map[string]any, result accountmodel.AccountDeletion) {
	t.Helper()
	after := deletionSnapshot(t, backend)
	if result.ID != tree.root.ID || result.Revision != 1 || result.DeletedRows != deletionModelCount(before)-deletionModelCount(after) || result.RevokedSessions != len(tree.removed) {
		t.Fatal("confirmed deletion metadata did not match actual rows and sessions", result)
	}
	for _, id := range []int64{tree.root.ID, tree.child.ID} {
		if n, err := people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(id)).Count(t.Context()); err != nil || n != 0 {
			t.Fatal("cascaded account remains", err)
		}
	}
	survivor, err := people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(tree.survivor.ID)).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if survivor.Version != 2 || survivor.ReviewerID != nil || survivor.GrantRefID != nil || survivor.Secret != tree.survivor.Secret || survivor.Subject != tree.survivor.Subject || survivor.Contact != tree.survivor.Contact {
		t.Fatal("merged account effects were incomplete or repeated")
	}
	group, err := access.TeamObjects.Using(backend).Filter(access.TeamFields.ID.Exact(tree.groupSurvivor.ID)).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	permission, err := access.GrantObjects.Using(backend).Filter(access.GrantFields.ID.Exact(tree.permissionSurvivor.ID)).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if group.Version != 2 || group.ReviewerID != nil || permission.Version != 2 || permission.ReviewerID != nil {
		t.Fatal("surviving catalog revision or NULL effect missing")
	}
	copies, err := people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(tree.copies.ID)).Get(t.Context())
	if err != nil || !reflect.DeepEqual(copies, tree.copies) {
		t.Fatal("removing one duplicate membership changed its owner", err)
	}
	for _, record := range tree.removed {
		accountSessionRecord(t, f.store, record, false)
	}
	for _, record := range tree.preserved {
		accountSessionRecord(t, f.store, record, true)
	}
	accountSessionRecord(t, tree.historical, tree.historicalRecord, true)
	if !reflect.DeepEqual(before["domain"], after["domain"]) {
		t.Fatal("deletion changed domain ownership history")
	}
	for _, target := range []struct {
		model  string
		id     int64
		action admin.Action
		fields []string
	}{
		{"people.member", tree.root.ID, admin.ActionDelete, nil}, {"people.member", tree.child.ID, admin.ActionDelete, nil},
		{"access.team", tree.group.ID, admin.ActionDelete, nil}, {"access.grant", tree.permission.ID, admin.ActionDelete, nil},
		{"people.member", tree.survivor.ID, admin.ActionChange, []string{"grant_ref", "grants", "reviewer", "teams"}},
		{"access.team", tree.groupSurvivor.ID, admin.ActionChange, []string{"grants", "reviewer"}},
		{"access.grant", tree.permissionSurvivor.ID, admin.ActionChange, []string{"reviewer"}},
	} {
		history := deletionHistory(t, backend, f.actor, target.model, target.id)
		if len(history) != 1 || history[0].Action() != target.action || history[0].ActorID() != f.actor.ID() || !history[0].Domain().MatchesPrincipal(f.actor) || !slices.Equal(history[0].ChangedFields(), target.fields) {
			t.Fatal("collected lifecycle audit is missing, duplicated or misattributed", target.model, target.id)
		}
	}
	if len(deletionHistory(t, backend, f.actor, "people.member", tree.copies.ID)) != 0 {
		t.Fatal("unchanged membership owner received an audit")
	}
}

func accountDeletionLifecycle(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	tree := deletionTreeFixture(t, backend, f, "commit")
	before := deletionSnapshot(t, reopen(t))
	result, err := deletionOwner(t, backend).Delete(t.Context(), f.actor, tree.root.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertDeletionTree(t, reopen(t), f, tree, before, result)
	if tree.root.Version != 1 || tree.root.ID == 0 || tree.root.Secret != f.secret {
		t.Fatal("deletion changed caller-held account")
	}
	if value, present, err := f.credentials.CredentialByID(t.Context(), tree.root.Subject); err != nil || present || value.Principal().Authenticated() {
		t.Fatal("deleted credential was retained", err)
	}
	// The final current actor may itself be among the deleted rows. Audit and
	// result publication must not attempt a fresh authorization after commit.
	record := deletionSession(t, f.manager, f.actorRow.Subject)
	self, err := deletionOwner(t, backend).Delete(t.Context(), f.actor, f.actorRow.ID, 1)
	if err != nil || self.ID != f.actorRow.ID || self.RevokedSessions != 2 {
		t.Fatal("self deletion lost its confirmed result", err)
	}
	accountSessionRecord(t, f.store, record, false)
	history := deletionHistory(t, reopen(t), f.actor, "people.member", f.actorRow.ID)
	if len(history) != 1 || history[0].ActorID() != f.actor.ID() {
		t.Fatal("self deletion lost historical actor identity")
	}
}

func accountCatalogDeletion(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	catalog := deletionCatalog(t, f.store)
	for _, model := range []string{"group", "permission"} {
		t.Run(model, func(t *testing.T) {
			tree := deletionTreeFixture(t, backend, f, model)
			before := deletionSnapshot(t, reopen(t))
			var result accountmodel.CatalogDeletion
			var err error
			if model == "group" {
				result, err = catalog.Groups().Delete(t.Context(), f.actor, tree.group.ID, 1)
			} else {
				result, err = catalog.Permissions().Delete(t.Context(), f.actor, tree.permission.ID, 1)
			}
			if err != nil || result.Revision != 1 || result.DeletedRows != deletionModelCount(before)-deletionModelCount(deletionSnapshot(t, reopen(t))) {
				t.Fatal("catalog deletion did not include all actual host effects", err)
			}
			wantRevoked := 0
			if model == "group" {
				wantRevoked = 1
			}
			if result.RevokedSessions != wantRevoked {
				t.Fatal("catalog deletion did not revoke cascaded account sessions", result.RevokedSessions)
			}
			survivor, err := people.MemberObjects.Using(reopen(t)).Filter(people.MemberFields.ID.Exact(tree.survivor.ID)).Get(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if survivor.Version != 2 || survivor.GrantRefID != nil || survivor.ReviewerID == nil || *survivor.ReviewerID != tree.root.ID {
				t.Fatal("catalog effects failed to merge direct membership and NULL revision")
			}
			accountSessionRecord(t, f.store, tree.removed[0], true)
			accountSessionRecord(t, f.store, tree.removed[2], model != "group")
			accountSessionRecord(t, tree.historical, tree.historicalRecord, true)
			history := deletionHistory(t, reopen(t), f.actor, "people.member", tree.survivor.ID)
			if len(history) != 1 || !slices.Contains(history[0].ChangedFields(), "grant_ref") {
				t.Fatal("catalog NULL effect omitted account audit")
			}
		})
	}
}

func accountDeletionMembershipCopies(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	for _, duplicate := range []bool{true, false} {
		t.Run(fmt.Sprintf("duplicate_%v", duplicate), func(t *testing.T) {
			root := deletionMember(t, backend, f, fmt.Sprintf("linkroot%v", duplicate), nil)
			member := deletionMember(t, backend, f, fmt.Sprintf("linkowner%v", duplicate), nil)
			group, err := access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate(fmt.Sprintf("link group %v", duplicate)))
			if err != nil {
				t.Fatal(err)
			}
			grant, err := access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate(fmt.Sprintf("copies.%v", duplicate), "Copies"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := links.MembershipObjects.Create(t.Context(), backend, links.NewMembershipCreate(member.ID, group.ID).WithAnchorID(root.ID)); err != nil {
				t.Fatal(err)
			}
			if _, err := links.GrantLinkObjects.Create(t.Context(), backend, links.NewGrantLinkCreate(group.ID, grant.ID).WithAnchorID(root.ID)); err != nil {
				t.Fatal(err)
			}
			if duplicate {
				if _, err := links.MembershipObjects.Create(t.Context(), backend, links.NewMembershipCreate(member.ID, group.ID)); err != nil {
					t.Fatal(err)
				}
				if _, err := links.GrantLinkObjects.Create(t.Context(), backend, links.NewGrantLinkCreate(group.ID, grant.ID)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := deletionOwner(t, backend).Delete(t.Context(), f.actor, root.ID, 1); err != nil {
				t.Fatal(err)
			}
			actualMember, err := people.MemberObjects.Using(reopen(t)).Filter(people.MemberFields.ID.Exact(member.ID)).Get(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			actualGroup, err := access.TeamObjects.Using(reopen(t)).Filter(access.TeamFields.ID.Exact(group.ID)).Get(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := int64(2)
			if duplicate {
				want = 1
			}
			if actualMember.Version != want || actualGroup.Version != want {
				t.Fatal("explicit-through set change did not own exactly one revision", actualMember.Version, actualGroup.Version)
			}
			if len(deletionHistory(t, backend, f.actor, "people.member", member.ID)) != 0 || len(deletionHistory(t, backend, f.actor, "access.team", group.ID)) != 0 {
				t.Fatal("implicit membership consequence became a direct edit audit")
			}
		})
	}
}

func accountDeletionSignedKeys(t *testing.T, backend Backend, reopen func(*testing.T) Backend, storedKey StoredKeyFixture) {
	f := provisionDeletion(t, backend)
	for _, key := range []int64{0, -7} {
		t.Run(fmt.Sprintf("key_%d", key), func(t *testing.T) {
			root := deletionMember(t, backend, f, fmt.Sprintf("signed%d", key), nil)
			storedKey(t, backend, "member", root.ID, key)
			record := deletionSession(t, f.manager, root.Subject)
			result, err := deletionOwner(t, backend).Delete(t.Context(), f.actor, key, 1)
			if err != nil || result.ID != key || result.DeletedRows != 1 || result.RevokedSessions != 1 {
				t.Fatal("stored signed account key not deleted", err)
			}
			accountSessionRecord(t, f.store, record, false)
			if history := deletionHistory(t, reopen(t), f.actor, "people.member", key); len(history) != 1 || history[0].ObjectID() != key {
				t.Fatal("signed-key audit omitted")
			}
		})
	}
}

func accountDeletionBatches(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	root := deletionMember(t, backend, f, "batchroot", nil)
	var members []people.Member
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for index := 0; index < 257; index++ {
			members = append(members, deletionMember(t, scope, f, fmt.Sprintf("batch%03d", index), func(value people.MemberCreate) people.MemberCreate { return value.WithReviewerID(root.ID) }))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := deletionOwner(t, backend).Delete(t.Context(), f.actor, root.ID, 1)
	if err != nil || result.DeletedRows != 1 {
		t.Fatal("complete NULL fanout deletion", err)
	}
	reader := reopen(t)
	for _, member := range members {
		row, err := people.MemberObjects.Using(reader).Filter(people.MemberFields.ID.Exact(member.ID)).Get(t.Context())
		if err != nil || row.Version != 2 || row.ReviewerID != nil {
			t.Fatal("later NULL owner batch omitted", member.ID, err)
		}
		if len(deletionHistory(t, backend, f.actor, "people.member", member.ID)) != 1 {
			t.Fatal("later owner audit omitted")
		}
	}
}
