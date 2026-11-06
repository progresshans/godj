//go:build darwin || linux

package godj

import (
	"context"
	"testing"
	"time"

	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
)

func migrationBudgetTestCommand(t *testing.T, pause string) productcheck.Command {
	t.Helper()
	return productcheck.Command{
		Dir: t.TempDir(), Argv: []string{"/bin/sleep", pause}, Env: []string{"PATH=/usr/bin:/bin"},
	}
}

func TestMigrationCommandActualPreparationDoesNotSpendExecutionBudget(t *testing.T) {
	backend := newMigrationCommandActualBackend()
	backend.limits = migrationCommandActualLimits{build: 3 * time.Second, execution: 300 * time.Millisecond}
	ctx, cancel := backend.limits.context(t.Context())
	defer cancel()
	// A real preparation child takes longer than the subsequent execution
	// allowance. It must not consume the probe or migration child's budget.
	for _, phase := range []struct {
		stage productcheck.ProcessStage
		pause string
	}{{productcheck.BuildStage, "0.6"}, {productcheck.CompatibilityStage, "0.05"}, {productcheck.MigrateRunnerStage, "0.05"}} {
		result := backend.Execute(ctx, nil, phase.stage, migrationBudgetTestCommand(t, phase.pause))
		if !result.Started || result.ExitCode != 0 || result.Failure != nil || result.DirectReaps != 1 || result.CleanupFailed || result.SIGINTAttempts != 0 || result.SIGKILLAttempts != 0 {
			t.Fatalf("preparation consumed a later execution allowance: stage=%d result=%+v phases=%+v", phase.stage, result, backend.phases)
		}
	}
}

func TestMigrationCommandActualExecutionKeepsItsOwnDeadlineAndReaps(t *testing.T) {
	backend := newMigrationCommandActualBackend()
	backend.limits = migrationCommandActualLimits{build: 3 * time.Second, execution: 300 * time.Millisecond}
	ctx, cancel := backend.limits.context(t.Context())
	defer cancel()
	result := backend.Execute(ctx, nil, productcheck.MigrateRunnerStage, migrationBudgetTestCommand(t, "5"))
	if ctx.Err() != nil || !result.Started || result.Failure == nil || result.Failure.Code != migrateprotocol.CodeProjectCanceled || result.DirectReaps != 1 || result.CleanupFailed || result.SIGINTAttempts != 1 {
		t.Fatalf("execution borrowed the build allowance or abandoned its child: result=%+v parent=%v phases=%+v", result, ctx.Err(), backend.phases)
	}
	if len(backend.phases) != 1 || !backend.phases[0].DeadlineExceeded {
		t.Fatalf("execution deadline was not observed: %+v", backend.phases)
	}
}

func TestMigrationCommandActualStagePreservesEarlierParentDeadline(t *testing.T) {
	backend := newMigrationCommandActualBackend()
	backend.limits = migrationCommandActualLimits{build: 3 * time.Second, execution: 3 * time.Second}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	result := backend.Execute(ctx, nil, productcheck.MigrateRunnerStage, migrationBudgetTestCommand(t, "5"))
	if ctx.Err() != context.DeadlineExceeded || !result.Started || result.Failure == nil || result.Failure.Code != migrateprotocol.CodeProjectCanceled || result.DirectReaps != 1 || result.CleanupFailed || result.SIGINTAttempts != 1 {
		t.Fatalf("phase allowance widened parent cancellation or lost its child: result=%+v phases=%+v", result, backend.phases)
	}
}
