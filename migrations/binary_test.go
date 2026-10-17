package migrations

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestBinaryInputPolicyRequiresCapabilityBeforeTransaction(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "binarycaps", Models: []schema.Model{{Name: "packet", GoName: "Packet", Fields: []schema.Field{schema.BinaryField("payload", "Payload", schema.MaxLength(4), schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := s.Models[0]
	before := model.Fields[1]
	for _, policy := range []string{"editable", "length"} {
		t.Run(policy, func(t *testing.T) {
			after := before.Clone()
			wantKind := ir.ChangeEditable
			if policy == "editable" {
				after.NonEditable = false
			} else {
				after.MaxLength = 8
				wantKind = ir.ChangeBinaryLength
			}
			if kind, err := ir.ClassifyFieldChange(before, after); err != nil || kind != wantKind {
				t.Fatal(kind, err)
			}
			initial := Migration{App: s.AppLabel, Name: "0001_initial", Operations: []Operation{CreateModel{AppLabel: s.AppLabel, Model: model}}}
			change := Migration{App: s.AppLabel, Name: "0002_policy", Dependencies: []MigrationKey{initial.Key()}, Operations: []Operation{AlterField{AppLabel: s.AppLabel, ModelName: model.Name, Before: before, After: after}}}
			loaded := testLoadedDefinitionSet(t, []Migration{initial, change})
			for _, reverse := range []bool{false, true} {
				records, request := lifecycleRecords(initial.Key()), LatestLifecycleRequest()
				if reverse {
					records = lifecycleRecords(initial.Key(), change.Key())
					request = TargetedLifecycleRequest(NamedTarget(initial.Key()))
				}
				session := newLifecycleTestSession(records, nil)
				fake := newLifecycleTestBackend(session)
				fake.capabilities = lifecycleAllRelationCapabilities()
				fake.capabilities.AlterFieldInputPolicy = false
				_, err := (Executor{Backend: fake}).Migrate(t.Context(), loaded, request)
				var capability *backend.CapabilityError
				if !errors.As(err, &capability) || !strings.Contains(capability.Detail, "AlterFieldInputPolicy") {
					t.Fatal("missing input-policy capability", err)
				}
				if session.beginCount != 0 || fake.atomicBeginCount != 0 || session.closeCount != 1 {
					t.Fatal("unsupported policy started or leaked a transaction")
				}
			}
		})
	}
}
