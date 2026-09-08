package codegen

import (
	"strings"
	"testing"
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
