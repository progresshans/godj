package migrations

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestUniqueHistoryRetainsExactForwardAndReversePreimages(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "unique", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{schema.UUIDField("reference", "Reference", schema.Nullable())}}}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := NewProjectState(s)
	if err != nil {
		t.Fatal(err)
	}
	before, after := s.Models[0].Fields[1].Clone(), s.Models[0].Fields[1].Clone()
	after.Unique = true
	op := AlterField{AppLabel: "unique", ModelName: "entry", Before: before, After: after}
	forward, err := op.stateForward(base)
	if err != nil || forward.Equal(base) {
		t.Fatal("unique change disappeared", err)
	}
	restored, err := op.stateBackward(forward)
	if err != nil || !restored.Equal(base) {
		t.Fatal("reverse failed to restore uniqueness", err)
	}
	if _, err := op.stateForward(forward); err == nil {
		t.Fatal("stale unique preimage accepted")
	}
	if _, err := op.stateBackward(base); err == nil {
		t.Fatal("stale reverse unique preimage accepted")
	}
	initial := Migration{App: "unique", Name: "0001_initial", Operations: []Operation{CreateModel{AppLabel: "unique", Model: s.Models[0]}}}
	change := Migration{App: "unique", Name: "0002_unique", Dependencies: []MigrationKey{initial.Key()}, Operations: []Operation{op}}
	loaded := testLoadedDefinitionSet(t, []Migration{initial, change})
	body := "CREATE UNIQUE INDEX reference_unique ON unique_entry (reference)"
	for _, group := range [][]string{{body}, nil, {""}} {
		renderer := &migrationSQLRendererSpy{groups: [][]string{group}}
		sql, err := RenderMigrationSQL(t.Context(), loaded, change.Key(), renderer)
		if renderer.calls != 1 || renderer.request.Intent.Operations[0].Before.Fields[1].Unique || !renderer.request.Intent.Operations[0].After.Fields[1].Unique {
			t.Fatal("renderer lost the unique transition")
		}
		if len(group) == 0 || group[0] == "" {
			assertMigrationSQLError(t, err, CategorySQLRender, CodeInvalidRenderedSQL, change.Key())
			if sql != nil {
				t.Fatal("empty unique SQL leaked partial output")
			}
		} else if err != nil || len(sql) != 1 || sql[0] != body {
			t.Fatal("physical unique SQL lost operation identity", err)
		}
	}
}

func TestUniqueCapabilityPrecedesTransactionsInBothDirections(t *testing.T) {
	for _, mode := range []string{"create", "add", "alter_add", "alter_remove", "retained", "target", "transitive"} {
		t.Run(mode, func(t *testing.T) {
			modelFields := []schema.Field{schema.CharField("reference", "Reference", 24)}
			if mode == "create" || mode == "alter_remove" || mode == "retained" || mode == "target" || mode == "transitive" {
				schema.Unique()(&modelFields[0])
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
				var operation Operation = AddField{AppLabel: "unique", ModelName: name, Field: ir.Field{Name: "extra", GoName: "Extra", Column: "extra", Kind: ir.FieldText, Nullable: true, Unique: mode == "add"}}
				if mode == "alter_add" || mode == "alter_remove" {
					before := models["owner"].Fields[1].Clone()
					after := before.Clone()
					after.Unique = !before.Unique
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
				if !errors.As(err, &capability) || !strings.Contains(capability.Detail, "UniqueConstraints") {
					t.Fatalf("missing unique capability: %v", err)
				}
				if session.beginCount != 0 || fake.atomicBeginCount != 0 || session.closeCount != 1 {
					t.Fatal("unsupported unique operation started or leaked a transaction")
				}
			}
		})
	}
}
