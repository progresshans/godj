package codegen

import (
	"encoding/json"
	"fmt"

	"github.com/progresshans/godj/schema/ir"
)

type projectManifestApp struct {
	Alias        string                 `json:"alias"`
	AppLabel     string                 `json:"app_label"`
	Package      projectPackageDocument `json:"package"`
	SchemaSHA256 string                 `json:"schema_sha256"`
}

type projectManifestFile struct {
	Path   string `json:"path"`
	Owner  string `json:"owner"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
}

type projectManifestDocument struct {
	FormatVersion  int                    `json:"format_version"`
	SnapshotSHA256 string                 `json:"snapshot_sha256"`
	GeneratorABI   []projectGeneratorABI  `json:"generator_abi"`
	Project        projectPackageDocument `json:"project"`
	Apps           []projectManifestApp   `json:"apps"`
	Files          []projectManifestFile  `json:"files"`
}

func projectManifest(
	input normalizedProjectSpec,
	snapshotSHA256 string,
	files []GeneratedFile,
) ([]byte, error) {
	if err := validateProjectAppCount(len(input.apps)); err != nil {
		return nil, err
	}
	if err := validateProjectGeneratedFileCount(len(files)); err != nil {
		return nil, err
	}
	document := projectManifestDocument{
		FormatVersion:  ProjectBundleFormatVersion,
		SnapshotSHA256: snapshotSHA256,
		GeneratorABI:   projectGeneratorABIRoster(),
		Project:        projectPackageDocumentFromSpec(input.project),
		Apps:           make([]projectManifestApp, len(input.apps)),
		Files:          make([]projectManifestFile, len(files)),
	}
	for index, app := range input.apps {
		hash, err := ir.Hash(app.Schema)
		if err != nil {
			return nil, fmt.Errorf("hash manifest app %q: %w", app.Alias, err)
		}
		document.Apps[index] = projectManifestApp{
			Alias:        app.Alias,
			AppLabel:     app.Schema.AppLabel,
			Package:      projectPackageDocumentFromSpec(app.Package),
			SchemaSHA256: hash,
		}
	}
	for index, file := range files {
		document.Files[index] = projectManifestFile{
			Path:   file.Path,
			Owner:  file.Owner,
			Mode:   "0644",
			SHA256: file.SHA256,
		}
	}
	data, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode generated project manifest: %w", err)
	}
	data = append(data, '\n')
	if err := validateProjectManifestSize(len(data)); err != nil {
		return nil, err
	}
	return data, nil
}
