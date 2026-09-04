// Package migrationrelationproduct characterizes GoDj's public relation-aware
// migration behavior. It owns its input documents and disposable SQLite files;
// it has no dependency on conformance protocol or reference artifacts.
package migrationrelationproduct

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// Case is a product-owned characterization case. It deliberately does not
// carry a manifest contract ID or reference scenario name.
type Case string

const (
	CaseCurrentABI          Case = "current_abi"
	CaseCurrentFormat       Case = "current_format_validation"
	CaseCurrentDigest       Case = "current_digest"
	CaseCurrentState        Case = "current_state"
	CaseStructuralPreflight Case = "structural_preflight"
	CaseCreateLifecycle     Case = "create_lifecycle"
	CaseAddRelation         Case = "add_relation"
	CaseRemoveRemake        Case = "remove_remake"
	CasePhysicalFKPolicy    Case = "physical_fk_policy"
	CaseFileRestart         Case = "file_restart"
	CasePrecommitFaults     Case = "precommit_faults"
	CaseCommitOutcomes      Case = "commit_outcomes"
)

// Cases returns the complete stable characterization order.
func Cases() []Case {
	return []Case{
		CaseCurrentABI,
		CaseCurrentFormat,
		CaseCurrentDigest,
		CaseCurrentState,
		CaseStructuralPreflight,
		CaseCreateLifecycle,
		CaseAddRelation,
		CaseRemoveRemake,
		CasePhysicalFKPolicy,
		CaseFileRestart,
		CasePrecommitFaults,
		CaseCommitOutcomes,
	}
}

type Observation struct {
	Case     Case           `json:"case"`
	Outcomes []OutcomeFact  `json:"outcomes"`
	Database *DatabaseState `json:"database,omitempty"`
	Metrics  Metrics        `json:"metrics"`
}

type OutcomeFact struct {
	Name        string             `json:"name"`
	Accepted    bool               `json:"accepted"`
	Digest      string             `json:"digest"`
	Definitions []DefinitionFact   `json:"definitions"`
	Sources     []SourceFact       `json:"sources"`
	State       StateFact          `json:"state"`
	Error       ErrorFact          `json:"error"`
	Intents     []IntentFact       `json:"intents"`
	Booleans    []NamedBooleanFact `json:"booleans"`
	Integers    []NamedIntegerFact `json:"integers"`
	Strings     []NamedStringFact  `json:"strings"`
}

type NamedBooleanFact struct {
	Name  string `json:"name"`
	Value bool   `json:"value"`
}

type NamedIntegerFact struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

type NamedStringFact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type SourceFact struct {
	SourceID        string `json:"source_id"`
	ProducerName    string `json:"producer_name"`
	ProducerVersion string `json:"producer_version"`
	App             string `json:"app"`
	Migration       string `json:"migration"`
}

type DefinitionFact struct {
	App          string             `json:"app"`
	Name         string             `json:"name"`
	Dependencies []MigrationKeyFact `json:"dependencies"`
	Operations   []OperationFact    `json:"operations"`
}

type MigrationKeyFact struct {
	App  string `json:"app"`
	Name string `json:"name"`
}

type OperationFact struct {
	Kind      string    `json:"kind"`
	App       string    `json:"app"`
	ModelName string    `json:"model_name"`
	Model     ModelFact `json:"model"`
	Field     FieldFact `json:"field"`
}

type StateFact struct {
	FormatVersion int         `json:"format_version"`
	Apps          []string    `json:"apps"`
	Models        []ModelFact `json:"models"`
}

type ModelFact struct {
	App    string      `json:"app"`
	Name   string      `json:"name"`
	GoName string      `json:"go_name"`
	Table  string      `json:"table"`
	Fields []FieldFact `json:"fields"`
}

type FieldFact struct {
	Name            string `json:"name"`
	GoName          string `json:"go_name"`
	Column          string `json:"column"`
	Kind            string `json:"kind"`
	PrimaryKey      bool   `json:"primary_key"`
	Nullable        bool   `json:"nullable"`
	MaxLength       int    `json:"max_length"`
	HasDefault      bool   `json:"has_default"`
	DefaultKind     string `json:"default_kind"`
	DefaultValue    string `json:"default_value"`
	HasRelation     bool   `json:"has_relation"`
	TargetApp       string `json:"target_app"`
	TargetModel     string `json:"target_model"`
	Cardinality     string `json:"cardinality"`
	ReverseName     string `json:"reverse_name"`
	ReverseDisabled bool   `json:"reverse_disabled"`
	OnDelete        string `json:"on_delete"`
}

type IntentFact struct {
	Transition  MigrationKeyFact      `json:"transition"`
	Kind        string                `json:"kind"`
	HasRelation bool                  `json:"has_relation"`
	Operations  []IntentOperationFact `json:"operations"`
}

type IntentOperationFact struct {
	Index   int                `json:"index"`
	Kind    int64              `json:"kind"`
	Before  ModelFact          `json:"before"`
	After   ModelFact          `json:"after"`
	Targets []IntentTargetFact `json:"targets"`
}

type IntentTargetFact struct {
	Source FieldFact `json:"source"`
	Target ModelFact `json:"target"`
	Key    FieldFact `json:"key"`
}

type ErrorFact struct {
	Present        bool   `json:"present"`
	Category       string `json:"category"`
	Code           string `json:"code"`
	Direction      string `json:"direction"`
	App            string `json:"app"`
	Migration      string `json:"migration"`
	OperationIndex int    `json:"operation_index"`
	Operation      string `json:"operation"`
	Feature        string `json:"feature"`
	Stage          string `json:"stage"`
	SourceID       string `json:"source_id"`
	JSONPointer    string `json:"json_pointer"`
	Reason         string `json:"reason"`
	Cause          string `json:"cause"`
	RollbackCause  string `json:"rollback_cause"`
}

type Metrics struct {
	Loads []LoadFact   `json:"loads"`
	Trace []TraceEvent `json:"trace"`
}

type LoadFact struct {
	Name                    string `json:"name"`
	DocumentsReceived       int    `json:"documents_received"`
	HeadersValidated        int    `json:"headers_validated"`
	OperationsDecoded       int    `json:"operations_decoded"`
	PlannerConstruction     int    `json:"planner_construction"`
	DefinitionsPublished    int    `json:"definitions_published"`
	DefinitionSetsPublished int    `json:"definition_sets_published"`
}

type TraceEvent struct {
	Ordinal int    `json:"ordinal"`
	Name    string `json:"name"`
	Detail  string `json:"detail"`
}

type DatabaseState struct {
	Snapshots []DatabaseSnapshot `json:"snapshots"`
}

type DatabaseSnapshot struct {
	Stage       string             `json:"stage"`
	Tables      []TableFact        `json:"tables"`
	ForeignKeys []ForeignKeyFact   `json:"foreign_keys"`
	Rows        []RowFact          `json:"rows"`
	Sequences   []SequenceFact     `json:"sequences"`
	History     []MigrationKeyFact `json:"history"`
}

type TableFact struct {
	Name    string       `json:"name"`
	SQL     string       `json:"sql"`
	Columns []ColumnFact `json:"columns"`
}

type ColumnFact struct {
	Position   int64  `json:"position"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	NotNull    bool   `json:"not_null"`
	Default    string `json:"default"`
	PrimaryKey int64  `json:"primary_key"`
	Hidden     int64  `json:"hidden"`
}

type ForeignKeyFact struct {
	Table    string `json:"table"`
	ID       int64  `json:"id"`
	Sequence int64  `json:"sequence"`
	Target   string `json:"target"`
	From     string `json:"from"`
	To       string `json:"to"`
	OnUpdate string `json:"on_update"`
	OnDelete string `json:"on_delete"`
	Match    string `json:"match"`
}

type RowFact struct {
	Table  string            `json:"table"`
	Values []NamedStringFact `json:"values"`
}

type SequenceFact struct {
	Table string `json:"table"`
	Value int64  `json:"value"`
}

// Observe executes one case through public GoDj APIs against fresh state.
func Observe(ctx context.Context, selected Case) (Observation, error) {
	if ctx == nil {
		return Observation{}, errors.New("characterize relation migration: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	observation := Observation{
		Case:     selected,
		Outcomes: make([]OutcomeFact, 0),
		Metrics: Metrics{
			Loads: make([]LoadFact, 0),
			Trace: make([]TraceEvent, 0),
		},
	}
	var err error
	switch selected {
	case CaseCurrentABI:
		err = observeCurrentABI(ctx, &observation)
	case CaseCurrentFormat:
		err = observeCurrentFormat(&observation)
	case CaseCurrentDigest:
		err = observeCurrentDigest(&observation)
	case CaseCurrentState:
		err = observeCurrentState(ctx, &observation)
	case CaseStructuralPreflight:
		err = observeStructuralPreflight(ctx, &observation)
	case CaseCreateLifecycle:
		err = observeCreateLifecycle(ctx, &observation)
	case CaseAddRelation:
		err = observeAddRelation(ctx, &observation)
	case CaseRemoveRemake:
		err = observeRemoveRemake(ctx, &observation)
	case CasePhysicalFKPolicy:
		err = observePhysicalFKPolicy(ctx, &observation)
	case CaseFileRestart:
		err = observeFileRestart(ctx, &observation)
	case CasePrecommitFaults:
		err = observePrecommitFaults(ctx, &observation)
	case CaseCommitOutcomes:
		err = observeCommitOutcomes(ctx, &observation)
	default:
		return Observation{}, fmt.Errorf("characterize relation migration: unknown case %q", selected)
	}
	if err != nil {
		return Observation{}, fmt.Errorf("characterize relation migration %s: %w", selected, err)
	}
	if observation.Outcomes == nil {
		observation.Outcomes = make([]OutcomeFact, 0)
	}
	return observation, nil
}

func emptyOutcome(name string) OutcomeFact {
	return OutcomeFact{
		Name:        name,
		Definitions: make([]DefinitionFact, 0),
		Sources:     make([]SourceFact, 0),
		State:       emptyStateFact(),
		Intents:     make([]IntentFact, 0),
		Booleans:    make([]NamedBooleanFact, 0),
		Integers:    make([]NamedIntegerFact, 0),
		Strings:     make([]NamedStringFact, 0),
	}
}

func emptyStateFact() StateFact {
	return StateFact{Apps: make([]string, 0), Models: make([]ModelFact, 0)}
}

func recordLoad(metrics *Metrics, name string, report definition.LoadReport) {
	metrics.Loads = append(metrics.Loads, LoadFact{
		Name:                    name,
		DocumentsReceived:       report.DocumentsReceived,
		HeadersValidated:        report.HeadersValidated,
		OperationsDecoded:       report.OperationsDecoded,
		PlannerConstruction:     report.PlannerConstruction,
		DefinitionsPublished:    report.DefinitionsPublished,
		DefinitionSetsPublished: report.DefinitionSetsPublished,
	})
}

func loadOutcome(name string, metrics *Metrics, sources ...definition.Source) (migrations.LoadedDefinitionSet, OutcomeFact) {
	set, report, err := definition.Load(sources...)
	recordLoad(metrics, name, report)
	outcome := emptyOutcome(name)
	outcome.Accepted = err == nil
	outcome.Error = errorFact(err)
	if err == nil {
		outcome.Digest = set.Digest()
		outcome.Definitions = definitionFacts(set.Definitions())
		outcome.Sources = sourceFacts(set.Sources())
	}
	return set, outcome
}

func definitionFacts(values []migrations.Migration) []DefinitionFact {
	result := make([]DefinitionFact, len(values))
	for index, value := range values {
		fact := DefinitionFact{
			App:          value.App,
			Name:         value.Name,
			Dependencies: make([]MigrationKeyFact, len(value.Dependencies)),
			Operations:   make([]OperationFact, len(value.Operations)),
		}
		for dependencyIndex, dependency := range value.Dependencies {
			fact.Dependencies[dependencyIndex] = migrationKeyFact(dependency)
		}
		for operationIndex, operation := range value.Operations {
			fact.Operations[operationIndex] = operationFact(operation)
		}
		result[index] = fact
	}
	return result
}

func operationFact(operation migrations.Operation) OperationFact {
	fact := OperationFact{Model: modelFact("", ir.Model{}), Field: fieldFact(ir.Field{})}
	switch value := operation.(type) {
	case migrations.CreateModel:
		fact.Kind = value.Kind()
		fact.App = value.AppLabel
		fact.ModelName = value.Model.Name
		fact.Model = modelFact(value.AppLabel, value.Model)
	case migrations.AddField:
		fact.Kind = value.Kind()
		fact.App = value.AppLabel
		fact.ModelName = value.ModelName
		fact.Field = fieldFact(value.Field)
	default:
		fact.Kind = operation.Kind()
	}
	return fact
}

func sourceFacts(values []definition.SourceInfo) []SourceFact {
	result := make([]SourceFact, len(values))
	for index, value := range values {
		result[index] = SourceFact{
			SourceID:        value.SourceID,
			ProducerName:    value.Producer.Name,
			ProducerVersion: value.Producer.Version,
			App:             value.Migration.App,
			Migration:       value.Migration.Name,
		}
	}
	return result
}

func stateFact(value migrations.ProjectState) StateFact {
	result := StateFact{FormatVersion: value.FormatVersion(), Apps: value.Apps(), Models: make([]ModelFact, 0)}
	for _, app := range result.Apps {
		schema, exists := value.Schema(app)
		if !exists {
			continue
		}
		for _, model := range schema.Models {
			result.Models = append(result.Models, modelFact(app, model))
		}
	}
	sort.Slice(result.Models, func(left, right int) bool {
		if result.Models[left].App != result.Models[right].App {
			return result.Models[left].App < result.Models[right].App
		}
		return result.Models[left].Name < result.Models[right].Name
	})
	return result
}

func modelFact(app string, value ir.Model) ModelFact {
	result := ModelFact{
		App: app, Name: value.Name, GoName: value.GoName, Table: value.DBTable,
		Fields: make([]FieldFact, len(value.Fields)),
	}
	for index, field := range value.Fields {
		result.Fields[index] = fieldFact(field)
	}
	return result
}

func fieldFact(value ir.Field) FieldFact {
	result := FieldFact{
		Name: value.Name, GoName: value.GoName, Column: value.Column, Kind: string(value.Kind),
		PrimaryKey: value.PrimaryKey, Nullable: value.Nullable, MaxLength: value.MaxLength,
		HasDefault: value.Default != nil, HasRelation: value.Relation != nil,
	}
	if value.Default != nil {
		result.DefaultKind = string(value.Default.Kind)
		switch value.Default.Kind {
		case ir.ScalarString:
			result.DefaultValue = value.Default.String
		case ir.ScalarBoolean:
			result.DefaultValue = strconv.FormatBool(value.Default.Boolean)
		case ir.ScalarInteger:
			result.DefaultValue = strconv.FormatInt(value.Default.Integer, 10)
		}
	}
	if value.Relation != nil {
		result.TargetApp = value.Relation.Target.AppLabel
		result.TargetModel = value.Relation.Target.ModelName
		result.Cardinality = string(value.Relation.Cardinality)
		result.ReverseName = value.Relation.Reverse.Name
		result.ReverseDisabled = value.Relation.Reverse.Disabled
		result.OnDelete = string(value.Relation.OnDelete)
	}
	return result
}

func migrationKeyFact(value migrations.MigrationKey) MigrationKeyFact {
	return MigrationKeyFact{App: value.App, Name: value.Name}
}

func appliedKeyFact(value migrationbackend.AppliedMigration) MigrationKeyFact {
	return MigrationKeyFact{App: value.App, Name: value.Name}
}

func errorFact(err error) ErrorFact {
	result := ErrorFact{Present: err != nil, OperationIndex: migrations.NoOperation}
	if err == nil {
		return result
	}
	var migrationError *migrations.Error
	if errors.As(err, &migrationError) && migrationError != nil {
		result.Category = string(migrationError.Category)
		result.Code = string(migrationError.Code)
		result.Direction = string(migrationError.Direction)
		result.App = migrationError.App
		result.Migration = migrationError.Migration
		result.OperationIndex = migrationError.OperationIndex
		result.Operation = migrationError.Operation
		result.Cause = causeTag(migrationError.Cause)
		result.RollbackCause = causeTag(migrationError.RollbackCause)
	}
	var planningError *migrations.PlanningError
	if errors.As(err, &planningError) && planningError != nil {
		result.Category = string(planningError.Category)
		result.Code = string(planningError.Code)
		result.App = planningError.Node.App
		result.Migration = planningError.Node.Name
	}
	var definitionError *definition.Error
	if errors.As(err, &definitionError) && definitionError != nil {
		result.Category = definitionError.Category
		result.Code = string(definitionError.Code)
		failure := definitionError.Context()
		result.Stage = failure.Stage
		result.SourceID = failure.SourceID
		result.JSONPointer = failure.JSONPointer
		result.Reason = failure.Reason
		result.App = failure.App
		result.Migration = failure.Name
		result.OperationIndex = failure.OperationIndex
	}
	var capabilityError *migrationbackend.CapabilityError
	if errors.As(err, &capabilityError) && capabilityError != nil {
		result.Feature = capabilityError.Feature
	}
	return result
}

var (
	errObservedOperation = errors.New("observer operation fault")
	errObservedRecorder  = errors.New("observer recorder fault")
	errObservedCommit    = errors.New("observer commit fault")
	errObservedUnknown   = errors.New("observer unknown outcome")
)

func causeTag(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errObservedOperation):
		return "observer_operation"
	case errors.Is(err, errObservedRecorder):
		return "observer_recorder"
	case errors.Is(err, errObservedCommit):
		return "observer_commit"
	case errors.Is(err, errObservedUnknown):
		return "observer_unknown"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context_deadline"
	default:
		return "present"
	}
}

type sourceName string

const (
	sourceCurrentAuthor     sourceName = "current_author"
	sourceCurrentScalarBlog sourceName = "current_scalar_blog"
	sourceRelationBlog      sourceName = "relation_blog"
	sourceCurrentTail       sourceName = "current_tail"
	sourceNullableReview    sourceName = "nullable_review"
	sourceRequiredReview    sourceName = "required_review"
)

func sourceFor(name sourceName) definition.Source {
	switch name {
	case sourceCurrentAuthor:
		return definition.Source{SourceID: "owned-current-author", Document: currentAuthorDocument()}
	case sourceCurrentScalarBlog:
		return definition.Source{SourceID: "owned-current-scalar-blog", Document: currentScalarBlogDocument()}
	case sourceRelationBlog:
		return definition.Source{SourceID: "owned-relation-blog", Document: relationBlogDocument()}
	case sourceCurrentTail:
		return definition.Source{SourceID: "owned-current-tail", Document: currentTailDocument()}
	case sourceNullableReview:
		return definition.Source{SourceID: "owned-nullable-review", Document: reviewerDocument(true)}
	case sourceRequiredReview:
		return definition.Source{SourceID: "owned-required-review", Document: reviewerDocument(false)}
	default:
		return definition.Source{SourceID: "unknown-owned-source"}
	}
}

func sourcesFor(names ...sourceName) []definition.Source {
	result := make([]definition.Source, len(names))
	for index, name := range names {
		result[index] = sourceFor(name)
	}
	return result
}

func currentAuthorDocument() []byte {
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"owned-current","version":"1"},` +
		`"migration":{"app":"authors","name":"0001_author","dependencies":[],"operations":[` +
		`{"kind":"create_model","app_label":"authors","model":{` +
		`"name":"author","go_name":"Author","db_table":"authors_author","fields":[` +
		`{"name":"id","go_name":"ID","column":"id","kind":"auto",` +
		`"primary_key":true,"nullable":false,"max_length":0,"default":null}]}}]}}`)
}

func currentScalarBlogDocument() []byte {
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"owned-current","version":"1"},` +
		`"migration":{"app":"blog","name":"0001_article",` +
		`"dependencies":[{"app":"authors","name":"0001_author"}],"operations":[` +
		`{"kind":"create_model","app_label":"blog","model":{` +
		`"name":"article","go_name":"Article","db_table":"blog_article","fields":[` +
		`{"name":"id","go_name":"ID","column":"id","kind":"auto",` +
		`"primary_key":true,"nullable":false,"max_length":0,"default":null}]}}]}}`)
}

func relationBlogDocument() []byte {
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"owned-relation","version":"1"},` +
		`"migration":{"app":"blog","name":"0001_article",` +
		`"dependencies":[{"app":"authors","name":"0001_author"}],"operations":[` +
		`{"kind":"create_model","app_label":"blog","model":{` +
		`"name":"article","go_name":"Article","db_table":"blog_article","fields":[` +
		`{"name":"id","go_name":"ID","column":"id","kind":"auto",` +
		`"primary_key":true,"nullable":false,"max_length":0,"default":null},` +
		`{"name":"author","go_name":"Author","column":"author_id","kind":"foreign_key",` +
		`"primary_key":false,"nullable":false,"max_length":0,"default":null,` +
		`"relation":{"target":{"app_label":"authors","model_name":"author"},` +
		`"cardinality":"many_to_one","reverse":{"name":"articles","disabled":false},` +
		`"on_delete":"protect"}}]}}]}}`)
}

func currentTailDocument() []byte {
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"owned-current","version":"1"},` +
		`"migration":{"app":"blog","name":"0002_article_title",` +
		`"dependencies":[{"app":"blog","name":"0001_article"}],"operations":[` +
		`{"kind":"add_field","app_label":"blog","model_name":"article","field":{` +
		`"name":"title","go_name":"Title","column":"title","kind":"char",` +
		`"primary_key":false,"nullable":false,"max_length":64,` +
		`"default":{"kind":"string","string":"untitled"}}}]}}`)
}

func reviewerDocument(nullable bool) []byte {
	nullability := "false"
	reverse := "required_reviews"
	if nullable {
		nullability = "true"
		reverse = "optional_reviews"
	}
	return []byte(`{"format_version":1,` +
		`"producer":{"name":"owned-relation","version":"1"},` +
		`"migration":{"app":"blog","name":"0003_article_reviewer",` +
		`"dependencies":[{"app":"blog","name":"0002_article_title"}],"operations":[` +
		`{"kind":"add_field","app_label":"blog","model_name":"article","field":{` +
		`"name":"reviewer","go_name":"Reviewer","column":"reviewer_id","kind":"foreign_key",` +
		`"primary_key":false,"nullable":` + nullability + `,"max_length":0,"default":null,` +
		`"relation":{"target":{"app_label":"authors","model_name":"author"},` +
		`"cardinality":"many_to_one","reverse":{"name":"` + reverse + `","disabled":false},` +
		`"on_delete":"protect"}}}]}}`)
}

func withFormatVersion(document []byte, lexical string) []byte {
	return []byte(strings.Replace(string(document), `"format_version":1`, `"format_version":`+lexical, 1))
}

func withoutFormatVersion(document []byte) []byte {
	return []byte(strings.Replace(string(document), `"format_version":1,`, "", 1))
}

func withRetiredCompatibilityTuple(document []byte) []byte {
	return append(
		[]byte(`{"compatibility":{"definition_format":1,"loader_abi":2,"operation_codec":2,"schema_ir":3},`),
		document[1:]...,
	)
}

func observeCurrentFormat(observation *Observation) error {
	cases := []struct {
		name   string
		source definition.Source
	}{
		{name: "exact_current", source: sourceFor(sourceCurrentAuthor)},
		{name: "missing_format_version", source: definition.Source{SourceID: "owned-missing-format", Document: withoutFormatVersion(currentAuthorDocument())}},
		{name: "unknown_format_version", source: definition.Source{SourceID: "owned-unknown-format", Document: withFormatVersion(currentAuthorDocument(), "2")}},
		{name: "wrong_type_format_version", source: definition.Source{SourceID: "owned-wrong-type-format", Document: withFormatVersion(currentAuthorDocument(), `"1"`)}},
		{name: "overflow_format_version", source: definition.Source{SourceID: "owned-overflow-format", Document: withFormatVersion(currentAuthorDocument(), "9223372036854775808")}},
		{name: "retired_compatibility_tuple", source: definition.Source{SourceID: "owned-retired-compatibility", Document: withRetiredCompatibilityTuple(currentAuthorDocument())}},
	}
	for _, item := range cases {
		_, outcome := loadOutcome(item.name, &observation.Metrics, item.source)
		observation.Outcomes = append(observation.Outcomes, outcome)
	}
	constants := emptyOutcome("public_constants")
	constants.Accepted = true
	constants.Integers = append(constants.Integers,
		NamedIntegerFact{Name: "definition_format", Value: definition.DefinitionFormatVersion},
		NamedIntegerFact{Name: "schema_ir", Value: ir.CurrentFormatVersion},
		NamedIntegerFact{Name: "state_format", Value: migrations.StateFormatVersion},
	)
	observation.Outcomes = append(observation.Outcomes, constants)
	return nil
}

func observeCurrentDigest(observation *Observation) error {
	sets := []struct {
		name    string
		sources []definition.Source
	}{
		{name: "scalar_only", sources: sourcesFor(sourceCurrentAuthor, sourceCurrentScalarBlog, sourceCurrentTail)},
		{name: "relation_only", sources: sourcesFor(sourceCurrentAuthor, sourceRelationBlog)},
		{name: "combined", sources: sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail)},
		{name: "combined_permuted", sources: sourcesFor(sourceCurrentTail, sourceRelationBlog, sourceCurrentAuthor)},
	}
	var combinedDigest string
	for _, item := range sets {
		_, outcome := loadOutcome(item.name, &observation.Metrics, item.sources...)
		if !outcome.Accepted {
			return fmt.Errorf("load owned %s set: %s/%s", item.name, outcome.Error.Category, outcome.Error.Code)
		}
		if item.name == "combined" {
			combinedDigest = outcome.Digest
		}
		if item.name == "combined_permuted" {
			outcome.Booleans = append(outcome.Booleans, NamedBooleanFact{Name: "equals_combined_digest", Value: outcome.Digest == combinedDigest})
		}
		observation.Outcomes = append(observation.Outcomes, outcome)
	}
	return nil
}

type databaseFixture struct {
	directory string
	path      string
	backend   *sqlite.Backend
}

func openDatabaseFixture(ctx context.Context, prefix string) (*databaseFixture, error) {
	directory, err := os.MkdirTemp("", "godj-migration-relation-product-"+prefix+"-")
	if err != nil {
		return nil, fmt.Errorf("create disposable SQLite directory: %w", err)
	}
	path := filepath.Join(directory, "product.sqlite3")
	backend, err := openFileBackend(ctx, path)
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(directory))
	}
	return &databaseFixture{directory: directory, path: path, backend: backend}, nil
}

func openFileBackend(ctx context.Context, path string) (*sqlite.Backend, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=rwc"
	backend, err := sqlite.Open(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open disposable SQLite backend: %w", err)
	}
	return backend, nil
}

func (fixture *databaseFixture) close() error {
	if fixture == nil {
		return nil
	}
	var closeErr error
	if fixture.backend != nil {
		closeErr = fixture.backend.Close()
		fixture.backend = nil
	}
	return errors.Join(closeErr, os.RemoveAll(fixture.directory))
}

func (fixture *databaseFixture) closeBackend() error {
	if fixture == nil || fixture.backend == nil {
		return nil
	}
	err := fixture.backend.Close()
	fixture.backend = nil
	return err
}

func (fixture *databaseFixture) reopen(ctx context.Context) error {
	if fixture == nil {
		return errors.New("reopen disposable SQLite backend: fixture is nil")
	}
	if fixture.backend != nil {
		return errors.New("reopen disposable SQLite backend: backend is still open")
	}
	backend, err := openFileBackend(ctx, fixture.path)
	if err != nil {
		return err
	}
	fixture.backend = backend
	return nil
}

type faultMode uint8

const (
	faultNone faultMode = iota
	faultOperation
	faultRecorder
)

type commitMode uint8

const (
	commitNormal commitMode = iota
	commitRolledBack
	commitUnknown
)

type observingBackend struct {
	delegate        *sqlite.Backend
	metrics         *Metrics
	fault           faultMode
	commit          commitMode
	historyOverride []migrationbackend.AppliedMigration
	hasOverride     bool
	intents         []IntentFact
}

func newObservingBackend(delegate *sqlite.Backend, metrics *Metrics) *observingBackend {
	return &observingBackend{delegate: delegate, metrics: metrics, intents: make([]IntentFact, 0)}
}

func (backend *observingBackend) record(name, detail string) {
	backend.metrics.Trace = append(backend.metrics.Trace, TraceEvent{
		Ordinal: len(backend.metrics.Trace) + 1,
		Name:    name,
		Detail:  detail,
	})
}

func traceNames(events []TraceEvent) string {
	names := make([]string, len(events))
	for index, event := range events {
		names[index] = event.Name
	}
	return strings.Join(names, ",")
}

func traceCount(events []TraceEvent, names ...string) int64 {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	var count int64
	for _, event := range events {
		if wanted[event.Name] {
			count++
		}
	}
	return count
}

func traceMutationCount(events []TraceEvent) int64 {
	return traceCount(
		events,
		"create_model",
		"delete_model",
		"add_field",
		"remove_field",
		"record_applied",
		"record_unapplied",
		"commit",
	)
}

func (backend *observingBackend) MigrationCapabilities() migrationbackend.MigrationCapabilities {
	backend.record("capabilities", "")
	return backend.delegate.MigrationCapabilities()
}

func (backend *observingBackend) OpenRevisionFencedSession(ctx context.Context) (migrationbackend.RevisionFencedSession, error) {
	backend.record("open_session", "")
	session, err := backend.delegate.OpenRevisionFencedSession(ctx)
	if err != nil {
		return session, err
	}
	return &observingSession{delegate: session, owner: backend}, nil
}

type observingSession struct {
	delegate migrationbackend.RevisionFencedSession
	owner    *observingBackend
}

func (session *observingSession) ReadAppliedMigrations(ctx context.Context) ([]migrationbackend.AppliedMigration, error) {
	session.owner.record("read_history", "")
	records, err := session.delegate.ReadAppliedMigrations(ctx)
	if err != nil {
		return records, err
	}
	if session.owner.hasOverride {
		return append([]migrationbackend.AppliedMigration(nil), session.owner.historyOverride...), nil
	}
	return records, nil
}

func (session *observingSession) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	detail := transition.Migration.App + "." + transition.Migration.Name
	session.owner.record("begin_migration", detail)
	session.owner.intents = append(session.owner.intents, intentFact(transition, intent))
	transaction, err := session.delegate.BeginMigration(ctx, transition, intent)
	if err != nil {
		return transaction, err
	}
	return &observingTransaction{delegate: transaction, owner: session.owner}, nil
}

func observerIntentContainsRelation(intent migrationbackend.MigrationIntent) bool {
	for _, operation := range intent.Operations {
		if len(operation.Targets) != 0 {
			return true
		}
		for _, model := range []ir.Model{operation.Before, operation.After} {
			for _, field := range model.Fields {
				if field.Relation != nil {
					return true
				}
			}
		}
	}
	return false
}

func (session *observingSession) Close(ctx context.Context) error {
	session.owner.record("close_session", "")
	return session.delegate.Close(ctx)
}

type observingTransaction struct {
	delegate migrationbackend.RevisionFencedTransaction
	owner    *observingBackend
}

func (transaction *observingTransaction) CreateModel(ctx context.Context, model ir.Model) error {
	transaction.owner.record("create_model", model.DBTable)
	return transaction.delegate.CreateModel(ctx, model)
}

func (transaction *observingTransaction) DeleteModel(ctx context.Context, model ir.Model) error {
	transaction.owner.record("delete_model", model.DBTable)
	return transaction.delegate.DeleteModel(ctx, model)
}

func (transaction *observingTransaction) AddField(ctx context.Context, model ir.Model, field ir.Field) error {
	transaction.owner.record("add_field", model.DBTable+"."+field.Column)
	if transaction.owner.fault == faultOperation {
		return errObservedOperation
	}
	return transaction.delegate.AddField(ctx, model, field)
}

func (transaction *observingTransaction) RemoveField(ctx context.Context, model ir.Model, field ir.Field) error {
	transaction.owner.record("remove_field", model.DBTable+"."+field.Column)
	if transaction.owner.fault == faultOperation {
		return errObservedOperation
	}
	return transaction.delegate.RemoveField(ctx, model, field)
}

func (transaction *observingTransaction) RecordApplied(ctx context.Context, app, name string) error {
	transaction.owner.record("record_applied", app+"."+name)
	if transaction.owner.fault == faultRecorder {
		return errObservedRecorder
	}
	return transaction.delegate.RecordApplied(ctx, app, name)
}

func (transaction *observingTransaction) RecordUnapplied(ctx context.Context, app, name string) error {
	transaction.owner.record("record_unapplied", app+"."+name)
	if transaction.owner.fault == faultRecorder {
		return errObservedRecorder
	}
	return transaction.delegate.RecordUnapplied(ctx, app, name)
}

func (transaction *observingTransaction) CommitFenced(ctx context.Context) (migrationbackend.CommitOutcome, error) {
	transaction.owner.record("commit", strconv.Itoa(int(transaction.owner.commit)))
	switch transaction.owner.commit {
	case commitNormal:
		return transaction.delegate.CommitFenced(ctx)
	case commitRolledBack:
		rollbackErr := transaction.delegate.Rollback(ctx)
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitRolledBack}, errors.Join(errObservedCommit, rollbackErr)
	case commitUnknown:
		rollbackErr := transaction.delegate.Rollback(ctx)
		return migrationbackend.CommitOutcome{Durability: migrationbackend.CommitUnknown}, errors.Join(errObservedUnknown, rollbackErr)
	default:
		return migrationbackend.CommitOutcome{}, errObservedUnknown
	}
}

func (transaction *observingTransaction) Rollback(ctx context.Context) error {
	transaction.owner.record("rollback", "")
	return transaction.delegate.Rollback(ctx)
}

func intentFact(transition migrationbackend.HistoryTransition, intent migrationbackend.MigrationIntent) IntentFact {
	fact := IntentFact{
		Transition:  MigrationKeyFact{App: transition.Migration.App, Name: transition.Migration.Name},
		Kind:        strconv.Itoa(int(transition.Kind)),
		HasRelation: observerIntentContainsRelation(intent),
		Operations:  make([]IntentOperationFact, len(intent.Operations)),
	}
	for index, operation := range intent.Operations {
		operationFact := IntentOperationFact{
			Index:   operation.OperationIndex,
			Kind:    int64(operation.Kind),
			Before:  modelFact("", operation.Before),
			After:   modelFact("", operation.After),
			Targets: make([]IntentTargetFact, len(operation.Targets)),
		}
		for targetIndex, target := range operation.Targets {
			operationFact.Targets[targetIndex] = IntentTargetFact{
				Source: fieldFact(target.SourceField),
				Target: modelFact("", target.TargetModel),
				Key:    fieldFact(target.TargetKey),
			}
		}
		fact.Operations[index] = operationFact
	}
	return fact
}

func cloneIntentFacts(values []IntentFact) []IntentFact {
	result := make([]IntentFact, len(values))
	copy(result, values)
	return result
}

func readDatabaseSnapshot(
	ctx context.Context,
	stage, path string,
	backend *sqlite.Backend,
) (snapshot DatabaseSnapshot, resultErr error) {
	snapshot = DatabaseSnapshot{
		Stage:       stage,
		Tables:      make([]TableFact, 0),
		ForeignKeys: make([]ForeignKeyFact, 0),
		Rows:        make([]RowFact, 0),
		Sequences:   make([]SequenceFact, 0),
		History:     make([]MigrationKeyFact, 0),
	}
	if backend != nil {
		records, err := backend.ReadAppliedMigrations(ctx)
		if err != nil {
			return snapshot, fmt.Errorf("read public applied history: %w", err)
		}
		for _, record := range records {
			snapshot.History = append(snapshot.History, appliedKeyFact(record))
		}
		sort.Slice(snapshot.History, func(left, right int) bool {
			if snapshot.History[left].App != snapshot.History[right].App {
				return snapshot.History[left].App < snapshot.History[right].App
			}
			return snapshot.History[left].Name < snapshot.History[right].Name
		})
	}

	inspector, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return snapshot, fmt.Errorf("open read-only SQLite inspector: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, inspector.Close()) }()
	inspector.SetMaxOpenConns(1)
	inspector.SetMaxIdleConns(1)
	if err := inspector.PingContext(ctx); err != nil {
		return snapshot, fmt.Errorf("ping read-only SQLite inspector: %w", err)
	}

	tableRows, err := inspector.QueryContext(ctx,
		`SELECT "name", COALESCE("sql", '') FROM main.sqlite_schema `+
			`WHERE "type"='table' AND "name" NOT LIKE 'sqlite_%' `+
			`AND "name" NOT IN ('godj_migrations','godj_migration_revision') ORDER BY "name"`,
	)
	if err != nil {
		return snapshot, fmt.Errorf("read SQLite product tables: %w", err)
	}
	tableNames := make([]string, 0)
	for tableRows.Next() {
		var table TableFact
		if err := tableRows.Scan(&table.Name, &table.SQL); err != nil {
			_ = tableRows.Close()
			return snapshot, fmt.Errorf("scan SQLite product table: %w", err)
		}
		table.Columns = make([]ColumnFact, 0)
		snapshot.Tables = append(snapshot.Tables, table)
		tableNames = append(tableNames, table.Name)
	}
	if err := errors.Join(tableRows.Err(), tableRows.Close()); err != nil {
		return snapshot, fmt.Errorf("finish SQLite product tables: %w", err)
	}

	for tableIndex, table := range tableNames {
		columns, err := readColumns(ctx, inspector, table)
		if err != nil {
			return snapshot, err
		}
		snapshot.Tables[tableIndex].Columns = columns
		foreignKeys, err := readForeignKeys(ctx, inspector, table)
		if err != nil {
			return snapshot, err
		}
		snapshot.ForeignKeys = append(snapshot.ForeignKeys, foreignKeys...)
		rows, err := readTableRows(ctx, inspector, table, columns)
		if err != nil {
			return snapshot, err
		}
		snapshot.Rows = append(snapshot.Rows, rows...)
	}
	sort.Slice(snapshot.ForeignKeys, func(left, right int) bool {
		leftValue, rightValue := snapshot.ForeignKeys[left], snapshot.ForeignKeys[right]
		if leftValue.Table != rightValue.Table {
			return leftValue.Table < rightValue.Table
		}
		if leftValue.From != rightValue.From {
			return leftValue.From < rightValue.From
		}
		if leftValue.ID != rightValue.ID {
			return leftValue.ID < rightValue.ID
		}
		return leftValue.Sequence < rightValue.Sequence
	})
	sequences, err := readSequences(ctx, inspector)
	if err != nil {
		return snapshot, err
	}
	snapshot.Sequences = sequences
	return snapshot, nil
}

func readColumns(ctx context.Context, inspector *sql.DB, table string) ([]ColumnFact, error) {
	rows, err := inspector.QueryContext(ctx, `PRAGMA main.table_xinfo(`+quoteSQLiteIdentifier(table)+`)`)
	if err != nil {
		return nil, fmt.Errorf("read SQLite columns for %s: %w", table, err)
	}
	result := make([]ColumnFact, 0)
	for rows.Next() {
		var column ColumnFact
		var notNull int64
		var defaultValue any
		if err := rows.Scan(
			&column.Position,
			&column.Name,
			&column.Type,
			&notNull,
			&defaultValue,
			&column.PrimaryKey,
			&column.Hidden,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan SQLite column for %s: %w", table, err)
		}
		column.NotNull = notNull != 0
		column.Default = stableSQLValue(defaultValue)
		result = append(result, column)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("finish SQLite columns for %s: %w", table, err)
	}
	return result, nil
}

func readForeignKeys(ctx context.Context, inspector *sql.DB, table string) ([]ForeignKeyFact, error) {
	rows, err := inspector.QueryContext(ctx, `PRAGMA main.foreign_key_list(`+quoteSQLiteIdentifier(table)+`)`)
	if err != nil {
		return nil, fmt.Errorf("read SQLite foreign keys for %s: %w", table, err)
	}
	result := make([]ForeignKeyFact, 0)
	for rows.Next() {
		var fact ForeignKeyFact
		fact.Table = table
		if err := rows.Scan(
			&fact.ID,
			&fact.Sequence,
			&fact.Target,
			&fact.From,
			&fact.To,
			&fact.OnUpdate,
			&fact.OnDelete,
			&fact.Match,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan SQLite foreign key for %s: %w", table, err)
		}
		result = append(result, fact)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("finish SQLite foreign keys for %s: %w", table, err)
	}
	return result, nil
}

func readTableRows(
	ctx context.Context,
	inspector *sql.DB,
	table string,
	columns []ColumnFact,
) ([]RowFact, error) {
	if len(columns) == 0 {
		return make([]RowFact, 0), nil
	}
	columnNames := make([]string, len(columns))
	for index, column := range columns {
		columnNames[index] = quoteSQLiteIdentifier(column.Name)
	}
	orderColumn := columnNames[0]
	for index, column := range columns {
		if column.PrimaryKey > 0 {
			orderColumn = columnNames[index]
			break
		}
	}
	statement := `SELECT ` + strings.Join(columnNames, ",") + ` FROM ` + quoteSQLiteIdentifier(table) + ` ORDER BY ` + orderColumn
	rows, err := inspector.QueryContext(ctx, statement)
	if err != nil {
		return nil, fmt.Errorf("read SQLite rows for %s: %w", table, err)
	}
	result := make([]RowFact, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan SQLite row for %s: %w", table, err)
		}
		fact := RowFact{Table: table, Values: make([]NamedStringFact, len(columns))}
		for index, column := range columns {
			fact.Values[index] = NamedStringFact{Name: column.Name, Value: stableSQLValue(values[index])}
		}
		result = append(result, fact)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("finish SQLite rows for %s: %w", table, err)
	}
	return result, nil
}

func readSequences(ctx context.Context, inspector *sql.DB) ([]SequenceFact, error) {
	var exists int
	if err := inspector.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM main.sqlite_schema WHERE "type"='table' AND "name"='sqlite_sequence'`,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("inspect SQLite sequence table: %w", err)
	}
	result := make([]SequenceFact, 0)
	if exists == 0 {
		return result, nil
	}
	rows, err := inspector.QueryContext(ctx, `SELECT "name", "seq" FROM main.sqlite_sequence ORDER BY "name"`)
	if err != nil {
		return nil, fmt.Errorf("read SQLite sequences: %w", err)
	}
	for rows.Next() {
		var fact SequenceFact
		if err := rows.Scan(&fact.Table, &fact.Value); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan SQLite sequence: %w", err)
		}
		result = append(result, fact)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("finish SQLite sequences: %w", err)
	}
	return result, nil
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func stableSQLValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "NULL"
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	case []byte:
		return string(typed)
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func snapshotsEqual(left, right DatabaseSnapshot) bool {
	if len(left.Tables) != len(right.Tables) ||
		len(left.Rows) != len(right.Rows) ||
		!equalComparableFacts(left.ForeignKeys, right.ForeignKeys) ||
		!equalComparableFacts(left.Sequences, right.Sequences) ||
		!equalComparableFacts(left.History, right.History) {
		return false
	}
	for index := range left.Tables {
		leftTable, rightTable := left.Tables[index], right.Tables[index]
		if leftTable.Name != rightTable.Name || leftTable.SQL != rightTable.SQL ||
			!equalComparableFacts(leftTable.Columns, rightTable.Columns) {
			return false
		}
	}
	for index := range left.Rows {
		leftRow, rightRow := left.Rows[index], right.Rows[index]
		if leftRow.Table != rightRow.Table || !equalComparableFacts(leftRow.Values, rightRow.Values) {
			return false
		}
	}
	return true
}

func equalComparableFacts[T comparable](left, right []T) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func seedBaseRows(ctx context.Context, backend *sqlite.Backend) error {
	if _, err := backend.ExecContext(ctx, `INSERT INTO "authors_author" ("id") VALUES (41), (47)`); err != nil {
		return fmt.Errorf("seed owned authors: %w", err)
	}
	if _, err := backend.ExecContext(ctx,
		`INSERT INTO "blog_article" ("id", "author_id", "title") VALUES (51,41,'first'), (59,47,'second')`,
	); err != nil {
		return fmt.Errorf("seed owned articles: %w", err)
	}
	if _, err := backend.ExecContext(ctx, `UPDATE main.sqlite_sequence SET "seq"=100 WHERE "name"='blog_article'`); err != nil {
		return fmt.Errorf("set owned article sequence: %w", err)
	}
	return nil
}

func observeCurrentABI(ctx context.Context, observation *Observation) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "current-abi")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()

	sources := sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail)
	loaded, load := loadOutcome("current_load", &observation.Metrics, sources...)
	if !load.Accepted {
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	load.Integers = append(load.Integers,
		NamedIntegerFact{Name: "definition_format", Value: definition.DefinitionFormatVersion},
		NamedIntegerFact{Name: "schema_ir", Value: ir.CurrentFormatVersion},
		NamedIntegerFact{Name: "state_format", Value: migrations.StateFormatVersion},
	)
	load.Booleans = append(load.Booleans,
		NamedBooleanFact{Name: "retired_compatibility_tuple_present", Value: false},
		NamedBooleanFact{Name: "scalar_and_relation_share_format", Value: true},
	)
	observation.Outcomes = append(observation.Outcomes, load)

	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	latest := emptyOutcome("latest")
	latest.Accepted = migrateErr == nil
	latest.State = stateFact(state)
	latest.Error = errorFact(migrateErr)
	observation.Outcomes = append(observation.Outcomes, latest)
	if migrateErr != nil {
		return nil
	}
	state, migrateErr = (migrations.Executor{Backend: probe}).Migrate(
		ctx,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.ZeroTarget("blog")),
	)

	zero := emptyOutcome("zero_blog")
	zero.Accepted = migrateErr == nil
	zero.State = stateFact(state)
	zero.Error = errorFact(migrateErr)
	observation.Outcomes = append(observation.Outcomes, zero)
	return nil
}

func observeCurrentState(ctx context.Context, observation *Observation) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "current-state")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()

	loaded, load := loadOutcome(
		"current_load",
		&observation.Metrics,
		sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail)...,
	)
	observation.Outcomes = append(observation.Outcomes, load)
	if !load.Accepted {
		return nil
	}
	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	forward := emptyOutcome("forward")
	forward.Accepted = migrateErr == nil
	forward.State = stateFact(state)
	forward.Error = errorFact(migrateErr)
	forward.Intents = cloneIntentFacts(probe.intents)
	if len(forward.State.Models) > 0 {
		model, found := state.Model("blog", "article")
		if found && len(model.Fields) > 1 {
			model.Fields[1].Name = "mutated-copy"
		}
		fresh, freshFound := state.Model("blog", "article")
		forward.Booleans = append(forward.Booleans, NamedBooleanFact{
			Name:  "state_accessor_isolated",
			Value: found && freshFound && len(fresh.Fields) > 1 && fresh.Fields[1].Name != "mutated-copy",
		})
	}
	observation.Outcomes = append(observation.Outcomes, forward)
	if migrateErr != nil {
		return nil
	}

	intentCount := len(probe.intents)
	state, migrateErr = (migrations.Executor{Backend: probe}).Migrate(
		ctx,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.ZeroTarget("blog")),
	)

	backward := emptyOutcome("backward")
	backward.Accepted = migrateErr == nil
	backward.State = stateFact(state)
	backward.Error = errorFact(migrateErr)
	backward.Intents = cloneIntentFacts(probe.intents[intentCount:])
	observation.Outcomes = append(observation.Outcomes, backward)
	return nil
}

func observeStructuralPreflight(ctx context.Context, observation *Observation) error {
	if err := observeStaticStructuralFailure(ctx, observation); err != nil {
		return err
	}
	if err := observeHistoryStructuralFailure(ctx, observation); err != nil {
		return err
	}
	return observePhysicalStructuralFailure(ctx, observation)
}

func observeStaticStructuralFailure(ctx context.Context, observation *Observation) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "preflight-static")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()
	invalid := definition.Source{
		SourceID: "owned-missing-target",
		Document: []byte(strings.Replace(string(relationBlogDocument()), `"model_name":"author"`, `"model_name":"missing"`, 1)),
	}
	loaded, load := loadOutcome("static_invalid_load", &observation.Metrics, sourceFor(sourceCurrentAuthor), invalid)
	if !load.Accepted {
		load.Name = "static_invalid"
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	traceStart := len(observation.Metrics.Trace)
	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	outcome := emptyOutcome("static_invalid")
	outcome.Accepted = migrateErr == nil
	outcome.State = stateFact(state)
	outcome.Error = errorFact(migrateErr)
	outcome.Integers = append(outcome.Integers, NamedIntegerFact{
		Name:  "backend_trace_events",
		Value: int64(len(observation.Metrics.Trace) - traceStart),
	})
	outcome.Strings = append(outcome.Strings,
		NamedStringFact{Name: "capability_unavailable_error", Value: "migration_capability_unavailable"},
		NamedStringFact{Name: "trace", Value: traceNames(observation.Metrics.Trace[traceStart:])},
	)
	observation.Outcomes = append(observation.Outcomes, outcome)
	return nil
}

func observeHistoryStructuralFailure(ctx context.Context, observation *Observation) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "preflight-history")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()
	loaded, load := loadOutcome("history_invalid_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog)...)
	if !load.Accepted {
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	probe.hasOverride = true
	probe.historyOverride = []migrationbackend.AppliedMigration{{App: "blog", Name: "0001_article"}}
	traceStart := len(observation.Metrics.Trace)
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	outcome := emptyOutcome("history_invalid")
	outcome.Accepted = migrateErr == nil
	outcome.State = stateFact(state)
	outcome.Error = errorFact(migrateErr)
	events := observation.Metrics.Trace[traceStart:]
	outcome.Integers = append(outcome.Integers,
		NamedIntegerFact{Name: "begin_migration_events", Value: traceCount(events, "begin_migration")},
		NamedIntegerFact{Name: "history_read_events", Value: traceCount(events, "read_history")},
		NamedIntegerFact{Name: "mutation_events", Value: traceMutationCount(events)},
		NamedIntegerFact{Name: "session_open_events", Value: traceCount(events, "open_session")},
	)
	outcome.Strings = append(outcome.Strings, NamedStringFact{Name: "trace", Value: traceNames(events)})
	observation.Outcomes = append(observation.Outcomes, outcome)
	return nil
}

func observePhysicalStructuralFailure(ctx context.Context, observation *Observation) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "preflight-physical")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()
	base, baseLoad := loadOutcome("physical_base_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail)...)
	if !baseLoad.Accepted {
		observation.Outcomes = append(observation.Outcomes, baseLoad)
		return nil
	}
	if _, err := (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, base, migrations.LatestLifecycleRequest()); err != nil {
		return fmt.Errorf("apply physical preflight base: %w", err)
	}
	if err := seedBaseRows(ctx, fixture.backend); err != nil {
		return err
	}
	before, err := readDatabaseSnapshot(ctx, "before", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	loaded, load := loadOutcome("physical_invalid_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail, sourceRequiredReview)...)
	if !load.Accepted {
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	traceStart := len(observation.Metrics.Trace)
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	after, err := readDatabaseSnapshot(ctx, "after", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	outcome := emptyOutcome("physical_invalid")
	outcome.Accepted = migrateErr == nil
	outcome.State = stateFact(state)
	outcome.Error = errorFact(migrateErr)
	events := observation.Metrics.Trace[traceStart:]
	outcome.Booleans = append(outcome.Booleans, NamedBooleanFact{Name: "durable_unchanged", Value: snapshotsEqual(before, after)})
	outcome.Integers = append(outcome.Integers,
		NamedIntegerFact{Name: "begin_migration_events", Value: traceCount(events, "begin_migration")},
		NamedIntegerFact{Name: "history_read_events", Value: traceCount(events, "read_history")},
		NamedIntegerFact{Name: "mutation_events", Value: traceMutationCount(events)},
	)
	outcome.Strings = append(outcome.Strings, NamedStringFact{Name: "trace", Value: traceNames(events)})
	observation.Outcomes = append(observation.Outcomes, outcome)
	return nil
}

func observeCreateLifecycle(ctx context.Context, observation *Observation) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "create")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()
	observation.Database = &DatabaseState{Snapshots: make([]DatabaseSnapshot, 0)}
	loaded, load := loadOutcome("create_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog)...)
	observation.Outcomes = append(observation.Outcomes, load)
	if !load.Accepted {
		return nil
	}
	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	apply := emptyOutcome("apply")
	apply.Accepted = migrateErr == nil
	apply.State = stateFact(state)
	apply.Error = errorFact(migrateErr)
	apply.Intents = cloneIntentFacts(probe.intents)
	observation.Outcomes = append(observation.Outcomes, apply)
	if migrateErr != nil {
		return nil
	}
	snapshot, err := readDatabaseSnapshot(ctx, "applied", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, snapshot)
	intentCount := len(probe.intents)
	state, migrateErr = (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.TargetedLifecycleRequest(migrations.ZeroTarget("blog")))
	unapply := emptyOutcome("unapply")
	unapply.Accepted = migrateErr == nil
	unapply.State = stateFact(state)
	unapply.Error = errorFact(migrateErr)
	unapply.Intents = cloneIntentFacts(probe.intents[intentCount:])
	observation.Outcomes = append(observation.Outcomes, unapply)
	if migrateErr != nil {
		return nil
	}
	snapshot, err = readDatabaseSnapshot(ctx, "unapplied", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, snapshot)
	intentCount = len(probe.intents)
	state, migrateErr = (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	reapply := emptyOutcome("reapply")
	reapply.Accepted = migrateErr == nil
	reapply.State = stateFact(state)
	reapply.Error = errorFact(migrateErr)
	reapply.Intents = cloneIntentFacts(probe.intents[intentCount:])
	observation.Outcomes = append(observation.Outcomes, reapply)
	if migrateErr == nil {
		snapshot, err = readDatabaseSnapshot(ctx, "reapplied", fixture.path, fixture.backend)
		if err != nil {
			return err
		}
		observation.Database.Snapshots = append(observation.Database.Snapshots, snapshot)
	}
	return nil
}

func observeAddRelation(ctx context.Context, observation *Observation) error {
	observation.Database = &DatabaseState{Snapshots: make([]DatabaseSnapshot, 0)}
	if err := observeAddRelationCase(ctx, observation, "nullable_populated", sourceNullableReview, true, true); err != nil {
		return err
	}
	if err := observeAddRelationCase(ctx, observation, "required_empty", sourceRequiredReview, false, true); err != nil {
		return err
	}
	return observeAddRelationCase(ctx, observation, "required_populated", sourceRequiredReview, true, false)
}

func observeAddRelationCase(
	ctx context.Context,
	observation *Observation,
	name string,
	reviewer sourceName,
	populateSource bool,
	wantSuccess bool,
) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "add-"+name)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()
	base, load := loadOutcome(name+"_base_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail)...)
	if !load.Accepted {
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	if _, err := (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, base, migrations.LatestLifecycleRequest()); err != nil {
		return fmt.Errorf("apply %s base: %w", name, err)
	}
	if populateSource {
		if err := seedBaseRows(ctx, fixture.backend); err != nil {
			return err
		}
	} else if _, err := fixture.backend.ExecContext(ctx, `INSERT INTO "authors_author" ("id") VALUES (41)`); err != nil {
		return fmt.Errorf("seed %s target: %w", name, err)
	}
	before, err := readDatabaseSnapshot(ctx, name+"_before", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, before)

	loaded, load := loadOutcome(name+"_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail, reviewer)...)
	if !load.Accepted {
		load.Name = name
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	after, err := readDatabaseSnapshot(ctx, name+"_after", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, after)
	outcome := emptyOutcome(name)
	outcome.Accepted = migrateErr == nil
	outcome.State = stateFact(state)
	outcome.Error = errorFact(migrateErr)
	outcome.Intents = cloneIntentFacts(probe.intents)
	outcome.Booleans = append(outcome.Booleans,
		NamedBooleanFact{Name: "durable_unchanged", Value: snapshotsEqual(before, after)},
		NamedBooleanFact{Name: "setup_expected_success", Value: wantSuccess},
	)
	observation.Outcomes = append(observation.Outcomes, outcome)
	if migrateErr != nil {
		return nil
	}
	if err := fixture.closeBackend(); err != nil {
		return err
	}
	if err := fixture.reopen(ctx); err != nil {
		return err
	}
	fresh, freshLoad := loadOutcome(name+"_reopen_load", &observation.Metrics, sourcesFor(reviewer, sourceCurrentTail, sourceRelationBlog, sourceCurrentAuthor)...)
	if !freshLoad.Accepted {
		return fmt.Errorf("reload %s sources: %s/%s", name, freshLoad.Error.Category, freshLoad.Error.Code)
	}
	state, migrateErr = (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, fresh, migrations.LatestLifecycleRequest())
	reopen := emptyOutcome(name + "_reopen")
	reopen.Accepted = migrateErr == nil
	reopen.State = stateFact(state)
	reopen.Error = errorFact(migrateErr)
	observation.Outcomes = append(observation.Outcomes, reopen)
	if migrateErr == nil {
		snapshot, err := readDatabaseSnapshot(ctx, name+"_reopen", fixture.path, fixture.backend)
		if err != nil {
			return err
		}
		observation.Database.Snapshots = append(observation.Database.Snapshots, snapshot)
	}
	return nil
}

func observeRemoveRemake(ctx context.Context, observation *Observation) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "remove")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()
	observation.Database = &DatabaseState{Snapshots: make([]DatabaseSnapshot, 0)}
	loaded, load := loadOutcome(
		"remove_load",
		&observation.Metrics,
		sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail, sourceNullableReview)...,
	)
	observation.Outcomes = append(observation.Outcomes, load)
	if !load.Accepted {
		return nil
	}
	if _, err := (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		return fmt.Errorf("apply remove/remake chain: %w", err)
	}
	if err := seedBaseRows(ctx, fixture.backend); err != nil {
		return err
	}
	if _, err := fixture.backend.ExecContext(ctx, `UPDATE "blog_article" SET "reviewer_id"=47 WHERE "id"=51`); err != nil {
		return fmt.Errorf("seed owned reviewer: %w", err)
	}
	before, err := readDatabaseSnapshot(ctx, "before_remove", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, before)

	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(
		ctx,
		loaded,
		migrations.TargetedLifecycleRequest(migrations.NamedTarget(migrations.MigrationKey{App: "blog", Name: "0002_article_title"})),
	)

	after, err := readDatabaseSnapshot(ctx, "after_remove", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, after)
	remove := emptyOutcome("remove")
	remove.Accepted = migrateErr == nil
	remove.State = stateFact(state)
	remove.Error = errorFact(migrateErr)
	remove.Intents = cloneIntentFacts(probe.intents)
	remove.Booleans = append(remove.Booleans,
		NamedBooleanFact{Name: "rows_preserved", Value: rowProjectionEqual(before.Rows, after.Rows, "reviewer_id")},
		NamedBooleanFact{Name: "sequence_preserved", Value: sequenceValue(before.Sequences, "blog_article") == sequenceValue(after.Sequences, "blog_article")},
	)
	observation.Outcomes = append(observation.Outcomes, remove)
	if migrateErr != nil {
		return nil
	}

	state, migrateErr = (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	reapply := emptyOutcome("reapply")
	reapply.Accepted = migrateErr == nil
	reapply.State = stateFact(state)
	reapply.Error = errorFact(migrateErr)
	observation.Outcomes = append(observation.Outcomes, reapply)
	if migrateErr == nil {
		snapshot, err := readDatabaseSnapshot(ctx, "after_reapply", fixture.path, fixture.backend)
		if err != nil {
			return err
		}
		observation.Database.Snapshots = append(observation.Database.Snapshots, snapshot)
	}
	return nil
}

func rowProjectionEqual(left, right []RowFact, omitted string) bool {
	project := func(values []RowFact) string {
		var builder strings.Builder
		for _, row := range values {
			builder.WriteString(row.Table)
			builder.WriteByte(':')
			for _, field := range row.Values {
				if field.Name == omitted {
					continue
				}
				builder.WriteString(field.Name)
				builder.WriteByte('=')
				builder.WriteString(field.Value)
				builder.WriteByte(';')
			}
			builder.WriteByte('|')
		}
		return builder.String()
	}
	return project(left) == project(right)
}

func sequenceValue(values []SequenceFact, table string) int64 {
	for _, value := range values {
		if value.Table == table {
			return value.Value
		}
	}
	return -1
}

func observePhysicalFKPolicy(ctx context.Context, observation *Observation) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "physical-fk")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()
	observation.Database = &DatabaseState{Snapshots: make([]DatabaseSnapshot, 0)}
	loaded, load := loadOutcome("physical_fk_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog)...)
	observation.Outcomes = append(observation.Outcomes, load)
	if !load.Accepted {
		return nil
	}
	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	outcome := emptyOutcome("physical_fk")
	outcome.Accepted = migrateErr == nil
	outcome.State = stateFact(state)
	outcome.Error = errorFact(migrateErr)
	outcome.Intents = cloneIntentFacts(probe.intents)
	capabilities := fixture.backend.MigrationCapabilities()
	outcome.Booleans = append(outcome.Booleans,
		NamedBooleanFact{Name: "create_model_foreign_keys", Value: capabilities.CreateModelForeignKeys},
		NamedBooleanFact{Name: "add_nullable_foreign_key", Value: capabilities.AddNullableForeignKey},
		NamedBooleanFact{Name: "add_required_foreign_key_to_empty", Value: capabilities.AddRequiredForeignKeyToEmptyTable},
		NamedBooleanFact{Name: "remove_foreign_key_by_remake", Value: capabilities.RemoveForeignKey},
	)
	if migrateErr == nil {
		if _, err := fixture.backend.ExecContext(ctx, `INSERT INTO "authors_author" ("id") VALUES (41)`); err != nil {
			return fmt.Errorf("seed physical FK target: %w", err)
		}
		if _, err := fixture.backend.ExecContext(ctx, `INSERT INTO "blog_article" ("id","author_id") VALUES (51,41)`); err != nil {
			return fmt.Errorf("seed physical FK source: %w", err)
		}
		_, orphanErr := fixture.backend.ExecContext(ctx, `INSERT INTO "blog_article" ("id","author_id") VALUES (52,9999)`)
		_, parentErr := fixture.backend.ExecContext(ctx, `DELETE FROM "authors_author" WHERE "id"=41`)
		outcome.Booleans = append(outcome.Booleans,
			NamedBooleanFact{Name: "orphan_rejected", Value: orphanErr != nil},
			NamedBooleanFact{Name: "parent_delete_rejected", Value: parentErr != nil},
		)
		snapshot, err := readDatabaseSnapshot(ctx, "physical", fixture.path, fixture.backend)
		if err != nil {
			return err
		}
		observation.Database.Snapshots = append(observation.Database.Snapshots, snapshot)
	}
	observation.Outcomes = append(observation.Outcomes, outcome)
	return nil
}

func observeFileRestart(ctx context.Context, observation *Observation) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "restart")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()
	observation.Database = &DatabaseState{Snapshots: make([]DatabaseSnapshot, 0)}

	initial, load := loadOutcome("process_a_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail)...)
	if !load.Accepted {
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	initialDigest := load.Digest
	state, migrateErr := (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, initial, migrations.LatestLifecycleRequest())
	processA := emptyOutcome("process_a")
	processA.Accepted = migrateErr == nil
	processA.Digest = initialDigest
	processA.State = stateFact(state)
	processA.Error = errorFact(migrateErr)
	observation.Outcomes = append(observation.Outcomes, processA)
	if migrateErr != nil {
		return nil
	}
	if err := seedBaseRows(ctx, fixture.backend); err != nil {
		return err
	}
	snapshot, err := readDatabaseSnapshot(ctx, "process_a", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, snapshot)
	if err := fixture.closeBackend(); err != nil {
		return err
	}
	if err := fixture.reopen(ctx); err != nil {
		return err
	}

	reopened, load := loadOutcome("process_b_load", &observation.Metrics, sourcesFor(sourceCurrentTail, sourceRelationBlog, sourceCurrentAuthor)...)
	if !load.Accepted {
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	state, migrateErr = (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, reopened, migrations.LatestLifecycleRequest())
	processB := emptyOutcome("process_b_noop")
	processB.Accepted = migrateErr == nil
	processB.Digest = load.Digest
	processB.State = stateFact(state)
	processB.Error = errorFact(migrateErr)
	processB.Booleans = append(processB.Booleans, NamedBooleanFact{Name: "digest_matches_process_a", Value: load.Digest == initialDigest})
	observation.Outcomes = append(observation.Outcomes, processB)
	if migrateErr != nil {
		return nil
	}
	snapshot, err = readDatabaseSnapshot(ctx, "process_b_noop", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, snapshot)
	state, migrateErr = (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, reopened, migrations.TargetedLifecycleRequest(migrations.ZeroTarget("blog")))
	branch := emptyOutcome("process_b_branch")
	branch.Accepted = migrateErr == nil
	branch.State = stateFact(state)
	branch.Error = errorFact(migrateErr)
	observation.Outcomes = append(observation.Outcomes, branch)
	if migrateErr != nil {
		return nil
	}
	snapshot, err = readDatabaseSnapshot(ctx, "process_b_branch", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, snapshot)
	if err := fixture.closeBackend(); err != nil {
		return err
	}
	if err := fixture.reopen(ctx); err != nil {
		return err
	}

	third, load := loadOutcome("process_c_load", &observation.Metrics, sourcesFor(sourceRelationBlog, sourceCurrentAuthor, sourceCurrentTail)...)
	if !load.Accepted {
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	state, migrateErr = (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, third, migrations.LatestLifecycleRequest())
	processC := emptyOutcome("process_c_reapply")
	processC.Accepted = migrateErr == nil
	processC.Digest = load.Digest
	processC.State = stateFact(state)
	processC.Error = errorFact(migrateErr)
	processC.Booleans = append(processC.Booleans, NamedBooleanFact{Name: "digest_matches_process_a", Value: load.Digest == initialDigest})
	observation.Outcomes = append(observation.Outcomes, processC)
	if migrateErr == nil {
		snapshot, err = readDatabaseSnapshot(ctx, "process_c_reapply", fixture.path, fixture.backend)
		if err != nil {
			return err
		}
		observation.Database.Snapshots = append(observation.Database.Snapshots, snapshot)
	}
	return nil
}

func observePrecommitFaults(ctx context.Context, observation *Observation) error {
	observation.Database = &DatabaseState{Snapshots: make([]DatabaseSnapshot, 0)}
	if err := observePrecommitFaultCase(ctx, observation, "operation", faultOperation); err != nil {
		return err
	}
	return observePrecommitFaultCase(ctx, observation, "recorder", faultRecorder)
}

func observePrecommitFaultCase(
	ctx context.Context,
	observation *Observation,
	name string,
	fault faultMode,
) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "fault-"+name)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()
	base, load := loadOutcome(name+"_base_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail)...)
	if !load.Accepted {
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	if _, err := (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, base, migrations.LatestLifecycleRequest()); err != nil {
		return fmt.Errorf("apply %s fault base: %w", name, err)
	}
	if err := seedBaseRows(ctx, fixture.backend); err != nil {
		return err
	}
	before, err := readDatabaseSnapshot(ctx, name+"_before", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, before)
	loaded, load := loadOutcome(name+"_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog, sourceCurrentTail, sourceNullableReview)...)
	if !load.Accepted {
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	probe.fault = fault
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	after, err := readDatabaseSnapshot(ctx, name+"_after", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, after)
	outcome := emptyOutcome(name)
	outcome.Accepted = migrateErr == nil
	outcome.State = stateFact(state)
	outcome.Error = errorFact(migrateErr)
	outcome.Intents = cloneIntentFacts(probe.intents)
	outcome.Booleans = append(outcome.Booleans, NamedBooleanFact{Name: "durable_unchanged", Value: snapshotsEqual(before, after)})
	observation.Outcomes = append(observation.Outcomes, outcome)
	if err := fixture.closeBackend(); err != nil {
		return err
	}
	if err := fixture.reopen(ctx); err != nil {
		return err
	}
	reopened, err := readDatabaseSnapshot(ctx, name+"_reopened", fixture.path, fixture.backend)
	if err != nil {
		return err
	}
	observation.Database.Snapshots = append(observation.Database.Snapshots, reopened)
	outcome.Booleans = append(outcome.Booleans, NamedBooleanFact{Name: "reopen_unchanged", Value: snapshotsEqual(before, reopened)})
	observation.Outcomes[len(observation.Outcomes)-1] = outcome
	return nil
}

func observeCommitOutcomes(ctx context.Context, observation *Observation) error {
	for _, item := range []struct {
		name string
		mode commitMode
	}{
		{name: "committed", mode: commitNormal},
		{name: "rolled_back", mode: commitRolledBack},
		{name: "unknown", mode: commitUnknown},
	} {
		if err := observeCommitOutcomeCase(ctx, observation, item.name, item.mode); err != nil {
			return err
		}
	}
	return nil
}

func observeCommitOutcomeCase(
	ctx context.Context,
	observation *Observation,
	name string,
	mode commitMode,
) (resultErr error) {
	fixture, err := openDatabaseFixture(ctx, "commit-"+name)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.close()) }()
	seed, seedLoad := loadOutcome(name+"_seed_load", &observation.Metrics, sourceFor(sourceCurrentAuthor))
	if !seedLoad.Accepted {
		observation.Outcomes = append(observation.Outcomes, seedLoad)
		return nil
	}
	if _, err := (migrations.Executor{Backend: fixture.backend}).Migrate(ctx, seed, migrations.LatestLifecycleRequest()); err != nil {
		return fmt.Errorf("apply commit outcome seed: %w", err)
	}
	loaded, load := loadOutcome(name+"_load", &observation.Metrics, sourcesFor(sourceCurrentAuthor, sourceRelationBlog)...)
	if !load.Accepted {
		observation.Outcomes = append(observation.Outcomes, load)
		return nil
	}
	traceStart := len(observation.Metrics.Trace)
	probe := newObservingBackend(fixture.backend, &observation.Metrics)
	probe.commit = mode
	state, migrateErr := (migrations.Executor{Backend: probe}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	records, readErr := fixture.backend.ReadAppliedMigrations(ctx)
	if readErr != nil {
		return fmt.Errorf("read commit outcome history: %w", readErr)
	}
	outcome := emptyOutcome(name)
	outcome.Accepted = migrateErr == nil
	outcome.State = stateFact(state)
	outcome.Error = errorFact(migrateErr)
	outcome.Intents = cloneIntentFacts(probe.intents)
	for _, record := range records {
		outcome.Strings = append(outcome.Strings, NamedStringFact{Name: "history", Value: record.App + "." + record.Name})
	}
	beginCount, commitCount, rollbackCount := int64(0), int64(0), int64(0)
	for _, event := range probe.metrics.Trace[traceStart:] {
		switch event.Name {
		case "begin_migration":
			beginCount++
		case "commit":
			commitCount++
		case "rollback":
			rollbackCount++
		}
	}
	outcome.Integers = append(outcome.Integers,
		NamedIntegerFact{Name: "begin_count", Value: beginCount},
		NamedIntegerFact{Name: "commit_count", Value: commitCount},
		NamedIntegerFact{Name: "rollback_count", Value: rollbackCount},
	)
	observation.Outcomes = append(observation.Outcomes, outcome)
	return nil
}
