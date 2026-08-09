package definition

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestResourceLimitBoundariesAndCombinedFaultPrecedence(t *testing.T) {
	tests := []struct {
		name           string
		maximum        int
		code           ErrorCode
		stage          string
		limit          string
		sources        func(int) []Source
		sourceID       string
		pointer        string
		app            string
		migration      string
		operationIndex int
	}{
		{
			name: "source count", maximum: MaxSources, code: CodeInvalidSource,
			stage: "source", limit: "source_count", sources: resourceSourceCount,
			sourceID: "", pointer: "", operationIndex: -1,
		},
		{
			name: "source ID bytes", maximum: MaxSourceIDBytes, code: CodeInvalidSource,
			stage: "source", limit: "source_id_bytes", sources: resourceSourceIDBytes,
			sourceID: "", pointer: "", operationIndex: -1,
		},
		{
			name: "document bytes", maximum: MaxDocumentBytes, code: CodeInvalidDocument,
			stage: "document", limit: "document_bytes", sources: resourceDocumentBytes,
			sourceID: "document", pointer: "", operationIndex: -1,
		},
		{
			name: "batch bytes", maximum: MaxBatchBytes, code: CodeInvalidDocument,
			stage: "document", limit: "batch_bytes", sources: resourceBatchBytes,
			sourceID: "", pointer: "", operationIndex: -1,
		},
		{
			name: "JSON depth", maximum: MaxJSONDepth, code: CodeInvalidDocument,
			stage: "document", limit: "json_depth", sources: resourceJSONDepth,
			sourceID: "depth", pointer: strings.Repeat("/0", MaxJSONDepth), operationIndex: -1,
		},
		{
			name: "document JSON values", maximum: MaxDocumentJSONValues, code: CodeInvalidDocument,
			stage: "document", limit: "document_json_values", sources: resourceDocumentJSONValues,
			sourceID: "document-values", pointer: "", operationIndex: -1,
		},
		{
			name: "batch JSON values", maximum: MaxJSONValues, code: CodeInvalidDocument,
			stage: "document", limit: "json_values", sources: resourceBatchJSONValues,
			sourceID: "value-0004", pointer: "", operationIndex: -1,
		},
		{
			name: "dependencies per migration", maximum: MaxDependenciesPerMigration, code: CodeInvalidOperation,
			stage: "semantic", limit: "dependencies_per_migration", sources: resourceDependencies,
			sourceID: "dependencies", pointer: "/migration/dependencies", app: "alpha", migration: "0001_initial", operationIndex: -1,
		},
		{
			name: "operations per migration", maximum: MaxOperationsPerMigration, code: CodeInvalidOperation,
			stage: "semantic", limit: "operations_per_migration", sources: resourceOperations,
			sourceID: "operations", pointer: "/migration/operations", app: "alpha", migration: "0001_initial", operationIndex: -1,
		},
		{
			name: "fields per CreateModel", maximum: MaxFieldsPerCreateModel, code: CodeInvalidIR,
			stage: "semantic", limit: "fields_per_create_model", sources: resourceCreateModelFields,
			sourceID: "fields", pointer: "/migration/operations/0/model/fields", app: "alpha", migration: "0001_initial", operationIndex: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, delta := range []int{-1, 0, 1} {
				value := test.maximum + delta
				name := fmt.Sprintf("maximum%+d", delta)
				t.Run(name, func(t *testing.T) {
					set, report, err := Load(test.sources(value)...)
					if delta <= 0 {
						assertLimitGuardPassed(t, report, err, test.limit)
						return
					}
					requireResourceLimitFailure(t, set, report, err, expectedLimitFailure{
						code: test.code, stage: test.stage, limit: test.limit,
						maximum: uint64(test.maximum), actual: uint64(value),
						sourceID: test.sourceID, pointer: test.pointer, app: test.app, migration: test.migration, operationIndex: test.operationIndex,
					})
				})
			}
		})
	}
}

func TestCompatibilityPrecedesSemanticCapsAndUnknownContainersDoNotGuessLimits(t *testing.T) {
	semanticOverLimit := []struct {
		name     string
		sourceID string
		document func(compatibilityTupleTest) []byte
	}{
		{
			name: "dependencies", sourceID: "dependencies",
			document: func(tuple compatibilityTupleTest) []byte {
				return resourceEnvelope(tuple, repeatedJSONArray(`{}`, MaxDependenciesPerMigration+1), []byte(`[]`))
			},
		},
		{
			name: "operations", sourceID: "operations",
			document: func(tuple compatibilityTupleTest) []byte {
				return resourceEnvelope(tuple, []byte(`[]`), repeatedJSONArray(`{}`, MaxOperationsPerMigration+1))
			},
		},
		{
			name: "CreateModel fields", sourceID: "fields",
			document: func(tuple compatibilityTupleTest) []byte {
				return resourceCreateModelEnvelope(tuple, repeatedJSONArray(`{}`, MaxFieldsPerCreateModel+1))
			},
		},
	}
	for _, test := range semanticOverLimit {
		t.Run("tuple before "+test.name, func(t *testing.T) {
			set, report, err := Load(Source{
				SourceID: test.sourceID,
				Document: test.document(compatibilityTupleTest{definitionFormat: 1, loaderABI: 1, operationCodec: 1, schemaIR: 3}),
			})
			definitionError := requireSourceDefinitionError(t, err)
			if definitionError.Code != CodeSchemaIRIncompatible {
				t.Fatalf("error code = %q, want %q", definitionError.Code, CodeSchemaIRIncompatible)
			}
			context := definitionError.Context()
			if context.Stage != "compatibility" || context.JSONPointer != "/compatibility/schema_ir" || context.Reason != "schema_ir" || context.Limit != "" || context.Maximum != 0 || context.Actual != 0 {
				t.Fatalf("tuple precedence context = %+v", context)
			}
			if report.PlannerConstruction != 0 {
				t.Fatalf("tuple failure crossed planner boundary: %+v", report)
			}
			assertAtomicLoadFailure(t, set, report)
		})
	}

	unknownContainers := []struct {
		name     string
		sourceID string
		document []byte
		code     ErrorCode
		pointer  string
		reason   string
	}{
		{
			name: "dependencies object", sourceID: "dependencies",
			document: resourceEnvelope(validCompatibilityTuple(), []byte(`{}`), []byte(`[]`)),
			code:     CodeInvalidOperation, pointer: "/migration/dependencies", reason: "invalid_operation",
		},
		{
			name: "operations object", sourceID: "operations",
			document: resourceEnvelope(validCompatibilityTuple(), []byte(`[]`), []byte(`{}`)),
			code:     CodeInvalidOperation, pointer: "/migration/operations", reason: "invalid_operation",
		},
		{
			name: "fields object", sourceID: "fields",
			document: resourceCreateModelEnvelope(validCompatibilityTuple(), []byte(`{}`)),
			code:     CodeInvalidIR, pointer: "/migration/operations/0/model/fields", reason: "invalid_ir",
		},
	}
	for _, test := range unknownContainers {
		t.Run(test.name, func(t *testing.T) {
			set, report, err := Load(Source{SourceID: test.sourceID, Document: test.document})
			definitionError := requireSourceDefinitionError(t, err)
			if definitionError.Code != test.code {
				t.Fatalf("error code = %q, want %q", definitionError.Code, test.code)
			}
			context := definitionError.Context()
			if context.Stage != "semantic" || context.JSONPointer != test.pointer || context.Reason != test.reason {
				t.Fatalf("undecidable container context = %+v", context)
			}
			if context.Limit != "" || context.Maximum != 0 || context.Actual != 0 {
				t.Fatalf("undecidable container guessed a resource limit: %+v", context)
			}
			if report.PlannerConstruction != 0 {
				t.Fatalf("semantic failure crossed planner boundary: %+v", report)
			}
			assertAtomicLoadFailure(t, set, report)
		})
	}
}

func TestSourceErrorsUseExactCodesAndImmutableReportContext(t *testing.T) {
	valid := resourceEnvelope(validCompatibilityTuple(), []byte(`[]`), []byte(`[]`))
	tests := []struct {
		name   string
		source Source
		code   ErrorCode
	}{
		{name: "invalid source", source: Source{SourceID: "", Document: valid}, code: CodeInvalidSource},
		{name: "invalid document", source: Source{SourceID: "source", Document: []byte(`{`)}, code: CodeInvalidDocument},
		{name: "definition format", source: Source{SourceID: "source", Document: resourceEnvelope(compatibilityTupleTest{2, 1, 1, 2}, []byte(`[]`), []byte(`[]`))}, code: CodeDefinitionFormatIncompatible},
		{name: "loader ABI", source: Source{SourceID: "source", Document: resourceEnvelope(compatibilityTupleTest{1, 2, 1, 2}, []byte(`[]`), []byte(`[]`))}, code: CodeLoaderABIIncompatible},
		{name: "operation codec", source: Source{SourceID: "source", Document: resourceEnvelope(compatibilityTupleTest{1, 1, 2, 2}, []byte(`[]`), []byte(`[]`))}, code: CodeOperationCodecIncompatible},
		{name: "Schema IR", source: Source{SourceID: "source", Document: resourceEnvelope(compatibilityTupleTest{1, 1, 1, 3}, []byte(`[]`), []byte(`[]`))}, code: CodeSchemaIRIncompatible},
		{name: "unsupported operation", source: Source{SourceID: "source", Document: resourceEnvelope(validCompatibilityTuple(), []byte(`[]`), []byte(`[{"kind":"run_python"}]`))}, code: CodeUnsupportedOperation},
		{name: "invalid operation", source: Source{SourceID: "source", Document: resourceEnvelope(validCompatibilityTuple(), []byte(`[]`), []byte(`[{}]`))}, code: CodeInvalidOperation},
		{name: "invalid IR", source: Source{SourceID: "source", Document: resourceCreateModelEnvelope(validCompatibilityTuple(), []byte(`[{}]`))}, code: CodeInvalidIR},
	}

	seen := make(map[ErrorCode]struct{}, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set, report, err := Load(test.source)
			definitionError := requireSourceDefinitionError(t, err)
			if definitionError.Category != CategorySource || definitionError.Code != test.code {
				t.Fatalf("source error = %+v, want category=%q code=%q", definitionError, CategorySource, test.code)
			}
			seen[definitionError.Code] = struct{}{}

			fromError := definitionError.Context()
			fromReport, exists := report.Failure()
			if !exists || !reflect.DeepEqual(fromReport, fromError) {
				t.Fatalf("report failure = %+v,%v; error context = %+v", fromReport, exists, fromError)
			}
			if fromError.Limit != "" || fromError.Maximum != 0 || fromError.Actual != 0 {
				t.Fatalf("non-resource source error has resource fields: %+v", fromError)
			}
			if len(fromError.GraphSources()) != 0 {
				t.Fatalf("source-owned error has graph mapping: %+v", fromError.GraphSources())
			}
			if report.PlannerConstruction != 0 {
				t.Fatalf("source-owned failure crossed planner boundary: %+v", report)
			}

			fromError.Stage = "mutated"
			fromError.SourceID = "mutated"
			fromError.Maximum = math.MaxUint64
			fromReport.Reason = "mutated"
			if definitionError.Context().Stage == "mutated" || definitionError.Context().SourceID == "mutated" {
				t.Fatal("Error.Context result aliases immutable error state")
			}
			reportedAgain, ok := report.Failure()
			if !ok || reportedAgain.Reason == "mutated" || reportedAgain.Maximum == math.MaxUint64 {
				t.Fatal("LoadReport.Failure result aliases immutable report state")
			}
			assertAtomicLoadFailure(t, set, report)
		})
	}
	if len(seen) != 9 {
		t.Fatalf("observed source-owned error codes = %d, want all 9", len(seen))
	}
}

func TestSaturatingAddFailsClosedBeforeUint64Wraparound(t *testing.T) {
	tests := []struct {
		name     string
		left     uint64
		right    uint64
		want     uint64
		overflow bool
	}{
		{name: "zero", left: 0, right: 0, want: 0},
		{name: "ordinary", left: MaxBatchBytes - 1, right: 1, want: MaxBatchBytes},
		{name: "uint64 boundary", left: math.MaxUint64 - 1, right: 1, want: math.MaxUint64},
		{name: "single overflow", left: math.MaxUint64, right: 1, want: math.MaxUint64, overflow: true},
		{name: "large overflow", left: math.MaxUint64 - 7, right: 42, want: math.MaxUint64, overflow: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, overflow := saturatingAdd(test.left, test.right)
			if got != test.want || overflow != test.overflow {
				t.Fatalf("saturatingAdd(%d,%d) = (%d,%v), want (%d,%v)", test.left, test.right, got, overflow, test.want, test.overflow)
			}
		})
	}
}

func TestDocumentResourceGuardsPrecedeLaterInvalidUTF8(t *testing.T) {
	payload := append(resourceNestedArray(MaxJSONDepth+1), 0xff)
	set, report, err := Load(Source{SourceID: "depth-before-invalid-utf8", Document: payload})
	requireResourceLimitFailure(t, set, report, err, expectedLimitFailure{
		code:           CodeInvalidDocument,
		stage:          "document",
		limit:          "json_depth",
		maximum:        MaxJSONDepth,
		actual:         MaxJSONDepth + 1,
		sourceID:       "depth-before-invalid-utf8",
		pointer:        strings.Repeat("/0", MaxJSONDepth),
		operationIndex: -1,
	})
}

func TestStrictScannerDoesNotMultiplyOneLongAncestorPointerByEveryCandidate(t *testing.T) {
	const duplicatePairs = 20_000
	ancestor := strings.Repeat("k", 256<<10)
	var document strings.Builder
	document.Grow(len(ancestor) + duplicatePairs*20)
	document.WriteString(`{"`)
	document.WriteString(ancestor)
	document.WriteString(`":{`)
	for index := 0; index < duplicatePairs; index++ {
		if index != 0 {
			document.WriteByte(',')
		}
		document.WriteString(`"a":null,"a":null`)
	}
	document.WriteString(`}}`)
	if document.Len() >= MaxDocumentBytes {
		t.Fatalf("amplification fixture bytes = %d, must stay below %d", document.Len(), MaxDocumentBytes)
	}

	_, stats, failures := scanJSONDocument(sourceSnapshot{
		sourceID: "long-ancestor",
		document: []byte(document.String()),
	})
	if !stats.Complete || stats.Values != 1+1+duplicatePairs*2 {
		t.Fatalf("long ancestor scan stats = %+v", stats)
	}
	if len(failures) != 1 {
		t.Fatalf("long ancestor failures = %d, want one canonical candidate", len(failures))
	}
	failure := failures[0]
	if failure.code != CodeInvalidDocument || failure.context.Stage != "document" || failure.context.Reason != "duplicate_key" || failure.context.JSONPointer != "/"+ancestor+"/a" {
		t.Fatalf("long ancestor failure = code:%q context:%+v", failure.code, failure.context)
	}
}

func TestStrictScannerDoesNotRerenderOneLongWinnerForEveryShortCandidate(t *testing.T) {
	const shortCandidates = 20_000
	ancestor := strings.Repeat("k", 256<<10)
	var document strings.Builder
	document.Grow(len(ancestor) + shortCandidates*32)
	document.WriteString(`{"`)
	document.WriteString(ancestor)
	document.WriteString(`":{"x":null,"x":null}`)
	for index := 0; index < shortCandidates; index++ {
		document.WriteString(fmt.Sprintf(`,"z%05d":{"x":null,"x":null}`, index))
	}
	document.WriteByte('}')
	if document.Len() >= MaxDocumentBytes {
		t.Fatalf("long winner fixture bytes = %d, must stay below %d", document.Len(), MaxDocumentBytes)
	}

	_, stats, failures := scanJSONDocument(sourceSnapshot{
		sourceID: "long-winner",
		document: []byte(document.String()),
	})
	if !stats.Complete || stats.Values != 1+(shortCandidates+1)*3 {
		t.Fatalf("long winner scan stats = %+v", stats)
	}
	if len(failures) != 1 {
		t.Fatalf("long winner failures = %d, want one canonical candidate", len(failures))
	}
	failure := failures[0]
	if failure.code != CodeInvalidDocument || failure.context.Stage != "document" || failure.context.Reason != "duplicate_key" || failure.context.JSONPointer != "/"+ancestor+"/x" {
		t.Fatalf("long winner failure = code:%q context:%+v", failure.code, failure.context)
	}
}

func TestLazyJSONPathComparatorMatchesRenderedRFC6901ByteOrder(t *testing.T) {
	tokens := []string{"", "a", "aa", "a\x00", "/", "~", "~0", "é", "\u2028"}
	paths := []*jsonPath{nil}
	for _, first := range tokens {
		parent := childJSONPath(nil, first)
		paths = append(paths, parent)
		for _, second := range tokens {
			paths = append(paths, childJSONPath(parent, second))
		}
	}

	for leftIndex, left := range paths {
		for rightIndex, right := range paths {
			got := compareJSONPaths(left, right)
			want := strings.Compare(renderJSONPath(left), renderJSONPath(right))
			if (got < 0) != (want < 0) || (got == 0) != (want == 0) || (got > 0) != (want > 0) {
				t.Fatalf(
					"compare paths[%d]=%q paths[%d]=%q = %d, want sign %d",
					leftIndex,
					renderJSONPath(left),
					rightIndex,
					renderJSONPath(right),
					got,
					want,
				)
			}
		}
	}
}

func TestDocumentResourceLimitRankIsCanonicalWhenMultipleCapsFail(t *testing.T) {
	deep := resourceNestedArray(MaxJSONDepth + 1)
	many := jsonDocumentWithValues(MaxDocumentJSONValues + 1)
	payload := make([]byte, 0, len(deep)+len(many)+3)
	payload = append(payload, '[')
	payload = append(payload, deep...)
	payload = append(payload, ',')
	payload = append(payload, many...)
	payload = append(payload, ']')

	set, report, err := Load(Source{SourceID: "combined-resource", Document: payload})
	requireResourceLimitFailure(t, set, report, err, expectedLimitFailure{
		code:           CodeInvalidDocument,
		stage:          "document",
		limit:          "json_depth",
		maximum:        MaxJSONDepth,
		actual:         MaxJSONDepth + 1,
		sourceID:       "combined-resource",
		pointer:        "/0" + strings.Repeat("/0", MaxJSONDepth-1),
		operationIndex: -1,
	})
}

func TestProductMaxLengthUsesTheLiteral32BitWireBoundary(t *testing.T) {
	maximum := bytes.Replace(
		lifecycleRootDocument(),
		[]byte(`"max_length":64`),
		[]byte(`"max_length":2147483647`),
		1,
	)
	set, report, err := Load(Source{SourceID: "maximum", Document: maximum})
	if err != nil {
		t.Fatalf("Load(max_length=2147483647): %v", err)
	}
	if report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 || len(set.Definitions()) != 1 {
		t.Fatalf("maximum boundary publication = set:%#v report:%+v", set.Definitions(), report)
	}

	overflow := bytes.Replace(
		lifecycleRootDocument(),
		[]byte(`"max_length":64`),
		[]byte(`"max_length":2147483648`),
		1,
	)
	failed, failedReport, err := Load(Source{SourceID: "overflow", Document: overflow})
	definitionError := requireSourceDefinitionError(t, err)
	context := definitionError.Context()
	if definitionError.Code != CodeInvalidDocument || context.Stage != "semantic" || context.JSONPointer != "/migration/operations/0/model/fields/1/max_length" || context.Reason != "out_of_range" {
		t.Fatalf("max_length=2147483648 failure = code:%q context:%+v", definitionError.Code, context)
	}
	assertAtomicLoadFailure(t, failed, failedReport)

	lexicalOverflow := bytes.Replace(
		lifecycleRootDocument(),
		[]byte(`"max_length":64`),
		[]byte(`"max_length":9223372036854775808`),
		1,
	)
	failed, failedReport, err = Load(Source{SourceID: "lexical-overflow", Document: lexicalOverflow})
	definitionError = requireSourceDefinitionError(t, err)
	context = definitionError.Context()
	if definitionError.Code != CodeInvalidDocument || context.Stage != "document" || context.JSONPointer != "/migration/operations/0/model/fields/1/max_length" || context.Reason != "out_of_range" {
		t.Fatalf("signed-int64 overflow failure = code:%q context:%+v", definitionError.Code, context)
	}
	assertAtomicLoadFailure(t, failed, failedReport)
}

func FuzzStrictScannerViaLoadNeverPanics(f *testing.F) {
	valid := resourceEnvelope(validCompatibilityTuple(), []byte(`[]`), []byte(`[]`))
	duplicate := bytes.Replace(valid, []byte(`"name":"0001_initial"`), []byte(`"name":"0001_initial","name":"shadow"`), 1)
	seeds := [][]byte{
		nil,
		[]byte(`{`),
		[]byte(`[]`),
		valid,
		append([]byte{0xef, 0xbb, 0xbf}, valid...),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
		duplicate,
		[]byte(`{"producer":{"version":"\uD800"}}`),
		[]byte(`{"producer":{"version":"\uDC00"}}`),
		[]byte(`{"producer":{"version":"\uD83D\uDE00"}}`),
		[]byte{0xff, 0xfe, 0xfd},
		resourceNestedArray(MaxJSONDepth + 1),
		[]byte(`[1.0,1e2,01,-0,9223372036854775808]`),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, document []byte) {
		if len(document) > MaxDocumentBytes+1 {
			t.Skip()
		}
		set, report, err := Load(Source{SourceID: "fuzz", Document: document})
		if err == nil {
			if failure, exists := report.Failure(); exists {
				t.Fatalf("successful Load published failure %+v", failure)
			}
			if report.DefinitionSetsPublished != 1 {
				t.Fatalf("successful Load set publication = %d, want 1", report.DefinitionSetsPublished)
			}
			return
		}
		assertAtomicLoadFailure(t, set, report)
		if _, exists := report.Failure(); !exists {
			t.Fatalf("failed Load(%T) omitted failure report: %v", err, err)
		}
	})
}

type expectedLimitFailure struct {
	code           ErrorCode
	stage          string
	limit          string
	maximum        uint64
	actual         uint64
	sourceID       string
	pointer        string
	app            string
	migration      string
	operationIndex int
}

func requireResourceLimitFailure(t *testing.T, set Set, report LoadReport, err error, want expectedLimitFailure) {
	t.Helper()
	definitionError := requireSourceDefinitionError(t, err)
	if definitionError.Category != CategorySource || definitionError.Code != want.code {
		t.Fatalf("limit error = %+v, want category=%q code=%q", definitionError, CategorySource, want.code)
	}
	context := definitionError.Context()
	if context.Stage != want.stage || context.Reason != "resource_limit_exceeded" || context.Limit != want.limit ||
		context.Maximum != want.maximum || context.Actual != want.actual || context.SourceID != want.sourceID ||
		context.JSONPointer != want.pointer || context.App != want.app || context.Name != want.migration || context.OperationIndex != want.operationIndex {
		t.Fatalf("limit context = %+v, want stage=%q source=%q pointer=%q migration=%s.%s operation=%d limit=%q maximum=%d actual=%d",
			context, want.stage, want.sourceID, want.pointer, want.app, want.migration, want.operationIndex, want.limit, want.maximum, want.actual)
	}
	if len(context.GraphSources()) != 0 {
		t.Fatalf("resource failure leaked graph identity: %+v graph=%+v", context, context.GraphSources())
	}
	if report.PlannerConstruction != 0 {
		t.Fatalf("resource failure crossed planner boundary: %+v", report)
	}
	reported, exists := report.Failure()
	if !exists || !reflect.DeepEqual(reported, context) {
		t.Fatalf("report failure = %+v,%v; error context = %+v", reported, exists, context)
	}
	assertAtomicLoadFailure(t, set, report)
}

func requireSourceDefinitionError(t *testing.T, err error) *Error {
	t.Helper()
	var definitionError *Error
	if !errors.As(err, &definitionError) {
		t.Fatalf("error = %T %v, want *definition.Error", err, err)
	}
	return definitionError
}

func assertLimitGuardPassed(t *testing.T, report LoadReport, err error, limit string) {
	t.Helper()
	if err == nil {
		return
	}
	context, exists := report.Failure()
	if exists && context.Reason == "resource_limit_exceeded" && context.Limit == limit {
		t.Fatalf("%s guard rejected maximum-1/equal input: %+v (%v)", limit, context, err)
	}
}

func assertAtomicLoadFailure(t *testing.T, set Set, report LoadReport) {
	t.Helper()
	if set.Digest() != EmptySetDigest || len(set.Definitions()) != 0 || len(set.Sources()) != 0 {
		t.Fatalf("failed Load returned non-zero Set: digest=%q definitions=%d sources=%d", set.Digest(), len(set.Definitions()), len(set.Sources()))
	}
	if report.PlannerConstruction != 0 && report.DefinitionsPublished == 0 {
		// A raw graph failure is allowed to construct the planner exactly once;
		// every resource/source failure tested here remains at zero.
		if report.PlannerConstruction != 1 {
			t.Fatalf("failed Load planner constructions = %d", report.PlannerConstruction)
		}
	}
	if report.DefinitionsPublished != 0 || report.DefinitionSetsPublished != 0 {
		t.Fatalf("failed Load partially published: %+v", report)
	}
}

func resourceSourceCount(count int) []Source {
	sources := make([]Source, count)
	for index := range sources {
		sources[index].SourceID = fmt.Sprintf("source-%04d", index)
	}
	return sources
}

func resourceSourceIDBytes(length int) []Source {
	return []Source{{SourceID: strings.Repeat("x", length), Document: []byte(`{`)}}
}

func resourceDocumentBytes(length int) []Source {
	return []Source{{SourceID: "document", Document: bytes.Repeat([]byte{' '}, length)}}
}

func resourceBatchBytes(length int) []Source {
	full := bytes.Repeat([]byte{' '}, MaxDocumentBytes)
	sources := make([]Source, 0, length/MaxDocumentBytes+1)
	remaining := length
	for remaining > 0 {
		current := remaining
		if current > MaxDocumentBytes {
			current = MaxDocumentBytes
		}
		document := full
		if current != MaxDocumentBytes {
			document = bytes.Repeat([]byte{' '}, current)
		}
		sources = append(sources, Source{SourceID: fmt.Sprintf("batch-%04d", len(sources)), Document: document})
		remaining -= current
	}
	return sources
}

func resourceJSONDepth(depth int) []Source {
	return []Source{{SourceID: "depth", Document: resourceNestedArray(depth)}}
}

func resourceNestedArray(depth int) []byte {
	document := make([]byte, 0, depth*2+4)
	document = append(document, bytes.Repeat([]byte{'['}, depth)...)
	document = append(document, "null"...)
	document = append(document, bytes.Repeat([]byte{']'}, depth)...)
	return document
}

func resourceDocumentJSONValues(values int) []Source {
	return []Source{{SourceID: "document-values", Document: jsonDocumentWithValues(values)}}
}

func resourceBatchJSONValues(values int) []Source {
	sources := make([]Source, 0, values/MaxDocumentJSONValues+1)
	remaining := values
	for remaining > 0 {
		current := remaining
		if current > MaxDocumentJSONValues {
			current = MaxDocumentJSONValues
		}
		sources = append(sources, Source{
			SourceID: fmt.Sprintf("value-%04d", len(sources)),
			Document: jsonDocumentWithValues(current),
		})
		remaining -= current
	}
	return sources
}

func jsonDocumentWithValues(values int) []byte {
	if values <= 0 {
		return nil
	}
	if values == 1 {
		return []byte(`null`)
	}
	return repeatedJSONArray(`null`, values-1)
}

func resourceDependencies(count int) []Source {
	return []Source{{
		SourceID: "dependencies",
		Document: resourceEnvelope(validCompatibilityTuple(), repeatedJSONArray(`{}`, count), []byte(`[]`)),
	}}
}

func resourceOperations(count int) []Source {
	return []Source{{
		SourceID: "operations",
		Document: resourceEnvelope(validCompatibilityTuple(), []byte(`[]`), repeatedJSONArray(`{}`, count)),
	}}
}

func resourceCreateModelFields(count int) []Source {
	return []Source{{
		SourceID: "fields",
		Document: resourceCreateModelEnvelope(validCompatibilityTuple(), repeatedJSONArray(`{}`, count)),
	}}
}

type compatibilityTupleTest struct {
	definitionFormat int64
	loaderABI        int64
	operationCodec   int64
	schemaIR         int64
}

func validCompatibilityTuple() compatibilityTupleTest {
	return compatibilityTupleTest{definitionFormat: 1, loaderABI: 1, operationCodec: 1, schemaIR: 2}
}

func resourceEnvelope(tuple compatibilityTupleTest, dependencies, operations []byte) []byte {
	return []byte(fmt.Sprintf(
		`{"compatibility":{"definition_format":%d,"loader_abi":%d,"operation_codec":%d,"schema_ir":%d},"producer":{"name":"resource-test","version":"1"},"migration":{"app":"alpha","name":"0001_initial","dependencies":%s,"operations":%s}}`,
		tuple.definitionFormat, tuple.loaderABI, tuple.operationCodec, tuple.schemaIR, dependencies, operations,
	))
}

func resourceCreateModelEnvelope(tuple compatibilityTupleTest, fields []byte) []byte {
	operation := []byte(fmt.Sprintf(
		`[{"kind":"create_model","app_label":"alpha","model":{"name":"widget","go_name":"Widget","db_table":"alpha_widget","fields":%s}}]`,
		fields,
	))
	return resourceEnvelope(tuple, []byte(`[]`), operation)
}

func repeatedJSONArray(element string, count int) []byte {
	if count <= 0 {
		return []byte(`[]`)
	}
	var builder strings.Builder
	builder.Grow(2 + count*len(element) + count - 1)
	builder.WriteByte('[')
	for index := 0; index < count; index++ {
		if index != 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(element)
	}
	builder.WriteByte(']')
	return []byte(builder.String())
}
