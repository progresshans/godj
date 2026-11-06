package codegen_test

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"go/format"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/creation_input_test.go.tmpl
var accountCreationInputConsumer string

func TestGeneratedAccountCreationInputPolicy(t *testing.T) {
	inputs, err := identityreference.UserCreationInputs()
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name string }
	if err := json.Unmarshal(inputs, &cases); err != nil || len(cases) != 20 {
		t.Fatal("creation input corpus is incomplete", err)
	}
	for _, custom := range []bool{false, true} {
		variant := "default"
		if custom {
			variant = "renamed"
		}
		t.Run(variant, func(t *testing.T) {
			spec := accountBindingConsumerSpec(t, custom)
			profile := "first_name"
			if custom {
				profile = "region"
				spec.Account.Email, spec.Account.WriteEmailNormalization = "", ""
				spec.Account.Staff = ir.AccountBoolean{Field: "privileged"}
			}
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
					if !custom || identity != spec.Account.Model {
						continue
					}
					for k := range model.Fields {
						field := &model.Fields[k]
						if field.Name == spec.Account.Login {
							field.Kind, field.MaxLength = ir.FieldChar, 256
						}
					}
					model.Fields = append(model.Fields,
						ir.Field{Name: "privileged", GoName: "Privileged", Kind: ir.FieldBoolean, Default: &ir.Scalar{Kind: ir.ScalarBoolean}},
						ir.Field{Name: "username", GoName: "Username", Kind: ir.FieldChar, MaxLength: 64, Default: &ir.Scalar{Kind: ir.ScalarString, String: "unrelated-name"}},
						ir.Field{Name: "password1", GoName: "Password1", Kind: ir.FieldChar, MaxLength: 64, Default: &ir.Scalar{Kind: ir.ScalarString, String: "unrelated-command-name"}})
				}
			}
			bundle, err := codegen.GenerateProject(spec)
			if err != nil {
				t.Fatal(err)
			}
			root := writeProjectBundleModule(t, bundle)
			source := strings.NewReplacer("@@IMPORTS@@", strings.Join(imports, "\n"), "@@SCHEMAS@@", strings.Join(schemas, ","),
				"@@ACCOUNT@@", types[spec.Account.Model], "@@GROUP@@", types[spec.Account.Catalog.Group], "@@PERMISSION@@", types[spec.Account.Catalog.Permission], "@@PROFILE@@", profile).Replace(accountCreationInputConsumer)
			formatted, err := format.Source([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			writeGeneratedTestFile(t, root, "consumer/creation_input_test.go", formatted)
			writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
			writeGeneratedTestFile(t, root, "consumer/testdata/user-creation-inputs.json", inputs)
			for _, vendor := range []string{"sqlite", "postgres"} {
				reference, err := identityreference.UserCreation(vendor)
				if err != nil {
					t.Fatal(err)
				}
				writeGeneratedTestFile(t, root, "consumer/testdata/user-creation-django61-"+vendor+".json", reference)
			}
			required := []string{"TestCreationInputPolicy"}
			for _, surface := range []string{"form", "admin"} {
				modes := []string{"standard", "admin_enabled", "admin_disabled"}
				if surface == "admin" {
					modes = modes[1:]
				}
				for _, mode := range modes {
					prefix := required[0] + "/" + surface + "/" + mode
					required = append(required, prefix)
					for _, probe := range cases {
						required = append(required, prefix+"/"+probe.Name)
					}
					if surface == "form" {
						for _, operation := range []string{"immediate", "deferred", "abandoned", "invalid"} {
							required = append(required, prefix+"/lifecycle_"+operation)
						}
					}
				}
			}
			for _, child := range []string{"stored_limit", "raw_normalization_limit", "forged_length", "forged_grammar", "forged_normalization", "late_duplicate"} {
				required = append(required, required[0]+"/boundaries/"+child)
			}
			for _, operation := range []string{"single_attempt_after_delete", "concurrent_copies", "unadmitted_calls"} {
				for _, mode := range []string{"usable", "unusable"} {
					required = append(required, required[0]+"/prepared_ownership/"+operation+"/"+mode)
				}
			}
			runAccountConsumerDatabases(t, root, required)
		})
	}
}
