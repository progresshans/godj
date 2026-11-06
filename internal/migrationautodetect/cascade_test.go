package migrationautodetect

import (
	"bytes"
	"slices"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectCascadePolicyAlterationAndReplay(t *testing.T) {
	field := testForeignKey("owner", true, "accounts", "owner", "children")
	state := func(field ir.Field) migrations.ProjectState {
		return mustProjectState(t, testSchema("accounts", testModel("owner", testChar("name", false, nil))), testSchema("links", testModel("link", field)))
	}
	initial := state(field)
	model, _ := initial.Model("links", "link")
	field = model.Fields[1].Clone()
	history := mustDetect(t, Request{Definitions: mustLoadDefinitions(t), Desired: initial, ManagedApps: initial.Apps()}).Migrations()
	for _, policy := range []ir.DeletePolicy{ir.DeleteCascade, ir.DeleteProtect, ir.DeleteSetNull, ir.DeleteCascade} {
		before := field.Clone()
		field.Relation.OnDelete = policy
		desired := state(field)
		loaded := mustLoadDefinitions(t, history...)
		request := Request{Definitions: loaded, Desired: desired, ManagedApps: desired.Apps()}
		changes := mustDetect(t, request).Migrations()
		if len(changes) != 1 || len(changes[0].Operations) != 1 {
			t.Fatal("policy change did not produce exactly one alteration")
		}
		alter, ok := changes[0].Operations[0].(migrations.AlterField)
		if !ok || !alter.Before.Equal(before) || !alter.After.Equal(field) {
			t.Fatal("policy alteration lost exact historical before/after")
		}
		assertGeneratedState(t, loaded, changes, desired)
		history = append(history, changes...)
		request.Definitions = mustLoadDefinitions(t, history...)
		if !mustDetect(t, request).Empty() {
			t.Fatal("serialized policy alteration did not become a no-op")
		}
	}
}

func TestDetectCascadeRequiredCycleRetainsEveryDurablePrefix(t *testing.T) {
	fk := func(app string) ir.Field {
		field := testForeignKey("peer", false, app, "node", "incoming")
		field.Relation.OnDelete = ir.DeleteCascade
		return field
	}
	desired := mustProjectState(t,
		testSchema("alpha", testModel("node", fk("beta"))),
		testSchema("beta", testModel("node", fk("alpha"))))
	request := Request{Definitions: mustLoadDefinitions(t), Desired: desired, ManagedApps: desired.Apps()}
	plan := mustDetect(t, request).Migrations()
	if len(plan) < 2 {
		t.Fatal("cycle test did not create an interruptible multi-migration plan")
	}
	assertGeneratedState(t, request.Definitions, plan, desired)
	for prefix := 0; prefix <= len(plan); prefix++ {
		history := cloneMigrations(plan[:prefix])
		slices.Reverse(history)
		request.Definitions = mustLoadDefinitions(t, history...)
		remaining := mustDetect(t, request).Migrations()
		if len(remaining) != len(plan)-prefix {
			t.Fatalf("prefix %d changed remaining work", prefix)
		}
		for index, migration := range remaining {
			actual, err := definition.Encode(testProducer, migration)
			if err != nil {
				t.Fatal(err)
			}
			want, err := definition.Encode(testProducer, plan[prefix+index])
			if err != nil || !bytes.Equal(actual, want) {
				t.Fatalf("prefix %d candidate %d changed exact bytes: %v", prefix, index, err)
			}
		}
		assertGeneratedState(t, request.Definitions, remaining, desired)
	}
}
