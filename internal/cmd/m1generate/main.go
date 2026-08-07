// Command m1generate is an M1-only project runner. It is intentionally not the
// public godj CLI while the CLI/library version contract remains open.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/examples/article/modeldef"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("m1generate", flag.ContinueOnError)
	check := flags.Bool("check", false, "verify committed generated output")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	schema, err := modeldef.Schema()
	if err != nil {
		return err
	}
	source, err := codegen.Generate("models", schema)
	if err != nil {
		return err
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("resolve generator source path")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	target := filepath.Join(repositoryRoot, "examples", "article", "models", "zz_godj_generated.go")
	options := codegen.WriteOptions{Check: *check}
	if !*check {
		options.Verify = func(ctx context.Context, candidate string) error {
			return verifyTargetPackage(ctx, repositoryRoot, "./examples/article/models", target, candidate)
		}
	}
	return codegen.WriteFile(ctx, target, source, options)
}

func verifyTargetPackage(ctx context.Context, repositoryRoot, packagePattern, target, candidate string) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	canonicalTarget, err := canonicalPath(target)
	if err != nil {
		return fmt.Errorf("resolve generated target path: %w", err)
	}
	canonicalCandidate, err := canonicalPath(candidate)
	if err != nil {
		return fmt.Errorf("resolve generated candidate path: %w", err)
	}
	overlay, err := os.CreateTemp("", "godj-codegen-overlay-*.json")
	if err != nil {
		return fmt.Errorf("create Go overlay: %w", err)
	}
	overlayPath := overlay.Name()
	defer func() { _ = os.Remove(overlayPath) }()
	contents, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: map[string]string{canonicalTarget: canonicalCandidate}})
	if err != nil {
		_ = overlay.Close()
		return fmt.Errorf("encode Go overlay: %w", err)
	}
	if _, err := overlay.Write(contents); err != nil {
		_ = overlay.Close()
		return fmt.Errorf("write Go overlay: %w", err)
	}
	if err := overlay.Close(); err != nil {
		return fmt.Errorf("close Go overlay: %w", err)
	}
	outputDirectory, err := os.MkdirTemp("", "godj-codegen-compile-*")
	if err != nil {
		return fmt.Errorf("create compile output directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(outputDirectory) }()
	command := exec.CommandContext(
		ctx,
		"go",
		"test",
		"-c",
		"-overlay="+overlayPath,
		"-o",
		filepath.Join(outputDirectory, "candidate.test"),
		packagePattern,
	)
	command.Dir = repositoryRoot
	command.Env = withoutGoWork(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compile generated target: %w\n%s", err, output)
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func withoutGoWork(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if len(entry) >= len("GOWORK=") && entry[:len("GOWORK=")] == "GOWORK=" {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GOWORK=off")
}
