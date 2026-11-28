//go:build darwin || linux

package godj

import (
	"context"
	"strings"
	"testing"
	"time"

	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
)

func TestMigrationCommandActualFailureDiagnosticsKeepCancellationAndDiscardPrivateInputs(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "canceled"
		if expired {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			} else {
				cancel()
			}
			defer cancel()
			const secret = "migration-command-diagnostic-private-canary"
			backend := newMigrationCommandActualBackend()
			result := backend.Execute(ctx, nil, productcheck.BuildStage, productcheck.Command{
				Dir: secret, Argv: []string{secret}, Env: []string{"SECRET=" + secret}, Stdin: []byte(secret),
			})
			if result.Started || result.DirectReaps != 0 || result.Failure == nil || result.Failure.Code != migrateprotocol.CodeProjectCanceled {
				t.Fatalf("diagnostic wrapper changed native cancellation: %+v", result)
			}
			if len(backend.phases) != 1 || backend.phases[0].Canceled == expired || backend.phases[0].DeadlineExceeded != expired || backend.phases[0].Started {
				t.Fatalf("phase lost the cancellation cause: %+v", backend.phases)
			}
			execution := migrationCommandActualExecution{
				report: productcheck.MigrateReport{HasMigrateFailure: true,
					MigrateFailure: migrateprotocol.Failure{Category: result.Failure.Category, Code: result.Failure.Code}},
				stdout: []byte(secret), stderr: []byte(secret), environment: []string{"SECRET=" + secret}, phases: backend.phases,
			}
			for _, err := range []error{
				migrationCommandActualSuccess(execution),
				migrationCommandActualFailure(execution, migrateprotocol.CategoryProcess, migrateprotocol.CodeProjectCanceled, 0, 0),
			} {
				if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), migrateprotocol.CodeProjectCanceled) || !strings.Contains(err.Error(), "phases:[{Stage:1") {
					t.Fatalf("failure diagnostics lost the stage or exposed private inputs: %v", err)
				}
			}
		})
	}
}
