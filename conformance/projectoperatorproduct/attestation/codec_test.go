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

func TestMarshalCanonicalLoadRoundTripPreservesObservedFacts(t *testing.T) {
	repository := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatalf("compute source binding: %v", err)
	}
	postgresqlObserved := failureFacts(BackendPostgreSQL)
	sqliteObserved := failureFacts(BackendSQLite)
	document, err := marshalPortableEvidence(postgresqlObserved, sqliteObserved, ExpectedPostgreSQLFingerprint(), source)
	if err != nil {
		t.Fatalf("marshal portable evidence: %v", err)
	}
	if len(document) == 0 || document[len(document)-1] != '\n' || bytes.Contains(document[:len(document)-1], []byte{'\n'}) {
		t.Fatalf("canonical document is not compact with one final LF: %q", document)
	}
	for _, forbidden := range []string{`"expected"`, `"pass"`, `"oracle"`, `"timestamp"`, `"commit"`, `"dsn"`, `"username"`, `"password"`, `"pid"`} {
		if bytes.Contains(bytes.ToLower(document), []byte(forbidden)) {
			t.Fatalf("canonical document contains forbidden carrier %s", forbidden)
		}
	}

	attestationPath := writeEvidenceFiles(t, document)
	loaded, err := Load(repository, attestationPath)
	if err != nil {
		t.Fatalf("load canonical evidence: %v", err)
	}
	if got := loaded.PostgreSQLFacts().Observed(); got != postgresqlObserved {
		t.Fatalf("loaded PostgreSQL observations = %+v, want %+v", got, postgresqlObserved)
	}
	if got := loaded.SQLiteFacts().Observed(); got != sqliteObserved {
		t.Fatalf("loaded SQLite observations = %+v, want %+v", got, sqliteObserved)
	}
	if got := loaded.PostgreSQLFingerprint(); got != ExpectedPostgreSQLFingerprint() {
		t.Fatalf("loaded PostgreSQL fingerprint = %+v", got)
	}
	if !loaded.SourceBinding().Equal(source) {
		t.Fatalf("loaded source binding = %d/%d/%s, want %d/%d/%s",
			loaded.SourceBinding().FileCount(), loaded.SourceBinding().PayloadBytes(), loaded.SourceBinding().SHA256(),
			source.FileCount(), source.PayloadBytes(), source.SHA256())
	}
}

func TestMarshalCaptureRequiresExactProducerProfile(t *testing.T) {
	repository := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal(err)
	}
	document, err := MarshalCapture(
		successFacts(BackendPostgreSQL),
		successFacts(BackendSQLite),
		ExpectedPostgreSQLFingerprint(),
		source,
	)
	exact := runtime.Version() == ProducerGo && runtime.GOOS == ProducerOS && runtime.GOARCH == ProducerArch
	if exact {
		if err != nil {
			t.Fatalf("MarshalCapture rejected exact producer: %v", err)
		}
		decoded, decodeErr := Decode(document)
		if decodeErr != nil || decoded.PostgreSQLFacts().Observed() != successFacts(BackendPostgreSQL) ||
			decoded.SQLiteFacts().Observed() != successFacts(BackendSQLite) {
			t.Fatalf("exact producer capture = (%+v, %+v, %v)", decoded.PostgreSQLFacts().Observed(), decoded.SQLiteFacts().Observed(), decodeErr)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "exact go1.26.5 linux/amd64 producer") {
		t.Fatalf("MarshalCapture on %s/%s %s error = %v", runtime.GOOS, runtime.GOARCH, runtime.Version(), err)
	}
	if document != nil {
		t.Fatalf("MarshalCapture returned %d bytes for rejected producer", len(document))
	}
}

func TestProductFactsCopiesInputAndPreservesFailureShape(t *testing.T) {
	input := failureFacts(BackendPostgreSQL)
	facts, err := NewProductFacts(input)
	if err != nil {
		t.Fatal(err)
	}
	input.ProvisionProcesses = 0
	input.AdminAuthenticated = true
	if got := facts.Observed(); got != failureFacts(BackendPostgreSQL) {
		t.Fatalf("immutable facts changed with input: %+v", got)
	}
	copyOut := facts.Observed()
	copyOut.RawSecretOccurrences = 0
	if facts.Observed().RawSecretOccurrences != failureFacts(BackendPostgreSQL).RawSecretOccurrences {
		t.Fatal("immutable facts changed through returned copy")
	}
}

func TestInvalidBackendValueIsNotReflectedInError(t *testing.T) {
	facts := successFacts(BackendPostgreSQL)
	const marker = "crafted-backend-sensitive-marker"
	facts.Backend = marker
	_, err := NewProductFacts(facts)
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("invalid backend error reflected input: %v", err)
	}
}

func TestCodecPreservesEveryContractFacingFailureMutation(t *testing.T) {
	repository := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ObservedFacts)
	}{
		{name: "provision processes", mutate: func(facts *ObservedFacts) { facts.ProvisionProcesses++ }},
		{name: "runtime processes", mutate: func(facts *ObservedFacts) { facts.RuntimeProcesses++ }},
		{name: "distinct processes", mutate: func(facts *ObservedFacts) { facts.DistinctProcesses++ }},
		{name: "provision calls", mutate: func(facts *ObservedFacts) { facts.ProvisionCalls++ }},
		{name: "credential rows", mutate: func(facts *ObservedFacts) { facts.CredentialRows++ }},
		{name: "provisioned", mutate: func(facts *ObservedFacts) { facts.Provisioned = false }},
		{name: "admin authenticated", mutate: func(facts *ObservedFacts) { facts.AdminAuthenticated = false }},
		{name: "api authenticated", mutate: func(facts *ObservedFacts) { facts.APIAuthenticated = false }},
		{name: "distinct process restart", mutate: func(facts *ObservedFacts) { facts.DistinctProcessRestart = false }},
		{name: "provision process distinct", mutate: func(facts *ObservedFacts) { facts.ProvisionProcessDistinctFromRuntime = false }},
		{name: "restart raw secret input", mutate: func(facts *ObservedFacts) { facts.RestartRawSecretInput = true }},
		{name: "restart state loss", mutate: func(facts *ObservedFacts) { facts.RestartStateLoss++ }},
		{name: "schema drift", mutate: func(facts *ObservedFacts) { facts.SchemaDrift = true }},
		{name: "raw secret occurrences", mutate: func(facts *ObservedFacts) { facts.RawSecretOccurrences++ }},
	}
	for _, backend := range []string{BackendPostgreSQL, BackendSQLite} {
		for _, test := range tests {
			t.Run(backend+"/"+test.name, func(t *testing.T) {
				postgresqlWant := successFacts(BackendPostgreSQL)
				sqliteWant := successFacts(BackendSQLite)
				if backend == BackendPostgreSQL {
					test.mutate(&postgresqlWant)
				} else {
					test.mutate(&sqliteWant)
				}
				document, err := marshalPortableEvidence(postgresqlWant, sqliteWant, ExpectedPostgreSQLFingerprint(), source)
				if err != nil {
					t.Fatalf("marshal failure-shaped evidence: %v", err)
				}
				decoded, err := Decode(document)
				if err != nil {
					t.Fatalf("decode failure-shaped evidence: %v", err)
				}
				if got := decoded.PostgreSQLFacts().Observed(); got != postgresqlWant {
					t.Fatalf("decoded PostgreSQL facts = %+v, want exact %+v", got, postgresqlWant)
				}
				if got := decoded.SQLiteFacts().Observed(); got != sqliteWant {
					t.Fatalf("decoded SQLite facts = %+v, want exact %+v", got, sqliteWant)
				}
			})
		}
	}
}

func TestValidatePostgreSQLFingerprintRejectsEveryDrift(t *testing.T) {
	if err := ValidatePostgreSQLFingerprint(ExpectedPostgreSQLFingerprint()); err != nil {
		t.Fatalf("exact fingerprint rejected: %v", err)
	}
	drifted := ExpectedPostgreSQLFingerprint()
	drifted.ServerVersionNum = "170009"
	if err := ValidatePostgreSQLFingerprint(drifted); err == nil {
		t.Fatal("version drift accepted")
	}
	drifted = ExpectedPostgreSQLFingerprint()
	drifted.TimeZone = "Etc/UTC"
	if err := ValidatePostgreSQLFingerprint(drifted); err == nil {
		t.Fatal("timezone drift accepted")
	}
}

func TestDecodeRejectsMalformedNonCanonicalAndWrongProfileDocuments(t *testing.T) {
	repository := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := marshalPortableEvidence(successFacts(BackendPostgreSQL), successFacts(BackendSQLite), ExpectedPostgreSQLFingerprint(), source)
	if err != nil {
		t.Fatal(err)
	}
	text := string(canonical)
	duplicate := strings.Replace(text, `"kind":"`+Kind+`"`, `"format":"`+Format+`","kind":"`+Kind+`"`, 1)
	swappedBackends := strings.Replace(text, `"backend":"`+BackendPostgreSQL+`"`, `"backend":"temporary"`, 1)
	swappedBackends = strings.Replace(swappedBackends, `"backend":"`+BackendSQLite+`"`, `"backend":"`+BackendPostgreSQL+`"`, 1)
	swappedBackends = strings.Replace(swappedBackends, `"backend":"temporary"`, `"backend":"`+BackendSQLite+`"`, 1)
	tests := map[string][]byte{
		"invalid utf8":          append(append([]byte(nil), canonical[:len(canonical)-1]...), 0xff),
		"unknown field":         []byte(strings.Replace(text, `"kind":`, `"unexpected":true,"kind":`, 1)),
		"nested unknown":        []byte(strings.Replace(text, `"os":"linux"`, `"unexpected":true,"os":"linux"`, 1)),
		"duplicate":             []byte(duplicate),
		"trailing value":        append(append([]byte(nil), canonical...), []byte("{}\n")...),
		"leading whitespace":    append([]byte(" "), canonical...),
		"missing final lf":      append([]byte(nil), canonical[:len(canonical)-1]...),
		"wrong format":          []byte(strings.Replace(text, Format, Format+"-wrong", 1)),
		"wrong kind":            []byte(strings.Replace(text, Kind, Kind+"-wrong", 1)),
		"wrong contract":        []byte(strings.Replace(text, `"contract":"SYS-029"`, `"contract":"SYS-020"`, 1)),
		"wrong scenario":        []byte(strings.Replace(text, Scenario, Scenario+"-wrong", 1)),
		"wrong producer":        []byte(strings.Replace(text, ProducerGo, "go1.26.4", 1)),
		"wrong postgres":        []byte(strings.Replace(text, `"server_version_num":"170010"`, `"server_version_num":"170009"`, 1)),
		"wrong source scope":    []byte(strings.Replace(text, SourceScope, SourceScope+"-wrong", 1)),
		"wrong required cases":  []byte(strings.Replace(text, `"required_backend_cases":2`, `"required_backend_cases":1`, 1)),
		"swapped backend order": []byte(swappedBackends),
		"invalid source digest": []byte(strings.Replace(text, source.SHA256(), "abc", 1)),
		"negative fact":         []byte(strings.Replace(text, `"credential_rows":1`, `"credential_rows":-1`, 1)),
		"oversize":              bytes.Repeat([]byte{'x'}, MaxDocumentSize+1),
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(document); err == nil {
				t.Fatal("Decode accepted invalid evidence")
			}
		})
	}
}

func TestDecodeRejectsNestedDuplicateObjectName(t *testing.T) {
	repository := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := marshalPortableEvidence(successFacts(BackendPostgreSQL), successFacts(BackendSQLite), ExpectedPostgreSQLFingerprint(), source)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := strings.Replace(string(canonical), `"os":"linux"`, `"os":"linux","os":"linux"`, 1)
	if _, err := Decode([]byte(duplicate)); err == nil || !strings.Contains(err.Error(), "invalid or duplicate object names") {
		t.Fatalf("nested duplicate error = %v", err)
	}
}

func TestSourceBindingDecodeAcceptsExactBoundsAndRejectsOverflow(t *testing.T) {
	boundary, err := newSourceBinding(maxSourceFiles, maxSourceBytes, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	document, err := marshalPortableEvidence(successFacts(BackendPostgreSQL), successFacts(BackendSQLite), ExpectedPostgreSQLFingerprint(), boundary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(document); err != nil {
		t.Fatalf("exact source bounds rejected: %v", err)
	}
	text := string(document)
	mutations := []string{
		strings.Replace(text, `"file_count":8192`, `"file_count":8193`, 1),
		strings.Replace(text, `"payload_bytes":268435456`, `"payload_bytes":268435457`, 1),
		strings.Replace(text, `"file_count":8192`, `"file_count":9223372036854775808`, 1),
	}
	for _, mutation := range mutations {
		if mutation == text {
			t.Fatal("overflow mutation did not change document")
		}
		if _, err := Decode([]byte(mutation)); err == nil {
			t.Fatal("Decode accepted overflowing source binding")
		}
	}
}

func TestLoadRejectsChecksumFilenameCanonicalAndStaleSourceFailures(t *testing.T) {
	repository := seedSourceRepository(t)
	source, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal(err)
	}
	document, err := marshalPortableEvidence(successFacts(BackendPostgreSQL), successFacts(BackendSQLite), ExpectedPostgreSQLFingerprint(), source)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("wrong filename", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "alternate.json")
		writeTestFile(t, path, document, 0o644)
		if _, err := Load(repository, path); err == nil {
			t.Fatal("Load accepted alternate filename")
		}
	})
	t.Run("missing checksum", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), FileName)
		writeTestFile(t, path, document, 0o644)
		if _, err := Load(repository, path); err == nil {
			t.Fatal("Load accepted missing checksum")
		}
	})
	t.Run("checksum mismatch", func(t *testing.T) {
		path := writeEvidenceFiles(t, document)
		writeTestFile(t, filepath.Join(filepath.Dir(path), ChecksumFileName), []byte(strings.Repeat("0", 64)+"  "+FileName+"\n"), 0o644)
		if _, err := Load(repository, path); err == nil {
			t.Fatal("Load accepted checksum mismatch")
		}
	})
	t.Run("extra checksum entry", func(t *testing.T) {
		path := writeEvidenceFiles(t, document)
		extra := append(ChecksumLine(document), []byte(strings.Repeat("0", 64)+"  other.json\n")...)
		writeTestFile(t, filepath.Join(filepath.Dir(path), ChecksumFileName), extra, 0o644)
		if _, err := Load(repository, path); err == nil {
			t.Fatal("Load accepted extra checksum inventory entry")
		}
	})
	t.Run("checksummed noncanonical", func(t *testing.T) {
		path := writeEvidenceFiles(t, append([]byte(" "), document...))
		if _, err := Load(repository, path); err == nil {
			t.Fatal("Load accepted checksummed noncanonical evidence")
		}
	})
	t.Run("stale source", func(t *testing.T) {
		path := writeEvidenceFiles(t, document)
		writeTestFile(t, filepath.Join(repository, "systemstate", "new.go"), []byte("package systemstate\n"), 0o644)
		if _, err := Load(repository, path); err == nil || !strings.Contains(err.Error(), "source binding is stale") {
			t.Fatalf("stale source error = %v", err)
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

func successFacts(backend string) ObservedFacts {
	return ObservedFacts{
		Backend:                             backend,
		ProvisionProcesses:                  1,
		RuntimeProcesses:                    2,
		DistinctProcesses:                   3,
		ProvisionCalls:                      1,
		CredentialRows:                      1,
		Provisioned:                         true,
		AdminAuthenticated:                  true,
		APIAuthenticated:                    true,
		DistinctProcessRestart:              true,
		ProvisionProcessDistinctFromRuntime: true,
		RestartRawSecretInput:               false,
		RestartStateLoss:                    0,
		SchemaDrift:                         false,
		RawSecretOccurrences:                0,
	}
}

func failureFacts(backend string) ObservedFacts {
	return ObservedFacts{
		Backend:                             backend,
		ProvisionProcesses:                  2,
		RuntimeProcesses:                    3,
		DistinctProcesses:                   4,
		ProvisionCalls:                      5,
		CredentialRows:                      6,
		Provisioned:                         false,
		AdminAuthenticated:                  false,
		APIAuthenticated:                    false,
		DistinctProcessRestart:              false,
		ProvisionProcessDistinctFromRuntime: false,
		RestartRawSecretInput:               true,
		RestartStateLoss:                    7,
		SchemaDrift:                         true,
		RawSecretOccurrences:                8,
	}
}

func marshalPortableEvidence(
	postgresqlObserved, sqliteObserved ObservedFacts,
	postgresql PostgreSQLFingerprint,
	source SourceBinding,
) ([]byte, error) {
	postgresqlFacts, err := NewProductFacts(postgresqlObserved)
	if err != nil {
		return nil, err
	}
	sqliteFacts, err := NewProductFacts(sqliteObserved)
	if err != nil {
		return nil, err
	}
	evidence, err := New(postgresqlFacts, sqliteFacts, postgresql, source)
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
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
