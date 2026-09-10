package codegen

import (
	"io/fs"

	"github.com/progresshans/godj/schema/ir"
)

// ProjectBundleFormatVersion is the single pre-alpha manifest and bundle
// format understood by the current project generator.
const ProjectBundleFormatVersion = 1

// GeneratedManifestPath is the canonical project-root-relative commit marker
// for a generated project bundle. Manifest bytes are exposed separately from
// Files and are published last.
const GeneratedManifestPath = ".godj/generated-manifest.json"

// PackageSpec identifies one generated Go package and its canonical location
// below the project root. Directory is either "." or a non-empty clean
// slash-separated relative path with no empty, dot, dot-dot or backslash
// segment. The reserved .godj control subtree is never a package directory.
// The project root itself is an execution concern and is not part of the
// project snapshot digest.
type PackageSpec struct {
	PackageName string
	ImportPath  string
	Directory   string
}

// AppSpec declares one app schema in the project generation universe.
// GenerateProject owns a normalized deep snapshot and never retains the
// caller's Schema slices.
type AppSpec struct {
	Alias   string
	Package PackageSpec
	Schema  ir.Schema
}

// ProjectSpec is the only semantic input to whole-project generation.
type ProjectSpec struct {
	Project PackageSpec
	Apps    []AppSpec
}

// GeneratedFile is one immutable Go-source member of a GeneratedBundle.
// Manifest bytes are not included in Files. Mode contains canonical permission
// bits only and is currently always 0644. Source returns a fresh clone so
// callers cannot mutate the bundle publication authority.
type GeneratedFile struct {
	Path   string
	Owner  string
	SHA256 string
	Mode   fs.FileMode

	source []byte
}

// Source returns a caller-owned copy of the generated source bytes.
func (file GeneratedFile) Source() []byte {
	return append([]byte(nil), file.source...)
}

// GeneratedBundle is an opaque immutable snapshot of every current app and
// project generated Go output plus its separate canonical manifest. The zero
// value is not a valid generation, check or publication input.
type GeneratedBundle struct {
	snapshotSHA256 string
	files          []GeneratedFile
	manifest       []byte
}

// SnapshotSHA256 identifies the normalized schema, package layout and
// generator ABI that own this bundle.
func (bundle GeneratedBundle) SnapshotSHA256() string {
	return bundle.snapshotSHA256
}

// Files returns the canonical ordered Go-source inventory and excludes
// GeneratedManifestPath. Each Source accessor returns a fresh byte slice.
func (bundle GeneratedBundle) Files() []GeneratedFile {
	return append([]GeneratedFile(nil), bundle.files...)
}

// Manifest returns a caller-owned copy of the canonical manifest document.
func (bundle GeneratedBundle) Manifest() []byte {
	return append([]byte(nil), bundle.manifest...)
}
