// Package protocol defines the private, closed project-spec generation wire
// shared by the global command and a project-linked declaration runner.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectwire"
	"github.com/progresshans/godj/internal/wirejson"
)

const (
	Version          uint64 = 1
	PrivateArgument         = "__godj_project_generate_runner_v1"
	MaxRequestBytes         = 64 << 10
	MaxResponseBytes        = 64 << 20
	MaxApps                 = projectwire.MaxApps
	MaxJSONDepth            = 32
)

const (
	CategoryProtocol    = "project_generation_protocol_error"
	CategoryDeclaration = "project_generation_declaration_error"

	CodeInvalidRequest        = "invalid_project_generate_runner_request"
	CodeProtocolIncompatible  = "project_generate_protocol_incompatible"
	CodeInvalidResponse       = "invalid_project_generate_runner_response"
	CodeRunnerFailed          = "project_generate_runner_failed"
	CodeProjectSpecLoadFailed = "project_spec_load_failed"
)

// Failure is the detail-free failure pair permitted on the private wire.
type Failure struct {
	Category string
	Code     string
}

// Response is one closed private outcome. A successful response owns a deep
// ProjectSpec snapshot; a failed response owns only its closed Failure pair.
type Response struct {
	OK          bool
	ProjectSpec codegen.ProjectSpec
	Failure     Failure
}

type successDocument struct {
	ProtocolVersion uint64           `json:"protocol_version"`
	Status          string           `json:"status"`
	ProjectSpec     projectwire.Spec `json:"project_spec"`
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

// RequestDocument returns a fresh copy of the sole canonical request.
func RequestDocument() []byte {
	return []byte(`{"protocol_version":1,"command":"generate.project_spec"}`)
}

// ReadRequest reads through EOF within a fixed bound. Completed malformed
// input is a logical failure; a Reader failure remains a Go transport error.
func ReadRequest(reader io.Reader) (Failure, bool, error) {
	if reader == nil {
		return Failure{}, false, errors.New("project generation protocol: nil request reader")
	}
	document, err := wirejson.Read(reader, MaxRequestBytes, wirejson.DrainToEOF)
	if err != nil {
		return Failure{}, false, fmt.Errorf("project generation protocol: read request: %w", err)
	}
	return parseRequest(document)
}

// ParseResponse validates completed response bytes. Transport failure takes
// precedence over any bytes. Valid linked logical failures remain in Response.
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
		if err := wirejson.DecodeCanonical(document, &decoded); err != nil {
			return invalidResponse()
		}
		if version != Version {
			return Response{}, Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true
		}
		spec := projectwire.Declaration(decoded.ProjectSpec)
		if err := projectwire.Validate(spec); err != nil {
			return invalidResponse()
		}
		return Response{OK: true, ProjectSpec: projectwire.Clone(spec)}, Failure{}, false
	case "error":
		var decoded failureDocument
		if err := wirejson.DecodeCanonical(document, &decoded); err != nil {
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

// EncodeResponse returns canonical, bounded response bytes.
func EncodeResponse(response Response) ([]byte, error) {
	var document []byte
	var err error
	if response.OK {
		if response.Failure != (Failure{}) {
			return nil, errors.New("project generation protocol: invalid success response")
		}
		if err := projectwire.Validate(response.ProjectSpec); err != nil {
			return nil, fmt.Errorf("project generation protocol: invalid success response: %w", err)
		}
		measured, err := measureSuccessDocument(projectwire.View(response.ProjectSpec))
		if err != nil {
			return nil, err
		}
		wireSpec := projectwire.Snapshot(response.ProjectSpec)
		document, err = json.Marshal(successDocument{
			ProtocolVersion: Version,
			Status:          "ok",
			ProjectSpec:     wireSpec,
		})
		if err == nil && len(document) != measured {
			return nil, errors.New("project generation protocol: internal response size mismatch")
		}
	} else {
		if !isZeroSpec(response.ProjectSpec) || !IsLinkedFailure(response.Failure) {
			return nil, errors.New("project generation protocol: invalid error response")
		}
		document, err = json.Marshal(failureDocument{
			ProtocolVersion: Version,
			Status:          "error",
			Error:           wireFailure{Category: response.Failure.Category, Code: response.Failure.Code},
		})
	}
	if err != nil {
		return nil, fmt.Errorf("project generation protocol: encode response: %w", err)
	}
	if err := validateDocumentSize(len(document), MaxResponseBytes); err != nil {
		return nil, err
	}
	return document, nil
}

// WriteResponse encodes and performs one complete write attempt.
func WriteResponse(writer io.Writer, response Response) error {
	if writer == nil {
		return errors.New("project generation protocol: nil response writer")
	}
	document, err := EncodeResponse(response)
	if err != nil {
		return err
	}
	written, err := writer.Write(document)
	if err != nil {
		return fmt.Errorf("project generation protocol: write response: %w", err)
	}
	if written != len(document) {
		return fmt.Errorf("project generation protocol: write response: %w", io.ErrShortWrite)
	}
	return nil
}

// IsLinkedFailure reports whether a pair may be emitted by the linked runner.
func IsLinkedFailure(failure Failure) bool {
	if failure.Category == CategoryProtocol {
		return failure.Code == CodeInvalidRequest || failure.Code == CodeProtocolIncompatible
	}
	return failure.Category == CategoryDeclaration && failure.Code == CodeProjectSpecLoadFailed
}

func parseRequest(document []byte) (Failure, bool, error) {
	if err := scanJSONDocument(document, MaxRequestBytes); err != nil {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	preflightVersion, preflightCommand, err := preflightRequest(document)
	if err != nil {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	var request struct {
		ProtocolVersion uint64 `json:"protocol_version"`
		Command         string `json:"command"`
	}
	if err := wirejson.DecodeCanonical(document, &request); err != nil {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	if request.ProtocolVersion != preflightVersion || request.Command != preflightCommand {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	if request.ProtocolVersion != Version {
		return Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true, nil
	}
	if request.Command != "generate.project_spec" || !bytes.Equal(document, RequestDocument()) {
		return Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, true, nil
	}
	return Failure{}, false, nil
}

func invalidResponse() (Response, Failure, bool) {
	return Response{}, Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}, true
}

func isZeroSpec(spec codegen.ProjectSpec) bool {
	return spec.Project == (codegen.PackageSpec{}) && len(spec.Apps) == 0
}

func validateDocumentSize(size, maximum int) error {
	if size > maximum {
		return fmt.Errorf("project generation protocol: document size %d exceeds %d", size, maximum)
	}
	return nil
}
