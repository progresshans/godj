package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0045DeviationPolicyDispatchPreservesExactOrderedSelectors(t *testing.T) {
	t.Parallel()

	policy, err := deviationPolicyForDecision("DEV-0008")
	if err != nil {
		t.Fatal(err)
	}
	want := []protocol.DeviationChangePolicy{
		{Dimension: protocol.DeviationResult, Path: "pre_restart.accepted", Operation: protocol.DeviationReplace},
		{Dimension: protocol.DeviationResult, Path: "pre_restart.status", Operation: protocol.DeviationReplace},
		{Dimension: protocol.DeviationDBState, Path: "pre_restart.article_delta", Operation: protocol.DeviationReplace},
		{Dimension: protocol.DeviationMetrics, Path: "pre_restart_mutations", Operation: protocol.DeviationReplace},
	}
	if policy.Decision != "DEV-0008" || len(policy.Contracts) != 1 || policy.Contracts[0].ID != "SYS-009" ||
		len(policy.Contracts[0].Changes) != len(want) {
		t.Fatalf("DEV-0008 policy = %#v", policy)
	}
	for index, selector := range want {
		if got := policy.Contracts[0].Changes[index]; got != selector {
			t.Fatalf("DEV-0008 selector %d = %#v, want %#v", index, got, selector)
		}
	}
}

func TestGDJ0045DeviationFixtureMatchesClosedPolicySelectors(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	expectation, err := protocol.LoadDeviationExpectation(filepath.Join(
		root,
		"conformance",
		"fixtures",
		"godj-system-state-deviation-expected.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	policy := systemStateDeviationPolicy()
	if expectation.Decision != policy.Decision || len(expectation.Contracts) != 1 ||
		expectation.Contracts[0].ID != policy.Contracts[0].ID ||
		len(expectation.Contracts[0].Changes) != len(policy.Contracts[0].Changes) {
		t.Fatalf("DEV-0008 fixture/policy shape = %#v / %#v", expectation, policy)
	}
	for index, change := range expectation.Contracts[0].Changes {
		selector := policy.Contracts[0].Changes[index]
		if change.Dimension != selector.Dimension || change.Path != selector.Path || change.Operation != selector.Operation {
			t.Fatalf("DEV-0008 fixture selector %d = %#v, want %#v", index, change, selector)
		}
	}
}

func TestRunGDJ0045MixedProductExpectationWritesActualOutput(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	actualPath := filepath.Join(t.TempDir(), "system-state-actual.json")
	arguments := gdj0045RunArguments(
		root,
		filepath.Join(root, "conformance", "fixtures", "godj-system-state-deviation-expected.json"),
		actualPath,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	want := "GoDj product observations match 12 required contracts; 8 remain not implemented"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatalf("load actual output: %v", err)
	}
	if len(actual.Contracts) != 20 || actual.Contracts[0].ID != "SYS-001" || actual.Contracts[19].ID != "SYS-020" {
		t.Fatalf("actual contracts = %#v, want 20 ordered from SYS-001 through SYS-020", actual.Contracts)
	}
	for index, observation := range actual.Contracts {
		if index < 12 {
			if observation.Status != protocol.StatusObserved {
				t.Fatalf("%s actual status = %q, want observed", observation.ID, observation.Status)
			}
			continue
		}
		if observation.Status != protocol.StatusNotImplemented || observation.Result != nil || observation.Error != nil || observation.DBState != nil || observation.Metrics != nil {
			t.Fatalf("%s actual = %#v, want payload-free not_implemented", observation.ID, observation)
		}
	}
	legacyActual := actual
	legacyActual.Contracts = append([]protocol.Observation(nil), actual.Contracts[:12]...)
	legacyBytes, err := protocol.MarshalCanonical(legacyActual)
	if err != nil {
		t.Fatal(err)
	}
	const legacySHA256 = "f30ac1a42b43b037067865b37a902bc2f07de187c0bf512712bc9c058d41c3a6"
	if len(legacyBytes) != 12944 || fmt.Sprintf("%x", sha256.Sum256(legacyBytes)) != legacySHA256 {
		t.Fatalf("legacy SYS-001..012 actual bytes drifted: size=%d sha256=%x", len(legacyBytes), sha256.Sum256(legacyBytes))
	}
}

func TestRunGDJ0045RequiresDeviationExpectationBeforeActualHandlers(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	directory := t.TempDir()
	actualPath := filepath.Join(directory, "must-not-exist.json")
	arguments := gdj0045RunArguments(root, "", actualPath)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertGDJ0045PreHandlerFailure(
		t,
		ctx,
		arguments,
		actualPath,
		"manifest contains deviation contracts but -deviation-expected is missing",
	)
}

func TestRunGDJ0045DeviationSelectorEscapesFailClosedBeforeActualHandlers(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	fixturePath := filepath.Join(root, "conformance", "fixtures", "godj-system-state-deviation-expected.json")
	tests := []struct {
		name      string
		mutate    func(*protocol.DeviationExpectation)
		wantError string
	}{
		{
			name: "missing selector",
			mutate: func(expectation *protocol.DeviationExpectation) {
				expectation.Contracts[0].Changes = expectation.Contracts[0].Changes[:3]
			},
			wantError: "SYS-009: deviation expectation contains 3 changes; policy requires 4",
		},
		{
			name: "extra selector",
			mutate: func(expectation *protocol.DeviationExpectation) {
				extra := expectation.Contracts[0].Changes[0]
				extra.Path = "unexpected.accepted"
				expectation.Contracts[0].Changes = append(expectation.Contracts[0].Changes, extra)
			},
			wantError: "SYS-009: deviation expectation contains 5 changes; policy requires 4",
		},
		{
			name: "fresh lane selector escape",
			mutate: func(expectation *protocol.DeviationExpectation) {
				expectation.Contracts[0].Changes[0].Path = "fresh.accepted"
			},
			wantError: fmt.Sprintf(
				"SYS-009: deviation change 0 selector (%q, %q, %q) does not match policy (%q, %q, %q)",
				protocol.DeviationResult,
				"fresh.accepted",
				protocol.DeviationReplace,
				protocol.DeviationResult,
				"pre_restart.accepted",
				protocol.DeviationReplace,
			),
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

			// A cancelled context makes reaching Generate or a scenario handler
			// observable. The deviation gate must reject the input first.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			assertGDJ0045PreHandlerFailure(
				t,
				ctx,
				gdj0045RunArguments(root, expectationPath, actualPath),
				actualPath,
				test.wantError,
			)
		})
	}
}

func gdj0045RunArguments(root, deviation, actual string) []string {
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "system-state-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "system-state.json"),
	}
	if deviation != "" {
		arguments = append(arguments, "-deviation-expected", deviation)
	}
	return append(arguments, "-actual-output", actual)
}

func assertGDJ0045PreHandlerFailure(
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
