// Package migrateprotocol defines the private, closed migration-command wire
// shared by the global GoDj command and a project-linked runner.
package migrateprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	Version          uint64 = 1
	PrivateArgument         = "__godj_project_migrate_runner_v1"
	MaxRequestBytes         = 64 << 10
	MaxResponseBytes        = 64 << 10
	MaxCount                = 2_048
	maxJSONDepth            = 16

	EmptySetDigest = "sha256:1412c48d7da2299b6f2be7a614c5bb9ce510027328f6baed72ae05cbecc9b494"
)

const (
	CategoryCommand     = "migration_project_command_error"
	CategorySelection   = "migration_project_selection_error"
	CategoryBuild       = "migration_project_build_error"
	CategoryProtocol    = "migration_project_protocol_error"
	CategoryProcess     = "migration_project_process_error"
	CategoryDiscovery   = "migration_definition_discovery_error"
	CategorySource      = "migration_definition_source_error"
	CategoryGraph       = "migration_graph_error"
	CategoryState       = "migration_state_error"
	CategoryCapability  = "migration_capability_error"
	CategoryExecution   = "migration_execution_error"
	CategoryRecorder    = "migration_recorder_error"
	CategoryTransaction = "migration_transaction_error"
	CategoryPlan        = "migration_plan_error"
	CategoryHistory     = "migration_history_error"
	CategoryConflict    = "migration_conflict_error"
	CategoryBackend     = "migration_backend_error"
	CategoryInternal    = "migration_project_internal_error"
)

const (
	CodeInvalidArguments              = "invalid_arguments"
	CodeProjectNotFound               = "project_not_found"
	CodeProjectSearchLimitExceeded    = "project_search_limit_exceeded"
	CodeInvalidProjectDescriptor      = "invalid_project_descriptor"
	CodeProjectDescriptorIncompatible = "project_descriptor_incompatible"
	CodeProjectSelectionFailed        = "project_selection_failed"
	CodeProjectTemporaryStorageFailed = "project_temporary_storage_failed"
	CodeProjectBuildFailed            = "project_build_failed"
	CodeInvalidRequest                = "invalid_project_migrate_runner_request"
	CodeRunnerFailed                  = "project_migrate_runner_failed"
	CodeProtocolIncompatible          = "project_migrate_protocol_incompatible"
	CodeInvalidResponse               = "invalid_project_migrate_runner_response"
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
	CodeBackendOpenFailed             = "backend_open_failed"
	CodeInvalidBackend                = "invalid_backend"
	CodeBackendCloseFailed            = "backend_close_failed"
	CodeRollbackFailed                = "rollback_failed"
	CodeProjectInternalError          = "project_internal_error"
)

// Failure is the closed, detail-free failure carried over the private wire.
// CleanupFailed records a secondary outer backend close failure without
// publishing its cause or replacing the primary category/code.
type Failure struct {
	Category      string
	Code          string
	CleanupFailed bool
}

// Result is the bounded summary of one successful latest migration command.
// It deliberately does not guess how many migrations were applied.
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

// RequestDocument returns a fresh copy of the sole supported request.
func RequestDocument() []byte {
	return []byte(`{"protocol_version":1,"command":"migrations.migrate"}`)
}

// ReadRequest reads one bounded request through EOF. Completed malformed input
// is a logical protocol failure; a Reader failure remains a Go transport error.
func ReadRequest(reader io.Reader) (Failure, bool, error) {
	if reader == nil {
		return Failure{}, false, errors.New("project migration protocol: nil request reader")
	}
	document, err := readAtMost(reader, MaxRequestBytes)
	if err != nil {
		return Failure{}, false, fmt.Errorf("project migration protocol: read request: %w", err)
	}
	if failure, failed := parseRequest(document); failed {
		return failure, true, nil
	}
	return Failure{}, false, nil
}

// ParseResponse validates completed response bytes. A failed transport has
// precedence over all response bytes; a valid linked failure remains in the
// returned Response rather than being reclassified by the global owner.
func ParseResponse(document []byte, transportOK bool) (Response, Failure, bool) {
	if !transportOK {
		return Response{}, Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}, true
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
		return Response{}, Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true
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
		if !ok || !hasExactKeys(errorObject, "category", "code", "cleanup_failed") {
			return invalidResponse()
		}
		category, categoryOK := errorObject["category"].(string)
		code, codeOK := errorObject["code"].(string)
		cleanupFailed, cleanupOK := errorObject["cleanup_failed"].(bool)
		failure := Failure{Category: category, Code: code, CleanupFailed: cleanupFailed}
		if !categoryOK || !codeOK || !cleanupOK || !IsLinkedFailure(failure) {
			return invalidResponse()
		}
		return Response{Failure: failure}, Failure{}, false
	default:
		return invalidResponse()
	}
}

// EncodeResponse returns canonical, bounded response bytes.
func EncodeResponse(response Response) ([]byte, error) {
	var document []byte
	if response.OK {
		if response.Failure != (Failure{}) || !validResult(response.Result) {
			return nil, errors.New("project migration protocol: invalid success response")
		}
		document = []byte(fmt.Sprintf(
			`{"protocol_version":1,"status":"ok","result":{"source_count":%d,"definition_count":%d,"definition_set_digest":%q}}`,
			response.Result.SourceCount,
			response.Result.DefinitionCount,
			response.Result.DefinitionSetDigest,
		))
	} else {
		if response.Result != (Result{}) || !IsLinkedFailure(response.Failure) {
			return nil, errors.New("project migration protocol: invalid error response")
		}
		document = []byte(fmt.Sprintf(
			`{"protocol_version":1,"status":"error","error":{"category":%q,"code":%q,"cleanup_failed":%s}}`,
			response.Failure.Category,
			response.Failure.Code,
			strconv.FormatBool(response.Failure.CleanupFailed),
		))
	}
	if len(document) > MaxResponseBytes {
		return nil, errors.New("project migration protocol: response exceeds resource limit")
	}
	return document, nil
}

// WriteResponse encodes and performs one complete write attempt.
func WriteResponse(writer io.Writer, response Response) error {
	if writer == nil {
		return errors.New("project migration protocol: nil response writer")
	}
	document, err := EncodeResponse(response)
	if err != nil {
		return err
	}
	written, err := writer.Write(document)
	if err != nil {
		return fmt.Errorf("project migration protocol: write response: %w", err)
	}
	if written != len(document) {
		return fmt.Errorf("project migration protocol: write response: %w", io.ErrShortWrite)
	}
	return nil
}

// ExitCode returns the exact public exit for a closed taxonomy value.
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
		return exactCode(failure.Code, 3, CodeInvalidRequest, CodeRunnerFailed, CodeProtocolIncompatible, CodeInvalidResponse)
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
			"unsupported_definition_operation",
			"invalid_definition_operation",
			"invalid_definition_ir",
		)
	case CategoryGraph:
		return exactCode(failure.Code, 1,
			"invalid_node", "duplicate_node", "invalid_dependency",
			"duplicate_dependency", "dependency_not_found", "dependency_cycle",
		)
	case CategoryState:
		return exactCode(failure.Code, 1, "invalid_state")
	case CategoryCapability:
		return exactCode(failure.Code, 1,
			"unsupported_operation", "revision_fence_unsupported", "revision_fence_adoption_required",
		)
	case CategoryExecution:
		return exactCode(failure.Code, 3, "operation_failed")
	case CategoryRecorder:
		return exactCode(failure.Code, 3, "read_failed", "record_failed")
	case CategoryTransaction:
		return exactCode(failure.Code, 3,
			"begin_failed", "commit_failed", "history_revision_contended",
			"commit_outcome_unknown", "commit_cleanup_failed", "session_close_failed", CodeRollbackFailed,
		)
	case CategoryPlan:
		return exactCode(failure.Code, 1,
			"invalid_target", "target_not_found", "invalid_execution_plan", "mixed_directions",
		)
	case CategoryHistory:
		return exactCode(failure.Code, 1,
			"invalid_applied_state", "duplicate_applied", "inconsistent_applied_history", "history_revision_integrity",
		)
	case CategoryConflict:
		return exactCode(failure.Code, 3, "stale_history_revision")
	case CategoryBackend:
		return exactCode(failure.Code, 3, CodeBackendOpenFailed, CodeInvalidBackend, CodeBackendCloseFailed)
	case CategoryInternal:
		return exactCode(failure.Code, 3, CodeProjectInternalError)
	}
	return 0, false
}

// IsLinkedFailure reports whether a value may be emitted by the project runner.
func IsLinkedFailure(failure Failure) bool {
	if _, ok := ExitCode(failure); !ok {
		return false
	}
	switch failure.Category {
	case CategoryCommand, CategorySelection, CategoryBuild, CategoryProcess:
		return false
	case CategoryProtocol:
		return !failure.CleanupFailed && (failure.Code == CodeInvalidRequest || failure.Code == CodeProtocolIncompatible)
	case CategoryDiscovery, CategorySource, CategoryGraph:
		return !failure.CleanupFailed
	case CategoryBackend:
		switch failure.Code {
		case CodeBackendCloseFailed:
			return failure.CleanupFailed
		case CodeInvalidBackend:
			return !failure.CleanupFailed
		case CodeBackendOpenFailed:
			return true
		}
		return false
	default:
		return true
	}
}

func parseRequest(document []byte) (Failure, bool) {
	object, err := decodeObject(document, MaxRequestBytes)
	if err != nil {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true
	}
	versionValue, exists := object["protocol_version"]
	if !exists {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true
	}
	version, valid := canonicalUint(versionValue, 65_535)
	if !valid {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true
	}
	if version != Version {
		return Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true
	}
	if !hasExactKeys(object, "protocol_version", "command") || object["command"] != "migrations.migrate" {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true
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
	if (result.SourceCount == 0) != (result.DefinitionCount == 0) || !validDigest(result.DefinitionSetDigest) {
		return false
	}
	if result.DefinitionCount == 0 {
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
	if !ok {
		return 0, false
	}
	text := number.String()
	if text == "" || (len(text) > 1 && text[0] == '0') {
		return 0, false
	}
	for _, character := range text {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
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
	return Response{}, Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}, true
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
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
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
	if depth > maxJSONDepth {
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
