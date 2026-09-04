// Command godjcheck executes a GoDj observation adapter and compares its
// results with the selected locked reference oracle.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/progresshans/godj/conformance/internal/protocol"
	operatorattestation "github.com/progresshans/godj/conformance/projectoperatorproduct/attestation"
	godjrunner "github.com/progresshans/godj/conformance/runners/godj"
	systemstateattestation "github.com/progresshans/godj/conformance/systemstate/attestation"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("godjcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "", "path to the locked profile JSON")
	manifestPath := flags.String("manifest", "", "path to the contract manifest JSON")
	expectedPath := flags.String("expected", "", "path to the expected reference observation suite JSON")
	deviationExpectedPath := flags.String("deviation-expected", "", "optional path to a reviewed product deviation expectation JSON")
	actualOutputPath := flags.String("actual-output", "", "optional path for the generated GoDj observation suite JSON")
	systemStatePostgreSQLAttestationPath := flags.String(
		"system-state-postgres-attestation",
		"",
		"path to the checked source-bound PostgreSQL 17.10 SYS-020 evidence",
	)
	projectOperatorPostgreSQLAttestationPath := flags.String(
		"project-operator-postgres-attestation",
		"",
		"path to the checked source-bound PostgreSQL 17.10 and SQLite SYS-029 evidence",
	)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: godjcheck -profile PROFILE.json -manifest MANIFEST.json -expected ORACLE.json [-deviation-expected PRODUCT.json] [-system-state-postgres-attestation EVIDENCE.json] [-project-operator-postgres-attestation EVIDENCE.json] [-actual-output ACTUAL.json]")
	}
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *profilePath == "" || *manifestPath == "" || *expectedPath == "" || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if ctx == nil {
		fmt.Fprintln(stderr, "godjcheck: context is nil")
		return 2
	}

	profile, err := protocol.LoadProfile(*profilePath)
	if err != nil {
		return reportFailure(stderr, err)
	}
	manifest, err := protocol.LoadManifest(*manifestPath)
	if err != nil {
		return reportFailure(stderr, err)
	}
	expected, err := protocol.LoadObservationSuite(*expectedPath)
	if err != nil {
		return reportFailure(stderr, err)
	}
	if _, err := protocol.Compare(profile, manifest, expected, expected); err != nil {
		return reportFailure(stderr, fmt.Errorf("locked reference oracle: %w", err))
	}
	manifestHasDeviation := false
	for _, contract := range manifest.Contracts {
		if contract.Status == protocol.ContractDeviation {
			manifestHasDeviation = true
			break
		}
	}
	if manifestHasDeviation && *deviationExpectedPath == "" {
		return reportFailure(stderr, fmt.Errorf("manifest contains deviation contracts but -deviation-expected is missing"))
	}
	comparisonManifest := manifest
	comparisonExpected := expected
	decision := ""
	if *deviationExpectedPath != "" {
		deviationExpected, err := protocol.LoadDeviationExpectation(*deviationExpectedPath)
		if err != nil {
			return reportFailure(stderr, err)
		}
		deviationPolicy, err := deviationPolicyForProduct(deviationExpected.Decision, manifest)
		if err != nil {
			return reportFailure(stderr, err)
		}
		comparisonManifest, comparisonExpected, err = protocol.PrepareDeviationExpectation(
			profile,
			manifest,
			expected,
			deviationExpected,
			deviationPolicy,
		)
		if err != nil {
			return reportFailure(stderr, err)
		}
		decision = deviationExpected.Decision
	}
	runnerInputs, err := loadRunnerInputs(
		comparisonManifest,
		*manifestPath,
		*systemStatePostgreSQLAttestationPath,
		*projectOperatorPostgreSQLAttestationPath,
	)
	if err != nil {
		return reportFailure(stderr, err)
	}
	requiredObserved, err := godjrunner.RequiredObservedContractIDs(comparisonManifest)
	if err != nil {
		return reportFailure(stderr, fmt.Errorf("actual handler registry: %w", err))
	}
	actual, err := godjrunner.GenerateWithInputs(ctx, profile, comparisonManifest, runnerInputs)
	if err != nil {
		return reportFailure(stderr, err)
	}

	mixedProduct := len(requiredObserved) != len(comparisonManifest.Contracts)
	var differences []protocol.Difference
	if mixedProduct {
		differences, err = protocol.CompareProduct(
			profile,
			comparisonManifest,
			comparisonExpected,
			actual,
			requiredObserved,
		)
	} else {
		differences, err = protocol.Compare(profile, comparisonManifest, comparisonExpected, actual)
	}
	if err != nil {
		return reportFailure(stderr, err)
	}
	if len(differences) == 0 {
		if *actualOutputPath != "" {
			contents, err := protocol.MarshalCanonical(actual)
			if err != nil {
				return reportFailure(stderr, fmt.Errorf("encode actual observations: %w", err))
			}
			if err := writeAtomic(ctx, *actualOutputPath, contents); err != nil {
				return reportFailure(stderr, fmt.Errorf("write actual observations: %w", err))
			}
		}
		if mixedProduct {
			notImplemented := 0
			for _, observation := range actual.Contracts {
				if observation.Status == protocol.StatusNotImplemented {
					notImplemented++
				}
			}
			fmt.Fprintf(
				stdout,
				"GoDj product observations match %d required contract%s; %d remain not implemented\n",
				len(requiredObserved),
				pluralSuffix(len(requiredObserved)),
				notImplemented,
			)
			return 0
		}
		if decision == "" {
			if manifestHasDecisionReference(manifest) {
				fmt.Fprintf(stdout, "GoDj observations match the locked reference oracle for %d contracts\n", len(manifest.Contracts))
			} else {
				fmt.Fprintf(stdout, "GoDj observations match the locked Django oracle for %d contracts\n", len(manifest.Contracts))
			}
		} else {
			fmt.Fprintf(stdout, "GoDj observations match the reviewed product expectation for %d contracts under %s\n", len(manifest.Contracts), decision)
		}
		return 0
	}
	for _, item := range differences {
		fmt.Fprintf(
			stderr,
			"%s %s: %s (expected %s, actual %s)\n",
			item.ContractID,
			item.Path,
			item.Message,
			item.Expected,
			item.Actual,
		)
	}
	fmt.Fprintf(stderr, "godjcheck: %d difference(s)\n", len(differences))
	return 1
}

func loadRunnerInputs(
	manifest protocol.Manifest,
	manifestPath string,
	systemStatePostgreSQLAttestationPath string,
	projectOperatorPostgreSQLAttestationPath string,
) (godjrunner.Inputs, error) {
	return loadRunnerInputsWithEvidenceLoader(
		manifest,
		manifestPath,
		systemStatePostgreSQLAttestationPath,
		projectOperatorPostgreSQLAttestationPath,
		checkedRunnerInputEvidenceLoader(),
	)
}

type runnerInputEvidenceLoader struct {
	loadSystemState     func(repositoryRoot, path string) (systemstateattestation.ObservedFacts, error)
	loadProjectOperator func(
		repositoryRoot, path string,
	) (operatorattestation.ObservedFacts, operatorattestation.ObservedFacts, error)
}

func checkedRunnerInputEvidenceLoader() runnerInputEvidenceLoader {
	return runnerInputEvidenceLoader{
		loadSystemState: func(repositoryRoot, path string) (systemstateattestation.ObservedFacts, error) {
			evidence, err := systemstateattestation.Load(repositoryRoot, path)
			if err != nil {
				return systemstateattestation.ObservedFacts{}, err
			}
			return evidence.BackendFacts().Observed(), nil
		},
		loadProjectOperator: func(repositoryRoot, path string) (operatorattestation.ObservedFacts, operatorattestation.ObservedFacts, error) {
			evidence, err := operatorattestation.Load(repositoryRoot, path)
			if err != nil {
				return operatorattestation.ObservedFacts{}, operatorattestation.ObservedFacts{}, err
			}
			return evidence.PostgreSQLFacts().Observed(), evidence.SQLiteFacts().Observed(), nil
		},
	}
}

func loadRunnerInputsWithEvidenceLoader(
	manifest protocol.Manifest,
	manifestPath string,
	systemStatePostgreSQLAttestationPath string,
	projectOperatorPostgreSQLAttestationPath string,
	loader runnerInputEvidenceLoader,
) (godjrunner.Inputs, error) {
	systemStateRequired, err := manifestAttestationRequired(
		manifest,
		systemstateattestation.Contract,
		systemstateattestation.Scenario,
		"PostgreSQL live attestation",
	)
	if err != nil {
		return godjrunner.Inputs{}, err
	}
	projectOperatorRequired, err := manifestAttestationRequired(
		manifest,
		operatorattestation.Contract,
		operatorattestation.Scenario,
		"external project operator attestation",
	)
	if err != nil {
		return godjrunner.Inputs{}, err
	}
	if !systemStateRequired && systemStatePostgreSQLAttestationPath != "" {
		return godjrunner.Inputs{}, errors.New("-system-state-postgres-attestation is not used by this manifest")
	}
	if !projectOperatorRequired && projectOperatorPostgreSQLAttestationPath != "" {
		return godjrunner.Inputs{}, errors.New("-project-operator-postgres-attestation is not used by this manifest")
	}
	if systemStateRequired && systemStatePostgreSQLAttestationPath == "" {
		return godjrunner.Inputs{}, errors.New("published SYS-020 requires -system-state-postgres-attestation")
	}
	if projectOperatorRequired && projectOperatorPostgreSQLAttestationPath == "" {
		return godjrunner.Inputs{}, errors.New("published SYS-029 requires -project-operator-postgres-attestation")
	}
	if !systemStateRequired && !projectOperatorRequired {
		return godjrunner.Inputs{}, nil
	}

	repositoryRoot, err := attestationRepositoryRoot(manifestPath)
	if err != nil {
		return godjrunner.Inputs{}, err
	}
	if systemStateRequired {
		if err := requireSystemStateAttestationPath(repositoryRoot, systemStatePostgreSQLAttestationPath); err != nil {
			return godjrunner.Inputs{}, err
		}
	}
	if projectOperatorRequired {
		if err := requireProjectOperatorAttestationPath(repositoryRoot, projectOperatorPostgreSQLAttestationPath); err != nil {
			return godjrunner.Inputs{}, err
		}
	}

	var inputs godjrunner.Inputs
	if systemStateRequired {
		if loader.loadSystemState == nil {
			return godjrunner.Inputs{}, errors.New("PostgreSQL live attestation loader is unavailable")
		}
		observed, err := loader.loadSystemState(repositoryRoot, systemStatePostgreSQLAttestationPath)
		if err != nil {
			return godjrunner.Inputs{}, fmt.Errorf("load PostgreSQL live attestation: %w", err)
		}
		facts, err := systemStateRunnerFacts(observed)
		if err != nil {
			return godjrunner.Inputs{}, fmt.Errorf("convert PostgreSQL live attestation: %w", err)
		}
		inputs.SystemStatePostgreSQLTwoProcess = &facts
	}
	if projectOperatorRequired {
		if loader.loadProjectOperator == nil {
			return godjrunner.Inputs{}, errors.New("external project operator attestation loader is unavailable")
		}
		postgresObserved, sqliteObserved, err := loader.loadProjectOperator(repositoryRoot, projectOperatorPostgreSQLAttestationPath)
		if err != nil {
			return godjrunner.Inputs{}, fmt.Errorf("load external project operator attestation: %w", err)
		}
		postgresFacts, err := projectOperatorRunnerFacts(postgresObserved, operatorattestation.BackendPostgreSQL)
		if err != nil {
			return godjrunner.Inputs{}, fmt.Errorf("convert external project operator PostgreSQL evidence: %w", err)
		}
		sqliteFacts, err := projectOperatorRunnerFacts(sqliteObserved, operatorattestation.BackendSQLite)
		if err != nil {
			return godjrunner.Inputs{}, fmt.Errorf("convert external project operator SQLite evidence: %w", err)
		}
		inputs.ProjectOperatorPostgreSQL = &postgresFacts
		inputs.ProjectOperatorSQLite = &sqliteFacts
	}
	return inputs, nil
}

func manifestAttestationRequired(
	manifest protocol.Manifest,
	contractID, scenario, label string,
) (bool, error) {
	found := false
	required := false
	for _, contract := range manifest.Contracts {
		if contract.ID != contractID && contract.Scenario != scenario {
			continue
		}
		if contract.ID != contractID || contract.Scenario != scenario {
			return false, fmt.Errorf("%s contract/scenario binding is inconsistent", label)
		}
		if found {
			return false, fmt.Errorf("%s contract/scenario binding appears more than once", label)
		}
		found = true
		switch contract.Status {
		case protocol.ContractPassing, protocol.ContractDeviation:
			required = true
		case protocol.ContractOracleLocked:
			required = false
		default:
			return false, fmt.Errorf("%s contract has unsupported status %q", label, contract.Status)
		}
	}
	return required, nil
}

func systemStateRunnerFacts(observed systemstateattestation.ObservedFacts) (godjrunner.SystemStateTwoProcessBackendFacts, error) {
	validated, err := systemstateattestation.NewBackendFacts(observed)
	if err != nil {
		return godjrunner.SystemStateTwoProcessBackendFacts{}, err
	}
	observed = validated.Observed()
	writerProcesses, err := checkedRunnerFactInt(observed.WriterProcesses, "writer processes")
	if err != nil {
		return godjrunner.SystemStateTwoProcessBackendFacts{}, err
	}
	divergenceCount, err := checkedRunnerFactInt(observed.DivergenceCount, "cross-process state divergence")
	if err != nil {
		return godjrunner.SystemStateTwoProcessBackendFacts{}, err
	}
	lossCount, err := checkedRunnerFactInt(observed.LossCount, "restart state loss")
	if err != nil {
		return godjrunner.SystemStateTwoProcessBackendFacts{}, err
	}
	secretOccurrences, err := checkedRunnerFactInt(observed.SecretOccurrences, "secret occurrences")
	if err != nil {
		return godjrunner.SystemStateTwoProcessBackendFacts{}, err
	}
	barrierRace := "not_linearized"
	if observed.BarrierLinearized {
		barrierRace = "linearized"
	}
	return godjrunner.SystemStateTwoProcessBackendFacts{
		Backend:                     "postgresql_17_10",
		WriterProcesses:             writerProcesses,
		BarrierRace:                 barrierRace,
		CleanRestartPreserved:       observed.RestartPreserved,
		SameDatabaseOrSchema:        observed.SameSchema,
		CrossProcessStateDivergence: divergenceCount,
		RestartStateLoss:            lossCount,
		SchemaDrift:                 observed.DriftCount != 0,
		SecretValuesSerialized:      secretOccurrences,
	}, nil
}

func projectOperatorRunnerFacts(
	observed operatorattestation.ObservedFacts,
	expectedBackend string,
) (godjrunner.GDJ0055OperatorBackendFacts, error) {
	validated, err := operatorattestation.NewProductFacts(observed)
	if err != nil {
		return godjrunner.GDJ0055OperatorBackendFacts{}, err
	}
	observed = validated.Observed()
	if observed.Backend != expectedBackend {
		return godjrunner.GDJ0055OperatorBackendFacts{}, errors.New("external project operator backend identity does not match its required case")
	}
	provisionProcesses, err := checkedRunnerFactInt(observed.ProvisionProcesses, "provision processes")
	if err != nil {
		return godjrunner.GDJ0055OperatorBackendFacts{}, err
	}
	runtimeProcesses, err := checkedRunnerFactInt(observed.RuntimeProcesses, "runtime processes")
	if err != nil {
		return godjrunner.GDJ0055OperatorBackendFacts{}, err
	}
	distinctProcesses, err := checkedRunnerFactInt(observed.DistinctProcesses, "distinct processes")
	if err != nil {
		return godjrunner.GDJ0055OperatorBackendFacts{}, err
	}
	provisionCalls, err := checkedRunnerFactInt(observed.ProvisionCalls, "provision calls")
	if err != nil {
		return godjrunner.GDJ0055OperatorBackendFacts{}, err
	}
	credentialRows, err := checkedRunnerFactInt(observed.CredentialRows, "credential rows")
	if err != nil {
		return godjrunner.GDJ0055OperatorBackendFacts{}, err
	}
	restartStateLoss, err := checkedRunnerFactInt(observed.RestartStateLoss, "restart state loss")
	if err != nil {
		return godjrunner.GDJ0055OperatorBackendFacts{}, err
	}
	rawSecretOccurrences, err := checkedRunnerFactInt(observed.RawSecretOccurrences, "raw secret occurrences")
	if err != nil {
		return godjrunner.GDJ0055OperatorBackendFacts{}, err
	}
	return godjrunner.GDJ0055OperatorBackendFacts{
		Backend:                             observed.Backend,
		ProvisionProcesses:                  provisionProcesses,
		RuntimeProcesses:                    runtimeProcesses,
		DistinctProcesses:                   distinctProcesses,
		ProvisionCalls:                      provisionCalls,
		CredentialRows:                      credentialRows,
		Provisioned:                         observed.Provisioned,
		AdminAuthenticated:                  observed.AdminAuthenticated,
		APIAuthenticated:                    observed.APIAuthenticated,
		DistinctProcessRestart:              observed.DistinctProcessRestart,
		ProvisionProcessDistinctFromRuntime: observed.ProvisionProcessDistinctFromRuntime,
		RestartRawSecretInput:               observed.RestartRawSecretInput,
		RestartStateLoss:                    restartStateLoss,
		SchemaDrift:                         observed.SchemaDrift,
		RawSecretOccurrences:                rawSecretOccurrences,
	}, nil
}

const maxPortableRunnerFact = int64(^uint32(0) >> 1)

func checkedRunnerFactInt(value int64, field string) (int, error) {
	if value < 0 || value > maxPortableRunnerFact {
		return 0, fmt.Errorf("%s is outside the portable runner integer range", field)
	}
	converted := int(value)
	if int64(converted) != value {
		return 0, fmt.Errorf("%s does not fit the runner integer type", field)
	}
	return converted, nil
}

func attestationRepositoryRoot(manifestPath string) (string, error) {
	repositoryRoot, err := currentGoDjRepositoryRoot()
	if err != nil {
		return "", err
	}
	expectedManifest := filepath.Join(repositoryRoot, "conformance", "contracts", "system-state-manifest.json")
	if err := requireExactResolvedPath(manifestPath, expectedManifest); err != nil {
		return "", fmt.Errorf("system-state manifest must use the checked current repository path: %w", err)
	}
	return repositoryRoot, nil
}

func requireSystemStateAttestationPath(repositoryRoot, attestationPath string) error {
	expectedAttestation := filepath.Join(
		repositoryRoot,
		"conformance",
		"systemstate",
		"attestations",
		systemstateattestation.FileName,
	)
	if err := requireExactResolvedPath(attestationPath, expectedAttestation); err != nil {
		return fmt.Errorf("PostgreSQL live attestation must use the checked current repository path: %w", err)
	}
	return nil
}

func requireProjectOperatorAttestationPath(repositoryRoot, attestationPath string) error {
	expectedAttestation := filepath.Join(
		repositoryRoot,
		"conformance",
		"projectoperatorproduct",
		"attestations",
		operatorattestation.FileName,
	)
	if err := requireExactResolvedPath(attestationPath, expectedAttestation); err != nil {
		return fmt.Errorf("external project operator attestation must use the checked current repository path: %w", err)
	}
	return nil
}

func currentGoDjRepositoryRoot() (string, error) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok || !filepath.IsAbs(sourceFile) {
		return "", errors.New("resolve current GoDj checkout from command source")
	}
	directory := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", errors.New("resolve current GoDj checkout symlinks")
	}
	contents, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if err != nil || !strings.HasPrefix(string(contents), "module github.com/progresshans/godj\n") {
		return "", errors.New("command source is not inside the GoDj repository")
	}
	return directory, nil
}

func requireExactResolvedPath(given, expected string) error {
	absolute, err := filepath.Abs(given)
	if err != nil {
		return errors.New("resolve path")
	}
	expectedAbsolute, err := filepath.Abs(expected)
	if err != nil {
		return errors.New("resolve checked path")
	}
	absolute = filepath.Clean(absolute)
	expectedAbsolute = filepath.Clean(expectedAbsolute)
	if absolute != expectedAbsolute {
		return errors.New("path is not the exact checked path")
	}
	resolvedPath := absolute
	for {
		resolvedGiven, err := filepath.EvalSymlinks(resolvedPath)
		if err == nil {
			if filepath.Clean(resolvedGiven) != resolvedPath {
				return errors.New("checked path contains a symbolic link")
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("resolve path symlinks")
		}
		parent := filepath.Dir(resolvedPath)
		if parent == resolvedPath {
			return errors.New("resolve path symlinks")
		}
		resolvedPath = parent
	}
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func manifestHasDecisionReference(manifest protocol.Manifest) bool {
	if len(manifest.Contracts) == 0 {
		return false
	}
	for _, contract := range manifest.Contracts {
		hasDecision := false
		for _, provenance := range contract.Provenance {
			if provenance.Kind == "decision" {
				hasDecision = true
				break
			}
		}
		if !hasDecision {
			return false
		}
	}
	return true
}

func reportFailure(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "godjcheck:", err)
	return 2
}

func writeAtomic(ctx context.Context, path string, contents []byte) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}
