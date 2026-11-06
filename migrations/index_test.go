package migrations

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestColumnIndexCapabilityPrecedesTransactionsInBothDirections(t *testing.T) {
	for _, mode := range []string{"create", "add", "alter_add", "alter_remove", "retained", "target", "transitive"} {
		t.Run(mode, func(t *testing.T) {
			modelFields := []schema.Field{schema.CharField("reference", "Reference", 24)}
			if mode == "create" || mode == "alter_remove" || mode == "retained" || mode == "target" || mode == "transitive" {
				schema.DBIndex(true)(&modelFields[0])
			}
			s, err := schema.Build(schema.Definition{AppLabel: "unique", Models: []schema.Model{
				{Name: "owner", GoName: "Owner", Fields: modelFields},
				{Name: "group", GoName: "Group", Fields: []schema.Field{schema.ForeignKey("owner", "Owner", schema.Target("unique", "owner"), schema.RelatedName("groups"), schema.Protect, schema.Nullable())}},
				{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.ForeignKey("group", "Group", schema.Target("unique", "group"), schema.RelatedName("entries"), schema.Protect, schema.Nullable())}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			models := make(map[string]ir.Model)
			for _, model := range s.Models {
				models[model.Name] = model
			}
			initial := Migration{App: "unique", Name: "0001_initial", Operations: []Operation{CreateModel{AppLabel: "unique", Model: models["owner"]}}}
			if mode == "target" || mode == "transitive" {
				initial.Operations = append(initial.Operations, CreateModel{AppLabel: "unique", Model: models["group"]})
			}
			if mode == "transitive" {
				initial.Operations = append(initial.Operations, CreateModel{AppLabel: "unique", Model: models["entry"]})
			}
			history := []Migration{initial}
			if mode != "create" {
				name := "owner"
				if mode == "target" {
					name = "group"
				}
				if mode == "transitive" {
					name = "entry"
				}
				var operation Operation = AddField{AppLabel: "unique", ModelName: name, Field: ir.Field{Name: "extra", GoName: "Extra", Column: "extra", Kind: ir.FieldText, Nullable: true, DBIndex: mode == "add"}}
				if mode == "alter_add" || mode == "alter_remove" {
					before := models["owner"].Fields[1].Clone()
					after := before.Clone()
					after.DBIndex = !before.DBIndex
					operation = AlterField{AppLabel: "unique", ModelName: "owner", Before: before, After: after}
				}
				history = append(history, Migration{App: "unique", Name: "0002_change", Dependencies: []MigrationKey{initial.Key()}, Operations: []Operation{operation}})
			}
			loaded := testLoadedDefinitionSet(t, history)
			for _, reverse := range []bool{false, true} {
				var records []backend.AppliedMigration
				request := LatestLifecycleRequest()
				if mode != "create" {
					records = lifecycleRecords(initial.Key())
				}
				if reverse {
					keys := make([]MigrationKey, len(history))
					for index := range history {
						keys[index] = history[index].Key()
					}
					records = lifecycleRecords(keys...)
					if mode == "create" {
						request = TargetedLifecycleRequest(ZeroTarget("unique"))
					} else {
						request = TargetedLifecycleRequest(NamedTarget(initial.Key()))
					}
				}
				session := newLifecycleTestSession(records, nil)
				fake := newLifecycleTestBackend(session)
				fake.capabilities = backend.MigrationCapabilities{CreateModelForeignKeys: true, AddNullableForeignKey: true, AddRequiredForeignKeyToEmptyTable: true, RemoveForeignKey: true, AlterFieldChoices: true, AlterFieldDecimalPrecision: true}
				_, err := (Executor{Backend: fake}).Migrate(t.Context(), loaded, request)
				var capability *backend.CapabilityError
				if !errors.As(err, &capability) || !strings.Contains(capability.Detail, "ColumnIndexes") {
					t.Fatalf("missing column index capability: %v", err)
				}
				if session.beginCount != 0 || fake.atomicBeginCount != 0 || session.closeCount != 1 {
					t.Fatal("unsupported column index operation started or leaked a transaction")
				}
			}
		})
	}
}
