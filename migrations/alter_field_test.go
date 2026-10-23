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

func TestChoiceAlterFieldHistoricalForwardBackwardAndPreimage(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "choices", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{
		schema.CharField("status", "Status", 12, schema.Default("open")),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := NewProjectState(definition)
	if err != nil {
		t.Fatal(err)
	}
	before := definition.Models[0].Fields[1]
	after := before.Clone()
	after.Choices = []ir.Choice{schema.Choice("open", "Open"), schema.Choice("closed", "Closed")}
	op := AlterField{AppLabel: "choices", ModelName: "entry", Before: before, After: after}
	forward, err := op.stateForward(base)
	if err != nil {
		t.Fatal(err)
	}
	if forward.Equal(base) {
		t.Fatal("metadata change disappeared")
	}
	restored, err := op.stateBackward(forward)
	if err != nil || !restored.Equal(base) {
		t.Fatalf("reverse failed to restore exact field: %v", err)
	}
	if _, err := op.stateForward(forward); err == nil {
		t.Fatal("stale before-field accepted")
	}
	if _, err := op.stateBackward(base); err == nil {
		t.Fatal("stale reverse preimage accepted")
	}
	for _, mutate := range []func(*ir.Field){
		func(f *ir.Field) { f.MaxLength++ }, func(f *ir.Field) { f.Nullable = true },
		func(f *ir.Field) { f.Column = "other" }, func(f *ir.Field) { f.GoName = "Other" },
		func(f *ir.Field) { f.Default.String = "closed" },
	} {
		bad := op
		bad.After = after.Clone()
		mutate(&bad.After)
		if state, err := bad.stateForward(base); err == nil || !state.Equal(base) {
			t.Fatal("physical or other metadata change was accepted or damaged the state")
		}
	}
	second := after.Clone()
	second.Choices[0], second.Choices[1] = second.Choices[1], second.Choices[0]
	second.Choices[1].Label = "Open again"
	definitions := []Migration{
		{App: "choices", Name: "0001_initial", Operations: []Operation{CreateModel{AppLabel: "choices", Model: definition.Models[0]}}},
		{App: "choices", Name: "0002_choices", Dependencies: []MigrationKey{{App: "choices", Name: "0001_initial"}}, Operations: []Operation{op}},
		{App: "choices", Name: "0003_labels", Dependencies: []MigrationKey{{App: "choices", Name: "0002_choices"}}, Operations: []Operation{AlterField{AppLabel: "choices", ModelName: "entry", Before: after, After: second}}},
	}
	reconstructor := mustStateReconstructor(t, definitions...)
	after.Choices[0].Label = "caller mutation"
	latest := reconstructState(t, reconstructor, LatestStateRequest())
	model, found := latest.Model("choices", "entry")
	if !found || !model.Fields[1].Equal(second) {
		t.Fatal("loaded history did not own ordered choice metadata")
	}
	previous := reconstructState(t, reconstructor, BeforeStateRequest(definitions[2].Key()))
	if !previous.Equal(forward) {
		t.Fatal("dependency-before reconstruction lost the old choice labels")
	}
}

func TestChoiceAlterRequiresCapabilityBeforeAnyTransaction(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "choices", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.CharField("status", "Status", 12)}}}})
	if err != nil {
		t.Fatal(err)
	}
	before := s.Models[0].Fields[1]
	after := before.Clone()
	after.Choices = []ir.Choice{schema.Choice("open", "Open")}
	initial := Migration{App: "choices", Name: "0001_initial", Operations: []Operation{CreateModel{AppLabel: "choices", Model: s.Models[0]}}}
	change := Migration{App: "choices", Name: "0002_choices", Dependencies: []MigrationKey{initial.Key()}, Operations: []Operation{AlterField{AppLabel: "choices", ModelName: "entry", Before: before, After: after}}}
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
		if !errors.As(err, &capability) || !strings.Contains(capability.Detail, "AlterFieldChoices") {
			t.Fatalf("missing choices capability: %v", err)
		}
		if fake.capabilityCount != 1 || session.beginCount != 0 || fake.atomicBeginCount != 0 || session.closeCount != 1 {
			t.Fatal("unsupported metadata change started or leaked a transaction")
		}
	}
}
