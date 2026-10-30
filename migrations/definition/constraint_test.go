package definition_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func constraintOperationsMigration(t *testing.T) migrations.Migration {
	t.Helper()
	model := namedConstraintModel(t)
	constraint := model.UniqueConstraints[1].Clone()
	model.UniqueConstraints = nil
	renamed := constraint.Clone()
	renamed.Name = "renamed"
	return migrations.Migration{App: "scoped", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "scoped", Model: model},
		&migrations.AddConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint.Clone()},
		&migrations.RemoveConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint.Clone()},
		migrations.AddConstraint{AppLabel: "scoped", ModelName: "label", Constraint: renamed},
	}}
}

func TestNamedConstraintDefinitionRoundTripOwnsMembersAndSemanticDigest(t *testing.T) {
	producer := definition.Producer{Name: "constraint-history-test", Version: "1"}
	migration := constraintOperationsMigration(t)
	wire, err := definition.Encode(producer, migration)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "constraints", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	migration.Operations[1].(*migrations.AddConstraint).Constraint.Fields[0] = "id"
	migration.Operations[2].(*migrations.RemoveConstraint).Constraint.Fields[0] = "id"
	snapshot := loaded.Definitions()[0]
	for _, operation := range snapshot.Operations {
		switch value := operation.(type) {
		case migrations.AddConstraint:
			value.Constraint.Fields[0] = "id"
		case migrations.RemoveConstraint:
			value.Constraint.Fields[0] = "id"
		}
	}
	again, err := definition.Encode(producer, loaded.Definitions()[0])
	if err != nil || !bytes.Equal(wire, again) {
		t.Fatal("history exposes named member storage", err)
	}
	reconstructor, err := loaded.Reconstructor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	model, exists := state.Model("scoped", "label")
	if !exists || len(model.UniqueConstraints) != 1 || model.UniqueConstraints[0].Name != "renamed" || !slices.Equal(model.UniqueConstraints[0].Fields, []string{"category", "name"}) {
		t.Fatal("serialized operations did not replay exact metadata")
	}
	for _, mutate := range []func(*ir.UniqueConstraint){
		func(c *ir.UniqueConstraint) { c.Name = "different" },
		func(c *ir.UniqueConstraint) { slices.Reverse(c.Fields) },
		func(c *ir.UniqueConstraint) { c.Fields[0] = "id" },
	} {
		changed := loaded.Definitions()[0]
		for index, operation := range changed.Operations {
			switch value := operation.(type) {
			case migrations.AddConstraint:
				mutate(&value.Constraint)
				changed.Operations[index] = value
			case migrations.RemoveConstraint:
				mutate(&value.Constraint)
				changed.Operations[index] = value
			}
		}
		encoded, err := definition.Encode(producer, changed)
		if err != nil {
			t.Fatal(err)
		}
		other, _, err := definition.Load(definition.Source{SourceID: "changed", Document: encoded})
		if err != nil || other.Digest() == loaded.Digest() {
			t.Fatal("constraint name/member/order disappeared from digest", err)
		}
	}
}

func TestNamedConstraintDefinitionRejectsOpenIncompleteAndAmbiguousOperations(t *testing.T) {
	producer := definition.Producer{Name: "test", Version: "1"}
	wire, err := definition.Encode(producer, constraintOperationsMigration(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"add_constraint", "remove_constraint"} {
		for _, constraint := range []string{
			`null`, `{}`, `[]`, `{"name":"scope"}`, `{"name":"scope","fields":[]}`, `{"name":"scope","fields":["name","name"]}`,
			`{"name":"scope","fields":["category.name"]}`, `{"name":"scope","fields":[1]}`, `{"name":"scope","fields":["name"],"condition":true}`,
			`{"name":"scope","fields":["name"],"deferrable":true}`, `{"name":"scope","name":"another","fields":["name"]}`,
		} {
			candidate := []byte(fmt.Sprintf(`{"kind":%q,"app_label":"scoped","model_name":"label","constraint":%s}`, kind, constraint))
			bad := replaceConstraintOperation(t, wire, candidate)
			if set, _, err := definition.Load(definition.Source{SourceID: "invalid", Document: bad}); err == nil || set.Digest() != "" {
				t.Fatal("invalid constraint operation produced usable history", kind, constraint)
			}
		}
		for _, suffix := range []string{`,"field":{}`, `,"before_field":"id"`, `,"model":{}`, `,"unknown":true`} {
			candidate := []byte(fmt.Sprintf(`{"kind":%q,"app_label":"scoped","model_name":"label","constraint":{"name":"scope","fields":["name"]}%s}`, kind, suffix))
			if set, _, err := definition.Load(definition.Source{SourceID: "extra", Document: replaceConstraintOperation(t, wire, candidate)}); err == nil || set.Digest() != "" {
				t.Fatal("constraint operation accepted an unrelated payload", suffix)
			}
		}
	}
}

func replaceConstraintOperation(t *testing.T, wire, operation []byte) []byte {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal(wire, &document); err != nil {
		t.Fatal(err)
	}
	var migration map[string]json.RawMessage
	if err := json.Unmarshal(document["migration"], &migration); err != nil {
		t.Fatal(err)
	}
	// Preserve the raw operation bytes, including duplicate object members.
	migration["operations"] = append(append([]byte{'['}, operation...), ']')
	document["migration"], _ = json.Marshal(migration)
	result, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNamedConstraintOperationResourceAdmissionPrecedesCloning(t *testing.T) {
	producer := definition.Producer{Name: "test", Version: "1"}
	for _, remove := range []bool{false, true} {
		for _, count := range []int{definition.MaxFieldsPerConstraint, definition.MaxFieldsPerConstraint + 1} {
			constraint := ir.UniqueConstraint{Name: "scope", Fields: make([]string, count)}
			for index := range constraint.Fields {
				constraint.Fields[index] = fmt.Sprintf("field_%04d", index)
			}
			var operation migrations.Operation = &migrations.AddConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint}
			if remove {
				operation = &migrations.RemoveConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint}
			}
			migration := migrations.Migration{App: "scoped", Name: "0002_scope", Operations: []migrations.Operation{operation}}
			wire, err := definition.Encode(producer, migration)
			if count == definition.MaxFieldsPerConstraint {
				if err != nil || len(wire) == 0 {
					t.Fatal("valid operation member limit rejected", err)
				}
				if _, _, err := definition.Load(definition.Source{SourceID: "limit", Document: wire}); err != nil {
					t.Fatal("wire member limit rejected", err)
				}
			} else {
				if err == nil || wire != nil || !strings.Contains(err.Error(), "fields_per_constraint") {
					t.Fatal("member overflow bypassed encoding preflight", err)
				}
				if _, err := migrations.NewStateReconstructor(migration); err == nil || !strings.Contains(err.Error(), "constraint_member_count") {
					t.Fatal("typed loader copied oversized member list", err)
				}
			}
		}
	}
	for _, operation := range []migrations.Operation{(*migrations.AddConstraint)(nil), (*migrations.RemoveConstraint)(nil)} {
		migration := migrations.Migration{App: "scoped", Name: "0001_initial", Operations: []migrations.Operation{operation}}
		if wire, err := definition.Encode(producer, migration); err == nil || wire != nil {
			t.Fatal("nil constraint operation encoded")
		}
		if _, err := migrations.NewStateReconstructor(migration); err == nil {
			t.Fatal("nil constraint operation reconstructed")
		}
	}
}
