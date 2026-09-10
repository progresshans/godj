package backend

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestMigrationCapabilitiesAndIntentCurrentShape(t *testing.T) {
	capabilities := reflect.TypeOf(MigrationCapabilities{})
	wantCapabilities := []string{
		"CreateModelForeignKeys",
		"AddNullableForeignKey",
		"AddRequiredForeignKeyToEmptyTable",
		"RemoveForeignKey",
	}
	if capabilities.NumField() != len(wantCapabilities) {
		t.Fatalf("MigrationCapabilities fields = %d, want %d", capabilities.NumField(), len(wantCapabilities))
	}
	for index, name := range wantCapabilities {
		field := capabilities.Field(index)
		if field.Name != name || field.Type.Kind() != reflect.Bool {
			t.Fatalf("MigrationCapabilities field[%d] = %s %s", index, field.Name, field.Type)
		}
	}

	operation := reflect.TypeOf(MigrationOperation{})
	wantOperation := []reflect.Type{
		reflect.TypeOf(int(0)),
		reflect.TypeOf(MigrationOperationKind(0)),
		reflect.TypeOf(ir.Model{}),
		reflect.TypeOf(ir.Model{}),
		reflect.TypeOf([]MigrationTarget(nil)),
	}
	if operation.NumField() != len(wantOperation) {
		t.Fatalf("MigrationOperation fields = %d", operation.NumField())
	}
	for index, want := range wantOperation {
		if operation.Field(index).Type != want {
			t.Fatalf("MigrationOperation field[%d] = %s, want %s", index, operation.Field(index).Type, want)
		}
	}
	if MigrationCreateModel != 1 || MigrationDeleteModel != 2 || MigrationAddField != 3 || MigrationRemoveField != 4 {
		t.Fatalf("migration operation values = %d/%d/%d/%d", MigrationCreateModel, MigrationDeleteModel, MigrationAddField, MigrationRemoveField)
	}
}

func TestMigrationIntentClonePreservesShapeAndDetachesNestedIR(t *testing.T) {
	field := ir.Field{Name: "owner", Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: "before"}, Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}}}
	model := ir.Model{Name: "entry", Fields: []ir.Field{field}}
	intent := MigrationIntent{Operations: []MigrationOperation{{
		Before: model, After: model,
		Targets: []MigrationTarget{{SourceField: field, TargetModel: model, TargetKey: field}},
	}}}
	cloned := intent.Clone()
	cloned.Operations[0].Before.Fields[0].Default.String = "changed before"
	cloned.Operations[0].After.Fields[0].Relation.Target.AppLabel = "changed after"
	cloned.Operations[0].Targets[0].SourceField.Default.String = "changed source"
	cloned.Operations[0].Targets[0].TargetModel.Fields[0].Default.String = "changed model"
	cloned.Operations[0].Targets[0].TargetKey.Relation.Target.ModelName = "changed key"
	if !reflect.DeepEqual(intent.Operations[0].Before, model) || field.Default.String != "before" || field.Relation.Target != (ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}) {
		t.Fatal("intent clone retained a nested caller alias")
	}
	for _, value := range []MigrationIntent{
		{}, {Operations: []MigrationOperation{}},
		{Operations: []MigrationOperation{{Before: ir.Model{Fields: []ir.Field{}}, Targets: []MigrationTarget{}}}},
		{Operations: []MigrationOperation{{Targets: []MigrationTarget{{TargetModel: ir.Model{Fields: []ir.Field{}}}}}}},
	} {
		if !reflect.DeepEqual(value.Clone(), value) {
			t.Fatalf("intent clone changed nil/empty shape: %#v", value)
		}
	}
}

func TestRevisionFencedPortsExposeOneCapabilityAndBeginPath(t *testing.T) {
	backendType := reflect.TypeOf((*RevisionFencedBackend)(nil)).Elem()
	if backendType.NumMethod() != 2 {
		t.Fatalf("RevisionFencedBackend methods = %d, want 2", backendType.NumMethod())
	}
	if method, ok := backendType.MethodByName("MigrationCapabilities"); !ok || method.Type != reflect.TypeOf((func() MigrationCapabilities)(nil)) {
		t.Fatalf("MigrationCapabilities method = %#v", method)
	}

	sessionType := reflect.TypeOf((*RevisionFencedSession)(nil)).Elem()
	begin, ok := sessionType.MethodByName("BeginMigration")
	wantBegin := reflect.TypeOf((func(context.Context, HistoryTransition, MigrationIntent) (RevisionFencedTransaction, error))(nil))
	if !ok || begin.Type != wantBegin {
		t.Fatalf("BeginMigration method = %#v, want %s", begin, wantBegin)
	}
	if _, exists := sessionType.MethodByName("BeginFencedMigration"); exists {
		t.Fatal("legacy scalar begin entry remains public")
	}
	if _, exists := sessionType.MethodByName("BeginRelationFencedMigration"); exists {
		t.Fatal("relation-only begin entry remains public")
	}
}

type migrationPortFixture struct{}

func (*migrationPortFixture) BeginMigration(context.Context) (Transaction, error) { return nil, nil }
func (*migrationPortFixture) MigrationCapabilities() MigrationCapabilities {
	return MigrationCapabilities{}
}
func (*migrationPortFixture) OpenRevisionFencedSession(context.Context) (RevisionFencedSession, error) {
	return &migrationSessionFixture{}, nil
}

type migrationSessionFixture struct{}

func (*migrationSessionFixture) ReadAppliedMigrations(context.Context) ([]AppliedMigration, error) {
	return nil, nil
}
func (*migrationSessionFixture) BeginMigration(context.Context, HistoryTransition, MigrationIntent) (RevisionFencedTransaction, error) {
	return nil, nil
}
func (*migrationSessionFixture) Close(context.Context) error { return nil }

var _ AtomicBackend = (*migrationPortFixture)(nil)
var _ RevisionFencedBackend = (*migrationPortFixture)(nil)
var _ RevisionFencedSession = (*migrationSessionFixture)(nil)
