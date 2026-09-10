package backend

import (
	"context"
	"reflect"
	"testing"
)

func TestMigrationSQLRendererCurrentPublicShape(t *testing.T) {
	t.Parallel()

	request := reflect.TypeOf(ForwardMigrationSQLRequest{})
	wantFields := []struct {
		name   string
		typeOf reflect.Type
	}{
		{name: "App", typeOf: reflect.TypeOf("")},
		{name: "Name", typeOf: reflect.TypeOf("")},
		{name: "Intent", typeOf: reflect.TypeOf(MigrationIntent{})},
	}
	if request.NumField() != len(wantFields) {
		t.Fatalf("ForwardMigrationSQLRequest fields = %d, want %d", request.NumField(), len(wantFields))
	}
	for index, want := range wantFields {
		field := request.Field(index)
		if field.Name != want.name || field.Type != want.typeOf {
			t.Fatalf("request field[%d] = %s %s, want %s %s", index, field.Name, field.Type, want.name, want.typeOf)
		}
	}

	renderer := reflect.TypeOf((*MigrationSQLRenderer)(nil)).Elem()
	method, ok := renderer.MethodByName("RenderForwardMigrationSQL")
	wantMethod := reflect.TypeOf((func(context.Context, ForwardMigrationSQLRequest) ([]string, error))(nil))
	if !ok || method.Type != wantMethod || renderer.NumMethod() != 1 {
		t.Fatalf("MigrationSQLRenderer method = %#v, want %s", method, wantMethod)
	}
}
