package main

import (
	"context"
	"os"

	fixture "github.com/progresshans/godj/conformance/accountdeletefixture"
	"github.com/progresshans/godj/project"
)

func main() {
	if err := project.Run(context.Background(), project.Config{LoadProjectSpec: fixture.ProjectSpec}, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		os.Exit(project.RunnerExitCode(err))
	}
}
