package main

import (
	"bytes"
	"context"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
	operatorattestation "github.com/progresshans/godj/conformance/projectoperatorproduct/attestation"
	godjrunner "github.com/progresshans/godj/conformance/runners/godj"
	systemstateattestation "github.com/progresshans/godj/conformance/systemstate/attestation"
)

func TestLoadRunnerInputsKeepsSYS020AndSYS029Independent(t *testing.T) {
	t.Parallel()

	manifestPath, systemStatePath, operatorPath := gdj0055CheckedAttestationPaths()
	systemObserved := gdj0055SystemStateObservedFacts()
	postgresObserved := gdj0055OperatorObservedFacts(operatorattestation.BackendPostgreSQL)
	sqliteObserved := gdj0055OperatorObservedFacts(operatorattestation.BackendSQLite)

	tests := []struct {
		name            string
		systemStatus    protocol.ContractStatus
		operatorStatus  protocol.ContractStatus
		systemPath      string
		operatorPath    string
		wantSystem      bool
		wantOperator    bool
		wantSystemLoads int
		wantOpLoads     int
	}{
		{
			name:           "neither published",
			systemStatus:   protocol.ContractOracleLocked,
			operatorStatus: protocol.ContractOracleLocked,
		},
		{
			name:            "SYS-020 only",
			systemStatus:    protocol.ContractPassing,
			operatorStatus:  protocol.ContractOracleLocked,
			systemPath:      systemStatePath,
			wantSystem:      true,
			wantSystemLoads: 1,
		},
		{
			name:           "SYS-029 only",
			systemStatus:   protocol.ContractOracleLocked,
			operatorStatus: protocol.ContractPassing,
			operatorPath:   operatorPath,
			wantOperator:   true,
			wantOpLoads:    1,
		},
		{
			name:            "both published",
			systemStatus:    protocol.ContractDeviation,
			operatorStatus:  protocol.ContractPassing,
			systemPath:      systemStatePath,
			operatorPath:    operatorPath,
			wantSystem:      true,
			wantOperator:    true,
			wantSystemLoads: 1,
			wantOpLoads:     1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			systemLoads := 0
			operatorLoads := 0
			inputs, err := loadRunnerInputsWithEvidenceLoader(
				gdj0055AttestationManifest(test.systemStatus, test.operatorStatus),
				manifestPath,
				test.systemPath,
				test.operatorPath,
				runnerInputEvidenceLoader{
					loadSystemState: func(repositoryRoot, path string) (systemstateattestation.ObservedFacts, error) {
						systemLoads++
						return systemObserved, nil
					},
					loadProjectOperator: func(repositoryRoot, path string) (operatorattestation.ObservedFacts, operatorattestation.ObservedFacts, error) {
						operatorLoads++
						return postgresObserved, sqliteObserved, nil
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := inputs.SystemStatePostgreSQLTwoProcess != nil; got != test.wantSystem {
				t.Fatalf("SYS-020 input present=%v, want %v", got, test.wantSystem)
			}
			if got := inputs.ProjectOperatorPostgreSQL != nil && inputs.ProjectOperatorSQLite != nil; got != test.wantOperator {
				t.Fatalf("SYS-029 inputs present=%v, want %v: %#v", got, test.wantOperator, inputs)
			}
			if systemLoads != test.wantSystemLoads || operatorLoads != test.wantOpLoads {
				t.Fatalf("loader calls=(SYS-020:%d SYS-029:%d), want (%d,%d)", systemLoads, operatorLoads, test.wantSystemLoads, test.wantOpLoads)
			}
		})
	}
}

func TestLoadRunnerInputsRejectsSYS029ManifestAndEvidenceEscapes(t *testing.T) {
	t.Parallel()

	manifestPath, systemStatePath, operatorPath := gdj0055CheckedAttestationPaths()
	passing := gdj0055AttestationManifest(protocol.ContractOracleLocked, protocol.ContractPassing)
	locked := gdj0055AttestationManifest(protocol.ContractOracleLocked, protocol.ContractOracleLocked)
	loader := runnerInputEvidenceLoader{
		loadProjectOperator: func(repositoryRoot, path string) (operatorattestation.ObservedFacts, operatorattestation.ObservedFacts, error) {
			return gdj0055OperatorObservedFacts(operatorattestation.BackendPostgreSQL), gdj0055OperatorObservedFacts(operatorattestation.BackendSQLite), nil
		},
	}

	if _, err := loadRunnerInputsWithEvidenceLoader(passing, manifestPath, "", "", loader); err == nil || !strings.Contains(err.Error(), "SYS-029 requires") {
		t.Fatalf("missing SYS-029 evidence error=%v", err)
	}
	if _, err := loadRunnerInputsWithEvidenceLoader(locked, manifestPath, "", operatorPath, loader); err == nil || !strings.Contains(err.Error(), "not used") {
		t.Fatalf("unused SYS-029 evidence error=%v", err)
	}
	if _, err := loadRunnerInputsWithEvidenceLoader(passing, manifestPath, "", systemStatePath, loader); err == nil || !strings.Contains(err.Error(), "checked current repository path") {
		t.Fatalf("cross-artifact SYS-029 evidence error=%v", err)
	}

	wrongID := passing
	wrongID.Contracts = append([]protocol.Contract(nil), passing.Contracts...)
	wrongID.Contracts[1].ID = "SYS-999"
	if _, err := loadRunnerInputsWithEvidenceLoader(wrongID, manifestPath, "", operatorPath, loader); err == nil || !strings.Contains(err.Error(), "binding is inconsistent") {
		t.Fatalf("wrong SYS-029 contract error=%v", err)
	}
	wrongScenario := passing
	wrongScenario.Contracts = append([]protocol.Contract(nil), passing.Contracts...)
	wrongScenario.Contracts[1].Scenario = "godj.system_state.not_sys029"
	if _, err := loadRunnerInputsWithEvidenceLoader(wrongScenario, manifestPath, "", operatorPath, loader); err == nil || !strings.Contains(err.Error(), "binding is inconsistent") {
		t.Fatalf("wrong SYS-029 scenario error=%v", err)
	}
	duplicate := passing
	duplicate.Contracts = append(append([]protocol.Contract(nil), passing.Contracts...), passing.Contracts[1])
	if _, err := loadRunnerInputsWithEvidenceLoader(duplicate, manifestPath, "", operatorPath, loader); err == nil || !strings.Contains(err.Error(), "appears more than once") {
		t.Fatalf("duplicate SYS-029 binding error=%v", err)
	}

	loadError := errors.New("checksum/source validation sentinel")
	loader.loadProjectOperator = func(repositoryRoot, path string) (operatorattestation.ObservedFacts, operatorattestation.ObservedFacts, error) {
		return operatorattestation.ObservedFacts{}, operatorattestation.ObservedFacts{}, loadError
	}
	if _, err := loadRunnerInputsWithEvidenceLoader(passing, manifestPath, "", operatorPath, loader); !errors.Is(err, loadError) {
		t.Fatalf("SYS-029 loader error=%v, want wrapped sentinel", err)
	}
	loader.loadProjectOperator = func(repositoryRoot, path string) (operatorattestation.ObservedFacts, operatorattestation.ObservedFacts, error) {
		return gdj0055OperatorObservedFacts(operatorattestation.BackendPostgreSQL), operatorattestation.ObservedFacts{}, nil
	}
	if _, err := loadRunnerInputsWithEvidenceLoader(passing, manifestPath, "", operatorPath, loader); err == nil || !strings.Contains(err.Error(), "SQLite evidence") {
		t.Fatalf("missing SQLite backend error=%v", err)
	}
	loader.loadProjectOperator = func(repositoryRoot, path string) (operatorattestation.ObservedFacts, operatorattestation.ObservedFacts, error) {
		return gdj0055OperatorObservedFacts(operatorattestation.BackendSQLite), gdj0055OperatorObservedFacts(operatorattestation.BackendPostgreSQL), nil
	}
	if _, err := loadRunnerInputsWithEvidenceLoader(passing, manifestPath, "", operatorPath, loader); err == nil || !strings.Contains(err.Error(), "PostgreSQL evidence") || !strings.Contains(err.Error(), "backend identity") {
		t.Fatalf("swapped backend identity error=%v", err)
	}
}

func TestProjectOperatorRunnerFactsPreservesEveryFactAndFailsClosed(t *testing.T) {
	t.Parallel()

	observed := gdj0055OperatorObservedFacts(operatorattestation.BackendPostgreSQL)
	want := godjrunner.GDJ0055OperatorBackendFacts{
		Backend:                             operatorattestation.BackendPostgreSQL,
		ProvisionProcesses:                  1,
		RuntimeProcesses:                    2,
		DistinctProcesses:                   3,
		ProvisionCalls:                      4,
		CredentialRows:                      5,
		Provisioned:                         false,
		AdminAuthenticated:                  true,
		APIAuthenticated:                    false,
		DistinctProcessRestart:              true,
		ProvisionProcessDistinctFromRuntime: false,
		RestartRawSecretInput:               true,
		RestartStateLoss:                    6,
		SchemaDrift:                         true,
		RawSecretOccurrences:                7,
	}
	got, err := projectOperatorRunnerFacts(observed, operatorattestation.BackendPostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("converted SYS-029 facts=%#v, want %#v", got, want)
	}

	wrongBackend := observed
	wrongBackend.Backend = operatorattestation.BackendSQLite
	if _, err := projectOperatorRunnerFacts(wrongBackend, operatorattestation.BackendPostgreSQL); err == nil || !strings.Contains(err.Error(), "backend identity") {
		t.Fatalf("wrong backend identity error=%v", err)
	}
	missingBackend := observed
	missingBackend.Backend = ""
	if _, err := projectOperatorRunnerFacts(missingBackend, operatorattestation.BackendPostgreSQL); err == nil {
		t.Fatal("missing backend identity was accepted")
	}
	overflow := observed
	overflow.CredentialRows = int64(math.MaxInt32) + 1
	if _, err := projectOperatorRunnerFacts(overflow, operatorattestation.BackendPostgreSQL); err == nil {
		t.Fatal("non-portable runner integer was accepted")
	}
	if _, err := checkedRunnerFactInt(int64(math.MaxInt32)+1, "overflow sentinel"); err == nil || !strings.Contains(err.Error(), "portable runner integer range") {
		t.Fatalf("portable integer overflow error=%v", err)
	}
	if _, err := checkedRunnerFactInt(math.MaxInt64, "overflow sentinel"); err == nil {
		t.Fatal("int64 maximum was accepted as a portable runner integer")
	}
}

func TestProjectOperatorAttestationRequiresExactCheckedPath(t *testing.T) {
	t.Parallel()

	manifestPath, systemStatePath, operatorPath := gdj0055CheckedAttestationPaths()
	root, err := attestationRepositoryRoot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireProjectOperatorAttestationPath(root, operatorPath); err != nil {
		t.Fatal(err)
	}
	if err := requireProjectOperatorAttestationPath(root, systemStatePath); err == nil || !strings.Contains(err.Error(), "checked current repository path") {
		t.Fatalf("cross-artifact exact-path error=%v", err)
	}
	wrong := filepath.Join(root, "conformance", "projectoperatorproduct", operatorattestation.FileName)
	if err := requireProjectOperatorAttestationPath(root, wrong); err == nil || !strings.Contains(err.Error(), "checked current repository path") {
		t.Fatalf("wrong SYS-029 exact-path error=%v", err)
	}
}

func TestRunRejectsCrossArtifactSYS029EvidenceWithExitTwo(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	arguments := gdj0045RunArguments(
		root,
		filepath.Join(root, "conformance", "fixtures", "godj-system-state-deviation-expected.json"),
		filepath.Join(t.TempDir(), "must-not-exist.json"),
	)
	_, systemStatePath, _ := gdj0055CheckedAttestationPaths()
	arguments = append(arguments, "-project-operator-postgres-attestation", systemStatePath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code=%d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "external project operator attestation must use the checked current repository path") {
		t.Fatalf("stdout=%q stderr=%q, want exact SYS-029 path failure", stdout.String(), stderr.String())
	}
}

func TestRunRequiresPublishedSYS029EvidenceWithExitTwo(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	arguments := gdj0045RunArguments(
		root,
		filepath.Join(root, "conformance", "fixtures", "godj-system-state-deviation-expected.json"),
		filepath.Join(t.TempDir(), "must-not-exist.json"),
	)
	arguments = removeFlagPair(arguments, "-project-operator-postgres-attestation")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code=%d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "published SYS-029 requires -project-operator-postgres-attestation") {
		t.Fatalf("stdout=%q stderr=%q, want missing SYS-029 evidence failure", stdout.String(), stderr.String())
	}
}

func removeFlagPair(arguments []string, name string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		if arguments[index] == name && index+1 < len(arguments) {
			index++
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

func gdj0055CheckedAttestationPaths() (manifest, systemState, operator string) {
	root := filepath.Join("..", "..", "..")
	return filepath.Join(root, "conformance", "contracts", "system-state-manifest.json"),
		filepath.Join(root, "conformance", "systemstate", "attestations", systemstateattestation.FileName),
		filepath.Join(root, "conformance", "projectoperatorproduct", "attestations", operatorattestation.FileName)
}

func gdj0055AttestationManifest(systemStatus, operatorStatus protocol.ContractStatus) protocol.Manifest {
	return protocol.Manifest{Contracts: []protocol.Contract{
		{ID: systemstateattestation.Contract, Scenario: systemstateattestation.Scenario, Status: systemStatus},
		{ID: operatorattestation.Contract, Scenario: operatorattestation.Scenario, Status: operatorStatus},
	}}
}

func gdj0055SystemStateObservedFacts() systemstateattestation.ObservedFacts {
	return systemstateattestation.ObservedFacts{
		WriterProcesses: 2, SameSchema: false, BarrierLinearized: false,
		RestartPreserved: false, DivergenceCount: 3, LossCount: 4,
		DriftCount: 5, SecretOccurrences: 6,
	}
}

func gdj0055OperatorObservedFacts(backend string) operatorattestation.ObservedFacts {
	return operatorattestation.ObservedFacts{
		Backend: backend, ProvisionProcesses: 1, RuntimeProcesses: 2,
		DistinctProcesses: 3, ProvisionCalls: 4, CredentialRows: 5,
		Provisioned: false, AdminAuthenticated: true, APIAuthenticated: false,
		DistinctProcessRestart: true, ProvisionProcessDistinctFromRuntime: false,
		RestartRawSecretInput: true, RestartStateLoss: 6, SchemaDrift: true,
		RawSecretOccurrences: 7,
	}
}
