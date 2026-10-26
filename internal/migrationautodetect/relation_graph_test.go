package migrationautodetect

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectSelfRelationCreateAndNullableAdd(t *testing.T) {
	for _, test := range []struct {
		name     string
		existing bool
		nullable bool
	}{
		{"nullable self Create", false, true},
		{"required self Create", false, false},
		{"nullable self Add", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			desired := mustProjectState(t, testSchema("content", testModel("article",
				testChar("title", false, nil), testForeignKey("parent", test.nullable, "content", "article", "children"))))
			var history []migrations.Migration
			if test.existing {
				before := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil))))
				history = initialMigrationsFromState(t, before)
			}
			request := Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}}
			plan := mustDetect(t, request)
			candidate := plan.Migrations()
			if len(candidate) != 1 || len(candidate[0].Operations) != 1 {
				t.Fatalf("self candidate inventory: %#v", candidate)
			}
			if test.existing {
				if _, ok := candidate[0].Operations[0].(migrations.AddField); !ok ||
					!reflect.DeepEqual(candidate[0].Dependencies, []migrations.MigrationKey{history[0].Key()}) {
					t.Fatal("self Add did not retain its historical dependency")
				}
			} else if _, ok := candidate[0].Operations[0].(migrations.CreateModel); !ok || len(candidate[0].Dependencies) != 0 {
				t.Fatal("self Create added a circular migration dependency")
			}
			if repeated := mustDetect(t, request).Migrations(); !reflect.DeepEqual(repeated, candidate) {
				t.Fatal("self detection is not deterministic")
			}
			loaded := mustLoadDefinitions(t, append(history, candidate...)...)
			reconstructor, err := loaded.Reconstructor(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
			if err != nil || !state.Equal(desired) {
				t.Fatalf("serialized self candidate changed desired state: %v", err)
			}
			if again := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}}); !again.Empty() {
				t.Fatal("self candidate reload did not become a clean no-op")
			}
		})
	}
}

func TestDetectRelationCyclesPreserveDeclaredOrderAndEveryPublicationPrefix(t *testing.T) {
	key := ir.Field{Name: "key", GoName: "Key", Kind: ir.FieldAuto, PrimaryKey: true}
	label := testChar("label", false, nil)
	fk := func(name, app, target string, nullable bool) ir.Field {
		field := testForeignKey(name, nullable, app, target, "")
		field.Relation.Reverse = ir.ReverseRelation{Disabled: true}
		return field
	}
	for _, test := range []struct {
		name           string
		schemas        []ir.Schema
		wantCandidates int
	}{
		{"same app later and mutual", []ir.Schema{testSchema("graph", testModel("a", fk("peer", "graph", "b", false), label, key, fk("parent", "graph", "a", true)), testModel("b", fk("owner", "graph", "a", true), label))}, 1},
		{"cross app same model name and nonleading key", []ir.Schema{
			testSchema("alpha", testModel("item", fk("peer", "beta", "item", false), label, key)),
			testSchema("beta", testModel("item", fk("peer", "alpha", "item", true), label)),
		}, 3},
		{"three app cycle with incoming chain", []ir.Schema{
			testSchema("aardvark", testModel("item", fk("peer", "alpha", "item", true), label)),
			testSchema("alpha", testModel("item", fk("peer", "beta", "item", true), label, key)),
			testSchema("beta", testModel("item", fk("peer", "gamma", "item", false), label)),
			testSchema("gamma", testModel("item", fk("peer", "alpha", "item", false), label)),
		}, 5},
		{"app cycle with acyclic models", []ir.Schema{
			testSchema("alpha", testModel("first", fk("peer", "beta", "last", true), label), testModel("last", label)),
			testSchema("beta", testModel("first", fk("peer", "alpha", "last", true), label), testModel("last", label)),
		}, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			desired := mustProjectState(t, test.schemas...)
			request := Request{Definitions: mustLoadDefinitions(t), Desired: desired, ManagedApps: desired.Apps()}
			plan := mustDetect(t, request).Migrations()
			if len(plan) != test.wantCandidates {
				t.Fatalf("candidate count %d, want %d: %#v", len(plan), test.wantCandidates, plan)
			}
			assertGeneratedState(t, request.Definitions, plan, desired)
			for prefix := 0; prefix <= len(plan); prefix++ {
				history := cloneMigrations(plan[:prefix])
				slices.Reverse(history)
				request.Definitions = mustLoadDefinitions(t, history...)
				remaining := mustDetect(t, request).Migrations()
				if len(remaining) != len(plan)-prefix {
					t.Fatalf("prefix %d changed remaining candidate count", prefix)
				}
				for index, migration := range remaining {
					actual, err := definition.Encode(testProducer, migration)
					if err != nil {
						t.Fatal(err)
					}
					want, err := definition.Encode(testProducer, plan[prefix+index])
					if err != nil {
						t.Fatal(err)
					}
					if string(actual) != string(want) {
						t.Fatalf("prefix %d candidate %d changed bytes after restart\n%s\n%s", prefix, index, actual, want)
					}
				}
			}
		})
	}
}

func TestDetectInsertsNewFieldsBetweenRetainedFieldsWithoutReordering(t *testing.T) {
	history := mustProjectState(t, testSchema("graph", testModel("node", testChar("label", false, nil), testChar("tail", false, nil))))
	desired := mustProjectState(t, testSchema("graph", testModel("node", testChar("label", false, nil),
		testForeignKey("parent", true, "graph", "node", "children"), testChar("note", true, nil), testChar("tail", false, nil))))
	loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, history)...)
	plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"graph"}}).Migrations()
	if len(plan) != 1 || len(plan[0].Operations) != 2 {
		t.Fatal("middle insertion candidate inventory")
	}
	for _, operation := range plan[0].Operations {
		if operation.(migrations.AddField).BeforeField != "tail" {
			t.Fatal("insertion anchor names a future field")
		}
	}
	assertGeneratedState(t, loaded, plan, desired)
}

func TestDetectCandidateLimitIncludesCycleSuccessors(t *testing.T) {
	var schemas []ir.Schema
	for index := 0; index < MaxCandidates; index++ {
		app, next := fmt.Sprintf("a%03d", index), fmt.Sprintf("a%03d", (index+1)%MaxCandidates)
		field := testForeignKey("next", true, next, "node", "")
		field.Relation.Reverse = ir.ReverseRelation{Disabled: true}
		schemas = append(schemas, testSchema(app, testModel("node", field)))
	}
	desired := mustProjectState(t, schemas...)
	detectError(t, Request{Definitions: mustLoadDefinitions(t), Desired: desired, ManagedApps: desired.Apps()}, CodeCandidateResourceLimit)
}

// A created through model can still lack its deferred endpoint columns. The
// owning M2M declaration must wait for those columns, while another model in
// the same dependency cycle can make the next publication prefix progress.
func TestDetectExplicitThroughCyclesResumeEveryPublicationPrefix(t *testing.T) {
	for _, names := range [][3]string{{"access", "links", "people"}, {"access", "people", "links"}, {"links", "access", "people"}, {"links", "people", "access"}, {"people", "access", "links"}, {"people", "links", "access"}} {
		t.Run(names[0]+"_"+names[1]+"_"+names[2], func(t *testing.T) {
			catalog, through, accounts := names[0], names[1], names[2]
			fk := func(name, app, model string) ir.Field {
				field := testForeignKey(name, true, app, model, "")
				field.Relation.Reverse = ir.ReverseRelation{Disabled: true}
				return field
			}
			member := testModel("member", testChar("label", false, nil))
			member.ManyToMany = []ir.ManyToManyField{{Name: "teams", GoName: "Teams", Target: ir.ModelIdentity{AppLabel: catalog, ModelName: "team"}, Reverse: ir.ReverseRelation{Disabled: true}, Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: through, ModelName: "membership"}, SourceField: "member", TargetField: "team"}}}
			team := testModel("team", fk("creator", accounts, "member"))
			team.ManyToMany = []ir.ManyToManyField{{Name: "grants", GoName: "Grants", Target: ir.ModelIdentity{AppLabel: catalog, ModelName: "grant"}, Reverse: ir.ReverseRelation{Disabled: true}, Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: through, ModelName: "grant_link"}, SourceField: "team", TargetField: "grant"}}}
			desired := mustProjectState(t,
				testSchema(catalog, team, testModel("grant", fk("issuer", accounts, "member"))),
				testSchema(through, testModel("membership", fk("member", accounts, "member"), fk("team", catalog, "team")), testModel("grant_link", fk("team", catalog, "team"), fk("grant", catalog, "grant"))),
				testSchema(accounts, member))
			request := Request{Definitions: mustLoadDefinitions(t), Desired: desired, ManagedApps: desired.Apps()}
			plan := mustDetect(t, request).Migrations()
			assertGeneratedState(t, request.Definitions, plan, desired)
			for prefix := 0; prefix <= len(plan); prefix++ {
				history := cloneMigrations(plan[:prefix])
				slices.Reverse(history)
				request.Definitions = mustLoadDefinitions(t, history...)
				remaining := mustDetect(t, request).Migrations()
				if len(remaining) != len(plan)-prefix {
					t.Fatal("publication prefix changed remaining operation count", prefix)
				}
				for index, migration := range remaining {
					actual, err := definition.Encode(testProducer, migration)
					if err != nil {
						t.Fatal(err)
					}
					want, err := definition.Encode(testProducer, plan[prefix+index])
					if err != nil {
						t.Fatal(err)
					}
					if string(actual) != string(want) {
						t.Fatal("publication restart changed exact migration bytes", prefix, index)
					}
				}
			}
		})
	}
}
