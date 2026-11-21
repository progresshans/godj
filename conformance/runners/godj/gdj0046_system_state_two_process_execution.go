package godj

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/conformance/systemstate/multiruntimeworker"
	"github.com/progresshans/godj/internal/gobuild"
)

const maximumSystemStateWorkerBuildOutput = 64 << 10

const systemStateTwoProcessScenario = "godj.system_state.two_process_backend_restart"

func systemStateTwoProcessScenarioHandler(inputs Inputs) scenarioHandler {
	inputs = inputs.snapshot()
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		return systemStateTwoProcessBackendRestart(ctx, contract, inputs)
	}
}

func systemStateTwoProcessBackendRestart(
	ctx context.Context,
	contract protocol.Contract,
	inputs Inputs,
) (protocol.Observation, error) {
	if ctx == nil {
		return protocol.Observation{}, errors.New("two-process system-state scenario: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return protocol.Observation{}, err
	}
	if inputs.SystemStatePostgreSQLTwoProcess == nil {
		return protocol.Observation{}, errors.New("two-process system-state scenario: verified PostgreSQL evidence is missing")
	}

	directory, err := os.MkdirTemp("", "godj-system-state-two-process-")
	if err != nil {
		return protocol.Observation{}, errors.New("create two-process system-state directory")
	}
	defer os.RemoveAll(directory)
	executable := filepath.Join(directory, "godj-system-state-multiruntime-worker")
	if err := buildSystemStateMultiRuntimeWorker(ctx, executable); err != nil {
		return protocol.Observation{}, err
	}
	database, err := multiruntimeworker.NewSQLiteDatabase(filepath.Join(directory, "system-state.sqlite3"))
	if err != nil {
		return protocol.Observation{}, errors.New("configure SQLite two-process system-state database")
	}
	observed, err := multiruntimeworker.RunScenario(ctx, executable, database)
	if err != nil {
		return protocol.Observation{}, errors.New("run SQLite two-process system-state scenario")
	}
	sqliteFacts := SystemStateTwoProcessBackendFacts{
		Backend:                      "sqlite",
		WriterProcesses:              observed.WriterProcesses,
		BarrierRace:                  systemStateBarrierRace(observed.BarrierLinearized),
		HolderCallbackInvocations:    observed.HolderCallbackInvocations,
		ContenderCallbackInvocations: observed.ContenderCallbackInvocations,
		CleanRestartPreserved:        observed.RestartPreserved,
		SameDatabaseOrSchema:         observed.SameSchema,
		CrossProcessStateDivergence:  boolCount(observed.Divergence),
		RestartStateLoss:             boolCount(observed.Loss),
		SchemaDrift:                  observed.Drift,
		SecretValuesSerialized:       observed.SecretOccurrences,
	}
	return systemStateTwoProcessObservation(contract, sqliteFacts, *inputs.SystemStatePostgreSQLTwoProcess)
}

func buildSystemStateMultiRuntimeWorker(ctx context.Context, executable string) error {
	command := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-buildvcs=false",
		"-mod=readonly",
		"-trimpath",
		"-o",
		executable,
		"./conformance/systemstate/multiruntimeworker/cmd",
	)
	repositoryRoot, err := systemStateRepositoryRoot()
	if err != nil {
		return errors.New("locate two-process system-state worker source")
	}
	command.Dir = repositoryRoot
	command.Env = systemStateWorkerBuildEnvironment(os.Environ())
	var stdout, stderr gobuild.Capture
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("build two-process system-state worker: %w", &gobuild.Error{
			Cause: err, Diagnostic: gobuild.Summary(stdout.Bytes(), stderr.Bytes(), command.Env),
		})
	}
	if stdout.Len()+stderr.Len() > maximumSystemStateWorkerBuildOutput {
		return errors.New("two-process system-state worker build exceeded diagnostic limit")
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("two-process system-state worker was not published as an executable")
	}
	return nil
}

func systemStateWorkerBuildEnvironment(base []string) []string {
	blocked := map[string]struct{}{
		"CGO_ENABLED":  {},
		"GO111MODULE":  {},
		"GO386":        {},
		"GOAMD64":      {},
		"GOARCH":       {},
		"GOARM":        {},
		"GOARM64":      {},
		"GOCACHEPROG":  {},
		"GODEBUG":      {},
		"GOENV":        {},
		"GOEXPERIMENT": {},
		"GOFIPS140":    {},
		"GOFLAGS":      {},
		"GOMIPS":       {},
		"GOMIPS64":     {},
		"GOOS":         {},
		"GOPPC64":      {},
		"GORISCV64":    {},
		"GOROOT":       {},
		"GOTOOLCHAIN":  {},
		"GOWASM":       {},
		"GOWORK":       {},
	}
	environment := make([]string, 0, len(base)+4)
	for _, entry := range base {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, excluded := blocked[name]; excluded {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(
		environment,
		"CGO_ENABLED=0",
		"GO111MODULE=on",
		"GOENV=off",
		"GOFLAGS=",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
	)
	sort.Strings(environment)
	return environment
}

func systemStateBarrierRace(linearized bool) string {
	if linearized {
		return "linearized"
	}
	return "not_linearized"
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
