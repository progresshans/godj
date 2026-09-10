// The declaration runner keeps generation independent of generated model code.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/progresshans/godj/examples/helpdesk/modeldef"
	godjproject "github.com/progresshans/godj/project"
)

func main() {
	err := godjproject.Run(context.Background(), godjproject.Config{LoadProjectSpec: modeldef.ProjectSpec}, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		code := godjproject.RunnerExitCode(err)
		if code == 1 {
			_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		}
		os.Exit(code)
	}
}
