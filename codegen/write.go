package codegen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
)

var ErrDrift = errors.New("generated source differs from committed output")

type WriteOptions struct {
	Check bool
	// Verify must compile or otherwise validate the candidate in its target
	// package. It is required for writes and is not called in Check mode.
	Verify func(context.Context, string) error
}

func WriteFile(ctx context.Context, path string, source []byte, options WriteOptions) error {
	if ctx == nil {
		return fmt.Errorf("write generated source: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := parser.ParseFile(token.NewFileSet(), path, source, parser.AllErrors); err != nil {
		return fmt.Errorf("validate generated source: %w", err)
	}
	if options.Check {
		existing, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%w: read %s: %v", ErrDrift, path, err)
		}
		if !bytes.Equal(existing, source) {
			return fmt.Errorf("%w: %s", ErrDrift, path)
		}
		return nil
	}
	if options.Verify == nil {
		return fmt.Errorf("write generated source: candidate verifier is required")
	}

	directory := filepath.Dir(path)
	candidate, err := os.CreateTemp(directory, ".godj-generated-*")
	if err != nil {
		return fmt.Errorf("create generated candidate: %w", err)
	}
	candidatePath := candidate.Name()
	keepCandidate := false
	defer func() {
		_ = candidate.Close()
		if !keepCandidate {
			_ = os.Remove(candidatePath)
		}
	}()
	if err := candidate.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod generated candidate: %w", err)
	}
	if _, err := candidate.Write(source); err != nil {
		return fmt.Errorf("write generated candidate: %w", err)
	}
	if err := candidate.Sync(); err != nil {
		return fmt.Errorf("sync generated candidate: %w", err)
	}
	if err := candidate.Close(); err != nil {
		return fmt.Errorf("close generated candidate: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := options.Verify(ctx, candidatePath); err != nil {
		return fmt.Errorf("verify generated candidate: %w", err)
	}
	if err := os.Rename(candidatePath, path); err != nil {
		return fmt.Errorf("replace generated source: %w", err)
	}
	keepCandidate = true
	return nil
}
