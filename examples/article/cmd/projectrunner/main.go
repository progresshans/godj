package main

import (
	"context"
	"fmt"
	"os"

	"github.com/progresshans/godj/examples/article/databaseconfig"
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
		_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		os.Exit(1)
	}
}

func articleProjectConfig(lookup databaseconfig.LookupEnvFunc) godjproject.Config {
	return godjproject.Config{
		MigrationDefinitionRoots:   []string{"migrations"},
		MigrationDefinitionSources: []definition.Source{systemstate.InitialDefinitionSource()},
		LoadProjectSpec:            modeldef.ProjectSpec,
		OpenMigrationBackend: func(ctx context.Context) (godjproject.MigrationBackend, error) {
			config, err := databaseconfig.FromEnvironment(lookup)
			if err != nil {
				return nil, err
			}
			return databaseconfig.Open(ctx, config)
		},
	}
}
