// Package protocol defines the private, closed project-linked
// makemigrations wire shared by the global command and a project runner.
//
// This protocol is deliberately separate from the check, generate, and
// migrate private protocols. Its success document owns one normalized project
// declaration, the exact programmatic migration documents, and the exact
// ordered candidate documents produced from the same child request.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const (
	Version          uint64 = 1
	PrivateArgument         = "__godj_project_makemigrations_runner_v1"
	MaxRequestBytes         = 64 << 10
	MaxResponseBytes        = 96 << 20
	MaxProjectApps          = 8_192
	MaxJSONDepth            = 64
	// MaxCandidates bounds one writer invocation independently of the larger
	// historical catalog bound. Publication performs a full source/catalog CAS
	// before every append, so accepting the entire 2,048-source catalog as one
	// batch would make an otherwise bounded request operationally unbounded.
	MaxCandidates          = 64
	MaxProgrammaticSources = definition.MaxSources
)

const (
	CategoryProtocol    = "migration_writer_protocol_error"
	CategoryDeclaration = "migration_writer_declaration_error"
	CategoryDiscovery   = "migration_definition_discovery_error"
	CategorySource      = "migration_definition_source_error"
	CategoryGraph       = "migration_graph_error"
	CategoryDetection   = "migration_autodetect_error"
	CategoryCandidate   = "migration_writer_candidate_error"
)

const (
	CodeInvalidRequest        = "invalid_project_makemigrations_runner_request"
	CodeProtocolIncompatible  = "project_makemigrations_protocol_incompatible"
	CodeInvalidResponse       = "invalid_project_makemigrations_runner_response"
	CodeRunnerFailed          = "project_makemigrations_runner_failed"
	CodeProjectSpecLoadFailed = "project_spec_load_failed"

	CodeInvalidProjectSourceConfig = "invalid_project_source_config"
	CodeInvalidSourceRoot          = "invalid_source_root"
	CodeInvalidSourceEntry         = "invalid_source_entry"
	CodeUnsafeSourceEntry          = "unsafe_source_entry"
	CodeSourceCatalogLimitExceeded = "source_catalog_limit_exceeded"
	CodeSourceDiscoveryFailed      = "source_discovery_failed"
	CodeSourceReadFailed           = "source_read_failed"

	CodeCandidateEncodeFailed          = "candidate_encode_failed"
	CodeCandidateValidationFailed      = "candidate_validation_failed"
	CodeCandidateResourceLimitExceeded = "candidate_resource_limit_exceeded"
)

// Failure is the detail-free category/code pair permitted on this private
// wire. Diagnostic text, paths, secrets, and raw migration documents do not
// cross the process boundary on failures.
type Failure struct {
	Category string
	Code     string
}

// CatalogSummary identifies one bounded physical source catalog snapshot.
// Digest uses a caller-owned versioned domain and must be strict lowercase
// sha256:<64-hex>. DocumentBytes is the sum of exact decoded source bytes.
type CatalogSummary struct {
	SourceCount   int
	DocumentBytes int
	Digest        string
}

// Source is one exact programmatic definition source. Document is copied at
// every protocol boundary and encoded as canonical padded base64 JSON.
type Source struct {
	SourceID string
	Document []byte
}

// ProgrammaticCatalog carries both its summary and its complete canonical
// SourceID-ordered source roster. The summary count and byte total must equal
// the roster exactly.
type ProgrammaticCatalog struct {
	SourceCount   int
	DocumentBytes int
	Digest        string
	Sources       []Source
}

// Candidate is one exact migration definition candidate. The global owner
// derives its basename, project-relative path, SourceID, and hashes; none of
// those redundant values are accepted on this wire.
type Candidate struct {
	App      string
	Name     string
	Document []byte
}

// Result is the immutable semantic output of one child snapshot request.
// Candidates preserve dependency-valid detector order. ProjectSpecDigest is
// derived from the normalized ProjectSpec by ProjectSpecDigest; the raw
// ProjectSnapshotSHA256 is the distinct generated-bundle snapshot identity.
type Result struct {
	WriterRoot            string
	ProjectSpec           codegen.ProjectSpec
	ProjectSpecDigest     string
	ProjectSnapshotSHA256 string
	FilesystemCatalog     CatalogSummary
	ProgrammaticCatalog   ProgrammaticCatalog
	DefinitionSetDigest   string
	Candidates            []Candidate
}

// Response is one closed private outcome. OK selects Result; otherwise only a
// linked Failure may be present.
type Response struct {
	OK      bool
	Result  Result
	Failure Failure
}

type wirePackage struct {
	PackageName string `json:"package_name"`
	ImportPath  string `json:"import_path"`
	Directory   string `json:"directory"`
}

type wireApp struct {
	Alias   string      `json:"alias"`
	Package wirePackage `json:"package"`
	Schema  ir.Schema   `json:"schema"`
}

type wireProjectSpec struct {
	Project wirePackage `json:"project"`
	Apps    []wireApp   `json:"apps"`
}

type wireCatalogSummary struct {
	SourceCount   int    `json:"source_count"`
	DocumentBytes int    `json:"document_bytes"`
	Digest        string `json:"digest"`
}

type wireSource struct {
	SourceID string `json:"source_id"`
	Document []byte `json:"document"`
}

type wireProgrammaticCatalog struct {
	SourceCount   int          `json:"source_count"`
	DocumentBytes int          `json:"document_bytes"`
	Digest        string       `json:"digest"`
	Sources       []wireSource `json:"sources"`
}

type wireCandidate struct {
	App      string `json:"app"`
	Name     string `json:"name"`
	Document []byte `json:"document"`
}

type wireResult struct {
	WriterRoot            string                  `json:"writer_root"`
	ProjectSpec           wireProjectSpec         `json:"project_spec"`
	ProjectSpecDigest     string                  `json:"project_spec_digest"`
	ProjectSnapshotSHA256 string                  `json:"project_snapshot_sha256"`
	FilesystemCatalog     wireCatalogSummary      `json:"filesystem_catalog"`
	ProgrammaticCatalog   wireProgrammaticCatalog `json:"programmatic_catalog"`
	DefinitionSetDigest   string                  `json:"definition_set_digest"`
	Candidates            []wireCandidate         `json:"candidates"`
}

type successDocument struct {
	ProtocolVersion uint64     `json:"protocol_version"`
	Status          string     `json:"status"`
	Result          wireResult `json:"result"`
}

type failureDocument struct {
	ProtocolVersion uint64      `json:"protocol_version"`
	Status          string      `json:"status"`
	Error           wireFailure `json:"error"`
}

type wireFailure struct {
	Category string `json:"category"`
	Code     string `json:"code"`
}

type requestDocument struct {
	ProtocolVersion uint64 `json:"protocol_version"`
	Command         string `json:"command"`
}

// RequestDocument returns a fresh copy of the sole canonical request. The
// terminal newline is part of the protocol framing and is mandatory.
func RequestDocument() []byte {
	return []byte("{\"protocol_version\":1,\"command\":\"migrations.makemigrations\"}\n")
}

// ReadRequest reads through EOF within a fixed retention bound. A completed
// malformed document is a logical protocol failure; a Reader error, including
// no progress, remains a Go transport error and takes precedence.
func ReadRequest(reader io.Reader) (Failure, bool, error) {
	if reader == nil {
		return Failure{}, false, errors.New("project makemigrations protocol: nil request reader")
	}
	document, err := readAtMost(reader, MaxRequestBytes)
	if err != nil {
		return Failure{}, false, fmt.Errorf("project makemigrations protocol: read request: %w", err)
	}
	return parseRequest(document)
}

// ParseResponse validates completed response bytes. Transport failure takes
// precedence over all bytes. A valid linked logical failure remains in the
// returned Response and is not reclassified by the global owner.
func ParseResponse(document []byte, transportOK bool) (Response, Failure, bool) {
	if !transportOK {
		return Response{}, Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}, true
	}
	status, version, err := preflightResponse(document)
	if err != nil {
		return invalidResponse()
	}
	switch status {
	case "ok":
		var decoded successDocument
		if err := decodeCanonical(document, &decoded); err != nil {
			return invalidResponse()
		}
		if version != Version {
			return Response{}, Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true
		}
		result := fromWireResult(decoded.Result)
		canonical, err := canonicalResult(result, true)
		if err != nil {
			return invalidResponse()
		}
		return Response{OK: true, Result: canonical}, Failure{}, false
	case "error":
		var decoded failureDocument
		if err := decodeCanonical(document, &decoded); err != nil {
			return invalidResponse()
		}
		if version != Version {
			return Response{}, Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true
		}
		failure := Failure{Category: decoded.Error.Category, Code: decoded.Error.Code}
		if !IsLinkedFailure(failure) {
			return invalidResponse()
		}
		return Response{Failure: failure}, Failure{}, false
	default:
		return invalidResponse()
	}
}

// EncodeResponse returns deterministic canonical, bounded response bytes. A
// success input is deeply snapshotted and its ProjectSpec is normalized before
// encoding; the caller's slices are never retained or reordered in place.
func EncodeResponse(response Response) ([]byte, error) {
	var document []byte
	var err error
	if response.OK {
		if response.Failure != (Failure{}) {
			return nil, errors.New("project makemigrations protocol: invalid success response")
		}
		canonical, canonicalErr := canonicalResult(response.Result, false)
		if canonicalErr != nil {
			return nil, fmt.Errorf("project makemigrations protocol: invalid success response: %w", canonicalErr)
		}
		wire := toWireResult(canonical)
		if err := measureSuccessDocument(wire); err != nil {
			return nil, err
		}
		document, err = json.Marshal(successDocument{
			ProtocolVersion: Version,
			Status:          "ok",
			Result:          wire,
		})
	} else {
		if !isZeroResult(response.Result) || !IsLinkedFailure(response.Failure) {
			return nil, errors.New("project makemigrations protocol: invalid error response")
		}
		document, err = json.Marshal(failureDocument{
			ProtocolVersion: Version,
			Status:          "error",
			Error:           wireFailure{Category: response.Failure.Category, Code: response.Failure.Code},
		})
	}
	if err != nil {
		return nil, fmt.Errorf("project makemigrations protocol: encode response: %w", err)
	}
	if err := validateDocumentSize(len(document), MaxResponseBytes); err != nil {
		return nil, err
	}
	return document, nil
}

// WriteResponse encodes and performs exactly one complete write attempt.
func WriteResponse(writer io.Writer, response Response) error {
	if writer == nil {
		return errors.New("project makemigrations protocol: nil response writer")
	}
	document, err := EncodeResponse(response)
	if err != nil {
		return err
	}
	written, err := writer.Write(document)
	if err != nil {
		return fmt.Errorf("project makemigrations protocol: write response: %w", err)
	}
	if written != len(document) {
		return fmt.Errorf("project makemigrations protocol: write response: %w", io.ErrShortWrite)
	}
	return nil
}

// IsLinkedFailure reports whether a pair may be emitted by the project child.
func IsLinkedFailure(failure Failure) bool {
	switch failure.Category {
	case CategoryProtocol:
		return oneOf(failure.Code, CodeInvalidRequest, CodeProtocolIncompatible)
	case CategoryDeclaration:
		return failure.Code == CodeProjectSpecLoadFailed
	case CategoryDiscovery:
		return oneOf(failure.Code,
			CodeInvalidProjectSourceConfig,
			CodeInvalidSourceRoot,
			CodeInvalidSourceEntry,
			CodeUnsafeSourceEntry,
			CodeSourceCatalogLimitExceeded,
			CodeSourceDiscoveryFailed,
			CodeSourceReadFailed,
		)
	case CategorySource:
		return oneOf(failure.Code,
			"invalid_definition_source",
			"invalid_definition_document",
			"definition_format_incompatible",
			"unsupported_definition_operation",
			"invalid_definition_operation",
			"invalid_definition_ir",
		)
	case CategoryGraph:
		return oneOf(failure.Code,
			"invalid_node",
			"duplicate_node",
			"invalid_dependency",
			"duplicate_dependency",
			"dependency_not_found",
			"dependency_cycle",
		)
	case CategoryDetection:
		return oneOf(failure.Code,
			"invalid_request",
			"unsupported_change",
			"ambiguous_history",
			"invalid_relation",
			"invalid_generated_plan",
		)
	case CategoryCandidate:
		return oneOf(failure.Code,
			CodeCandidateEncodeFailed,
			CodeCandidateValidationFailed,
			CodeCandidateResourceLimitExceeded,
		)
	default:
		return false
	}
}

func parseRequest(document []byte) (Failure, bool, error) {
	if err := scanJSONDocument(document, MaxRequestBytes); err != nil {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	var request requestDocument
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	if request.ProtocolVersion > 65_535 {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	canonical, err := json.Marshal(request)
	if err != nil {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(document, canonical) {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	if request.ProtocolVersion != Version {
		return Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true, nil
	}
	if request.Command != "migrations.makemigrations" || !bytes.Equal(document, RequestDocument()) {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	return Failure{}, false, nil
}

func invalidResponse() (Response, Failure, bool) {
	return Response{}, Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}, true
}

func readAtMost(reader io.Reader, maximum int) ([]byte, error) {
	retained := make([]byte, 0, maximum+1)
	buffer := make([]byte, 32<<10)
	emptyReads := 0
	for {
		count, err := reader.Read(buffer)
		if count < 0 || count > len(buffer) {
			return nil, errors.New("invalid request reader count")
		}
		if count > 0 {
			emptyReads = 0
			remaining := maximum + 1 - len(retained)
			if remaining > 0 {
				if count < remaining {
					remaining = count
				}
				retained = append(retained, buffer[:remaining]...)
			}
		} else if err == nil {
			emptyReads++
			if emptyReads >= 100 {
				return nil, io.ErrNoProgress
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return retained, nil
			}
			return nil, err
		}
	}
}

func validateDocumentSize(size, maximum int) error {
	if size > maximum {
		return fmt.Errorf("project makemigrations protocol: document size %d exceeds %d", size, maximum)
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
