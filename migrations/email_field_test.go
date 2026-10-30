package migrations

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestEmailAlterRequiresSpecificCapabilityBeforeAnyTransaction(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "contacts", Models: []schema.Model{{Name: "contact", GoName: "Contact", Fields: []schema.Field{schema.CharField("address", "Address", 254)}}}})
	if err != nil {
		t.Fatal(err)
	}
	before := s.Models[0].Fields[1]
	after := before.Clone()
	after.Kind = ir.FieldEmail
	initial := Migration{App: "contacts", Name: "0001_initial", Operations: []Operation{CreateModel{AppLabel: "contacts", Model: s.Models[0]}}}
	change := Migration{App: "contacts", Name: "0002_email", Dependencies: []MigrationKey{initial.Key()}, Operations: []Operation{AlterField{AppLabel: "contacts", ModelName: "contact", Before: before, After: after}}}
	loaded := testLoadedDefinitionSet(t, []Migration{initial, change})
	for _, reverse := range []bool{false, true} {
		var records []backend.AppliedMigration
		request := LatestLifecycleRequest()
		if reverse {
			records = lifecycleRecords(initial.Key(), change.Key())
			request = TargetedLifecycleRequest(NamedTarget(initial.Key()))
		}
		session := newLifecycleTestSession(records, nil)
		fake := newLifecycleTestBackend(session)
		_, err := (Executor{Backend: fake}).Migrate(context.Background(), loaded, request)
		var capability *backend.CapabilityError
		if !errors.As(err, &capability) || !strings.Contains(capability.Detail, "AlterFieldStringSemantics") {
			t.Fatalf("missing email alteration capability: %v", err)
		}
		if fake.capabilityCount != 1 || session.beginCount != 0 || fake.atomicBeginCount != 0 || session.closeCount != 1 {
			t.Fatal("unsupported email metadata change started or leaked a transaction")
		}
	}
}
