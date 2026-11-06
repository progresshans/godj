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

//go:embed testdata/accountbinding/profile_files_test.go.tmpl
var profileFilesConsumer string

//go:embed testdata/accountbinding/profile_files_support.go.tmpl
var profileFilesSupport string

func TestGeneratedAccountProfileFiles(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "default"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			root := writeAccountProfileFileConsumer(t, custom)
			required := []string{"TestGeneratedProfileFileLifecycle"}
			for _, child := range []string{"construction", "typed_edit", "admin_upload", "keep_clear", "image_choices", "json_read_only", "combined_roles", "final_authority", "final_revision", "failures", "concurrent_revision", "signed_keys", "restart"} {
				required = append(required, required[0]+"/"+child)
			}
			for _, child := range []string{"stale", "denied", "invalid_image", "read_cleanup", "not_published", "published_cleanup", "uncertain", "partial", "unique", "validator", "audit", "write_cleanup", "unknown_commit", "canceled_after_publication", "reporter"} {
				required = append(required, required[0]+"/failures/"+child)
			}
			runAccountConsumerDatabases(t, root, required)
		})
	}
}

func writeAccountProfileFileConsumer(t *testing.T, custom bool) string {
	return writeAccountProfileFileSource(t, custom, false, profileFilesConsumer)
}

func writeAccountProfileFileSource(t *testing.T, custom, requiredFiles bool, body string) string {
	t.Helper()
	spec := accountBindingConsumerSpec(t, custom)
	if custom {
		*spec.Account.Staff.Constant = true
	}
	extra, err := schema.Build(schema.Definition{AppLabel: "profile_fields", Models: []schema.Model{{Name: "fields", GoName: "Fields", Fields: []schema.Field{
		schema.FileField("document", "Document", schema.Blank(), schema.Nullable(), schema.Unique()),
		schema.FileField("second_document", "SecondDocument", schema.Blank(), schema.Nullable()),
		schema.ImageField("portrait", "Portrait", schema.Nullable(), schema.Blank(), schema.ImageDimensions("portrait_width", "portrait_height")),
		schema.IntegerField("portrait_width", "PortraitWidth", schema.Nullable(), schema.Editable(false)),
		schema.IntegerField("portrait_height", "PortraitHeight", schema.Nullable(), schema.Editable(false)),
		schema.ImageField("preset", "Preset", schema.Nullable(), schema.Blank(), schema.ImageDimensions("preset_width", "preset_height"),
			schema.Choices(schema.Choice("presets/first.png", "First"), schema.Choice("presets/second.png", "Second"))),
		schema.IntegerField("preset_width", "PresetWidth", schema.Nullable(), schema.Editable(false)),
		schema.IntegerField("preset_height", "PresetHeight", schema.Nullable(), schema.Editable(false)),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	if requiredFiles {
		for i := range extra.Models[0].Fields {
			field := &extra.Models[0].Fields[i]
			if field.Name == "document" || field.Name == "portrait" {
				field.Blank, field.Nullable = false, false
			}
		}
	}
	var imports, schemas []string
	if requiredFiles {
		imports = append(imports, `accountdomain "github.com/progresshans/godj/account/domain"`, `"github.com/progresshans/godj/project/firstaccount"`, `"runtime"`)
	}
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
				for _, field := range extra.Models[0].Fields {
					if !field.PrimaryKey {
						model.Fields = append(model.Fields, field.Clone())
					}
				}
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
	source := strings.NewReplacer("@@IMPORTS@@", strings.Join(imports, "\n"), "@@SCHEMAS@@", strings.Join(schemas, ","),
		"@@ACCOUNT@@", types[spec.Account.Model], "@@GROUP@@", types[spec.Account.Catalog.Group], "@@PERMISSION@@", types[spec.Account.Catalog.Permission], "@@PROFILE@@", profile).Replace(profileFilesSupport + body)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/profile_files_test.go", formatted)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	return root
}
