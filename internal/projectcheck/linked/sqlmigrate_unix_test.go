//go:build darwin || linux

package linked

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestRunSQLMigrateLoadsCompleteCatalogThenRendersExactlyOnce(t *testing.T) {
	t.Parallel()
	renderer := &linkedSQLRenderer{statements: []string{"CREATE TABLE article (id integer)", "ALTER TABLE article\nADD COLUMN title text"}}
	sources := []definition.Source{
		linkedSQLSource(t, migrations.Migration{
			App:  "blog",
			Name: "0001_article",
			Operations: []migrations.Operation{migrations.CreateModel{
				AppLabel: "blog",
				Model:    linkedSQLModel(),
			}},
		}),
		linkedSQLSource(t, migrations.Migration{
			App:          "blog",
			Name:         "0002_title",
			Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_article"}},
			Operations: []migrations.Operation{
				migrations.AddField{
					AppLabel:  "blog",
					ModelName: "article",
					Field: ir.Field{
						Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 100,
					},
				},
				migrations.AddField{
					AppLabel:  "blog",
					ModelName: "article",
					Field: ir.Field{
						Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean,
					},
				},
			},
		}),
	}
	response, report, document, err := invokeLinkedSQLMigrate(
		t,
		context.Background(),
		SQLMigrateConfig{
			ProjectRoot:                newProjectRoot(t),
			MigrationDefinitionSources: sources,
			MigrationSQLRenderer:       renderer,
		},
		sqlmigrateprotocol.Request{App: "blog", Name: "0002_title"},
		new(bytes.Buffer),
		systemDependencies{},
	)
	if err != nil || !response.OK || !reflect.DeepEqual(response.Result.Statements, renderer.statements) {
		t.Fatalf("RunSQLMigrate = response %+v report %+v wire %q err %v", response, report, document, err)
	}
	if renderer.calls != 1 || renderer.request.App != "blog" || renderer.request.Name != "0002_title" ||
		len(renderer.request.Intent.Operations) != 2 || renderer.request.Intent.Operations[0].Kind != backend.MigrationAddField ||
		renderer.request.Intent.Operations[1].Kind != backend.MigrationAddField {
		t.Fatalf("renderer = calls %d request %+v", renderer.calls, renderer.request)
	}
	if report.LoadCalls != 1 || report.DocumentsReceived != 2 || report.HeadersValidated != 2 ||
		report.OperationsDecoded != 3 || report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 ||
		report.DefinitionSetsPublished != 1 || report.CommandDispatches != 1 || report.RunnerResponseWrites != 1 ||
		report.BackendOpenCalls != 0 || report.BackendCloseCalls != 0 || report.RevisionSessionOpens != 0 ||
		report.AppliedHistoryReads != 0 || report.RevisionLifecycleCalls != 0 || report.GoDjDBCalls != 0 {
		t.Fatalf("linked SQL report = %+v", report)
	}

	renderer.statements[0] = "mutated after publication"
	if bytes.Contains(document, []byte("mutated")) {
		t.Fatalf("wire retained renderer mutation: %q", document)
	}
}

func TestRunSQLMigratePrecedenceTypedNilAndRedaction(t *testing.T) {
	t.Parallel()
	target := linkedSQLSource(t, migrations.Migration{
		App:  "blog",
		Name: "0001_article",
		Operations: []migrations.Operation{migrations.CreateModel{
			AppLabel: "blog",
			Model:    linkedSQLModel(),
		}},
	})

	tests := []struct {
		name     string
		sources  []definition.Source
		request  sqlmigrateprotocol.Request
		renderer backend.MigrationSQLRenderer
		category string
		code     string
	}{
		{
			name: "unrelated invalid source precedes renderer",
			sources: []definition.Source{
				target,
				{SourceID: "secret-source", Document: []byte(`{"password":"do-not-publish"}`)},
			},
			request:  sqlmigrateprotocol.Request{App: "blog", Name: "0001_article"},
			category: sqlmigrateprotocol.CategorySource,
			code:     "invalid_definition_document",
		},
		{
			name:     "exact miss precedes renderer",
			sources:  []definition.Source{target},
			request:  sqlmigrateprotocol.Request{App: "blog", Name: "0001_missing"},
			category: sqlmigrateprotocol.CategoryPlan,
			code:     "target_not_found",
		},
		{
			name:     "nil renderer",
			sources:  []definition.Source{target},
			request:  sqlmigrateprotocol.Request{App: "blog", Name: "0001_article"},
			category: sqlmigrateprotocol.CategorySQLRender,
			code:     sqlmigrateprotocol.CodeRendererUnavailable,
		},
		{
			name:     "typed nil renderer",
			sources:  []definition.Source{target},
			request:  sqlmigrateprotocol.Request{App: "blog", Name: "0001_article"},
			renderer: (*linkedSQLRenderer)(nil),
			category: sqlmigrateprotocol.CategorySQLRender,
			code:     sqlmigrateprotocol.CodeRendererUnavailable,
		},
		{
			name:     "renderer cause redacted",
			sources:  []definition.Source{target},
			request:  sqlmigrateprotocol.Request{App: "blog", Name: "0001_article"},
			renderer: &linkedSQLRenderer{statements: []string{"PARTIAL SECRET SQL"}, err: errors.New("postgres://user:secret@example.invalid")},
			category: sqlmigrateprotocol.CategorySQLRender,
			code:     sqlmigrateprotocol.CodeRenderFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, report, document, err := invokeLinkedSQLMigrate(
				t,
				context.Background(),
				SQLMigrateConfig{
					ProjectRoot:                newProjectRoot(t),
					MigrationDefinitionSources: test.sources,
					MigrationSQLRenderer:       test.renderer,
				},
				test.request,
				new(bytes.Buffer),
				systemDependencies{},
			)
			want := sqlmigrateprotocol.Failure{Category: test.category, Code: test.code}
			if err != nil || response.OK || response.Failure != want || report.RunnerResponseWrites != 1 {
				t.Fatalf("failure = response %+v report %+v err %v, want %+v", response, report, err, want)
			}
			for _, secret := range []string{"do-not-publish", "password", "postgres://", "PARTIAL SECRET SQL", "secret-source"} {
				if bytes.Contains(document, []byte(secret)) {
					t.Fatalf("wire exposed %q: %q", secret, document)
				}
			}
		})
	}
}

func TestRunSQLMigrateStrictRequestCancellationAndSingleWrite(t *testing.T) {
	t.Parallel()
	root := newProjectRoot(t)
	renderer := &linkedSQLRenderer{statements: []string{"SELECT 1"}}
	config := SQLMigrateConfig{
		ProjectRoot:                root,
		MigrationDefinitionSources: []definition.Source{linkedSQLSource(t, migrations.Migration{App: "blog", Name: "zero"})},
		MigrationSQLRenderer:       renderer,
	}

	var malformed bytes.Buffer
	report, err := RunSQLMigrate(
		context.Background(), config,
		[]string{sqlmigrateprotocol.PrivateArgument},
		strings.NewReader(`{"protocol_version":2,"command":"migrations.sql","app":"blog","name":"zero"}`),
		&malformed,
	)
	response, failure, failed := sqlmigrateprotocol.ParseResponse(malformed.Bytes(), true)
	if err != nil || failed || failure != (sqlmigrateprotocol.Failure{}) || response.Failure.Code != sqlmigrateprotocol.CodeProtocolIncompatible ||
		report.CommandDispatches != 0 || report.LoadCalls != 0 || report.RunnerResponseWrites != 1 || renderer.calls != 0 {
		t.Fatalf("malformed request = response %+v failure %+v failed %v report %+v calls %d err %v", response, failure, failed, report, renderer.calls, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	document, encodeErr := sqlmigrateprotocol.EncodeRequest(sqlmigrateprotocol.Request{App: "blog", Name: "zero"})
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	var canceled bytes.Buffer
	report, err = RunSQLMigrate(
		ctx, config,
		[]string{sqlmigrateprotocol.PrivateArgument},
		bytes.NewReader(document),
		&canceled,
	)
	if !errors.Is(err, context.Canceled) || canceled.Len() != 0 || report.CommandDispatches != 0 || report.LoadCalls != 0 || report.RunnerResponseWrites != 0 {
		t.Fatalf("pre-canceled = report %+v output %q err %v", report, canceled.String(), err)
	}

	report, err = RunSQLMigrate(
		context.Background(), config,
		[]string{sqlmigrateprotocol.PrivateArgument},
		bytes.NewReader(document),
		linkedSQLShortWriter{},
	)
	if !errors.Is(err, io.ErrShortWrite) || report.RunnerResponseWrites != 1 {
		t.Fatalf("short write = report %+v err %v", report, err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	var responseBoundary bytes.Buffer
	report, err = runSQLMigrate(
		ctx, config,
		[]string{sqlmigrateprotocol.PrivateArgument},
		bytes.NewReader(document),
		&responseBoundary,
		systemDependencies{beforeResponseWrite: cancel},
	)
	if !errors.Is(err, context.Canceled) || responseBoundary.Len() != 0 || report.RunnerResponseWrites != 0 {
		t.Fatalf("response cancellation = report %+v output %q err %v", report, responseBoundary.String(), err)
	}
}

func TestClassifySQLMigrateFailureDoesNotTraverseArbitraryWrappers(t *testing.T) {
	t.Parallel()
	wrapped := errors.Join(&migrations.MigrationSQLError{
		Category: migrations.CategorySQLRender,
		Code:     migrations.CodeRenderFailed,
	}, errors.New("secret cause"))
	if failure, ok := classifySQLMigrateFailure(wrapped); ok || failure != (sqlmigrateprotocol.Failure{}) {
		t.Fatalf("wrapped error classified = %+v, %v", failure, ok)
	}
}

func invokeLinkedSQLMigrate(
	t *testing.T,
	ctx context.Context,
	config SQLMigrateConfig,
	request sqlmigrateprotocol.Request,
	writer io.Writer,
	dependencies systemDependencies,
) (sqlmigrateprotocol.Response, Report, []byte, error) {
	t.Helper()
	document, err := sqlmigrateprotocol.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if writer == nil {
		writer = &output
	} else if buffer, ok := writer.(*bytes.Buffer); ok {
		output = *buffer
		writer = &output
	}
	report, runErr := runSQLMigrate(
		ctx,
		config,
		[]string{sqlmigrateprotocol.PrivateArgument},
		bytes.NewReader(document),
		writer,
		dependencies,
	)
	if runErr != nil {
		return sqlmigrateprotocol.Response{}, report, output.Bytes(), runErr
	}
	if output.Len() == 0 {
		return sqlmigrateprotocol.Response{}, report, nil, errors.New("missing SQL response")
	}
	response, failure, failed := sqlmigrateprotocol.ParseResponse(output.Bytes(), true)
	if failed {
		return sqlmigrateprotocol.Response{}, report, output.Bytes(), errors.New(failure.Category + "/" + failure.Code)
	}
	return response, report, append([]byte(nil), output.Bytes()...), nil
}

func linkedSQLSource(t *testing.T, migration migrations.Migration) definition.Source {
	t.Helper()
	document, err := definition.Encode(definition.Producer{Name: "linked-sql-test", Version: "1"}, migration)
	if err != nil {
		t.Fatalf("encode migration: %v", err)
	}
	return definition.Source{SourceID: migration.App + "/" + migration.Name + ".godj.json", Document: document}
}

func linkedSQLModel() ir.Model {
	return ir.Model{
		Name: "article", GoName: "Article", DBTable: "blog_article",
		Fields: []ir.Field{{
			Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true,
		}},
	}
}

type linkedSQLRenderer struct {
	calls      int
	request    backend.ForwardMigrationSQLRequest
	statements []string
	err        error
}

func (renderer *linkedSQLRenderer) RenderForwardMigrationSQL(
	_ context.Context,
	request backend.ForwardMigrationSQLRequest,
) ([]string, error) {
	renderer.calls++
	renderer.request = request
	return renderer.statements, renderer.err
}

type linkedSQLShortWriter struct{}

func (linkedSQLShortWriter) Write(payload []byte) (int, error) { return len(payload) - 1, nil }
