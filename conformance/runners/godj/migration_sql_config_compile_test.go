//go:build darwin || linux

package godj

import (
	"context"
	"errors"
	"testing"
)

func TestMigrationSQLConfigSourceImpactRequiresActualCompilerArityRejection(t *testing.T) {
	fixture, err := newMigrationSQLRenderingActualProject()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fixture.close(); err != nil {
			t.Error(err)
		}
	})
	for _, probe := range []struct {
		name, source     string
		rejected, failed bool
	}{
		{"partial_unkeyed", migrationSQLRenderingUnkeyedSource(false), true, false},
		{"complete_unkeyed", migrationSQLRenderingUnkeyedSource(true), false, false},
		{"current_keyed", `package unkeyed
import "github.com/progresshans/godj/project"
var _ = project.Config{MigrationSQLRenderer:nil}
`, false, false},
		{"unrelated_compile_failure", `package unkeyed
var _ = missingConfigCompileIdentifier
`, false, true},
	} {
		t.Run(probe.name, func(t *testing.T) {
			rejected, err := migrationSQLRenderingCompileUnkeyed(t.Context(), fixture, probe.source)
			if rejected != probe.rejected || (err != nil) != probe.failed {
				t.Fatalf("compiler evidence rejected=%t error=%v", rejected, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if rejected, err := migrationSQLRenderingCompileUnkeyed(ctx, fixture, migrationSQLRenderingUnkeyedSource(false)); rejected || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation counted as compiler rejection", err)
	}
}
