package accountstoretest

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/conformance/accountdeletefixture/links"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	"github.com/progresshans/godj/conformance/accountdeletefixture/tickets"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func runCollectionObservations(t *testing.T, open Open, storedKey StoredKeyFixture) {
	t.Helper()
	t.Run("collection_observations", func(t *testing.T) {
		for _, mode := range []string{"commit", "nonpositive_keys", "snapshot_cleanup", "row_close", "protected", "link_identity", "new_descendant", "reparented_cascade", "reparented_null", "changed_nullable_endpoint", "final_query_failure", "final_close_failure", "late_insert_failure", "outer_failure"} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				migrateDeletion(t, backend)
				collectionObservation(t, backend, reopen, storedKey, mode)
			})
		}
	})
}

type collectionObservedInput struct {
	input links.MembershipCreate
	calls *int
}

func (input collectionObservedInput) BuildManyToManyCreate(source, target ir.Field, sourceKey, targetKey int64) orm.Mutation[links.Membership] {
	*input.calls++
	return input.input.BuildManyToManyCreate(source, target, sourceKey, targetKey)
}

type collectionObservationReadScope struct {
	db.Queryer
	db.SessionValidator
	fault  error
	closes int
}

func (scope *collectionObservationReadScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := scope.Queryer.Query(ctx, plan)
	if err != nil || plan.Table() != "links_grant_link" || len(plan.SourceFields()) != 3 {
		return rows, err
	}
	return &collectionObservationRows{Rows: rows, scope: scope}, nil
}

type collectionObservationRows struct {
	db.Rows
	scope *collectionObservationReadScope
}

func (rows *collectionObservationRows) Close() error {
	rows.scope.closes++
	return errors.Join(rows.Rows.Close(), rows.scope.fault)
}

type collectionObservationCleanup struct {
	db.SnapshotReader
	fault error
}

func (reader collectionObservationCleanup) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	return errors.Join(reader.SnapshotReader.ReadSnapshot(ctx, read), reader.fault)
}

func collectionObservation(t *testing.T, backend Backend, reopen func(*testing.T) Backend, storedKey StoredKeyFixture, mode string) {
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
	makeChanges := func(calls *int, anchor bool) []orm.CollectionChange {
		input := links.MembershipCreate{}
		if anchor {
			input = input.WithAnchorID(tree.owner.ID)
		}
		member, err := collections.PeopleMemberTeams.SetKeysChange(tree.owner, []int64{tree.retainedTeam.ID}, orm.ManyToManySetOptions[links.Membership]{ThroughDefaults: collectionObservedInput{input: input, calls: calls}})
		if err != nil {
			t.Fatal(err)
		}
		group, err := collections.AccessTeamGrants.SetKeysChange(tree.team, []int64{tree.nextGrant.ID})
		if err != nil {
			t.Fatal(err)
		}
		direct, err := collections.PeopleMemberGrants.SetKeysChange(tree.owner, []int64{tree.nextGrant.ID})
		if err != nil {
			t.Fatal(err)
		}
		return []orm.CollectionChange{member, group, direct}
	}
	before := deletionSnapshot(t, reopen(t))
	fault := errors.New("private collection observation failure")
	readCalls, finalCalls := 0, 0
	changes := makeChanges(&readCalls, false)
	var snapshots db.SnapshotReader = backend
	if mode == "snapshot_cleanup" {
		snapshots = collectionObservationCleanup{SnapshotReader: backend, fault: fault}
	}
	var retainedReader db.Queryer
	var wrapped *collectionObservationReadScope
	observed, err := db.ReadSnapshotResult(t.Context(), snapshots, func(ctx context.Context, reader db.Queryer) (orm.ObservedCollectionChanges, error) {
		retainedReader = reader
		if mode == "row_close" {
			wrapped = &collectionObservationReadScope{Queryer: reader, SessionValidator: reader.(db.SessionValidator), fault: fault}
			reader = wrapped
		}
		return orm.ObserveCollectionChangesInReadScope(ctx, reader, changes...)
	})
	if mode == "snapshot_cleanup" || mode == "row_close" || mode == "protected" {
		if err == nil || observed.ValidateSource(binding) == nil || len(observed.Effects().Insertions()) != 0 {
			t.Fatal("failed read published a collection observation", err)
		}
		if mode == "protected" {
			var protected *query.ProtectedForeignKeyError
			if !errors.As(err, &protected) || protected.ProtectedSourceRows() != 1 || readCalls != 0 {
				t.Fatal("PROTECT did not precede typed input preparation", err)
			}
		} else if !errors.Is(err, fault) {
			t.Fatal("read cleanup cause lost", err)
		}
		if mode == "row_close" && (wrapped.closes != 1 || readCalls != 0) {
			t.Fatal("failed row cleanup did not prevent typed preparation")
		}
		requireDeletionUnchanged(t, reopen(t), before)
		return
	}
	if err != nil || observed.ValidateSource(binding) != nil || readCalls != 1 {
		t.Fatal("native collection observation failed", err)
	}
	if len(observed.Effects().Deletions().Rows()) != 5 || len(observed.Effects().Insertions()) != 3 {
		t.Fatal("snapshot omitted the cross-collection cascade or its required replacement")
	}
	if err := retainedReader.(db.SessionValidator).ValidateSession(t.Context()); err == nil {
		t.Fatal("observation retained a live read snapshot")
	}
	requireDeletionUnchanged(t, reopen(t), before)
	// Each mutation commits through an independent connection after the read
	// snapshot closes. Final rejection must preserve this newly committed state.
	other := reopen(t)
	switch mode {
	case "link_identity":
		replacement, err := links.MembershipObjects.Create(t.Context(), other, links.NewMembershipCreate(tree.owner.ID, tree.retainedTeam.ID).WithDependsOnID(tree.grantLink.ID))
		if err != nil || replacement.ID == tree.dependent.ID {
			t.Fatal("physical link replacement fixture", err)
		}
		count, err := other.Delete(t.Context(), query.NewDeletePlan("links_membership", query.NewFieldRef("id", "id", query.FieldInteger, false), query.Integer(tree.dependent.ID)))
		if err != nil || count != 1 {
			t.Fatal("physical link replacement did not remove its old key", err)
		}
	case "new_descendant":
		if _, err := tickets.TicketObjects.Create(t.Context(), other, tickets.NewTicketCreate("new dependent", tree.owner.ID).WithMembershipID(tree.membership.ID)); err != nil {
			t.Fatal(err)
		}
	case "reparented_cascade":
		deletionAssign(t, other, tickets.TicketObjects, tree.deleted, map[string]query.Value{"membership": query.Integer(tree.dependent.ID)})
	case "reparented_null":
		deletionAssign(t, other, tickets.TicketObjects, tree.nulled, map[string]query.Value{"nullable_membership": query.Integer(tree.dependent.ID)})
	}
	baseline := deletionSnapshot(t, reopen(t))
	final := makeChanges(&finalCalls, mode == "changed_nullable_endpoint")
	var writes *collectionFaultScope
	var applied orm.AppliedCollectionChanges
	err = backend.CoordinatedAtomicRelation(t.Context(), func(session db.RelationSession) error {
		faultMode := mode
		if mode == "final_query_failure" {
			faultMode = "query_failure"
		}
		if mode == "final_close_failure" {
			faultMode = "close_failure"
		}
		writes = &collectionFaultScope{RelationSession: session, mode: faultMode, fault: fault, owner: tree.owner.ID}
		prepared, err := observed.PrepareInSession(t.Context(), writes, final...)
		if err != nil {
			if prepared.ValidateSource(binding) == nil || len(prepared.Effects().Insertions()) != 0 {
				t.Fatal("failed recheck published executable effects")
			}
			return err
		}
		if writes.inserts+writes.updates+writes.deletes+writes.nulls != 0 || finalCalls != 1 || readCalls != 1 {
			t.Fatal("recheck wrote or retained/repeated a snapshot input")
		}
		applied, err = prepared.Apply(t.Context(), func(context.Context, orm.CollectionChangeEffects) error { writes.callbacks++; return nil })
		if err != nil {
			if applied.ValidateSource(binding) == nil || len(applied.Insertions()) != 0 {
				t.Fatal("failed apply published partial insertions")
			}
			return err
		}
		if mode == "outer_failure" {
			return fault
		}
		return applied.Verify(t.Context())
	})
	if mode != "commit" && mode != "nonpositive_keys" {
		if err == nil {
			t.Fatal("changed or failed observation committed", mode)
		}
		stale := slices.Contains([]string{"link_identity", "new_descendant", "reparented_cascade", "reparented_null", "changed_nullable_endpoint"}, mode)
		if stale && !errors.Is(err, &query.Error{Code: query.CodeCollectionChanged}) || !stale && !errors.Is(err, fault) {
			t.Fatal("recheck lost its conflict or failure classification", err)
		}
		if stale || mode == "final_query_failure" || mode == "final_close_failure" {
			wantBuilds := 0
			if mode == "changed_nullable_endpoint" {
				wantBuilds = 1
			}
			if writes.inserts+writes.updates+writes.deletes+writes.nulls+writes.callbacks != 0 || finalCalls != wantBuilds {
				t.Fatal("stale topology reached inputs or DML")
			}
		}
		if mode == "late_insert_failure" && (writes.inserts != 2 || writes.callbacks != 1) {
			t.Fatal("late failure did not follow actual batch writes")
		}
		requireDeletionUnchanged(t, reopen(t), baseline)
		return
	}
	if err != nil || writes.callbacks != 1 || writes.inserts != 3 || writes.deletes != 4 || writes.nulls != 2 {
		t.Fatal("native observed collection commit", err)
	}
	memberRows := deletionRows(t, reopen(t), links.MembershipObjects)
	grantRows := deletionRows(t, reopen(t), links.GrantLinkObjects)
	directRows := deletionRows(t, reopen(t), people.MemberGrantsLinkObjects)
	ticketRows := deletionRows(t, reopen(t), tickets.TicketObjects)
	if len(memberRows) != 1 || memberRows[0].MemberID != tree.owner.ID || memberRows[0].TeamID != tree.retainedTeam.ID || memberRows[0].DependsOnID != nil ||
		len(grantRows) != 1 || grantRows[0].TeamID != tree.team.ID || grantRows[0].GrantID != tree.nextGrant.ID ||
		len(directRows) != 1 || directRows[0].SourceID != tree.owner.ID || directRows[0].TargetID != tree.nextGrant.ID ||
		len(ticketRows) != 1 || ticketRows[0].ID != tree.nulled.ID || ticketRows[0].NullableMembershipID != nil || ticketRows[0].NullableGrantLinkID != nil {
		t.Fatal("observed commit disagrees with independently read full graph")
	}
	full := make(map[ir.ModelIdentity]map[string]query.Value)
	full[ir.ModelIdentity{AppLabel: "links", ModelName: "membership"}], err = links.MembershipObjects.ModelValues(memberRows[0])
	if err != nil {
		t.Fatal(err)
	}
	full[ir.ModelIdentity{AppLabel: "links", ModelName: "grant_link"}], err = links.GrantLinkObjects.ModelValues(grantRows[0])
	if err != nil {
		t.Fatal(err)
	}
	full[ir.ModelIdentity{AppLabel: "people", ModelName: "member_grants"}], err = people.MemberGrantsLinkObjects.ModelValues(directRows[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range applied.Insertions() {
		values, found := full[row.Model]
		if !found || !maps.EqualFunc(values, row.Values, query.Value.Equal) {
			t.Fatal("actual inserted row differs from its receipt")
		}
		delete(full, row.Model)
	}
	if len(full) != 0 {
		t.Fatal("inserted receipt omitted native rows")
	}
	after := deletionSnapshot(t, reopen(t))
	for _, name := range []string{"domain", "session", "audit"} {
		if !reflect.DeepEqual(before[name], after[name]) {
			t.Fatal("generic collection observation wrote account lifecycle state", name)
		}
	}
}
