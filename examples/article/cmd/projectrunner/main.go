package main

import (
	"context"
	"fmt"
	"os"

	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/modeldef"
	godjproject "github.com/progresshans/godj/project"
	"github.com/progresshans/godj/project/compatibility"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/settings/database"
)

func main() {
	if handled, err := compatibility.Serve(context.Background(), os.Args[1:], os.Stdout); handled {
		if err != nil {
			os.Exit(1)
		}
		return
	}
	environment, err := settings.NewEnvironment(os.Environ())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "project runner configuration failed")
		os.Exit(1)
	}
	err = godjproject.Run(
		context.Background(),
		articleProjectConfig(environment.Lookup),
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

func articleProjectConfig(lookup database.LookupEnvFunc) godjproject.Config {
	selected, selectionErr := databaseconfig.FromEnvironment(lookup)
	return godjproject.Config{
		MigrationDefinitionRoots:   []string{"migrations"},
		MigrationDefinitionSources: modeldef.MigrationSources(),
		LoadProjectSpec:            modeldef.ProjectSpec,
		OpenMigrationBackend: func(ctx context.Context) (godjproject.MigrationBackend, error) {
			if selectionErr != nil {
				return nil, selectionErr
			}
			return database.Open(ctx, selected)
		},
		MigrationSQLRenderer: selected.MigrationSQLRenderer(),
	}
}
