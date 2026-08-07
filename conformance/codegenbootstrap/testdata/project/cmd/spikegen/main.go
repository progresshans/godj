package main

import (
	"flag"
	"fmt"
	"os"

	"example.com/codegenbootstrapfixture/modeldef"
	"example.com/codegenbootstrapfixture/spikecodegen"
)

func main() {
	output := flag.String("out", "models/zz_godj_generated.go", "generated Go output")
	target := flag.String("target", "./models", "package compiled with the candidate output")
	flag.Parse()

	if err := spikecodegen.Generate(modeldef.Models, *output, *target); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
