package codegen_test

import (
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

type appFixtureFeatures struct {
	object     bool
	projection bool
}

// Tests choose their actual prerequisite files. Incomplete and incompatible
// unions remain possible without depending on the whole-project generator.
func writeGeneratedAppFixture(t *testing.T, root, directory, packageName string, schema ir.Schema, features appFixtureFeatures) []string {
	t.Helper()
	generators := []struct {
		filename string
		enabled  bool
		render   func(string, ir.Schema) ([]byte, error)
	}{
		{"zz_godj_generated.go", true, codegen.Generate},
		{"zz_godj_relation.go", true, codegen.GenerateRelationMetadata},
		{"zz_godj_relation_object.go", features.object, codegen.GenerateRelationObject},
		{"zz_godj_relation_projection.go", features.projection, codegen.GenerateRelationProjection},
	}
	var names []string
	for _, generator := range generators {
		if !generator.enabled {
			continue
		}
		source, err := generator.render(packageName, schema)
		if err != nil {
			t.Fatalf("generate %s/%s: %v", directory, generator.filename, err)
		}
		name := directory + "/" + generator.filename
		writeGeneratedTestFile(t, root, name, source)
		names = append(names, name)
	}
	return names
}
