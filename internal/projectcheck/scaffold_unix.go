//go:build darwin || linux

package projectcheck

import (
	"context"
	"errors"
	"strings"

	"github.com/progresshans/godj/internal/projectscaffold"
)

func scaffoldPath(value string) bool {
	return value != "" && len(value) <= 4096 && !strings.HasPrefix(value, "-") && !strings.ContainsAny(value, "\x00\r\n")
}

// scaffoldCommandContext bridges the process interrupt to the filesystem
// publication context and returns a join, not an abandoned signal goroutine.
func scaffoldCommandContext(ctx context.Context, interrupt <-chan struct{}) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-interrupt:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() { cancel(); <-done }
}

func scaffoldFailure(ctx context.Context, interrupt <-chan struct{}, publication projectscaffold.Result, err error) (string, int) {
	code, exit := "publication_failed", 1
	var stage *projectscaffold.Error
	if errors.As(err, &stage) {
		code = stage.Code
	}
	var preparation *startProjectPreparationError
	if errors.As(err, &preparation) {
		code = preparation.code
	}
	cleanupFailed := false
	for _, cleanup := range []string{"staging_recovery_required", "cleanup_failed"} {
		if errors.Is(err, &projectscaffold.Error{Code: cleanup}) {
			code, cleanupFailed = cleanup, true
			break
		}
	}
	if !cleanupFailed && errors.Is(err, &startProjectPreparationError{code: "build_cleanup_failed"}) {
		code, cleanupFailed = "build_cleanup_failed", true
	}
	if publication.Published {
		return "published_" + code, exit
	}
	if !cleanupFailed {
		if interrupted := commandInterruption(ctx, interrupt); interrupted != "" {
			return interrupted, 130
		}
	}
	return code, exit
}
