package codegen

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestEmptyAppsRetainNamespaceAndMissingModelValidation(t *testing.T) {
	t.Parallel()
	base := projectBundleTestSpec()
	base.Apps = []AppSpec{{
		Alias:   "notifications",
		Package: PackageSpec{PackageName: "notifications", ImportPath: "example.com/godj-project-bundle/notifications", Directory: "notifications"},
		Schema:  ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "notifications"},
	}}
	good, err := GenerateProject(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"duplicate_label", "duplicate_import", "duplicate_alias", "missing_relation_target"} {
		t.Run(scenario, func(t *testing.T) {
			spec := base
			spec.Apps = append([]AppSpec(nil), base.Apps...)
			other := base.Apps[0]
			other.Alias = "other"
			other.Package = PackageSpec{PackageName: "other", ImportPath: "example.com/godj-project-bundle/other", Directory: "other"}
			other.Schema = ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "other"}
			switch scenario {
			case "duplicate_label":
				other.Schema.AppLabel = "notifications"
			case "duplicate_import":
				other.Package.ImportPath = base.Apps[0].Package.ImportPath
			case "duplicate_alias":
				other.Alias = base.Apps[0].Alias
			case "missing_relation_target":
				other.Schema.Models = []ir.Model{{Name: "delivery", GoName: "Delivery", Fields: []ir.Field{{
					Name: "notice", GoName: "NoticeID", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "notifications", ModelName: "notice"},
						Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Name: "deliveries"}, OnDelete: ir.DeleteProtect},
				}}}}
			}
			spec.Apps = append(spec.Apps, other)
			candidate, err := GenerateProject(spec)
			if err == nil || candidate.SnapshotSHA256() != "" || len(candidate.Files()) != 0 || len(candidate.Manifest()) != 0 {
				t.Fatalf("invalid empty-app composition returned a partial candidate: %v", err)
			}
		})
	}
	after, err := GenerateProject(base)
	if err != nil || !bytes.Equal(after.Manifest(), good.Manifest()) {
		t.Fatalf("failed compositions changed original declaration: %v", err)
	}
}
