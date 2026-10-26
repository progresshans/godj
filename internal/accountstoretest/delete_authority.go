package accountstoretest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"testing"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/accountdeletefixture/access"
	"github.com/progresshans/godj/conformance/accountdeletefixture/links"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	"github.com/progresshans/godj/conformance/accountdeletefixture/tickets"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func deletionAssign[M any](t *testing.T, backend db.Session, objects orm.Manager[M], row M, values map[string]query.Value) M {
	t.Helper()
	prepared, err := objects.PrepareValues(&row, values)
	if err != nil {
		t.Fatal(err)
	}
	value, err := prepared.Model()
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Save(t.Context(), backend, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func deletionActorGrants(t *testing.T, backend Backend, f *deletionFixture, codes ...auth.Permission) {
	t.Helper()
	links := deletionRows(t, backend, people.MemberGrantsLinkObjects)
	for _, link := range links {
		if link.SourceID == f.actorRow.ID {
			if count, err := people.MemberGrantsLinkObjects.Delete(t.Context(), backend, &link); err != nil || count != 1 {
				t.Fatal("reset actor grants", err)
			}
		}
	}
	for _, code := range codes {
		row, found, err := access.GrantObjects.Using(backend).Filter(access.GrantFields.Key.Exact(string(code))).OrderBy(access.GrantFields.ID.Asc()).First(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			row, err = access.GrantObjects.Create(t.Context(), backend, access.NewGrantCreate(string(code), string(code)))
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := people.MemberGrantsLinkObjects.Create(t.Context(), backend, people.NewMemberGrantsLinkCreate(f.actorRow.ID, row.ID)); err != nil {
			t.Fatal(err)
		}
	}
}

func accountDeletionAuthority(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	tree := deletionTreeFixture(t, backend, f, "authority")
	// Keep the old superuser Principal as the submitted observation; current
	// database grants must control every attempted deletion below.
	f.actorRow = deletionAssign(t, backend, people.MemberObjects, f.actorRow, map[string]query.Value{"root": query.Boolean(false)})
	all := []auth.Permission{"account.delete", "account.change", "catalog.delete_group", "catalog.change_group", "catalog.delete_permission", "catalog.change_permission"}
	for _, mode := range []string{"root_permission", "cascade_permission", "survivor_permission", "additional_permission", "overlay_denial", "overlay_failure", "inactive_actor", "stale_revision", "not_found", "corrupt_dependent", "overflow_survivor", "protected_descendant"} {
		t.Run(mode, func(t *testing.T) {
			var grants []auth.Permission
			for _, code := range all {
				if mode == "root_permission" && code == "account.delete" || mode == "cascade_permission" && code == "catalog.delete_group" || mode == "survivor_permission" && code == "account.change" {
					continue
				}
				grants = append(grants, code)
			}
			deletionActorGrants(t, backend, f, grants...)
			var overlay auth.Authorizer = auth.PrincipalAuthorizer{}
			fault := errors.New("private collected deletion authority failure")
			if mode == "overlay_denial" || mode == "overlay_failure" {
				overlay = overlayFunc(func(_ context.Context, _ auth.Principal, permission auth.Permission) (bool, error) {
					if permission != "catalog.delete_group" {
						return true, nil
					}
					if mode == "overlay_failure" {
						return false, fault
					}
					return false, nil
				})
			}
			config := deletionConfig()
			if mode == "additional_permission" {
				config.Group.Additional = []auth.Permission{"catalog.confirm"}
			}
			id, revision := tree.root.ID, int64(1)
			if mode == "stale_revision" {
				revision = 2
			}
			if mode == "not_found" {
				id = math.MaxInt64
			}
			if mode == "inactive_actor" {
				f.actorRow = deletionAssign(t, backend, people.MemberObjects, f.actorRow, map[string]query.Value{"enabled": query.Boolean(false)})
			}
			if mode == "corrupt_dependent" {
				tree.child = deletionAssign(t, backend, people.MemberObjects, tree.child, map[string]query.Value{"version": query.Integer(0)})
			}
			if mode == "overflow_survivor" {
				tree.survivor = deletionAssign(t, backend, people.MemberObjects, tree.survivor, map[string]query.Value{"version": query.Integer(math.MaxInt64)})
			}
			var guard tickets.Ticket
			if mode == "protected_descendant" {
				var err error
				guard, err = tickets.TicketObjects.Create(t.Context(), backend, tickets.NewTicketCreate("protect descendant", tree.child.ID))
				if err != nil {
					t.Fatal(err)
				}
			}
			owner, err := project.NewAccountDeleter(f.store, overlay, config)
			if err != nil {
				t.Fatal(err)
			}
			before := deletionSnapshot(t, reopen(t))
			result, err := owner.Delete(t.Context(), f.actor, id, revision)
			want := accountmodel.CodePermission
			switch mode {
			case "stale_revision":
				want = accountmodel.CodeConflict
			case "not_found":
				want = accountmodel.CodeNotFound
			case "overlay_failure", "corrupt_dependent", "overflow_survivor", "protected_descendant":
				want = accountmodel.CodePersistence
			}
			if !errors.Is(err, &accountmodel.Error{Code: want}) || result != (accountmodel.AccountDeletion{}) {
				t.Fatal("collected deletion authority failure was misclassified or published", mode, err)
			}
			if mode == "overlay_failure" && !errors.Is(err, fault) {
				t.Fatal("private authority cause lost")
			}
			if mode == "protected_descendant" {
				var protected *query.ProtectedForeignKeyError
				if !errors.As(err, &protected) || protected.ProtectedSourceRows() != 1 {
					t.Fatal("descendant protection missing", err)
				}
			}
			requireDeletionUnchanged(t, reopen(t), before)
			if mode == "inactive_actor" {
				f.actorRow = deletionAssign(t, backend, people.MemberObjects, f.actorRow, map[string]query.Value{"enabled": query.Boolean(true)})
			}
			if mode == "corrupt_dependent" {
				tree.child = deletionAssign(t, backend, people.MemberObjects, tree.child, map[string]query.Value{"version": query.Integer(1)})
			}
			if mode == "overflow_survivor" {
				tree.survivor = deletionAssign(t, backend, people.MemberObjects, tree.survivor, map[string]query.Value{"version": query.Integer(1)})
			}
			if mode == "protected_descendant" {
				if _, err := tickets.TicketObjects.Delete(t.Context(), backend, &guard); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	t.Run("implicit_membership_needs_only_catalog_delete", func(t *testing.T) {
		deletionActorGrants(t, backend, f, "catalog.delete_group")
		group, err := access.TeamObjects.Create(t.Context(), backend, access.NewTeamCreate("membership authority"))
		if err != nil {
			t.Fatal(err)
		}
		member := deletionMember(t, backend, f, "implicitowner", nil)
		if _, err := links.MembershipObjects.Create(t.Context(), backend, links.NewMembershipCreate(member.ID, group.ID)); err != nil {
			t.Fatal(err)
		}
		record := deletionSession(t, f.manager, member.Subject)
		result, err := deletionCatalog(t, f.store).Groups().Delete(t.Context(), f.actor, group.ID, 1)
		if err != nil || result.RevokedSessions != 0 {
			t.Fatal("implicit membership required an unrelated account grant", err)
		}
		actual, err := people.MemberObjects.Using(reopen(t)).Filter(people.MemberFields.ID.Exact(member.ID)).Get(t.Context())
		if err != nil || actual.Version != 2 {
			t.Fatal("implicit membership revision missing", err)
		}
		accountSessionRecord(t, f.store, record, true)
		if len(deletionHistory(t, backend, f.actor, "people.member", member.ID)) != 0 {
			t.Fatal("implicit membership created a direct audit")
		}
	})
	t.Run("old_domain", func(t *testing.T) {
		deletionActorGrants(t, backend, f, all...)
		fingerprint, err := f.binding.RoleFingerprint()
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte("deletion domain successor"))
		change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(digest[:]))
		if err != nil {
			t.Fatal(err)
		}
		if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
			_, err := domain.TransitionIn(t.Context(), scope, f.owner, change, joined)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		before := deletionSnapshot(t, reopen(t))
		result, err := deletionOwner(t, backend).Delete(t.Context(), f.actor, tree.root.ID, 1)
		if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || result != (accountmodel.AccountDeletion{}) {
			t.Fatal("old domain deleted matching new-domain rows", err)
		}
		requireDeletionUnchanged(t, reopen(t), before)
	})
}
