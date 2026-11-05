package codegen_test

import (
	_ "embed"
	"fmt"
	"go/format"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/migrationautodetect"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/systemstate"
)

//go:embed testdata/accountbinding/historical_handoff_test.go.tmpl
var accountHistoricalHandoffConsumer string

func TestGeneratedHistoricalAccountHandoff(t *testing.T) {
	spec, targetRole := historicalHandoffSpec(t)
	legacy := historicalHandoffVariant(spec, "legacy")
	contracted := historicalHandoffVariant(spec, "contracted")
	contracted.Account = &targetRole
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	target := spec
	target.Project = codegen.PackageSpec{PackageName: "selected", ImportPath: "example.com/godj-project-bundle/selected", Directory: "selected"}
	target.Account = &targetRole
	target.Apps = slices.Clone(spec.Apps)
	for index := range target.Apps {
		target.Apps[index].External = true
		target.Apps[index].Package.Directory = ""
	}
	for _, variant := range []codegen.ProjectSpec{legacy, target, contracted} {
		generated, err := codegen.GenerateProject(variant)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range generated.Files() {
			if variant.Project.Directory == "selected" && !strings.HasPrefix(file.Path, "selected/") {
				t.Fatal("selected host attempted to regenerate an imported app", file.Path)
			}
			writeGeneratedTestFile(t, root, file.Path, file.Source())
		}
	}
	stages := historicalHandoffMigrations(t, legacy, spec, contracted)
	source := strings.NewReplacer("@@LEGACY@@", stages[0], "@@EXPANDED@@", stages[1], "@@CONTRACTED@@", stages[2]).Replace(accountHistoricalHandoffConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/historical_handoff_test.go", formatted)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	required := []string{"TestHistoricalAccountOperatorFlow"}
	for _, name := range []string{"lifecycle", "signed_key", "handoff_source_changed", "transition_source_changed", "handoff_response_lost", "transition_response_lost"} {
		required = append(required, "TestHistoricalAccountOperatorFlow/"+name)
	}
	runAccountConsumerDatabases(t, root, required, "TestHistoricalAccountOperatorFlow")
}

func historicalHandoffSpec(t *testing.T) (codegen.ProjectSpec, ir.AccountRole) {
	t.Helper()
	spec := accountBindingConsumerSpec(t, false)
	// The source really is the independently generated built-in app. Recreating
	// similar fields in another package would not exercise its historical ABI.
	spec.Apps[0].External = true
	spec.Apps[0].Package = codegen.PackageSpec{PackageName: "models", ImportPath: "github.com/progresshans/godj/identity/models"}
	var member ir.Model
	for _, model := range spec.Apps[0].Schema.Models {
		if model.Name == spec.Account.Model.ModelName {
			member = model.Clone()
		}
	}
	member.Name, member.GoName, member.DBTable = "member", "Member", "members_member"
	names := map[string][2]string{"username": {"handle", "Handle"}, "principal_id": {"subject", "Subject"}, "encoded_password": {"secret_hash", "SecretHash"}, "revision": {"version", "Version"}}
	for index := range member.Fields {
		field := &member.Fields[index]
		if renamed, ok := names[field.Name]; ok {
			field.Name, field.Column, field.GoName = renamed[0], renamed[0], renamed[1]
		}
	}
	for index := range member.ManyToMany {
		member.ManyToMany[index].Reverse = ir.ReverseRelation{Disabled: true}
	}
	member.Fields = append(member.Fields, ir.Field{Name: "region", GoName: "Region", Column: "region", Kind: ir.FieldChar, MaxLength: 32})
	spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: "members", Package: codegen.PackageSpec{PackageName: "members", ImportPath: "example.com/godj-project-bundle/members", Directory: "members"}, Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "members", Models: []ir.Model{member}}})
	addAccountTransitionHostModels(&spec)
	role := spec.Account.Clone()
	role.Model = ir.ModelIdentity{AppLabel: "members", ModelName: "member"}
	role.Login, role.PrincipalID, role.Password, role.Revision = "handle", "subject", "secret_hash", "version"
	return spec, role
}

func historicalHandoffVariant(spec codegen.ProjectSpec, phase string) codegen.ProjectSpec {
	result := spec
	result.Project = codegen.PackageSpec{PackageName: "project", Directory: phase + "/project", ImportPath: "example.com/godj-project-bundle/" + phase + "/project"}
	result.Apps = slices.Clone(spec.Apps)
	if phase == "legacy" {
		result.Apps = slices.DeleteFunc(result.Apps, func(app codegen.AppSpec) bool { return app.Schema.AppLabel == "members" })
	}
	for index := range result.Apps {
		app := &result.Apps[index]
		if app.Schema.AppLabel != "host" {
			app.External = true
			app.Package.Directory = ""
			continue
		}
		app.Package.Directory, app.Package.ImportPath = phase+"/host", "example.com/godj-project-bundle/"+phase+"/host"
		app.Schema = app.Schema.Clone()
		for index := range app.Schema.Models {
			model := &app.Schema.Models[index]
			model.Fields = slices.DeleteFunc(model.Fields, func(field ir.Field) bool {
				if phase == "contracted" {
					return strings.HasPrefix(field.Name, "old_")
				}
				return field.Relation != nil && field.Relation.Target == (ir.ModelIdentity{AppLabel: "members", ModelName: "member"})
			})
			model.ManyToMany = slices.DeleteFunc(model.ManyToMany, func(field ir.ManyToManyField) bool {
				if phase == "contracted" {
					return field.Name == "legacy_members"
				}
				return field.Name == "members"
			})
		}
	}
	return result
}

func historicalHandoffMigrations(t *testing.T, specs ...codegen.ProjectSpec) []string {
	t.Helper()
	// Keep the actual historical names/bytes, including all applied revisions.
	// Only the host/member apps are added and evolved by this external consumer.
	sources := systemstate.IdentityMigrationSources()
	var result []string
	for _, spec := range specs {
		var schemas []ir.Schema
		for _, app := range spec.Apps {
			schemas = append(schemas, app.Schema)
		}
		desired, err := migrations.NewProjectState(schemas...)
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(sources...)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := migrationautodetect.Detect(migrationautodetect.Request{Definitions: loaded, Desired: desired, ManagedApps: desired.Apps()})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Migrations()) == 0 {
			t.Fatal("historical host migration phase has no change")
		}
		for _, migration := range plan.Migrations() {
			if migration.App == "godj_identity" {
				t.Fatal("consumer rewrote historical identity instead of importing it")
			}
			wire, err := definition.Encode(definition.Producer{Name: "historical-account-consumer", Version: "1"}, migration)
			if err != nil {
				t.Fatal(err)
			}
			sources = append(sources, definition.Source{SourceID: migration.App + "/" + migration.Name, Document: wire})
		}
		var literals []string
		for _, source := range sources {
			literals = append(literals, fmt.Sprintf("definition.Source{SourceID:%q,Document:[]byte(%q)}", source.SourceID, source.Document))
		}
		result = append(result, strings.Join(literals, ","))
	}
	return result
}
