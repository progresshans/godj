package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0047DeviationPolicyDispatchPreservesExactOrderedSelectors(t *testing.T) {
	t.Parallel()

	policy, err := deviationPolicyForDecision("DEV-0009")
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.DeviationPolicy{
		Decision: "DEV-0009",
		Contracts: []protocol.DeviationContractPolicy{
			{
				ID: "AUT-012",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "[0].response.error_codes.detail", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "[0].response.www_authenticate", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "[1].response.error_codes.detail", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "[1].response.www_authenticate", Operation: protocol.DeviationReplace},
				},
			},
			{
				ID: "AUT-013",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "www_authenticate", Operation: protocol.DeviationReplace},
				},
			},
			{
				ID: "AUT-015",
				Changes: []protocol.DeviationChangePolicy{
					{Dimension: protocol.DeviationResult, Path: "[1].response.error_codes.detail", Operation: protocol.DeviationReplace},
					{Dimension: protocol.DeviationResult, Path: "[1].response.www_authenticate", Operation: protocol.DeviationReplace},
				},
			},
		},
	}
	if !reflect.DeepEqual(policy, want) {
		t.Fatalf("DEV-0009 policy = %#v, want %#v", policy, want)
	}
}

func TestGDJ0047DeviationFixtureMatchesClosedPolicyAndExactBytes(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	path := filepath.Join(root, "conformance", "fixtures", "godj-api-authentication-deviation-expected.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const wantSHA256 = "85a9a8b2261e7265b00a33c2cf5b63b9e5b5cd963b2ac7e894dd77988206fc4b"
	if got := fmt.Sprintf("%x", sha256.Sum256(contents)); len(contents) != 2291 || got != wantSHA256 {
		t.Fatalf("DEV-0009 fixture bytes = %d/%s, want 2291/%s", len(contents), got, wantSHA256)
	}
	if strings.Contains(string(contents), `"dimension": "db_state"`) || strings.Contains(strings.ToLower(string(contents)), "token_table") {
		t.Fatal("DEV-0009 fixture escaped into an unapproved token-table or db_state selector")
	}

	expectation, err := protocol.LoadDeviationExpectation(path)
	if err != nil {
		t.Fatal(err)
	}
	policy := apiAuthenticationDeviationPolicy()
	if expectation.Decision != policy.Decision || len(expectation.Contracts) != len(policy.Contracts) {
		t.Fatalf("DEV-0009 fixture/policy shape = %#v / %#v", expectation, policy)
	}
	for contractIndex, contract := range expectation.Contracts {
		closed := policy.Contracts[contractIndex]
		if contract.ID != closed.ID || len(contract.Changes) != len(closed.Changes) {
			t.Fatalf("DEV-0009 contract %d shape = %#v, want %#v", contractIndex, contract, closed)
		}
		for changeIndex, change := range contract.Changes {
			selector := closed.Changes[changeIndex]
			if change.Dimension != selector.Dimension || change.Path != selector.Path || change.Operation != selector.Operation {
				t.Fatalf("DEV-0009 %s selector %d = %#v, want %#v", contract.ID, changeIndex, change, selector)
			}
		}
	}

	wantValues := [][2]protocol.Value{
		{protocol.String("authentication_failed"), protocol.String("not_authenticated")},
		{protocol.String("Bearer"), protocol.String(`Bearer error="invalid_token"`)},
		{protocol.String("authentication_failed"), protocol.String("not_authenticated")},
		{protocol.String("Bearer"), protocol.String(`Bearer error="invalid_token"`)},
		{protocol.Null(), protocol.String(`Bearer error="insufficient_scope"`)},
		{protocol.String("authentication_failed"), protocol.String("not_authenticated")},
		{protocol.String("Bearer"), protocol.String(`Bearer error="invalid_token"`)},
	}
	var valueIndex int
	for _, contract := range expectation.Contracts {
		for _, change := range contract.Changes {
			want := wantValues[valueIndex]
			if !reflect.DeepEqual(change.Reference, want[0]) || !reflect.DeepEqual(change.Product, want[1]) {
				t.Fatalf("DEV-0009 replacement %d = %#v -> %#v, want %#v -> %#v", valueIndex, change.Reference, change.Product, want[0], want[1])
			}
			valueIndex++
		}
	}
	if valueIndex != len(wantValues) {
		t.Fatalf("DEV-0009 replacements = %d, want %d", valueIndex, len(wantValues))
	}
}

func TestRunGDJ0047MixedProductExpectationWritesTenSecretFreeActuals(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	actualPath := filepath.Join(t.TempDir(), "api-authentication-actual.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), gdj0047RunArguments(root,
		filepath.Join(root, "conformance", "fixtures", "godj-api-authentication-deviation-expected.json"),
		actualPath,
	), &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if want := "GoDj observations match the reviewed product expectation for 10 contracts under DEV-0009"; !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	contents, err := os.ReadFile(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "gdj0047.") || strings.Contains(string(contents), "access_token=") {
		t.Fatal("GDJ-0047 actual artifact contains raw credential-shaped material")
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != 10 || actual.Contracts[0].ID != "AUT-009" || actual.Contracts[9].ID != "API-012" {
		t.Fatalf("actual contracts = %#v, want 10 ordered AUT-009 through API-012", actual.Contracts)
	}
	for _, observation := range actual.Contracts {
		if observation.Status != protocol.StatusObserved {
			t.Fatalf("%s actual status = %q, want observed", observation.ID, observation.Status)
		}
	}
}

func TestRunGDJ0047RequiresExactDeviationBeforeActualHandlers(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	directory := t.TempDir()
	actualPath := filepath.Join(directory, "must-not-exist.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertGDJ0047PreHandlerFailure(
		t,
		ctx,
		gdj0047RunArguments(root, "", actualPath),
		actualPath,
		"manifest contains deviation contracts but -deviation-expected is missing",
	)
}

func TestRunGDJ0047RejectsTokenTableAndDBStateSelectorEscapesBeforeActualHandlers(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	fixturePath := filepath.Join(root, "conformance", "fixtures", "godj-api-authentication-deviation-expected.json")
	tests := []struct {
		name      string
		mutate    func(*protocol.DeviationExpectation)
		wantError string
	}{
		{
			name: "db state dimension",
			mutate: func(expectation *protocol.DeviationExpectation) {
				expectation.Contracts[0].Changes[0].Dimension = protocol.DeviationDBState
			},
			wantError: `AUT-012: deviation change 0 selector ("db_state", "[0].response.error_codes.detail", "replace") does not match policy ("result", "[0].response.error_codes.detail", "replace")`,
		},
		{
			name: "token table path",
			mutate: func(expectation *protocol.DeviationExpectation) {
				expectation.Contracts[0].Changes[0].Path = "token_table[0].digest"
			},
			wantError: `AUT-012: deviation change 0 selector ("result", "token_table[0].digest", "replace") does not match policy ("result", "[0].response.error_codes.detail", "replace")`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			expectation, err := protocol.LoadDeviationExpectation(fixturePath)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&expectation)
			directory := t.TempDir()
			expectationPath := writeCanonicalMainTestArtifact(t, directory, "invalid-deviation.json", expectation)
			actualPath := filepath.Join(directory, "must-not-exist.json")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			assertGDJ0047PreHandlerFailure(
				t,
				ctx,
				gdj0047RunArguments(root, expectationPath, actualPath),
				actualPath,
				test.wantError,
			)
		})
	}
}

func gdj0047RunArguments(root, deviation, actual string) []string {
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "api-authentication-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64", "api-authentication-oracle.json"),
	}
	if deviation != "" {
		arguments = append(arguments, "-deviation-expected", deviation)
	}
	return append(arguments, "-actual-output", actual)
}

func assertGDJ0047PreHandlerFailure(
	t *testing.T,
	ctx context.Context,
	arguments []string,
	actualPath string,
	wantError string,
) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(ctx, arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), wantError) {
		t.Fatalf("stdout=%q stderr=%q, want error containing %q", stdout.String(), stderr.String(), wantError)
	}
	if strings.Contains(stderr.String(), context.Canceled.Error()) {
		t.Fatalf("stderr=%q reached actual generation instead of failing at the deviation gate", stderr.String())
	}
	if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
		t.Fatalf("actual output Stat() error = %v, want not-exist", err)
	}
}
