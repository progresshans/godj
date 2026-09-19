package codegen

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedNamespaceChecksActualDeclarationsAcrossFiles(t *testing.T) {
	for _, test := range []struct {
		name, first, second, conflict string
	}{
		{"package declaration", "type Model struct{}", "var Model int", "package symbol Model"},
		{"receiver field", "type Model struct{ Value int }", "func (Model) Value() {}", "Model member Value"},
		{"generic receiver", "type Model[T any] struct{ Value T }", "func (*Model[T]) Value() {}", "Model member Value"},
		{"embedded field", "type Model struct{ *Value }; type Value struct{}", "func (*Model) Value() {}", "Model member Value"},
		{"import after declaration", "type used struct{}", "import used \"io\"", "import alias used"},
		{"declaration after import", "import used \"io\"", "type used struct{}", "package symbol used"},
		{"default import", "import \"net/http\"", "var http int", "package symbol http"},
		{"same method on different receivers", "type A struct{}; func (A) From() {}", "type B struct{}; func (B) From() {}", ""},
		{"shared import", "import used \"io\"", "import used \"io\"", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := []projectRenderedFile{
				{path: "project/a.go", source: []byte("package project\n" + test.first)},
				{path: "project/b.go", source: []byte("package project\n" + test.second)},
			}
			err := finalizeGeneratedFiles(files)
			if test.conflict == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.conflict) {
				t.Fatalf("namespace error = %v, want %q", err, test.conflict)
			}
		})
	}
}

func TestStandaloneAppCompanionsCheckActualPrerequisiteNamespaces(t *testing.T) {
	entrypoints := []func(string, ir.Schema) ([]byte, error){Generate, GenerateRelationMetadata, GenerateRelationObject, GenerateRelationProjection}
	for owner, symbol := range []string{"GoDjGeneratorVersion", "GoDjRelationSchema", "GoDjRelationObjectGeneratorVersion", "GoDjRelationProjectionGeneratorVersion"} {
		input := ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "app", Models: []ir.Model{{
			Name: "record", GoName: symbol, Fields: []ir.Field{{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true}},
		}}}
		for stage, generate := range entrypoints {
			source, err := generate("models", input)
			if stage < owner {
				if err != nil || len(source) == 0 {
					t.Fatalf("stage %d rejected %s before its declaration exists: %v", stage, symbol, err)
				}
			} else if source != nil || err == nil || !strings.Contains(err.Error(), "conflicts with") {
				t.Fatalf("stage %d published colliding prerequisite %s: %v", stage, symbol, err)
			}
		}
	}
}
