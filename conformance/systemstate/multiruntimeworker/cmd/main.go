package main

import (
	"context"
	"io"
	"os"

	"github.com/progresshans/godj/conformance/systemstate/multiruntimeworker"
)

func main() { os.Exit(runMain()) }

func runMain() (exitCode int) {
	defer func() {
		if recover() != nil {
			_, _ = io.WriteString(os.Stderr, "system-state multi-runtime worker failed\n")
			exitCode = 1
		}
	}()
	config := os.NewFile(3, "system-state-multiruntime-config")
	events := os.NewFile(4, "system-state-multiruntime-events")
	control := os.NewFile(5, "system-state-multiruntime-control")
	if config == nil || events == nil || control == nil {
		_, _ = io.WriteString(os.Stderr, "system-state multi-runtime worker failed\n")
		return 1
	}
	defer func() {
		_ = config.Close()
		_ = events.Close()
		_ = control.Close()
	}()
	if err := multiruntimeworker.RunWorker(context.Background(), config, events, control, os.Stdout); err != nil {
		_, _ = io.WriteString(os.Stderr, "system-state multi-runtime worker failed\n")
		return 1
	}
	return 0
}
