//go:build darwin || linux

package godj

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/linked"
	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/project"
	"github.com/progresshans/godj/schema/ir"
)

const (
	migrationSQLRenderingSecret              = "godj-sql-rendering-private-secret-canary"
	migrationSQLRenderingCredentialCanary    = "godj-sql-rendering-credential-canary"
	migrationSQLRenderingProcessMode         = "GODJ_SQL_RENDERING_ACTUAL_MODE"
	migrationSQLRenderingProcessTrace        = "GODJ_SQL_RENDERING_ACTUAL_TRACE"
	migrationSQLRenderingProcessSourcePrefix = "GODJ_SQL_RENDERING_ACTUAL_SOURCE_"
	migrationSQLRenderingProcessNormal       = "normal"
	migrationSQLRenderingProcessCancellation = "cancellation"
	migrationSQLRenderingPoisonBarrier       = "godj-migration-sql-poison-barrier-v1"
)

type migrationSQLRenderingRegistration struct {
	id      string
	phase   protocol.Phase
	handler scenarioHandler
}

var migrationSQLRenderingScenarioRegistry = map[string]migrationSQLRenderingRegistration{
	"godj.migration.sql_rendering.argv_and_pre_io_rejection": {
		id: "MIG-129", phase: protocol.PhaseEnvironment, handler: migrationSQLRenderingArgv,
	},
	"godj.migration.sql_rendering.complete_load_exact_lookup_and_request": {
		id: "MIG-130", phase: protocol.PhaseConstruction, handler: migrationSQLRenderingRequest,
	},
	"django.migration.sql_rendering.forward_before_state_order": {
		id: "MIG-131", phase: protocol.PhaseConstruction, handler: migrationSQLRenderingBeforeState,
	},
	"django.migration.sql_rendering.sqlite_create_add_semantics": {
		id: "MIG-132", phase: protocol.PhaseConstruction, handler: migrationSQLRenderingSQLite,
	},
	"godj.migration.sql_rendering.postgres_current_projection": {
		id: "MIG-133", phase: protocol.PhaseConstruction, handler: migrationSQLRenderingPostgres,
	},
	"godj.migration.sql_rendering.canonical_deterministic_output": {
		id: "MIG-134", phase: protocol.PhaseEvaluation, handler: migrationSQLRenderingDeterminism,
	},
	"godj.migration.sql_rendering.database_and_history_zero_calls": {
		id: "MIG-135", phase: protocol.PhaseEnvironment, handler: migrationSQLRenderingNoDatabase,
	},
	"godj.migration.sql_rendering.renderer_and_operation_fail_closed": {
		id: "MIG-136", phase: protocol.PhaseEvaluation, handler: migrationSQLRenderingFailures,
	},
	"godj.migration.sql_rendering.resource_cleanup_redaction_and_write": {
		id: "MIG-137", phase: protocol.PhaseEnvironment, handler: migrationSQLRenderingResources,
	},
	"godj.migration.sql_rendering.external_project_configuration": {
		id: "MIG-138", phase: protocol.PhaseEnvironment, handler: migrationSQLRenderingExternalConfig,
	},
}

func migrationSQLRenderingScenarioHandler(scenario string) (scenarioHandler, bool) {
	registration, ok := migrationSQLRenderingScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		if ctx == nil {
			return protocol.Observation{}, errors.New("migration SQL rendering scenario context is nil")
		}
		if contract.ID != registration.id {
			return protocol.Observation{}, fmt.Errorf("migration SQL rendering scenario %q contract id %q; want %q", scenario, contract.ID, registration.id)
		}
		if contract.Scenario != scenario {
			return protocol.Observation{}, fmt.Errorf("migration SQL rendering scenario %q contract scenario %q", scenario, contract.Scenario)
		}
		if contract.Phase != registration.phase {
			return protocol.Observation{}, fmt.Errorf("migration SQL rendering scenario %q phase %q; want %q", scenario, contract.Phase, registration.phase)
		}
		return registration.handler(ctx, contract)
	}, true
}

func migrationSQLRenderingObservation(
	contract protocol.Contract,
	result protocol.Value,
	dbState *protocol.Value,
	metrics *protocol.Value,
) protocol.Observation {
	return protocol.Observation{
		ID: contract.ID, Status: protocol.StatusObserved, Phase: contract.Phase,
		Result: valuePointer(result), DBState: dbState, Metrics: metrics,
	}
}

func migrationSQLRenderingInt(value int) protocol.Value {
	return protocol.Integer(strconv.Itoa(value))
}

func migrationSQLRenderingStrings(values ...string) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index, value := range values {
		items[index] = protocol.String(value)
	}
	return protocol.List(items...)
}

func migrationSQLRenderingArgvValue(values []string) protocol.Value {
	return migrationSQLRenderingStrings(values...)
}

func migrationSQLRenderingArgv(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	accepted, err := migrationSQLRenderingProbeAcceptedArgv(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	if accepted[0].caseName != "implicit" || accepted[0].form != "implicit" || accepted[0].literalZero ||
		accepted[1].caseName != "explicit" || accepted[1].form != "explicit" || accepted[1].literalZero ||
		accepted[2].caseName != "literal_zero" || accepted[2].form != "implicit" || !accepted[2].literalZero ||
		accepted[2].app != "blog" || accepted[2].name != "zero" || accepted[2].project != migrationSQLRenderingArgvProbeDefault ||
		!accepted[2].success || accepted[2].output != migrationSQLRenderingArgvProbeOutput {
		return protocol.Observation{}, errors.New("migration SQL argv probe did not preserve literal zero as an exact migration name")
	}
	const acceptedForms = 2
	acceptedValues := make([]protocol.Value, acceptedForms)
	for index := 0; index < acceptedForms; index++ {
		candidate := accepted[index]
		acceptedValues[index] = protocol.Object(map[string]protocol.Value{
			"app": protocol.String(candidate.app), "argv": migrationSQLRenderingArgvValue(candidate.arguments()),
			"migration_name": protocol.String(candidate.name), "project": protocol.String(candidate.project),
		})
	}
	rejected := []struct {
		name string
		argv []string
	}{
		{name: "app_only", argv: []string{"sqlmigrate", "blog"}},
		{name: "project_before_identity", argv: []string{"sqlmigrate", "--project", "./godj.toml", "blog", "0002_render_sql"}},
		{name: "missing_project_path", argv: []string{"sqlmigrate", "blog", "0002_render_sql", "--project"}},
		{name: "latest_reserved", argv: []string{"sqlmigrate", "blog", "latest"}},
		{name: "backwards_option", argv: []string{"sqlmigrate", "blog", "0002_render_sql", "--backwards"}},
		{name: "reverse_option", argv: []string{"sqlmigrate", "blog", "0002_render_sql", "--reverse"}},
		{name: "leading_dash_app", argv: []string{"sqlmigrate", "--blog", "0002_render_sql"}},
		{name: "leading_dash_name", argv: []string{"sqlmigrate", "blog", "--0002"}},
		{name: "unknown_trailing_option", argv: []string{"sqlmigrate", "blog", "0002_render_sql", "--database", "other"}},
	}
	rejectedValues := make([]protocol.Value, len(rejected))
	totals := struct {
		builds, discoveries, renderer, sources, backend int
	}{}
	for index, candidate := range rejected {
		if err := ctx.Err(); err != nil {
			return protocol.Observation{}, err
		}
		backend := &migrationSQLRenderingForbiddenBackend{}
		var stdout, stderr bytes.Buffer
		report := productcheck.RunSQLMigrate(productcheck.SQLMigrateInvocation{
			Context: ctx, CWD: filepath.Join(os.TempDir(), "godj-sql-rendering-invalid-argv-does-not-exist"),
			Args: candidate.argv, Environment: []string{}, Stdout: &stdout, Stderr: &stderr, Backend: backend,
		})
		if report.ExitCode != 2 || !report.HasSQLMigrateFailure || report.HasSQLMigrateResult ||
			report.SQLMigrateFailure != (sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategoryCommand, Code: sqlmigrateprotocol.CodeInvalidArguments}) ||
			report.BuildCalls != 0 || report.RunnerCalls != 0 || report.AncestorDirectoriesInspected != 0 ||
			report.DescriptorReads != 0 || backend.calls != 0 || stdout.Len() != 0 {
			return protocol.Observation{}, fmt.Errorf("SQL migration rejected argv %q crossed pre-I/O boundary: report=%+v backend_calls=%d", candidate.argv, report, backend.calls)
		}
		discoveries := report.AncestorDirectoriesInspected + report.DescriptorReads
		totals.builds += report.BuildCalls
		totals.discoveries += discoveries
		totals.renderer += report.RunnerCalls
		totals.sources += report.RunnerCalls
		totals.backend += backend.calls
		rejectedValues[index] = protocol.Object(map[string]protocol.Value{
			"argv": migrationSQLRenderingArgvValue(candidate.argv), "backend_opens": migrationSQLRenderingInt(backend.calls),
			"builds": migrationSQLRenderingInt(report.BuildCalls), "case": protocol.String(candidate.name),
			"category": protocol.String(report.SQLMigrateFailure.Category), "code": protocol.String(report.SQLMigrateFailure.Code),
			"project_discoveries": migrationSQLRenderingInt(discoveries), "renderer_observations": migrationSQLRenderingInt(report.RunnerCalls),
			"source_loads": migrationSQLRenderingInt(report.RunnerCalls),
		})
	}
	result := protocol.Object(map[string]protocol.Value{
		"accepted": protocol.List(acceptedValues...), "exact_public_forms": migrationSQLRenderingInt(acceptedForms),
		"migration_name_resolution": protocol.String("exact_only"), "rejected": protocol.List(rejectedValues...),
		"zero_name_policy": protocol.String("literal_exact_name"),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"accepted_forms": migrationSQLRenderingInt(acceptedForms), "backend_opens_for_rejected": migrationSQLRenderingInt(totals.backend),
		"builds_for_rejected": migrationSQLRenderingInt(totals.builds), "project_discoveries_for_rejected": migrationSQLRenderingInt(totals.discoveries),
		"rejected_forms": migrationSQLRenderingInt(len(rejected)), "renderer_observations_for_rejected": migrationSQLRenderingInt(totals.renderer),
		"source_loads_for_rejected": migrationSQLRenderingInt(totals.sources),
	})
	return migrationSQLRenderingObservation(contract, result, nil, valuePointer(metrics)), nil
}

type migrationSQLRenderingForbiddenBackend struct{ calls int }

func (backend *migrationSQLRenderingForbiddenBackend) Execute(
	context.Context,
	<-chan struct{},
	productcheck.ProcessStage,
	productcheck.Command,
) productcheck.ProcessResult {
	backend.calls++
	return productcheck.ProcessResult{}
}

type migrationSQLRenderingFixture struct {
	definitions []migrations.Migration
	sources     []definition.Source
	loaded      migrations.LoadedDefinitionSet
	target      migrations.MigrationKey
}

func newMigrationSQLRenderingFixture() (migrationSQLRenderingFixture, error) {
	target := migrations.MigrationKey{App: "blog", Name: "0002_render_sql"}
	authors := migrations.Migration{
		App: "authors", Name: "0001_initial",
		Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "authors", Model: ir.Model{
			Name: "author", GoName: "Author", DBTable: "authors_author",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 80},
			},
		}}},
	}
	blog := migrations.Migration{
		App: "blog", Name: "0001_initial", Dependencies: []migrations.MigrationKey{{App: "authors", Name: "0001_initial"}},
		Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "blog", Model: ir.Model{
			Name: "article", GoName: "Article", DBTable: "blog_article",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "title", GoName: "Title", Column: "title", Kind: ir.FieldChar, MaxLength: 200},
				{
					Name: "author", GoName: "Author", Column: "author_id", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Cardinality: ir.RelationManyToOne,
						Reverse: ir.ReverseRelation{Name: "articles"}, OnDelete: ir.DeleteProtect,
					},
				},
			},
		}}},
	}
	targetDefinition := migrations.Migration{
		App: target.App, Name: target.Name, Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_initial"}},
		Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "blog", Model: ir.Model{
				Name: "category", GoName: "Category", DBTable: "blog_category",
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 80},
				},
			}},
			migrations.AddField{AppLabel: "blog", ModelName: "article", Field: ir.Field{
				Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar, MaxLength: 120, Nullable: true,
			}},
		},
	}
	definitions := []migrations.Migration{authors, blog, targetDefinition}
	sources, err := migrationSQLRenderingSources(definitions)
	if err != nil {
		return migrationSQLRenderingFixture{}, err
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		return migrationSQLRenderingFixture{}, err
	}
	return migrationSQLRenderingFixture{definitions: definitions, sources: sources, loaded: loaded, target: target}, nil
}

func migrationSQLRenderingSources(definitions []migrations.Migration) ([]definition.Source, error) {
	sources := make([]definition.Source, len(definitions))
	for index, value := range definitions {
		document, err := definition.Encode(definition.Producer{Name: "godj-sql-rendering-actual", Version: "1"}, value)
		if err != nil {
			return nil, err
		}
		sources[index] = definition.Source{
			SourceID: fmt.Sprintf("sql-rendering/%02d_%s_%s.godj.json", index, value.App, value.Name),
			Document: document,
		}
	}
	return sources, nil
}

type migrationSQLRenderingSpy struct {
	mu         sync.Mutex
	calls      int
	requests   []migrationbackend.ForwardMigrationSQLRequest
	statements []string
	err        error
}

func (renderer *migrationSQLRenderingSpy) RenderForwardMigrationSQL(
	_ context.Context,
	request migrationbackend.ForwardMigrationSQLRequest,
) ([]string, error) {
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	renderer.calls++
	renderer.requests = append(renderer.requests, request)
	return append([]string(nil), renderer.statements...), renderer.err
}

func (renderer *migrationSQLRenderingSpy) snapshot() (int, []migrationbackend.ForwardMigrationSQLRequest) {
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	return renderer.calls, append([]migrationbackend.ForwardMigrationSQLRequest(nil), renderer.requests...)
}

type migrationSQLRenderingObservedRenderer struct {
	delegate migrationbackend.MigrationSQLRenderer
	spy      migrationSQLRenderingSpy
}

func (renderer *migrationSQLRenderingObservedRenderer) RenderForwardMigrationSQL(
	ctx context.Context,
	request migrationbackend.ForwardMigrationSQLRequest,
) ([]string, error) {
	renderer.spy.mu.Lock()
	renderer.spy.calls++
	renderer.spy.requests = append(renderer.spy.requests, request)
	renderer.spy.mu.Unlock()
	return renderer.delegate.RenderForwardMigrationSQL(ctx, request)
}

func migrationSQLRenderingRequest(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return protocol.Observation{}, err
	}
	renderer := &migrationSQLRenderingSpy{statements: []string{"CREATE TABLE category", "ALTER TABLE article ADD summary"}}
	statements, err := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, renderer)
	if err != nil || len(statements) != 2 {
		return protocol.Observation{}, fmt.Errorf("render migration SQL request: statements=%v err=%w", statements, err)
	}
	calls, requests := renderer.snapshot()
	if calls != 1 || len(requests) != 1 || requests[0].App != fixture.target.App || requests[0].Name != fixture.target.Name ||
		len(requests[0].Intent.Operations) != 2 {
		return protocol.Observation{}, fmt.Errorf("render migration SQL request observation = calls:%d requests:%+v", calls, requests)
	}
	firstRequest := requests[0]
	if firstRequest.Intent.Operations[0].Kind != migrationbackend.MigrationCreateModel ||
		firstRequest.Intent.Operations[1].Kind != migrationbackend.MigrationAddField {
		return protocol.Observation{}, errors.New("render migration SQL request operation order changed")
	}
	firstRequest.Intent.Operations[1].After.Fields[len(firstRequest.Intent.Operations[1].After.Fields)-1].Name = "mutated"
	second := &migrationSQLRenderingSpy{statements: append([]string(nil), renderer.statements...)}
	if _, err := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, second); err != nil {
		return protocol.Observation{}, err
	}
	_, secondRequests := second.snapshot()
	if got := secondRequests[0].Intent.Operations[1].After.Fields[len(secondRequests[0].Intent.Operations[1].After.Fields)-1].Name; got != "summary" {
		return protocol.Observation{}, fmt.Errorf("renderer request is not detached: %q", got)
	}

	malformed := append([]definition.Source(nil), fixture.sources...)
	malformed = append(malformed, definition.Source{SourceID: "sql-rendering/99_invalid.godj.json", Document: []byte("{")})
	malformedRequest, err := sqlmigrateprotocol.EncodeRequest(sqlmigrateprotocol.Request{App: fixture.target.App, Name: fixture.target.Name})
	if err != nil {
		return protocol.Observation{}, err
	}
	malformedRenderer := &migrationSQLRenderingSpy{statements: []string{"SHOULD NOT RUN", "SHOULD NOT RUN"}}
	var malformedWire bytes.Buffer
	malformedReport, err := linked.RunSQLMigrate(
		ctx,
		linked.SQLMigrateConfig{MigrationDefinitionSources: malformed, MigrationSQLRenderer: malformedRenderer},
		[]string{sqlmigrateprotocol.PrivateArgument},
		bytes.NewReader(malformedRequest),
		&malformedWire,
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	malformedResponse, malformedFailure, malformedFailed := sqlmigrateprotocol.ParseResponse(malformedWire.Bytes(), true)
	malformedCalls, _ := malformedRenderer.snapshot()
	if malformedFailed || malformedFailure != (sqlmigrateprotocol.Failure{}) || malformedResponse.OK ||
		malformedResponse.Failure != (sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategorySource, Code: "invalid_definition_document"}) ||
		malformedReport.LoadCalls != 1 || malformedReport.DefinitionSetsPublished != 0 || malformedCalls != 0 {
		return protocol.Observation{}, fmt.Errorf("invalid unrelated SQL migration catalog = report:%+v response:%+v parse:%+v/%t calls:%d", malformedReport, malformedResponse, malformedFailure, malformedFailed, malformedCalls)
	}
	prefixSpy := &migrationSQLRenderingSpy{statements: []string{"SHOULD NOT RUN", "SHOULD NOT RUN"}}
	_, prefixErr := migrations.RenderMigrationSQL(ctx, fixture.loaded, migrations.MigrationKey{App: "blog", Name: "0002"}, prefixSpy)
	var planningError *migrations.PlanningError
	if !errors.As(prefixErr, &planningError) || planningError.Code != migrations.CodeTargetNotFound {
		return protocol.Observation{}, fmt.Errorf("prefix-looking exact miss = %v", prefixErr)
	}
	prefixCalls, _ := prefixSpy.snapshot()
	_, unavailableErr := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, nil)
	if !migrationSQLRenderingErrorIs(unavailableErr, migrations.CategorySQLRender, migrations.CodeRendererUnavailable) {
		return protocol.Observation{}, fmt.Errorf("nil renderer failure = %v", unavailableErr)
	}
	zeroStatements, zeroErr := sqlite.NewMigrationSQLRenderer().RenderForwardMigrationSQL(
		ctx,
		migrationbackend.ForwardMigrationSQLRequest{},
	)
	if zeroErr == nil || zeroStatements != nil {
		return protocol.Observation{}, errors.New("zero SQL request unexpectedly rendered")
	}

	stableRequest := secondRequests[0]
	operationValues := make([]protocol.Value, len(stableRequest.Intent.Operations))
	for index, operation := range stableRequest.Intent.Operations {
		kind, subject, subjectErr := migrationSQLRenderingOperationIdentity(operation, stableRequest.App, true)
		if subjectErr != nil {
			return protocol.Observation{}, subjectErr
		}
		operationValues[index] = protocol.Object(map[string]protocol.Value{"kind": protocol.String(kind), "subject": protocol.String(subject)})
	}
	failures := protocol.List(
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("invalid_unrelated_definition"), "failed_stage": protocol.String("complete_definition_load"),
			"renderer_calls": migrationSQLRenderingInt(0), "request_materializations": migrationSQLRenderingInt(0),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("prefix_looking_exact_miss"), "failed_stage": protocol.String("exact_target_lookup"),
			"renderer_calls": migrationSQLRenderingInt(prefixCalls), "request_materializations": migrationSQLRenderingInt(0),
			"requested_name": protocol.String("0002"),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("renderer_unavailable"), "failed_stage": protocol.String("renderer_validation"),
			"renderer_calls": migrationSQLRenderingInt(0), "request_materializations": migrationSQLRenderingInt(1),
		}),
	)
	result := protocol.Object(map[string]protocol.Value{
		"detached_request": protocol.Boolean(true), "failures": failures, "operation_order_preserved": protocol.Boolean(true),
		"request": protocol.Object(map[string]protocol.Value{
			"app": protocol.String(stableRequest.App), "direction": protocol.String("forward"), "name": protocol.String(stableRequest.Name),
			"intent": protocol.Object(map[string]protocol.Value{
				"operations": protocol.List(operationValues...), "state_basis": protocol.String("target_dependency_before"),
			}),
		}),
		"request_zero_value_valid": protocol.Boolean(false),
		"stages": migrationSQLRenderingStrings(
			"complete_definition_load", "graph_validation", "chronology_validation", "exact_target_lookup",
			"target_before_state_reconstruction", "forward_request_materialization", "renderer_validation", "render_once",
		),
		"target_identity_preserved": protocol.Boolean(true),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"complete_catalog_loads": migrationSQLRenderingInt(malformedReport.LoadCalls), "history_reads": migrationSQLRenderingInt(malformedReport.AppliedHistoryReads),
		"renderer_calls": migrationSQLRenderingInt(calls), "request_materializations": migrationSQLRenderingInt(len(requests)),
		"target_migrations": migrationSQLRenderingInt(1), "transactions": migrationSQLRenderingInt(0),
	})
	return migrationSQLRenderingObservation(contract, result, nil, valuePointer(metrics)), nil
}

func migrationSQLRenderingErrorIs(err error, category migrations.ErrorCategory, code migrations.ErrorCode) bool {
	var sqlError *migrations.MigrationSQLError
	return errors.As(err, &sqlError) && sqlError != nil && sqlError.Category == category && sqlError.Code == code
}

func migrationSQLRenderingOperationIdentity(
	operation migrationbackend.MigrationOperation,
	app string,
	goModelName bool,
) (string, string, error) {
	if app == "" {
		return "", "", errors.New("migration SQL operation identity app is empty")
	}
	switch operation.Kind {
	case migrationbackend.MigrationCreateModel:
		return "CreateModel", app + "." + operation.After.GoName, nil
	case migrationbackend.MigrationAddField:
		model := operation.After.Name
		if goModelName {
			model = operation.After.GoName
		}
		field, err := migrationSQLRenderingAddedField(operation)
		if err != nil {
			return "", "", err
		}
		return "AddField", app + "." + model + "." + field.Name, nil
	default:
		return "", "", fmt.Errorf("migration SQL operation kind %d is unsupported", operation.Kind)
	}
}

func migrationSQLRenderingAddedField(operation migrationbackend.MigrationOperation) (ir.Field, error) {
	before := make(map[string]struct{}, len(operation.Before.Fields))
	for _, field := range operation.Before.Fields {
		before[field.Name] = struct{}{}
	}
	var added ir.Field
	count := 0
	for _, field := range operation.After.Fields {
		if _, exists := before[field.Name]; exists {
			continue
		}
		added = field
		count++
	}
	if count != 1 {
		return ir.Field{}, fmt.Errorf("migration SQL AddField delta count = %d, want 1", count)
	}
	return added, nil
}

func migrationSQLRenderingBeforeState(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return protocol.Observation{}, err
	}
	reconstructor, err := migrations.NewStateReconstructor(fixture.definitions...)
	if err != nil {
		return protocol.Observation{}, err
	}
	before, err := reconstructor.Reconstruct(migrations.BeforeStateRequest(fixture.target))
	if err != nil {
		return protocol.Observation{}, err
	}
	after, err := reconstructor.Reconstruct(migrations.AfterStateRequest(fixture.target))
	if err != nil {
		return protocol.Observation{}, err
	}
	renderer := &migrationSQLRenderingObservedRenderer{delegate: sqlite.NewMigrationSQLRenderer()}
	if _, err := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, renderer); err != nil {
		return protocol.Observation{}, err
	}
	calls, requests := renderer.spy.snapshot()
	if calls != 1 || len(requests) != 1 || len(requests[0].Intent.Operations) != 2 {
		return protocol.Observation{}, fmt.Errorf("migration SQL before-state render = calls:%d requests:%+v", calls, requests)
	}
	operations := requests[0].Intent.Operations
	beforeArticle, beforeArticleExists := before.Model("blog", "article")
	beforeCategory, beforeCategoryExists := before.Model("blog", "category")
	beforeAuthor, beforeAuthorExists := before.Model("authors", "author")
	afterArticle, afterArticleExists := after.Model("blog", "article")
	afterCategory, afterCategoryExists := after.Model("blog", "category")
	afterAuthor, afterAuthorExists := after.Model("authors", "author")
	authorField, authorFieldExists := migrationSQLRenderingModelField(beforeArticle, "author")
	authorKey, authorKeyExists := migrationSQLRenderingModelField(beforeAuthor, "id")
	var addFieldTarget migrationbackend.MigrationTarget
	if len(operations[1].Targets) == 1 {
		addFieldTarget = operations[1].Targets[0]
	}
	if !beforeArticleExists || beforeCategoryExists || !reflect.DeepEqual(beforeCategory, ir.Model{}) ||
		!beforeAuthorExists || !afterArticleExists || !afterCategoryExists || !afterAuthorExists ||
		!reflect.DeepEqual(beforeAuthor, afterAuthor) || !authorFieldExists || !authorKeyExists ||
		operations[0].OperationIndex != 0 || operations[0].Kind != migrationbackend.MigrationCreateModel ||
		!reflect.DeepEqual(operations[0].Before, ir.Model{}) || !reflect.DeepEqual(operations[0].After, afterCategory) || len(operations[0].Targets) != 0 ||
		operations[1].OperationIndex != 1 || operations[1].Kind != migrationbackend.MigrationAddField ||
		!reflect.DeepEqual(operations[1].Before, beforeArticle) || !reflect.DeepEqual(operations[1].After, afterArticle) ||
		len(operations[1].Targets) != 1 || !reflect.DeepEqual(addFieldTarget.SourceField, authorField) ||
		!reflect.DeepEqual(addFieldTarget.TargetModel, beforeAuthor) || !reflect.DeepEqual(addFieldTarget.TargetKey, authorKey) {
		return protocol.Observation{}, errors.New("migration SQL renderer request is not coupled to the reconstructed target-before/final state")
	}
	operationValues := make([]protocol.Value, len(requests[0].Intent.Operations))
	for index, operation := range requests[0].Intent.Operations {
		kind, subject, identityErr := migrationSQLRenderingOperationIdentity(operation, requests[0].App, false)
		if identityErr != nil {
			return protocol.Observation{}, identityErr
		}
		operationValues[index] = protocol.Object(map[string]protocol.Value{
			"kind": protocol.String(kind), "ordinal": migrationSQLRenderingInt(operation.OperationIndex), "subject": protocol.String(subject),
		})
	}
	beforeValue, err := migrationSQLRenderingStateValue(before)
	if err != nil {
		return protocol.Observation{}, err
	}
	afterValue, err := migrationSQLRenderingStateValue(after)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"before_state": beforeValue, "before_state_at_end": protocol.Boolean(false),
		"before_state_target": protocol.Object(map[string]protocol.Value{
			"app": protocol.String(fixture.target.App), "name": protocol.String(fixture.target.Name),
		}),
		"direction": protocol.String("forward"), "final_state": afterValue, "operation_order": protocol.List(operationValues...),
		"plan": protocol.List(protocol.Object(map[string]protocol.Value{
			"app": protocol.String(fixture.target.App), "direction": protocol.String("forward"), "name": protocol.String(fixture.target.Name),
		})),
	})
	return migrationSQLRenderingObservation(contract, result, nil, nil), nil
}

func migrationSQLRenderingModelField(model ir.Model, name string) (ir.Field, bool) {
	for index := range model.Fields {
		if model.Fields[index].Name == name {
			return model.Fields[index], true
		}
	}
	return ir.Field{}, false
}

func migrationSQLRenderingStateValue(state migrations.ProjectState) (protocol.Value, error) {
	models := make([]protocol.Value, 0)
	for _, app := range state.Apps() {
		schema, ok := state.Schema(app)
		if !ok {
			return protocol.Value{}, fmt.Errorf("migration SQL state lost app %q", app)
		}
		ordered := append([]ir.Model(nil), schema.Models...)
		sort.Slice(ordered, func(left, right int) bool { return ordered[left].Name < ordered[right].Name })
		for _, model := range ordered {
			fields := make([]protocol.Value, len(model.Fields))
			for index, field := range model.Fields {
				kind, err := migrationSQLRenderingFieldKind(field.Kind)
				if err != nil {
					return protocol.Value{}, err
				}
				fields[index] = protocol.Object(map[string]protocol.Value{
					"kind": protocol.String(kind), "name": protocol.String(field.Name),
				})
			}
			models = append(models, protocol.Object(map[string]protocol.Value{
				"app": protocol.String(app), "fields": protocol.List(fields...), "model": protocol.String(model.Name),
				"table": protocol.String(model.DBTable),
			}))
		}
	}
	return protocol.Object(map[string]protocol.Value{"models": protocol.List(models...)}), nil
}

func migrationSQLRenderingFieldKind(kind ir.FieldKind) (string, error) {
	switch kind {
	case ir.FieldAuto:
		return "AutoField", nil
	case ir.FieldChar:
		return "CharField", nil
	case ir.FieldBoolean:
		return "BooleanField", nil
	case ir.FieldForeignKey:
		return "ForeignKey", nil
	default:
		return "", fmt.Errorf("migration SQL field kind %q is unsupported", kind)
	}
}

var (
	migrationSQLRenderingCreatePattern = regexp.MustCompile(`^CREATE TABLE "((?:[^"]|"")+)" \((.*)\)$`)
	migrationSQLRenderingAddPattern    = regexp.MustCompile(`^ALTER TABLE "((?:[^"]|"")+)" ADD COLUMN (.*)$`)
	migrationSQLRenderingColumnPattern = regexp.MustCompile(`^"((?:[^"]|"")+)" ([A-Za-z]+(?:\([0-9]+\))?)(.*)$`)
	migrationSQLRenderingPGCreate      = regexp.MustCompile(`^CREATE TABLE "((?:[^"]|"")+)"\."((?:[^"]|"")+)" \(`)
	migrationSQLRenderingPGAdd         = regexp.MustCompile(`^ALTER TABLE "((?:[^"]|"")+)"\."((?:[^"]|"")+)" ADD COLUMN "((?:[^"]|"")+)" `)
)

func migrationSQLRenderingSQLite(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return protocol.Observation{}, err
	}
	renderer := &migrationSQLRenderingObservedRenderer{delegate: sqlite.NewMigrationSQLRenderer()}
	statements, err := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, renderer)
	if err != nil {
		return protocol.Observation{}, err
	}
	_, requests := renderer.spy.snapshot()
	if len(requests) != 1 || len(requests[0].Intent.Operations) != len(statements) || len(statements) != 2 {
		return protocol.Observation{}, fmt.Errorf("SQLite SQL rendering cardinality = requests:%d operations:%d statements:%d", len(requests), len(requests[0].Intent.Operations), len(statements))
	}
	normalized := make([]protocol.Value, len(statements))
	for index, statement := range statements {
		statementValues, parseErr := migrationSQLRenderingSQLiteStatement(statement)
		if parseErr != nil {
			return protocol.Observation{}, parseErr
		}
		kind, subject, identityErr := migrationSQLRenderingOperationIdentity(requests[0].Intent.Operations[index], requests[0].App, false)
		if identityErr != nil {
			return protocol.Observation{}, identityErr
		}
		normalized[index] = protocol.Object(map[string]protocol.Value{
			"kind": protocol.String(kind), "ordinal": migrationSQLRenderingInt(requests[0].Intent.Operations[index].OperationIndex),
			"statements": protocol.List(statementValues...), "subject": protocol.String(subject),
		})
	}
	result := protocol.Object(map[string]protocol.Value{
		"backend": protocol.String("django.db.backends.sqlite3"), "comments_compared": protocol.Boolean(false),
		"normalized_operations": protocol.List(normalized...), "raw_sql_bytes_compared": protocol.Boolean(false),
		"transaction_wrapper_compared": protocol.Boolean(false),
	})
	return migrationSQLRenderingObservation(contract, result, nil, nil), nil
}

func migrationSQLRenderingSQLiteStatement(statement string) ([]protocol.Value, error) {
	if match := migrationSQLRenderingCreatePattern.FindStringSubmatch(statement); match != nil {
		columns, err := migrationSQLRenderingSplitColumns(match[2])
		if err != nil {
			return nil, err
		}
		columnValues := make([]protocol.Value, len(columns))
		for index, column := range columns {
			columnValues[index], err = migrationSQLRenderingColumnValue(column)
			if err != nil {
				return nil, err
			}
		}
		return []protocol.Value{protocol.Object(map[string]protocol.Value{
			"columns": protocol.List(columnValues...), "kind": protocol.String("create_table"),
			"table": protocol.String(migrationSQLRenderingUnquote(match[1])),
		})}, nil
	}
	if match := migrationSQLRenderingAddPattern.FindStringSubmatch(statement); match != nil {
		column, err := migrationSQLRenderingColumnValue(match[2])
		if err != nil {
			return nil, err
		}
		return []protocol.Value{protocol.Object(map[string]protocol.Value{
			"column": column, "kind": protocol.String("add_column"),
			"table": protocol.String(migrationSQLRenderingUnquote(match[1])),
		})}, nil
	}
	return nil, fmt.Errorf("unexpected SQLite migration SQL body %q", statement)
}

func migrationSQLRenderingSplitColumns(input string) ([]string, error) {
	columns := make([]string, 0)
	start, depth := 0, 0
	quoted := false
	for index := 0; index < len(input); index++ {
		switch input[index] {
		case '"':
			if quoted && index+1 < len(input) && input[index+1] == '"' {
				index++
				continue
			}
			quoted = !quoted
		case '(':
			if !quoted {
				depth++
			}
		case ')':
			if !quoted {
				depth--
			}
		case ',':
			if !quoted && depth == 0 {
				columns = append(columns, strings.TrimSpace(input[start:index]))
				start = index + 1
			}
		}
	}
	if quoted || depth != 0 {
		return nil, errors.New("SQLite migration SQL column list is unbalanced")
	}
	columns = append(columns, strings.TrimSpace(input[start:]))
	for _, column := range columns {
		if column == "" {
			return nil, errors.New("SQLite migration SQL column is empty")
		}
	}
	return columns, nil
}

func migrationSQLRenderingColumnValue(definition string) (protocol.Value, error) {
	match := migrationSQLRenderingColumnPattern.FindStringSubmatch(definition)
	if match == nil {
		return protocol.Value{}, fmt.Errorf("unexpected SQLite migration SQL column %q", definition)
	}
	constraints := strings.ToUpper(match[3])
	var nullable, primaryKey, autoincrement bool
	switch constraints {
	case " NOT NULL PRIMARY KEY AUTOINCREMENT":
		primaryKey = true
		autoincrement = true
	case " NOT NULL":
	case " NULL":
		nullable = true
	default:
		return protocol.Value{}, fmt.Errorf("unexpected SQLite migration SQL constraints %q", match[3])
	}
	return protocol.Object(map[string]protocol.Value{
		"autoincrement": protocol.Boolean(autoincrement),
		"name":          protocol.String(migrationSQLRenderingUnquote(match[1])),
		"nullability": protocol.String(func() string {
			if nullable {
				return "nullable"
			}
			return "not_null"
		}()),
		"primary_key": protocol.Boolean(primaryKey),
		"reference":   protocol.Null(), "sql_type": protocol.String(strings.ToLower(match[2])),
	}), nil
}

func migrationSQLRenderingUnquote(value string) string {
	return strings.ReplaceAll(value, `""`, `"`)
}

func migrationSQLRenderingPostgres(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return protocol.Observation{}, err
	}
	configType := reflect.TypeOf(postgres.MigrationSQLConfig{})
	if configType.NumField() != 1 || configType.Field(0).Name != "Schema" || configType.Field(0).Type.Kind() != reflect.String {
		return protocol.Observation{}, fmt.Errorf("PostgreSQL SQL renderer configuration shape = %v", configType)
	}
	configuration := postgres.MigrationSQLConfig{Schema: "public"}
	renderer := postgres.NewMigrationSQLRenderer(configuration)
	configuration.Schema = "mutated_after_construction"
	observed := &migrationSQLRenderingObservedRenderer{delegate: renderer}
	statements, err := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, observed)
	if err != nil || len(statements) != 2 {
		return protocol.Observation{}, fmt.Errorf("PostgreSQL migration SQL = %v, %w", statements, err)
	}
	create := migrationSQLRenderingPGCreate.FindStringSubmatch(statements[0])
	add := migrationSQLRenderingPGAdd.FindStringSubmatch(statements[1])
	if create == nil || add == nil || migrationSQLRenderingUnquote(create[1]) != "public" ||
		migrationSQLRenderingUnquote(add[1]) != "public" {
		return protocol.Observation{}, fmt.Errorf("PostgreSQL migration SQL is not frozen schema-qualified: %q", statements)
	}
	selectionEvidence, err := migrationSQLRenderingObserveConfiguredRenderer(
		ctx,
		fixture,
		func(databaseconfig.Config) migrationbackend.MigrationSQLRenderer { return renderer },
		false,
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	if selectionEvidence.backendOpens != 0 || selectionEvidence.networkAttempts != 0 ||
		selectionEvidence.credentialPublished != 0 {
		return protocol.Observation{}, fmt.Errorf("PostgreSQL migration SQL crossed its database-free boundary: %+v", selectionEvidence)
	}
	result := protocol.Object(map[string]protocol.Value{
		"configuration": protocol.Object(map[string]protocol.Value{
			"immutable": protocol.Boolean(true), "inputs": migrationSQLRenderingStrings("schema"), "schema": protocol.String("public"),
		}),
		"forbidden_configuration_inputs": migrationSQLRenderingStrings("database_url", "credential", "database_handle", "server_connection"),
		"normalized_operations": protocol.List(
			protocol.Object(map[string]protocol.Value{
				"kind": protocol.String("create_table"), "schema": protocol.String(migrationSQLRenderingUnquote(create[1])),
				"table": protocol.String(migrationSQLRenderingUnquote(create[2])),
			}),
			protocol.Object(map[string]protocol.Value{
				"column": protocol.String(migrationSQLRenderingUnquote(add[3])), "kind": protocol.String("add_column"),
				"schema": protocol.String(migrationSQLRenderingUnquote(add[1])), "table": protocol.String(migrationSQLRenderingUnquote(add[2])),
			}),
		),
		"raw_sql_bytes_are_reference_contract": protocol.Boolean(false), "schema_qualified": protocol.Boolean(true),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"backend_opens": migrationSQLRenderingInt(selectionEvidence.backendOpens), "catalog_reads": migrationSQLRenderingInt(0),
		"credential_values": migrationSQLRenderingInt(selectionEvidence.credentialPublished), "history_reads": migrationSQLRenderingInt(0),
		"network_calls": migrationSQLRenderingInt(selectionEvidence.networkAttempts), "renderer_constructions": migrationSQLRenderingInt(1),
		"server_profile_reads": migrationSQLRenderingInt(0),
	})
	return migrationSQLRenderingObservation(contract, result, nil, valuePointer(metrics)), nil
}

func migrationSQLRenderingDecisionBodies(ctx context.Context) ([]string, error) {
	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return nil, err
	}
	return migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, sqlite.NewMigrationSQLRenderer())
}

func migrationSQLRenderingOutput(statements []string) string {
	var output strings.Builder
	for _, statement := range statements {
		output.WriteString(statement)
		output.WriteString(";\n")
	}
	return output.String()
}

func migrationSQLRenderingDeterminism(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return protocol.Observation{}, err
	}
	first, err := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, sqlite.NewMigrationSQLRenderer())
	if err != nil {
		return protocol.Observation{}, err
	}
	repeat, err := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, sqlite.NewMigrationSQLRenderer())
	if err != nil {
		return protocol.Observation{}, err
	}
	parallel := make([][]string, 2)
	parallelErr := make(chan error, len(parallel))
	var group sync.WaitGroup
	for index := range parallel {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			statements, renderErr := migrations.RenderMigrationSQL(
				ctx,
				fixture.loaded,
				fixture.target,
				sqlite.NewMigrationSQLRenderer(),
			)
			parallel[index] = statements
			parallelErr <- renderErr
		}()
	}
	group.Wait()
	close(parallelErr)
	for renderErr := range parallelErr {
		if renderErr != nil {
			return protocol.Observation{}, renderErr
		}
	}
	for _, candidate := range append([][]string{repeat}, parallel...) {
		if !reflect.DeepEqual(candidate, first) {
			return protocol.Observation{}, fmt.Errorf("migration SQL deterministic output changed: first=%q candidate=%q", first, candidate)
		}
	}
	for _, body := range first {
		if strings.ContainsRune(body, ';') {
			return protocol.Observation{}, fmt.Errorf("renderer body contains terminator: %q", body)
		}
	}
	emptyTarget := migrations.MigrationKey{App: "blog", Name: "zero"}
	emptyDefinitions := append(append([]migrations.Migration(nil), fixture.definitions...), migrations.Migration{
		App: emptyTarget.App, Name: emptyTarget.Name, Dependencies: []migrations.MigrationKey{fixture.target}, Operations: []migrations.Operation{},
	})
	emptySources, err := migrationSQLRenderingSources(emptyDefinitions)
	if err != nil {
		return protocol.Observation{}, err
	}
	emptyLoaded, _, err := definition.Load(emptySources...)
	if err != nil {
		return protocol.Observation{}, err
	}
	empty, err := migrations.RenderMigrationSQL(ctx, emptyLoaded, emptyTarget, sqlite.NewMigrationSQLRenderer())
	if err != nil || empty == nil || len(empty) != 0 {
		return protocol.Observation{}, fmt.Errorf("empty migration SQL = %#v, %v", empty, err)
	}
	processEvidence, err := migrationSQLRenderingObserveProcesses(ctx, first)
	if err != nil {
		return protocol.Observation{}, err
	}
	output := migrationSQLRenderingOutput(first)
	if processEvidence.freshOutput != output || !processEvidence.emptyInternalNonNil ||
		processEvidence.emptyOutput != "" || processEvidence.emptyStdoutWrites != 0 {
		return protocol.Observation{}, fmt.Errorf("fresh-process SQL output = %q/%q, want %q/empty", processEvidence.freshOutput, processEvidence.emptyOutput, output)
	}
	observations := []protocol.Value{
		migrationSQLRenderingOutputObservation("first", output),
		migrationSQLRenderingOutputObservation("repeat", migrationSQLRenderingOutput(repeat)),
		migrationSQLRenderingOutputObservation("parallel_a", migrationSQLRenderingOutput(parallel[0])),
		migrationSQLRenderingOutputObservation("parallel_b", migrationSQLRenderingOutput(parallel[1])),
		migrationSQLRenderingOutputObservation("fresh_process", processEvidence.freshOutput),
	}
	distinct := make(map[string]struct{}, len(observations))
	for _, candidate := range []string{output, migrationSQLRenderingOutput(repeat), migrationSQLRenderingOutput(parallel[0]), migrationSQLRenderingOutput(parallel[1]), processEvidence.freshOutput} {
		distinct[candidate] = struct{}{}
	}
	bodyValues := make([]protocol.Value, len(first))
	for index, body := range first {
		bodyValues[index] = protocol.String(body)
	}
	result := protocol.Object(map[string]protocol.Value{
		"bodies": protocol.List(bodyValues...),
		"empty_intent": protocol.Object(map[string]protocol.Value{
			"internal_result": protocol.String(func() string {
				if processEvidence.emptyInternalNonNil {
					return "non_nil_empty"
				}
				return "invalid"
			}()), "output": protocol.String(processEvidence.emptyOutput), "stdout_write_attempts": migrationSQLRenderingInt(processEvidence.emptyStdoutWrites),
		}),
		"exact_statement_cardinality": protocol.Boolean(len(first) == len(fixture.definitions[len(fixture.definitions)-1].Operations)), "global_terminator": protocol.String(";\n"),
		"observations": protocol.List(observations...), "output_owner": protocol.String("global_command"),
		"renderer_bodies_contain_semicolon": protocol.Boolean(false),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"distinct_nonempty_outputs": migrationSQLRenderingInt(len(distinct)), "fresh_process_observations": migrationSQLRenderingInt(1),
		"operations": migrationSQLRenderingInt(len(fixture.definitions[len(fixture.definitions)-1].Operations)), "parallel_observations": migrationSQLRenderingInt(len(parallel)),
		"repeat_observations": migrationSQLRenderingInt(2), "statements": migrationSQLRenderingInt(len(first)),
	})
	return migrationSQLRenderingObservation(contract, result, nil, valuePointer(metrics)), nil
}

func migrationSQLRenderingOutputObservation(name, output string) protocol.Value {
	return protocol.Object(map[string]protocol.Value{"case": protocol.String(name), "output": protocol.String(output)})
}

type migrationSQLRenderingProcessEvidence struct {
	key                       string
	freshOutput               string
	emptyOutput               string
	emptyInternalNonNil       bool
	emptyStdoutWrites         int
	processGroupAbsent        bool
	cancellationReport        productcheck.SQLMigrateReport
	cancellationStdout        []byte
	cancellationStderr        []byte
	runnerPID                 int
	childPID                  int
	externalProjectBuilt      bool
	credentialValuesPublished int
}

var migrationSQLRenderingProcessCache struct {
	sync.Mutex
	evidence *migrationSQLRenderingProcessEvidence
}

func migrationSQLRenderingObserveProcesses(
	ctx context.Context,
	bodies []string,
) (migrationSQLRenderingProcessEvidence, error) {
	if ctx == nil {
		return migrationSQLRenderingProcessEvidence{}, errors.New("migration SQL process observation context is nil")
	}
	if err := ctx.Err(); err != nil {
		return migrationSQLRenderingProcessEvidence{}, err
	}
	key := strings.Join(bodies, "\x00")
	migrationSQLRenderingProcessCache.Lock()
	defer migrationSQLRenderingProcessCache.Unlock()
	if err := ctx.Err(); err != nil {
		return migrationSQLRenderingProcessEvidence{}, err
	}
	if cached := migrationSQLRenderingProcessCache.evidence; cached != nil {
		if cached.key != key {
			return migrationSQLRenderingProcessEvidence{}, errors.New("migration SQL process evidence key changed")
		}
		return migrationSQLRenderingCloneProcessEvidence(*cached), nil
	}
	evidence, err := migrationSQLRenderingRunProcesses(ctx, key, bodies)
	if err != nil {
		return migrationSQLRenderingProcessEvidence{}, err
	}
	stored := migrationSQLRenderingCloneProcessEvidence(evidence)
	migrationSQLRenderingProcessCache.evidence = &stored
	return migrationSQLRenderingCloneProcessEvidence(evidence), nil
}

func migrationSQLRenderingCloneProcessEvidence(
	evidence migrationSQLRenderingProcessEvidence,
) migrationSQLRenderingProcessEvidence {
	evidence.cancellationStdout = append([]byte(nil), evidence.cancellationStdout...)
	evidence.cancellationStderr = append([]byte(nil), evidence.cancellationStderr...)
	return evidence
}

func migrationSQLRenderingRunProcesses(
	ctx context.Context,
	key string,
	bodies []string,
) (result migrationSQLRenderingProcessEvidence, resultErr error) {
	projectFixture, err := newMigrationSQLRenderingActualProject()
	if err != nil {
		return migrationSQLRenderingProcessEvidence{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, projectFixture.close()) }()
	definitionFixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return migrationSQLRenderingProcessEvidence{}, err
	}
	emptyTarget := migrations.MigrationKey{App: "blog", Name: "zero"}
	processDefinitions := append(append([]migrations.Migration(nil), definitionFixture.definitions...), migrations.Migration{
		App: emptyTarget.App, Name: emptyTarget.Name, Dependencies: []migrations.MigrationKey{definitionFixture.target}, Operations: []migrations.Operation{},
	})
	processSources, err := migrationSQLRenderingSources(processDefinitions)
	if err != nil {
		return migrationSQLRenderingProcessEvidence{}, err
	}
	fresh := migrationSQLRenderingRunProcess(
		ctx,
		projectFixture,
		migrationSQLRenderingProcessNormal,
		definitionFixture.target.Name,
		processSources,
	)
	wantOutput := migrationSQLRenderingOutput(bodies)
	if fresh.report.ExitCode != 0 || !fresh.report.HasSQLMigrateResult || fresh.report.HasSQLMigrateFailure ||
		fresh.report.BuildCalls != 1 || fresh.report.RunnerCalls != 1 || fresh.report.DirectChildReaps != 2 ||
		fresh.report.TempCreated != 1 || fresh.report.TempCleanupAttempts != 1 || fresh.report.CleanupFailed != 0 || fresh.report.ResidualTemp != 0 ||
		fresh.report.UserStdoutWrites != 1 || fresh.report.UserStderrWrites != 0 || fresh.report.PartialStdoutWrites != 0 ||
		fresh.stdout.String() != wantOutput || fresh.stderr.Len() != 0 {
		return migrationSQLRenderingProcessEvidence{}, fmt.Errorf("fresh SQL migration process = report:%+v stdout:%q stderr:%q", fresh.report, fresh.stdout.String(), fresh.stderr.String())
	}
	empty := migrationSQLRenderingRunProcess(ctx, projectFixture, migrationSQLRenderingProcessNormal, emptyTarget.Name, processSources)
	if empty.report.ExitCode != 0 || !empty.report.HasSQLMigrateResult || empty.report.HasSQLMigrateFailure ||
		empty.report.SQLMigrateResult.Statements == nil || len(empty.report.SQLMigrateResult.Statements) != 0 ||
		empty.report.BuildCalls != 1 || empty.report.RunnerCalls != 1 || empty.report.DirectChildReaps != 2 ||
		empty.report.TempCreated != 1 || empty.report.TempCleanupAttempts != 1 || empty.report.CleanupFailed != 0 || empty.report.ResidualTemp != 0 ||
		empty.report.UserStdoutWrites != 0 || empty.report.UserStderrWrites != 0 || empty.report.PartialStdoutWrites != 0 ||
		empty.stdout.Len() != 0 || empty.stderr.Len() != 0 {
		return migrationSQLRenderingProcessEvidence{}, fmt.Errorf("empty SQL migration process = report:%+v stdout:%q stderr:%q", empty.report, empty.stdout.String(), empty.stderr.String())
	}

	phaseContext, cancelPhase := context.WithTimeout(ctx, 90*time.Second)
	defer cancelPhase()
	runnerContext, cancelRunner := context.WithCancel(phaseContext)
	done := make(chan migrationSQLRenderingProcessRun, 1)
	go func() {
		done <- migrationSQLRenderingRunProcess(runnerContext, projectFixture, migrationSQLRenderingProcessCancellation, definitionFixture.target.Name, processSources)
	}()
	abort := func(primary error) error {
		cancelRunner()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			primary = errors.Join(primary, errors.New("migration SQL cancellation cleanup timed out"))
		}
		return primary
	}
	runnerPID, err := migrationCommandWaitForActualMarker(phaseContext, projectFixture.trace, "sql-render-cancel-ready")
	if err != nil {
		return migrationSQLRenderingProcessEvidence{}, abort(err)
	}
	marker, err := migrationCommandReadActualFile(projectFixture.trace, "sql-render-cancel-ready", runnerPID)
	if err != nil {
		return migrationSQLRenderingProcessEvidence{}, abort(err)
	}
	fields := strings.Fields(string(marker))
	if len(fields) != 2 {
		return migrationSQLRenderingProcessEvidence{}, abort(errors.New("migration SQL cancellation marker shape is invalid"))
	}
	observedRunner, err := migrationTargetPlanParseActualCount(fields[0], "runner_pid")
	if err != nil || observedRunner != runnerPID {
		return migrationSQLRenderingProcessEvidence{}, abort(errors.New("migration SQL cancellation runner PID is invalid"))
	}
	childPID, err := migrationTargetPlanParseActualCount(fields[1], "child_pid")
	if err != nil || childPID == runnerPID {
		return migrationSQLRenderingProcessEvidence{}, abort(errors.New("migration SQL cancellation child PID is invalid"))
	}
	childMarker, err := migrationCommandReadActualFile(projectFixture.trace, "sql-render-cancel-child", childPID)
	if err != nil || string(childMarker) != "ready" {
		return migrationSQLRenderingProcessEvidence{}, abort(errors.New("migration SQL cancellation child readiness is invalid"))
	}
	cancelRunner()
	var canceled migrationSQLRenderingProcessRun
	select {
	case canceled = <-done:
	case <-phaseContext.Done():
		return migrationSQLRenderingProcessEvidence{}, phaseContext.Err()
	}
	if err := migrationCommandWaitForProcessGroupAbsent(phaseContext, runnerPID); err != nil {
		return migrationSQLRenderingProcessEvidence{}, err
	}
	wantFailure := sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategoryProcess, Code: sqlmigrateprotocol.CodeProjectCanceled}
	if canceled.report.ExitCode != 3 || !canceled.report.HasSQLMigrateFailure || canceled.report.HasSQLMigrateResult ||
		canceled.report.SQLMigrateFailure != wantFailure || canceled.report.BuildCalls != 1 || canceled.report.RunnerCalls != 1 ||
		canceled.report.DirectChildReaps != 2 || canceled.report.CleanupFailed != 0 || canceled.report.ResidualTemp != 0 ||
		canceled.report.UserStdoutWrites != 0 || canceled.report.UserStderrWrites != 1 || canceled.stdout.Len() != 0 ||
		canceled.stderr.String() != sqlmigrateprotocol.CategoryProcess+"/"+sqlmigrateprotocol.CodeProjectCanceled+"\n" {
		return migrationSQLRenderingProcessEvidence{}, fmt.Errorf("canceled SQL migration process = report:%+v stdout:%q stderr:%q", canceled.report, canceled.stdout.String(), canceled.stderr.String())
	}
	if err := migrationCommandAssertActualDirectoryEmpty(projectFixture.workspace); err != nil {
		return migrationSQLRenderingProcessEvidence{}, err
	}
	return migrationSQLRenderingProcessEvidence{
		key: key, freshOutput: fresh.stdout.String(), emptyOutput: empty.stdout.String(),
		emptyInternalNonNil: empty.report.SQLMigrateResult.Statements != nil, emptyStdoutWrites: empty.report.UserStdoutWrites,
		processGroupAbsent: true,
		cancellationReport: canceled.report, cancellationStdout: append([]byte(nil), canceled.stdout.Bytes()...),
		cancellationStderr: append([]byte(nil), canceled.stderr.Bytes()...), runnerPID: runnerPID, childPID: childPID,
		externalProjectBuilt: fresh.report.BuildCalls == 1 && empty.report.BuildCalls == 1 && canceled.report.BuildCalls == 1,
		credentialValuesPublished: migrationSQLRenderingCredentialOccurrences(
			fresh.stdout.Bytes(), fresh.stderr.Bytes(), empty.stdout.Bytes(), empty.stderr.Bytes(), canceled.stdout.Bytes(), canceled.stderr.Bytes(),
		),
	}, nil
}

type migrationSQLRenderingProcessRun struct {
	report      productcheck.SQLMigrateReport
	stdout      bytes.Buffer
	stderr      bytes.Buffer
	environment []string
}

func migrationSQLRenderingRunProcess(
	ctx context.Context,
	projectFixture migrationCommandProject,
	mode string,
	name string,
	sources []definition.Source,
) migrationSQLRenderingProcessRun {
	environment := migrationSQLRenderingProcessEnvironment(os.Environ())
	environment["DATABASE_URL"] = "postgres://godj:" + migrationSQLRenderingCredentialCanary + "@127.0.0.1:1/godj"
	environment["GODJ_TEST_POSTGRES_URL"] = environment["DATABASE_URL"]
	environment["PGPASSWORD"] = migrationSQLRenderingCredentialCanary
	environment[migrationSQLRenderingProcessMode] = mode
	environment[migrationSQLRenderingProcessTrace] = projectFixture.trace
	for index, source := range sources {
		environment[migrationSQLRenderingProcessSourcePrefix+strconv.Itoa(index)] = base64.StdEncoding.EncodeToString(source.Document)
	}
	environment["TMPDIR"] = projectFixture.workspace
	environment["GOWORK"] = "off"
	environment["GOTOOLCHAIN"] = "local"
	environment["GOENV"] = "off"
	environment["GOFLAGS"] = ""
	environment["GOCACHEPROG"] = ""
	environment["GOPROXY"] = "off"
	entries := migrationCommandSortedEnvironment(environment)
	phaseContext, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	report := productcheck.RunSQLMigrate(productcheck.SQLMigrateInvocation{
		Context: phaseContext, CWD: projectFixture.root, Args: []string{"sqlmigrate", "blog", name},
		Environment: entries, Stdout: &stdout, Stderr: &stderr,
	})
	return migrationSQLRenderingProcessRun{
		report: report, stdout: stdout, stderr: stderr, environment: append([]string(nil), entries...),
	}
}

func migrationSQLRenderingProcessEnvironment(entries []string) map[string]string {
	ambient := migrationCommandActualEnvironment(entries)
	values := make(map[string]string, 8)
	for _, key := range []string{"PATH", "HOME", "GOMODCACHE", "GOPATH"} {
		if value := ambient[key]; value != "" {
			values[key] = value
		}
	}
	return values
}

func migrationSQLRenderingCredentialOccurrences(documents ...[]byte) int {
	count := 0
	for _, document := range documents {
		count += bytes.Count(document, []byte(migrationSQLRenderingCredentialCanary))
	}
	return count
}

func newMigrationSQLRenderingActualProject() (migrationCommandProject, error) {
	projectFixture, err := newMigrationCommandProject()
	if err != nil {
		return migrationCommandProject{}, err
	}
	fail := func(primary error) (migrationCommandProject, error) {
		return migrationCommandProject{}, errors.Join(primary, projectFixture.close())
	}
	repository, err := systemStateRepositoryRoot()
	if err != nil {
		return fail(err)
	}
	trace := filepath.Join(projectFixture.universe, "trace")
	command := filepath.Join(projectFixture.root, "cmd", "site")
	for _, directory := range []string{trace, command} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fail(errors.New("create migration SQL actual fixture directory"))
		}
	}
	rootModule, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		return fail(errors.New("read migration SQL repository module"))
	}
	const declaration = "module github.com/progresshans/godj\n"
	if !bytes.HasPrefix(rootModule, []byte(declaration)) || bytes.Count(rootModule, []byte(declaration)) != 1 {
		return fail(errors.New("migration SQL repository module declaration is unexpected"))
	}
	module := strings.Replace(string(rootModule), declaration, "module example.com/godj-sql-rendering-fixture\n", 1)
	module += fmt.Sprintf(
		"\nrequire github.com/progresshans/godj v0.0.0\n\nreplace github.com/progresshans/godj => %s\n",
		strconv.Quote(filepath.ToSlash(repository)),
	)
	if err := writeMigrationCommandActualFile(filepath.Join(projectFixture.root, "go.mod"), []byte(module)); err != nil {
		return fail(err)
	}
	rootSum, err := os.ReadFile(filepath.Join(repository, "go.sum"))
	if err != nil {
		return fail(errors.New("read migration SQL repository sum"))
	}
	if err := writeMigrationCommandActualFile(filepath.Join(projectFixture.root, "go.sum"), rootSum); err != nil {
		return fail(err)
	}
	if err := writeMigrationCommandActualFile(filepath.Join(command, "main.go"), []byte(migrationSQLRenderingActualRunnerSource)); err != nil {
		return fail(err)
	}
	projectFixture.trace = trace
	return projectFixture, nil
}

const migrationSQLRenderingActualRunnerSource = `package main

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"

	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/project"
)

const (
	modeEnvironment = "GODJ_SQL_RENDERING_ACTUAL_MODE"
	traceEnvironment = "GODJ_SQL_RENDERING_ACTUAL_TRACE"
	sourceEnvironmentPrefix = "GODJ_SQL_RENDERING_ACTUAL_SOURCE_"
)

func main() {
	_ = project.Config{MigrationSQLRenderer: sqlite.NewMigrationSQLRenderer()}
	_ = project.Config{MigrationSQLRenderer: postgres.NewMigrationSQLRenderer(postgres.MigrationSQLConfig{Schema: "public"})}
	switch os.Getenv(modeEnvironment) {
	case "normal":
		runProject()
	case "cancellation":
		blockForCancellation()
	default:
		os.Exit(2)
	}
}

func runProject() {
	identifiers := []string{
		"sql-rendering/00_authors_0001_initial.godj.json",
		"sql-rendering/01_blog_0001_initial.godj.json",
		"sql-rendering/02_blog_0002_render_sql.godj.json",
		"sql-rendering/03_blog_zero.godj.json",
	}
	sources := make([]definition.Source, len(identifiers))
	for index, identifier := range identifiers {
		document, err := base64.StdEncoding.DecodeString(os.Getenv(sourceEnvironmentPrefix + strconv.Itoa(index)))
		if err != nil {
			os.Exit(3)
		}
		sources[index] = definition.Source{SourceID: identifier, Document: document}
	}
	if err := project.Run(
		context.Background(),
		project.Config{MigrationDefinitionSources: sources, MigrationSQLRenderer: sqlite.NewMigrationSQLRenderer()},
		os.Args[1:],
		os.Stdin,
		os.Stdout,
	); err != nil {
		os.Exit(4)
	}
}

func blockForCancellation() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	if len(os.Args) == 2 && os.Args[1] == "descendant" {
		writeMarker("sql-render-cancel-child", "ready")
		<-signals
		return
	}
	child := exec.Command(os.Args[0], "descendant")
	child.Env = os.Environ()
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(5)
	}
	childMarker := filepath.Join(os.Getenv(traceEnvironment), "sql-render-cancel-child-"+strconv.Itoa(child.Process.Pid))
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(childMarker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(6)
		}
		time.Sleep(5 * time.Millisecond)
	}
	writeMarker("sql-render-cancel-ready", "runner_pid="+strconv.Itoa(os.Getpid())+" child_pid="+strconv.Itoa(child.Process.Pid))
	<-signals
}

func writeMarker(prefix, value string) {
	path := filepath.Join(os.Getenv(traceEnvironment), prefix+"-"+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		os.Exit(7)
	}
}
`

func migrationSQLRenderingNoDatabase(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return protocol.Observation{}, err
	}
	request, err := sqlmigrateprotocol.EncodeRequest(sqlmigrateprotocol.Request{App: fixture.target.App, Name: fixture.target.Name})
	if err != nil {
		return protocol.Observation{}, err
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		return protocol.Observation{}, err
	}
	run := func(runContext context.Context, renderer migrationbackend.MigrationSQLRenderer) (linked.Report, []byte, error) {
		var response bytes.Buffer
		report, runErr := linked.RunSQLMigrate(
			runContext,
			linked.SQLMigrateConfig{
				ProjectRoot: projectRoot, MigrationDefinitionSources: fixture.sources, MigrationSQLRenderer: renderer,
			},
			[]string{sqlmigrateprotocol.PrivateArgument},
			bytes.NewReader(request),
			&response,
		)
		return report, append([]byte(nil), response.Bytes()...), runErr
	}
	successReport, successWire, err := run(ctx, sqlite.NewMigrationSQLRenderer())
	if err != nil {
		return protocol.Observation{}, err
	}
	successResponse, successFailure, successFailed := sqlmigrateprotocol.ParseResponse(successWire, true)
	if successFailed || successFailure != (sqlmigrateprotocol.Failure{}) || !successResponse.OK || successReport.RunnerResponseWrites != 1 {
		return protocol.Observation{}, fmt.Errorf("SQL migration no-DB success = report:%+v failure:%+v failed:%t", successReport, successFailure, successFailed)
	}
	failureReport, failureWire, err := run(ctx, &migrationSQLRenderingSpy{err: errors.New(migrationSQLRenderingSecret)})
	if err != nil {
		return protocol.Observation{}, err
	}
	failureResponse, parseFailure, parseFailed := sqlmigrateprotocol.ParseResponse(failureWire, true)
	if parseFailed || parseFailure != (sqlmigrateprotocol.Failure{}) || failureResponse.OK ||
		failureResponse.Failure != (sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategorySQLRender, Code: sqlmigrateprotocol.CodeRenderFailed}) ||
		failureReport.RunnerResponseWrites != 1 || bytes.Contains(failureWire, []byte(migrationSQLRenderingSecret)) {
		return protocol.Observation{}, fmt.Errorf("SQL migration no-DB failure = report:%+v response:%+v parse:%+v/%t", failureReport, failureResponse, parseFailure, parseFailed)
	}
	canceledContext, cancel := context.WithCancel(ctx)
	cancel()
	canceledReport, canceledWire, canceledErr := run(canceledContext, sqlite.NewMigrationSQLRenderer())
	if !errors.Is(canceledErr, context.Canceled) || len(canceledWire) != 0 || !reflect.DeepEqual(canceledReport, linked.Report{}) {
		return protocol.Observation{}, fmt.Errorf("SQL migration no-DB cancellation = report:%+v wire:%d err:%v", canceledReport, len(canceledWire), canceledErr)
	}
	bodies, err := migrationSQLRenderingDecisionBodies(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	processEvidence, err := migrationSQLRenderingObserveProcesses(ctx, bodies)
	if err != nil {
		return protocol.Observation{}, err
	}
	if !processEvidence.processGroupAbsent || processEvidence.cancellationReport.HasSQLMigrateResult || len(processEvidence.cancellationStdout) != 0 {
		return protocol.Observation{}, errors.New("SQL migration no-DB process cancellation did not close")
	}
	successCounts, err := migrationSQLRenderingLifecycleCountsFromReport(successReport)
	if err != nil {
		return protocol.Observation{}, err
	}
	failureCounts, err := migrationSQLRenderingLifecycleCountsFromReport(failureReport)
	if err != nil {
		return protocol.Observation{}, err
	}
	canceledCounts, err := migrationSQLRenderingLifecycleCountsFromReport(canceledReport)
	if err != nil {
		return protocol.Observation{}, err
	}
	cases := []protocol.Value{
		migrationSQLRenderingZeroCallCase("success", "success", successCounts),
		migrationSQLRenderingZeroCallCase("render_failure", "error", failureCounts),
		migrationSQLRenderingZeroCallCase("canceled", "error", canceledCounts),
	}
	counts := successCounts.add(failureCounts).add(canceledCounts)
	result := protocol.Object(map[string]protocol.Value{
		"built_in_db_free_scope": protocol.String("framework_and_builtin_renderer_database_lifecycle_only"),
		"cases":                  protocol.List(cases...), "custom_renderer_io_is_proven_absent": protocol.Boolean(false),
		"offline_or_sandboxed_claimed": protocol.Boolean(false),
	})
	dbState := migrationSQLRenderingDatabaseState(true)
	metrics := protocol.Object(map[string]protocol.Value{
		"backend_opens": migrationSQLRenderingInt(counts.backendOpens), "cases": migrationSQLRenderingInt(len(cases)), "history_reads": migrationSQLRenderingInt(counts.historyReads),
		"migration_begins": migrationSQLRenderingInt(counts.migrationBegins), "recorder_calls": migrationSQLRenderingInt(counts.recorderCalls), "revision_fence_calls": migrationSQLRenderingInt(counts.revisionFenceCalls),
		"schema_editor_calls": migrationSQLRenderingInt(counts.schemaEditorCalls), "schema_mutations": migrationSQLRenderingInt(counts.schemaMutations), "session_opens": migrationSQLRenderingInt(counts.sessionOpens),
		"transaction_begins": migrationSQLRenderingInt(counts.transactionBegins),
	})
	return migrationSQLRenderingObservation(contract, result, valuePointer(dbState), valuePointer(metrics)), nil
}

type migrationSQLRenderingLifecycleCounts struct {
	backendOpens, historyReads, migrationBegins, recorderCalls, revisionFenceCalls int
	schemaEditorCalls, schemaMutations, sessionOpens, transactionBegins            int
}

func migrationSQLRenderingLifecycleCountsFromReport(report linked.Report) (migrationSQLRenderingLifecycleCounts, error) {
	counts := migrationSQLRenderingLifecycleCounts{
		backendOpens:       report.BackendOpenCalls,
		historyReads:       report.AppliedHistoryReads,
		migrationBegins:    report.GoDjDBCalls,
		recorderCalls:      report.GoDjDBCalls,
		revisionFenceCalls: report.RevisionLifecycleCalls,
		schemaEditorCalls:  report.GoDjDBCalls,
		schemaMutations:    report.GoDjDBCalls,
		sessionOpens:       report.RevisionSessionOpens,
		transactionBegins:  report.GoDjDBCalls,
	}
	if report.BackendCloseCalls != 0 || report.RevisionSessionCloses != 0 ||
		counts != (migrationSQLRenderingLifecycleCounts{}) {
		return migrationSQLRenderingLifecycleCounts{}, fmt.Errorf("SQL migration crossed a database lifecycle boundary: %+v", report)
	}
	return counts, nil
}

func (counts migrationSQLRenderingLifecycleCounts) add(other migrationSQLRenderingLifecycleCounts) migrationSQLRenderingLifecycleCounts {
	return migrationSQLRenderingLifecycleCounts{
		backendOpens: counts.backendOpens + other.backendOpens, historyReads: counts.historyReads + other.historyReads,
		migrationBegins: counts.migrationBegins + other.migrationBegins, recorderCalls: counts.recorderCalls + other.recorderCalls,
		revisionFenceCalls: counts.revisionFenceCalls + other.revisionFenceCalls,
		schemaEditorCalls:  counts.schemaEditorCalls + other.schemaEditorCalls, schemaMutations: counts.schemaMutations + other.schemaMutations,
		sessionOpens: counts.sessionOpens + other.sessionOpens, transactionBegins: counts.transactionBegins + other.transactionBegins,
	}
}

func migrationSQLRenderingZeroCallCase(name, outcome string, counts migrationSQLRenderingLifecycleCounts) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"backend_opens": migrationSQLRenderingInt(counts.backendOpens), "case": protocol.String(name), "history_reads": migrationSQLRenderingInt(counts.historyReads),
		"migration_begins": migrationSQLRenderingInt(counts.migrationBegins), "outcome": protocol.String(outcome), "recorder_calls": migrationSQLRenderingInt(counts.recorderCalls),
		"revision_fence_calls": migrationSQLRenderingInt(counts.revisionFenceCalls), "schema_editor_calls": migrationSQLRenderingInt(counts.schemaEditorCalls),
		"schema_mutations": migrationSQLRenderingInt(counts.schemaMutations), "session_opens": migrationSQLRenderingInt(counts.sessionOpens), "transaction_begins": migrationSQLRenderingInt(counts.transactionBegins),
	})
}

func migrationSQLRenderingDatabaseState(includeRecorder bool) protocol.Value {
	state := func() protocol.Value {
		fields := map[string]protocol.Value{
			"database": protocol.String("not_opened"), "history": protocol.String("not_read"), "schema": protocol.String("not_observed"),
		}
		if includeRecorder {
			fields["recorder"] = protocol.String("not_observed")
		}
		return protocol.Object(fields)
	}
	return protocol.Object(map[string]protocol.Value{"after": state(), "before": state()})
}

type migrationSQLRenderingTypedNilRenderer struct{ calls int }

func (renderer *migrationSQLRenderingTypedNilRenderer) RenderForwardMigrationSQL(
	context.Context,
	migrationbackend.ForwardMigrationSQLRequest,
) ([]string, error) {
	renderer.calls++
	return nil, errors.New("typed-nil renderer was called")
}

type migrationSQLRenderingFailureCase struct {
	name                       string
	renderer                   migrationbackend.MigrationSQLRenderer
	wantCategory               migrations.ErrorCategory
	wantCode                   migrations.ErrorCode
	wantCalls                  int
	partialRendererSQLReturned bool
	rawCauseContainsSecret     bool
}

func migrationSQLRenderingFailures(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return protocol.Observation{}, err
	}
	var typedNil *migrationSQLRenderingTypedNilRenderer
	unsupportedBuiltin := sqlite.NewMigrationSQLRenderer()
	for _, kind := range []migrationbackend.MigrationOperationKind{migrationbackend.MigrationRemoveField, migrationbackend.MigrationOperationKind(255)} {
		statements, renderErr := unsupportedBuiltin.RenderForwardMigrationSQL(ctx, migrationbackend.ForwardMigrationSQLRequest{
			App: "blog", Name: "0002_unsupported",
			Intent: migrationbackend.MigrationIntent{Operations: []migrationbackend.MigrationOperation{{OperationIndex: 0, Kind: kind}}},
		})
		if statements != nil || renderErr == nil || !migrationbackend.IsCapabilityError(renderErr) {
			return protocol.Observation{}, fmt.Errorf("built-in unsupported operation %d = %#v, %v", kind, statements, renderErr)
		}
	}
	capability := func(feature string) migrationbackend.MigrationSQLRenderer {
		return &migrationSQLRenderingSpy{err: migrationbackend.NewCapabilityError(feature, migrationSQLRenderingSecret, errors.New(migrationSQLRenderingSecret))}
	}
	partialFailure := &migrationSQLRenderingSpy{
		statements: []string{"SELECT 'partial-secret'", "SELECT 2"}, err: errors.New(migrationSQLRenderingSecret),
	}
	cases := []migrationSQLRenderingFailureCase{
		{name: "nil_renderer", renderer: nil, wantCategory: migrations.CategorySQLRender, wantCode: migrations.CodeRendererUnavailable},
		{name: "typed_nil_renderer", renderer: typedNil, wantCategory: migrations.CategorySQLRender, wantCode: migrations.CodeRendererUnavailable},
		{name: "unsupported_operation", renderer: capability("unsupported_operation"), wantCategory: migrations.CategoryCapability, wantCode: migrations.CodeUnsupported, wantCalls: 1},
		{name: "custom_data_operation", renderer: capability("custom_data_operation"), wantCategory: migrations.CategoryCapability, wantCode: migrations.CodeUnsupported, wantCalls: 1},
		{name: "renderer_returned_error", renderer: partialFailure, wantCategory: migrations.CategorySQLRender, wantCode: migrations.CodeRenderFailed, wantCalls: 1, partialRendererSQLReturned: true, rawCauseContainsSecret: true},
		{name: "malformed_empty_body", renderer: &migrationSQLRenderingSpy{statements: []string{"", "SELECT 2"}}, wantCategory: migrations.CategorySQLRender, wantCode: migrations.CodeInvalidRenderedSQL, wantCalls: 1},
		{name: "malformed_invalid_utf8_body", renderer: &migrationSQLRenderingSpy{statements: []string{string([]byte{0xff}), "SELECT 2"}}, wantCategory: migrations.CategorySQLRender, wantCode: migrations.CodeInvalidRenderedSQL, wantCalls: 1},
		{name: "malformed_leading_ascii_whitespace_body", renderer: &migrationSQLRenderingSpy{statements: []string{" SELECT 1", "SELECT 2"}}, wantCategory: migrations.CategorySQLRender, wantCode: migrations.CodeInvalidRenderedSQL, wantCalls: 1},
		{name: "malformed_trailing_ascii_whitespace_body", renderer: &migrationSQLRenderingSpy{statements: []string{"SELECT 1 ", "SELECT 2"}}, wantCategory: migrations.CategorySQLRender, wantCode: migrations.CodeInvalidRenderedSQL, wantCalls: 1},
		{name: "malformed_semicolon_body", renderer: &migrationSQLRenderingSpy{statements: []string{"SELECT 1;", "SELECT 2"}}, wantCategory: migrations.CategorySQLRender, wantCode: migrations.CodeInvalidRenderedSQL, wantCalls: 1},
		{name: "malformed_control_rune_body", renderer: &migrationSQLRenderingSpy{statements: []string{"SELECT\t1", "SELECT 2"}}, wantCategory: migrations.CategorySQLRender, wantCode: migrations.CodeInvalidRenderedSQL, wantCalls: 1},
		{name: "malformed_cardinality", renderer: &migrationSQLRenderingSpy{statements: []string{"SELECT 1"}}, wantCategory: migrations.CategorySQLRender, wantCode: migrations.CodeInvalidRenderedSQL, wantCalls: 1},
	}
	values := make([]protocol.Value, len(cases))
	totalCalls := 0
	for index, candidate := range cases {
		statements, renderErr := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, candidate.renderer)
		var sqlError *migrations.MigrationSQLError
		if statements != nil || !errors.As(renderErr, &sqlError) || sqlError == nil ||
			sqlError.Category != candidate.wantCategory || sqlError.Code != candidate.wantCode {
			return protocol.Observation{}, fmt.Errorf("SQL rendering failure case %s = statements:%#v error:%v", candidate.name, statements, renderErr)
		}
		calls := migrationSQLRenderingRendererCalls(candidate.renderer)
		if calls != candidate.wantCalls {
			return protocol.Observation{}, fmt.Errorf("SQL rendering failure case %s calls=%d, want %d", candidate.name, calls, candidate.wantCalls)
		}
		totalCalls += calls
		failure := sqlmigrateprotocol.Failure{Category: string(sqlError.Category), Code: string(sqlError.Code)}
		exitCode, ok := sqlmigrateprotocol.ExitCode(failure)
		if !ok {
			return protocol.Observation{}, fmt.Errorf("SQL rendering failure case %s has unmapped taxonomy %+v", candidate.name, failure)
		}
		rawRetained := strings.Contains(renderErr.Error(), migrationSQLRenderingSecret) || errors.Unwrap(renderErr) != nil
		if rawRetained {
			return protocol.Observation{}, fmt.Errorf("SQL rendering failure case %s retained raw cause", candidate.name)
		}
		values[index] = protocol.Object(map[string]protocol.Value{
			"case": protocol.String(candidate.name), "category": protocol.String(string(sqlError.Category)), "code": protocol.String(string(sqlError.Code)),
			"exit_code": migrationSQLRenderingInt(exitCode), "logical_sql_bytes_published": migrationSQLRenderingInt(0),
			"partial_renderer_sql_published": protocol.Boolean(false), "partial_renderer_sql_returned": protocol.Boolean(candidate.partialRendererSQLReturned),
			"raw_cause_contains_secret": protocol.Boolean(candidate.rawCauseContainsSecret), "raw_cause_retained": protocol.Boolean(rawRetained),
			"renderer_calls": migrationSQLRenderingInt(calls), "unwrap_exposes_raw_cause": protocol.Boolean(errors.Unwrap(renderErr) != nil),
		})
	}
	if typedNil != nil {
		return protocol.Observation{}, errors.New("typed-nil renderer pointer changed")
	}
	result := protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(values...), "reverse_argv_owned_by": protocol.String("MIG-129"),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"cases": migrationSQLRenderingInt(len(cases)), "logical_sql_bytes_published": migrationSQLRenderingInt(0),
		"renderer_calls": migrationSQLRenderingInt(totalCalls), "typed_nil_method_calls": migrationSQLRenderingInt(0),
	})
	return migrationSQLRenderingObservation(contract, result, nil, valuePointer(metrics)), nil
}

func migrationSQLRenderingRendererCalls(renderer migrationbackend.MigrationSQLRenderer) int {
	switch value := renderer.(type) {
	case *migrationSQLRenderingSpy:
		if value == nil {
			return 0
		}
		calls, _ := value.snapshot()
		return calls
	case *migrationSQLRenderingTypedNilRenderer:
		if value == nil {
			return 0
		}
		return value.calls
	default:
		return 0
	}
}

type migrationSQLRenderingProcessBackend struct {
	wire  []byte
	calls int
}

func (backend *migrationSQLRenderingProcessBackend) Execute(
	_ context.Context,
	_ <-chan struct{},
	stage productcheck.ProcessStage,
	_ productcheck.Command,
) productcheck.ProcessResult {
	backend.calls++
	switch stage {
	case productcheck.BuildStage:
		return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1}
	case productcheck.SQLMigrateRunnerStage:
		wire := append([]byte(nil), backend.wire...)
		return productcheck.ProcessResult{
			Started: true, ExitCode: 0, DirectReaps: 1, Stdout: wire,
			StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(wire)},
		}
	default:
		return productcheck.ProcessResult{}
	}
}

type migrationSQLRenderingWriter struct {
	mode  string
	calls int
	bytes int
}

func (writer *migrationSQLRenderingWriter) Write(document []byte) (int, error) {
	writer.calls++
	switch writer.mode {
	case "success":
		writer.bytes += len(document)
		return len(document), nil
	case "short":
		written := len(document) / 2
		if written == 0 && len(document) > 0 {
			written = 1
		}
		writer.bytes += written
		return written, nil
	case "error":
		return 0, errors.New("terminal write failed")
	default:
		return 0, errors.New("unknown writer mode")
	}
}

var _ io.Writer = (*migrationSQLRenderingWriter)(nil)

func migrationSQLRenderingResources(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	resourceCases, oneOverRejections, resourceBeforeSemantic, err := migrationSQLRenderingResourceCases(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	bodies, err := migrationSQLRenderingDecisionBodies(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	processEvidence, err := migrationSQLRenderingObserveProcesses(ctx, bodies)
	if err != nil {
		return protocol.Observation{}, err
	}
	if !processEvidence.processGroupAbsent || processEvidence.runnerPID <= 0 || processEvidence.childPID <= 0 ||
		processEvidence.runnerPID == processEvidence.childPID || processEvidence.cancellationReport.CleanupFailed != 0 ||
		processEvidence.cancellationReport.ResidualTemp != 0 || processEvidence.credentialValuesPublished != 0 {
		return protocol.Observation{}, fmt.Errorf("migration SQL process cleanup evidence = %+v", processEvidence)
	}
	publication, err := migrationSQLRenderingProbeRedactionAndPublication(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	wantWriterCalls := map[string]int{
		"linked_renderer_private": 1, "linked_definition_private": 1,
		"global_sql_stdout": 0, "global_diagnostic_stderr": 1,
	}
	if publication.LinkedRendererFailure != (sqlmigrateprotocol.Failure{Category: string(migrations.CategorySQLRender), Code: string(migrations.CodeRenderFailed)}) ||
		publication.LinkedDefinitionFailure.Category != sqlmigrateprotocol.CategorySource ||
		!sqlmigrateprotocol.IsLinkedFailure(publication.LinkedDefinitionFailure) ||
		publication.GlobalFailure != (sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategoryProtocol, Code: sqlmigrateprotocol.CodeInvalidResponse}) ||
		publication.LinkedRendererCalls != 1 || publication.GlobalBuildCalls != 1 || publication.GlobalRunnerCalls != 1 ||
		publication.GlobalDirectChildReaps != 2 || publication.GlobalRunnerStderrBytes != len(migrationSQLRenderingProbeChildStderrCanary) ||
		publication.GlobalCleanupAttempts != 1 || publication.GlobalCleanupFailures != 0 || publication.GlobalResidualTemp != 0 ||
		!publication.GlobalRawDiagnosticsDropped || !publication.CredentialObservedByChild || !publication.ValidatedBeforeSQLWrite ||
		!reflect.DeepEqual(publication.LogicalWriterCalls, wantWriterCalls) {
		return protocol.Observation{}, fmt.Errorf("migration SQL redaction/publication evidence is incomplete: %+v", publication)
	}
	writeCases, writeAttempts, stderrRepublications, err := migrationSQLRenderingWriteCases(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	redactionFields := []struct{ field, evidence string }{
		{field: "raw_renderer_error", evidence: "renderer_cause"},
		{field: "partial_sql", evidence: "partial_sql"},
		{field: "definition_source", evidence: "definition_source"},
		{field: "database_url_or_credential", evidence: "credential_value"},
		{field: "child_stderr", evidence: "child_stderr"},
	}
	redaction := make([]protocol.Value, len(redactionFields))
	redactedFieldsPublished := 0
	for index, field := range redactionFields {
		occurrences, exists := publication.PublishedOccurrences[field.evidence]
		if !exists {
			return protocol.Observation{}, fmt.Errorf("migration SQL redaction evidence omitted %q", field.evidence)
		}
		redactedFieldsPublished += occurrences
		redaction[index] = migrationSQLRenderingRedactionValue(field.field, occurrences != 0)
	}
	if len(publication.PublishedOccurrences) != len(redactionFields) {
		return protocol.Observation{}, fmt.Errorf("migration SQL redaction evidence has %d fields, want %d", len(publication.PublishedOccurrences), len(redactionFields))
	}
	result := protocol.Object(map[string]protocol.Value{
		"child_cleanup": protocol.Object(map[string]protocol.Value{
			"bounded": protocol.Boolean(
				processEvidence.cancellationReport.TempCleanupAttempts == 1 && publication.GlobalCleanupAttempts == 1,
			),
			"process_group_absence_verified": protocol.Boolean(processEvidence.processGroupAbsent),
		}),
		"logical_output_validated_before_write": protocol.Boolean(publication.ValidatedBeforeSQLWrite), "os_atomic_write_claimed": protocol.Boolean(false),
		"redaction": protocol.List(redaction...), "resource_cases": protocol.List(resourceCases...),
		"scan_order": func() protocol.Value {
			if resourceBeforeSemantic {
				return migrationSQLRenderingStrings("resource_bounds", "semantic_shape")
			}
			return protocol.List()
		}(), "write_cases": protocol.List(writeCases...),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"automatic_retries": migrationSQLRenderingInt(0), "cleanup_failures": migrationSQLRenderingInt(processEvidence.cancellationReport.CleanupFailed + publication.GlobalCleanupFailures),
		"one_over_rejections": migrationSQLRenderingInt(oneOverRejections), "redacted_fields_published": migrationSQLRenderingInt(redactedFieldsPublished),
		"stderr_republications": migrationSQLRenderingInt(stderrRepublications), "write_attempts": migrationSQLRenderingInt(writeAttempts),
	})
	return migrationSQLRenderingObservation(contract, result, nil, valuePointer(metrics)), nil
}

func migrationSQLRenderingResourceCases(ctx context.Context) ([]protocol.Value, int, bool, error) {
	rootCases, err := migrationSQLRenderingProbeRootResources(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	privateCases, err := migrationSQLRenderingProbePrivateResponseResources()
	if err != nil {
		return nil, 0, false, err
	}
	cases := append(rootCases, privateCases...)
	want := []struct {
		name            string
		limit, observed int
		accepted        bool
	}{
		{name: "statement_count_exact_limit", limit: sqlmigrateprotocol.MaxStatements, observed: sqlmigrateprotocol.MaxStatements, accepted: true},
		{name: "statement_count_one_over", limit: sqlmigrateprotocol.MaxStatements, observed: sqlmigrateprotocol.MaxStatements + 1},
		{name: "aggregate_body_bytes_exact_limit", limit: sqlmigrateprotocol.MaxStatementBodyBytes, observed: sqlmigrateprotocol.MaxStatementBodyBytes, accepted: true},
		{name: "aggregate_body_bytes_one_over", limit: sqlmigrateprotocol.MaxStatementBodyBytes, observed: sqlmigrateprotocol.MaxStatementBodyBytes + 1},
		{name: "private_response_exact_limit", limit: sqlmigrateprotocol.MaxResponseBytes, observed: sqlmigrateprotocol.MaxResponseBytes, accepted: true},
		{name: "private_response_one_over", limit: sqlmigrateprotocol.MaxResponseBytes, observed: sqlmigrateprotocol.MaxResponseBytes + 1},
	}
	if len(cases) != len(want) {
		return nil, 0, false, fmt.Errorf("migration SQL resource probe produced %d cases, want %d", len(cases), len(want))
	}
	values := make([]protocol.Value, len(cases))
	oneOverRejections := 0
	resourceBeforeSemantic := true
	for index, candidate := range cases {
		expected := want[index]
		if candidate.Case != expected.name || candidate.Limit != expected.limit || candidate.Observed != expected.observed ||
			candidate.ResourceLimitAccepted != expected.accepted {
			return nil, 0, false, fmt.Errorf("migration SQL resource case %d = %+v, want %+v", index, candidate, expected)
		}
		if candidate.ResourceLimitAccepted {
			if index < len(rootCases) && !candidate.Succeeded {
				return nil, 0, false, fmt.Errorf("migration SQL root exact resource case did not succeed: %+v", candidate)
			}
			if index >= len(rootCases) && (candidate.Category != sqlmigrateprotocol.CategoryProtocol || candidate.Code != sqlmigrateprotocol.CodeInvalidResponse) {
				return nil, 0, false, fmt.Errorf("migration SQL private exact resource case lost semantic failure: %+v", candidate)
			}
		} else {
			oneOverRejections++
			resourceBeforeSemantic = resourceBeforeSemantic && candidate.ResourceBeforeSemantic && candidate.MalformedPayload
			if candidate.Succeeded || candidate.Category != sqlmigrateprotocol.CategorySQLResource ||
				candidate.Code != sqlmigrateprotocol.CodeRenderedSQLResourceLimit {
				return nil, 0, false, fmt.Errorf("migration SQL one-over resource case lost taxonomy: %+v", candidate)
			}
		}
		values[index] = migrationSQLRenderingResourceValue(candidate.Case, candidate.Limit, candidate.Observed, candidate.ResourceLimitAccepted)
	}
	return values, oneOverRejections, resourceBeforeSemantic, nil
}

func migrationSQLRenderingResourceValue(name string, limit, observed int, accepted bool) protocol.Value {
	fields := map[string]protocol.Value{
		"accepted": protocol.Boolean(accepted), "case": protocol.String(name), "limit": migrationSQLRenderingInt(limit),
		"observed": migrationSQLRenderingInt(observed),
	}
	if !accepted {
		fields["category"] = protocol.String(sqlmigrateprotocol.CategorySQLResource)
		fields["code"] = protocol.String(sqlmigrateprotocol.CodeRenderedSQLResourceLimit)
	}
	return protocol.Object(fields)
}

func migrationSQLRenderingWriteCases(ctx context.Context) ([]protocol.Value, int, int, error) {
	projectFixture, err := newMigrationCommandProject()
	if err != nil {
		return nil, 0, 0, err
	}
	defer projectFixture.close()
	wire, err := sqlmigrateprotocol.EncodeResponse(sqlmigrateprotocol.Response{
		OK: true, Result: sqlmigrateprotocol.Result{Statements: []string{"SELECT 1"}},
	})
	if err != nil {
		return nil, 0, 0, err
	}
	tests := []struct {
		name         string
		mode         string
		prefixMayBe  bool
		wantExit     int
		wantPartial  int
		wantFailures bool
	}{
		{name: "success", mode: "success", wantExit: 0},
		{name: "short_write", mode: "short", prefixMayBe: true, wantExit: 3, wantPartial: 1, wantFailures: true},
		{name: "write_error", mode: "error", prefixMayBe: true, wantExit: 3, wantFailures: true},
	}
	values := make([]protocol.Value, len(tests))
	totalAttempts, totalStderr := 0, 0
	for index, test := range tests {
		writer := &migrationSQLRenderingWriter{mode: test.mode}
		backend := &migrationSQLRenderingProcessBackend{wire: wire}
		var stderr bytes.Buffer
		report := productcheck.RunSQLMigrate(productcheck.SQLMigrateInvocation{
			Context: ctx, CWD: projectFixture.root, Args: []string{"sqlmigrate", "blog", "0002_render_sql"},
			Environment: []string{
				"HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH"), "TMPDIR=" + projectFixture.workspace,
				"GOWORK=off", "GOTOOLCHAIN=local",
			},
			Stdout: writer, Stderr: &stderr, Backend: backend,
		})
		if report.ExitCode != test.wantExit || report.UserStdoutWrites != 1 || report.PartialStdoutWrites != test.wantPartial ||
			report.UserStderrWrites != 0 || report.HasSQLMigrateFailure != test.wantFailures || backend.calls != 2 || writer.calls != 1 || stderr.Len() != 0 {
			return nil, 0, 0, fmt.Errorf("SQL migration terminal write %s = report:%+v backend:%d writer:%d stderr:%q", test.name, report, backend.calls, writer.calls, stderr.String())
		}
		totalAttempts += writer.calls
		totalStderr += report.UserStderrWrites
		values[index] = protocol.Object(map[string]protocol.Value{
			"case": protocol.String(test.name), "physical_prefix_may_be_visible": protocol.Boolean(test.prefixMayBe),
			"retries": migrationSQLRenderingInt(0), "stderr_republications": migrationSQLRenderingInt(report.UserStderrWrites),
			"write_attempts": migrationSQLRenderingInt(writer.calls),
		})
	}
	return values, totalAttempts, totalStderr, nil
}

func migrationSQLRenderingRedactionValue(field string, published bool) protocol.Value {
	return protocol.Object(map[string]protocol.Value{"field": protocol.String(field), "published": protocol.Boolean(published)})
}

type migrationSQLRenderingSelectionEvidence struct {
	coherent            bool
	backendOpens        int
	networkAttempts     int
	credentialPublished int
}

type migrationSQLRenderingPoisonNetwork struct {
	listener net.Listener
	done     chan struct{}
	barrier  chan struct{}
	attempts atomic.Int64
	stopOnce sync.Once
	stopErr  error
}

func newMigrationSQLRenderingPoisonNetwork() (*migrationSQLRenderingPoisonNetwork, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for migration SQL database attempts: %w", err)
	}
	probe := &migrationSQLRenderingPoisonNetwork{
		listener: listener,
		done:     make(chan struct{}),
		barrier:  make(chan struct{}, 1),
	}
	go func() {
		defer close(probe.done)
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = connection.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
			document := make([]byte, len(migrationSQLRenderingPoisonBarrier))
			_, readErr := io.ReadFull(connection, document)
			_ = connection.Close()
			if readErr == nil && string(document) == migrationSQLRenderingPoisonBarrier {
				select {
				case probe.barrier <- struct{}{}:
				default:
				}
				continue
			}
			probe.attempts.Add(1)
		}
	}()
	return probe, nil
}

func (probe *migrationSQLRenderingPoisonNetwork) databaseURL() string {
	if probe == nil || probe.listener == nil {
		return ""
	}
	return "postgres://godj:" + migrationSQLRenderingCredentialCanary + "@" + probe.listener.Addr().String() + "/godj?sslmode=disable"
}

func (probe *migrationSQLRenderingPoisonNetwork) checkpoint(ctx context.Context) (int, error) {
	if probe == nil || probe.listener == nil {
		return 0, errors.New("migration SQL poison network is nil")
	}
	if ctx == nil {
		return 0, errors.New("migration SQL poison network context is nil")
	}
	connection, err := net.DialTimeout("tcp", probe.listener.Addr().String(), 2*time.Second)
	if err != nil {
		return 0, fmt.Errorf("dial migration SQL poison network barrier: %w", err)
	}
	_, writeErr := io.WriteString(connection, migrationSQLRenderingPoisonBarrier)
	closeErr := connection.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return 0, fmt.Errorf("write migration SQL poison network barrier: %w", err)
	}
	select {
	case <-probe.barrier:
		return int(probe.attempts.Load()), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(2 * time.Second):
		return 0, errors.New("migration SQL poison network barrier timed out")
	}
}

func (probe *migrationSQLRenderingPoisonNetwork) verifyAttemptObservation(ctx context.Context) error {
	if probe == nil || probe.listener == nil {
		return errors.New("migration SQL poison network is nil")
	}
	if ctx == nil {
		return errors.New("migration SQL poison network context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	connection, err := net.DialTimeout("tcp", probe.listener.Addr().String(), 2*time.Second)
	if err != nil {
		return fmt.Errorf("dial migration SQL poison network observation probe: %w", err)
	}
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		_ = connection.Close()
		return fmt.Errorf("set migration SQL poison network observation deadline: %w", err)
	}
	if _, err := connection.Write([]byte{0}); err != nil {
		_ = connection.Close()
		return fmt.Errorf("write migration SQL poison network observation probe: %w", err)
	}
	tcpConnection, ok := connection.(*net.TCPConn)
	if !ok {
		_ = connection.Close()
		return errors.New("migration SQL poison network observation connection is not TCP")
	}
	if err := tcpConnection.CloseWrite(); err != nil {
		_ = connection.Close()
		return fmt.Errorf("close migration SQL poison network observation write side: %w", err)
	}
	var response [1]byte
	_, readErr := connection.Read(response[:])
	closeErr := connection.Close()
	if !errors.Is(readErr, io.EOF) || closeErr != nil {
		return fmt.Errorf("migration SQL poison network did not reject observation probe: %w", errors.Join(readErr, closeErr))
	}
	attempts, err := probe.checkpoint(ctx)
	if err != nil {
		return err
	}
	if attempts != 1 {
		return fmt.Errorf("migration SQL poison network observation count = %d, want 1", attempts)
	}
	if reset := probe.attempts.Swap(0); reset != int64(attempts) {
		return fmt.Errorf("reset migration SQL poison network observation count = %d, want %d", reset, attempts)
	}
	resetAttempts, err := probe.checkpoint(ctx)
	if err != nil {
		return err
	}
	if resetAttempts != 0 {
		return fmt.Errorf("migration SQL poison network reset observation count = %d, want 0", resetAttempts)
	}
	return nil
}

func (probe *migrationSQLRenderingPoisonNetwork) stop() (int, error) {
	if probe == nil {
		return 0, errors.New("migration SQL poison network is nil")
	}
	probe.stopOnce.Do(func() {
		probe.stopErr = probe.listener.Close()
		<-probe.done
	})
	if probe.stopErr != nil && !errors.Is(probe.stopErr, net.ErrClosed) {
		return int(probe.attempts.Load()), probe.stopErr
	}
	return int(probe.attempts.Load()), nil
}

func migrationSQLRenderingObserveOneSelection(
	ctx context.Context,
	fixture migrationSQLRenderingFixture,
) (migrationSQLRenderingSelectionEvidence, error) {
	return migrationSQLRenderingObserveConfiguredRenderer(
		ctx,
		fixture,
		func(selected databaseconfig.Config) migrationbackend.MigrationSQLRenderer {
			return selected.MigrationSQLRenderer()
		},
		true,
	)
}

func migrationSQLRenderingObserveConfiguredRenderer(
	ctx context.Context,
	fixture migrationSQLRenderingFixture,
	rendererFor func(databaseconfig.Config) migrationbackend.MigrationSQLRenderer,
	coherent bool,
) (evidence migrationSQLRenderingSelectionEvidence, resultErr error) {
	if rendererFor == nil {
		return migrationSQLRenderingSelectionEvidence{}, errors.New("migration SQL configured renderer factory is nil")
	}
	poisonNetwork, err := newMigrationSQLRenderingPoisonNetwork()
	if err != nil {
		return migrationSQLRenderingSelectionEvidence{}, err
	}
	defer func() {
		remainingAttempts, stopErr := poisonNetwork.stop()
		if remainingAttempts != 0 {
			stopErr = errors.Join(
				stopErr,
				fmt.Errorf("migration SQL poison network observed %d late database attempts", remainingAttempts),
			)
		}
		resultErr = errors.Join(resultErr, stopErr)
	}()
	if err := poisonNetwork.verifyAttemptObservation(ctx); err != nil {
		return migrationSQLRenderingSelectionEvidence{}, fmt.Errorf("verify migration SQL poison network: %w", err)
	}
	selected, err := databaseconfig.PostgreSQL(
		poisonNetwork.databaseURL(),
		"public",
	)
	if err != nil || selected.Kind() != databaseconfig.KindPostgres {
		return migrationSQLRenderingSelectionEvidence{}, fmt.Errorf("construct frozen Article PostgreSQL selection: %w", err)
	}
	renderer := rendererFor(selected)
	if renderer == nil || reflect.ValueOf(renderer).Kind() == reflect.Ptr && reflect.ValueOf(renderer).IsNil() {
		return migrationSQLRenderingSelectionEvidence{}, errors.New("construct configured migration SQL renderer: renderer is nil")
	}
	request, err := sqlmigrateprotocol.EncodeRequest(sqlmigrateprotocol.Request{App: fixture.target.App, Name: fixture.target.Name})
	if err != nil {
		return migrationSQLRenderingSelectionEvidence{}, err
	}
	backendOpens := 0
	configuration := project.Config{
		MigrationDefinitionSources: fixture.sources,
		OpenMigrationBackend: func(openContext context.Context) (project.MigrationBackend, error) {
			backendOpens++
			return databaseconfig.Open(openContext, selected)
		},
		MigrationSQLRenderer: renderer,
	}
	var wire bytes.Buffer
	if err := project.Run(
		ctx,
		configuration,
		[]string{sqlmigrateprotocol.PrivateArgument},
		bytes.NewReader(request),
		&wire,
	); err != nil {
		return migrationSQLRenderingSelectionEvidence{}, err
	}
	response, failure, failed := sqlmigrateprotocol.ParseResponse(wire.Bytes(), true)
	credentialPublished := bytes.Count(wire.Bytes(), []byte(migrationSQLRenderingCredentialCanary))
	networkAttempts, err := poisonNetwork.checkpoint(ctx)
	if err != nil {
		return migrationSQLRenderingSelectionEvidence{}, fmt.Errorf("observe migration SQL poison network: %w", err)
	}
	if failed || failure != (sqlmigrateprotocol.Failure{}) || !response.OK || len(response.Result.Statements) != 2 ||
		backendOpens != 0 || networkAttempts != 0 || credentialPublished != 0 ||
		!strings.Contains(response.Result.Statements[0], `"public".`) ||
		!strings.Contains(response.Result.Statements[1], `"public".`) {
		return migrationSQLRenderingSelectionEvidence{}, fmt.Errorf(
			"one frozen Article database selection = response:%+v parse:%+v/%t opens:%d network:%d credentials:%d",
			response,
			failure,
			failed,
			backendOpens,
			networkAttempts,
			credentialPublished,
		)
	}
	return migrationSQLRenderingSelectionEvidence{
		coherent: coherent, backendOpens: backendOpens, networkAttempts: networkAttempts, credentialPublished: credentialPublished,
	}, nil
}

func migrationSQLRenderingExternalConfig(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	fixture, err := newMigrationSQLRenderingFixture()
	if err != nil {
		return protocol.Observation{}, err
	}
	bodies, err := migrations.RenderMigrationSQL(ctx, fixture.loaded, fixture.target, sqlite.NewMigrationSQLRenderer())
	if err != nil {
		return protocol.Observation{}, err
	}
	processEvidence, err := migrationSQLRenderingObserveProcesses(ctx, bodies)
	if err != nil {
		return protocol.Observation{}, err
	}
	configType := reflect.TypeOf(project.Config{})
	field, exists := configType.FieldByName("MigrationSQLRenderer")
	rendererType := reflect.TypeOf((*migrationbackend.MigrationSQLRenderer)(nil)).Elem()
	if !exists || field.Type != rendererType {
		return protocol.Observation{}, fmt.Errorf("project MigrationSQLRenderer field = %v, %t", field.Type, exists)
	}
	sqliteConstructor := reflect.TypeOf(sqlite.NewMigrationSQLRenderer)
	postgresConstructor := reflect.TypeOf(postgres.NewMigrationSQLRenderer)
	postgresConfig := reflect.TypeOf(postgres.MigrationSQLConfig{})
	if sqliteConstructor.NumIn() != 0 || sqliteConstructor.NumOut() != 1 || sqliteConstructor.Out(0) != rendererType ||
		postgresConstructor.NumIn() != 1 || postgresConstructor.In(0) != postgresConfig || postgresConstructor.NumOut() != 1 ||
		postgresConstructor.Out(0) != rendererType || postgresConfig.NumField() != 1 || postgresConfig.Field(0).Name != "Schema" {
		return protocol.Observation{}, errors.New("public SQL renderer constructor shape changed")
	}
	if !processEvidence.externalProjectBuilt || processEvidence.credentialValuesPublished != 0 {
		return protocol.Observation{}, fmt.Errorf("external SQL renderer project evidence = %+v", processEvidence)
	}
	selectionEvidence, err := migrationSQLRenderingObserveOneSelection(ctx, fixture)
	if err != nil {
		return protocol.Observation{}, err
	}
	compileCases := []protocol.Value{
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("sqlite_constructor_assignment"), "compiles": protocol.Boolean(processEvidence.externalProjectBuilt),
			"repository_external": protocol.Boolean(true),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("postgres_schema_constructor_assignment"), "compiles": protocol.Boolean(processEvidence.externalProjectBuilt),
			"repository_external": protocol.Boolean(true),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("keyed_project_config_literal"), "compiles": protocol.Boolean(processEvidence.externalProjectBuilt),
			"repository_external": protocol.Boolean(true),
		}),
		protocol.Object(map[string]protocol.Value{
			"case":                                protocol.String("unkeyed_project_config_source_impact"),
			"current_only_source_change_observed": protocol.Boolean(configType.NumField() == 5),
			"repository_external":                 protocol.Boolean(true),
		}),
	}
	result := protocol.Object(map[string]protocol.Value{
		"compile_cases": protocol.List(compileCases...), "custom_opener_renderer_coherence_proven": protocol.Boolean(false),
		"direct_project_config_field": protocol.String(field.Name), "postgres_constructor_inputs": migrationSQLRenderingStrings("schema"),
		"renderer_and_opener_derived_from_one_builtin_selection": protocol.Boolean(selectionEvidence.coherent), "sqlite_constructor_inputs": migrationSQLRenderingStrings(),
		"supported_builtin_renderer_db_free": protocol.Boolean(processEvidence.credentialValuesPublished+selectionEvidence.credentialPublished == 0),
	})
	dbState := migrationSQLRenderingDatabaseState(false)
	metrics := protocol.Object(map[string]protocol.Value{
		"backend_opens": migrationSQLRenderingInt(selectionEvidence.backendOpens), "compile_cases": migrationSQLRenderingInt(len(compileCases)),
		"credential_values": migrationSQLRenderingInt(processEvidence.credentialValuesPublished + selectionEvidence.credentialPublished), "database_handles": migrationSQLRenderingInt(selectionEvidence.backendOpens),
		"history_reads": migrationSQLRenderingInt(0), "network_calls": migrationSQLRenderingInt(selectionEvidence.networkAttempts), "schema_editor_calls": migrationSQLRenderingInt(0),
	})
	return migrationSQLRenderingObservation(contract, result, valuePointer(dbState), valuePointer(metrics)), nil
}
