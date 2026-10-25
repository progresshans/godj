package consumer

import (
	"context"
	"example.com/godj-project-bundle/access"
	"example.com/godj-project-bundle/people"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/tickets"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
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
	if role.Staff.Constant == nil || !*role.Staff.Constant {
		t.Fatal("positive custom fixture requires fixed staff=true")
	}
	backend, selected := accountDatabaseWithConfig(t, access.GoDjRelationSchema(), people.GoDjRelationSchema(), tickets.GoDjRelationSchema())
	hasher, _ := accountPassword(t)
	config := firstConsumerConfig[people.Member](role, "region")
	config.Validate = func(_ context.Context, _ db.Queryer, candidate people.Member, _ accountmodel.RoleMemberships) (validation.Errors, error) {
		if candidate.Region != "NZ" || !candidate.Enabled || !candidate.Root || candidate.Secret != "" {
			return validation.NewErrors(validation.New("region", "policy")), nil
		}
		return validation.Errors{}, nil
	}
	definition, err := project.NewFirstAccountDefinition(hasher, config)
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
	row := assertFirstAccountConsumer(t, backend, selected, binding, definition, hasher, store, sessions, "region", firstProfileInput{present: true, text: "NZ", expected: "NZ"})
	if row.Region != "NZ" || !row.Enabled || !row.Root || row.Contact != "first@example.test" {
		t.Fatal("first custom typed fields lost")
	}
	t.Run("admin_password", func(t *testing.T) {
		assertAccountAdminPasswordHTTP(t, backend, binding, store, sessions, hasher, row, "region", project.NewAccountPasswordEditor)
	})
}
