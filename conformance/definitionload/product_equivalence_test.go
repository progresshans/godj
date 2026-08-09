package definitionload

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	productdefinition "github.com/progresshans/godj/migrations/definition"
)

// TestProductLoaderMatchesTheIndependentContractProof keeps the old
// contract-only candidate as an independent comparator. It deliberately calls
// neither the public lifecycle handoff nor NewPlanner directly; those exact
// call-site and lifecycle gates remain owned by the product and scope tests.
func TestProductLoaderMatchesTheIndependentContractProof(t *testing.T) {
	canonical := definitionSources(t)
	root := encodeDocument(t, rootDocument())

	duplicateKey := bytes.Replace(
		append([]byte(nil), root...),
		[]byte(`"name":"0001_initial"`),
		[]byte(`"name":"0001_initial","name":"shadow"`),
		1,
	)
	loneSurrogate := bytes.Replace(
		append([]byte(nil), root...),
		[]byte(`"version":"0.1.0"`),
		[]byte(`"version":"\uD800"`),
		1,
	)
	decimalTuple := bytes.Replace(
		append([]byte(nil), root...),
		[]byte(`"definition_format":1`),
		[]byte(`"definition_format":1.0`),
		1,
	)

	tupleMismatch := rootDocument()
	tupleMismatch["compatibility"].(map[string]any)["schema_ir"] = 3
	unsupported := rootDocument()
	unsupported["migration"].(map[string]any)["operations"] = []any{map[string]any{
		"aaa":  true,
		"kind": "run_python",
	}}
	invalidOperation := rootDocument()
	invalidOperation["migration"].(map[string]any)["operations"] = []any{map[string]any{}}
	invalidIR := rootDocument()
	createOperation(invalidIR)["model"].(map[string]any)["db_table"] = ""

	addFieldRestriction := tailDocument()
	operation := createOperation(addFieldRestriction)
	operation["field"].(map[string]any)["kind"] = "auto"
	operation["field"].(map[string]any)["zzz"] = true

	duplicateGraph := graphSources(t,
		graphDefinition{sourceID: "a-original", key: migrations.MigrationKey{App: "alpha", Name: "0001_initial"}},
		graphDefinition{sourceID: "z-duplicate", key: migrations.MigrationKey{App: "alpha", Name: "0001_initial"}},
	)
	missingDependency := graphSources(t,
		graphDefinition{
			sourceID:     "child",
			key:          migrations.MigrationKey{App: "alpha", Name: "0002_child"},
			dependencies: []migrations.MigrationKey{{App: "alpha", Name: "0001_missing"}},
		},
	)
	invalidNode := graphSources(t,
		graphDefinition{sourceID: "invalid", key: migrations.MigrationKey{Name: "0001_invalid"}},
	)
	invalidDependency := graphSources(t,
		graphDefinition{
			sourceID:     "child",
			key:          migrations.MigrationKey{App: "alpha", Name: "0002_child"},
			dependencies: []migrations.MigrationKey{{Name: "0001_invalid"}},
		},
	)
	duplicateDependency := graphSources(t,
		graphDefinition{sourceID: "root", key: migrations.MigrationKey{App: "alpha", Name: "0001_root"}},
		graphDefinition{
			sourceID: "child",
			key:      migrations.MigrationKey{App: "alpha", Name: "0002_child"},
			dependencies: []migrations.MigrationKey{
				{App: "alpha", Name: "0001_root"},
				{App: "alpha", Name: "0001_root"},
			},
		},
	)
	cycle := graphSources(t,
		graphDefinition{
			sourceID:     "cycle-a",
			key:          migrations.MigrationKey{App: "alpha", Name: "0100_cycle_a"},
			dependencies: []migrations.MigrationKey{{App: "alpha", Name: "0101_cycle_b"}},
		},
		graphDefinition{
			sourceID:     "cycle-b",
			key:          migrations.MigrationKey{App: "alpha", Name: "0101_cycle_b"},
			dependencies: []migrations.MigrationKey{{App: "alpha", Name: "0100_cycle_a"}},
		},
	)

	tests := []struct {
		name    string
		sources []sourceDocument
	}{
		{name: "empty"},
		{name: "canonical batch", sources: canonical},
		{name: "empty source ID", sources: []sourceDocument{{SourceID: "", Document: root}}},
		{name: "invalid source ID UTF-8", sources: []sourceDocument{{SourceID: string([]byte{0xff}), Document: root}}},
		{name: "duplicate source ID", sources: []sourceDocument{{SourceID: "same", Document: root}, {SourceID: "same", Document: root}}},
		{name: "syntax", sources: []sourceDocument{{SourceID: "source", Document: []byte(`{"compatibility":`)}}},
		{name: "duplicate key", sources: []sourceDocument{{SourceID: "source", Document: duplicateKey}}},
		{name: "lone surrogate", sources: []sourceDocument{{SourceID: "source", Document: loneSurrogate}}},
		{name: "decimal tuple", sources: []sourceDocument{{SourceID: "source", Document: decimalTuple}}},
		{name: "tuple mismatch", sources: []sourceDocument{{SourceID: "source", Document: encodeDocument(t, tupleMismatch)}}},
		{name: "unsupported operation pointer precedence", sources: []sourceDocument{{SourceID: "source", Document: encodeDocument(t, unsupported)}}},
		{name: "invalid operation", sources: []sourceDocument{{SourceID: "source", Document: encodeDocument(t, invalidOperation)}}},
		{name: "invalid IR", sources: []sourceDocument{{SourceID: "source", Document: encodeDocument(t, invalidIR)}}},
		{name: "AddField restriction with unrelated member", sources: []sourceDocument{{SourceID: "source", Document: encodeDocument(t, addFieldRestriction)}}},
		{name: "invalid graph node", sources: invalidNode},
		{name: "duplicate graph node", sources: duplicateGraph},
		{name: "invalid graph dependency", sources: invalidDependency},
		{name: "duplicate graph dependency", sources: duplicateDependency},
		{name: "missing graph dependency", sources: missingDependency},
		{name: "graph dependency cycle", sources: cycle},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertProductCandidateSourcesEqual(t, test.sources)
		})
	}
}

type expectedProductDefinitionError struct {
	code           string
	stage          string
	pointer        string
	reason         string
	app            string
	name           string
	operationIndex int
}

func TestProductLoaderStrictRawDocumentParityMatrix(t *testing.T) {
	root := encodeDocument(t, rootDocument())
	invalidUTF8 := append([]byte(nil), root...)
	marker := bytes.Index(invalidUTF8, []byte("godj-test"))
	if marker < 0 {
		t.Fatal("root fixture has no producer marker")
	}
	invalidUTF8[marker] = 0xff

	deepDuplicate := replaceRawOnce(
		t,
		root,
		`"column":"title"`,
		`"column":"title","\u0063olumn":"shadow"`,
	)
	escapedDuplicate := replaceRawOnce(
		t,
		root,
		`"name":"0001_initial"`,
		`"name":"0001_initial","\u006eame":"shadow"`,
	)
	loneHigh := replaceRawOnce(t, root, `"version":"0.1.0"`, `"version":"\uD800"`)
	loneLow := replaceRawOnce(t, root, `"version":"0.1.0"`, `"version":"\uDC00"`)
	validPair := replaceRawOnce(t, root, `"version":"0.1.0"`, `"version":"\uD83D\uDE00"`)

	document := func(pointer, reason string) *expectedProductDefinitionError {
		return &expectedProductDefinitionError{
			code:           "invalid_definition_document",
			stage:          "document",
			pointer:        pointer,
			reason:         reason,
			operationIndex: -1,
		}
	}
	tests := []struct {
		name        string
		document    []byte
		want        *expectedProductDefinitionError
		wantSuccess bool
	}{
		{name: "invalid raw UTF-8", document: invalidUTF8, want: document("", "invalid_utf8")},
		{name: "UTF-8 BOM", document: append([]byte{0xef, 0xbb, 0xbf}, root...), want: document("", "syntax")},
		{name: "syntax", document: []byte(`{"compatibility":`), want: document("", "syntax")},
		{name: "trailing JSON value", document: append(append([]byte(nil), root...), []byte(` {}`)...), want: document("", "trailing_value")},
		{name: "decoded-equivalent duplicate", document: escapedDuplicate, want: document("/migration/name", "duplicate_key")},
		{name: "decoded-equivalent duplicate at depth", document: deepDuplicate, want: document("/migration/operations/0/model/fields/1/column", "duplicate_key")},
		{name: "lone high surrogate", document: loneHigh, want: document("/producer/version", "lone_surrogate")},
		{name: "lone low surrogate", document: loneLow, want: document("/producer/version", "lone_surrogate")},
		{name: "valid surrogate pair", document: validPair, wantSuccess: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertProductCandidateExpected(
				t,
				[]sourceDocument{{SourceID: "source", Document: test.document}},
				test.want,
				test.wantSuccess,
			)
		})
	}
}

func TestProductLoaderKnownObjectShapeAndRFC6901ParityMatrix(t *testing.T) {
	type mutation struct {
		name   string
		mutate func(map[string]any)
		want   expectedProductDefinitionError
	}
	document := func(pointer, reason string) expectedProductDefinitionError {
		return expectedProductDefinitionError{
			code:           "invalid_definition_document",
			stage:          "document",
			pointer:        pointer,
			reason:         reason,
			operationIndex: -1,
		}
	}
	semantic := func(code, pointer, reason string, operationIndex int) expectedProductDefinitionError {
		return expectedProductDefinitionError{
			code:           code,
			stage:          "semantic",
			pointer:        pointer,
			reason:         reason,
			app:            "alpha",
			name:           "0001_initial",
			operationIndex: operationIndex,
		}
	}

	mutations := []mutation{
		{
			name: "outer unknown",
			mutate: func(value map[string]any) {
				value["unexpected"] = true
			},
			want: document("/unexpected", "unknown_field"),
		},
		{
			name: "outer missing",
			mutate: func(value map[string]any) {
				delete(value, "producer")
			},
			want: document("/producer", "missing_field"),
		},
		{
			name: "compatibility unknown",
			mutate: func(value map[string]any) {
				value["compatibility"].(map[string]any)["unexpected"] = true
			},
			want: document("/compatibility/unexpected", "unknown_field"),
		},
		{
			name: "compatibility missing",
			mutate: func(value map[string]any) {
				delete(value["compatibility"].(map[string]any), "loader_abi")
			},
			want: document("/compatibility/loader_abi", "missing_field"),
		},
		{
			name: "producer unknown",
			mutate: func(value map[string]any) {
				value["producer"].(map[string]any)["unexpected"] = true
			},
			want: document("/producer/unexpected", "unknown_field"),
		},
		{
			name: "producer missing",
			mutate: func(value map[string]any) {
				delete(value["producer"].(map[string]any), "version")
			},
			want: document("/producer/version", "missing_field"),
		},
		{
			name: "migration unknown",
			mutate: func(value map[string]any) {
				value["migration"].(map[string]any)["unexpected"] = true
			},
			want: document("/migration/unexpected", "unknown_field"),
		},
		{
			name: "migration missing",
			mutate: func(value map[string]any) {
				delete(value["migration"].(map[string]any), "dependencies")
			},
			want: document("/migration/dependencies", "missing_field"),
		},
		{
			name: "dependency unknown",
			mutate: func(value map[string]any) {
				value["migration"].(map[string]any)["dependencies"] = []any{map[string]any{
					"app": "alpha", "name": "0000_base", "unexpected": true,
				}}
			},
			want: semantic("invalid_definition_operation", "/migration/dependencies/0/unexpected", "invalid_operation", -1),
		},
		{
			name: "dependency missing",
			mutate: func(value map[string]any) {
				value["migration"].(map[string]any)["dependencies"] = []any{map[string]any{"app": "alpha"}}
			},
			want: semantic("invalid_definition_operation", "/migration/dependencies/0/name", "invalid_operation", -1),
		},
		{
			name: "operation unknown",
			mutate: func(value map[string]any) {
				createOperation(value)["unexpected"] = true
			},
			want: semantic("invalid_definition_operation", "/migration/operations/0/unexpected", "invalid_operation", 0),
		},
		{
			name: "operation missing",
			mutate: func(value map[string]any) {
				delete(createOperation(value), "app_label")
			},
			want: semantic("invalid_definition_operation", "/migration/operations/0/app_label", "invalid_operation", 0),
		},
		{
			name: "model unknown",
			mutate: func(value map[string]any) {
				createOperation(value)["model"].(map[string]any)["unexpected"] = true
			},
			want: semantic("invalid_definition_ir", "/migration/operations/0/model/unexpected", "invalid_ir", 0),
		},
		{
			name: "model missing",
			mutate: func(value map[string]any) {
				delete(createOperation(value)["model"].(map[string]any), "db_table")
			},
			want: semantic("invalid_definition_ir", "/migration/operations/0/model/db_table", "invalid_ir", 0),
		},
		{
			name: "field unknown",
			mutate: func(value map[string]any) {
				rootCharField(value)["unexpected"] = true
			},
			want: semantic("invalid_definition_ir", "/migration/operations/0/model/fields/1/unexpected", "invalid_ir", 0),
		},
		{
			name: "field missing",
			mutate: func(value map[string]any) {
				delete(rootCharField(value), "column")
			},
			want: semantic("invalid_definition_ir", "/migration/operations/0/model/fields/1/column", "invalid_ir", 0),
		},
		{
			name: "default unknown",
			mutate: func(value map[string]any) {
				rootCharField(value)["default"].(map[string]any)["unexpected"] = true
			},
			want: semantic("invalid_definition_ir", "/migration/operations/0/model/fields/1/default/unexpected", "invalid_ir", 0),
		},
		{
			name: "default missing",
			mutate: func(value map[string]any) {
				delete(rootCharField(value)["default"].(map[string]any), "string")
			},
			want: semantic("invalid_definition_ir", "/migration/operations/0/model/fields/1/default/string", "invalid_ir", 0),
		},
		{
			name: "RFC6901 tilde and slash escaping",
			mutate: func(value map[string]any) {
				value["compatibility"].(map[string]any)["~/"] = true
			},
			want: document("/compatibility/~0~1", "unknown_field"),
		},
		{
			name: "RFC6901 decoded control token",
			mutate: func(value map[string]any) {
				value["compatibility"].(map[string]any)["\x01"] = true
			},
			want: document("/compatibility/\x01", "unknown_field"),
		},
	}

	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			document := rootDocument()
			test.mutate(document)
			assertProductCandidateExpected(
				t,
				[]sourceDocument{{SourceID: "source", Document: encodeDocument(t, document)}},
				&test.want,
				false,
			)
		})
	}
}

func TestProductLoaderNumericLexemeAndPrecedenceParityMatrix(t *testing.T) {
	root := encodeDocument(t, rootDocument())
	coordinateCodes := map[string]string{
		"definition_format": "definition_format_incompatible",
		"loader_abi":        "loader_abi_incompatible",
		"operation_codec":   "operation_codec_incompatible",
		"schema_ir":         "schema_ir_incompatible",
	}
	coordinates := []string{"definition_format", "loader_abi", "operation_codec", "schema_ir"}
	for _, coordinate := range coordinates {
		coordinate := coordinate
		oldValue := `"` + coordinate + `":1`
		if coordinate == "schema_ir" {
			oldValue = `"schema_ir":2`
		}
		for _, test := range []struct {
			name   string
			lexeme string
			want   expectedProductDefinitionError
		}{
			{
				name:   "negative zero",
				lexeme: "-0",
				want: expectedProductDefinitionError{
					code: coordinateCodes[coordinate], stage: "compatibility",
					pointer: "/compatibility/" + coordinate, reason: coordinate, operationIndex: -1,
				},
			},
			{
				name:   "negative one",
				lexeme: "-1",
				want: expectedProductDefinitionError{
					code: coordinateCodes[coordinate], stage: "compatibility",
					pointer: "/compatibility/" + coordinate, reason: coordinate, operationIndex: -1,
				},
			},
			{
				name:   "signed int64 minimum",
				lexeme: "-9223372036854775808",
				want: expectedProductDefinitionError{
					code: coordinateCodes[coordinate], stage: "compatibility",
					pointer: "/compatibility/" + coordinate, reason: coordinate, operationIndex: -1,
				},
			},
			{
				name:   "signed int64 maximum",
				lexeme: "9223372036854775807",
				want: expectedProductDefinitionError{
					code: coordinateCodes[coordinate], stage: "compatibility",
					pointer: "/compatibility/" + coordinate, reason: coordinate, operationIndex: -1,
				},
			},
			{
				name:   "signed int64 underflow",
				lexeme: "-9223372036854775809",
				want: expectedProductDefinitionError{
					code: "invalid_definition_document", stage: "document",
					pointer: "/compatibility/" + coordinate, reason: "out_of_range", operationIndex: -1,
				},
			},
			{
				name:   "signed int64 overflow",
				lexeme: "9223372036854775808",
				want: expectedProductDefinitionError{
					code: "invalid_definition_document", stage: "document",
					pointer: "/compatibility/" + coordinate, reason: "out_of_range", operationIndex: -1,
				},
			},
			{
				name:   "explicit plus one",
				lexeme: "+1",
				want: expectedProductDefinitionError{
					code: "invalid_definition_document", stage: "document",
					pointer: "", reason: "syntax", operationIndex: -1,
				},
			},
			{
				name:   "leading zero",
				lexeme: "01",
				want: expectedProductDefinitionError{
					code: "invalid_definition_document", stage: "document",
					pointer: "", reason: "syntax", operationIndex: -1,
				},
			},
			{
				name:   "decimal",
				lexeme: "1.0",
				want: expectedProductDefinitionError{
					code: "invalid_definition_document", stage: "document",
					pointer: "/compatibility/" + coordinate, reason: "wrong_type", operationIndex: -1,
				},
			},
			{
				name:   "exponent",
				lexeme: "1e0",
				want: expectedProductDefinitionError{
					code: "invalid_definition_document", stage: "document",
					pointer: "/compatibility/" + coordinate, reason: "wrong_type", operationIndex: -1,
				},
			},
		} {
			test := test
			t.Run(coordinate+"/"+test.name, func(t *testing.T) {
				payload := replaceRawOnce(t, root, oldValue, `"`+coordinate+`":`+test.lexeme)
				assertProductCandidateExpected(
					t,
					[]sourceDocument{{SourceID: "source", Document: payload}},
					&test.want,
					false,
				)
			})
		}
	}

	maxLengthDocument := rootDocument()
	rootCharField(maxLengthDocument)["default"] = nil
	maxLengthRoot := encodeDocument(t, maxLengthDocument)
	maxLengthToken := `"max_length":64`
	maxLengthTests := []struct {
		name        string
		lexeme      string
		want        *expectedProductDefinitionError
		wantSuccess bool
	}{
		{
			name: "portable maximum", lexeme: "2147483647", wantSuccess: true,
		},
		{
			name: "negative zero", lexeme: "-0",
			want: &expectedProductDefinitionError{
				code: "invalid_definition_ir", stage: "semantic",
				pointer: "/migration/operations/0/model/fields/1/max_length", reason: "invalid_ir",
				app: "alpha", name: "0001_initial", operationIndex: 0,
			},
		},
		{
			name: "negative one", lexeme: "-1",
			want: &expectedProductDefinitionError{
				code: "invalid_definition_document", stage: "semantic",
				pointer: "/migration/operations/0/model/fields/1/max_length", reason: "out_of_range",
				app: "alpha", name: "0001_initial", operationIndex: 0,
			},
		},
		{
			name: "signed int64 minimum", lexeme: "-9223372036854775808",
			want: &expectedProductDefinitionError{
				code: "invalid_definition_document", stage: "semantic",
				pointer: "/migration/operations/0/model/fields/1/max_length", reason: "out_of_range",
				app: "alpha", name: "0001_initial", operationIndex: 0,
			},
		},
		{
			name: "signed int64 maximum", lexeme: "9223372036854775807",
			want: &expectedProductDefinitionError{
				code: "invalid_definition_document", stage: "semantic",
				pointer: "/migration/operations/0/model/fields/1/max_length", reason: "out_of_range",
				app: "alpha", name: "0001_initial", operationIndex: 0,
			},
		},
		{
			name: "signed int64 underflow", lexeme: "-9223372036854775809",
			want: &expectedProductDefinitionError{
				code: "invalid_definition_document", stage: "document",
				pointer: "/migration/operations/0/model/fields/1/max_length", reason: "out_of_range", operationIndex: -1,
			},
		},
		{
			name: "signed int64 overflow", lexeme: "9223372036854775808",
			want: &expectedProductDefinitionError{
				code: "invalid_definition_document", stage: "document",
				pointer: "/migration/operations/0/model/fields/1/max_length", reason: "out_of_range", operationIndex: -1,
			},
		},
		{
			name: "explicit plus one", lexeme: "+1",
			want: &expectedProductDefinitionError{
				code: "invalid_definition_document", stage: "document", pointer: "", reason: "syntax", operationIndex: -1,
			},
		},
		{
			name: "leading zero", lexeme: "01",
			want: &expectedProductDefinitionError{
				code: "invalid_definition_document", stage: "document", pointer: "", reason: "syntax", operationIndex: -1,
			},
		},
		{
			name: "decimal", lexeme: "1.0",
			want: &expectedProductDefinitionError{
				code: "invalid_definition_document", stage: "document",
				pointer: "/migration/operations/0/model/fields/1/max_length", reason: "wrong_type", operationIndex: -1,
			},
		},
		{
			name: "exponent", lexeme: "1e0",
			want: &expectedProductDefinitionError{
				code: "invalid_definition_document", stage: "document",
				pointer: "/migration/operations/0/model/fields/1/max_length", reason: "wrong_type", operationIndex: -1,
			},
		},
	}
	for _, test := range maxLengthTests {
		t.Run("max_length/"+test.name, func(t *testing.T) {
			payload := replaceRawOnce(t, maxLengthRoot, maxLengthToken, `"max_length":`+test.lexeme)
			assertProductCandidateExpected(
				t,
				[]sourceDocument{{SourceID: "source", Document: payload}},
				test.want,
				test.wantSuccess,
			)
		})
	}

	t.Run("tuple precedes signed-int64 max_length semantic range", func(t *testing.T) {
		payload := replaceRawOnce(t, maxLengthRoot, `"definition_format":1`, `"definition_format":2`)
		payload = replaceRawOnce(t, payload, maxLengthToken, `"max_length":9223372036854775807`)
		want := expectedProductDefinitionError{
			code: "definition_format_incompatible", stage: "compatibility",
			pointer: "/compatibility/definition_format", reason: "definition_format", operationIndex: -1,
		}
		assertProductCandidateExpected(t, []sourceDocument{{SourceID: "source", Document: payload}}, &want, false)
	})

	for _, test := range []struct {
		name   string
		lexeme string
		reason string
	}{
		{name: "overflow", lexeme: "9223372036854775808", reason: "out_of_range"},
		{name: "decimal", lexeme: "1.0", reason: "wrong_type"},
		{name: "exponent", lexeme: "1e0", reason: "wrong_type"},
	} {
		t.Run("recognized max_length document failure precedes tuple/"+test.name, func(t *testing.T) {
			payload := replaceRawOnce(t, maxLengthRoot, `"definition_format":1`, `"definition_format":2`)
			payload = replaceRawOnce(t, payload, maxLengthToken, `"max_length":`+test.lexeme)
			want := expectedProductDefinitionError{
				code: "invalid_definition_document", stage: "document",
				pointer: "/migration/operations/0/model/fields/1/max_length", reason: test.reason, operationIndex: -1,
			}
			assertProductCandidateExpected(t, []sourceDocument{{SourceID: "source", Document: payload}}, &want, false)
		})
	}
}

func TestProductLoaderCombinedSemanticCanonicalSelectionParity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   expectedProductDefinitionError
	}{
		{
			name: "operation pointer precedes reason rank",
			mutate: func(document map[string]any) {
				operation := createOperation(document)
				operation["aaa"] = true
				operation["kind"] = "run_python"
			},
			want: expectedProductDefinitionError{
				code: "invalid_definition_operation", stage: "semantic",
				pointer: "/migration/operations/0/aaa", reason: "invalid_operation",
				app: "alpha", name: "0001_initial", operationIndex: 0,
			},
		},
		{
			name: "field pointer precedes reason rank",
			mutate: func(document map[string]any) {
				field := rootCharField(document)
				field["aaa"] = true
				field["kind"] = "custom"
			},
			want: expectedProductDefinitionError{
				code: "invalid_definition_ir", stage: "semantic",
				pointer: "/migration/operations/0/model/fields/1/aaa", reason: "invalid_ir",
				app: "alpha", name: "0001_initial", operationIndex: 0,
			},
		},
		{
			name: "lexical array pointer beats traversal order",
			mutate: func(document map[string]any) {
				operations := make([]any, 11)
				for index := range operations {
					operations[index] = map[string]any{
						"kind": "add_field", "app_label": "alpha", "model_name": "entry",
						"field": booleanFieldDocument("flag", "Flag", map[string]any{"kind": "boolean", "boolean": false}),
					}
				}
				operations[2].(map[string]any)["kind"] = "run_python"
				operations[10].(map[string]any)["aaa"] = true
				document["migration"].(map[string]any)["operations"] = operations
			},
			want: expectedProductDefinitionError{
				code: "invalid_definition_operation", stage: "semantic",
				pointer: "/migration/operations/10/aaa", reason: "invalid_operation",
				app: "alpha", name: "0001_initial", operationIndex: 10,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := rootDocument()
			test.mutate(document)
			assertProductCandidateExpected(
				t,
				[]sourceDocument{{SourceID: "source", Document: encodeDocument(t, document)}},
				&test.want,
				false,
			)
		})
	}
}

func TestProductLoaderCanonicalDigestParityAndOrderSemantics(t *testing.T) {
	special := "<>&\u2028\u2029\x01\b\t\n\f\r\\\"雪🙂"
	root := rootDocument()
	field := rootCharField(root)
	field["max_length"] = 64
	field["default"] = map[string]any{"kind": "string", "string": special}
	tail := tailDocument()

	baselineSources := []sourceDocument{
		{SourceID: "z-root", Document: encodeDocument(t, root)},
		{SourceID: "a-tail", Document: encodeDocument(t, tail)},
	}
	candidateBaseline, _, candidateErr, productBaseline, _, productErr := assertProductCandidateSourcesEqual(t, baselineSources)
	if candidateErr != nil || productErr != nil {
		t.Fatalf("canonical baseline failed: candidate=%v product=%v", candidateErr, productErr)
	}
	canonical, err := canonicalDefinitionSet(candidateBaseline.Definitions)
	if err != nil {
		t.Fatalf("canonicalize special-string baseline: %v", err)
	}
	wantString := []byte("\"<>&\u2028\u2029\\u0001\\b\\t\\n\\f\\r\\\\\\\"雪🙂\"")
	if !bytes.Contains(canonical, wantString) {
		t.Fatalf("canonical special string missing: canonical=%q want-substring=%q", canonical, wantString)
	}
	for _, forbidden := range [][]byte{[]byte(`\u003c`), []byte(`\u003e`), []byte(`\u0026`), []byte(`\u2028`), []byte(`\u2029`)} {
		if bytes.Contains(canonical, forbidden) {
			t.Fatalf("canonical string used forbidden HTML/JavaScript escape %q in %q", forbidden, canonical)
		}
	}

	orderEquivalent := []sourceDocument{baselineSources[1], baselineSources[0]}
	_, _, orderCandidateErr, orderProduct, _, orderProductErr := assertProductCandidateSourcesEqual(t, orderEquivalent)
	if orderCandidateErr != nil || orderProductErr != nil || orderProduct.Digest() != productBaseline.Digest() {
		t.Fatalf("source order changed digest: baseline=%s reordered=%s candidateErr=%v productErr=%v", productBaseline.Digest(), orderProduct.Digest(), orderCandidateErr, orderProductErr)
	}

	producerRoot := cloneJSONValue(t, root).(map[string]any)
	producerTail := cloneJSONValue(t, tail).(map[string]any)
	producerRoot["producer"] = map[string]any{"name": "another-producer", "version": "9.9.9"}
	producerTail["producer"] = map[string]any{"name": "third-producer", "version": "8.8.8"}
	_, _, producerCandidateErr, producerProduct, _, producerProductErr := assertProductCandidateSourcesEqual(t, []sourceDocument{
		{SourceID: "z-root", Document: encodeDocument(t, producerRoot)},
		{SourceID: "a-tail", Document: encodeDocument(t, producerTail)},
	})
	if producerCandidateErr != nil || producerProductErr != nil || producerProduct.Digest() != productBaseline.Digest() {
		t.Fatalf("producer provenance changed digest: baseline=%s changed=%s candidateErr=%v productErr=%v", productBaseline.Digest(), producerProduct.Digest(), producerCandidateErr, producerProductErr)
	}

	keyOrderPayload := orderedTopLevelDocument(t, root, []string{"producer", "migration", "compatibility"})
	_, _, keyCandidateErr, keyProduct, _, keyProductErr := assertProductCandidateSourcesEqual(t, []sourceDocument{
		{SourceID: "z-root", Document: keyOrderPayload},
		{SourceID: "a-tail", Document: encodeDocument(t, tail)},
	})
	if keyCandidateErr != nil || keyProductErr != nil || keyProduct.Digest() != productBaseline.Digest() {
		t.Fatalf("object-key order changed digest: baseline=%s changed=%s candidateErr=%v productErr=%v", productBaseline.Digest(), keyProduct.Digest(), keyCandidateErr, keyProductErr)
	}

	operationReorderedTail := cloneJSONValue(t, tail).(map[string]any)
	operations := operationReorderedTail["migration"].(map[string]any)["operations"].([]any)
	operations[0], operations[1] = operations[1], operations[0]
	_, _, operationCandidateErr, operationProduct, _, operationProductErr := assertProductCandidateSourcesEqual(t, []sourceDocument{
		{SourceID: "z-root", Document: encodeDocument(t, root)},
		{SourceID: "a-tail", Document: encodeDocument(t, operationReorderedTail)},
	})
	if operationCandidateErr != nil || operationProductErr != nil {
		t.Fatalf("operation-order variant failed: candidate=%v product=%v", operationCandidateErr, operationProductErr)
	}
	if operationProduct.Digest() == productBaseline.Digest() {
		t.Fatal("operation order did not change semantic digest")
	}

	fieldReorderedRoot := cloneJSONValue(t, root).(map[string]any)
	fields := createOperation(fieldReorderedRoot)["model"].(map[string]any)["fields"].([]any)
	fields[0], fields[1] = fields[1], fields[0]
	_, _, fieldCandidateErr, fieldProduct, _, fieldProductErr := assertProductCandidateSourcesEqual(t, []sourceDocument{
		{SourceID: "z-root", Document: encodeDocument(t, fieldReorderedRoot)},
		{SourceID: "a-tail", Document: encodeDocument(t, tail)},
	})
	if fieldCandidateErr != nil || fieldProductErr != nil {
		t.Fatalf("field-order variant failed: candidate=%v product=%v", fieldCandidateErr, fieldProductErr)
	}
	if fieldProduct.Digest() == productBaseline.Digest() {
		t.Fatal("field order did not change semantic digest")
	}
}

func TestProductLoaderReturnedAccessorMutationCannotChangeSet(t *testing.T) {
	sources := definitionSources(t)
	candidateSet, candidateMetrics, candidateErr, productSet, productReport, productErr := assertProductCandidateSourcesEqual(t, sources)
	if candidateErr != nil || productErr != nil {
		t.Fatalf("accessor-mutation baseline failed: candidate=%v product=%v", candidateErr, productErr)
	}
	wantDigest := productSet.Digest()

	definitions := productSet.Definitions()
	if len(definitions) != 2 || len(definitions[0].Operations) == 0 || len(definitions[1].Dependencies) == 0 {
		t.Fatalf("unexpected accessor fixture: %#v", definitions)
	}
	create, ok := definitions[0].Operations[0].(migrations.CreateModel)
	if !ok || len(create.Model.Fields) < 2 || create.Model.Fields[1].Default == nil {
		t.Fatalf("unexpected CreateModel accessor: %#v", definitions[0].Operations[0])
	}
	create.Model.Fields[1].Default.String = "mutated-default"
	create.Model.Fields[1].Name = "mutated_field"
	create.Model.Fields = create.Model.Fields[:1]
	definitions[0].Operations[0] = create
	definitions[0].Operations = definitions[0].Operations[:0]
	definitions[1].Dependencies[0].Name = "mutated_dependency"
	definitions[1].Dependencies = append(definitions[1].Dependencies, migrations.MigrationKey{App: "mutated", Name: "0001_added"})
	definitions = append(definitions, migrations.Migration{App: "mutated", Name: "0002_added"})

	inventory := productSet.Sources()
	if len(inventory) == 0 {
		t.Fatal("source accessor unexpectedly empty")
	}
	inventory[0].SourceID = "mutated-source"
	inventory[0].Producer.Name = "mutated-producer"
	inventory[0].Migration.Name = "mutated-migration"
	inventory = inventory[:0]

	if productSet.Digest() != wantDigest {
		t.Fatalf("accessor mutation changed digest: got=%s want=%s", productSet.Digest(), wantDigest)
	}
	assertProductCandidateOutcomeEqual(
		t,
		candidateSet,
		candidateMetrics,
		candidateErr,
		productSet,
		productReport,
		productErr,
	)
}

func assertProductCandidateExpected(
	t *testing.T,
	sources []sourceDocument,
	want *expectedProductDefinitionError,
	wantSuccess bool,
) {
	t.Helper()
	_, _, candidateErr, _, _, productErr := assertProductCandidateSourcesEqual(t, sources)
	if wantSuccess {
		if candidateErr != nil || productErr != nil {
			t.Fatalf("load failed: candidate=%v product=%v", candidateErr, productErr)
		}
		return
	}
	if want == nil {
		t.Fatal("failure case has no expected error")
	}
	candidate := requireDefinitionError(t, candidateErr)
	var product *productdefinition.Error
	if !errors.As(productErr, &product) {
		t.Fatalf("product error = %T %v, want *definition.Error", productErr, productErr)
	}
	context := product.Context()
	wantSourceID := "source"
	if len(sources) == 1 {
		wantSourceID = sources[0].SourceID
	}
	if candidate.Code != want.code ||
		candidate.Stage != want.stage ||
		candidate.SourceID != wantSourceID ||
		candidate.JSONPointer != want.pointer ||
		candidate.App != want.app ||
		candidate.Name != want.name ||
		candidate.OperationIndex != want.operationIndex ||
		candidate.Reason != want.reason {
		t.Fatalf("candidate error = %+v, want=%+v source=%q", candidate, *want, wantSourceID)
	}
	if string(product.Code) != want.code ||
		context.Stage != want.stage ||
		context.SourceID != wantSourceID ||
		context.JSONPointer != want.pointer ||
		context.App != want.app ||
		context.Name != want.name ||
		context.OperationIndex != want.operationIndex ||
		context.Reason != want.reason {
		t.Fatalf("product error = %+v/%+v, want=%+v source=%q", product, context, *want, wantSourceID)
	}
}

func replaceRawOnce(t *testing.T, payload []byte, oldValue, newValue string) []byte {
	t.Helper()
	if bytes.Count(payload, []byte(oldValue)) != 1 {
		t.Fatalf("raw fixture occurrence count for %q = %d, want 1", oldValue, bytes.Count(payload, []byte(oldValue)))
	}
	return bytes.Replace(append([]byte(nil), payload...), []byte(oldValue), []byte(newValue), 1)
}

func rootCharField(document map[string]any) map[string]any {
	return createOperation(document)["model"].(map[string]any)["fields"].([]any)[1].(map[string]any)
}

func orderedTopLevelDocument(t *testing.T, document map[string]any, keys []string) []byte {
	t.Helper()
	payload := []byte{'{'}
	for index, key := range keys {
		value, exists := document[key]
		if !exists {
			t.Fatalf("ordered top-level key %q is absent", key)
		}
		if index != 0 {
			payload = append(payload, ',')
		}
		payload = append(payload, encodeValue(t, key)...)
		payload = append(payload, ':')
		payload = append(payload, encodeValue(t, value)...)
	}
	return append(payload, '}')
}

func assertProductCandidateSourcesEqual(t *testing.T, sources []sourceDocument) (
	loadedDefinitionSet,
	loadMetrics,
	error,
	productdefinition.Set,
	productdefinition.LoadReport,
	error,
) {
	t.Helper()
	candidateSet, candidateMetrics, candidateErr := loadDefinitions(sources)
	productSources := make([]productdefinition.Source, len(sources))
	for index, source := range sources {
		productSources[index] = productdefinition.Source{
			SourceID: source.SourceID,
			Document: append([]byte(nil), source.Document...),
		}
	}
	productSet, productReport, productErr := productdefinition.Load(productSources...)
	assertProductCandidateOutcomeEqual(
		t,
		candidateSet,
		candidateMetrics,
		candidateErr,
		productSet,
		productReport,
		productErr,
	)
	return candidateSet, candidateMetrics, candidateErr, productSet, productReport, productErr
}

func assertProductCandidateOutcomeEqual(
	t *testing.T,
	candidateSet loadedDefinitionSet,
	candidateMetrics loadMetrics,
	candidateErr error,
	productSet productdefinition.Set,
	productReport productdefinition.LoadReport,
	productErr error,
) {
	t.Helper()
	if (candidateErr == nil) != (productErr == nil) {
		t.Fatalf("error presence differs: candidate=%v product=%v", candidateErr, productErr)
	}
	if candidateMetrics.DocumentsReceived != productReport.DocumentsReceived ||
		candidateMetrics.HeadersValidated != productReport.HeadersValidated ||
		candidateMetrics.OperationsDecoded != productReport.OperationsDecoded ||
		candidateMetrics.PlannerConstruction != productReport.PlannerConstruction ||
		candidateMetrics.DefinitionsPublished != productReport.DefinitionsPublished ||
		candidateMetrics.DefinitionSetsPublished != productReport.DefinitionSetsPublished {
		t.Fatalf("load metrics differ: candidate=%+v product=%+v", candidateMetrics, productReport)
	}

	if candidateErr == nil {
		if failure, ok := productReport.Failure(); ok {
			t.Fatalf("successful product report has failure: %+v", failure)
		}
		if candidateSet.Digest != productSet.Digest() || !reflect.DeepEqual(normalizeDefinitionSlices(candidateSet.Definitions), normalizeDefinitionSlices(productSet.Definitions())) {
			t.Fatalf("published definition set differs: candidate=%#v/%s product=%#v/%s", candidateSet.Definitions, candidateSet.Digest, productSet.Definitions(), productSet.Digest())
		}
		productSources := productSet.Sources()
		if len(candidateSet.Sources) != len(productSources) {
			t.Fatalf("source inventory lengths differ: candidate=%#v product=%#v", candidateSet.Sources, productSources)
		}
		for index := range candidateSet.Sources {
			candidate := candidateSet.Sources[index]
			product := productSources[index]
			if candidate.SourceID != product.SourceID || candidate.Producer.name != product.Producer.Name || candidate.Producer.version != product.Producer.Version || candidate.App != product.Migration.App || candidate.Name != product.Migration.Name {
				t.Fatalf("source inventory[%d] differs: candidate=%#v product=%#v", index, candidate, product)
			}
		}
		return
	}
	if candidateSet.Digest != "" || candidateSet.Definitions != nil || candidateSet.Sources != nil {
		t.Fatalf("candidate failure published partial set: %#v", candidateSet)
	}
	if productSet.Digest() != productdefinition.EmptySetDigest || len(productSet.Definitions()) != 0 || len(productSet.Sources()) != 0 {
		t.Fatalf("product failure published partial set: digest=%s definitions=%#v sources=%#v", productSet.Digest(), productSet.Definitions(), productSet.Sources())
	}
	if candidateMetrics.DefinitionsPublished != 0 || candidateMetrics.DefinitionSetsPublished != 0 || productReport.DefinitionsPublished != 0 || productReport.DefinitionSetsPublished != 0 {
		t.Fatalf("failure publication metrics are nonzero: candidate=%+v product=%+v", candidateMetrics, productReport)
	}

	var candidateSourceError *definitionError
	var productSourceError *productdefinition.Error
	candidateHasSourceError := errors.As(candidateErr, &candidateSourceError)
	productHasSourceError := errors.As(productErr, &productSourceError)
	if candidateHasSourceError || productHasSourceError {
		if candidateSourceError == nil || productSourceError == nil {
			t.Fatalf("source error type differs: candidate=%T product=%T", candidateErr, productErr)
		}
		context := productSourceError.Context()
		if candidateSourceError.Category != productSourceError.Category ||
			candidateSourceError.Code != string(productSourceError.Code) ||
			candidateSourceError.Stage != context.Stage ||
			candidateSourceError.SourceID != context.SourceID ||
			candidateSourceError.JSONPointer != context.JSONPointer ||
			candidateSourceError.App != context.App ||
			candidateSourceError.Name != context.Name ||
			candidateSourceError.OperationIndex != context.OperationIndex ||
			candidateSourceError.Reason != context.Reason {
			t.Fatalf("source error differs: candidate=%+v product=%+v/%+v", candidateSourceError, productSourceError, context)
		}
		failure, ok := productReport.Failure()
		if !ok || !equalProductFailureContext(failure, context) {
			t.Fatalf("product source report failure = %+v, %v, want %+v", failure, ok, context)
		}
		return
	}

	var candidatePlanningError *migrations.PlanningError
	var productPlanningError *migrations.PlanningError
	if !errors.As(candidateErr, &candidatePlanningError) || !errors.As(productErr, &productPlanningError) {
		t.Fatalf("unclassified errors: candidate=%T %v product=%T %v", candidateErr, candidateErr, productErr, productErr)
	}
	if candidatePlanningError.Category != productPlanningError.Category ||
		candidatePlanningError.Code != productPlanningError.Code ||
		candidatePlanningError.Node != productPlanningError.Node ||
		candidatePlanningError.Related != productPlanningError.Related ||
		!reflect.DeepEqual(candidatePlanningError.Members(), productPlanningError.Members()) {
		t.Fatalf("planning error differs: candidate=%+v members=%v product=%+v members=%v", candidatePlanningError, candidatePlanningError.Members(), productPlanningError, productPlanningError.Members())
	}
	if failure, ok := productReport.Failure(); !ok || failure.Stage != "graph" {
		t.Fatalf("product graph report failure = %+v, %v", failure, ok)
	}
}

func equalProductFailureContext(left, right productdefinition.FailureContext) bool {
	return left.Stage == right.Stage &&
		left.SourceID == right.SourceID &&
		left.JSONPointer == right.JSONPointer &&
		left.App == right.App &&
		left.Name == right.Name &&
		left.OperationIndex == right.OperationIndex &&
		left.Reason == right.Reason &&
		left.Limit == right.Limit &&
		left.Maximum == right.Maximum &&
		left.Actual == right.Actual &&
		reflect.DeepEqual(left.GraphSources(), right.GraphSources())
}

func normalizeDefinitionSlices(definitions []migrations.Migration) []migrations.Migration {
	normalized := make([]migrations.Migration, len(definitions))
	for index, migration := range definitions {
		normalized[index] = migration
		normalized[index].Dependencies = append([]migrations.MigrationKey{}, migration.Dependencies...)
		normalized[index].Operations = append([]migrations.Operation{}, migration.Operations...)
	}
	return normalized
}
