package main

import (
	"context"
	"fmt"
	"os"

	"github.com/progresshans/godj/conformance/relationdeleteproduct/fixture"
	godjproject "github.com/progresshans/godj/project"
)

func main() {
	err := godjproject.Run(
		context.Background(),
		godjproject.Config{LoadProjectSpec: fixture.ProjectSpec},
		os.Args[1:],
		os.Stdin,
		os.Stdout,
	)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		os.Exit(1)
	}
}
