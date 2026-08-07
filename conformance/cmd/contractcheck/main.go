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
	suitePath := flag.String("suite", "", "optional path to an observation suite JSON")
	flag.Parse()

	if *profilePath == "" || *manifestPath == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: contractcheck -profile PROFILE.json -manifest MANIFEST.json [-suite SUITE.json]")
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
	if manifest.ProfileID != profile.ID {
		fail(fmt.Errorf("manifest profile_id %q does not match profile id %q", manifest.ProfileID, profile.ID))
	}

	if *suitePath == "" {
		fmt.Printf("valid profile %s and manifest with %d ordered contracts\n", profile.ID, len(manifest.Contracts))
		return
	}

	suite, err := protocol.LoadObservationSuite(*suitePath)
	if err != nil {
		fail(err)
	}
	if err := protocol.ValidateSuiteAgainst(profile, manifest, suite); err != nil {
		fail(err)
	}
	fmt.Printf("valid observation suite for profile %s with %d ordered contracts\n", profile.ID, len(suite.Contracts))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "contractcheck:", err)
	os.Exit(1)
}
