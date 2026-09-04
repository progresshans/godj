package projectgenerate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/token"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/codegen"
)

const (
	committedManifestFormatVersion = 1
	maxCommittedManifestBytes      = 16 << 20
	maxManifestApps                = 8192
	maxManifestFiles               = 65536
	maxManifestArrayEntries        = 65536
	maxManifestObjectMembers       = 64
	maxManifestStringBytes         = 4096
	maxManifestJSONDepth           = 8
	maxManifestPathDepth           = 64
)

var currentManifestABI = []manifestABI{
	{Role: "bundle", Filename: codegen.GeneratedManifestPath, Version: "godj-codegen-project-bundle-v1"},
	{Role: "app.main", Filename: "zz_godj_generated.go", Version: codegen.GeneratorVersion},
	{Role: "app.relation_metadata", Filename: "zz_godj_relation.go", Version: codegen.RelationMetadataGeneratorVersion},
	{Role: "app.relation_object", Filename: "zz_godj_relation_object.go", Version: codegen.RelationObjectGeneratorVersion},
	{Role: "app.relation_projection", Filename: "zz_godj_relation_projection.go", Version: codegen.RelationProjectionGeneratorVersion},
	{Role: "project.bindings", Filename: "zz_godj_bindings.go", Version: "godj-codegen-rel-project-v1"},
	{Role: "project.relation_query", Filename: "zz_godj_relation_query.go", Version: codegen.ProjectRelationQueryGeneratorVersion},
	{Role: "project.relation_object", Filename: "zz_godj_relation_object.go", Version: codegen.ProjectRelationObjectGeneratorVersion},
	{Role: "project.relation_reverse", Filename: "zz_godj_relation_reverse.go", Version: codegen.ProjectRelationReverseGeneratorVersion},
	{Role: "project.relation_prefetch", Filename: "zz_godj_relation_prefetch.go", Version: codegen.ProjectRelationPrefetchGeneratorVersion},
	{Role: "project.relation_select_related", Filename: "zz_godj_relation_select_related.go", Version: codegen.ProjectRelationSelectRelatedGeneratorVersion},
	{Role: "project.relation_delete", Filename: "zz_godj_relation_delete.go", Version: codegen.ProjectRelationDeleteGeneratorVersion},
	{Role: "project.relation_facade", Filename: "zz_godj_relation_facade.go", Version: codegen.ProjectRelationFacadeGeneratorVersion},
}

var currentAppFilenames = []string{
	"zz_godj_generated.go",
	"zz_godj_relation.go",
	"zz_godj_relation_object.go",
	"zz_godj_relation_projection.go",
}

var currentProjectFilenames = []string{
	"zz_godj_bindings.go",
	"zz_godj_relation_query.go",
	"zz_godj_relation_object.go",
	"zz_godj_relation_reverse.go",
	"zz_godj_relation_prefetch.go",
	"zz_godj_relation_select_related.go",
	"zz_godj_relation_delete.go",
	"zz_godj_relation_facade.go",
}

// decodeCommittedManifest accepts structurally safe canonical format-1
// documents, including older generated rosters needed to delete retired
// generated files during an upgrade. validateGeneratedBundle separately
// requires the exact current ABI and roster for desired output.
func decodeCommittedManifest(document []byte) (committedManifest, error) {
	var manifest committedManifest
	if len(document) == 0 {
		return manifest, fmt.Errorf("decode generated manifest: document is empty")
	}
	if len(document) > maxCommittedManifestBytes {
		return manifest, fmt.Errorf("decode generated manifest: document exceeds resource limit")
	}
	if document[len(document)-1] != '\n' {
		return manifest, fmt.Errorf("decode generated manifest: canonical final LF is missing")
	}
	if err := preflightCommittedManifest(document); err != nil {
		return manifest, fmt.Errorf("decode generated manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return committedManifest{}, fmt.Errorf("decode generated manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return committedManifest{}, fmt.Errorf("decode generated manifest: trailing JSON value")
		}
		return committedManifest{}, fmt.Errorf("decode generated manifest: trailing data: %w", err)
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return committedManifest{}, fmt.Errorf("re-encode generated manifest: %w", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(document, canonical) {
		return committedManifest{}, fmt.Errorf("decode generated manifest: document is not canonical")
	}
	if err := validateCommittedManifestStructure(manifest); err != nil {
		return committedManifest{}, fmt.Errorf("decode generated manifest: %w", err)
	}
	return manifest, nil
}

func validateCommittedManifestStructure(manifest committedManifest) error {
	if manifest.FormatVersion != committedManifestFormatVersion {
		return fmt.Errorf("unsupported format version %d", manifest.FormatVersion)
	}
	if !validLowerSHA256(manifest.SnapshotSHA256) {
		return fmt.Errorf("invalid snapshot SHA-256")
	}
	if err := validateManifestABI(manifest.GeneratorABI); err != nil {
		return err
	}
	if !validManifestPackage(manifest.Project) {
		return fmt.Errorf("invalid project package")
	}
	if len(manifest.Apps) > maxManifestApps || len(manifest.Files) > maxManifestFiles {
		return fmt.Errorf("manifest inventory exceeds resource limits")
	}

	owners := map[string]manifestPackage{"project": manifest.Project}
	aliases := make(map[string]struct{}, len(manifest.Apps))
	labels := make(map[string]struct{}, len(manifest.Apps))
	imports := map[string]struct{}{manifest.Project.ImportPath: {}}
	directories := map[string]struct{}{strings.ToLower(manifest.Project.Directory): {}}
	for index, app := range manifest.Apps {
		if app.Alias == "" || !validManifestAlias(app.Alias) || app.AppLabel == "" || !validManifestAppLabel(app.AppLabel) ||
			!validManifestPackage(app.Package) || !validLowerSHA256(app.SchemaSHA256) {
			return fmt.Errorf("invalid app at index %d", index)
		}
		if index > 0 && !manifestAppLess(manifest.Apps[index-1], app) {
			return fmt.Errorf("apps are not in canonical order")
		}
		if _, exists := aliases[app.Alias]; exists {
			return fmt.Errorf("duplicate app alias %q", app.Alias)
		}
		aliases[app.Alias] = struct{}{}
		if _, exists := labels[app.AppLabel]; exists {
			return fmt.Errorf("duplicate app label %q", app.AppLabel)
		}
		labels[app.AppLabel] = struct{}{}
		if _, exists := imports[app.Package.ImportPath]; exists {
			return fmt.Errorf("duplicate package import path %q", app.Package.ImportPath)
		}
		imports[app.Package.ImportPath] = struct{}{}
		foldedDirectory := strings.ToLower(app.Package.Directory)
		if _, exists := directories[foldedDirectory]; exists {
			return fmt.Errorf("duplicate package directory %q", app.Package.Directory)
		}
		directories[foldedDirectory] = struct{}{}
		owners["app:"+app.AppLabel] = app.Package
	}

	seenFiles := make(map[string]struct{}, len(manifest.Files))
	seenFoldedFiles := make(map[string]string, len(manifest.Files))
	for index, file := range manifest.Files {
		if !validManifestFilePath(file.Path) || file.Mode != "0644" || !validLowerSHA256(file.SHA256) {
			return fmt.Errorf("invalid file at index %d", index)
		}
		if index > 0 && manifest.Files[index-1].Path >= file.Path {
			return fmt.Errorf("files are not in canonical order")
		}
		owner, known := owners[file.Owner]
		if !known {
			return fmt.Errorf("unknown owner %q", file.Owner)
		}
		if manifestPathDirectory(file.Path) != owner.Directory || !validGeneratedFilename(path.Base(file.Path)) {
			return fmt.Errorf("file %q is outside owner %q generated namespace", file.Path, file.Owner)
		}
		if _, duplicate := seenFiles[file.Path]; duplicate {
			return fmt.Errorf("duplicate file path %q", file.Path)
		}
		seenFiles[file.Path] = struct{}{}
		foldedPath := strings.ToLower(file.Path)
		if previous, duplicate := seenFoldedFiles[foldedPath]; duplicate {
			return fmt.Errorf("file path %q case-folds onto %q", file.Path, previous)
		}
		seenFoldedFiles[foldedPath] = file.Path
	}
	return nil
}

func validateGeneratedBundle(bundle codegen.GeneratedBundle) (committedManifest, error) {
	manifestBytes := bundle.Manifest()
	manifest, err := decodeCommittedManifest(manifestBytes)
	if err != nil {
		return committedManifest{}, fmt.Errorf("%w: %v", ErrInvalidGeneratedBundle, err)
	}
	if bundle.SnapshotSHA256() != manifest.SnapshotSHA256 {
		return committedManifest{}, fmt.Errorf("%w: snapshot does not match manifest", ErrInvalidGeneratedBundle)
	}
	if err := validateCurrentManifest(manifest); err != nil {
		return committedManifest{}, fmt.Errorf("%w: %v", ErrInvalidGeneratedBundle, err)
	}
	files := bundle.Files()
	if len(files) != len(manifest.Files) {
		return committedManifest{}, fmt.Errorf("%w: file inventory length does not match manifest", ErrInvalidGeneratedBundle)
	}
	for index, file := range files {
		entry := manifest.Files[index]
		source := file.Source()
		if file.Path != entry.Path || file.Owner != entry.Owner || file.Mode.Perm() != 0o644 || file.Mode != file.Mode.Perm() ||
			file.SHA256 != entry.SHA256 || sha256Hex(source) != entry.SHA256 {
			return committedManifest{}, fmt.Errorf("%w: file %d does not match manifest", ErrInvalidGeneratedBundle, index)
		}
	}
	return manifest, nil
}

func validateCurrentManifest(manifest committedManifest) error {
	if !equalManifestABI(manifest.GeneratorABI, currentManifestABI) {
		return fmt.Errorf("generator ABI roster is not current")
	}
	wantFiles := make(map[string]string, len(manifest.Apps)*len(currentAppFilenames)+len(currentProjectFilenames))
	for _, filename := range currentProjectFilenames {
		wantFiles[joinManifestPath(manifest.Project.Directory, filename)] = "project"
	}
	for _, app := range manifest.Apps {
		owner := "app:" + app.AppLabel
		for _, filename := range currentAppFilenames {
			wantFiles[joinManifestPath(app.Package.Directory, filename)] = owner
		}
	}
	if len(manifest.Files) != len(wantFiles) {
		return fmt.Errorf("current file roster has %d entries, want %d", len(manifest.Files), len(wantFiles))
	}
	for _, file := range manifest.Files {
		if wantOwner, expected := wantFiles[file.Path]; !expected || file.Owner != wantOwner {
			return fmt.Errorf("file %q is not in the current roster", file.Path)
		}
	}
	return nil
}

func validateManifestABI(roster []manifestABI) error {
	if len(roster) == 0 || len(roster) > 64 {
		return fmt.Errorf("generator ABI roster exceeds resource limits")
	}
	roles := make(map[string]struct{}, len(roster))
	bundleEntry := false
	for index, entry := range roster {
		if !validManifestToken(entry.Role) || !validManifestToken(entry.Version) {
			return fmt.Errorf("invalid generator ABI entry at index %d", index)
		}
		if _, duplicate := roles[entry.Role]; duplicate {
			return fmt.Errorf("duplicate generator ABI role %q", entry.Role)
		}
		roles[entry.Role] = struct{}{}
		if entry.Role == "bundle" {
			if entry.Filename != codegen.GeneratedManifestPath {
				return fmt.Errorf("invalid bundle ABI filename %q", entry.Filename)
			}
			bundleEntry = true
		} else if !validManifestABIFilename(entry.Filename) {
			return fmt.Errorf("invalid generator ABI filename at index %d", index)
		}
	}
	if !bundleEntry {
		return fmt.Errorf("generator ABI roster has no bundle entry")
	}
	return nil
}

func validManifestToken(value string) bool {
	if value == "" || len(value) > maxManifestStringBytes || !utf8.ValidString(value) {
		return false
	}
	for _, current := range value {
		if current < 0x21 || current > 0x7e {
			return false
		}
	}
	return true
}

func validManifestABIFilename(filename string) bool {
	return path.Base(filename) == filename && validGeneratedFilename(filename)
}

func validGeneratedFilename(filename string) bool {
	return strings.HasPrefix(filename, "zz_godj_") && strings.HasSuffix(filename, ".go") && validManifestFilePath(filename)
}

func manifestPathDirectory(filename string) string {
	directory := path.Dir(filename)
	if directory == "." {
		return "."
	}
	return directory
}

func equalManifestABI(left, right []manifestABI) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func manifestAppLess(left, right manifestApp) bool {
	if left.AppLabel != right.AppLabel {
		return left.AppLabel < right.AppLabel
	}
	if left.Alias != right.Alias {
		return left.Alias < right.Alias
	}
	if left.Package.ImportPath != right.Package.ImportPath {
		return left.Package.ImportPath < right.Package.ImportPath
	}
	if left.Package.Directory != right.Package.Directory {
		return left.Package.Directory < right.Package.Directory
	}
	return left.Package.PackageName < right.Package.PackageName
}

func validManifestPackage(pkg manifestPackage) bool {
	return pkg.PackageName != "_" && token.IsIdentifier(pkg.PackageName) && !token.Lookup(pkg.PackageName).IsKeyword() &&
		validManifestImportPath(pkg.ImportPath) && validManifestDirectory(pkg.Directory)
}

func validManifestDirectory(directory string) bool {
	if directory == "." {
		return true
	}
	folded := strings.ToLower(directory)
	if folded == ".godj" || strings.HasPrefix(folded, ".godj/") {
		return false
	}
	return validManifestFilePath(directory)
}

func validManifestFilePath(value string) bool {
	if value == "" || value == "." || len(value) > maxManifestStringBytes || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 ||
		strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return false
	}
	segments := strings.Split(value, "/")
	if len(segments) > maxManifestPathDepth {
		return false
	}
	for _, segment := range segments {
		if !validPortableManifestPathElement(segment) {
			return false
		}
	}
	return true
}

func validPortableManifestPathElement(element string) bool {
	if element == "" || element == "." || element == ".." || strings.Trim(element, ".") == "" || strings.HasSuffix(element, ".") {
		return false
	}
	for _, current := range element {
		if current != '-' && current != '.' && current != '_' && current != '~' && current != '+' &&
			!(current >= '0' && current <= '9') && !(current >= 'A' && current <= 'Z') && !(current >= 'a' && current <= 'z') {
			return false
		}
	}
	short := element
	if dot := strings.IndexByte(short, '.'); dot >= 0 {
		short = short[:dot]
	}
	return !reservedWindowsManifestElement(short) && !hasWindowsManifestShortName(short)
}

func validManifestImportPath(importPath string) bool {
	if importPath == "" || len(importPath) > maxManifestStringBytes || !utf8.ValidString(importPath) || importPath == "go" || importPath == "type" ||
		strings.HasPrefix(importPath, "-") || strings.HasPrefix(importPath, "/") || strings.HasSuffix(importPath, "/") || strings.Contains(importPath, "//") {
		return false
	}
	for _, element := range strings.Split(importPath, "/") {
		if element == "" || strings.Trim(element, ".") == "" || strings.HasSuffix(element, ".") {
			return false
		}
		for _, current := range element {
			if current != '-' && current != '.' && current != '_' && current != '~' && current != '+' &&
				!(current >= '0' && current <= '9') && !(current >= 'A' && current <= 'Z') && !(current >= 'a' && current <= 'z') {
				return false
			}
		}
		short := element
		if dot := strings.IndexByte(short, '.'); dot >= 0 {
			short = short[:dot]
		}
		if reservedWindowsManifestElement(short) || hasWindowsManifestShortName(short) {
			return false
		}
	}
	return true
}

func reservedWindowsManifestElement(element string) bool {
	upper := strings.ToUpper(element)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" {
		return true
	}
	if len(upper) != 4 || upper[3] < '1' || upper[3] > '9' {
		return false
	}
	return strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")
}

func hasWindowsManifestShortName(element string) bool {
	tilde := strings.LastIndexByte(element, '~')
	if tilde < 0 || tilde == len(element)-1 {
		return false
	}
	for _, current := range element[tilde+1:] {
		if current < '0' || current > '9' {
			return false
		}
	}
	return true
}

func validManifestAlias(value string) bool {
	if value == "" || len(value) > maxManifestStringBytes || value[0] < 'a' || value[0] > 'z' || token.Lookup(value).IsKeyword() {
		return false
	}
	reserved := map[string]struct{}{
		"init": {}, "context": {}, "db": {}, "orm": {}, "query": {}, "ir": {},
		"bool": {}, "error": {}, "false": {}, "nil": {}, "true": {},
	}
	if _, exists := reserved[value]; exists {
		return false
	}
	for index := 1; index < len(value); index++ {
		current := value[index]
		if !('a' <= current && current <= 'z') && !('A' <= current && current <= 'Z') && !('0' <= current && current <= '9') {
			return false
		}
	}
	return true
}

func validManifestAppLabel(value string) bool {
	if value == "" || len(value) > maxManifestStringBytes || !utf8.ValidString(value) || !((value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') {
		return false
	}
	for _, current := range value {
		if !(current >= 'a' && current <= 'z') && !(current >= '0' && current <= '9') && current != '_' {
			return false
		}
	}
	return true
}

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func joinManifestPath(directory, filename string) string {
	if directory == "." {
		return filename
	}
	return path.Join(directory, filename)
}

func sortedManifestFilePaths(manifest committedManifest) []string {
	paths := make([]string, len(manifest.Files))
	for index, file := range manifest.Files {
		paths[index] = file.Path
	}
	sort.Strings(paths)
	return paths
}

func preflightCommittedManifest(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := preflightManifestValue(decoder, "", 0); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing token %v", token)
		}
		return fmt.Errorf("trailing data: %w", err)
	}
	return nil
}

func preflightManifestValue(decoder *json.Decoder, location string, depth int) error {
	if depth > maxManifestJSONDepth {
		return fmt.Errorf("JSON nesting exceeds resource limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			seen := make(map[string]struct{})
			members := 0
			for decoder.More() {
				member, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := member.(string)
				if !ok || name == "" || len(name) > maxManifestStringBytes {
					return fmt.Errorf("invalid JSON object member")
				}
				members++
				if members > maxManifestObjectMembers {
					return fmt.Errorf("JSON object exceeds resource limit")
				}
				if _, duplicate := seen[name]; duplicate {
					return fmt.Errorf("duplicate JSON member %q", name)
				}
				seen[name] = struct{}{}
				if err := preflightManifestValue(decoder, location+"/"+name, depth+1); err != nil {
					return err
				}
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim('}') {
				return fmt.Errorf("invalid JSON object terminator")
			}
		case '[':
			maximum := maxManifestArrayEntries
			switch location {
			case "/apps":
				maximum = maxManifestApps
			case "/files":
				maximum = maxManifestFiles
			case "/generator_abi":
				maximum = 64
			}
			count := 0
			for decoder.More() {
				count++
				if count > maximum {
					return fmt.Errorf("JSON array %s exceeds resource limit", location)
				}
				if err := preflightManifestValue(decoder, location+"[]", depth+1); err != nil {
					return err
				}
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim(']') {
				return fmt.Errorf("invalid JSON array terminator")
			}
		default:
			return fmt.Errorf("invalid JSON delimiter %q", value)
		}
	case string:
		if len(value) > maxManifestStringBytes {
			return fmt.Errorf("JSON string exceeds resource limit")
		}
	}
	return nil
}
