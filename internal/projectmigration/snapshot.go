// Package projectmigration owns the pure project-linked makemigrations
// snapshot. Filesystem discovery, private transport, locking and publication
// are deliberately outside this package.
package projectmigration

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"path"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/migrationautodetect"
	"github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const (
	filesystemCatalogDigestDomain   = "godj/makemigrations-source-catalog/v1\x00"
	programmaticCatalogDigestDomain = "godj/makemigrations-programmatic-source-catalog/v1\x00"

	candidateProducerName    = "godj-makemigrations"
	candidateProducerVersion = "1"
)

// ErrorCategory is the closed snapshot failure category vocabulary.
type ErrorCategory string

const (
	CategoryRequest   ErrorCategory = "project_migration_request_error"
	CategoryProject   ErrorCategory = "project_migration_project_error"
	CategoryCatalog   ErrorCategory = "project_migration_catalog_error"
	CategoryPlanning  ErrorCategory = "project_migration_planning_error"
	CategoryCandidate ErrorCategory = "project_migration_candidate_error"
)

// ErrorCode is the closed snapshot failure code vocabulary.
type ErrorCode string

const (
	CodeInvalidWriterRoot        ErrorCode = "invalid_writer_root"
	CodeInvalidProjectSpec       ErrorCode = "invalid_project_spec"
	CodeInvalidGeneratorIdentity ErrorCode = "invalid_generator_identity"
	CodeCatalogResourceLimit     ErrorCode = "catalog_resource_limit_exceeded"
	CodeInvalidCatalog           ErrorCode = "invalid_catalog"
	CodeUnsupportedChange        ErrorCode = "unsupported_change"
	CodeInvalidPlan              ErrorCode = "invalid_plan"
	CodeCandidateEncodingFailed  ErrorCode = "candidate_encoding_failed"
	CodeInvalidCandidateCatalog  ErrorCode = "invalid_candidate_catalog"
	CodeFinalStateMismatch       ErrorCode = "final_state_mismatch"
)

// Error carries an inspectable cause without rendering source identifiers,
// source bytes or other project-owned values through Error().
type Error struct {
	Category ErrorCategory
	Code     ErrorCode

	cause error
}

func (e *Error) Error() string {
	if e == nil {
		return "project migration snapshot error"
	}
	return string(e.Category) + "/" + string(e.Code)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Request contains the exact declaration and catalog values owned by one
// project-child request. BuildSnapshot synchronously deep-copies every value.
type Request struct {
	ProjectSpec         codegen.ProjectSpec
	FilesystemSources   []definition.Source
	ProgrammaticSources []definition.Source
	WriterRoot          string
}

// Candidate is one immutable encoded migration in dependency-valid
// topological order. Its document accessor returns a fresh clone.
type Candidate struct {
	app      string
	name     string
	document []byte
}

func (c Candidate) App() string { return c.app }

func (c Candidate) Name() string { return c.name }

func (c Candidate) Document() []byte { return append([]byte(nil), c.document...) }

// Snapshot is the immutable output of one pure schema-and-catalog request.
// Its zero value is not an initialized authority.
type Snapshot struct {
	initialized bool

	projectSpec                   codegen.ProjectSpec
	desiredState                  migrations.ProjectState
	filesystemSources             []definition.Source
	programmaticSources           []definition.Source
	writerRoot                    string
	managedApps                   []string
	candidates                    []Candidate
	projectSpecDigest             string
	generatedBundleSnapshotSHA256 string
	filesystemCatalogDigest       string
	programmaticCatalogDigest     string
	existingSemanticDigest        string
	finalSemanticDigest           string
}

// Initialized reports whether this value was produced successfully by
// BuildSnapshot.
func (s Snapshot) Initialized() bool { return s.initialized }

func (s Snapshot) ProjectSpec() codegen.ProjectSpec { return cloneProjectSpec(s.projectSpec) }

func (s Snapshot) DesiredState() migrations.ProjectState { return s.desiredState.Clone() }

func (s Snapshot) FilesystemSources() []definition.Source {
	return cloneSources(s.filesystemSources)
}

func (s Snapshot) ProgrammaticSources() []definition.Source {
	return cloneSources(s.programmaticSources)
}

func (s Snapshot) WriterRoot() string { return s.writerRoot }

func (s Snapshot) ManagedApps() []string { return append([]string(nil), s.managedApps...) }

func (s Snapshot) Candidates() []Candidate { return cloneCandidates(s.candidates) }

func (s Snapshot) ProjectSpecDigest() string { return s.projectSpecDigest }

func (s Snapshot) GeneratedBundleSnapshotSHA256() string {
	return s.generatedBundleSnapshotSHA256
}

func (s Snapshot) FilesystemCatalogDigest() string { return s.filesystemCatalogDigest }

func (s Snapshot) ProgrammaticCatalogDigest() string { return s.programmaticCatalogDigest }

func (s Snapshot) ExistingSemanticDigest() string { return s.existingSemanticDigest }

func (s Snapshot) FinalSemanticDigest() string { return s.finalSemanticDigest }

// BuildSnapshot validates and owns one normalized ProjectSpec and exact source
// catalog, detects all current additive candidates, encodes them, and then
// re-loads every dependency prefix through the strict definition boundary.
// It performs no I/O.
func BuildSnapshot(request Request) (Snapshot, error) {
	if err := validateWriterRoot(request.WriterRoot); err != nil {
		return Snapshot{}, snapshotError(CategoryRequest, CodeInvalidWriterRoot, err)
	}
	if err := codegen.ValidateProjectSpec(request.ProjectSpec); err != nil {
		return Snapshot{}, snapshotError(CategoryProject, CodeInvalidProjectSpec, err)
	}
	if err := validateSourceResourceBounds(request.FilesystemSources, request.ProgrammaticSources); err != nil {
		return Snapshot{}, snapshotError(CategoryCatalog, CodeCatalogResourceLimit, err)
	}

	// cloneProjectSpec canonicalizes a nil empty app roster to an owned empty
	// slice before the strict private-result normalization boundary.
	normalized, err := protocol.NormalizeProjectSpec(cloneProjectSpec(request.ProjectSpec))
	if err != nil {
		return Snapshot{}, snapshotError(CategoryProject, CodeInvalidProjectSpec, err)
	}
	bundle, err := codegen.GenerateProject(normalized)
	if err != nil {
		return Snapshot{}, snapshotError(CategoryProject, CodeInvalidGeneratorIdentity, err)
	}
	if !validGeneratedIdentity(bundle.SnapshotSHA256()) {
		return Snapshot{}, snapshotError(CategoryProject, CodeInvalidGeneratorIdentity, errors.New("empty or malformed generated bundle identity"))
	}

	desired, err := desiredState(normalized)
	if err != nil {
		return Snapshot{}, snapshotError(CategoryProject, CodeInvalidProjectSpec, err)
	}
	specDigest, err := protocol.ProjectSpecDigest(normalized)
	if err != nil {
		return Snapshot{}, snapshotError(CategoryProject, CodeInvalidProjectSpec, err)
	}

	filesystem := canonicalSources(request.FilesystemSources)
	programmatic := canonicalSources(request.ProgrammaticSources)
	combined := mergeSources(filesystem, programmatic)
	loaded, _, err := definition.Load(combined...)
	if err != nil {
		return Snapshot{}, snapshotError(CategoryCatalog, CodeInvalidCatalog, err)
	}

	current, err := reconstructLatest(loaded)
	if err != nil {
		return Snapshot{}, snapshotError(CategoryCatalog, CodeInvalidCatalog, err)
	}
	managed, err := managedApps(normalized, filesystem, loaded)
	if err != nil {
		return Snapshot{}, snapshotError(CategoryCatalog, CodeInvalidCatalog, err)
	}

	plan, err := migrationautodetect.Detect(migrationautodetect.Request{
		Definitions: loaded,
		Desired:     desired,
		ManagedApps: managed,
	})
	if err != nil {
		var detection *migrationautodetect.Error
		if errors.As(err, &detection) && detection != nil && detection.Code == migrationautodetect.CodeUnsupportedChange {
			return Snapshot{}, snapshotError(CategoryPlanning, CodeUnsupportedChange, err)
		}
		return Snapshot{}, snapshotError(CategoryPlanning, CodeInvalidPlan, err)
	}

	migrationsPlan := plan.Migrations()
	if len(migrationsPlan) > definition.MaxSources-len(combined) {
		return Snapshot{}, snapshotError(
			CategoryCandidate,
			CodeInvalidCandidateCatalog,
			errors.New("combined candidate source count exceeds current definition catalog bounds"),
		)
	}
	for index := range migrationsPlan {
		if _, sourceErr := candidateSourceID(request.WriterRoot, Candidate{
			app:  migrationsPlan[index].App,
			name: migrationsPlan[index].Name,
		}); sourceErr != nil {
			return Snapshot{}, snapshotError(CategoryCandidate, CodeInvalidCandidateCatalog, sourceErr)
		}
	}
	existingDocumentBytes := sourceDocumentBytes(combined)
	candidateDocumentBytes := uint64(0)
	candidates := make([]Candidate, len(migrationsPlan))
	for index := range migrationsPlan {
		document, encodeErr := definition.Encode(definition.Producer{
			Name:    candidateProducerName,
			Version: candidateProducerVersion,
		}, migrationsPlan[index])
		if encodeErr != nil {
			return Snapshot{}, snapshotError(CategoryCandidate, CodeCandidateEncodingFailed, encodeErr)
		}
		documentBytes := uint64(len(document))
		remaining := uint64(definition.MaxBatchBytes) - existingDocumentBytes
		if documentBytes > remaining || candidateDocumentBytes > remaining-documentBytes {
			return Snapshot{}, snapshotError(
				CategoryCandidate,
				CodeInvalidCandidateCatalog,
				errors.New("combined candidate document bytes exceed current definition catalog bounds"),
			)
		}
		candidateDocumentBytes += documentBytes
		candidates[index] = Candidate{
			app:      migrationsPlan[index].App,
			name:     migrationsPlan[index].Name,
			document: append([]byte(nil), document...),
		}
	}

	prefixSources := cloneSources(combined)
	finalLoaded := loaded
	finalState := current
	for index := range candidates {
		sourceID, sourceErr := candidateSourceID(request.WriterRoot, candidates[index])
		if sourceErr != nil {
			return Snapshot{}, snapshotError(CategoryCandidate, CodeInvalidCandidateCatalog, sourceErr)
		}
		prefixSources = append(prefixSources, definition.Source{
			SourceID: sourceID,
			Document: candidates[index].Document(),
		})
		finalLoaded, _, err = definition.Load(prefixSources...)
		if err != nil {
			return Snapshot{}, snapshotError(CategoryCandidate, CodeInvalidCandidateCatalog, err)
		}
		finalState, err = reconstructLatest(finalLoaded)
		if err != nil {
			return Snapshot{}, snapshotError(CategoryCandidate, CodeInvalidCandidateCatalog, err)
		}
	}
	if err := verifyFinalState(current, desired, finalState, managed); err != nil {
		return Snapshot{}, snapshotError(CategoryCandidate, CodeFinalStateMismatch, err)
	}

	return Snapshot{
		initialized:                   true,
		projectSpec:                   cloneProjectSpec(normalized),
		desiredState:                  desired.Clone(),
		filesystemSources:             cloneSources(filesystem),
		programmaticSources:           cloneSources(programmatic),
		writerRoot:                    request.WriterRoot,
		managedApps:                   append([]string(nil), managed...),
		candidates:                    cloneCandidates(candidates),
		projectSpecDigest:             specDigest,
		generatedBundleSnapshotSHA256: bundle.SnapshotSHA256(),
		filesystemCatalogDigest:       digestSources(filesystemCatalogDigestDomain, filesystem),
		programmaticCatalogDigest:     digestSources(programmaticCatalogDigestDomain, programmatic),
		existingSemanticDigest:        loaded.Digest(),
		finalSemanticDigest:           finalLoaded.Digest(),
	}, nil
}

func snapshotError(category ErrorCategory, code ErrorCode, cause error) *Error {
	return &Error{Category: category, Code: code, cause: cause}
}

func validateWriterRoot(root string) error {
	if root == "" || !utf8.ValidString(root) || len([]byte(root)) > definition.MaxSourceIDBytes ||
		path.IsAbs(root) || path.Clean(root) != root || strings.ContainsAny(root, "\\\x00") {
		return errors.New("writer root is not one clean project-relative path")
	}
	for _, component := range strings.Split(root, "/") {
		if component == "" || component == ".." {
			return errors.New("writer root contains an invalid path component")
		}
	}
	return nil
}

func cloneProjectSpec(input codegen.ProjectSpec) codegen.ProjectSpec {
	cloned := codegen.ProjectSpec{Project: input.Project, Apps: make([]codegen.AppSpec, len(input.Apps))}
	for index := range input.Apps {
		cloned.Apps[index] = codegen.AppSpec{
			Alias:   input.Apps[index].Alias,
			Package: input.Apps[index].Package,
			Schema:  input.Apps[index].Schema.Clone(),
		}
	}
	return cloned
}

func desiredState(spec codegen.ProjectSpec) (migrations.ProjectState, error) {
	schemas := make([]ir.Schema, len(spec.Apps))
	for index := range spec.Apps {
		schemas[index] = spec.Apps[index].Schema.Clone()
	}
	return migrations.NewProjectState(schemas...)
}

func canonicalSources(input []definition.Source) []definition.Source {
	cloned := cloneSources(input)
	sort.Slice(cloned, func(left, right int) bool {
		return bytes.Compare([]byte(cloned[left].SourceID), []byte(cloned[right].SourceID)) < 0
	})
	return cloned
}

func validateSourceResourceBounds(catalogs ...[]definition.Source) error {
	count := 0
	var batchBytes uint64
	for _, sources := range catalogs {
		if len(sources) > definition.MaxSources-count {
			return errors.New("source count exceeds current definition catalog bounds")
		}
		count += len(sources)
		for index := range sources {
			if len([]byte(sources[index].SourceID)) > definition.MaxSourceIDBytes {
				return errors.New("source identity exceeds current definition catalog bounds")
			}
			documentBytes := uint64(len(sources[index].Document))
			if documentBytes > definition.MaxDocumentBytes {
				return errors.New("source document exceeds current definition catalog bounds")
			}
			if batchBytes > definition.MaxBatchBytes-documentBytes {
				return errors.New("source batch exceeds current definition catalog bounds")
			}
			batchBytes += documentBytes
		}
	}
	return nil
}

func sourceDocumentBytes(sources []definition.Source) uint64 {
	var total uint64
	for index := range sources {
		total += uint64(len(sources[index].Document))
	}
	return total
}

func cloneSources(input []definition.Source) []definition.Source {
	if len(input) == 0 {
		return nil
	}
	cloned := make([]definition.Source, len(input))
	for index := range input {
		cloned[index] = definition.Source{
			SourceID: strings.Clone(input[index].SourceID),
			Document: append([]byte(nil), input[index].Document...),
		}
	}
	return cloned
}

func mergeSources(filesystem, programmatic []definition.Source) []definition.Source {
	combined := make([]definition.Source, 0, len(filesystem)+len(programmatic))
	combined = append(combined, cloneSources(filesystem)...)
	combined = append(combined, cloneSources(programmatic)...)
	sort.Slice(combined, func(left, right int) bool {
		return bytes.Compare([]byte(combined[left].SourceID), []byte(combined[right].SourceID)) < 0
	})
	return combined
}

func digestSources(domain string, sources []definition.Source) string {
	order := make([]int, len(sources))
	for index := range sources {
		order[index] = index
	}
	sort.Slice(order, func(left, right int) bool {
		return bytes.Compare([]byte(sources[order[left]].SourceID), []byte(sources[order[right]].SourceID)) < 0
	})
	digest := sha256.New()
	_, _ = digest.Write([]byte(domain))
	writeUint64(digest, uint64(len(sources)))
	for _, index := range order {
		source := sources[index]
		writeUint64(digest, uint64(len([]byte(source.SourceID))))
		_, _ = digest.Write([]byte(source.SourceID))
		writeUint64(digest, uint64(len(source.Document)))
		_, _ = digest.Write(source.Document)
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeUint64(writer byteWriter, value uint64) {
	var framed [8]byte
	binary.BigEndian.PutUint64(framed[:], value)
	_, _ = writer.Write(framed[:])
}

func reconstructLatest(loaded migrations.LoadedDefinitionSet) (migrations.ProjectState, error) {
	reconstructor, err := migrations.NewStateReconstructor(loaded.Definitions()...)
	if err != nil {
		return migrations.ProjectState{}, err
	}
	return reconstructor.Reconstruct(migrations.LatestStateRequest())
}

func managedApps(
	spec codegen.ProjectSpec,
	filesystem []definition.Source,
	loaded migrations.LoadedDefinitionSet,
) ([]string, error) {
	managed := make(map[string]struct{}, len(spec.Apps)+len(filesystem))
	for _, app := range spec.Apps {
		managed[app.Schema.AppLabel] = struct{}{}
	}
	filesystemIDs := make(map[string]struct{}, len(filesystem))
	for _, source := range filesystem {
		filesystemIDs[source.SourceID] = struct{}{}
	}
	matched := make(map[string]struct{}, len(filesystem))
	for _, source := range loaded.Sources() {
		if _, owned := filesystemIDs[source.SourceID]; !owned {
			continue
		}
		matched[source.SourceID] = struct{}{}
		managed[source.Migration.App] = struct{}{}
	}
	if len(matched) != len(filesystemIDs) {
		return nil, errors.New("loaded source inventory does not preserve filesystem ownership")
	}
	result := make([]string, 0, len(managed))
	for app := range managed {
		result = append(result, app)
	}
	sort.Strings(result)
	return result, nil
}

func candidateSourceID(root string, candidate Candidate) (string, error) {
	basename := candidate.app + "_" + candidate.name + ".godj.json"
	identifier := basename
	if root != "." {
		identifier = root + "/" + basename
	}
	if !utf8.ValidString(identifier) || len([]byte(identifier)) > definition.MaxSourceIDBytes {
		return "", errors.New("candidate source identity exceeds current bounds")
	}
	return identifier, nil
}

func verifyFinalState(
	current migrations.ProjectState,
	desired migrations.ProjectState,
	actual migrations.ProjectState,
	managedApps []string,
) error {
	managed := make(map[string]struct{}, len(managedApps))
	for _, app := range managedApps {
		managed[app] = struct{}{}
	}
	apps := make(map[string]struct{})
	for _, state := range []migrations.ProjectState{current, desired, actual} {
		for _, app := range state.Apps() {
			apps[app] = struct{}{}
		}
	}
	for app := range apps {
		expectedState := current
		if _, owned := managed[app]; owned {
			expectedState = desired
		}
		expected, expectedExists := expectedState.Schema(app)
		got, gotExists := actual.Schema(app)
		if expectedExists != gotExists || (expectedExists && !reflect.DeepEqual(expected, got)) {
			return errors.New("latest reconstructed state differs from the managed desired state")
		}
	}
	return nil
}

func cloneCandidates(input []Candidate) []Candidate {
	if len(input) == 0 {
		return nil
	}
	cloned := make([]Candidate, len(input))
	for index := range input {
		cloned[index] = Candidate{
			app:      input[index].app,
			name:     input[index].name,
			document: append([]byte(nil), input[index].document...),
		}
	}
	return cloned
}

func validGeneratedIdentity(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}
