package linked

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate/protocol"
	"github.com/progresshans/godj/schema/ir"
)

func TestRunValidatesRequestThenLoadsExactlyOnce(t *testing.T) {
	loads := 0
	var output bytes.Buffer
	report, err := Run(context.Background(), func(context.Context) (codegen.ProjectSpec, error) {
		loads++
		return linkedSpec(), nil
	}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), &output)
	if err != nil {
		t.Fatal(err)
	}
	if loads != 1 || report != (Report{CommandDispatches: 1, LoaderCalls: 1, RunnerResponseWrites: 1}) {
		t.Fatalf("loads=%d report=%+v", loads, report)
	}
	response, failure, failed := protocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (protocol.Failure{}) || !response.OK || response.ProjectSpec.Apps[0].Alias != "articles" {
		t.Fatalf("response=%+v failure=%+v failed=%v", response, failure, failed)
	}
}

func TestRunRejectsRequestBeforeLoader(t *testing.T) {
	loads := 0
	var output bytes.Buffer
	report, err := Run(context.Background(), func(context.Context) (codegen.ProjectSpec, error) {
		loads++
		return linkedSpec(), nil
	}, []string{protocol.PrivateArgument}, strings.NewReader(`{"protocol_version":1,"command":"other"}`), &output)
	if err != nil {
		t.Fatal(err)
	}
	if loads != 0 || report != (Report{RunnerResponseWrites: 1}) {
		t.Fatalf("loads=%d report=%+v", loads, report)
	}
	response, failure, failed := protocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (protocol.Failure{}) || response.Failure.Code != protocol.CodeInvalidRequest {
		t.Fatalf("response=%+v failure=%+v failed=%v", response, failure, failed)
	}
}

func TestRunClosesLoaderFailureWithoutDetail(t *testing.T) {
	secret := "private source path and declaration detail"
	var output bytes.Buffer
	report, err := Run(context.Background(), func(context.Context) (codegen.ProjectSpec, error) {
		return codegen.ProjectSpec{}, errors.New(secret)
	}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), &output)
	if err != nil {
		t.Fatal(err)
	}
	if report != (Report{CommandDispatches: 1, LoaderCalls: 1, RunnerResponseWrites: 1}) {
		t.Fatalf("report=%+v", report)
	}
	if bytes.Contains(output.Bytes(), []byte(secret)) {
		t.Fatal("loader detail leaked onto wire")
	}
	response, failure, failed := protocol.ParseResponse(output.Bytes(), true)
	want := protocol.Failure{Category: protocol.CategoryDeclaration, Code: protocol.CodeProjectSpecLoadFailed}
	if failed || failure != (protocol.Failure{}) || response.Failure != want {
		t.Fatalf("response=%+v failure=%+v failed=%v", response, failure, failed)
	}
}

func TestRunClosesInvalidLoadedProjectSpecWithoutEncodingDetail(t *testing.T) {
	var output bytes.Buffer
	report, err := Run(context.Background(), func(context.Context) (codegen.ProjectSpec, error) {
		invalid := linkedSpec()
		invalid.Project.PackageName = ""
		return invalid, nil
	}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), &output)
	if err != nil {
		t.Fatalf("Run(invalid loaded spec) error = %v", err)
	}
	if report != (Report{CommandDispatches: 1, LoaderCalls: 1, RunnerResponseWrites: 1}) {
		t.Fatalf("report=%+v", report)
	}
	response, failure, failed := protocol.ParseResponse(output.Bytes(), true)
	want := protocol.Failure{Category: protocol.CategoryDeclaration, Code: protocol.CodeProjectSpecLoadFailed}
	if failed || failure != (protocol.Failure{}) || response.Failure != want || response.OK {
		t.Fatalf("response=%+v failure=%+v failed=%v", response, failure, failed)
	}
}

func TestRunCancellationRemainsGoError(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if report, err := Run(canceled, func(context.Context) (codegen.ProjectSpec, error) {
		t.Fatal("loader called for canceled request")
		return codegen.ProjectSpec{}, nil
	}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), io.Discard); !errors.Is(err, context.Canceled) || report != (Report{}) {
		t.Fatalf("pre-canceled = %+v, %v", report, err)
	}

	during, cancelDuring := context.WithCancel(context.Background())
	var output bytes.Buffer
	report, err := Run(during, func(context.Context) (codegen.ProjectSpec, error) {
		cancelDuring()
		return linkedSpec(), nil
	}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), &output)
	if !errors.Is(err, context.Canceled) || report != (Report{CommandDispatches: 1, LoaderCalls: 1}) || output.Len() != 0 {
		t.Fatalf("loader cancellation = %+v, %v, bytes=%d", report, err, output.Len())
	}
}

func TestRunIOAndBoundaryErrorsRemainGoErrors(t *testing.T) {
	readerErr := errors.New("read failed")
	if report, err := Run(context.Background(), nil, []string{protocol.PrivateArgument}, errorReader{err: readerErr}, io.Discard); !errors.Is(err, readerErr) || report != (Report{}) {
		t.Fatalf("reader failure = %+v, %v", report, err)
	}

	writerErr := errors.New("write failed")
	report, err := Run(context.Background(), func(context.Context) (codegen.ProjectSpec, error) {
		return linkedSpec(), nil
	}, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), errorWriter{err: writerErr})
	if !errors.Is(err, writerErr) || report != (Report{CommandDispatches: 1, LoaderCalls: 1, RunnerResponseWrites: 1}) {
		t.Fatalf("writer failure = %+v, %v", report, err)
	}

	if _, err := Run(nil, nil, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := Run(context.Background(), nil, []string{protocol.PrivateArgument}, nil, io.Discard); err == nil {
		t.Fatal("nil stdin accepted")
	}
	if _, err := Run(context.Background(), nil, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), nil); err == nil {
		t.Fatal("nil stdout accepted")
	}
	if _, err := Run(context.Background(), nil, []string{"wrong"}, bytes.NewReader(protocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("invalid argv accepted")
	}
}

func TestNilLoaderIsClosedFailure(t *testing.T) {
	var output bytes.Buffer
	report, err := Run(context.Background(), nil, []string{protocol.PrivateArgument}, bytes.NewReader(protocol.RequestDocument()), &output)
	if err != nil {
		t.Fatal(err)
	}
	if report != (Report{CommandDispatches: 1, RunnerResponseWrites: 1}) {
		t.Fatalf("report=%+v", report)
	}
	response, failure, failed := protocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (protocol.Failure{}) || response.Failure.Code != protocol.CodeProjectSpecLoadFailed {
		t.Fatalf("response=%+v failure=%+v failed=%v", response, failure, failed)
	}
}

func linkedSpec() codegen.ProjectSpec {
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/site/project", Directory: "project"},
		Apps: []codegen.AppSpec{{
			Alias:   "articles",
			Package: codegen.PackageSpec{PackageName: "articles", ImportPath: "example.com/site/articles", Directory: "articles"},
			Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "articles", Models: []ir.Model{{
				Name: "article", GoName: "Article", Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200}},
			}}},
		}},
	}
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }
