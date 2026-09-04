package migrateprotocol

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const nonemptyDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRequestDocumentIsFreshCanonicalV2LatestExecute(t *testing.T) {
	t.Parallel()
	want := `{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"latest"}}`
	first := RequestDocument()
	if string(first) != want {
		t.Fatalf("request = %q, want %q", first, want)
	}
	first[0] = '!'
	if string(RequestDocument()) != want {
		t.Fatal("RequestDocument retained caller mutation")
	}
	encoded, err := EncodeRequest(Request{Mode: ModeExecute, Target: Target{Kind: TargetLatest}})
	if err != nil || !bytes.Equal(encoded, RequestDocument()) {
		t.Fatalf("default convenience drifted from encoder: %s, %v", encoded, err)
	}
}

func TestEncodeAndReadRequestAllModesAndTargets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		request Request
		want    string
	}{
		{name: "execute latest", request: Request{Mode: ModeExecute, Target: Target{Kind: TargetLatest}}, want: `{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"latest"}}`},
		{name: "plan latest", request: Request{Mode: ModePlan, Target: Target{Kind: TargetLatest}}, want: `{"protocol_version":2,"command":"migrations.migrate","mode":"plan","target":{"kind":"latest"}}`},
		{name: "execute named", request: Request{Mode: ModeExecute, Target: Target{Kind: TargetNamed, App: "blog", Name: "0001_article"}}, want: `{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"named","app":"blog","name":"0001_article"}}`},
		{name: "plan named", request: Request{Mode: ModePlan, Target: Target{Kind: TargetNamed, App: "blog", Name: "0001_article"}}, want: `{"protocol_version":2,"command":"migrations.migrate","mode":"plan","target":{"kind":"named","app":"blog","name":"0001_article"}}`},
		{name: "execute zero", request: Request{Mode: ModeExecute, Target: Target{Kind: TargetZero, App: "blog"}}, want: `{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"zero","app":"blog"}}`},
		{name: "plan zero", request: Request{Mode: ModePlan, Target: Target{Kind: TargetZero, App: "blog"}}, want: `{"protocol_version":2,"command":"migrations.migrate","mode":"plan","target":{"kind":"zero","app":"blog"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := EncodeRequest(test.request)
			if err != nil {
				t.Fatalf("EncodeRequest = %v", err)
			}
			if string(document) != test.want {
				t.Fatalf("request = %q, want %q", document, test.want)
			}
			request, failure, failed, err := ReadRequest(bytes.NewReader(document))
			if err != nil || failed || failure != (Failure{}) || request != test.request {
				t.Fatalf("ReadRequest = %+v, %+v, %v, %v", request, failure, failed, err)
			}
		})
	}

	reordered := `{"target":{"name":"0001_article","kind":"named","app":"blog"},"mode":"plan","command":"migrations.migrate","protocol_version":2}`
	request, failure, failed, err := ReadRequest(strings.NewReader(reordered))
	wantRequest := Request{Mode: ModePlan, Target: Target{Kind: TargetNamed, App: "blog", Name: "0001_article"}}
	if err != nil || failed || failure != (Failure{}) || request != wantRequest {
		t.Fatalf("reordered request = %+v, %+v, %v, %v", request, failure, failed, err)
	}
}

func TestRequestMinimalJSONEscaping(t *testing.T) {
	t.Parallel()
	request := Request{Mode: ModePlan, Target: Target{Kind: TargetNamed, App: "<app>&\"\\\n\x00한\u2028\u2029", Name: "0001_>article"}}
	document, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, escaped := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if bytes.Contains(document, []byte(escaped)) {
			t.Fatalf("request used HTML escaping %q: %s", escaped, document)
		}
	}
	if !bytes.Contains(document, []byte(`\u0000`)) {
		t.Fatalf("request did not minimally escape control byte: %s", document)
	}
	for _, literal := range []string{"\u2028", "\u2029"} {
		if !bytes.Contains(document, []byte(literal)) {
			t.Fatalf("request escaped valid UTF-8 literal %U: %s", []rune(literal)[0], document)
		}
	}
	got, failure, failed, err := ReadRequest(bytes.NewReader(document))
	if err != nil || failed || failure != (Failure{}) || got != request {
		t.Fatalf("ReadRequest = %+v, %+v, %v, %v", got, failure, failed, err)
	}
}

func TestReadRequestRejectsStrictShapeAndSemanticViolations(t *testing.T) {
	t.Parallel()
	valid := string(RequestDocument())
	tests := []struct {
		name string
		wire string
		code string
	}{
		{name: "valid", wire: valid},
		{name: "incompatible", wire: strings.Replace(valid, `"protocol_version":2`, `"protocol_version":1`, 1), code: CodeProtocolIncompatible},
		{name: "missing version", wire: `{"command":"migrations.migrate","mode":"execute","target":{"kind":"latest"}}`, code: CodeInvalidRequest},
		{name: "duplicate", wire: strings.Replace(valid, `"protocol_version":2`, `"protocol_version":2,"protocol_version":2`, 1), code: CodeInvalidRequest},
		{name: "noncanonical number", wire: strings.Replace(valid, `"protocol_version":2`, `"protocol_version":2.0`, 1), code: CodeInvalidRequest},
		{name: "unknown command", wire: strings.Replace(valid, `migrations.migrate`, `migrations.check`, 1), code: CodeInvalidRequest},
		{name: "unknown root member", wire: strings.Replace(valid, `"mode":`, `"dsn":"secret","mode":`, 1), code: CodeInvalidRequest},
		{name: "unknown mode", wire: strings.Replace(valid, `"mode":"execute"`, `"mode":"dry-run"`, 1), code: CodeInvalidRequest},
		{name: "mode wrong type", wire: strings.Replace(valid, `"mode":"execute"`, `"mode":true`, 1), code: CodeInvalidRequest},
		{name: "target wrong type", wire: strings.Replace(valid, `"target":{"kind":"latest"}`, `"target":[]`, 1), code: CodeInvalidRequest},
		{name: "unknown target kind", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"leaf"`, 1), code: CodeInvalidRequest},
		{name: "latest extra app", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"latest","app":"blog"`, 1), code: CodeInvalidRequest},
		{name: "named missing app", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"named","name":"0001_article"`, 1), code: CodeInvalidRequest},
		{name: "named missing name", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"named","app":"blog"`, 1), code: CodeInvalidRequest},
		{name: "named extra member", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"named","app":"blog","name":"0001_article","prefix":true`, 1), code: CodeInvalidRequest},
		{name: "named empty app", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"named","app":"","name":"0001_article"`, 1), code: CodeInvalidRequest},
		{name: "named empty name", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"named","app":"blog","name":""`, 1), code: CodeInvalidRequest},
		{name: "named reserved zero", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"named","app":"blog","name":"zero"`, 1), code: CodeInvalidRequest},
		{name: "zero missing app", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"zero"`, 1), code: CodeInvalidRequest},
		{name: "zero empty app", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"zero","app":""`, 1), code: CodeInvalidRequest},
		{name: "zero extra name", wire: strings.Replace(valid, `"kind":"latest"`, `"kind":"zero","app":"blog","name":"zero"`, 1), code: CodeInvalidRequest},
		{name: "trailing", wire: valid + `{}`, code: CodeInvalidRequest},
		{name: "invalid utf8", wire: valid + "\xff", code: CodeInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, failure, failed, err := ReadRequest(strings.NewReader(test.wire))
			if err != nil {
				t.Fatalf("ReadRequest = %v", err)
			}
			if test.code == "" {
				want := Request{Mode: ModeExecute, Target: Target{Kind: TargetLatest}}
				if failed || failure != (Failure{}) || request != want {
					t.Fatalf("valid request = %+v, %+v, %v", request, failure, failed)
				}
				return
			}
			wantFailure := Failure{Category: CategoryProtocol, Code: test.code}
			if request != (Request{}) || !failed || failure != wantFailure {
				t.Fatalf("failure = %+v, %+v, %v, want %+v", request, failure, failed, wantFailure)
			}
		})
	}
}

func TestWireRejectsUnpairedSurrogatesWithoutCollapsingValidIdentities(t *testing.T) {
	t.Parallel()
	requestPrefix := `{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"named","app":"blog","name":"`
	requestSuffix := `"}}`
	for _, identity := range []string{`\ud800`, `\udfff`, `\ud800\u0041`, `\ud800x`, `\ud800\ud800`} {
		request, failure, failed, err := ReadRequest(strings.NewReader(requestPrefix + identity + requestSuffix))
		if err != nil || !failed || request != (Request{}) || failure != (Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}) {
			t.Fatalf("unpaired request identity %q = %+v, %+v, %v, %v", identity, request, failure, failed, err)
		}

		responseWire := `{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":[{"app":"blog","name":"` + identity + `","direction":"forward"}]}}`
		response, responseFailure, responseFailed := ParseResponse([]byte(responseWire), true)
		if !responseFailed || !reflect.DeepEqual(response, Response{}) || responseFailure != (Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}) {
			t.Fatalf("unpaired response identity %q = %+v, %+v, %v", identity, response, responseFailure, responseFailed)
		}
	}

	tests := []struct {
		name     string
		identity string
		want     string
	}{
		{name: "paired surrogate", identity: `\ud83d\ude00`, want: "😀"},
		{name: "literal replacement rune", identity: "�", want: "�"},
		{name: "escaped replacement rune", identity: `\ufffd`, want: "�"},
		{name: "escaped backslash before surrogate text", identity: `\\ud800`, want: `\ud800`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, failure, failed, err := ReadRequest(strings.NewReader(requestPrefix + test.identity + requestSuffix))
			if err != nil || failed || failure != (Failure{}) || request.Target.Name != test.want {
				t.Fatalf("valid request identity %q = %+v, %+v, %v, %v", test.identity, request, failure, failed, err)
			}

			responseWire := `{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":[{"app":"blog","name":"` + test.identity + `","direction":"forward"}]}}`
			response, responseFailure, responseFailed := ParseResponse([]byte(responseWire), true)
			if responseFailed || responseFailure != (Failure{}) || !response.OK || len(response.Result.Plan) != 1 || response.Result.Plan[0].Name != test.want {
				t.Fatalf("valid response identity %q = %+v, %+v, %v", test.identity, response, responseFailure, responseFailed)
			}
		})
	}
}

func TestRequestIdentityAndResourceBounds(t *testing.T) {
	for _, request := range []Request{
		{},
		{Mode: "preview", Target: Target{Kind: TargetLatest}},
		{Mode: ModeExecute, Target: Target{Kind: TargetLatest, App: "blog"}},
		{Mode: ModeExecute, Target: Target{Kind: TargetNamed, App: "blog"}},
		{Mode: ModeExecute, Target: Target{Kind: TargetNamed, App: "blog", Name: "zero"}},
		{Mode: ModeExecute, Target: Target{Kind: TargetZero, App: "blog", Name: "zero"}},
	} {
		if _, err := EncodeRequest(request); err == nil {
			t.Errorf("EncodeRequest(%+v) unexpectedly succeeded", request)
		}
	}
	if _, err := EncodeRequest(Request{Mode: ModeExecute, Target: Target{Kind: TargetNamed, App: strings.Repeat("a", MaxIdentityBytes+1), Name: "0001"}}); err == nil {
		t.Fatal("oversized app accepted")
	}
	if _, err := EncodeRequest(Request{Mode: ModeExecute, Target: Target{Kind: TargetNamed, App: "blog", Name: strings.Repeat("n", MaxIdentityBytes+1)}}); err == nil {
		t.Fatal("oversized name accepted")
	}
	if _, err := EncodeRequest(Request{Mode: ModeExecute, Target: Target{Kind: TargetNamed, App: "\xff", Name: "0001"}}); err == nil {
		t.Fatal("invalid UTF-8 app accepted")
	}

	worstIdentityBytes := 2 * MaxIdentityBytes
	worstEscapedIdentityBytes := 6 * worstIdentityBytes
	staticNamedRequestBytes := len(`{"protocol_version":2,"command":"migrations.migrate","mode":"execute","target":{"kind":"named","app":,"name":}}`)
	if maximum := worstEscapedIdentityBytes + staticNamedRequestBytes + 4; maximum > MaxRequestBytes {
		t.Fatalf("valid request worst case %d exceeds cap %d", maximum, MaxRequestBytes)
	}

	document, err := readAtMost(strings.NewReader("12345"), 5)
	if err != nil || string(document) != "12345" {
		t.Fatalf("at-limit read = %q, %v", document, err)
	}
	oversized := &trackingReader{chunks: [][]byte{[]byte("12345"), []byte("67"), []byte("tail")}}
	document, err = readAtMost(oversized, 5)
	if err != nil || string(document) != "123456" || oversized.consumed != 11 || len(oversized.chunks) != 0 {
		t.Fatalf("oversized read = %q, %v, consumed %d", document, err, oversized.consumed)
	}
}

func TestReadRequestTransportFailures(t *testing.T) {
	t.Parallel()
	readerErr := errors.New("reader failed")
	if _, _, failed, err := ReadRequest(errorReader{err: readerErr}); failed || !errors.Is(err, readerErr) {
		t.Fatalf("reader error = failed %v err %v", failed, err)
	}
	if _, _, failed, err := ReadRequest(nil); failed || err == nil {
		t.Fatalf("nil reader = failed %v err %v", failed, err)
	}
	tailErr := errors.New("tail failed")
	failingTail := &trackingReader{chunks: [][]byte{[]byte("123456")}, finalErr: tailErr}
	if _, err := readAtMost(failingTail, 5); !errors.Is(err, tailErr) {
		t.Fatalf("post-cap transport error = %v", err)
	}
}

func TestExecuteResponseRoundTripAndTransportPrecedence(t *testing.T) {
	t.Parallel()
	results := []ExecuteResult{
		{DefinitionSetDigest: EmptySetDigest},
		{SourceCount: 1, DefinitionCount: 2, DefinitionSetDigest: nonemptyDigest},
	}
	for _, execute := range results {
		result := Result{Mode: ModeExecute, Execute: execute}
		document, err := EncodeResponse(Response{OK: true, Result: result})
		if err != nil {
			t.Fatalf("EncodeResponse(%+v) = %v", result, err)
		}
		response, failure, failed := ParseResponse(document, true)
		if failed || failure != (Failure{}) || !reflect.DeepEqual(response, Response{OK: true, Result: result}) {
			t.Fatalf("ParseResponse(%s) = %+v, %+v, %v", document, response, failure, failed)
		}
	}

	wantSuccess := `{"protocol_version":2,"status":"ok","result":{"mode":"execute","execute":{"source_count":1,"definition_count":2,"definition_set_digest":"` + nonemptyDigest + `"}}}`
	document, err := EncodeResponse(Response{OK: true, Result: Result{Mode: ModeExecute, Execute: ExecuteResult{SourceCount: 1, DefinitionCount: 2, DefinitionSetDigest: nonemptyDigest}}})
	if err != nil || string(document) != wantSuccess {
		t.Fatalf("execute response = %s, %v, want %s", document, err, wantSuccess)
	}

	linked := Failure{Category: CategoryTransaction, Code: "commit_outcome_unknown", CleanupFailed: true}
	document, err = EncodeResponse(Response{Failure: linked})
	if err != nil {
		t.Fatal(err)
	}
	wantFailure := `{"protocol_version":2,"status":"error","error":{"category":"migration_transaction_error","code":"commit_outcome_unknown","cleanup_failed":true}}`
	if string(document) != wantFailure {
		t.Fatalf("failure response = %s, want %s", document, wantFailure)
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || response.OK || response.Failure != linked {
		t.Fatalf("logical failure = %+v, %+v, %v", response, failure, failed)
	}
	_, failure, failed = ParseResponse(document, false)
	if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeRunnerFailed}) {
		t.Fatalf("transport precedence = %+v, %v", failure, failed)
	}
}

func TestPlanResponseAndPublicPlanRoundTrip(t *testing.T) {
	t.Parallel()
	rows := []PlanRow{
		{App: "accounts", Name: "0001_user", Direction: DirectionForward},
		{App: "blog", Name: "0001_article", Direction: DirectionForward},
	}
	result := Result{Mode: ModePlan, Plan: rows}
	document, err := EncodeResponse(Response{OK: true, Result: result})
	if err != nil {
		t.Fatal(err)
	}
	wantPrivate := `{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":[{"app":"accounts","name":"0001_user","direction":"forward"},{"app":"blog","name":"0001_article","direction":"forward"}]}}`
	if string(document) != wantPrivate {
		t.Fatalf("private plan = %s, want %s", document, wantPrivate)
	}
	if len(document) != encodedPrivatePlanLength(rows) {
		t.Fatalf("private measured length %d != derived %d", len(document), encodedPrivatePlanLength(rows))
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || !reflect.DeepEqual(response, Response{OK: true, Result: result}) {
		t.Fatalf("ParseResponse = %+v, %+v, %v", response, failure, failed)
	}

	public, err := EncodePublicPlan(rows)
	if err != nil {
		t.Fatal(err)
	}
	wantPublic := `{"plan":[{"app":"accounts","name":"0001_user","direction":"forward"},{"app":"blog","name":"0001_article","direction":"forward"}]}` + "\n"
	if string(public) != wantPublic || len(public) != encodedPublicPlanLength(rows) {
		t.Fatalf("public plan = %q (%d), want %q (%d)", public, len(public), wantPublic, encodedPublicPlanLength(rows))
	}

	empty := []PlanRow{}
	document, err = EncodeResponse(Response{OK: true, Result: Result{Mode: ModePlan, Plan: empty}})
	if err != nil || string(document) != `{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":[]}}` {
		t.Fatalf("empty private plan = %s, %v", document, err)
	}
	response, failure, failed = ParseResponse(document, true)
	if failed || failure != (Failure{}) || response.Result.Plan == nil || len(response.Result.Plan) != 0 {
		t.Fatalf("empty plan response = %+v, %+v, %v", response, failure, failed)
	}
	public, err = EncodePublicPlan(nil)
	if err != nil || string(public) != "{\"plan\":[]}\n" {
		t.Fatalf("empty public plan = %q, %v", public, err)
	}
}

func TestPlanMinimalJSONEscapingAndOrderPreservation(t *testing.T) {
	t.Parallel()
	rows := []PlanRow{
		{App: "<app>&\"\\\n\x00한\u2028\u2029", Name: "0002_>article", Direction: DirectionBackward},
		{App: "blog", Name: "0001_article", Direction: DirectionBackward},
	}
	document, err := EncodeResponse(Response{OK: true, Result: Result{Mode: ModePlan, Plan: rows}})
	if err != nil {
		t.Fatal(err)
	}
	for _, escaped := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if bytes.Contains(document, []byte(escaped)) {
			t.Fatalf("plan used HTML escaping %q: %s", escaped, document)
		}
	}
	if !bytes.Contains(document, []byte(`\u0000`)) {
		t.Fatalf("plan did not minimally escape control byte: %s", document)
	}
	for _, literal := range []string{"\u2028", "\u2029"} {
		if !bytes.Contains(document, []byte(literal)) {
			t.Fatalf("plan escaped valid UTF-8 literal %U: %s", []rune(literal)[0], document)
		}
	}
	response, failure, failed := ParseResponse(document, true)
	if failed || failure != (Failure{}) || !reflect.DeepEqual(response.Result.Plan, rows) {
		t.Fatalf("plan order/identity changed: %+v, %+v, %v", response, failure, failed)
	}
}

func TestResponseRejectsStrictShapeVersionAndUnionViolations(t *testing.T) {
	t.Parallel()
	validExecute, err := EncodeResponse(Response{OK: true, Result: Result{Mode: ModeExecute, Execute: ExecuteResult{SourceCount: 1, DefinitionCount: 2, DefinitionSetDigest: nonemptyDigest}}})
	if err != nil {
		t.Fatal(err)
	}
	validPlan, err := EncodeResponse(Response{OK: true, Result: Result{Mode: ModePlan, Plan: []PlanRow{{App: "blog", Name: "0001", Direction: DirectionForward}}}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		wire []byte
		code string
	}{
		{name: "duplicate", wire: bytes.Replace(validExecute, []byte(`"status":"ok"`), []byte(`"status":"ok","status":"ok"`), 1), code: CodeInvalidResponse},
		{name: "unknown root", wire: bytes.Replace(validExecute, []byte(`"result":`), []byte(`"secret":"dsn","result":`), 1), code: CodeInvalidResponse},
		{name: "trailing", wire: append(append([]byte(nil), validExecute...), []byte(`{}`)...), code: CodeInvalidResponse},
		{name: "noncanonical count", wire: bytes.Replace(validExecute, []byte(`"source_count":1`), []byte(`"source_count":1.0`), 1), code: CodeInvalidResponse},
		{name: "negative count", wire: bytes.Replace(validExecute, []byte(`"source_count":1`), []byte(`"source_count":-1`), 1), code: CodeInvalidResponse},
		{name: "zero source nonzero definitions", wire: bytes.Replace(validExecute, []byte(`"source_count":1`), []byte(`"source_count":0`), 1), code: CodeInvalidResponse},
		{name: "empty digest with definitions", wire: bytes.Replace(validExecute, []byte(nonemptyDigest), []byte(EmptySetDigest), 1), code: CodeInvalidResponse},
		{name: "uppercase digest", wire: bytes.Replace(validExecute, []byte(nonemptyDigest), []byte(strings.ToUpper(nonemptyDigest)), 1), code: CodeInvalidResponse},
		{name: "execute with plan arm", wire: bytes.Replace(validExecute, []byte(`"execute":`), []byte(`"plan":`), 1), code: CodeInvalidResponse},
		{name: "execute with both arms", wire: bytes.Replace(validExecute, []byte(`"execute":`), []byte(`"plan":[],"execute":`), 1), code: CodeInvalidResponse},
		{name: "plan with execute arm", wire: bytes.Replace(validPlan, []byte(`"plan":`), []byte(`"execute":`), 1), code: CodeInvalidResponse},
		{name: "unknown mode", wire: bytes.Replace(validPlan, []byte(`"mode":"plan"`), []byte(`"mode":"preview"`), 1), code: CodeInvalidResponse},
		{name: "plan row unknown member", wire: bytes.Replace(validPlan, []byte(`"direction":"forward"`), []byte(`"direction":"forward","sql":"secret"`), 1), code: CodeInvalidResponse},
		{name: "plan row wrong type", wire: bytes.Replace(validPlan, []byte(`"app":"blog"`), []byte(`"app":true`), 1), code: CodeInvalidResponse},
		{name: "plan invalid direction", wire: bytes.Replace(validPlan, []byte(`"direction":"forward"`), []byte(`"direction":"sideways"`), 1), code: CodeInvalidResponse},
		{name: "invalid utf8", wire: append(append([]byte(nil), validExecute...), 0xff), code: CodeInvalidResponse},
		{name: "incompatible", wire: bytes.Replace(validExecute, []byte(`"protocol_version":2`), []byte(`"protocol_version":1`), 1), code: CodeProtocolIncompatible},
		{name: "missing cleanup observation", wire: []byte(`{"protocol_version":2,"status":"error","error":{"category":"migration_backend_error","code":"backend_close_failed"}}`), code: CodeInvalidResponse},
		{name: "close without cleanup failure", wire: []byte(`{"protocol_version":2,"status":"error","error":{"category":"migration_backend_error","code":"backend_close_failed","cleanup_failed":false}}`), code: CodeInvalidResponse},
		{name: "preflight with cleanup failure", wire: []byte(`{"protocol_version":2,"status":"error","error":{"category":"migration_definition_source_error","code":"invalid_definition_document","cleanup_failed":true}}`), code: CodeInvalidResponse},
		{name: "unknown taxonomy", wire: []byte(`{"protocol_version":2,"status":"error","error":{"category":"migration_transaction_error","code":"secret text","cleanup_failed":false}}`), code: CodeInvalidResponse},
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

	invalid := []Response{
		{},
		{OK: true},
		{OK: true, Result: Result{Mode: ModeExecute, Execute: ExecuteResult{DefinitionSetDigest: EmptySetDigest}, Plan: []PlanRow{}}},
		{OK: true, Result: Result{Mode: ModePlan, Plan: nil}},
		{OK: true, Result: Result{Mode: ModePlan, Execute: ExecuteResult{DefinitionSetDigest: EmptySetDigest}, Plan: []PlanRow{}}},
		{OK: true, Result: Result{Mode: ModeExecute, Execute: ExecuteResult{SourceCount: 1, DefinitionCount: 2, DefinitionSetDigest: EmptySetDigest}}},
		{OK: true, Result: Result{Mode: ModeExecute, Execute: ExecuteResult{DefinitionSetDigest: EmptySetDigest}}, Failure: Failure{Category: CategoryTransaction, Code: "commit_failed"}},
		{Failure: Failure{Category: CategoryBackend, Code: CodeBackendCloseFailed}},
		{Failure: Failure{Category: CategorySource, Code: "invalid_definition_document", CleanupFailed: true}},
		{Result: Result{Mode: ModePlan, Plan: []PlanRow{}}, Failure: Failure{Category: CategorySource, Code: "invalid_definition_document"}},
	}
	for _, response := range invalid {
		if _, err := EncodeResponse(response); err == nil {
			t.Errorf("EncodeResponse(%+v) unexpectedly succeeded", response)
		}
	}
}

func TestPlanSemanticAndAggregateBounds(t *testing.T) {
	valid := []PlanRow{{App: "blog", Name: "0001", Direction: DirectionForward}}
	tests := []struct {
		name string
		rows []PlanRow
	}{
		{name: "empty app", rows: []PlanRow{{App: "", Name: "0001", Direction: DirectionForward}}},
		{name: "empty name", rows: []PlanRow{{App: "blog", Name: "", Direction: DirectionForward}}},
		{name: "invalid utf8", rows: []PlanRow{{App: "blog", Name: "\xff", Direction: DirectionForward}}},
		{name: "oversized identity", rows: []PlanRow{{App: "blog", Name: strings.Repeat("n", MaxIdentityBytes+1), Direction: DirectionForward}}},
		{name: "invalid direction", rows: []PlanRow{{App: "blog", Name: "0001", Direction: "sideways"}}},
		{name: "mixed direction", rows: []PlanRow{{App: "blog", Name: "0002", Direction: DirectionBackward}, {App: "blog", Name: "0001", Direction: DirectionForward}}},
		{name: "duplicate", rows: append(append([]PlanRow(nil), valid...), valid...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := EncodeResponse(Response{OK: true, Result: Result{Mode: ModePlan, Plan: test.rows}}); err == nil {
				t.Fatal("invalid private plan accepted")
			}
			if _, err := EncodePublicPlan(test.rows); err == nil {
				t.Fatal("invalid public plan accepted")
			}
		})
	}

	maximumRows := make([]PlanRow, MaxPlanRows)
	for index := range maximumRows {
		maximumRows[index] = PlanRow{App: "app", Name: strconv.Itoa(index), Direction: DirectionForward}
	}
	if !validPlanRows(maximumRows) {
		t.Fatal("maximum row count rejected")
	}
	tooManyRows := append(append([]PlanRow(nil), maximumRows...), PlanRow{App: "app", Name: "overflow", Direction: DirectionForward})
	if validPlanRows(tooManyRows) {
		t.Fatal("row count above maximum accepted")
	}

	aggregateRows := make([]PlanRow, 16)
	for index := range aggregateRows {
		name := strings.Repeat("n", MaxIdentityBytes-2) + strconv.FormatInt(int64(index), 16)
		if len(name) < MaxIdentityBytes-1 {
			name += strings.Repeat("x", MaxIdentityBytes-1-len(name))
		}
		aggregateRows[index] = PlanRow{App: "a", Name: name, Direction: DirectionForward}
	}
	if !validPlanRows(aggregateRows) {
		t.Fatal("exact aggregate boundary rejected")
	}
	aggregateRows = append(aggregateRows, PlanRow{App: "a", Name: "overflow", Direction: DirectionForward})
	if validPlanRows(aggregateRows) {
		t.Fatal("aggregate above maximum accepted")
	}
}

func TestPlanResponseDecodeBoundsAndUniqueness(t *testing.T) {
	t.Parallel()
	row := `{"app":"blog","name":"0001","direction":"forward"}`
	tests := []string{
		`{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":[` + row + `,` + row + `]}}`,
		`{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":[{"app":"blog","name":"0002","direction":"backward"},{"app":"blog","name":"0001","direction":"forward"}]}}`,
	}
	for _, document := range tests {
		_, failure, failed := ParseResponse([]byte(document), true)
		if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}) {
			t.Fatalf("invalid plan response = %+v, %v", failure, failed)
		}
	}

	rows := strings.TrimSuffix(strings.Repeat(`{},`, MaxPlanRows+1), ",")
	document := `{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":[` + rows + `]}}`
	_, failure, failed := ParseResponse([]byte(document), true)
	if !failed || failure != (Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}) {
		t.Fatalf("oversized plan array = %+v, %v", failure, failed)
	}
}

func TestDerivedWireBoundsCoverWorstCaseMinimalEscaping(t *testing.T) {
	t.Parallel()
	if maxPrivatePlanDocumentBytes != 100_753_478 {
		t.Fatalf("private derived maximum = %d", maxPrivatePlanDocumentBytes)
	}
	if maxPublicPlanDocumentBytes != 100_753_419 {
		t.Fatalf("public derived maximum = %d", maxPublicPlanDocumentBytes)
	}
	if maxPrivatePlanDocumentBytes > MaxResponseBytes || maxPublicPlanDocumentBytes > MaxResponseBytes {
		t.Fatalf("derived maxima %d/%d exceed response cap %d", maxPrivatePlanDocumentBytes, maxPublicPlanDocumentBytes, MaxResponseBytes)
	}
	if MaxResponseBytes != 105_906_176 {
		t.Fatalf("response cap = %d", MaxResponseBytes)
	}
	framingRows := make([]PlanRow, MaxPlanRows)
	for index := range framingRows {
		framingRows[index].Direction = DirectionBackward
	}
	privateFraming := encodedPrivatePlanLength(framingRows)
	publicFraming := encodedPublicPlanLength(framingRows)
	if privateFraming > maxPrivatePlanFramingBytes || publicFraming > maxPublicPlanFramingBytes {
		t.Fatalf("derived framing %d/%d exceeds allowance %d/%d", privateFraming, publicFraming, maxPrivatePlanFramingBytes, maxPublicPlanFramingBytes)
	}
	worstByte := strings.Repeat("\x00", 128)
	if got, want := encodedJSONStringLength(worstByte), 2+6*len(worstByte); got != want {
		t.Fatalf("worst escape length = %d, want %d", got, want)
	}
	rows := []PlanRow{{App: worstByte, Name: "<>&", Direction: DirectionBackward}}
	private, err := EncodeResponse(Response{OK: true, Result: Result{Mode: ModePlan, Plan: rows}})
	if err != nil {
		t.Fatal(err)
	}
	if len(private) != encodedPrivatePlanLength(rows) {
		t.Fatalf("private encoded length = %d, derived %d", len(private), encodedPrivatePlanLength(rows))
	}
	public, err := EncodePublicPlan(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(public) != encodedPublicPlanLength(rows) {
		t.Fatalf("public encoded length = %d, derived %d", len(public), encodedPublicPlanLength(rows))
	}
	if _, err := decodeObject([]byte(`{"a":1}`), len(`{"a":1}`)-1); err == nil {
		t.Fatal("decoder accepted bytes above supplied cap")
	}
}

func TestClosedTaxonomyLinkedBoundaryAndExitCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		failure Failure
		exit    int
		linked  bool
	}{
		{failure: Failure{Category: CategoryCommand, Code: CodeInvalidArguments}, exit: 2},
		{failure: Failure{Category: CategorySelection, Code: CodeProjectNotFound}, exit: 2},
		{failure: Failure{Category: CategoryBuild, Code: CodeProjectBuildFailed}, exit: 3},
		{failure: Failure{Category: CategoryProtocol, Code: CodeInvalidRequest}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryProtocol, Code: CodeInvalidResponse}, exit: 3},
		{failure: Failure{Category: CategoryProcess, Code: CodeProjectInterrupted}, exit: 130},
		{failure: Failure{Category: CategoryDiscovery, Code: CodeUnsafeSourceEntry}, exit: 1, linked: true},
		{failure: Failure{Category: CategorySource, Code: "invalid_definition_document"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryGraph, Code: "dependency_cycle"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryState, Code: "invalid_state"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryCapability, Code: "revision_fence_unsupported"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryExecution, Code: "operation_failed"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryRecorder, Code: "read_failed"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryRecorder, Code: "record_failed"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryTransaction, Code: CodeRollbackFailed}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryPlan, Code: "invalid_execution_plan"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryHistory, Code: "inconsistent_applied_history"}, exit: 1, linked: true},
		{failure: Failure{Category: CategoryConflict, Code: "stale_history_revision"}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryBackend, Code: CodeBackendOpenFailed}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryBackend, Code: CodeBackendOpenFailed, CleanupFailed: true}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryBackend, Code: CodeBackendCloseFailed, CleanupFailed: true}, exit: 3, linked: true},
		{failure: Failure{Category: CategoryInternal, Code: CodeProjectInternalError}, exit: 3, linked: true},
	}
	for _, test := range tests {
		exit, ok := ExitCode(test.failure)
		if !ok || exit != test.exit {
			t.Errorf("ExitCode(%+v) = %d, %v, want %d", test.failure, exit, ok, test.exit)
		}
		if got := IsLinkedFailure(test.failure); got != test.linked {
			t.Errorf("IsLinkedFailure(%+v) = %v, want %v", test.failure, got, test.linked)
		}
	}
	for _, invalid := range []Failure{
		{Category: CategorySource, Code: "invented"},
		{Category: CategoryBackend, Code: CodeBackendCloseFailed},
		{Category: CategoryBackend, Code: CodeInvalidBackend, CleanupFailed: true},
		{Category: CategoryProtocol, Code: CodeInvalidRequest, CleanupFailed: true},
	} {
		if IsLinkedFailure(invalid) {
			t.Errorf("invalid linked failure accepted: %+v", invalid)
		}
	}
}

func TestWriteResponseRejectsNilShortAndFailedWriters(t *testing.T) {
	t.Parallel()
	response := Response{OK: true, Result: Result{Mode: ModeExecute, Execute: ExecuteResult{DefinitionSetDigest: EmptySetDigest}}}
	if err := WriteResponse(nil, response); err == nil {
		t.Fatal("nil writer accepted")
	}
	if err := WriteResponse(shortWriter{}, response); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer error = %v", err)
	}
	want := errors.New("write failed")
	if err := WriteResponse(errorWriter{err: want}, response); !errors.Is(err, want) {
		t.Fatalf("failed writer error = %v", err)
	}
}

func TestArbitraryProtocolBytesDoNotPanic(t *testing.T) {
	t.Parallel()
	for length := 0; length <= 512; length++ {
		document := make([]byte, length)
		for index := range document {
			document[index] = byte((length*31 + index*17) % 256)
		}
		_, _, _ = ParseResponse(document, true)
		_, _, _, _ = ReadRequest(bytes.NewReader(document))
	}
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type trackingReader struct {
	chunks   [][]byte
	finalErr error
	consumed int
}

func (reader *trackingReader) Read(buffer []byte) (int, error) {
	if len(reader.chunks) == 0 {
		if reader.finalErr != nil {
			err := reader.finalErr
			reader.finalErr = nil
			return 0, err
		}
		return 0, io.EOF
	}
	chunk := reader.chunks[0]
	read := copy(buffer, chunk)
	reader.consumed += read
	if read == len(chunk) {
		reader.chunks = reader.chunks[1:]
	} else {
		reader.chunks[0] = chunk[read:]
	}
	return read, nil
}

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }
