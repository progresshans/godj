package definitionload

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

const (
	definitionFormatVersion int64 = 1
	loaderABIVersion        int64 = 1
	operationCodecVersion   int64 = 1
	schemaIRVersion         int64 = ir.FormatVersion
	maximumWireLength             = int64(1<<31 - 1)
	digestDomain                  = "godj:migration-definition-set:v1"
)

var databaseIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

type sourceDocument struct {
	SourceID string
	Document []byte
}

type sourceSnapshot struct {
	sourceID string
	rawID    []byte
	document []byte
}

type compatibilityTuple struct {
	definitionFormat int64
	loaderABI        int64
	operationCodec   int64
	schemaIR         int64
}

type producer struct {
	name    string
	version string
}

type parsedDocument struct {
	source        sourceSnapshot
	root          jsonValue
	compatibility compatibilityTuple
	producer      producer
}

type sourceInventory struct {
	SourceID string
	Producer producer
	App      string
	Name     string
}

type loadedDefinitionSet struct {
	Definitions []migrations.Migration
	Digest      string
	Sources     []sourceInventory
}

type loadMetrics struct {
	DocumentsReceived       int
	HeadersValidated        int
	OperationsDecoded       int
	PlannerConstruction     int
	DefinitionsPublished    int
	DefinitionSetsPublished int
}

type definitionError struct {
	Category       string
	Code           string
	Stage          string
	SourceID       string
	JSONPointer    string
	App            string
	Name           string
	OperationIndex int
	Reason         string
}

func (e *definitionError) Error() string {
	if e == nil {
		return "migration definition source error"
	}
	return fmt.Sprintf("%s/%s stage=%s source=%q pointer=%q reason=%s", e.Category, e.Code, e.Stage, e.SourceID, e.JSONPointer, e.Reason)
}

func sourceFailure(sourceID, reason string) *definitionError {
	return &definitionError{
		Category:       "migration_definition_source_error",
		Code:           "invalid_definition_source",
		Stage:          "source",
		SourceID:       sourceID,
		OperationIndex: -1,
		Reason:         reason,
	}
}

func documentFailure(sourceID, pointer, reason string) *definitionError {
	return &definitionError{
		Category:       "migration_definition_source_error",
		Code:           "invalid_definition_document",
		Stage:          "document",
		SourceID:       sourceID,
		JSONPointer:    pointer,
		OperationIndex: -1,
		Reason:         reason,
	}
}

func semanticFailure(code, sourceID, pointer, app, name string, operationIndex int, reason string) *definitionError {
	return &definitionError{
		Category:       "migration_definition_source_error",
		Code:           code,
		Stage:          "semantic",
		SourceID:       sourceID,
		JSONPointer:    pointer,
		App:            app,
		Name:           name,
		OperationIndex: operationIndex,
		Reason:         reason,
	}
}

func compatibilityFailure(code, sourceID, coordinate string) *definitionError {
	return &definitionError{
		Category:       "migration_definition_source_error",
		Code:           code,
		Stage:          "compatibility",
		SourceID:       sourceID,
		JSONPointer:    "/compatibility/" + coordinate,
		OperationIndex: -1,
		Reason:         coordinate,
	}
}

type jsonKind uint8

const (
	jsonNull jsonKind = iota + 1
	jsonBoolean
	jsonString
	jsonNumber
	jsonArray
	jsonObject
)

type jsonValue struct {
	kind    jsonKind
	boolean bool
	string  string
	number  string
	array   []jsonValue
	object  map[string]jsonValue
}

type jsonParser struct {
	data       []byte
	position   int
	sourceID   string
	candidates []*definitionError
}

func parseJSONDocument(source sourceSnapshot) (jsonValue, []*definitionError) {
	if !utf8.Valid(source.document) {
		return jsonValue{}, []*definitionError{documentFailure(source.sourceID, "", "invalid_utf8")}
	}
	parser := jsonParser{data: source.document, sourceID: source.sourceID}
	parser.skipWhitespace()
	root, err := parser.parseValue("")
	if err != nil {
		return jsonValue{}, []*definitionError{documentFailure(source.sourceID, "", "syntax")}
	}
	parser.skipWhitespace()
	if parser.position != len(parser.data) {
		return jsonValue{}, []*definitionError{documentFailure(source.sourceID, "", "trailing_value")}
	}
	return root, parser.candidates
}

func (p *jsonParser) parseValue(pointer string) (jsonValue, error) {
	if p.position >= len(p.data) {
		return jsonValue{}, errors.New("unexpected end of JSON")
	}
	switch p.data[p.position] {
	case '{':
		return p.parseObject(pointer)
	case '[':
		return p.parseArray(pointer)
	case '"':
		value, err := p.parseString(pointer)
		return jsonValue{kind: jsonString, string: value}, err
	case 't':
		if !p.consumeLiteral("true") {
			return jsonValue{}, errors.New("invalid true literal")
		}
		return jsonValue{kind: jsonBoolean, boolean: true}, nil
	case 'f':
		if !p.consumeLiteral("false") {
			return jsonValue{}, errors.New("invalid false literal")
		}
		return jsonValue{kind: jsonBoolean}, nil
	case 'n':
		if !p.consumeLiteral("null") {
			return jsonValue{}, errors.New("invalid null literal")
		}
		return jsonValue{kind: jsonNull}, nil
	default:
		number, integer, err := p.parseNumber()
		if err != nil {
			return jsonValue{}, err
		}
		if !integer {
			p.candidates = append(p.candidates, documentFailure(p.sourceID, pointer, "wrong_type"))
		}
		return jsonValue{kind: jsonNumber, number: number}, nil
	}
}

func (p *jsonParser) parseObject(pointer string) (jsonValue, error) {
	p.position++
	p.skipWhitespace()
	object := make(map[string]jsonValue)
	if p.consumeByte('}') {
		return jsonValue{kind: jsonObject, object: object}, nil
	}
	for {
		if p.position >= len(p.data) || p.data[p.position] != '"' {
			return jsonValue{}, errors.New("object key is not a string")
		}
		key, err := p.parseString(pointer)
		if err != nil {
			return jsonValue{}, err
		}
		memberPointer := joinJSONPointer(pointer, key)
		p.skipWhitespace()
		if !p.consumeByte(':') {
			return jsonValue{}, errors.New("object member has no colon")
		}
		p.skipWhitespace()
		child, err := p.parseValue(memberPointer)
		if err != nil {
			return jsonValue{}, err
		}
		if _, exists := object[key]; exists {
			p.candidates = append(p.candidates, documentFailure(p.sourceID, memberPointer, "duplicate_key"))
		} else {
			object[key] = child
		}
		p.skipWhitespace()
		if p.consumeByte('}') {
			return jsonValue{kind: jsonObject, object: object}, nil
		}
		if !p.consumeByte(',') {
			return jsonValue{}, errors.New("object member has no comma")
		}
		p.skipWhitespace()
	}
}

func (p *jsonParser) parseArray(pointer string) (jsonValue, error) {
	p.position++
	p.skipWhitespace()
	array := make([]jsonValue, 0)
	if p.consumeByte(']') {
		return jsonValue{kind: jsonArray, array: array}, nil
	}
	for index := 0; ; index++ {
		child, err := p.parseValue(joinJSONPointer(pointer, strconv.Itoa(index)))
		if err != nil {
			return jsonValue{}, err
		}
		array = append(array, child)
		p.skipWhitespace()
		if p.consumeByte(']') {
			return jsonValue{kind: jsonArray, array: array}, nil
		}
		if !p.consumeByte(',') {
			return jsonValue{}, errors.New("array element has no comma")
		}
		p.skipWhitespace()
	}
}

func (p *jsonParser) parseString(pointer string) (string, error) {
	if !p.consumeByte('"') {
		return "", errors.New("missing string quote")
	}
	var builder strings.Builder
	for p.position < len(p.data) {
		current := p.data[p.position]
		if current == '"' {
			p.position++
			return builder.String(), nil
		}
		if current < 0x20 {
			return "", errors.New("unescaped control character")
		}
		if current != '\\' {
			runeValue, size := utf8.DecodeRune(p.data[p.position:])
			if runeValue == utf8.RuneError && size == 1 {
				return "", errors.New("invalid UTF-8 in string")
			}
			builder.Write(p.data[p.position : p.position+size])
			p.position += size
			continue
		}

		p.position++
		if p.position >= len(p.data) {
			return "", errors.New("unfinished string escape")
		}
		escape := p.data[p.position]
		p.position++
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
			unit, err := p.parseHexUnit()
			if err != nil {
				return "", err
			}
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if p.position+6 <= len(p.data) && p.data[p.position] == '\\' && p.data[p.position+1] == 'u' {
					low, lowErr := parseHex4(p.data[p.position+2 : p.position+6])
					if lowErr == nil && low >= 0xdc00 && low <= 0xdfff {
						p.position += 6
						builder.WriteRune(rune(0x10000 + (int(unit)-0xd800)*0x400 + int(low) - 0xdc00))
						continue
					}
				}
				p.candidates = append(p.candidates, documentFailure(p.sourceID, pointer, "lone_surrogate"))
				builder.WriteRune(utf8.RuneError)
			case unit >= 0xdc00 && unit <= 0xdfff:
				p.candidates = append(p.candidates, documentFailure(p.sourceID, pointer, "lone_surrogate"))
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

func (p *jsonParser) parseHexUnit() (uint16, error) {
	if p.position+4 > len(p.data) {
		return 0, errors.New("short Unicode escape")
	}
	unit, err := parseHex4(p.data[p.position : p.position+4])
	if err != nil {
		return 0, err
	}
	p.position += 4
	return unit, nil
}

func parseHex4(value []byte) (uint16, error) {
	if len(value) != 4 {
		return 0, errors.New("Unicode escape is not four digits")
	}
	var result uint16
	for _, current := range value {
		result <<= 4
		switch {
		case current >= '0' && current <= '9':
			result += uint16(current - '0')
		case current >= 'a' && current <= 'f':
			result += uint16(current-'a') + 10
		case current >= 'A' && current <= 'F':
			result += uint16(current-'A') + 10
		default:
			return 0, errors.New("invalid Unicode escape")
		}
	}
	return result, nil
}

func (p *jsonParser) parseNumber() (string, bool, error) {
	start := p.position
	if p.consumeByte('-') && p.position >= len(p.data) {
		return "", false, errors.New("unfinished negative number")
	}
	if p.consumeByte('0') {
		// A following decimal digit is rejected by the parent delimiter check.
	} else {
		if p.position >= len(p.data) || p.data[p.position] < '1' || p.data[p.position] > '9' {
			return "", false, errors.New("invalid number integer part")
		}
		for p.position < len(p.data) && p.data[p.position] >= '0' && p.data[p.position] <= '9' {
			p.position++
		}
	}
	integer := true
	if p.consumeByte('.') {
		integer = false
		if p.position >= len(p.data) || p.data[p.position] < '0' || p.data[p.position] > '9' {
			return "", false, errors.New("invalid number fraction")
		}
		for p.position < len(p.data) && p.data[p.position] >= '0' && p.data[p.position] <= '9' {
			p.position++
		}
	}
	if p.position < len(p.data) && (p.data[p.position] == 'e' || p.data[p.position] == 'E') {
		integer = false
		p.position++
		if p.position < len(p.data) && (p.data[p.position] == '+' || p.data[p.position] == '-') {
			p.position++
		}
		if p.position >= len(p.data) || p.data[p.position] < '0' || p.data[p.position] > '9' {
			return "", false, errors.New("invalid number exponent")
		}
		for p.position < len(p.data) && p.data[p.position] >= '0' && p.data[p.position] <= '9' {
			p.position++
		}
	}
	return string(p.data[start:p.position]), integer, nil
}

func (p *jsonParser) skipWhitespace() {
	for p.position < len(p.data) {
		switch p.data[p.position] {
		case ' ', '\t', '\n', '\r':
			p.position++
		default:
			return
		}
	}
}

func (p *jsonParser) consumeByte(want byte) bool {
	if p.position >= len(p.data) || p.data[p.position] != want {
		return false
	}
	p.position++
	return true
}

func (p *jsonParser) consumeLiteral(want string) bool {
	if !bytes.HasPrefix(p.data[p.position:], []byte(want)) {
		return false
	}
	p.position += len(want)
	return true
}

func joinJSONPointer(parent, token string) string {
	escaped := strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
	return parent + "/" + escaped
}

func cloneSourceInput(sources []sourceDocument) ([]sourceSnapshot, *definitionError) {
	candidates := make([]*definitionError, 0)
	snapshots := make([]sourceSnapshot, 0, len(sources))
	for _, source := range sources {
		rawID := append([]byte(nil), []byte(source.SourceID)...)
		diagnosticID := string(rawID)
		switch {
		case len(rawID) == 0:
			candidates = append(candidates, sourceFailure("", "empty_source_id"))
		case !utf8.Valid(rawID):
			candidates = append(candidates, sourceFailure("hex:"+hex.EncodeToString(rawID), "invalid_source_id_utf8"))
		}
		snapshots = append(snapshots, sourceSnapshot{
			sourceID: diagnosticID,
			rawID:    rawID,
			document: append([]byte(nil), source.Document...),
		})
	}
	sort.Slice(snapshots, func(left, right int) bool {
		return bytes.Compare(snapshots[left].rawID, snapshots[right].rawID) < 0
	})
	for index := 1; index < len(snapshots); index++ {
		if bytes.Equal(snapshots[index-1].rawID, snapshots[index].rawID) {
			candidates = append(candidates, sourceFailure(snapshots[index].sourceID, "duplicate_source_id"))
		}
	}
	if len(candidates) != 0 {
		sortDefinitionErrors(candidates)
		return nil, candidates[0]
	}
	return snapshots, nil
}

func sortDefinitionErrors(candidates []*definitionError) {
	sort.SliceStable(candidates, func(left, right int) bool {
		leftRank := stageRank(candidates[left].Stage)
		rightRank := stageRank(candidates[right].Stage)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if candidates[left].Stage == "source" {
			leftReason := reasonRank(candidates[left].Stage, candidates[left].Reason)
			rightReason := reasonRank(candidates[right].Stage, candidates[right].Reason)
			if leftReason != rightReason {
				return leftReason < rightReason
			}
			return candidates[left].SourceID < candidates[right].SourceID
		}
		if candidates[left].SourceID != candidates[right].SourceID {
			return candidates[left].SourceID < candidates[right].SourceID
		}
		if candidates[left].JSONPointer != candidates[right].JSONPointer {
			return candidates[left].JSONPointer < candidates[right].JSONPointer
		}
		return reasonRank(candidates[left].Stage, candidates[left].Reason) < reasonRank(candidates[right].Stage, candidates[right].Reason)
	})
}

func stageRank(stage string) int {
	switch stage {
	case "source":
		return 0
	case "document":
		return 1
	case "compatibility":
		return 2
	case "semantic":
		return 3
	default:
		return 4
	}
}

func reasonRank(stage, reason string) int {
	orders := map[string][]string{
		"source":        {"empty_source_id", "invalid_source_id_utf8", "duplicate_source_id"},
		"document":      {"invalid_utf8", "syntax", "duplicate_key", "lone_surrogate", "unknown_field", "missing_field", "wrong_type", "out_of_range", "trailing_value"},
		"compatibility": {"definition_format", "loader_abi", "operation_codec", "schema_ir"},
		"semantic":      {"unsupported_operation", "invalid_operation", "invalid_ir", "wrong_type", "out_of_range"},
	}
	for index, candidate := range orders[stage] {
		if candidate == reason {
			return index
		}
	}
	return len(orders[stage])
}

func exactObjectCandidates(value jsonValue, fields []string, sourceID, pointer string) (map[string]jsonValue, []*definitionError) {
	if value.kind != jsonObject {
		return nil, []*definitionError{documentFailure(sourceID, pointer, "wrong_type")}
	}
	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
	}
	candidates := make([]*definitionError, 0)
	for field := range value.object {
		if _, exists := wanted[field]; !exists {
			candidates = append(candidates, documentFailure(sourceID, joinJSONPointer(pointer, field), "unknown_field"))
		}
	}
	for _, field := range fields {
		if _, exists := value.object[field]; !exists {
			candidates = append(candidates, documentFailure(sourceID, joinJSONPointer(pointer, field), "missing_field"))
		}
	}
	return value.object, candidates
}

func validateOuter(source sourceSnapshot, root jsonValue, framing []*definitionError) (parsedDocument, []*definitionError) {
	candidates := append([]*definitionError(nil), framing...)
	rootObject, faults := exactObjectCandidates(root, []string{"compatibility", "migration", "producer"}, source.sourceID, "")
	candidates = append(candidates, faults...)
	parsed := parsedDocument{source: source, root: root}
	if rootObject == nil {
		return parsed, candidates
	}

	compatibilityObject, faults := exactObjectCandidates(rootObject["compatibility"], []string{"definition_format", "loader_abi", "operation_codec", "schema_ir"}, source.sourceID, "/compatibility")
	candidates = append(candidates, faults...)
	if compatibilityObject != nil {
		coordinates := []struct {
			name   string
			target *int64
		}{
			{"definition_format", &parsed.compatibility.definitionFormat},
			{"loader_abi", &parsed.compatibility.loaderABI},
			{"operation_codec", &parsed.compatibility.operationCodec},
			{"schema_ir", &parsed.compatibility.schemaIR},
		}
		for _, coordinate := range coordinates {
			value, exists := compatibilityObject[coordinate.name]
			if !exists {
				continue
			}
			pointer := "/compatibility/" + coordinate.name
			if value.kind != jsonNumber || strings.ContainsAny(value.number, ".eE") {
				candidates = append(candidates, documentFailure(source.sourceID, pointer, "wrong_type"))
				continue
			}
			parsedValue, err := strconv.ParseInt(value.number, 10, 64)
			if err != nil {
				candidates = append(candidates, documentFailure(source.sourceID, pointer, "out_of_range"))
				continue
			}
			*coordinate.target = parsedValue
		}
	}

	producerObject, faults := exactObjectCandidates(rootObject["producer"], []string{"name", "version"}, source.sourceID, "/producer")
	candidates = append(candidates, faults...)
	if producerObject != nil {
		for _, field := range []struct {
			name   string
			target *string
		}{{"name", &parsed.producer.name}, {"version", &parsed.producer.version}} {
			value, exists := producerObject[field.name]
			if !exists {
				continue
			}
			if value.kind != jsonString || value.string == "" {
				candidates = append(candidates, documentFailure(source.sourceID, "/producer/"+field.name, "wrong_type"))
				continue
			}
			*field.target = value.string
		}
	}

	_, faults = exactObjectCandidates(rootObject["migration"], []string{"app", "dependencies", "name", "operations"}, source.sourceID, "/migration")
	candidates = append(candidates, faults...)
	candidates = append(candidates, knownMaxLengthLexicalCandidates(rootObject["migration"], source.sourceID)...)
	return parsed, candidates
}

func knownMaxLengthLexicalCandidates(migration jsonValue, sourceID string) []*definitionError {
	if migration.kind != jsonObject {
		return nil
	}
	operations, exists := migration.object["operations"]
	if !exists || operations.kind != jsonArray {
		return nil
	}
	candidates := make([]*definitionError, 0)
	for operationIndex, operation := range operations.array {
		if operation.kind != jsonObject {
			continue
		}
		kind, exists := operation.object["kind"]
		if !exists || kind.kind != jsonString {
			continue
		}
		operationPointer := "/migration/operations/" + strconv.Itoa(operationIndex)
		switch kind.string {
		case "create_model":
			model, exists := operation.object["model"]
			if !exists || model.kind != jsonObject {
				continue
			}
			fields, exists := model.object["fields"]
			if !exists || fields.kind != jsonArray {
				continue
			}
			for fieldIndex, field := range fields.array {
				fieldPointer := operationPointer + "/model/fields/" + strconv.Itoa(fieldIndex)
				candidates = append(candidates, maxLengthLexicalCandidate(field, sourceID, fieldPointer)...)
			}
		case "add_field":
			field, exists := operation.object["field"]
			if !exists {
				continue
			}
			candidates = append(candidates, maxLengthLexicalCandidate(field, sourceID, operationPointer+"/field")...)
		}
	}
	return candidates
}

func maxLengthLexicalCandidate(field jsonValue, sourceID, pointer string) []*definitionError {
	if field.kind != jsonObject {
		return nil
	}
	maxLength, exists := field.object["max_length"]
	if !exists || maxLength.kind != jsonNumber || strings.ContainsAny(maxLength.number, ".eE") {
		return nil
	}
	if _, err := strconv.ParseInt(maxLength.number, 10, 64); err != nil {
		return []*definitionError{documentFailure(sourceID, pointer+"/max_length", "out_of_range")}
	}
	return nil
}

func loadDefinitions(sources []sourceDocument) (loadedDefinitionSet, loadMetrics, error) {
	metrics := loadMetrics{DocumentsReceived: len(sources)}
	snapshots, sourceErr := cloneSourceInput(sources)
	if sourceErr != nil {
		return loadedDefinitionSet{}, metrics, sourceErr
	}

	parsed := make([]parsedDocument, 0, len(snapshots))
	documentErrors := make([]*definitionError, 0)
	for _, source := range snapshots {
		root, framing := parseJSONDocument(source)
		if len(framing) == 1 && (framing[0].Reason == "invalid_utf8" || framing[0].Reason == "syntax" || framing[0].Reason == "trailing_value") {
			documentErrors = append(documentErrors, framing...)
			continue
		}
		document, candidates := validateOuter(source, root, framing)
		if len(candidates) != 0 {
			documentErrors = append(documentErrors, candidates...)
			continue
		}
		parsed = append(parsed, document)
		metrics.HeadersValidated++
	}
	if len(documentErrors) != 0 {
		sortDefinitionErrors(documentErrors)
		return loadedDefinitionSet{}, metrics, documentErrors[0]
	}

	type coordinate struct {
		name     string
		code     string
		expected int64
		value    func(compatibilityTuple) int64
	}
	coordinates := []coordinate{
		{"definition_format", "definition_format_incompatible", definitionFormatVersion, func(value compatibilityTuple) int64 { return value.definitionFormat }},
		{"loader_abi", "loader_abi_incompatible", loaderABIVersion, func(value compatibilityTuple) int64 { return value.loaderABI }},
		{"operation_codec", "operation_codec_incompatible", operationCodecVersion, func(value compatibilityTuple) int64 { return value.operationCodec }},
		{"schema_ir", "schema_ir_incompatible", schemaIRVersion, func(value compatibilityTuple) int64 { return value.schemaIR }},
	}
	for _, coordinate := range coordinates {
		for _, document := range parsed {
			if coordinate.value(document.compatibility) != coordinate.expected {
				return loadedDefinitionSet{}, metrics, compatibilityFailure(coordinate.code, document.source.sourceID, coordinate.name)
			}
		}
	}

	semanticErrors := make([]*definitionError, 0)
	for _, document := range parsed {
		semanticErrors = append(semanticErrors, collectSemanticCandidates(document)...)
	}
	if len(semanticErrors) != 0 {
		sortDefinitionErrors(semanticErrors)
		return loadedDefinitionSet{}, metrics, semanticErrors[0]
	}

	decoded := make([]decodedDocument, 0, len(parsed))
	for _, document := range parsed {
		definition, decodedOperations, err := decodeMigration(document)
		metrics.OperationsDecoded += decodedOperations
		if err != nil {
			return loadedDefinitionSet{}, metrics, err
		}
		decoded = append(decoded, definition)
	}
	sort.Slice(decoded, func(left, right int) bool {
		leftKey := decoded[left].migration.Key()
		rightKey := decoded[right].migration.Key()
		if leftKey.App != rightKey.App {
			return leftKey.App < rightKey.App
		}
		if leftKey.Name != rightKey.Name {
			return leftKey.Name < rightKey.Name
		}
		return decoded[left].source.sourceID < decoded[right].source.sourceID
	})
	definitions := make([]migrations.Migration, len(decoded))
	for index := range decoded {
		definitions[index] = cloneMigration(decoded[index].migration)
	}
	metrics.PlannerConstruction++
	if _, err := migrations.NewPlanner(definitions...); err != nil {
		return loadedDefinitionSet{}, metrics, err
	}
	digest, err := definitionSetDigest(definitions)
	if err != nil {
		return loadedDefinitionSet{}, metrics, err
	}
	inventory := make([]sourceInventory, len(decoded))
	for index := range decoded {
		inventory[index] = sourceInventory{
			SourceID: decoded[index].source.sourceID,
			Producer: decoded[index].producer,
			App:      decoded[index].migration.App,
			Name:     decoded[index].migration.Name,
		}
	}
	sort.Slice(inventory, func(left, right int) bool { return inventory[left].SourceID < inventory[right].SourceID })
	metrics.DefinitionsPublished = len(definitions)
	metrics.DefinitionSetsPublished = 1
	return loadedDefinitionSet{
		Definitions: cloneMigrations(definitions),
		Digest:      digest,
		Sources:     append([]sourceInventory(nil), inventory...),
	}, metrics, nil
}

type decodedDocument struct {
	source    sourceSnapshot
	producer  producer
	migration migrations.Migration
}

func collectSemanticCandidates(document parsedDocument) []*definitionError {
	migration := document.root.object["migration"]
	if migration.kind != jsonObject {
		return []*definitionError{semanticFailure("invalid_definition_operation", document.source.sourceID, "/migration", "", "", -1, "invalid_operation")}
	}
	candidates := make([]*definitionError, 0)
	app := ""
	name := ""
	if value, exists := migration.object["app"]; exists && value.kind == jsonString {
		app = value.string
	} else {
		candidates = append(candidates, semanticFailure("invalid_definition_operation", document.source.sourceID, "/migration/app", "", "", -1, "invalid_operation"))
	}
	if value, exists := migration.object["name"]; exists && value.kind == jsonString {
		name = value.string
	} else {
		candidates = append(candidates, semanticFailure("invalid_definition_operation", document.source.sourceID, "/migration/name", app, "", -1, "invalid_operation"))
	}

	dependencies, exists := migration.object["dependencies"]
	if !exists || dependencies.kind != jsonArray {
		candidates = append(candidates, semanticFailure("invalid_definition_operation", document.source.sourceID, "/migration/dependencies", app, name, -1, "invalid_operation"))
	} else {
		for index, dependency := range dependencies.array {
			pointer := "/migration/dependencies/" + strconv.Itoa(index)
			object, faults := semanticObjectCandidates(dependency, []string{"app", "name"}, document.source.sourceID, pointer, app, name, -1, "invalid_definition_operation")
			candidates = append(candidates, faults...)
			if object == nil {
				continue
			}
			for _, field := range []string{"app", "name"} {
				if value, present := object[field]; present && value.kind != jsonString {
					candidates = append(candidates, semanticFailure("invalid_definition_operation", document.source.sourceID, pointer+"/"+field, app, name, -1, "invalid_operation"))
				}
			}
		}
	}

	operations, exists := migration.object["operations"]
	if !exists || operations.kind != jsonArray {
		candidates = append(candidates, semanticFailure("invalid_definition_operation", document.source.sourceID, "/migration/operations", app, name, -1, "invalid_operation"))
	} else {
		for index, operation := range operations.array {
			candidates = append(candidates, collectOperationCandidates(operation, document.source.sourceID, app, name, index)...)
		}
	}
	return candidates
}

func semanticObjectCandidates(value jsonValue, fields []string, sourceID, pointer, app, name string, operationIndex int, code string) (map[string]jsonValue, []*definitionError) {
	if value.kind != jsonObject {
		return nil, []*definitionError{semanticFailure(code, sourceID, pointer, app, name, operationIndex, reasonForSemanticCode(code))}
	}
	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
	}
	candidates := make([]*definitionError, 0)
	for field := range value.object {
		if _, exists := wanted[field]; !exists {
			candidates = append(candidates, semanticFailure(code, sourceID, joinJSONPointer(pointer, field), app, name, operationIndex, reasonForSemanticCode(code)))
		}
	}
	for _, field := range fields {
		if _, exists := value.object[field]; !exists {
			candidates = append(candidates, semanticFailure(code, sourceID, joinJSONPointer(pointer, field), app, name, operationIndex, reasonForSemanticCode(code)))
		}
	}
	return value.object, candidates
}

func semanticUnknownCandidates(value jsonValue, recognized []string, sourceID, pointer, app, name string, operationIndex int, code string) []*definitionError {
	if value.kind != jsonObject {
		return nil
	}
	wanted := make(map[string]struct{}, len(recognized))
	for _, field := range recognized {
		wanted[field] = struct{}{}
	}
	candidates := make([]*definitionError, 0)
	for field := range value.object {
		if _, exists := wanted[field]; !exists {
			candidates = append(candidates, semanticFailure(code, sourceID, joinJSONPointer(pointer, field), app, name, operationIndex, reasonForSemanticCode(code)))
		}
	}
	return candidates
}

func collectOperationCandidates(value jsonValue, sourceID, app, name string, operationIndex int) []*definitionError {
	pointer := "/migration/operations/" + strconv.Itoa(operationIndex)
	if value.kind != jsonObject {
		return []*definitionError{semanticFailure("invalid_definition_operation", sourceID, pointer, app, name, operationIndex, "invalid_operation")}
	}
	const operationCode = "invalid_definition_operation"
	commonFields := []string{"app_label", "field", "kind", "model", "model_name"}
	candidates := semanticUnknownCandidates(value, commonFields, sourceID, pointer, app, name, operationIndex, operationCode)
	kind, exists := value.object["kind"]
	if !exists || kind.kind != jsonString {
		return append(candidates, semanticFailure(operationCode, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_operation"))
	}
	if kind.string != "create_model" && kind.string != "add_field" {
		return append(candidates, semanticFailure("unsupported_definition_operation", sourceID, pointer+"/kind", app, name, operationIndex, "unsupported_operation"))
	}

	fields := []string{"app_label", "kind", "model"}
	if kind.string == "add_field" {
		fields = []string{"app_label", "field", "kind", "model_name"}
	}
	object, faults := semanticObjectCandidates(value, fields, sourceID, pointer, app, name, operationIndex, operationCode)
	candidates = append(candidates, faults...)
	if appLabel, present := object["app_label"]; present {
		if appLabel.kind != jsonString || appLabel.string != app {
			candidates = append(candidates, semanticFailure(operationCode, sourceID, pointer+"/app_label", app, name, operationIndex, "invalid_operation"))
		} else if !databaseIdentifier.MatchString(appLabel.string) {
			candidates = append(candidates, semanticFailure("invalid_definition_ir", sourceID, pointer+"/app_label", app, name, operationIndex, "invalid_ir"))
		}
	}
	if kind.string == "create_model" {
		if model, present := object["model"]; present {
			candidates = append(candidates, collectModelCandidates(model, sourceID, pointer+"/model", app, name, operationIndex)...)
		}
	} else {
		if modelName, present := object["model_name"]; present {
			if modelName.kind != jsonString || !databaseIdentifier.MatchString(modelName.string) {
				candidates = append(candidates, semanticFailure(operationCode, sourceID, pointer+"/model_name", app, name, operationIndex, "invalid_operation"))
			}
		}
		if field, present := object["field"]; present {
			fieldCandidates := collectFieldCandidates(field, sourceID, pointer+"/field", app, name, operationIndex)
			candidates = append(candidates, fieldCandidates...)
			if field.kind == jsonObject {
				if kind, exists := field.object["kind"]; exists && kind.kind == jsonString && kind.string != string(ir.FieldChar) && kind.string != string(ir.FieldBoolean) {
					candidates = append(candidates, semanticFailure("invalid_definition_ir", sourceID, pointer+"/field/kind", app, name, operationIndex, "invalid_ir"))
				}
				if primaryKey, exists := field.object["primary_key"]; exists && primaryKey.kind == jsonBoolean && primaryKey.boolean {
					candidates = append(candidates, semanticFailure("invalid_definition_ir", sourceID, pointer+"/field/primary_key", app, name, operationIndex, "invalid_ir"))
				}
			}
		}
	}
	if len(candidates) == 0 && hasObjectFields(value, fields) {
		if _, err := decodeOperation(sanitizeOperationValue(value, kind.string), sourceID, app, name, operationIndex); err != nil {
			if definitionErr, ok := err.(*definitionError); ok {
				candidates = append(candidates, definitionErr)
			}
		}
	}
	return candidates
}

func collectModelCandidates(value jsonValue, sourceID, pointer, app, name string, operationIndex int) []*definitionError {
	const code = "invalid_definition_ir"
	fields := []string{"db_table", "fields", "go_name", "name"}
	object, candidates := semanticObjectCandidates(value, fields, sourceID, pointer, app, name, operationIndex, code)
	if object == nil {
		return candidates
	}
	for _, field := range []string{"db_table", "go_name", "name"} {
		child, exists := object[field]
		if !exists {
			continue
		}
		if child.kind != jsonString {
			candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir"))
			continue
		}
		valid := databaseIdentifier.MatchString(child.string)
		if field == "go_name" {
			valid = isExportedGoIdentifier(child.string)
		}
		if !valid {
			candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir"))
		}
	}
	if child, exists := object["fields"]; exists {
		if child.kind != jsonArray || len(child.array) == 0 {
			candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/fields", app, name, operationIndex, "invalid_ir"))
		} else {
			for index, field := range child.array {
				fieldPointer := pointer + "/fields/" + strconv.Itoa(index)
				fieldCandidates := collectFieldCandidates(field, sourceID, fieldPointer, app, name, operationIndex)
				candidates = append(candidates, fieldCandidates...)
			}
			candidates = append(candidates, collectModelFieldAggregateCandidates(child.array, sourceID, pointer+"/fields", app, name, operationIndex)...)
		}
	}
	if len(candidates) == 0 && hasObjectFields(value, fields) {
		if _, err := decodeModel(sanitizeModelValue(value), sourceID, pointer, app, name, operationIndex); err != nil {
			if definitionErr, ok := err.(*definitionError); ok {
				candidates = append(candidates, definitionErr)
			}
		}
	}
	return candidates
}

func collectModelFieldAggregateCandidates(values []jsonValue, sourceID, pointer, app, name string, operationIndex int) []*definitionError {
	seenNames := make(map[string]struct{}, len(values))
	seenGoNames := make(map[string]struct{}, len(values))
	seenColumns := make(map[string]struct{}, len(values))
	primaryKeys := 0
	primaryKeysComplete := true
	hasAuto := false
	kindsComplete := true
	aggregateInvalid := false

	for _, value := range values {
		if value.kind != jsonObject {
			primaryKeysComplete = false
			kindsComplete = false
			continue
		}
		for _, member := range []struct {
			name  string
			seen  map[string]struct{}
			valid func(string) bool
		}{
			{name: "name", seen: seenNames, valid: databaseIdentifier.MatchString},
			{name: "go_name", seen: seenGoNames, valid: isExportedGoIdentifier},
			{name: "column", seen: seenColumns, valid: databaseIdentifier.MatchString},
		} {
			candidate, exists := value.object[member.name]
			if !exists || candidate.kind != jsonString || !member.valid(candidate.string) {
				continue
			}
			if _, duplicate := member.seen[candidate.string]; duplicate {
				aggregateInvalid = true
			}
			member.seen[candidate.string] = struct{}{}
		}

		primaryKey, exists := value.object["primary_key"]
		if !exists || primaryKey.kind != jsonBoolean {
			primaryKeysComplete = false
		} else if primaryKey.boolean {
			primaryKeys++
		}
		kind, exists := value.object["kind"]
		if !exists || kind.kind != jsonString {
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
	return []*definitionError{semanticFailure("invalid_definition_ir", sourceID, pointer, app, name, operationIndex, "invalid_ir")}
}

func hasObjectFields(value jsonValue, fields []string) bool {
	if value.kind != jsonObject {
		return false
	}
	for _, field := range fields {
		if _, exists := value.object[field]; !exists {
			return false
		}
	}
	return true
}

func filteredObjectValue(value jsonValue, fields []string) jsonValue {
	filtered := jsonValue{kind: jsonObject, object: make(map[string]jsonValue, len(fields))}
	if value.kind != jsonObject {
		return filtered
	}
	for _, field := range fields {
		if child, exists := value.object[field]; exists {
			filtered.object[field] = child
		}
	}
	return filtered
}

func sanitizeOperationValue(value jsonValue, kind string) jsonValue {
	fields := []string{"app_label", "kind", "model"}
	if kind == "add_field" {
		fields = []string{"app_label", "field", "kind", "model_name"}
	}
	filtered := filteredObjectValue(value, fields)
	if model, exists := filtered.object["model"]; exists {
		filtered.object["model"] = sanitizeModelValue(model)
	}
	if field, exists := filtered.object["field"]; exists {
		filtered.object["field"] = sanitizeFieldValue(field)
	}
	return filtered
}

func sanitizeModelValue(value jsonValue) jsonValue {
	filtered := filteredObjectValue(value, []string{"db_table", "fields", "go_name", "name"})
	fields, exists := filtered.object["fields"]
	if !exists || fields.kind != jsonArray {
		return filtered
	}
	fieldValues := make([]jsonValue, len(fields.array))
	for index, field := range fields.array {
		fieldValues[index] = sanitizeFieldValue(field)
	}
	filtered.object["fields"] = jsonValue{kind: jsonArray, array: fieldValues}
	return filtered
}

func sanitizeFieldValue(value jsonValue) jsonValue {
	filtered := filteredObjectValue(value, []string{"column", "default", "go_name", "kind", "max_length", "name", "nullable", "primary_key"})
	defaultValue, exists := filtered.object["default"]
	if exists {
		filtered.object["default"] = sanitizeDefaultValue(defaultValue)
	}
	return filtered
}

func sanitizeDefaultValue(value jsonValue) jsonValue {
	if value.kind != jsonObject {
		return value
	}
	kind, exists := value.object["kind"]
	if !exists || kind.kind != jsonString {
		return filteredObjectValue(value, []string{"kind"})
	}
	switch kind.string {
	case string(ir.ScalarString):
		return filteredObjectValue(value, []string{"kind", "string"})
	case string(ir.ScalarBoolean):
		return filteredObjectValue(value, []string{"boolean", "kind"})
	default:
		return filteredObjectValue(value, []string{"kind"})
	}
}

func collectFieldCandidates(value jsonValue, sourceID, pointer, app, name string, operationIndex int) []*definitionError {
	const code = "invalid_definition_ir"
	fields := []string{"column", "default", "go_name", "kind", "max_length", "name", "nullable", "primary_key"}
	object, candidates := semanticObjectCandidates(value, fields, sourceID, pointer, app, name, operationIndex, code)
	if object == nil {
		return candidates
	}
	stringValid := make(map[string]bool, 4)
	for _, field := range []string{"column", "go_name", "kind", "name"} {
		child, exists := object[field]
		if !exists {
			continue
		}
		stringValid[field] = child.kind == jsonString
		if !stringValid[field] {
			candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir"))
			continue
		}
		valid := true
		switch field {
		case "column", "name":
			valid = databaseIdentifier.MatchString(child.string)
		case "go_name":
			valid = isExportedGoIdentifier(child.string)
		}
		if !valid {
			stringValid[field] = false
			candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir"))
		}
	}
	booleanValid := make(map[string]bool, 2)
	for _, field := range []string{"nullable", "primary_key"} {
		child, exists := object[field]
		if !exists {
			continue
		}
		booleanValid[field] = child.kind == jsonBoolean
		if !booleanValid[field] {
			candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir"))
		}
	}
	maxLengthValid := false
	var maxLength int64
	if maxLengthNode, exists := object["max_length"]; exists {
		if maxLengthNode.kind != jsonNumber || strings.ContainsAny(maxLengthNode.number, ".eE") {
			candidates = append(candidates, semanticFailure("invalid_definition_document", sourceID, pointer+"/max_length", app, name, operationIndex, "wrong_type"))
		} else if parsed, err := strconv.ParseInt(maxLengthNode.number, 10, 64); err != nil || parsed < 0 || parsed > maximumWireLength {
			candidates = append(candidates, semanticFailure("invalid_definition_document", sourceID, pointer+"/max_length", app, name, operationIndex, "out_of_range"))
		} else {
			maxLengthValid = true
			maxLength = parsed
		}
	}
	defaultValid := false
	var defaultValue *ir.ScalarDefault
	if defaultNode, exists := object["default"]; exists {
		defaultCandidates := collectDefaultCandidates(defaultNode, sourceID, pointer+"/default", app, name, operationIndex)
		candidates = append(candidates, defaultCandidates...)
		decoded, err := decodeDefault(sanitizeDefaultValue(defaultNode), sourceID, pointer+"/default", app, name, operationIndex)
		if err == nil {
			defaultValid = true
			defaultValue = decoded
		}
	}
	if stringValid["kind"] {
		kind := object["kind"].string
		switch ir.FieldKind(kind) {
		case ir.FieldAuto:
			if defaultValid && defaultValue != nil {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir"))
			}
			if maxLengthValid && maxLength != 0 {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir"))
			}
			if booleanValid["nullable"] && object["nullable"].boolean {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/nullable", app, name, operationIndex, "invalid_ir"))
			}
			if booleanValid["primary_key"] && !object["primary_key"].boolean {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir"))
			}
		case ir.FieldChar:
			if defaultValid && defaultValue != nil && (defaultValue.Kind != ir.ScalarString || maxLengthValid && utf8.RuneCountInString(defaultValue.String) > int(maxLength)) {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir"))
			}
			if maxLengthValid && maxLength <= 0 {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir"))
			}
			if booleanValid["primary_key"] && object["primary_key"].boolean {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir"))
			}
		case ir.FieldBoolean:
			if defaultValid && defaultValue != nil && defaultValue.Kind != ir.ScalarBoolean {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir"))
			}
			if maxLengthValid && maxLength != 0 {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir"))
			}
			if booleanValid["nullable"] && object["nullable"].boolean {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/nullable", app, name, operationIndex, "invalid_ir"))
			}
			if booleanValid["primary_key"] && object["primary_key"].boolean {
				candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir"))
			}
		default:
			candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir"))
		}
	}
	if len(candidates) == 0 {
		if _, err := decodeField(value, sourceID, pointer, app, name, operationIndex); err != nil {
			if definitionErr, ok := err.(*definitionError); ok {
				candidates = append(candidates, definitionErr)
			}
		}
	}
	return candidates
}

func isExportedGoIdentifier(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	first, size := utf8.DecodeRuneInString(value)
	if first == utf8.RuneError || !unicode.IsUpper(first) {
		return false
	}
	for _, current := range value[size:] {
		if current != '_' && !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			return false
		}
	}
	return true
}

func collectDefaultCandidates(value jsonValue, sourceID, pointer, app, name string, operationIndex int) []*definitionError {
	if value.kind == jsonNull {
		return nil
	}
	const code = "invalid_definition_ir"
	if value.kind != jsonObject {
		return []*definitionError{semanticFailure(code, sourceID, pointer, app, name, operationIndex, "invalid_ir")}
	}
	commonFields := []string{"boolean", "kind", "string"}
	candidates := semanticUnknownCandidates(value, commonFields, sourceID, pointer, app, name, operationIndex, code)
	kind, exists := value.object["kind"]
	if !exists || kind.kind != jsonString {
		return append(candidates, semanticFailure(code, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir"))
	}
	switch kind.string {
	case string(ir.ScalarString):
		object, faults := semanticObjectCandidates(value, []string{"kind", "string"}, sourceID, pointer, app, name, operationIndex, code)
		candidates = append(candidates, faults...)
		if child, present := object["string"]; present && child.kind != jsonString {
			candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/string", app, name, operationIndex, "invalid_ir"))
		}
	case string(ir.ScalarBoolean):
		object, faults := semanticObjectCandidates(value, []string{"boolean", "kind"}, sourceID, pointer, app, name, operationIndex, code)
		candidates = append(candidates, faults...)
		if child, present := object["boolean"]; present && child.kind != jsonBoolean {
			candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/boolean", app, name, operationIndex, "invalid_ir"))
		}
	default:
		candidates = append(candidates, semanticFailure(code, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir"))
	}
	return candidates
}

func decodeMigration(document parsedDocument) (decodedDocument, int, error) {
	migrationNode := document.root.object["migration"]
	migrationObject := migrationNode.object
	app, err := requireString(migrationObject["app"], document.source.sourceID, "/migration/app", "invalid_definition_operation", "", "", -1)
	if err != nil {
		return decodedDocument{}, 0, err
	}
	name, err := requireString(migrationObject["name"], document.source.sourceID, "/migration/name", "invalid_definition_operation", app, "", -1)
	if err != nil {
		return decodedDocument{}, 0, err
	}
	dependenciesNode := migrationObject["dependencies"]
	if dependenciesNode.kind != jsonArray {
		return decodedDocument{}, 0, semanticFailure("invalid_definition_operation", document.source.sourceID, "/migration/dependencies", app, name, -1, "invalid_operation")
	}
	dependencies := make([]migrations.MigrationKey, 0, len(dependenciesNode.array))
	for index, dependencyNode := range dependenciesNode.array {
		pointer := "/migration/dependencies/" + strconv.Itoa(index)
		dependencyObject, objectErr := requireSemanticObject(dependencyNode, []string{"app", "name"}, document.source.sourceID, pointer, app, name, -1, "invalid_definition_operation")
		if objectErr != nil {
			return decodedDocument{}, 0, objectErr
		}
		dependencyApp, stringErr := requireString(dependencyObject["app"], document.source.sourceID, pointer+"/app", "invalid_definition_operation", app, name, -1)
		if stringErr != nil {
			return decodedDocument{}, 0, stringErr
		}
		dependencyName, stringErr := requireString(dependencyObject["name"], document.source.sourceID, pointer+"/name", "invalid_definition_operation", app, name, -1)
		if stringErr != nil {
			return decodedDocument{}, 0, stringErr
		}
		dependencies = append(dependencies, migrations.MigrationKey{App: dependencyApp, Name: dependencyName})
	}
	sort.Slice(dependencies, func(left, right int) bool {
		if dependencies[left].App != dependencies[right].App {
			return dependencies[left].App < dependencies[right].App
		}
		return dependencies[left].Name < dependencies[right].Name
	})

	operationsNode := migrationObject["operations"]
	if operationsNode.kind != jsonArray {
		return decodedDocument{}, 0, semanticFailure("invalid_definition_operation", document.source.sourceID, "/migration/operations", app, name, -1, "invalid_operation")
	}
	operations := make([]migrations.Operation, 0, len(operationsNode.array))
	decodedOperations := 0
	for index, operationNode := range operationsNode.array {
		operation, operationErr := decodeOperation(operationNode, document.source.sourceID, app, name, index)
		if operationErr != nil {
			return decodedDocument{}, decodedOperations, operationErr
		}
		operations = append(operations, operation)
		decodedOperations++
	}
	return decodedDocument{
		source:   document.source,
		producer: document.producer,
		migration: migrations.Migration{
			App:          app,
			Name:         name,
			Dependencies: dependencies,
			Operations:   operations,
		},
	}, decodedOperations, nil
}

func requireString(value jsonValue, sourceID, pointer, code, app, name string, operationIndex int) (string, error) {
	if value.kind != jsonString {
		return "", semanticFailure(code, sourceID, pointer, app, name, operationIndex, reasonForSemanticCode(code))
	}
	return value.string, nil
}

func requireSemanticObject(value jsonValue, fields []string, sourceID, pointer, app, name string, operationIndex int, code string) (map[string]jsonValue, error) {
	if value.kind != jsonObject {
		return nil, semanticFailure(code, sourceID, pointer, app, name, operationIndex, reasonForSemanticCode(code))
	}
	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
	}
	candidates := make([]string, 0)
	for field := range value.object {
		if _, exists := wanted[field]; !exists {
			candidates = append(candidates, field)
		}
	}
	for _, field := range fields {
		if _, exists := value.object[field]; !exists {
			candidates = append(candidates, field)
		}
	}
	if len(candidates) != 0 {
		sort.Strings(candidates)
		return nil, semanticFailure(code, sourceID, joinJSONPointer(pointer, candidates[0]), app, name, operationIndex, reasonForSemanticCode(code))
	}
	return value.object, nil
}

func reasonForSemanticCode(code string) string {
	switch code {
	case "unsupported_definition_operation":
		return "unsupported_operation"
	case "invalid_definition_ir":
		return "invalid_ir"
	default:
		return "invalid_operation"
	}
}

func decodeOperation(value jsonValue, sourceID, app, name string, operationIndex int) (migrations.Operation, error) {
	pointer := "/migration/operations/" + strconv.Itoa(operationIndex)
	if value.kind != jsonObject {
		return nil, semanticFailure("invalid_definition_operation", sourceID, pointer, app, name, operationIndex, "invalid_operation")
	}
	kindNode, exists := value.object["kind"]
	if !exists || kindNode.kind != jsonString {
		return nil, semanticFailure("invalid_definition_operation", sourceID, pointer+"/kind", app, name, operationIndex, "invalid_operation")
	}
	kind := kindNode.string
	if kind != "create_model" && kind != "add_field" {
		return nil, semanticFailure("unsupported_definition_operation", sourceID, pointer+"/kind", app, name, operationIndex, "unsupported_operation")
	}
	fields := []string{"app_label", "kind", "model"}
	if kind == "add_field" {
		fields = []string{"app_label", "field", "kind", "model_name"}
	}
	object, err := requireSemanticObject(value, fields, sourceID, pointer, app, name, operationIndex, "invalid_definition_operation")
	if err != nil {
		return nil, err
	}
	appLabel, err := requireString(object["app_label"], sourceID, pointer+"/app_label", "invalid_definition_operation", app, name, operationIndex)
	if err != nil {
		return nil, err
	}
	if appLabel != app {
		return nil, semanticFailure("invalid_definition_operation", sourceID, pointer+"/app_label", app, name, operationIndex, "invalid_operation")
	}
	if kind == "create_model" {
		model, modelErr := decodeModel(object["model"], sourceID, pointer+"/model", app, name, operationIndex)
		if modelErr != nil {
			return nil, modelErr
		}
		wrapper := ir.Schema{FormatVersion: ir.FormatVersion, AppLabel: appLabel, Models: []ir.Model{model.Clone()}}
		normalized, normalizeErr := ir.Normalize(wrapper)
		if normalizeErr != nil || !reflect.DeepEqual(normalized, wrapper) {
			return nil, semanticFailure("invalid_definition_ir", sourceID, pointer+"/model", app, name, operationIndex, "invalid_ir")
		}
		return migrations.CreateModel{AppLabel: appLabel, Model: model.Clone()}, nil
	}

	modelName, err := requireString(object["model_name"], sourceID, pointer+"/model_name", "invalid_definition_operation", app, name, operationIndex)
	if err != nil {
		return nil, err
	}
	if !databaseIdentifier.MatchString(modelName) {
		return nil, semanticFailure("invalid_definition_operation", sourceID, pointer+"/model_name", app, name, operationIndex, "invalid_operation")
	}
	field, fieldErr := decodeField(object["field"], sourceID, pointer+"/field", app, name, operationIndex)
	if fieldErr != nil {
		return nil, fieldErr
	}
	if field.PrimaryKey || (field.Kind != ir.FieldChar && field.Kind != ir.FieldBoolean) {
		return nil, semanticFailure("invalid_definition_ir", sourceID, pointer+"/field", app, name, operationIndex, "invalid_ir")
	}
	syntheticName := "_godj_loader_pk"
	syntheticGoName := "GodjLoaderPK"
	syntheticColumn := "_godj_loader_pk"
	for field.Name == syntheticName || field.GoName == syntheticGoName || field.Column == syntheticColumn {
		syntheticName += "_"
		syntheticGoName += "X"
		syntheticColumn += "_"
	}
	syntheticField := ir.Field{
		Name:       syntheticName,
		GoName:     syntheticGoName,
		Column:     syntheticColumn,
		Kind:       ir.FieldAuto,
		PrimaryKey: true,
	}
	syntheticModel := ir.Model{
		Name:    "_godj_loader_validation",
		GoName:  "GodjLoaderValidation",
		DBTable: "_godj_loader_validation",
		Fields:  []ir.Field{syntheticField, cloneField(field)},
	}
	wrapper := ir.Schema{FormatVersion: ir.FormatVersion, AppLabel: appLabel, Models: []ir.Model{syntheticModel}}
	normalized, normalizeErr := ir.Normalize(wrapper)
	if normalizeErr != nil || !reflect.DeepEqual(normalized, wrapper) || len(normalized.Models) != 1 || len(normalized.Models[0].Fields) != 2 || !reflect.DeepEqual(normalized.Models[0].Fields[1], field) {
		return nil, semanticFailure("invalid_definition_ir", sourceID, pointer+"/field", app, name, operationIndex, "invalid_ir")
	}
	return migrations.AddField{AppLabel: appLabel, ModelName: modelName, Field: cloneField(field)}, nil
}

func decodeModel(value jsonValue, sourceID, pointer, app, name string, operationIndex int) (ir.Model, error) {
	object, err := requireSemanticObject(value, []string{"db_table", "fields", "go_name", "name"}, sourceID, pointer, app, name, operationIndex, "invalid_definition_ir")
	if err != nil {
		return ir.Model{}, err
	}
	modelName, err := requireString(object["name"], sourceID, pointer+"/name", "invalid_definition_ir", app, name, operationIndex)
	if err != nil {
		return ir.Model{}, err
	}
	goName, err := requireString(object["go_name"], sourceID, pointer+"/go_name", "invalid_definition_ir", app, name, operationIndex)
	if err != nil {
		return ir.Model{}, err
	}
	dbTable, err := requireString(object["db_table"], sourceID, pointer+"/db_table", "invalid_definition_ir", app, name, operationIndex)
	if err != nil {
		return ir.Model{}, err
	}
	fieldsNode := object["fields"]
	if fieldsNode.kind != jsonArray {
		return ir.Model{}, semanticFailure("invalid_definition_ir", sourceID, pointer+"/fields", app, name, operationIndex, "invalid_ir")
	}
	fields := make([]ir.Field, 0, len(fieldsNode.array))
	for index, fieldNode := range fieldsNode.array {
		field, fieldErr := decodeField(fieldNode, sourceID, pointer+"/fields/"+strconv.Itoa(index), app, name, operationIndex)
		if fieldErr != nil {
			return ir.Model{}, fieldErr
		}
		fields = append(fields, field)
	}
	return ir.Model{Name: modelName, GoName: goName, DBTable: dbTable, Fields: fields}, nil
}

func decodeField(value jsonValue, sourceID, pointer, app, name string, operationIndex int) (ir.Field, error) {
	object, err := requireSemanticObject(value, []string{"column", "default", "go_name", "kind", "max_length", "name", "nullable", "primary_key"}, sourceID, pointer, app, name, operationIndex, "invalid_definition_ir")
	if err != nil {
		return ir.Field{}, err
	}
	fieldName, err := requireString(object["name"], sourceID, pointer+"/name", "invalid_definition_ir", app, name, operationIndex)
	if err != nil {
		return ir.Field{}, err
	}
	goName, err := requireString(object["go_name"], sourceID, pointer+"/go_name", "invalid_definition_ir", app, name, operationIndex)
	if err != nil {
		return ir.Field{}, err
	}
	column, err := requireString(object["column"], sourceID, pointer+"/column", "invalid_definition_ir", app, name, operationIndex)
	if err != nil {
		return ir.Field{}, err
	}
	kind, err := requireString(object["kind"], sourceID, pointer+"/kind", "invalid_definition_ir", app, name, operationIndex)
	if err != nil {
		return ir.Field{}, err
	}
	primaryKeyNode := object["primary_key"]
	nullableNode := object["nullable"]
	if primaryKeyNode.kind != jsonBoolean || nullableNode.kind != jsonBoolean {
		return ir.Field{}, semanticFailure("invalid_definition_ir", sourceID, pointer, app, name, operationIndex, "invalid_ir")
	}
	maxLengthNode := object["max_length"]
	if maxLengthNode.kind != jsonNumber || strings.ContainsAny(maxLengthNode.number, ".eE") {
		return ir.Field{}, semanticFailure("invalid_definition_document", sourceID, pointer+"/max_length", app, name, operationIndex, "wrong_type")
	}
	maxLength, parseErr := strconv.ParseInt(maxLengthNode.number, 10, 64)
	if parseErr != nil || maxLength < 0 || maxLength > maximumWireLength {
		return ir.Field{}, semanticFailure("invalid_definition_document", sourceID, pointer+"/max_length", app, name, operationIndex, "out_of_range")
	}
	defaultValue, defaultErr := decodeDefault(object["default"], sourceID, pointer+"/default", app, name, operationIndex)
	if defaultErr != nil {
		return ir.Field{}, defaultErr
	}
	return ir.Field{
		Name:       fieldName,
		GoName:     goName,
		Column:     column,
		Kind:       ir.FieldKind(kind),
		PrimaryKey: primaryKeyNode.boolean,
		Nullable:   nullableNode.boolean,
		MaxLength:  int(maxLength),
		Default:    defaultValue,
	}, nil
}

func decodeDefault(value jsonValue, sourceID, pointer, app, name string, operationIndex int) (*ir.ScalarDefault, error) {
	if value.kind == jsonNull {
		return nil, nil
	}
	if value.kind != jsonObject {
		return nil, semanticFailure("invalid_definition_ir", sourceID, pointer, app, name, operationIndex, "invalid_ir")
	}
	kindNode, exists := value.object["kind"]
	if !exists || kindNode.kind != jsonString {
		return nil, semanticFailure("invalid_definition_ir", sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir")
	}
	switch kindNode.string {
	case string(ir.ScalarString):
		object, err := requireSemanticObject(value, []string{"kind", "string"}, sourceID, pointer, app, name, operationIndex, "invalid_definition_ir")
		if err != nil {
			return nil, err
		}
		stringValue, err := requireString(object["string"], sourceID, pointer+"/string", "invalid_definition_ir", app, name, operationIndex)
		if err != nil {
			return nil, err
		}
		return &ir.ScalarDefault{Kind: ir.ScalarString, String: stringValue}, nil
	case string(ir.ScalarBoolean):
		object, err := requireSemanticObject(value, []string{"boolean", "kind"}, sourceID, pointer, app, name, operationIndex, "invalid_definition_ir")
		if err != nil {
			return nil, err
		}
		if object["boolean"].kind != jsonBoolean {
			return nil, semanticFailure("invalid_definition_ir", sourceID, pointer+"/boolean", app, name, operationIndex, "invalid_ir")
		}
		return &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: object["boolean"].boolean}, nil
	default:
		return nil, semanticFailure("invalid_definition_ir", sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir")
	}
}

func cloneField(field ir.Field) ir.Field {
	clone := field
	if field.Default != nil {
		value := *field.Default
		clone.Default = &value
	}
	return clone
}

func cloneOperation(operation migrations.Operation) migrations.Operation {
	switch value := operation.(type) {
	case migrations.CreateModel:
		return migrations.CreateModel{AppLabel: value.AppLabel, Model: value.Model.Clone()}
	case *migrations.CreateModel:
		if value == nil {
			return (*migrations.CreateModel)(nil)
		}
		clone := migrations.CreateModel{AppLabel: value.AppLabel, Model: value.Model.Clone()}
		return &clone
	case migrations.AddField:
		return migrations.AddField{AppLabel: value.AppLabel, ModelName: value.ModelName, Field: cloneField(value.Field)}
	case *migrations.AddField:
		if value == nil {
			return (*migrations.AddField)(nil)
		}
		clone := migrations.AddField{AppLabel: value.AppLabel, ModelName: value.ModelName, Field: cloneField(value.Field)}
		return &clone
	default:
		return operation
	}
}

func cloneMigration(migration migrations.Migration) migrations.Migration {
	clone := migrations.Migration{
		App:          migration.App,
		Name:         migration.Name,
		Dependencies: append([]migrations.MigrationKey(nil), migration.Dependencies...),
		Operations:   make([]migrations.Operation, len(migration.Operations)),
	}
	for index, operation := range migration.Operations {
		clone.Operations[index] = cloneOperation(operation)
	}
	return clone
}

func cloneMigrations(definitions []migrations.Migration) []migrations.Migration {
	clones := make([]migrations.Migration, len(definitions))
	for index := range definitions {
		clones[index] = cloneMigration(definitions[index])
	}
	return clones
}

func definitionSetDigest(definitions []migrations.Migration) (string, error) {
	canonical, err := canonicalDefinitionSet(definitions)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func canonicalDefinitionSet(definitions []migrations.Migration) ([]byte, error) {
	var output []byte
	output = append(output, `{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"definitions":[`...)
	for definitionIndex, definition := range definitions {
		if definitionIndex != 0 {
			output = append(output, ',')
		}
		output = append(output, `{"app":`...)
		var err error
		output, err = appendCanonicalString(output, definition.App)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"dependencies":[`...)
		for dependencyIndex, dependency := range definition.Dependencies {
			if dependencyIndex != 0 {
				output = append(output, ',')
			}
			output = append(output, `{"app":`...)
			output, err = appendCanonicalString(output, dependency.App)
			if err != nil {
				return nil, err
			}
			output = append(output, `,"name":`...)
			output, err = appendCanonicalString(output, dependency.Name)
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
		for operationIndex, operation := range definition.Operations {
			if operationIndex != 0 {
				output = append(output, ',')
			}
			output, err = appendCanonicalOperation(output, operation)
			if err != nil {
				return nil, err
			}
		}
		output = append(output, "]}"...)
	}
	output = append(output, `],"domain":`...)
	output, err := appendCanonicalString(output, digestDomain)
	if err != nil {
		return nil, err
	}
	return append(output, '}'), nil
}

func appendCanonicalOperation(output []byte, operation migrations.Operation) ([]byte, error) {
	switch value := operation.(type) {
	case migrations.CreateModel:
		output = append(output, `{"app_label":`...)
		var err error
		output, err = appendCanonicalString(output, value.AppLabel)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"kind":"create_model","model":`...)
		output, err = appendCanonicalModel(output, value.Model)
		if err != nil {
			return nil, err
		}
		return append(output, '}'), nil
	case migrations.AddField:
		output = append(output, `{"app_label":`...)
		var err error
		output, err = appendCanonicalString(output, value.AppLabel)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"field":`...)
		output, err = appendCanonicalField(output, value.Field)
		if err != nil {
			return nil, err
		}
		output = append(output, `,"kind":"add_field","model_name":`...)
		output, err = appendCanonicalString(output, value.ModelName)
		if err != nil {
			return nil, err
		}
		return append(output, '}'), nil
	default:
		return nil, fmt.Errorf("unsupported canonical operation %T", operation)
	}
}

func appendCanonicalModel(output []byte, model ir.Model) ([]byte, error) {
	output = append(output, `{"db_table":`...)
	var err error
	output, err = appendCanonicalString(output, model.DBTable)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"fields":[`...)
	for index, field := range model.Fields {
		if index != 0 {
			output = append(output, ',')
		}
		output, err = appendCanonicalField(output, field)
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

func appendCanonicalField(output []byte, field ir.Field) ([]byte, error) {
	output = append(output, `{"column":`...)
	var err error
	output, err = appendCanonicalString(output, field.Column)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"default":`...)
	if field.Default == nil {
		output = append(output, "null"...)
	} else {
		switch field.Default.Kind {
		case ir.ScalarString:
			output = append(output, `{"kind":"string","string":`...)
			output, err = appendCanonicalString(output, field.Default.String)
			if err != nil {
				return nil, err
			}
			output = append(output, '}')
		case ir.ScalarBoolean:
			output = append(output, `{"boolean":`...)
			output = strconv.AppendBool(output, field.Default.Boolean)
			output = append(output, `,"kind":"boolean"}`...)
		default:
			return nil, fmt.Errorf("unsupported canonical default %q", field.Default.Kind)
		}
	}
	output = append(output, `,"go_name":`...)
	output, err = appendCanonicalString(output, field.GoName)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"kind":`...)
	output, err = appendCanonicalString(output, string(field.Kind))
	if err != nil {
		return nil, err
	}
	output = append(output, `,"max_length":`...)
	output = strconv.AppendInt(output, int64(field.MaxLength), 10)
	output = append(output, `,"name":`...)
	output, err = appendCanonicalString(output, field.Name)
	if err != nil {
		return nil, err
	}
	output = append(output, `,"nullable":`...)
	output = strconv.AppendBool(output, field.Nullable)
	output = append(output, `,"primary_key":`...)
	output = strconv.AppendBool(output, field.PrimaryKey)
	return append(output, '}'), nil
}

func appendCanonicalString(output []byte, value string) ([]byte, error) {
	if !utf8.ValidString(value) {
		return nil, errors.New("canonical string is not valid UTF-8")
	}
	const hexadecimal = "0123456789abcdef"
	output = append(output, '"')
	for len(value) != 0 {
		runeValue, size := utf8.DecodeRuneInString(value)
		switch runeValue {
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
			if runeValue < 0x20 {
				output = append(output, '\\', 'u', '0', '0', hexadecimal[byte(runeValue)>>4], hexadecimal[byte(runeValue)&0x0f])
			} else {
				output = append(output, value[:size]...)
			}
		}
		value = value[size:]
	}
	return append(output, '"'), nil
}
