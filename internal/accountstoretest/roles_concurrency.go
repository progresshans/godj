package accountstoretest

import (
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func roleConcurrentRevision(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	target := roleTarget(t, backend, f, "role-concurrent")
	now := joined
	store, _, _ := accountSessions(t, backend, &now)
	other, _, _ := accountSessions(t, reopen(t), &now)
	editors := []*accountmodel.RoleEditor[models.User]{roleEditor(t, store), roleEditor(t, other)}
	start := make(chan struct{})
	done := make(chan error, 2)
	for i, field := range []string{"staff", "superuser"} {
		input := roleInput(t, map[string]query.Value{field: query.Boolean(true)}, nil)
		go func() { <-start; _, err := editors[i].Update(t.Context(), actor, target.ID, 1, input); done <- err }()
	}
	close(start)
	success, conflicts := 0, 0
	for range 2 {
		err := <-done
		if err == nil {
			success++
		} else if errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	row := profileRow(t, reopen(t), target.ID)
	if success != 1 || conflicts != 1 || row.Revision != 2 || row.Staff == row.Superuser || len(profileHistory(t, backend, actor, target.ID)) != 1 {
		t.Fatal("same revision role writes did not have one winner")
	}
}

func roleConcurrentCatalogExpansion(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	target := roleTarget(t, backend, f.account, "role-expansion")
	var keys []int64
	var group models.Group
	var extra models.Permission
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for i := range 256 {
			p, err := models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate(fmt.Sprintf("race.role%03d", i), "Concurrent role grant"))
			if err != nil {
				return err
			}
			keys = append(keys, p.ID)
			if i < 255 {
				if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), scope, models.NewUserPermissionsLinkCreate(target.ID, p.ID)); err != nil {
					return err
				}
			}
		}
		var err error
		extra, err = models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate("race.catalog_extra", "Concurrent group grant"))
		if err != nil {
			return err
		}
		group, err = models.GroupObjects.Create(t.Context(), scope, models.NewGroupCreate("concurrent membership owner"))
		if err != nil {
			return err
		}
		_, err = models.UserGroupsLinkObjects.Create(t.Context(), scope, models.NewUserGroupsLinkCreate(target.ID, group.ID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	now := joined
	store, _, _ := accountSessions(t, reopen(t), &now)
	editor := roleEditor(t, store)
	roleInput := roleInput(t, nil, map[string][]int64{"permissions": keys})
	groupInput := catalogGroupInput(t, nil, map[string][]int64{"permissions": {extra.ID}})
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() { <-start; _, err := editor.Update(t.Context(), actor, target.ID, 1, roleInput); done <- err }()
	go func() {
		<-start
		_, err := f.catalog.Groups().Update(t.Context(), actor, group.ID, 1, groupInput)
		done <- err
	}()
	close(start)
	success, rejected := 0, 0
	for range 2 {
		err := <-done
		if err == nil {
			success++
		} else if _, invalid := validation.Rejected(err); invalid {
			rejected++
		} else {
			t.Fatal("unexpected concurrent owner failure", err)
		}
	}
	credential, present, err := f.account.store.CredentialByID(t.Context(), target.PrincipalID)
	if err != nil || !present || success != 1 || rejected != 1 || len(credential.Principal().Permissions()) != 256 {
		t.Fatal("independent role/catalog owners exceeded effective permission bound", err)
	}
}

func roleDeactivationVersusLogin(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	target := roleTarget(t, backend, f, "role-login-race")
	credential, present, err := f.store.CredentialByID(t.Context(), target.PrincipalID)
	if err != nil || !present {
		t.Fatal(err)
	}
	now := joined
	store, manager, _ := accountSessions(t, backend, &now)
	_, _, login := accountSessions(t, reopen(t), &now)
	editor := roleEditor(t, store)
	previous, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: target.PrincipalID})
	if err != nil {
		t.Fatal(err)
	}
	input := roleInput(t, map[string]query.Value{"active": query.Boolean(false)}, nil)
	start := make(chan struct{})
	roleDone := make(chan error, 1)
	loginDone := make(chan error, 1)
	go func() {
		<-start
		result, err := editor.Update(t.Context(), actor, target.ID, 1, input)
		if err == nil && (result.Revision != 2 || result.RevokedSessions != 1) {
			err = errors.New("deactivation did not revoke exactly current target session")
		}
		roleDone <- err
	}()
	go func() {
		<-start
		_, err := login.Login(t.Context(), selectedSessionLogin(credential, previous, now))
		loginDone <- err
	}()
	close(start)
	if err := <-roleDone; err != nil {
		t.Fatal(err)
	}
	if err := <-loginDone; err != nil && !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatal("concurrent login failed outside credential recheck", err)
	}
	if row := profileRow(t, reopen(t), target.ID); row.Active || row.Revision != 2 {
		t.Fatal("login resurrected deactivated account")
	}
	if count := accountSessionCount(t, reopen(t)); count != 0 {
		t.Fatal("login left a live session after deactivation", count)
	}
	accountSessionRecord(t, store, previous, false)
}
