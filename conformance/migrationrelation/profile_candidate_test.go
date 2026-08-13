package migrationrelation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

// Everything in this file is a test-only feasibility candidate. The names,
// limits, wire values, and error shape are not a public API proposal.

const (
	profileMixedDigestDomain = "godj:migration-definition-set:v2"
	profileEmptyDigest       = definition.EmptySetDigest

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
	Category string
	Code     string
	Stage    string
	Reason   string
	SourceID string
	Pointer  string
	Limit    string
	Maximum  int
	Actual   int
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
	Column      string           `json:"column"`
	Default     *profileDefault  `json:"default"`
	GoName      string           `json:"go_name"`
	Kind        string           `json:"kind"`
	MaxLength   int              `json:"max_length"`
	Name        string           `json:"name"`
	Nullable    bool             `json:"nullable"`
	PrimaryKey  bool             `json:"primary_key"`
	Relation    *profileRelation `json:"relation,omitempty"`
	TargetField string           `json:"target_field,omitempty"`
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
	Profile    profileCompatibility
	Definition profileDefinition
}

type profileSet struct {
	canonical   []byte
	digest      string
	definitions []profilePublishedDefinition
	legacy      definition.Set
	hasLegacy   bool
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

// profileLegacyDefinitions intentionally returns the product Set's snapshots,
// not a candidate re-decode. It is populated only when the entire raw batch was
// accepted by definition.Load as the legacy profile.
func (s profileSet) profileLegacyDefinitions() []migrations.Migration {
	if !s.hasLegacy {
		return nil
	}
	return s.legacy.Definitions()
}

type profileLoadReport struct {
	DocumentsReceived    int
	ProfilesAccepted     int
	DefinitionsPublished int
	SetsPublished        int
}

type profileResourceCandidate struct {
	rank           int
	sourceID       string
	app            string
	name           string
	operationIndex int
	limit          string
	maximum        int
	actual         int
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
		Category: "migration_relation_profile_candidate_error",
		Code:     "compatibility_dispatch_invariant",
		Stage:    "compatibility",
		Reason:   "tuple_matched_no_supported_profile",
		Pointer:  "/compatibility",
	}
}

func profileCompatibilityFailure(coordinate string) *profileCandidateError {
	return &profileCandidateError{
		Category: "migration_relation_profile_candidate_error",
		Code:     coordinate + "_incompatible",
		Stage:    "compatibility",
		Reason:   coordinate,
		Pointer:  "/compatibility/" + coordinate,
	}
}

func profileHybridFailure(coordinate string) *profileCandidateError {
	return &profileCandidateError{
		Category: "migration_relation_profile_candidate_error",
		Code:     "hybrid_profile_incompatible",
		Stage:    "compatibility",
		Reason:   coordinate,
		Pointer:  "/compatibility/" + coordinate,
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

// profileLoadRaw deliberately calls the existing product loader first. A
// successful legacy-only batch publishes the exact product Set and digest.
// For relation/mixed input, the product loader still owns raw source limits,
// strict envelope checks, duplicate keys, framing, depth and JSON value caps;
// only its expected compatibility rejection opens the v2 candidate decoder.
func profileLoadRaw(sources ...definition.Source) (profileSet, profileLoadReport, error) {
	// The product loader runs before the candidate allocates a second document
	// copy, so its source/document/batch limits remain the allocation boundary.
	legacySet, legacyReport, legacyErr := definition.Load(sources...)
	if legacyErr == nil {
		return profileLegacyProductSet(legacySet, legacyReport)
	}
	var sourceError *definition.Error
	if !errors.As(legacyErr, &sourceError) || sourceError.Context().Stage != "compatibility" {
		return profileSet{}, profileReportFromProduct(legacyReport), profileMapProductError(legacyErr)
	}
	snapshots := make([]definition.Source, len(sources))
	for index := range sources {
		snapshots[index] = definition.Source{
			SourceID: sources[index].SourceID,
			Document: append([]byte(nil), sources[index].Document...),
		}
	}

	report := profileLoadReport{DocumentsReceived: len(snapshots)}
	profiles := make([]profileCompatibility, len(snapshots))
	compatibilityFailures := make([]*profileCandidateError, 0)
	for index := range snapshots {
		profile, err := profileRawCompatibility(snapshots[index].Document)
		if err != nil {
			return profileSet{}, report, profileRawSemanticError(snapshots[index].SourceID, "invalid_compatibility_shape")
		}
		profiles[index] = profile
		_, failure := profileDispatch(profile)
		if failure != nil {
			failure.SourceID = snapshots[index].SourceID
			compatibilityFailures = append(compatibilityFailures, failure)
			continue
		}
		report.ProfilesAccepted++
	}
	if len(compatibilityFailures) != 0 {
		sort.Slice(compatibilityFailures, func(left, right int) bool {
			leftRank := profileCoordinateRank(compatibilityFailures[left].Reason)
			rightRank := profileCoordinateRank(compatibilityFailures[right].Reason)
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			return bytes.Compare([]byte(compatibilityFailures[left].SourceID), []byte(compatibilityFailures[right].SourceID)) < 0
		})
		return profileSet{}, report, compatibilityFailures[0]
	}

	if failure := profileRawResourceFailure(snapshots); failure != nil {
		return profileSet{}, report, failure
	}

	decoded := make([]profileSource, 0, len(snapshots))
	decodeFailures := make([]*profileCandidateError, 0)
	for index := range snapshots {
		source, failure := profileStrictDecodeSource(snapshots[index], profiles[index])
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
		return profileSet{}, report, decodeFailures[0]
	}
	return profileLoadRelationDecoded(decoded, snapshots, report)
}

func profileLegacyProductSet(set definition.Set, report definition.LoadReport) (profileSet, profileLoadReport, error) {
	definitions := set.Definitions()
	published := make([]profilePublishedDefinition, len(definitions))
	for index := range definitions {
		converted, err := profileDefinitionFromMigration(definitions[index])
		if err != nil {
			return profileSet{}, profileReportFromProduct(report), err
		}
		published[index] = profilePublishedDefinition{Profile: profileLegacy, Definition: converted}
	}
	return profileSet{
		digest:      set.Digest(),
		definitions: published,
		legacy:      set,
		hasLegacy:   true,
	}, profileReportFromProduct(report), nil
}

func profileReportFromProduct(report definition.LoadReport) profileLoadReport {
	return profileLoadReport{
		DocumentsReceived:    report.DocumentsReceived,
		ProfilesAccepted:     report.HeadersValidated,
		DefinitionsPublished: report.DefinitionsPublished,
		SetsPublished:        report.DefinitionSetsPublished,
	}
}

func profileMapProductError(err error) error {
	var sourceError *definition.Error
	if !errors.As(err, &sourceError) {
		return err
	}
	context := sourceError.Context()
	return &profileCandidateError{
		Category: sourceError.Category,
		Code:     string(sourceError.Code),
		Stage:    context.Stage,
		Reason:   context.Reason,
		SourceID: context.SourceID,
		Pointer:  context.JSONPointer,
		Limit:    context.Limit,
		Maximum:  profileUintToInt(context.Maximum),
		Actual:   profileUintToInt(context.Actual),
	}
}

func profileUintToInt(value uint64) int {
	maximum := uint64(^uint(0) >> 1)
	if value > maximum {
		return int(maximum)
	}
	return int(value)
}

func profileRawCompatibility(document []byte) (profileCompatibility, error) {
	var envelope struct {
		Compatibility profileCompatibility `json:"compatibility"`
	}
	if err := json.Unmarshal(document, &envelope); err != nil {
		return profileCompatibility{}, err
	}
	return envelope.Compatibility, nil
}

func profileStrictDecodeSource(source definition.Source, profile profileCompatibility) (profileSource, *profileCandidateError) {
	if reason := profileValidateRawShape(source.Document); reason != "" {
		return profileSource{}, profileRawSemanticError(source.SourceID, reason)
	}
	decoder := json.NewDecoder(bytes.NewReader(source.Document))
	decoder.DisallowUnknownFields()
	var document profileWireDocument
	if err := decoder.Decode(&document); err != nil {
		return profileSource{}, profileRawSemanticError(source.SourceID, "strict_decode_failed")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return profileSource{}, profileRawSemanticError(source.SourceID, "trailing_value")
	}
	if document.Compatibility != profile {
		return profileSource{}, profileRawSemanticError(source.SourceID, "profile_dispatch_mismatch")
	}
	return profileSource{
		SourceID:   source.SourceID,
		Producer:   document.Producer,
		Profile:    profile,
		Definition: document.Migration,
	}, nil
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

func profileValidateRawShape(document []byte) string {
	root, ok := profileRawObject(document, "compatibility", "migration", "producer")
	if !ok {
		return "invalid_envelope_shape"
	}
	if _, ok := profileRawObject(root["compatibility"], "definition_format", "loader_abi", "operation_codec", "schema_ir"); !ok {
		return "invalid_compatibility_shape"
	}
	if _, ok := profileRawObject(root["producer"], "name", "version"); !ok {
		return "invalid_producer_shape"
	}
	migration, ok := profileRawObject(root["migration"], "app", "dependencies", "name", "operations")
	if !ok {
		return "invalid_migration_shape"
	}
	var dependencies []json.RawMessage
	if err := json.Unmarshal(migration["dependencies"], &dependencies); err != nil {
		return "invalid_dependencies_shape"
	}
	for _, dependency := range dependencies {
		if _, ok := profileRawObject(dependency, "app", "name"); !ok {
			return "invalid_dependency_shape"
		}
	}
	var operations []json.RawMessage
	if err := json.Unmarshal(migration["operations"], &operations); err != nil {
		return "invalid_operations_shape"
	}
	for _, operationRaw := range operations {
		var header struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(operationRaw, &header); err != nil {
			return "invalid_operation_shape"
		}
		var operation map[string]json.RawMessage
		switch header.Kind {
		case "create_model":
			operation, ok = profileRawObject(operationRaw, "app_label", "kind", "model")
			if !ok {
				return "invalid_create_model_shape"
			}
			model, modelOK := profileRawObject(operation["model"], "db_table", "fields", "go_name", "name")
			if !modelOK {
				return "invalid_model_shape"
			}
			var fields []json.RawMessage
			if err := json.Unmarshal(model["fields"], &fields); err != nil {
				return "invalid_fields_shape"
			}
			for _, field := range fields {
				if reason := profileValidateRawFieldShape(field); reason != "" {
					return reason
				}
			}
		case "add_field":
			operation, ok = profileRawObject(operationRaw, "app_label", "field", "kind", "model_name")
			if !ok {
				return "invalid_add_field_shape"
			}
			if reason := profileValidateRawFieldShape(operation["field"]); reason != "" {
				return reason
			}
		default:
			return "unsupported_operation"
		}
	}
	return ""
}

func profileValidateRawFieldShape(fieldRaw json.RawMessage) string {
	var header struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(fieldRaw, &header); err != nil {
		return "invalid_field_shape"
	}
	fields := []string{"column", "default", "go_name", "kind", "max_length", "name", "nullable", "primary_key"}
	if header.Kind == string(ir.FieldForeignKey) {
		fields = append(fields, "relation", "target_field")
	}
	field, ok := profileRawObject(fieldRaw, fields...)
	if !ok {
		return "invalid_field_shape"
	}
	if defaultRaw := field["default"]; !bytes.Equal(bytes.TrimSpace(defaultRaw), []byte("null")) {
		var header struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(defaultRaw, &header); err != nil {
			return "invalid_default_shape"
		}
		var defaultFields []string
		switch header.Kind {
		case string(ir.ScalarString):
			defaultFields = []string{"kind", "string"}
		case string(ir.ScalarBoolean):
			defaultFields = []string{"boolean", "kind"}
		case string(ir.ScalarInteger):
			defaultFields = []string{"integer", "kind"}
		default:
			return "invalid_default_shape"
		}
		if _, ok := profileRawObject(defaultRaw, defaultFields...); !ok {
			return "invalid_default_shape"
		}
	}
	if header.Kind != string(ir.FieldForeignKey) {
		return ""
	}
	relation, ok := profileRawObject(field["relation"], "cardinality", "on_delete", "reverse", "target")
	if !ok {
		return "invalid_relation_shape"
	}
	if _, ok := profileRawObject(relation["target"], "app_label", "model_name"); !ok {
		return "invalid_relation_target_shape"
	}
	if _, ok := profileRawObject(relation["reverse"], "disabled", "name"); !ok {
		return "invalid_relation_reverse_shape"
	}
	return ""
}

func profileRawObject(raw json.RawMessage, fields ...string) (map[string]json.RawMessage, bool) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil || len(value) != len(fields) {
		return nil, false
	}
	for _, field := range fields {
		if _, exists := value[field]; !exists {
			return nil, false
		}
	}
	return value, true
}

func profileRawResourceFailure(sources []definition.Source) *profileCandidateError {
	candidates := make([]profileResourceCandidate, 0)
	for _, source := range sources {
		root, ok := profileRawObject(source.Document, "compatibility", "migration", "producer")
		if !ok {
			continue
		}
		migration, ok := profileRawObject(root["migration"], "app", "dependencies", "name", "operations")
		if !ok {
			continue
		}
		var identity struct {
			App  string `json:"app"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(root["migration"], &identity)
		var dependencies []json.RawMessage
		if json.Unmarshal(migration["dependencies"], &dependencies) == nil && len(dependencies) > profileMaxDependencies {
			candidates = append(candidates, profileResourceCandidate{
				rank: 0, sourceID: source.SourceID, app: identity.App, name: identity.Name,
				operationIndex: -1, limit: "dependencies", maximum: profileMaxDependencies, actual: len(dependencies),
			})
		}
		var operations []json.RawMessage
		if json.Unmarshal(migration["operations"], &operations) != nil {
			continue
		}
		if len(operations) > profileMaxOperations {
			candidates = append(candidates, profileResourceCandidate{
				rank: 1, sourceID: source.SourceID, app: identity.App, name: identity.Name,
				operationIndex: -1, limit: "operations", maximum: profileMaxOperations, actual: len(operations),
			})
		}
		for operationIndex, operation := range operations {
			var value struct {
				Kind  string `json:"kind"`
				Model *struct {
					Fields []json.RawMessage `json:"fields"`
				} `json:"model"`
			}
			if json.Unmarshal(operation, &value) != nil || value.Kind != "create_model" || value.Model == nil {
				continue
			}
			if len(value.Model.Fields) > profileMaxFields {
				candidates = append(candidates, profileResourceCandidate{
					rank: 2, sourceID: source.SourceID, app: identity.App, name: identity.Name,
					operationIndex: operationIndex, limit: "fields", maximum: profileMaxFields, actual: len(value.Model.Fields),
				})
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].rank != candidates[right].rank {
			return candidates[left].rank < candidates[right].rank
		}
		if candidates[left].app != candidates[right].app {
			return bytes.Compare([]byte(candidates[left].app), []byte(candidates[right].app)) < 0
		}
		if candidates[left].name != candidates[right].name {
			return bytes.Compare([]byte(candidates[left].name), []byte(candidates[right].name)) < 0
		}
		if candidates[left].sourceID != candidates[right].sourceID {
			return bytes.Compare([]byte(candidates[left].sourceID), []byte(candidates[right].sourceID)) < 0
		}
		return candidates[left].operationIndex < candidates[right].operationIndex
	})
	winner := candidates[0]
	return profileResourceFailure(winner.sourceID, winner.limit, winner.maximum, winner.actual)
}

func profileLoadRelationDecoded(
	sources []profileSource,
	rawSources []definition.Source,
	report profileLoadReport,
) (profileSet, profileLoadReport, error) {
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
	definitions := make([]migrations.Migration, len(snapshots))
	for index := range snapshots {
		decoder, _ := profileDispatch(snapshots[index].Profile)
		canonical, migration, failure := profileCanonicalDefinition(snapshots[index].Definition, decoder)
		if failure != nil {
			failure.SourceID = snapshots[index].SourceID
			return profileSet{}, report, failure
		}
		canonicalItems[index] = map[string]any{
			"definition": canonical,
			"profile":    profileCompatibilityValue(snapshots[index].Profile),
		}
		definitions[index] = migration
	}
	if failure := profileValidateLegacyProductSubset(rawSources, snapshots, definitions); failure != nil {
		return profileSet{}, report, failure
	}
	if _, err := migrations.NewPlanner(definitions...); err != nil {
		return profileSet{}, report, profileGraphFailure(err, snapshots)
	}

	canonical, err := profileCanonicalJSON(map[string]any{
		"definitions": canonicalItems,
		"domain":      profileMixedDigestDomain,
	})
	if err != nil {
		return profileSet{}, report, err
	}
	sum := sha256.Sum256(canonical)
	published := make([]profilePublishedDefinition, len(snapshots))
	for index := range snapshots {
		published[index] = profilePublishedDefinition{
			Profile:    snapshots[index].Profile,
			Definition: profileCloneDefinition(snapshots[index].Definition),
		}
	}
	report.DefinitionsPublished = len(published)
	report.SetsPublished = 1
	return profileSet{
		canonical:   append([]byte(nil), canonical...),
		digest:      "sha256:" + hex.EncodeToString(sum[:]),
		definitions: published,
	}, report, nil
}

// A mixed candidate must not silently reinterpret legacy documents. Until the
// product loader exposes a composable per-document decode API, the exact legacy
// subset's original raw bytes are independently passed through definition.Load
// and compared to the candidate conversion. Re-marshalling an already-decoded
// value here would not prove exact product wire semantics. If that raw subset
// depends on a relation-profile document, the current product API cannot decode
// it in isolation; Phase B reports that integration blocker instead of claiming
// general mixed-loader feasibility.
func profileValidateLegacyProductSubset(
	rawSources []definition.Source,
	sources []profileSource,
	definitions []migrations.Migration,
) *profileCandidateError {
	rawByID := make(map[string]definition.Source, len(rawSources))
	for _, source := range rawSources {
		rawByID[source.SourceID] = definition.Source{
			SourceID: source.SourceID,
			Document: append([]byte(nil), source.Document...),
		}
	}
	raw := make([]definition.Source, 0)
	want := make(map[migrations.MigrationKey]migrations.Migration)
	firstSourceID := ""
	for index, source := range sources {
		if source.Profile != profileLegacy {
			continue
		}
		if firstSourceID == "" {
			firstSourceID = source.SourceID
		}
		rawSource, exists := rawByID[source.SourceID]
		if !exists {
			return profileLegacyIntegrationFailure(firstSourceID, "legacy_raw_source_missing")
		}
		raw = append(raw, rawSource)
		migration := definitions[index]
		want[migrations.MigrationKey{App: migration.App, Name: migration.Name}] = migration
	}
	if len(raw) == 0 {
		return nil
	}
	set, _, err := definition.Load(raw...)
	if err != nil {
		return profileLegacyIntegrationFailure(firstSourceID, "legacy_product_decoder_not_composable")
	}
	product := set.Definitions()
	if len(product) != len(want) {
		return profileLegacyIntegrationFailure(firstSourceID, "legacy_product_definition_count_mismatch")
	}
	for _, migration := range product {
		key := migrations.MigrationKey{App: migration.App, Name: migration.Name}
		candidate, exists := want[key]
		if !exists || !reflect.DeepEqual(candidate, migration) {
			return profileLegacyIntegrationFailure(firstSourceID, "legacy_product_definition_mismatch")
		}
	}
	return nil
}

func profileLegacyIntegrationFailure(sourceID, reason string) *profileCandidateError {
	return &profileCandidateError{
		Category: "migration_relation_profile_candidate_error",
		Code:     "legacy_decoder_integration_blocked",
		Stage:    "integration",
		Reason:   reason,
		SourceID: sourceID,
	}
}

func profileGraphFailure(err error, sources []profileSource) *profileCandidateError {
	failure := &profileCandidateError{
		Category: "migration_relation_profile_candidate_error",
		Code:     "invalid_graph",
		Stage:    "graph",
		Reason:   "planner_error",
	}
	var planning *migrations.PlanningError
	if !errors.As(err, &planning) {
		return failure
	}
	failure.Code = string(planning.Code)
	failure.Reason = string(planning.Code)
	selected := planning.Node
	if selected == (migrations.MigrationKey{}) {
		members := planning.Members()
		if len(members) != 0 {
			selected = members[0]
		}
	}
	for _, source := range sources {
		if source.Definition.App == selected.App && source.Definition.Name == selected.Name {
			failure.SourceID = source.SourceID
		}
	}
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
		MaxLength:  field.MaxLength,
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
		if field.Kind == string(ir.FieldForeignKey) || field.TargetField != "" {
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
	if field.TargetField == "" || !profileValidTargetField(field.TargetField) {
		return ir.Field{}, nil, profileSemanticFailure("relation_target_field_required")
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
	canonical["target_field"] = field.TargetField
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

func profileValidTargetField(value string) bool {
	wrapper := ir.Schema{
		FormatVersion: ir.RelationFormatVersion,
		AppLabel:      "_godj_profile_validation",
		Models: []ir.Model{{
			Name:    "target",
			GoName:  "Target",
			DBTable: "_godj_profile_target",
			Fields: []ir.Field{{
				Name: value, GoName: "TargetField", Column: value, Kind: ir.FieldAuto, PrimaryKey: true,
			}},
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

func profileResourceFailure(sourceID, limit string, maximum, actual int) *profileCandidateError {
	return &profileCandidateError{
		Category: "migration_relation_profile_candidate_error",
		Code:     "resource_limit_exceeded",
		Stage:    "semantic",
		Reason:   "resource_limit_exceeded",
		SourceID: sourceID,
		Limit:    limit,
		Maximum:  maximum,
		Actual:   actual,
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
		Column: value.Column, GoName: value.GoName, Kind: string(value.Kind), MaxLength: value.MaxLength,
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
	return profilePublishedDefinition{Profile: value.Profile, Definition: profileCloneDefinition(value.Definition)}
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
