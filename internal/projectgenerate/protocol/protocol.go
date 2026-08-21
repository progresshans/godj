// Package protocol defines the private, closed project-spec generation wire
// shared by the global command and a project-linked declaration runner.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/schema/ir"
)

const (
	Version          uint64 = 1
	PrivateArgument         = "__godj_project_generate_runner_v1"
	MaxRequestBytes         = 64 << 10
	MaxResponseBytes        = 64 << 20
	MaxApps                 = 8_192
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

type successDocument struct {
	ProtocolVersion uint64          `json:"protocol_version"`
	Status          string          `json:"status"`
	ProjectSpec     wireProjectSpec `json:"project_spec"`
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
	document, err := readAtMost(reader, MaxRequestBytes)
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
		if err := decodeCanonical(document, &decoded); err != nil {
			return invalidResponse()
		}
		if version != Version {
			return Response{}, Failure{Category: CategoryProtocol, Code: CodeProtocolIncompatible}, true
		}
		spec := fromWireProjectSpec(decoded.ProjectSpec)
		if err := validateProjectSpec(spec); err != nil {
			return invalidResponse()
		}
		return Response{OK: true, ProjectSpec: cloneProjectSpec(spec)}, Failure{}, false
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

// EncodeResponse returns canonical, bounded response bytes.
func EncodeResponse(response Response) ([]byte, error) {
	var document []byte
	var err error
	if response.OK {
		if response.Failure != (Failure{}) {
			return nil, errors.New("project generation protocol: invalid success response")
		}
		if err := validateProjectSpec(response.ProjectSpec); err != nil {
			return nil, fmt.Errorf("project generation protocol: invalid success response: %w", err)
		}
		measured, err := measureSuccessDocument(shallowWireProjectSpec(response.ProjectSpec))
		if err != nil {
			return nil, err
		}
		wireSpec := toWireProjectSpec(response.ProjectSpec)
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

// ValidateProjectSpec applies the exact success-wire semantic and resource
// bounds without encoding or retaining the caller's declaration snapshot.
func ValidateProjectSpec(spec codegen.ProjectSpec) error {
	return validateProjectSpec(spec)
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
	if err := decodeCanonical(document, &request); err != nil {
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

func decodeCanonical(document []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, document) {
		return errors.New("non-canonical JSON document")
	}
	return nil
}

func validateProjectSpec(spec codegen.ProjectSpec) error {
	if len(spec.Apps) > MaxApps {
		return fmt.Errorf("apps exceed maximum %d", MaxApps)
	}
	for _, candidate := range []struct {
		path  string
		value string
	}{
		{path: "project.package_name", value: spec.Project.PackageName},
		{path: "project.import_path", value: spec.Project.ImportPath},
		{path: "project.directory", value: spec.Project.Directory},
	} {
		if err := validateWireString(candidate.path, candidate.value); err != nil {
			return err
		}
	}
	schemas := make([]ir.Schema, len(spec.Apps))
	for index := range spec.Apps {
		app := spec.Apps[index]
		for _, candidate := range []struct {
			path  string
			value string
		}{
			{path: fmt.Sprintf("apps[%d].alias", index), value: app.Alias},
			{path: fmt.Sprintf("apps[%d].package.package_name", index), value: app.Package.PackageName},
			{path: fmt.Sprintf("apps[%d].package.import_path", index), value: app.Package.ImportPath},
			{path: fmt.Sprintf("apps[%d].package.directory", index), value: app.Package.Directory},
		} {
			if err := validateWireString(candidate.path, candidate.value); err != nil {
				return err
			}
		}
		if app.Schema.FormatVersion != ir.CurrentFormatVersion {
			return fmt.Errorf("apps[%d].schema format version %d is incompatible", index, app.Schema.FormatVersion)
		}
		schemas[index] = app.Schema
	}
	return projectspec.ValidateSchemas(schemas)
}

func validateWireString(path, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not UTF-8", path)
	}
	if len(value) > projectspec.MaxSchemaStringBytes {
		return fmt.Errorf("%s exceeds %d bytes", path, projectspec.MaxSchemaStringBytes)
	}
	return nil
}

func cloneProjectSpec(input codegen.ProjectSpec) codegen.ProjectSpec {
	clone := input
	clone.Apps = make([]codegen.AppSpec, len(input.Apps))
	for index := range input.Apps {
		clone.Apps[index] = input.Apps[index]
		clone.Apps[index].Schema = input.Apps[index].Schema.Clone()
	}
	return clone
}

func toWireProjectSpec(input codegen.ProjectSpec) wireProjectSpec {
	result := wireProjectSpec{Project: toWirePackage(input.Project), Apps: make([]wireApp, len(input.Apps))}
	for index := range input.Apps {
		result.Apps[index] = wireApp{
			Alias: input.Apps[index].Alias, Package: toWirePackage(input.Apps[index].Package), Schema: canonicalWireSchema(input.Apps[index].Schema),
		}
	}
	return result
}

func shallowWireProjectSpec(input codegen.ProjectSpec) wireProjectSpec {
	result := wireProjectSpec{Project: toWirePackage(input.Project), Apps: make([]wireApp, len(input.Apps))}
	for index := range input.Apps {
		result.Apps[index] = wireApp{
			Alias: input.Apps[index].Alias, Package: toWirePackage(input.Apps[index].Package), Schema: input.Apps[index].Schema,
		}
	}
	return result
}

func fromWireProjectSpec(input wireProjectSpec) codegen.ProjectSpec {
	result := codegen.ProjectSpec{Project: fromWirePackage(input.Project), Apps: make([]codegen.AppSpec, len(input.Apps))}
	for index := range input.Apps {
		result.Apps[index] = codegen.AppSpec{
			Alias: input.Apps[index].Alias, Package: fromWirePackage(input.Apps[index].Package), Schema: input.Apps[index].Schema,
		}
	}
	return result
}

func toWirePackage(input codegen.PackageSpec) wirePackage {
	return wirePackage{PackageName: input.PackageName, ImportPath: input.ImportPath, Directory: input.Directory}
}

func fromWirePackage(input wirePackage) codegen.PackageSpec {
	return codegen.PackageSpec{PackageName: input.PackageName, ImportPath: input.ImportPath, Directory: input.Directory}
}

func canonicalWireSchema(input ir.Schema) ir.Schema {
	clone := input.Clone()
	if clone.Models == nil {
		clone.Models = []ir.Model{}
	}
	for index := range clone.Models {
		if clone.Models[index].Fields == nil {
			clone.Models[index].Fields = []ir.Field{}
		}
	}
	return clone
}

func isZeroSpec(spec codegen.ProjectSpec) bool {
	return spec.Project == (codegen.PackageSpec{}) && len(spec.Apps) == 0
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
		return fmt.Errorf("project generation protocol: document size %d exceeds %d", size, maximum)
	}
	return nil
}
