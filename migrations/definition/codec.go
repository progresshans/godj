package definition

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

const maximumWireLength int64 = 1<<31 - 1

type parsedDocument struct {
	source        sourceSnapshot
	root          jsonValue
	formatVersion int64
	producer      Producer
}

type decodedDocument struct {
	source    sourceSnapshot
	producer  Producer
	migration migrations.Migration
}

// parseEnvelope owns strict outer shape and the pieces that must be trusted
// before format dispatch. Nested operation and IR shape remains a
// semantic-stage concern.
func parseEnvelope(source sourceSnapshot, root jsonValue, framing []failureCandidate) (parsedDocument, []failureCandidate) {
	parsed := parsedDocument{source: source, root: root}
	candidates := append([]failureCandidate(nil), framing...)
	rootObject, ok, faults := exactDocumentObject(root, []string{"format_version", "migration", "producer"}, source.sourceID, "")
	candidates = append(candidates, faults...)
	if !ok {
		return parsed, candidates
	}

	if version, exists := rootObject.member("format_version"); exists {
		parsedValue, reason, valid := signedInteger(version)
		if !valid {
			candidates = append(candidates, documentFailure(source.sourceID, "/format_version", reason))
		} else {
			parsed.formatVersion = parsedValue
		}
	}

	producer, exists := rootObject.member("producer")
	if exists {
		producerObject, objectOK, objectFaults := exactDocumentObject(
			producer,
			[]string{"name", "version"},
			source.sourceID,
			"/producer",
		)
		candidates = append(candidates, objectFaults...)
		if objectOK {
			for _, field := range []struct {
				name   string
				target *string
			}{
				{name: "name", target: &parsed.producer.Name},
				{name: "version", target: &parsed.producer.Version},
			} {
				value, present := producerObject.member(field.name)
				if !present {
					continue
				}
				if value.kind != jsonString || value.string == "" {
					candidates = append(candidates, documentFailure(source.sourceID, "/producer/"+field.name, "wrong_type"))
					continue
				}
				*field.target = value.string
			}
		}
	}

	migration, exists := rootObject.member("migration")
	if exists {
		_, migrationOK, migrationFaults := exactDocumentObject(
			migration,
			[]string{"app", "dependencies", "name", "operations"},
			source.sourceID,
			"/migration",
		)
		candidates = append(candidates, migrationFaults...)
		if migrationOK {
			candidates = append(candidates, knownMaxLengthLexicalCandidates(migration, source.sourceID)...)
		}
	}
	return parsed, candidates
}

func exactDocumentObject(value jsonValue, fields []string, sourceID, pointer string) (jsonValue, bool, []failureCandidate) {
	if value.kind != jsonObject {
		return jsonValue{}, false, []failureCandidate{documentFailure(sourceID, pointer, "wrong_type")}
	}
	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
	}
	candidates := make([]failureCandidate, 0)
	for _, member := range value.object {
		if _, recognized := wanted[member.key]; !recognized {
			candidates = append(candidates, documentFailure(sourceID, codecPointer(pointer, member.key), "unknown_field"))
		}
	}
	for _, field := range fields {
		if _, exists := value.member(field); !exists {
			candidates = append(candidates, documentFailure(sourceID, codecPointer(pointer, field), "missing_field"))
		}
	}
	return value, true, candidates
}

func signedInteger(value jsonValue) (int64, string, bool) {
	if value.kind != jsonNumber || strings.ContainsAny(value.number, ".eE") {
		return 0, "wrong_type", false
	}
	parsed, err := strconv.ParseInt(value.number, 10, 64)
	if err != nil {
		return 0, "out_of_range", false
	}
	return parsed, "", true
}

func knownMaxLengthLexicalCandidates(migration jsonValue, sourceID string) []failureCandidate {
	operations, exists := migration.member("operations")
	if !exists || operations.kind != jsonArray {
		return nil
	}
	candidates := make([]failureCandidate, 0)
	for operationIndex, operation := range operations.array {
		if operation.kind != jsonObject {
			continue
		}
		kind, exists := operation.member("kind")
		if !exists || kind.kind != jsonString {
			continue
		}
		operationPointer := "/migration/operations/" + strconv.Itoa(operationIndex)
		switch kind.string {
		case "create_model":
			model, exists := operation.member("model")
			if !exists || model.kind != jsonObject {
				continue
			}
			fields, exists := model.member("fields")
			if !exists || fields.kind != jsonArray {
				continue
			}
			for fieldIndex, field := range fields.array {
				pointer := operationPointer + "/model/fields/" + strconv.Itoa(fieldIndex)
				candidates = append(candidates, maxLengthLexicalCandidate(field, sourceID, pointer)...)
			}
		case "add_field":
			field, exists := operation.member("field")
			if exists {
				candidates = append(candidates, maxLengthLexicalCandidate(field, sourceID, operationPointer+"/field")...)
			}
		}
	}
	return candidates
}

func maxLengthLexicalCandidate(field jsonValue, sourceID, pointer string) []failureCandidate {
	if field.kind != jsonObject {
		return nil
	}
	maximum, exists := field.member("max_length")
	if !exists || maximum.kind != jsonNumber || strings.ContainsAny(maximum.number, ".eE") {
		return nil
	}
	if _, err := strconv.ParseInt(maximum.number, 10, 64); err != nil {
		return []failureCandidate{documentFailure(sourceID, pointer+"/max_length", "out_of_range")}
	}
	return nil
}

func formatCandidates(documents []parsedDocument) []failureCandidate {
	candidates := make([]failureCandidate, 0)
	for _, document := range documents {
		if document.formatVersion != DefinitionFormatVersion {
			candidates = append(candidates, formatFailure(document.source.sourceID))
		}
	}
	sortFailureCandidates(candidates)
	return candidates
}

// semanticLimitCandidates implements class-major cap precedence. A known
// dependency cap in any source precedes every operation cap; a known operation
// cap precedes every recognized CreateModel fields cap.
func semanticLimitCandidates(documents []parsedDocument) []failureCandidate {
	dependencies := make([]failureCandidate, 0)
	for _, document := range documents {
		migration, exists := document.root.member("migration")
		if !exists || migration.kind != jsonObject {
			continue
		}
		value, exists := migration.member("dependencies")
		if exists && value.kind == jsonArray && len(value.array) > MaxDependenciesPerMigration {
			app, name := migrationIdentity(migration)
			dependencies = append(dependencies, semanticResourceFailure(
				CodeInvalidOperation,
				document.source.sourceID,
				"/migration/dependencies",
				app,
				name,
				"dependencies_per_migration",
				uint64(MaxDependenciesPerMigration),
				uint64(len(value.array)),
				-1,
			))
		}
	}
	if len(dependencies) != 0 {
		sortFailureCandidates(dependencies)
		return dependencies
	}

	operations := make([]failureCandidate, 0)
	for _, document := range documents {
		migration, exists := document.root.member("migration")
		if !exists || migration.kind != jsonObject {
			continue
		}
		value, exists := migration.member("operations")
		if exists && value.kind == jsonArray && len(value.array) > MaxOperationsPerMigration {
			app, name := migrationIdentity(migration)
			operations = append(operations, semanticResourceFailure(
				CodeInvalidOperation,
				document.source.sourceID,
				"/migration/operations",
				app,
				name,
				"operations_per_migration",
				uint64(MaxOperationsPerMigration),
				uint64(len(value.array)),
				-1,
			))
		}
	}
	if len(operations) != 0 {
		sortFailureCandidates(operations)
		return operations
	}

	fields := make([]failureCandidate, 0)
	for _, document := range documents {
		migration, exists := document.root.member("migration")
		if !exists || migration.kind != jsonObject {
			continue
		}
		operationValues, exists := migration.member("operations")
		if !exists || operationValues.kind != jsonArray {
			continue
		}
		app, name := migrationIdentity(migration)
		for operationIndex, operation := range operationValues.array {
			if operation.kind != jsonObject {
				continue
			}
			kind, exists := operation.member("kind")
			if !exists || kind.kind != jsonString || kind.string != "create_model" {
				continue
			}
			model, exists := operation.member("model")
			if !exists || model.kind != jsonObject {
				continue
			}
			fieldValues, exists := model.member("fields")
			if exists && fieldValues.kind == jsonArray && len(fieldValues.array) > MaxFieldsPerCreateModel {
				fields = append(fields, semanticResourceFailure(
					CodeInvalidIR,
					document.source.sourceID,
					"/migration/operations/"+strconv.Itoa(operationIndex)+"/model/fields",
					app,
					name,
					"fields_per_create_model",
					uint64(MaxFieldsPerCreateModel),
					uint64(len(fieldValues.array)),
					operationIndex,
				))
			}
		}
	}
	sortFailureCandidates(fields)
	return fields
}

func semanticResourceFailure(
	code ErrorCode,
	sourceID string,
	pointer string,
	app string,
	name string,
	limit string,
	maximum uint64,
	actual uint64,
	operationIndex int,
) failureCandidate {
	candidate := resourceFailure(code, "semantic", sourceID, pointer, limit, maximum, actual, operationIndex)
	candidate.context.App = app
	candidate.context.Name = name
	return candidate
}

func migrationIdentity(migration jsonValue) (string, string) {
	app := ""
	name := ""
	if value, exists := migration.member("app"); exists && value.kind == jsonString {
		app = value.string
	}
	if value, exists := migration.member("name"); exists && value.kind == jsonString {
		name = value.string
	}
	return app, name
}

func semanticCandidates(document parsedDocument) []failureCandidate {
	if document.formatVersion != DefinitionFormatVersion {
		return nil
	}
	migration, exists := document.root.member("migration")
	if !exists || migration.kind != jsonObject {
		return []failureCandidate{semanticFailure(CodeInvalidOperation, document.source.sourceID, "/migration", "", "", -1, "invalid_operation")}
	}
	candidates := make([]failureCandidate, 0)
	app := ""
	name := ""
	if value, present := migration.member("app"); present && value.kind == jsonString {
		app = value.string
	} else {
		candidates = append(candidates, semanticFailure(CodeInvalidOperation, document.source.sourceID, "/migration/app", "", "", -1, "invalid_operation"))
	}
	if value, present := migration.member("name"); present && value.kind == jsonString {
		name = value.string
	} else {
		candidates = append(candidates, semanticFailure(CodeInvalidOperation, document.source.sourceID, "/migration/name", app, "", -1, "invalid_operation"))
	}

	dependencies, present := migration.member("dependencies")
	if !present || dependencies.kind != jsonArray {
		candidates = append(candidates, semanticFailure(CodeInvalidOperation, document.source.sourceID, "/migration/dependencies", app, name, -1, "invalid_operation"))
	} else {
		for index, dependency := range dependencies.array {
			pointer := "/migration/dependencies/" + strconv.Itoa(index)
			object, objectOK, faults := semanticObjectCandidates(dependency, []string{"app", "name"}, document.source.sourceID, pointer, app, name, -1, CodeInvalidOperation)
			candidates = append(candidates, faults...)
			if !objectOK {
				continue
			}
			for _, field := range []string{"app", "name"} {
				if value, exists := object.member(field); exists && value.kind != jsonString {
					candidates = append(candidates, semanticFailure(CodeInvalidOperation, document.source.sourceID, pointer+"/"+field, app, name, -1, "invalid_operation"))
				}
			}
		}
	}

	operations, present := migration.member("operations")
	if !present || operations.kind != jsonArray {
		candidates = append(candidates, semanticFailure(CodeInvalidOperation, document.source.sourceID, "/migration/operations", app, name, -1, "invalid_operation"))
	} else {
		for index, operation := range operations.array {
			candidates = append(candidates, collectOperationCandidates(operation, document.source.sourceID, app, name, index)...)
		}
	}
	sortFailureCandidates(candidates)
	return candidates
}

func semanticObjectCandidates(value jsonValue, fields []string, sourceID, pointer, app, name string, operationIndex int, code ErrorCode) (jsonValue, bool, []failureCandidate) {
	if value.kind != jsonObject {
		return jsonValue{}, false, []failureCandidate{semanticFailure(code, sourceID, pointer, app, name, operationIndex, semanticReason(code))}
	}
	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
	}
	candidates := make([]failureCandidate, 0)
	for _, member := range value.object {
		if _, recognized := wanted[member.key]; !recognized {
			candidates = append(candidates, semanticFailure(code, sourceID, codecPointer(pointer, member.key), app, name, operationIndex, semanticReason(code)))
		}
	}
	for _, field := range fields {
		if _, exists := value.member(field); !exists {
			candidates = append(candidates, semanticFailure(code, sourceID, codecPointer(pointer, field), app, name, operationIndex, semanticReason(code)))
		}
	}
	return value, true, candidates
}

func semanticObjectWithOptionalCandidates(
	value jsonValue,
	required []string,
	optional []string,
	sourceID, pointer, app, name string,
	operationIndex int,
	code ErrorCode,
) (jsonValue, bool, []failureCandidate) {
	if value.kind != jsonObject {
		return jsonValue{}, false, []failureCandidate{semanticFailure(code, sourceID, pointer, app, name, operationIndex, semanticReason(code))}
	}
	wanted := make(map[string]struct{}, len(required)+len(optional))
	for _, field := range required {
		wanted[field] = struct{}{}
	}
	for _, field := range optional {
		wanted[field] = struct{}{}
	}
	candidates := make([]failureCandidate, 0)
	for _, member := range value.object {
		if _, recognized := wanted[member.key]; !recognized {
			candidates = append(candidates, semanticFailure(code, sourceID, codecPointer(pointer, member.key), app, name, operationIndex, semanticReason(code)))
		}
	}
	for _, field := range required {
		if _, exists := value.member(field); !exists {
			candidates = append(candidates, semanticFailure(code, sourceID, codecPointer(pointer, field), app, name, operationIndex, semanticReason(code)))
		}
	}
	return value, true, candidates
}

func semanticUnknownCandidates(value jsonValue, fields []string, sourceID, pointer, app, name string, operationIndex int, code ErrorCode) []failureCandidate {
	if value.kind != jsonObject {
		return nil
	}
	wanted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		wanted[field] = struct{}{}
	}
	candidates := make([]failureCandidate, 0)
	for _, member := range value.object {
		if _, recognized := wanted[member.key]; !recognized {
			candidates = append(candidates, semanticFailure(code, sourceID, codecPointer(pointer, member.key), app, name, operationIndex, semanticReason(code)))
		}
	}
	return candidates
}

func semanticReason(code ErrorCode) string {
	switch code {
	case CodeUnsupportedOperation:
		return "unsupported_operation"
	case CodeInvalidIR:
		return "invalid_ir"
	default:
		return "invalid_operation"
	}
}

func collectOperationCandidates(value jsonValue, sourceID, app, name string, operationIndex int) []failureCandidate {
	pointer := "/migration/operations/" + strconv.Itoa(operationIndex)
	if value.kind != jsonObject {
		return []failureCandidate{semanticFailure(CodeInvalidOperation, sourceID, pointer, app, name, operationIndex, "invalid_operation")}
	}
	commonFields := []string{"app_label", "field", "kind", "model", "model_name"}
	candidates := semanticUnknownCandidates(value, commonFields, sourceID, pointer, app, name, operationIndex, CodeInvalidOperation)
	kind, exists := value.member("kind")
	if !exists || kind.kind != jsonString {
		return append(candidates, semanticFailure(CodeInvalidOperation, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_operation"))
	}
	if kind.string != "create_model" && kind.string != "add_field" {
		return append(candidates, semanticFailure(CodeUnsupportedOperation, sourceID, pointer+"/kind", app, name, operationIndex, "unsupported_operation"))
	}

	fields := []string{"app_label", "kind", "model"}
	if kind.string == "add_field" {
		fields = []string{"app_label", "field", "kind", "model_name"}
	}
	object, _, faults := semanticObjectCandidates(value, fields, sourceID, pointer, app, name, operationIndex, CodeInvalidOperation)
	candidates = append(candidates, faults...)
	if appLabel, present := object.member("app_label"); present {
		if appLabel.kind != jsonString || appLabel.string != app {
			candidates = append(candidates, semanticFailure(CodeInvalidOperation, sourceID, pointer+"/app_label", app, name, operationIndex, "invalid_operation"))
		} else if !validAppLabel(appLabel.string) {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/app_label", app, name, operationIndex, "invalid_ir"))
		}
	}

	if kind.string == "create_model" {
		if model, present := object.member("model"); present {
			candidates = append(candidates, collectModelCandidates(model, sourceID, pointer+"/model", app, name, operationIndex)...)
		}
	} else {
		if modelName, present := object.member("model_name"); present {
			if modelName.kind != jsonString || !validAddFieldModelName(modelName.string) {
				candidates = append(candidates, semanticFailure(CodeInvalidOperation, sourceID, pointer+"/model_name", app, name, operationIndex, "invalid_operation"))
			}
		}
		if field, present := object.member("field"); present {
			candidates = append(candidates, collectFieldCandidates(field, sourceID, pointer+"/field", app, name, operationIndex)...)
			if field.kind == jsonObject {
				if fieldKind, exists := field.member("kind"); exists && fieldKind.kind == jsonString &&
					fieldKind.string != string(ir.FieldChar) && fieldKind.string != string(ir.FieldBoolean) &&
					fieldKind.string != string(ir.FieldForeignKey) {
					candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/field/kind", app, name, operationIndex, "invalid_ir"))
				}
				if primaryKey, exists := field.member("primary_key"); exists && primaryKey.kind == jsonBoolean && primaryKey.boolean {
					candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/field/primary_key", app, name, operationIndex, "invalid_ir"))
				}
			}
		}
	}
	return candidates
}

func collectModelCandidates(value jsonValue, sourceID, pointer, app, name string, operationIndex int) []failureCandidate {
	fields := []string{"db_table", "fields", "go_name", "name"}
	object, objectOK, candidates := semanticObjectCandidates(value, fields, sourceID, pointer, app, name, operationIndex, CodeInvalidIR)
	if !objectOK {
		return candidates
	}
	for _, field := range []string{"db_table", "go_name", "name"} {
		child, exists := object.member(field)
		if !exists {
			continue
		}
		valid := child.kind == jsonString
		if valid {
			switch field {
			case "db_table":
				valid = validModelTable(child.string)
			case "go_name":
				valid = validModelGoName(child.string)
			case "name":
				valid = validModelName(child.string)
			}
		}
		if !valid {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir"))
		}
	}
	if child, exists := object.member("fields"); exists {
		if child.kind != jsonArray || len(child.array) == 0 {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/fields", app, name, operationIndex, "invalid_ir"))
		} else {
			for index, field := range child.array {
				candidates = append(candidates, collectFieldCandidates(field, sourceID, pointer+"/fields/"+strconv.Itoa(index), app, name, operationIndex)...)
			}
			candidates = append(candidates, collectModelFieldAggregateCandidates(child.array, sourceID, pointer+"/fields", app, name, operationIndex)...)
		}
	}
	return candidates
}

func collectModelFieldAggregateCandidates(values []jsonValue, sourceID, pointer, app, name string, operationIndex int) []failureCandidate {
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
			{name: "name", seen: seenNames, valid: validFieldName},
			{name: "go_name", seen: seenGoNames, valid: validFieldGoName},
			{name: "column", seen: seenColumns, valid: validFieldColumn},
		} {
			candidate, exists := value.member(member.name)
			if !exists || candidate.kind != jsonString || !member.valid(candidate.string) {
				continue
			}
			if _, duplicate := member.seen[candidate.string]; duplicate {
				aggregateInvalid = true
			}
			member.seen[candidate.string] = struct{}{}
		}
		primaryKey, exists := value.member("primary_key")
		if !exists || primaryKey.kind != jsonBoolean {
			primaryKeysComplete = false
		} else if primaryKey.boolean {
			primaryKeys++
		}
		kind, exists := value.member("kind")
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
	return []failureCandidate{semanticFailure(CodeInvalidIR, sourceID, pointer, app, name, operationIndex, "invalid_ir")}
}

func collectFieldCandidates(value jsonValue, sourceID, pointer, app, name string, operationIndex int) []failureCandidate {
	fields := []string{"column", "default", "go_name", "kind", "max_length", "name", "nullable", "primary_key"}
	object, objectOK, candidates := semanticObjectWithOptionalCandidates(
		value, fields, []string{"relation"}, sourceID, pointer, app, name, operationIndex, CodeInvalidIR,
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
		valid := child.kind == jsonString
		if valid {
			switch field {
			case "column":
				valid = validFieldColumn(child.string)
			case "go_name":
				valid = validFieldGoName(child.string)
			case "name":
				valid = validFieldName(child.string)
			}
		}
		stringValid[field] = valid
		if !valid {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir"))
		}
	}
	booleanValid := make(map[string]bool, 2)
	for _, field := range []string{"nullable", "primary_key"} {
		child, exists := object.member(field)
		if !exists {
			continue
		}
		booleanValid[field] = child.kind == jsonBoolean
		if !booleanValid[field] {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/"+field, app, name, operationIndex, "invalid_ir"))
		}
	}

	maxLengthValid := false
	var maxLength int64
	if maximum, exists := object.member("max_length"); exists {
		parsed, reason, valid := signedInteger(maximum)
		if !valid {
			candidates = append(candidates, semanticFailure(CodeInvalidDocument, sourceID, pointer+"/max_length", app, name, operationIndex, reason))
		} else if parsed < 0 || parsed > maximumWireLength {
			candidates = append(candidates, semanticFailure(CodeInvalidDocument, sourceID, pointer+"/max_length", app, name, operationIndex, "out_of_range"))
		} else {
			maxLengthValid = true
			maxLength = parsed
		}
	}

	defaultValid := false
	var defaultValue *ir.ScalarDefault
	if defaultNode, exists := object.member("default"); exists {
		candidates = append(candidates, collectDefaultCandidates(defaultNode, sourceID, pointer+"/default", app, name, operationIndex)...)
		if decoded, valid := materializeDefault(defaultNode); valid {
			defaultValid = true
			defaultValue = decoded
		}
	}

	if stringValid["kind"] {
		kind, _ := object.member("kind")
		switch ir.FieldKind(kind.string) {
		case ir.FieldAuto:
			if defaultValid && defaultValue != nil {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir"))
			}
			if maxLengthValid && maxLength != 0 {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir"))
			}
			if booleanValid["nullable"] {
				nullable, _ := object.member("nullable")
				if nullable.boolean {
					candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/nullable", app, name, operationIndex, "invalid_ir"))
				}
			}
			if booleanValid["primary_key"] {
				primaryKey, _ := object.member("primary_key")
				if !primaryKey.boolean {
					candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir"))
				}
			}
		case ir.FieldChar:
			if defaultValid && defaultValue != nil && defaultValue.Kind != ir.ScalarString {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir"))
			}
			if defaultValid && defaultValue != nil && defaultValue.Kind == ir.ScalarString && maxLengthValid && int64(utf8.RuneCountInString(defaultValue.String)) > maxLength {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir"))
			}
			if maxLengthValid && maxLength <= 0 {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir"))
			}
			if booleanValid["primary_key"] {
				primaryKey, _ := object.member("primary_key")
				if primaryKey.boolean {
					candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir"))
				}
			}
		case ir.FieldBoolean:
			if defaultValid && defaultValue != nil && defaultValue.Kind != ir.ScalarBoolean {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir"))
			}
			if maxLengthValid && maxLength != 0 {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir"))
			}
			if booleanValid["nullable"] {
				nullable, _ := object.member("nullable")
				if nullable.boolean {
					candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/nullable", app, name, operationIndex, "invalid_ir"))
				}
			}
			if booleanValid["primary_key"] {
				primaryKey, _ := object.member("primary_key")
				if primaryKey.boolean {
					candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir"))
				}
			}
		case ir.FieldForeignKey:
			if defaultValid && defaultValue != nil {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/default", app, name, operationIndex, "invalid_ir"))
			}
			if maxLengthValid && maxLength != 0 {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/max_length", app, name, operationIndex, "invalid_ir"))
			}
			if booleanValid["primary_key"] {
				primaryKey, _ := object.member("primary_key")
				if primaryKey.boolean {
					candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/primary_key", app, name, operationIndex, "invalid_ir"))
				}
			}
		default:
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir"))
		}
	}
	relationNode, hasRelation := object.member("relation")
	kindNode, hasKind := object.member("kind")
	isForeignKey := hasKind && kindNode.kind == jsonString && kindNode.string == string(ir.FieldForeignKey)
	if !hasRelation {
		if isForeignKey {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/relation", app, name, operationIndex, "invalid_ir"))
		}
		return candidates
	}
	if !isForeignKey {
		candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/relation", app, name, operationIndex, "invalid_ir"))
		return candidates
	}
	candidates = append(candidates, collectRelationCandidates(relationNode, sourceID, pointer+"/relation", app, name, operationIndex, object)...)
	return candidates
}

func collectRelationCandidates(value jsonValue, sourceID, pointer, app, name string, operationIndex int, field jsonValue) []failureCandidate {
	object, objectOK, candidates := semanticObjectCandidates(
		value, []string{"cardinality", "on_delete", "reverse", "target"}, sourceID, pointer, app, name, operationIndex, CodeInvalidIR,
	)
	if !objectOK {
		return candidates
	}
	cardinality, hasCardinality := object.member("cardinality")
	if !hasCardinality || cardinality.kind != jsonString || cardinality.string != string(ir.RelationManyToOne) {
		candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/cardinality", app, name, operationIndex, "invalid_ir"))
	}
	onDelete, hasOnDelete := object.member("on_delete")
	if !hasOnDelete || onDelete.kind != jsonString || (onDelete.string != string(ir.DeleteProtect) && onDelete.string != string(ir.DeleteSetNull)) {
		candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/on_delete", app, name, operationIndex, "invalid_ir"))
	} else if onDelete.string == string(ir.DeleteSetNull) {
		nullable, exists := field.member("nullable")
		if !exists || nullable.kind != jsonBoolean || !nullable.boolean {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/on_delete", app, name, operationIndex, "invalid_ir"))
		}
	}
	target, hasTarget := object.member("target")
	if hasTarget {
		targetObject, targetOK, faults := semanticObjectCandidates(
			target, []string{"app_label", "model_name"}, sourceID, pointer+"/target", app, name, operationIndex, CodeInvalidIR,
		)
		candidates = append(candidates, faults...)
		if targetOK {
			if targetApp, exists := targetObject.member("app_label"); !exists || targetApp.kind != jsonString || !validAppLabel(targetApp.string) {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/target/app_label", app, name, operationIndex, "invalid_ir"))
			}
			if targetModel, exists := targetObject.member("model_name"); !exists || targetModel.kind != jsonString || !validModelName(targetModel.string) {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/target/model_name", app, name, operationIndex, "invalid_ir"))
			}
		}
	}
	reverse, hasReverse := object.member("reverse")
	if hasReverse {
		reverseObject, reverseOK, faults := semanticObjectCandidates(
			reverse, []string{"disabled", "name"}, sourceID, pointer+"/reverse", app, name, operationIndex, CodeInvalidIR,
		)
		candidates = append(candidates, faults...)
		if reverseOK {
			nameNode, hasName := reverseObject.member("name")
			disabledNode, hasDisabled := reverseObject.member("disabled")
			if !hasName || nameNode.kind != jsonString || !hasDisabled || disabledNode.kind != jsonBoolean ||
				disabledNode.boolean == (nameNode.string != "") || (nameNode.string != "" && !validFieldName(nameNode.string)) {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/reverse", app, name, operationIndex, "invalid_ir"))
			}
		}
	}
	return candidates
}

func collectDefaultCandidates(value jsonValue, sourceID, pointer, app, name string, operationIndex int) []failureCandidate {
	if value.kind == jsonNull {
		return nil
	}
	if value.kind != jsonObject {
		return []failureCandidate{semanticFailure(CodeInvalidIR, sourceID, pointer, app, name, operationIndex, "invalid_ir")}
	}
	commonFields := []string{"boolean", "integer", "kind", "string"}
	candidates := semanticUnknownCandidates(value, commonFields, sourceID, pointer, app, name, operationIndex, CodeInvalidIR)
	kind, exists := value.member("kind")
	if !exists || kind.kind != jsonString {
		return append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir"))
	}
	switch kind.string {
	case string(ir.ScalarString):
		object, _, faults := semanticObjectCandidates(value, []string{"kind", "string"}, sourceID, pointer, app, name, operationIndex, CodeInvalidIR)
		candidates = append(candidates, faults...)
		if child, present := object.member("string"); present && child.kind != jsonString {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/string", app, name, operationIndex, "invalid_ir"))
		}
	case string(ir.ScalarBoolean):
		object, _, faults := semanticObjectCandidates(value, []string{"boolean", "kind"}, sourceID, pointer, app, name, operationIndex, CodeInvalidIR)
		candidates = append(candidates, faults...)
		if child, present := object.member("boolean"); present && child.kind != jsonBoolean {
			candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/boolean", app, name, operationIndex, "invalid_ir"))
		}
	case string(ir.ScalarInteger):
		object, _, faults := semanticObjectCandidates(value, []string{"integer", "kind"}, sourceID, pointer, app, name, operationIndex, CodeInvalidIR)
		candidates = append(candidates, faults...)
		if child, present := object.member("integer"); present {
			if _, _, valid := signedInteger(child); !valid {
				candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/integer", app, name, operationIndex, "invalid_ir"))
			}
		}
	default:
		candidates = append(candidates, semanticFailure(CodeInvalidIR, sourceID, pointer+"/kind", app, name, operationIndex, "invalid_ir"))
	}
	return candidates
}

func decodeDocument(document parsedDocument) (decodedDocument, int, []failureCandidate) {
	if document.formatVersion != DefinitionFormatVersion {
		return decodedDocument{}, 0, []failureCandidate{formatFailure(document.source.sourceID)}
	}
	migration, exists := document.root.member("migration")
	if !exists || migration.kind != jsonObject {
		return decodedDocument{}, 0, []failureCandidate{semanticFailure(CodeInvalidOperation, document.source.sourceID, "/migration", "", "", -1, "invalid_operation")}
	}
	appNode, _ := migration.member("app")
	nameNode, _ := migration.member("name")
	app := appNode.string
	name := nameNode.string

	dependenciesNode, _ := migration.member("dependencies")
	dependencies := make([]migrations.MigrationKey, 0, len(dependenciesNode.array))
	for index, dependencyNode := range dependenciesNode.array {
		dependencyApp, dependencyName, ok := materializeDependency(dependencyNode)
		if !ok {
			pointer := "/migration/dependencies/" + strconv.Itoa(index)
			return decodedDocument{}, 0, []failureCandidate{semanticFailure(CodeInvalidOperation, document.source.sourceID, pointer, app, name, -1, "invalid_operation")}
		}
		dependencies = append(dependencies, migrations.MigrationKey{App: dependencyApp, Name: dependencyName})
	}
	sort.Slice(dependencies, func(left, right int) bool {
		if dependencies[left].App != dependencies[right].App {
			return dependencies[left].App < dependencies[right].App
		}
		return dependencies[left].Name < dependencies[right].Name
	})

	operationsNode, _ := migration.member("operations")
	operations := make([]migrations.Operation, 0, len(operationsNode.array))
	for index, operationNode := range operationsNode.array {
		operation, ok := materializeOperation(operationNode, app)
		if !ok {
			pointer := "/migration/operations/" + strconv.Itoa(index)
			return decodedDocument{}, len(operations), []failureCandidate{semanticFailure(CodeInvalidOperation, document.source.sourceID, pointer, app, name, index, "invalid_operation")}
		}
		operations = append(operations, operation)
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
	}, len(operations), nil
}

func materializeDependency(value jsonValue) (string, string, bool) {
	if value.kind != jsonObject {
		return "", "", false
	}
	app, appExists := value.member("app")
	name, nameExists := value.member("name")
	if !appExists || !nameExists || app.kind != jsonString || name.kind != jsonString {
		return "", "", false
	}
	return app.string, name.string, true
}

func materializeOperation(value jsonValue, migrationApp string) (migrations.Operation, bool) {
	if value.kind != jsonObject {
		return nil, false
	}
	kind, kindExists := value.member("kind")
	appLabel, appExists := value.member("app_label")
	if !kindExists || !appExists || kind.kind != jsonString || appLabel.kind != jsonString || appLabel.string != migrationApp {
		return nil, false
	}
	switch kind.string {
	case "create_model":
		modelValue, exists := value.member("model")
		if !exists {
			return nil, false
		}
		model, valid := materializeModel(modelValue)
		if !valid || !fullyNormalizedCreateModel(appLabel.string, model) {
			return nil, false
		}
		return migrations.CreateModel{AppLabel: appLabel.string, Model: model.Clone()}, true
	case "add_field":
		modelName, modelExists := value.member("model_name")
		fieldValue, fieldExists := value.member("field")
		if !modelExists || !fieldExists || modelName.kind != jsonString || !validAddFieldModelName(modelName.string) {
			return nil, false
		}
		field, valid := materializeField(fieldValue)
		if !valid || !fullyNormalizedAddField(appLabel.string, field) {
			return nil, false
		}
		return migrations.AddField{AppLabel: appLabel.string, ModelName: modelName.string, Field: cloneField(field)}, true
	default:
		return nil, false
	}
}

func materializeModel(value jsonValue) (ir.Model, bool) {
	if value.kind != jsonObject {
		return ir.Model{}, false
	}
	name, nameExists := value.member("name")
	goName, goNameExists := value.member("go_name")
	dbTable, tableExists := value.member("db_table")
	fields, fieldsExist := value.member("fields")
	if !nameExists || !goNameExists || !tableExists || !fieldsExist || name.kind != jsonString || goName.kind != jsonString || dbTable.kind != jsonString || fields.kind != jsonArray {
		return ir.Model{}, false
	}
	decodedFields := make([]ir.Field, len(fields.array))
	for index, fieldValue := range fields.array {
		field, valid := materializeField(fieldValue)
		if !valid {
			return ir.Model{}, false
		}
		decodedFields[index] = field
	}
	return ir.Model{Name: name.string, GoName: goName.string, DBTable: dbTable.string, Fields: decodedFields}, true
}

func materializeField(value jsonValue) (ir.Field, bool) {
	if value.kind != jsonObject {
		return ir.Field{}, false
	}
	name, nameExists := value.member("name")
	goName, goNameExists := value.member("go_name")
	column, columnExists := value.member("column")
	kind, kindExists := value.member("kind")
	primaryKey, primaryKeyExists := value.member("primary_key")
	nullable, nullableExists := value.member("nullable")
	maxLength, maximumExists := value.member("max_length")
	defaultValue, defaultExists := value.member("default")
	if !nameExists || !goNameExists || !columnExists || !kindExists || !primaryKeyExists || !nullableExists || !maximumExists || !defaultExists ||
		name.kind != jsonString || goName.kind != jsonString || column.kind != jsonString || kind.kind != jsonString ||
		primaryKey.kind != jsonBoolean || nullable.kind != jsonBoolean {
		return ir.Field{}, false
	}
	parsedMaximum, _, valid := signedInteger(maxLength)
	if !valid || parsedMaximum < 0 || parsedMaximum > maximumWireLength {
		return ir.Field{}, false
	}
	decodedDefault, valid := materializeDefault(defaultValue)
	if !valid {
		return ir.Field{}, false
	}
	field := ir.Field{
		Name:       name.string,
		GoName:     goName.string,
		Column:     column.string,
		Kind:       ir.FieldKind(kind.string),
		PrimaryKey: primaryKey.boolean,
		Nullable:   nullable.boolean,
		MaxLength:  int(parsedMaximum),
		Default:    decodedDefault,
	}
	if relationValue, exists := value.member("relation"); exists {
		relation, valid := materializeRelation(relationValue)
		if !valid {
			return ir.Field{}, false
		}
		field.Relation = &relation
	}
	return field, true
}

func materializeRelation(value jsonValue) (ir.ForeignKeyRelation, bool) {
	if value.kind != jsonObject {
		return ir.ForeignKeyRelation{}, false
	}
	target, targetExists := value.member("target")
	cardinality, cardinalityExists := value.member("cardinality")
	reverse, reverseExists := value.member("reverse")
	onDelete, onDeleteExists := value.member("on_delete")
	if !targetExists || !cardinalityExists || !reverseExists || !onDeleteExists || target.kind != jsonObject ||
		cardinality.kind != jsonString || reverse.kind != jsonObject || onDelete.kind != jsonString {
		return ir.ForeignKeyRelation{}, false
	}
	targetApp, targetAppExists := target.member("app_label")
	targetModel, targetModelExists := target.member("model_name")
	reverseName, reverseNameExists := reverse.member("name")
	reverseDisabled, reverseDisabledExists := reverse.member("disabled")
	if !targetAppExists || !targetModelExists || targetApp.kind != jsonString || targetModel.kind != jsonString ||
		!reverseNameExists || !reverseDisabledExists || reverseName.kind != jsonString || reverseDisabled.kind != jsonBoolean {
		return ir.ForeignKeyRelation{}, false
	}
	return ir.ForeignKeyRelation{
		Target:      ir.ModelIdentity{AppLabel: targetApp.string, ModelName: targetModel.string},
		Cardinality: ir.RelationCardinality(cardinality.string),
		Reverse:     ir.ReverseRelation{Name: reverseName.string, Disabled: reverseDisabled.boolean},
		OnDelete:    ir.DeletePolicy(onDelete.string),
	}, true
}

func materializeDefault(value jsonValue) (*ir.ScalarDefault, bool) {
	if value.kind == jsonNull {
		return nil, true
	}
	if value.kind != jsonObject {
		return nil, false
	}
	kind, exists := value.member("kind")
	if !exists || kind.kind != jsonString {
		return nil, false
	}
	switch kind.string {
	case string(ir.ScalarString):
		payload, exists := value.member("string")
		if !exists || payload.kind != jsonString {
			return nil, false
		}
		return &ir.ScalarDefault{Kind: ir.ScalarString, String: payload.string}, true
	case string(ir.ScalarBoolean):
		payload, exists := value.member("boolean")
		if !exists || payload.kind != jsonBoolean {
			return nil, false
		}
		return &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: payload.boolean}, true
	case string(ir.ScalarInteger):
		payload, exists := value.member("integer")
		if !exists {
			return nil, false
		}
		integer, _, valid := signedInteger(payload)
		if !valid {
			return nil, false
		}
		return &ir.ScalarDefault{Kind: ir.ScalarInteger, Integer: integer}, true
	default:
		return nil, false
	}
}

func codecPointer(parent, token string) string {
	escaped := strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
	if parent == "" {
		return "/" + escaped
	}
	return parent + "/" + escaped
}
