package migrations

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
)

func TestBlankChangesRequireSpecificCapabilityBeforeTransaction(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "blankcaps", Models: []schema.Model{{Name: "owner", GoName: "Owner", Fields: []schema.Field{schema.CharField("name", "Name", 30)}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("peers", "Peers", schema.Target("blankcaps", "owner"), schema.ReverseRelation{Disabled: true})}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := s.Models[0]
	before, after := model.Fields[1], model.Fields[1].Clone()
	after.Blank = true
	manyBefore, manyAfter := model.ManyToMany[0], model.ManyToMany[0].Clone()
	manyAfter.Blank = true
	initial := Migration{App: s.AppLabel, Name: "0001_initial", Operations: []Operation{CreateModel{AppLabel: s.AppLabel, Model: model}}}
	for _, operation := range []Operation{AlterField{AppLabel: s.AppLabel, ModelName: model.Name, Before: before, After: after}, AlterManyToMany{AppLabel: s.AppLabel, ModelName: model.Name, Before: manyBefore, After: manyAfter}} {
		t.Run(operation.Kind(), func(t *testing.T) {
			change := Migration{App: s.AppLabel, Name: "0002_blank", Dependencies: []MigrationKey{initial.Key()}, Operations: []Operation{operation}}
			loaded := testLoadedDefinitionSet(t, []Migration{initial, change})
			for _, reverse := range []bool{false, true} {
				records := lifecycleRecords(initial.Key())
				request := LatestLifecycleRequest()
				if reverse {
					records = lifecycleRecords(initial.Key(), change.Key())
					request = TargetedLifecycleRequest(NamedTarget(initial.Key()))
				}
				session := newLifecycleTestSession(records, nil)
				fake := newLifecycleTestBackend(session)
				fake.capabilities = lifecycleAllRelationCapabilities()
				fake.capabilities.AutomaticManyToMany = true
				fake.capabilities.AlterFieldBlank = false
				_, err := (Executor{Backend: fake}).Migrate(t.Context(), loaded, request)
				var capability *backend.CapabilityError
				if !errors.As(err, &capability) || !strings.Contains(capability.Detail, "AlterFieldBlank") {
					t.Fatalf("missing blank capability: %v", err)
				}
				if session.beginCount != 0 || fake.atomicBeginCount != 0 || session.closeCount != 1 {
					t.Fatal("unsupported policy change started or leaked a transaction")
				}
			}
		})
	}
}
