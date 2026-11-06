package main

import (
	"context"
	"fmt"
	"os"

	fixture "github.com/progresshans/godj/conformance/cascadefixture"
	"github.com/progresshans/godj/project"
)

func main() {
	if err := project.Run(context.Background(), project.Config{LoadProjectSpec: fixture.ProjectSpec}, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "cascade fixture runner failed:", err)
		os.Exit(1)
	}
}
