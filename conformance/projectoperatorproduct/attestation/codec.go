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

type wireEvidence struct {
	Format               string             `json:"format"`
	Kind                 string             `json:"kind"`
	Contract             string             `json:"contract"`
	Scenario             string             `json:"scenario"`
	Producer             wireProducer       `json:"producer"`
	PostgreSQL           wirePostgreSQL     `json:"postgresql"`
	Source               wireSourceBinding  `json:"source_binding"`
	RequiredBackendCases int64              `json:"required_backend_cases"`
	BackendCases         []wireProductFacts `json:"backend_cases"`
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

type wireProductFacts struct {
	Backend                             string `json:"backend"`
	ProvisionProcesses                  int64  `json:"provision_processes"`
	RuntimeProcesses                    int64  `json:"runtime_processes"`
	DistinctProcesses                   int64  `json:"distinct_processes"`
	ProvisionCalls                      int64  `json:"provision_calls"`
	CredentialRows                      int64  `json:"credential_rows"`
	Provisioned                         bool   `json:"provisioned"`
	AdminAuthenticated                  bool   `json:"admin_authenticated"`
	APIAuthenticated                    bool   `json:"api_authenticated"`
	DistinctProcessRestart              bool   `json:"distinct_process_restart"`
	ProvisionProcessDistinctFromRuntime bool   `json:"provision_process_distinct_from_runtime"`
	RestartRawSecretInput               bool   `json:"restart_raw_secret_input"`
	RestartStateLoss                    int64  `json:"restart_state_loss"`
	SchemaDrift                         bool   `json:"schema_drift"`
	RawSecretOccurrences                int64  `json:"raw_secret_occurrences"`
}

// MarshalCapture returns canonical capture bytes only on the exact hosted
// producer coordinate. The database profile is supplied by the live test and
// is validated before it can enter evidence.
func MarshalCapture(
	postgresqlObserved, sqliteObserved ObservedFacts,
	postgresql PostgreSQLFingerprint,
	source SourceBinding,
) ([]byte, error) {
	if runtime.Version() != ProducerGo || runtime.GOOS != ProducerOS || runtime.GOARCH != ProducerArch {
		return nil, errors.New("external operator PostgreSQL capture requires the exact go1.26.5 linux/amd64 producer")
	}
	postgresqlFacts, err := NewProductFacts(postgresqlObserved)
	if err != nil {
		return nil, fmt.Errorf("capture PostgreSQL external operator product facts: %w", err)
	}
	sqliteFacts, err := NewProductFacts(sqliteObserved)
	if err != nil {
		return nil, fmt.Errorf("capture SQLite external operator product facts: %w", err)
	}
	evidence, err := New(postgresqlFacts, sqliteFacts, postgresql, source)
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
	encoded, err := json.Marshal(evidenceToWire(evidence))
	if err != nil {
		return nil, errors.New("marshal external operator PostgreSQL attestation")
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxDocumentSize {
		return nil, errors.New("canonical external operator PostgreSQL attestation exceeds its size limit")
	}
	return encoded, nil
}

// Decode validates canonical bytes, rejects duplicate or unknown object names,
// and accepts only the fixed SYS-029 profile.
func Decode(document []byte) (Evidence, error) {
	if len(document) == 0 {
		return Evidence{}, io.ErrUnexpectedEOF
	}
	if len(document) > MaxDocumentSize {
		return Evidence{}, errors.New("external operator PostgreSQL attestation exceeds its size limit")
	}
	if !utf8.Valid(document) {
		return Evidence{}, errors.New("external operator PostgreSQL attestation is not valid UTF-8")
	}
	if err := rejectDuplicateObjectNames(document); err != nil {
		return Evidence{}, errors.New("external operator PostgreSQL attestation has invalid or duplicate object names")
	}

	var wire wireEvidence
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Evidence{}, errors.New("external operator PostgreSQL attestation has an invalid JSON shape")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Evidence{}, errors.New("external operator PostgreSQL attestation contains a trailing JSON value")
		}
		return Evidence{}, errors.New("external operator PostgreSQL attestation contains invalid trailing data")
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
		return Evidence{}, errors.New("external operator PostgreSQL attestation is not canonical JSON")
	}
	return evidence, nil
}

// Load validates a checked document, its fixed one-line SHA256SUMS inventory,
// and its binding to the current repository source.
func Load(repositoryRoot, attestationPath string) (Evidence, error) {
	if filepath.Base(attestationPath) != FileName {
		return Evidence{}, fmt.Errorf("external operator PostgreSQL attestation filename must be %q", FileName)
	}
	document, err := readBoundedRegularFile(attestationPath, MaxDocumentSize)
	if err != nil {
		return Evidence{}, fmt.Errorf("read external operator PostgreSQL attestation: %w", err)
	}
	checksum, err := readBoundedRegularFile(filepath.Join(filepath.Dir(attestationPath), ChecksumFileName), maxChecksumSize)
	if err != nil {
		return Evidence{}, fmt.Errorf("read external operator PostgreSQL attestation checksum: %w", err)
	}
	if !bytes.Equal(checksum, ChecksumLine(document)) {
		return Evidence{}, errors.New("external operator PostgreSQL attestation checksum inventory does not match")
	}
	evidence, err := Decode(document)
	if err != nil {
		return Evidence{}, err
	}
	current, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		return Evidence{}, fmt.Errorf("compute current external operator source binding: %w", err)
	}
	if !evidence.SourceBinding().Equal(current) {
		return Evidence{}, errors.New("external operator PostgreSQL attestation source binding is stale")
	}
	return evidence, nil
}

// ChecksumLine returns the only SHA256SUMS representation accepted by Load.
func ChecksumLine(document []byte) []byte {
	digest := sha256.Sum256(document)
	return []byte(hex.EncodeToString(digest[:]) + "  " + FileName + "\n")
}

func evidenceToWire(evidence Evidence) wireEvidence {
	postgresqlObserved := evidence.PostgreSQLFacts().Observed()
	sqliteObserved := evidence.SQLiteFacts().Observed()
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
		PostgreSQL: fingerprintToWire(evidence.PostgreSQLFingerprint()),
		Source: wireSourceBinding{
			Scope:        SourceScope,
			FileCount:    source.FileCount(),
			PayloadBytes: source.PayloadBytes(),
			SHA256:       source.SHA256(),
		},
		RequiredBackendCases: RequiredBackendCases,
		BackendCases: []wireProductFacts{
			observedToWire(postgresqlObserved),
			observedToWire(sqliteObserved),
		},
	}
}

func observedToWire(observed ObservedFacts) wireProductFacts {
	return wireProductFacts{
		Backend:                             observed.Backend,
		ProvisionProcesses:                  observed.ProvisionProcesses,
		RuntimeProcesses:                    observed.RuntimeProcesses,
		DistinctProcesses:                   observed.DistinctProcesses,
		ProvisionCalls:                      observed.ProvisionCalls,
		CredentialRows:                      observed.CredentialRows,
		Provisioned:                         observed.Provisioned,
		AdminAuthenticated:                  observed.AdminAuthenticated,
		APIAuthenticated:                    observed.APIAuthenticated,
		DistinctProcessRestart:              observed.DistinctProcessRestart,
		ProvisionProcessDistinctFromRuntime: observed.ProvisionProcessDistinctFromRuntime,
		RestartRawSecretInput:               observed.RestartRawSecretInput,
		RestartStateLoss:                    observed.RestartStateLoss,
		SchemaDrift:                         observed.SchemaDrift,
		RawSecretOccurrences:                observed.RawSecretOccurrences,
	}
}

func (wire wireEvidence) toEvidence() (Evidence, error) {
	if wire.Format != Format || wire.Kind != Kind || wire.Contract != Contract || wire.Scenario != Scenario {
		return Evidence{}, errors.New("external operator PostgreSQL attestation has the wrong fixed identity")
	}
	if wire.Producer != (wireProducer{Go: ProducerGo, OS: ProducerOS, Arch: ProducerArch, Harness: HarnessVersion}) {
		return Evidence{}, errors.New("external operator PostgreSQL attestation has the wrong producer profile")
	}
	postgresql := wire.PostgreSQL.toFingerprint()
	if err := ValidatePostgreSQLFingerprint(postgresql); err != nil {
		return Evidence{}, err
	}
	if wire.Source.Scope != SourceScope {
		return Evidence{}, errors.New("external operator PostgreSQL attestation has the wrong source scope")
	}
	if wire.RequiredBackendCases != RequiredBackendCases || len(wire.BackendCases) != RequiredBackendCases {
		return Evidence{}, errors.New("external operator attestation does not contain exactly two required backend cases")
	}
	if wire.BackendCases[0].Backend != BackendPostgreSQL || wire.BackendCases[1].Backend != BackendSQLite {
		return Evidence{}, errors.New("external operator attestation has the wrong backend order or identity")
	}
	source, err := newSourceBinding(wire.Source.FileCount, wire.Source.PayloadBytes, wire.Source.SHA256)
	if err != nil {
		return Evidence{}, fmt.Errorf("validate external operator attestation source binding: %w", err)
	}
	postgresqlFacts, err := NewProductFacts(wire.BackendCases[0].toObserved())
	if err != nil {
		return Evidence{}, fmt.Errorf("validate PostgreSQL external operator attestation facts: %w", err)
	}
	sqliteFacts, err := NewProductFacts(wire.BackendCases[1].toObserved())
	if err != nil {
		return Evidence{}, fmt.Errorf("validate SQLite external operator attestation facts: %w", err)
	}
	return New(postgresqlFacts, sqliteFacts, postgresql, source)
}

func (wire wireProductFacts) toObserved() ObservedFacts {
	return ObservedFacts{
		Backend:                             wire.Backend,
		ProvisionProcesses:                  wire.ProvisionProcesses,
		RuntimeProcesses:                    wire.RuntimeProcesses,
		DistinctProcesses:                   wire.DistinctProcesses,
		ProvisionCalls:                      wire.ProvisionCalls,
		CredentialRows:                      wire.CredentialRows,
		Provisioned:                         wire.Provisioned,
		AdminAuthenticated:                  wire.AdminAuthenticated,
		APIAuthenticated:                    wire.APIAuthenticated,
		DistinctProcessRestart:              wire.DistinctProcessRestart,
		ProvisionProcessDistinctFromRuntime: wire.ProvisionProcessDistinctFromRuntime,
		RestartRawSecretInput:               wire.RestartRawSecretInput,
		RestartStateLoss:                    wire.RestartStateLoss,
		SchemaDrift:                         wire.SchemaDrift,
		RawSecretOccurrences:                wire.RawSecretOccurrences,
	}
}

func fingerprintToWire(value PostgreSQLFingerprint) wirePostgreSQL {
	return wirePostgreSQL{
		ServerVersionNum:          value.ServerVersionNum,
		ServerEncoding:            value.ServerEncoding,
		ClientEncoding:            value.ClientEncoding,
		LocaleProvider:            value.LocaleProvider,
		Locale:                    value.Locale,
		Collation:                 value.Collation,
		CharacterType:             value.CharacterType,
		TimeZone:                  value.TimeZone,
		StandardConformingStrings: value.StandardConformingStrings,
		SynchronousCommit:         value.SynchronousCommit,
		DefaultIsolation:          value.DefaultIsolation,
		DefaultReadOnly:           value.DefaultReadOnly,
		DefaultDeferrable:         value.DefaultDeferrable,
		FSync:                     value.FSync,
		FullPageWrites:            value.FullPageWrites,
		SessionReplicationRole:    value.SessionReplicationRole,
	}
}

func (wire wirePostgreSQL) toFingerprint() PostgreSQLFingerprint {
	return PostgreSQLFingerprint{
		ServerVersionNum:          wire.ServerVersionNum,
		ServerEncoding:            wire.ServerEncoding,
		ClientEncoding:            wire.ClientEncoding,
		LocaleProvider:            wire.LocaleProvider,
		Locale:                    wire.Locale,
		Collation:                 wire.Collation,
		CharacterType:             wire.CharacterType,
		TimeZone:                  wire.TimeZone,
		StandardConformingStrings: wire.StandardConformingStrings,
		SynchronousCommit:         wire.SynchronousCommit,
		DefaultIsolation:          wire.DefaultIsolation,
		DefaultReadOnly:           wire.DefaultReadOnly,
		DefaultDeferrable:         wire.DefaultDeferrable,
		FSync:                     wire.FSync,
		FullPageWrites:            wire.FullPageWrites,
		SessionReplicationRole:    wire.SessionReplicationRole,
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
