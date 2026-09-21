package definition_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func namedConstraintModel(t *testing.T) ir.Model {
	t.Helper()
	value, err := schema.Build(schema.Definition{AppLabel: "scoped", Models: []schema.Model{{
		Name: "label", GoName: "Label", Fields: []schema.Field{schema.IntegerField("category", "Category"), schema.CharField("name", "Name", 64)},
		UniqueConstraints: []schema.UniqueConstraint{{Name: "named", Fields: []string{"name"}}, {Name: "scoped_name", Fields: []string{"category", "name"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return value.Models[0]
}

func namedConstraintMigration(t *testing.T) migrations.Migration {
	return migrations.Migration{App: "scoped", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "scoped", Model: namedConstraintModel(t)}}}
}

func TestNamedUniqueModelDefinitionRetainsDigestHistoryAndDetachedMembers(t *testing.T) {
	producer := definition.Producer{Name: "named-constraint-test", Version: "1"}
	migration := namedConstraintMigration(t)
	wire, err := definition.Encode(producer, migration)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "named", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	definitions := loaded.Definitions()
	model := definitions[0].Operations[0].(migrations.CreateModel).Model
	if !model.Equal(namedConstraintModel(t)) {
		t.Fatal("historical wire omitted named constraints")
	}
	again, err := definition.Encode(producer, definitions[0])
	if err != nil || !bytes.Equal(again, wire) {
		t.Fatal("canonical round trip changed model constraints", err)
	}
	model.UniqueConstraints[1].Fields[0] = "id"
	if loaded.Definitions()[0].Operations[0].(migrations.CreateModel).Model.UniqueConstraints[1].Fields[0] != "category" {
		t.Fatal("loaded snapshot exposes member storage")
	}
	reconstructor, err := loaded.Reconstructor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	got, found := state.Model("scoped", "label")
	if !found || !got.Equal(namedConstraintModel(t)) {
		t.Fatal("state reconstruction discarded named constraints")
	}
	got.UniqueConstraints[1].Fields[0] = "id"
	original, _ := state.Model("scoped", "label")
	if original.UniqueConstraints[1].Fields[0] != "category" {
		t.Fatal("state accessor exposes member storage")
	}
	for _, change := range []func(*ir.Model){
		func(model *ir.Model) { model.UniqueConstraints[0].Name = "renamed" },
		func(model *ir.Model) {
			model.UniqueConstraints[1].Fields[0], model.UniqueConstraints[1].Fields[1] = model.UniqueConstraints[1].Fields[1], model.UniqueConstraints[1].Fields[0]
		},
		func(model *ir.Model) { model.UniqueConstraints[1].Fields[0] = "id" },
		func(model *ir.Model) { model.UniqueConstraints = nil },
	} {
		model := namedConstraintModel(t)
		change(&model)
		changed := namedConstraintMigration(t)
		changed.Operations[0] = migrations.CreateModel{AppLabel: "scoped", Model: model}
		encoded, err := definition.Encode(producer, changed)
		if err != nil {
			t.Fatal(err)
		}
		other, _, err := definition.Load(definition.Source{SourceID: "changed", Document: encoded})
		if err != nil || other.Digest() == loaded.Digest() {
			t.Fatal("changed constraint did not change historical identity", err)
		}
		otherReconstructor, err := other.Reconstructor(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		otherState, err := otherReconstructor.Reconstruct(migrations.LatestStateRequest())
		if err != nil || otherState.Equal(state) {
			t.Fatal("state equality ignored named constraints", err)
		}
	}
}

func TestNamedUniqueModelDefinitionRejectsUnsupportedAndMalformedConstraintWire(t *testing.T) {
	wire, err := definition.Encode(definition.Producer{Name: "test", Version: "1"}, namedConstraintMigration(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, constraints := range []string{
		`null`, `[]`, `{}`, `true`, `[null]`, `[{}]`,
		`[{"name":"scope","fields":[]}]`, `[{"name":"scope","fields":null}]`,
		`[{"name":"scope","fields":["category",1]}]`, `[{"name":"scope","fields":["category","missing"]}]`,
		`[{"name":"scope","fields":["name","name"]}]`, `[{"name":"scope","fields":["name"],"condition":true}]`,
		`[{"name":"scope","fields":["name"],"include":["id"]}]`,
		`[{"name":"scope","fields":["name"]},{"name":"scope","fields":["category"]}]`,
		`[{"name":"z","fields":["name"]},{"name":"a","fields":["category"]}]`,
	} {
		var document map[string]json.RawMessage
		if err := json.Unmarshal(wire, &document); err != nil {
			t.Fatal(err)
		}
		var migration map[string]json.RawMessage
		if err := json.Unmarshal(document["migration"], &migration); err != nil {
			t.Fatal(err)
		}
		var operations []map[string]json.RawMessage
		if err := json.Unmarshal(migration["operations"], &operations); err != nil {
			t.Fatal(err)
		}
		var model map[string]json.RawMessage
		if err := json.Unmarshal(operations[0]["model"], &model); err != nil {
			t.Fatal(err)
		}
		model["unique_constraints"] = json.RawMessage(constraints)
		operations[0]["model"], _ = json.Marshal(model)
		migration["operations"], _ = json.Marshal(operations)
		document["migration"], _ = json.Marshal(migration)
		bad, err := json.Marshal(document)
		if err != nil || bytes.Equal(wire, bad) {
			t.Fatal("negative control did not alter the wire", err)
		}
		if loaded, _, err := definition.Load(definition.Source{SourceID: "invalid", Document: bad}); err == nil || loaded.Digest() != "" {
			t.Fatalf("invalid constraint was normalized, dropped or partially published: %s", constraints)
		}
	}
}

func TestNamedUniqueEncodingChecksConstraintResourcesBeforeClone(t *testing.T) {
	for _, count := range []int{definition.MaxConstraintsPerCreateModel, definition.MaxConstraintsPerCreateModel + 1} {
		model := namedConstraintModel(t)
		model.UniqueConstraints = make([]ir.UniqueConstraint, count)
		for index := range model.UniqueConstraints {
			model.UniqueConstraints[index] = ir.UniqueConstraint{Name: fmt.Sprintf("constraint_%04d", index), Fields: []string{"name"}}
		}
		migration := namedConstraintMigration(t)
		migration.Operations[0] = migrations.CreateModel{AppLabel: "scoped", Model: model}
		before := model.Clone()
		encoded, err := definition.Encode(definition.Producer{Name: "test", Version: "1"}, migration)
		if count == definition.MaxConstraintsPerCreateModel {
			if err != nil || len(encoded) == 0 {
				t.Fatal("valid constraint limit rejected", err)
			}
		} else if err == nil || encoded != nil || !strings.Contains(err.Error(), "constraints_per_create_model") {
			t.Fatal("constraint overflow did not fail resource admission", err)
		}
		if !reflect.DeepEqual(before, model) {
			t.Fatal("encoding changed rejected constraint input")
		}
	}
	model := namedConstraintModel(t)
	model.UniqueConstraints[0].Fields = make([]string, definition.MaxFieldsPerConstraint+1)
	migration := namedConstraintMigration(t)
	migration.Operations[0] = migrations.CreateModel{AppLabel: "scoped", Model: model}
	if encoded, err := definition.Encode(definition.Producer{Name: "test", Version: "1"}, migration); err == nil || encoded != nil || !strings.Contains(err.Error(), "fields_per_constraint") {
		t.Fatal("oversized member list bypassed resource admission", err)
	}
}

func TestNamedUniquePhysicalProjectionCannotSilentlyDropPendingConstraints(t *testing.T) {
	for name, renderer := range map[string]backend.MigrationSQLRenderer{
		"sqlite":   sqlite.NewMigrationSQLRenderer(),
		"postgres": postgres.NewMigrationSQLRenderer(postgres.MigrationSQLConfig{Schema: "public"}),
	} {
		t.Run(name, func(t *testing.T) {
			for _, mode := range []string{"plain", "create", "add", "remove"} {
				t.Run(mode, func(t *testing.T) {
					initial := namedConstraintMigration(t)
					operation := initial.Operations[0].(migrations.CreateModel)
					constraint := operation.Model.UniqueConstraints[1].Clone()
					if mode != "create" && mode != "remove" {
						operation.Model.UniqueConstraints = nil
					}
					initial.Operations[0] = operation
					history := []migrations.Migration{initial}
					if mode == "add" || mode == "remove" {
						var change migrations.Operation = migrations.AddConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint}
						if mode == "remove" {
							change = migrations.RemoveConstraint{AppLabel: "scoped", ModelName: "label", Constraint: constraint}
						}
						history = append(history, migrations.Migration{App: "scoped", Name: "0002_constraint", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{change}})
					}
					var sources []definition.Source
					for _, migration := range history {
						wire, err := definition.Encode(definition.Producer{Name: "test", Version: "1"}, migration)
						if err != nil {
							t.Fatal(err)
						}
						sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
					}
					loaded, _, err := definition.Load(sources...)
					if err != nil {
						t.Fatal(err)
					}
					statements, err := migrations.RenderMigrationSQL(t.Context(), loaded, history[len(history)-1].Key(), renderer)
					if mode != "plain" {
						var rejected *migrations.MigrationSQLError
						if !errors.As(err, &rejected) || rejected.Category != migrations.CategoryCapability || rejected.Code != migrations.CodeUnsupported || statements != nil {
							t.Fatal("pending physical ownership was omitted from projection", err)
						}
					} else if err != nil || len(statements) == 0 {
						t.Fatal("plain-model control failed", err)
					}
				})
			}
		})
	}
}
