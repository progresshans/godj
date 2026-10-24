package consumer

import (
	"example.com/godj-project-bundle/models"
	"example.com/godj-project-bundle/project"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"testing"
)

func TestGeneratedFirstAccountBootstrap(t *testing.T) {
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	role, err := binding.Role()
	if err != nil {
		t.Fatal(err)
	}
	backend := accountDatabase(t, models.GoDjRelationSchema())
	hasher, _ := accountPassword(t)
	owner, err := project.NewFirstAccount(backend, hasher, firstConsumerConfig[models.User](role, "first_name"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.NewAccountCredentialStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	row := assertFirstAccountConsumer(t, backend, binding, owner, hasher, store, sessions, "first_name")
	if row.FirstName != "NZ" || !row.Staff || !row.Superuser {
		t.Fatal("first default typed fields lost")
	}
}
