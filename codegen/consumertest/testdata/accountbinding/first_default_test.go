package consumer

import (
	"example.com/godj-project-bundle/models"
	"example.com/godj-project-bundle/project"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"testing"
)

func TestGeneratedFirstAccountBootstrap(t *testing.T) {
	for _, test := range []struct {
		name    string
		profile firstProfileInput
	}{
		{"trimmed", firstProfileInput{present: true, text: "  NZ  ", expected: "NZ"}},
		{"omitted_default", firstProfileInput{expected: "NZ"}},
		{"explicit_empty", firstProfileInput{present: true, expected: ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			firstDefaultAccount(t, test.profile, false)
		})
	}
}

func TestGeneratedAdminAccountMutation(t *testing.T) {
	firstDefaultAccount(t, firstProfileInput{present: true, text: "NZ", expected: "NZ"}, true)
}

func firstDefaultAccount(t *testing.T, profile firstProfileInput, editOwnAccount bool) {
	t.Helper()
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	role, err := binding.Role()
	if err != nil {
		t.Fatal(err)
	}
	backend, selected := accountDatabaseWithConfig(t, models.GoDjRelationSchema())
	hasher, _ := accountPassword(t)
	definition, err := project.NewFirstAccountDefinition(hasher, firstConsumerConfig[models.User](role, "first_name"))
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
	row := assertFirstAccountConsumer(t, backend, selected, binding, definition, hasher, store, sessions, "first_name", profile)
	if row.FirstName != profile.expected || !row.Staff || !row.Superuser {
		t.Fatal("first default typed fields lost")
	}
	if editOwnAccount {
		assertAccountAdminMutationHTTP(t, backend, binding, store, sessions, hasher, row, "first_name", project.NewAccountEditor)
		return
	}
	t.Run("admin_catalog", func(t *testing.T) {
		assertCatalogAdminHTTP(t, backend, binding, sessions, hasher, row, "", "", project.NewAccountCatalog)
	})
	t.Run("admin_password", func(t *testing.T) {
		assertAccountAdminPasswordHTTP(t, backend, binding, store, sessions, hasher, row, "first_name", project.NewAccountPasswordEditor)
	})
}
