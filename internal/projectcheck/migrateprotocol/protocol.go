// Package migrateprotocol defines the private, closed migration-command wire
// shared by the global GoDj command and a project-linked runner.
package migrateprotocol

import (
	"errors"
	"fmt"
	"github.com/progresshans/godj/internal/projectcheck/failurecode"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/wirejson"
)

const (
	Version         uint64 = 2
	PrivateArgument        = "__godj_project_migrate_runner_v2"

	MaxRequestBytes           = 16 << 20
	MaxResponseBytes          = 101 << 20
	MaxCount                  = 2_048
	MaxPlanRows               = 2_048
	MaxIdentityBytes          = 1 << 20
	MaxIdentityAggregateBytes = 16 << 20

	maxJSONDepth       = 16
	maxWireValues      = MaxPlanRows*4 + 64
	maxWireArrayValues = MaxPlanRows

	// A valid identity byte can require at most six JSON bytes (for example,
	// U+0000 becomes \u0000). The private plan framing is at most 44 bytes per
	// row plus 70 bytes outside non-empty row arrays. The public framing is
	// smaller. Keep the derivation executable in tests instead of relying on a
	// sample payload that cannot exercise the worst-case escaping expansion.
	maxEscapedPlanIdentityBytes = 6 * MaxIdentityAggregateBytes
	maxPrivatePlanFramingBytes  = 70 + 44*MaxPlanRows
	maxPrivatePlanDocumentBytes = maxEscapedPlanIdentityBytes + maxPrivatePlanFramingBytes
	maxPublicPlanFramingBytes   = 11 + 44*MaxPlanRows // includes the terminal LF
	maxPublicPlanDocumentBytes  = maxEscapedPlanIdentityBytes + maxPublicPlanFramingBytes

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

// Mode is the closed migrate operation selected by one request and success.
type Mode string

const (
	ModeExecute Mode = "execute"
	ModePlan    Mode = "plan"
)

// TargetKind is the closed target representation carried by protocol v2.
type TargetKind string

const (
	TargetLatest TargetKind = "latest"
	TargetNamed  TargetKind = "named"
	TargetZero   TargetKind = "zero"
)

// Target is an immutable-by-value wire target. Latest uses only Kind, named
// uses App and Name, and zero uses App. Its zero value is invalid.
type Target struct {
	Kind TargetKind
	App  string
	Name string
}

// Request is one strict operation/target pair. Its zero value is invalid.
type Request struct {
	Mode   Mode
	Target Target
}

// Failure is the closed, detail-free failure carried over the private wire.
// CleanupFailed records a secondary outer backend close failure without
// publishing its cause or replacing the primary category/code.
type Failure struct {
	Category      string
	Code          string
	CleanupFailed bool
}

// ExecuteResult is the existing bounded execution summary. It deliberately
// does not guess how many migrations were applied.
type ExecuteResult struct {
	SourceCount         int
	DefinitionCount     int
	DefinitionSetDigest string
}

// Direction is the closed plan-step direction carried by protocol v2.
type Direction string

const (
	DirectionForward  Direction = "forward"
	DirectionBackward Direction = "backward"
)

// PlanRow is one detached migration identity in linked-core plan order.
type PlanRow struct {
	App       string
	Name      string
	Direction Direction
}

// Result is a strict success union. Execute mode requires Execute and a nil
// Plan. Plan mode requires a non-nil Plan and a zero Execute value.
type Result struct {
	Mode    Mode
	Execute ExecuteResult
	Plan    []PlanRow
}

// Response is one closed private outcome. OK selects Result; otherwise
// Failure is selected. EncodeResponse rejects invalid or ambiguous values.
type Response struct {
	OK      bool
	Result  Result
	Failure Failure
}

// RequestDocument returns a fresh canonical v2 execute/latest default for
// existing execute/latest probes. Callers selecting a mode or target use
// EncodeRequest.
func RequestDocument() []byte {
	return []byte(`{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"latest"}}`)
}

// EncodeRequest returns the canonical, bounded v2 request document.
func EncodeRequest(request Request) ([]byte, error) {
	if !validRequest(request) {
		return nil, errors.New("project migration protocol: invalid request")
	}
	document := make([]byte, 0, 256)
	document = append(document, `{"protocol_version":2,"command":"migrations.migrate","mode":`...)
	document = appendJSONString(document, string(request.Mode))
	document = append(document, `,"target":{"kind":`...)
	document = appendJSONString(document, string(request.Target.Kind))
	switch request.Target.Kind {
	case TargetLatest:
	case TargetNamed:
		document = append(document, `,"app":`...)
		document = appendJSONString(document, request.Target.App)
		document = append(document, `,"name":`...)
		document = appendJSONString(document, request.Target.Name)
	case TargetZero:
		document = append(document, `,"app":`...)
		document = appendJSONString(document, request.Target.App)
	}
	document = append(document, '}', '}')
	if len(document) > MaxRequestBytes {
		return nil, errors.New("project migration protocol: request exceeds resource limit")
	}
	return document, nil
}

// ReadRequest reads one bounded request through EOF. Completed malformed input
// is a logical protocol failure; a Reader failure remains a Go transport error.
func ReadRequest(reader io.Reader) (Request, Failure, bool, error) {
	if reader == nil {
		return Request{}, Failure{}, false, errors.New("project migration protocol: nil request reader")
	}
	document, err := wirejson.Read(reader, MaxRequestBytes, wirejson.DrainToEOF)
	if err != nil {
		return Request{}, Failure{}, false, fmt.Errorf("project migration protocol: read request: %w", err)
	}
	request, failure, failed := parseRequest(document)
	if failed {
		return Request{}, failure, true, nil
	}
	return request, Failure{}, false, nil
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
		if !ok {
			return invalidResponse()
		}
		mode, ok := resultObject["mode"].(string)
		if !ok {
			return invalidResponse()
		}
		switch Mode(mode) {
		case ModeExecute:
			return parseExecuteResponse(resultObject)
		case ModePlan:
			return parsePlanResponse(resultObject)
		default:
			return invalidResponse()
		}
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

func parseExecuteResponse(resultObject map[string]any) (Response, Failure, bool) {
	if !wirejson.HasKeys(resultObject, "mode", "execute") {
		return invalidResponse()
	}
	executeObject, ok := resultObject["execute"].(map[string]any)
	if !ok || !wirejson.HasKeys(executeObject, "source_count", "definition_count", "definition_set_digest") {
		return invalidResponse()
	}
	sourceCount, sourceOK := wirejson.Uint(executeObject["source_count"], MaxCount)
	definitionCount, definitionOK := wirejson.Uint(executeObject["definition_count"], MaxCount)
	digest, digestOK := executeObject["definition_set_digest"].(string)
	execute := ExecuteResult{
		SourceCount:         int(sourceCount),
		DefinitionCount:     int(definitionCount),
		DefinitionSetDigest: digest,
	}
	if !sourceOK || !definitionOK || !digestOK || !validExecuteResult(execute) {
		return invalidResponse()
	}
	return Response{OK: true, Result: Result{Mode: ModeExecute, Execute: execute}}, Failure{}, false
}

func parsePlanResponse(resultObject map[string]any) (Response, Failure, bool) {
	if !wirejson.HasKeys(resultObject, "mode", "plan") {
		return invalidResponse()
	}
	wireRows, ok := resultObject["plan"].([]any)
	if !ok || len(wireRows) > MaxPlanRows {
		return invalidResponse()
	}
	rows := make([]PlanRow, len(wireRows))
	for index, value := range wireRows {
		rowObject, ok := value.(map[string]any)
		if !ok || !wirejson.HasKeys(rowObject, "app", "name", "direction") {
			return invalidResponse()
		}
		app, appOK := rowObject["app"].(string)
		name, nameOK := rowObject["name"].(string)
		direction, directionOK := rowObject["direction"].(string)
		if !appOK || !nameOK || !directionOK {
			return invalidResponse()
		}
		rows[index] = PlanRow{App: app, Name: name, Direction: Direction(direction)}
	}
	if !validPlanRows(rows) {
		return invalidResponse()
	}
	return Response{OK: true, Result: Result{Mode: ModePlan, Plan: rows}}, Failure{}, false
}

// EncodeResponse returns canonical, bounded response bytes.
func EncodeResponse(response Response) ([]byte, error) {
	var document []byte
	if response.OK {
		if response.Failure != (Failure{}) || !validResult(response.Result) {
			return nil, errors.New("project migration protocol: invalid success response")
		}
		switch response.Result.Mode {
		case ModeExecute:
			document = append(document, `{"protocol_version":2,"status":"ok","result":{"mode":"execute","execute":{"source_count":`...)
			document = strconv.AppendInt(document, int64(response.Result.Execute.SourceCount), 10)
			document = append(document, `,"definition_count":`...)
			document = strconv.AppendInt(document, int64(response.Result.Execute.DefinitionCount), 10)
			document = append(document, `,"definition_set_digest":`...)
			document = appendJSONString(document, response.Result.Execute.DefinitionSetDigest)
			document = append(document, '}', '}', '}')
		case ModePlan:
			document = make([]byte, 0, encodedPrivatePlanLength(response.Result.Plan))
			document = append(document, `{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":`...)
			document = appendPlanRows(document, response.Result.Plan)
			document = append(document, '}', '}')
		}
	} else {
		if !zeroResult(response.Result) || !IsLinkedFailure(response.Failure) {
			return nil, errors.New("project migration protocol: invalid error response")
		}
		document = append(document, `{"protocol_version":2,"status":"error","error":{"category":`...)
		document = appendJSONString(document, response.Failure.Category)
		document = append(document, `,"code":`...)
		document = appendJSONString(document, response.Failure.Code)
		document = append(document, `,"cleanup_failed":`...)
		document = strconv.AppendBool(document, response.Failure.CleanupFailed)
		document = append(document, '}', '}')
	}
	if len(document) > MaxResponseBytes {
		return nil, errors.New("project migration protocol: response exceeds resource limit")
	}
	return document, nil
}

// EncodePublicPlan returns one canonical public JSON line using the same row,
// identity, escaping, and byte bounds as the private plan arm.
func EncodePublicPlan(rows []PlanRow) ([]byte, error) {
	if !validPlanRows(rows) {
		return nil, errors.New("project migration protocol: invalid public plan")
	}
	document := make([]byte, 0, encodedPublicPlanLength(rows))
	document = append(document, `{"plan":`...)
	document = appendPlanRows(document, rows)
	document = append(document, '}', '\n')
	if len(document) > MaxResponseBytes {
		return nil, errors.New("project migration protocol: public plan exceeds resource limit")
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
	case CategoryState:
		return failurecode.Exact(failure.Code, 1, "invalid_state")
	case CategoryCapability:
		return failurecode.Exact(failure.Code, 1,
			"unsupported_operation", "revision_fence_unsupported", "revision_fence_adoption_required",
		)
	case CategoryExecution:
		return failurecode.Exact(failure.Code, 3, "operation_failed")
	case CategoryRecorder:
		return failurecode.Exact(failure.Code, 3, "read_failed", "record_failed")
	case CategoryTransaction:
		return failurecode.Exact(failure.Code, 3,
			"begin_failed", "commit_failed", "history_revision_contended",
			"commit_outcome_unknown", "commit_cleanup_failed", "session_close_failed", CodeRollbackFailed,
		)
	case CategoryPlan:
		return failurecode.Exact(failure.Code, 1,
			"invalid_target", "target_not_found", "invalid_execution_plan", "mixed_directions",
		)
	case CategoryHistory:
		return failurecode.Exact(failure.Code, 1,
			"invalid_applied_state", "duplicate_applied", "inconsistent_applied_history", "history_revision_integrity",
		)
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
			return !failure.CleanupFailed
		case CodeBackendOpenFailed:
			return true
		}
		return false
	default:
		return true
	}
}

func parseRequest(document []byte) (Request, Failure, bool) {
	object, err := decodeObject(document, MaxRequestBytes)
	if err != nil {
		return invalidRequest()
	}
	versionValue, exists := object["protocol_version"]
	if !exists {
		return invalidRequest()
	}
	version, valid := wirejson.Uint(versionValue, 65_535)
	if !valid {
		return invalidRequest()
	}
	if version != Version {
		return Request{}, Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true
	}
	if !wirejson.HasKeys(object, "protocol_version", "command", "mode", "target") || object["command"] != "migrations.migrate" {
		return invalidRequest()
	}
	mode, ok := object["mode"].(string)
	if !ok {
		return invalidRequest()
	}
	targetObject, ok := object["target"].(map[string]any)
	if !ok {
		return invalidRequest()
	}
	kind, ok := targetObject["kind"].(string)
	if !ok {
		return invalidRequest()
	}
	target := Target{Kind: TargetKind(kind)}
	switch target.Kind {
	case TargetLatest:
		if !wirejson.HasKeys(targetObject, "kind") {
			return invalidRequest()
		}
	case TargetNamed:
		if !wirejson.HasKeys(targetObject, "kind", "app", "name") {
			return invalidRequest()
		}
		app, appOK := targetObject["app"].(string)
		name, nameOK := targetObject["name"].(string)
		if !appOK || !nameOK {
			return invalidRequest()
		}
		target.App = app
		target.Name = name
	case TargetZero:
		if !wirejson.HasKeys(targetObject, "kind", "app") {
			return invalidRequest()
		}
		app, appOK := targetObject["app"].(string)
		if !appOK {
			return invalidRequest()
		}
		target.App = app
	default:
		return invalidRequest()
	}
	request := Request{Mode: Mode(mode), Target: target}
	if !validRequest(request) {
		return invalidRequest()
	}
	return request, Failure{}, false
}

func validRequest(request Request) bool {
	if request.Mode != ModeExecute && request.Mode != ModePlan {
		return false
	}
	switch request.Target.Kind {
	case TargetLatest:
		return request.Target.App == "" && request.Target.Name == ""
	case TargetNamed:
		return request.Target.Name != "zero" && validIdentity(request.Target.App) && validIdentity(request.Target.Name) &&
			len(request.Target.App)+len(request.Target.Name) <= MaxIdentityAggregateBytes
	case TargetZero:
		return validIdentity(request.Target.App) && request.Target.Name == ""
	default:
		return false
	}
}

func validResult(result Result) bool {
	switch result.Mode {
	case ModeExecute:
		return result.Plan == nil && validExecuteResult(result.Execute)
	case ModePlan:
		return result.Execute == (ExecuteResult{}) && result.Plan != nil && validPlanRows(result.Plan)
	default:
		return false
	}
}

func zeroResult(result Result) bool {
	return result.Mode == "" && result.Execute == (ExecuteResult{}) && result.Plan == nil
}

func validExecuteResult(result ExecuteResult) bool {
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

func validPlanRows(rows []PlanRow) bool {
	if len(rows) > MaxPlanRows {
		return false
	}
	type identity struct{ app, name string }
	seen := make(map[identity]struct{}, len(rows))
	total := 0
	var direction Direction
	for _, row := range rows {
		if !validIdentity(row.App) || !validIdentity(row.Name) {
			return false
		}
		if len(row.App) > MaxIdentityAggregateBytes-total {
			return false
		}
		total += len(row.App)
		if len(row.Name) > MaxIdentityAggregateBytes-total {
			return false
		}
		total += len(row.Name)
		if row.Direction != DirectionForward && row.Direction != DirectionBackward {
			return false
		}
		if direction == "" {
			direction = row.Direction
		} else if row.Direction != direction {
			return false
		}
		key := identity{app: row.App, name: row.Name}
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validIdentity(value string) bool {
	return value != "" && len(value) <= MaxIdentityBytes && utf8.ValidString(value)
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

func appendPlanRows(document []byte, rows []PlanRow) []byte {
	document = append(document, '[')
	for index, row := range rows {
		if index != 0 {
			document = append(document, ',')
		}
		document = append(document, `{"app":`...)
		document = appendJSONString(document, row.App)
		document = append(document, `,"name":`...)
		document = appendJSONString(document, row.Name)
		document = append(document, `,"direction":`...)
		document = appendJSONString(document, string(row.Direction))
		document = append(document, '}')
	}
	return append(document, ']')
}

func encodedPrivatePlanLength(rows []PlanRow) int {
	return len(`{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":[]}}`) - 2 + encodedPlanRowsLength(rows)
}

func encodedPublicPlanLength(rows []PlanRow) int {
	return len(`{"plan":[]}`) - 2 + encodedPlanRowsLength(rows) + 1
}

func encodedPlanRowsLength(rows []PlanRow) int {
	length := 2
	for index, row := range rows {
		if index != 0 {
			length++
		}
		length += len(`{"app":,"name":,"direction":}`)
		length += encodedJSONStringLength(row.App)
		length += encodedJSONStringLength(row.Name)
		length += encodedJSONStringLength(string(row.Direction))
	}
	return length
}

func appendJSONString(document []byte, value string) []byte {
	const hexadecimal = "0123456789abcdef"
	document = append(document, '"')
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch character {
		case '"', '\\':
			document = append(document, '\\', character)
		case '\b':
			document = append(document, '\\', 'b')
		case '\f':
			document = append(document, '\\', 'f')
		case '\n':
			document = append(document, '\\', 'n')
		case '\r':
			document = append(document, '\\', 'r')
		case '\t':
			document = append(document, '\\', 't')
		default:
			if character < 0x20 {
				document = append(document, '\\', 'u', '0', '0', hexadecimal[character>>4], hexadecimal[character&0x0f])
			} else {
				document = append(document, character)
			}
		}
	}
	return append(document, '"')
}

func encodedJSONStringLength(value string) int {
	length := 2
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			length += 2
		default:
			if value[index] < 0x20 {
				length += 6
			} else {
				length++
			}
		}
	}
	return length
}

func invalidRequest() (Request, Failure, bool) {
	return Request{}, Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true
}

func invalidResponse() (Response, Failure, bool) {
	return Response{}, Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}, true
}

func decodeObject(document []byte, maximum int) (map[string]any, error) {
	return wirejson.DecodeObject(document, wirejson.Limits{Bytes: maximum, ValueDepth: maxJSONDepth, Values: maxWireValues, ArrayValues: maxWireArrayValues})
}
