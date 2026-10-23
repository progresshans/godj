package main

import (
	"context"
	"fmt"
	"os"

	"github.com/progresshans/godj/identity/domain/modeldef"
	"github.com/progresshans/godj/project"
)

func main() {
	err := project.Run(context.Background(), project.Config{
		LoadProjectSpec: modeldef.ProjectSpec, MigrationDefinitionRoots: []string{"migrations"},
	}, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		code := project.RunnerExitCode(err)
		if code == 1 {
			_, _ = fmt.Fprintln(os.Stderr, "account domain project runner failed")
		}
		os.Exit(code)
	}
}
