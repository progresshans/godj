package main

import (
	"context"
	"fmt"
	"os"

	fixture "github.com/progresshans/godj/conformance/onetoonefixture"
	"github.com/progresshans/godj/project"
)

func main() {
	err := project.Run(context.Background(), project.Config{LoadProjectSpec: fixture.ProjectSpec}, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "one-to-one fixture runner failed:", err)
		os.Exit(1)
	}
}
