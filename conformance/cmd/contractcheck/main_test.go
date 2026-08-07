package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestPathParameterizedCommandsAcceptSecondContractSet(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	profilePath := filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json")
	readManifestPath := filepath.Join(root, "conformance", "contracts", "manifest.json")
	readOraclePath := filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "oracle.json")

	manifest, err := protocol.LoadManifest(readManifestPath)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(readOraclePath)
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	actual, err := protocol.LoadObservationSuite(readOraclePath)
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	for index := range manifest.Contracts {
		contractID := fmt.Sprintf("MOD-%03d", index+1)
		manifest.Contracts[index].ID = contractID
		manifest.Contracts[index].Scenario = fmt.Sprintf("django.write.command_%03d", index+1)
		manifest.Contracts[index].Status = protocol.ContractRed
		expected.Contracts[index].ID = contractID
		actual.Contracts[index].ID = contractID
	}

	temporary := t.TempDir()
	manifestPath := writeCanonical(t, temporary, "write-manifest.json", manifest)
	expectedPath := writeCanonical(t, temporary, "write-oracle.json", expected)
	actualPath := writeCanonical(t, temporary, "write-actual.json", actual)

	stdout, stderr, err := runGoCommand(".",
		"-profile", profilePath,
		"-manifest", manifestPath,
		"-suite", expectedPath,
	)
	if err != nil {
		t.Fatalf("contractcheck failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "11 ordered contracts") || stderr != "" {
		t.Fatalf("contractcheck output = stdout %q, stderr %q", stdout, stderr)
	}

	stdout, stderr, err = runGoCommand("../observationcmp",
		"-profile", profilePath,
		"-manifest", manifestPath,
		"-expected", expectedPath,
		"-actual", actualPath,
	)
	if err != nil {
		t.Fatalf("observationcmp failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "observations match for 11 contracts") || stderr != "" {
		t.Fatalf("observationcmp output = stdout %q, stderr %q", stdout, stderr)
	}

	changed := protocol.List(protocol.String("changed"))
	actual.Contracts[0].Result = &changed
	actualPath = writeCanonical(t, temporary, "write-actual-mutated.json", actual)
	stdout, stderr, err = runGoCommand("../observationcmp",
		"-profile", profilePath,
		"-manifest", manifestPath,
		"-expected", expectedPath,
		"-actual", actualPath,
	)
	if err == nil {
		t.Fatalf("mutated artifact produced a false green\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "MOD-001 result") || !strings.Contains(stderr, "difference(s)") {
		t.Fatalf("observationcmp mutation stderr = %q", stderr)
	}
}

func writeCanonical(t *testing.T, directory, name string, value any) string {
	t.Helper()
	contents, err := protocol.MarshalCanonical(value)
	if err != nil {
		t.Fatalf("MarshalCanonical(%s) error = %v", name, err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
	return path
}

func runGoCommand(packagePath string, arguments ...string) (string, string, error) {
	commandArguments := append([]string{"run", packagePath}, arguments...)
	command := exec.Command("go", commandArguments...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}
