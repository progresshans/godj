package definitionload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

const (
	emptyDefinitionDigest    = "sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450"
	nonemptyDefinitionDigest = "sha256:07e61f8d956002cff0d7fe2db10c16ea4a30829e9f0ced09c69c40ff2c2399bc"
)

func TestDefinitionLoaderCanonicalDigestGoldens(t *testing.T) {
	t.Parallel()

	empty, emptyMetrics, err := loadDefinitions(nil)
	if err != nil {
		t.Fatalf("load empty source: %v", err)
	}
	if empty.Digest != emptyDefinitionDigest {
		t.Fatalf("empty digest = %s, want %s", empty.Digest, emptyDefinitionDigest)
	}
	if len(empty.Definitions) != 0 || emptyMetrics.DefinitionsPublished != 0 || emptyMetrics.DefinitionSetsPublished != 1 {
		t.Fatalf("empty publication = definitions:%d metrics:%+v", len(empty.Definitions), emptyMetrics)
	}
	if emptyMetrics.PlannerConstruction != 1 {
		t.Fatalf("empty NewPlanner calls = %d, want 1", emptyMetrics.PlannerConstruction)
	}

	goldenDocument := map[string]any{
		"compatibility": compatibilityDocument(),
		"producer":      map[string]any{"name": "godj-example-generator", "version": "0.1.0"},
		"migration": map[string]any{
			"app":          "alpha",
			"name":         "0001_initial",
			"dependencies": []any{},
			"operations": []any{
				map[string]any{
					"kind":      "create_model",
					"app_label": "alpha",
					"model": map[string]any{
						"name":     "widget",
						"go_name":  "Widget",
						"db_table": "alpha_widget",
						"fields":   []any{autoFieldDocument()},
					},
				},
			},
		},
	}
	nonempty, metrics, err := loadDefinitions([]sourceDocument{{SourceID: "opaque-golden", Document: encodeDocument(t, goldenDocument)}})
	if err != nil {
		t.Fatalf("load nonempty golden: %v", err)
	}
	if nonempty.Digest != nonemptyDefinitionDigest {
		t.Fatalf("nonempty digest = %s, want %s", nonempty.Digest, nonemptyDefinitionDigest)
	}
	canonical, err := canonicalDefinitionSet(nonempty.Definitions)
	if err != nil {
		t.Fatalf("canonicalize nonempty golden: %v", err)
	}
	if len(canonical) != 470 {
		t.Fatalf("nonempty canonical bytes = %d, want 470\n%s", len(canonical), canonical)
	}
	if metrics.PlannerConstruction != 1 || metrics.DefinitionsPublished != 1 || metrics.DefinitionSetsPublished != 1 {
		t.Fatalf("nonempty metrics = %+v", metrics)
	}
}

func TestDefinitionLoaderSnapshotsCallerInputAndKeepsSourceIDDiagnosticOnly(t *testing.T) {
	t.Parallel()

	raw := encodeDocument(t, rootDocument())
	sources := []sourceDocument{{SourceID: "opaque-z", Document: raw}}
	loaded, _, err := loadDefinitions(sources)
	if err != nil {
		t.Fatalf("load definitions: %v", err)
	}
	wantDigest := loaded.Digest
	wantDefinitions := cloneMigrations(loaded.Definitions)

	for index := range raw {
		raw[index] = 'x'
	}
	sources[0].SourceID = "mutated-label"
	sources[0].Document = []byte(`{}`)
	if loaded.Digest != wantDigest || !reflect.DeepEqual(loaded.Definitions, wantDefinitions) {
		t.Fatal("caller mutation changed the loader-owned snapshot")
	}
	if loaded.Sources[0].SourceID != "opaque-z" {
		t.Fatalf("snapshot SourceID = %q", loaded.Sources[0].SourceID)
	}

	relabeled := rootDocument()
	relabeled["producer"].(map[string]any)["version"] = "99.0.0"
	reloaded, _, err := loadDefinitions([]sourceDocument{{SourceID: "different-diagnostic-label", Document: encodeDocument(t, relabeled)}})
	if err != nil {
		t.Fatalf("load relabeled definitions: %v", err)
	}
	if reloaded.Digest != wantDigest || !reflect.DeepEqual(reloaded.Definitions, wantDefinitions) {
		t.Fatal("SourceID/producer relabel changed semantic definitions or digest")
	}

	loaded.Definitions[0].Name = "caller_mutated_result"
	again, _, err := loadDefinitions([]sourceDocument{{SourceID: "again", Document: encodeDocument(t, rootDocument())}})
	if err != nil {
		t.Fatalf("reload after returned-result mutation: %v", err)
	}
	if again.Definitions[0].Name != "0001_initial" {
		t.Fatal("returned result mutation leaked into a later load")
	}
}

func TestDefinitionLoaderSourceIDValidationAndCanonicalPrecedence(t *testing.T) {
	t.Parallel()

	valid := encodeDocument(t, rootDocument())
	tests := []struct {
		name    string
		sources []sourceDocument
		reason  string
	}{
		{name: "empty", sources: []sourceDocument{{SourceID: "", Document: valid}}, reason: "empty_source_id"},
		{name: "invalid UTF-8", sources: []sourceDocument{{SourceID: string([]byte{0xff}), Document: valid}}, reason: "invalid_source_id_utf8"},
		{name: "duplicate", sources: []sourceDocument{{SourceID: "same", Document: valid}, {SourceID: "same", Document: valid}}, reason: "duplicate_source_id"},
		{
			name: "reason rank beats caller order",
			sources: []sourceDocument{
				{SourceID: string([]byte{0xff}), Document: valid},
				{SourceID: "", Document: valid},
			},
			reason: "empty_source_id",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded, metrics, err := loadDefinitions(test.sources)
			assertAtomicFailure(t, loaded, metrics, err)
			definitionErr := requireDefinitionError(t, err)
			if definitionErr.Code != "invalid_definition_source" || definitionErr.Stage != "source" || definitionErr.Reason != test.reason {
				t.Fatalf("source error = %+v, want reason %s", definitionErr, test.reason)
			}
			if metrics.PlannerConstruction != 0 {
				t.Fatalf("source failure NewPlanner calls = %d", metrics.PlannerConstruction)
			}
		})
	}
}

func TestDefinitionLoaderRejectsFullDocumentFramingBeforeCompatibility(t *testing.T) {
	t.Parallel()

	valid := encodeDocument(t, rootDocument())
	duplicate := bytes.Replace(valid, []byte(`"name":"0001_initial"`), []byte(`"name":"0001_initial","name":"shadow"`), 1)
	deepDuplicate := bytes.Replace(valid, []byte(`"column":"title"`), []byte(`"column":"title","column":"shadow"`), 1)
	escapedDuplicate := bytes.Replace(valid, []byte(`"name":"0001_initial"`), []byte(`"name":"0001_initial","\u006eame":"shadow"`), 1)
	loneSurrogate := bytes.Replace(valid, []byte(`"version":"0.1.0"`), []byte(`"version":"\uD800"`), 1)
	decimal := bytes.Replace(valid, []byte(`"definition_format":1`), []byte(`"definition_format":1.0`), 1)
	invalidUTF8 := append([]byte(nil), valid...)
	producerOffset := bytes.Index(invalidUTF8, []byte("godj-test"))
	if producerOffset < 0 {
		t.Fatal("test fixture has no producer marker")
	}
	invalidUTF8[producerOffset] = 0xff

	tests := []struct {
		name    string
		payload []byte
		reason  string
		pointer string
	}{
		{name: "invalid UTF-8", payload: invalidUTF8, reason: "invalid_utf8", pointer: ""},
		{name: "syntax", payload: []byte(`{"compatibility":`), reason: "syntax", pointer: ""},
		{name: "BOM", payload: append([]byte{0xef, 0xbb, 0xbf}, valid...), reason: "syntax", pointer: ""},
		{name: "any-depth duplicate", payload: duplicate, reason: "duplicate_key", pointer: "/migration/name"},
		{name: "deep duplicate", payload: deepDuplicate, reason: "duplicate_key", pointer: "/migration/operations/0/model/fields/1/column"},
		{name: "escaped-equivalent duplicate", payload: escapedDuplicate, reason: "duplicate_key", pointer: "/migration/name"},
		{name: "lone surrogate", payload: loneSurrogate, reason: "lone_surrogate", pointer: "/producer/version"},
		{name: "non-integer", payload: decimal, reason: "wrong_type", pointer: "/compatibility/definition_format"},
		{name: "trailing value", payload: append(append([]byte(nil), valid...), []byte(` {}`)...), reason: "trailing_value", pointer: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded, metrics, err := loadDefinitions([]sourceDocument{{SourceID: "source", Document: test.payload}})
			assertAtomicFailure(t, loaded, metrics, err)
			definitionErr := requireDefinitionError(t, err)
			if definitionErr.Code != "invalid_definition_document" || definitionErr.Reason != test.reason || definitionErr.JSONPointer != test.pointer {
				t.Fatalf("document error = %+v, want reason=%s pointer=%q", definitionErr, test.reason, test.pointer)
			}
			if metrics.OperationsDecoded != 0 || metrics.PlannerConstruction != 0 {
				t.Fatalf("framing failure crossed semantic/graph boundary: %+v", metrics)
			}
		})
	}

	paired := bytes.Replace(valid, []byte(`"version":"0.1.0"`), []byte(`"version":"\uD83D\uDE00"`), 1)
	if _, _, err := loadDefinitions([]sourceDocument{{SourceID: "paired", Document: paired}}); err != nil {
		t.Fatalf("valid surrogate pair rejected: %v", err)
	}

	versionMismatch := rootDocument()
	versionMismatch["compatibility"].(map[string]any)["definition_format"] = 2
	loaded, metrics, err := loadDefinitions([]sourceDocument{
		{SourceID: "a-version", Document: encodeDocument(t, versionMismatch)},
		{SourceID: "z-syntax", Document: []byte(`{`)},
	})
	assertAtomicFailure(t, loaded, metrics, err)
	if got := requireDefinitionError(t, err); got.Reason != "syntax" || got.SourceID != "z-syntax" {
		t.Fatalf("document stage did not precede all-source tuple: %+v", got)
	}
}

func TestDefinitionLoaderChecksAllSourceCompatibilityBeforeSemanticDecode(t *testing.T) {
	t.Parallel()

	coordinates := []struct {
		name   string
		code   string
		values []int
	}{
		{"definition_format", "definition_format_incompatible", []int{0, 2}},
		{"loader_abi", "loader_abi_incompatible", []int{0, 2}},
		{"operation_codec", "operation_codec_incompatible", []int{0, 2}},
		{"schema_ir", "schema_ir_incompatible", []int{1, 3}},
	}
	for _, coordinate := range coordinates {
		for _, value := range coordinate.values {
			name := fmt.Sprintf("%s=%d", coordinate.name, value)
			t.Run(name, func(t *testing.T) {
				document := rootDocument()
				document["compatibility"].(map[string]any)[coordinate.name] = value
				document["migration"].(map[string]any)["operations"] = []any{map[string]any{"kind": "run_python"}}
				loaded, metrics, err := loadDefinitions([]sourceDocument{{SourceID: "version", Document: encodeDocument(t, document)}})
				assertAtomicFailure(t, loaded, metrics, err)
				definitionErr := requireDefinitionError(t, err)
				if definitionErr.Code != coordinate.code || definitionErr.Stage != "compatibility" {
					t.Fatalf("tuple error = %+v, want %s", definitionErr, coordinate.code)
				}
				if metrics.OperationsDecoded != 0 || metrics.PlannerConstruction != 0 {
					t.Fatalf("tuple failure decoded/published work: %+v", metrics)
				}
			})
		}
	}

	for _, producerField := range []string{"name", "version"} {
		t.Run("empty producer "+producerField+" precedes definition format mismatch", func(t *testing.T) {
			document := rootDocument()
			document["compatibility"].(map[string]any)["definition_format"] = 2
			document["producer"].(map[string]any)[producerField] = ""
			loaded, metrics, err := loadDefinitions([]sourceDocument{{SourceID: "producer", Document: encodeDocument(t, document)}})
			assertAtomicFailure(t, loaded, metrics, err)
			definitionErr := requireDefinitionError(t, err)
			if definitionErr.Code != "invalid_definition_document" || definitionErr.Stage != "document" || definitionErr.Reason != "wrong_type" || definitionErr.JSONPointer != "/producer/"+producerField {
				t.Fatalf("empty producer error = %+v", definitionErr)
			}
			if metrics.PlannerConstruction != 0 || metrics.DefinitionsPublished != 0 || metrics.DefinitionSetsPublished != 0 {
				t.Fatalf("empty producer failure crossed graph/publication boundary: %+v", metrics)
			}
		})
	}

	definitionMismatch := rootDocument()
	definitionMismatch["compatibility"].(map[string]any)["definition_format"] = 2
	definitionMismatch["migration"].(map[string]any)["name"] = "0002_definition"
	schemaMismatch := rootDocument()
	schemaMismatch["compatibility"].(map[string]any)["schema_ir"] = 3
	schemaMismatch["migration"].(map[string]any)["name"] = "0001_schema"
	_, _, err := loadDefinitions([]sourceDocument{
		{SourceID: "a-schema", Document: encodeDocument(t, schemaMismatch)},
		{SourceID: "z-definition", Document: encodeDocument(t, definitionMismatch)},
	})
	if got := requireDefinitionError(t, err); got.Code != "definition_format_incompatible" || got.SourceID != "z-definition" {
		t.Fatalf("coordinate-major tuple precedence = %+v", got)
	}

	outOfRange := bytes.Replace(encodeDocument(t, rootDocument()), []byte(`"definition_format":1`), []byte(`"definition_format":9223372036854775808`), 1)
	loaded, metrics, err := loadDefinitions([]sourceDocument{{SourceID: "range", Document: outOfRange}})
	assertAtomicFailure(t, loaded, metrics, err)
	if got := requireDefinitionError(t, err); got.Code != "invalid_definition_document" || got.Reason != "out_of_range" {
		t.Fatalf("out-of-range version error = %+v", got)
	}

	for _, test := range []struct {
		name      string
		document  map[string]any
		oldLength string
		newLength string
		pointer   string
	}{
		{
			name:      "CreateModel positive signed-int64 overflow",
			document:  rootDocument(),
			oldLength: `"max_length":64`,
			newLength: `"max_length":9223372036854775808`,
			pointer:   "/migration/operations/0/model/fields/1/max_length",
		},
		{
			name:      "AddField negative signed-int64 overflow",
			document:  tailDocument(),
			oldLength: `"max_length":0`,
			newLength: `"max_length":-9223372036854775809`,
			pointer:   "/migration/operations/0/field/max_length",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.document["compatibility"].(map[string]any)["definition_format"] = 2
			payload := encodeDocument(t, test.document)
			payload = bytes.Replace(payload, []byte(test.oldLength), []byte(test.newLength), 1)
			loaded, metrics, err := loadDefinitions([]sourceDocument{{SourceID: "overflow", Document: payload}})
			assertAtomicFailure(t, loaded, metrics, err)
			got := requireDefinitionError(t, err)
			if got.Code != "invalid_definition_document" || got.Stage != "document" || got.Reason != "out_of_range" || got.JSONPointer != test.pointer {
				t.Fatalf("max_length lexical overflow = %+v", got)
			}
			if metrics.OperationsDecoded != 0 || metrics.PlannerConstruction != 0 {
				t.Fatalf("max_length framing overflow crossed tuple/semantic boundary: %+v", metrics)
			}
		})
	}

	semanticRange := rootDocument()
	semanticRange["compatibility"].(map[string]any)["definition_format"] = 2
	semanticRangePayload := bytes.Replace(encodeDocument(t, semanticRange), []byte(`"max_length":64`), []byte(`"max_length":2147483648`), 1)
	_, _, err = loadDefinitions([]sourceDocument{{SourceID: "semantic-range", Document: semanticRangePayload}})
	if got := requireDefinitionError(t, err); got.Code != "definition_format_incompatible" {
		t.Fatalf("semantic 32-bit range ran before compatibility: %+v", got)
	}

	unsupportedOverflow := rootDocument()
	unsupportedOverflow["compatibility"].(map[string]any)["definition_format"] = 2
	unsupportedOverflow["migration"].(map[string]any)["operations"] = []any{
		map[string]any{
			"field": charFieldDocument("summary", "Summary", 64, false, nil),
			"kind":  "run_python",
		},
	}
	unsupportedOverflowPayload := bytes.Replace(encodeDocument(t, unsupportedOverflow), []byte(`"max_length":64`), []byte(`"max_length":9223372036854775808`), 1)
	loaded, metrics, err = loadDefinitions([]sourceDocument{{SourceID: "unsupported-overflow", Document: unsupportedOverflowPayload}})
	assertAtomicFailure(t, loaded, metrics, err)
	if got := requireDefinitionError(t, err); got.Code != "definition_format_incompatible" || got.Stage != "compatibility" || got.JSONPointer != "/compatibility/definition_format" {
		t.Fatalf("unsupported payload was interpreted before tuple handshake: %+v", got)
	}
	if metrics.OperationsDecoded != 0 || metrics.PlannerConstruction != 0 {
		t.Fatalf("unsupported payload tuple failure crossed semantic/graph boundary: %+v", metrics)
	}

	unsupportedOverflow["compatibility"].(map[string]any)["definition_format"] = 1
	unsupportedOverflowPayload = bytes.Replace(encodeDocument(t, unsupportedOverflow), []byte(`"max_length":64`), []byte(`"max_length":9223372036854775808`), 1)
	loaded, metrics, err = loadDefinitions([]sourceDocument{{SourceID: "unsupported-overflow", Document: unsupportedOverflowPayload}})
	assertAtomicFailure(t, loaded, metrics, err)
	if got := requireDefinitionError(t, err); got.Code != "unsupported_definition_operation" || got.Stage != "semantic" || got.JSONPointer != "/migration/operations/0/kind" {
		t.Fatalf("unsupported payload nested field was interpreted as codec v1: %+v", got)
	}
}

func TestDefinitionLoaderClosedCodecAndFullyNormalizedIR(t *testing.T) {
	t.Parallel()

	sources := definitionSources(t)
	loaded, metrics, err := loadDefinitions(sources)
	if err != nil {
		t.Fatalf("load CreateModel/AddField batch: %v", err)
	}
	if len(loaded.Definitions) != 2 || metrics.OperationsDecoded != 3 || metrics.PlannerConstruction != 1 {
		t.Fatalf("loaded batch shape = definitions:%d metrics:%+v", len(loaded.Definitions), metrics)
	}
	if loaded.Definitions[0].Key() != (migrations.MigrationKey{App: "alpha", Name: "0001_initial"}) || loaded.Definitions[1].Key() != (migrations.MigrationKey{App: "alpha", Name: "0002_fields"}) {
		t.Fatalf("canonical definition order = %v, %v", loaded.Definitions[0].Key(), loaded.Definitions[1].Key())
	}
	booleanAdd, ok := loaded.Definitions[1].Operations[0].(migrations.AddField)
	if !ok || booleanAdd.Field.Default == nil || booleanAdd.Field.Default.Kind != ir.ScalarBoolean || booleanAdd.Field.Default.Boolean {
		t.Fatalf("false BooleanField default was not lossless: %#v", loaded.Definitions[1].Operations[0])
	}

	inertDocument := rootDocument()
	model := createOperation(inertDocument)["model"].(map[string]any)
	model["fields"].([]any)[1].(map[string]any)["default"] = map[string]any{"kind": "string", "string": `print("still data")`}
	inert, _, err := loadDefinitions([]sourceDocument{{SourceID: "inert", Document: encodeDocument(t, inertDocument)}})
	if err != nil {
		t.Fatalf("load inert code-like string: %v", err)
	}
	created := inert.Definitions[0].Operations[0].(migrations.CreateModel)
	if got := created.Model.Fields[1].Default.String; got != `print("still data")` {
		t.Fatalf("inert string = %q", got)
	}

	collisionTail := tailDocument()
	collisionOperation := collisionTail["migration"].(map[string]any)["operations"].([]any)[0].(map[string]any)
	collisionOperation["field"] = charFieldDocument("_godj_loader_pk", "GodjLoaderPK", 12, false, nil)
	collisionTail["migration"].(map[string]any)["operations"] = []any{collisionOperation}
	if _, _, err := loadDefinitions([]sourceDocument{
		{SourceID: "root", Document: encodeDocument(t, rootDocument())},
		{SourceID: "collision", Document: encodeDocument(t, collisionTail)},
	}); err != nil {
		t.Fatalf("synthetic AddField collision adjustment rejected valid candidate: %v", err)
	}
}

func TestDefinitionLoaderRejectsUnknownImplicitAndExecutablePayloads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mutate    func(map[string]any)
		wantCode  string
		wantStage string
	}{
		{
			name:     "unknown outer field",
			mutate:   func(document map[string]any) { document["extension"] = true },
			wantCode: "invalid_definition_document", wantStage: "document",
		},
		{
			name:     "implicit model table",
			mutate:   func(document map[string]any) { createOperation(document)["model"].(map[string]any)["db_table"] = "" },
			wantCode: "invalid_definition_ir", wantStage: "semantic",
		},
		{
			name: "implicit field column",
			mutate: func(document map[string]any) {
				createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)["column"] = ""
			},
			wantCode: "invalid_definition_ir", wantStage: "semantic",
		},
		{
			name: "omitted explicit default",
			mutate: func(document map[string]any) {
				delete(createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any), "default")
			},
			wantCode: "invalid_definition_ir", wantStage: "semantic",
		},
		{
			name: "integer default arm",
			mutate: func(document map[string]any) {
				createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)["default"] = map[string]any{"kind": "integer", "integer": 1}
			},
			wantCode: "invalid_definition_ir", wantStage: "semantic",
		},
		{
			name: "executable discriminator",
			mutate: func(document map[string]any) {
				document["migration"].(map[string]any)["operations"] = []any{map[string]any{"kind": "run_python"}}
			},
			wantCode: "unsupported_definition_operation", wantStage: "semantic",
		},
		{
			name:     "executable-bearing extension key",
			mutate:   func(document map[string]any) { createOperation(document)["callback"] = "pkg.Func" },
			wantCode: "invalid_definition_operation", wantStage: "semantic",
		},
		{
			name: "invalid schema app",
			mutate: func(document map[string]any) {
				document["migration"].(map[string]any)["app"] = "Alpha"
				createOperation(document)["app_label"] = "Alpha"
			},
			wantCode: "invalid_definition_ir", wantStage: "semantic",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := rootDocument()
			test.mutate(document)
			loaded, metrics, err := loadDefinitions([]sourceDocument{{SourceID: "mutation", Document: encodeDocument(t, document)}})
			assertAtomicFailure(t, loaded, metrics, err)
			definitionErr := requireDefinitionError(t, err)
			if definitionErr.Code != test.wantCode || definitionErr.Stage != test.wantStage {
				t.Fatalf("mutation error = %+v, want %s/%s", definitionErr, test.wantStage, test.wantCode)
			}
			if metrics.DefinitionsPublished != 0 || metrics.DefinitionSetsPublished != 0 {
				t.Fatalf("mutation published a partial set: %+v", metrics)
			}
		})
	}

	addFieldMutations := []struct {
		name   string
		mutate func(map[string]any)
		code   string
	}{
		{
			name: "AddField auto",
			mutate: func(operation map[string]any) {
				operation["field"] = autoFieldDocument()
			},
			code: "invalid_definition_ir",
		},
		{
			name: "AddField primary key",
			mutate: func(operation map[string]any) {
				operation["field"].(map[string]any)["primary_key"] = true
			},
			code: "invalid_definition_ir",
		},
		{
			name:   "AddField invalid model name",
			mutate: func(operation map[string]any) { operation["model_name"] = "Entry" },
			code:   "invalid_definition_operation",
		},
	}
	for _, test := range addFieldMutations {
		t.Run(test.name, func(t *testing.T) {
			document := tailDocument()
			operation := document["migration"].(map[string]any)["operations"].([]any)[0].(map[string]any)
			test.mutate(operation)
			loaded, metrics, err := loadDefinitions([]sourceDocument{{SourceID: "add", Document: encodeDocument(t, document)}})
			assertAtomicFailure(t, loaded, metrics, err)
			if got := requireDefinitionError(t, err); got.Code != test.code {
				t.Fatalf("AddField error = %+v, want %s", got, test.code)
			}
		})
	}
}

func TestDefinitionLoaderWireLengthRangeAndRuneSemantics(t *testing.T) {
	t.Parallel()

	maximum := rootDocument()
	maximumField := createOperation(maximum)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
	maximumField["max_length"] = int64(1<<31 - 1)
	maximumField["default"] = map[string]any{"kind": "string", "string": "ok"}
	if _, _, err := loadDefinitions([]sourceDocument{{SourceID: "maximum", Document: encodeDocument(t, maximum)}}); err != nil {
		t.Fatalf("maximum cross-platform max_length rejected: %v", err)
	}

	runeLength := rootDocument()
	runeField := createOperation(runeLength)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
	runeField["max_length"] = 1
	runeField["default"] = map[string]any{"kind": "string", "string": "🙂"}
	if _, _, err := loadDefinitions([]sourceDocument{{SourceID: "rune", Document: encodeDocument(t, runeLength)}}); err != nil {
		t.Fatalf("one-rune multibyte default rejected: %v", err)
	}

	for _, value := range []int64{-1, 1 << 31} {
		t.Run(fmt.Sprintf("out_of_range_%d", value), func(t *testing.T) {
			document := rootDocument()
			createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)["max_length"] = value
			loaded, metrics, err := loadDefinitions([]sourceDocument{{SourceID: "length", Document: encodeDocument(t, document)}})
			assertAtomicFailure(t, loaded, metrics, err)
			if got := requireDefinitionError(t, err); got.Code != "invalid_definition_document" || got.Reason != "out_of_range" {
				t.Fatalf("max_length range error = %+v", got)
			}
		})
	}
}

func TestDefinitionLoaderSelectsSemanticErrorsByCanonicalJSONPointer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mutate      func(map[string]any)
		wantCode    string
		wantPointer string
	}{
		{
			name: "unknown key precedes unsupported discriminator",
			mutate: func(document map[string]any) {
				document["migration"].(map[string]any)["operations"] = []any{
					map[string]any{"aaa": true, "kind": "run_python"},
				}
			},
			wantCode:    "invalid_definition_operation",
			wantPointer: "/migration/operations/0/aaa",
		},
		{
			name: "model candidate precedes operation candidate",
			mutate: func(document map[string]any) {
				operation := createOperation(document)
				operation["zzz"] = true
				operation["model"].(map[string]any)["aaa"] = true
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/aaa",
		},
		{
			name: "field candidate precedes model candidate",
			mutate: func(document map[string]any) {
				model := createOperation(document)["model"].(map[string]any)
				model["zzz"] = true
				model["fields"].([]any)[1].(map[string]any)["aaa"] = true
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/1/aaa",
		},
		{
			name: "normalized model fault precedes nested unknown field",
			mutate: func(document map[string]any) {
				model := createOperation(document)["model"].(map[string]any)
				model["db_table"] = ""
				model["zzz"] = true
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/db_table",
		},
		{
			name: "normalized AddField fault precedes field unknown",
			mutate: func(document map[string]any) {
				field := charFieldDocument("summary", "Summary", 10, false, nil)
				field["column"] = ""
				field["zzz"] = true
				document["migration"].(map[string]any)["operations"] = []any{
					map[string]any{"app_label": "alpha", "field": field, "kind": "add_field", "model_name": "entry"},
				}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/field/column",
		},
		{
			name: "model invariant precedes nested field type fault",
			mutate: func(document map[string]any) {
				model := createOperation(document)["model"].(map[string]any)
				model["db_table"] = ""
				model["fields"].([]any)[1].(map[string]any)["go_name"] = 7
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/db_table",
		},
		{
			name: "AddField invariant precedes nested default type fault",
			mutate: func(document map[string]any) {
				field := charFieldDocument("summary", "Summary", 10, false, map[string]any{"kind": "string", "string": 7})
				field["column"] = ""
				document["migration"].(map[string]any)["operations"] = []any{
					map[string]any{"app_label": "alpha", "field": field, "kind": "add_field", "model_name": "entry"},
				}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/field/column",
		},
		{
			name: "field value fault precedes later missing member",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
				field["column"] = "Invalid-Column"
				delete(field, "primary_key")
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/1/column",
		},
		{
			name: "AddField auto restriction precedes field unknown",
			mutate: func(document map[string]any) {
				field := autoFieldDocument()
				field["zzz"] = true
				document["migration"].(map[string]any)["operations"] = []any{
					map[string]any{"app_label": "alpha", "field": field, "kind": "add_field", "model_name": "entry"},
				}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/field/kind",
		},
		{
			name: "AddField primary key restriction precedes field unknown",
			mutate: func(document map[string]any) {
				field := charFieldDocument("summary", "Summary", 10, false, nil)
				field["primary_key"] = true
				field["zzz"] = true
				document["migration"].(map[string]any)["operations"] = []any{
					map[string]any{"app_label": "alpha", "field": field, "kind": "add_field", "model_name": "entry"},
				}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/field/primary_key",
		},
		{
			name: "model no-auto aggregate precedes model unknown",
			mutate: func(document map[string]any) {
				model := createOperation(document)["model"].(map[string]any)
				model["fields"] = []any{
					charFieldDocument("first", "First", 10, false, nil),
					charFieldDocument("second", "Second", 10, false, nil),
				}
				model["zzz"] = true
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "model no-auto aggregate precedes nested unknown",
			mutate: func(document map[string]any) {
				model := createOperation(document)["model"].(map[string]any)
				first := charFieldDocument("first", "First", 10, false, nil)
				first["zzz"] = true
				model["fields"] = []any{first, charFieldDocument("second", "Second", 10, false, nil)}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "model no-auto aggregate ignores missing primary key",
			mutate: func(document map[string]any) {
				model := createOperation(document)["model"].(map[string]any)
				first := charFieldDocument("first", "First", 10, false, nil)
				delete(first, "primary_key")
				model["fields"] = []any{first, charFieldDocument("second", "Second", 10, false, nil)}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "model primary key aggregate ignores invalid kind",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
				field["kind"] = "custom"
				field["primary_key"] = true
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "duplicate field name aggregate precedes invalid go name",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
				field["name"] = "id"
				field["go_name"] = 7
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "duplicate field go name aggregate precedes invalid column",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
				field["go_name"] = "ID"
				field["column"] = 7
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "duplicate field column aggregate precedes invalid name",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
				field["column"] = "id"
				field["name"] = 7
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "duplicate field name pair survives another missing name",
			mutate: func(document map[string]any) {
				second := charFieldDocument("id", "Second", 10, false, nil)
				third := charFieldDocument("third", "Third", 10, false, nil)
				delete(third, "name")
				createOperation(document)["model"].(map[string]any)["fields"] = []any{autoFieldDocument(), second, third}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "duplicate field go name pair survives another wrong go name",
			mutate: func(document map[string]any) {
				second := charFieldDocument("second", "ID", 10, false, nil)
				third := charFieldDocument("third", "Third", 10, false, nil)
				third["go_name"] = 7
				createOperation(document)["model"].(map[string]any)["fields"] = []any{autoFieldDocument(), second, third}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "duplicate field column pair survives another missing column",
			mutate: func(document map[string]any) {
				second := charFieldDocument("second", "Second", 10, false, nil)
				second["column"] = "id"
				third := charFieldDocument("third", "Third", 10, false, nil)
				delete(third, "column")
				createOperation(document)["model"].(map[string]any)["fields"] = []any{autoFieldDocument(), second, third}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "two known primary keys survive another unknown primary key",
			mutate: func(document map[string]any) {
				second := charFieldDocument("second", "Second", 10, false, nil)
				second["primary_key"] = true
				third := charFieldDocument("third", "Third", 10, false, nil)
				delete(third, "primary_key")
				createOperation(document)["model"].(map[string]any)["fields"] = []any{autoFieldDocument(), second, third}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields",
		},
		{
			name: "one known primary key plus unknown remains undecidable",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
				delete(field, "primary_key")
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/1/primary_key",
		},
		{
			name: "zero known primary keys plus unknown remains undecidable",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[0].(map[string]any)
				delete(field, "primary_key")
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/0/primary_key",
		},
		{
			name: "no-auto with unknown kind remains undecidable",
			mutate: func(document map[string]any) {
				first := charFieldDocument("first", "First", 10, false, nil)
				first["primary_key"] = true
				delete(first, "kind")
				createOperation(document)["model"].(map[string]any)["fields"] = []any{first, charFieldDocument("second", "Second", 10, false, nil)}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/0/kind",
		},
		{
			name: "char overlength default invariant precedes default unknown",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
				field["max_length"] = 1
				field["default"] = map[string]any{"kind": "string", "string": "too long", "zzz": true}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/1/default",
		},
		{
			name: "char boolean default invariant precedes default unknown",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
				field["default"] = map[string]any{"boolean": false, "kind": "boolean", "zzz": true}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/1/default",
		},
		{
			name: "boolean string default invariant precedes default unknown",
			mutate: func(document map[string]any) {
				field := booleanFieldDocument("enabled", "Enabled", map[string]any{"kind": "string", "string": "yes", "zzz": true})
				createOperation(document)["model"].(map[string]any)["fields"].([]any)[1] = field
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/1/default",
		},
		{
			name: "auto nonnull default invariant precedes default unknown",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[0].(map[string]any)
				field["default"] = map[string]any{"kind": "string", "string": "id", "zzz": true}
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/0/default",
		},
		{
			name: "auto default invariant ignores missing max length",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[0].(map[string]any)
				field["default"] = map[string]any{"kind": "string", "string": "id"}
				delete(field, "max_length")
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/0/default",
		},
		{
			name: "boolean default invariant ignores missing nullable",
			mutate: func(document map[string]any) {
				field := booleanFieldDocument("enabled", "Enabled", map[string]any{"kind": "string", "string": "yes"})
				delete(field, "nullable")
				createOperation(document)["model"].(map[string]any)["fields"].([]any)[1] = field
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/1/default",
		},
		{
			name: "char overlength default invariant ignores nullable type fault",
			mutate: func(document map[string]any) {
				field := createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
				field["max_length"] = 1
				field["default"] = map[string]any{"kind": "string", "string": "too long"}
				field["nullable"] = "false"
			},
			wantCode:    "invalid_definition_ir",
			wantPointer: "/migration/operations/0/model/fields/1/default",
		},
		{
			name: "unsupported discriminator does not interpret nested field",
			mutate: func(document map[string]any) {
				field := charFieldDocument("summary", "Summary", 10, false, nil)
				field["aaa"] = true
				document["migration"].(map[string]any)["operations"] = []any{
					map[string]any{"field": field, "kind": "run_python"},
				}
			},
			wantCode:    "unsupported_definition_operation",
			wantPointer: "/migration/operations/0/kind",
		},
		{
			name: "RFC6901 lexical array index beats traversal order",
			mutate: func(document map[string]any) {
				base := createOperation(document)
				operations := make([]any, 11)
				for index := range operations {
					copyValue := cloneJSONValue(t, base).(map[string]any)
					copyValue["model"].(map[string]any)["name"] = fmt.Sprintf("entry_%d", index)
					copyValue["model"].(map[string]any)["go_name"] = fmt.Sprintf("Entry%d", index)
					copyValue["model"].(map[string]any)["db_table"] = fmt.Sprintf("godj_definition_entry_%d", index)
					operations[index] = copyValue
				}
				operations[2].(map[string]any)["aaa"] = true
				operations[10].(map[string]any)["aaa"] = true
				document["migration"].(map[string]any)["operations"] = operations
			},
			wantCode:    "invalid_definition_operation",
			wantPointer: "/migration/operations/10/aaa",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := rootDocument()
			test.mutate(document)
			loaded, metrics, err := loadDefinitions([]sourceDocument{{SourceID: "semantic", Document: encodeDocument(t, document)}})
			assertAtomicFailure(t, loaded, metrics, err)
			got := requireDefinitionError(t, err)
			if got.Code != test.wantCode || got.JSONPointer != test.wantPointer {
				t.Fatalf("canonical semantic error = %+v, want code=%s pointer=%s", got, test.wantCode, test.wantPointer)
			}
			if metrics.PlannerConstruction != 0 {
				t.Fatalf("semantic candidate reached NewPlanner: %+v", metrics)
			}
		})
	}

	firstSource := rootDocument()
	createOperation(firstSource)["zzz"] = true
	secondSource := rootDocument()
	secondSource["migration"].(map[string]any)["name"] = "0002_second"
	createOperation(secondSource)["aaa"] = true
	loaded, metrics, err := loadDefinitions([]sourceDocument{
		{SourceID: "z-second", Document: encodeDocument(t, secondSource)},
		{SourceID: "a-first", Document: encodeDocument(t, firstSource)},
	})
	assertAtomicFailure(t, loaded, metrics, err)
	if got := requireDefinitionError(t, err); got.SourceID != "a-first" || got.JSONPointer != "/migration/operations/0/zzz" {
		t.Fatalf("SourceID-major semantic precedence = %+v", got)
	}
}

func TestDefinitionLoaderAtomicGraphValidationAndSinglePlannerConstruction(t *testing.T) {
	t.Parallel()

	first := rootDocument()
	second := rootDocument()
	second["producer"].(map[string]any)["version"] = "2.0.0"
	loaded, metrics, err := loadDefinitions([]sourceDocument{
		{SourceID: "z-duplicate", Document: encodeDocument(t, second)},
		{SourceID: "a-original", Document: encodeDocument(t, first)},
	})
	assertAtomicFailure(t, loaded, metrics, err)
	var planningErr *migrations.PlanningError
	if !errors.As(err, &planningErr) || planningErr.Code != migrations.CodeDuplicateNode || planningErr.Category != migrations.CategoryGraph {
		t.Fatalf("duplicate identity error = %#v", err)
	}
	if metrics.PlannerConstruction != 1 {
		t.Fatalf("duplicate identity NewPlanner calls = %d, want 1", metrics.PlannerConstruction)
	}

	invalid := tailDocument()
	invalid["migration"].(map[string]any)["operations"].([]any)[0].(map[string]any)["kind"] = "run_python"
	loaded, metrics, err = loadDefinitions([]sourceDocument{
		{SourceID: "a-valid", Document: encodeDocument(t, rootDocument())},
		{SourceID: "b-invalid", Document: encodeDocument(t, invalid)},
	})
	assertAtomicFailure(t, loaded, metrics, err)
	if metrics.PlannerConstruction != 0 {
		t.Fatalf("semantic failure NewPlanner calls = %d, want 0", metrics.PlannerConstruction)
	}
	if got := requireDefinitionError(t, err); got.Code != "unsupported_definition_operation" {
		t.Fatalf("semantic atomic failure = %+v", got)
	}
}

func TestDefinitionLoaderDelegatesCompleteGraphTaxonomyToOneNewPlannerCall(t *testing.T) {
	t.Parallel()

	root := migrations.MigrationKey{App: "alpha", Name: "0001_root"}
	child := migrations.MigrationKey{App: "alpha", Name: "0002_child"}
	missing := migrations.MigrationKey{App: "alpha", Name: "0999_missing"}
	invalidParent := migrations.MigrationKey{Name: "0000_invalid"}
	cycleA := migrations.MigrationKey{App: "alpha", Name: "0100_cycle_a"}
	cycleB := migrations.MigrationKey{App: "alpha", Name: "0101_cycle_b"}

	tests := []struct {
		name        string
		sources     []sourceDocument
		code        migrations.ErrorCode
		node        migrations.MigrationKey
		related     migrations.MigrationKey
		cycleMember []migrations.MigrationKey
	}{
		{
			name:    "invalid_node",
			sources: graphSources(t, graphDefinition{"invalid", migrations.MigrationKey{Name: "0001_invalid"}, nil}),
			code:    migrations.CodeInvalidNode,
			node:    migrations.MigrationKey{Name: "0001_invalid"},
		},
		{
			name: "duplicate_node",
			sources: graphSources(t,
				graphDefinition{"original", root, nil},
				graphDefinition{"duplicate", root, nil},
			),
			code: migrations.CodeDuplicateNode,
			node: root,
		},
		{
			name:    "invalid_dependency",
			sources: graphSources(t, graphDefinition{"child", child, []migrations.MigrationKey{invalidParent}}),
			code:    migrations.CodeInvalidDependency,
			node:    child,
			related: invalidParent,
		},
		{
			name: "duplicate_dependency",
			sources: graphSources(t,
				graphDefinition{"root", root, nil},
				graphDefinition{"child", child, []migrations.MigrationKey{root, root}},
			),
			code:    migrations.CodeDuplicateDependency,
			node:    child,
			related: root,
		},
		{
			name:    "dependency_not_found",
			sources: graphSources(t, graphDefinition{"child", child, []migrations.MigrationKey{missing}}),
			code:    migrations.CodeDependencyNotFound,
			node:    child,
			related: missing,
		},
		{
			name: "dependency_cycle",
			sources: graphSources(t,
				graphDefinition{"cycle-a", cycleA, []migrations.MigrationKey{cycleB}},
				graphDefinition{"cycle-b", cycleB, []migrations.MigrationKey{cycleA}},
			),
			code:        migrations.CodeDependencyCycle,
			cycleMember: []migrations.MigrationKey{cycleA, cycleB},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded, metrics, err := loadDefinitions(test.sources)
			assertAtomicFailure(t, loaded, metrics, err)
			var planningErr *migrations.PlanningError
			if !errors.As(err, &planningErr) {
				t.Fatalf("graph error = %T %v, want *migrations.PlanningError", err, err)
			}
			if planningErr.Category != migrations.CategoryGraph || planningErr.Code != test.code || planningErr.Node != test.node || planningErr.Related != test.related || !reflect.DeepEqual(planningErr.Members(), test.cycleMember) {
				t.Fatalf("graph diagnostic = category:%s code:%s node:%v related:%v members:%v", planningErr.Category, planningErr.Code, planningErr.Node, planningErr.Related, planningErr.Members())
			}
			if metrics.PlannerConstruction != 1 || metrics.DefinitionsPublished != 0 || metrics.DefinitionSetsPublished != 0 {
				t.Fatalf("graph failure atomicity/call count = %+v", metrics)
			}
		})
	}
}

func TestDefinitionLoaderUsesExistingGraphFaultPrecedence(t *testing.T) {
	t.Parallel()

	root := migrations.MigrationKey{App: "alpha", Name: "0001_root"}
	child := migrations.MigrationKey{App: "alpha", Name: "0002_child"}
	missing := migrations.MigrationKey{App: "alpha", Name: "0999_missing"}
	invalidParent := migrations.MigrationKey{Name: "0000_invalid"}
	cycleA := migrations.MigrationKey{App: "alpha", Name: "0100_cycle_a"}
	cycleB := migrations.MigrationKey{App: "alpha", Name: "0101_cycle_b"}

	tests := []struct {
		name    string
		sources []sourceDocument
		code    migrations.ErrorCode
	}{
		{
			name: "invalid node before duplicate node",
			sources: graphSources(t,
				graphDefinition{"invalid", migrations.MigrationKey{Name: "invalid"}, nil},
				graphDefinition{"root-a", root, nil},
				graphDefinition{"root-b", root, nil},
			),
			code: migrations.CodeInvalidNode,
		},
		{
			name: "duplicate node before invalid dependency",
			sources: graphSources(t,
				graphDefinition{"root-a", root, nil},
				graphDefinition{"root-b", root, nil},
				graphDefinition{"child", child, []migrations.MigrationKey{invalidParent}},
			),
			code: migrations.CodeDuplicateNode,
		},
		{
			name: "invalid dependency before duplicate dependency",
			sources: graphSources(t,
				graphDefinition{"root", root, nil},
				graphDefinition{"child", child, []migrations.MigrationKey{root, root, invalidParent}},
			),
			code: migrations.CodeInvalidDependency,
		},
		{
			name: "duplicate dependency before missing dependency",
			sources: graphSources(t,
				graphDefinition{"root", root, nil},
				graphDefinition{"child", child, []migrations.MigrationKey{root, root, missing}},
			),
			code: migrations.CodeDuplicateDependency,
		},
		{
			name: "missing dependency before cycle",
			sources: graphSources(t,
				graphDefinition{"cycle-a", cycleA, []migrations.MigrationKey{cycleB}},
				graphDefinition{"cycle-b", cycleB, []migrations.MigrationKey{cycleA}},
				graphDefinition{"child", child, []migrations.MigrationKey{missing}},
			),
			code: migrations.CodeDependencyNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded, metrics, err := loadDefinitions(test.sources)
			assertAtomicFailure(t, loaded, metrics, err)
			var planningErr *migrations.PlanningError
			if !errors.As(err, &planningErr) || planningErr.Code != test.code {
				t.Fatalf("combined graph error = %#v, want %s", err, test.code)
			}
			if metrics.PlannerConstruction != 1 || metrics.DefinitionsPublished != 0 || metrics.DefinitionSetsPublished != 0 {
				t.Fatalf("combined graph failure atomicity/call count = %+v", metrics)
			}
		})
	}
}

func TestDefinitionLoaderCanonicalPermutationAndOperationOrder(t *testing.T) {
	t.Parallel()

	baseline, _, err := loadDefinitions(definitionSources(t))
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	root := rootDocument()
	root["producer"].(map[string]any)["version"] = "different"
	equivalent, _, err := loadDefinitions([]sourceDocument{
		{SourceID: "relabel-root", Document: encodeDocumentIndented(t, root)},
		{SourceID: "relabel-tail", Document: encodeDocument(t, tailDocument())},
	})
	if err != nil {
		t.Fatalf("load equivalent permutation: %v", err)
	}
	if equivalent.Digest != baseline.Digest || !reflect.DeepEqual(equivalent.Definitions, baseline.Definitions) {
		t.Fatal("syntax/source/input permutation changed semantic set")
	}

	reorderedTail := tailDocument()
	operations := reorderedTail["migration"].(map[string]any)["operations"].([]any)
	operations[0], operations[1] = operations[1], operations[0]
	reordered, _, err := loadDefinitions([]sourceDocument{
		{SourceID: "root", Document: encodeDocument(t, rootDocument())},
		{SourceID: "tail", Document: encodeDocument(t, reorderedTail)},
	})
	if err != nil {
		t.Fatalf("load operation-reordered set: %v", err)
	}
	if reordered.Digest == baseline.Digest {
		t.Fatal("operation-order mutation did not change digest")
	}

	fieldReorderedRoot := rootDocument()
	fields := createOperation(fieldReorderedRoot)["model"].(map[string]any)["fields"].([]any)
	fields[0], fields[1] = fields[1], fields[0]
	fieldReordered, _, err := loadDefinitions([]sourceDocument{
		{SourceID: "root", Document: encodeDocument(t, fieldReorderedRoot)},
		{SourceID: "tail", Document: encodeDocument(t, tailDocument())},
	})
	if err != nil {
		t.Fatalf("load field-reordered set: %v", err)
	}
	if fieldReordered.Digest == baseline.Digest {
		t.Fatal("field-order mutation did not change digest")
	}

	keyOrderRoot := rootDocument()
	compatibilityBytes := encodeValue(t, keyOrderRoot["compatibility"])
	migrationBytes := encodeValue(t, keyOrderRoot["migration"])
	producerBytes := encodeValue(t, keyOrderRoot["producer"])
	keyOrderPayload := []byte(fmt.Sprintf(`{"producer":%s,"migration":%s,"compatibility":%s}`, producerBytes, migrationBytes, compatibilityBytes))
	keyOrderEquivalent, _, err := loadDefinitions([]sourceDocument{
		{SourceID: "root", Document: keyOrderPayload},
		{SourceID: "tail", Document: encodeDocument(t, tailDocument())},
	})
	if err != nil {
		t.Fatalf("load object-key-reordered set: %v", err)
	}
	if keyOrderEquivalent.Digest != baseline.Digest {
		t.Fatal("object-key order changed semantic digest")
	}
}

func TestDefinitionLoaderConcurrentRepeatedDeterminism(t *testing.T) {
	sources := definitionSources(t)
	baseline, _, err := loadDefinitions(sources)
	if err != nil {
		t.Fatalf("load deterministic baseline: %v", err)
	}
	wantDefinitions := cloneMigrations(baseline.Definitions)

	const goroutines = 24
	const repetitions = 12
	start := make(chan struct{})
	errCh := make(chan error, goroutines)
	var wait sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for iteration := 0; iteration < repetitions; iteration++ {
				loaded, metrics, loadErr := loadDefinitions(sources)
				if loadErr != nil {
					errCh <- loadErr
					return
				}
				if loaded.Digest != baseline.Digest || !reflect.DeepEqual(loaded.Definitions, wantDefinitions) || metrics.PlannerConstruction != 1 {
					errCh <- fmt.Errorf("nondeterministic result digest=%s metrics=%+v", loaded.Digest, metrics)
					return
				}
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func assertAtomicFailure(t *testing.T, loaded loadedDefinitionSet, metrics loadMetrics, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("load succeeded, want atomic failure")
	}
	if loaded.Digest != "" || loaded.Definitions != nil || loaded.Sources != nil {
		t.Fatalf("failure returned partial result: %#v", loaded)
	}
	if metrics.DefinitionsPublished != 0 || metrics.DefinitionSetsPublished != 0 {
		t.Fatalf("failure published partial definitions: %+v", metrics)
	}
}

func requireDefinitionError(t *testing.T, err error) *definitionError {
	t.Helper()
	var definitionErr *definitionError
	if !errors.As(err, &definitionErr) {
		t.Fatalf("error = %T %v, want *definitionError", err, err)
	}
	return definitionErr
}

func compatibilityDocument() map[string]any {
	return map[string]any{
		"definition_format": 1,
		"loader_abi":        1,
		"operation_codec":   1,
		"schema_ir":         2,
	}
}

func autoFieldDocument() map[string]any {
	return map[string]any{
		"name":        "id",
		"go_name":     "ID",
		"column":      "id",
		"kind":        "auto",
		"primary_key": true,
		"nullable":    false,
		"max_length":  0,
		"default":     nil,
	}
}

func charFieldDocument(name, goName string, maxLength int, nullable bool, defaultValue any) map[string]any {
	return map[string]any{
		"name":        name,
		"go_name":     goName,
		"column":      name,
		"kind":        "char",
		"primary_key": false,
		"nullable":    nullable,
		"max_length":  maxLength,
		"default":     defaultValue,
	}
}

func booleanFieldDocument(name, goName string, defaultValue any) map[string]any {
	return map[string]any{
		"name":        name,
		"go_name":     goName,
		"column":      name,
		"kind":        "boolean",
		"primary_key": false,
		"nullable":    false,
		"max_length":  0,
		"default":     defaultValue,
	}
}

func rootDocument() map[string]any {
	return map[string]any{
		"compatibility": compatibilityDocument(),
		"producer":      map[string]any{"name": "godj-test", "version": "0.1.0"},
		"migration": map[string]any{
			"app":          "alpha",
			"name":         "0001_initial",
			"dependencies": []any{},
			"operations": []any{
				map[string]any{
					"kind":      "create_model",
					"app_label": "alpha",
					"model": map[string]any{
						"name":     "entry",
						"go_name":  "Entry",
						"db_table": "godj_definition_entry",
						"fields": []any{
							autoFieldDocument(),
							charFieldDocument("title", "Title", 64, false, map[string]any{"kind": "string", "string": "untitled"}),
						},
					},
				},
			},
		},
	}
}

func tailDocument() map[string]any {
	return map[string]any{
		"compatibility": compatibilityDocument(),
		"producer":      map[string]any{"name": "godj-test", "version": "0.1.0"},
		"migration": map[string]any{
			"app":          "alpha",
			"name":         "0002_fields",
			"dependencies": []any{map[string]any{"app": "alpha", "name": "0001_initial"}},
			"operations": []any{
				map[string]any{
					"kind":       "add_field",
					"app_label":  "alpha",
					"model_name": "entry",
					"field":      booleanFieldDocument("published", "Published", map[string]any{"kind": "boolean", "boolean": false}),
				},
				map[string]any{
					"kind":       "add_field",
					"app_label":  "alpha",
					"model_name": "entry",
					"field":      charFieldDocument("summary", "Summary", 255, true, nil),
				},
			},
		},
	}
}

func createOperation(document map[string]any) map[string]any {
	return document["migration"].(map[string]any)["operations"].([]any)[0].(map[string]any)
}

func definitionSources(t *testing.T) []sourceDocument {
	t.Helper()
	return []sourceDocument{
		{SourceID: "opaque-z-root", Document: encodeDocument(t, rootDocument())},
		{SourceID: "opaque-a-tail", Document: encodeDocumentIndented(t, tailDocument())},
	}
}

type graphDefinition struct {
	sourceID     string
	key          migrations.MigrationKey
	dependencies []migrations.MigrationKey
}

func graphSources(t *testing.T, definitions ...graphDefinition) []sourceDocument {
	t.Helper()
	sources := make([]sourceDocument, len(definitions))
	for index, definition := range definitions {
		dependencies := make([]any, len(definition.dependencies))
		for dependencyIndex, dependency := range definition.dependencies {
			dependencies[dependencyIndex] = map[string]any{"app": dependency.App, "name": dependency.Name}
		}
		document := map[string]any{
			"compatibility": compatibilityDocument(),
			"producer":      map[string]any{"name": "godj-graph-proof", "version": "0.1.0"},
			"migration": map[string]any{
				"app":          definition.key.App,
				"name":         definition.key.Name,
				"dependencies": dependencies,
				"operations":   []any{},
			},
		}
		sources[index] = sourceDocument{SourceID: definition.sourceID, Document: encodeDocument(t, document)}
	}
	return sources
}

func encodeDocument(t *testing.T, document map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	return encoded
}

func encodeDocumentIndented(t *testing.T, document map[string]any) []byte {
	t.Helper()
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("marshal indented document: %v", err)
	}
	return encoded
}

func encodeValue(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON value: %v", err)
	}
	return encoded
}

func cloneJSONValue(t *testing.T, value any) any {
	t.Helper()
	encoded := encodeValue(t, value)
	var clone any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatalf("clone JSON value: %v", err)
	}
	return clone
}

func TestCanonicalJSONSubsetEscapesOnlyRequiredCodePoints(t *testing.T) {
	t.Parallel()
	value := "<>&/\u2028\u2029\x00\b\t\n\f\r\\\""
	encoded, err := appendCanonicalString(nil, value)
	if err != nil {
		t.Fatal(err)
	}
	want := "\"<>&/\u2028\u2029\\u0000\\b\\t\\n\\f\\r\\\\\\\"\""
	if string(encoded) != want {
		t.Fatalf("canonical string = %q, want %q", encoded, want)
	}
	if strings.Contains(string(encoded), `\u003c`) || strings.Contains(string(encoded), `\u2028`) {
		t.Fatalf("canonical subset used Go HTML/JavaScript escaping: %s", encoded)
	}
}
