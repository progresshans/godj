//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const (
	migrationSQLRenderingProbeRendererCauseCanary = "mig137-renderer-cause-4d57c91e"
	migrationSQLRenderingProbePartialSQLCanary    = "mig137-partial-sql-8fa324b6"
	migrationSQLRenderingProbeDefinitionCanary    = "mig137-definition-source-6bc1e205"
	migrationSQLRenderingProbeCredentialCanary    = "mig137-credential-value-2e93ad74"
	migrationSQLRenderingProbeChildStderrCanary   = "mig137-child-stderr-a75f6083"
)

// migrationSQLRenderingActualResourceCase records values observed at an
// actual trust boundary. ResourceLimitAccepted is deliberately separate from
// Succeeded: a malformed private response exactly at its byte cap passes the
// resource gate but still fails protocol validation.
type migrationSQLRenderingActualResourceCase struct {
	Case                   string
	Limit                  int
	Observed               int
	ResourceLimitAccepted  bool
	Succeeded              bool
	Category               string
	Code                   string
	RendererCalls          int
	RequestOperations      int
	MalformedPayload       bool
	ResourceBeforeSemantic bool
}

type migrationSQLRenderingProbeRenderer struct {
	statements        []string
	err               error
	calls             int
	requestOperations int
}

func (renderer *migrationSQLRenderingProbeRenderer) RenderForwardMigrationSQL(
	_ context.Context,
	request migrationbackend.ForwardMigrationSQLRequest,
) ([]string, error) {
	renderer.calls++
	renderer.requestOperations = len(request.Intent.Operations)
	return append([]string(nil), renderer.statements...), renderer.err
}

// migrationSQLRenderingProbeRootResources exercises the exported root rather
// than its private validator. The statement-count target itself is loader
// valid and contains the maximum public definition operation count.
func migrationSQLRenderingProbeRootResources(ctx context.Context) ([]migrationSQLRenderingActualResourceCase, error) {
	if ctx == nil {
		return nil, errors.New("migration SQL resource probe context is nil")
	}
	statementCases, err := migrationSQLRenderingProbeRootStatementCount(ctx)
	if err != nil {
		return nil, err
	}
	aggregateCases, err := migrationSQLRenderingProbeRootAggregateBytes(ctx)
	if err != nil {
		return nil, err
	}
	return append(statementCases, aggregateCases...), nil
}

func migrationSQLRenderingProbeRootStatementCount(ctx context.Context) ([]migrationSQLRenderingActualResourceCase, error) {
	limit := sqlmigrateprotocol.MaxStatements
	if definition.MaxOperationsPerMigration != limit {
		return nil, fmt.Errorf(
			"migration SQL statement cap %d differs from loader operation cap %d",
			limit,
			definition.MaxOperationsPerMigration,
		)
	}

	target := migrations.MigrationKey{App: "resource_probe", Name: "0001_statement_count"}
	operations := make([]migrations.Operation, limit)
	for index := range operations {
		suffix := fmt.Sprintf("%04d", index)
		operations[index] = migrations.CreateModel{
			AppLabel: target.App,
			Model: ir.Model{
				Name:    "model_" + suffix,
				GoName:  "Model" + suffix,
				DBTable: "resource_probe_model_" + suffix,
				Fields: []ir.Field{{
					Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true,
				}},
			},
		}
	}
	sources, err := migrationSQLRenderingSources([]migrations.Migration{{
		App: target.App, Name: target.Name, Operations: operations,
	}})
	if err != nil {
		return nil, fmt.Errorf("encode migration SQL statement-count fixture: %w", err)
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		return nil, fmt.Errorf("load migration SQL statement-count fixture: %w", err)
	}

	exactStatements := make([]string, limit)
	for index := range exactStatements {
		exactStatements[index] = "X"
	}
	exactRenderer := &migrationSQLRenderingProbeRenderer{statements: exactStatements}
	exactResult, err := migrations.RenderMigrationSQL(ctx, loaded, target, exactRenderer)
	if err != nil || len(exactResult) != limit || exactRenderer.calls != 1 || exactRenderer.requestOperations != limit {
		return nil, fmt.Errorf(
			"root exact statement-count probe = result:%d calls:%d operations:%d error:%w",
			len(exactResult), exactRenderer.calls, exactRenderer.requestOperations, err,
		)
	}
	for index := range exactResult {
		if exactResult[index] != "X" {
			return nil, fmt.Errorf("root exact statement-count probe body %d changed", index)
		}
	}

	oneOverStatements := make([]string, limit+1)
	for index := range oneOverStatements {
		oneOverStatements[index] = "X"
	}
	// The extra statement is also semantically malformed. A resource failure
	// therefore proves that count scanning precedes statement-shape scanning.
	oneOverStatements[len(oneOverStatements)-1] = ";"
	oneOverRenderer := &migrationSQLRenderingProbeRenderer{statements: oneOverStatements}
	oneOverResult, oneOverErr := migrations.RenderMigrationSQL(ctx, loaded, target, oneOverRenderer)
	category, code, classifyErr := migrationSQLRenderingProbeRootFailure(oneOverErr)
	if classifyErr != nil {
		return nil, classifyErr
	}
	if oneOverResult != nil || category != string(migrations.CategorySQLResource) ||
		code != string(migrations.CodeRenderedSQLResourceLimit) || oneOverRenderer.calls != 1 ||
		oneOverRenderer.requestOperations != limit {
		return nil, fmt.Errorf(
			"root one-over statement-count probe = result:%d category:%s code:%s calls:%d operations:%d",
			len(oneOverResult), category, code, oneOverRenderer.calls, oneOverRenderer.requestOperations,
		)
	}

	return []migrationSQLRenderingActualResourceCase{
		{
			Case: "statement_count_exact_limit", Limit: limit, Observed: len(exactResult),
			ResourceLimitAccepted: true, Succeeded: true, RendererCalls: exactRenderer.calls,
			RequestOperations: exactRenderer.requestOperations,
		},
		{
			Case: "statement_count_one_over", Limit: limit, Observed: len(oneOverStatements),
			ResourceLimitAccepted: false, Category: category, Code: code, RendererCalls: oneOverRenderer.calls,
			RequestOperations: oneOverRenderer.requestOperations, MalformedPayload: true, ResourceBeforeSemantic: true,
		},
	}, nil
}

func migrationSQLRenderingProbeRootAggregateBytes(ctx context.Context) ([]migrationSQLRenderingActualResourceCase, error) {
	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return nil, fmt.Errorf("create migration SQL aggregate fixture: %w", err)
	}
	limit := sqlmigrateprotocol.MaxStatementBodyBytes
	if limit < 2 {
		return nil, fmt.Errorf("migration SQL aggregate cap %d is too small for two-operation probe", limit)
	}

	// One backing string supports both probes. The one-over call runs first and
	// fails during resource scanning, so it cannot retain a cloned 16 MiB body.
	aggregateBody := strings.Repeat("X", limit)
	oneOverRenderer := &migrationSQLRenderingProbeRenderer{statements: []string{aggregateBody, ";"}}
	oneOverResult, oneOverErr := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, oneOverRenderer)
	category, code, classifyErr := migrationSQLRenderingProbeRootFailure(oneOverErr)
	if classifyErr != nil {
		return nil, classifyErr
	}
	if oneOverResult != nil || category != string(migrations.CategorySQLResource) ||
		code != string(migrations.CodeRenderedSQLResourceLimit) || oneOverRenderer.calls != 1 ||
		oneOverRenderer.requestOperations != len(oneOverRenderer.statements) {
		return nil, fmt.Errorf(
			"root one-over aggregate probe = result:%d category:%s code:%s calls:%d operations:%d",
			len(oneOverResult), category, code, oneOverRenderer.calls, oneOverRenderer.requestOperations,
		)
	}

	exactRenderer := &migrationSQLRenderingProbeRenderer{statements: []string{aggregateBody[:limit-1], "X"}}
	exactResult, err := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, exactRenderer)
	if err != nil || len(exactResult) != len(exactRenderer.statements) || exactRenderer.calls != 1 ||
		exactRenderer.requestOperations != len(exactRenderer.statements) ||
		migrationSQLRenderingProbeBodyBytes(exactResult) != limit {
		return nil, fmt.Errorf(
			"root exact aggregate probe = result:%d bytes:%d calls:%d operations:%d error:%w",
			len(exactResult), migrationSQLRenderingProbeBodyBytes(exactResult), exactRenderer.calls,
			exactRenderer.requestOperations, err,
		)
	}

	return []migrationSQLRenderingActualResourceCase{
		{
			Case: "aggregate_body_bytes_exact_limit", Limit: limit, Observed: migrationSQLRenderingProbeBodyBytes(exactResult),
			ResourceLimitAccepted: true, Succeeded: true, RendererCalls: exactRenderer.calls,
			RequestOperations: exactRenderer.requestOperations,
		},
		{
			Case: "aggregate_body_bytes_one_over", Limit: limit, Observed: limit + 1,
			ResourceLimitAccepted: false, Category: category, Code: code, RendererCalls: oneOverRenderer.calls,
			RequestOperations: oneOverRenderer.requestOperations, MalformedPayload: true, ResourceBeforeSemantic: true,
		},
	}, nil
}

func migrationSQLRenderingProbeBodyBytes(statements []string) int {
	total := 0
	for index := range statements {
		total += len(statements[index])
	}
	return total
}

func migrationSQLRenderingProbeRootFailure(err error) (string, string, error) {
	var sqlError *migrations.MigrationSQLError
	if !errors.As(err, &sqlError) || sqlError == nil {
		return "", "", fmt.Errorf("migration SQL resource probe returned non-SQL failure: %v", err)
	}
	return string(sqlError.Category), string(sqlError.Code), nil
}

// migrationSQLRenderingProbePrivateResponseResources keeps the private-wire
// exact/one-over observation independent of the root probes. A single
// MaxResponseBytes+1 buffer bounds retained memory for both calls.
func migrationSQLRenderingProbePrivateResponseResources() ([]migrationSQLRenderingActualResourceCase, error) {
	limit := sqlmigrateprotocol.MaxResponseBytes
	document := make([]byte, limit+1)

	_, exactFailure, exactFailed := sqlmigrateprotocol.ParseResponse(document[:limit], true)
	if !exactFailed || exactFailure != (sqlmigrateprotocol.Failure{
		Category: sqlmigrateprotocol.CategoryProtocol,
		Code:     sqlmigrateprotocol.CodeInvalidResponse,
	}) {
		return nil, fmt.Errorf("private response exact-cap probe = failure:%+v failed:%t", exactFailure, exactFailed)
	}
	_, oneOverFailure, oneOverFailed := sqlmigrateprotocol.ParseResponse(document, true)
	if !oneOverFailed || oneOverFailure != (sqlmigrateprotocol.Failure{
		Category: sqlmigrateprotocol.CategorySQLResource,
		Code:     sqlmigrateprotocol.CodeRenderedSQLResourceLimit,
	}) {
		return nil, fmt.Errorf("private response one-over probe = failure:%+v failed:%t", oneOverFailure, oneOverFailed)
	}

	return []migrationSQLRenderingActualResourceCase{
		{
			Case: "private_response_exact_limit", Limit: limit, Observed: limit,
			ResourceLimitAccepted: true, Category: exactFailure.Category, Code: exactFailure.Code,
			MalformedPayload: true,
		},
		{
			Case: "private_response_one_over", Limit: limit, Observed: len(document),
			ResourceLimitAccepted: false, Category: oneOverFailure.Category, Code: oneOverFailure.Code,
			MalformedPayload: true, ResourceBeforeSemantic: true,
		},
	}, nil
}

type migrationSQLRenderingPublicationProbe struct {
	PublishedOccurrences        map[string]int
	LogicalWriterCalls          map[string]int
	LinkedRendererFailure       sqlmigrateprotocol.Failure
	LinkedDefinitionFailure     sqlmigrateprotocol.Failure
	GlobalFailure               sqlmigrateprotocol.Failure
	LinkedRendererCalls         int
	GlobalBuildCalls            int
	GlobalRunnerCalls           int
	GlobalDirectChildReaps      int
	GlobalRunnerStderrBytes     int
	GlobalCleanupAttempts       int
	GlobalCleanupFailures       int
	GlobalResidualTemp          int
	GlobalRawDiagnosticsDropped bool
	CredentialObservedByChild   bool
	ValidatedBeforeSQLWrite     bool
}

type migrationSQLRenderingProbeCountingWriter struct {
	calls  int
	output bytes.Buffer
}

func (writer *migrationSQLRenderingProbeCountingWriter) Write(document []byte) (int, error) {
	writer.calls++
	return writer.output.Write(document)
}

// migrationSQLRenderingProbeRedactionAndPublication uses the public linked
// runner and the public global command kernel. The global half builds and
// reaps a tiny repository-external child that observes the credential canary,
// writes a distinct stderr canary, and returns a semantically malformed private
// success response. Thus zero public SQL writer calls are evidence that private
// response validation completed before terminal SQL publication.
func migrationSQLRenderingProbeRedactionAndPublication(
	ctx context.Context,
) (evidence migrationSQLRenderingPublicationProbe, resultErr error) {
	if ctx == nil {
		return evidence, errors.New("migration SQL publication probe context is nil")
	}
	projectFixture, err := newMigrationCommandProject()
	if err != nil {
		return evidence, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, projectFixture.close())
	}()
	linkedProjectRoot, err := filepath.EvalSymlinks(projectFixture.root)
	if err == nil {
		linkedProjectRoot, err = filepath.Abs(linkedProjectRoot)
	}
	if err != nil {
		return evidence, fmt.Errorf("resolve migration SQL publication probe project root: %w", err)
	}

	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return evidence, err
	}
	requestDocument, err := sqlmigrateprotocol.EncodeRequest(sqlmigrateprotocol.Request{
		App: fixture.target.App, Name: fixture.target.Name,
	})
	if err != nil {
		return evidence, err
	}

	renderer := &migrationSQLRenderingProbeRenderer{
		statements: []string{migrationSQLRenderingProbePartialSQLCanary, "SELECT 2"},
		err:        errors.New(migrationSQLRenderingProbeRendererCauseCanary),
	}
	rendererWriter := &migrationSQLRenderingProbeCountingWriter{}
	rendererReport, err := linked.RunSQLMigrate(
		ctx,
		linked.SQLMigrateConfig{
			ProjectRoot: linkedProjectRoot, MigrationDefinitionSources: fixture.sources,
			MigrationSQLRenderer: renderer,
		},
		[]string{sqlmigrateprotocol.PrivateArgument},
		bytes.NewReader(requestDocument),
		rendererWriter,
	)
	if err != nil {
		return evidence, fmt.Errorf("linked renderer redaction probe: %w", err)
	}
	rendererResponse, rendererParseFailure, rendererParseFailed := sqlmigrateprotocol.ParseResponse(rendererWriter.output.Bytes(), true)
	wantRendererFailure := sqlmigrateprotocol.Failure{
		Category: string(migrations.CategorySQLRender), Code: string(migrations.CodeRenderFailed),
	}
	if rendererParseFailed || rendererParseFailure != (sqlmigrateprotocol.Failure{}) || rendererResponse.OK ||
		rendererResponse.Failure != wantRendererFailure || rendererReport.RunnerResponseWrites != 1 ||
		rendererWriter.calls != 1 || renderer.calls != 1 {
		return evidence, fmt.Errorf(
			"linked renderer redaction probe = response:%+v parse:%+v/%t report:%+v writer:%d renderer:%d",
			rendererResponse, rendererParseFailure, rendererParseFailed, rendererReport, rendererWriter.calls, renderer.calls,
		)
	}

	malformedSources := append([]definition.Source(nil), fixture.sources...)
	malformedSources = append(malformedSources, definition.Source{
		SourceID: "probe/malformed.godj.json",
		Document: []byte(`{"` + migrationSQLRenderingProbeDefinitionCanary + `":`),
	})
	definitionRenderer := &migrationSQLRenderingProbeRenderer{statements: []string{"SELECT 1", "SELECT 2"}}
	definitionWriter := &migrationSQLRenderingProbeCountingWriter{}
	definitionReport, err := linked.RunSQLMigrate(
		ctx,
		linked.SQLMigrateConfig{
			ProjectRoot: linkedProjectRoot, MigrationDefinitionSources: malformedSources,
			MigrationSQLRenderer: definitionRenderer,
		},
		[]string{sqlmigrateprotocol.PrivateArgument},
		bytes.NewReader(requestDocument),
		definitionWriter,
	)
	if err != nil {
		return evidence, fmt.Errorf("linked definition redaction probe: %w", err)
	}
	definitionResponse, definitionParseFailure, definitionParseFailed := sqlmigrateprotocol.ParseResponse(definitionWriter.output.Bytes(), true)
	if definitionParseFailed || definitionParseFailure != (sqlmigrateprotocol.Failure{}) || definitionResponse.OK ||
		definitionResponse.Failure == (sqlmigrateprotocol.Failure{}) ||
		definitionResponse.Failure.Category != sqlmigrateprotocol.CategorySource ||
		!sqlmigrateprotocol.IsLinkedFailure(definitionResponse.Failure) || definitionReport.RunnerResponseWrites != 1 ||
		definitionWriter.calls != 1 || definitionRenderer.calls != 0 {
		return evidence, fmt.Errorf(
			"linked definition redaction probe = response:%+v parse:%+v/%t report:%+v writer:%d renderer:%d",
			definitionResponse, definitionParseFailure, definitionParseFailed, definitionReport,
			definitionWriter.calls, definitionRenderer.calls,
		)
	}

	if err := migrationSQLRenderingPrepareGlobalProbeProject(projectFixture); err != nil {
		return evidence, err
	}
	validPrivateWire, err := sqlmigrateprotocol.EncodeResponse(sqlmigrateprotocol.Response{
		OK: true, Result: sqlmigrateprotocol.Result{Statements: []string{migrationSQLRenderingProbePartialSQLCanary}},
	})
	if err != nil {
		return evidence, err
	}
	needle := []byte(migrationSQLRenderingProbePartialSQLCanary)
	malformedPrivateWire := bytes.Replace(validPrivateWire, needle, append(append([]byte(nil), needle...), ';'), 1)
	if bytes.Equal(validPrivateWire, malformedPrivateWire) {
		return evidence, errors.New("migration SQL global probe did not construct malformed private response")
	}

	pathValue := os.Getenv("PATH")
	if pathValue == "" {
		return evidence, errors.New("migration SQL global probe PATH is empty")
	}
	environment := []string{
		"HOME=" + projectFixture.home,
		"PATH=" + pathValue,
		"TMPDIR=" + projectFixture.workspace,
		"GOWORK=off",
		"GOTOOLCHAIN=local",
		"GOENV=off",
		"GOFLAGS=",
		"GOPROXY=off",
		"DATABASE_URL=" + migrationSQLRenderingProbeCredentialCanary,
		"GODJ_MIG137_EXPECTED_CREDENTIAL=" + migrationSQLRenderingProbeCredentialCanary,
		"GODJ_MIG137_PRIVATE_RESPONSE=" + base64.StdEncoding.EncodeToString(malformedPrivateWire),
		"GODJ_MIG137_CHILD_STDERR=" + migrationSQLRenderingProbeChildStderrCanary,
	}
	globalStdout := &migrationSQLRenderingProbeCountingWriter{}
	globalStderr := &migrationSQLRenderingProbeCountingWriter{}
	globalReport := productcheck.RunSQLMigrate(productcheck.SQLMigrateInvocation{
		Context:     ctx,
		CWD:         projectFixture.root,
		Args:        []string{"sqlmigrate", fixture.target.App, fixture.target.Name},
		Environment: environment,
		Stdout:      globalStdout,
		Stderr:      globalStderr,
	})
	wantGlobalFailure := sqlmigrateprotocol.Failure{
		Category: sqlmigrateprotocol.CategoryProtocol, Code: sqlmigrateprotocol.CodeInvalidResponse,
	}
	if globalReport.ExitCode != 3 || !globalReport.HasSQLMigrateFailure || globalReport.HasSQLMigrateResult ||
		globalReport.SQLMigrateFailure != wantGlobalFailure || globalReport.BuildCalls != 1 || globalReport.RunnerCalls != 1 ||
		globalReport.DirectChildReaps != 2 || globalReport.RunnerResponseWrites != 1 ||
		globalReport.RunnerStderrRetainedBytes != len(migrationSQLRenderingProbeChildStderrCanary) ||
		globalReport.TempCleanupAttempts != 1 || globalReport.CleanupFailed != 0 || globalReport.ResidualTemp != 0 ||
		globalReport.UserStdoutWrites != 0 || globalReport.UserStderrWrites != 1 ||
		globalStdout.calls != 0 || globalStderr.calls != 1 || !globalReport.RawDiagnosticsDiscarded {
		return evidence, fmt.Errorf(
			"global validation-before-write probe = report:%+v stdout:%d/%q stderr:%d/%q",
			globalReport, globalStdout.calls, globalStdout.output.String(), globalStderr.calls, globalStderr.output.String(),
		)
	}
	if err := migrationCommandAssertActualDirectoryEmpty(projectFixture.workspace); err != nil {
		return evidence, err
	}

	published := bytes.Join([][]byte{
		rendererWriter.output.Bytes(), definitionWriter.output.Bytes(),
		globalStdout.output.Bytes(), globalStderr.output.Bytes(),
	}, nil)
	canaries := map[string]string{
		"renderer_cause":    migrationSQLRenderingProbeRendererCauseCanary,
		"partial_sql":       migrationSQLRenderingProbePartialSQLCanary,
		"definition_source": migrationSQLRenderingProbeDefinitionCanary,
		"credential_value":  migrationSQLRenderingProbeCredentialCanary,
		"child_stderr":      migrationSQLRenderingProbeChildStderrCanary,
	}
	occurrences := make(map[string]int, len(canaries))
	for name, canary := range canaries {
		occurrences[name] = bytes.Count(published, []byte(canary))
		if occurrences[name] != 0 {
			return evidence, fmt.Errorf("migration SQL publication retained %s canary", name)
		}
	}

	return migrationSQLRenderingPublicationProbe{
		PublishedOccurrences: occurrences,
		LogicalWriterCalls: map[string]int{
			"linked_renderer_private":   rendererWriter.calls,
			"linked_definition_private": definitionWriter.calls,
			"global_sql_stdout":         globalStdout.calls,
			"global_diagnostic_stderr":  globalStderr.calls,
		},
		LinkedRendererFailure:       rendererResponse.Failure,
		LinkedDefinitionFailure:     definitionResponse.Failure,
		GlobalFailure:               globalReport.SQLMigrateFailure,
		LinkedRendererCalls:         renderer.calls,
		GlobalBuildCalls:            globalReport.BuildCalls,
		GlobalRunnerCalls:           globalReport.RunnerCalls,
		GlobalDirectChildReaps:      globalReport.DirectChildReaps,
		GlobalRunnerStderrBytes:     globalReport.RunnerStderrRetainedBytes,
		GlobalCleanupAttempts:       globalReport.TempCleanupAttempts,
		GlobalCleanupFailures:       globalReport.CleanupFailed,
		GlobalResidualTemp:          globalReport.ResidualTemp,
		GlobalRawDiagnosticsDropped: globalReport.RawDiagnosticsDiscarded,
		CredentialObservedByChild:   true,
		ValidatedBeforeSQLWrite:     globalReport.RunnerResponseWrites == 1 && globalStdout.calls == 0,
	}, nil
}

func migrationSQLRenderingPrepareGlobalProbeProject(projectFixture migrationCommandProject) error {
	commandDirectory := filepath.Join(projectFixture.root, "cmd", "site")
	if err := os.MkdirAll(commandDirectory, 0o700); err != nil {
		return errors.New("create migration SQL global probe command directory")
	}
	repository, err := systemStateRepositoryRoot()
	if err != nil {
		return err
	}
	rootModule, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		return errors.New("read migration SQL global probe module authority")
	}
	goDirective := ""
	for _, line := range strings.Split(string(rootModule), "\n") {
		if strings.HasPrefix(line, "go ") {
			if goDirective != "" {
				return errors.New("migration SQL global probe found multiple Go directives")
			}
			goDirective = line
		}
	}
	if goDirective == "" {
		return errors.New("migration SQL global probe found no Go directive")
	}
	moduleDocument := []byte("module example.com/godj-mig137-resource-probe\n\n" + goDirective + "\n")
	if err := writeMigrationCommandActualFile(filepath.Join(projectFixture.root, "go.mod"), moduleDocument); err != nil {
		return err
	}
	if err := writeMigrationCommandActualFile(
		filepath.Join(commandDirectory, "main.go"),
		[]byte(migrationSQLRenderingGlobalProbeSource),
	); err != nil {
		return err
	}
	return nil
}

const migrationSQLRenderingGlobalProbeSource = `package main

import (
	"encoding/base64"
	"os"
)

func main() {
	expectedCredential := os.Getenv("GODJ_MIG137_EXPECTED_CREDENTIAL")
	if expectedCredential == "" || os.Getenv("DATABASE_URL") != expectedCredential {
		os.Exit(11)
	}
	childDiagnostic := os.Getenv("GODJ_MIG137_CHILD_STDERR")
	if childDiagnostic == "" {
		os.Exit(12)
	}
	if _, err := os.Stderr.WriteString(childDiagnostic); err != nil {
		os.Exit(13)
	}
	document, err := base64.StdEncoding.DecodeString(os.Getenv("GODJ_MIG137_PRIVATE_RESPONSE"))
	if err != nil {
		os.Exit(14)
	}
	written, err := os.Stdout.Write(document)
	if err != nil || written != len(document) {
		os.Exit(15)
	}
}
`
