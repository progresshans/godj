package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0044DeviationPolicyDispatchPreservesExactOrderedSelectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		decision  string
		contracts []gdj0044ReviewedContract
	}{
		{
			decision: "DEV-0006",
			contracts: []gdj0044ReviewedContract{
				{id: "WEB-028", paths: []string{"parameter.pk_type"}},
				{id: "WEB-029", paths: []string{
					"invalid[0].matched",
					"invalid[1].matched",
					"invalid[2].matched",
					"invalid[3].matched",
					"valid[0].type",
					"valid[1].type",
					"valid[2].type",
				}},
			},
		},
		{
			decision: "DEV-0007",
			contracts: []gdj0044ReviewedContract{
				{id: "API-001", paths: []string{
					"[10].response.error_codes.detail",
					"[10].response.status",
				}},
				{id: "API-003", paths: []string{
					"unsafe_attempts[0].response.error_codes.detail",
					"unsafe_attempts[1].response.error_codes.detail",
					"unsafe_attempts[2].response.error_codes.detail",
				}},
				{id: "API-010", paths: []string{"missing_csrf.error_codes.detail"}},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.decision, func(t *testing.T) {
			t.Parallel()

			policy, err := deviationPolicyForDecision(test.decision)
			if err != nil {
				t.Fatal(err)
			}
			if policy.Decision != test.decision || len(policy.Contracts) != len(test.contracts) {
				t.Fatalf("policy = %#v, want %s/%d contracts", policy, test.decision, len(test.contracts))
			}
			for contractIndex, wantContract := range test.contracts {
				gotContract := policy.Contracts[contractIndex]
				if gotContract.ID != wantContract.id || len(gotContract.Changes) != len(wantContract.paths) {
					t.Fatalf("contract %d = %#v, want %s/%#v", contractIndex, gotContract, wantContract.id, wantContract.paths)
				}
				for changeIndex, wantPath := range wantContract.paths {
					gotChange := gotContract.Changes[changeIndex]
					if gotChange.Dimension != protocol.DeviationResult || gotChange.Path != wantPath || gotChange.Operation != protocol.DeviationReplace {
						t.Fatalf("%s change %d = %#v, want result/%s/replace", wantContract.id, changeIndex, gotChange, wantPath)
					}
				}
			}
		})
	}
}

type gdj0044ReviewedContract struct {
	id    string
	paths []string
}

func TestRunGDJ0044ReviewedProductExpectationsAndWritesActualOutput(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	tests := []struct {
		name     string
		manifest string
		oracle   string
		fixture  string
		decision string
		count    int
		firstID  string
		lastID   string
	}{
		{
			name:     "parameter-routing",
			manifest: "parameter-routing-manifest.json",
			oracle:   "parameter-routing-oracle.json",
			fixture:  "godj-parameter-routing-deviation-expected.json",
			decision: "DEV-0006",
			count:    8,
			firstID:  "WEB-028",
			lastID:   "WEB-035",
		},
		{
			name:     "article-api",
			manifest: "article-api-manifest.json",
			oracle:   "article-api-oracle.json",
			fixture:  "godj-article-api-deviation-expected.json",
			decision: "DEV-0007",
			count:    10,
			firstID:  "API-001",
			lastID:   "API-010",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actualPath := filepath.Join(t.TempDir(), test.name+"-actual.json")
			arguments := gdj0044RunArguments(root, test.manifest, test.oracle, filepath.Join(root, "conformance", "fixtures", test.fixture), actualPath)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
				t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			want := fmt.Sprintf("match the reviewed product expectation for %d contracts under %s", test.count, test.decision)
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
			if len(actual.Contracts) != test.count || actual.Contracts[0].ID != test.firstID || actual.Contracts[test.count-1].ID != test.lastID {
				t.Fatalf("actual contracts = %#v, want %d ordered from %s through %s", actual.Contracts, test.count, test.firstID, test.lastID)
			}
			for _, observation := range actual.Contracts {
				if observation.Status != protocol.StatusObserved {
					t.Fatalf("%s actual status = %q, want observed", observation.ID, observation.Status)
				}
			}
		})
	}
}

func TestRunGDJ0044DeviationExpectationsFailClosedBeforeActualOutput(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	tests := []struct {
		name      string
		manifest  string
		oracle    string
		fixture   string
		mutate    func(*protocol.DeviationExpectation)
		wantError string
	}{
		{
			name:     "unknown decision",
			manifest: "parameter-routing-manifest.json",
			oracle:   "parameter-routing-oracle.json",
			fixture:  "godj-parameter-routing-deviation-expected.json",
			mutate: func(expectation *protocol.DeviationExpectation) {
				expectation.Decision = "DEV-9999"
			},
			wantError: `unsupported deviation decision "DEV-9999"`,
		},
		{
			name:     "cross decision",
			manifest: "parameter-routing-manifest.json",
			oracle:   "parameter-routing-oracle.json",
			fixture:  "godj-parameter-routing-deviation-expected.json",
			mutate: func(expectation *protocol.DeviationExpectation) {
				expectation.Decision = "DEV-0007"
			},
			wantError: "policy requires 3",
		},
		{
			name:     "missing selector",
			manifest: "parameter-routing-manifest.json",
			oracle:   "parameter-routing-oracle.json",
			fixture:  "godj-parameter-routing-deviation-expected.json",
			mutate: func(expectation *protocol.DeviationExpectation) {
				changes := expectation.Contracts[1].Changes
				expectation.Contracts[1].Changes = changes[:len(changes)-1]
			},
			wantError: "policy requires 7",
		},
		{
			name:     "extra selector",
			manifest: "article-api-manifest.json",
			oracle:   "article-api-oracle.json",
			fixture:  "godj-article-api-deviation-expected.json",
			mutate: func(expectation *protocol.DeviationExpectation) {
				expectation.Contracts[0].Changes = append(expectation.Contracts[0].Changes, protocol.DeviationChange{
					Dimension: protocol.DeviationResult,
					Path:      "[10].response.body_empty",
					Operation: protocol.DeviationReplace,
					Reference: protocol.Boolean(false),
					Product:   protocol.Boolean(true),
				})
			},
			wantError: "policy requires 2",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			expectation, err := protocol.LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", test.fixture))
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&expectation)
			directory := t.TempDir()
			expectationPath := writeCanonicalMainTestArtifact(t, directory, "invalid-deviation.json", expectation)
			actualPath := filepath.Join(directory, "must-not-exist.json")
			arguments := gdj0044RunArguments(root, test.manifest, test.oracle, expectationPath, actualPath)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
				t.Fatalf("run() code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf("stdout=%q stderr=%q, want error containing %q", stdout.String(), stderr.String(), test.wantError)
			}
			if _, err := os.Stat(actualPath); !os.IsNotExist(err) {
				t.Fatalf("actual output Stat() error = %v, want not-exist", err)
			}
		})
	}
}

func gdj0044RunArguments(root, manifest, oracle, deviation, actual string) []string {
	return []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", manifest),
		"-expected", filepath.Join(root, "conformance", "oracles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64", oracle),
		"-deviation-expected", deviation,
		"-actual-output", actual,
	}
}
