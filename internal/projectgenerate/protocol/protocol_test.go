package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/schema/ir"
)

func TestCanonicalRequestAndFreshOwnership(t *testing.T) {
	want := `{"protocol_version":1,"command":"generate.project_spec"}`
	first := RequestDocument()
	if string(first) != want {
		t.Fatalf("request = %s", first)
	}
	first[0] = '!'
	if string(RequestDocument()) != want {
		t.Fatal("RequestDocument retained caller mutation")
	}
	failure, failed, err := ReadRequest(bytes.NewReader(RequestDocument()))
	if err != nil || failed || failure != (Failure{}) {
		t.Fatalf("ReadRequest = %+v, %v, %v", failure, failed, err)
	}
	failure, failed, err = ReadRequest(iotest.OneByteReader(bytes.NewReader(RequestDocument())))
	if err != nil || failed || failure != (Failure{}) {
		t.Fatalf("one-byte ReadRequest = %+v, %v, %v", failure, failed, err)
	}
}

func TestRequestRejectsNoncanonicalAndClassifiesCanonicalVersion(t *testing.T) {
	tests := []struct {
		name string
		wire string
		code string
	}{
		{name: "reordered", wire: `{"command":"generate.project_spec","protocol_version":1}`, code: CodeInvalidRequest},
		{name: "whitespace", wire: ` {"protocol_version":1,"command":"generate.project_spec"}`, code: CodeInvalidRequest},
		{name: "duplicate", wire: `{"protocol_version":1,"protocol_version":1,"command":"generate.project_spec"}`, code: CodeInvalidRequest},
		{name: "unknown", wire: `{"protocol_version":1,"command":"generate.project_spec","x":0}`, code: CodeInvalidRequest},
		{name: "trailing", wire: `{"protocol_version":1,"command":"generate.project_spec"}{}`, code: CodeInvalidRequest},
		{name: "short", wire: `{"protocol_version":1`, code: CodeInvalidRequest},
		{name: "wrong command", wire: `{"protocol_version":1,"command":"other"}`, code: CodeInvalidRequest},
		{name: "incompatible", wire: `{"protocol_version":2,"command":"generate.project_spec"}`, code: CodeProtocolIncompatible},
		{name: "noncanonical incompatible", wire: `{"protocol_version":2.0,"command":"generate.project_spec"}`, code: CodeInvalidRequest},
		{name: "bom", wire: "\ufeff" + `{"protocol_version":1,"command":"generate.project_spec"}`, code: CodeInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure, failed, err := ReadRequest(strings.NewReader(test.wire))
			if err != nil || !failed || failure != (Failure{Category: CategoryProtocol, Code: test.code}) {
				t.Fatalf("ReadRequest = %+v, %v, %v", failure, failed, err)
			}
		})
	}
}

func TestRequestTransportAndSizeBoundaries(t *testing.T) {
	readerErr := errors.New("reader failed")
	if _, failed, err := ReadRequest(errorReader{err: readerErr}); failed || !errors.Is(err, readerErr) {
		t.Fatalf("error reader = failed %v err %v", failed, err)
	}
	if _, failed, err := ReadRequest(nil); failed || err == nil {
		t.Fatalf("nil reader = failed %v err %v", failed, err)
	}
	if err := validateDocumentSize(MaxRequestBytes, MaxRequestBytes); err != nil {
		t.Fatalf("maximum request size rejected: %v", err)
	}
	if err := validateDocumentSize(MaxRequestBytes+1, MaxRequestBytes); err == nil {
		t.Fatal("maximum+1 request size accepted")
	}
	oversized := bytes.Repeat([]byte{' '}, MaxRequestBytes+1)
	failure, failed, err := ReadRequest(bytes.NewReader(oversized))
	if err != nil || !failed || failure.Code != CodeInvalidRequest {
		t.Fatalf("oversized request = %+v, %v, %v", failure, failed, err)
	}
}

func TestSuccessResponseCanonicalRoundTripAndDeepOwnership(t *testing.T) {
	spec := validSpec()
	document, err := EncodeResponse(Response{OK: true, ProjectSpec: spec})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"protocol_version":1,"status":"ok","project_spec":{"project":{"package_name":"project","import_path":"example.com/site/project","directory":"project"},"apps":[{"alias":"articles","package":{"package_name":"articles","import_path":"example.com/site/articles","directory":"articles"},"schema":{"format_version":1,"app_label":"articles","models":[]}}]}}`
	if string(document) != want {
		t.Fatalf("response = %s", document)
	}
	if bytes.Contains(document, []byte("project_root")) || bytes.Contains(document, []byte(`"root"`)) {
		t.Fatal("execution root leaked onto generation wire")
	}

	spec.Apps[0].Alias = "mutated"
	spec.Apps[0].Schema.AppLabel = "mutated"
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || !response.OK || response.ProjectSpec.Apps[0].Alias != "articles" {
		t.Fatalf("ParseResponse = %+v, %+v, %v", response, failure, failed)
	}
	response.ProjectSpec.Apps[0].Schema.AppLabel = "caller-mutated"
	again, _, failed := ParseResponse(document, true)
	if failed || again.ProjectSpec.Apps[0].Schema.AppLabel != "articles" {
		t.Fatal("parsed response retained prior caller mutation")
	}
}

func TestClosedFailureResponseAndTransportPrecedence(t *testing.T) {
	linked := Failure{Category: CategoryDeclaration, Code: CodeProjectSpecLoadFailed}
	document, err := EncodeResponse(Response{Failure: linked})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"protocol_version":1,"status":"error","error":{"category":"project_generation_declaration_error","code":"project_spec_load_failed"}}`
	if string(document) != want || bytes.Contains(document, []byte("detail")) {
		t.Fatalf("closed response = %s", document)
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || response.OK || response.Failure != linked || failure != (Failure{}) {
		t.Fatalf("ParseResponse = %+v, %+v, %v", response, failure, failed)
	}
	_, failure, failed = ParseResponse(document, false)
	if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}) {
		t.Fatalf("transport precedence = %+v, %v", failure, failed)
	}
}

func TestResponseStrictShapeCanonicalVersionsAndResources(t *testing.T) {
	valid, err := EncodeResponse(Response{OK: true, ProjectSpec: validSpec()})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		wire []byte
		code string
	}{
		{name: "duplicate", wire: bytes.Replace(valid, []byte(`"status":"ok"`), []byte(`"status":"ok","status":"ok"`), 1), code: CodeInvalidResponse},
		{name: "unknown", wire: bytes.Replace(valid, []byte(`"project_spec":`), []byte(`"unknown":0,"project_spec":`), 1), code: CodeInvalidResponse},
		{name: "missing", wire: bytes.Replace(valid, []byte(`,"apps":[{"alias"`), []byte(`,"not_apps":[{"alias"`), 1), code: CodeInvalidResponse},
		{name: "trailing", wire: append(append([]byte(nil), valid...), []byte(`{}`)...), code: CodeInvalidResponse},
		{name: "noncanonical", wire: append([]byte{' '}, valid...), code: CodeInvalidResponse},
		{name: "empty", wire: nil, code: CodeInvalidResponse},
		{name: "schema version", wire: bytes.Replace(valid, []byte(`"format_version":1`), []byte(`"format_version":2`), 1), code: CodeInvalidResponse},
		{name: "protocol version", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":2`), 1), code: CodeProtocolIncompatible},
		{name: "long decoded string", wire: responseWithAlias(strings.Repeat("a", projectspec.MaxSchemaStringBytes+1)), code: CodeInvalidResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, failure, failed := ParseResponse(test.wire, true)
			if !failed || failure != (Failure{Category: CategoryProtocol, Code: test.code}) {
				t.Fatalf("ParseResponse = %+v, %v", failure, failed)
			}
		})
	}
}

func TestAppsAndSchemaWireBudgetBoundaries(t *testing.T) {
	stringSpec := validSpec()
	stringSpec.Project.PackageName = strings.Repeat("p", projectspec.MaxSchemaStringBytes)
	if _, err := EncodeResponse(Response{OK: true, ProjectSpec: stringSpec}); err != nil {
		t.Fatalf("maximum protocol string rejected: %v", err)
	}
	stringSpec.Project.PackageName += "p"
	if _, err := EncodeResponse(Response{OK: true, ProjectSpec: stringSpec}); err == nil {
		t.Fatal("maximum+1 protocol string accepted")
	}

	spec := validSpec()
	spec.Apps = make([]codegen.AppSpec, MaxApps)
	for index := range spec.Apps {
		spec.Apps[index] = codegen.AppSpec{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion}}
	}
	if _, err := EncodeResponse(Response{OK: true, ProjectSpec: spec}); err != nil {
		t.Fatalf("maximum apps rejected: %v", err)
	}
	spec.Apps = append(spec.Apps, codegen.AppSpec{Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion}})
	if _, err := EncodeResponse(Response{OK: true, ProjectSpec: spec}); err == nil {
		t.Fatal("maximum+1 apps accepted")
	}

	budget := wireBudget{fields: projectspec.MaxAggregateFields - 1, nodes: projectspec.MaxAggregateNodes - 1}
	if err := budget.consumeFields(1); err != nil {
		t.Fatalf("maximum fields rejected: %v", err)
	}
	if err := budget.consumeNodes(1); err != nil {
		t.Fatalf("maximum nodes rejected: %v", err)
	}
	if err := budget.consumeFields(1); err == nil {
		t.Fatal("maximum+1 fields accepted")
	}
	if err := budget.consumeNodes(1); err == nil {
		t.Fatal("maximum+1 nodes accepted")
	}
}

func TestJSONDepthAndResponseSizeExactBoundaries(t *testing.T) {
	maximumDepth := strings.Repeat("[", MaxJSONDepth) + "0" + strings.Repeat("]", MaxJSONDepth)
	if err := scanJSONDocument([]byte(maximumDepth), MaxResponseBytes); err != nil {
		t.Fatalf("maximum depth rejected: %v", err)
	}
	overDepth := "[" + maximumDepth + "]"
	if err := scanJSONDocument([]byte(overDepth), MaxResponseBytes); err == nil {
		t.Fatal("maximum+1 depth accepted")
	}
	if err := validateDocumentSize(MaxResponseBytes, MaxResponseBytes); err != nil {
		t.Fatalf("maximum response size rejected: %v", err)
	}
	if err := validateDocumentSize(MaxResponseBytes+1, MaxResponseBytes); err == nil {
		t.Fatal("maximum+1 response size accepted")
	}
	sizer := &wireSizer{size: MaxResponseBytes - 1}
	if !sizer.add(1) || sizer.size != MaxResponseBytes {
		t.Fatalf("producer maximum rejected: %+v", sizer)
	}
	if sizer.add(1) || sizer.size != MaxResponseBytes+1 {
		t.Fatalf("producer maximum+1 accepted: %+v", sizer)
	}
}

func TestSuccessSizePreflightMatchesCanonicalMarshalForAllOptionalShapes(t *testing.T) {
	spec := validSpec()
	spec.Apps[0].Schema.Models = []ir.Model{{
		Name: "Article", GoName: "Article", DBTable: "articles_article",
		Fields: []ir.Field{{
			Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
			Nullable: true, MaxLength: 17,
			Default: &ir.ScalarDefault{Kind: ir.ScalarString, String: "<&\u2028"},
			Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "auth", ModelName: "user"}, Cardinality: ir.RelationManyToOne,
				Reverse: ir.ReverseRelation{Name: "articles", Disabled: true}, OnDelete: ir.DeleteSetNull,
			},
		}},
	}}
	wireSpec := toWireProjectSpec(spec)
	measured, err := measureSuccessDocument(shallowWireProjectSpec(spec))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(successDocument{ProtocolVersion: Version, Status: "ok", ProjectSpec: wireSpec})
	if err != nil {
		t.Fatal(err)
	}
	if measured != len(encoded) {
		t.Fatalf("measured=%d encoded=%d\n%s", measured, len(encoded), encoded)
	}
}

func TestWriteResponseErrorsAndAmbiguousValues(t *testing.T) {
	response := Response{Failure: Failure{Category: CategoryDeclaration, Code: CodeProjectSpecLoadFailed}}
	writerErr := errors.New("writer failed")
	if err := WriteResponse(errorWriter{err: writerErr}, response); !errors.Is(err, writerErr) {
		t.Fatalf("writer error = %v", err)
	}
	if err := WriteResponse(shortWriter{}, response); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer error = %v", err)
	}
	if err := WriteResponse(nil, response); err == nil {
		t.Fatal("nil writer accepted")
	}
	if _, err := EncodeResponse(Response{OK: true, ProjectSpec: validSpec(), Failure: response.Failure}); err == nil {
		t.Fatal("ambiguous response accepted")
	}
	if _, err := EncodeResponse(Response{Failure: Failure{Category: CategoryDeclaration, Code: "raw_detail"}}); err == nil {
		t.Fatal("unknown linked failure accepted")
	}
}

func validSpec() codegen.ProjectSpec {
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/site/project", Directory: "project"},
		Apps: []codegen.AppSpec{{
			Alias:   "articles",
			Package: codegen.PackageSpec{PackageName: "articles", ImportPath: "example.com/site/articles", Directory: "articles"},
			Schema:  ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "articles"},
		}},
	}
}

func responseWithAlias(alias string) []byte {
	spec := validSpec()
	spec.Apps[0].Alias = alias
	document, _ := jsonMarshalSuccessForTest(spec)
	return document
}

func jsonMarshalSuccessForTest(spec codegen.ProjectSpec) ([]byte, error) {
	return json.Marshal(successDocument{ProtocolVersion: Version, Status: "ok", ProjectSpec: toWireProjectSpec(spec)})
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }
