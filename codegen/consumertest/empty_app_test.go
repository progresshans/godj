package codegen_test

import (
	"bytes"
	"fmt"
	"path"
	"slices"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedProjectSupportsEmptyAppsAndLaterModels(t *testing.T) {
	for _, scenario := range []string{"no_apps", "empty_only", "external_empty", "mixed_relations", "mixed_collections", "first_model"} {
		t.Run(scenario, func(t *testing.T) {
			spec := projectBundleTestSpec()
			empty := codegen.AppSpec{
				Alias:   "notifications",
				Package: codegen.PackageSpec{PackageName: "notifications", ImportPath: "example.com/godj-project-bundle/notifications", Directory: "notifications"},
				Schema:  ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "notifications"},
			}
			switch scenario {
			case "no_apps":
				spec.Apps = nil
			case "empty_only", "external_empty", "first_model":
				spec.Apps = []codegen.AppSpec{empty}
			default:
				spec.Apps = append(spec.Apps, empty)
			}
			if scenario == "external_empty" {
				spec.Apps[0].External = true
				spec.Apps[0].Package.Directory = ""
			}
			if scenario == "first_model" {
				spec.Apps[0].Schema.Models = []ir.Model{{Name: "notice", GoName: "Notice", Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 80}}}}
			}
			if scenario == "mixed_collections" {
				spec.Apps[0].Schema.Models[0].ManyToMany = []ir.ManyToManyField{{
					Name: "fans", GoName: "Fans", Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
					Reverse: ir.ReverseRelation{Name: "favourite_posts"},
				}}
			}
			bundle, err := codegen.GenerateProject(spec)
			if err != nil {
				t.Fatal(err)
			}
			permuted := spec
			permuted.Apps = slices.Clone(spec.Apps)
			slices.Reverse(permuted.Apps)
			other, err := codegen.GenerateProject(permuted)
			if err != nil || !bytes.Equal(bundle.Manifest(), other.Manifest()) {
				t.Fatalf("app order changed canonical output: %v", err)
			}
			root := writeProjectBundleModule(t, bundle)
			if scenario == "external_empty" {
				writeEmptyAppCompanions(t, root, empty)
			}
			imports, modelCheck := "", ""
			if scenario != "no_apps" {
				imports = `notifications "example.com/godj-project-bundle/notifications"`
				count := 0
				if scenario == "first_model" {
					count = 1
				}
				modelCheck = fmt.Sprintf(`if got := notifications.GoDjRelationSchema(); got.AppLabel != "notifications" || len(got.Models) != %d { t.Fatalf("notification schema: %%+v", got) }`, count)
				if scenario == "first_model" {
					modelCheck += `
if _, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel:"notifications", ModelName:"notice"}, notifications.NoticeDescriptor{}); err != nil { t.Fatal(err) }
_ = notifications.NoticeObjects
`
					imports += "\n\"github.com/progresshans/godj/orm\"\n\"github.com/progresshans/godj/schema/ir\""
				}
			}
			want := "[]apps.Config{"
			ordered := slices.Clone(spec.Apps)
			slices.SortFunc(ordered, func(a, b codegen.AppSpec) int { return bytes.Compare([]byte(a.Alias), []byte(b.Alias)) })
			for _, app := range ordered {
				want += fmt.Sprintf("{Name:%q,Label:%q},", app.Package.ImportPath, app.Schema.AppLabel)
			}
			want += "}"
			writeGeneratedTestFile(t, root, "consumer/inventory_test.go", []byte(fmt.Sprintf(`package consumer
import (
 "reflect"
 "testing"
 "github.com/progresshans/godj/apps"
 "github.com/progresshans/godj/settings"
 project "example.com/godj-project-bundle/project"
 %s
)
func TestInstalledAppsAndBinding(t *testing.T) {
 want := %s
 got := project.InstalledApps()
 if !reflect.DeepEqual(got, want) { t.Fatalf("inventory: %%+v, want %%+v", got, want) }
 configured, err := settings.New(settings.Definition{ProjectName:"empty-app-consumer", InstalledApps:got})
 if err != nil { t.Fatal(err) }
 for _, app := range want {
   if value, ok := configured.Apps().Lookup(app.Label); !ok || value != app { t.Fatalf("installed app lost: %%+v %%v", value, ok) }
 }
 if len(got)>0 { got[0].Label = "mutated"; got[0].Name = "mutated" }
 if !reflect.DeepEqual(project.InstalledApps(), want) { t.Fatal("caller changed generated inventory") }
 for _, app := range want {
   if value, ok := configured.Apps().Lookup(app.Label); !ok || value != app { t.Fatal("caller changed settings snapshot") }
 }
 binding, err := project.Bind()
 if err != nil { t.Fatal(err) }
 _ = binding
 if _, err := project.BindRelations(); err != nil { t.Fatal(err) }
 if _, err := project.BindObjects(); err != nil { t.Fatal(err) }
 if _, err := project.BindRelationDeleters(); err != nil { t.Fatal(err) }
 if _, err := project.Using(nil); err == nil { t.Fatal("empty project bypassed backend validation") }
 %s
}
`, imports, want, modelCheck)))
			output, err := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-v", "./...").CombinedOutput()
			if err != nil || !bytes.Contains(output, []byte("--- PASS: TestInstalledAppsAndBinding")) {
				t.Fatalf("external inventory consumer did not execute successfully: %v\n%s", err, output)
			}
		})
	}
}

func TestGeneratedFirstModelRejectsStaleEmptyAppCompanions(t *testing.T) {
	spec := projectBundleTestSpec()
	empty := spec.Apps[0]
	empty.Schema.Models = nil
	spec.Apps = []codegen.AppSpec{empty}
	before, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Apps[0].Schema.Models = []ir.Model{{Name: "notice", GoName: "Notice"}}
	after, err := codegen.GenerateProject(spec)
	if err != nil || before.SnapshotSHA256() == after.SnapshotSHA256() {
		t.Fatalf("first model did not change snapshot: %v", err)
	}
	root := writeProjectBundleModule(t, after)
	if output, err := compileProjectBundleModule(t.Context(), root); err != nil {
		t.Fatalf("first model's complete bundle: %v\n%s", err, output)
	}
	for _, file := range before.Files() {
		if file.Owner == "app:"+empty.Schema.AppLabel && path.Base(file.Path) == "zz_godj_relation.go" {
			writeGeneratedTestFile(t, root, file.Path, file.Source())
			break
		}
	}
	if output, err := compileProjectBundleModule(t.Context(), root); err == nil || !bytes.Contains(output, []byte("GoDjProjectSnapshot_")) {
		t.Fatalf("stale empty metadata was not rejected by the snapshot boundary: %v\n%s", err, output)
	}
}

func writeEmptyAppCompanions(t *testing.T, root string, app codegen.AppSpec) {
	t.Helper()
	for _, part := range []struct {
		name     string
		generate func(string, ir.Schema) ([]byte, error)
	}{
		{"zz_godj_generated.go", codegen.Generate},
		{"zz_godj_relation.go", codegen.GenerateRelationMetadata},
		{"zz_godj_relation_object.go", codegen.GenerateRelationObject},
		{"zz_godj_relation_projection.go", codegen.GenerateRelationProjection},
	} {
		source, err := part.generate(app.Package.PackageName, app.Schema)
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, path.Join(app.Package.Directory, part.name), source)
	}
}
