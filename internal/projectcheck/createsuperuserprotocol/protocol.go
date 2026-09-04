// Package createsuperuserprotocol defines the private, current-only operator
// provisioning wire shared by the global GoDj command and one project-linked
// runner. Request bytes are sensitive and deliberately do not use the generic
// project command stdin document.
package createsuperuserprotocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"unicode/utf8"
)

const (
	Version         uint64 = 1
	PrivateArgument        = "__godj_project_createsuperuser_runner_v1"
	// KnownCreatedResponseFailureExitCode and
	// KnownCreatedBackendCleanupResponseFailureExitCode are private child-only
	// exits used when the linked runner knows the durable insert committed but
	// could not publish its strict response. They preserve whether output alone
	// failed or a preceding backend close also failed. Neither is exposed as the
	// global command's exit code.
	KnownCreatedResponseFailureExitCode               = 86
	KnownCreatedBackendCleanupResponseFailureExitCode = 87

	Magic = "GODJCSU1"

	MaxUsernameBytes = 256
	MaxPasswordBytes = 1_024
	MaxRequestBytes  = len(Magic) + 2 + 2 + MaxUsernameBytes + MaxPasswordBytes
	MaxResponseBytes = 4_096

	requestHeaderBytes = len(Magic) + 2 + 2
	maxJSONDepth       = 4
	maxJSONValues      = 16
)

const (
	CategoryProtocol = "operator_project_protocol_error"
	CategoryState    = "system_state_error"
	CategoryBackend  = "system_state_backend_error"
	CategoryInternal = "operator_project_internal_error"
)

const (
	CodeInvalidRequest       = "invalid_project_createsuperuser_runner_request"
	CodeRunnerFailed         = "project_createsuperuser_runner_failed"
	CodeProtocolIncompatible = "project_createsuperuser_protocol_incompatible"
	CodeInvalidResponse      = "invalid_project_createsuperuser_runner_response"

	CodeBackendOpenFailed  = "backend_open_failed"
	CodeInvalidBackend     = "invalid_backend"
	CodeBackendCloseFailed = "backend_close_failed"

	CodeInvalidConfig            = "invalid_config"
	CodeInvalidInput             = "invalid_input"
	CodeSchemaUnavailable        = "schema_unavailable"
	CodeInvalidCardinality       = "invalid_cardinality"
	CodeCorruptState             = "corrupt_state"
	CodePersistenceFailure       = "persistence_failure"
	CodeCredentialAlreadyExists  = "credential_already_exists"
	CodeCredentialPolicyMismatch = "credential_policy_mismatch"
	CodeProjectInternalError     = "project_internal_error"
)

var (
	errInvalidRequest   = errors.New("project createsuperuser protocol: invalid request")
	errRequestTransport = errors.New("project createsuperuser protocol: request transport failed")
)

// Request owns mutable copies of the one username and password transported to
// the linked runner. Call Clear as soon as the provisioning call no longer
// needs them. String and GoString never expose either value.
type Request struct {
	Username []byte
	Password []byte
}

// String returns a value-free representation safe for framework diagnostics.
func (Request) String() string {
	return "createsuperuserprotocol.Request{redacted}"
}

// GoString returns a value-free representation safe for %#v formatting.
func (Request) GoString() string {
	return "createsuperuserprotocol.Request{redacted}"
}

// Clear best-effort clears the mutable buffers owned by request and releases
// its references. It cannot guarantee removal of runtime, transport, or caller
// copies outside these slices.
func (request *Request) Clear() {
	if request == nil {
		return
	}
	clear(request.Username)
	clear(request.Password)
	request.Username = nil
	request.Password = nil
}

// Failure is the closed, detail-free private failure metadata. KnownCreated is
// true only when a durable create is known to have happened before a later
// failure; false is omitted from the canonical response.
type Failure struct {
	Category     string
	Code         string
	KnownCreated bool
}

// Response is one strict success/failure union. A success requires Created to
// be true. A failure requires one allowed linked Failure and Created false.
type Response struct {
	OK      bool
	Created bool
	Failure Failure
}

// EncodeRequest returns a fresh mutable canonical binary frame. It does not
// retain or modify the caller-owned request buffers.
func EncodeRequest(request Request) ([]byte, error) {
	if !validRequest(request) {
		return nil, errInvalidRequest
	}

	document := make([]byte, requestHeaderBytes+len(request.Username)+len(request.Password))
	copy(document, Magic)
	binary.BigEndian.PutUint16(document[len(Magic):], uint16(len(request.Username)))
	binary.BigEndian.PutUint16(document[len(Magic)+2:], uint16(len(request.Password)))
	offset := requestHeaderBytes
	copy(document[offset:], request.Username)
	offset += len(request.Username)
	copy(document[offset:], request.Password)
	return document, nil
}

// DecodeRequest validates a completed frame, including exact length and EOF
// shape, before allocating detached username and password buffers. It never
// modifies or retains document. The caller remains responsible for clearing
// document when it owns sensitive bytes.
func DecodeRequest(document []byte) (Request, Failure, bool) {
	if len(document) < len(Magic) {
		return invalidRequest()
	}
	if !bytes.Equal(document[:len(Magic)], []byte(Magic)) {
		if bytes.Equal(document[:len(Magic)-1], []byte(Magic[:len(Magic)-1])) {
			return Request{}, Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true
		}
		return invalidRequest()
	}
	if len(document) < requestHeaderBytes || len(document) > MaxRequestBytes {
		return invalidRequest()
	}

	usernameLength := int(binary.BigEndian.Uint16(document[len(Magic) : len(Magic)+2]))
	passwordLength := int(binary.BigEndian.Uint16(document[len(Magic)+2 : requestHeaderBytes]))
	if usernameLength < 1 || usernameLength > MaxUsernameBytes || passwordLength < 1 || passwordLength > MaxPasswordBytes {
		return invalidRequest()
	}
	expected := requestHeaderBytes + usernameLength + passwordLength
	if len(document) != expected {
		return invalidRequest()
	}

	usernameView := document[requestHeaderBytes : requestHeaderBytes+usernameLength]
	passwordView := document[requestHeaderBytes+usernameLength : expected]
	if !validUsername(usernameView) || !validPassword(passwordView) {
		return invalidRequest()
	}

	request := Request{
		Username: append([]byte(nil), usernameView...),
		Password: append([]byte(nil), passwordView...),
	}
	return request, Failure{}, false
}

// ReadRequest reads at most one byte beyond the hard frame bound, requires the
// reader's EOF for any valid frame, and clears all package-owned temporary
// request buffers before returning. Reader error text is not propagated onto
// the sensitive framework boundary.
func ReadRequest(reader io.Reader) (Request, Failure, bool, error) {
	if reader == nil {
		return Request{}, Failure{}, false, errRequestTransport
	}
	document, err := readSensitiveAtMost(reader, MaxRequestBytes)
	if err != nil {
		return Request{}, Failure{}, false, errRequestTransport
	}
	defer clear(document)
	request, failure, failed := DecodeRequest(document)
	return request, failure, failed, nil
}

// ParseResponse validates a completed, bounded private response. A failed
// transport takes precedence over all response bytes.
func ParseResponse(document []byte, transportOK bool) (Response, Failure, bool) {
	if !transportOK {
		return Response{}, Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}, true
	}
	object, err := decodeObject(document)
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
		result, ok := object["result"].(map[string]wireValue)
		if !ok || !hasExactKeys(result, "created") || result["created"] != true {
			return invalidResponse()
		}
		return Response{OK: true, Created: true}, Failure{}, false
	case "error":
		if !hasExactKeys(object, "protocol_version", "status", "error") {
			return invalidResponse()
		}
		errorObject, ok := object["error"].(map[string]wireValue)
		if !ok {
			return invalidResponse()
		}
		if !hasExactKeys(errorObject, "category", "code") &&
			!hasExactKeys(errorObject, "category", "code", "known_created") {
			return invalidResponse()
		}
		category, categoryOK := errorObject["category"].(string)
		code, codeOK := errorObject["code"].(string)
		failure := Failure{Category: category, Code: code}
		if known, exists := errorObject["known_created"]; exists {
			knownCreated, knownOK := known.(bool)
			if !knownOK || !knownCreated {
				return invalidResponse()
			}
			failure.KnownCreated = true
		}
		if !categoryOK || !codeOK || !IsLinkedFailure(failure) {
			return invalidResponse()
		}
		return Response{Failure: failure}, Failure{}, false
	default:
		return invalidResponse()
	}
}

// EncodeResponse returns the one canonical response union document.
func EncodeResponse(response Response) ([]byte, error) {
	if response.OK {
		if !response.Created || response.Failure != (Failure{}) {
			return nil, errors.New("project createsuperuser protocol: invalid success response")
		}
		return []byte(`{"protocol_version":1,"status":"ok","result":{"created":true}}`), nil
	}
	if response.Created || !IsLinkedFailure(response.Failure) {
		return nil, errors.New("project createsuperuser protocol: invalid error response")
	}

	type failureDocument struct {
		Category     string `json:"category"`
		Code         string `json:"code"`
		KnownCreated bool   `json:"known_created,omitempty"`
	}
	document, err := json.Marshal(struct {
		ProtocolVersion uint64          `json:"protocol_version"`
		Status          string          `json:"status"`
		Error           failureDocument `json:"error"`
	}{
		ProtocolVersion: Version,
		Status:          "error",
		Error: failureDocument{
			Category:     response.Failure.Category,
			Code:         response.Failure.Code,
			KnownCreated: response.Failure.KnownCreated,
		},
	})
	if err != nil {
		return nil, errors.New("project createsuperuser protocol: response encoding failed")
	}
	if len(document) > MaxResponseBytes {
		clear(document)
		return nil, errors.New("project createsuperuser protocol: response exceeds resource limit")
	}
	return document, nil
}

// WriteResponse encodes and performs exactly one complete write attempt.
func WriteResponse(writer io.Writer, response Response) error {
	if writer == nil {
		return errors.New("project createsuperuser protocol: nil response writer")
	}
	document, err := EncodeResponse(response)
	if err != nil {
		return err
	}
	written, err := writer.Write(document)
	if err != nil {
		return errors.New("project createsuperuser protocol: response write failed")
	}
	if written != len(document) {
		return fmt.Errorf("project createsuperuser protocol: response write failed: %w", io.ErrShortWrite)
	}
	return nil
}

// IsLinkedFailure reports whether failure may be emitted by the linked runner.
// Arbitrary category/code text is rejected to keep raw causes off the wire.
func IsLinkedFailure(failure Failure) bool {
	switch failure.Category {
	case CategoryProtocol:
		return !failure.KnownCreated && (failure.Code == CodeInvalidRequest || failure.Code == CodeProtocolIncompatible)
	case CategoryState:
		if failure.KnownCreated {
			return false
		}
		switch failure.Code {
		case CodeInvalidConfig,
			CodeInvalidInput,
			CodeSchemaUnavailable,
			CodeInvalidCardinality,
			CodeCorruptState,
			CodePersistenceFailure,
			CodeCredentialAlreadyExists,
			CodeCredentialPolicyMismatch:
			return true
		}
	case CategoryBackend:
		if failure.KnownCreated {
			return failure.Code == CodeBackendCloseFailed
		}
		switch failure.Code {
		case CodeBackendOpenFailed, CodeInvalidBackend, CodeBackendCloseFailed:
			return true
		}
	case CategoryInternal:
		return !failure.KnownCreated && failure.Code == CodeProjectInternalError
	}
	return false
}

// ValidUsername reports whether username has the exact canonical shape shared
// by terminal input and the private request decoder.
func ValidUsername(username []byte) bool {
	return validUsername(username)
}

// ValidPassword reports whether password has the exact canonical shape shared
// by terminal input and the private request decoder. Leading and trailing
// spaces are preserved; an all-whitespace value is rejected.
func ValidPassword(password []byte) bool {
	return validPassword(password)
}

func validRequest(request Request) bool {
	return validUsername(request.Username) && validPassword(request.Password) &&
		requestHeaderBytes+len(request.Username)+len(request.Password) <= MaxRequestBytes
}

func validUsername(username []byte) bool {
	if len(username) < 1 || len(username) > MaxUsernameBytes || !utf8.Valid(username) ||
		!bytes.Equal(username, bytes.TrimSpace(username)) {
		return false
	}
	for _, character := range username {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validPassword(password []byte) bool {
	if len(password) < 1 || len(password) > MaxPasswordBytes || !utf8.Valid(password) || len(bytes.TrimSpace(password)) == 0 {
		return false
	}
	for _, character := range password {
		if character == 0 || character == '\r' || character == '\n' {
			return false
		}
	}
	return true
}

func readSensitiveAtMost(reader io.Reader, maximum int) ([]byte, error) {
	retained := make([]byte, 0, maximum+1)
	buffer := make([]byte, maximum+1)
	defer clear(buffer)
	emptyReads := 0
	for {
		read, err := reader.Read(buffer)
		if read < 0 || read > len(buffer) {
			clear(retained)
			return nil, errRequestTransport
		}
		if read > 0 {
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
				clear(retained)
				return nil, errRequestTransport
			}
		}
		if err != nil {
			if safeRequestErrorIs(err, io.EOF) {
				return retained, nil
			}
			clear(retained)
			return nil, errRequestTransport
		}
		if len(retained) > maximum {
			return retained, nil
		}
	}
}

func safeRequestErrorIs(err, target error) (matched bool) {
	if err == nil {
		return false
	}
	value := reflect.ValueOf(err)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return false
		}
	}
	defer func() {
		if recover() != nil {
			matched = false
		}
	}()
	return errors.Is(err, target)
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

func invalidRequest() (Request, Failure, bool) {
	return Request{}, Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true
}

func invalidResponse() (Response, Failure, bool) {
	return Response{}, Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}, true
}

type wireValue any

func decodeObject(document []byte) (map[string]wireValue, error) {
	if len(document) > MaxResponseBytes || !utf8.Valid(document) || !validJSONSurrogateEscapes(document) {
		return nil, errors.New("invalid response framing")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	budget := maxJSONValues
	value, err := decodeValue(decoder, 0, &budget)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("trailing response value")
		}
		return nil, err
	}
	object, ok := value.(map[string]wireValue)
	if !ok {
		return nil, errors.New("response root is not an object")
	}
	return object, nil
}

func decodeValue(decoder *json.Decoder, depth int, budget *int) (wireValue, error) {
	if depth > maxJSONDepth || *budget <= 0 {
		return nil, errors.New("response structure limit exceeded")
	}
	*budget--
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	if delimiter != '{' {
		return nil, errors.New("response arrays are not supported")
	}
	object := make(map[string]wireValue)
	for decoder.More() {
		member, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := member.(string)
		if !ok {
			return nil, errors.New("response object key is not a string")
		}
		if _, exists := object[key]; exists {
			return nil, errors.New("duplicate response object key")
		}
		child, err := decodeValue(decoder, depth+1, budget)
		if err != nil {
			return nil, err
		}
		object[key] = child
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, errors.New("invalid response object close")
	}
	return object, nil
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

// validJSONSurrogateEscapes rejects unpaired UTF-16 surrogate escapes that
// encoding/json would otherwise replace with U+FFFD.
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
