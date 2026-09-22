package migrations

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestManyToManyCapabilityRequiredForChangedAndRetainedBindingsBeforeBegin(t *testing.T) {
	fk := func(name, goName, target string) ir.Field {
		return ir.Field{Name: name, GoName: goName, Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "scope", ModelName: target}, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteCascade, Reverse: ir.ReverseRelation{Disabled: true}}}
	}
	schema, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "scope", Models: []ir.Model{{Name: "owner", GoName: "Owner"}, {Name: "label", GoName: "Label"}, {Name: "link", GoName: "Link", Fields: []ir.Field{fk("owner", "OwnerID", "owner"), fk("label", "LabelID", "label")}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial := Migration{App: "scope", Name: "0001_initial"}
	for _, model := range schema.Models {
		initial.Operations = append(initial.Operations, CreateModel{AppLabel: "scope", Model: model})
	}
	field, err := ir.NormalizeManyToManyField("scope", "owner", ir.ManyToManyField{Name: "labels", GoName: "Labels", Target: ir.ModelIdentity{AppLabel: "scope", ModelName: "label"}, Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "scope", ModelName: "link"}, SourceField: "owner", TargetField: "label"}})
	if err != nil {
		t.Fatal(err)
	}
	add := Migration{App: "scope", Name: "0002_labels", Dependencies: []MigrationKey{initial.Key()}, Operations: []Operation{AddManyToMany{AppLabel: "scope", ModelName: "owner", Field: field}}}
	for _, source := range []string{"new_relation", "owner", "link"} {
		t.Run(source, func(t *testing.T) {
			definitions := []Migration{initial, add}
			var records []backend.AppliedMigration
			if source != "new_relation" {
				records = lifecycleRecords(initial.Key(), add.Key())
				definitions = append(definitions, Migration{App: "scope", Name: "0003_note", Dependencies: []MigrationKey{add.Key()}, Operations: []Operation{AddField{AppLabel: "scope", ModelName: source, Field: ir.Field{Name: "note", GoName: "Note", Column: "note", Kind: ir.FieldText, Nullable: true}}}})
			}
			session := newLifecycleTestSession(records, nil)
			fake := newLifecycleTestBackend(session)
			fake.capabilities = backend.MigrationCapabilities{CreateModelForeignKeys: true, AddNullableForeignKey: true, AddRequiredForeignKeyToEmptyTable: true, RemoveForeignKey: true, UniqueConstraints: true}
			_, err := (Executor{Backend: fake}).Migrate(t.Context(), testLoadedDefinitionSet(t, definitions), LatestLifecycleRequest())
			var capability *backend.CapabilityError
			if !errors.As(err, &capability) || !strings.Contains(capability.Detail, "ExplicitManyToMany") || session.beginCount != 0 {
				t.Fatal("unsupported changed/retained collection crossed mutation boundary", err, session.beginCount)
			}
		})
	}
}
