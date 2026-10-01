// The declaration runner keeps generation independent of generated model code.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/progresshans/godj/examples/helpdesk/modeldef"
	godjproject "github.com/progresshans/godj/project"
	"github.com/progresshans/godj/systemstate"
)

func main() {
	err := godjproject.Run(context.Background(), godjproject.Config{
		LoadProjectSpec:            modeldef.ProjectSpec,
		MigrationDefinitionRoots:   []string{"migrations"},
		MigrationDefinitionSources: systemstate.IdentityMigrationSources(),
	}, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		code := godjproject.RunnerExitCode(err)
		if code == 1 {
			_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		}
		os.Exit(code)
	}
}
