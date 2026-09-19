package main

import (
	"context"
	"fmt"
	"os"

	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/examples/article/modeldef"
	"github.com/progresshans/godj/migrations/definition"
	godjproject "github.com/progresshans/godj/project"
	"github.com/progresshans/godj/systemstate"
)

func main() {
	err := godjproject.Run(
		context.Background(),
		articleProjectConfig(os.LookupEnv),
		os.Args[1:],
		os.Stdin,
		os.Stdout,
	)
	if err != nil {
		exitCode := godjproject.RunnerExitCode(err)
		if exitCode == 1 {
			_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		}
		os.Exit(exitCode)
	}
}

func articleProjectConfig(lookup databaseconfig.LookupEnvFunc) godjproject.Config {
	selected, selectionErr := databaseconfig.FromEnvironment(lookup)
	operatorPolicy, operatorPolicyErr := operatorconfig.CredentialPolicy()
	return godjproject.Config{
		MigrationDefinitionRoots:   []string{"migrations"},
		MigrationDefinitionSources: []definition.Source{systemstate.InitialDefinitionSource()},
		LoadProjectSpec:            modeldef.ProjectSpec,
		OpenMigrationBackend: func(ctx context.Context) (godjproject.MigrationBackend, error) {
			if selectionErr != nil {
				return nil, selectionErr
			}
			return databaseconfig.Open(ctx, selected)
		},
		MigrationSQLRenderer: selected.MigrationSQLRenderer(),
		OpenSystemStateBackend: func(ctx context.Context) (godjproject.SystemStateBackend, error) {
			if operatorPolicyErr != nil {
				return nil, operatorPolicyErr
			}
			if selectionErr != nil {
				return nil, selectionErr
			}
			return databaseconfig.Open(ctx, selected)
		},
		SystemOperatorPolicy: operatorPolicy,
	}
}
