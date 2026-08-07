package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestTypedScalarRoundTrip(t *testing.T) {
	t.Parallel()

	values := []Value{
		Null(),
		Boolean(false),
		Boolean(true),
		Integer("-42"),
		Integer("0"),
		String("한글 and \u0000"),
		Decimal("12.340"),
		Datetime("2026-08-07T01:02:03.123456Z"),
		UUID("123e4567-e89b-12d3-a456-426614174000"),
		Bytes([]byte{0x00, 0x01, 0xfe, 0xff}),
		PrimaryKey(Integer("7")),
	}

	for _, value := range values {
		value := value
		t.Run(string(value.Type), func(t *testing.T) {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Value
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, value) {
				t.Fatalf("round trip mismatch\nwant: %#v\n got: %#v", value, decoded)
			}
		})
	}
}

func TestValueValidationRejectsNonCanonicalShapes(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unknown field":     `{"type":"int","value":"1","extra":true}`,
		"missing value":     `{"type":"int"}`,
		"leading zero int":  `{"type":"int","value":"01"}`,
		"negative zero int": `{"type":"int","value":"-0"}`,
		"decimal exponent":  `{"type":"decimal","value":"1e3"}`,
		"uppercase uuid":    `{"type":"uuid","value":"123E4567-E89B-12D3-A456-426614174000"}`,
		"invalid base64":    `{"type":"bytes","encoding":"base64","value":"%%%"}`,
		"wrong encoding":    `{"type":"bytes","encoding":"hex","value":"00"}`,
		"list pk":           `{"type":"pk","value":{"type":"list","items":[]}}`,
		"missing list":      `{"type":"list"}`,
		"unsorted object":   `{"type":"object","fields":[{"name":"b","value":{"type":"null"}},{"name":"a","value":{"type":"null"}}]}`,
		"duplicate field":   `{"type":"object","fields":[{"name":"a","value":{"type":"null"}},{"name":"a","value":{"type":"null"}}]}`,
		"trailing JSON":     `{"type":"null"} {"type":"null"}`,
	}

	for name, input := range tests {
		name, input := name, input
		t.Run(name, func(t *testing.T) {
			var value Value
			if err := json.Unmarshal([]byte(input), &value); err == nil {
				t.Fatalf("expected %s to fail", input)
			}
		})
	}
}

func TestObjectCanonicalizationIsIndependentOfMapOrder(t *testing.T) {
	t.Parallel()

	keys := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	baseline := Object(map[string]Value{
		"alpha":   Integer("1"),
		"bravo":   Integer("2"),
		"charlie": Integer("3"),
		"delta":   Integer("4"),
		"echo":    Integer("5"),
	})
	want, err := MarshalCanonical(baseline)
	if err != nil {
		t.Fatal(err)
	}

	random := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 200; iteration++ {
		permutation := random.Perm(len(keys))
		fields := make(map[string]Value, len(keys))
		for _, index := range permutation {
			fields[keys[index]] = Integer(fmt.Sprint(index + 1))
		}
		got, err := MarshalCanonical(Object(fields))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("iteration %d changed canonical bytes\nwant: %s\n got: %s", iteration, want, got)
		}
	}
}

func TestListOrderIsPreserved(t *testing.T) {
	t.Parallel()

	forward, err := MarshalCanonical(List(Integer("1"), Integer("2"), Integer("3")))
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := MarshalCanonical(List(Integer("3"), Integer("2"), Integer("1")))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(forward, reverse) {
		t.Fatalf("list ordering was erased: %s", forward)
	}
	if !strings.Contains(string(forward), `"value":"1"},{"type":"int","value":"2"`) {
		t.Fatalf("forward order not present in canonical JSON: %s", forward)
	}
}

func TestCanonicalObservationSuiteIsIdempotent(t *testing.T) {
	t.Parallel()

	original := validSuite()
	first, err := MarshalCanonical(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeObservationSuite(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalCanonical(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("canonical encoding is not idempotent\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestIntegerRoundTripProperty(t *testing.T) {
	t.Parallel()

	for number := -1000; number <= 1000; number++ {
		text := fmt.Sprint(number)
		encoded, err := json.Marshal(Integer(text))
		if err != nil {
			t.Fatalf("%d: %v", number, err)
		}
		var decoded Value
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("%d: %v", number, err)
		}
		if decoded.Text == nil || *decoded.Text != text {
			t.Fatalf("%d round tripped as %#v", number, decoded)
		}
	}
}

func TestStrictTopLevelDecoding(t *testing.T) {
	t.Parallel()

	profileBytes, err := MarshalCanonical(validProfile())
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := bytes.Replace(profileBytes, []byte(`"id":"django-6.1"`), []byte(`"id":"django-6.1","unknown":true`), 1)
	if _, err := DecodeProfile(bytes.NewReader(withUnknown)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
	withTrailing := append(profileBytes, []byte(`{"extra":true}`)...)
	if _, err := DecodeProfile(bytes.NewReader(withTrailing)); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("expected trailing JSON error, got %v", err)
	}
}

func TestExplicitNullOptionalObservationFieldsAreAccepted(t *testing.T) {
	t.Parallel()

	input := `{
		"id":"QRY-001",
		"status":"observed",
		"phase":"evaluation",
		"result":{"type":"null"},
		"error":null,
		"db_state":null,
		"metrics":null
	}`
	var observation Observation
	if err := decodeStrict(strings.NewReader(input), &observation); err != nil {
		t.Fatal(err)
	}
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
	if observation.Error != nil || observation.DBState != nil || observation.Metrics != nil {
		t.Fatalf("explicit null fields must decode as absent: %#v", observation)
	}
}

func TestProfileValidation(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}

	tests := map[string]func(*Profile){
		"format version": func(value *Profile) { value.FormatVersion = 2 },
		"django commit":  func(value *Profile) { value.Fingerprint.DjangoCommit = "abc" },
		"distribution":   func(value *Profile) { value.Fingerprint.DjangoDistributionSHA256 = "abc" },
		"sqlite source":  func(value *Profile) { value.Fingerprint.SQLiteSourceID = "" },
		"use tz missing": func(value *Profile) { value.Fingerprint.UseTZ = nil },
		"lock hash":      func(value *Profile) { value.Lock.SHA256 = "ABC" },
		"lock traversal": func(value *Profile) { value.Lock.File = "../uv.lock" },
		"manager":        func(value *Profile) { value.Lock.Manager = "" },
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			value := profile
			mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestManifestValidation(t *testing.T) {
	t.Parallel()

	manifest := validManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}

	tests := map[string]func(*Manifest){
		"too few contracts": func(value *Manifest) { value.Contracts = value.Contracts[:7] },
		"duplicate id": func(value *Manifest) {
			value.Contracts[1].ID = value.Contracts[0].ID
		},
		"bad scenario": func(value *Manifest) { value.Contracts[0].Scenario = "Query Exact" },
		"bad status":   func(value *Manifest) { value.Contracts[0].Status = "green" },
		"no provenance": func(value *Manifest) {
			value.Contracts[0].Provenance = nil
		},
		"missing derived marker": func(value *Manifest) {
			value.Contracts[0].Provenance[0].Derived = nil
		},
		"derived without license": func(value *Manifest) {
			value.Contracts[0].Provenance[0].Derived = boolPointer(true)
			value.Contracts[0].Provenance[0].License = ""
		},
		"duplicate dimension": func(value *Manifest) {
			value.Contracts[0].Comparison = []ComparisonDimension{CompareResult, CompareResult}
		},
		"unknown dimension": func(value *Manifest) {
			value.Contracts[0].Comparison = []ComparisonDimension{"sql"}
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			value := cloneManifest(t, manifest)
			mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestObservedErrorRequiresComparisonFields(t *testing.T) {
	t.Parallel()

	errorValue := ObservedError{
		Category: "field_error",
		Code:     "unknown_field",
	}
	if err := errorValue.Validate(); err == nil || !strings.Contains(err.Error(), "message_is_contract") {
		t.Fatalf("expected missing marker error, got %v", err)
	}
	errorValue.MessageIsContract = boolPointer(true)
	if err := errorValue.Validate(); err == nil || !strings.Contains(err.Error(), "message is required") {
		t.Fatalf("expected missing message error, got %v", err)
	}
	errorValue.Message = "contract text"
	if err := errorValue.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSuiteMustMatchLockedProfileAndManifestOrder(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	manifest := validManifest()
	suite := validSuite()
	if err := ValidateSuiteAgainst(profile, manifest, suite); err != nil {
		t.Fatal(err)
	}

	t.Run("profile", func(t *testing.T) {
		changed := cloneSuite(t, suite)
		changed.Profile.Fingerprint.SQLiteSourceID += " changed"
		if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil || !strings.Contains(err.Error(), "fingerprint") {
			t.Fatalf("expected fingerprint mismatch, got %v", err)
		}
	})
	t.Run("contract order", func(t *testing.T) {
		changed := cloneSuite(t, suite)
		changed.Contracts[0], changed.Contracts[1] = changed.Contracts[1], changed.Contracts[0]
		if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil || !strings.Contains(err.Error(), "position") {
			t.Fatalf("expected ordering mismatch, got %v", err)
		}
	})
	t.Run("undeclared payload", func(t *testing.T) {
		changed := cloneSuite(t, suite)
		changed.Contracts[2].Metrics = valuePointer(Object(map[string]Value{"query_count": Integer("1")}))
		if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil || !strings.Contains(err.Error(), "not declared") {
			t.Fatalf("expected dimension mismatch, got %v", err)
		}
	})
	t.Run("missing payload", func(t *testing.T) {
		changed := cloneSuite(t, suite)
		changed.Contracts[0].Metrics = nil
		if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil || !strings.Contains(err.Error(), "requires") {
			t.Fatalf("expected missing dimension error, got %v", err)
		}
	})
}

func TestComparatorIdenticalSuitesMatch(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	manifest := validManifest()
	expected := validSuite()
	actual := cloneSuite(t, expected)
	differences, err := Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 0 {
		t.Fatalf("unexpected differences: %#v", differences)
	}
}

func TestComparatorMutationCasesCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	manifest := validManifest()
	expected := validSuite()

	tests := []struct {
		name   string
		path   string
		mutate func(*ObservationSuite)
	}{
		{
			name: "result value",
			path: "result.items[0].value",
			mutate: func(suite *ObservationSuite) {
				suite.Contracts[0].Result.Items[0] = String("changed")
			},
		},
		{
			name: "result order",
			path: "result.items[0].value",
			mutate: func(suite *ObservationSuite) {
				items := suite.Contracts[0].Result.Items
				items[0], items[1] = items[1], items[0]
			},
		},
		{
			name: "phase",
			path: "phase",
			mutate: func(suite *ObservationSuite) {
				suite.Contracts[0].Phase = PhaseConstruction
			},
		},
		{
			name: "error category",
			path: "error.category",
			mutate: func(suite *ObservationSuite) {
				suite.Contracts[1].Error.Category = "value_error"
			},
		},
		{
			name: "error code",
			path: "error.code",
			mutate: func(suite *ObservationSuite) {
				suite.Contracts[1].Error.Code = "unknown_lookup"
			},
		},
		{
			name: "contractual message",
			path: "error.message",
			mutate: func(suite *ObservationSuite) {
				suite.Contracts[1].Error.Message = "changed"
			},
		},
		{
			name: "db state",
			path: "db_state.items[0].value",
			mutate: func(suite *ObservationSuite) {
				suite.Contracts[0].DBState.Items[0] = Integer("99")
			},
		},
		{
			name: "metrics",
			path: "metrics.fields[0].value.value",
			mutate: func(suite *ObservationSuite) {
				suite.Contracts[0].Metrics.Fields[0].Value = Integer("2")
			},
		},
		{
			name: "not implemented",
			path: "status",
			mutate: func(suite *ObservationSuite) {
				observation := &suite.Contracts[0]
				observation.Status = StatusNotImplemented
				observation.Result = nil
				observation.DBState = nil
				observation.Metrics = nil
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, expected)
			test.mutate(&actual)
			differences, err := Compare(profile, manifest, expected, actual)
			if err != nil {
				t.Fatal(err)
			}
			if len(differences) == 0 {
				t.Fatal("mutation produced a false green")
			}
			if !hasDifferencePath(differences, test.path) {
				t.Fatalf("expected path %q, got %#v", test.path, differences)
			}
		})
	}
}

func TestComparatorIgnoresDiagnosticErrorTextAndPythonType(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	manifest := validManifest()
	expected := validSuite()
	expected.Contracts[1].Error.MessageIsContract = boolPointer(false)
	actual := cloneSuite(t, expected)
	actual.Contracts[1].Error.Message = "backend-specific diagnostic"
	actual.Contracts[1].Error.PythonType = "different.Type"

	differences, err := Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 0 {
		t.Fatalf("diagnostic text should not be contractual: %#v", differences)
	}
}

func TestComparatorRejectsNotImplementedExpectedOracle(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	manifest := validManifest()
	expected := validSuite()
	expected.Contracts[0].Status = StatusNotImplemented
	expected.Contracts[0].Result = nil
	expected.Contracts[0].DBState = nil
	expected.Contracts[0].Metrics = nil
	actual := cloneSuite(t, expected)

	if _, err := Compare(profile, manifest, expected, actual); err == nil || !strings.Contains(err.Error(), "must be observed") {
		t.Fatalf("expected invalid oracle error, got %v", err)
	}
}

func hasDifferencePath(differences []Difference, path string) bool {
	for _, item := range differences {
		if item.Path == path {
			return true
		}
	}
	return false
}

func validProfile() Profile {
	return Profile{
		FormatVersion: FormatVersion,
		ID:            "django-6.1",
		Fingerprint: ProfileFingerprint{
			DjangoVersion:            "6.1",
			DjangoCommit:             strings.Repeat("a", 40),
			DjangoDistributionSHA256: strings.Repeat("b", 64),
			PythonImplementation:     "CPython",
			PythonVersion:            "3.14.6",
			SQLiteVersion:            "3.50.4",
			SQLiteSourceID:           "2025-07-30 11:23:50 source-id",
			DatabaseEngine:           "django.db.backends.sqlite3",
			UseTZ:                    boolPointer(true),
			Timezone:                 "UTC",
			LanguageCode:             "en-us",
			Locale:                   "C",
			Platform:                 "darwin",
			Architecture:             "arm64",
		},
		Lock: LockMetadata{
			File:           "uv.lock",
			SHA256:         strings.Repeat("c", 64),
			Manager:        "uv",
			ManagerVersion: "0.10.12",
		},
	}
}

func validManifest() Manifest {
	contracts := make([]Contract, 8)
	for index := range contracts {
		id := fmt.Sprintf("QRY-%03d", index+1)
		contracts[index] = Contract{
			ID:       id,
			Title:    "Contract " + id,
			Scenario: fmt.Sprintf("query_%03d", index+1),
			Status:   ContractOracleLocked,
			Provenance: []Provenance{{
				Kind:      "independent",
				Reference: "https://docs.djangoproject.com/en/6.1/ref/models/querysets/",
				Derived:   boolPointer(false),
			}},
			Comparison: []ComparisonDimension{CompareResult},
		}
	}
	contracts[0].Comparison = []ComparisonDimension{CompareResult, CompareDBState, CompareMetrics}
	contracts[1].Comparison = []ComparisonDimension{CompareError}
	return Manifest{
		FormatVersion: FormatVersion,
		ProfileID:     "django-6.1",
		Contracts:     contracts,
	}
}

func validSuite() ObservationSuite {
	contracts := make([]Observation, 8)
	for index := range contracts {
		contracts[index] = Observation{
			ID:     fmt.Sprintf("QRY-%03d", index+1),
			Status: StatusObserved,
			Phase:  PhaseEvaluation,
			Result: valuePointer(List(String(fmt.Sprintf("row-%d", index+1)))),
		}
	}
	contracts[0].Result = valuePointer(List(String("alpha"), String("bravo")))
	contracts[0].DBState = valuePointer(List(Integer("1"), Integer("2")))
	contracts[0].Metrics = valuePointer(Object(map[string]Value{"query_count": Integer("1")}))
	contracts[1].Result = nil
	contracts[1].Phase = PhaseConstruction
	contracts[1].Error = &ObservedError{
		Category:          "field_error",
		Code:              "unknown_field",
		PythonType:        "django.core.exceptions.FieldError",
		Message:           "Cannot resolve keyword",
		MessageIsContract: boolPointer(true),
	}
	return ObservationSuite{
		FormatVersion: FormatVersion,
		Profile:       validProfile().Snapshot(),
		Contracts:     contracts,
	}
}

func valuePointer(value Value) *Value {
	return &value
}

func boolPointer(value bool) *bool {
	return &value
}

func cloneSuite(t *testing.T, suite ObservationSuite) ObservationSuite {
	t.Helper()
	encoded, err := MarshalCanonical(suite)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeObservationSuite(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func cloneManifest(t *testing.T, manifest Manifest) Manifest {
	t.Helper()
	encoded, err := MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeManifest(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
