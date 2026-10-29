package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/accountdeletefixture/access"
	"github.com/progresshans/godj/conformance/accountdeletefixture/links"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	"github.com/progresshans/godj/conformance/accountdeletefixture/tickets"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func runCollectionChanges(t *testing.T, open Open, storedKey StoredKeyFixture) {
	t.Helper()
	t.Run("collection_changes", func(t *testing.T) {
		for _, mode := range []string{
			"commit", "preserved_duplicate", "clear", "nonpositive_keys", "protected",
			"query_failure", "close_failure", "callback_failure", "callback_cancel",
			"late_delete_failure", "late_insert_failure", "missing_insert", "postcondition_failure",
			"wrong_null_count", "outer_failure", "expired_preparation",
			"actual_signed_keys", "returned_key_missing", "late_row_rewrite", "late_receipt_rewrite", "readback_close", "readback_scan", "noop_returned_key", "conflict_skipped",
		} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				migrateDeletion(t, backend)
				collectionChanges(t, backend, reopen, storedKey, mode)
			})
		}
	})
}

type collectionTree struct {
	owner                            people.Member
	team, retainedTeam               access.Team
	grant, nextGrant                 access.Grant
	membership, dependent, duplicate links.Membership
	grantLink                        links.GrantLink
	deleted, nulled                  tickets.Ticket
}

func collectionTreeFixture(t *testing.T, backend Backend, storedKey StoredKeyFixture, mode string) collectionTree {
	t.Helper()
	f := provisionDeletion(t, backend)
	tree := collectionTree{owner: f.actorRow}
	var err error
	tree.team, err = access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate("old team"))
	if err != nil {
		t.Fatal(err)
	}
	tree.retainedTeam, err = access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate("retained team"))
	if err != nil {
		t.Fatal(err)
	}
	tree.grant, err = access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate("batch.old", "Old grant"))
	if err != nil {
		t.Fatal(err)
	}
	tree.nextGrant, err = access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate("batch.next", "Next grant"))
	if err != nil {
		t.Fatal(err)
	}
	if mode == "nonpositive_keys" {
		storedKey(t, backend, "member", tree.owner.ID, 0)
		storedKey(t, backend, "team", tree.retainedTeam.ID, -7)
		storedKey(t, backend, "grant", tree.nextGrant.ID, 0)
		tree.owner, err = people.MemberObjects.Using(backend).Filter(people.MemberFields.ID.Exact(0)).Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		tree.retainedTeam, err = access.TeamObjects.Using(backend).Filter(access.TeamFields.ID.Exact(-7)).Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		tree.nextGrant, err = access.GrantObjects.Using(backend).Filter(access.GrantFields.ID.Exact(0)).Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
	}
	tree.grantLink, err = links.GrantLinkObjects.Create(t.Context(), backend, links.NewGrantLinkCreate(tree.team.ID, tree.grant.ID))
	if err != nil {
		t.Fatal(err)
	}
	tree.membership, err = links.MembershipObjects.Create(t.Context(), backend, links.NewMembershipCreate(tree.owner.ID, tree.team.ID))
	if err != nil {
		t.Fatal(err)
	}
	tree.dependent, err = links.MembershipObjects.Create(t.Context(), backend, links.NewMembershipCreate(tree.owner.ID, tree.retainedTeam.ID).WithDependsOnID(tree.grantLink.ID))
	if err != nil {
		t.Fatal(err)
	}
	if mode == "preserved_duplicate" {
		tree.duplicate, err = links.MembershipObjects.Create(t.Context(), backend, links.NewMembershipCreate(tree.owner.ID, tree.retainedTeam.ID))
		if err != nil {
			t.Fatal(err)
		}
	}
	tree.deleted, err = tickets.TicketObjects.Create(t.Context(), backend, tickets.NewTicketCreate("shared dependent", tree.owner.ID).WithMembershipID(tree.membership.ID).WithGrantLinkID(tree.grantLink.ID))
	if err != nil {
		t.Fatal(err)
	}
	tree.nulled, err = tickets.TicketObjects.Create(t.Context(), backend, tickets.NewTicketCreate("two surviving nulls", tree.owner.ID).WithNullableMembershipID(tree.membership.ID).WithNullableGrantLinkID(tree.grantLink.ID))
	if err != nil {
		t.Fatal(err)
	}
	if mode == "protected" {
		if _, err := tickets.TicketObjects.Create(t.Context(), backend, tickets.NewTicketCreate("protected second collection", tree.owner.ID).WithProtectedGrantLinkID(tree.grantLink.ID)); err != nil {
			t.Fatal(err)
		}
	}
	return tree
}

type collectionFaultScope struct {
	db.RelationSession
	mode                                                string
	fault                                               error
	firstKey, owner                                     int64
	inserts, deletes, updates, nulls, closes, callbacks int
}

func (scope *collectionFaultScope) ValidateSession(ctx context.Context) error {
	return scope.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *collectionFaultScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := scope.RelationSession.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	target := plan.Table() == "links_grant_link" && len(plan.SourceFields()) == 3
	readback := plan.Table() == "links_membership" && len(plan.SourceFields()) == 5 && scope.inserts >= 3 && (scope.mode == "readback_close" || scope.mode == "readback_scan")
	fault := readback || target && (scope.mode == "query_failure" || scope.mode == "close_failure" || scope.mode == "postcondition_failure" && scope.inserts >= 3)
	if !fault {
		return rows, nil
	}
	wrapped := &collectionFaultRows{Rows: rows, scope: scope}
	if scope.mode != "close_failure" && !readback {
		return wrapped, scope.fault
	}
	return wrapped, nil
}

type collectionFaultRows struct {
	db.Rows
	scope *collectionFaultScope
}

func (rows *collectionFaultRows) Close() error {
	rows.scope.closes++
	err := rows.Rows.Close()
	if rows.scope.mode == "close_failure" || rows.scope.mode == "readback_close" {
		return errors.Join(err, rows.scope.fault)
	}
	return err
}
func (rows *collectionFaultRows) Scan(destinations ...any) error {
	if err := rows.Rows.Scan(destinations...); err != nil {
		return err
	}
	if rows.scope.mode == "readback_scan" {
		return rows.scope.fault
	}
	return nil
}
func (scope *collectionFaultScope) insertionPlan(plan query.InsertPlan) query.InsertPlan {
	if scope.mode != "actual_signed_keys" {
		return plan
	}
	key, _ := plan.ReturningKey()
	assigned := []int64{0, -7, 9007199254740993}[scope.inserts-1]
	return query.NewInsertPlanReturningKey(plan.Table(), append(plan.Assignments(), query.NewAssignment(key, query.Integer(assigned))), key)
}
func (scope *collectionFaultScope) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	count, err := scope.RelationSession.Update(ctx, plan)
	scope.updates += int(count)
	return count, err
}
func (scope *collectionFaultScope) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	count, err := scope.RelationSession.Delete(ctx, plan)
	scope.deletes += int(count)
	if err == nil && scope.mode == "late_delete_failure" && scope.deletes == 2 {
		return count, scope.fault
	}
	return count, err
}
func (scope *collectionFaultScope) RelationSetNull(ctx context.Context, plan query.RelationSetNullPlan) (int64, error) {
	count, err := scope.RelationSession.RelationSetNull(ctx, plan)
	scope.nulls += int(count)
	if err == nil && scope.mode == "wrong_null_count" {
		return 0, nil
	}
	return count, err
}
func (scope *collectionFaultScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	scope.inserts++
	if scope.mode == "missing_insert" && scope.inserts == 2 {
		return 0, nil
	}
	key, err := scope.RelationSession.Insert(ctx, scope.insertionPlan(plan))
	if err == nil && scope.mode == "late_insert_failure" && scope.inserts == 2 {
		return key, scope.fault
	}
	if err == nil && scope.inserts == 1 {
		scope.firstKey = key
	}
	if err == nil && scope.mode == "returned_key_missing" && scope.inserts == 2 {
		return key + 999999, nil
	}
	return key, err
}
func (scope *collectionFaultScope) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	scope.inserts++
	if scope.mode == "missing_insert" && scope.inserts == 2 {
		return db.ConflictInsertResult{}, nil
	}
	if scope.mode == "conflict_skipped" {
		return db.ConflictInsertResult{}, nil
	}
	plan = query.NewConflictInsertPlan(scope.insertionPlan(plan.Insert()), plan.Target())
	count, err := scope.RelationSession.(db.ConflictInserter).InsertOnConflict(ctx, plan)
	if err == nil && scope.mode == "noop_returned_key" {
		return db.ConflictInsertResult{Key: count.Key}, nil
	}
	if err == nil && scope.mode == "late_row_rewrite" {
		_, err = scope.RelationSession.Update(ctx, query.NewUpdatePlan("links_membership", []query.Assignment{query.NewAssignment(query.NewFieldRef("anchor", "anchor_id", query.FieldInteger, true), query.Integer(scope.owner))}, query.NewFieldRef("id", "id", query.FieldInteger, false), query.Integer(scope.firstKey)))
	}
	if err == nil && scope.mode == "late_insert_failure" && scope.inserts == 2 {
		return count, scope.fault
	}
	return count, err
}

func collectionChanges(t *testing.T, backend Backend, reopen func(*testing.T) Backend, storedKey StoredKeyFixture, mode string) {
	t.Helper()
	tree := collectionTreeFixture(t, backend, storedKey, mode)
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	keys := []int64{tree.retainedTeam.ID, tree.retainedTeam.ID}
	member, err := collections.PeopleMemberTeams.SetKeysChange(tree.owner, keys, orm.ManyToManySetOptions[links.Membership]{Clear: mode == "clear"})
	if err != nil {
		t.Fatal(err)
	}
	// The caller retains and may reuse its submitted key slice.
	keys[0], keys[1] = tree.team.ID, tree.team.ID
	group, err := collections.AccessTeamGrants.SetKeysChange(tree.team, []int64{tree.nextGrant.ID})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := collections.PeopleMemberGrants.SetKeysChange(tree.owner, []int64{tree.nextGrant.ID})
	if err != nil {
		t.Fatal(err)
	}
	before := deletionSnapshot(t, reopen(t))
	fault := errors.New("private whole collection batch failure")
	var observed *collectionFaultScope
	var retained orm.PreparedCollectionChanges
	var applied orm.AppliedCollectionChanges
	err = backend.CoordinatedAtomicRelation(t.Context(), func(session db.RelationSession) error {
		observed = &collectionFaultScope{RelationSession: session, mode: mode, fault: fault, owner: tree.owner.ID}
		prepared, err := orm.PrepareCollectionChangesInSession(t.Context(), observed, member, group, direct)
		if err != nil {
			return err
		}
		retained = prepared
		if err := prepared.ValidateSource(binding); err != nil {
			return err
		}
		if observed.updates != 0 || observed.deletes != 0 || observed.inserts != 0 || observed.nulls != 0 {
			t.Fatal("batch preparation performed DML")
		}
		effects := prepared.Effects()
		rows := effects.Deletions().Rows()
		if len(rows) != 5 {
			t.Fatal("union duplicated or omitted shared effects", len(rows))
		}
		var deleted, nulled int
		for _, row := range rows {
			if row.Deleted {
				deleted++
			} else if row.Model.AppLabel == "tickets" && row.Key == tree.nulled.ID {
				nulled++
				if !slices.Equal(row.NullFields, []string{"nullable_grant_link", "nullable_membership"}) {
					t.Fatal("multiple NULL fields were not merged")
				}
			}
		}
		if deleted != 4 || nulled != 1 {
			t.Fatal("whole collected deletion changed shape", deleted, nulled)
		}
		wantInserts := 3
		if mode == "preserved_duplicate" {
			wantInserts--
		}
		if len(effects.Insertions()) != wantInserts {
			t.Fatal("retained collection did not account for another collection's cascade")
		}
		original := prepared.Effects()
		rows[0].NullFields = []string{"changed"}
		insertions := effects.Insertions()
		insertions[0].Values["member"] = query.Integer(99999)
		insertions[0].Model.ModelName = "changed"
		if !reflect.DeepEqual(original, prepared.Effects()) {
			t.Fatal("effect accessors aliased the executable plan")
		}
		if mode == "expired_preparation" {
			return nil
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		applied, err = prepared.Apply(ctx, func(ctx context.Context, effects orm.CollectionChangeEffects) error {
			observed.callbacks++
			if !reflect.DeepEqual(original, effects) {
				t.Fatal("callback saw different collected effects")
			}
			updated := deletionAssign(t, observed, access.TeamObjects, tree.retainedTeam, map[string]query.Value{"version": query.Integer(2)})
			if updated.Version != 2 {
				t.Fatal("companion update did not run")
			}
			if mode == "callback_cancel" {
				cancel()
			}
			if mode == "callback_failure" {
				return fault
			}
			return nil
		})
		if err != nil {
			if len(applied.Insertions()) != 0 || applied.ValidateSource(binding) == nil {
				t.Fatal("partial inserted rows escaped failed Apply")
			}
			return err
		}
		if err := applied.ValidateSource(binding); err != nil {
			return err
		}
		insertedRows := applied.Insertions()
		if len(insertedRows) != wantInserts {
			t.Fatal("owned actual insertion count", len(insertedRows), wantInserts)
		}
		originalRows := applied.Insertions()
		insertedRows[0].Values["id"] = query.Integer(99999)
		insertedRows[0].Model.ModelName = "changed"
		if !reflect.DeepEqual(originalRows, applied.Insertions()) {
			t.Fatal("applied rows alias caller memory")
		}
		for _, value := range []any{applied, originalRows[0]} {
			if !strings.Contains(fmt.Sprintf("%#v", value), "redacted") {
				t.Fatal("applied rows exposed values")
			}
		}
		if mode == "late_receipt_rewrite" {
			count, err := observed.Update(ctx, query.NewUpdatePlan("links_membership", []query.Assignment{query.NewAssignment(query.NewFieldRef("anchor", "anchor_id", query.FieldInteger, true), query.Integer(tree.owner.ID))}, query.NewFieldRef("id", "id", query.FieldInteger, false), query.Integer(observed.firstKey)))
			if err != nil {
				return err
			}
			if count != 1 {
				t.Fatal("late receipt rewrite did not reach stored row")
			}
		}
		if err := applied.Verify(ctx); err != nil {
			return err
		}
		if mode == "commit" || mode == "actual_signed_keys" {
			again, err := orm.PrepareCollectionChangesInSession(ctx, observed, member, group, direct)
			if err != nil {
				return err
			}
			noop, err := again.Apply(ctx, nil)
			if err != nil || noop.ValidateSource(binding) != nil || len(noop.Insertions()) != 0 {
				t.Fatal("retained memberships invented insertions", err)
			}
		}
		writes := observed.inserts + observed.updates + observed.deletes + observed.nulls
		copy := prepared
		if _, err := copy.Apply(t.Context(), nil); err == nil || writes != observed.inserts+observed.updates+observed.deletes+observed.nulls {
			t.Fatal("copied prepared batch replayed writes", err)
		}
		if mode == "outer_failure" {
			return fault
		}
		return nil
	})
	if mode == "expired_preparation" {
		if err != nil {
			t.Fatal(err)
		}
		if _, err := retained.Apply(t.Context(), func(context.Context, orm.CollectionChangeEffects) error {
			t.Fatal("expired preparation invoked callback")
			return nil
		}); err == nil {
			t.Fatal("prepared collection escaped its transaction")
		}
		requireDeletionUnchanged(t, reopen(t), before)
		return
	}
	success := mode == "commit" || mode == "preserved_duplicate" || mode == "clear" || mode == "nonpositive_keys" || mode == "actual_signed_keys"
	if !success {
		if err == nil {
			t.Fatal("failure mode committed", mode)
		}
		if mode == "protected" {
			var protected *query.ProtectedForeignKeyError
			if !errors.As(err, &protected) || protected.ProtectedSourceRows() != 1 {
				t.Fatal("second collection PROTECT was lost", err)
			}
		} else if mode == "callback_cancel" {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
		} else if !slices.Contains([]string{"missing_insert", "wrong_null_count", "returned_key_missing", "late_row_rewrite", "late_receipt_rewrite", "noop_returned_key", "conflict_skipped"}, mode) && !errors.Is(err, fault) {
			t.Fatal("failure cause lost", mode, err)
		}
		if mode == "query_failure" || mode == "close_failure" || mode == "protected" {
			if observed.callbacks != 0 || observed.updates+observed.deletes+observed.inserts+observed.nulls != 0 {
				t.Fatal("partial preparation reached a callback or write")
			}
		} else if observed.callbacks != 1 {
			t.Fatal("whole batch callback was missing or repeated")
		}
		if mode == "query_failure" || mode == "close_failure" || mode == "postcondition_failure" || mode == "readback_close" || mode == "readback_scan" {
			if observed.closes != 1 {
				t.Fatal("failed batch query did not close its returned rowset exactly once")
			}
		}
		if mode == "late_delete_failure" && observed.deletes != 2 || mode == "late_insert_failure" && observed.inserts != 2 || mode == "postcondition_failure" && observed.inserts != 3 {
			t.Fatal("late failure did not reach actual preceding writes")
		}
		requireDeletionUnchanged(t, reopen(t), before)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := applied.Verify(t.Context()); err == nil {
		t.Fatal("committed result verification reopened an expired scope")
	}
	after := deletionSnapshot(t, reopen(t))
	if !reflect.DeepEqual(before["domain"], after["domain"]) || !reflect.DeepEqual(before["session"], after["session"]) || !reflect.DeepEqual(before["audit"], after["audit"]) {
		t.Fatal("generic collection companion test altered account lifecycle storage")
	}
	memberLinks := deletionRows(t, reopen(t), links.MembershipObjects)
	if len(memberLinks) != 1 || memberLinks[0].MemberID != tree.owner.ID || memberLinks[0].TeamID != tree.retainedTeam.ID || memberLinks[0].DependsOnID != nil {
		t.Fatal("retained selection was lost with another collection's cascade")
	}
	if mode == "preserved_duplicate" {
		if memberLinks[0].ID != tree.duplicate.ID {
			t.Fatal("existing duplicate's identity was replaced")
		}
	} else if memberLinks[0].ID == tree.dependent.ID {
		t.Fatal("cascaded retained link was not recreated")
	}
	grantLinks := deletionRows(t, reopen(t), links.GrantLinkObjects)
	if len(grantLinks) != 1 || grantLinks[0].TeamID != tree.team.ID || grantLinks[0].GrantID != tree.nextGrant.ID {
		t.Fatal("second collection replacement incomplete")
	}
	directLinks := deletionRows(t, reopen(t), people.MemberGrantsLinkObjects)
	if len(directLinks) != 1 || directLinks[0].SourceID != tree.owner.ID || directLinks[0].TargetID != tree.nextGrant.ID {
		t.Fatal("automatic through insertion incomplete")
	}
	expectedRows := map[string]map[string]query.Value{}
	if mode != "preserved_duplicate" {
		values, e := links.MembershipObjects.ModelValues(memberLinks[0])
		if e != nil {
			t.Fatal(e)
		}
		expectedRows["links.membership"] = values
	}
	values, e := links.GrantLinkObjects.ModelValues(grantLinks[0])
	if e != nil {
		t.Fatal(e)
	}
	expectedRows["links.grant_link"] = values
	values, e = people.MemberGrantsLinkObjects.ModelValues(directLinks[0])
	if e != nil {
		t.Fatal(e)
	}
	expectedRows["people.member_grants"] = values
	for _, row := range applied.Insertions() {
		name := row.Model.AppLabel + "." + row.Model.ModelName
		want, ok := expectedRows[name]
		key, valid := row.Values["id"].Integer()
		if !ok || !valid || key != row.Key || !maps.EqualFunc(want, row.Values, query.Value.Equal) {
			t.Fatal("inserted receipt differs from native full row", name)
		}
		delete(expectedRows, name)
	}
	if len(expectedRows) != 0 {
		t.Fatal("native inserted row omitted from result")
	}
	if mode == "actual_signed_keys" && (memberLinks[0].ID != 0 || grantLinks[0].ID != -7 || directLinks[0].ID != 9007199254740993) {
		t.Fatal("native signed/large inserted keys not retained")
	}
	actualTickets := deletionRows(t, reopen(t), tickets.TicketObjects)
	if len(actualTickets) != 1 || actualTickets[0].ID != tree.nulled.ID || actualTickets[0].NullableMembershipID != nil || actualTickets[0].NullableGrantLinkID != nil {
		t.Fatal("shared delete or combined NULL effect incomplete")
	}
	actual, err := access.TeamObjects.Using(reopen(t)).Filter(access.TeamFields.ID.Exact(tree.retainedTeam.ID)).Get(t.Context())
	if err != nil || actual.Version != 2 || observed.callbacks != 1 || observed.deletes != 4 {
		t.Fatal("companion write or shared deletion repeated", err, fmt.Sprint(observed.callbacks))
	}
}
