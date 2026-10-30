package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/accountdeletefixture/access"
	"github.com/progresshans/godj/conformance/accountdeletefixture/links"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

// This native-session decorator establishes links immediately after the real
// catalog INSERT. It exercises the same transaction's post-insert state; it
// does not claim to install or validate any backend-specific trigger DDL.
type lifecycleCreationScope struct {
	db.RelationSession
	late        lifecycleLateCollectionRow
	afterInsert func(context.Context, db.RelationSession, int64) error
	createdID   int64
	table       string
	called      bool
}

func (scope *lifecycleCreationScope) ValidateSession(ctx context.Context) error {
	return scope.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}

func (scope *lifecycleCreationScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := scope.late.beforeRead(ctx, scope.RelationSession, plan); err != nil {
		return nil, err
	}
	return scope.RelationSession.Query(ctx, plan)
}

func (scope *lifecycleCreationScope) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	result, err := scope.RelationSession.(db.ConflictInserter).InsertOnConflict(ctx, plan)
	if err == nil && result.Inserted {
		scope.late.inserted(plan.Insert(), result.Key)
	}
	return result, err
}
func (scope *lifecycleCreationScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := scope.RelationSession.Insert(ctx, plan)
	if err == nil {
		scope.late.inserted(plan, key)
	}
	table := scope.table
	if table == "" {
		table = "access_team"
	}
	if err != nil || plan.Table() != table {
		return key, err
	}
	if scope.called {
		return key, errors.New("catalog creation repeated its root insert")
	}
	scope.called, scope.createdID = true, key
	scope.late.reference = key
	return key, scope.afterInsert(ctx, scope.RelationSession, key)
}

func collectionCatalogCreation(t *testing.T, backend Backend, reopen func(*testing.T) Backend, mode string) {
	t.Helper()
	f := provisionDeletion(t, backend)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := people.MemberObjects.Patch(t.Context(), backend, f.actorRow, people.MemberPatch{}.WithRoot(false))
	check(err)
	add, err := access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate("catalog.add_group", "Create group"))
	check(err)
	remove, err := access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate("account.delete", "Delete account"))
	check(err)
	for _, key := range []int64{add.ID, remove.ID} {
		if key == remove.ID && mode == "new_authority_denied" {
			continue
		}
		_, err := people.MemberGrantsLinkObjects.Create(t.Context(), backend, people.NewMemberGrantsLinkCreate(f.actorRow.ID, key))
		check(err)
	}
	credential, present, err := f.credentials.CredentialByID(t.Context(), f.actorRow.Subject)
	if err != nil || !present || credential.Principal().Superuser() {
		t.Fatal("creation fixture did not remove superuser authority", err)
	}
	f.actor = credential.Principal()
	victim := deletionMember(t, backend, f, "created-group-victim", nil)
	session := deletionSession(t, f.manager, victim.Subject)
	var observed *lifecycleCreationScope
	owner := &profileFaultBackend{Backend: backend}
	owner.decorate = func(session db.Session) db.Session {
		relation, ok := session.(db.RelationSession)
		if !ok {
			t.Fatal("catalog creation lost native relation session")
		}
		observed = &lifecycleCreationScope{RelationSession: relation, late: lifecycleLateCollectionRow{mode: mode, rootTable: "access_team"}, afterInsert: func(ctx context.Context, scope db.RelationSession, id int64) error {
			grantLink, err := links.GrantLinkObjects.Create(ctx, scope, links.NewGrantLinkCreate(id, remove.ID))
			if err != nil {
				return err
			}
			membership, err := links.MembershipObjects.Create(ctx, scope, links.NewMembershipCreate(victim.ID, id).WithDependsOnID(grantLink.ID))
			if err != nil {
				return err
			}
			if _, err := people.MemberObjects.Patch(ctx, scope, victim, people.MemberPatch{}.WithMembershipID(membership.ID)); err != nil {
				return err
			}
			if mode == "new_authority_denied" {
				_, err := people.MemberGrantsLinkObjects.Create(ctx, scope, people.NewMemberGrantsLinkCreate(f.actorRow.ID, remove.ID))
				return err
			}
			return nil
		}}
		return observed
	}
	store, err := project.NewAccountSessionStore(owner, sessiondb.Config{})
	check(err)
	config := lifecycleCatalogConfig()
	validations := 0
	config.Group.Validate = func(_ context.Context, _ db.Queryer, current *access.Team, candidate access.Team) (validation.Errors, error) {
		validations++
		if current != nil || observed == nil || !observed.called || candidate.ID != observed.createdID || candidate.Version != 1 || candidate.Title != "Created with effects" {
			t.Fatal("creation validator missed the final generated identity")
		}
		if mode == "validator_rollback" {
			return validation.NewErrors(validation.New("title", "rejected")), nil
		}
		return validation.Errors{}, nil
	}
	catalog, err := project.NewAccountCatalog(store, auth.PrincipalAuthorizer{}, config)
	check(err)
	before := deletionSnapshot(t, reopen(t))
	var selected []int64
	if mode == "late_link_key" {
		selected = []int64{add.ID}
	}
	result, err := catalog.Groups().Create(t.Context(), f.actor, lifecycleInput(t, access.TeamObjects, map[string]query.Value{"title": query.String("Created with effects")}, map[string][]int64{"grants": selected}))
	if owner.calls != 1 || observed == nil || !observed.called || observed.ValidateSession(t.Context()) == nil {
		t.Fatal("creation did not use exactly one expired native owner")
	}
	if mode != "effects_allowed" {
		if result.ID != 0 || result.Revision != 0 || result.ChangedFields != nil || result.RevokedSessions != 0 {
			t.Fatal("failed creation published a result")
		}
		if mode == "new_authority_denied" {
			if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || validations != 0 {
				t.Fatal("provisional insert conferred new effect authority", err)
			}
		} else if mode == "late_link_key" {
			if !observed.late.changed || validations != 1 || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePersistence}) {
				t.Fatal("late created catalog link rewrite was not rejected", err)
			}
		} else if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("title").Empty() || validations != 1 {
			t.Fatal("creation validator refusal was lost", err)
		}
		requireDeletionUnchanged(t, reopen(t), before)
		accountSessionRecord(t, f.store, session, true)
		return
	}
	if err != nil || result.ID != observed.createdID || result.Revision != 1 || result.RevokedSessions != 1 || !slices.Equal(result.ChangedFields, []string{"title", "grants"}) || validations != 1 {
		t.Fatal("composed creation result differs", err)
	}
	reader := reopen(t)
	row, err := access.TeamObjects.Using(reader).Filter(access.TeamFields.ID.Exact(result.ID)).Get(t.Context())
	if err != nil || row.Version != 1 || row.Title != "Created with effects" || len(lifecycleGroupGrants(t, reader, result.ID)) != 0 {
		t.Fatal("created catalog state differs", err)
	}
	count, err := people.MemberObjects.Using(reader).Filter(people.MemberFields.ID.Exact(victim.ID)).Count(t.Context())
	if err != nil || count != 0 {
		t.Fatal("creation did not apply authorized collected deletion", err)
	}
	accountSessionRecord(t, f.store, session, false)
	created := deletionHistory(t, reader, f.actor, "access.team", result.ID)
	deleted := deletionHistory(t, reader, f.actor, "people.member", victim.ID)
	if len(created) != 1 || created[0].Action() != admin.ActionAdd || !slices.Equal(created[0].ChangedFields(), []string{"title", "grants"}) || len(deleted) != 1 || deleted[0].Action() != admin.ActionDelete || created[0].ActorID() != f.actor.ID() || deleted[0].ActorID() != f.actor.ID() {
		t.Fatal("creation and collected deletion audits differ")
	}
	after := deletionSnapshot(t, reader)
	for _, name := range []string{"grant", "direct", "domain", "ticket"} {
		if !reflect.DeepEqual(before[name], after[name]) {
			t.Fatal("creation changed unrelated storage", name)
		}
	}
	if reflect.ValueOf(after["membership"]).Len() != 0 || reflect.ValueOf(after["grant_link"]).Len() != 0 || reflect.ValueOf(after["audit"]).Len() != 2 {
		t.Fatal("creation retained transient links or repeated audit")
	}
}
