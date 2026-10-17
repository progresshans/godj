package main

import (
	"context"
	"fmt"
	"os"

	"github.com/progresshans/godj/conformance/identityfixture/modeldef"
	"github.com/progresshans/godj/project"
)

func main() {
	err := project.Run(context.Background(), project.Config{LoadProjectSpec: modeldef.ProjectSpec}, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		code := project.RunnerExitCode(err)
		if code == 1 {
			_, _ = fmt.Fprintln(os.Stderr, "identity fixture runner failed")
		}
		os.Exit(code)
	}
}
