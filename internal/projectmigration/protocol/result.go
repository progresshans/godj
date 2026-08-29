package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const projectSpecDigestDomain = "godj/makemigrations-project-spec/v1\x00"

// NormalizeProjectSpec returns the exact declaration snapshot carried by a
// success response. It mirrors codegen normalization: schemas are normalized
// and apps are sorted by app label, alias, import path, directory, then package
// name. No caller-owned slice or pointer is retained.
func NormalizeProjectSpec(input codegen.ProjectSpec) (codegen.ProjectSpec, error) {
	return normalizeProjectSpec(input)
}

// ProjectSpecDigest returns the strict lowercase semantic digest of the
// normalized ProjectSpec. The digest is intentionally distinct from
// GeneratedBundle.SnapshotSHA256(), which also binds the generator ABI.
func ProjectSpecDigest(input codegen.ProjectSpec) (string, error) {
	normalized, err := normalizeProjectSpec(input)
	if err != nil {
		return "", err
	}
	return digestNormalizedProjectSpec(normalized)
}

func canonicalResult(input Result, requireNormalized bool) (Result, error) {
	if input.ProjectSpec.Apps == nil {
		return Result{}, errors.New("project_spec.apps must not be nil")
	}
	if input.ProgrammaticCatalog.Sources == nil {
		return Result{}, errors.New("programmatic_catalog.sources must not be nil")
	}
	if input.Candidates == nil {
		return Result{}, errors.New("candidates must not be nil")
	}

	normalized, err := normalizeProjectSpec(input.ProjectSpec)
	if err != nil {
		return Result{}, fmt.Errorf("project_spec: %w", err)
	}
	if requireNormalized && !reflect.DeepEqual(input.ProjectSpec, normalized) {
		return Result{}, errors.New("project_spec is not normalized")
	}
	digest, err := digestNormalizedProjectSpec(normalized)
	if err != nil {
		return Result{}, fmt.Errorf("project_spec_digest: %w", err)
	}
	if input.ProjectSpecDigest != digest {
		return Result{}, errors.New("project_spec_digest does not match normalized project_spec")
	}
	if !validRawSHA256(input.ProjectSnapshotSHA256) {
		return Result{}, errors.New("project_snapshot_sha256 is not strict lowercase SHA-256")
	}
	if err := validateWriterRoot(input.WriterRoot); err != nil {
		return Result{}, err
	}
	if err := validateCatalogSummary("filesystem_catalog", input.FilesystemCatalog); err != nil {
		return Result{}, err
	}
	if err := validateProgrammaticCatalog(input.ProgrammaticCatalog); err != nil {
		return Result{}, err
	}

	existingSources, ok := checkedAdd(input.FilesystemCatalog.SourceCount, input.ProgrammaticCatalog.SourceCount, definition.MaxSources)
	if !ok {
		return Result{}, fmt.Errorf("existing source count exceeds %d", definition.MaxSources)
	}
	existingBytes, ok := checkedAdd(input.FilesystemCatalog.DocumentBytes, input.ProgrammaticCatalog.DocumentBytes, definition.MaxBatchBytes)
	if !ok {
		return Result{}, fmt.Errorf("existing document bytes exceed %d", definition.MaxBatchBytes)
	}
	if !validDigest(input.DefinitionSetDigest) {
		return Result{}, errors.New("definition_set_digest is not strict lowercase SHA-256")
	}
	if existingSources == 0 {
		if input.DefinitionSetDigest != definition.EmptySetDigest {
			return Result{}, errors.New("empty existing catalog has non-empty semantic digest")
		}
	} else if input.DefinitionSetDigest == definition.EmptySetDigest {
		return Result{}, errors.New("non-empty existing catalog has empty semantic digest")
	}

	if err := validateCandidates(
		input.WriterRoot,
		input.ProgrammaticCatalog.Sources,
		input.Candidates,
		existingSources,
		existingBytes,
	); err != nil {
		return Result{}, err
	}

	result := cloneResult(input)
	result.ProjectSpec = normalized
	result.ProjectSpecDigest = digest
	return result, nil
}

func normalizeProjectSpec(input codegen.ProjectSpec) (codegen.ProjectSpec, error) {
	if input.Apps == nil {
		return codegen.ProjectSpec{}, errors.New("apps must not be nil")
	}
	if len(input.Apps) > MaxProjectApps {
		return codegen.ProjectSpec{}, fmt.Errorf("apps exceed %d", MaxProjectApps)
	}
	// ValidateProjectSpec owns codegen's allocation-before-normalization
	// resource boundary. Invoke it on the original declaration before cloning
	// any schema-sized slice, then reproduce its successful normalization below.
	if err := codegen.ValidateProjectSpec(input); err != nil {
		return codegen.ProjectSpec{}, err
	}

	normalized := codegen.ProjectSpec{
		Project: input.Project,
		Apps:    make([]codegen.AppSpec, len(input.Apps)),
	}
	for index := range input.Apps {
		app := input.Apps[index]
		schema, err := ir.Normalize(app.Schema)
		if err != nil {
			return codegen.ProjectSpec{}, fmt.Errorf("normalize app[%d]: %w", index, err)
		}
		normalized.Apps[index] = codegen.AppSpec{
			Alias:   app.Alias,
			Package: app.Package,
			Schema:  schema,
		}
	}
	sort.Slice(normalized.Apps, func(left, right int) bool {
		if normalized.Apps[left].Schema.AppLabel != normalized.Apps[right].Schema.AppLabel {
			return normalized.Apps[left].Schema.AppLabel < normalized.Apps[right].Schema.AppLabel
		}
		if normalized.Apps[left].Alias != normalized.Apps[right].Alias {
			return normalized.Apps[left].Alias < normalized.Apps[right].Alias
		}
		if normalized.Apps[left].Package.ImportPath != normalized.Apps[right].Package.ImportPath {
			return normalized.Apps[left].Package.ImportPath < normalized.Apps[right].Package.ImportPath
		}
		if normalized.Apps[left].Package.Directory != normalized.Apps[right].Package.Directory {
			return normalized.Apps[left].Package.Directory < normalized.Apps[right].Package.Directory
		}
		return normalized.Apps[left].Package.PackageName < normalized.Apps[right].Package.PackageName
	})
	return cloneProjectSpec(normalized), nil
}

func digestNormalizedProjectSpec(normalized codegen.ProjectSpec) (string, error) {
	wire := toWireProjectSpec(normalized)
	if _, err := measureProjectSpec(wire, MaxResponseBytes); err != nil {
		return "", err
	}
	document, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("encode normalized project spec: %w", err)
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(projectSpecDigestDomain))
	_, _ = digest.Write(document)
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func validateCatalogSummary(owner string, summary CatalogSummary) error {
	if summary.SourceCount < 0 || summary.SourceCount > definition.MaxSources {
		return fmt.Errorf("%s.source_count exceeds %d", owner, definition.MaxSources)
	}
	if summary.DocumentBytes < 0 || summary.DocumentBytes > definition.MaxBatchBytes {
		return fmt.Errorf("%s.document_bytes exceeds %d", owner, definition.MaxBatchBytes)
	}
	if (summary.SourceCount == 0) != (summary.DocumentBytes == 0) {
		return fmt.Errorf("%s count and bytes disagree", owner)
	}
	if summary.DocumentBytes < summary.SourceCount {
		return fmt.Errorf("%s document bytes cannot contain its source count", owner)
	}
	if !validDigest(summary.Digest) {
		return fmt.Errorf("%s.digest is not strict lowercase SHA-256", owner)
	}
	return nil
}

func validateProgrammaticCatalog(catalog ProgrammaticCatalog) error {
	summary := CatalogSummary{
		SourceCount:   catalog.SourceCount,
		DocumentBytes: catalog.DocumentBytes,
		Digest:        catalog.Digest,
	}
	if err := validateCatalogSummary("programmatic_catalog", summary); err != nil {
		return err
	}
	if catalog.SourceCount != len(catalog.Sources) {
		return errors.New("programmatic_catalog.source_count does not match sources")
	}

	total := 0
	for index := range catalog.Sources {
		source := catalog.Sources[index]
		if err := validateSourceID(fmt.Sprintf("programmatic_catalog.sources[%d].source_id", index), source.SourceID); err != nil {
			return err
		}
		if source.Document == nil {
			return fmt.Errorf("programmatic_catalog.sources[%d].document must not be nil", index)
		}
		if len(source.Document) == 0 || len(source.Document) > definition.MaxDocumentBytes {
			return fmt.Errorf("programmatic_catalog.sources[%d].document exceeds current bounds", index)
		}
		updated, ok := checkedAdd(total, len(source.Document), definition.MaxBatchBytes)
		if !ok {
			return fmt.Errorf("programmatic_catalog document bytes exceed %d", definition.MaxBatchBytes)
		}
		total = updated
		if index > 0 && bytes.Compare([]byte(catalog.Sources[index-1].SourceID), []byte(source.SourceID)) >= 0 {
			return errors.New("programmatic_catalog.sources must be strictly SourceID-sorted and unique")
		}
	}
	if total != catalog.DocumentBytes {
		return errors.New("programmatic_catalog.document_bytes does not match sources")
	}
	return nil
}

func validateCandidates(
	writerRoot string,
	programmatic []Source,
	candidates []Candidate,
	existingSources, existingBytes int,
) error {
	if len(candidates) > MaxCandidates {
		return fmt.Errorf("candidates exceed %d", MaxCandidates)
	}
	if _, ok := checkedAdd(existingSources, len(candidates), definition.MaxSources); !ok {
		return fmt.Errorf("combined source count exceeds %d", definition.MaxSources)
	}

	seen := make(map[string]struct{}, len(candidates))
	seenSourceIDs := make(map[string]struct{}, len(programmatic)+len(candidates))
	for index := range programmatic {
		seenSourceIDs[programmatic[index].SourceID] = struct{}{}
	}
	total := existingBytes
	for index := range candidates {
		candidate := candidates[index]
		if err := validateCandidateIdentity(index, candidate.App, candidate.Name); err != nil {
			return err
		}
		key := candidate.App + "\x00" + candidate.Name
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("candidates[%d] duplicates %s.%s", index, candidate.App, candidate.Name)
		}
		seen[key] = struct{}{}
		if candidate.Document == nil {
			return fmt.Errorf("candidates[%d].document must not be nil", index)
		}
		if len(candidate.Document) == 0 || len(candidate.Document) > definition.MaxDocumentBytes {
			return fmt.Errorf("candidates[%d].document exceeds current bounds", index)
		}
		updated, ok := checkedAdd(total, len(candidate.Document), definition.MaxBatchBytes)
		if !ok {
			return fmt.Errorf("combined document bytes exceed %d", definition.MaxBatchBytes)
		}
		total = updated

		basename := candidate.App + "_" + candidate.Name + ".godj.json"
		sourceID := basename
		if writerRoot != "." {
			sourceID = writerRoot + "/" + basename
		}
		if len(sourceID) > definition.MaxSourceIDBytes {
			return fmt.Errorf("candidates[%d] derived source ID exceeds %d bytes", index, definition.MaxSourceIDBytes)
		}
		if _, duplicate := seenSourceIDs[sourceID]; duplicate {
			return fmt.Errorf("candidates[%d] derived source ID is not unique", index)
		}
		seenSourceIDs[sourceID] = struct{}{}
	}
	return nil
}

func validateCandidateIdentity(index int, app, name string) error {
	if !validAppIdentity(app) {
		return fmt.Errorf("candidates[%d].app is not a safe normalized app identity", index)
	}
	if !validMigrationName(name) {
		return fmt.Errorf("candidates[%d].name is not a safe migration identity", index)
	}
	if len(app) > projectspec.MaxSchemaStringBytes || len(name) > projectspec.MaxSchemaStringBytes {
		return fmt.Errorf("candidates[%d] identity exceeds %d bytes", index, projectspec.MaxSchemaStringBytes)
	}
	return nil
}

func validAppIdentity(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for index, character := range []byte(value) {
		if index == 0 {
			if (character < 'a' || character > 'z') && character != '_' {
				return false
			}
			continue
		}
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validMigrationName(value string) bool {
	if len(value) < 6 || !utf8.ValidString(value) || value[4] != '_' {
		return false
	}
	for index, character := range []byte(value) {
		if index < 4 {
			if character < '0' || character > '9' {
				return false
			}
			continue
		}
		if index == 4 {
			continue
		}
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validateWriterRoot(root string) error {
	if root == "" || !utf8.ValidString(root) || len(root) > definition.MaxSourceIDBytes ||
		strings.ContainsAny(root, "\\\x00") || path.IsAbs(root) || path.Clean(root) != root {
		return errors.New("writer_root is not one clean bounded relative slash path")
	}
	for _, component := range strings.Split(root, "/") {
		if component == "" || component == ".." {
			return errors.New("writer_root contains an invalid path component")
		}
	}
	return nil
}

func validateSourceID(owner, sourceID string) error {
	if sourceID == "" || !utf8.ValidString(sourceID) || len(sourceID) > definition.MaxSourceIDBytes {
		return fmt.Errorf("%s is not a bounded non-empty UTF-8 source ID", owner)
	}
	return nil
}

func validDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && len(value) == len("sha256:")+64 && validLowerHex(value[len("sha256:"):])
}

func validRawSHA256(value string) bool {
	return len(value) == 64 && validLowerHex(value)
}

func validLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func checkedAdd(left, right, maximum int) (int, bool) {
	if left < 0 || right < 0 || left > maximum || right > maximum-left {
		return 0, false
	}
	return left + right, true
}

func cloneResult(input Result) Result {
	clone := input
	clone.ProjectSpec = cloneProjectSpec(input.ProjectSpec)
	clone.ProgrammaticCatalog.Sources = make([]Source, len(input.ProgrammaticCatalog.Sources))
	for index := range input.ProgrammaticCatalog.Sources {
		clone.ProgrammaticCatalog.Sources[index] = Source{
			SourceID: input.ProgrammaticCatalog.Sources[index].SourceID,
			Document: append([]byte(nil), input.ProgrammaticCatalog.Sources[index].Document...),
		}
	}
	clone.Candidates = make([]Candidate, len(input.Candidates))
	for index := range input.Candidates {
		clone.Candidates[index] = Candidate{
			App:      input.Candidates[index].App,
			Name:     input.Candidates[index].Name,
			Document: append([]byte(nil), input.Candidates[index].Document...),
		}
	}
	return clone
}

func cloneProjectSpec(input codegen.ProjectSpec) codegen.ProjectSpec {
	clone := input
	clone.Apps = make([]codegen.AppSpec, len(input.Apps))
	for index := range input.Apps {
		clone.Apps[index] = input.Apps[index]
		clone.Apps[index].Schema = input.Apps[index].Schema.Clone()
	}
	return clone
}

func toWireResult(input Result) wireResult {
	programmatic := wireProgrammaticCatalog{
		SourceCount:   input.ProgrammaticCatalog.SourceCount,
		DocumentBytes: input.ProgrammaticCatalog.DocumentBytes,
		Digest:        input.ProgrammaticCatalog.Digest,
		Sources:       make([]wireSource, len(input.ProgrammaticCatalog.Sources)),
	}
	for index := range input.ProgrammaticCatalog.Sources {
		programmatic.Sources[index] = wireSource{
			SourceID: input.ProgrammaticCatalog.Sources[index].SourceID,
			Document: append([]byte(nil), input.ProgrammaticCatalog.Sources[index].Document...),
		}
	}
	candidates := make([]wireCandidate, len(input.Candidates))
	for index := range input.Candidates {
		candidates[index] = wireCandidate{
			App:      input.Candidates[index].App,
			Name:     input.Candidates[index].Name,
			Document: append([]byte(nil), input.Candidates[index].Document...),
		}
	}
	return wireResult{
		WriterRoot:            input.WriterRoot,
		ProjectSpec:           toWireProjectSpec(input.ProjectSpec),
		ProjectSpecDigest:     input.ProjectSpecDigest,
		ProjectSnapshotSHA256: input.ProjectSnapshotSHA256,
		FilesystemCatalog:     toWireCatalogSummary(input.FilesystemCatalog),
		ProgrammaticCatalog:   programmatic,
		DefinitionSetDigest:   input.DefinitionSetDigest,
		Candidates:            candidates,
	}
}

func fromWireResult(input wireResult) Result {
	programmatic := ProgrammaticCatalog{
		SourceCount:   input.ProgrammaticCatalog.SourceCount,
		DocumentBytes: input.ProgrammaticCatalog.DocumentBytes,
		Digest:        input.ProgrammaticCatalog.Digest,
		Sources:       make([]Source, len(input.ProgrammaticCatalog.Sources)),
	}
	for index := range input.ProgrammaticCatalog.Sources {
		programmatic.Sources[index] = Source{
			SourceID: input.ProgrammaticCatalog.Sources[index].SourceID,
			Document: append([]byte(nil), input.ProgrammaticCatalog.Sources[index].Document...),
		}
	}
	candidates := make([]Candidate, len(input.Candidates))
	for index := range input.Candidates {
		candidates[index] = Candidate{
			App:      input.Candidates[index].App,
			Name:     input.Candidates[index].Name,
			Document: append([]byte(nil), input.Candidates[index].Document...),
		}
	}
	return Result{
		WriterRoot:            input.WriterRoot,
		ProjectSpec:           fromWireProjectSpec(input.ProjectSpec),
		ProjectSpecDigest:     input.ProjectSpecDigest,
		ProjectSnapshotSHA256: input.ProjectSnapshotSHA256,
		FilesystemCatalog:     fromWireCatalogSummary(input.FilesystemCatalog),
		ProgrammaticCatalog:   programmatic,
		DefinitionSetDigest:   input.DefinitionSetDigest,
		Candidates:            candidates,
	}
}

func toWireProjectSpec(input codegen.ProjectSpec) wireProjectSpec {
	result := wireProjectSpec{
		Project: toWirePackage(input.Project),
		Apps:    make([]wireApp, len(input.Apps)),
	}
	for index := range input.Apps {
		result.Apps[index] = wireApp{
			Alias:   input.Apps[index].Alias,
			Package: toWirePackage(input.Apps[index].Package),
			Schema:  input.Apps[index].Schema.Clone(),
		}
	}
	return result
}

func fromWireProjectSpec(input wireProjectSpec) codegen.ProjectSpec {
	result := codegen.ProjectSpec{
		Project: fromWirePackage(input.Project),
		Apps:    make([]codegen.AppSpec, len(input.Apps)),
	}
	for index := range input.Apps {
		result.Apps[index] = codegen.AppSpec{
			Alias:   input.Apps[index].Alias,
			Package: fromWirePackage(input.Apps[index].Package),
			Schema:  input.Apps[index].Schema.Clone(),
		}
	}
	return result
}

func toWirePackage(input codegen.PackageSpec) wirePackage {
	return wirePackage{PackageName: input.PackageName, ImportPath: input.ImportPath, Directory: input.Directory}
}

func fromWirePackage(input wirePackage) codegen.PackageSpec {
	return codegen.PackageSpec{PackageName: input.PackageName, ImportPath: input.ImportPath, Directory: input.Directory}
}

func toWireCatalogSummary(input CatalogSummary) wireCatalogSummary {
	return wireCatalogSummary{SourceCount: input.SourceCount, DocumentBytes: input.DocumentBytes, Digest: input.Digest}
}

func fromWireCatalogSummary(input wireCatalogSummary) CatalogSummary {
	return CatalogSummary{SourceCount: input.SourceCount, DocumentBytes: input.DocumentBytes, Digest: input.Digest}
}

func isZeroResult(result Result) bool {
	return result.WriterRoot == "" &&
		result.ProjectSpec.Project == (codegen.PackageSpec{}) && len(result.ProjectSpec.Apps) == 0 &&
		result.ProjectSpecDigest == "" && result.ProjectSnapshotSHA256 == "" &&
		result.FilesystemCatalog == (CatalogSummary{}) &&
		result.ProgrammaticCatalog.SourceCount == 0 && result.ProgrammaticCatalog.DocumentBytes == 0 &&
		result.ProgrammaticCatalog.Digest == "" && len(result.ProgrammaticCatalog.Sources) == 0 &&
		result.DefinitionSetDigest == "" && len(result.Candidates) == 0
}
