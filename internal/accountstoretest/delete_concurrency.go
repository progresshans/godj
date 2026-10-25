package accountstoretest

import (
	"errors"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

func accountDeletionRevisionRace(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	target := deletionMember(t, backend, f, "revision-race", nil)
	store, err := project.NewAccountSessionStore(reopen(t), sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	editor, err := project.NewAccountEditor(store, auth.PrincipalAuthorizer{}, accountmodel.AccountEditConfig[people.Member]{Fields: []string{"region"}, ChangePermission: "account.change"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := people.MemberObjects.Input(map[string]query.Value{"region": query.String("JP")})
	if err != nil {
		t.Fatal(err)
	}
	deleter := deletionOwner(t, backend)
	start := make(chan struct{})
	deleted, edited := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		result, err := deleter.Delete(t.Context(), f.actor, target.ID, 1)
		if err == nil && (result.ID != target.ID || result.DeletedRows != 1) {
			err = errors.New("revision race deletion result mismatch")
		}
		deleted <- err
	}()
	go func() {
		<-start
		result, err := editor.Update(t.Context(), f.actor, target.ID, 1, input)
		if err == nil && result.Revision != 2 {
			err = errors.New("revision race edit result mismatch")
		}
		edited <- err
	}()
	close(start)
	deletionErr, editErr := <-deleted, <-edited
	if (deletionErr == nil) == (editErr == nil) {
		t.Fatal("delete/edit with one revision did not have exactly one winner", deletionErr, editErr)
	}
	if deletionErr != nil && !errors.Is(deletionErr, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
		t.Fatal("delete loser did not observe changed revision", deletionErr)
	}
	if editErr != nil && !errors.Is(editErr, &accountmodel.Error{Code: accountmodel.CodeNotFound}) {
		t.Fatal("edit loser did not observe deletion", editErr)
	}
	row, found, err := people.MemberObjects.Using(reopen(t)).Filter(people.MemberFields.ID.Exact(target.ID)).OrderBy(people.MemberFields.ID.Asc()).First(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if deletionErr == nil && found || editErr == nil && (!found || row.Version != 2 || row.Region != "JP") {
		t.Fatal("durable revision race state disagrees with winning result")
	}
	if len(deletionHistory(t, backend, f.actor, "people.member", target.ID)) != 1 {
		t.Fatal("revision race appended multiple lifecycle audits")
	}
}

func accountDeletionLoginRace(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	target := deletionMember(t, backend, f, "login-race", nil)
	credential, found, err := f.credentials.CredentialByID(t.Context(), target.Subject)
	if err != nil || !found {
		t.Fatal(err)
	}
	previous := deletionSession(t, f.manager, target.Subject)
	otherStore, err := project.NewAccountSessionStore(reopen(t), sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(otherStore, sessions.Config{Clock: func() time.Time { return joined }})
	if err != nil {
		t.Fatal(err)
	}
	login, err := otherStore.LoginPersistence(manager)
	if err != nil {
		t.Fatal(err)
	}
	deleter := deletionOwner(t, backend)
	start := make(chan struct{})
	deleted, loggedIn := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		result, err := deleter.Delete(t.Context(), f.actor, target.ID, 1)
		if err == nil && result.RevokedSessions != 1 {
			err = errors.New("concurrent login escaped target revocation")
		}
		deleted <- err
	}()
	go func() {
		<-start
		_, err := login.Login(t.Context(), selectedSessionLogin(credential, previous, joined))
		loggedIn <- err
	}()
	close(start)
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if err := <-loggedIn; err != nil && !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatal("concurrent login failed outside current credential recheck", err)
	}
	if count, err := people.MemberObjects.Using(reopen(t)).Filter(people.MemberFields.ID.Exact(target.ID)).Count(t.Context()); err != nil || count != 0 {
		t.Fatal("concurrent login retained deleted account", err)
	}
	if count := accountSessionCount(t, reopen(t)); count != 0 {
		t.Fatal("concurrent login left a session after account deletion", count)
	}
	accountSessionRecord(t, f.store, previous, false)
}
