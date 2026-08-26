package attestation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"unicode/utf8"
)

const maxChecksumSize = 256

const (
	postgresServerVersionNum          = "170010"
	postgresServerEncoding            = "UTF8"
	postgresClientEncoding            = "UTF8"
	postgresLocaleProvider            = "c"
	postgresLocale                    = "<null>"
	postgresCollation                 = "C"
	postgresCharacterType             = "C"
	postgresTimeZone                  = "UTC"
	postgresStandardConformingStrings = "on"
	postgresSynchronousCommit         = "on"
	postgresDefaultIsolation          = "read committed"
	postgresDefaultReadOnly           = "off"
	postgresDefaultDeferrable         = "off"
	postgresFSync                     = "on"
	postgresFullPageWrites            = "on"
	postgresSessionReplicationRole    = "origin"
)

type wireEvidence struct {
	Format     string            `json:"format"`
	Kind       string            `json:"kind"`
	Contract   string            `json:"contract"`
	Scenario   string            `json:"scenario"`
	Producer   wireProducer      `json:"producer"`
	PostgreSQL wirePostgreSQL    `json:"postgresql"`
	Source     wireSourceBinding `json:"source_binding"`
	Facts      wireBackendFacts  `json:"facts"`
}

type wireProducer struct {
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Harness string `json:"harness"`
}

type wirePostgreSQL struct {
	ServerVersionNum          string `json:"server_version_num"`
	ServerEncoding            string `json:"server_encoding"`
	ClientEncoding            string `json:"client_encoding"`
	LocaleProvider            string `json:"locale_provider"`
	Locale                    string `json:"locale"`
	Collation                 string `json:"collation"`
	CharacterType             string `json:"character_type"`
	TimeZone                  string `json:"time_zone"`
	StandardConformingStrings string `json:"standard_conforming_strings"`
	SynchronousCommit         string `json:"synchronous_commit"`
	DefaultIsolation          string `json:"default_transaction_isolation"`
	DefaultReadOnly           string `json:"default_transaction_read_only"`
	DefaultDeferrable         string `json:"default_transaction_deferrable"`
	FSync                     string `json:"fsync"`
	FullPageWrites            string `json:"full_page_writes"`
	SessionReplicationRole    string `json:"session_replication_role"`
}

type wireSourceBinding struct {
	Scope        string `json:"scope"`
	FileCount    int64  `json:"file_count"`
	PayloadBytes int64  `json:"payload_bytes"`
	SHA256       string `json:"sha256"`
}

type wireBackendFacts struct {
	WriterProcesses   int64 `json:"writer_processes"`
	SameSchema        bool  `json:"same_schema"`
	BarrierLinearized bool  `json:"barrier_linearized"`
	RestartPreserved  bool  `json:"restart_preserved"`
	DivergenceCount   int64 `json:"divergence_count"`
	LossCount         int64 `json:"loss_count"`
	DriftCount        int64 `json:"drift_count"`
	SecretOccurrences int64 `json:"secret_occurrences"`
}

// MarshalCapture copies observed facts into an immutable value and returns the
// canonical bytes that a live PostgreSQL sentinel may write to a temporary
// capture path. Failure observations remain unchanged.
func MarshalCapture(observed ObservedFacts, source SourceBinding) ([]byte, error) {
	if runtime.Version() != ProducerGo || runtime.GOOS != ProducerOS || runtime.GOARCH != ProducerArch {
		return nil, errors.New("PostgreSQL live attestation capture requires the exact go1.26.5 linux/amd64 producer")
	}
	facts, err := NewBackendFacts(observed)
	if err != nil {
		return nil, fmt.Errorf("capture PostgreSQL backend facts: %w", err)
	}
	evidence, err := New(facts, source)
	if err != nil {
		return nil, err
	}
	return MarshalCanonical(evidence)
}

// MarshalCanonical returns compact UTF-8 JSON with exactly one final LF.
func MarshalCanonical(evidence Evidence) ([]byte, error) {
	if err := evidence.validate(); err != nil {
		return nil, err
	}
	wire := evidenceToWire(evidence)
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, errors.New("marshal PostgreSQL live attestation")
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxDocumentSize {
		return nil, errors.New("canonical PostgreSQL live attestation exceeds its size limit")
	}
	return encoded, nil
}

// Decode validates a complete canonical attestation document. It rejects
// duplicate object names before decoding, unknown fields, trailing values,
// non-canonical bytes, and every profile other than the fixed SYS-020 profile.
func Decode(document []byte) (Evidence, error) {
	if len(document) == 0 {
		return Evidence{}, io.ErrUnexpectedEOF
	}
	if len(document) > MaxDocumentSize {
		return Evidence{}, errors.New("PostgreSQL live attestation exceeds its size limit")
	}
	if !utf8.Valid(document) {
		return Evidence{}, errors.New("PostgreSQL live attestation is not valid UTF-8")
	}
	if err := rejectDuplicateObjectNames(document); err != nil {
		return Evidence{}, errors.New("PostgreSQL live attestation has invalid or duplicate object names")
	}

	var wire wireEvidence
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Evidence{}, errors.New("PostgreSQL live attestation has an invalid JSON shape")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Evidence{}, errors.New("PostgreSQL live attestation contains a trailing JSON value")
		}
		return Evidence{}, errors.New("PostgreSQL live attestation contains invalid trailing data")
	}
	evidence, err := wire.toEvidence()
	if err != nil {
		return Evidence{}, err
	}
	canonical, err := MarshalCanonical(evidence)
	if err != nil {
		return Evidence{}, err
	}
	if !bytes.Equal(document, canonical) {
		return Evidence{}, errors.New("PostgreSQL live attestation is not canonical JSON")
	}
	return evidence, nil
}

// Load verifies the fixed sibling SHA256SUMS entry, decodes the canonical
// evidence, and compares its binding with the current repository source.
func Load(repositoryRoot, attestationPath string) (Evidence, error) {
	if filepath.Base(attestationPath) != FileName {
		return Evidence{}, fmt.Errorf("PostgreSQL live attestation filename must be %q", FileName)
	}
	document, err := readBoundedRegularFile(attestationPath, MaxDocumentSize)
	if err != nil {
		return Evidence{}, fmt.Errorf("read PostgreSQL live attestation: %w", err)
	}
	checksumPath := filepath.Join(filepath.Dir(attestationPath), ChecksumFileName)
	checksum, err := readBoundedRegularFile(checksumPath, maxChecksumSize)
	if err != nil {
		return Evidence{}, fmt.Errorf("read PostgreSQL live attestation checksum: %w", err)
	}
	wantedChecksum := ChecksumLine(document)
	if !bytes.Equal(checksum, wantedChecksum) {
		return Evidence{}, errors.New("PostgreSQL live attestation checksum inventory does not match")
	}
	evidence, err := Decode(document)
	if err != nil {
		return Evidence{}, err
	}
	currentSource, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		return Evidence{}, fmt.Errorf("compute current PostgreSQL attestation source binding: %w", err)
	}
	if !evidence.SourceBinding().Equal(currentSource) {
		return Evidence{}, errors.New("PostgreSQL live attestation source binding is stale")
	}
	return evidence, nil
}

// ChecksumLine returns the only SHA256SUMS representation accepted by Load.
func ChecksumLine(document []byte) []byte {
	digest := sha256.Sum256(document)
	line := hex.EncodeToString(digest[:]) + "  " + FileName + "\n"
	return []byte(line)
}

func evidenceToWire(evidence Evidence) wireEvidence {
	observed := evidence.BackendFacts().Observed()
	source := evidence.SourceBinding()
	return wireEvidence{
		Format:   Format,
		Kind:     Kind,
		Contract: Contract,
		Scenario: Scenario,
		Producer: wireProducer{
			Go:      ProducerGo,
			OS:      ProducerOS,
			Arch:    ProducerArch,
			Harness: HarnessVersion,
		},
		PostgreSQL: expectedPostgreSQLFingerprint(),
		Source: wireSourceBinding{
			Scope:        SourceScope,
			FileCount:    source.FileCount(),
			PayloadBytes: source.PayloadBytes(),
			SHA256:       source.SHA256(),
		},
		Facts: wireBackendFacts{
			WriterProcesses:   observed.WriterProcesses,
			SameSchema:        observed.SameSchema,
			BarrierLinearized: observed.BarrierLinearized,
			RestartPreserved:  observed.RestartPreserved,
			DivergenceCount:   observed.DivergenceCount,
			LossCount:         observed.LossCount,
			DriftCount:        observed.DriftCount,
			SecretOccurrences: observed.SecretOccurrences,
		},
	}
}

func (wire wireEvidence) toEvidence() (Evidence, error) {
	if wire.Format != Format || wire.Kind != Kind || wire.Contract != Contract || wire.Scenario != Scenario {
		return Evidence{}, errors.New("PostgreSQL live attestation has the wrong fixed identity")
	}
	if wire.Producer != (wireProducer{Go: ProducerGo, OS: ProducerOS, Arch: ProducerArch, Harness: HarnessVersion}) {
		return Evidence{}, errors.New("PostgreSQL live attestation has the wrong producer profile")
	}
	if wire.PostgreSQL != expectedPostgreSQLFingerprint() {
		return Evidence{}, errors.New("PostgreSQL live attestation has the wrong PostgreSQL 17.10 fingerprint")
	}
	if wire.Source.Scope != SourceScope {
		return Evidence{}, errors.New("PostgreSQL live attestation has the wrong source scope")
	}
	source, err := newSourceBinding(wire.Source.FileCount, wire.Source.PayloadBytes, wire.Source.SHA256)
	if err != nil {
		return Evidence{}, fmt.Errorf("validate PostgreSQL live attestation source binding: %w", err)
	}
	facts, err := NewBackendFacts(ObservedFacts{
		WriterProcesses:   wire.Facts.WriterProcesses,
		SameSchema:        wire.Facts.SameSchema,
		BarrierLinearized: wire.Facts.BarrierLinearized,
		RestartPreserved:  wire.Facts.RestartPreserved,
		DivergenceCount:   wire.Facts.DivergenceCount,
		LossCount:         wire.Facts.LossCount,
		DriftCount:        wire.Facts.DriftCount,
		SecretOccurrences: wire.Facts.SecretOccurrences,
	})
	if err != nil {
		return Evidence{}, fmt.Errorf("validate PostgreSQL live attestation facts: %w", err)
	}
	return New(facts, source)
}

func expectedPostgreSQLFingerprint() wirePostgreSQL {
	return wirePostgreSQL{
		ServerVersionNum:          postgresServerVersionNum,
		ServerEncoding:            postgresServerEncoding,
		ClientEncoding:            postgresClientEncoding,
		LocaleProvider:            postgresLocaleProvider,
		Locale:                    postgresLocale,
		Collation:                 postgresCollation,
		CharacterType:             postgresCharacterType,
		TimeZone:                  postgresTimeZone,
		StandardConformingStrings: postgresStandardConformingStrings,
		SynchronousCommit:         postgresSynchronousCommit,
		DefaultIsolation:          postgresDefaultIsolation,
		DefaultReadOnly:           postgresDefaultReadOnly,
		DefaultDeferrable:         postgresDefaultDeferrable,
		FSync:                     postgresFSync,
		FullPageWrites:            postgresFullPageWrites,
		SessionReplicationRole:    postgresSessionReplicationRole,
	}
}

func rejectDuplicateObjectNames(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON token")
		}
		return errors.New("invalid trailing JSON")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("JSON object name is not a string")
			}
			if _, duplicate := seen[name]; duplicate {
				return errors.New("duplicate JSON object name")
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("JSON object has an invalid closing delimiter")
		}
		return nil
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("JSON array has an invalid closing delimiter")
		}
		return nil
	default:
		return fmt.Errorf("JSON has invalid opening delimiter %q", delimiter)
	}
}

func readBoundedRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, errors.New("file exceeds its size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) != info.Size() {
		return nil, errors.New("file changed while it was being read")
	}
	return contents, nil
}
