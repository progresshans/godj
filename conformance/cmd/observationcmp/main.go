package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func main() {
	profilePath := flag.String("profile", "", "path to the locked profile JSON")
	manifestPath := flag.String("manifest", "", "path to the contract manifest JSON")
	expectedPath := flag.String("expected", "", "path to the expected observation suite JSON")
	actualPath := flag.String("actual", "", "path to the actual observation suite JSON")
	flag.Parse()

	if *profilePath == "" || *manifestPath == "" || *expectedPath == "" || *actualPath == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: observationcmp -profile PROFILE.json -manifest MANIFEST.json -expected EXPECTED.json -actual ACTUAL.json")
		os.Exit(2)
	}

	profile, err := protocol.LoadProfile(*profilePath)
	if err != nil {
		fail(err)
	}
	manifest, err := protocol.LoadManifest(*manifestPath)
	if err != nil {
		fail(err)
	}
	expected, err := protocol.LoadObservationSuite(*expectedPath)
	if err != nil {
		fail(err)
	}
	actual, err := protocol.LoadObservationSuite(*actualPath)
	if err != nil {
		fail(err)
	}

	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		fail(err)
	}
	if len(differences) == 0 {
		fmt.Printf("observations match for %d contracts\n", len(manifest.Contracts))
		return
	}

	for _, item := range differences {
		fmt.Fprintf(
			os.Stderr,
			"%s %s: %s (expected %s, actual %s)\n",
			item.ContractID,
			item.Path,
			item.Message,
			item.Expected,
			item.Actual,
		)
	}
	fmt.Fprintf(os.Stderr, "observationcmp: %d difference(s)\n", len(differences))
	os.Exit(1)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "observationcmp:", err)
	os.Exit(2)
}
