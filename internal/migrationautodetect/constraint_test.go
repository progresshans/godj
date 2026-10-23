package migrationautodetect

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

type constraintPlanObservation struct {
	Kind   string
	Name   string
	Fields []string
}

func TestDetectNamedConstraintsAgainstIndependentDjangoPlanningObservations(t *testing.T) {
	first := ir.UniqueConstraint{Name: "compound_first_second", Fields: []string{"first", "second"}}
	second := ir.UniqueConstraint{Name: "compound_first_third", Fields: []string{"first", "third"}}
	reordered := first.Clone()
	slices.Reverse(reordered.Fields)
	renamed := first.Clone()
	renamed.Name = "compound_renamed"
	replaced := first.Clone()
	replaced.Fields = second.Clone().Fields
	state := func(names []string, constraints ...ir.UniqueConstraint) migrations.ProjectState {
		var fields []ir.Field
		for _, name := range names {
			field := testChar(name, true, nil)
			field.Kind = ir.FieldInteger
			field.MaxLength = 0
			fields = append(fields, field)
		}
		model := testModel("changepair", fields...)
		model.UniqueConstraints = constraints
		return mustProjectState(t, testSchema("compound", model))
	}
	all := []string{"first", "second", "third"}
	tests := []struct {
		name          string
		before, after migrations.ProjectState
	}{
		{"add", state(all), state(all, first)},
		{"remove", state(all, first), state(all)},
		{"rename", state(all, first), state(all, renamed)},
		{"reorder_fields", state(all, first), state(all, reordered)},
		{"replace_member", state(all, first), state(all, replaced)},
		{"reorder_constraints", state(all, first, second), state(all, second, first)},
		{"add_field_and_constraint", state([]string{"first", "second"}), state(all, second)},
	}
	for _, database := range []string{"sqlite", "postgres"} {
		raw, err := os.ReadFile("../uniquetest/testdata/composite-django61-" + database + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			Autodetection map[string][]struct {
				Operations []struct {
					Kind       string
					Name       string
					Constraint *ir.UniqueConstraint
				}
			}
		}
		if err := json.Unmarshal(raw, &reference); err != nil || len(reference.Autodetection) != 10 {
			t.Fatal("missing independent planning inventory", err)
		}
		for _, test := range tests {
			t.Run(database+"/"+test.name, func(t *testing.T) {
				loaded := mustLoadDefinitions(t, initialMigrationsFromState(t, test.before)...)
				request := Request{Definitions: loaded, Desired: test.after, ManagedApps: []string{"compound"}}
				plan := mustDetect(t, request)
				changes := plan.Migrations()
				var actual, expected []constraintPlanObservation
				for _, migration := range changes {
					for _, operation := range migration.Operations {
						switch value := operation.(type) {
						case migrations.AddConstraint:
							actual = append(actual, constraintPlanObservation{Kind: value.Kind(), Name: value.Constraint.Name, Fields: value.Constraint.Fields})
						case migrations.RemoveConstraint:
							actual = append(actual, constraintPlanObservation{Kind: value.Kind(), Name: value.Constraint.Name})
						case migrations.AddField:
							actual = append(actual, constraintPlanObservation{Kind: value.Kind(), Name: value.Field.Name})
						default:
							t.Fatalf("unexpected named-constraint operation %T", operation)
						}
					}
				}
				observation, exists := reference.Autodetection[test.name]
				if !exists {
					t.Fatal("missing named reference case")
				}
				for _, migration := range observation {
					for _, operation := range migration.Operations {
						value := constraintPlanObservation{Kind: operation.Kind, Name: operation.Name}
						if operation.Constraint != nil {
							value.Name = operation.Constraint.Name
							value.Fields = operation.Constraint.Fields
						}
						expected = append(expected, value)
					}
				}
				if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("independent operation meaning/order differs: actual=%#v expected=%#v", actual, expected)
				}
				assertGeneratedState(t, loaded, changes, test.after)
				complete := mustLoadDefinitions(t, append(loaded.Definitions(), changes...)...)
				if !mustDetect(t, Request{Definitions: complete, Desired: test.after, ManagedApps: request.ManagedApps}).Empty() {
					t.Fatal("published named constraint did not become a no-op")
				}
				for _, migration := range changes {
					for _, operation := range migration.Operations {
						switch value := operation.(type) {
						case migrations.AddConstraint:
							value.Constraint.Fields[0] = "id"
						case migrations.RemoveConstraint:
							value.Constraint.Fields[0] = "id"
						}
					}
				}
				if !reflect.DeepEqual(plan.Migrations(), mustDetect(t, request).Migrations()) {
					t.Fatal("plan accessor exposes member storage or detection is nondeterministic")
				}
			})
		}
	}
	// Forward field removal remains outside the current operation domain. Do
	// not publish the preceding RemoveConstraint as a misleading partial plan.
	before := state(all, first)
	after := state([]string{"first", "third"})
	plan, err := Detect(Request{Definitions: mustLoadDefinitions(t, initialMigrationsFromState(t, before)...), Desired: after, ManagedApps: []string{"compound"}})
	var unsupported *Error
	if !errors.As(err, &unsupported) || unsupported.Code != CodeUnsupportedChange || !plan.Empty() {
		t.Fatal("unsupported field removal published a partial constraint plan", err)
	}
}

func TestDetectNamedConstraintsWaitForCyclicMembersAndPreserveEveryPublishedPrefix(t *testing.T) {
	for _, cross := range []bool{false, true} {
		t.Run(map[bool]string{false: "same_app", true: "cross_app"}[cross], func(t *testing.T) {
			left, right := "compound", "compound"
			if cross {
				right = "compound_peer"
			}
			model := func(name, targetApp, targetModel string) ir.Model {
				peer := testForeignKey("peer", true, targetApp, targetModel, "")
				peer.Relation.Reverse = ir.ReverseRelation{Disabled: true}
				result := testModel(name, peer, testCharLength("key", false, nil, 32))
				result.UniqueConstraints = []ir.UniqueConstraint{{Name: "compound_" + name + "_peer_key", Fields: []string{"peer", "key"}}, {Name: "local_key", Fields: []string{"key"}}}
				return result
			}
			schemas := []ir.Schema{testSchema(left, model("a", right, "b"), model("b", left, "a"))}
			if cross {
				schemas = []ir.Schema{testSchema(left, model("a", right, "b")), testSchema(right, model("b", left, "a"))}
			}
			desired := mustProjectState(t, schemas...)
			request := Request{Definitions: mustLoadDefinitions(t), Desired: desired, ManagedApps: desired.Apps()}
			plan := mustDetect(t, request).Migrations()
			wantCandidates := 1
			if cross {
				wantCandidates = 3
			}
			if len(plan) != wantCandidates {
				t.Fatal("cycle candidate count", len(plan))
			}
			var creates, adds, constraints int
			for _, migration := range plan {
				for _, operation := range migration.Operations {
					switch value := operation.(type) {
					case migrations.CreateModel:
						creates++
						fields := fieldNameSet(value.Model.Fields)
						for _, constraint := range value.Model.UniqueConstraints {
							if !constraintMembersPresent(constraint, fields) {
								t.Fatal("CreateModel constraint preceded a deferred member")
							}
						}
						if !fields["peer"] && (len(value.Model.UniqueConstraints) != 1 || value.Model.UniqueConstraints[0].Name != "local_key") {
							t.Fatal("deferral dropped independent inline constraints")
						}
					case migrations.AddField:
						adds++
					case migrations.AddConstraint:
						constraints++
					default:
						t.Fatalf("unexpected cycle operation %T", operation)
					}
				}
			}
			if creates != 2 || adds != 1 || constraints != 1 {
				t.Fatal("cycle lost or duplicated deferred work", creates, adds, constraints)
			}
			assertGeneratedState(t, request.Definitions, plan, desired)
			for prefix := 0; prefix <= len(plan); prefix++ {
				history := cloneMigrations(plan[:prefix])
				slices.Reverse(history)
				request.Definitions = mustLoadDefinitions(t, history...)
				remaining := mustDetect(t, request).Migrations()
				if len(remaining) != len(plan)-prefix {
					t.Fatal("prefix changed remaining work", prefix)
				}
				for index := range remaining {
					actual, err := definition.Encode(testProducer, remaining[index])
					if err != nil {
						t.Fatal(err)
					}
					want, err := definition.Encode(testProducer, plan[prefix+index])
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(actual, want) {
						t.Fatal("prefix restart changed exact migration bytes", prefix, index)
					}
				}
			}
		})
	}
}
