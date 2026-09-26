// Package showmigrationsprotocol defines the private, closed read-only
// migration-status wire shared by the global GoDj command and a project-linked
// runner.
//
// Rows are grouped by strictly ascending app label. Unknown recorder rows form
// a name-sorted tail within their app. Known rows deliberately retain the
// dependency order produced by the linked core: this graph-free wire cannot
// independently reconstruct that order and must not reinterpret it as name
// order.
package showmigrationsprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/progresshans/godj/internal/projectcheck/failurecode"
	"io"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/wirejson"
)

const (
	Version          uint64 = 1
	PrivateArgument         = "__godj_project_showmigrations_runner_v1"
	MaxRequestBytes         = 64 << 10
	MaxResponseBytes        = 16 << 20
	MaxRows                 = 4_096
	maxJSONDepth            = 16
)

const (
	StatusApplied   = "applied"
	StatusUnapplied = "unapplied"
	StatusUnknown   = "unknown"
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
	CategoryHistory     = "migration_history_error"
	CategoryRecorder    = "migration_recorder_error"
	CategoryCapability  = "migration_capability_error"
	CategoryTransaction = "migration_transaction_error"
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
	CodeInvalidRequest                = "invalid_project_showmigrations_runner_request"
	CodeRunnerFailed                  = "project_showmigrations_runner_failed"
	CodeProtocolIncompatible          = "project_showmigrations_protocol_incompatible"
	CodeInvalidResponse               = "invalid_project_showmigrations_runner_response"
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
	CodeProjectInternalError          = "project_internal_error"
)

// Failure is the closed, detail-free failure carried over the private wire.
// CleanupFailed records a secondary backend close failure without publishing
// its cause or replacing the primary category/code.
type Failure struct {
	Category      string
	Code          string
	CleanupFailed bool
}

// Row is one known definition or unknown durable recorder identity.
type Row struct {
	App    string `json:"app"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Result is the complete bounded status listing. Rows returned by
// ParseResponse are newly allocated and are owned by the caller.
type Result struct {
	Rows []Row
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
	return []byte(`{"protocol_version":1,"command":"migrations.show"}`)
}

// ReadRequest reads one bounded request through EOF. Completed malformed input
// is a logical protocol failure; a Reader failure remains a Go transport error.
func ReadRequest(reader io.Reader) (Failure, bool, error) {
	if reader == nil {
		return Failure{}, false, errors.New("project showmigrations protocol: nil request reader")
	}
	document, err := wirejson.Read(reader, MaxRequestBytes, wirejson.DrainToEOF)
	if err != nil {
		return Failure{}, false, fmt.Errorf("project showmigrations protocol: read request: %w", err)
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
	version, valid := wirejson.Uint(versionValue, 65_535)
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
		if !wirejson.HasKeys(object, "protocol_version", "status", "result") {
			return invalidResponse()
		}
		resultObject, ok := object["result"].(map[string]any)
		if !ok || !wirejson.HasKeys(resultObject, "rows") {
			return invalidResponse()
		}
		wireRows, ok := resultObject["rows"].([]any)
		if !ok || len(wireRows) > MaxRows {
			return invalidResponse()
		}
		rows := make([]Row, len(wireRows))
		for index, value := range wireRows {
			rowObject, ok := value.(map[string]any)
			if !ok || !wirejson.HasKeys(rowObject, "app", "name", "status") {
				return invalidResponse()
			}
			app, appOK := rowObject["app"].(string)
			name, nameOK := rowObject["name"].(string)
			rowStatus, statusOK := rowObject["status"].(string)
			if !appOK || !nameOK || !statusOK {
				return invalidResponse()
			}
			rows[index] = Row{App: app, Name: name, Status: rowStatus}
		}
		if !validRows(rows) {
			return invalidResponse()
		}
		return Response{OK: true, Result: Result{Rows: rows}}, Failure{}, false
	case "error":
		if !wirejson.HasKeys(object, "protocol_version", "status", "error") {
			return invalidResponse()
		}
		errorObject, ok := object["error"].(map[string]any)
		if !ok || !wirejson.HasKeys(errorObject, "category", "code", "cleanup_failed") {
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
	var err error
	if response.OK {
		if response.Failure != (Failure{}) || !validRows(response.Result.Rows) {
			return nil, errors.New("project showmigrations protocol: invalid success response")
		}
		rows := response.Result.Rows
		if rows == nil {
			rows = []Row{}
		}
		document, err = json.Marshal(struct {
			ProtocolVersion uint64 `json:"protocol_version"`
			Status          string `json:"status"`
			Result          struct {
				Rows []Row `json:"rows"`
			} `json:"result"`
		}{
			ProtocolVersion: Version,
			Status:          "ok",
			Result: struct {
				Rows []Row `json:"rows"`
			}{Rows: rows},
		})
	} else {
		if response.Result.Rows != nil || !IsLinkedFailure(response.Failure) {
			return nil, errors.New("project showmigrations protocol: invalid error response")
		}
		document, err = json.Marshal(struct {
			ProtocolVersion uint64 `json:"protocol_version"`
			Status          string `json:"status"`
			Error           struct {
				Category      string `json:"category"`
				Code          string `json:"code"`
				CleanupFailed bool   `json:"cleanup_failed"`
			} `json:"error"`
		}{
			ProtocolVersion: Version,
			Status:          "error",
			Error: struct {
				Category      string `json:"category"`
				Code          string `json:"code"`
				CleanupFailed bool   `json:"cleanup_failed"`
			}{
				Category:      response.Failure.Category,
				Code:          response.Failure.Code,
				CleanupFailed: response.Failure.CleanupFailed,
			},
		})
	}
	if err != nil {
		return nil, fmt.Errorf("project showmigrations protocol: encode response: %w", err)
	}
	if len(document) > MaxResponseBytes {
		return nil, errors.New("project showmigrations protocol: response exceeds resource limit")
	}
	return document, nil
}

// WriteResponse encodes and performs one complete write attempt.
func WriteResponse(writer io.Writer, response Response) error {
	if writer == nil {
		return errors.New("project showmigrations protocol: nil response writer")
	}
	document, err := EncodeResponse(response)
	if err != nil {
		return err
	}
	written, err := writer.Write(document)
	if err != nil {
		return fmt.Errorf("project showmigrations protocol: write response: %w", err)
	}
	if written != len(document) {
		return fmt.Errorf("project showmigrations protocol: write response: %w", io.ErrShortWrite)
	}
	return nil
}

// ExitCode returns the exact public exit for a closed taxonomy value.
func ExitCode(failure Failure) (int, bool) {
	switch failure.Category {
	case CategoryCommand:
		return failurecode.Exact(failure.Code, 2, CodeInvalidArguments)
	case CategorySelection:
		return failurecode.Selection(failure.Code, 3)
	case CategoryBuild:
		return failurecode.Exact(failure.Code, 3, CodeProjectTemporaryStorageFailed, CodeProjectBuildFailed)
	case CategoryProtocol:
		return failurecode.Exact(failure.Code, 3, CodeInvalidRequest, CodeRunnerFailed, CodeProtocolIncompatible, CodeInvalidResponse)
	case CategoryProcess:
		return failurecode.Process(failure.Code)
	case CategoryDiscovery:
		return failurecode.Discovery(failure.Code)
	case CategorySource:
		return failurecode.Source(failure.Code)
	case CategoryGraph:
		return failurecode.Graph(failure.Code)
	case CategoryHistory:
		return failurecode.Exact(failure.Code, 1,
			"invalid_applied_state",
			"duplicate_applied",
			"inconsistent_applied_history",
			"history_revision_integrity",
		)
	case CategoryRecorder:
		return failurecode.Exact(failure.Code, 3, "read_failed")
	case CategoryCapability:
		return failurecode.Exact(failure.Code, 1, "revision_fence_adoption_required")
	case CategoryTransaction:
		return failurecode.Exact(failure.Code, 3, "history_revision_contended")
	case CategoryConflict:
		return failurecode.Exact(failure.Code, 3, "stale_history_revision")
	case CategoryBackend:
		return failurecode.Exact(failure.Code, 3, CodeBackendOpenFailed, CodeInvalidBackend, CodeBackendCloseFailed)
	case CategoryInternal:
		return failurecode.Exact(failure.Code, 3, CodeProjectInternalError)
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
			// A backend acquired successfully can still return no revision session.
			// Its subsequent outer Close failure is retained as secondary cleanup
			// without replacing the invalid-backend primary.
			return true
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
	version, valid := wirejson.Uint(versionValue, 65_535)
	if !valid {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true
	}
	if version != Version {
		return Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true
	}
	if !wirejson.HasKeys(object, "protocol_version", "command") || object["command"] != "migrations.show" {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true
	}
	return Failure{}, false
}

func validRows(rows []Row) bool {
	if len(rows) > MaxRows {
		return false
	}
	seen := make(map[string]map[string]struct{})
	var previousApp string
	var previousUnknownName string
	haveApp := false
	unknownTail := false
	for _, row := range rows {
		if row.App == "" || row.Name == "" || !utf8.ValidString(row.App) || !utf8.ValidString(row.Name) || !validStatus(row.Status) {
			return false
		}
		if !haveApp || row.App != previousApp {
			if haveApp && bytes.Compare([]byte(previousApp), []byte(row.App)) >= 0 {
				return false
			}
			previousApp = row.App
			previousUnknownName = ""
			unknownTail = false
			haveApp = true
		}
		names := seen[row.App]
		if names == nil {
			names = make(map[string]struct{})
			seen[row.App] = names
		}
		if _, duplicate := names[row.Name]; duplicate {
			return false
		}
		names[row.Name] = struct{}{}

		if row.Status == StatusUnknown {
			if unknownTail && bytes.Compare([]byte(previousUnknownName), []byte(row.Name)) >= 0 {
				return false
			}
			unknownTail = true
			previousUnknownName = row.Name
		} else if unknownTail {
			return false
		}
	}
	return true
}

func validStatus(status string) bool {
	switch status {
	case StatusApplied, StatusUnapplied, StatusUnknown:
		return true
	default:
		return false
	}
}

func invalidResponse() (Response, Failure, bool) {
	return Response{}, Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}, true
}

func decodeObject(document []byte, maximum int) (map[string]any, error) {
	return wirejson.DecodeObject(document, wirejson.Limits{Bytes: maximum, ValueDepth: maxJSONDepth, ObjectKeys: MaxRows, ArrayValues: MaxRows})
}
