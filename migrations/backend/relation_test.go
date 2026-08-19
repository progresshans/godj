package backend

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestRelationMigrationPublicShape(t *testing.T) {
	kind := reflect.TypeOf(RelationMigrationOperationKind(0))
	if kind.Kind() != reflect.Uint8 {
		t.Fatalf("RelationMigrationOperationKind underlying kind = %s, want uint8", kind.Kind())
	}

	capabilities := reflect.TypeOf(RelationMigrationCapabilities{})
	if capabilities.NumField() != 4 {
		t.Fatalf("RelationMigrationCapabilities fields = %d, want 4", capabilities.NumField())
	}
	for index, name := range []string{
		"CreateModelForeignKeys",
		"AddNullableForeignKey",
		"AddRequiredForeignKeyToEmptyTable",
		"RemoveForeignKeyByTableRemake",
	} {
		field := capabilities.Field(index)
		if field.Name != name || field.Type != reflect.TypeOf(false) {
			t.Fatalf("RelationMigrationCapabilities field[%d] = %s %s, want %s bool", index, field.Name, field.Type, name)
		}
	}

	operation := reflect.TypeOf(RelationMigrationOperation{})
	if operation.NumField() != 5 {
		t.Fatalf("RelationMigrationOperation fields = %d, want 5", operation.NumField())
	}
	wantOperationFields := []struct {
		name   string
		typeOf reflect.Type
	}{
		{name: "OperationIndex", typeOf: reflect.TypeOf(int(0))},
		{name: "Kind", typeOf: reflect.TypeOf(RelationMigrationOperationKind(0))},
		{name: "Before", typeOf: reflect.TypeOf(ir.Model{})},
		{name: "After", typeOf: reflect.TypeOf(ir.Model{})},
		{name: "Targets", typeOf: reflect.TypeOf([]RelationMigrationTarget(nil))},
	}
	for index, want := range wantOperationFields {
		field := operation.Field(index)
		if field.Name != want.name || field.Type != want.typeOf {
			t.Fatalf("RelationMigrationOperation field[%d] = %s %s, want %s %s", index, field.Name, field.Type, want.name, want.typeOf)
		}
	}

	target := reflect.TypeOf(RelationMigrationTarget{})
	if target.NumField() != 3 {
		t.Fatalf("RelationMigrationTarget fields = %d, want 3", target.NumField())
	}
	wantTargetFields := []struct {
		name   string
		typeOf reflect.Type
	}{
		{name: "SourceField", typeOf: reflect.TypeOf(ir.Field{})},
		{name: "TargetModel", typeOf: reflect.TypeOf(ir.Model{})},
		{name: "TargetKey", typeOf: reflect.TypeOf(ir.Field{})},
	}
	for index, want := range wantTargetFields {
		field := target.Field(index)
		if field.Name != want.name || field.Type != want.typeOf {
			t.Fatalf("RelationMigrationTarget field[%d] = %s %s, want %s %s", index, field.Name, field.Type, want.name, want.typeOf)
		}
	}

	intent := reflect.TypeOf(RelationMigrationIntent{})
	if intent.NumField() != 1 || intent.Field(0).Name != "Operations" ||
		intent.Field(0).Type != reflect.TypeOf([]RelationMigrationOperation(nil)) {
		t.Fatalf("RelationMigrationIntent shape = %+v, want exact Operations []RelationMigrationOperation", intent)
	}

	if RelationMigrationCreateModel != 1 || RelationMigrationDeleteModel != 2 ||
		RelationMigrationAddField != 3 || RelationMigrationRemoveField != 4 {
		t.Fatalf("relation migration operation values = (%d,%d,%d,%d), want (1,2,3,4)",
			RelationMigrationCreateModel,
			RelationMigrationDeleteModel,
			RelationMigrationAddField,
			RelationMigrationRemoveField,
		)
	}
}

func TestRelationMigrationInterfaceMethodInventory(t *testing.T) {
	backendType := reflect.TypeOf((*RelationRevisionFencedBackend)(nil)).Elem()
	backendMethods := []struct {
		name   string
		typeOf reflect.Type
	}{
		{
			name:   "OpenRevisionFencedSession",
			typeOf: reflect.TypeOf((func(context.Context) (RevisionFencedSession, error))(nil)),
		},
		{
			name:   "RelationMigrationCapabilities",
			typeOf: reflect.TypeOf((func() RelationMigrationCapabilities)(nil)),
		},
	}
	if backendType.NumMethod() != len(backendMethods) {
		t.Fatalf("RelationRevisionFencedBackend methods = %d, want %d", backendType.NumMethod(), len(backendMethods))
	}
	for index, want := range backendMethods {
		method := backendType.Method(index)
		if method.Name != want.name || method.Type != want.typeOf {
			t.Fatalf("RelationRevisionFencedBackend method[%d] = %s %s, want %s %s", index, method.Name, method.Type, want.name, want.typeOf)
		}
	}

	sessionType := reflect.TypeOf((*RelationRevisionFencedSession)(nil)).Elem()
	sessionMethods := []struct {
		name   string
		typeOf reflect.Type
	}{
		{
			name:   "BeginFencedMigration",
			typeOf: reflect.TypeOf((func(context.Context, HistoryTransition) (RevisionFencedTransaction, error))(nil)),
		},
		{
			name:   "BeginRelationFencedMigration",
			typeOf: reflect.TypeOf((func(context.Context, HistoryTransition, RelationMigrationIntent) (RevisionFencedTransaction, error))(nil)),
		},
		{name: "Close", typeOf: reflect.TypeOf((func(context.Context) error)(nil))},
		{
			name:   "ReadAppliedMigrations",
			typeOf: reflect.TypeOf((func(context.Context) ([]AppliedMigration, error))(nil)),
		},
	}
	if sessionType.NumMethod() != len(sessionMethods) {
		t.Fatalf("RelationRevisionFencedSession methods = %d, want %d", sessionType.NumMethod(), len(sessionMethods))
	}
	for index, want := range sessionMethods {
		method := sessionType.Method(index)
		if method.Name != want.name || method.Type != want.typeOf {
			t.Fatalf("RelationRevisionFencedSession method[%d] = %s %s, want %s %s", index, method.Name, method.Type, want.name, want.typeOf)
		}
	}
}

func TestRelationMigrationExportedDeclarationInventory(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	file, err := parser.ParseFile(token.NewFileSet(), currentFile[:len(currentFile)-len("relation_test.go")]+"relation.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				switch specification := specification.(type) {
				case *ast.TypeSpec:
					if ast.IsExported(specification.Name.Name) {
						got = append(got, "type:"+specification.Name.Name)
					}
				case *ast.ValueSpec:
					for _, name := range specification.Names {
						if ast.IsExported(name.Name) {
							got = append(got, declaration.Tok.String()+":"+name.Name)
						}
					}
				}
			}
		case *ast.FuncDecl:
			if ast.IsExported(declaration.Name.Name) {
				got = append(got, "func:"+declaration.Name.Name)
			}
		}
	}
	sort.Strings(got)
	want := []string{
		"const:RelationMigrationAddField",
		"const:RelationMigrationCreateModel",
		"const:RelationMigrationDeleteModel",
		"const:RelationMigrationRemoveField",
		"type:RelationMigrationCapabilities",
		"type:RelationMigrationIntent",
		"type:RelationMigrationOperation",
		"type:RelationMigrationOperationKind",
		"type:RelationMigrationTarget",
		"type:RelationRevisionFencedBackend",
		"type:RelationRevisionFencedSession",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relation.go exported declarations = %v, want %v", got, want)
	}
}

type relationPortFixture struct{}

func (*relationPortFixture) OpenRevisionFencedSession(context.Context) (RevisionFencedSession, error) {
	return nil, nil
}

func (*relationPortFixture) RelationMigrationCapabilities() RelationMigrationCapabilities {
	return RelationMigrationCapabilities{}
}

type relationSessionFixture struct{}

func (*relationSessionFixture) ReadAppliedMigrations(context.Context) ([]AppliedMigration, error) {
	return nil, nil
}

func (*relationSessionFixture) BeginFencedMigration(context.Context, HistoryTransition) (RevisionFencedTransaction, error) {
	return nil, nil
}

func (*relationSessionFixture) BeginRelationFencedMigration(context.Context, HistoryTransition, RelationMigrationIntent) (RevisionFencedTransaction, error) {
	return nil, nil
}

func (*relationSessionFixture) Close(context.Context) error { return nil }

var _ RelationRevisionFencedBackend = (*relationPortFixture)(nil)
var _ RelationRevisionFencedSession = (*relationSessionFixture)(nil)

type legacyOnlyRelationBackendFixture struct{}

func (*legacyOnlyRelationBackendFixture) OpenRevisionFencedSession(context.Context) (RevisionFencedSession, error) {
	return &legacyOnlyRelationSessionFixture{}, nil
}

type legacyOnlyRelationSessionFixture struct{}

func (*legacyOnlyRelationSessionFixture) ReadAppliedMigrations(context.Context) ([]AppliedMigration, error) {
	return nil, nil
}

func (*legacyOnlyRelationSessionFixture) BeginFencedMigration(context.Context, HistoryTransition) (RevisionFencedTransaction, error) {
	return nil, nil
}

func (*legacyOnlyRelationSessionFixture) Close(context.Context) error { return nil }

var _ RevisionFencedBackend = (*legacyOnlyRelationBackendFixture)(nil)
var _ RevisionFencedSession = (*legacyOnlyRelationSessionFixture)(nil)

func TestRelationMigrationInterfacesRemainOptional(t *testing.T) {
	if _, ok := any(&legacyOnlyRelationBackendFixture{}).(RelationRevisionFencedBackend); ok {
		t.Fatal("legacy-only backend unexpectedly implements relation extension")
	}
	if _, ok := any(&legacyOnlyRelationSessionFixture{}).(RelationRevisionFencedSession); ok {
		t.Fatal("legacy-only session unexpectedly implements relation extension")
	}
}
