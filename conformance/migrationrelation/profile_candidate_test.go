package migrationrelation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// Everything in this file is a test-only feasibility candidate. The names,
// limits, wire values, and error shape are not a public API proposal.

const (
	profileLegacyDigestDomain = "godj:migration-definition-set:v1"
	profileMixedDigestDomain  = "godj:migration-definition-set:v2"
	profileEmptyDigest        = definition.EmptySetDigest
	profileMaximumWireLength  = int64(1<<31 - 1)

	profileMaxDocuments    = definition.MaxSources
	profileMaxDependencies = definition.MaxDependenciesPerMigration
	profileMaxOperations   = definition.MaxOperationsPerMigration
	profileMaxFields       = definition.MaxFieldsPerCreateModel
)

type profileCompatibility struct {
	DefinitionFormat int64 `json:"definition_format"`
	LoaderABI        int64 `json:"loader_abi"`
	OperationCodec   int64 `json:"operation_codec"`
	SchemaIR         int64 `json:"schema_ir"`
}

var (
	profileLegacy = profileCompatibility{
		DefinitionFormat: definition.DefinitionFormatVersion,
		LoaderABI:        definition.LoaderABIVersion,
		OperationCodec:   definition.OperationCodecVersion,
		SchemaIR:         definition.SchemaIRVersion,
	}
	profileRelationTuple = profileCompatibility{
		DefinitionFormat: definition.DefinitionFormatVersion,
		LoaderABI:        2,
		OperationCodec:   2,
		SchemaIR:         ir.RelationFormatVersion,
	}
)

type profileDecoder string

const (
	profileDecoderLegacy   profileDecoder = "legacy_scalar_v1"
	profileDecoderRelation profileDecoder = "relation_v2"
)

type profileCandidateError struct {
	Category       string
	Code           string
	Stage          string
	Reason         string
	SourceID       string
	Pointer        string
	App            string
	Name           string
	OperationIndex int
	Limit          string
	Maximum        int
	Actual         int
	GraphSources   []definition.GraphSource
}

func (e *profileCandidateError) Error() string {
	if e == nil {
		return "migration relation profile candidate error"
	}
	return fmt.Sprintf("%s/%s stage=%s reason=%s source=%q", e.Category, e.Code, e.Stage, e.Reason, e.SourceID)
}

type profileIdentity struct {
	App  string `json:"app"`
	Name string `json:"name"`
}

// Pointer payloads make the candidate wire union closed even for explicit
// zero values. The current field kinds use string and boolean defaults;
// integer remains represented so the full Schema IR scalar union cannot be
// silently aliased to either arm.
type profileDefault struct {
	Boolean *bool   `json:"boolean,omitempty"`
	Integer *int64  `json:"integer,omitempty"`
	Kind    string  `json:"kind"`
	String  *string `json:"string,omitempty"`
}

type profileTarget struct {
	App   string `json:"app_label"`
	Model string `json:"model_name"`
}

type profileReverse struct {
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
}

type profileRelation struct {
	Target      profileTarget  `json:"target"`
	Cardinality string         `json:"cardinality"`
	Reverse     profileReverse `json:"reverse"`
	OnDelete    string         `json:"on_delete"`
}

type profileField struct {
	Column     string           `json:"column"`
	Default    *profileDefault  `json:"default"`
	GoName     string           `json:"go_name"`
	Kind       string           `json:"kind"`
	MaxLength  int64            `json:"max_length"`
	Name       string           `json:"name"`
	Nullable   bool             `json:"nullable"`
	PrimaryKey bool             `json:"primary_key"`
	Relation   *profileRelation `json:"relation,omitempty"`
}

type profileModel struct {
	DBTable string         `json:"db_table"`
	Fields  []profileField `json:"fields"`
	GoName  string         `json:"go_name"`
	Name    string         `json:"name"`
}

type profileOperation struct {
	AppLabel  string        `json:"app_label"`
	Field     *profileField `json:"field,omitempty"`
	Kind      string        `json:"kind"`
	Model     *profileModel `json:"model,omitempty"`
	ModelName string        `json:"model_name,omitempty"`
}

type profileDefinition struct {
	App          string             `json:"app"`
	Dependencies []profileIdentity  `json:"dependencies"`
	Name         string             `json:"name"`
	Operations   []profileOperation `json:"operations"`
}

type profileProducer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type profileSource struct {
	SourceID   string
	Producer   profileProducer
	Profile    profileCompatibility
	Definition profileDefinition
}

type profileWireDocument struct {
	Compatibility profileCompatibility `json:"compatibility"`
	Producer      profileProducer      `json:"producer"`
	Migration     profileDefinition    `json:"migration"`
}

type profilePublishedDefinition struct {
	SourceID       string
	Producer       profileProducer
	Profile        profileCompatibility
	Definition     profileDefinition
	provenanceSeal string
}

type profileSet struct {
	canonical         []byte
	digest            string
	definitions       []profilePublishedDefinition
	legacyDefinitions []migrations.Migration
	hasLegacy         bool
}

func (s profileSet) profileDigest() string {
	if s.digest == "" {
		return profileEmptyDigest
	}
	return s.digest
}

func (s profileSet) profileCanonicalBytes() []byte {
	return append([]byte(nil), s.canonical...)
}

func (s profileSet) profileDefinitions() []profilePublishedDefinition {
	cloned := make([]profilePublishedDefinition, len(s.definitions))
	for index := range s.definitions {
		cloned[index] = profileClonePublishedDefinition(s.definitions[index])
	}
	return cloned
}

// profileLegacyDefinitions exposes candidate snapshots only for an all-legacy
// set. Product parity is proved independently against definition.Load; the
// candidate route never invokes that second scanner.
func (s profileSet) profileLegacyDefinitions() []migrations.Migration {
	if !s.hasLegacy {
		return nil
	}
	return profileCloneMigrations(s.legacyDefinitions)
}

type profileLoadReport struct {
	DocumentsReceived    int
	ProfilesAccepted     int
	OperationsDecoded    int
	PlannerConstruction  int
	DefinitionsPublished int
	SetsPublished        int
	ParserInvocations    int
	Failure              *profileFailureContext
}

type profileFailureContext struct {
	Stage          string
	SourceID       string
	Pointer        string
	App            string
	Name           string
	OperationIndex int
	Reason         string
	Limit          string
	Maximum        int
	Actual         int
	GraphSources   []definition.GraphSource
}

func profileDispatch(value profileCompatibility) (profileDecoder, *profileCandidateError) {
	if value == profileLegacy {
		return profileDecoderLegacy, nil
	}
	if value == profileRelationTuple {
		return profileDecoderRelation, nil
	}
	coordinates := []struct {
		name     string
		actual   int64
		legacy   int64
		relation int64
	}{
		{name: "definition_format", actual: value.DefinitionFormat, legacy: profileLegacy.DefinitionFormat, relation: profileRelationTuple.DefinitionFormat},
		{name: "loader_abi", actual: value.LoaderABI, legacy: profileLegacy.LoaderABI, relation: profileRelationTuple.LoaderABI},
		{name: "operation_codec", actual: value.OperationCodec, legacy: profileLegacy.OperationCodec, relation: profileRelationTuple.OperationCodec},
		{name: "schema_ir", actual: value.SchemaIR, legacy: profileLegacy.SchemaIR, relation: profileRelationTuple.SchemaIR},
	}
	for _, coordinate := range coordinates {
		if coordinate.actual != coordinate.legacy && coordinate.actual != coordinate.relation {
			return "", profileCompatibilityFailure(coordinate.name)
		}
	}
	for _, coordinate := range coordinates {
		if coordinate.actual != coordinate.legacy {
			return "", profileHybridFailure(coordinate.name)
		}
	}
	return "", &profileCandidateError{
		Category:       definition.CategorySource,
		Code:           "compatibility_dispatch_invariant",
		Stage:          "compatibility",
		Reason:         "tuple_matched_no_supported_profile",
		Pointer:        "/compatibility",
		OperationIndex: -1,
	}
}

func profileCompatibilityFailure(coordinate string) *profileCandidateError {
	return &profileCandidateError{
		Category:       definition.CategorySource,
		Code:           coordinate + "_incompatible",
		Stage:          "compatibility",
		Reason:         coordinate,
		Pointer:        "/compatibility/" + coordinate,
		OperationIndex: -1,
	}
}

func profileHybridFailure(coordinate string) *profileCandidateError {
	return &profileCandidateError{
		Category:       definition.CategorySource,
		Code:           "hybrid_profile_incompatible",
		Stage:          "compatibility",
		Reason:         coordinate,
		Pointer:        "/compatibility/" + coordinate,
		OperationIndex: -1,
	}
}

// profileLoad is only a typed-fixture convenience. Every candidate document is
// serialized and re-enters through profileLoadRaw, so relation success cannot
// be established by an already-decoded Go value.
func profileLoad(sources ...profileSource) (profileSet, profileLoadReport, error) {
	raw := make([]definition.Source, len(sources))
	for index := range sources {
		document, err := json.Marshal(profileWireDocument{
			Compatibility: sources[index].Profile,
			Producer:      sources[index].Producer,
			Migration:     sources[index].Definition,
		})
		if err != nil {
			return profileSet{}, profileLoadReport{DocumentsReceived: len(sources)}, &profileCandidateError{
				Category: "migration_relation_profile_candidate_error",
				Code:     "invalid_document",
				Stage:    "document",
				Reason:   "marshal_failed",
				SourceID: sources[index].SourceID,
			}
		}
		raw[index] = definition.Source{SourceID: sources[index].SourceID, Document: document}
	}
	return profileLoadRaw(raw...)
}

// profileLoadRaw checks identity and byte limits before snapshot or parser
// allocation. Every admissible source is scanned exactly once into a retained,
// bounded tree used for compatibility, diagnostics, resource checks, semantic
// validation, and direct typed materialization. Legacy, relation, and mixed
// sets all enter one combined Planner. Product code should share this scanner
// behind its single public Load; this file remains test-only feasibility proof.
func profileLoadRaw(sources ...definition.Source) (profileSet, profileLoadReport, error) {
	report := profileLoadReport{DocumentsReceived: len(sources)}
	snapshots, failure := profilePreflightAndSnapshot(sources)
	if failure != nil {
		return profileSet{}, profileReportWithFailure(report, failure), failure
	}
	scanned, rawFailures := profileScanRawBatch(snapshots, &report.ParserInvocations)
	documentFailures := append([]*profileCandidateError(nil), rawFailures...)
	for index := range scanned {
		if scanned[index].complete {
			documentFailures = append(documentFailures, profileDocumentCandidates(scanned[index])...)
		}
	}
	if len(documentFailures) != 0 {
		profileSortCandidateFailures(documentFailures)
		failure := documentFailures[0]
		return profileSet{}, profileReportWithFailure(report, failure), failure
	}
	compatibilityFailures := make([]*profileCandidateError, 0)
	for index := range scanned {
		if !scanned[index].profileOK {
			failure := profileRawSemanticError(scanned[index].source.SourceID, "invalid_compatibility_shape")
			return profileSet{}, profileReportWithFailure(report, failure), failure
		}
		_, failure := profileDispatch(scanned[index].profile)
		if failure != nil {
			failure.SourceID = scanned[index].source.SourceID
			compatibilityFailures = append(compatibilityFailures, failure)
			continue
		}
		report.ProfilesAccepted++
	}
	if len(compatibilityFailures) != 0 {
		profileSortCandidateFailures(compatibilityFailures)
		return profileSet{}, profileReportWithFailure(report, compatibilityFailures[0]), compatibilityFailures[0]
	}

	if failure := profileRawResourceFailure(scanned); failure != nil {
		return profileSet{}, profileReportWithFailure(report, failure), failure
	}
	semanticFailures := make([]*profileCandidateError, 0)
	for index := range scanned {
		decoder, _ := profileDispatch(scanned[index].profile)
		semanticFailures = append(semanticFailures, profileSemanticCandidates(scanned[index], decoder)...)
	}
	if len(semanticFailures) != 0 {
		profileSortCandidateFailures(semanticFailures)
		failure := semanticFailures[0]
		return profileSet{}, profileReportWithFailure(report, failure), failure
	}

	decoded := make([]profileSource, 0, len(scanned))
	decodeFailures := make([]*profileCandidateError, 0)
	for index := range scanned {
		source, failure := profileMaterializeSource(scanned[index])
		if failure != nil {
			decodeFailures = append(decodeFailures, failure)
			continue
		}
		decoded = append(decoded, source)
	}
	if len(decodeFailures) != 0 {
		sort.Slice(decodeFailures, func(left, right int) bool {
			return bytes.Compare([]byte(decodeFailures[left].SourceID), []byte(decodeFailures[right].SourceID)) < 0
		})
		return profileSet{}, profileReportWithFailure(report, decodeFailures[0]), decodeFailures[0]
	}
	return profileLoadCandidateDecoded(decoded, report)
}

func profilePreflightAndSnapshot(sources []definition.Source) ([]definition.Source, *profileCandidateError) {
	if len(sources) > definition.MaxSources {
		return nil, profilePreflightResourceFailure(definition.CodeInvalidSource, "source", "", "source_count", definition.MaxSources, len(sources))
	}
	oversizedID := 0
	for index := range sources {
		actual := len(sources[index].SourceID)
		if actual > definition.MaxSourceIDBytes && (oversizedID == 0 || actual < oversizedID) {
			oversizedID = actual
		}
	}
	if oversizedID != 0 {
		return nil, profilePreflightResourceFailure(definition.CodeInvalidSource, "source", "", "source_id_bytes", definition.MaxSourceIDBytes, oversizedID)
	}

	sourceFailures := make([]*profileCandidateError, 0)
	order := make([]int, len(sources))
	for index := range sources {
		order[index] = index
		rawID := []byte(sources[index].SourceID)
		switch {
		case len(rawID) == 0:
			sourceFailures = append(sourceFailures, profilePreflightSourceFailure("", "empty_source_id"))
		case !utf8.Valid(rawID):
			sourceFailures = append(sourceFailures, profilePreflightSourceFailure("hex:"+hex.EncodeToString(rawID), "invalid_source_id_utf8"))
		}
	}
	sort.Slice(order, func(left, right int) bool {
		return bytes.Compare([]byte(sources[order[left]].SourceID), []byte(sources[order[right]].SourceID)) < 0
	})
	for index := 1; index < len(order); index++ {
		left := sources[order[index-1]].SourceID
		right := sources[order[index]].SourceID
		if left == right {
			sourceFailures = append(sourceFailures, profilePreflightSourceFailure(right, "duplicate_source_id"))
		}
	}
	if len(sourceFailures) != 0 {
		profileSortCandidateFailures(sourceFailures)
		return nil, sourceFailures[0]
	}

	documentFailures := make([]*profileCandidateError, 0)
	batchBytes := uint64(0)
	for index := range sources {
		actual := len(sources[index].Document)
		if actual > definition.MaxDocumentBytes {
			documentFailures = append(documentFailures, profilePreflightResourceFailure(
				definition.CodeInvalidDocument, "document", sources[index].SourceID, "document_bytes", definition.MaxDocumentBytes, actual,
			))
		}
		batchBytes += uint64(actual)
	}
	if len(documentFailures) != 0 {
		profileSortCandidateFailures(documentFailures)
		return nil, documentFailures[0]
	}
	if batchBytes > uint64(definition.MaxBatchBytes) {
		return nil, profilePreflightResourceFailure(definition.CodeInvalidDocument, "document", "", "batch_bytes", definition.MaxBatchBytes, int(batchBytes))
	}

	snapshots := make([]definition.Source, len(sources))
	for index := range sources {
		snapshots[index] = definition.Source{SourceID: sources[index].SourceID, Document: append([]byte(nil), sources[index].Document...)}
	}
	sort.Slice(snapshots, func(left, right int) bool {
		return bytes.Compare([]byte(snapshots[left].SourceID), []byte(snapshots[right].SourceID)) < 0
	})
	return snapshots, nil
}

func profilePreflightSourceFailure(sourceID, reason string) *profileCandidateError {
	return &profileCandidateError{
		Category: definition.CategorySource, Code: string(definition.CodeInvalidSource), Stage: "source",
		Reason: reason, SourceID: sourceID, OperationIndex: -1,
	}
}

func profilePreflightResourceFailure(code definition.ErrorCode, stage, sourceID, limit string, maximum, actual int) *profileCandidateError {
	return &profileCandidateError{
		Category: definition.CategorySource, Code: string(code), Stage: stage, Reason: "resource_limit_exceeded",
		SourceID: sourceID, OperationIndex: -1, Limit: limit, Maximum: maximum, Actual: actual,
	}
}

func profileReportFromProduct(report definition.LoadReport) profileLoadReport {
	mapped := profileLoadReport{
		DocumentsReceived:    report.DocumentsReceived,
		ProfilesAccepted:     report.HeadersValidated,
		OperationsDecoded:    report.OperationsDecoded,
		PlannerConstruction:  report.PlannerConstruction,
		DefinitionsPublished: report.DefinitionsPublished,
		SetsPublished:        report.DefinitionSetsPublished,
		ParserInvocations:    report.DocumentsReceived,
	}
	if failure, exists := report.Failure(); exists {
		mappedFailure := profileFailureFromProduct(failure)
		mapped.Failure = &mappedFailure
		if failure.Stage == "source" || (failure.Stage == "document" && (failure.Limit == "document_bytes" || failure.Limit == "batch_bytes")) {
			mapped.ParserInvocations = 0
		}
	}
	return mapped
}

func profileMapProductError(err error) error {
	var sourceError *definition.Error
	if !errors.As(err, &sourceError) {
		return err
	}
	context := sourceError.Context()
	mapped := profileFailureFromProduct(context)
	return &profileCandidateError{
		Category:       sourceError.Category,
		Code:           string(sourceError.Code),
		Stage:          mapped.Stage,
		Reason:         mapped.Reason,
		SourceID:       mapped.SourceID,
		Pointer:        mapped.Pointer,
		App:            mapped.App,
		Name:           mapped.Name,
		OperationIndex: mapped.OperationIndex,
		Limit:          mapped.Limit,
		Maximum:        mapped.Maximum,
		Actual:         mapped.Actual,
		GraphSources:   append([]definition.GraphSource(nil), mapped.GraphSources...),
	}
}

func profileMapProductLoadError(err error, report definition.LoadReport) error {
	mapped := profileMapProductError(err)
	var candidate *profileCandidateError
	if errors.As(mapped, &candidate) && candidate != nil {
		return mapped
	}
	context, exists := report.Failure()
	if !exists {
		return err
	}
	failure := profileFailureFromProduct(context)
	code := "invalid_graph"
	var planning *migrations.PlanningError
	if errors.As(err, &planning) && planning != nil {
		code = string(planning.Code)
	}
	return &profileCandidateError{
		Category:       "migration_relation_profile_candidate_error",
		Code:           code,
		Stage:          failure.Stage,
		Reason:         failure.Reason,
		SourceID:       failure.SourceID,
		Pointer:        failure.Pointer,
		App:            failure.App,
		Name:           failure.Name,
		OperationIndex: failure.OperationIndex,
		Limit:          failure.Limit,
		Maximum:        failure.Maximum,
		Actual:         failure.Actual,
		GraphSources:   append([]definition.GraphSource(nil), failure.GraphSources...),
	}
}

func profileFailureFromProduct(context definition.FailureContext) profileFailureContext {
	mapped := profileFailureContext{
		Stage:          context.Stage,
		SourceID:       context.SourceID,
		Pointer:        context.JSONPointer,
		App:            context.App,
		Name:           context.Name,
		OperationIndex: context.OperationIndex,
		Reason:         context.Reason,
		Limit:          context.Limit,
		Maximum:        profileUintToInt(context.Maximum),
		Actual:         profileUintToInt(context.Actual),
		GraphSources:   context.GraphSources(),
	}
	if len(mapped.GraphSources) == 0 {
		mapped.GraphSources = nil
	}
	return mapped
}

func profileReportWithFailure(report profileLoadReport, failure *profileCandidateError) profileLoadReport {
	if failure == nil {
		return report
	}
	context := profileFailureContext{
		Stage:          failure.Stage,
		SourceID:       failure.SourceID,
		Pointer:        failure.Pointer,
		App:            failure.App,
		Name:           failure.Name,
		OperationIndex: failure.OperationIndex,
		Reason:         failure.Reason,
		Limit:          failure.Limit,
		Maximum:        failure.Maximum,
		Actual:         failure.Actual,
		GraphSources:   append([]definition.GraphSource(nil), failure.GraphSources...),
	}
	if len(context.GraphSources) == 0 {
		context.GraphSources = nil
	}
	report.Failure = &context
	return report
}

func profileUintToInt(value uint64) int {
	maximum := uint64(^uint(0) >> 1)
	if value > maximum {
		return int(maximum)
	}
	return int(value)
}

type profileJSONKind uint8

const (
	profileJSONNull profileJSONKind = iota + 1
	profileJSONBoolean
	profileJSONString
	profileJSONNumber
	profileJSONArray
	profileJSONObject
)

type profileJSONMember struct {
	key   string
	value profileJSONValue
}

type profileJSONValue struct {
	kind    profileJSONKind
	boolean bool
	string  string
	number  string
	array   []profileJSONValue
	object  []profileJSONMember
}

func (value profileJSONValue) member(name string) (profileJSONValue, bool) {
	for _, member := range value.object {
		if member.key == name {
			return member.value, true
		}
	}
	return profileJSONValue{}, false
}

type profileRawScan struct {
	source    definition.Source
	root      profileJSONValue
	profile   profileCompatibility
	profileOK bool
	complete  bool
}

// profileScanRawBatch performs the route's single raw structural pass.
// The caller has already completed source/id/document/batch preflight and made
// immutable snapshots. The returned trees are reused by compatibility, strict
// shape, and semantic resource validation; no route-probe decode occurs.
func profileScanRawBatch(sources []definition.Source, invocations *int) ([]profileRawScan, []*profileCandidateError) {
	scanned := make([]profileRawScan, len(sources))
	failures := make([]*profileCandidateError, 0)
	totalValues := uint64(0)
	for index := range sources {
		root, values, complete, failure := profileParseJSONDocument(sources[index], invocations)
		scanned[index] = profileRawScan{source: sources[index], root: root, complete: complete}
		if profile, ok := profileCompatibilityFromJSON(root); ok {
			scanned[index].profile = profile
			scanned[index].profileOK = true
		}
		if failure != nil {
			failures = append(failures, failure)
		}
		if totalValues <= uint64(definition.MaxJSONValues) {
			totalValues += values
			if totalValues > uint64(definition.MaxJSONValues) {
				failures = append(failures, profileDocumentResourceFailure(
					sources[index].SourceID,
					"json_values",
					definition.MaxJSONValues,
					definition.MaxJSONValues+1,
				))
			}
		}
	}
	if len(failures) == 0 {
		return scanned, nil
	}
	sort.SliceStable(failures, func(left, right int) bool {
		return profileLessDocumentFailure(failures[left], failures[right])
	})
	return scanned, failures
}

// profileCompatibilityFromJSON materializes the small route header directly
// from the retained tree. Missing and null integer fields retain their zero
// value, while incompatible JSON kinds and int64 overflow reject the header.
func profileCompatibilityFromJSON(root profileJSONValue) (profileCompatibility, bool) {
	if root.kind != profileJSONObject {
		return profileCompatibility{}, false
	}
	compatibility, exists := root.member("compatibility")
	if !exists || compatibility.kind == profileJSONNull {
		return profileCompatibility{}, true
	}
	if compatibility.kind != profileJSONObject {
		return profileCompatibility{}, false
	}
	coordinates := []struct {
		name   string
		target *int64
	}{
		{name: "definition_format"},
		{name: "loader_abi"},
		{name: "operation_codec"},
		{name: "schema_ir"},
	}
	value := profileCompatibility{}
	coordinates[0].target = &value.DefinitionFormat
	coordinates[1].target = &value.LoaderABI
	coordinates[2].target = &value.OperationCodec
	coordinates[3].target = &value.SchemaIR
	for _, coordinate := range coordinates {
		member, exists := compatibility.member(coordinate.name)
		if !exists || member.kind == profileJSONNull {
			continue
		}
		if member.kind != profileJSONNumber {
			return profileCompatibility{}, false
		}
		parsed, err := strconv.ParseInt(member.number, 10, 64)
		if err != nil {
			return profileCompatibility{}, false
		}
		*coordinate.target = parsed
	}
	return value, true
}

type profileJSONPath struct {
	parent *profileJSONPath
	token  string
}

type profileJSONFailure struct {
	failure *profileCandidateError
	path    *profileJSONPath
}

type profileJSONParser struct {
	data            []byte
	position        int
	sourceID        string
	values          uint64
	buildTree       bool
	resourceFailure *profileJSONFailure
	regularFailure  *profileJSONFailure
}

func profileParseJSONDocument(source definition.Source, invocations *int) (profileJSONValue, uint64, bool, *profileCandidateError) {
	if invocations != nil {
		*invocations = *invocations + 1
	}
	validUTF8 := utf8.Valid(source.Document)
	parser := profileJSONParser{
		data:      source.Document,
		sourceID:  source.SourceID,
		buildTree: true,
	}
	parser.skipWhitespace()
	root, err := parser.parseValue(nil, 0)
	if err != nil {
		reason := "syntax"
		if !validUTF8 {
			reason = "invalid_utf8"
		}
		parser.recordFailure(profileJSONFailure{failure: profileDocumentFailure(source.SourceID, reason)})
		return profileJSONValue{}, parser.values, false, parser.failure()
	}
	parser.skipWhitespace()
	if parser.position != len(parser.data) {
		// Continue over complete trailing values only to expose an earlier
		// resource class. The framing result itself remains trailing_value.
		parser.buildTree = false
		for parser.position < len(parser.data) {
			if _, trailingErr := parser.parseValue(nil, 0); trailingErr != nil {
				break
			}
			parser.skipWhitespace()
		}
		reason := "trailing_value"
		if !validUTF8 {
			reason = "invalid_utf8"
		}
		parser.recordFailure(profileJSONFailure{failure: profileDocumentFailure(source.SourceID, reason)})
		return profileJSONValue{}, parser.values, false, parser.failure()
	}
	return root, parser.values, parser.resourceFailure == nil, parser.failure()
}

func (parser *profileJSONParser) failure() *profileCandidateError {
	winner := parser.regularFailure
	if parser.resourceFailure != nil {
		winner = parser.resourceFailure
	}
	if winner == nil {
		return nil
	}
	materialized := *winner.failure
	materialized.Pointer = profileRenderJSONPath(winner.path)
	materialized.GraphSources = append([]definition.GraphSource(nil), winner.failure.GraphSources...)
	return &materialized
}

func (parser *profileJSONParser) recordFailure(candidate profileJSONFailure) {
	target := &parser.regularFailure
	if candidate.failure.Reason == "resource_limit_exceeded" {
		target = &parser.resourceFailure
	}
	if *target == nil || profileLessJSONFailure(candidate, **target) {
		winner := candidate
		*target = &winner
	}
}

func profileLessJSONFailure(left, right profileJSONFailure) bool {
	leftResource := left.failure.Reason == "resource_limit_exceeded"
	rightResource := right.failure.Reason == "resource_limit_exceeded"
	if leftResource != rightResource {
		return leftResource
	}
	if leftResource {
		leftRank := profileDocumentLimitRank(left.failure.Limit)
		rightRank := profileDocumentLimitRank(right.failure.Limit)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
	}
	if pointerOrder := profileCompareJSONPaths(left.path, right.path); pointerOrder != 0 {
		return pointerOrder < 0
	}
	if !leftResource {
		leftRank := profileDocumentReasonRank(left.failure.Reason)
		rightRank := profileDocumentReasonRank(right.failure.Reason)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
	}
	if left.failure.Code != right.failure.Code {
		return left.failure.Code < right.failure.Code
	}
	if left.failure.Limit != right.failure.Limit {
		return left.failure.Limit < right.failure.Limit
	}
	if left.failure.Maximum != right.failure.Maximum {
		return left.failure.Maximum < right.failure.Maximum
	}
	return left.failure.Actual < right.failure.Actual
}

func profileChildJSONPath(parent *profileJSONPath, token string) *profileJSONPath {
	return &profileJSONPath{parent: parent, token: token}
}

// profileCompareJSONPaths follows the product scanner's allocation-bounded
// comparator. It compares RFC 6901 bytes without rendering a potentially long
// shared ancestor for every duplicate/lone-scalar candidate.
func profileCompareJSONPaths(left, right *profileJSONPath) int {
	if left == right {
		return 0
	}
	leftNodes := profileJSONPathNodes(left)
	rightNodes := profileJSONPathNodes(right)
	common := 0
	for common < len(leftNodes) && common < len(rightNodes) && leftNodes[common] == rightNodes[common] {
		common++
	}
	return profileCompareJSONPathNodeStreams(leftNodes[common:], rightNodes[common:])
}

func profileCompareJSONPathNodeStreams(left, right []*profileJSONPath) int {
	leftIterator := profileJSONPointerByteIterator{nodes: left}
	rightIterator := profileJSONPointerByteIterator{nodes: right}
	for {
		leftByte, leftOK := leftIterator.next()
		rightByte, rightOK := rightIterator.next()
		switch {
		case !leftOK && !rightOK:
			return 0
		case !leftOK:
			return -1
		case !rightOK:
			return 1
		case leftByte < rightByte:
			return -1
		case leftByte > rightByte:
			return 1
		}
	}
}

type profileJSONPointerByteIterator struct {
	nodes          []*profileJSONPath
	nodeIndex      int
	tokenOffset    int
	segmentStarted bool
	escapeSecond   byte
}

func (iterator *profileJSONPointerByteIterator) next() (byte, bool) {
	for iterator.nodeIndex < len(iterator.nodes) {
		if !iterator.segmentStarted {
			iterator.segmentStarted = true
			return '/', true
		}
		if iterator.escapeSecond != 0 {
			value := iterator.escapeSecond
			iterator.escapeSecond = 0
			iterator.tokenOffset++
			return value, true
		}
		token := iterator.nodes[iterator.nodeIndex].token
		if iterator.tokenOffset < len(token) {
			switch token[iterator.tokenOffset] {
			case '~':
				iterator.escapeSecond = '0'
				return '~', true
			case '/':
				iterator.escapeSecond = '1'
				return '~', true
			default:
				value := token[iterator.tokenOffset]
				iterator.tokenOffset++
				return value, true
			}
		}
		iterator.nodeIndex++
		iterator.tokenOffset = 0
		iterator.segmentStarted = false
	}
	return 0, false
}

func profileJSONPathNodes(path *profileJSONPath) []*profileJSONPath {
	depth := 0
	for current := path; current != nil; current = current.parent {
		depth++
	}
	nodes := make([]*profileJSONPath, depth)
	for current := path; current != nil; current = current.parent {
		depth--
		nodes[depth] = current
	}
	return nodes
}

func profileRenderJSONPath(path *profileJSONPath) string {
	var builder strings.Builder
	for _, node := range profileJSONPathNodes(path) {
		builder.WriteByte('/')
		for _, current := range []byte(node.token) {
			switch current {
			case '~':
				builder.WriteString("~0")
			case '/':
				builder.WriteString("~1")
			default:
				builder.WriteByte(current)
			}
		}
	}
	return builder.String()
}

func (parser *profileJSONParser) parseValue(pointer *profileJSONPath, depth int) (profileJSONValue, error) {
	if parser.position >= len(parser.data) {
		return profileJSONValue{}, errors.New("unexpected end of JSON")
	}
	switch parser.data[parser.position] {
	case '{':
		parser.countValue()
		if depth+1 > definition.MaxJSONDepth {
			parser.recordDepthFailure(pointer, depth+1)
			if err := parser.skipOverDepthContainer(); err != nil {
				return profileJSONValue{}, err
			}
			return profileJSONValue{kind: profileJSONObject}, nil
		}
		return parser.parseObject(pointer, depth+1)
	case '[':
		parser.countValue()
		if depth+1 > definition.MaxJSONDepth {
			parser.recordDepthFailure(pointer, depth+1)
			if err := parser.skipOverDepthContainer(); err != nil {
				return profileJSONValue{}, err
			}
			return profileJSONValue{kind: profileJSONArray}, nil
		}
		return parser.parseArray(pointer, depth+1)
	case '"':
		decoded, err := parser.parseString(pointer)
		if err != nil {
			return profileJSONValue{}, err
		}
		parser.countValue()
		return profileJSONValue{kind: profileJSONString, string: decoded}, nil
	case 't':
		if !parser.consumeLiteral("true") {
			return profileJSONValue{}, errors.New("invalid true literal")
		}
		parser.countValue()
		return profileJSONValue{kind: profileJSONBoolean, boolean: true}, nil
	case 'f':
		if !parser.consumeLiteral("false") {
			return profileJSONValue{}, errors.New("invalid false literal")
		}
		parser.countValue()
		return profileJSONValue{kind: profileJSONBoolean}, nil
	case 'n':
		if !parser.consumeLiteral("null") {
			return profileJSONValue{}, errors.New("invalid null literal")
		}
		parser.countValue()
		return profileJSONValue{kind: profileJSONNull}, nil
	default:
		lexeme, integer, err := parser.parseNumber()
		if err != nil {
			return profileJSONValue{}, err
		}
		if !integer {
			parser.recordFailure(profileJSONFailure{
				failure: profileDocumentFailure(parser.sourceID, "wrong_type"),
				path:    pointer,
			})
		}
		parser.countValue()
		return profileJSONValue{kind: profileJSONNumber, number: lexeme}, nil
	}
}

func (parser *profileJSONParser) parseObject(pointer *profileJSONPath, depth int) (profileJSONValue, error) {
	parser.position++
	parser.skipWhitespace()
	members := make([]profileJSONMember, 0)
	seen := make(map[string]*profileJSONPath)
	if parser.consumeByte('}') {
		return profileJSONValue{kind: profileJSONObject, object: members}, nil
	}
	for {
		if parser.position >= len(parser.data) || parser.data[parser.position] != '"' {
			return profileJSONValue{}, errors.New("object key is not a string")
		}
		key, err := parser.parseString(pointer)
		if err != nil {
			return profileJSONValue{}, err
		}
		memberPointer := profileChildJSONPath(pointer, key)
		firstPointer, duplicate := seen[key]
		if duplicate {
			memberPointer = firstPointer
		} else {
			seen[key] = memberPointer
		}
		parser.skipWhitespace()
		if !parser.consumeByte(':') {
			return profileJSONValue{}, errors.New("object member has no colon")
		}
		parser.skipWhitespace()
		child, err := parser.parseValue(memberPointer, depth)
		if err != nil {
			return profileJSONValue{}, err
		}
		if parser.buildTree {
			if duplicate {
				parser.recordFailure(profileJSONFailure{
					failure: profileDocumentFailure(parser.sourceID, "duplicate_key"),
					path:    memberPointer,
				})
			} else {
				members = append(members, profileJSONMember{key: key, value: child})
			}
		}
		parser.skipWhitespace()
		if parser.consumeByte('}') {
			return profileJSONValue{kind: profileJSONObject, object: members}, nil
		}
		if !parser.consumeByte(',') {
			return profileJSONValue{}, errors.New("object member has no comma")
		}
		parser.skipWhitespace()
	}
}

func (parser *profileJSONParser) parseArray(pointer *profileJSONPath, depth int) (profileJSONValue, error) {
	parser.position++
	parser.skipWhitespace()
	values := make([]profileJSONValue, 0)
	if parser.consumeByte(']') {
		return profileJSONValue{kind: profileJSONArray, array: values}, nil
	}
	for index := 0; ; index++ {
		child, err := parser.parseValue(profileChildJSONPath(pointer, strconv.Itoa(index)), depth)
		if err != nil {
			return profileJSONValue{}, err
		}
		if parser.buildTree {
			values = append(values, child)
		}
		parser.skipWhitespace()
		if parser.consumeByte(']') {
			return profileJSONValue{kind: profileJSONArray, array: values}, nil
		}
		if !parser.consumeByte(',') {
			return profileJSONValue{}, errors.New("array element has no comma")
		}
		parser.skipWhitespace()
	}
}

func (parser *profileJSONParser) parseString(pointer *profileJSONPath) (string, error) {
	if !parser.consumeByte('"') {
		return "", errors.New("missing string quote")
	}
	var builder strings.Builder
	for parser.position < len(parser.data) {
		current := parser.data[parser.position]
		if current == '"' {
			parser.position++
			return builder.String(), nil
		}
		if current < 0x20 {
			return "", errors.New("unescaped control character")
		}
		if current != '\\' {
			runeValue, size := utf8.DecodeRune(parser.data[parser.position:])
			if runeValue == utf8.RuneError && size == 1 {
				return "", errors.New("invalid UTF-8 in string")
			}
			builder.Write(parser.data[parser.position : parser.position+size])
			parser.position += size
			continue
		}
		parser.position++
		if parser.position >= len(parser.data) {
			return "", errors.New("unfinished string escape")
		}
		escape := parser.data[parser.position]
		parser.position++
		switch escape {
		case '"', '\\', '/':
			builder.WriteByte(escape)
		case 'b':
			builder.WriteByte('\b')
		case 'f':
			builder.WriteByte('\f')
		case 'n':
			builder.WriteByte('\n')
		case 'r':
			builder.WriteByte('\r')
		case 't':
			builder.WriteByte('\t')
		case 'u':
			unit, err := parser.parseHexUnit()
			if err != nil {
				return "", err
			}
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if parser.position+6 <= len(parser.data) && parser.data[parser.position] == '\\' && parser.data[parser.position+1] == 'u' {
					low, lowErr := profileParseHex4(parser.data[parser.position+2 : parser.position+6])
					if lowErr == nil && low >= 0xdc00 && low <= 0xdfff {
						parser.position += 6
						builder.WriteRune(rune(0x10000 + (int(unit)-0xd800)*0x400 + int(low) - 0xdc00))
						continue
					}
				}
				parser.recordFailure(profileJSONFailure{
					failure: profileDocumentFailure(parser.sourceID, "lone_surrogate"),
					path:    pointer,
				})
				builder.WriteRune(utf8.RuneError)
			case unit >= 0xdc00 && unit <= 0xdfff:
				parser.recordFailure(profileJSONFailure{
					failure: profileDocumentFailure(parser.sourceID, "lone_surrogate"),
					path:    pointer,
				})
				builder.WriteRune(utf8.RuneError)
			default:
				builder.WriteRune(rune(unit))
			}
		default:
			return "", errors.New("unknown string escape")
		}
	}
	return "", errors.New("unterminated string")
}

func (parser *profileJSONParser) parseHexUnit() (uint16, error) {
	if parser.position+4 > len(parser.data) {
		return 0, errors.New("short Unicode escape")
	}
	unit, err := profileParseHex4(parser.data[parser.position : parser.position+4])
	if err != nil {
		return 0, err
	}
	parser.position += 4
	return unit, nil
}

func profileParseHex4(value []byte) (uint16, error) {
	unit, ok := profileParseHexUnit(value)
	if !ok {
		return 0, errors.New("invalid Unicode escape")
	}
	return unit, nil
}

func (parser *profileJSONParser) parseNumber() (string, bool, error) {
	start := parser.position
	if parser.consumeByte('-') && parser.position >= len(parser.data) {
		return "", false, errors.New("unfinished negative number")
	}
	if parser.consumeByte('0') {
		// A following decimal digit is rejected by the parent delimiter or the
		// single-root framing check.
	} else {
		if parser.position >= len(parser.data) || parser.data[parser.position] < '1' || parser.data[parser.position] > '9' {
			return "", false, errors.New("invalid number integer part")
		}
		for parser.position < len(parser.data) && parser.data[parser.position] >= '0' && parser.data[parser.position] <= '9' {
			parser.position++
		}
	}
	integer := true
	if parser.consumeByte('.') {
		integer = false
		if parser.position >= len(parser.data) || parser.data[parser.position] < '0' || parser.data[parser.position] > '9' {
			return "", false, errors.New("invalid number fraction")
		}
		for parser.position < len(parser.data) && parser.data[parser.position] >= '0' && parser.data[parser.position] <= '9' {
			parser.position++
		}
	}
	if parser.position < len(parser.data) && (parser.data[parser.position] == 'e' || parser.data[parser.position] == 'E') {
		integer = false
		parser.position++
		if parser.position < len(parser.data) && (parser.data[parser.position] == '+' || parser.data[parser.position] == '-') {
			parser.position++
		}
		if parser.position >= len(parser.data) || parser.data[parser.position] < '0' || parser.data[parser.position] > '9' {
			return "", false, errors.New("invalid number exponent")
		}
		for parser.position < len(parser.data) && parser.data[parser.position] >= '0' && parser.data[parser.position] <= '9' {
			parser.position++
		}
	}
	return string(parser.data[start:parser.position]), integer, nil
}

func (parser *profileJSONParser) countValue() {
	maximum := uint64(definition.MaxDocumentJSONValues)
	if parser.values >= maximum+1 {
		return
	}
	parser.values++
	if parser.values == maximum+1 {
		parser.recordFailure(profileJSONFailure{failure: profileDocumentResourceFailure(
			parser.sourceID,
			"document_json_values",
			definition.MaxDocumentJSONValues,
			definition.MaxDocumentJSONValues+1,
		)})
		parser.buildTree = false
	}
}

func (parser *profileJSONParser) recordDepthFailure(pointer *profileJSONPath, actual int) {
	parser.recordFailure(profileJSONFailure{
		failure: profileDocumentResourceFailure(
			parser.sourceID,
			"json_depth",
			definition.MaxJSONDepth,
			actual,
		),
		path: pointer,
	})
	parser.buildTree = false
}

func (parser *profileJSONParser) skipOverDepthContainer() error {
	if parser.position >= len(parser.data) || (parser.data[parser.position] != '{' && parser.data[parser.position] != '[') {
		return errors.New("over-depth value is not a container")
	}
	stack := []byte{parser.data[parser.position]}
	parser.position++
	inString := false
	for parser.position < len(parser.data) {
		current := parser.data[parser.position]
		parser.position++
		if inString {
			switch current {
			case '\\':
				if parser.position >= len(parser.data) {
					return errors.New("unfinished string escape")
				}
				parser.position++
			case '"':
				inString = false
			}
			continue
		}
		switch current {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, current)
		case '}', ']':
			if len(stack) == 0 || !profileMatchingJSONDelimiter(stack[len(stack)-1], current) {
				return errors.New("mismatched JSON delimiter")
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return nil
			}
		}
	}
	return errors.New("unterminated over-depth container")
}

func profileMatchingJSONDelimiter(open, close byte) bool {
	return open == '{' && close == '}' || open == '[' && close == ']'
}

func (parser *profileJSONParser) skipWhitespace() {
	for parser.position < len(parser.data) {
		switch parser.data[parser.position] {
		case ' ', '\t', '\n', '\r':
			parser.position++
		default:
			return
		}
	}
}

func (parser *profileJSONParser) consumeByte(want byte) bool {
	if parser.position >= len(parser.data) || parser.data[parser.position] != want {
		return false
	}
	parser.position++
	return true
}

func (parser *profileJSONParser) consumeLiteral(want string) bool {
	if len(parser.data)-parser.position < len(want) || string(parser.data[parser.position:parser.position+len(want)]) != want {
		return false
	}
	parser.position += len(want)
	return true
}

func profileParseHexUnit(value []byte) (uint16, bool) {
	if len(value) != 4 {
		return 0, false
	}
	var output uint16
	for _, current := range value {
		output <<= 4
		switch {
		case current >= '0' && current <= '9':
			output += uint16(current - '0')
		case current >= 'a' && current <= 'f':
			output += uint16(current-'a') + 10
		case current >= 'A' && current <= 'F':
			output += uint16(current-'A') + 10
		default:
			return 0, false
		}
	}
	return output, true
}

func profileLessDocumentFailure(left, right *profileCandidateError) bool {
	leftResource := left.Reason == "resource_limit_exceeded"
	rightResource := right.Reason == "resource_limit_exceeded"
	if leftResource != rightResource {
		return leftResource
	}
	if leftResource {
		leftRank := profileDocumentLimitRank(left.Limit)
		rightRank := profileDocumentLimitRank(right.Limit)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
	}
	if left.SourceID != right.SourceID {
		return bytes.Compare([]byte(left.SourceID), []byte(right.SourceID)) < 0
	}
	if left.Pointer != right.Pointer {
		return bytes.Compare([]byte(left.Pointer), []byte(right.Pointer)) < 0
	}
	if !leftResource {
		leftRank := profileDocumentReasonRank(left.Reason)
		rightRank := profileDocumentReasonRank(right.Reason)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
	}
	if left.Limit != right.Limit {
		return left.Limit < right.Limit
	}
	if left.Maximum != right.Maximum {
		return left.Maximum < right.Maximum
	}
	return left.Actual < right.Actual
}

func profileDocumentLimitRank(limit string) int {
	switch limit {
	case "json_depth":
		return 0
	case "document_json_values":
		return 1
	case "json_values":
		return 2
	default:
		return 3
	}
}

func profileDocumentReasonRank(reason string) int {
	switch reason {
	case "invalid_utf8":
		return 0
	case "syntax":
		return 1
	case "duplicate_key":
		return 2
	case "lone_surrogate":
		return 3
	case "trailing_value":
		return 8
	default:
		return 100
	}
}

func profileDocumentFailure(sourceID, reason string) *profileCandidateError {
	return &profileCandidateError{
		Category:       definition.CategorySource,
		Code:           string(definition.CodeInvalidDocument),
		Stage:          "document",
		Reason:         reason,
		SourceID:       sourceID,
		OperationIndex: -1,
	}
}

func profileDocumentResourceFailure(sourceID, limit string, maximum, actual int) *profileCandidateError {
	return &profileCandidateError{
		Category:       definition.CategorySource,
		Code:           string(definition.CodeInvalidDocument),
		Stage:          "document",
		Reason:         "resource_limit_exceeded",
		SourceID:       sourceID,
		OperationIndex: -1,
		Limit:          limit,
		Maximum:        maximum,
		Actual:         actual,
	}
}

func profileMaterializeSource(scanned profileRawScan) (profileSource, *profileCandidateError) {
	producerNode, producerOK := scanned.root.member("producer")
	migrationNode, migrationOK := scanned.root.member("migration")
	producer, producerDecoded := profileMaterializeProducer(producerNode)
	migration, migrationDecoded := profileMaterializeDefinition(migrationNode)
	if !producerOK || !migrationOK || !producerDecoded || !migrationDecoded {
		return profileSource{}, profileRawSemanticError(scanned.source.SourceID, "tree_materialization_invariant")
	}
	return profileSource{
		SourceID: scanned.source.SourceID, Producer: producer, Profile: scanned.profile, Definition: migration,
	}, nil
}

func profileMaterializeProducer(value profileJSONValue) (profileProducer, bool) {
	name, nameOK := profileTreeString(value, "name")
	version, versionOK := profileTreeString(value, "version")
	return profileProducer{Name: name, Version: version}, value.kind == profileJSONObject && nameOK && versionOK
}

func profileMaterializeDefinition(value profileJSONValue) (profileDefinition, bool) {
	app, appOK := profileTreeString(value, "app")
	name, nameOK := profileTreeString(value, "name")
	dependenciesNode, dependenciesOK := value.member("dependencies")
	operationsNode, operationsOK := value.member("operations")
	if value.kind != profileJSONObject || !appOK || !nameOK || !dependenciesOK || dependenciesNode.kind != profileJSONArray ||
		!operationsOK || operationsNode.kind != profileJSONArray {
		return profileDefinition{}, false
	}
	result := profileDefinition{
		App: app, Name: name,
		Dependencies: make([]profileIdentity, len(dependenciesNode.array)),
		Operations:   make([]profileOperation, len(operationsNode.array)),
	}
	for index := range dependenciesNode.array {
		dependency := dependenciesNode.array[index]
		dependencyApp, dependencyAppOK := profileTreeString(dependency, "app")
		dependencyName, dependencyNameOK := profileTreeString(dependency, "name")
		if dependency.kind != profileJSONObject || !dependencyAppOK || !dependencyNameOK {
			return profileDefinition{}, false
		}
		result.Dependencies[index] = profileIdentity{App: dependencyApp, Name: dependencyName}
	}
	for index := range operationsNode.array {
		operation, ok := profileMaterializeOperation(operationsNode.array[index])
		if !ok {
			return profileDefinition{}, false
		}
		result.Operations[index] = operation
	}
	return result, true
}

func profileMaterializeOperation(value profileJSONValue) (profileOperation, bool) {
	kind, kindOK := profileTreeString(value, "kind")
	if value.kind != profileJSONObject || !kindOK {
		return profileOperation{}, false
	}
	result := profileOperation{Kind: kind}
	if member, exists := value.member("app_label"); exists && member.kind != profileJSONNull {
		if member.kind != profileJSONString {
			return profileOperation{}, false
		}
		result.AppLabel = member.string
	}
	if member, exists := value.member("model_name"); exists && member.kind != profileJSONNull {
		if member.kind != profileJSONString {
			return profileOperation{}, false
		}
		result.ModelName = member.string
	}
	if member, exists := value.member("field"); exists && member.kind != profileJSONNull {
		field, ok := profileMaterializeField(member)
		if !ok {
			return profileOperation{}, false
		}
		result.Field = &field
	}
	if member, exists := value.member("model"); exists && member.kind != profileJSONNull {
		model, ok := profileMaterializeModel(member)
		if !ok {
			return profileOperation{}, false
		}
		result.Model = &model
	}
	return result, true
}

func profileMaterializeModel(value profileJSONValue) (profileModel, bool) {
	dbTable, dbTableOK := profileTreeString(value, "db_table")
	goName, goNameOK := profileTreeString(value, "go_name")
	name, nameOK := profileTreeString(value, "name")
	fieldsNode, fieldsOK := value.member("fields")
	if value.kind != profileJSONObject || !dbTableOK || !goNameOK || !nameOK || !fieldsOK || fieldsNode.kind != profileJSONArray {
		return profileModel{}, false
	}
	result := profileModel{DBTable: dbTable, GoName: goName, Name: name, Fields: make([]profileField, len(fieldsNode.array))}
	for index := range fieldsNode.array {
		field, ok := profileMaterializeField(fieldsNode.array[index])
		if !ok {
			return profileModel{}, false
		}
		result.Fields[index] = field
	}
	return result, true
}

func profileMaterializeField(value profileJSONValue) (profileField, bool) {
	column, columnOK := profileTreeString(value, "column")
	goName, goNameOK := profileTreeString(value, "go_name")
	kind, kindOK := profileTreeString(value, "kind")
	name, nameOK := profileTreeString(value, "name")
	maximum, maximumOK := profileTreeInteger(value, "max_length")
	nullable, nullableOK := profileTreeBoolean(value, "nullable")
	primaryKey, primaryKeyOK := profileTreeBoolean(value, "primary_key")
	if value.kind != profileJSONObject || !columnOK || !goNameOK || !kindOK || !nameOK || !maximumOK || !nullableOK || !primaryKeyOK {
		return profileField{}, false
	}
	result := profileField{
		Column: column, GoName: goName, Kind: kind, MaxLength: maximum, Name: name,
		Nullable: nullable, PrimaryKey: primaryKey,
	}
	if member, exists := value.member("default"); exists && member.kind != profileJSONNull {
		defaultValue, ok := profileMaterializeDefault(member)
		if !ok {
			return profileField{}, false
		}
		result.Default = &defaultValue
	}
	if member, exists := value.member("relation"); exists && member.kind != profileJSONNull {
		relation, ok := profileMaterializeRelation(member)
		if !ok {
			return profileField{}, false
		}
		result.Relation = &relation
	}
	return result, true
}

func profileMaterializeDefault(value profileJSONValue) (profileDefault, bool) {
	kind, kindOK := profileTreeString(value, "kind")
	if value.kind != profileJSONObject || !kindOK {
		return profileDefault{}, false
	}
	result := profileDefault{Kind: kind}
	if member, exists := value.member("boolean"); exists && member.kind != profileJSONNull {
		if member.kind != profileJSONBoolean {
			return profileDefault{}, false
		}
		copy := member.boolean
		result.Boolean = &copy
	}
	if member, exists := value.member("integer"); exists && member.kind != profileJSONNull {
		parsed, _, ok := profileSignedInteger(member)
		if !ok {
			return profileDefault{}, false
		}
		copy := parsed
		result.Integer = &copy
	}
	if member, exists := value.member("string"); exists && member.kind != profileJSONNull {
		if member.kind != profileJSONString {
			return profileDefault{}, false
		}
		copy := member.string
		result.String = &copy
	}
	return result, true
}

func profileMaterializeRelation(value profileJSONValue) (profileRelation, bool) {
	cardinality, cardinalityOK := profileTreeString(value, "cardinality")
	onDelete, onDeleteOK := profileTreeString(value, "on_delete")
	targetNode, targetOK := value.member("target")
	reverseNode, reverseOK := value.member("reverse")
	targetApp, targetAppOK := profileTreeString(targetNode, "app_label")
	targetModel, targetModelOK := profileTreeString(targetNode, "model_name")
	reverseName, reverseNameOK := profileTreeString(reverseNode, "name")
	reverseDisabled, reverseDisabledOK := profileTreeBoolean(reverseNode, "disabled")
	if value.kind != profileJSONObject || !cardinalityOK || !onDeleteOK || !targetOK || targetNode.kind != profileJSONObject ||
		!reverseOK || reverseNode.kind != profileJSONObject || !targetAppOK || !targetModelOK || !reverseNameOK || !reverseDisabledOK {
		return profileRelation{}, false
	}
	return profileRelation{
		Target: profileTarget{App: targetApp, Model: targetModel}, Cardinality: cardinality,
		Reverse: profileReverse{Name: reverseName, Disabled: reverseDisabled}, OnDelete: onDelete,
	}, true
}

func profileTreeString(value profileJSONValue, name string) (string, bool) {
	member, exists := value.member(name)
	return member.string, exists && member.kind == profileJSONString
}

func profileTreeBoolean(value profileJSONValue, name string) (bool, bool) {
	member, exists := value.member(name)
	return member.boolean, exists && member.kind == profileJSONBoolean
}

func profileTreeInteger(value profileJSONValue, name string) (int64, bool) {
	member, exists := value.member(name)
	if !exists {
		return 0, false
	}
	parsed, _, ok := profileSignedInteger(member)
	return parsed, ok
}

func profileRawSemanticError(sourceID, reason string) *profileCandidateError {
	return &profileCandidateError{
		Category: "migration_relation_profile_candidate_error",
		Code:     "invalid_definition",
		Stage:    "semantic",
		Reason:   reason,
		SourceID: sourceID,
	}
}

func profileDocumentCandidates(scanned profileRawScan) []*profileCandidateError {
	root, rootOK, candidates := profileExactDocumentObject(
		scanned.root,
		[]string{"compatibility", "migration", "producer"},
		scanned.source.SourceID,
		"",
	)
	if !rootOK {
		return candidates
	}

	if compatibility, exists := root.member("compatibility"); exists {
		compatibilityObject, objectOK, faults := profileExactDocumentObject(
			compatibility,
			[]string{"definition_format", "loader_abi", "operation_codec", "schema_ir"},
			scanned.source.SourceID,
			"/compatibility",
		)
		candidates = append(candidates, faults...)
		if objectOK {
			for _, coordinate := range []string{"definition_format", "loader_abi", "operation_codec", "schema_ir"} {
				value, present := compatibilityObject.member(coordinate)
				if !present {
					continue
				}
				if _, reason, valid := profileSignedInteger(value); !valid {
					candidates = append(candidates, profileDocumentFailure(scanned.source.SourceID, reason))
					candidates[len(candidates)-1].Pointer = "/compatibility/" + coordinate
				}
			}
		}
	}

	if producer, exists := root.member("producer"); exists {
		producerObject, objectOK, faults := profileExactDocumentObject(
			producer,
			[]string{"name", "version"},
			scanned.source.SourceID,
			"/producer",
		)
		candidates = append(candidates, faults...)
		if objectOK {
			for _, field := range []string{"name", "version"} {
				value, present := producerObject.member(field)
				if present && (value.kind != profileJSONString || value.string == "") {
					failure := profileDocumentFailure(scanned.source.SourceID, "wrong_type")
					failure.Pointer = "/producer/" + field
					candidates = append(candidates, failure)
				}
			}
		}
	}

	if migration, exists := root.member("migration"); exists {
		_, migrationOK, faults := profileExactDocumentObject(
			migration,
			[]string{"app", "dependencies", "name", "operations"},
			scanned.source.SourceID,
			"/migration",
		)
		candidates = append(candidates, faults...)
		if migrationOK {
			candidates = append(candidates, profileKnownMaxLengthLexicalCandidates(migration, scanned.source.SourceID)...)
		}
	}
	return candidates
}

func profileExactDocumentObject(value profileJSONValue, fields []string, sourceID, pointer string) (profileJSONValue, bool, []*profileCandidateError) {
	if value.kind != profileJSONObject {
		failure := profileDocumentFailure(sourceID, "wrong_type")
		failure.Pointer = pointer
		return profileJSONValue{}, false, []*profileCandidateError{failure}
	}
	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
	}
	candidates := make([]*profileCandidateError, 0)
	for _, member := range value.object {
		if _, recognized := wanted[member.key]; !recognized {
			failure := profileDocumentFailure(sourceID, "unknown_field")
			failure.Pointer = profileCodecPointer(pointer, member.key)
			candidates = append(candidates, failure)
		}
	}
	for _, field := range fields {
		if _, exists := value.member(field); !exists {
			failure := profileDocumentFailure(sourceID, "missing_field")
			failure.Pointer = profileCodecPointer(pointer, field)
			candidates = append(candidates, failure)
		}
	}
	return value, true, candidates
}

func profileSignedInteger(value profileJSONValue) (int64, string, bool) {
	if value.kind != profileJSONNumber || strings.ContainsAny(value.number, ".eE") {
		return 0, "wrong_type", false
	}
	parsed, err := strconv.ParseInt(value.number, 10, 64)
	if err != nil {
		return 0, "out_of_range", false
	}
	return parsed, "", true
}

func profileKnownMaxLengthLexicalCandidates(migration profileJSONValue, sourceID string) []*profileCandidateError {
	operations, exists := migration.member("operations")
	if !exists || operations.kind != profileJSONArray {
		return nil
	}
	candidates := make([]*profileCandidateError, 0)
	for operationIndex, operation := range operations.array {
		if operation.kind != profileJSONObject {
			continue
		}
		kind, exists := operation.member("kind")
		if !exists || kind.kind != profileJSONString {
			continue
		}
		operationPointer := "/migration/operations/" + strconv.Itoa(operationIndex)
		switch kind.string {
		case "create_model":
			model, exists := operation.member("model")
			if !exists || model.kind != profileJSONObject {
				continue
			}
			fields, exists := model.member("fields")
			if !exists || fields.kind != profileJSONArray {
				continue
			}
			for fieldIndex, field := range fields.array {
				candidates = append(candidates, profileMaxLengthLexicalCandidate(field, sourceID, operationPointer+"/model/fields/"+strconv.Itoa(fieldIndex))...)
			}
		case "add_field":
			if field, exists := operation.member("field"); exists {
				candidates = append(candidates, profileMaxLengthLexicalCandidate(field, sourceID, operationPointer+"/field")...)
			}
		}
	}
	return candidates
}

func profileMaxLengthLexicalCandidate(field profileJSONValue, sourceID, pointer string) []*profileCandidateError {
	if field.kind != profileJSONObject {
		return nil
	}
	maximum, exists := field.member("max_length")
	if !exists || maximum.kind != profileJSONNumber || strings.ContainsAny(maximum.number, ".eE") {
		return nil
	}
	if _, err := strconv.ParseInt(maximum.number, 10, 64); err != nil {
		failure := profileDocumentFailure(sourceID, "out_of_range")
		failure.Pointer = pointer + "/max_length"
		return []*profileCandidateError{failure}
	}
	return nil
}

func profileSemanticFailureCandidate(
	code definition.ErrorCode,
	sourceID string,
	pointer string,
	app string,
	name string,
	operationIndex int,
	reason string,
) *profileCandidateError {
	return &profileCandidateError{
		Category:       definition.CategorySource,
		Code:           string(code),
		Stage:          "semantic",
		Reason:         reason,
		SourceID:       sourceID,
		Pointer:        pointer,
		App:            app,
		Name:           name,
		OperationIndex: operationIndex,
	}
}

func profileSemanticResourceFailure(
	code definition.ErrorCode,
	sourceID string,
	pointer string,
	app string,
	name string,
	limit string,
	maximum int,
	actual int,
	operationIndex int,
) *profileCandidateError {
	failure := profileSemanticFailureCandidate(code, sourceID, pointer, app, name, operationIndex, "resource_limit_exceeded")
	failure.Limit = limit
	failure.Maximum = maximum
	failure.Actual = actual
	return failure
}

func profileSortCandidateFailures(candidates []*profileCandidateError) {
	sort.SliceStable(candidates, func(left, right int) bool {
		return profileLessCandidateFailure(candidates[left], candidates[right])
	})
}

func profileLessCandidateFailure(left, right *profileCandidateError) bool {
	leftStage := profileFailureStageRank(left.Stage)
	rightStage := profileFailureStageRank(right.Stage)
	if leftStage != rightStage {
		return leftStage < rightStage
	}
	leftResource := left.Reason == "resource_limit_exceeded"
	rightResource := right.Reason == "resource_limit_exceeded"
	if leftResource != rightResource {
		return leftResource
	}
	if leftResource {
		leftLimit := profileFailureLimitRank(left.Limit)
		rightLimit := profileFailureLimitRank(right.Limit)
		if leftLimit != rightLimit {
			return leftLimit < rightLimit
		}
	} else if left.Stage == "source" || left.Stage == "compatibility" {
		leftReason := profileFailureReasonRank(left.Stage, left.Reason)
		rightReason := profileFailureReasonRank(right.Stage, right.Reason)
		if leftReason != rightReason {
			return leftReason < rightReason
		}
	}
	if left.SourceID != right.SourceID {
		return left.SourceID < right.SourceID
	}
	if left.Pointer != right.Pointer {
		return left.Pointer < right.Pointer
	}
	if !leftResource {
		leftReason := profileFailureReasonRank(left.Stage, left.Reason)
		rightReason := profileFailureReasonRank(right.Stage, right.Reason)
		if leftReason != rightReason {
			return leftReason < rightReason
		}
	}
	if left.App != right.App {
		return left.App < right.App
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	if left.OperationIndex != right.OperationIndex {
		return left.OperationIndex < right.OperationIndex
	}
	if left.Code != right.Code {
		return left.Code < right.Code
	}
	if left.Limit != right.Limit {
		return left.Limit < right.Limit
	}
	if left.Maximum != right.Maximum {
		return left.Maximum < right.Maximum
	}
	return left.Actual < right.Actual
}

func profileFailureStageRank(stage string) int {
	switch stage {
	case "source":
		return 0
	case "document":
		return 1
	case "compatibility":
		return 2
	case "semantic":
		return 3
	case "graph":
		return 4
	default:
		return 5
	}
}

func profileFailureLimitRank(limit string) int {
	switch limit {
	case "source_count":
		return 0
	case "source_id_bytes":
		return 1
	case "document_bytes":
		return 2
	case "batch_bytes":
		return 3
	case "json_depth":
		return 4
	case "document_json_values":
		return 5
	case "json_values":
		return 6
	case "dependencies_per_migration":
		return 7
	case "operations_per_migration":
		return 8
	case "fields_per_create_model":
		return 9
	default:
		return 10
	}
}

func profileFailureReasonRank(stage, reason string) int {
	switch stage {
	case "source":
		switch reason {
		case "empty_source_id":
			return 0
		case "invalid_source_id_utf8":
			return 1
		case "duplicate_source_id":
			return 2
		}
	case "document":
		switch reason {
		case "invalid_utf8":
			return 0
		case "syntax":
			return 1
		case "duplicate_key":
			return 2
		case "lone_surrogate":
			return 3
		case "unknown_field":
			return 4
		case "missing_field":
			return 5
		case "wrong_type":
			return 6
		case "out_of_range":
			return 7
		case "trailing_value":
			return 8
		}
	case "compatibility":
		return profileCoordinateRank(reason)
	case "semantic":
		switch reason {
		case "unsupported_operation":
			return 0
		case "invalid_operation":
			return 1
		case "invalid_ir":
			return 2
		case "wrong_type":
			return 3
		case "out_of_range":
			return 4
		}
	}
	return 100
}

func profileCodecPointer(parent, token string) string {
	escaped := strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
	if parent == "" {
		return "/" + escaped
	}
	return parent + "/" + escaped
}

func profileJSONHeaderString(value profileJSONValue, name string) (string, bool) {
	if value.kind != profileJSONObject {
		return "", false
	}
	member, exists := value.member(name)
	if !exists || member.kind == profileJSONNull {
		return "", true
	}
	if member.kind != profileJSONString {
		return "", false
	}
	return member.string, true
}

func profileJSONArrayValues(value profileJSONValue) ([]profileJSONValue, bool) {
	if value.kind == profileJSONNull {
		return nil, true
	}
	if value.kind != profileJSONArray {
		return nil, false
	}
	return value.array, true
}

func profileRawResourceFailure(sources []profileRawScan) *profileCandidateError {
	dependencies := make([]*profileCandidateError, 0)
	for _, scanned := range sources {
		migration, exists := scanned.root.member("migration")
		if !exists || migration.kind != profileJSONObject {
			continue
		}
		value, exists := migration.member("dependencies")
		if exists && value.kind == profileJSONArray && len(value.array) > profileMaxDependencies {
			app, name := profileMigrationIdentity(migration)
			dependencies = append(dependencies, profileSemanticResourceFailure(
				definition.CodeInvalidOperation,
				scanned.source.SourceID,
				"/migration/dependencies",
				app,
				name,
				"dependencies_per_migration",
				profileMaxDependencies,
				len(value.array),
				-1,
			))
		}
	}
	if len(dependencies) != 0 {
		profileSortCandidateFailures(dependencies)
		return dependencies[0]
	}

	operations := make([]*profileCandidateError, 0)
	for _, scanned := range sources {
		migration, exists := scanned.root.member("migration")
		if !exists || migration.kind != profileJSONObject {
			continue
		}
		value, exists := migration.member("operations")
		if exists && value.kind == profileJSONArray && len(value.array) > profileMaxOperations {
			app, name := profileMigrationIdentity(migration)
			operations = append(operations, profileSemanticResourceFailure(
				definition.CodeInvalidOperation,
				scanned.source.SourceID,
				"/migration/operations",
				app,
				name,
				"operations_per_migration",
				profileMaxOperations,
				len(value.array),
				-1,
			))
		}
	}
	if len(operations) != 0 {
		profileSortCandidateFailures(operations)
		return operations[0]
	}

	fields := make([]*profileCandidateError, 0)
	for _, scanned := range sources {
		migration, exists := scanned.root.member("migration")
		if !exists || migration.kind != profileJSONObject {
			continue
		}
		operations, _ := migration.member("operations")
		operationValues, operationsOK := profileJSONArrayValues(operations)
		if !operationsOK {
			continue
		}
		app, name := profileMigrationIdentity(migration)
		for operationIndex, operation := range operationValues {
			kind, ok := profileJSONHeaderString(operation, "kind")
			if !ok || kind != "create_model" {
				continue
			}
			model, exists := operation.member("model")
			if !exists || model.kind != profileJSONObject {
				continue
			}
			fieldNode, exists := model.member("fields")
			fieldValues, fieldsOK := profileJSONArrayValues(fieldNode)
			if !exists || !fieldsOK {
				continue
			}
			if len(fieldValues) > profileMaxFields {
				fields = append(fields, profileSemanticResourceFailure(
					definition.CodeInvalidIR,
					scanned.source.SourceID,
					"/migration/operations/"+strconv.Itoa(operationIndex)+"/model/fields",
					app,
					name,
					"fields_per_create_model",
					profileMaxFields,
					len(fieldValues),
					operationIndex,
				))
			}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	profileSortCandidateFailures(fields)
	return fields[0]
}

func profileMigrationIdentity(migration profileJSONValue) (string, string) {
	app := ""
	name := ""
	if value, exists := migration.member("app"); exists && value.kind == profileJSONString {
		app = value.string
	}
	if value, exists := migration.member("name"); exists && value.kind == profileJSONString {
		name = value.string
	}
	return app, name
}

func profileSemanticCandidates(scanned profileRawScan, decoder profileDecoder) []*profileCandidateError {
	migration, exists := scanned.root.member("migration")
	if !exists || migration.kind != profileJSONObject {
		return []*profileCandidateError{profileSemanticFailureCandidate(
			definition.CodeInvalidOperation, scanned.source.SourceID, "/migration", "", "", -1, "invalid_operation",
		)}
	}
	candidates := make([]*profileCandidateError, 0)
	app := ""
	name := ""
	if value, present := migration.member("app"); present && value.kind == profileJSONString {
		app = value.string
	} else {
		candidates = append(candidates, profileSemanticFailureCandidate(
			definition.CodeInvalidOperation, scanned.source.SourceID, "/migration/app", "", "", -1, "invalid_operation",
		))
	}
	if value, present := migration.member("name"); present && value.kind == profileJSONString {
		name = value.string
	} else {
		candidates = append(candidates, profileSemanticFailureCandidate(
			definition.CodeInvalidOperation, scanned.source.SourceID, "/migration/name", app, "", -1, "invalid_operation",
		))
	}

	dependencies, present := migration.member("dependencies")
	if !present || dependencies.kind != profileJSONArray {
		candidates = append(candidates, profileSemanticFailureCandidate(
			definition.CodeInvalidOperation, scanned.source.SourceID, "/migration/dependencies", app, name, -1, "invalid_operation",
		))
	} else {
		for index, dependency := range dependencies.array {
			pointer := "/migration/dependencies/" + strconv.Itoa(index)
			object, objectOK, faults := profileSemanticObjectCandidates(
				dependency, []string{"app", "name"}, scanned.source.SourceID, pointer, app, name, -1, definition.CodeInvalidOperation,
			)
			candidates = append(candidates, faults...)
			if !objectOK {
				continue
			}
			for _, field := range []string{"app", "name"} {
				if value, exists := object.member(field); exists && value.kind != profileJSONString {
					candidates = append(candidates, profileSemanticFailureCandidate(
						definition.CodeInvalidOperation, scanned.source.SourceID, pointer+"/"+field, app, name, -1, "invalid_operation",
					))
				}
			}
		}
	}

	operations, present := migration.member("operations")
	if !present || operations.kind != profileJSONArray {
		candidates = append(candidates, profileSemanticFailureCandidate(
			definition.CodeInvalidOperation, scanned.source.SourceID, "/migration/operations", app, name, -1, "invalid_operation",
		))
	} else {
		for index, operation := range operations.array {
			candidates = append(candidates, profileCollectOperationCandidates(
				operation, scanned.source.SourceID, app, name, index, decoder,
			)...)
		}
	}
	profileSortCandidateFailures(candidates)
	return candidates
}

func profileSemanticObjectCandidates(
	value profileJSONValue,
	fields []string,
	sourceID string,
	pointer string,
	app string,
	name string,
	operationIndex int,
	code definition.ErrorCode,
) (profileJSONValue, bool, []*profileCandidateError) {
	if value.kind != profileJSONObject {
		return profileJSONValue{}, false, []*profileCandidateError{profileSemanticFailureCandidate(
			code, sourceID, pointer, app, name, operationIndex, profileSemanticReason(code),
		)}
	}
	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
	}
	candidates := make([]*profileCandidateError, 0)
	for _, member := range value.object {
		if _, recognized := wanted[member.key]; !recognized {
			candidates = append(candidates, profileSemanticFailureCandidate(
				code, sourceID, profileCodecPointer(pointer, member.key), app, name, operationIndex, profileSemanticReason(code),
			))
		}
	}
	for _, field := range fields {
		if _, exists := value.member(field); !exists {
			candidates = append(candidates, profileSemanticFailureCandidate(
				code, sourceID, profileCodecPointer(pointer, field), app, name, operationIndex, profileSemanticReason(code),
			))
		}
	}
	return value, true, candidates
}

func profileSemanticUnknownCandidates(
	value profileJSONValue,
	fields []string,
	sourceID string,
	pointer string,
	app string,
	name string,
	operationIndex int,
	code definition.ErrorCode,
) []*profileCandidateError {
	if value.kind != profileJSONObject {
		return nil
	}
	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
	}
	candidates := make([]*profileCandidateError, 0)
	for _, member := range value.object {
		if _, recognized := wanted[member.key]; !recognized {
			candidates = append(candidates, profileSemanticFailureCandidate(
				code, sourceID, profileCodecPointer(pointer, member.key), app, name, operationIndex, profileSemanticReason(code),
			))
		}
	}
	return candidates
}

func profileSemanticReason(code definition.ErrorCode) string {
	switch code {
	case definition.CodeUnsupportedOperation:
		return "unsupported_operation"
	case definition.CodeInvalidIR:
		return "invalid_ir"
	default:
		return "invalid_operation"
	}
}

func profileCollectOperationCandidates(
	value profileJSONValue,
	sourceID string,
	app string,
	name string,
	operationIndex int,
	decoder profileDecoder,
) []*profileCandidateError {
	pointer := "/migration/operations/" + strconv.Itoa(operationIndex)
	if value.kind != profileJSONObject {
		return []*profileCandidateError{profileSemanticFailureCandidate(
			definition.CodeInvalidOperation, sourceID, pointer, app, name, operationIndex, "invalid_operation",
		)}
	}
	commonFields := []string{"app_label", "field", "kind", "model", "model_name"}
	candidates := profileSemanticUnknownCandidates(
		value, commonFields, sourceID, pointer, app, name, operationIndex, definition.CodeInvalidOperation,
	)
	kind, exists := value.member("kind")
	if !exists || kind.kind != profileJSONString {
		return append(candidates, profileSemanticFailureCandidate(
			definition.CodeInvalidOperation, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_operation",
		))
	}
	if kind.string != "create_model" && kind.string != "add_field" {
		return append(candidates, profileSemanticFailureCandidate(
			definition.CodeUnsupportedOperation, sourceID, pointer+"/kind", app, name, operationIndex, "unsupported_operation",
		))
	}

	fields := []string{"app_label", "kind", "model"}
	if kind.string == "add_field" {
		fields = []string{"app_label", "field", "kind", "model_name"}
	}
	object, _, faults := profileSemanticObjectCandidates(
		value, fields, sourceID, pointer, app, name, operationIndex, definition.CodeInvalidOperation,
	)
	candidates = append(candidates, faults...)
	if appLabel, present := object.member("app_label"); present {
		if appLabel.kind != profileJSONString || appLabel.string != app {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidOperation, sourceID, pointer+"/app_label", app, name, operationIndex, "invalid_operation",
			))
		} else if !profileValidAppLabel(appLabel.string, decoder) {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidIR, sourceID, pointer+"/app_label", app, name, operationIndex, "invalid_ir",
			))
		}
	}

	if kind.string == "create_model" {
		if model, present := object.member("model"); present {
			candidates = append(candidates, profileCollectModelCandidates(
				model, sourceID, pointer+"/model", app, name, operationIndex, decoder,
			)...)
		}
		return candidates
	}

	if modelName, present := object.member("model_name"); present {
		if modelName.kind != profileJSONString || !profileValidModelName(modelName.string, decoder) {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidOperation, sourceID, pointer+"/model_name", app, name, operationIndex, "invalid_operation",
			))
		}
	}
	if field, present := object.member("field"); present {
		candidates = append(candidates, profileCollectFieldCandidates(
			field, sourceID, pointer+"/field", app, name, operationIndex, decoder,
		)...)
		if field.kind == profileJSONObject {
			if fieldKind, exists := field.member("kind"); exists && fieldKind.kind == profileJSONString {
				allowed := fieldKind.string == string(ir.FieldChar) || fieldKind.string == string(ir.FieldBoolean)
				if decoder == profileDecoderRelation {
					allowed = allowed || fieldKind.string == string(ir.FieldForeignKey)
				}
				if !allowed {
					candidates = append(candidates, profileSemanticFailureCandidate(
						definition.CodeInvalidIR, sourceID, pointer+"/field/kind", app, name, operationIndex, "invalid_ir",
					))
				}
			}
			if primaryKey, exists := field.member("primary_key"); exists && primaryKey.kind == profileJSONBoolean && primaryKey.boolean {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/field/primary_key", app, name, operationIndex, "invalid_ir",
				))
			}
		}
	}
	return candidates
}

func profileCollectModelCandidates(
	value profileJSONValue,
	sourceID string,
	pointer string,
	app string,
	name string,
	operationIndex int,
	decoder profileDecoder,
) []*profileCandidateError {
	fields := []string{"db_table", "fields", "go_name", "name"}
	object, objectOK, candidates := profileSemanticObjectCandidates(
		value, fields, sourceID, pointer, app, name, operationIndex, definition.CodeInvalidIR,
	)
	if !objectOK {
		return candidates
	}
	for _, field := range []string{"db_table", "go_name", "name"} {
		child, exists := object.member(field)
		if !exists {
			continue
		}
		valid := child.kind == profileJSONString
		if valid {
			switch field {
			case "db_table":
				valid = profileValidModelTable(child.string, decoder)
			case "go_name":
				valid = profileValidModelGoName(child.string, decoder)
			case "name":
				valid = profileValidModelName(child.string, decoder)
			}
		}
		if !valid {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidIR, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir",
			))
		}
	}
	if child, exists := object.member("fields"); exists {
		if child.kind != profileJSONArray || len(child.array) == 0 {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidIR, sourceID, pointer+"/fields", app, name, operationIndex, "invalid_ir",
			))
		} else {
			for index, field := range child.array {
				candidates = append(candidates, profileCollectFieldCandidates(
					field, sourceID, pointer+"/fields/"+strconv.Itoa(index), app, name, operationIndex, decoder,
				)...)
			}
			candidates = append(candidates, profileCollectModelFieldAggregateCandidates(
				child.array, sourceID, pointer+"/fields", app, name, operationIndex, decoder,
			)...)
		}
	}
	return candidates
}

func profileCollectModelFieldAggregateCandidates(
	values []profileJSONValue,
	sourceID string,
	pointer string,
	app string,
	name string,
	operationIndex int,
	decoder profileDecoder,
) []*profileCandidateError {
	seenNames := make(map[string]struct{}, len(values))
	seenGoNames := make(map[string]struct{}, len(values))
	seenColumns := make(map[string]struct{}, len(values))
	primaryKeys := 0
	primaryKeysComplete := true
	hasAuto := false
	kindsComplete := true
	aggregateInvalid := false
	for _, value := range values {
		if value.kind != profileJSONObject {
			primaryKeysComplete = false
			kindsComplete = false
			continue
		}
		for _, member := range []struct {
			name  string
			seen  map[string]struct{}
			valid func(string, profileDecoder) bool
		}{
			{name: "name", seen: seenNames, valid: profileValidFieldName},
			{name: "go_name", seen: seenGoNames, valid: profileValidFieldGoName},
			{name: "column", seen: seenColumns, valid: profileValidFieldColumn},
		} {
			candidate, exists := value.member(member.name)
			if !exists || candidate.kind != profileJSONString || !member.valid(candidate.string, decoder) {
				continue
			}
			if _, duplicate := member.seen[candidate.string]; duplicate {
				aggregateInvalid = true
			}
			member.seen[candidate.string] = struct{}{}
		}
		primaryKey, exists := value.member("primary_key")
		if !exists || primaryKey.kind != profileJSONBoolean {
			primaryKeysComplete = false
		} else if primaryKey.boolean {
			primaryKeys++
		}
		kind, exists := value.member("kind")
		if !exists || kind.kind != profileJSONString {
			kindsComplete = false
		} else if kind.string == string(ir.FieldAuto) {
			hasAuto = true
		}
	}
	if primaryKeys >= 2 || (primaryKeysComplete && primaryKeys != 1) {
		aggregateInvalid = true
	}
	if kindsComplete && !hasAuto {
		aggregateInvalid = true
	}
	if !aggregateInvalid {
		return nil
	}
	return []*profileCandidateError{profileSemanticFailureCandidate(
		definition.CodeInvalidIR, sourceID, pointer, app, name, operationIndex, "invalid_ir",
	)}
}

func profileCollectFieldCandidates(
	value profileJSONValue,
	sourceID string,
	pointer string,
	app string,
	name string,
	operationIndex int,
	decoder profileDecoder,
) []*profileCandidateError {
	fields := []string{"column", "default", "go_name", "kind", "max_length", "name", "nullable", "primary_key"}
	if decoder == profileDecoderRelation {
		if kind, exists := value.member("kind"); exists && kind.kind == profileJSONString && kind.string == string(ir.FieldForeignKey) {
			fields = append(fields, "relation")
		}
	}
	object, objectOK, candidates := profileSemanticObjectCandidates(
		value, fields, sourceID, pointer, app, name, operationIndex, definition.CodeInvalidIR,
	)
	if !objectOK {
		return candidates
	}
	stringValid := make(map[string]bool, 4)
	for _, field := range []string{"column", "go_name", "kind", "name"} {
		child, exists := object.member(field)
		if !exists {
			continue
		}
		valid := child.kind == profileJSONString
		if valid {
			switch field {
			case "column":
				valid = profileValidFieldColumn(child.string, decoder)
			case "go_name":
				valid = profileValidFieldGoName(child.string, decoder)
			case "name":
				valid = profileValidFieldName(child.string, decoder)
			}
		}
		stringValid[field] = valid
		if !valid {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidIR, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir",
			))
		}
	}
	booleanValid := make(map[string]bool, 2)
	for _, field := range []string{"nullable", "primary_key"} {
		child, exists := object.member(field)
		if !exists {
			continue
		}
		booleanValid[field] = child.kind == profileJSONBoolean
		if !booleanValid[field] {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidIR, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir",
			))
		}
	}

	maxLengthValid := false
	var maxLength int64
	if maximum, exists := object.member("max_length"); exists {
		parsed, reason, valid := profileSignedInteger(maximum)
		if !valid {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidDocument, sourceID, pointer+"/max_length", app, name, operationIndex, reason,
			))
		} else if parsed < 0 || parsed > profileMaximumWireLength {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidDocument, sourceID, pointer+"/max_length", app, name, operationIndex, "out_of_range",
			))
		} else {
			maxLengthValid = true
			maxLength = parsed
		}
	}

	defaultValid := false
	var defaultValue *ir.ScalarDefault
	if defaultNode, exists := object.member("default"); exists {
		candidates = append(candidates, profileCollectDefaultCandidates(
			defaultNode, sourceID, pointer+"/default", app, name, operationIndex, decoder,
		)...)
		if decoded, valid := profileMaterializeDefaultJSON(defaultNode, decoder); valid {
			defaultValid = true
			defaultValue = decoded
		}
	}

	if stringValid["kind"] {
		kind, _ := object.member("kind")
		switch ir.FieldKind(kind.string) {
		case ir.FieldAuto:
			if defaultValid && defaultValue != nil {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir",
				))
			}
			if maxLengthValid && maxLength != 0 {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir",
				))
			}
			if booleanValid["nullable"] {
				nullable, _ := object.member("nullable")
				if nullable.boolean {
					candidates = append(candidates, profileSemanticFailureCandidate(
						definition.CodeInvalidIR, sourceID, pointer+"/nullable", app, name, operationIndex, "invalid_ir",
					))
				}
			}
			if booleanValid["primary_key"] {
				primaryKey, _ := object.member("primary_key")
				if !primaryKey.boolean {
					candidates = append(candidates, profileSemanticFailureCandidate(
						definition.CodeInvalidIR, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir",
					))
				}
			}
		case ir.FieldChar:
			if defaultValid && defaultValue != nil && defaultValue.Kind != ir.ScalarString {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir",
				))
			}
			if defaultValid && defaultValue != nil && defaultValue.Kind == ir.ScalarString && maxLengthValid && int64(utf8.RuneCountInString(defaultValue.String)) > maxLength {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir",
				))
			}
			if maxLengthValid && maxLength <= 0 {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir",
				))
			}
			if booleanValid["primary_key"] {
				primaryKey, _ := object.member("primary_key")
				if primaryKey.boolean {
					candidates = append(candidates, profileSemanticFailureCandidate(
						definition.CodeInvalidIR, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir",
					))
				}
			}
		case ir.FieldBoolean:
			if defaultValid && defaultValue != nil && defaultValue.Kind != ir.ScalarBoolean {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir",
				))
			}
			if maxLengthValid && maxLength != 0 {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir",
				))
			}
			if booleanValid["nullable"] {
				nullable, _ := object.member("nullable")
				if nullable.boolean {
					candidates = append(candidates, profileSemanticFailureCandidate(
						definition.CodeInvalidIR, sourceID, pointer+"/nullable", app, name, operationIndex, "invalid_ir",
					))
				}
			}
			if booleanValid["primary_key"] {
				primaryKey, _ := object.member("primary_key")
				if primaryKey.boolean {
					candidates = append(candidates, profileSemanticFailureCandidate(
						definition.CodeInvalidIR, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir",
					))
				}
			}
		case ir.FieldForeignKey:
			if decoder != profileDecoderRelation {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir",
				))
				break
			}
			if defaultValid && defaultValue != nil {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir",
				))
			}
			if maxLengthValid && maxLength != 0 {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir",
				))
			}
			if booleanValid["primary_key"] {
				primaryKey, _ := object.member("primary_key")
				if primaryKey.boolean {
					candidates = append(candidates, profileSemanticFailureCandidate(
						definition.CodeInvalidIR, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir",
					))
				}
			}
			if relation, exists := object.member("relation"); exists {
				candidates = append(candidates, profileCollectRelationCandidates(
					relation, object, sourceID, pointer+"/relation", app, name, operationIndex, decoder,
				)...)
			}
		default:
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidIR, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir",
			))
		}
	}
	return candidates
}

func profileCollectDefaultCandidates(
	value profileJSONValue,
	sourceID string,
	pointer string,
	app string,
	name string,
	operationIndex int,
	decoder profileDecoder,
) []*profileCandidateError {
	if value.kind == profileJSONNull {
		return nil
	}
	if value.kind != profileJSONObject {
		return []*profileCandidateError{profileSemanticFailureCandidate(
			definition.CodeInvalidIR, sourceID, pointer, app, name, operationIndex, "invalid_ir",
		)}
	}
	commonFields := []string{"boolean", "kind", "string"}
	if decoder == profileDecoderRelation {
		commonFields = append(commonFields, "integer")
	}
	candidates := profileSemanticUnknownCandidates(
		value, commonFields, sourceID, pointer, app, name, operationIndex, definition.CodeInvalidIR,
	)
	kind, exists := value.member("kind")
	if !exists || kind.kind != profileJSONString {
		return append(candidates, profileSemanticFailureCandidate(
			definition.CodeInvalidIR, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir",
		))
	}
	fields := []string(nil)
	payloadName := ""
	payloadKind := profileJSONNull
	switch kind.string {
	case string(ir.ScalarString):
		fields = []string{"kind", "string"}
		payloadName = "string"
		payloadKind = profileJSONString
	case string(ir.ScalarBoolean):
		fields = []string{"boolean", "kind"}
		payloadName = "boolean"
		payloadKind = profileJSONBoolean
	case string(ir.ScalarInteger):
		if decoder == profileDecoderRelation {
			fields = []string{"integer", "kind"}
			payloadName = "integer"
			payloadKind = profileJSONNumber
		}
	}
	if fields == nil {
		return append(candidates, profileSemanticFailureCandidate(
			definition.CodeInvalidIR, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir",
		))
	}
	object, _, faults := profileSemanticObjectCandidates(
		value, fields, sourceID, pointer, app, name, operationIndex, definition.CodeInvalidIR,
	)
	candidates = append(candidates, faults...)
	if child, present := object.member(payloadName); present {
		valid := child.kind == payloadKind
		if valid && payloadKind == profileJSONNumber {
			_, _, valid = profileSignedInteger(child)
		}
		if !valid {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidIR, sourceID, pointer+"/"+payloadName, app, name, operationIndex, "invalid_ir",
			))
		}
	}
	return candidates
}

func profileCollectRelationCandidates(
	value profileJSONValue,
	field profileJSONValue,
	sourceID string,
	pointer string,
	app string,
	name string,
	operationIndex int,
	decoder profileDecoder,
) []*profileCandidateError {
	object, objectOK, candidates := profileSemanticObjectCandidates(
		value,
		[]string{"cardinality", "on_delete", "reverse", "target"},
		sourceID,
		pointer,
		app,
		name,
		operationIndex,
		definition.CodeInvalidIR,
	)
	if !objectOK {
		return candidates
	}

	target, exists := object.member("target")
	if exists {
		targetObject, targetOK, faults := profileSemanticObjectCandidates(
			target,
			[]string{"app_label", "model_name"},
			sourceID,
			pointer+"/target",
			app,
			name,
			operationIndex,
			definition.CodeInvalidIR,
		)
		candidates = append(candidates, faults...)
		if targetOK {
			for _, targetField := range []string{"app_label", "model_name"} {
				child, present := targetObject.member(targetField)
				valid := present && child.kind == profileJSONString
				if valid {
					if targetField == "app_label" {
						valid = profileValidAppLabel(child.string, decoder)
					} else {
						valid = profileValidModelName(child.string, decoder)
					}
				}
				if present && !valid {
					candidates = append(candidates, profileSemanticFailureCandidate(
						definition.CodeInvalidIR, sourceID, pointer+"/target/"+targetField, app, name, operationIndex, "invalid_ir",
					))
				}
			}
		}
	}

	if cardinality, exists := object.member("cardinality"); exists {
		if cardinality.kind != profileJSONString || cardinality.string != string(ir.RelationManyToOne) {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidIR, sourceID, pointer+"/cardinality", app, name, operationIndex, "invalid_ir",
			))
		}
	}

	if reverse, exists := object.member("reverse"); exists {
		reverseObject, reverseOK, faults := profileSemanticObjectCandidates(
			reverse,
			[]string{"disabled", "name"},
			sourceID,
			pointer+"/reverse",
			app,
			name,
			operationIndex,
			definition.CodeInvalidIR,
		)
		candidates = append(candidates, faults...)
		if reverseOK {
			disabled, disabledExists := reverseObject.member("disabled")
			reverseName, nameExists := reverseObject.member("name")
			disabledValid := disabledExists && disabled.kind == profileJSONBoolean
			nameValid := nameExists && reverseName.kind == profileJSONString
			if disabledExists && !disabledValid {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/reverse/disabled", app, name, operationIndex, "invalid_ir",
				))
			}
			if nameExists && (!nameValid || (reverseName.string != "" && !profileValidFieldName(reverseName.string, decoder))) {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/reverse/name", app, name, operationIndex, "invalid_ir",
				))
			}
			if disabledValid && nameValid && (disabled.boolean == (reverseName.string != "")) {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/reverse", app, name, operationIndex, "invalid_ir",
				))
			}
		}
	}

	if onDelete, exists := object.member("on_delete"); exists {
		valid := onDelete.kind == profileJSONString &&
			(onDelete.string == string(ir.DeleteProtect) || onDelete.string == string(ir.DeleteSetNull))
		if !valid {
			candidates = append(candidates, profileSemanticFailureCandidate(
				definition.CodeInvalidIR, sourceID, pointer+"/on_delete", app, name, operationIndex, "invalid_ir",
			))
		} else if onDelete.string == string(ir.DeleteSetNull) {
			nullable, nullableExists := field.member("nullable")
			if nullableExists && nullable.kind == profileJSONBoolean && !nullable.boolean {
				candidates = append(candidates, profileSemanticFailureCandidate(
					definition.CodeInvalidIR, sourceID, pointer+"/on_delete", app, name, operationIndex, "invalid_ir",
				))
			}
		}
	}
	return candidates
}

func profileMaterializeDefaultJSON(value profileJSONValue, decoder profileDecoder) (*ir.ScalarDefault, bool) {
	if value.kind == profileJSONNull {
		return nil, true
	}
	if value.kind != profileJSONObject {
		return nil, false
	}
	kind, exists := value.member("kind")
	if !exists || kind.kind != profileJSONString {
		return nil, false
	}
	switch kind.string {
	case string(ir.ScalarString):
		payload, exists := value.member("string")
		if !exists || payload.kind != profileJSONString {
			return nil, false
		}
		return &ir.ScalarDefault{Kind: ir.ScalarString, String: payload.string}, true
	case string(ir.ScalarBoolean):
		payload, exists := value.member("boolean")
		if !exists || payload.kind != profileJSONBoolean {
			return nil, false
		}
		return &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: payload.boolean}, true
	case string(ir.ScalarInteger):
		if decoder != profileDecoderRelation {
			return nil, false
		}
		payload, exists := value.member("integer")
		if !exists {
			return nil, false
		}
		parsed, _, valid := profileSignedInteger(payload)
		if !valid {
			return nil, false
		}
		return &ir.ScalarDefault{Kind: ir.ScalarInteger, Integer: parsed}, true
	default:
		return nil, false
	}
}

func profileValidationAutoField() ir.Field {
	return ir.Field{
		Name:       "_godj_profile_loader_pk",
		GoName:     "GodjProfileLoaderPK",
		Column:     "_godj_profile_loader_pk",
		Kind:       ir.FieldAuto,
		PrimaryKey: true,
	}
}

func profileValidationModel() ir.Model {
	return ir.Model{
		Name:    "_godj_profile_loader_validation",
		GoName:  "GodjProfileLoaderValidation",
		DBTable: "_godj_profile_loader_validation",
		Fields:  []ir.Field{profileValidationAutoField()},
	}
}

func profileExactNormalizedSchema(schema ir.Schema) bool {
	normalized, err := ir.Normalize(schema)
	return err == nil && reflect.DeepEqual(normalized, schema)
}

func profileValidAppLabel(value string, decoder profileDecoder) bool {
	return profileExactNormalizedSchema(ir.Schema{
		FormatVersion: profileIRVersion(decoder),
		AppLabel:      value,
		Models:        []ir.Model{profileValidationModel()},
	})
}

func profileValidModelName(value string, decoder profileDecoder) bool {
	model := profileValidationModel()
	model.Name = value
	return profileExactNormalizedSchema(ir.Schema{
		FormatVersion: profileIRVersion(decoder),
		AppLabel:      "_godj_profile_loader_validation",
		Models:        []ir.Model{model},
	})
}

func profileValidModelGoName(value string, decoder profileDecoder) bool {
	model := profileValidationModel()
	model.GoName = value
	return profileExactNormalizedSchema(ir.Schema{
		FormatVersion: profileIRVersion(decoder),
		AppLabel:      "_godj_profile_loader_validation",
		Models:        []ir.Model{model},
	})
}

func profileValidModelTable(value string, decoder profileDecoder) bool {
	model := profileValidationModel()
	model.DBTable = value
	return profileExactNormalizedSchema(ir.Schema{
		FormatVersion: profileIRVersion(decoder),
		AppLabel:      "_godj_profile_loader_validation",
		Models:        []ir.Model{model},
	})
}

func profileValidFieldName(value string, decoder profileDecoder) bool {
	field := profileValidationAutoField()
	field.Name = value
	model := profileValidationModel()
	model.Fields = []ir.Field{field}
	return profileExactNormalizedSchema(ir.Schema{
		FormatVersion: profileIRVersion(decoder),
		AppLabel:      "_godj_profile_loader_validation",
		Models:        []ir.Model{model},
	})
}

func profileValidFieldGoName(value string, decoder profileDecoder) bool {
	field := profileValidationAutoField()
	field.GoName = value
	model := profileValidationModel()
	model.Fields = []ir.Field{field}
	return profileExactNormalizedSchema(ir.Schema{
		FormatVersion: profileIRVersion(decoder),
		AppLabel:      "_godj_profile_loader_validation",
		Models:        []ir.Model{model},
	})
}

func profileValidFieldColumn(value string, decoder profileDecoder) bool {
	field := profileValidationAutoField()
	field.Column = value
	model := profileValidationModel()
	model.Fields = []ir.Field{field}
	return profileExactNormalizedSchema(ir.Schema{
		FormatVersion: profileIRVersion(decoder),
		AppLabel:      "_godj_profile_loader_validation",
		Models:        []ir.Model{model},
	})
}

func profileLoadCandidateDecoded(sources []profileSource, report profileLoadReport) (profileSet, profileLoadReport, error) {
	snapshots := make([]profileSource, len(sources))
	for index := range sources {
		snapshots[index] = profileCloneSource(sources[index])
	}
	sort.Slice(snapshots, func(left, right int) bool {
		if snapshots[left].Definition.App != snapshots[right].Definition.App {
			return bytes.Compare([]byte(snapshots[left].Definition.App), []byte(snapshots[right].Definition.App)) < 0
		}
		if snapshots[left].Definition.Name != snapshots[right].Definition.Name {
			return bytes.Compare([]byte(snapshots[left].Definition.Name), []byte(snapshots[right].Definition.Name)) < 0
		}
		return bytes.Compare([]byte(snapshots[left].SourceID), []byte(snapshots[right].SourceID)) < 0
	})

	canonicalItems := make([]any, len(snapshots))
	canonicalDefinitions := make([]any, len(snapshots))
	provenanceSeals := make([]string, len(snapshots))
	definitions := make([]migrations.Migration, len(snapshots))
	allLegacy := true
	for index := range snapshots {
		decoder, _ := profileDispatch(snapshots[index].Profile)
		canonical, migration, failure := profileCanonicalDefinition(snapshots[index].Definition, decoder)
		if failure != nil {
			failure.SourceID = snapshots[index].SourceID
			return profileSet{}, profileReportWithFailure(report, failure), failure
		}
		canonicalDefinitions[index] = canonical
		canonicalItems[index] = map[string]any{
			"definition": canonical,
			"profile":    profileCompatibilityValue(snapshots[index].Profile),
		}
		provenanceSeal, err := profilePublishedProvenanceSeal(
			snapshots[index].SourceID,
			snapshots[index].Producer,
			snapshots[index].Profile,
			canonical,
		)
		if err != nil {
			return profileSet{}, report, err
		}
		provenanceSeals[index] = provenanceSeal
		allLegacy = allLegacy && snapshots[index].Profile == profileLegacy
		definitions[index] = migration
		report.OperationsDecoded += len(migration.Operations)
	}
	// This is the route's only graph construction. Legacy and relation
	// definitions, including edges that cross profile tuples, enter together.
	report.PlannerConstruction++
	if _, err := migrations.NewPlanner(definitions...); err != nil {
		failure := profileGraphFailure(err, snapshots)
		return profileSet{}, profileReportWithFailure(report, failure), failure
	}

	canonicalValue := map[string]any{"definitions": canonicalItems, "domain": profileMixedDigestDomain}
	if allLegacy {
		canonicalValue = map[string]any{
			"compatibility": profileCompatibilityValue(profileLegacy),
			"definitions":   canonicalDefinitions,
			"domain":        profileLegacyDigestDomain,
		}
	}
	canonical, err := profileCanonicalJSON(canonicalValue)
	if err != nil {
		return profileSet{}, report, err
	}
	sum := sha256.Sum256(canonical)
	published := make([]profilePublishedDefinition, len(snapshots))
	for index := range snapshots {
		published[index] = profilePublishedDefinition{
			SourceID:       snapshots[index].SourceID,
			Producer:       snapshots[index].Producer,
			Profile:        snapshots[index].Profile,
			Definition:     profileCloneDefinition(snapshots[index].Definition),
			provenanceSeal: provenanceSeals[index],
		}
	}
	report.DefinitionsPublished = len(published)
	report.SetsPublished = 1
	set := profileSet{
		canonical: append([]byte(nil), canonical...), digest: "sha256:" + hex.EncodeToString(sum[:]),
		definitions: published, hasLegacy: allLegacy,
	}
	if allLegacy {
		set.legacyDefinitions = profileCloneMigrations(definitions)
	}
	return set, report, nil
}

func profileGraphFailure(err error, sources []profileSource) *profileCandidateError {
	failure := &profileCandidateError{
		Category:       "migration_relation_profile_candidate_error",
		Code:           "invalid_graph",
		Stage:          "graph",
		Reason:         "planner_error",
		OperationIndex: -1,
	}
	var planning *migrations.PlanningError
	if !errors.As(err, &planning) {
		return failure
	}
	failure.Code = string(planning.Code)
	failure.Reason = string(planning.Code)
	members := planning.Members()
	selected := planning.Node
	if selected == (migrations.MigrationKey{}) {
		if len(members) != 0 {
			selected = members[0]
		}
	}
	failure.App = selected.App
	failure.Name = selected.Name
	if planning.Code == migrations.CodeInvalidNode || planning.Code == migrations.CodeDuplicateNode {
		failure.Pointer = "/migration"
	} else {
		failure.Pointer = "/migration/dependencies"
	}
	byKey := make(map[migrations.MigrationKey][]string)
	for _, source := range sources {
		key := migrations.MigrationKey{App: source.Definition.App, Name: source.Definition.Name}
		byKey[key] = append(byKey[key], source.SourceID)
	}
	for key := range byKey {
		sort.Slice(byKey[key], func(left, right int) bool {
			return bytes.Compare([]byte(byKey[key][left]), []byte(byKey[key][right])) < 0
		})
	}
	if sourceIDs := byKey[selected]; len(sourceIDs) != 0 {
		if planning.Node != (migrations.MigrationKey{}) {
			failure.SourceID = sourceIDs[len(sourceIDs)-1]
		} else {
			failure.SourceID = sourceIDs[0]
		}
	}
	keys := make([]migrations.MigrationKey, 0, len(members)+2)
	if planning.Node != (migrations.MigrationKey{}) {
		keys = append(keys, planning.Node)
	}
	if planning.Related != (migrations.MigrationKey{}) {
		keys = append(keys, planning.Related)
	}
	keys = append(keys, members...)
	seen := make(map[definition.GraphSource]struct{})
	for _, key := range keys {
		for _, sourceID := range byKey[key] {
			item := definition.GraphSource{Migration: key, SourceID: sourceID}
			if _, exists := seen[item]; exists {
				continue
			}
			seen[item] = struct{}{}
			failure.GraphSources = append(failure.GraphSources, item)
		}
	}
	sort.Slice(failure.GraphSources, func(left, right int) bool {
		if failure.GraphSources[left].Migration.App != failure.GraphSources[right].Migration.App {
			return failure.GraphSources[left].Migration.App < failure.GraphSources[right].Migration.App
		}
		if failure.GraphSources[left].Migration.Name != failure.GraphSources[right].Migration.Name {
			return failure.GraphSources[left].Migration.Name < failure.GraphSources[right].Migration.Name
		}
		return bytes.Compare([]byte(failure.GraphSources[left].SourceID), []byte(failure.GraphSources[right].SourceID)) < 0
	})
	return failure
}

func profileCanonicalDefinition(definitionValue profileDefinition, decoder profileDecoder) (map[string]any, migrations.Migration, *profileCandidateError) {
	canonicalDependencies := make([]profileIdentity, len(definitionValue.Dependencies))
	copy(canonicalDependencies, definitionValue.Dependencies)
	sort.Slice(canonicalDependencies, func(left, right int) bool {
		if canonicalDependencies[left].App != canonicalDependencies[right].App {
			return bytes.Compare([]byte(canonicalDependencies[left].App), []byte(canonicalDependencies[right].App)) < 0
		}
		return bytes.Compare([]byte(canonicalDependencies[left].Name), []byte(canonicalDependencies[right].Name)) < 0
	})
	canonicalDependencyValues := make([]any, len(canonicalDependencies))
	for index := range canonicalDependencies {
		canonicalDependencyValues[index] = map[string]any{"app": canonicalDependencies[index].App, "name": canonicalDependencies[index].Name}
	}
	dependencies := make([]migrations.MigrationKey, len(canonicalDependencies))
	for index := range canonicalDependencies {
		dependencies[index] = migrations.MigrationKey{App: canonicalDependencies[index].App, Name: canonicalDependencies[index].Name}
	}

	operations := make([]migrations.Operation, len(definitionValue.Operations))
	canonicalOperations := make([]any, len(definitionValue.Operations))
	for index := range definitionValue.Operations {
		canonical, operation, failure := profileCanonicalOperationValue(definitionValue.Operations[index], definitionValue.App, decoder)
		if failure != nil {
			return nil, migrations.Migration{}, failure
		}
		canonicalOperations[index] = canonical
		operations[index] = operation
	}
	migration := migrations.Migration{
		App:          definitionValue.App,
		Name:         definitionValue.Name,
		Dependencies: dependencies,
		Operations:   operations,
	}
	return map[string]any{
		"app":          definitionValue.App,
		"dependencies": canonicalDependencyValues,
		"name":         definitionValue.Name,
		"operations":   canonicalOperations,
	}, migration, nil
}

func profileCanonicalOperationValue(operation profileOperation, definitionApp string, decoder profileDecoder) (map[string]any, migrations.Operation, *profileCandidateError) {
	if operation.AppLabel != definitionApp {
		return nil, nil, profileSemanticFailure("operation_app_mismatch")
	}
	switch operation.Kind {
	case "create_model":
		if operation.Field != nil || operation.ModelName != "" {
			return nil, nil, profileSemanticFailure("create_model_sibling_arms_unsupported")
		}
		if operation.Model == nil {
			return nil, nil, profileSemanticFailure("create_model_missing_model")
		}
		model, fields, failure := profileModelIR(*operation.Model, operation.AppLabel, decoder)
		if failure != nil {
			return nil, nil, failure
		}
		return map[string]any{
			"app_label": operation.AppLabel,
			"kind":      operation.Kind,
			"model": map[string]any{
				"db_table": operation.Model.DBTable,
				"fields":   fields,
				"go_name":  operation.Model.GoName,
				"name":     operation.Model.Name,
			},
		}, migrations.CreateModel{AppLabel: operation.AppLabel, Model: model}, nil
	case "add_field":
		if operation.Model != nil {
			return nil, nil, profileSemanticFailure("add_field_sibling_arms_unsupported")
		}
		if operation.Field == nil || operation.ModelName == "" {
			return nil, nil, profileSemanticFailure("add_field_missing_arm")
		}
		field, canonical, failure := profileFieldIR(*operation.Field, decoder)
		if failure != nil {
			return nil, nil, failure
		}
		if !profileExactNormalizedAddField(operation.AppLabel, operation.ModelName, field, decoder) {
			return nil, nil, profileSemanticFailure("invalid_ir")
		}
		return map[string]any{
			"app_label":  operation.AppLabel,
			"field":      canonical,
			"kind":       operation.Kind,
			"model_name": operation.ModelName,
		}, migrations.AddField{AppLabel: operation.AppLabel, ModelName: operation.ModelName, Field: field.Clone()}, nil
	default:
		return nil, nil, profileSemanticFailure("unsupported_operation")
	}
}

func profileModelIR(model profileModel, app string, decoder profileDecoder) (ir.Model, []any, *profileCandidateError) {
	fields := make([]ir.Field, len(model.Fields))
	canonical := make([]any, len(model.Fields))
	for index := range model.Fields {
		field, value, failure := profileFieldIR(model.Fields[index], decoder)
		if failure != nil {
			return ir.Model{}, nil, failure
		}
		fields[index] = field
		canonical[index] = value
	}
	converted := ir.Model{Name: model.Name, GoName: model.GoName, DBTable: model.DBTable, Fields: fields}
	wrapper := ir.Schema{FormatVersion: profileIRVersion(decoder), AppLabel: app, Models: []ir.Model{converted.Clone()}}
	normalized, err := ir.Normalize(wrapper)
	if err != nil || !reflect.DeepEqual(normalized, wrapper) {
		return ir.Model{}, nil, profileSemanticFailure("invalid_ir")
	}
	return converted, canonical, nil
}

func profileFieldIR(field profileField, decoder profileDecoder) (ir.Field, map[string]any, *profileCandidateError) {
	if field.MaxLength < 0 || field.MaxLength > profileMaximumWireLength {
		return ir.Field{}, nil, profileSemanticFailure("max_length_out_of_range")
	}
	defaultValue, canonicalDefault, failure := profileDefaultIR(field.Default)
	if failure != nil {
		return ir.Field{}, nil, failure
	}
	converted := ir.Field{
		Name:       field.Name,
		GoName:     field.GoName,
		Column:     field.Column,
		Kind:       ir.FieldKind(field.Kind),
		PrimaryKey: field.PrimaryKey,
		Nullable:   field.Nullable,
		MaxLength:  int(field.MaxLength),
		Default:    defaultValue,
	}
	canonical := map[string]any{
		"column":      field.Column,
		"default":     canonicalDefault,
		"go_name":     field.GoName,
		"kind":        field.Kind,
		"max_length":  field.MaxLength,
		"name":        field.Name,
		"nullable":    field.Nullable,
		"primary_key": field.PrimaryKey,
	}
	if field.Relation == nil {
		if field.Kind == string(ir.FieldForeignKey) {
			return ir.Field{}, nil, profileSemanticFailure("relation_metadata_required")
		}
		return converted, canonical, nil
	}
	if decoder != profileDecoderRelation {
		return ir.Field{}, nil, profileSemanticFailure("relation_profile_required")
	}
	if field.Kind != string(ir.FieldForeignKey) {
		return ir.Field{}, nil, profileSemanticFailure("relation_kind_mismatch")
	}
	converted.Relation = &ir.ForeignKeyRelation{
		Target: ir.ModelIdentity{
			AppLabel:  field.Relation.Target.App,
			ModelName: field.Relation.Target.Model,
		},
		Cardinality: ir.RelationCardinality(field.Relation.Cardinality),
		Reverse: ir.ReverseRelation{
			Name:     field.Relation.Reverse.Name,
			Disabled: field.Relation.Reverse.Disabled,
		},
		OnDelete: ir.DeletePolicy(field.Relation.OnDelete),
	}
	canonical["relation"] = map[string]any{
		"cardinality": field.Relation.Cardinality,
		"on_delete":   field.Relation.OnDelete,
		"reverse": map[string]any{
			"disabled": field.Relation.Reverse.Disabled,
			"name":     field.Relation.Reverse.Name,
		},
		"target": map[string]any{
			"app_label":  field.Relation.Target.App,
			"model_name": field.Relation.Target.Model,
		},
	}
	return converted, canonical, nil
}

func profileDefaultIR(value *profileDefault) (*ir.ScalarDefault, any, *profileCandidateError) {
	if value == nil {
		return nil, nil, nil
	}
	switch value.Kind {
	case string(ir.ScalarString):
		if value.String == nil || value.Boolean != nil || value.Integer != nil {
			return nil, nil, profileSemanticFailure("invalid_scalar_default_union")
		}
		converted := &ir.ScalarDefault{Kind: ir.ScalarString, String: *value.String}
		return converted, map[string]any{"kind": string(ir.ScalarString), "string": *value.String}, nil
	case string(ir.ScalarBoolean):
		if value.Boolean == nil || value.String != nil || value.Integer != nil {
			return nil, nil, profileSemanticFailure("invalid_scalar_default_union")
		}
		converted := &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: *value.Boolean}
		return converted, map[string]any{"boolean": *value.Boolean, "kind": string(ir.ScalarBoolean)}, nil
	case string(ir.ScalarInteger):
		if value.Integer == nil || value.String != nil || value.Boolean != nil {
			return nil, nil, profileSemanticFailure("invalid_scalar_default_union")
		}
		converted := &ir.ScalarDefault{Kind: ir.ScalarInteger, Integer: *value.Integer}
		return converted, map[string]any{"integer": *value.Integer, "kind": string(ir.ScalarInteger)}, nil
	default:
		return nil, nil, profileSemanticFailure("invalid_scalar_default_union")
	}
}

func profileExactNormalizedAddField(app, modelName string, field ir.Field, decoder profileDecoder) bool {
	synthetic := ir.Field{Name: "_godj_profile_pk", GoName: "GodjProfilePK", Column: "_godj_profile_pk", Kind: ir.FieldAuto, PrimaryKey: true}
	for field.Name == synthetic.Name || field.GoName == synthetic.GoName || field.Column == synthetic.Column {
		synthetic.Name += "_"
		synthetic.GoName += "X"
		synthetic.Column += "_"
	}
	wrapper := ir.Schema{
		FormatVersion: profileIRVersion(decoder),
		AppLabel:      app,
		Models: []ir.Model{{
			Name:    modelName,
			GoName:  "GodjProfileValidation",
			DBTable: "_godj_profile_validation",
			Fields:  []ir.Field{synthetic, field.Clone()},
		}},
	}
	normalized, err := ir.Normalize(wrapper)
	return err == nil && reflect.DeepEqual(normalized, wrapper)
}

func profileIRVersion(decoder profileDecoder) int {
	if decoder == profileDecoderRelation {
		return ir.RelationFormatVersion
	}
	return ir.FormatVersion
}

func profileSemanticFailure(reason string) *profileCandidateError {
	return &profileCandidateError{
		Category: "migration_relation_profile_candidate_error",
		Code:     "invalid_definition",
		Stage:    "semantic",
		Reason:   reason,
	}
}

func profileCoordinateRank(value string) int {
	switch value {
	case "definition_format":
		return 0
	case "loader_abi":
		return 1
	case "operation_codec":
		return 2
	case "schema_ir":
		return 3
	default:
		return 4
	}
}

func profileCompatibilityValue(value profileCompatibility) map[string]any {
	return map[string]any{
		"definition_format": value.DefinitionFormat,
		"loader_abi":        value.LoaderABI,
		"operation_codec":   value.OperationCodec,
		"schema_ir":         value.SchemaIR,
	}
}

func profileCanonicalJSON(value any) ([]byte, error) {
	return profileAppendCanonical(nil, value)
}

// profilePublishedProvenanceSeal binds loader-owned provenance to one exact
// profile/definition pair without adding SourceID or Producer to the semantic
// migration-set digest. State handoffs validate this private seal against the
// loader set's internal records before they can be minted.
func profilePublishedProvenanceSeal(
	sourceID string,
	producer profileProducer,
	profile profileCompatibility,
	canonicalDefinition map[string]any,
) (string, error) {
	canonical, err := profileCanonicalJSON(map[string]any{
		"definition": canonicalDefinition,
		"domain":     "godj:migration-loader-published-definition:v1",
		"producer": map[string]any{
			"name": producer.Name, "version": producer.Version,
		},
		"profile":   profileCompatibilityValue(profile),
		"source_id": sourceID,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func profileAppendCanonical(output []byte, value any) ([]byte, error) {
	switch current := value.(type) {
	case nil:
		return append(output, "null"...), nil
	case bool:
		return strconv.AppendBool(output, current), nil
	case int:
		return strconv.AppendInt(output, int64(current), 10), nil
	case int64:
		return strconv.AppendInt(output, current, 10), nil
	case string:
		return profileAppendCanonicalString(output, current)
	case []any:
		output = append(output, '[')
		for index := range current {
			if index != 0 {
				output = append(output, ',')
			}
			var err error
			output, err = profileAppendCanonical(output, current[index])
			if err != nil {
				return nil, err
			}
		}
		return append(output, ']'), nil
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output = append(output, '{')
		for index, key := range keys {
			if index != 0 {
				output = append(output, ',')
			}
			var err error
			output, err = profileAppendCanonicalString(output, key)
			if err != nil {
				return nil, err
			}
			output = append(output, ':')
			output, err = profileAppendCanonical(output, current[key])
			if err != nil {
				return nil, err
			}
		}
		return append(output, '}'), nil
	default:
		return nil, fmt.Errorf("unsupported profile canonical value %T", value)
	}
}

// This is the existing v1 canonical string subset, reused for the new v2
// candidate domain. It deliberately does not HTML/JavaScript-escape <, >, &,
// U+2028 or U+2029.
func profileAppendCanonicalString(output []byte, value string) ([]byte, error) {
	if !utf8.ValidString(value) {
		return nil, errors.New("canonical string is not valid UTF-8")
	}
	const hexadecimal = "0123456789abcdef"
	output = append(output, '"')
	for len(value) != 0 {
		current, size := utf8.DecodeRuneInString(value)
		switch current {
		case '"':
			output = append(output, '\\', '"')
		case '\\':
			output = append(output, '\\', '\\')
		case '\b':
			output = append(output, '\\', 'b')
		case '\t':
			output = append(output, '\\', 't')
		case '\n':
			output = append(output, '\\', 'n')
		case '\f':
			output = append(output, '\\', 'f')
		case '\r':
			output = append(output, '\\', 'r')
		default:
			if current < 0x20 {
				output = append(output, '\\', 'u', '0', '0', hexadecimal[byte(current)>>4], hexadecimal[byte(current)&0x0f])
			} else {
				output = append(output, value[:size]...)
			}
		}
		value = value[size:]
	}
	return append(output, '"'), nil
}

func profileDefinitionFromMigration(value migrations.Migration) (profileDefinition, error) {
	converted := profileDefinition{
		App:          value.App,
		Name:         value.Name,
		Dependencies: make([]profileIdentity, len(value.Dependencies)),
		Operations:   make([]profileOperation, len(value.Operations)),
	}
	for index := range value.Dependencies {
		converted.Dependencies[index] = profileIdentity{App: value.Dependencies[index].App, Name: value.Dependencies[index].Name}
	}
	for index, operation := range value.Operations {
		switch current := operation.(type) {
		case migrations.CreateModel:
			model := profileModelFromIR(current.Model)
			converted.Operations[index] = profileOperation{AppLabel: current.AppLabel, Kind: "create_model", Model: &model}
		case *migrations.CreateModel:
			if current == nil {
				return profileDefinition{}, errors.New("legacy product Set contains nil CreateModel")
			}
			model := profileModelFromIR(current.Model)
			converted.Operations[index] = profileOperation{AppLabel: current.AppLabel, Kind: "create_model", Model: &model}
		case migrations.AddField:
			field := profileFieldFromIR(current.Field)
			converted.Operations[index] = profileOperation{AppLabel: current.AppLabel, Kind: "add_field", ModelName: current.ModelName, Field: &field}
		case *migrations.AddField:
			if current == nil {
				return profileDefinition{}, errors.New("legacy product Set contains nil AddField")
			}
			field := profileFieldFromIR(current.Field)
			converted.Operations[index] = profileOperation{AppLabel: current.AppLabel, Kind: "add_field", ModelName: current.ModelName, Field: &field}
		default:
			return profileDefinition{}, fmt.Errorf("legacy product Set contains unsupported operation %T", operation)
		}
	}
	return converted, nil
}

func profileModelFromIR(value ir.Model) profileModel {
	converted := profileModel{DBTable: value.DBTable, GoName: value.GoName, Name: value.Name, Fields: make([]profileField, len(value.Fields))}
	for index := range value.Fields {
		converted.Fields[index] = profileFieldFromIR(value.Fields[index])
	}
	return converted
}

func profileFieldFromIR(value ir.Field) profileField {
	converted := profileField{
		Column: value.Column, GoName: value.GoName, Kind: string(value.Kind), MaxLength: int64(value.MaxLength),
		Name: value.Name, Nullable: value.Nullable, PrimaryKey: value.PrimaryKey,
	}
	if value.Default != nil {
		converted.Default = &profileDefault{Kind: string(value.Default.Kind)}
		switch value.Default.Kind {
		case ir.ScalarString:
			copy := value.Default.String
			converted.Default.String = &copy
		case ir.ScalarBoolean:
			copy := value.Default.Boolean
			converted.Default.Boolean = &copy
		case ir.ScalarInteger:
			copy := value.Default.Integer
			converted.Default.Integer = &copy
		}
	}
	if value.Relation != nil {
		converted.Relation = &profileRelation{
			Target:      profileTarget{App: value.Relation.Target.AppLabel, Model: value.Relation.Target.ModelName},
			Cardinality: string(value.Relation.Cardinality),
			Reverse:     profileReverse{Name: value.Relation.Reverse.Name, Disabled: value.Relation.Reverse.Disabled},
			OnDelete:    string(value.Relation.OnDelete),
		}
	}
	return converted
}

func profileCloneSource(value profileSource) profileSource {
	return profileSource{
		SourceID:   value.SourceID,
		Producer:   value.Producer,
		Profile:    value.Profile,
		Definition: profileCloneDefinition(value.Definition),
	}
}

func profileClonePublishedDefinition(value profilePublishedDefinition) profilePublishedDefinition {
	return profilePublishedDefinition{
		SourceID: value.SourceID, Producer: value.Producer, Profile: value.Profile,
		Definition: profileCloneDefinition(value.Definition), provenanceSeal: value.provenanceSeal,
	}
}

func profileCloneMigrations(values []migrations.Migration) []migrations.Migration {
	cloned := make([]migrations.Migration, len(values))
	for index := range values {
		cloned[index] = migrations.Migration{
			App: values[index].App, Name: values[index].Name,
			Dependencies: make([]migrations.MigrationKey, len(values[index].Dependencies)),
			Operations:   make([]migrations.Operation, len(values[index].Operations)),
		}
		copy(cloned[index].Dependencies, values[index].Dependencies)
		for operationIndex, operation := range values[index].Operations {
			switch current := operation.(type) {
			case migrations.CreateModel:
				cloned[index].Operations[operationIndex] = migrations.CreateModel{AppLabel: current.AppLabel, Model: current.Model.Clone()}
			case *migrations.CreateModel:
				if current != nil {
					cloned[index].Operations[operationIndex] = migrations.CreateModel{AppLabel: current.AppLabel, Model: current.Model.Clone()}
				}
			case migrations.AddField:
				cloned[index].Operations[operationIndex] = migrations.AddField{AppLabel: current.AppLabel, ModelName: current.ModelName, Field: current.Field.Clone()}
			case *migrations.AddField:
				if current != nil {
					cloned[index].Operations[operationIndex] = migrations.AddField{AppLabel: current.AppLabel, ModelName: current.ModelName, Field: current.Field.Clone()}
				}
			default:
				cloned[index].Operations[operationIndex] = operation
			}
		}
	}
	return cloned
}

func profileCloneDefinition(value profileDefinition) profileDefinition {
	clone := profileDefinition{
		App:          value.App,
		Dependencies: make([]profileIdentity, len(value.Dependencies)),
		Name:         value.Name,
		Operations:   make([]profileOperation, len(value.Operations)),
	}
	copy(clone.Dependencies, value.Dependencies)
	for index := range value.Operations {
		clone.Operations[index] = profileCloneOperation(value.Operations[index])
	}
	return clone
}

func profileCloneOperation(value profileOperation) profileOperation {
	clone := value
	if value.Field != nil {
		field := profileCloneField(*value.Field)
		clone.Field = &field
	}
	if value.Model != nil {
		model := profileModel{DBTable: value.Model.DBTable, GoName: value.Model.GoName, Name: value.Model.Name, Fields: make([]profileField, len(value.Model.Fields))}
		for index := range value.Model.Fields {
			model.Fields[index] = profileCloneField(value.Model.Fields[index])
		}
		clone.Model = &model
	}
	return clone
}

func profileCloneField(value profileField) profileField {
	clone := value
	if value.Default != nil {
		defaultValue := *value.Default
		if value.Default.Boolean != nil {
			copy := *value.Default.Boolean
			defaultValue.Boolean = &copy
		}
		if value.Default.Integer != nil {
			copy := *value.Default.Integer
			defaultValue.Integer = &copy
		}
		if value.Default.String != nil {
			copy := *value.Default.String
			defaultValue.String = &copy
		}
		clone.Default = &defaultValue
	}
	if value.Relation != nil {
		relation := *value.Relation
		clone.Relation = &relation
	}
	return clone
}
