// Package sqlmigrateprotocol defines the private, closed migration-SQL wire
// shared by the global GoDj command and one project-linked runner.
package sqlmigrateprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Version         uint64 = 1
	PrivateArgument        = "__godj_project_sqlmigrate_runner_v1"

	MaxRequestBytes       = 16 << 20
	MaxResponseBytes      = 101 << 20
	MaxStatements         = 2_048
	MaxIdentityBytes      = 1 << 20
	MaxIdentityTotalBytes = 2 << 20
	MaxStatementBodyBytes = 16 << 20
	MaxPublicOutputBytes  = MaxStatementBodyBytes + 2*MaxStatements

	maxJSONDepth       = 16
	maxWireObjectKeys  = 64
	maxWireArrayValues = MaxStatements + 1
	maxWireValues      = MaxStatements + 64
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
	CategoryPlan        = "migration_plan_error"
	CategorySQLRender   = "migration_sql_render_error"
	CategorySQLResource = "migration_sql_resource_error"
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
	CodeInvalidRequest                = "invalid_project_sqlmigrate_runner_request"
	CodeRunnerFailed                  = "project_sqlmigrate_runner_failed"
	CodeProtocolIncompatible          = "project_sqlmigrate_protocol_incompatible"
	CodeInvalidResponse               = "invalid_project_sqlmigrate_runner_response"
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
	CodeRendererUnavailable           = "renderer_unavailable"
	CodeRenderFailed                  = "render_failed"
	CodeInvalidRenderedSQL            = "invalid_rendered_sql"
	CodeRenderedSQLResourceLimit      = "rendered_sql_resource_limit"
	CodeProjectInternalError          = "project_internal_error"
)

// Request identifies exactly one literal forward migration. Its zero value is
// invalid; zero is an ordinary valid Name and latest is deliberately rejected.
type Request struct {
	App  string
	Name string
}

// Failure is the closed, detail-free failure carried over the private wire.
// CleanupFailed records only a secondary outer cleanup observation.
type Failure struct {
	Category      string
	Code          string
	CleanupFailed bool
}

// Result owns detached semicolon-free statement bodies in operation order.
// A successful empty migration uses a non-nil empty Statements slice.
type Result struct {
	Statements []string
}

// Response is one strict success/failure union.
type Response struct {
	OK      bool
	Result  Result
	Failure Failure
}

// EncodeRequest returns a fresh canonical bounded request document.
func EncodeRequest(request Request) ([]byte, error) {
	if !validRequest(request) {
		return nil, errors.New("project sqlmigrate protocol: invalid request")
	}
	document, err := json.Marshal(struct {
		ProtocolVersion uint64 `json:"protocol_version"`
		Command         string `json:"command"`
		App             string `json:"app"`
		Name            string `json:"name"`
	}{Version, "migrations.sql", request.App, request.Name})
	if err != nil {
		return nil, fmt.Errorf("project sqlmigrate protocol: encode request: %w", err)
	}
	if len(document) > MaxRequestBytes {
		return nil, errors.New("project sqlmigrate protocol: request exceeds resource limit")
	}
	return document, nil
}

// ReadRequest reads one bounded request through EOF, or stops immediately once
// one byte beyond the hard limit is retained. Completed malformed input is a
// logical protocol failure; Reader failures remain Go transport errors.
func ReadRequest(reader io.Reader) (Request, Failure, bool, error) {
	if reader == nil {
		return Request{}, Failure{}, false, errors.New("project sqlmigrate protocol: nil request reader")
	}
	document, err := readAtMost(reader, MaxRequestBytes)
	if err != nil {
		return Request{}, Failure{}, false, fmt.Errorf("project sqlmigrate protocol: read request: %w", err)
	}
	request, failure, failed := parseRequest(document)
	return request, failure, failed, nil
}

// ParseResponse validates a completed private response. Transport failure has
// precedence. Resource overflow is distinguished from malformed framing so an
// over-limit child cannot be misreported as a renderer-shape failure.
func ParseResponse(document []byte, transportOK bool) (Response, Failure, bool) {
	if !transportOK {
		return Response{}, Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}, true
	}
	if len(document) > MaxResponseBytes {
		return resourceResponseFailure()
	}
	object, err := decodeObject(document, MaxResponseBytes)
	if err != nil {
		if errors.Is(err, errWireResource) {
			return resourceResponseFailure()
		}
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
		if !ok || !hasExactKeys(resultObject, "statements") {
			return invalidResponse()
		}
		wireStatements, ok := resultObject["statements"].([]wireValue)
		if !ok {
			return invalidResponse()
		}
		if len(wireStatements) > MaxStatements {
			return resourceResponseFailure()
		}
		statements := make([]string, len(wireStatements))
		for index, value := range wireStatements {
			statement, ok := value.(string)
			if !ok {
				return invalidResponse()
			}
			statements[index] = statement
		}
		result := Result{Statements: statements}
		resource, semantic := validateResult(result)
		if resource {
			return resourceResponseFailure()
		}
		if !semantic {
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

// EncodeResponse returns canonical bounded private bytes.
func EncodeResponse(response Response) ([]byte, error) {
	var document []byte
	var err error
	if response.OK {
		resource, semantic := validateResult(response.Result)
		if response.Failure != (Failure{}) || resource || !semantic || response.Result.Statements == nil {
			return nil, errors.New("project sqlmigrate protocol: invalid success response")
		}
		document, err = json.Marshal(struct {
			ProtocolVersion uint64 `json:"protocol_version"`
			Status          string `json:"status"`
			Result          struct {
				Statements []string `json:"statements"`
			} `json:"result"`
		}{ProtocolVersion: Version, Status: "ok", Result: struct {
			Statements []string `json:"statements"`
		}{Statements: response.Result.Statements}})
	} else {
		if response.Result.Statements != nil || !IsLinkedFailure(response.Failure) {
			return nil, errors.New("project sqlmigrate protocol: invalid error response")
		}
		document, err = json.Marshal(struct {
			ProtocolVersion uint64 `json:"protocol_version"`
			Status          string `json:"status"`
			Error           struct {
				Category      string `json:"category"`
				Code          string `json:"code"`
				CleanupFailed bool   `json:"cleanup_failed"`
			} `json:"error"`
		}{ProtocolVersion: Version, Status: "error", Error: struct {
			Category      string `json:"category"`
			Code          string `json:"code"`
			CleanupFailed bool   `json:"cleanup_failed"`
		}{response.Failure.Category, response.Failure.Code, response.Failure.CleanupFailed}})
	}
	if err != nil {
		return nil, fmt.Errorf("project sqlmigrate protocol: encode response: %w", err)
	}
	if len(document) > MaxResponseBytes {
		return nil, errors.New("project sqlmigrate protocol: response exceeds resource limit")
	}
	return document, nil
}

// WriteResponse encodes and performs exactly one complete write attempt.
func WriteResponse(writer io.Writer, response Response) error {
	if writer == nil {
		return errors.New("project sqlmigrate protocol: nil response writer")
	}
	document, err := EncodeResponse(response)
	if err != nil {
		return err
	}
	written, err := writer.Write(document)
	if err != nil {
		return fmt.Errorf("project sqlmigrate protocol: write response: %w", err)
	}
	if written != len(document) {
		return fmt.Errorf("project sqlmigrate protocol: write response: %w", io.ErrShortWrite)
	}
	return nil
}

// ExitCode maps only closed public taxonomy values.
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
			"invalid_definition_source", "invalid_definition_document", "definition_format_incompatible",
			"unsupported_definition_operation", "invalid_definition_operation", "invalid_definition_ir",
		)
	case CategoryGraph:
		return exactCode(failure.Code, 1,
			"invalid_node", "duplicate_node", "invalid_dependency", "duplicate_dependency", "dependency_not_found", "dependency_cycle",
		)
	case CategoryState:
		return exactCode(failure.Code, 1, "invalid_state")
	case CategoryCapability:
		return exactCode(failure.Code, 1, "unsupported_operation")
	case CategoryExecution:
		return exactCode(failure.Code, 3, "operation_failed")
	case CategoryPlan:
		return exactCode(failure.Code, 1, "invalid_target", "target_not_found", "invalid_execution_plan", "mixed_directions")
	case CategorySQLRender:
		return exactCode(failure.Code, 3, CodeRendererUnavailable, CodeRenderFailed, CodeInvalidRenderedSQL)
	case CategorySQLResource:
		return exactCode(failure.Code, 1, CodeRenderedSQLResourceLimit)
	case CategoryInternal:
		return exactCode(failure.Code, 3, CodeProjectInternalError)
	}
	return 0, false
}

// IsLinkedFailure reports whether the project runner may emit failure.
func IsLinkedFailure(failure Failure) bool {
	if _, ok := ExitCode(failure); !ok {
		return false
	}
	switch failure.Category {
	case CategoryCommand, CategorySelection, CategoryBuild, CategoryProcess:
		return false
	case CategoryProtocol:
		return !failure.CleanupFailed && (failure.Code == CodeInvalidRequest || failure.Code == CodeProtocolIncompatible)
	default:
		return !failure.CleanupFailed
	}
}

// ValidateResult repeats the private trust-boundary resource and semantic
// checks without serializing. It is used before terminal public formatting.
func ValidateResult(result Result) error {
	resource, semantic := validateResult(result)
	if resource {
		return errors.New("project sqlmigrate protocol: result exceeds resource limit")
	}
	if !semantic || result.Statements == nil {
		return errors.New("project sqlmigrate protocol: result is invalid")
	}
	return nil
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
	version, valid := canonicalUint(versionValue, 65_535)
	if !valid {
		return invalidRequest()
	}
	if version != Version {
		return Request{}, Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true
	}
	if !hasExactKeys(object, "protocol_version", "command", "app", "name") || object["command"] != "migrations.sql" {
		return invalidRequest()
	}
	app, appOK := object["app"].(string)
	name, nameOK := object["name"].(string)
	request := Request{App: app, Name: name}
	if !appOK || !nameOK || !validRequest(request) {
		return invalidRequest()
	}
	return request, Failure{}, false
}

func validRequest(request Request) bool {
	return validApp(request.App) && validName(request.Name) && len(request.App)+len(request.Name) <= MaxIdentityTotalBytes
}

func validApp(value string) bool {
	if value == "" || len(value) > MaxIdentityBytes || !utf8.ValidString(value) {
		return false
	}
	for index := range value {
		character := value[index]
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

func validName(value string) bool {
	return value != "" && value != "latest" && len(value) <= MaxIdentityBytes && utf8.ValidString(value) && !strings.HasPrefix(value, "-")
}

func validateResult(result Result) (resource, semantic bool) {
	if len(result.Statements) > MaxStatements {
		return true, false
	}
	total := 0
	for _, body := range result.Statements {
		if len(body) > MaxStatementBodyBytes-total {
			return true, false
		}
		total += len(body)
	}
	for _, body := range result.Statements {
		if !validStatementBody(body) {
			return false, false
		}
	}
	return false, true
}

func validStatementBody(body string) bool {
	if body == "" || !utf8.ValidString(body) || strings.ContainsRune(body, ';') {
		return false
	}
	if asciiWhitespace(body[0]) || asciiWhitespace(body[len(body)-1]) {
		return false
	}
	for _, character := range body {
		if character != '\n' && unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func asciiWhitespace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

func readAtMost(reader io.Reader, maximum int) ([]byte, error) {
	retained := make([]byte, 0, min(maximum+1, 32<<10))
	buffer := make([]byte, 32<<10)
	emptyReads := 0
	for {
		read, err := reader.Read(buffer)
		if read < 0 || read > len(buffer) {
			return nil, errors.New("invalid request reader count")
		}
		if read > 0 {
			emptyReads = 0
			remaining := maximum + 1 - len(retained)
			if remaining > 0 {
				retained = append(retained, buffer[:min(read, remaining)]...)
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
		if len(retained) > maximum {
			return retained, nil
		}
	}
}

func exactCode(code string, exit int, allowed ...string) (int, bool) {
	for _, candidate := range allowed {
		if code == candidate {
			return exit, true
		}
	}
	return 0, false
}

func invalidRequest() (Request, Failure, bool) {
	return Request{}, Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true
}

func invalidResponse() (Response, Failure, bool) {
	return Response{}, Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}, true
}

func resourceResponseFailure() (Response, Failure, bool) {
	return Response{}, Failure{Category: CategorySQLResource, Code: CodeRenderedSQLResourceLimit}, true
}

type wireValue any

var errWireResource = errors.New("wire resource limit exceeded")

type decodeBudget struct {
	values int
}

func decodeObject(document []byte, maximum int) (map[string]wireValue, error) {
	if len(document) > maximum {
		return nil, errWireResource
	}
	if !utf8.Valid(document) || !validJSONSurrogateEscapes(document) {
		return nil, errors.New("invalid wire framing")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	budget := decodeBudget{}
	value, err := decodeValue(decoder, 0, &budget)
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

func decodeValue(decoder *json.Decoder, depth int, budget *decodeBudget) (wireValue, error) {
	if depth > maxJSONDepth {
		return nil, errors.New("wire nesting limit exceeded")
	}
	budget.values++
	if budget.values > maxWireValues {
		return nil, errWireResource
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
			if len(object) >= maxWireObjectKeys {
				return nil, errWireResource
			}
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
			child, err := decodeValue(decoder, depth+1, budget)
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
			if len(array) >= maxWireArrayValues {
				return nil, errWireResource
			}
			child, err := decodeValue(decoder, depth+1, budget)
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

// validJSONSurrogateEscapes rejects only unpaired UTF-16 surrogate escapes in
// JSON strings. encoding/json otherwise replaces those escapes with U+FFFD,
// which could collapse distinct migration or SQL bytes at the wire boundary.
func validJSONSurrogateEscapes(document []byte) bool {
	inString := false
	for index := 0; index < len(document); index++ {
		switch document[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString || index+1 >= len(document) {
				continue
			}
			if document[index+1] != 'u' {
				index++
				continue
			}
			unit, ok := jsonHexCodeUnit(document, index+2)
			if !ok {
				// The JSON decoder remains the authority for malformed escapes.
				return true
			}
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if index+7 >= len(document) || document[index+6] != '\\' || document[index+7] != 'u' {
					return false
				}
				low, paired := jsonHexCodeUnit(document, index+8)
				if !paired || low < 0xdc00 || low > 0xdfff {
					return false
				}
				index += 11
			case unit >= 0xdc00 && unit <= 0xdfff:
				return false
			default:
				index += 5
			}
		}
	}
	return true
}

func jsonHexCodeUnit(document []byte, offset int) (uint16, bool) {
	if offset < 0 || offset+4 > len(document) {
		return 0, false
	}
	var value uint16
	for _, character := range document[offset : offset+4] {
		value <<= 4
		switch {
		case character >= '0' && character <= '9':
			value |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
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
