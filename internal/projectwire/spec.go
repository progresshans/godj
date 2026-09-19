// Package projectwire owns the canonical ProjectSpec/Schema IR wire shape.
// Protocols retain their enclosing commands, documents, and failure policies.
package projectwire

import (
	"fmt"
	"unicode/utf8"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/schema/ir"
)

const MaxApps = 8_192

// Snapshot and Clone detach caller data. View is a synchronous borrowed schema
// view used only for bounded preflight. Declaration transfers wire-owned schema
// storage to a declaration; callers retain one owner of that storage.
type Package struct {
	PackageName string `json:"package_name"`
	ImportPath  string `json:"import_path"`
	Directory   string `json:"directory"`
}

type App struct {
	Alias   string    `json:"alias"`
	Package Package   `json:"package"`
	Schema  ir.Schema `json:"schema"`
}

type Spec struct {
	Project Package `json:"project"`
	Apps    []App   `json:"apps"`
}

func Snapshot(input codegen.ProjectSpec) Spec {
	result := Spec{Project: packageValue(input.Project), Apps: make([]App, len(input.Apps))}
	for index := range input.Apps {
		result.Apps[index] = App{
			Alias: input.Apps[index].Alias, Package: packageValue(input.Apps[index].Package), Schema: canonicalWireSchema(input.Apps[index].Schema),
		}
	}
	return result
}

func View(input codegen.ProjectSpec) Spec {
	result := Spec{Project: packageValue(input.Project), Apps: make([]App, len(input.Apps))}
	for index := range input.Apps {
		result.Apps[index] = App{
			Alias: input.Apps[index].Alias, Package: packageValue(input.Apps[index].Package), Schema: input.Apps[index].Schema,
		}
	}
	return result
}

func Declaration(input Spec) codegen.ProjectSpec {
	result := codegen.ProjectSpec{Project: declarationPackage(input.Project), Apps: make([]codegen.AppSpec, len(input.Apps))}
	for index := range input.Apps {
		result.Apps[index] = codegen.AppSpec{
			Alias: input.Apps[index].Alias, Package: declarationPackage(input.Apps[index].Package), Schema: input.Apps[index].Schema,
		}
	}
	return result
}

func packageValue(input codegen.PackageSpec) Package {
	return Package{PackageName: input.PackageName, ImportPath: input.ImportPath, Directory: input.Directory}
}

func declarationPackage(input Package) codegen.PackageSpec {
	return codegen.PackageSpec{PackageName: input.PackageName, ImportPath: input.ImportPath, Directory: input.Directory}
}

func canonicalWireSchema(input ir.Schema) ir.Schema {
	clone := input.Clone()
	if clone.Models == nil {
		clone.Models = []ir.Model{}
	}
	for index := range clone.Models {
		if clone.Models[index].Fields == nil {
			clone.Models[index].Fields = []ir.Field{}
		}
	}
	return clone
}

func Clone(input codegen.ProjectSpec) codegen.ProjectSpec {
	clone := input
	clone.Apps = make([]codegen.AppSpec, len(input.Apps))
	for index := range input.Apps {
		clone.Apps[index] = input.Apps[index]
		clone.Apps[index].Schema = input.Apps[index].Schema.Clone()
	}
	return clone
}

func Validate(spec codegen.ProjectSpec) error {
	if len(spec.Apps) > MaxApps {
		return fmt.Errorf("apps exceed maximum %d", MaxApps)
	}
	for _, candidate := range []struct {
		path  string
		value string
	}{
		{path: "project.package_name", value: spec.Project.PackageName},
		{path: "project.import_path", value: spec.Project.ImportPath},
		{path: "project.directory", value: spec.Project.Directory},
	} {
		if err := ValidateString(candidate.path, candidate.value); err != nil {
			return err
		}
	}
	schemas := make([]ir.Schema, len(spec.Apps))
	for index := range spec.Apps {
		app := spec.Apps[index]
		for _, candidate := range []struct {
			path  string
			value string
		}{
			{path: fmt.Sprintf("apps[%d].alias", index), value: app.Alias},
			{path: fmt.Sprintf("apps[%d].package.package_name", index), value: app.Package.PackageName},
			{path: fmt.Sprintf("apps[%d].package.import_path", index), value: app.Package.ImportPath},
			{path: fmt.Sprintf("apps[%d].package.directory", index), value: app.Package.Directory},
		} {
			if err := ValidateString(candidate.path, candidate.value); err != nil {
				return err
			}
		}
		if app.Schema.FormatVersion != ir.CurrentFormatVersion {
			return fmt.Errorf("apps[%d].schema format version %d is incompatible", index, app.Schema.FormatVersion)
		}
		schemas[index] = app.Schema
	}
	return projectspec.ValidateSchemas(schemas)
}

func ValidateString(path, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not UTF-8", path)
	}
	if len(value) > projectspec.MaxSchemaStringBytes {
		return fmt.Errorf("%s exceeds %d bytes", path, projectspec.MaxSchemaStringBytes)
	}
	return nil
}
