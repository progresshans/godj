package main

import (
	"os"
	"path/filepath"
	"testing"

	operatorattestation "github.com/progresshans/godj/conformance/projectoperatorproduct/attestation"
	systemstateattestation "github.com/progresshans/godj/conformance/systemstate/attestation"
)

// testCaptureInputs supplies synthetic backend observations to adapter tests.
// It is not a PostgreSQL execution or live proof. The CI conformance command
// consumes only captures produced by its actual PostgreSQL service jobs.
func testCaptureInputs(t *testing.T, root string) (string, string) {
	t.Helper()
	read := func(parts ...string) []byte {
		contents, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}
	systemFixture, err := systemstateattestation.Decode(read("conformance", "systemstate", "attestation", "testdata", systemstateattestation.FileName))
	if err != nil {
		t.Fatal(err)
	}
	systemSource, err := systemstateattestation.ComputeSourceBinding(root)
	if err != nil {
		t.Fatal(err)
	}
	systemEvidence, err := systemstateattestation.New(systemFixture.BackendFacts(), systemSource)
	if err != nil {
		t.Fatal(err)
	}
	systemDocument, err := systemstateattestation.MarshalCanonical(systemEvidence)
	if err != nil {
		t.Fatal(err)
	}
	operatorFixture, err := operatorattestation.Decode(read("conformance", "projectoperatorproduct", "attestation", "testdata", operatorattestation.FileName))
	if err != nil {
		t.Fatal(err)
	}
	operatorSource, err := operatorattestation.ComputeSourceBinding(root)
	if err != nil {
		t.Fatal(err)
	}
	operatorEvidence, err := operatorattestation.New(operatorFixture.PostgreSQLFacts(), operatorFixture.SQLiteFacts(), operatorFixture.PostgreSQLFingerprint(), operatorSource)
	if err != nil {
		t.Fatal(err)
	}
	operatorDocument, err := operatorattestation.MarshalCanonical(operatorEvidence)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, document, checksum []byte) string {
		directory, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, document, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), checksum, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return write(systemstateattestation.FileName, systemDocument, systemstateattestation.ChecksumLine(systemDocument)),
		write(operatorattestation.FileName, operatorDocument, operatorattestation.ChecksumLine(operatorDocument))
}
