package main

import (
	"context"
	"fmt"
	"os"

	"github.com/progresshans/godj/admin/testdata/browser/modeldef"
	"github.com/progresshans/godj/project"
)

func main() {
	if err := project.Run(context.Background(), project.Config{LoadProjectSpec: modeldef.ProjectSpec}, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "collection browser generation failed:", err)
		os.Exit(1)
	}
}
