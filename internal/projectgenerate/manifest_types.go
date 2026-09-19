package projectgenerate

type manifestABI struct {
	Role     string `json:"role"`
	Filename string `json:"filename"`
	Version  string `json:"version"`
}

type manifestPackage struct {
	PackageName string `json:"package_name"`
	ImportPath  string `json:"import_path"`
	Directory   string `json:"directory"`
}

type manifestApp struct {
	Alias        string          `json:"alias"`
	AppLabel     string          `json:"app_label"`
	Package      manifestPackage `json:"package"`
	SchemaSHA256 string          `json:"schema_sha256"`
}

type manifestFile struct {
	Path   string `json:"path"`
	Owner  string `json:"owner"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
}

// committedManifest mirrors codegen's persisted canonical manifest. The
// decoder owned by the check lane must reject noncanonical bytes, unknown or
// duplicate members, invalid ordering, unsupported versions and bad hashes.
type committedManifest struct {
	FormatVersion  int             `json:"format_version"`
	SnapshotSHA256 string          `json:"snapshot_sha256"`
	GeneratorABI   []manifestABI   `json:"generator_abi"`
	Project        manifestPackage `json:"project"`
	Apps           []manifestApp   `json:"apps"`
	Files          []manifestFile  `json:"files"`
}
