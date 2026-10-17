package main

import (
	"context"
	"fmt"
	"os"

	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/examples/article/modeldef"
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
	initialSuperuser, principalErr := operatorconfig.InitialSuperuser()
	identityConfig, identityConfigErr := operatorconfig.IdentityRuntimeConfig()
	return godjproject.Config{
		MigrationDefinitionRoots:   []string{"migrations"},
		MigrationDefinitionSources: systemstate.IdentityMigrationSources(),
		LoadProjectSpec:            modeldef.ProjectSpec,
		OpenMigrationBackend: func(ctx context.Context) (godjproject.MigrationBackend, error) {
			if selectionErr != nil {
				return nil, selectionErr
			}
			return databaseconfig.Open(ctx, selected)
		},
		MigrationSQLRenderer: selected.MigrationSQLRenderer(),
		OpenSystemStateBackend: func(ctx context.Context) (godjproject.SystemStateBackend, error) {
			if principalErr != nil {
				return nil, principalErr
			}
			if identityConfigErr != nil {
				return nil, identityConfigErr
			}
			if selectionErr != nil {
				return nil, selectionErr
			}
			return databaseconfig.Open(ctx, selected)
		},
		InitialSuperuser: initialSuperuser,
		PasswordHasher:   identityConfig.PasswordHasher,
	}
}
