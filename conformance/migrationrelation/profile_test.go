package migrationrelation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestProfileDispatchAcceptsOnlyExactTuplesAndUsesRawProductGuards(t *testing.T) {
	t.Parallel()

	t.Run("exact tuple and coordinate precedence", func(t *testing.T) {
		tests := []struct {
			name       string
			value      profileCompatibility
			want       profileDecoder
			wantCode   string
			wantReason string
		}{
			{name: "legacy exact", value: profileLegacy, want: profileDecoderLegacy},
			{name: "relation exact", value: profileRelationTuple, want: profileDecoderRelation},
			{
				name: "hybrid loader only",
				value: profileCompatibility{
					DefinitionFormat: 1, LoaderABI: 2, OperationCodec: 1, SchemaIR: 2,
				},
				wantCode: "hybrid_profile_incompatible", wantReason: "loader_abi",
			},
			{
				name: "hybrid codec and schema IR",
				value: profileCompatibility{
					DefinitionFormat: 1, LoaderABI: 1, OperationCodec: 2, SchemaIR: 3,
				},
				wantCode: "hybrid_profile_incompatible", wantReason: "operation_codec",
			},
			{
				name: "unknown definition format before unknown schema IR",
				value: profileCompatibility{
					DefinitionFormat: 9, LoaderABI: 2, OperationCodec: 2, SchemaIR: 9,
				},
				wantCode: "definition_format_incompatible", wantReason: "definition_format",
			},
			{
				name: "unknown operation codec before hybrid schema IR",
				value: profileCompatibility{
					DefinitionFormat: 1, LoaderABI: 1, OperationCodec: 9, SchemaIR: 3,
				},
				wantCode: "operation_codec_incompatible", wantReason: "operation_codec",
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				got, failure := profileDispatch(test.value)
				if test.wantCode == "" {
					if failure != nil || got != test.want {
						t.Fatalf("profileDispatch(%+v) = %q, %v; want %q, nil", test.value, got, failure, test.want)
					}
					return
				}
				if failure == nil || failure.Code != test.wantCode || failure.Reason != test.wantReason ||
					failure.Pointer != "/compatibility/"+test.wantReason || failure.Stage != "compatibility" {
					t.Fatalf("profileDispatch(%+v) failure = %+v", test.value, failure)
				}
			})
		}

		_, report, err := profileLoad(
			profileSource{SourceID: "a-schema", Producer: profileTestProducer(), Profile: profileCompatibility{DefinitionFormat: 1, LoaderABI: 2, OperationCodec: 2, SchemaIR: 9}},
			profileSource{SourceID: "z-loader", Producer: profileTestProducer(), Profile: profileCompatibility{DefinitionFormat: 1, LoaderABI: 2, OperationCodec: 1, SchemaIR: 2}},
			profileSource{SourceID: "m-definition", Producer: profileTestProducer(), Profile: profileCompatibility{DefinitionFormat: 9, LoaderABI: 2, OperationCodec: 2, SchemaIR: 3}},
		)
		failure := profileRequireCandidateFailure(t, err)
		if failure.Code != "definition_format_incompatible" || failure.SourceID != "m-definition" {
			t.Fatalf("batch precedence error = %#v, want definition format at m-definition", err)
		}
		profileRequireNoPublication(t, profileSet{}, report)
	})

	relationRaw := profileRawSources(t, profileRelationFixture())[0]
	tests := []struct {
		name           string
		sources        func() []definition.Source
		wantStage      string
		wantReason     string
		wantLimit      string
		wantPointer    string
		compareProduct bool
	}{
		{
			name: "duplicate JSON key precedes relation dispatch",
			sources: func() []definition.Source {
				source := profileCloneRawSource(relationRaw)
				source.Document = bytes.Replace(source.Document, []byte(`"loader_abi":2`), []byte(`"loader_abi":2,"loader_abi":2`), 1)
				return []definition.Source{source}
			},
			wantStage: "document", wantReason: "duplicate_key", wantPointer: "/compatibility/loader_abi", compareProduct: true,
		},
		{
			name: "trailing framing precedes relation dispatch",
			sources: func() []definition.Source {
				source := profileCloneRawSource(relationRaw)
				source.Document = append(source.Document, []byte(` {}`)...)
				return []definition.Source{source}
			},
			wantStage: "document", wantReason: "trailing_value", compareProduct: true,
		},
		{
			name: "JSON depth precedes relation dispatch",
			sources: func() []definition.Source {
				source := profileCloneRawSource(relationRaw)
				source.Document = profileAddRawRootMember(source.Document, "noise", strings.Repeat("[", definition.MaxJSONDepth+1)+"null"+strings.Repeat("]", definition.MaxJSONDepth+1))
				return []definition.Source{source}
			},
			wantStage: "document", wantReason: "resource_limit_exceeded", wantLimit: "json_depth",
			wantPointer: "/noise" + strings.Repeat("/0", definition.MaxJSONDepth-1), compareProduct: true,
		},
		{
			name: "document bytes precede parsing",
			sources: func() []definition.Source {
				return []definition.Source{{SourceID: "too-large", Document: bytes.Repeat([]byte(" "), definition.MaxDocumentBytes+1)}}
			},
			wantStage: "document", wantReason: "resource_limit_exceeded", wantLimit: "document_bytes",
		},
		{
			name: "batch bytes precede parsing",
			sources: func() []definition.Source {
				sources := make([]definition.Source, definition.MaxBatchBytes/definition.MaxDocumentBytes+1)
				for index := range sources {
					sources[index] = definition.Source{
						SourceID: fmt.Sprintf("batch-%02d", index),
						Document: bytes.Repeat([]byte(" "), definition.MaxDocumentBytes),
					}
				}
				return sources
			},
			wantStage: "document", wantReason: "resource_limit_exceeded", wantLimit: "batch_bytes",
		},
		{
			name: "document JSON values precede unknown field",
			sources: func() []definition.Source {
				source := profileCloneRawSource(relationRaw)
				source.Document = profileAddRawRootMember(source.Document, "noise", profileNullArray(definition.MaxDocumentJSONValues+1))
				return []definition.Source{source}
			},
			wantStage: "document", wantReason: "resource_limit_exceeded", wantLimit: "document_json_values", compareProduct: true,
		},
		{
			name: "batch JSON values precede unknown fields",
			sources: func() []definition.Source {
				const valuesPerDocument = 55_000
				sources := make([]definition.Source, 5)
				for index := range sources {
					sources[index] = profileCloneRawSource(relationRaw)
					sources[index].SourceID = fmt.Sprintf("values-%d", index)
					sources[index].Document = profileAddRawRootMember(sources[index].Document, "noise", profileNullArray(valuesPerDocument))
				}
				return sources
			},
			wantStage: "document", wantReason: "resource_limit_exceeded", wantLimit: "json_values", compareProduct: true,
		},
		{
			name: "source count is bounded before snapshots",
			sources: func() []definition.Source {
				return make([]definition.Source, definition.MaxSources+1)
			},
			wantStage: "source", wantReason: "resource_limit_exceeded", wantLimit: "source_count",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			sources := test.sources()
			set, report, err := profileLoadRaw(sources...)
			wantParserInvocations := len(sources)
			if test.wantLimit == "source_count" || test.wantLimit == "document_bytes" || test.wantLimit == "batch_bytes" {
				wantParserInvocations = 0
			}
			if report.ParserInvocations != wantParserInvocations {
				t.Fatalf("parser invocations = %d, want %d", report.ParserInvocations, wantParserInvocations)
			}
			failure := profileRequireCandidateFailure(t, err)
			if failure.Stage != test.wantStage || failure.Reason != test.wantReason || failure.Limit != test.wantLimit ||
				failure.Pointer != test.wantPointer {
				t.Fatalf("raw failure = %+v, want stage=%s reason=%s limit=%s pointer=%q", failure, test.wantStage, test.wantReason, test.wantLimit, test.wantPointer)
			}
			profileRequireFailureContextMatchesError(t, report, failure)
			if test.compareProduct {
				_, productReport, productErr := definition.Load(sources...)
				mapped := profileRequireCandidateFailure(t, profileMapProductLoadError(productErr, productReport))
				if !reflect.DeepEqual(failure, mapped) {
					t.Fatalf("raw scanner diverged from product context:\ncandidate=%+v\nproduct=%+v", failure, mapped)
				}
			}
			profileRequireNoPublication(t, set, report)
		})
	}

	t.Run("mixed source preflight remains the exact product boundary", func(t *testing.T) {
		relation := profileRawSources(t, profileRelationFixture())[0]
		legacy, _ := profileLegacyFixture()
		legacyRaw := profileRawSources(t, legacy)[0]
		invalidUTF8 := profileCloneRawSource(relation)
		invalidUTF8.SourceID = string([]byte{0xff})
		empty := profileCloneRawSource(relation)
		empty.SourceID = ""
		duplicateRelation := profileCloneRawSource(relation)
		duplicateRelation.SourceID = "same-source"
		duplicateLegacy := profileCloneRawSource(legacyRaw)
		duplicateLegacy.SourceID = "same-source"
		for _, test := range []struct {
			name    string
			sources []definition.Source
		}{
			{name: "empty source id", sources: []definition.Source{empty}},
			{name: "invalid UTF-8 source id", sources: []definition.Source{invalidUTF8}},
			{name: "duplicate source id", sources: []definition.Source{duplicateRelation, duplicateLegacy}},
		} {
			t.Run(test.name, func(t *testing.T) {
				_, productReport, productErr := definition.Load(test.sources...)
				set, candidateReport, candidateErr := profileLoadRaw(test.sources...)
				candidateFailure := profileRequireCandidateFailure(t, candidateErr)
				if candidateReport.ParserInvocations != 0 {
					t.Fatalf("preflight parser invocations = %d, want 0", candidateReport.ParserInvocations)
				}
				mappedFailure := profileRequireCandidateFailure(t, profileMapProductLoadError(productErr, productReport))
				if !reflect.DeepEqual(candidateFailure, mappedFailure) || !reflect.DeepEqual(candidateReport, profileReportFromProduct(productReport)) {
					t.Fatalf("mixed preflight diverged: candidate=%+v/%+v product=%+v/%+v", candidateFailure, candidateReport, mappedFailure, productReport)
				}
				profileRequireNoPublication(t, set, candidateReport)
			})
		}
	})

	t.Run("mixed raw failure precedence is permutation invariant", func(t *testing.T) {
		raw := profileRawSources(t, profileRelationFixture())[0]
		duplicate := profileCloneRawSource(raw)
		duplicate.SourceID = "a-duplicate"
		duplicate.Document = bytes.Replace(duplicate.Document, []byte(`"loader_abi":2`), []byte(`"loader_abi":2,"loader_abi":2`), 1)
		depth := profileCloneRawSource(raw)
		depth.SourceID = "z-depth"
		depth.Document = profileAddRawRootMember(depth.Document, "noise", strings.Repeat("[", definition.MaxJSONDepth+1)+"null"+strings.Repeat("]", definition.MaxJSONDepth+1))
		var first *profileCandidateError
		for _, sources := range [][]definition.Source{{duplicate, depth}, {depth, duplicate}} {
			_, productReport, productErr := definition.Load(sources...)
			set, report, err := profileLoadRaw(sources...)
			failure := profileRequireCandidateFailure(t, err)
			mapped := profileRequireCandidateFailure(t, profileMapProductLoadError(productErr, productReport))
			if failure.Limit != "json_depth" || failure.SourceID != "z-depth" || !reflect.DeepEqual(failure, mapped) {
				t.Fatalf("mixed raw winner = %+v, product = %+v", failure, mapped)
			}
			profileRequireFailureContextMatchesError(t, report, failure)
			if first != nil && !reflect.DeepEqual(failure, first) {
				t.Fatalf("source permutation changed raw winner: first=%+v second=%+v", first, failure)
			}
			copy := *failure
			copy.GraphSources = append([]definition.GraphSource(nil), failure.GraphSources...)
			first = &copy
			profileRequireNoPublication(t, set, report)
		}
	})

	t.Run("mixed raw scanner rejects a lone JSON surrogate", func(t *testing.T) {
		source := profileRawSources(t, profileRelationFixture())[0]
		source.Document = bytes.Replace(source.Document, []byte(`"name":"test-only-relation-candidate"`), []byte(`"name":"\ud800"`), 1)
		_, productReport, productErr := definition.Load(source)
		set, report, err := profileLoadRaw(source)
		failure := profileRequireCandidateFailure(t, err)
		mapped := profileRequireCandidateFailure(t, profileMapProductLoadError(productErr, productReport))
		if failure.Stage != "document" || failure.Reason != "lone_surrogate" || failure.Pointer != "/producer/name" ||
			!reflect.DeepEqual(failure, mapped) {
			t.Fatalf("lone surrogate failure = %+v, product = %+v", failure, mapped)
		}
		profileRequireFailureContextMatchesError(t, report, failure)
		profileRequireNoPublication(t, set, report)
	})

	t.Run("compound raw diagnostics retain exact pointers and permutation winner", func(t *testing.T) {
		samePointer := profileRawSources(t, profileRelationFixture())[0]
		samePointer.Document = bytes.Replace(
			samePointer.Document,
			[]byte(`"name":"test-only-relation-candidate"`),
			[]byte(`"name":"test-only-relation-candidate","name":"\ud800"`),
			1,
		)
		_, productReport, productErr := definition.Load(samePointer)
		set, report, err := profileLoadRaw(samePointer)
		failure := profileRequireCandidateFailure(t, err)
		mapped := profileRequireCandidateFailure(t, profileMapProductLoadError(productErr, productReport))
		if failure.Reason != "duplicate_key" || failure.Pointer != "/producer/name" || !reflect.DeepEqual(failure, mapped) {
			t.Fatalf("same-pointer compound failure = %+v, product = %+v", failure, mapped)
		}
		profileRequireFailureContextMatchesError(t, report, failure)
		profileRequireNoPublication(t, set, report)

		lone := profileRawSources(t, profileRelationFixture())[0]
		lone.SourceID = "a-lone"
		lone.Document = bytes.Replace(lone.Document, []byte(`"name":"test-only-relation-candidate"`), []byte(`"name":"\ud800"`), 1)
		duplicate := profileRawSources(t, profileRelationFixture())[0]
		duplicate.SourceID = "z-duplicate"
		duplicate.Document = bytes.Replace(duplicate.Document, []byte(`"loader_abi":2`), []byte(`"loader_abi":2,"loader_abi":2`), 1)
		var first *profileCandidateError
		for _, sources := range [][]definition.Source{{duplicate, lone}, {lone, duplicate}} {
			_, productReport, productErr := definition.Load(sources...)
			set, report, err := profileLoadRaw(sources...)
			failure := profileRequireCandidateFailure(t, err)
			mapped := profileRequireCandidateFailure(t, profileMapProductLoadError(productErr, productReport))
			if failure.SourceID != "a-lone" || failure.Reason != "lone_surrogate" || failure.Pointer != "/producer/name" ||
				!reflect.DeepEqual(failure, mapped) {
				t.Fatalf("permuted compound failure = %+v, product = %+v", failure, mapped)
			}
			if first != nil && !reflect.DeepEqual(failure, first) {
				t.Fatalf("compound source permutation changed winner: first=%+v second=%+v", first, failure)
			}
			copy := *failure
			first = &copy
			profileRequireFailureContextMatchesError(t, report, failure)
			profileRequireNoPublication(t, set, report)
		}
	})

	t.Run("raw and envelope failures share the exact product winner", func(t *testing.T) {
		source := profileRawSources(t, profileRelationFixture())[0]
		source.Document = bytes.Replace(source.Document, []byte(`"loader_abi":2`), []byte(`"loader_abi":2,"loader_abi":2`), 1)
		source.Document = profileAddRawRootMember(source.Document, "a", "null")
		_, productReport, productErr := definition.Load(source)
		set, report, err := profileLoadRaw(source)
		failure := profileRequireCandidateFailure(t, err)
		mapped := profileRequireCandidateFailure(t, profileMapProductLoadError(productErr, productReport))
		if !reflect.DeepEqual(failure, mapped) || failure.Stage != "document" || failure.Reason != "unknown_field" || failure.Pointer != "/a" {
			t.Fatalf("compound raw/envelope failure = %+v, product = %+v", failure, mapped)
		}
		profileRequireFailureContextMatchesError(t, report, failure)
		profileRequireNoPublication(t, set, report)
	})

	t.Run("relation nested unknown field cannot bypass raw decoder", func(t *testing.T) {
		source := profileCloneRawSource(relationRaw)
		source.Document = bytes.Replace(source.Document, []byte(`"cardinality":"many_to_one"`), []byte(`"cardinality":"many_to_one","mystery":true`), 1)
		set, report, err := profileLoadRaw(source)
		failure := profileRequireCandidateFailure(t, err)
		if failure.Stage != "semantic" || failure.Code != string(definition.CodeInvalidIR) || failure.Reason != "invalid_ir" ||
			failure.Pointer != "/migration/operations/0/field/relation/mystery" || failure.OperationIndex != 0 {
			t.Fatalf("strict relation failure = %+v", failure)
		}
		profileRequireFailureContextMatchesError(t, report, failure)
		profileRequireNoPublication(t, set, report)
	})
}

func TestProfileLegacyOnlyDelegatesToExistingLoadDigestAndDefinitionsExactly(t *testing.T) {
	t.Parallel()

	root, tail := profileLegacyFixture()
	raw := profileRawSources(t, tail, root)
	original := profileCloneRawSources(raw)
	product, productReport, err := definition.Load(raw...)
	if err != nil {
		t.Fatalf("definition.Load legacy baseline: %v", err)
	}
	candidate, report, err := profileLoadRaw(raw...)
	if err != nil {
		t.Fatalf("profileLoadRaw legacy: %v", err)
	}
	const wantDigest = "sha256:5a73e03d3448f3f19f7646eed67f4e312610f4389f2e3e537c379e725f0b106d"
	if candidate.profileDigest() != product.Digest() || candidate.profileDigest() != wantDigest {
		t.Fatalf("legacy digest = candidate %q product %q want %q", candidate.profileDigest(), product.Digest(), wantDigest)
	}
	if got, want := len(raw[0].Document)+len(raw[1].Document), 1379; got != want {
		t.Fatalf("legacy raw document bytes = %d, want %d", got, want)
	}
	if !reflect.DeepEqual(candidate.profileLegacyDefinitions(), product.Definitions()) {
		t.Fatalf("legacy candidate definitions = %#v\nproduct definitions = %#v", candidate.profileLegacyDefinitions(), product.Definitions())
	}
	if report.ParserInvocations != len(raw) {
		t.Fatalf("legacy parser invocations = %d, want exactly one/source (%d)", report.ParserInvocations, len(raw))
	}
	canonicalSum := sha256.Sum256(candidate.profileCanonicalBytes())
	if got := "sha256:" + hex.EncodeToString(canonicalSum[:]); got != product.Digest() {
		t.Fatalf("legacy canonical bytes hash = %q, product digest %q", got, product.Digest())
	}
	if report != profileReportFromProduct(productReport) || report != (profileLoadReport{
		DocumentsReceived: 2, ProfilesAccepted: 2, OperationsDecoded: 3, PlannerConstruction: 1,
		DefinitionsPublished: 2, SetsPublished: 1, ParserInvocations: 2,
	}) {
		t.Fatalf("legacy report = candidate %+v product %+v", report, productReport)
	}
	if !reflect.DeepEqual(raw, original) {
		t.Fatal("legacy raw documents were rewritten")
	}

	t.Run("canonical string edge cases stay on product v1 path", func(t *testing.T) {
		edge := profileCloneSource(root)
		edge.SourceID = "legacy-edge"
		edge.Definition.Name = "0003_edge"
		value := "<>&\u2028\u2029"
		edge.Definition.Operations[0].Model.Fields[1].Default = &profileDefault{Kind: string(ir.ScalarString), String: &value}
		raw := profileRawSources(t, edge)
		original := profileCloneRawSources(raw)
		product, _, err := definition.Load(raw...)
		if err != nil {
			t.Fatalf("definition.Load edge default: %v", err)
		}
		candidate, _, err := profileLoadRaw(raw...)
		if err != nil {
			t.Fatalf("profileLoadRaw edge default: %v", err)
		}
		const wantDigest = "sha256:9fccb9db3a9987db48e4b4a491eb545228a843faeb288e1f1a585871535356d1"
		if candidate.profileDigest() != product.Digest() || candidate.profileDigest() != wantDigest {
			t.Fatalf("edge digest = candidate %q product %q want %q", candidate.profileDigest(), product.Digest(), wantDigest)
		}
		if !reflect.DeepEqual(candidate.profileLegacyDefinitions(), product.Definitions()) || !reflect.DeepEqual(raw, original) {
			t.Fatal("edge legacy source was reinterpreted or rewritten")
		}
	})

	t.Run("legacy errors and limits stay on the exact product path", func(t *testing.T) {
		unknown := profileRawSources(t, root)
		unknown[0].Document = profileAddRawRootMember(unknown[0].Document, "unknown", "null")
		tooMany := make([]definition.Source, definition.MaxSources+1)
		for _, test := range []struct {
			name    string
			sources []definition.Source
		}{
			{name: "semantic error", sources: unknown},
			{name: "source limit", sources: tooMany},
		} {
			t.Run(test.name, func(t *testing.T) {
				_, productReport, productErr := definition.Load(test.sources...)
				if productErr == nil {
					t.Fatal("definition.Load unexpectedly succeeded")
				}
				set, candidateReport, candidateErr := profileLoadRaw(test.sources...)
				candidateFailure := profileRequireCandidateFailure(t, candidateErr)
				mappedFailure := profileRequireCandidateFailure(t, profileMapProductError(productErr))
				if !reflect.DeepEqual(candidateFailure, mappedFailure) || !reflect.DeepEqual(candidateReport, profileReportFromProduct(productReport)) {
					t.Fatalf("legacy failure diverged: candidate=%+v/%+v product=%+v/%+v", candidateFailure, candidateReport, mappedFailure, productReport)
				}
				profileRequireNoPublication(t, set, candidateReport)
			})
		}
	})

	t.Run("legacy semantic and graph diagnostics retain every product field", func(t *testing.T) {
		invalidOperation := profileCloneSource(tail)
		invalidOperation.Definition.Operations[0].AppLabel = "other"
		for _, test := range []struct {
			name      string
			sources   []definition.Source
			wantStage string
			wantApp   string
			wantName  string
			wantIndex int
			wantGraph bool
		}{
			{
				name: "semantic context", sources: profileRawSources(t, invalidOperation),
				wantStage: "semantic", wantApp: "alpha", wantName: "0002_fields", wantIndex: 0,
			},
			{
				name: "graph context", sources: profileRawSources(t, tail),
				wantStage: "graph", wantApp: "alpha", wantName: "0002_fields", wantIndex: -1, wantGraph: true,
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				_, productReport, productErr := definition.Load(test.sources...)
				set, candidateReport, candidateErr := profileLoadRaw(test.sources...)
				failure := profileRequireCandidateFailure(t, candidateErr)
				mapped := profileRequireCandidateFailure(t, profileMapProductLoadError(productErr, productReport))
				if !reflect.DeepEqual(failure, mapped) || !reflect.DeepEqual(candidateReport, profileReportFromProduct(productReport)) {
					t.Fatalf("legacy diagnostic diverged: candidate=%+v/%+v product=%+v/%+v", failure, candidateReport, mapped, productReport)
				}
				if failure.Stage != test.wantStage || failure.App != test.wantApp || failure.Name != test.wantName || failure.OperationIndex != test.wantIndex ||
					(test.wantGraph && len(failure.GraphSources) == 0) {
					t.Fatalf("legacy diagnostic fields = %+v", failure)
				}
				profileRequireNoPublication(t, set, candidateReport)
			})
		}
	})

	t.Run("mixed relation batches retain exact legacy product diagnostics and precedence", func(t *testing.T) {
		relationRaw := profileRawSources(t, profileRelationFixture())[0]
		root, tail := profileLegacyFixture()

		rootUnknown := profileRawSources(t, root)
		rootUnknown[0].Document = profileAddRawRootMember(rootUnknown[0].Document, "unknown", "null")
		compatibilityNull := profileRawSources(t, root)
		compatibilityNull[0].Document = bytes.Replace(compatibilityNull[0].Document, []byte(`"loader_abi":1`), []byte(`"loader_abi":null`), 1)
		compatibilityOverflow := profileRawSources(t, root)
		compatibilityOverflow[0].Document = bytes.Replace(compatibilityOverflow[0].Document, []byte(`"loader_abi":1`), []byte(`"loader_abi":9223372036854775808`), 1)
		producerWrongType := profileRawSources(t, root)
		producerWrongType[0].Document = bytes.Replace(producerWrongType[0].Document, []byte(`"name":"godj-reference"`), []byte(`"name":null`), 1)

		wrongArm := profileRawSources(t, root)
		wrongArm[0].Document = bytes.Replace(
			wrongArm[0].Document,
			[]byte(`"kind":"create_model"`),
			[]byte(`"field":{},"kind":"create_model"`),
			1,
		)

		tooManyDependencies := profileCloneSource(root)
		tooManyDependencies.SourceID = "legacy-dependencies"
		tooManyDependencies.Definition.Dependencies = make([]profileIdentity, profileMaxDependencies+1)

		tooManyOperations := profileCloneSource(root)
		tooManyOperations.SourceID = "legacy-operations"
		tooManyOperations.Definition.Operations = make([]profileOperation, profileMaxOperations+1)

		tooManyFields := profileCloneSource(root)
		tooManyFields.SourceID = "legacy-fields"
		tooManyFields.Definition.Operations[0].Model.Fields = make([]profileField, profileMaxFields+1)

		secondOperation := profileCloneSource(tail)
		secondOperation.SourceID = "legacy-operation-index"
		secondOperation.Definition.Operations[1].AppLabel = "other"
		maxLengthOutOfRange := profileCloneSource(root)
		maxLengthOutOfRange.SourceID = "legacy-max-length"
		maxLengthOutOfRange.Definition.Operations[0].Model.Fields[1].MaxLength = profileMaximumWireLength + 1

		compound := profileCloneRawSources(rootUnknown)
		compound[0].Document = bytes.Replace(
			compound[0].Document,
			[]byte(`"kind":"create_model"`),
			[]byte(`"field":{},"kind":"create_model"`),
			1,
		)

		tests := []struct {
			name        string
			legacy      []definition.Source
			wantStage   string
			wantCode    string
			wantPointer string
			wantLimit   string
			wantOpIndex int
		}{
			{name: "root unknown", legacy: rootUnknown, wantStage: "document", wantCode: string(definition.CodeInvalidDocument), wantPointer: "/unknown", wantOpIndex: -1},
			{name: "compatibility null", legacy: compatibilityNull, wantStage: "document", wantCode: string(definition.CodeInvalidDocument), wantPointer: "/compatibility/loader_abi", wantOpIndex: -1},
			{name: "compatibility overflow", legacy: compatibilityOverflow, wantStage: "document", wantCode: string(definition.CodeInvalidDocument), wantPointer: "/compatibility/loader_abi", wantOpIndex: -1},
			{name: "producer wrong type", legacy: producerWrongType, wantStage: "document", wantCode: string(definition.CodeInvalidDocument), wantPointer: "/producer/name", wantOpIndex: -1},
			{name: "semantic wrong arm", legacy: wrongArm, wantStage: "semantic", wantCode: string(definition.CodeInvalidOperation), wantPointer: "/migration/operations/0/field", wantOpIndex: 0},
			{name: "dependency limit", legacy: profileRawSources(t, tooManyDependencies), wantStage: "semantic", wantCode: string(definition.CodeInvalidOperation), wantPointer: "/migration/dependencies", wantLimit: "dependencies_per_migration", wantOpIndex: -1},
			{name: "operation limit", legacy: profileRawSources(t, tooManyOperations), wantStage: "semantic", wantCode: string(definition.CodeInvalidOperation), wantPointer: "/migration/operations", wantLimit: "operations_per_migration", wantOpIndex: -1},
			{name: "field limit", legacy: profileRawSources(t, tooManyFields), wantStage: "semantic", wantCode: string(definition.CodeInvalidIR), wantPointer: "/migration/operations/0/model/fields", wantLimit: "fields_per_create_model", wantOpIndex: 0},
			{name: "operation index", legacy: profileRawSources(t, secondOperation), wantStage: "semantic", wantCode: string(definition.CodeInvalidOperation), wantPointer: "/migration/operations/1/app_label", wantOpIndex: 1},
			{name: "semantic max length range", legacy: profileRawSources(t, maxLengthOutOfRange), wantStage: "semantic", wantCode: string(definition.CodeInvalidDocument), wantPointer: "/migration/operations/0/model/fields/1/max_length", wantOpIndex: 0},
			{name: "compound document before semantic", legacy: compound, wantStage: "document", wantCode: string(definition.CodeInvalidDocument), wantPointer: "/unknown", wantOpIndex: -1},
			{name: "graph source mapping", legacy: profileRawSources(t, tail), wantStage: "graph", wantCode: string(migrations.CodeDependencyNotFound), wantPointer: "/migration/dependencies", wantOpIndex: -1},
		}
		for _, test := range tests {
			test := test
			t.Run(test.name, func(t *testing.T) {
				_, productReport, productErr := definition.Load(test.legacy...)
				productFailure := profileRequireCandidateFailure(t, profileMapProductLoadError(productErr, productReport))
				for permutation, mixedRaw := range [][]definition.Source{
					append(append([]definition.Source(nil), test.legacy...), profileCloneRawSource(relationRaw)),
					append([]definition.Source{profileCloneRawSource(relationRaw)}, profileCloneRawSources(test.legacy)...),
				} {
					set, report, err := profileLoadRaw(mixedRaw...)
					failure := profileRequireCandidateFailure(t, err)
					if !reflect.DeepEqual(failure, productFailure) {
						t.Fatalf("permutation %d mixed legacy failure diverged:\ncandidate=%+v\nproduct=%+v", permutation, failure, productFailure)
					}
					if failure.Stage != test.wantStage || failure.Code != test.wantCode || failure.Pointer != test.wantPointer ||
						failure.Limit != test.wantLimit || failure.OperationIndex != test.wantOpIndex {
						t.Fatalf("permutation %d exact context = %+v", permutation, failure)
					}
					profileRequireFailureContextMatchesError(t, report, failure)
					profileRequireNoPublication(t, set, report)
				}
			})
		}

		left := profileCloneSource(root)
		left.SourceID = "z-resource"
		left.Definition.App = "alpha"
		left.Definition.Operations[0].AppLabel = "alpha"
		left.Definition.Dependencies = make([]profileIdentity, profileMaxDependencies+1)
		right := profileCloneSource(root)
		right.SourceID = "a-resource"
		right.Definition.App = "zeta"
		right.Definition.Operations[0].AppLabel = "zeta"
		right.Definition.Name = "0002_resource"
		right.Definition.Dependencies = make([]profileIdentity, profileMaxDependencies+1)
		legacyResources := profileRawSources(t, left, right)
		_, productReport, productErr := definition.Load(legacyResources...)
		productFailure := profileRequireCandidateFailure(t, profileMapProductLoadError(productErr, productReport))
		for permutation, mixedRaw := range [][]definition.Source{
			append(profileCloneRawSources(legacyResources), profileCloneRawSource(relationRaw)),
			{profileCloneRawSource(relationRaw), profileCloneRawSource(legacyResources[1]), profileCloneRawSource(legacyResources[0])},
		} {
			set, report, err := profileLoadRaw(mixedRaw...)
			failure := profileRequireCandidateFailure(t, err)
			if !reflect.DeepEqual(failure, productFailure) || failure.SourceID != "a-resource" {
				t.Fatalf("same-class permutation %d = candidate %+v product %+v", permutation, failure, productFailure)
			}
			profileRequireFailureContextMatchesError(t, report, failure)
			profileRequireNoPublication(t, set, report)
		}
	})

	empty, emptyReport, err := profileLoadRaw()
	if err != nil {
		t.Fatalf("profileLoadRaw empty: %v", err)
	}
	if empty.profileDigest() != definition.EmptySetDigest || !empty.hasLegacy ||
		emptyReport != (profileLoadReport{PlannerConstruction: 1, SetsPublished: 1}) {
		t.Fatalf("empty legacy set = digest %q hasLegacy=%t report=%+v", empty.profileDigest(), empty.hasLegacy, emptyReport)
	}
}

func TestProfileMixedDigestIncludesExactProfileAndFullIRV3Meaning(t *testing.T) {
	t.Parallel()

	legacy, _ := profileLegacyFixture()
	relation := profileRelationFixture()
	mixed, mixedReport, err := profileLoad(relation, legacy)
	if err != nil {
		t.Fatalf("profileLoad mixed: %v", err)
	}
	relationOnly, relationReport, err := profileLoad(relation)
	if err != nil {
		t.Fatalf("profileLoad relation: %v", err)
	}
	const (
		wantMixedDigest    = "sha256:08127d3e13bcedaedb52bf80b9ae2281b4ab596481d31f5b1f78d749fdae1644"
		wantRelationDigest = "sha256:5abaa4dff57b7454d1526cb88917390d5593b5c297be12eebbb8bb175d1fa682"
	)
	if mixed.profileDigest() != wantMixedDigest || relationOnly.profileDigest() != wantRelationDigest {
		t.Fatalf("candidate v2 digests = mixed %q, relation %q", mixed.profileDigest(), relationOnly.profileDigest())
	}
	if mixedReport.ParserInvocations != 2 || relationReport.ParserInvocations != 1 {
		t.Fatalf("mixed/relation parser invocations = %d/%d, want exactly one/source", mixedReport.ParserInvocations, relationReport.ParserInvocations)
	}
	if got, want := len(mixed.profileCanonicalBytes()), 1216; got != want {
		t.Fatalf("mixed candidate canonical bytes = %d, want %d", got, want)
	}
	if got, want := len(relationOnly.profileCanonicalBytes()), 618; got != want {
		t.Fatalf("relation candidate canonical bytes = %d, want %d", got, want)
	}
	canonical := string(mixed.profileCanonicalBytes())
	for _, fragment := range []string{
		`"domain":"godj:migration-definition-set:v2"`,
		`"profile":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2}`,
		`"profile":{"definition_format":1,"loader_abi":2,"operation_codec":2,"schema_ir":3}`,
		`"cardinality":"many_to_one"`,
		`"reverse":{"disabled":false,"name":"articles"}`,
		`"target":{"app_label":"authors","model_name":"author"}`,
	} {
		if !strings.Contains(canonical, fragment) {
			t.Fatalf("mixed canonical bytes omit %s:\n%s", fragment, canonical)
		}
	}
	for _, noncanonical := range []string{`"target_field"`, `"decoder"`, `legacy_scalar_v1`, `relation_v2`} {
		if strings.Contains(canonical, noncanonical) {
			t.Fatalf("mixed canonical bytes contain internal/non-IR payload %q:\n%s", noncanonical, canonical)
		}
	}
	if mixed.profileDigest() == relationOnly.profileDigest() || mixed.profileDigest() == profileEmptyDigest {
		t.Fatalf("digest domains collapsed: mixed=%s relation=%s", mixed.profileDigest(), relationOnly.profileDigest())
	}

	legacyProduct, _, err := definition.Load(profileRawSources(t, legacy)...)
	if err != nil {
		t.Fatalf("decode legacy mixed member through product loader: %v", err)
	}
	legacyKey := migrations.MigrationKey{App: legacy.Definition.App, Name: legacy.Definition.Name}
	var mixedLegacy migrations.Migration
	for _, published := range mixed.profileDefinitions() {
		if published.Profile == profileLegacy && published.Definition.App == legacyKey.App && published.Definition.Name == legacyKey.Name {
			_, converted, failure := profileCanonicalDefinition(published.Definition, profileDecoderLegacy)
			if failure != nil {
				t.Fatalf("convert published legacy member: %v", failure)
			}
			mixedLegacy = converted
		}
	}
	if productDefinitions := legacyProduct.Definitions(); len(productDefinitions) != 1 || !reflect.DeepEqual(mixedLegacy, productDefinitions[0]) {
		t.Fatalf("mixed legacy member diverged from product decoder: mixed=%#v product=%#v", mixedLegacy, productDefinitions)
	}

	permuted, _, err := profileLoad(legacy, relation)
	if err != nil || permuted.profileDigest() != mixed.profileDigest() || !reflect.DeepEqual(permuted.profileCanonicalBytes(), mixed.profileCanonicalBytes()) {
		t.Fatalf("input order changed mixed set: digest=%q/%q error=%v", permuted.profileDigest(), mixed.profileDigest(), err)
	}
	metadataOnly := []profileSource{profileCloneSource(legacy), profileCloneSource(relation)}
	metadataOnly[0].SourceID = "renamed-z"
	metadataOnly[0].Producer = profileProducer{Name: "different", Version: "9"}
	metadataOnly[1].SourceID = "renamed-a"
	metadataOnly[1].Producer = profileProducer{Name: "another", Version: "8"}
	equivalent, _, err := profileLoad(metadataOnly...)
	if err != nil || equivalent.profileDigest() != mixed.profileDigest() {
		t.Fatalf("source/provenance changed semantic digest: %q/%q error=%v", equivalent.profileDigest(), mixed.profileDigest(), err)
	}
	originalSeals := make(map[migrations.MigrationKey]string)
	for _, published := range mixed.profileDefinitions() {
		originalSeals[migrations.MigrationKey{App: published.Definition.App, Name: published.Definition.Name}] = published.provenanceSeal
	}
	gotProvenance := make(map[string]profileProducer)
	equivalentDefinitions := equivalent.profileDefinitions()
	for _, published := range equivalentDefinitions {
		gotProvenance[published.SourceID] = published.Producer
		key := migrations.MigrationKey{App: published.Definition.App, Name: published.Definition.Name}
		if published.provenanceSeal == "" || published.provenanceSeal == originalSeals[key] {
			t.Fatalf("published provenance seal did not bind metadata for %v: original=%q equivalent=%q", key, originalSeals[key], published.provenanceSeal)
		}
	}
	if !reflect.DeepEqual(gotProvenance, map[string]profileProducer{
		"renamed-z": {Name: "different", Version: "9"},
		"renamed-a": {Name: "another", Version: "8"},
	}) {
		t.Fatalf("published provenance = %+v", gotProvenance)
	}
	equivalentDefinitions[0].SourceID = "mutated-accessor"
	equivalentDefinitions[0].Producer.Name = "mutated-accessor"
	equivalentDefinitions[0].provenanceSeal = "mutated-accessor"
	freshDefinitions := equivalent.profileDefinitions()
	if freshDefinitions[0].SourceID == "mutated-accessor" || freshDefinitions[0].Producer.Name == "mutated-accessor" ||
		freshDefinitions[0].provenanceSeal == "mutated-accessor" {
		t.Fatalf("published provenance accessor retained alias: %#v", freshDefinitions[0])
	}

	changedReverse := profileCloneSource(relation)
	changedReverse.Definition.Operations[0].Field.Relation.Reverse.Name = "edited_articles"
	changed, _, err := profileLoad(legacy, changedReverse)
	if err != nil || changed.profileDigest() == mixed.profileDigest() {
		t.Fatalf("reverse metadata did not produce a distinct valid digest: digest=%q error=%v", changed.profileDigest(), err)
	}
	changedDelete := profileCloneSource(relation)
	changedDelete.Definition.Operations[0].Field.Nullable = true
	changedDelete.Definition.Operations[0].Field.Relation.OnDelete = string(ir.DeleteSetNull)
	changed, _, err = profileLoad(legacy, changedDelete)
	if err != nil || changed.profileDigest() == mixed.profileDigest() {
		t.Fatalf("delete/nullability did not produce a distinct valid digest: digest=%q error=%v", changed.profileDigest(), err)
	}
	changedTarget := profileCloneSource(relation)
	changedTarget.Definition.Operations[0].Field.Relation.Target.Model = "editor"
	changed, _, err = profileLoad(legacy, changedTarget)
	if err != nil || changed.profileDigest() == mixed.profileDigest() {
		t.Fatalf("target identity did not produce a distinct valid digest: digest=%q error=%v", changed.profileDigest(), err)
	}
	field := relation.Definition.Operations[0].Field
	converted, _, failure := profileFieldIR(*field, profileDecoderRelation)
	if failure != nil {
		t.Fatalf("profileFieldIR: %v", failure)
	}
	want := ir.Field{
		Name: "author", GoName: "Author", Column: "author_id", Kind: ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "articles"},
			OnDelete:    ir.DeleteProtect,
		},
	}
	if !reflect.DeepEqual(converted, want) {
		t.Fatalf("candidate relation IR = %+v, want %+v", converted, want)
	}

	t.Run("one combined planner accepts a legacy dependency on a relation profile", func(t *testing.T) {
		dependent := profileCloneSource(legacy)
		dependent.SourceID = "legacy-after-relation"
		dependent.Definition.Name = "0002_after_relation"
		dependent.Definition.Dependencies = []profileIdentity{{App: relation.Definition.App, Name: relation.Definition.Name}}
		set, report, err := profileLoad(relation, dependent)
		if err != nil {
			t.Fatalf("combined mixed planner: %v", err)
		}
		if report != (profileLoadReport{
			DocumentsReceived: 2, ProfilesAccepted: 2, OperationsDecoded: 2, PlannerConstruction: 1,
			DefinitionsPublished: 2, SetsPublished: 1, ParserInvocations: 2,
		}) ||
			len(set.profileDefinitions()) != 2 || set.profileDigest() == profileEmptyDigest {
			t.Fatalf("combined mixed publication = digest %q report %+v definitions %d", set.profileDigest(), report, len(set.profileDefinitions()))
		}
	})
}

func TestProfilePublishedSetIsDeepCopiedAndRawFailuresAreAtomic(t *testing.T) {
	t.Parallel()

	legacy, _ := profileLegacyFixture()
	relation := profileRelationFixture()
	raw := profileRawSources(t, legacy, relation)
	original := profileCloneRawSources(raw)
	set, report, err := profileLoadRaw(raw...)
	if err != nil {
		t.Fatalf("profileLoadRaw mixed: %v", err)
	}
	if report != (profileLoadReport{
		DocumentsReceived: 2, ProfilesAccepted: 2, OperationsDecoded: 2, PlannerConstruction: 1,
		DefinitionsPublished: 2, SetsPublished: 1, ParserInvocations: 2,
	}) {
		t.Fatalf("mixed report = %+v", report)
	}
	wantDigest := set.profileDigest()
	for index := range raw {
		for byteIndex := range raw[index].Document {
			raw[index].Document[byteIndex] = 'x'
		}
		raw[index].SourceID = "mutated"
	}
	first := set.profileDefinitions()
	first[1].Definition.Operations[0].Field.Relation.Reverse.Name = "mutated-accessor"
	second := set.profileDefinitions()
	if got := second[1].Definition.Operations[0].Field.Relation; got.Target.App != "authors" || got.Reverse.Name != "articles" {
		t.Fatalf("published relation retained alias: %+v", got)
	}
	if set.profileDigest() != wantDigest || len(set.profileCanonicalBytes()) == 0 {
		t.Fatal("caller mutation changed published mixed set")
	}
	if reflect.DeepEqual(raw, original) {
		t.Fatal("test failed to mutate the caller-owned raw sources")
	}

	t.Run("strict raw relation rejection publishes nothing", func(t *testing.T) {
		invalid := profileRawSources(t, relation)[0]
		invalid.Document = bytes.Replace(invalid.Document, []byte(`"relation":{`), []byte(`"target_field":"id","relation":{`), 1)
		failed, failedReport, err := profileLoadRaw(invalid)
		failure := profileRequireCandidateFailure(t, err)
		if failure.Code != string(definition.CodeInvalidIR) || failure.Reason != "invalid_ir" ||
			failure.Pointer != "/migration/operations/0/field/target_field" || failure.OperationIndex != 0 {
			t.Fatalf("failure = %+v", failure)
		}
		profileRequireFailureContextMatchesError(t, failedReport, failure)
		profileRequireNoPublication(t, failed, failedReport)
		if set.profileDigest() != wantDigest {
			t.Fatal("failed load mutated an already-published set")
		}
	})

	t.Run("resource class precedence remains deterministic", func(t *testing.T) {
		dependency := profileCloneSource(relation)
		dependency.SourceID = "z-dependencies"
		dependency.Definition.Dependencies = make([]profileIdentity, profileMaxDependencies+1)
		operation := profileCloneSource(relation)
		operation.SourceID = "a-operations"
		operation.Definition.App = "other"
		operation.Definition.Name = "0001_ops"
		operation.Definition.Operations = make([]profileOperation, profileMaxOperations+1)
		for _, sources := range [][]profileSource{{operation, dependency}, {dependency, operation}} {
			failed, failedReport, err := profileLoad(sources...)
			failure := profileRequireCandidateFailure(t, err)
			if failure.Code != string(definition.CodeInvalidOperation) || failure.Limit != "dependencies_per_migration" ||
				failure.Pointer != "/migration/dependencies" || failure.SourceID != "z-dependencies" || failure.OperationIndex != -1 ||
				failure.Maximum != profileMaxDependencies || failure.Actual != profileMaxDependencies+1 {
				t.Fatalf("resource precedence failure = %+v", failure)
			}
			profileRequireFailureContextMatchesError(t, failedReport, failure)
			profileRequireNoPublication(t, failed, failedReport)
		}
	})

	t.Run("field cap is checked before decoding field payloads", func(t *testing.T) {
		source := profileCloneSource(relation)
		model := profileModel{Name: "huge", GoName: "Huge", DBTable: "huge", Fields: make([]profileField, profileMaxFields+1)}
		source.Definition.Name = "0003_huge"
		source.Definition.Operations = []profileOperation{{AppLabel: "blog", Kind: "create_model", Model: &model}}
		failed, failedReport, err := profileLoad(source)
		failure := profileRequireCandidateFailure(t, err)
		if failure.Code != string(definition.CodeInvalidIR) || failure.Limit != "fields_per_create_model" ||
			failure.Pointer != "/migration/operations/0/model/fields" || failure.OperationIndex != 0 ||
			failure.Maximum != profileMaxFields || failure.Actual != profileMaxFields+1 {
			t.Fatalf("field resource failure = %+v", failure)
		}
		profileRequireFailureContextMatchesError(t, failedReport, failure)
		profileRequireNoPublication(t, failed, failedReport)
	})
}

func TestProfileRelationAndScalarSemanticsUseExactNormalizedSchemaIR(t *testing.T) {
	t.Parallel()

	relation := func() profileSource { return profileRelationFixture() }
	relationCreate := func() profileSource { return profileRelationCreateFixture() }

	tests := []struct {
		name       string
		source     func() profileSource
		mutate     func(*profileSource)
		wantReason string
	}{
		{
			name: "create model rejects field sibling arm", source: relationCreate, wantReason: "invalid_operation",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field = &profileField{} },
		},
		{
			name: "create model rejects model name sibling arm", source: relationCreate, wantReason: "invalid_operation",
			mutate: func(source *profileSource) { source.Definition.Operations[0].ModelName = "article" },
		},
		{
			name: "add field rejects model sibling arm", source: relation, wantReason: "invalid_operation",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Model = &profileModel{} },
		},
		{
			name: "operation app matches migration", source: relation, wantReason: "invalid_operation",
			mutate: func(source *profileSource) { source.Definition.Operations[0].AppLabel = "other" },
		},
		{
			name: "app identifier", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) {
				source.Definition.App = "bad-app"
				source.Definition.Operations[0].AppLabel = "bad-app"
			},
		},
		{
			name: "model identifier", source: relation, wantReason: "invalid_operation",
			mutate: func(source *profileSource) { source.Definition.Operations[0].ModelName = "Bad-Model" },
		},
		{
			name: "table identifier", source: relationCreate, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Model.DBTable = "Bad-Table" },
		},
		{
			name: "table must already be normalized", source: relationCreate, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Model.DBTable = "" },
		},
		{
			name: "model GoName", source: relationCreate, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Model.GoName = "article" },
		},
		{
			name: "field name identifier", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.Name = "bad-name" },
		},
		{
			name: "field column identifier", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.Column = "Bad-Column" },
		},
		{
			name: "field column must already be normalized", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.Column = "" },
		},
		{
			name: "field GoName", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.GoName = "author" },
		},
		{
			name: "duplicate field name", source: relationCreate, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Model.Fields[1].Name = "id" },
		},
		{
			name: "duplicate field GoName", source: relationCreate, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Model.Fields[1].GoName = "ID" },
		},
		{
			name: "duplicate field column", source: relationCreate, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Model.Fields[1].Column = "id" },
		},
		{
			name: "relation target app", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.Relation.Target.App = "bad-app" },
		},
		{
			name: "relation target model", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.Relation.Target.Model = "Bad-Model" },
		},
		{
			name: "cardinality is exactly many to one", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) {
				source.Definition.Operations[0].Field.Relation.Cardinality = string(ir.RelationOneToMany)
			},
		},
		{
			name: "reverse requires name or disabled", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.Relation.Reverse = profileReverse{} },
		},
		{
			name: "reverse cannot have name and disabled", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.Relation.Reverse.Disabled = true },
		},
		{
			name: "reverse name identifier", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.Relation.Reverse.Name = "bad-name" },
		},
		{
			name: "unsupported delete policy", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.Relation.OnDelete = "cascade" },
		},
		{
			name: "set null requires nullable", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) {
				source.Definition.Operations[0].Field.Relation.OnDelete = string(ir.DeleteSetNull)
			},
		},
		{
			name: "relation default arm is rejected", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) {
				value := "hidden"
				source.Definition.Operations[0].Field.Default = &profileDefault{Kind: string(ir.ScalarString), String: &value}
			},
		},
		{
			name: "relation scalar union is closed", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) {
				value := "hidden"
				boolean := false
				source.Definition.Operations[0].Field.Default = &profileDefault{Kind: string(ir.ScalarString), String: &value, Boolean: &boolean}
			},
		},
		{
			name: "relation metadata is required", source: relation, wantReason: "invalid_ir",
			mutate: func(source *profileSource) { source.Definition.Operations[0].Field.Relation = nil },
		},
	}
	wantPointers := map[string]string{
		"create model rejects field sibling arm":      "/migration/operations/0/field",
		"create model rejects model name sibling arm": "/migration/operations/0/model_name",
		"add field rejects model sibling arm":         "/migration/operations/0/model",
		"operation app matches migration":             "/migration/operations/0/app_label",
		"app identifier":                              "/migration/operations/0/app_label",
		"model identifier":                            "/migration/operations/0/model_name",
		"table identifier":                            "/migration/operations/0/model/db_table",
		"table must already be normalized":            "/migration/operations/0/model/db_table",
		"model GoName":                                "/migration/operations/0/model/go_name",
		"field name identifier":                       "/migration/operations/0/field/name",
		"field column identifier":                     "/migration/operations/0/field/column",
		"field column must already be normalized":     "/migration/operations/0/field/column",
		"field GoName":                                "/migration/operations/0/field/go_name",
		"duplicate field name":                        "/migration/operations/0/model/fields",
		"duplicate field GoName":                      "/migration/operations/0/model/fields",
		"duplicate field column":                      "/migration/operations/0/model/fields",
		"relation target app":                         "/migration/operations/0/field/relation/target/app_label",
		"relation target model":                       "/migration/operations/0/field/relation/target/model_name",
		"cardinality is exactly many to one":          "/migration/operations/0/field/relation/cardinality",
		"reverse requires name or disabled":           "/migration/operations/0/field/relation/reverse",
		"reverse cannot have name and disabled":       "/migration/operations/0/field/relation/reverse",
		"reverse name identifier":                     "/migration/operations/0/field/relation/reverse/name",
		"unsupported delete policy":                   "/migration/operations/0/field/relation/on_delete",
		"set null requires nullable":                  "/migration/operations/0/field/relation/on_delete",
		"relation default arm is rejected":            "/migration/operations/0/field/default",
		"relation scalar union is closed":             "/migration/operations/0/field/default",
		"relation metadata is required":               "/migration/operations/0/field/relation",
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			source := test.source()
			published, _, err := profileLoad(source)
			if err != nil {
				t.Fatalf("profileLoad semantic baseline: %v", err)
			}
			wantDigest := published.profileDigest()
			wantCanonical := published.profileCanonicalBytes()
			test.mutate(&source)
			set, report, err := profileLoad(source)
			failure := profileRequireCandidateFailure(t, err)
			wantCode := string(definition.CodeInvalidIR)
			if test.wantReason == "invalid_operation" {
				wantCode = string(definition.CodeInvalidOperation)
			}
			if failure.Category != definition.CategorySource || failure.Code != wantCode || failure.Stage != "semantic" ||
				failure.Reason != test.wantReason || failure.SourceID != source.SourceID || failure.Pointer != wantPointers[test.name] ||
				failure.App != source.Definition.App || failure.Name != source.Definition.Name || failure.OperationIndex != 0 {
				t.Fatalf("semantic failure = %#v, want %s/%s at %q pointer %q", err, wantCode, test.wantReason, source.SourceID, wantPointers[test.name])
			}
			profileRequireFailureContextMatchesError(t, report, failure)
			profileRequireNoPublication(t, set, report)
			if published.profileDigest() != wantDigest || !reflect.DeepEqual(published.profileCanonicalBytes(), wantCanonical) {
				t.Fatalf("rejected mutation aliased an already published set: digest=%s/%s", published.profileDigest(), wantDigest)
			}
		})
	}

	t.Run("reverse can be explicitly disabled", func(t *testing.T) {
		source := relation()
		source.Definition.Operations[0].Field.Relation.Reverse = profileReverse{Disabled: true}
		set, report, err := profileLoad(source)
		if err != nil || len(set.profileDefinitions()) != 1 || report.SetsPublished != 1 {
			t.Fatalf("disabled reverse = digest %q report %+v error %v", set.profileDigest(), report, err)
		}
	})

	t.Run("legacy tuple still rejects relation through actual product IR v2", func(t *testing.T) {
		source := relation()
		source.Profile = profileLegacy
		set, report, err := profileLoad(source)
		failure := profileRequireCandidateFailure(t, err)
		if failure.Stage != "semantic" || failure.Code != string(definition.CodeInvalidIR) || failure.Reason != "invalid_ir" {
			t.Fatalf("legacy relation failure = %+v", failure)
		}
		profileRequireNoPublication(t, set, report)
	})

	t.Run("max length uses the fixed signed 32-bit wire boundary", func(t *testing.T) {
		boundary := relationCreate()
		boundary.Definition.Operations[0].Model.Fields[1].MaxLength = profileMaximumWireLength
		set, report, err := profileLoad(boundary)
		if err != nil || len(set.profileDefinitions()) != 1 || report.SetsPublished != 1 {
			t.Fatalf("maximum max_length = digest %q report %+v error %v", set.profileDigest(), report, err)
		}

		overflow := relationCreate()
		overflow.Definition.Operations[0].Model.Fields[1].MaxLength = profileMaximumWireLength + 1
		failed, failedReport, err := profileLoad(overflow)
		failure := profileRequireCandidateFailure(t, err)
		if failure.Stage != "semantic" || failure.Code != string(definition.CodeInvalidDocument) || failure.Reason != "out_of_range" ||
			failure.Pointer != "/migration/operations/0/model/fields/1/max_length" || failure.OperationIndex != 0 {
			t.Fatalf("overflow max_length failure = %+v", failure)
		}
		profileRequireFailureContextMatchesError(t, failedReport, failure)
		profileRequireNoPublication(t, failed, failedReport)
	})
}

func TestProfileGraphIdentityDependenciesAndOperationOrderUseProductSemantics(t *testing.T) {
	t.Parallel()

	requireGraphFailure := func(t *testing.T, sources []profileSource, code migrations.ErrorCode) {
		t.Helper()
		for _, permutation := range [][]profileSource{sources, profileReverseSources(sources)} {
			set, report, err := profileLoad(permutation...)
			failure := profileRequireCandidateFailure(t, err)
			if failure.Stage != "graph" || failure.Code != string(code) || failure.Reason != string(code) {
				t.Fatalf("graph failure = %+v, want %s", failure, code)
			}
			profileRequireNoPublication(t, set, report)
		}
	}

	t.Run("duplicate definition identity", func(t *testing.T) {
		left := profileRelationFixture()
		right := profileCloneSource(left)
		right.SourceID = "relation-duplicate"
		requireGraphFailure(t, []profileSource{left, right}, migrations.CodeDuplicateNode)
	})

	t.Run("duplicate dependency", func(t *testing.T) {
		root := profileRelationCreateFixture()
		root.Definition.App = "authors"
		root.Definition.Name = "0001_initial"
		root.Definition.Operations[0].AppLabel = "authors"
		root.Definition.Operations[0].Model.DBTable = "authors_author"
		child := profileRelationFixture()
		child.Definition.Dependencies = []profileIdentity{
			{App: "authors", Name: "0001_initial"},
			{App: "authors", Name: "0001_initial"},
		}
		requireGraphFailure(t, []profileSource{root, child}, migrations.CodeDuplicateDependency)
	})

	t.Run("missing dependency", func(t *testing.T) {
		source := profileRelationFixture()
		source.Definition.Dependencies = []profileIdentity{{App: "authors", Name: "0001_initial"}}
		requireGraphFailure(t, []profileSource{source}, migrations.CodeDependencyNotFound)
	})

	t.Run("invalid definition identity", func(t *testing.T) {
		source := profileRelationFixture()
		source.Definition.Name = ""
		source.Definition.Dependencies = []profileIdentity{}
		source.Definition.Operations = []profileOperation{}
		requireGraphFailure(t, []profileSource{source}, migrations.CodeInvalidNode)
	})

	t.Run("operation sequence remains semantic", func(t *testing.T) {
		_, tail := profileLegacyFixture()
		tail.Profile = profileRelationTuple
		tail.Definition.Dependencies = []profileIdentity{}
		ordered, _, err := profileLoad(tail)
		if err != nil {
			t.Fatalf("ordered: %v", err)
		}
		reversedTail := profileCloneSource(tail)
		reversedTail.Definition.Operations[0], reversedTail.Definition.Operations[1] = reversedTail.Definition.Operations[1], reversedTail.Definition.Operations[0]
		reversed, _, err := profileLoad(reversedTail)
		if err != nil {
			t.Fatalf("reversed: %v", err)
		}
		if ordered.profileDigest() == reversed.profileDigest() {
			t.Fatal("operation order did not affect mixed-domain semantic digest")
		}
	})
}

func profileRequireCandidateFailure(t *testing.T, err error) *profileCandidateError {
	t.Helper()
	var failure *profileCandidateError
	if !errors.As(err, &failure) || failure == nil {
		t.Fatalf("error = %T %v, want *profileCandidateError", err, err)
	}
	return failure
}

func profileRequireFailureContextMatchesError(t *testing.T, report profileLoadReport, failure *profileCandidateError) {
	t.Helper()
	want := profileFailureContext{
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
	if report.Failure == nil {
		t.Fatalf("report failure context is nil, want %+v", want)
	}
	got := *report.Failure
	if len(got.GraphSources) == 0 {
		got.GraphSources = nil
	}
	if len(want.GraphSources) == 0 {
		want.GraphSources = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report failure context = %+v, want %+v", report.Failure, want)
	}
}

func profileRequireNoPublication(t *testing.T, set profileSet, report profileLoadReport) {
	t.Helper()
	if len(set.profileCanonicalBytes()) != 0 || len(set.profileDefinitions()) != 0 || len(set.profileLegacyDefinitions()) != 0 ||
		set.profileDigest() != profileEmptyDigest || report.DefinitionsPublished != 0 || report.SetsPublished != 0 {
		t.Fatalf("failure published partial state: digest=%s report=%+v", set.profileDigest(), report)
	}
}

func profileRawSources(t *testing.T, sources ...profileSource) []definition.Source {
	t.Helper()
	raw := make([]definition.Source, len(sources))
	for index := range sources {
		document, err := json.Marshal(profileWireDocument{
			Compatibility: sources[index].Profile,
			Producer:      sources[index].Producer,
			Migration:     sources[index].Definition,
		})
		if err != nil {
			t.Fatalf("marshal profile source %s: %v", sources[index].SourceID, err)
		}
		raw[index] = definition.Source{SourceID: sources[index].SourceID, Document: document}
	}
	return raw
}

func profileCloneRawSource(value definition.Source) definition.Source {
	return definition.Source{SourceID: value.SourceID, Document: append([]byte(nil), value.Document...)}
}

func profileCloneRawSources(values []definition.Source) []definition.Source {
	cloned := make([]definition.Source, len(values))
	for index := range values {
		cloned[index] = profileCloneRawSource(values[index])
	}
	return cloned
}

func profileAddRawRootMember(document []byte, name, value string) []byte {
	trimmed := bytes.TrimSpace(document)
	output := append([]byte(nil), trimmed[:len(trimmed)-1]...)
	output = append(output, []byte(`,"`+name+`":`+value+`}`)...)
	return output
}

func profileNullArray(values int) string {
	if values <= 0 {
		return "[]"
	}
	return "[" + strings.Repeat("null,", values-1) + "null]"
}

func profileReverseSources(values []profileSource) []profileSource {
	cloned := make([]profileSource, len(values))
	for index := range values {
		cloned[len(values)-1-index] = profileCloneSource(values[index])
	}
	return cloned
}

func profileTestProducer() profileProducer {
	return profileProducer{Name: "test-only-relation-candidate", Version: "0.1.0"}
}

func profileLegacyFixture() (profileSource, profileSource) {
	falseValue := false
	untitled := "untitled"
	root := profileSource{
		SourceID: "opaque-z-root",
		Producer: profileProducer{Name: "godj-reference", Version: "0.1.0"},
		Profile:  profileLegacy,
		Definition: profileDefinition{
			App:          "alpha",
			Dependencies: []profileIdentity{},
			Name:         "0001_initial",
			Operations: []profileOperation{{
				AppLabel: "alpha",
				Kind:     "create_model",
				Model: &profileModel{
					DBTable: "godj_definition_alpha_entry",
					GoName:  "Entry",
					Name:    "entry",
					Fields: []profileField{
						{Column: "id", GoName: "ID", Kind: string(ir.FieldAuto), Name: "id", PrimaryKey: true},
						{Column: "title", Default: &profileDefault{Kind: string(ir.ScalarString), String: &untitled}, GoName: "Title", Kind: string(ir.FieldChar), MaxLength: 64, Name: "title"},
					},
				},
			}},
		},
	}
	tail := profileSource{
		SourceID: "opaque-a-tail",
		Producer: profileProducer{Name: "godj-reference", Version: "0.1.0"},
		Profile:  profileLegacy,
		Definition: profileDefinition{
			App:          "alpha",
			Dependencies: []profileIdentity{{App: "alpha", Name: "0001_initial"}},
			Name:         "0002_fields",
			Operations: []profileOperation{
				{
					AppLabel: "alpha", Kind: "add_field", ModelName: "entry",
					Field: &profileField{Column: "published", Default: &profileDefault{Boolean: &falseValue, Kind: string(ir.ScalarBoolean)}, GoName: "Published", Kind: string(ir.FieldBoolean), Name: "published"},
				},
				{
					AppLabel: "alpha", Kind: "add_field", ModelName: "entry",
					Field: &profileField{Column: "summary", GoName: "Summary", Kind: string(ir.FieldChar), MaxLength: 255, Name: "summary", Nullable: true},
				},
			},
		},
	}
	return root, tail
}

func profileRelationFixture() profileSource {
	return profileSource{
		SourceID: "relation-blog-author",
		Producer: profileTestProducer(),
		Profile:  profileRelationTuple,
		Definition: profileDefinition{
			App:          "blog",
			Dependencies: []profileIdentity{},
			Name:         "0002_article_author",
			Operations: []profileOperation{{
				AppLabel:  "blog",
				Kind:      "add_field",
				ModelName: "article",
				Field: &profileField{
					Column: "author_id",
					GoName: "Author",
					Kind:   string(ir.FieldForeignKey),
					Name:   "author",
					Relation: &profileRelation{
						Target:      profileTarget{App: "authors", Model: "author"},
						Cardinality: string(ir.RelationManyToOne),
						Reverse:     profileReverse{Name: "articles"},
						OnDelete:    string(ir.DeleteProtect),
					},
				},
			}},
		},
	}
}

func profileRelationCreateFixture() profileSource {
	return profileSource{
		SourceID: "relation-create-article",
		Producer: profileTestProducer(),
		Profile:  profileRelationTuple,
		Definition: profileDefinition{
			App:          "blog",
			Dependencies: []profileIdentity{},
			Name:         "0001_initial",
			Operations: []profileOperation{{
				AppLabel: "blog",
				Kind:     "create_model",
				Model: &profileModel{
					DBTable: "blog_article",
					GoName:  "Article",
					Name:    "article",
					Fields: []profileField{
						{Column: "id", GoName: "ID", Kind: string(ir.FieldAuto), Name: "id", PrimaryKey: true},
						{Column: "title", GoName: "Title", Kind: string(ir.FieldChar), MaxLength: 128, Name: "title"},
					},
				},
			}},
		},
	}
}
