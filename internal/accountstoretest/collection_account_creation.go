package accountstoretest

import (
	"context"
	"errors"
	"reflect"
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

func collectionAccountCreation(t *testing.T, backend Backend, reopen func(*testing.T) Backend, mode string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	f := provisionDeletion(t, backend)
	_, err := people.MemberObjects.Patch(t.Context(), backend, f.actorRow, people.MemberPatch{}.WithRoot(false))
	check(err)
	var remove access.Grant
	for _, code := range []string{"account.add", "account.change", "account.delete"} {
		grant, err := access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate(code, code))
		check(err)
		if code == "account.delete" {
			remove = grant
			if mode == "new_authority_denied" {
				continue
			}
		}
		_, err = people.MemberGrantsLinkObjects.Create(t.Context(), backend, people.NewMemberGrantsLinkCreate(f.actorRow.ID, grant.ID))
		check(err)
	}
	credential, found, err := f.credentials.CredentialByID(t.Context(), f.actorRow.Subject)
	if err != nil || !found || credential.Principal().Superuser() {
		t.Fatal("creation actor fixture", err)
	}
	f.actor = credential.Principal()
	group, err := access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate("Transient new account membership"))
	check(err)
	victim := deletionMember(t, backend, f, "account-creation-victim", nil)
	session := deletionSession(t, f.manager, victim.Subject)
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	check(err)
	fault := errors.New("private account creation lifecycle fault")
	owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault}
	var observed *lifecycleCreationScope
	owner.decorate = func(session db.Session) db.Session {
		scope, ok := session.(db.RelationSession)
		if !ok {
			t.Fatal("account creation lost relation scope")
		}
		observed = &lifecycleCreationScope{RelationSession: scope, table: "people_member", afterInsert: func(ctx context.Context, native db.RelationSession, id int64) error {
			membership, err := links.MembershipObjects.Create(ctx, native, links.NewMembershipCreate(id, group.ID))
			if err != nil {
				return err
			}
			row := victim
			if mode == "root_deleted" {
				row, err = people.MemberObjects.Using(native).Filter(people.MemberFields.ID.Exact(id)).Get(ctx)
				if err != nil {
					return err
				}
			}
			if _, err := people.MemberObjects.Patch(ctx, native, row, people.MemberPatch{}.WithMembershipID(membership.ID)); err != nil {
				return err
			}
			if mode == "new_authority_denied" {
				_, err = people.MemberGrantsLinkObjects.Create(ctx, native, people.NewMemberGrantsLinkCreate(f.actorRow.ID, remove.ID))
			}
			return err
		}}
		return observed
	}
	store, err := project.NewAccountSessionStore(owner, sessiondb.Config{})
	check(err)
	related := deletionConfig()
	config := accountmodel.CreationConfig[people.Member]{
		CreationPolicy: accountmodel.CreationPolicy[people.Member]{Fields: []string{"contact", "region", "teams", "grants"},
			RelatedPermissions: accountmodel.RelatedRowPermissions{Account: related.Account, Group: related.Group, Permission: related.Permission}},
		AddPermission: "account.add", ChangePermission: "account.change",
	}
	if mode == "missing_effect_policy" {
		config.RelatedPermissions.Account = accountmodel.DeletionPermissions{}
	}
	finalChecks := 0
	config.Validate = func(ctx context.Context, scope db.Queryer, row people.Member, _ accountmodel.AccountMemberships) (validation.Errors, error) {
		if observed == nil || !observed.called {
			return validation.Errors{}, nil
		}
		finalChecks++
		if row.ID != observed.createdID || row.Version != 1 || row.Secret != "" {
			t.Fatal("final creation validator lost identity or leaked credential")
		}
		remaining, err := people.MemberObjects.Using(scope).Filter(people.MemberFields.ID.Exact(victim.ID)).Count(ctx)
		if err != nil {
			return validation.Errors{}, err
		}
		if remaining != 0 {
			t.Fatal("final validator did not observe collected graph")
		}
		if mode == "validator_rollback" {
			return validation.NewErrors(validation.New("region", "rejected")), nil
		}
		return validation.Errors{}, nil
	}
	creator, err := project.NewAccountCreator(store, auth.PrincipalAuthorizer{}, hasher, config)
	check(err)
	input := lifecycleInput(t, people.MemberObjects, map[string]query.Value{"contact": query.String("created-with-effects@example.test"), "region": query.String("KR")}, map[string][]int64{"teams": nil, "grants": nil})
	pending, err := creator.Prepare(t.Context(), f.actor, input, password)
	check(err)
	before := deletionSnapshot(t, reopen(t))
	result, failure := creator.Commit(t.Context(), f.actor, pending)
	if owner.calls != 1 || observed == nil || !observed.called || observed.ValidateSession(t.Context()) == nil {
		t.Fatal("creation scope was skipped, repeated or retained")
	}
	committed := mode == "effects_allowed" || mode == "unknown_commit" || mode == "cleanup_failure"
	if mode == "effects_allowed" {
		if failure != nil || result.ID != observed.createdID || result.Revision != 1 || result.RevokedSessions != 1 || finalChecks != 1 {
			t.Fatal("confirmed composed account creation", failure)
		}
	} else {
		if failure == nil || !reflect.DeepEqual(result, accountmodel.Creation{}) {
			t.Fatal("failed creation published provisional metadata")
		}
		switch mode {
		case "new_authority_denied", "missing_effect_policy":
			if !errors.Is(failure, &accountmodel.Error{Code: accountmodel.CodePermission}) || finalChecks != 0 {
				t.Fatal("creation granted its own effect authority", failure)
			}
		case "root_deleted":
			if !errors.Is(failure, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
				t.Fatal("creation accepted its own collected deletion", failure)
			}
		case "validator_rollback":
			if _, rejected := validation.Rejected(failure); !rejected || finalChecks != 1 {
				t.Fatal("final validation lost", failure)
			}
		case "unknown_commit", "unknown_rollback":
			if !errors.Is(failure, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
				t.Fatal("unknown outcome was downgraded", failure)
			}
		default:
			if !errors.Is(failure, fault) {
				t.Fatal("native outcome cause lost", failure)
			}
		}
	}
	if !committed {
		requireDeletionUnchanged(t, reopen(t), before)
		accountSessionRecord(t, f.store, session, true)
		return
	}
	reader := reopen(t)
	created, err := people.MemberObjects.Using(reader).Filter(people.MemberFields.ID.Exact(observed.createdID)).Get(t.Context())
	if err != nil || created.Version != 1 || created.Contact != "created-with-effects@example.test" {
		t.Fatal("confirmed creation readback", err)
	}
	remaining, err := people.MemberObjects.Using(reader).Filter(people.MemberFields.ID.Exact(victim.ID)).Count(t.Context())
	if err != nil || remaining != 0 {
		t.Fatal("creation did not apply collected deletion", err)
	}
	accountSessionRecord(t, f.store, session, false)
	added := deletionHistory(t, reader, f.actor, "people.member", created.ID)
	removed := deletionHistory(t, reader, f.actor, "people.member", victim.ID)
	if len(added) != 1 || added[0].Action() != admin.ActionAdd || len(removed) != 1 || removed[0].Action() != admin.ActionDelete || added[0].ActorID() != f.actor.ID() || removed[0].ActorID() != f.actor.ID() {
		t.Fatal("creation and effect audit are not atomic")
	}
}
