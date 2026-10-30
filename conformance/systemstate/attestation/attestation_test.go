package attestation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMarshalCanonicalLoadRoundTripPreservesFailureFacts(t *testing.T) {
	repositoryRoot := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		t.Fatalf("compute source binding: %v", err)
	}
	observed := ObservedFacts{
		WriterProcesses:   1,
		SameSchema:        false,
		BarrierLinearized: false,
		RestartPreserved:  false,
		DivergenceCount:   2,
		LossCount:         3,
		DriftCount:        4,
		SecretOccurrences: 5,
	}
	document, err := marshalPortableEvidence(observed, source)
	if err != nil {
		t.Fatalf("marshal capture: %v", err)
	}
	if len(document) == 0 || document[len(document)-1] != '\n' || bytes.Contains(document[:len(document)-1], []byte{'\n'}) {
		t.Fatalf("canonical document is not compact with one final LF: %q", document)
	}
	for _, forbidden := range []string{`"expected"`, `"pass"`, `"oracle"`, `"timestamp"`, `"commit"`, `"dsn"`} {
		if bytes.Contains(bytes.ToLower(document), []byte(forbidden)) {
			t.Fatalf("canonical document contains forbidden carrier %s", forbidden)
		}
	}

	attestationPath := writeEvidenceFiles(t, document)
	loaded, err := Load(repositoryRoot, attestationPath)
	if err != nil {
		t.Fatalf("load canonical capture: %v", err)
	}
	if got := loaded.BackendFacts().Observed(); got != observed {
		t.Fatalf("loaded failure observations = %+v, want %+v", got, observed)
	}
	if !loaded.SourceBinding().Equal(source) {
		t.Fatalf("loaded source binding = %d/%d/%s, want %d/%d/%s",
			loaded.SourceBinding().FileCount(), loaded.SourceBinding().PayloadBytes(), loaded.SourceBinding().SHA256(),
			source.FileCount(), source.PayloadBytes(), source.SHA256())
	}
}

func TestMarshalCaptureRequiresExactProducerProfile(t *testing.T) {
	repositoryRoot := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		t.Fatalf("compute source binding: %v", err)
	}
	document, err := MarshalCapture(successFacts(), source)
	exactProducer := runtime.Version() == ProducerGo && runtime.GOOS == ProducerOS && runtime.GOARCH == ProducerArch
	if exactProducer {
		if err != nil {
			t.Fatalf("MarshalCapture rejected exact producer: %v", err)
		}
		decoded, decodeErr := Decode(document)
		if decodeErr != nil {
			t.Fatalf("decode exact-producer capture: %v", decodeErr)
		}
		if got := decoded.BackendFacts().Observed(); got != successFacts() {
			t.Fatalf("captured facts = %+v, want %+v", got, successFacts())
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "exact go1.26.5 linux/amd64 producer") {
		t.Fatalf("MarshalCapture on %s/%s %s error = %v", runtime.GOOS, runtime.GOARCH, runtime.Version(), err)
	}
	if document != nil {
		t.Fatalf("MarshalCapture returned %d bytes for a rejected producer", len(document))
	}
}

func TestBackendFactsCopiesCaptureInput(t *testing.T) {
	input := ObservedFacts{
		WriterProcesses:   2,
		SameSchema:        true,
		BarrierLinearized: true,
		RestartPreserved:  true,
	}
	facts, err := NewBackendFacts(input)
	if err != nil {
		t.Fatalf("new backend facts: %v", err)
	}
	input.WriterProcesses = 0
	input.SameSchema = false
	if facts.WriterProcesses() != 2 || !facts.SameSchema() || !facts.BarrierLinearized() || !facts.RestartPreserved() {
		t.Fatalf("immutable backend facts changed with capture input: %+v", facts.Observed())
	}
	copyOut := facts.Observed()
	copyOut.LossCount = 12
	if facts.LossCount() != 0 {
		t.Fatalf("immutable backend facts changed through Observed copy: %+v", facts.Observed())
	}
}

func TestDecodeRejectsNonCanonicalMalformedAndWrongProfileDocuments(t *testing.T) {
	repositoryRoot := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		t.Fatalf("compute source binding: %v", err)
	}
	canonical, err := marshalPortableEvidence(successFacts(), source)
	if err != nil {
		t.Fatalf("marshal canonical capture: %v", err)
	}
	canonicalText := string(canonical)
	duplicateFormat := strings.Replace(
		canonicalText,
		`"kind":"`+Kind+`"`,
		`"format":"`+Format+`","kind":"`+Kind+`"`,
		1,
	)
	tests := map[string][]byte{
		"invalid UTF-8":         append(append([]byte(nil), canonical[:len(canonical)-1]...), 0xff),
		"unknown field":         []byte(strings.Replace(canonicalText, `"kind":`, `"expected":true,"kind":`, 1)),
		"nested unknown field":  []byte(strings.Replace(canonicalText, `"os":"linux"`, `"unexpected":true,"os":"linux"`, 1)),
		"duplicate field":       []byte(duplicateFormat),
		"trailing value":        append(append([]byte(nil), canonical...), []byte("{}\n")...),
		"leading whitespace":    append([]byte(" "), canonical...),
		"missing final LF":      append([]byte(nil), canonical[:len(canonical)-1]...),
		"wrong format":          []byte(strings.Replace(canonicalText, Format, Format+"-wrong", 1)),
		"wrong kind":            []byte(strings.Replace(canonicalText, Kind, Kind+"-wrong", 1)),
		"wrong contract":        []byte(strings.Replace(canonicalText, `"contract":"SYS-020"`, `"contract":"SYS-019"`, 1)),
		"wrong scenario":        []byte(strings.Replace(canonicalText, Scenario, Scenario+"_wrong", 1)),
		"wrong producer":        []byte(strings.Replace(canonicalText, ProducerGo, "go1.26.4", 1)),
		"wrong fingerprint":     []byte(strings.Replace(canonicalText, postgresServerVersionNum, "170009", 1)),
		"wrong source scope":    []byte(strings.Replace(canonicalText, SourceScope, SourceScope+"-wrong", 1)),
		"invalid source digest": []byte(strings.Replace(canonicalText, source.SHA256(), "abc", 1)),
		"negative fact":         []byte(strings.Replace(canonicalText, `"loss_count":0`, `"loss_count":-1`, 1)),
		"oversize":              bytes.Repeat([]byte{'x'}, MaxDocumentSize+1),
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(document); err == nil {
				t.Fatal("Decode accepted invalid document")
			}
		})
	}
}

func TestDecodeRejectsNestedDuplicateObjectName(t *testing.T) {
	repositoryRoot := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		t.Fatalf("compute source binding: %v", err)
	}
	canonical, err := marshalPortableEvidence(successFacts(), source)
	if err != nil {
		t.Fatalf("marshal canonical capture: %v", err)
	}
	duplicate := strings.Replace(
		string(canonical),
		`"os":"linux"`,
		`"os":"linux","os":"linux"`,
		1,
	)
	if _, err := Decode([]byte(duplicate)); err == nil || !strings.Contains(err.Error(), "invalid or duplicate object names") {
		t.Fatalf("Decode nested duplicate error = %v", err)
	}
}

func TestSourceBindingDecodeAcceptsExactBoundsAndRejectsOverflow(t *testing.T) {
	digest := strings.Repeat("a", 64)
	boundary, err := newSourceBinding(maxSourceFiles, maxSourceBytes, digest)
	if err != nil {
		t.Fatalf("new exact-boundary source binding: %v", err)
	}
	facts, err := NewBackendFacts(successFacts())
	if err != nil {
		t.Fatalf("new backend facts: %v", err)
	}
	evidence, err := New(facts, boundary)
	if err != nil {
		t.Fatalf("new exact-boundary evidence: %v", err)
	}
	canonical, err := MarshalCanonical(evidence)
	if err != nil {
		t.Fatalf("marshal exact-boundary evidence: %v", err)
	}
	decoded, err := Decode(canonical)
	if err != nil {
		t.Fatalf("Decode rejected exact source bounds: %v", err)
	}
	if !decoded.SourceBinding().Equal(boundary) {
		t.Fatalf("decoded source binding = %d/%d/%s, want exact boundary %d/%d/%s",
			decoded.SourceBinding().FileCount(), decoded.SourceBinding().PayloadBytes(), decoded.SourceBinding().SHA256(),
			boundary.FileCount(), boundary.PayloadBytes(), boundary.SHA256())
	}

	canonicalText := string(canonical)
	overflows := map[string]string{
		"file count": strings.Replace(
			canonicalText,
			`"file_count":4096`,
			`"file_count":4097`,
			1,
		),
		"payload bytes": strings.Replace(
			canonicalText,
			`"payload_bytes":134217728`,
			`"payload_bytes":134217729`,
			1,
		),
		"JSON integer": strings.Replace(
			canonicalText,
			`"file_count":4096`,
			`"file_count":9223372036854775808`,
			1,
		),
	}
	for name, document := range overflows {
		t.Run(name, func(t *testing.T) {
			if document == canonicalText {
				t.Fatal("overflow mutation did not change the canonical document")
			}
			if _, err := Decode([]byte(document)); err == nil {
				t.Fatal("Decode accepted an overflowing source binding")
			}
		})
	}
}

func TestLoadRejectsChecksumFilenameCanonicalAndStaleSourceFailures(t *testing.T) {
	repositoryRoot := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		t.Fatalf("compute source binding: %v", err)
	}
	document, err := marshalPortableEvidence(successFacts(), source)
	if err != nil {
		t.Fatalf("marshal canonical capture: %v", err)
	}

	t.Run("wrong filename", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, "alternate.json")
		writeTestFile(t, path, document, 0o644)
		if _, err := Load(repositoryRoot, path); err == nil {
			t.Fatal("Load accepted alternate attestation filename")
		}
	})

	t.Run("missing checksum", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, FileName)
		writeTestFile(t, path, document, 0o644)
		if _, err := Load(repositoryRoot, path); err == nil {
			t.Fatal("Load accepted missing checksum")
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		path := writeEvidenceFiles(t, document)
		writeTestFile(t, filepath.Join(filepath.Dir(path), ChecksumFileName), []byte(strings.Repeat("0", 64)+"  "+FileName+"\n"), 0o644)
		if _, err := Load(repositoryRoot, path); err == nil {
			t.Fatal("Load accepted mismatched checksum")
		}
	})

	t.Run("extra checksum entry", func(t *testing.T) {
		path := writeEvidenceFiles(t, document)
		checksumPath := filepath.Join(filepath.Dir(path), ChecksumFileName)
		extra := append(ChecksumLine(document), []byte(strings.Repeat("0", 64)+"  other.json\n")...)
		writeTestFile(t, checksumPath, extra, 0o644)
		if _, err := Load(repositoryRoot, path); err == nil {
			t.Fatal("Load accepted extra checksum entry")
		}
	})

	t.Run("checksummed noncanonical bytes", func(t *testing.T) {
		noncanonical := append([]byte(" "), document...)
		path := writeEvidenceFiles(t, noncanonical)
		if _, err := Load(repositoryRoot, path); err == nil {
			t.Fatal("Load accepted checksummed noncanonical evidence")
		}
	})

	t.Run("stale source", func(t *testing.T) {
		path := writeEvidenceFiles(t, document)
		writeTestFile(t, filepath.Join(repositoryRoot, "systemstate", "new.go"), []byte("package systemstate\n"), 0o644)
		if _, err := Load(repositoryRoot, path); err == nil || !strings.Contains(err.Error(), "source binding is stale") {
			t.Fatalf("Load stale source error = %v", err)
		}
	})
}

func TestChecksumLineIsStrictLowercaseInventory(t *testing.T) {
	payload := []byte("evidence\n")
	digest := sha256.Sum256(payload)
	want := hex.EncodeToString(digest[:]) + "  " + FileName + "\n"
	if got := string(ChecksumLine(payload)); got != want {
		t.Fatalf("ChecksumLine = %q, want %q", got, want)
	}
}

func successFacts() ObservedFacts {
	return ObservedFacts{
		WriterProcesses:   2,
		SameSchema:        true,
		BarrierLinearized: true,
		RestartPreserved:  true,
	}
}

func marshalPortableEvidence(observed ObservedFacts, source SourceBinding) ([]byte, error) {
	facts, err := NewBackendFacts(observed)
	if err != nil {
		return nil, err
	}
	evidence, err := New(facts, source)
	if err != nil {
		return nil, err
	}
	return MarshalCanonical(evidence)
}

func writeEvidenceFiles(t *testing.T, document []byte) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, FileName)
	writeTestFile(t, path, document, 0o644)
	writeTestFile(t, filepath.Join(directory, ChecksumFileName), ChecksumLine(document), 0o644)
	return path
}

func writeTestFile(t *testing.T, path string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create test parent directory: %v", err)
	}
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatalf("write test file %s: %v", filepath.Base(path), err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod test file %s: %v", filepath.Base(path), err)
	}
}
