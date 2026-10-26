package accountstoretest

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/conformance/accountdeletefixture/access"
	"github.com/progresshans/godj/conformance/accountdeletefixture/links"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/tickets"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/sessions"
)

type collectionLifecycleTree struct {
	member, victim, survivor, duplicateOwner people.Member
	oldGroup, group, deletedGroup            access.Team
	deletedGrant, grant, nextGrant           access.Grant
	anchor, retained                         links.Membership
	grantLink                                links.GrantLink
	rootSessions, victimSessions             []sessions.Record
	preserved                                []sessions.Record
	desired                                  []int64
}

func collectionLifecycleFixture(t *testing.T, backend Backend, f *deletionFixture, storedKey StoredKeyFixture, kind, mode string) collectionLifecycleTree {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var tree collectionLifecycleTree
	if kind == "account" && mode == "self" {
		tree.member = f.actorRow
	} else {
		tree.member = deletionMember(t, backend, f, "edited", nil)
	}
	if mode == "zero_key" || mode == "negative_key" {
		key := int64(0)
		if mode == "negative_key" {
			key = -11
		}
		storedKey(t, backend, "member", tree.member.ID, key)
		var err error
		tree.member, err = people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(key)).Get(t.Context())
		check(err)
	}
	var err error
	tree.oldGroup, err = access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate("Old group"))
	check(err)
	tree.group, err = access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate("Retained group"))
	check(err)
	if kind == "catalog" && (mode == "zero_key" || mode == "negative_key") {
		key := int64(0)
		if mode == "negative_key" {
			key = -7
		}
		storedKey(t, backend, "team", tree.group.ID, key)
		tree.group, err = access.TeamObjects.Using(backend).Filter(access.TeamFields.ID.Exact(key)).Get(t.Context())
		check(err)
	}
	old, err := links.MembershipObjects.Create(t.Context(), backend, links.NewMembershipCreate(tree.member.ID, tree.oldGroup.ID))
	check(err)
	// This grant is later assigned to the cascade victim. Its group link also
	// owns a retained account membership, exercising reinsertion in one batch.
	tree.deletedGrant, err = access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate("effects.old", "Deleted grant"))
	check(err)
	tree.grant, err = access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate("effects.keep", "Surviving grant"))
	check(err)
	tree.nextGrant, err = access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate("effects.next", "Next grant"))
	check(err)
	tree.desired = []int64{tree.nextGrant.ID}
	tree.grantLink, err = links.GrantLinkObjects.Create(t.Context(), backend, links.NewGrantLinkCreate(tree.group.ID, tree.deletedGrant.ID))
	check(err)
	_, err = links.GrantLinkObjects.Create(t.Context(), backend, links.NewGrantLinkCreate(tree.group.ID, tree.grant.ID))
	check(err)
	tree.retained, err = links.MembershipObjects.Create(t.Context(), backend, links.NewMembershipCreate(tree.member.ID, tree.group.ID).WithDependsOnID(tree.grantLink.ID))
	check(err)
	tree.anchor = old
	if kind == "catalog" {
		tree.anchor = tree.retained
	}
	tree.victim = deletionMember(t, backend, f, "cascade-victim", func(input people.MemberCreate) people.MemberCreate { return input.WithMembershipID(tree.anchor.ID) })
	tree.deletedGroup, err = access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate("Deleted group").WithCreatorID(tree.victim.ID))
	check(err)
	_, err = access.GrantObjects.Patch(t.Context(), backend, tree.deletedGrant, access.GrantPatch{}.WithIssuerID(tree.victim.ID).WithTeamID(tree.deletedGroup.ID))
	check(err)
	_, err = people.MemberObjects.Patch(t.Context(), backend, tree.member, people.MemberPatch{}.WithNullableMembershipID(tree.anchor.ID))
	check(err)
	_, err = access.TeamObjects.Patch(t.Context(), backend, tree.group, access.TeamPatch{}.WithNullableMembershipID(tree.anchor.ID).WithReviewerID(tree.victim.ID))
	check(err)
	_, err = access.GrantObjects.Patch(t.Context(), backend, tree.grant, access.GrantPatch{}.WithNullableMembershipID(tree.anchor.ID).WithReviewerID(tree.victim.ID))
	check(err)
	tree.survivor = deletionMember(t, backend, f, "survivor", func(input people.MemberCreate) people.MemberCreate {
		return input.WithNullableMembershipID(tree.anchor.ID).WithReviewerID(tree.victim.ID)
	})
	_, err = links.MembershipObjects.Create(t.Context(), backend, links.NewMembershipCreate(tree.survivor.ID, tree.deletedGroup.ID))
	check(err)
	tree.duplicateOwner = deletionMember(t, backend, f, "duplicate-owner", nil)
	for _, input := range []links.MembershipCreate{links.NewMembershipCreate(tree.duplicateOwner.ID, tree.group.ID).WithDependsOnID(tree.grantLink.ID), links.NewMembershipCreate(tree.duplicateOwner.ID, tree.group.ID)} {
		_, err = links.MembershipObjects.Create(t.Context(), backend, input)
		check(err)
	}
	for _, member := range []people.Member{tree.member, tree.survivor} {
		_, err = people.MemberGrantsLinkObjects.Create(t.Context(), backend, people.NewMemberGrantsLinkCreate(member.ID, tree.deletedGrant.ID))
		check(err)
	}
	_, err = people.MemberGrantsLinkObjects.Create(t.Context(), backend, people.NewMemberGrantsLinkCreate(tree.member.ID, tree.grant.ID))
	check(err)
	if mode == "final_union_bound" || mode == "effective_limit" {
		keys := []int64{tree.grant.ID, tree.nextGrant.ID}
		if kind == "catalog" {
			keys = []int64{tree.deletedGrant.ID, tree.grant.ID}
		}
		for i := 0; i < 254; i++ {
			grant, err := access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate(fmt.Sprintf("effects.bound_%03d", i), "Bounded grant"))
			check(err)
			keys = append(keys, grant.ID)
		}
		if kind == "account" {
			tree.desired = keys
		} else {
			for _, key := range keys {
				_, err = people.MemberGrantsLinkObjects.Create(t.Context(), backend, people.NewMemberGrantsLinkCreate(tree.duplicateOwner.ID, key))
				check(err)
			}
			extra, err := access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate("effects.extra_next", "Extra next grant"))
			check(err)
			tree.desired = append(tree.desired, extra.ID)
		}
	}
	if mode == "protected" {
		_, err = tickets.TicketObjects.Create(t.Context(), backend, tickets.NewTicketCreate("Protected link", f.actorRow.ID).WithProtectedGrantLinkID(tree.grantLink.ID))
		check(err)
	}
	if mode == "root_deleted" {
		if kind == "account" {
			_, err = people.MemberObjects.Patch(t.Context(), backend, tree.member, people.MemberPatch{}.WithMembershipID(tree.anchor.ID))
		} else {
			_, err = access.TeamObjects.Patch(t.Context(), backend, tree.group, access.TeamPatch{}.WithMembershipID(tree.anchor.ID))
		}
		check(err)
	}
	if mode == "corrupt_revision" {
		_, err = people.MemberObjects.Patch(t.Context(), backend, tree.survivor, people.MemberPatch{}.WithVersion(0))
		check(err)
	}
	if mode == "late_revision_failure" {
		for i := 0; i < 260; i++ {
			deletionMember(t, backend, f, fmt.Sprintf("later-%03d", i), func(input people.MemberCreate) people.MemberCreate {
				return input.WithNullableMembershipID(tree.anchor.ID)
			})
		}
	}
	tree.member, err = people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(tree.member.ID)).Get(t.Context())
	check(err)
	tree.group, err = access.TeamObjects.Using(backend).Filter(access.TeamFields.ID.Exact(tree.group.ID)).Get(t.Context())
	check(err)
	tree.rootSessions = []sessions.Record{deletionSession(t, f.manager, tree.member.Subject), deletionSession(t, f.manager, tree.member.Subject)}
	tree.victimSessions = []sessions.Record{deletionSession(t, f.manager, tree.victim.Subject)}
	if mode == "late_session_failure" {
		for len(tree.victimSessions) < 259 {
			tree.victimSessions = append(tree.victimSessions, deletionSession(t, f.manager, tree.victim.Subject))
		}
	}
	tree.preserved = []sessions.Record{deletionSession(t, f.manager, tree.survivor.Subject), deletionSession(t, f.manager, tree.duplicateOwner.Subject), deletionSession(t, f.manager, "")}
	slices.Sort(tree.desired)
	return tree
}

func assertCollectionLifecycle(t *testing.T, backend Backend, f *deletionFixture, tree collectionLifecycleTree, kind string) {
	t.Helper()
	for _, target := range []struct {
		objects any
		id      int64
	}{{people.MemberObjects, tree.victim.ID}, {access.TeamObjects, tree.deletedGroup.ID}, {access.GrantObjects, tree.deletedGrant.ID}} {
		var count int64
		var err error
		switch objects := target.objects.(type) {
		case orm.Manager[people.Member]:
			count, err = objects.Using(backend).Filter(people.MemberFields.ID.Exact(target.id)).Count(t.Context())
		case orm.Manager[access.Team]:
			count, err = objects.Using(backend).Filter(access.TeamFields.ID.Exact(target.id)).Count(t.Context())
		case orm.Manager[access.Grant]:
			count, err = objects.Using(backend).Filter(access.GrantFields.ID.Exact(target.id)).Count(t.Context())
		default:
			t.Fatal("unexpected lifecycle fixture descriptor")
		}
		if err != nil || count != 0 {
			t.Fatal("cascaded selected model remains", err)
		}
	}
	member, err := people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(tree.member.ID)).Get(t.Context())
	if err != nil || member.Version != 2 || member.NullableMembershipID != nil || member.Secret != tree.member.Secret || member.Subject != tree.member.Subject {
		t.Fatal("root account final scalar/revision state differs", err)
	}
	if kind == "account" && (member.Region != "JP" || member.Enabled) {
		t.Fatal("account candidate did not commit with collected effects")
	}
	group, err := access.TeamObjects.Using(backend).Filter(access.TeamFields.ID.Exact(tree.group.ID)).Get(t.Context())
	if err != nil || group.Version != 2 || group.NullableMembershipID != nil || group.ReviewerID != nil {
		t.Fatal("merged group effects were missing or repeated", err)
	}
	if kind == "catalog" && group.Title != "Updated group" {
		t.Fatal("catalog scalar candidate did not commit")
	}
	grant, err := access.GrantObjects.Using(backend).Filter(access.GrantFields.ID.Exact(tree.grant.ID)).Get(t.Context())
	if err != nil || grant.Version != 2 || grant.NullableMembershipID != nil || grant.ReviewerID != nil {
		t.Fatal("merged permission effects were missing or repeated", err)
	}
	survivor, err := people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(tree.survivor.ID)).Get(t.Context())
	if err != nil || survivor.Version != 2 || survivor.NullableMembershipID != nil || survivor.ReviewerID != nil {
		t.Fatal("merged account effects were missing or repeated", err)
	}
	duplicate, err := people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(tree.duplicateOwner.ID)).Get(t.Context())
	if err != nil || !reflect.DeepEqual(duplicate, tree.duplicateOwner) {
		t.Fatal("one removed duplicate or indirect grant change advanced account revision", err)
	}
	rootGroups := []int64{tree.group.ID}
	rootDirect := tree.desired
	groupGrants := []int64{tree.grant.ID}
	if kind == "catalog" {
		rootGroups, rootDirect, groupGrants = []int64{tree.oldGroup.ID}, []int64{tree.grant.ID}, tree.desired
	}
	if got := lifecycleMemberGroups(t, backend, tree.member.ID); !slices.Equal(got, rootGroups) {
		t.Fatal("final account groups differ from the composed candidate")
	}
	if got := lifecycleMemberGrants(t, backend, tree.member.ID); !slices.Equal(got, rootDirect) {
		t.Fatal("final direct grants differ from the composed candidate")
	}
	if got := lifecycleGroupGrants(t, backend, tree.group.ID); !slices.Equal(got, groupGrants) {
		t.Fatal("final group grants differ from the composed candidate")
	}
	for _, record := range tree.victimSessions {
		accountSessionRecord(t, f.store, record, false)
	}
	for _, record := range tree.rootSessions {
		accountSessionRecord(t, f.store, record, kind != "account")
	}
	for _, record := range tree.preserved {
		accountSessionRecord(t, f.store, record, true)
	}
	memberFields := []string{"nullable_membership", "teams", "grants"}
	groupFields := []string{"nullable_membership", "reviewer", "grants"}
	if kind == "account" {
		memberFields = append([]string{"enabled", "region"}, memberFields...)
	} else {
		groupFields = append([]string{"title"}, groupFields...)
	}
	for _, target := range []struct {
		model  string
		id     int64
		action admin.Action
		fields []string
	}{
		{"people.member", tree.member.ID, admin.ActionChange, memberFields},
		{"people.member", tree.victim.ID, admin.ActionDelete, nil},
		{"people.member", tree.survivor.ID, admin.ActionChange, []string{"nullable_membership", "reviewer", "teams", "grants"}},
		{"access.team", tree.group.ID, admin.ActionChange, groupFields},
		{"access.team", tree.deletedGroup.ID, admin.ActionDelete, nil},
		{"access.grant", tree.grant.ID, admin.ActionChange, []string{"nullable_membership", "reviewer"}},
		{"access.grant", tree.deletedGrant.ID, admin.ActionDelete, nil},
	} {
		events := deletionHistory(t, backend, f.actor, target.model, target.id)
		if len(events) != 1 || events[0].Action() != target.action || !slices.Equal(events[0].ChangedFields(), target.fields) || events[0].ActorID() != f.actor.ID() {
			t.Fatal("direct lifecycle effect audit was omitted, repeated or misattributed", target.model, target.id)
		}
	}
	if len(deletionHistory(t, backend, f.actor, "people.member", tree.duplicateOwner.ID)) != 0 {
		t.Fatal("unchanged direct membership received an audit")
	}
}

func lifecycleMemberGroups(t *testing.T, backend Backend, id int64) []int64 {
	t.Helper()
	rows, err := links.MembershipObjects.Using(backend).Filter(links.MembershipFields.MemberID.Exact(id)).OrderBy(links.MembershipFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var keys []int64
	for _, row := range rows {
		keys = append(keys, row.TeamID)
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}
func lifecycleMemberGrants(t *testing.T, backend Backend, id int64) []int64 {
	t.Helper()
	rows, err := people.MemberGrantsLinkObjects.Using(backend).Filter(people.MemberGrantsLinkFields.SourceID.Exact(id)).OrderBy(people.MemberGrantsLinkFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var keys []int64
	for _, row := range rows {
		keys = append(keys, row.TargetID)
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}
func lifecycleGroupGrants(t *testing.T, backend Backend, id int64) []int64 {
	t.Helper()
	rows, err := links.GrantLinkObjects.Using(backend).Filter(links.GrantLinkFields.TeamID.Exact(id)).OrderBy(links.GrantLinkFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var keys []int64
	for _, row := range rows {
		keys = append(keys, row.GrantID)
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}
