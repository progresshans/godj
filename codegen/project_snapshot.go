package codegen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/schema/ir"
)

const (
	projectBundleGeneratorVersion    = "godj-codegen-project-bundle-v1"
	maxProjectApps                   = 8192
	maxProjectGeneratedFiles         = 65536
	maxProjectGeneratorABIEntries    = 64
	maxProjectWireStringBytes        = 4096
	maxProjectGeneratedPathBytes     = 4096
	maxProjectGeneratedPathDepth     = 64
	maxProjectManifestBytes          = 16 << 20
	maxProjectGeneratedSourceBytes   = 64 << 20
	projectGeneratedFilesPerApp      = 4
	projectGeneratedProjectFileCount = 8
)

type projectGeneratorABI struct {
	Role     string `json:"role"`
	Filename string `json:"filename"`
	Version  string `json:"version"`
}

type projectPackageDocument struct {
	PackageName string `json:"package_name"`
	ImportPath  string `json:"import_path"`
	Directory   string `json:"directory"`
}

type normalizedProjectSpec struct {
	project PackageSpec
	apps    []AppSpec
}

type projectSnapshotApp struct {
	Alias   string                 `json:"alias"`
	Package projectPackageDocument `json:"package"`
	Schema  ir.Schema              `json:"schema"`
}

type projectSnapshotDocument struct {
	FormatVersion int                    `json:"format_version"`
	GeneratorABI  []projectGeneratorABI  `json:"generator_abi"`
	Project       projectPackageDocument `json:"project"`
	Apps          []projectSnapshotApp   `json:"apps"`
}

func projectGeneratorABIRoster() []projectGeneratorABI {
	return []projectGeneratorABI{
		{Role: "bundle", Filename: GeneratedManifestPath, Version: projectBundleGeneratorVersion},
		{Role: "app.main", Filename: "zz_godj_generated.go", Version: GeneratorVersion},
		{Role: "app.relation_metadata", Filename: "zz_godj_relation.go", Version: RelationMetadataGeneratorVersion},
		{Role: "app.relation_object", Filename: "zz_godj_relation_object.go", Version: RelationObjectGeneratorVersion},
		{Role: "app.relation_projection", Filename: "zz_godj_relation_projection.go", Version: RelationProjectionGeneratorVersion},
		{Role: "project.bindings", Filename: "zz_godj_bindings.go", Version: projectBindingGeneratorVersion},
		{Role: "project.relation_query", Filename: "zz_godj_relation_query.go", Version: ProjectRelationQueryGeneratorVersion},
		{Role: "project.relation_object", Filename: "zz_godj_relation_object.go", Version: ProjectRelationObjectGeneratorVersion},
		{Role: "project.relation_reverse", Filename: "zz_godj_relation_reverse.go", Version: ProjectRelationReverseGeneratorVersion},
		{Role: "project.relation_prefetch", Filename: "zz_godj_relation_prefetch.go", Version: ProjectRelationPrefetchGeneratorVersion},
		{Role: "project.relation_select_related", Filename: "zz_godj_relation_select_related.go", Version: ProjectRelationSelectRelatedGeneratorVersion},
		{Role: "project.relation_delete", Filename: "zz_godj_relation_delete.go", Version: ProjectRelationDeleteGeneratorVersion},
		{Role: "project.relation_facade", Filename: "zz_godj_relation_facade.go", Version: ProjectRelationFacadeGeneratorVersion},
	}
}

func normalizeProjectSpec(input ProjectSpec) (normalizedProjectSpec, error) {
	if err := validateProjectAppCount(len(input.Apps)); err != nil {
		return normalizedProjectSpec{}, err
	}
	if err := validateProjectGeneratedFileCount(
		len(input.Apps)*projectGeneratedFilesPerApp + projectGeneratedProjectFileCount,
	); err != nil {
		return normalizedProjectSpec{}, err
	}
	schemas := make([]ir.Schema, len(input.Apps))
	for index := range input.Apps {
		schemas[index] = input.Apps[index].Schema
	}
	if err := projectspec.ValidateSchemas(schemas); err != nil {
		return normalizedProjectSpec{}, err
	}
	project, err := normalizeProjectPackage("project", input.Project)
	if err != nil {
		return normalizedProjectSpec{}, err
	}

	apps := make([]AppSpec, len(input.Apps))
	for index := range input.Apps {
		candidate := input.Apps[index]
		if err := validateProjectWireString(fmt.Sprintf("apps[%d] alias", index), candidate.Alias); err != nil {
			return normalizedProjectSpec{}, err
		}
		if !validRelationObjectAlias(candidate.Alias) || !validRelationReverseAlias(candidate.Alias) {
			return normalizedProjectSpec{}, fmt.Errorf("invalid project app alias %q", candidate.Alias)
		}
		pkg, err := normalizeProjectPackage(fmt.Sprintf("apps[%d]", index), candidate.Package)
		if err != nil {
			return normalizedProjectSpec{}, err
		}
		schema, err := ir.Normalize(candidate.Schema)
		if err != nil {
			return normalizedProjectSpec{}, fmt.Errorf("normalize project app %q: %w", candidate.Alias, err)
		}
		if err := validateProjectWireString(fmt.Sprintf("apps[%d] app label", index), schema.AppLabel); err != nil {
			return normalizedProjectSpec{}, err
		}
		if err := validateProjectWireString(fmt.Sprintf("apps[%d] owner", index), "app:"+schema.AppLabel); err != nil {
			return normalizedProjectSpec{}, err
		}
		apps[index] = AppSpec{Alias: candidate.Alias, Package: pkg, Schema: schema}
	}

	sort.Slice(apps, func(left, right int) bool {
		if apps[left].Schema.AppLabel != apps[right].Schema.AppLabel {
			return apps[left].Schema.AppLabel < apps[right].Schema.AppLabel
		}
		if apps[left].Alias != apps[right].Alias {
			return apps[left].Alias < apps[right].Alias
		}
		if apps[left].Package.ImportPath != apps[right].Package.ImportPath {
			return apps[left].Package.ImportPath < apps[right].Package.ImportPath
		}
		if apps[left].Package.Directory != apps[right].Package.Directory {
			return apps[left].Package.Directory < apps[right].Package.Directory
		}
		return apps[left].Package.PackageName < apps[right].Package.PackageName
	})

	aliases := make(map[string]struct{}, len(apps))
	imports := map[string]struct{}{project.ImportPath: {}}
	directories := map[string]string{strings.ToLower(project.Directory): project.Directory}
	appLabels := make(map[string]struct{}, len(apps))
	for _, app := range apps {
		if _, duplicate := aliases[app.Alias]; duplicate {
			return normalizedProjectSpec{}, fmt.Errorf("duplicate project app alias %q", app.Alias)
		}
		aliases[app.Alias] = struct{}{}
		if _, duplicate := imports[app.Package.ImportPath]; duplicate {
			return normalizedProjectSpec{}, fmt.Errorf("duplicate project import path %q", app.Package.ImportPath)
		}
		imports[app.Package.ImportPath] = struct{}{}
		foldedDirectory := strings.ToLower(app.Package.Directory)
		if previous, duplicate := directories[foldedDirectory]; duplicate {
			return normalizedProjectSpec{}, fmt.Errorf(
				"duplicate project package directory %q conflicts with %q",
				app.Package.Directory,
				previous,
			)
		}
		directories[foldedDirectory] = app.Package.Directory
		if _, duplicate := appLabels[app.Schema.AppLabel]; duplicate {
			return normalizedProjectSpec{}, fmt.Errorf("duplicate project app label %q", app.Schema.AppLabel)
		}
		appLabels[app.Schema.AppLabel] = struct{}{}
	}

	return normalizedProjectSpec{project: project, apps: apps}, nil
}

func normalizeProjectPackage(owner string, input PackageSpec) (PackageSpec, error) {
	for _, value := range []struct {
		name  string
		value string
	}{
		{name: "package name", value: input.PackageName},
		{name: "import path", value: input.ImportPath},
		{name: "package directory", value: input.Directory},
	} {
		if err := validateProjectWireString(owner+" "+value.name, value.value); err != nil {
			return PackageSpec{}, err
		}
	}
	if !validGeneratedPackageName(input.PackageName) {
		return PackageSpec{}, fmt.Errorf("invalid %s package name %q", owner, input.PackageName)
	}
	if !validImportPath(input.ImportPath) {
		return PackageSpec{}, fmt.Errorf("invalid %s import path %q", owner, input.ImportPath)
	}
	if !validProjectDirectory(input.Directory) {
		return PackageSpec{}, fmt.Errorf("invalid %s package directory %q", owner, input.Directory)
	}
	return input, nil
}

func validProjectDirectory(directory string) bool {
	if directory == "." {
		return true
	}
	if directory == "" || path.Clean(directory) != directory || !validImportPath(directory) {
		return false
	}
	if len(strings.Split(directory, "/"))+1 > maxProjectGeneratedPathDepth {
		return false
	}
	folded := strings.ToLower(directory)
	if folded == ".godj" || strings.HasPrefix(folded, ".godj/") {
		return false
	}
	return true
}

func projectSnapshot(input normalizedProjectSpec) ([]byte, string, error) {
	return projectSnapshotWithABI(input, projectGeneratorABIRoster())
}

func projectSnapshotWithABI(
	input normalizedProjectSpec,
	abi []projectGeneratorABI,
) ([]byte, string, error) {
	if len(abi) == 0 || len(abi) > maxProjectGeneratorABIEntries {
		return nil, "", fmt.Errorf("project generator ABI count %d exceeds current bounds", len(abi))
	}
	for index, entry := range abi {
		for _, value := range []struct {
			name  string
			value string
		}{
			{name: "role", value: entry.Role},
			{name: "filename", value: entry.Filename},
			{name: "version", value: entry.Version},
		} {
			if err := validateProjectWireString(fmt.Sprintf("generator ABI[%d] %s", index, value.name), value.value); err != nil {
				return nil, "", err
			}
		}
	}
	document := projectSnapshotDocument{
		FormatVersion: ProjectBundleFormatVersion,
		GeneratorABI:  append([]projectGeneratorABI(nil), abi...),
		Project:       projectPackageDocumentFromSpec(input.project),
		Apps:          make([]projectSnapshotApp, len(input.apps)),
	}
	for index, app := range input.apps {
		document.Apps[index] = projectSnapshotApp{
			Alias:   app.Alias,
			Package: projectPackageDocumentFromSpec(app.Package),
			Schema:  app.Schema.Clone(),
		}
	}
	data, err := json.Marshal(document)
	if err != nil {
		return nil, "", fmt.Errorf("encode project snapshot: %w", err)
	}
	data = append(data, '\n')
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func validateProjectWireString(name, value string) error {
	if len(value) > maxProjectWireStringBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, maxProjectWireStringBytes)
	}
	return nil
}

func validateProjectAppCount(count int) error {
	if count > maxProjectApps {
		return fmt.Errorf("project app count %d exceeds limit %d", count, maxProjectApps)
	}
	return nil
}

func validateProjectGeneratedFileCount(count int) error {
	if count > maxProjectGeneratedFiles {
		return fmt.Errorf("generated project file count %d exceeds limit %d", count, maxProjectGeneratedFiles)
	}
	return nil
}

func validateProjectGeneratedPath(filename string) error {
	if len(filename) > maxProjectGeneratedPathBytes {
		return fmt.Errorf("generated output path exceeds %d bytes", maxProjectGeneratedPathBytes)
	}
	return nil
}

func validateProjectGeneratedSourceSize(filename string, size int) error {
	if size > maxProjectGeneratedSourceBytes {
		return fmt.Errorf("generated output %q exceeds %d bytes", filename, maxProjectGeneratedSourceBytes)
	}
	return nil
}

func validateProjectManifestSize(size int) error {
	if size > maxProjectManifestBytes {
		return fmt.Errorf("generated project manifest exceeds %d bytes", maxProjectManifestBytes)
	}
	return nil
}

func projectPackageDocumentFromSpec(input PackageSpec) projectPackageDocument {
	return projectPackageDocument{
		PackageName: input.PackageName,
		ImportPath:  input.ImportPath,
		Directory:   input.Directory,
	}
}
