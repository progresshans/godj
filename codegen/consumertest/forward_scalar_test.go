package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedForwardScalarConsumer(t *testing.T) {
	fields := []schema.Field{schema.TextField("label", "Label")}
	kinds := []struct {
		name, goName string
		field        func(string, string, ...schema.FieldOption) schema.Field
	}{
		{"integer", "Integer", schema.IntegerField}, {"text", "Text", schema.TextField}, {"boolean", "Boolean", schema.BooleanField},
		{"float", "Float", schema.FloatField}, {"decimal", "Decimal", func(n, g string, o ...schema.FieldOption) schema.Field { return schema.DecimalField(n, g, 30, 6, o...) }},
		{"datetime", "DateTime", schema.DateTimeField}, {"date", "Date", schema.DateField}, {"time", "Time", schema.TimeField},
		{"duration", "Duration", schema.DurationField}, {"uuid", "UUID", schema.UUIDField}, {"json", "JSON", schema.JSONField},
	}
	for _, prefix := range []string{"v_", "n_"} {
		options := []schema.FieldOption{}
		goPrefix := "V"
		if prefix == "n_" {
			options = append(options, schema.Nullable())
			goPrefix = "N"
		}
		for _, kind := range kinds {
			fields = append(fields, kind.field(prefix+kind.name, goPrefix+kind.goName, options...))
		}
	}
	definition, err := schema.Build(schema.Definition{AppLabel: "scalarref", Models: []schema.Model{
		{Name: "datum", GoName: "Datum", Fields: fields},
		{Name: "holder", GoName: "Holder", Fields: []schema.Field{
			schema.ForeignKey("primary", "PrimaryID", schema.Target("scalarref", "datum"), schema.RelatedName("primary_holders"), schema.Protect),
			schema.ForeignKey("secondary", "SecondaryID", schema.Target("scalarref", "datum"), schema.RelatedName("secondary_holders"), schema.SetNull, schema.Nullable()),
		}},
		{Name: "entry", GoName: "Entry", Fields: []schema.Field{
			schema.TextField("label", "Label"),
			schema.ForeignKey("primary", "PrimaryID", schema.Target("scalarref", "holder"), schema.RelatedName("primary_entries"), schema.Protect),
			schema.ForeignKey("secondary", "SecondaryID", schema.Target("scalarref", "holder"), schema.RelatedName("secondary_entries"), schema.SetNull, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-forward-scalar"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	for _, name := range []string{"consumer_test.go", "scalars_test.go"} {
		source, err := os.ReadFile(filepath.Join("testdata/forwardscalar", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, source)
	}
	for _, backend := range []string{"sqlite", "postgres"} {
		raw, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "orm/testdata/forward-scalar-django61-"+backend+".json"))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+backend+"_reference.json", raw)
	}
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	required := []string{"TestForwardScalarSelections", "TestForwardScalarSelections/sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestForwardScalarSelections/postgres")
	}
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
	for _, input := range []struct{ name, source, fragment string }{
		{"related values stay nullable", `package wrong;import "example.com/godj-forward-scalar/models";import "example.com/godj-forward-scalar/project";import "github.com/progresshans/godj/orm";func bad(){r,_:=project.BindRelations();_=orm.Project1(r.ModelsHolder.Primary.VInteger,func(value int64)int64{return value});_=models.Holder{}}`, "*int64"},
		{"target field cannot become source", `package wrong;import "example.com/godj-forward-scalar/models";import "example.com/godj-forward-scalar/project";import "github.com/progresshans/godj/orm";func bad(){r,_:=project.BindRelations();_=orm.Project2(r.ModelsHolder.Primary.VText,models.DatumFields.Label,func(a *string,b string)string{return b})}`, "does not match"},
	} {
		t.Run(input.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, "wrong/wrong.go", []byte(input.source))
			output, err := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./wrong").CombinedOutput()
			if err == nil || !strings.Contains(string(output), input.fragment) {
				t.Fatalf("invalid typed result compiled or failed outside expected boundary: %v\n%s", err, output)
			}
		})
	}
}
