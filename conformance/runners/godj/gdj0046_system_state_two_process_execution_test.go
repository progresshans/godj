//go:build darwin || linux

package godj

import (
	"context"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestSystemStateTwoProcessBackendRestartRunsLiveSQLiteAndInjectedPostgreSQLFacts(t *testing.T) {
	postgresql := systemStateTestTwoProcessFacts("postgresql_17_10")
	observation, err := systemStateTwoProcessBackendRestart(
		context.Background(),
		protocol.Contract{ID: "SYS-020", Phase: protocol.PhaseEnvironment},
		Inputs{SystemStatePostgreSQLTwoProcess: &postgresql},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := systemStateTestInteger(t, *observation.Metrics, "distinct_processes"); got != 2 {
		t.Fatalf("distinct processes = %d, want 2", got)
	}
	if got := systemStateTestInteger(t, *observation.Metrics, "secret_values_serialized"); got != 0 {
		t.Fatalf("serialized secret occurrences = %d, want 0", got)
	}
	if got := systemStateTestField(t, *observation.Result, "barrier_race"); got.Text == nil || *got.Text != "linearized" {
		t.Fatalf("barrier race = %#v", got)
	}
}

func TestSystemStateWorkerBuildEnvironmentRejectsAmbientWorkspaceAndToolchainOverrides(t *testing.T) {
	t.Parallel()

	got := systemStateWorkerBuildEnvironment([]string{
		"KEEP=bounded",
		"CGO_ENABLED=1",
		"GO111MODULE=off",
		"GOAMD64=v4",
		"GOARCH=386",
		"GOCACHEPROG=/tmp/cache-helper",
		"GODEBUG=gotypesalias=0",
		"GOENV=/tmp/ambient-goenv",
		"GOEXPERIMENT=fieldtrack",
		"GOFIPS140=v1.0.0",
		"GOFLAGS=-mod=mod",
		"GOOS=plan9",
		"GOROOT=/tmp/ambient-goroot",
		"GOTOOLCHAIN=auto",
		"GOWORK=/tmp/ambient-go.work",
	})
	want := []string{
		"CGO_ENABLED=0",
		"GO111MODULE=on",
		"GOENV=off",
		"GOFLAGS=",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"KEEP=bounded",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("worker build environment = %#v, want %#v", got, want)
	}
}

func TestSystemStateTwoProcessBackendRestartRequiresInjectedPostgreSQLEvidenceBeforeBuilding(t *testing.T) {
	t.Parallel()

	_, err := systemStateTwoProcessBackendRestart(
		context.Background(),
		protocol.Contract{ID: "SYS-020", Phase: protocol.PhaseEnvironment},
		Inputs{},
	)
	if err == nil || err.Error() != "two-process system-state scenario: verified PostgreSQL evidence is missing" {
		t.Fatalf("missing PostgreSQL evidence error = %v", err)
	}
}
