// Package protocol defines the private, closed project-runner protocol used by
// the global GoDj command and a project-linked runner.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	Version          uint64 = 1
	PrivateArgument         = "__godj_project_runner_v1"
	MaxRequestBytes         = 64 << 10
	MaxResponseBytes        = 64 << 10
	MaxCount                = 2_048

	EmptySetDigest = "sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450"
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

var canonicalUnsigned = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

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
	document, err := readAtMost(reader, MaxRequestBytes)
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
	version, valid := canonicalUint(versionValue, 65_535)
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
		if !hasExactKeys(object, "protocol_version", "status", "result") {
			return invalidResponse()
		}
		resultObject, ok := object["result"].(map[string]wireValue)
		if !ok || !hasExactKeys(resultObject, "source_count", "definition_count", "definition_set_digest") {
			return invalidResponse()
		}
		sourceCount, sourceOK := canonicalUint(resultObject["source_count"], MaxCount)
		definitionCount, definitionOK := canonicalUint(resultObject["definition_count"], MaxCount)
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
		if !hasExactKeys(object, "protocol_version", "status", "error") {
			return invalidResponse()
		}
		errorObject, ok := object["error"].(map[string]wireValue)
		if !ok || !hasExactKeys(errorObject, "category", "code") {
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
		return exactCode(failure.Code, 2, CodeInvalidArguments)
	case CategorySelection:
		switch failure.Code {
		case CodeProjectNotFound, CodeProjectSearchLimitExceeded, CodeInvalidProjectDescriptor, CodeProjectDescriptorIncompatible:
			return 2, true
		case CodeProjectSelectionFailed:
			return 3, true
		}
	case CategoryBuild:
		return exactCode(failure.Code, 3, CodeProjectTemporaryStorageFailed, CodeProjectBuildFailed)
	case CategoryProtocol:
		return exactCode(failure.Code, 3, CodeInvalidProjectRunnerRequest, CodeProjectRunnerFailed, CodeProjectProtocolIncompatible, CodeInvalidProjectRunnerResponse)
	case CategoryProcess:
		switch failure.Code {
		case CodeProjectCanceled, CodeProjectCleanupFailed:
			return 3, true
		case CodeProjectInterrupted:
			return 130, true
		}
	case CategoryDiscovery:
		switch failure.Code {
		case CodeInvalidProjectSourceConfig, CodeInvalidSourceRoot:
			return 2, true
		case CodeInvalidSourceEntry, CodeUnsafeSourceEntry, CodeSourceCatalogLimitExceeded:
			return 1, true
		case CodeSourceDiscoveryFailed, CodeSourceReadFailed:
			return 3, true
		}
	case CategorySource:
		return exactCode(failure.Code, 1,
			"invalid_definition_source",
			"invalid_definition_document",
			"definition_format_incompatible",
			"loader_abi_incompatible",
			"operation_codec_incompatible",
			"schema_ir_incompatible",
			"unsupported_definition_operation",
			"invalid_definition_operation",
			"invalid_definition_ir",
		)
	case CategoryGraph:
		return exactCode(failure.Code, 1,
			"invalid_node",
			"duplicate_node",
			"invalid_dependency",
			"duplicate_dependency",
			"dependency_not_found",
			"dependency_cycle",
		)
	case CategoryInternal:
		return exactCode(failure.Code, 3, CodeProjectInternalError)
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
	version, valid := canonicalUint(versionValue, 65_535)
	if !valid {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidProjectRunnerRequest}, true
	}
	if version != Version {
		return Failure{Category: CategoryProtocol, Code: CodeProjectProtocolIncompatible}, true
	}
	if !hasExactKeys(object, "protocol_version", "command") || object["command"] != "migrations.check" {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidProjectRunnerRequest}, true
	}
	return Failure{}, false
}

func readAtMost(reader io.Reader, maximum int) ([]byte, error) {
	retained := make([]byte, 0, maximum+1)
	buffer := make([]byte, 32<<10)
	emptyReads := 0
	for {
		read, err := reader.Read(buffer)
		if read < 0 || read > len(buffer) {
			return nil, errors.New("invalid request reader count")
		}
		if read != 0 {
			emptyReads = 0
			remaining := maximum + 1 - len(retained)
			if remaining > 0 {
				if read < remaining {
					remaining = read
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

func canonicalUint(value wireValue, maximum uint64) (uint64, bool) {
	number, ok := value.(json.Number)
	if !ok || !canonicalUnsigned.MatchString(number.String()) {
		return 0, false
	}
	parsed, err := strconv.ParseUint(number.String(), 10, 64)
	return parsed, err == nil && parsed <= maximum
}

func exactCode(code string, exit int, allowed ...string) (int, bool) {
	for _, candidate := range allowed {
		if code == candidate {
			return exit, true
		}
	}
	return 0, false
}

func invalidResponse() (Response, Failure, bool) {
	return Response{}, Failure{Category: CategoryProtocol, Code: CodeInvalidProjectRunnerResponse}, true
}

type wireValue any

func decodeObject(document []byte, maximum int) (map[string]wireValue, error) {
	if len(document) > maximum || !utf8.Valid(document) {
		return nil, errors.New("invalid wire framing")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	value, err := decodeValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("trailing wire value")
		}
		return nil, err
	}
	object, ok := value.(map[string]wireValue)
	if !ok {
		return nil, errors.New("wire root is not an object")
	}
	return object, nil
}

func decodeValue(decoder *json.Decoder, depth int) (wireValue, error) {
	if depth > 32 {
		return nil, errors.New("wire nesting limit exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]wireValue)
		for decoder.More() {
			member, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := member.(string)
			if !ok {
				return nil, errors.New("wire object key is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, errors.New("duplicate wire object key")
			}
			child, err := decodeValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = child
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return nil, errors.New("invalid wire object close")
		}
		return object, nil
	case '[':
		array := make([]wireValue, 0)
		for decoder.More() {
			child, err := decodeValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, child)
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return nil, errors.New("invalid wire array close")
		}
		return array, nil
	default:
		return nil, errors.New("invalid wire delimiter")
	}
}

func hasExactKeys(object map[string]wireValue, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			return false
		}
	}
	return true
}
