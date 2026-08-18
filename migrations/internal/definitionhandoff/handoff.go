// Package definitionhandoff carries loader-owned migration definition
// authority across the public definition.Set.Migrate -> migrations.Executor
// boundary. The package deliberately depends on neither the migrations root
// package nor migrations/definition so it cannot create an import cycle or
// become a second definition implementation.
package definitionhandoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf8"
)

const (
	legacyDigestDomain   = "godj:migration-definition-set:v1"
	relationDigestDomain = "godj:migration-definition-set:v2"
	provenanceDomain     = "godj:migration-loader-published-definition:v1"
	graphDomain          = "godj:migration-loader-full-graph:v1"
)

// Compatibility is the neutral, exact definition compatibility tuple.
type Compatibility struct {
	DefinitionFormat int64
	LoaderABI        int64
	OperationCodec   int64
	SchemaIR         int64
}

// Producer is non-semantic definition generator provenance.
type Producer struct {
	Name    string
	Version string
}

// Identity is a neutral migration identity.
type Identity struct {
	App  string
	Name string
}

// Default is the closed neutral scalar-default union. Present distinguishes a
// missing default from an explicit zero-valued scalar.
type Default struct {
	Present bool
	Kind    string
	String  string
	Boolean bool
	Integer int64
}

// Relation is the neutral ForeignKey relation arm. Present distinguishes a
// missing arm from an invalid, zero-valued arm.
type Relation struct {
	Present         bool
	TargetApp       string
	TargetModel     string
	Cardinality     string
	ReverseName     string
	ReverseDisabled bool
	OnDelete        string
}

// Field is the complete neutral Schema IR field meaning used by a definition.
type Field struct {
	Name       string
	GoName     string
	Column     string
	Kind       string
	PrimaryKey bool
	Nullable   bool
	MaxLength  int64
	Default    Default
	Relation   Relation
}

// Model is the complete neutral Schema IR model meaning used by an operation.
type Model struct {
	Name    string
	GoName  string
	DBTable string
	Fields  []Field
}

// Operation is the closed neutral definition operation union. HasModel and
// HasField preserve the selected arm independently of zero-valued payloads.
type Operation struct {
	Kind      string
	AppLabel  string
	ModelName string
	HasModel  bool
	Model     Model
	HasField  bool
	Field     Field
}

// Definition is the caller-visible semantic migration definition.
type Definition struct {
	App          string
	Name         string
	Dependencies []Identity
	Operations   []Operation
}

// Record binds one semantic definition to its loader-owned profile and
// diagnostic provenance. SourceID and Producer do not participate in the set
// digest.
type Record struct {
	SourceID   string
	Producer   Producer
	Profile    Compatibility
	Definition Definition
}

type sealedRecord struct {
	record         Record
	canonical      []byte
	definitionSeal string
	provenanceSeal string
}

// Handoff is an immutable-by-contract loader authority carrier. Its fields are
// intentionally private; all constructors and accessors deep-copy nested data.
type Handoff struct {
	initialized bool
	records     []sealedRecord
	digest      string
	graphSeal   string
}

// New snapshots records and seals the per-definition canonical meaning,
// diagnostic provenance, semantic set digest, and sorted full graph.
func New(records []Record) (Handoff, error) {
	if len(records) == 0 {
		return Handoff{}, errors.New("definition handoff requires at least one record")
	}
	canonical := cloneRecords(records)
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].Definition.App != canonical[right].Definition.App {
			return canonical[left].Definition.App < canonical[right].Definition.App
		}
		if canonical[left].Definition.Name != canonical[right].Definition.Name {
			return canonical[left].Definition.Name < canonical[right].Definition.Name
		}
		return canonical[left].SourceID < canonical[right].SourceID
	})
	sealed := make([]sealedRecord, len(canonical))
	for index := range canonical {
		if err := validateProvenance(canonical[index]); err != nil {
			return Handoff{}, fmt.Errorf("definition handoff record %d: %w", index, err)
		}
		if !supportedProfile(canonical[index].Profile) {
			return Handoff{}, fmt.Errorf("definition handoff record %d has unsupported compatibility profile", index)
		}
		if err := validateProfileMeaning(canonical[index]); err != nil {
			return Handoff{}, fmt.Errorf("definition handoff record %d: %w", index, err)
		}
		definitionBytes, err := canonicalDefinition(canonical[index].Definition)
		if err != nil {
			return Handoff{}, fmt.Errorf("canonical definition %d: %w", index, err)
		}
		sealed[index] = sealedRecord{
			record:         cloneRecord(canonical[index]),
			canonical:      append([]byte(nil), definitionBytes...),
			definitionSeal: hash(definitionBytes),
		}
		sealed[index].provenanceSeal, err = provenanceSeal(canonical[index], definitionBytes)
		if err != nil {
			return Handoff{}, fmt.Errorf("provenance seal %d: %w", index, err)
		}
	}
	digest, err := setDigest(sealed)
	if err != nil {
		return Handoff{}, err
	}
	graph, err := fullGraphSeal(sealed)
	if err != nil {
		return Handoff{}, err
	}
	return Handoff{initialized: true, records: sealed, digest: digest, graphSeal: graph}, nil
}

// IsZero reports whether no loader authority is present.
func (h Handoff) IsZero() bool {
	return !h.initialized
}

// Clone returns a fresh deep copy of the carrier.
func (h Handoff) Clone() Handoff {
	if h.IsZero() {
		return Handoff{}
	}
	clone := Handoff{initialized: true, digest: h.digest, graphSeal: h.graphSeal, records: make([]sealedRecord, len(h.records))}
	for index := range h.records {
		clone.records[index] = sealedRecord{
			record:         cloneRecord(h.records[index].record),
			canonical:      append([]byte(nil), h.records[index].canonical...),
			definitionSeal: h.records[index].definitionSeal,
			provenanceSeal: h.records[index].provenanceSeal,
		}
	}
	return clone
}

// Digest returns the sealed semantic definition-set digest.
func (h Handoff) Digest() string {
	return h.digest
}

// Records returns fresh snapshots for module-private validation and tests.
func (h Handoff) Records() []Record {
	records := make([]Record, len(h.records))
	for index := range h.records {
		records[index] = cloneRecord(h.records[index].record)
	}
	return records
}

// ValidateVisible verifies the exact caller-visible definitions and every
// stored seal. It intentionally does not accept caller-selected profiles or
// provenance: those values come only from the loader-owned carrier.
func (h Handoff) ValidateVisible(visible []Definition) error {
	if h.IsZero() {
		return errors.New("definition handoff is empty")
	}
	definitions := cloneDefinitions(visible)
	sort.Slice(definitions, func(left, right int) bool {
		if definitions[left].App != definitions[right].App {
			return definitions[left].App < definitions[right].App
		}
		return definitions[left].Name < definitions[right].Name
	})
	if len(definitions) != len(h.records) {
		return fmt.Errorf("definition count %d does not match sealed count %d", len(definitions), len(h.records))
	}
	for index := range h.records {
		record := h.records[index]
		if !reflect.DeepEqual(definitions[index], record.record.Definition) {
			return fmt.Errorf("definition %d does not match sealed loader definition", index)
		}
		if !supportedProfile(record.record.Profile) {
			return fmt.Errorf("definition %d has unsupported sealed profile", index)
		}
		if err := validateProvenance(record.record); err != nil {
			return fmt.Errorf("definition %d provenance: %w", index, err)
		}
		if err := validateProfileMeaning(record.record); err != nil {
			return fmt.Errorf("definition %d profile meaning: %w", index, err)
		}
		canonical, err := canonicalDefinition(record.record.Definition)
		if err != nil {
			return fmt.Errorf("definition %d canonical form: %w", index, err)
		}
		if !reflect.DeepEqual(canonical, record.canonical) || hash(canonical) != record.definitionSeal {
			return fmt.Errorf("definition %d canonical seal mismatch", index)
		}
		provenance, err := provenanceSeal(record.record, canonical)
		if err != nil || provenance != record.provenanceSeal {
			return fmt.Errorf("definition %d provenance seal mismatch", index)
		}
	}
	digest, err := setDigest(h.records)
	if err != nil || digest != h.digest {
		return errors.New("definition set digest seal mismatch")
	}
	graph, err := fullGraphSeal(h.records)
	if err != nil || graph != h.graphSeal {
		return errors.New("definition full-graph seal mismatch")
	}
	return nil
}

type carrierContext struct {
	context.Context
	handoff Handoff
}

// WithContext attaches a fresh carrier clone in a package-private wrapper.
// Callers must not pass a nil context.
func WithContext(ctx context.Context, handoff Handoff) context.Context {
	return carrierContext{Context: ctx, handoff: handoff.Clone()}
}

// Take consumes the outer private wrapper and returns its original base
// context. Downstream cancellation cleanup, sessions, and backends therefore
// cannot retain the loader carrier as an ordinary context value.
func Take(ctx context.Context) (context.Context, Handoff, bool) {
	if ctx == nil {
		return nil, Handoff{}, false
	}
	value, ok := ctx.(carrierContext)
	if !ok || value.handoff.IsZero() {
		return ctx, Handoff{}, false
	}
	return value.Context, value.handoff.Clone(), true
}

func supportedProfile(value Compatibility) bool {
	return value == (Compatibility{DefinitionFormat: 1, LoaderABI: 1, OperationCodec: 1, SchemaIR: 2}) ||
		value == (Compatibility{DefinitionFormat: 1, LoaderABI: 2, OperationCodec: 2, SchemaIR: 3})
}

func validateProvenance(record Record) error {
	if record.SourceID == "" || !utf8.ValidString(record.SourceID) {
		return errors.New("source ID is empty or invalid UTF-8")
	}
	if record.Producer.Name == "" || !utf8.ValidString(record.Producer.Name) {
		return errors.New("producer name is empty or invalid UTF-8")
	}
	if record.Producer.Version == "" || !utf8.ValidString(record.Producer.Version) {
		return errors.New("producer version is empty or invalid UTF-8")
	}
	return nil
}

func validateProfileMeaning(record Record) error {
	relationProfile := record.Profile == (Compatibility{DefinitionFormat: 1, LoaderABI: 2, OperationCodec: 2, SchemaIR: 3})
	for operationIndex, operation := range record.Definition.Operations {
		if operation.HasModel {
			for fieldIndex, field := range operation.Model.Fields {
				if (field.Kind == "foreign_key" || field.Relation.Present) && !relationProfile {
					return fmt.Errorf("operation %d model field %d relation requires relation profile", operationIndex, fieldIndex)
				}
			}
		}
		if operation.HasField && (operation.Field.Kind == "foreign_key" || operation.Field.Relation.Present) && !relationProfile {
			return fmt.Errorf("operation %d field relation requires relation profile", operationIndex)
		}
	}
	return nil
}

func setDigest(records []sealedRecord) (string, error) {
	allLegacy := true
	for index := range records {
		allLegacy = allLegacy && records[index].record.Profile == (Compatibility{DefinitionFormat: 1, LoaderABI: 1, OperationCodec: 1, SchemaIR: 2})
	}
	output := make([]byte, 0, len(records)*256)
	if allLegacy {
		output = append(output, `{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"definitions":[`...)
		for index := range records {
			if index != 0 {
				output = append(output, ',')
			}
			output = append(output, records[index].canonical...)
		}
		output = append(output, `],"domain":"`...)
		output = append(output, legacyDigestDomain...)
		output = append(output, `"}`...)
		return hash(output), nil
	}
	output = append(output, `{"definitions":[`...)
	for index := range records {
		if index != 0 {
			output = append(output, ',')
		}
		output = append(output, `{"definition":`...)
		output = append(output, records[index].canonical...)
		output = append(output, `,"profile":`...)
		output = appendCompatibility(output, records[index].record.Profile)
		output = append(output, '}')
	}
	output = append(output, `],"domain":"`...)
	output = append(output, relationDigestDomain...)
	output = append(output, `"}`...)
	return hash(output), nil
}

func provenanceSeal(record Record, canonical []byte) (string, error) {
	output := []byte(`{"definition":`)
	output = append(output, canonical...)
	output = append(output, `,"domain":"`...)
	output = append(output, provenanceDomain...)
	output = append(output, `","producer":{"name":`...)
	var err error
	output, err = appendCanonicalString(output, record.Producer.Name)
	if err != nil {
		return "", err
	}
	output = append(output, `,"version":`...)
	output, err = appendCanonicalString(output, record.Producer.Version)
	if err != nil {
		return "", err
	}
	output = append(output, `},"profile":`...)
	output = appendCompatibility(output, record.Profile)
	output = append(output, `,"source_id":`...)
	output, err = appendCanonicalString(output, record.SourceID)
	if err != nil {
		return "", err
	}
	output = append(output, '}')
	return hash(output), nil
}

func fullGraphSeal(records []sealedRecord) (string, error) {
	output := []byte(`{"definitions":[`)
	for index := range records {
		if index != 0 {
			output = append(output, ',')
		}
		definition := records[index].record.Definition
		output = append(output, `{"app":`...)
		var err error
		output, err = appendCanonicalString(output, definition.App)
		if err != nil {
			return "", err
		}
		output = append(output, `,"dependencies":[`...)
		dependencies := append([]Identity(nil), definition.Dependencies...)
		sort.Slice(dependencies, func(left, right int) bool {
			if dependencies[left].App != dependencies[right].App {
				return dependencies[left].App < dependencies[right].App
			}
			return dependencies[left].Name < dependencies[right].Name
		})
		for dependencyIndex := range dependencies {
			if dependencyIndex != 0 {
				output = append(output, ',')
			}
			output = append(output, `{"app":`...)
			output, err = appendCanonicalString(output, dependencies[dependencyIndex].App)
			if err != nil {
				return "", err
			}
			output = append(output, `,"name":`...)
			output, err = appendCanonicalString(output, dependencies[dependencyIndex].Name)
			if err != nil {
				return "", err
			}
			output = append(output, '}')
		}
		output = append(output, `],"name":`...)
		output, err = appendCanonicalString(output, definition.Name)
		if err != nil {
			return "", err
		}
		output = append(output, '}')
	}
	output = append(output, `],"domain":"`...)
	output = append(output, graphDomain...)
	output = append(output, `"}`...)
	return hash(output), nil
}

func canonicalDefinition(definition Definition) ([]byte, error) {
	dependencies := append([]Identity(nil), definition.Dependencies...)
	sort.Slice(dependencies, func(left, right int) bool {
		if dependencies[left].App != dependencies[right].App {
			return dependencies[left].App < dependencies[right].App
		}
		return dependencies[left].Name < dependencies[right].Name
	})
	output := []byte(`{"app":`)
	var err error
	output, err = appendCanonicalString(output, definition.App)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"dependencies":[`...)
	for index := range dependencies {
		if index != 0 {
			output = append(output, ',')
		}
		output = append(output, `{"app":`...)
		output, err = appendCanonicalString(output, dependencies[index].App)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"name":`...)
		output, err = appendCanonicalString(output, dependencies[index].Name)
		if err != nil {
			return nil, err
		}
		output = append(output, '}')
	}
	output = append(output, `],"name":`...)
	output, err = appendCanonicalString(output, definition.Name)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"operations":[`...)
	for index := range definition.Operations {
		if index != 0 {
			output = append(output, ',')
		}
		output, err = appendOperation(output, definition.Operations[index])
		if err != nil {
			return nil, err
		}
	}
	return append(output, ']', '}'), nil
}

func appendOperation(output []byte, operation Operation) ([]byte, error) {
	switch operation.Kind {
	case "create_model":
		if !operation.HasModel || operation.HasField || operation.ModelName != "" {
			return nil, errors.New("invalid create_model arm")
		}
		output = append(output, `{"app_label":`...)
		var err error
		output, err = appendCanonicalString(output, operation.AppLabel)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"kind":"create_model","model":`...)
		output, err = appendModel(output, operation.Model)
		if err != nil {
			return nil, err
		}
		return append(output, '}'), nil
	case "add_field":
		if operation.HasModel || !operation.HasField || operation.ModelName == "" {
			return nil, errors.New("invalid add_field arm")
		}
		output = append(output, `{"app_label":`...)
		var err error
		output, err = appendCanonicalString(output, operation.AppLabel)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"field":`...)
		output, err = appendField(output, operation.Field)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"kind":"add_field","model_name":`...)
		output, err = appendCanonicalString(output, operation.ModelName)
		if err != nil {
			return nil, err
		}
		return append(output, '}'), nil
	default:
		return nil, fmt.Errorf("unsupported operation %q", operation.Kind)
	}
}

func appendModel(output []byte, model Model) ([]byte, error) {
	output = append(output, `{"db_table":`...)
	var err error
	output, err = appendCanonicalString(output, model.DBTable)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"fields":[`...)
	for index := range model.Fields {
		if index != 0 {
			output = append(output, ',')
		}
		output, err = appendField(output, model.Fields[index])
		if err != nil {
			return nil, err
		}
	}
	output = append(output, `],"go_name":`...)
	output, err = appendCanonicalString(output, model.GoName)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"name":`...)
	output, err = appendCanonicalString(output, model.Name)
	if err != nil {
		return nil, err
	}
	return append(output, '}'), nil
}

func appendField(output []byte, field Field) ([]byte, error) {
	output = append(output, `{"column":`...)
	var err error
	output, err = appendCanonicalString(output, field.Column)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"default":`...)
	output, err = appendDefault(output, field.Default)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"go_name":`...)
	output, err = appendCanonicalString(output, field.GoName)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"kind":`...)
	output, err = appendCanonicalString(output, field.Kind)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"max_length":`...)
	output = strconv.AppendInt(output, field.MaxLength, 10)
	output = append(output, `,"name":`...)
	output, err = appendCanonicalString(output, field.Name)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"nullable":`...)
	output = strconv.AppendBool(output, field.Nullable)
	output = append(output, `,"primary_key":`...)
	output = strconv.AppendBool(output, field.PrimaryKey)
	if field.Relation.Present {
		output = append(output, `,"relation":{"cardinality":`...)
		output, err = appendCanonicalString(output, field.Relation.Cardinality)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"on_delete":`...)
		output, err = appendCanonicalString(output, field.Relation.OnDelete)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"reverse":{"disabled":`...)
		output = strconv.AppendBool(output, field.Relation.ReverseDisabled)
		output = append(output, `,"name":`...)
		output, err = appendCanonicalString(output, field.Relation.ReverseName)
		if err != nil {
			return nil, err
		}
		output = append(output, `},"target":{"app_label":`...)
		output, err = appendCanonicalString(output, field.Relation.TargetApp)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"model_name":`...)
		output, err = appendCanonicalString(output, field.Relation.TargetModel)
		if err != nil {
			return nil, err
		}
		output = append(output, '}', '}')
	}
	return append(output, '}'), nil
}

func appendDefault(output []byte, value Default) ([]byte, error) {
	if !value.Present {
		return append(output, "null"...), nil
	}
	switch value.Kind {
	case "string":
		output = append(output, `{"kind":"string","string":`...)
		var err error
		output, err = appendCanonicalString(output, value.String)
		if err != nil {
			return nil, err
		}
		return append(output, '}'), nil
	case "boolean":
		output = append(output, `{"boolean":`...)
		output = strconv.AppendBool(output, value.Boolean)
		return append(output, `,"kind":"boolean"}`...), nil
	case "integer":
		output = append(output, `{"integer":`...)
		output = strconv.AppendInt(output, value.Integer, 10)
		return append(output, `,"kind":"integer"}`...), nil
	default:
		return nil, fmt.Errorf("unsupported scalar default %q", value.Kind)
	}
}

func appendCompatibility(output []byte, value Compatibility) []byte {
	output = append(output, `{"definition_format":`...)
	output = strconv.AppendInt(output, value.DefinitionFormat, 10)
	output = append(output, `,"loader_abi":`...)
	output = strconv.AppendInt(output, value.LoaderABI, 10)
	output = append(output, `,"operation_codec":`...)
	output = strconv.AppendInt(output, value.OperationCodec, 10)
	output = append(output, `,"schema_ir":`...)
	output = strconv.AppendInt(output, value.SchemaIR, 10)
	return append(output, '}')
}

func appendCanonicalString(output []byte, value string) ([]byte, error) {
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

func hash(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cloneRecords(values []Record) []Record {
	clones := make([]Record, len(values))
	for index := range values {
		clones[index] = cloneRecord(values[index])
	}
	return clones
}

func cloneRecord(value Record) Record {
	clone := value
	clone.Definition = cloneDefinition(value.Definition)
	return clone
}

func cloneDefinitions(values []Definition) []Definition {
	clones := make([]Definition, len(values))
	for index := range values {
		clones[index] = cloneDefinition(values[index])
	}
	return clones
}

func cloneDefinition(value Definition) Definition {
	clone := value
	clone.Dependencies = append([]Identity(nil), value.Dependencies...)
	clone.Operations = make([]Operation, len(value.Operations))
	for index := range value.Operations {
		clone.Operations[index] = cloneOperation(value.Operations[index])
	}
	return clone
}

func cloneOperation(value Operation) Operation {
	clone := value
	clone.Model.Fields = append([]Field(nil), value.Model.Fields...)
	return clone
}
