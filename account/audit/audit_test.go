package audit_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/audit/modeldef"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

func TestAccountAuditDefinitionAndGeneratedBinding(t *testing.T) {
	selected, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	sources := audit.MigrationSources()
	loaded, _, err := definition.Load(sources...)
	if err != nil || len(loaded.Definitions()) != 1 {
		t.Fatal("audit migration graph", err)
	}
	initial := loaded.Definitions()[0]
	if initial.App != modeldef.AppLabel || initial.Name != audit.InitialMigrationKey().Name || len(initial.Dependencies) != 0 || len(initial.Operations) != 1 {
		t.Fatal("audit depends on an account implementation")
	}
	create, ok := initial.Operations[0].(migrations.CreateModel)
	if !ok || !reflect.DeepEqual(create.Model, selected.Models[0]) {
		t.Fatal("migration does not implement the declared audit model")
	}
	sources[0].Document[0] = '!'
	if audit.MigrationSources()[0].Document[0] != '{' {
		t.Fatal("historical migration is mutable")
	}
	spec, err := modeldef.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if spec.Account != nil || len(spec.Apps) != 1 {
		t.Fatal("audit selected a hidden account")
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	report, err := projectgenerate.Check(t.Context(), ".", bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("audit generated drift: %+v, %v", report, err)
	}
}

func TestAccountAuditRejectsUnboundActorsInvalidEventsAndMissingScopes(t *testing.T) {
	domain, err := auth.NewCredentialDomain(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "operator", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	marker, err := auth.MakeUnusablePassword(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredentialForDomain(domain, "operator", marker, principal)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 24, 0, 0, 1, 0, time.UTC)
	for _, actor := range []auth.Principal{{}, principal} {
		if _, err := audit.Prepare(actor, "members.account", 0, admin.ActionChange, nil, at); !errors.Is(err, audit.ErrInvalid) {
			t.Fatal("unbound actor accepted", err)
		}
	}
	for _, bad := range []time.Time{{}, time.Date(1, 1, 1, 0, 0, 0, 1, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := audit.Prepare(credential.Principal(), "members.account", 0, admin.ActionChange, nil, bad); !errors.Is(err, audit.ErrInvalid) {
			t.Fatal("invalid normalized time accepted", err)
		}
	}
	if _, err := audit.Prepare(credential.Principal(), "members/account", 0, admin.ActionChange, nil, at); !errors.Is(err, audit.ErrInvalid) {
		t.Fatal("invalid model accepted", err)
	}
	if _, err := audit.Prepare(credential.Principal(), "members.account", 0, admin.ActionChange, []string{"profile", "profile"}, at); !errors.Is(err, audit.ErrInvalid) {
		t.Fatal("invalid changed fields accepted", err)
	}
	for _, id := range []int64{0, -17, 1} {
		event, err := audit.Prepare(credential.Principal(), "members.account", id, admin.ActionChange, []string{"profile"}, at)
		if err != nil {
			t.Fatal(err)
		}
		if entry, err := audit.AppendIn(t.Context(), nil, event); !errors.Is(err, audit.ErrInvalid) || entry.Sequence() != 0 {
			t.Fatal("missing scope accepted", err)
		}
	}
	if entries, err := audit.HistoryIn(t.Context(), nil, domain, "members.account", 0, 1); !errors.Is(err, audit.ErrInvalid) || entries != nil {
		t.Fatal("missing history scope accepted", err)
	}
}
