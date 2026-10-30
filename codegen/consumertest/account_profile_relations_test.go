package codegen_test

import (
	_ "embed"
	"fmt"
	"go/format"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/profile_relations_test.go.tmpl
var profileRelationsConsumer string

func TestGeneratedAccountProfileRelations(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "default"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			root := writeAccountProfileRelationConsumer(t, custom)
			required := []string{"TestGeneratedProfileRelationLifecycle"}
			for _, child := range []string{"first_command", "bounded_choices", "admin_create", "one_to_one_unique", "json_edit", "current_choice", "partial_form", "nullable_clear", "restart"} {
				required = append(required, required[0]+"/"+child)
			}
			runAccountConsumerDatabases(t, root, required)
		})
	}
}

func writeAccountProfileRelationConsumer(t *testing.T, custom bool) string {
	t.Helper()
	spec := accountBindingConsumerSpec(t, custom)
	if custom {
		*spec.Account.Staff.Constant = true
	}
	profiles, err := schema.Build(schema.Definition{AppLabel: "profiles", Models: []schema.Model{
		{Name: "department", GoName: "Department", Fields: []schema.Field{schema.CharField("title", "Title", 60), schema.BooleanField("enabled", "Enabled", schema.Default(true))}},
		{Name: "badge", GoName: "Badge", Fields: []schema.Field{schema.CharField("code", "Code", 60, schema.Unique())}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: "profiles", Package: codegen.PackageSpec{PackageName: "profiles", ImportPath: "example.com/godj-project-bundle/profiles", Directory: "profiles"}, Schema: profiles})
	var imports, schemas []string
	types := make(map[ir.ModelIdentity]string)
	for i := range spec.Apps {
		app := &spec.Apps[i]
		alias := fmt.Sprintf("model%d", i)
		imports = append(imports, fmt.Sprintf("%s %q", alias, app.Package.ImportPath))
		schemas = append(schemas, alias+".GoDjRelationSchema()")
		for j := range app.Schema.Models {
			model := &app.Schema.Models[j]
			identity := ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}
			types[identity] = alias + "." + model.GoName
			if identity == spec.Account.Model {
				model.Fields = append(model.Fields,
					ir.Field{Name: "department", GoName: "DepartmentID", Column: "department_id", Kind: ir.FieldForeignKey,
						Relation: &ir.ForeignKeyRelation{Target: schema.Target("profiles", "department"), Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteProtect, Reverse: ir.ReverseRelation{Disabled: true}}},
					ir.Field{Name: "badge", GoName: "BadgeID", Column: "badge_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true, Unique: true,
						Relation: &ir.ForeignKeyRelation{Target: schema.Target("profiles", "badge"), Cardinality: ir.RelationOneToOne, OnDelete: ir.DeleteSetNull, Reverse: ir.ReverseRelation{Disabled: true}}})
			}
		}
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	profile := "first_name"
	if custom {
		profile = "region"
	}
	// The independent migration consumer applies target tables before their required FK.
	schemas = append([]string{schemas[len(schemas)-1]}, schemas[:len(schemas)-1]...)
	departmentType := types[schema.Target("profiles", "department")]
	badgeType := types[schema.Target("profiles", "badge")]
	prefix := strings.TrimSuffix(departmentType, "Department")
	source := strings.NewReplacer("@@IMPORTS@@", strings.Join(imports, "\n"), "@@SCHEMAS@@", strings.Join(schemas, ","),
		"@@ACCOUNT@@", types[spec.Account.Model], "@@GROUP@@", types[spec.Account.Catalog.Group], "@@PERMISSION@@", types[spec.Account.Catalog.Permission],
		"@@DEPARTMENT@@", departmentType, "@@BADGE@@", badgeType, "@@PROFILES@@", prefix, "@@PROFILE@@", profile).Replace(profileRelationsConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/profile_relations_test.go", formatted)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	return root
}
