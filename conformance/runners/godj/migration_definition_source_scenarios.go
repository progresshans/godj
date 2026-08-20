package godj

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

type migrationDefinitionSourceScenarioKind uint8

const (
	migrationDefinitionSourceLoad migrationDefinitionSourceScenarioKind = iota + 1
	migrationDefinitionSourceCanonicality
	migrationDefinitionSourceLifecycle
)

type migrationDefinitionSourceFixture struct {
	phase   protocol.Phase
	kind    migrationDefinitionSourceScenarioKind
	sources []definition.Source
	targets []migrations.MigrationKey
}

// migrationDefinitionLifecycleBackendProbe derives execution and plan facts
// from the backend calls made by Executor.Migrate while delegating
// every operation to a fresh SQLite backend.
type migrationDefinitionLifecycleBackendProbe struct {
	delegate         *sqlite.Backend
	sessionOpenCalls int
	transitions      []migrationbackend.HistoryTransition
}

func (probe *migrationDefinitionLifecycleBackendProbe) BeginMigration(
	ctx context.Context,
) (migrationbackend.Transaction, error) {
	return probe.delegate.BeginMigration(ctx)
}

func (probe *migrationDefinitionLifecycleBackendProbe) MigrationCapabilities() migrationbackend.MigrationCapabilities {
	return probe.delegate.MigrationCapabilities()
}

func (probe *migrationDefinitionLifecycleBackendProbe) OpenRevisionFencedSession(
	ctx context.Context,
) (migrationbackend.RevisionFencedSession, error) {
	probe.sessionOpenCalls++
	session, err := probe.delegate.OpenRevisionFencedSession(ctx)
	if err != nil {
		return session, err
	}
	return &migrationDefinitionLifecycleSessionProbe{delegate: session, owner: probe}, nil
}

type migrationDefinitionLifecycleSessionProbe struct {
	delegate migrationbackend.RevisionFencedSession
	owner    *migrationDefinitionLifecycleBackendProbe
}

func (probe *migrationDefinitionLifecycleSessionProbe) ReadAppliedMigrations(
	ctx context.Context,
) ([]migrationbackend.AppliedMigration, error) {
	return probe.delegate.ReadAppliedMigrations(ctx)
}

func (probe *migrationDefinitionLifecycleSessionProbe) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	transaction, err := probe.delegate.BeginMigration(ctx, transition, intent)
	if err == nil {
		probe.owner.transitions = append(probe.owner.transitions, transition)
	}
	return transaction, err
}

func (probe *migrationDefinitionLifecycleSessionProbe) Close(ctx context.Context) error {
	return probe.delegate.Close(ctx)
}

var migrationDefinitionSourceFixtures = map[string]func() migrationDefinitionSourceFixture{
	"godj.migration.definition_source.canonical_batch": func() migrationDefinitionSourceFixture {
		return migrationDefinitionSourceFixture{
			phase:   protocol.PhaseConstruction,
			kind:    migrationDefinitionSourceLoad,
			sources: migrationDefinitionCanonicalSources(),
		}
	},
	"godj.migration.definition_source.empty_source": func() migrationDefinitionSourceFixture {
		return migrationDefinitionSourceFixture{
			phase: protocol.PhaseConstruction,
			kind:  migrationDefinitionSourceLoad,
		}
	},
	"godj.migration.definition_source.canonical_syntax_and_order": func() migrationDefinitionSourceFixture {
		return migrationDefinitionSourceFixture{
			phase:   protocol.PhaseConstruction,
			kind:    migrationDefinitionSourceCanonicality,
			sources: migrationDefinitionCanonicalSources(),
		}
	},
	"godj.migration.definition_source.unsupported_format": func() migrationDefinitionSourceFixture {
		document := migrationDefinitionRootDocument()
		document["format_version"] = definition.DefinitionFormatVersion + 1
		return migrationDefinitionSourceFixture{
			phase: protocol.PhaseEnvironment,
			kind:  migrationDefinitionSourceLoad,
			sources: []definition.Source{{
				SourceID: "a-version",
				Document: migrationDefinitionMarshal(document, false),
			}},
		}
	},
	"godj.migration.definition_source.malformed_atomic_batch": func() migrationDefinitionSourceFixture {
		duplicate := migrationDefinitionMarshal(migrationDefinitionTailDocument(), false)
		duplicate = bytes.Replace(
			duplicate,
			[]byte(`"name":"0002_fields"`),
			[]byte(`"name":"0002_fields","name":"shadow"`),
			1,
		)
		return migrationDefinitionSourceFixture{
			phase: protocol.PhaseConstruction,
			kind:  migrationDefinitionSourceLoad,
			sources: []definition.Source{
				{SourceID: "a-valid", Document: migrationDefinitionMarshal(migrationDefinitionRootDocument(), false)},
				{SourceID: "b-invalid", Document: duplicate},
			},
		}
	},
	"godj.migration.definition_source.duplicate_identity": func() migrationDefinitionSourceFixture {
		first := migrationDefinitionRootDocument()
		second := migrationDefinitionRootDocument()
		second["producer"].(map[string]any)["version"] = "2.0.0"
		model := second["migration"].(map[string]any)["operations"].([]any)[0].(map[string]any)["model"].(map[string]any)
		model["fields"].([]any)[1].(map[string]any)["default"] = map[string]any{
			"kind":   "string",
			"string": "other",
		}
		return migrationDefinitionSourceFixture{
			phase: protocol.PhaseConstruction,
			kind:  migrationDefinitionSourceLoad,
			sources: []definition.Source{
				{SourceID: "z-duplicate", Document: migrationDefinitionMarshal(second, false)},
				{SourceID: "a-original", Document: migrationDefinitionMarshal(first, false)},
			},
		}
	},
	"godj.migration.definition_source.closed_codec": func() migrationDefinitionSourceFixture {
		document := migrationDefinitionRootDocument()
		document["migration"].(map[string]any)["operations"] = []any{
			map[string]any{"app_label": "alpha", "kind": "run_python"},
		}
		return migrationDefinitionSourceFixture{
			phase: protocol.PhaseConstruction,
			kind:  migrationDefinitionSourceLoad,
			sources: []definition.Source{{
				SourceID: "a-operation",
				Document: migrationDefinitionMarshal(document, false),
			}},
		}
	},
	"django.migration.definition_source.public_loaded_executor": func() migrationDefinitionSourceFixture {
		return migrationDefinitionSourceFixture{
			phase:   protocol.PhaseCommit,
			kind:    migrationDefinitionSourceLifecycle,
			sources: migrationDefinitionCanonicalSources(),
			targets: []migrations.MigrationKey{{App: "alpha", Name: "0002_fields"}},
		}
	},
}

func migrationDefinitionSourceScenario(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	factory, ok := migrationDefinitionSourceFixtures[contract.Scenario]
	if !ok {
		return protocol.Observation{}, fmt.Errorf("unsupported migration definition source scenario %q", contract.Scenario)
	}
	fixture := factory()
	if fixture.phase != contract.Phase {
		return protocol.Observation{}, fmt.Errorf(
			"migration definition source scenario %q phase = %q, manifest requires %q",
			contract.Scenario,
			fixture.phase,
			contract.Phase,
		)
	}
	return runMigrationDefinitionSourceFixture(ctx, contract.ID, fixture)
}

func runMigrationDefinitionSourceFixture(
	ctx context.Context,
	contractID string,
	fixture migrationDefinitionSourceFixture,
) (protocol.Observation, error) {
	if ctx == nil {
		return protocol.Observation{}, errors.New("migration definition source scenario: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return protocol.Observation{}, err
	}

	switch fixture.kind {
	case migrationDefinitionSourceLoad:
		set, report, err := definition.Load(fixture.sources...)
		return migrationDefinitionLoadObservation(contractID, fixture.phase, set, report, err)
	case migrationDefinitionSourceCanonicality:
		return migrationDefinitionCanonicalityObservation(contractID, fixture)
	case migrationDefinitionSourceLifecycle:
		return migrationDefinitionLifecycleObservation(ctx, contractID, fixture)
	default:
		return protocol.Observation{}, fmt.Errorf("unsupported migration definition source fixture kind %d", fixture.kind)
	}
}

func migrationDefinitionLoadObservation(
	contractID string,
	phase protocol.Phase,
	set migrations.LoadedDefinitionSet,
	report definition.LoadReport,
	loadErr error,
) (protocol.Observation, error) {
	metrics := migrationDefinitionMetrics(report, 0, nil)
	observation := protocol.Observation{
		ID:      contractID,
		Status:  protocol.StatusObserved,
		Phase:   phase,
		Metrics: valuePointer(metrics),
	}
	if loadErr != nil {
		observed, err := migrationDefinitionObservedError(loadErr, report)
		if err != nil {
			return protocol.Observation{}, err
		}
		observation.Error = observed
		return observation, nil
	}

	result, err := migrationDefinitionSuccessResult(set, false, 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	observation.Result = valuePointer(protocol.Object(result))
	return observation, nil
}

func migrationDefinitionCanonicalityObservation(
	contractID string,
	fixture migrationDefinitionSourceFixture,
) (protocol.Observation, error) {
	baseline, report, err := definition.Load(fixture.sources...)
	if err != nil {
		return migrationDefinitionLoadObservation(contractID, fixture.phase, baseline, report, err)
	}

	equivalentRoot := migrationDefinitionRootDocument()
	equivalentRoot["producer"].(map[string]any)["version"] = "9.9.9"
	equivalent, _, err := definition.Load(
		definition.Source{
			SourceID: "relabel-b",
			Document: migrationDefinitionMarshal(equivalentRoot, true),
		},
		definition.Source{
			SourceID: "relabel-a",
			Document: migrationDefinitionMarshal(migrationDefinitionTailDocument(), false),
		},
	)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("load equivalent migration definition syntax: %w", err)
	}

	reorderedTail := migrationDefinitionTailDocument()
	operations := reorderedTail["migration"].(map[string]any)["operations"].([]any)
	operations[0], operations[1] = operations[1], operations[0]
	changed, _, err := definition.Load(
		definition.Source{SourceID: "changed-root", Document: migrationDefinitionMarshal(migrationDefinitionRootDocument(), false)},
		definition.Source{SourceID: "changed-tail", Document: migrationDefinitionMarshal(reorderedTail, false)},
	)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("load operation-reordered migration definitions: %w", err)
	}

	result, err := migrationDefinitionSuccessResult(baseline, false, 0)
	if err != nil {
		return protocol.Observation{}, err
	}
	result["canonicality"] = protocol.Object(map[string]protocol.Value{
		"equivalent_definition_set":       protocol.Boolean(reflect.DeepEqual(baseline.Definitions(), equivalent.Definitions())),
		"equivalent_digest":               protocol.String(equivalent.Digest()),
		"operation_order_changed_digest":  protocol.String(changed.Digest()),
		"operation_order_is_semantic":     protocol.Boolean(changed.Digest() != baseline.Digest()),
		"source_relabel_preserved_digest": protocol.Boolean(equivalent.Digest() == baseline.Digest()),
	})
	return protocol.Observation{
		ID:      contractID,
		Status:  protocol.StatusObserved,
		Phase:   fixture.phase,
		Result:  valuePointer(protocol.Object(result)),
		Metrics: valuePointer(migrationDefinitionMetrics(report, 0, nil)),
	}, nil
}

func migrationDefinitionLifecycleObservation(
	ctx context.Context,
	contractID string,
	fixture migrationDefinitionSourceFixture,
) (observation protocol.Observation, resultErr error) {
	set, report, err := definition.Load(fixture.sources...)
	if err != nil {
		return migrationDefinitionLoadObservation(contractID, fixture.phase, set, report, err)
	}
	if len(fixture.targets) == 0 {
		return protocol.Observation{}, errors.New("migration definition lifecycle fixture has no target")
	}

	definitions := set.Definitions()
	targets := make([]migrations.Target, len(fixture.targets))
	for index, key := range fixture.targets {
		targets[index] = migrations.NamedTarget(key)
	}
	request := migrations.TargetedLifecycleRequest(targets[0], targets[1:]...)

	directory, err := os.MkdirTemp("", "godj-migration-definition-")
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("create migration definition lifecycle directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(directory)) }()
	databasePath := filepath.Join(directory, "lifecycle.sqlite3")
	backend, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("open migration definition lifecycle backend: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, backend.Close()) }()
	observer, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("open migration definition lifecycle observer: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, observer.Close()) }()
	if err := observer.PingContext(ctx); err != nil {
		return protocol.Observation{}, fmt.Errorf("ping migration definition lifecycle observer: %w", err)
	}

	before, err := migrationDefinitionDatabaseSnapshot(ctx, observer)
	if err != nil {
		return protocol.Observation{}, err
	}
	frozenDefinitions := set.Definitions()
	frozenDigest := set.Digest()
	probe := &migrationDefinitionLifecycleBackendProbe{delegate: backend}
	executor := migrations.Executor{Backend: probe}
	returnedState, err := executor.Migrate(ctx, set, request)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("migrate loaded definition set: %w", err)
	}
	sessionOpenCalls := probe.sessionOpenCalls
	if sessionOpenCalls != 1 {
		return protocol.Observation{}, fmt.Errorf("loaded definition set session-open calls = %d, want one", sessionOpenCalls)
	}
	after, err := migrationDefinitionDatabaseSnapshot(ctx, observer)
	if err != nil {
		return protocol.Observation{}, err
	}
	definitionsUnchanged := reflect.DeepEqual(frozenDefinitions, set.Definitions()) && frozenDigest == set.Digest()

	result, err := migrationDefinitionSuccessResult(set, true, sessionOpenCalls)
	if err != nil {
		return protocol.Observation{}, err
	}
	returnedStateValue, err := migrationDefinitionProjectStateValue(returnedState)
	if err != nil {
		return protocol.Observation{}, err
	}
	result["lifecycle"] = protocol.Object(map[string]protocol.Value{
		"plan":           protocol.List(migrationDefinitionTransitionValues(probe.transitions)...),
		"returned_state": returnedStateValue,
		"targets":        protocol.List(migrationDefinitionKeyValues(fixture.targets)...),
	})
	lifecycleMetrics := protocol.Object(map[string]protocol.Value{
		"definitions_unchanged": protocol.Boolean(definitionsUnchanged),
		"digest":                protocol.String(frozenDigest),
		"graph_node_count":      protocol.Integer(strconv.Itoa(len(definitions))),
		"plan_step_count":       protocol.Integer(strconv.Itoa(len(probe.transitions))),
		"route":                 protocol.String("loaded_definition_executor"),
	})
	return protocol.Observation{
		ID:     contractID,
		Status: protocol.StatusObserved,
		Phase:  fixture.phase,
		Result: valuePointer(protocol.Object(result)),
		DBState: valuePointer(protocol.Object(map[string]protocol.Value{
			"after":  after,
			"before": before,
		})),
		Metrics: valuePointer(migrationDefinitionMetrics(report, sessionOpenCalls, &lifecycleMetrics)),
	}, nil
}

func migrationDefinitionSuccessResult(
	set migrations.LoadedDefinitionSet,
	attempted bool,
	sessionOpenCalls int,
) (map[string]protocol.Value, error) {
	definitions, err := migrationDefinitionValues(set.Definitions())
	if err != nil {
		return nil, err
	}
	observedDigest := protocol.Null()
	if attempted {
		observedDigest = protocol.String(set.Digest())
	}
	return map[string]protocol.Value{
		"format": migrationDefinitionFormatValue(),
		"definition_set": protocol.Object(map[string]protocol.Value{
			"definitions": protocol.List(definitions...),
			"digest":      protocol.String(set.Digest()),
		}),
		"execution": protocol.Object(map[string]protocol.Value{
			"attempted":          protocol.Boolean(attempted),
			"observed_digest":    observedDigest,
			"session_open_calls": protocol.Integer(strconv.Itoa(sessionOpenCalls)),
		}),
		"sources": protocol.List(migrationDefinitionSourceValues(set.Sources())...),
	}, nil
}

func migrationDefinitionFormatValue() protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"definition_format": protocol.Integer(strconv.FormatInt(definition.DefinitionFormatVersion, 10)),
		"schema_ir":         protocol.Integer(strconv.Itoa(ir.CurrentFormatVersion)),
		"state_format":      protocol.Integer(strconv.Itoa(migrations.StateFormatVersion)),
	})
}

func migrationDefinitionMetrics(
	report definition.LoadReport,
	sessionOpenCalls int,
	lifecycle *protocol.Value,
) protocol.Value {
	failure := protocol.Null()
	if context, ok := report.Failure(); ok {
		failure = migrationDefinitionFailureValue(context)
	}
	fields := map[string]protocol.Value{
		"definition_sets_published":   protocol.Integer(strconv.Itoa(report.DefinitionSetsPublished)),
		"definitions_published":       protocol.Integer(strconv.Itoa(report.DefinitionsPublished)),
		"documents_received":          protocol.Integer(strconv.Itoa(report.DocumentsReceived)),
		"failure":                     failure,
		"session_open_calls":          protocol.Integer(strconv.Itoa(sessionOpenCalls)),
		"headers_validated":           protocol.Integer(strconv.Itoa(report.HeadersValidated)),
		"operations_decoded":          protocol.Integer(strconv.Itoa(report.OperationsDecoded)),
		"source_reads_after_snapshot": protocol.Integer("0"),
	}
	if lifecycle != nil {
		fields["lifecycle"] = *lifecycle
	}
	return protocol.Object(fields)
}

func migrationDefinitionFailureValue(context definition.FailureContext) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"app":             protocol.String(context.App),
		"json_pointer":    protocol.String(context.JSONPointer),
		"name":            protocol.String(context.Name),
		"operation_index": protocol.Integer(strconv.Itoa(context.OperationIndex)),
		"reason":          protocol.String(context.Reason),
		"source_id":       protocol.String(context.SourceID),
		"stage":           protocol.String(context.Stage),
	})
}

func migrationDefinitionObservedError(
	loadErr error,
	report definition.LoadReport,
) (*protocol.ObservedError, error) {
	failure, ok := report.Failure()
	if !ok {
		return nil, fmt.Errorf("migration definition load error has no report failure: %w", loadErr)
	}
	sourceErr := new(definition.Error)
	planningErr := new(migrations.PlanningError)
	switch {
	case errors.As(loadErr, &sourceErr):
		if !reflect.DeepEqual(sourceErr.Context(), failure) {
			return nil, fmt.Errorf("migration definition error/report failure mismatch: error=%+v report=%+v", sourceErr.Context(), failure)
		}
		return &protocol.ObservedError{
			Category:          sourceErr.Category,
			Code:              string(sourceErr.Code),
			MessageIsContract: boolPointer(false),
		}, nil
	case errors.As(loadErr, &planningErr):
		return &protocol.ObservedError{
			Category:          string(planningErr.Category),
			Code:              string(planningErr.Code),
			MessageIsContract: boolPointer(false),
		}, nil
	default:
		return nil, fmt.Errorf("migration definition load returned unclassified error %T: %w", loadErr, loadErr)
	}
}

func migrationDefinitionValues(definitions []migrations.Migration) ([]protocol.Value, error) {
	values := make([]protocol.Value, len(definitions))
	for index, migration := range definitions {
		dependencies := migrationDefinitionKeyValues(migration.Dependencies)
		operations := make([]protocol.Value, len(migration.Operations))
		for operationIndex, operation := range migration.Operations {
			value, err := migrationDefinitionOperationValue(operation)
			if err != nil {
				return nil, fmt.Errorf("migration %s.%s operation %d: %w", migration.App, migration.Name, operationIndex, err)
			}
			operations[operationIndex] = value
		}
		values[index] = protocol.Object(map[string]protocol.Value{
			"app":          protocol.String(migration.App),
			"dependencies": protocol.List(dependencies...),
			"name":         protocol.String(migration.Name),
			"operations":   protocol.List(operations...),
		})
	}
	return values, nil
}

func migrationDefinitionOperationValue(operation migrations.Operation) (protocol.Value, error) {
	switch current := operation.(type) {
	case migrations.CreateModel:
		return protocol.Object(map[string]protocol.Value{
			"app_label": protocol.String(current.AppLabel),
			"kind":      protocol.String("create_model"),
			"model":     migrationDefinitionModelValue(current.Model),
		}), nil
	case *migrations.CreateModel:
		if current == nil {
			return protocol.Value{}, errors.New("typed nil CreateModel")
		}
		return migrationDefinitionOperationValue(*current)
	case migrations.AddField:
		return protocol.Object(map[string]protocol.Value{
			"app_label":  protocol.String(current.AppLabel),
			"field":      migrationDefinitionFieldValue(current.Field),
			"kind":       protocol.String("add_field"),
			"model_name": protocol.String(current.ModelName),
		}), nil
	case *migrations.AddField:
		if current == nil {
			return protocol.Value{}, errors.New("typed nil AddField")
		}
		return migrationDefinitionOperationValue(*current)
	default:
		return protocol.Value{}, fmt.Errorf("unsupported loaded operation %T", operation)
	}
}

func migrationDefinitionModelValue(model ir.Model) protocol.Value {
	fields := make([]protocol.Value, len(model.Fields))
	for index, field := range model.Fields {
		fields[index] = migrationDefinitionFieldValue(field)
	}
	return protocol.Object(map[string]protocol.Value{
		"db_table": protocol.String(model.DBTable),
		"fields":   protocol.List(fields...),
		"go_name":  protocol.String(model.GoName),
		"name":     protocol.String(model.Name),
	})
}

func migrationDefinitionFieldValue(field ir.Field) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"column":      protocol.String(field.Column),
		"default":     migrationDefinitionDefaultValue(field.Default),
		"go_name":     protocol.String(field.GoName),
		"kind":        protocol.String(string(field.Kind)),
		"max_length":  protocol.Integer(strconv.Itoa(field.MaxLength)),
		"name":        protocol.String(field.Name),
		"nullable":    protocol.Boolean(field.Nullable),
		"primary_key": protocol.Boolean(field.PrimaryKey),
	})
}

func migrationDefinitionDefaultValue(value *ir.ScalarDefault) protocol.Value {
	if value == nil {
		return protocol.Null()
	}
	switch value.Kind {
	case ir.ScalarString:
		return protocol.Object(map[string]protocol.Value{
			"kind":   protocol.String(string(value.Kind)),
			"string": protocol.String(value.String),
		})
	case ir.ScalarBoolean:
		return protocol.Object(map[string]protocol.Value{
			"boolean": protocol.Boolean(value.Boolean),
			"kind":    protocol.String(string(value.Kind)),
		})
	default:
		return protocol.Object(map[string]protocol.Value{
			"kind": protocol.String(string(value.Kind)),
		})
	}
}

func migrationDefinitionSourceValues(sources []definition.SourceInfo) []protocol.Value {
	values := make([]protocol.Value, len(sources))
	for index, source := range sources {
		values[index] = protocol.Object(map[string]protocol.Value{
			"app":  protocol.String(source.Migration.App),
			"name": protocol.String(source.Migration.Name),
			"producer": protocol.Object(map[string]protocol.Value{
				"name":    protocol.String(source.Producer.Name),
				"version": protocol.String(source.Producer.Version),
			}),
			"source_id": protocol.String(source.SourceID),
		})
	}
	return values
}

func migrationDefinitionTransitionValues(transitions []migrationbackend.HistoryTransition) []protocol.Value {
	values := make([]protocol.Value, len(transitions))
	for index, transition := range transitions {
		direction := migrations.DirectionForward
		if transition.Kind == migrationbackend.HistoryTransitionUnapply {
			direction = migrations.DirectionBackward
		}
		values[index] = protocol.Object(map[string]protocol.Value{
			"app":       protocol.String(transition.Migration.App),
			"direction": protocol.String(string(direction)),
			"name":      protocol.String(transition.Migration.Name),
		})
	}
	return values
}

func migrationDefinitionKeyValues(keys []migrations.MigrationKey) []protocol.Value {
	values := make([]protocol.Value, len(keys))
	for index, key := range keys {
		values[index] = protocol.Object(map[string]protocol.Value{
			"app":  protocol.String(key.App),
			"name": protocol.String(key.Name),
		})
	}
	return values
}

func migrationDefinitionProjectStateValue(state migrations.ProjectState) (protocol.Value, error) {
	models := make([]protocol.Value, 0)
	for _, app := range state.Apps() {
		schema, ok := state.Schema(app)
		if !ok {
			return protocol.Value{}, fmt.Errorf("migration definition state app %q disappeared", app)
		}
		orderedModels := append([]ir.Model(nil), schema.Models...)
		sort.Slice(orderedModels, func(left, right int) bool {
			return orderedModels[left].Name < orderedModels[right].Name
		})
		for _, model := range orderedModels {
			fields := make([]protocol.Value, len(model.Fields))
			for index, field := range model.Fields {
				fields[index] = protocol.String(field.Name)
			}
			models = append(models, protocol.Object(map[string]protocol.Value{
				"app":      protocol.String(app),
				"db_table": protocol.String(model.DBTable),
				"fields":   protocol.List(fields...),
				"name":     protocol.String(model.Name),
			}))
		}
	}
	return protocol.Object(map[string]protocol.Value{"models": protocol.List(models...)}), nil
}

func migrationDefinitionDatabaseSnapshot(ctx context.Context, observer *sql.DB) (protocol.Value, error) {
	rows, err := observer.QueryContext(
		ctx,
		`SELECT "name" FROM "sqlite_schema" WHERE "type" = 'table' AND "name" LIKE 'godj_definition_%' ORDER BY "name"`,
	)
	if err != nil {
		return protocol.Value{}, fmt.Errorf("list migration definition schema: %w", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			return protocol.Value{}, fmt.Errorf("scan migration definition table: %w", err)
		}
		tables = append(tables, table)
	}
	iterateErr := rows.Err()
	closeErr := rows.Close()
	if iterateErr != nil || closeErr != nil {
		return protocol.Value{}, errors.Join(iterateErr, closeErr)
	}

	managed := make([]protocol.Value, len(tables))
	for index, table := range tables {
		columns, err := migrationDefinitionColumns(ctx, observer, table)
		if err != nil {
			return protocol.Value{}, err
		}
		managed[index] = protocol.Object(map[string]protocol.Value{
			"columns": protocol.List(columns...),
			"name":    protocol.String(table),
		})
	}
	recorderPresent, err := sqliteTableExistsContext(ctx, observer, goDjMigrationRecordTable)
	if err != nil {
		return protocol.Value{}, err
	}
	records := make([]protocol.Value, 0)
	if recorderPresent {
		rows, err := observer.QueryContext(ctx, `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
		if err != nil {
			return protocol.Value{}, fmt.Errorf("read migration definition records: %w", err)
		}
		for rows.Next() {
			var key migrations.MigrationKey
			if err := rows.Scan(&key.App, &key.Name); err != nil {
				_ = rows.Close()
				return protocol.Value{}, fmt.Errorf("scan migration definition record: %w", err)
			}
			records = append(records, migrationLifecycleKeyValue(key))
		}
		iterateErr := rows.Err()
		closeErr := rows.Close()
		if iterateErr != nil || closeErr != nil {
			return protocol.Value{}, errors.Join(iterateErr, closeErr)
		}
	}
	return protocol.Object(map[string]protocol.Value{
		"managed_schema":    protocol.List(managed...),
		"migration_records": protocol.List(records...),
		"recorder_present":  protocol.Boolean(recorderPresent),
	}), nil
}

func migrationDefinitionColumns(ctx context.Context, observer *sql.DB, table string) ([]protocol.Value, error) {
	rows, err := observer.QueryContext(ctx, "PRAGMA table_info("+quoteSQLiteIdentifier(table)+")")
	if err != nil {
		return nil, fmt.Errorf("describe migration definition table %s: %w", table, err)
	}
	type column struct {
		name       string
		nullable   bool
		primaryKey bool
	}
	columns := make([]column, 0)
	for rows.Next() {
		var (
			sequence     int
			name         string
			declaredType string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
		)
		if err := rows.Scan(&sequence, &name, &declaredType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan migration definition column: %w", err)
		}
		columns = append(columns, column{
			name:       name,
			nullable:   notNull == 0 && primaryKey == 0,
			primaryKey: primaryKey != 0,
		})
	}
	iterateErr := rows.Err()
	closeErr := rows.Close()
	if iterateErr != nil || closeErr != nil {
		return nil, errors.Join(iterateErr, closeErr)
	}
	sort.Slice(columns, func(left, right int) bool { return columns[left].name < columns[right].name })
	values := make([]protocol.Value, len(columns))
	for index, column := range columns {
		values[index] = protocol.Object(map[string]protocol.Value{
			"name":        protocol.String(column.name),
			"nullable":    protocol.Boolean(column.nullable),
			"primary_key": protocol.Boolean(column.primaryKey),
		})
	}
	return values, nil
}

func migrationDefinitionCanonicalSources() []definition.Source {
	return []definition.Source{
		{
			SourceID: "opaque-a-tail",
			Document: migrationDefinitionMarshal(migrationDefinitionTailDocument(), true),
		},
		{
			SourceID: "opaque-z-root",
			Document: migrationDefinitionMarshal(migrationDefinitionRootDocument(), false),
		},
	}
}

func migrationDefinitionAutoField() map[string]any {
	return map[string]any{
		"column":      "id",
		"default":     nil,
		"go_name":     "ID",
		"kind":        "auto",
		"max_length":  0,
		"name":        "id",
		"nullable":    false,
		"primary_key": true,
	}
}

func migrationDefinitionCharField(
	name string,
	goName string,
	maxLength int,
	nullable bool,
	defaultValue any,
) map[string]any {
	return map[string]any{
		"column":      name,
		"default":     defaultValue,
		"go_name":     goName,
		"kind":        "char",
		"max_length":  maxLength,
		"name":        name,
		"nullable":    nullable,
		"primary_key": false,
	}
}

func migrationDefinitionBooleanField(name string, goName string, defaultValue any) map[string]any {
	return map[string]any{
		"column":      name,
		"default":     defaultValue,
		"go_name":     goName,
		"kind":        "boolean",
		"max_length":  0,
		"name":        name,
		"nullable":    false,
		"primary_key": false,
	}
}

func migrationDefinitionRootDocument() map[string]any {
	return map[string]any{
		"format_version": definition.DefinitionFormatVersion,
		"migration": map[string]any{
			"app":          "alpha",
			"dependencies": []any{},
			"name":         "0001_initial",
			"operations": []any{
				map[string]any{
					"app_label": "alpha",
					"kind":      "create_model",
					"model": map[string]any{
						"db_table": "godj_definition_alpha_entry",
						"fields": []any{
							migrationDefinitionAutoField(),
							migrationDefinitionCharField(
								"title",
								"Title",
								64,
								false,
								map[string]any{"kind": "string", "string": "untitled"},
							),
						},
						"go_name": "Entry",
						"name":    "entry",
					},
				},
			},
		},
		"producer": map[string]any{"name": "godj-reference", "version": "0.1.0"},
	}
}

func migrationDefinitionTailDocument() map[string]any {
	return map[string]any{
		"format_version": definition.DefinitionFormatVersion,
		"migration": map[string]any{
			"app": "alpha",
			"dependencies": []any{
				map[string]any{"app": "alpha", "name": "0001_initial"},
			},
			"name": "0002_fields",
			"operations": []any{
				map[string]any{
					"app_label": "alpha",
					"field": migrationDefinitionBooleanField(
						"published",
						"Published",
						map[string]any{"boolean": false, "kind": "boolean"},
					),
					"kind":       "add_field",
					"model_name": "entry",
				},
				map[string]any{
					"app_label": "alpha",
					"field": migrationDefinitionCharField(
						"summary",
						"Summary",
						255,
						true,
						nil,
					),
					"kind":       "add_field",
					"model_name": "entry",
				},
			},
		},
		"producer": map[string]any{"name": "godj-reference", "version": "0.1.0"},
	}
}

func migrationDefinitionMarshal(document map[string]any, pretty bool) []byte {
	var (
		encoded []byte
		err     error
	)
	if pretty {
		encoded, err = json.MarshalIndent(document, "", "  ")
	} else {
		encoded, err = json.Marshal(document)
	}
	if err != nil {
		panic(fmt.Sprintf("marshal static migration definition fixture: %v", err))
	}
	return encoded
}
