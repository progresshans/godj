// Command godjcheck executes a GoDj observation adapter and compares its
// results with the selected locked Django oracle.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/progresshans/godj/conformance/internal/protocol"
	godjrunner "github.com/progresshans/godj/conformance/runners/godj"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("godjcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "", "path to the locked profile JSON")
	manifestPath := flags.String("manifest", "", "path to the contract manifest JSON")
	expectedPath := flags.String("expected", "", "path to the expected Django observation suite JSON")
	deviationExpectedPath := flags.String("deviation-expected", "", "optional path to a reviewed product deviation expectation JSON")
	actualOutputPath := flags.String("actual-output", "", "optional path for the generated GoDj observation suite JSON")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: godjcheck -profile PROFILE.json -manifest MANIFEST.json -expected ORACLE.json [-deviation-expected PRODUCT.json] [-actual-output ACTUAL.json]")
	}
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *profilePath == "" || *manifestPath == "" || *expectedPath == "" || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if ctx == nil {
		fmt.Fprintln(stderr, "godjcheck: context is nil")
		return 2
	}

	profile, err := protocol.LoadProfile(*profilePath)
	if err != nil {
		return reportFailure(stderr, err)
	}
	manifest, err := protocol.LoadManifest(*manifestPath)
	if err != nil {
		return reportFailure(stderr, err)
	}
	expected, err := protocol.LoadObservationSuite(*expectedPath)
	if err != nil {
		return reportFailure(stderr, err)
	}
	if _, err := protocol.Compare(profile, manifest, expected, expected); err != nil {
		return reportFailure(stderr, fmt.Errorf("locked Django oracle: %w", err))
	}
	manifestHasDeviation := false
	for _, contract := range manifest.Contracts {
		if contract.Status == protocol.ContractDeviation {
			manifestHasDeviation = true
			break
		}
	}
	if manifestHasDeviation && *deviationExpectedPath == "" {
		return reportFailure(stderr, fmt.Errorf("manifest contains deviation contracts but -deviation-expected is missing"))
	}
	comparisonManifest := manifest
	comparisonExpected := expected
	decision := ""
	if *deviationExpectedPath != "" {
		deviationExpected, err := protocol.LoadDeviationExpectation(*deviationExpectedPath)
		if err != nil {
			return reportFailure(stderr, err)
		}
		comparisonManifest, comparisonExpected, err = protocol.PrepareDeviationExpectation(
			profile,
			manifest,
			expected,
			deviationExpected,
			migrationExecutionDeviationPolicy(),
		)
		if err != nil {
			return reportFailure(stderr, err)
		}
		decision = deviationExpected.Decision
	}
	actual, err := godjrunner.Generate(ctx, profile, comparisonManifest)
	if err != nil {
		return reportFailure(stderr, err)
	}
	if *actualOutputPath != "" {
		contents, err := protocol.MarshalCanonical(actual)
		if err != nil {
			return reportFailure(stderr, fmt.Errorf("encode actual observations: %w", err))
		}
		if err := writeAtomic(ctx, *actualOutputPath, contents); err != nil {
			return reportFailure(stderr, fmt.Errorf("write actual observations: %w", err))
		}
	}

	differences, err := protocol.Compare(profile, comparisonManifest, comparisonExpected, actual)
	if err != nil {
		return reportFailure(stderr, err)
	}
	if len(differences) == 0 {
		if decision == "" {
			fmt.Fprintf(stdout, "GoDj observations match the locked Django oracle for %d contracts\n", len(manifest.Contracts))
		} else {
			fmt.Fprintf(stdout, "GoDj observations match the reviewed product expectation for %d contracts under %s\n", len(manifest.Contracts), decision)
		}
		return 0
	}
	for _, item := range differences {
		fmt.Fprintf(
			stderr,
			"%s %s: %s (expected %s, actual %s)\n",
			item.ContractID,
			item.Path,
			item.Message,
			item.Expected,
			item.Actual,
		)
	}
	fmt.Fprintf(stderr, "godjcheck: %d difference(s)\n", len(differences))
	return 1
}

func reportFailure(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "godjcheck:", err)
	return 2
}

func writeAtomic(ctx context.Context, path string, contents []byte) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}
