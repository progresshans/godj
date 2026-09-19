package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/nullableforwardproduct"
)

func TestGeneratedNestedForwardConsumer(t *testing.T) {
	schemas, err := nullableforwardproduct.NestedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-nested-forward"
	apps := make([]codegen.AppSpec, len(schemas))
	for i, s := range schemas {
		apps[i] = codegen.AppSpec{Alias: s.AppLabel, Package: codegen.PackageSpec{PackageName: s.AppLabel, ImportPath: module + "/" + s.AppLabel, Directory: s.AppLabel}, Schema: s}
	}
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: apps})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/nestedforward/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	fixture, err := os.ReadFile("testdata/nestedforward/fixture_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/fixture_test.go", fixture)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "orm/testdata/nested-forward-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestGeneratedNestedForwardReference", "TestGeneratedNestedForwardInvalidRoutes", "TestGeneratedNestedForwardDerivation")
	writeGeneratedTestFile(t, root, "wrong/wrong.go", []byte(`package wrong
import (
 "example.com/godj-nested-forward/blog"
 "example.com/godj-nested-forward/people"
 "example.com/godj-nested-forward/directory"
 "github.com/progresshans/godj/orm"
)
func bad(prefix orm.ForwardRelation[blog.Post,people.Person],suffix orm.ForwardRelation[directory.Team,directory.Organization]){
 _ = orm.ChainForward(prefix,suffix)
}
`))
	output, err := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./wrong").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("ChainForward")) || !bytes.Contains(output, []byte("does not match")) {
		t.Fatalf("intermediate Go type mismatch was not rejected: %v\n%s", err, output)
	}
}
