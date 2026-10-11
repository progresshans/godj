// Package protocol defines the private, closed project-runner protocol used by
// the global GoDj command and a project-linked runner.
package protocol

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/progresshans/godj/internal/projectcheck/failurecode"
	"github.com/progresshans/godj/internal/wirejson"
)

const (
	Version          uint64 = 1
	PrivateArgument         = "__godj_project_runner_v1"
	MaxRequestBytes         = 64 << 10
	MaxResponseBytes        = 64 << 10
	MaxCount                = 2_048

	EmptySetDigest = "sha256:1412c48d7da2299b6f2be7a614c5bb9ce510027328f6baed72ae05cbecc9b494"
)

const (
	CategoryCommand   = "migration_project_command_error"
	CategorySelection = "migration_project_selection_error"
	CategoryBuild     = "migration_project_build_error"
	CategoryProtocol  = "migration_project_protocol_error"
	CategoryProcess   = "migration_project_process_error"
	CategoryDiscovery = "migration_definition_discovery_error"
	CategorySource    = "migration_definition_source_error"
	CategoryGraph     = "migration_graph_error"
	CategoryInternal  = "migration_project_internal_error"

	CodeInvalidArguments              = "invalid_arguments"
	CodeProjectNotFound               = "project_not_found"
	CodeProjectSearchLimitExceeded    = "project_search_limit_exceeded"
	CodeInvalidProjectDescriptor      = "invalid_project_descriptor"
	CodeProjectDescriptorIncompatible = "project_descriptor_incompatible"
	CodeProjectSelectionFailed        = "project_selection_failed"
	CodeProjectTemporaryStorageFailed = "project_temporary_storage_failed"
	CodeProjectBuildFailed            = "project_build_failed"
	CodeInvalidProjectRunnerRequest   = "invalid_project_runner_request"
	CodeProjectRunnerFailed           = "project_runner_failed"
	CodeProjectProtocolIncompatible   = "project_protocol_incompatible"
	CodeInvalidProjectRunnerResponse  = "invalid_project_runner_response"
	CodeProjectCanceled               = "project_canceled"
	CodeProjectCleanupFailed          = "project_cleanup_failed"
	CodeProjectInterrupted            = "project_interrupted"
	CodeInvalidProjectSourceConfig    = "invalid_project_source_config"
	CodeInvalidSourceRoot             = "invalid_source_root"
	CodeInvalidSourceEntry            = "invalid_source_entry"
	CodeUnsafeSourceEntry             = "unsafe_source_entry"
	CodeSourceCatalogLimitExceeded    = "source_catalog_limit_exceeded"
	CodeSourceDiscoveryFailed         = "source_discovery_failed"
	CodeSourceReadFailed              = "source_read_failed"
	CodeProjectInternalError          = "project_internal_error"
)

// Failure is the public-detail-free category/code pair carried over the
// private wire. It deliberately contains no path, document, or diagnostic.
type Failure struct {
	Category string
	Code     string
}

// Result is the complete successful private response payload.
type Result struct {
	SourceCount         int
	DefinitionCount     int
	DefinitionSetDigest string
}

// Response is one closed private outcome. OK selects Result; otherwise
// Failure is selected. EncodeResponse rejects invalid or ambiguous values.
type Response struct {
	OK      bool
	Result  Result
	Failure Failure
}

// RequestDocument returns a fresh copy of the one supported request.
func RequestDocument() []byte {
	return []byte(`{"protocol_version":1,"command":"migrations.check"}`)
}

// ReadRequest reads one bounded request through EOF. Completed malformed input
// is a logical protocol failure; a Reader failure remains a Go transport error.
func ReadRequest(reader io.Reader) (Failure, bool, error) {
	if reader == nil {
		return Failure{}, false, errors.New("project protocol: nil request reader")
	}
	document, err := wirejson.Read(reader, MaxRequestBytes, wirejson.DrainToEOF)
	if err != nil {
		return Failure{}, false, fmt.Errorf("project protocol: read request: %w", err)
	}
	if failure, failed := parseRequest(document); failed {
		return failure, true, nil
	}
	return Failure{}, false, nil
}

// ParseResponse validates a completed runner response. A failed transport has
// precedence over all response bytes. The third result reports a global-owned
// protocol classification; a valid linked logical failure remains in Response.
func ParseResponse(document []byte, transportOK bool) (Response, Failure, bool) {
	if !transportOK {
		return Response{}, Failure{Category: CategoryProtocol, Code: CodeProjectRunnerFailed}, true
	}
	object, err := decodeObject(document, MaxResponseBytes)
	if err != nil {
		return invalidResponse()
	}
	versionValue, exists := object["protocol_version"]
	if !exists {
		return invalidResponse()
	}
	version, valid := wirejson.Uint(versionValue, 65_535)
	if !valid {
		return invalidResponse()
	}
	if version != Version {
		return Response{}, Failure{Category: CategoryProtocol, Code: CodeProjectProtocolIncompatible}, true
	}
	status, ok := object["status"].(string)
	if !ok {
		return invalidResponse()
	}
	switch status {
	case "ok":
		if !wirejson.HasKeys(object, "protocol_version", "status", "result") {
			return invalidResponse()
		}
		resultObject, ok := object["result"].(map[string]any)
		if !ok || !wirejson.HasKeys(resultObject, "source_count", "definition_count", "definition_set_digest") {
			return invalidResponse()
		}
		sourceCount, sourceOK := wirejson.Uint(resultObject["source_count"], MaxCount)
		definitionCount, definitionOK := wirejson.Uint(resultObject["definition_count"], MaxCount)
		digest, digestOK := resultObject["definition_set_digest"].(string)
		result := Result{
			SourceCount:         int(sourceCount),
			DefinitionCount:     int(definitionCount),
			DefinitionSetDigest: digest,
		}
		if !sourceOK || !definitionOK || !digestOK || !validResult(result) {
			return invalidResponse()
		}
		return Response{OK: true, Result: result}, Failure{}, false
	case "error":
		if !wirejson.HasKeys(object, "protocol_version", "status", "error") {
			return invalidResponse()
		}
		errorObject, ok := object["error"].(map[string]any)
		if !ok || !wirejson.HasKeys(errorObject, "category", "code") {
			return invalidResponse()
		}
		category, categoryOK := errorObject["category"].(string)
		code, codeOK := errorObject["code"].(string)
		failure := Failure{Category: category, Code: code}
		if !categoryOK || !codeOK || !IsLinkedFailure(failure) {
			return invalidResponse()
		}
		return Response{Failure: failure}, Failure{}, false
	default:
		return invalidResponse()
	}
}

// EncodeResponse returns the canonical closed response bytes.
func EncodeResponse(response Response) ([]byte, error) {
	if response.OK {
		if response.Failure != (Failure{}) || !validResult(response.Result) {
			return nil, errors.New("project protocol: invalid success response")
		}
		return []byte(fmt.Sprintf(
			`{"protocol_version":1,"status":"ok","result":{"source_count":%d,"definition_count":%d,"definition_set_digest":%q}}`,
			response.Result.SourceCount,
			response.Result.DefinitionCount,
			response.Result.DefinitionSetDigest,
		)), nil
	}
	if response.Result != (Result{}) || !IsLinkedFailure(response.Failure) {
		return nil, errors.New("project protocol: invalid error response")
	}
	return []byte(fmt.Sprintf(
		`{"protocol_version":1,"status":"error","error":{"category":%q,"code":%q}}`,
		response.Failure.Category,
		response.Failure.Code,
	)), nil
}

// WriteResponse encodes and performs one complete write attempt.
func WriteResponse(writer io.Writer, response Response) error {
	if writer == nil {
		return errors.New("project protocol: nil response writer")
	}
	document, err := EncodeResponse(response)
	if err != nil {
		return err
	}
	written, err := writer.Write(document)
	if err != nil {
		return fmt.Errorf("project protocol: write response: %w", err)
	}
	if written != len(document) {
		return fmt.Errorf("project protocol: write response: %w", io.ErrShortWrite)
	}
	return nil
}

// ExitCode returns the exact public exit for a closed taxonomy pair.
func ExitCode(failure Failure) (int, bool) {
	switch failure.Category {
	case CategoryCommand:
		return failurecode.Exact(failure.Code, 2, CodeInvalidArguments)
	case CategorySelection:
		return failurecode.Selection(failure.Code, 3)
	case CategoryBuild:
		return failurecode.Exact(failure.Code, 3, CodeProjectTemporaryStorageFailed, CodeProjectBuildFailed)
	case CategoryProtocol:
		return failurecode.Exact(failure.Code, 3, CodeInvalidProjectRunnerRequest, CodeProjectRunnerFailed, CodeProjectProtocolIncompatible, CodeInvalidProjectRunnerResponse)
	case CategoryProcess:
		return failurecode.Process(failure.Code)
	case CategoryDiscovery:
		return failurecode.Discovery(failure.Code)
	case CategorySource:
		return failurecode.Source(failure.Code)
	case CategoryGraph:
		return failurecode.Graph(failure.Code)
	case CategoryInternal:
		return failurecode.Exact(failure.Code, 3, CodeProjectInternalError)
	}
	return 0, false
}

// IsLinkedFailure reports whether a pair may be emitted by the linked runner.
func IsLinkedFailure(failure Failure) bool {
	switch failure.Category {
	case CategoryProtocol:
		return failure.Code == CodeInvalidProjectRunnerRequest || failure.Code == CodeProjectProtocolIncompatible
	case CategoryDiscovery, CategorySource, CategoryGraph:
		_, ok := ExitCode(failure)
		return ok
	default:
		return false
	}
}

func parseRequest(document []byte) (Failure, bool) {
	object, err := decodeObject(document, MaxRequestBytes)
	if err != nil {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidProjectRunnerRequest}, true
	}
	versionValue, exists := object["protocol_version"]
	if !exists {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidProjectRunnerRequest}, true
	}
	version, valid := wirejson.Uint(versionValue, 65_535)
	if !valid {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidProjectRunnerRequest}, true
	}
	if version != Version {
		return Failure{Category: CategoryProtocol, Code: CodeProjectProtocolIncompatible}, true
	}
	if !wirejson.HasKeys(object, "protocol_version", "command") || object["command"] != "migrations.check" {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidProjectRunnerRequest}, true
	}
	return Failure{}, false
}

func validResult(result Result) bool {
	if result.SourceCount < 0 || result.SourceCount > MaxCount || result.DefinitionCount < 0 || result.DefinitionCount > MaxCount {
		return false
	}
	if result.SourceCount != result.DefinitionCount || !validDigest(result.DefinitionSetDigest) {
		return false
	}
	if result.SourceCount == 0 {
		return result.DefinitionSetDigest == EmptySetDigest
	}
	return result.DefinitionSetDigest != EmptySetDigest
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func invalidResponse() (Response, Failure, bool) {
	return Response{}, Failure{Category: CategoryProtocol, Code: CodeInvalidProjectRunnerResponse}, true
}

func decodeObject(document []byte, maximum int) (map[string]any, error) {
	return wirejson.DecodeObject(document, wirejson.Limits{Bytes: maximum, ValueDepth: 32})
}
