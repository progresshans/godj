package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/schema/ir"
)

func TestProjectSnapshotCanonicalizesAppsButPreservesSchemaOrder(t *testing.T) {
	firstSpec := projectBundleTestSpec()
	first, err := normalizeProjectSpec(firstSpec)
	if err != nil {
		t.Fatalf("normalizeProjectSpec() error = %v", err)
	}
	firstBytes, firstSHA, err := projectSnapshot(first)
	if err != nil {
		t.Fatalf("projectSnapshot() error = %v", err)
	}
	permutedSpec := projectBundleTestSpec()
	permutedSpec.Apps[0], permutedSpec.Apps[1] = permutedSpec.Apps[1], permutedSpec.Apps[0]
	permuted, err := normalizeProjectSpec(permutedSpec)
	if err != nil {
		t.Fatalf("normalizeProjectSpec(permuted) error = %v", err)
	}
	permutedBytes, permutedSHA, err := projectSnapshot(permuted)
	if err != nil {
		t.Fatalf("projectSnapshot(permuted) error = %v", err)
	}
	if firstSHA != permutedSHA || !bytes.Equal(firstBytes, permutedBytes) {
		t.Fatal("app permutation changed canonical snapshot")
	}

	fieldReorderedSpec := projectBundleTestSpec()
	fields := fieldReorderedSpec.Apps[0].Schema.Models[0].Fields
	fields[0], fields[1] = fields[1], fields[0]
	fieldReordered, err := normalizeProjectSpec(fieldReorderedSpec)
	if err != nil {
		t.Fatalf("normalizeProjectSpec(field reordered) error = %v", err)
	}
	_, fieldSHA, err := projectSnapshot(fieldReordered)
	if err != nil {
		t.Fatalf("projectSnapshot(field reordered) error = %v", err)
	}
	if fieldSHA == firstSHA {
		t.Fatal("semantic field order change did not change project snapshot")
	}
}

func TestProjectSpecRejectsInvalidAndDuplicatePackageIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProjectSpec)
		want   string
	}{
		{name: "dot godj", mutate: func(spec *ProjectSpec) { spec.Project.Directory = ".godj" }, want: "package directory"},
		{name: "case folded dot godj", mutate: func(spec *ProjectSpec) { spec.Project.Directory = ".GODJ/project" }, want: "package directory"},
		{name: "dot godj child", mutate: func(spec *ProjectSpec) { spec.Project.Directory = ".godj/project" }, want: "package directory"},
		{name: "unclean directory", mutate: func(spec *ProjectSpec) { spec.Project.Directory = "project/../other" }, want: "package directory"},
		{name: "backslash directory", mutate: func(spec *ProjectSpec) { spec.Project.Directory = `project\\other` }, want: "package directory"},
		{name: "unicode directory", mutate: func(spec *ProjectSpec) { spec.Project.Directory = "prøjëct" }, want: "package directory"},
		{name: "space directory", mutate: func(spec *ProjectSpec) { spec.Project.Directory = "project files" }, want: "package directory"},
		{name: "control directory", mutate: func(spec *ProjectSpec) { spec.Project.Directory = "project\nfiles" }, want: "package directory"},
		{name: "wire string", mutate: func(spec *ProjectSpec) {
			spec.Project.PackageName = strings.Repeat("p", maxProjectWireStringBytes+1)
		}, want: "exceeds 4096 bytes"},
		{name: "generated path", mutate: func(spec *ProjectSpec) {
			spec.Project.Directory = strings.Repeat("p", maxProjectWireStringBytes)
		}, want: "generated output path exceeds 4096 bytes"},
		{name: "derived owner", mutate: func(spec *ProjectSpec) {
			spec.Apps[0].Schema.AppLabel = strings.Repeat("a", maxProjectWireStringBytes)
		}, want: "owner exceeds 4096 bytes"},
		{name: "duplicate alias", mutate: func(spec *ProjectSpec) { spec.Apps[1].Alias = spec.Apps[0].Alias }, want: "duplicate project app alias"},
		{name: "duplicate import", mutate: func(spec *ProjectSpec) { spec.Apps[1].Package.ImportPath = spec.Apps[0].Package.ImportPath }, want: "duplicate project import path"},
		{name: "project import", mutate: func(spec *ProjectSpec) { spec.Apps[0].Package.ImportPath = spec.Project.ImportPath }, want: "duplicate project import path"},
		{name: "case folded directory", mutate: func(spec *ProjectSpec) {
			spec.Apps[1].Package.Directory = strings.ToUpper(spec.Apps[0].Package.Directory)
		}, want: "duplicate project package directory"},
		{name: "duplicate app label", mutate: func(spec *ProjectSpec) { spec.Apps[1].Schema.AppLabel = spec.Apps[0].Schema.AppLabel }, want: "duplicate project app label"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := projectBundleTestSpec()
			test.mutate(&spec)
			bundle, err := GenerateProject(spec)
			if err == nil {
				t.Fatal("GenerateProject() accepted invalid project spec")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("GenerateProject() error = %q, want substring %q", err, test.want)
			}
			if bundle.SnapshotSHA256() != "" || len(bundle.Files()) != 0 || len(bundle.Manifest()) != 0 {
				t.Fatal("GenerateProject() returned partial bundle on validation failure")
			}
		})
	}
}

func TestProjectSpecProducerLimitsUseExactBoundaries(t *testing.T) {
	if err := validateProjectWireString("boundary", strings.Repeat("a", maxProjectWireStringBytes)); err != nil {
		t.Fatalf("maximum wire string rejected: %v", err)
	}
	if err := validateProjectWireString("boundary", strings.Repeat("a", maxProjectWireStringBytes+1)); err == nil {
		t.Fatal("wire string above maximum was accepted")
	}
	if err := validateProjectAppCount(maxProjectApps); err != nil {
		t.Fatalf("maximum app count rejected: %v", err)
	}
	if err := validateProjectAppCount(maxProjectApps + 1); err == nil {
		t.Fatal("app count above maximum was accepted")
	}
	if _, err := normalizeProjectSpec(ProjectSpec{
		Project: projectBundleTestSpec().Project,
		Apps:    make([]AppSpec, maxProjectApps+1),
	}); err == nil || !strings.Contains(err.Error(), "project app count") {
		t.Fatalf("app count above maximum error = %v", err)
	}
}

func TestProjectSpecSchemaResourcesPrecedeNormalizationAndRendering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProjectSpec)
		want   string
	}{
		{
			name: "models per app",
			mutate: func(spec *ProjectSpec) {
				spec.Apps[0].Schema.FormatVersion = 0
				spec.Apps[0].Schema.Models = make([]ir.Model, projectspec.MaxModelsPerApp+1)
			},
			want: "models_per_app",
		},
		{
			name: "fields per model",
			mutate: func(spec *ProjectSpec) {
				spec.Apps[0].Schema.FormatVersion = 0
				spec.Apps[0].Schema.Models[0].Fields = make([]ir.Field, projectspec.MaxFieldsPerModel+1)
			},
			want: "fields_per_model",
		},
		{
			name: "nested schema string",
			mutate: func(spec *ProjectSpec) {
				spec.Apps[0].Schema.FormatVersion = 0
				spec.Apps[0].Schema.Models[0].Fields[0].Default = &ir.ScalarDefault{
					Kind: ir.ScalarString, String: strings.Repeat("x", projectspec.MaxSchemaStringBytes+1),
				}
			},
			want: "schema_string_bytes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := projectBundleTestSpec()
			test.mutate(&spec)
			bundle, err := GenerateProject(spec)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("GenerateProject() error = %v, want %q", err, test.want)
			}
			if bundle.SnapshotSHA256() != "" || len(bundle.Files()) != 0 || len(bundle.Manifest()) != 0 {
				t.Fatal("schema resource failure returned a partial bundle")
			}
		})
	}
}

func TestProjectDirectoryUsesPortableImportPathCharacterSet(t *testing.T) {
	maximumDirectory := strings.TrimSuffix(strings.Repeat("a/", maxProjectGeneratedPathDepth-1), "/")
	for _, directory := range []string{
		".",
		"project",
		"apps/blog-v1",
		"apps/blog.v1",
		"apps/blog_v1",
		"apps/blog~tools",
		"apps/blog+tools",
		maximumDirectory,
	} {
		if !validProjectDirectory(directory) {
			t.Errorf("validProjectDirectory(%q) = false", directory)
		}
	}
	for _, directory := range []string{
		".godj",
		".GODJ/owned",
		"apps/한글",
		"apps/with space",
		"apps/with:colon",
		"apps/with\\backslash",
		"apps/with\x00nul",
		"apps/blog~1",
		maximumDirectory + "/a",
	} {
		if validProjectDirectory(directory) {
			t.Errorf("validProjectDirectory(%q) = true", directory)
		}
	}
}

func TestProjectSpecRejectsCrossRendererNamespaceCollision(t *testing.T) {
	spec := projectBundleTestSpec()
	spec.Apps[0].Alias = "a"
	spec.Apps[0].Schema.Models[0].GoName = "BC"
	spec.Apps[1].Alias = "aB"
	spec.Apps[1].Schema.Models[0].GoName = "C"
	bundle, err := GenerateProject(spec)
	if err == nil {
		t.Fatal("GenerateProject() accepted colliding project namespace")
	}
	if bundle.SnapshotSHA256() != "" || len(bundle.Files()) != 0 || len(bundle.Manifest()) != 0 {
		t.Fatal("namespace failure returned a partial bundle")
	}
}

func TestProjectSnapshotChangesWithSchemaLayoutAndGeneratorABIInputs(t *testing.T) {
	baseline, err := normalizeProjectSpec(projectBundleTestSpec())
	if err != nil {
		t.Fatalf("normalizeProjectSpec() error = %v", err)
	}
	_, baselineSHA, err := projectSnapshot(baseline)
	if err != nil {
		t.Fatalf("projectSnapshot() error = %v", err)
	}
	changes := []func(*ProjectSpec){
		func(spec *ProjectSpec) { spec.Apps[0].Schema.Models[0].Fields[0].MaxLength++ },
		func(spec *ProjectSpec) { spec.Apps[0].Package.Directory = "content" },
		func(spec *ProjectSpec) { spec.Apps[0].Package.ImportPath += "-next" },
		func(spec *ProjectSpec) { spec.Project.PackageName = "generatedproject" },
	}
	for index, mutate := range changes {
		spec := projectBundleTestSpec()
		mutate(&spec)
		normalized, err := normalizeProjectSpec(spec)
		if err != nil {
			t.Fatalf("normalizeProjectSpec(change %d) error = %v", index, err)
		}
		_, changedSHA, err := projectSnapshot(normalized)
		if err != nil {
			t.Fatalf("projectSnapshot(change %d) error = %v", index, err)
		}
		if changedSHA == baselineSHA {
			t.Fatalf("snapshot input change %d did not change digest", index)
		}
	}
	if got := len(projectGeneratorABIRoster()); got != 13 {
		t.Fatalf("len(projectGeneratorABIRoster()) = %d, want 13", got)
	}
	abi := projectGeneratorABIRoster()
	abi[0].Version += "-next"
	_, changedABI, err := projectSnapshotWithABI(baseline, abi)
	if err != nil {
		t.Fatalf("projectSnapshotWithABI() error = %v", err)
	}
	if changedABI == baselineSHA {
		t.Fatal("generator ABI change did not change project snapshot")
	}
}
