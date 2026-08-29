//go:build darwin || linux

package linked

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectmigration"
	writerprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestRunMakemigrationsSnapshotsDeclarationAndCatalogOnce(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	loads := 0
	var output bytes.Buffer
	report, err := RunMakemigrations(context.Background(), MakemigrationsConfig{
		ProjectRoot:              root,
		MigrationDefinitionRoots: []string{"migrations"},
		LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) {
			loads++
			return makemigrationsLinkedSpec(), nil
		},
	}, []string{writerprotocol.PrivateArgument}, bytes.NewReader(writerprotocol.RequestDocument()), &output)
	if err != nil {
		t.Fatal(err)
	}
	response, failure, failed := writerprotocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (writerprotocol.Failure{}) || !response.OK {
		t.Fatalf("response=%+v failure=%+v failed=%v", response, failure, failed)
	}
	if loads != 1 || report.ProjectSpecLoaderCalls != 1 || report.BuildSnapshotCalls != 1 || report.CandidatesProduced != 1 ||
		report.CommandDispatches != 1 || report.RootsOpened != 1 || report.RunnerResponseWrites != 1 ||
		report.BackendOpenCalls != 0 || report.GoDjDBCalls != 0 {
		t.Fatalf("loads=%d report=%+v", loads, report)
	}
	result := response.Result
	if result.WriterRoot != "migrations" || result.FilesystemCatalog.SourceCount != 0 ||
		result.ProgrammaticCatalog.SourceCount != 0 || len(result.Candidates) != 1 ||
		result.Candidates[0].App != "content" || result.Candidates[0].Name != "0001_initial" {
		t.Fatalf("result=%+v", result)
	}
	loaded, _, err := definition.Load(definition.Source{
		SourceID: filepath.ToSlash(filepath.Join("migrations", "content_0001_initial.godj.json")),
		Document: result.Candidates[0].Document,
	})
	if err != nil || len(loaded.Definitions()) != 1 {
		t.Fatalf("strict candidate load=%+v err=%v", loaded.Definitions(), err)
	}
}

func TestRunMakemigrationsRejectsRequestAndConfigBeforeLoader(t *testing.T) {
	t.Parallel()
	loads := 0
	loader := func(context.Context) (codegen.ProjectSpec, error) {
		loads++
		return makemigrationsLinkedSpec(), nil
	}
	missing := filepath.Join(t.TempDir(), "missing")

	var output bytes.Buffer
	report, err := RunMakemigrations(context.Background(), MakemigrationsConfig{
		ProjectRoot:                missing,
		MigrationDefinitionSources: make([]definition.Source, definition.MaxSources+1),
		LoadProjectSpec:            loader,
	}, []string{writerprotocol.PrivateArgument}, strings.NewReader(`{"protocol_version":2,"command":"migrations.makemigrations"}`), &output)
	if err != nil {
		t.Fatal(err)
	}
	response, failure, failed := writerprotocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (writerprotocol.Failure{}) || response.Failure.Code != writerprotocol.CodeInvalidRequest || loads != 0 || report.CommandDispatches != 0 {
		t.Fatalf("request response=%+v failure=%+v failed=%v loads=%d report=%+v", response, failure, failed, loads, report)
	}

	output.Reset()
	report, err = RunMakemigrations(context.Background(), MakemigrationsConfig{
		ProjectRoot: missing, LoadProjectSpec: loader,
	}, []string{writerprotocol.PrivateArgument}, bytes.NewReader(writerprotocol.RequestDocument()), &output)
	if err != nil {
		t.Fatal(err)
	}
	response, failure, failed = writerprotocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (writerprotocol.Failure{}) || response.Failure.Code != writerprotocol.CodeInvalidProjectSourceConfig || loads != 0 || report.CommandDispatches != 1 {
		t.Fatalf("config response=%+v failure=%+v failed=%v loads=%d report=%+v", response, failure, failed, loads, report)
	}
}

func TestRunMakemigrationsBoundsProgrammaticCatalogBeforeOwnershipClone(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	tests := []struct {
		name    string
		sources []definition.Source
	}{
		{name: "source count", sources: make([]definition.Source, definition.MaxSources+1)},
		{name: "source identity", sources: []definition.Source{{SourceID: strings.Repeat("a", definition.MaxSourceIDBytes+1)}}},
		{name: "document", sources: []definition.Source{{SourceID: "embedded/oversized", Document: make([]byte, definition.MaxDocumentBytes+1)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loads := 0
			var output bytes.Buffer
			report, err := RunMakemigrations(context.Background(), MakemigrationsConfig{
				ProjectRoot:                root,
				MigrationDefinitionRoots:   []string{"migrations"},
				MigrationDefinitionSources: test.sources,
				LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) {
					loads++
					return makemigrationsLinkedSpec(), nil
				},
			}, []string{writerprotocol.PrivateArgument}, bytes.NewReader(writerprotocol.RequestDocument()), &output)
			if err != nil {
				t.Fatal(err)
			}
			response, failure, failed := writerprotocol.ParseResponse(output.Bytes(), true)
			want := writerprotocol.Failure{Category: writerprotocol.CategoryCandidate, Code: writerprotocol.CodeCandidateResourceLimitExceeded}
			if failed || failure != (writerprotocol.Failure{}) || response.Failure != want || loads != 0 ||
				report.ProjectSpecLoaderCalls != 0 || report.RootsOpened != 0 || report.BuildSnapshotCalls != 0 {
				t.Fatalf("response=%+v failure=%+v failed=%v loads=%d report=%+v", response, failure, failed, loads, report)
			}
		})
	}
}

func TestRunMakemigrationsRedactsLoaderAndSnapshotFailures(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	secret := "private declaration path and token"
	var output bytes.Buffer
	report, err := RunMakemigrations(context.Background(), MakemigrationsConfig{
		ProjectRoot: root, MigrationDefinitionRoots: []string{"migrations"},
		LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) { return codegen.ProjectSpec{}, errors.New(secret) },
	}, []string{writerprotocol.PrivateArgument}, bytes.NewReader(writerprotocol.RequestDocument()), &output)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte(secret)) || report.ProjectSpecLoaderCalls != 1 || report.BuildSnapshotCalls != 0 {
		t.Fatalf("loader failure leaked or wrong report: %q %+v", output.Bytes(), report)
	}
	response, _, failed := writerprotocol.ParseResponse(output.Bytes(), true)
	if failed || response.Failure != (writerprotocol.Failure{Category: writerprotocol.CategoryDeclaration, Code: writerprotocol.CodeProjectSpecLoadFailed}) {
		t.Fatalf("loader response=%+v failed=%v", response, failed)
	}

	output.Reset()
	report, err = RunMakemigrations(context.Background(), MakemigrationsConfig{
		ProjectRoot: root, MigrationDefinitionRoots: []string{"migrations"},
		LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) {
			invalid := makemigrationsLinkedSpec()
			invalid.Project.PackageName = ""
			return invalid, nil
		},
	}, []string{writerprotocol.PrivateArgument}, bytes.NewReader(writerprotocol.RequestDocument()), &output)
	if err != nil {
		t.Fatal(err)
	}
	response, _, failed = writerprotocol.ParseResponse(output.Bytes(), true)
	if failed || response.Failure.Code != writerprotocol.CodeProjectSpecLoadFailed || report.BuildSnapshotCalls != 1 || report.CandidatesProduced != 0 {
		t.Fatalf("snapshot response=%+v failed=%v report=%+v", response, failed, report)
	}
}

func TestRunMakemigrationsMapsStrictLoadableInvalidHistoryToLogicalFailure(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	document, err := definition.Encode(definition.Producer{Name: "fixture", Version: "1"}, migrations.Migration{
		App:  "broken",
		Name: "0001_add_without_model",
		Operations: []migrations.Operation{migrations.AddField{
			AppLabel:  "broken",
			ModelName: "missing",
			Field: ir.Field{
				Name:      "title",
				GoName:    "Title",
				Column:    "title",
				Kind:      ir.FieldChar,
				Nullable:  true,
				MaxLength: 200,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	report, err := RunMakemigrations(context.Background(), MakemigrationsConfig{
		ProjectRoot:                root,
		MigrationDefinitionRoots:   []string{"migrations"},
		MigrationDefinitionSources: []definition.Source{{SourceID: "embedded/broken", Document: document}},
		LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) {
			return codegen.ProjectSpec{Project: makemigrationsLinkedSpec().Project}, nil
		},
	}, []string{writerprotocol.PrivateArgument}, bytes.NewReader(writerprotocol.RequestDocument()), &output)
	if err != nil {
		t.Fatal(err)
	}
	response, failure, failed := writerprotocol.ParseResponse(output.Bytes(), true)
	want := writerprotocol.Failure{Category: writerprotocol.CategoryCandidate, Code: writerprotocol.CodeCandidateValidationFailed}
	if failed || failure != (writerprotocol.Failure{}) || response.Failure != want || report.BuildSnapshotCalls != 1 || report.CandidatesProduced != 0 {
		t.Fatalf("response=%+v failure=%+v failed=%v report=%+v", response, failure, failed, report)
	}
}

func TestClassifyMakemigrationsSnapshotFallbacksAreClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code projectmigration.ErrorCode
		want writerprotocol.Failure
	}{
		{
			code: projectmigration.CodeCatalogResourceLimit,
			want: writerprotocol.Failure{Category: writerprotocol.CategoryCandidate, Code: writerprotocol.CodeCandidateResourceLimitExceeded},
		},
		{
			code: projectmigration.CodeInvalidCatalog,
			want: writerprotocol.Failure{Category: writerprotocol.CategoryCandidate, Code: writerprotocol.CodeCandidateValidationFailed},
		},
		{
			code: projectmigration.CodeInvalidPlan,
			want: writerprotocol.Failure{Category: writerprotocol.CategoryDetection, Code: "invalid_generated_plan"},
		},
	}
	for _, test := range tests {
		got, ok := classifyMakemigrationsSnapshotFailure(&projectmigration.Error{Category: projectmigration.CategoryCatalog, Code: test.code})
		if !ok || got != test.want || !writerprotocol.IsLinkedFailure(got) {
			t.Fatalf("code %q = %+v, %v; want %+v", test.code, got, ok, test.want)
		}
	}
}

func TestRunMakemigrationsOwnsConfigSourcesBeforeLoaderMutation(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t, "migrations")
	external := ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "external", Models: []ir.Model{{
		Name: "token", GoName: "Token", Fields: []ir.Field{{Name: "value", GoName: "Value", Kind: ir.FieldChar, MaxLength: 64}},
	}}}
	external, err := ir.Normalize(external)
	if err != nil {
		t.Fatal(err)
	}
	document, err := definition.Encode(definition.Producer{Name: "fixture", Version: "1"}, migrations.Migration{
		App: "external", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "external", Model: external.Models[0]}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sources := []definition.Source{{SourceID: "embedded/external", Document: document}}
	var output bytes.Buffer
	_, err = RunMakemigrations(context.Background(), MakemigrationsConfig{
		ProjectRoot: root, MigrationDefinitionRoots: []string{"migrations"}, MigrationDefinitionSources: sources,
		LoadProjectSpec: func(context.Context) (codegen.ProjectSpec, error) {
			sources[0].SourceID = "mutated"
			sources[0].Document[0] ^= 0xff
			return makemigrationsLinkedSpec(), nil
		},
	}, []string{writerprotocol.PrivateArgument}, bytes.NewReader(writerprotocol.RequestDocument()), &output)
	if err != nil {
		t.Fatal(err)
	}
	response, _, failed := writerprotocol.ParseResponse(output.Bytes(), true)
	if failed || !response.OK || response.Result.ProgrammaticCatalog.SourceCount != 1 || response.Result.ProgrammaticCatalog.Sources[0].SourceID != "embedded/external" {
		t.Fatalf("response=%+v failed=%v", response, failed)
	}
}

func TestRunMakemigrationsCancellationAndBoundaryErrors(t *testing.T) {
	t.Parallel()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if report, err := RunMakemigrations(canceled, MakemigrationsConfig{}, []string{writerprotocol.PrivateArgument}, bytes.NewReader(writerprotocol.RequestDocument()), io.Discard); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(report, MakemigrationsReport{}) {
		t.Fatalf("pre-canceled report=%+v err=%v", report, err)
	}
	if _, err := RunMakemigrations(nil, MakemigrationsConfig{}, []string{writerprotocol.PrivateArgument}, bytes.NewReader(writerprotocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := RunMakemigrations(context.Background(), MakemigrationsConfig{}, []string{"wrong"}, bytes.NewReader(writerprotocol.RequestDocument()), io.Discard); err == nil {
		t.Fatal("invalid private argv accepted")
	}
	if _, err := RunMakemigrations(context.Background(), MakemigrationsConfig{}, []string{writerprotocol.PrivateArgument}, nil, io.Discard); err == nil {
		t.Fatal("nil request reader accepted")
	}
	if _, err := RunMakemigrations(context.Background(), MakemigrationsConfig{}, []string{writerprotocol.PrivateArgument}, bytes.NewReader(writerprotocol.RequestDocument()), nil); err == nil {
		t.Fatal("nil response writer accepted")
	}
}

func makemigrationsLinkedSpec() codegen.ProjectSpec {
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/site/project", Directory: "project"},
		Apps: []codegen.AppSpec{{
			Alias: "content", Package: codegen.PackageSpec{PackageName: "content", ImportPath: "example.com/site/content", Directory: "content"},
			Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "content", Models: []ir.Model{{
				Name: "article", GoName: "Article", Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200}},
			}}},
		}},
	}
}
