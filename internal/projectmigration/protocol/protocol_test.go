package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

const (
	testDigestA = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testDigestB = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
)

func TestCanonicalRequestStrictParsingAndFreshOwnership(t *testing.T) {
	t.Parallel()
	want := "{\"protocol_version\":1,\"command\":\"migrations.makemigrations\"}\n"
	first := RequestDocument()
	if string(first) != want {
		t.Fatalf("request = %q, want %q", first, want)
	}
	first[0] = '!'
	if string(RequestDocument()) != want {
		t.Fatal("RequestDocument retained caller mutation")
	}
	for _, reader := range []io.Reader{
		bytes.NewReader(RequestDocument()),
		iotest.OneByteReader(bytes.NewReader(RequestDocument())),
	} {
		failure, failed, err := ReadRequest(reader)
		if err != nil || failed || failure != (Failure{}) {
			t.Fatalf("ReadRequest = %+v, %v, %v", failure, failed, err)
		}
	}
}

func TestRequestRejectsEveryNoncanonicalShapeAndClassifiesVersion(t *testing.T) {
	t.Parallel()
	canonical := string(RequestDocument())
	tests := []struct {
		name string
		wire []byte
		code string
	}{
		{name: "reordered", wire: []byte("{\"command\":\"migrations.makemigrations\",\"protocol_version\":1}\n"), code: CodeInvalidRequest},
		{name: "leading whitespace", wire: []byte(" " + canonical), code: CodeInvalidRequest},
		{name: "interior whitespace", wire: []byte("{\"protocol_version\": 1,\"command\":\"migrations.makemigrations\"}\n"), code: CodeInvalidRequest},
		{name: "missing newline", wire: []byte(strings.TrimSuffix(canonical, "\n")), code: CodeInvalidRequest},
		{name: "extra newline", wire: []byte(canonical + "\n"), code: CodeInvalidRequest},
		{name: "duplicate", wire: []byte("{\"protocol_version\":1,\"protocol_version\":1,\"command\":\"migrations.makemigrations\"}\n"), code: CodeInvalidRequest},
		{name: "unknown", wire: []byte("{\"protocol_version\":1,\"command\":\"migrations.makemigrations\",\"dsn\":\"secret\"}\n"), code: CodeInvalidRequest},
		{name: "missing version", wire: []byte("{\"command\":\"migrations.makemigrations\"}\n"), code: CodeInvalidRequest},
		{name: "missing command", wire: []byte("{\"protocol_version\":1}\n"), code: CodeInvalidRequest},
		{name: "trailing value", wire: []byte(canonical + "{}"), code: CodeInvalidRequest},
		{name: "wrong command", wire: []byte("{\"protocol_version\":1,\"command\":\"migrations.migrate\"}\n"), code: CodeInvalidRequest},
		{name: "canonical incompatible", wire: []byte("{\"protocol_version\":2,\"command\":\"migrations.makemigrations\"}\n"), code: CodeProtocolIncompatible},
		{name: "noncanonical incompatible", wire: []byte("{\"protocol_version\":2.0,\"command\":\"migrations.makemigrations\"}\n"), code: CodeInvalidRequest},
		{name: "version bound", wire: []byte("{\"protocol_version\":65536,\"command\":\"migrations.makemigrations\"}\n"), code: CodeInvalidRequest},
		{name: "BOM", wire: append([]byte{0xef, 0xbb, 0xbf}, RequestDocument()...), code: CodeInvalidRequest},
		{name: "invalid UTF-8", wire: append(append([]byte(nil), RequestDocument()...), 0xff), code: CodeInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure, failed, err := ReadRequest(bytes.NewReader(test.wire))
			want := Failure{Category: CategoryProtocol, Code: test.code}
			if err != nil || !failed || failure != want {
				t.Fatalf("ReadRequest = %+v, %v, %v, want %+v", failure, failed, err, want)
			}
		})
	}
}

func TestRequestTransportNoProgressAndDrainPrecedence(t *testing.T) {
	t.Parallel()
	readerErr := errors.New("reader failed")
	if _, failed, err := ReadRequest(errorReader{err: readerErr}); failed || !errors.Is(err, readerErr) {
		t.Fatalf("reader error = failed %v err %v", failed, err)
	}
	if _, failed, err := ReadRequest(nil); failed || err == nil {
		t.Fatalf("nil reader = failed %v err %v", failed, err)
	}
	if _, failed, err := ReadRequest(noProgressReader{}); failed || !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("no-progress reader = failed %v err %v", failed, err)
	}

	oversized := &trackingReader{chunks: [][]byte{
		bytes.Repeat([]byte{'x'}, MaxRequestBytes),
		bytes.Repeat([]byte{'y'}, 17),
		[]byte("drained-tail"),
	}}
	failure, failed, err := ReadRequest(oversized)
	wantConsumed := MaxRequestBytes + 17 + len("drained-tail")
	if err != nil || !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}) ||
		oversized.consumed != wantConsumed || len(oversized.chunks) != 0 {
		t.Fatalf("oversized = %+v, %v, %v, consumed=%d", failure, failed, err, oversized.consumed)
	}

	tailErr := errors.New("post-cap transport failure")
	failingTail := &trackingReader{
		chunks:   [][]byte{bytes.Repeat([]byte{'z'}, MaxRequestBytes+1)},
		finalErr: tailErr,
	}
	if _, failed, err := ReadRequest(failingTail); failed || !errors.Is(err, tailErr) {
		t.Fatalf("post-cap transport = failed %v err %v", failed, err)
	}
}

func TestProjectSpecNormalizationDigestAndCallerOwnership(t *testing.T) {
	t.Parallel()
	input := permutedSpec()
	original := cloneProjectSpec(input)
	normalized, err := NormalizeProjectSpec(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatal("NormalizeProjectSpec mutated caller")
	}
	if got := []string{normalized.Apps[0].Schema.AppLabel, normalized.Apps[1].Schema.AppLabel}; !reflect.DeepEqual(got, []string{"accounts", "content"}) {
		t.Fatalf("normalized app order = %v", got)
	}
	for index := range normalized.Apps {
		model := normalized.Apps[index].Schema.Models[0]
		if model.DBTable == "" || len(model.Fields) < 2 || model.Fields[0].Kind != ir.FieldAuto {
			t.Fatalf("app[%d] schema not normalized: %+v", index, model)
		}
	}

	digest, err := ProjectSpecDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDigest, err := ProjectSpecDigest(normalized)
	if err != nil || digest != canonicalDigest || !validDigest(digest) {
		t.Fatalf("digest = %q/%q, err=%v", digest, canonicalDigest, err)
	}
	permuted := input
	permuted.Apps = append([]codegen.AppSpec(nil), input.Apps...)
	permuted.Apps[0], permuted.Apps[1] = permuted.Apps[1], permuted.Apps[0]
	permutedDigest, err := ProjectSpecDigest(permuted)
	if err != nil || permutedDigest != digest {
		t.Fatalf("permuted digest = %q, want %q, err=%v", permutedDigest, digest, err)
	}

	normalized.Apps[0].Schema.Models[0].Fields[0].Name = "mutated"
	again, err := NormalizeProjectSpec(input)
	if err != nil || again.Apps[0].Schema.Models[0].Fields[0].Name != "id" {
		t.Fatal("normalized output retained caller mutation")
	}
}

func TestSuccessCanonicalRoundTripBase64DeterminismAndDeepOwnership(t *testing.T) {
	t.Parallel()
	result := validResult(t)
	document, err := EncodeResponse(Response{OK: true, Result: result})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(document, []byte(`"candidate_path"`)) || bytes.Contains(document, []byte(`"source_id":"migrations/content_`)) ||
		bytes.Contains(document, []byte(`"candidate_sha256"`)) {
		t.Fatalf("derived candidate authority leaked onto wire: %s", document)
	}
	if !bytes.Contains(document, []byte(`"document":"AAEC/w=="`)) || !bytes.Contains(document, []byte(`"document":"e30="`)) {
		t.Fatalf("documents are not canonical padded base64: %s", document)
	}

	// Encoder normalizes app order without retaining or mutating the caller.
	result.ProjectSpec.Apps[0].Alias = "caller_mutation"
	result.Candidates[0].Document[0] = '!'
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || !response.OK {
		t.Fatalf("ParseResponse = %+v, %+v, %v", response, failure, failed)
	}
	if response.Result.ProjectSpec.Apps[0].Schema.AppLabel != "accounts" ||
		!bytes.Equal(response.Result.Candidates[0].Document, []byte("{}")) {
		t.Fatalf("parsed result changed: %+v", response.Result)
	}

	response.Result.ProjectSpec.Apps[0].Schema.Models[0].Fields[0].Name = "mutated"
	response.Result.ProgrammaticCatalog.Sources[0].Document[0] = '!'
	response.Result.Candidates[0].Document[0] = '!'
	again, _, failed := ParseResponse(document, true)
	if failed || again.Result.ProjectSpec.Apps[0].Schema.Models[0].Fields[0].Name != "id" ||
		!bytes.Equal(again.Result.ProgrammaticCatalog.Sources[0].Document, []byte{0, 1, 2, 0xff}) ||
		!bytes.Equal(again.Result.Candidates[0].Document, []byte("{}")) {
		t.Fatal("ParseResponse retained prior caller mutation")
	}

	permuted := validResult(t)
	permuted.ProjectSpec.Apps[0], permuted.ProjectSpec.Apps[1] = permuted.ProjectSpec.Apps[1], permuted.ProjectSpec.Apps[0]
	permutedDocument, err := EncodeResponse(Response{OK: true, Result: permuted})
	if err != nil || !bytes.Equal(permutedDocument, document) {
		t.Fatalf("permuted encoding differs: err=%v\n%s\n%s", err, document, permutedDocument)
	}
}

func TestClosedFailureRoundTripTaxonomyAndTransportPrecedence(t *testing.T) {
	t.Parallel()
	linked := Failure{Category: CategoryDetection, Code: "unsupported_change"}
	document, err := EncodeResponse(Response{Failure: linked})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"protocol_version":1,"status":"error","error":{"category":"migration_autodetect_error","code":"unsupported_change"}}`
	if string(document) != want {
		t.Fatalf("failure = %s, want %s", document, want)
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || response.OK || response.Failure != linked {
		t.Fatalf("logical failure = %+v, %+v, %v", response, failure, failed)
	}
	_, failure, failed = ParseResponse(document, false)
	if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}) {
		t.Fatalf("transport precedence = %+v, %v", failure, failed)
	}

	linkedValues := []Failure{
		{Category: CategoryProtocol, Code: CodeInvalidRequest},
		{Category: CategoryDeclaration, Code: CodeProjectSpecLoadFailed},
		{Category: CategoryDiscovery, Code: CodeUnsafeSourceEntry},
		{Category: CategorySource, Code: "invalid_definition_document"},
		{Category: CategoryGraph, Code: "dependency_cycle"},
		{Category: CategoryDetection, Code: "invalid_relation"},
		{Category: CategoryCandidate, Code: CodeCandidateValidationFailed},
	}
	for _, candidate := range linkedValues {
		if !IsLinkedFailure(candidate) {
			t.Errorf("linked failure rejected: %+v", candidate)
		}
	}
	for _, candidate := range []Failure{{}, {Category: CategoryProtocol, Code: CodeRunnerFailed}, {Category: CategoryDetection, Code: "secret"}} {
		if IsLinkedFailure(candidate) {
			t.Errorf("unlinked failure accepted: %+v", candidate)
		}
	}
}

func TestResponseRejectsNoncanonicalUnknownNullDigestBase64AndRosterFailures(t *testing.T) {
	t.Parallel()
	result := validResult(t)
	valid, err := EncodeResponse(Response{OK: true, Result: result})
	if err != nil {
		t.Fatal(err)
	}
	emptyCandidates := result
	emptyCandidates.Candidates = []Candidate{}
	emptyWire, err := EncodeResponse(Response{OK: true, Result: emptyCandidates})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		wire []byte
		code string
	}{
		{name: "duplicate", wire: bytes.Replace(valid, []byte(`"status":"ok"`), []byte(`"status":"ok","status":"ok"`), 1), code: CodeInvalidResponse},
		{name: "unknown top", wire: bytes.Replace(valid, []byte(`"result":`), []byte(`"secret":"dsn","result":`), 1), code: CodeInvalidResponse},
		{name: "unknown nested", wire: bytes.Replace(valid, []byte(`"writer_root":`), []byte(`"secret":"dsn","writer_root":`), 1), code: CodeInvalidResponse},
		{name: "reordered", wire: reorderTopLevelSuccess(t, valid), code: CodeInvalidResponse},
		{name: "leading whitespace", wire: append([]byte{' '}, valid...), code: CodeInvalidResponse},
		{name: "trailing newline", wire: append(append([]byte(nil), valid...), '\n'), code: CodeInvalidResponse},
		{name: "trailing value", wire: append(append([]byte(nil), valid...), []byte(`{}`)...), code: CodeInvalidResponse},
		{name: "BOM", wire: append([]byte{0xef, 0xbb, 0xbf}, valid...), code: CodeInvalidResponse},
		{name: "invalid UTF-8", wire: append(append([]byte(nil), valid...), 0xff), code: CodeInvalidResponse},
		{name: "null roster", wire: bytes.Replace(emptyWire, []byte(`"candidates":[]`), []byte(`"candidates":null`), 1), code: CodeInvalidResponse},
		{name: "invalid project digest", wire: bytes.Replace(valid, []byte(result.ProjectSpecDigest), []byte(testDigestA), 1), code: CodeInvalidResponse},
		{name: "uppercase digest", wire: bytes.Replace(valid, []byte(testDigestA), []byte(strings.ToUpper(testDigestA)), 1), code: CodeInvalidResponse},
		{name: "uppercase snapshot", wire: bytes.Replace(valid, []byte(strings.Repeat("b", 64)), []byte(strings.Repeat("B", 64)), 1), code: CodeInvalidResponse},
		{name: "invalid base64", wire: bytes.Replace(valid, []byte(`AAEC/w==`), []byte(`AAEC%%%%`), 1), code: CodeInvalidResponse},
		{name: "unpadded base64", wire: bytes.Replace(valid, []byte(`e30=`), []byte(`e30`), 1), code: CodeInvalidResponse},
		{name: "canonical incompatible", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":2`), 1), code: CodeProtocolIncompatible},
		{name: "noncanonical incompatible", wire: bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":2.0`), 1), code: CodeInvalidResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, failure, failed := ParseResponse(test.wire, true)
			want := Failure{Category: CategoryProtocol, Code: test.code}
			if !failed || failure != want {
				t.Fatalf("ParseResponse = %+v, %v, want %+v", failure, failed, want)
			}
		})
	}

	unsorted := result
	unsorted.ProgrammaticCatalog.Sources = []Source{
		{SourceID: "z", Document: []byte("z")},
		{SourceID: "a", Document: []byte("a")},
	}
	unsorted.ProgrammaticCatalog.SourceCount = 2
	unsorted.ProgrammaticCatalog.DocumentBytes = 2
	unsortedWire := rawSuccess(t, unsorted)
	if _, failure, failed := ParseResponse(unsortedWire, true); !failed || failure.Code != CodeInvalidResponse {
		t.Fatalf("unsorted sources = %+v, %v", failure, failed)
	}

	duplicateCandidates := result
	duplicateCandidates.Candidates = append(append([]Candidate(nil), result.Candidates...), result.Candidates[0])
	duplicateWire := rawSuccess(t, duplicateCandidates)
	if _, failure, failed := ParseResponse(duplicateWire, true); !failed || failure.Code != CodeInvalidResponse {
		t.Fatalf("duplicate candidates = %+v, %v", failure, failed)
	}
}

func TestEncodeRejectsNilAmbiguousInvalidSummariesAndHashes(t *testing.T) {
	t.Parallel()
	valid := validResult(t)
	mutations := []struct {
		name   string
		mutate func(*Result)
	}{
		{name: "nil apps", mutate: func(result *Result) { result.ProjectSpec.Apps = nil }},
		{name: "nil program sources", mutate: func(result *Result) { result.ProgrammaticCatalog.Sources = nil }},
		{name: "nil candidates", mutate: func(result *Result) { result.Candidates = nil }},
		{name: "project digest mismatch", mutate: func(result *Result) { result.ProjectSpecDigest = testDigestA }},
		{name: "raw snapshot prefixed", mutate: func(result *Result) { result.ProjectSnapshotSHA256 = testDigestA }},
		{name: "invalid writer root", mutate: func(result *Result) { result.WriterRoot = "../migrations" }},
		{name: "filesystem count bytes mismatch", mutate: func(result *Result) { result.FilesystemCatalog.SourceCount = 0 }},
		{name: "program count mismatch", mutate: func(result *Result) { result.ProgrammaticCatalog.SourceCount = 2 }},
		{name: "program bytes mismatch", mutate: func(result *Result) { result.ProgrammaticCatalog.DocumentBytes++ }},
		{name: "program digest uppercase", mutate: func(result *Result) { result.ProgrammaticCatalog.Digest = strings.ToUpper(testDigestA) }},
		{name: "empty semantic digest for sources", mutate: func(result *Result) { result.DefinitionSetDigest = definition.EmptySetDigest }},
		{name: "nil source document", mutate: func(result *Result) { result.ProgrammaticCatalog.Sources[0].Document = nil }},
		{name: "empty source id", mutate: func(result *Result) { result.ProgrammaticCatalog.Sources[0].SourceID = "" }},
		{name: "unsafe candidate app", mutate: func(result *Result) { result.Candidates[0].App = "../content" }},
		{name: "unsafe candidate name", mutate: func(result *Result) { result.Candidates[0].Name = "0001/initial" }},
		{name: "noncanonical candidate number", mutate: func(result *Result) { result.Candidates[0].Name = "initial" }},
		{name: "program candidate source collision", mutate: func(result *Result) {
			result.ProgrammaticCatalog.Sources[0].SourceID = "migrations/content_0002_article_summary.godj.json"
		}},
		{name: "nil candidate document", mutate: func(result *Result) { result.Candidates[0].Document = nil }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneResult(valid)
			test.mutate(&candidate)
			if _, err := EncodeResponse(Response{OK: true, Result: candidate}); err == nil {
				t.Fatalf("invalid result accepted: %+v", candidate)
			}
		})
	}

	if _, err := EncodeResponse(Response{OK: true, Result: valid, Failure: Failure{Category: CategoryDetection, Code: "unsupported_change"}}); err == nil {
		t.Fatal("ambiguous success accepted")
	}
	if _, err := EncodeResponse(Response{Failure: Failure{Category: CategoryDetection, Code: "secret_detail"}}); err == nil {
		t.Fatal("unknown failure accepted")
	}
	if _, err := EncodeResponse(Response{}); err == nil {
		t.Fatal("zero response accepted")
	}
}

func TestDocumentBatchCandidateAndProgrammaticResourceBoundaries(t *testing.T) {
	valid := validResult(t)
	valid.ProgrammaticCatalog = ProgrammaticCatalog{SourceCount: 0, DocumentBytes: 0, Digest: testDigestB, Sources: []Source{}}
	valid.DefinitionSetDigest = definition.EmptySetDigest

	maximumDocument := bytes.Repeat([]byte{'x'}, definition.MaxDocumentBytes)
	valid.FilesystemCatalog = CatalogSummary{
		SourceCount:   1,
		DocumentBytes: definition.MaxBatchBytes - definition.MaxDocumentBytes,
		Digest:        testDigestA,
	}
	valid.DefinitionSetDigest = testDigestA
	valid.Candidates = []Candidate{{App: "content", Name: "0001_initial", Document: maximumDocument}}
	if _, err := EncodeResponse(Response{OK: true, Result: valid}); err != nil {
		t.Fatalf("exact document/batch maximum rejected: %v", err)
	}
	valid.FilesystemCatalog.DocumentBytes++
	if _, err := EncodeResponse(Response{OK: true, Result: valid}); err == nil {
		t.Fatal("batch maximum+1 accepted")
	}
	valid.FilesystemCatalog.DocumentBytes--
	valid.Candidates[0].Document = append(maximumDocument, 'x')
	if _, err := EncodeResponse(Response{OK: true, Result: valid}); err == nil {
		t.Fatal("document maximum+1 accepted")
	}

	countBoundary := validResult(t)
	countBoundary.ProgrammaticCatalog = ProgrammaticCatalog{Digest: testDigestB, Sources: []Source{}}
	countBoundary.FilesystemCatalog = CatalogSummary{Digest: testDigestA}
	countBoundary.DefinitionSetDigest = definition.EmptySetDigest
	countBoundary.Candidates = make([]Candidate, MaxCandidates)
	for index := range countBoundary.Candidates {
		countBoundary.Candidates[index] = Candidate{
			App:      "content",
			Name:     fmt.Sprintf("%04d_candidate", index+1),
			Document: []byte{'x'},
		}
	}
	if _, err := EncodeResponse(Response{OK: true, Result: countBoundary}); err != nil {
		t.Fatalf("candidate maximum rejected: %v", err)
	}
	countBoundary.Candidates = append(countBoundary.Candidates, Candidate{App: "content", Name: "overflow", Document: []byte{'x'}})
	if _, err := EncodeResponse(Response{OK: true, Result: countBoundary}); err == nil {
		t.Fatal("candidate maximum+1 accepted")
	}

	programBoundary := validResult(t)
	programBoundary.FilesystemCatalog = CatalogSummary{Digest: testDigestA}
	programBoundary.ProgrammaticCatalog = ProgrammaticCatalog{
		SourceCount:   MaxProgrammaticSources,
		DocumentBytes: MaxProgrammaticSources,
		Digest:        testDigestB,
		Sources:       make([]Source, MaxProgrammaticSources),
	}
	for index := range programBoundary.ProgrammaticCatalog.Sources {
		programBoundary.ProgrammaticCatalog.Sources[index] = Source{
			SourceID: fmt.Sprintf("source-%04d", index),
			Document: []byte{'x'},
		}
	}
	programBoundary.DefinitionSetDigest = testDigestA
	programBoundary.Candidates = []Candidate{}
	if _, err := EncodeResponse(Response{OK: true, Result: programBoundary}); err != nil {
		t.Fatalf("programmatic maximum rejected: %v", err)
	}
	programBoundary.ProgrammaticCatalog.SourceCount++
	if _, err := EncodeResponse(Response{OK: true, Result: programBoundary}); err == nil {
		t.Fatal("programmatic maximum+1 accepted")
	}
}

func TestWriterRootResourceBoundaryRoundTripsAtExactLimit(t *testing.T) {
	t.Parallel()
	boundary := validResult(t)
	boundary.WriterRoot = strings.Repeat("a", definition.MaxSourceIDBytes)
	boundary.Candidates = []Candidate{}
	document, err := EncodeResponse(Response{OK: true, Result: boundary})
	if err != nil {
		t.Fatalf("writer root maximum rejected: %v", err)
	}
	parsed, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || !parsed.OK || parsed.Result.WriterRoot != boundary.WriterRoot {
		t.Fatalf("writer root maximum round trip = %+v, %+v, %v", parsed, failure, failed)
	}

	overflow := cloneResult(boundary)
	overflow.WriterRoot += "a"
	if _, err := EncodeResponse(Response{OK: true, Result: overflow}); err == nil {
		t.Fatal("writer root maximum+1 encoded")
	}
	if _, failure, failed := ParseResponse(rawSuccess(t, overflow), true); !failed ||
		failure != (Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}) {
		t.Fatalf("writer root maximum+1 parsed: %+v, %v", failure, failed)
	}
}

func TestJSONScannerDepthNullValueAndSizeBoundaries(t *testing.T) {
	t.Parallel()
	maximumDepth := strings.Repeat("[", MaxJSONDepth) + "0" + strings.Repeat("]", MaxJSONDepth)
	if err := scanJSONDocument([]byte(maximumDepth), MaxResponseBytes); err != nil {
		t.Fatalf("maximum depth rejected: %v", err)
	}
	if err := scanJSONDocument([]byte("["+maximumDepth+"]"), MaxResponseBytes); err == nil {
		t.Fatal("maximum depth+1 accepted")
	}
	if err := scanJSONDocument([]byte(`{"value":null}`), MaxResponseBytes); err == nil {
		t.Fatal("JSON null accepted")
	}
	if err := scanJSONDocument([]byte(`{"x":1,"x":2}`), MaxResponseBytes); err == nil {
		t.Fatal("duplicate key accepted")
	}
	if err := validateDocumentSize(MaxResponseBytes, MaxResponseBytes); err != nil {
		t.Fatalf("maximum response rejected: %v", err)
	}
	if err := validateDocumentSize(MaxResponseBytes+1, MaxResponseBytes); err == nil {
		t.Fatal("maximum response+1 accepted")
	}
	if _, failure, failed := ParseResponse(bytes.Repeat([]byte{'x'}, MaxResponseBytes+1), true); !failed || failure.Code != CodeInvalidResponse {
		t.Fatalf("oversized response = %+v, %v", failure, failed)
	}
}

func TestMeasureMatchesCanonicalEncodingAndRejectsOverflow(t *testing.T) {
	t.Parallel()
	result := validResult(t)
	wire := toWireResult(result)
	sizer := &wireSizer{maximum: MaxResponseBytes}
	if !sizer.literal(`{"protocol_version":1,"status":"ok","result":`) ||
		!measureResult(sizer, wire) || !sizer.literal(`}`) {
		t.Fatal("valid response did not measure")
	}
	encoded, err := json.Marshal(successDocument{ProtocolVersion: Version, Status: "ok", Result: wire})
	if err != nil {
		t.Fatal(err)
	}
	if sizer.size != len(encoded) {
		t.Fatalf("measured=%d encoded=%d", sizer.size, len(encoded))
	}
	boundary := &wireSizer{size: MaxResponseBytes - 1, maximum: MaxResponseBytes}
	if !boundary.add(1) || boundary.size != MaxResponseBytes {
		t.Fatalf("maximum rejected: %+v", boundary)
	}
	if boundary.add(1) || boundary.size != MaxResponseBytes+1 {
		t.Fatalf("maximum+1 accepted: %+v", boundary)
	}
}

func TestWriteResponseOneAttemptShortWriteAndErrors(t *testing.T) {
	t.Parallel()
	response := Response{Failure: Failure{Category: CategoryDeclaration, Code: CodeProjectSpecLoadFailed}}
	writerErr := errors.New("writer failed")
	if err := WriteResponse(errorWriter{err: writerErr}, response); !errors.Is(err, writerErr) {
		t.Fatalf("writer error = %v", err)
	}
	if err := WriteResponse(shortWriter{}, response); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer = %v", err)
	}
	if err := WriteResponse(nil, response); err == nil {
		t.Fatal("nil writer accepted")
	}
	writer := &countingWriter{}
	if err := WriteResponse(writer, response); err != nil || writer.calls != 1 {
		t.Fatalf("complete writer = calls %d err %v", writer.calls, err)
	}
}

func TestArbitraryResponseBytesNeverPanic(t *testing.T) {
	t.Parallel()
	for length := 0; length < 512; length++ {
		document := make([]byte, length)
		for index := range document {
			document[index] = byte((index*31 + length*17) & 0xff)
		}
		_, _, _ = ParseResponse(document, true)
	}
}

func validResult(t *testing.T) Result {
	t.Helper()
	spec := permutedSpec()
	digest, err := ProjectSpecDigest(spec)
	if err != nil {
		t.Fatal(err)
	}
	return Result{
		WriterRoot:            "migrations",
		ProjectSpec:           spec,
		ProjectSpecDigest:     digest,
		ProjectSnapshotSHA256: strings.Repeat("b", 64),
		FilesystemCatalog: CatalogSummary{
			SourceCount:   1,
			DocumentBytes: 2,
			Digest:        testDigestA,
		},
		ProgrammaticCatalog: ProgrammaticCatalog{
			SourceCount:   1,
			DocumentBytes: 4,
			Digest:        testDigestB,
			Sources: []Source{{
				SourceID: "programmatic/0001.godj.json",
				Document: []byte{0, 1, 2, 0xff},
			}},
		},
		DefinitionSetDigest: testDigestA,
		Candidates: []Candidate{{
			App:      "content",
			Name:     "0002_article_summary",
			Document: []byte("{}"),
		}},
	}
}

func permutedSpec() codegen.ProjectSpec {
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{
			PackageName: "project",
			ImportPath:  "example.com/site/project",
			Directory:   "project",
		},
		Apps: []codegen.AppSpec{
			{
				Alias: "content",
				Package: codegen.PackageSpec{
					PackageName: "content",
					ImportPath:  "example.com/site/content",
					Directory:   "content",
				},
				Schema: ir.Schema{
					FormatVersion: ir.CurrentFormatVersion,
					AppLabel:      "content",
					Models: []ir.Model{{
						Name:   "article",
						GoName: "Article",
						Fields: []ir.Field{{
							Name:      "title",
							GoName:    "Title",
							Kind:      ir.FieldChar,
							MaxLength: 80,
						}},
					}},
				},
			},
			{
				Alias: "accounts",
				Package: codegen.PackageSpec{
					PackageName: "accounts",
					ImportPath:  "example.com/site/accounts",
					Directory:   "accounts",
				},
				Schema: ir.Schema{
					FormatVersion: ir.CurrentFormatVersion,
					AppLabel:      "accounts",
					Models: []ir.Model{{
						Name:   "author",
						GoName: "Author",
						Fields: []ir.Field{{
							Name:      "name",
							GoName:    "Name",
							Kind:      ir.FieldChar,
							MaxLength: 80,
						}},
					}},
				},
			},
		},
	}
}

func rawSuccess(t *testing.T, result Result) []byte {
	t.Helper()
	normalized, err := NormalizeProjectSpec(result.ProjectSpec)
	if err != nil {
		t.Fatal(err)
	}
	result.ProjectSpec = normalized
	document, err := json.Marshal(successDocument{
		ProtocolVersion: Version,
		Status:          "ok",
		Result:          toWireResult(result),
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func reorderTopLevelSuccess(t *testing.T, document []byte) []byte {
	t.Helper()
	var decoded successDocument
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(decoded.Result)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf(`{"status":"ok","protocol_version":1,"result":%s}`, result))
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type noProgressReader struct{}

func (noProgressReader) Read([]byte) (int, error) { return 0, nil }

type trackingReader struct {
	chunks   [][]byte
	finalErr error
	consumed int
}

func (reader *trackingReader) Read(destination []byte) (int, error) {
	if len(reader.chunks) == 0 {
		if reader.finalErr != nil {
			err := reader.finalErr
			reader.finalErr = nil
			return 0, err
		}
		return 0, io.EOF
	}
	chunk := reader.chunks[0]
	count := copy(destination, chunk)
	reader.consumed += count
	if count == len(chunk) {
		reader.chunks = reader.chunks[1:]
	} else {
		reader.chunks[0] = chunk[count:]
	}
	return count, nil
}

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }

type countingWriter struct {
	calls int
}

func (writer *countingWriter) Write(value []byte) (int, error) {
	writer.calls++
	return len(value), nil
}
