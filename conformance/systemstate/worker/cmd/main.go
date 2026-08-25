package main

import (
	"context"
	"io"
	"os"

	"github.com/progresshans/godj/conformance/systemstate/worker"
)

func main() {
	os.Exit(runMain())
}

func runMain() (exitCode int) {
	defer func() {
		if recover() != nil {
			_, _ = io.WriteString(os.Stderr, "system-state worker failed\n")
			exitCode = 1
		}
	}()
	secret := os.NewFile(3, "system-state-worker-secret")
	if secret == nil {
		_, _ = io.WriteString(os.Stderr, "system-state worker failed\n")
		return 1
	}
	defer func() { _ = secret.Close() }()
	if err := worker.Run(context.Background(), os.Stdin, os.Stdout, secret); err != nil {
		_, _ = io.WriteString(os.Stderr, "system-state worker failed\n")
		return 1
	}
	return 0
}
