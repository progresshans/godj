// Package attestation defines the source-bound PostgreSQL and SQLite facts
// captured by the SYS-029 external-project operator product lane. It is
// intentionally independent from the SYS-020 system-state profile.
package attestation

import (
	"errors"
	"fmt"
	"regexp"
)

const (
	// FileName is the single checked capture name accepted by Load.
	FileName = "postgresql-17.10-sqlite-external-operator-v1.json"

	// ChecksumFileName is the fixed sibling checksum inventory.
	ChecksumFileName = "SHA256SUMS"

	Format               = "godj.project-operator.combined-live-attestation/v1"
	Kind                 = "godj.project-operator.combined-live-product-facts"
	Contract             = "SYS-029"
	Scenario             = "godj.system_state.operator_backend_login_restart"
	HarnessVersion       = "godj.project-operator.external-global-combined/v1"
	SourceScope          = "godj.project-operator.combined-external-global-source/v1"
	BackendPostgreSQL    = "postgresql_17_10"
	BackendSQLite        = "sqlite"
	RequiredBackendCases = 2
	ProducerGo           = "go1.26.5"
	ProducerOS           = "linux"
	ProducerArch         = "amd64"
	MaxDocumentSize      = 16 << 10

	maxInvocationCount = 16
	maxRowCount        = 1_000_000_000
)

var lowercaseSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ObservedFacts is the mutable input collected from one complete external
// PostgreSQL operator product run. It records observations, not an expected or
// pass decision, and deliberately carries no PID, credential, DSN, timestamp,
// or other machine-specific value.
type ObservedFacts struct {
	Backend                             string
	ProvisionProcesses                  int64
	RuntimeProcesses                    int64
	DistinctProcesses                   int64
	ProvisionCalls                      int64
	CredentialRows                      int64
	Provisioned                         bool
	AdminAuthenticated                  bool
	APIAuthenticated                    bool
	DistinctProcessRestart              bool
	ProvisionProcessDistinctFromRuntime bool
	RestartRawSecretInput               bool
	RestartStateLoss                    int64
	SchemaDrift                         bool
	RawSecretOccurrences                int64
}

type productFactsState struct {
	observed ObservedFacts
}

// ProductFacts is an immutable copy of the observed external product facts.
// Failure-shaped booleans and counts remain representable; construction does
// not silently convert them into a success verdict.
type ProductFacts struct {
	state *productFactsState
}

func NewProductFacts(observed ObservedFacts) (ProductFacts, error) {
	if err := validateObservedFacts(observed); err != nil {
		return ProductFacts{}, err
	}
	return ProductFacts{state: &productFactsState{observed: observed}}, nil
}

func validateObservedFacts(observed ObservedFacts) error {
	if observed.Backend != BackendPostgreSQL && observed.Backend != BackendSQLite {
		return errors.New("backend is not a fixed SYS-029 backend")
	}
	invocations := []struct {
		name  string
		count int64
	}{
		{name: "provision processes", count: observed.ProvisionProcesses},
		{name: "runtime processes", count: observed.RuntimeProcesses},
		{name: "distinct processes", count: observed.DistinctProcesses},
		{name: "provision calls", count: observed.ProvisionCalls},
	}
	for _, item := range invocations {
		if item.count < 0 || item.count > maxInvocationCount {
			return fmt.Errorf("%s count %d is outside [0,%d]", item.name, item.count, maxInvocationCount)
		}
	}
	counts := []struct {
		name  string
		count int64
	}{
		{name: "credential rows", count: observed.CredentialRows},
		{name: "restart state loss", count: observed.RestartStateLoss},
		{name: "raw secret occurrences", count: observed.RawSecretOccurrences},
	}
	for _, item := range counts {
		if item.count < 0 || item.count > maxRowCount {
			return fmt.Errorf("%s count %d is outside [0,%d]", item.name, item.count, maxRowCount)
		}
	}
	return nil
}

func (facts ProductFacts) validate() error {
	if facts.state == nil {
		return errors.New("external operator product facts are uninitialized")
	}
	return validateObservedFacts(facts.state.observed)
}

func (facts ProductFacts) Observed() ObservedFacts {
	if facts.state == nil {
		return ObservedFacts{}
	}
	return facts.state.observed
}

// PostgreSQLFingerprint is the exact database/session profile observed by the
// product test before it executes the public command sequence.
type PostgreSQLFingerprint struct {
	ServerVersionNum          string
	ServerEncoding            string
	ClientEncoding            string
	LocaleProvider            string
	Locale                    string
	Collation                 string
	CharacterType             string
	TimeZone                  string
	StandardConformingStrings string
	SynchronousCommit         string
	DefaultIsolation          string
	DefaultReadOnly           string
	DefaultDeferrable         string
	FSync                     string
	FullPageWrites            string
	SessionReplicationRole    string
}

// ExpectedPostgreSQLFingerprint returns the immutable PostgreSQL 17.10 profile
// required by the source-bound producer lane.
func ExpectedPostgreSQLFingerprint() PostgreSQLFingerprint {
	return PostgreSQLFingerprint{
		ServerVersionNum:          "170010",
		ServerEncoding:            "UTF8",
		ClientEncoding:            "UTF8",
		LocaleProvider:            "c",
		Locale:                    "<null>",
		Collation:                 "C",
		CharacterType:             "C",
		TimeZone:                  "UTC",
		StandardConformingStrings: "on",
		SynchronousCommit:         "on",
		DefaultIsolation:          "read committed",
		DefaultReadOnly:           "off",
		DefaultDeferrable:         "off",
		FSync:                     "on",
		FullPageWrites:            "on",
		SessionReplicationRole:    "origin",
	}
}

// ValidatePostgreSQLFingerprint rejects every backend profile except the
// pinned PostgreSQL 17.10 UTF-8/C/UTC producer profile.
func ValidatePostgreSQLFingerprint(fingerprint PostgreSQLFingerprint) error {
	if fingerprint != ExpectedPostgreSQLFingerprint() {
		return errors.New("external operator attestation has the wrong PostgreSQL 17.10 fingerprint")
	}
	return nil
}

type sourceBindingState struct {
	fileCount    int64
	payloadBytes int64
	sha256       string
}

// SourceBinding is an immutable digest of the fixed SYS-029 behavioral source
// inventory.
type SourceBinding struct {
	state *sourceBindingState
}

func newSourceBinding(fileCount, payloadBytes int64, digest string) (SourceBinding, error) {
	if fileCount <= 0 {
		return SourceBinding{}, errors.New("source binding contains no files")
	}
	if fileCount > maxSourceFiles {
		return SourceBinding{}, fmt.Errorf("source binding file count %d exceeds %d", fileCount, maxSourceFiles)
	}
	if payloadBytes < 0 {
		return SourceBinding{}, errors.New("source binding payload size is negative")
	}
	if payloadBytes > maxSourceBytes {
		return SourceBinding{}, fmt.Errorf("source binding payload size %d exceeds %d", payloadBytes, maxSourceBytes)
	}
	if !lowercaseSHA256Pattern.MatchString(digest) {
		return SourceBinding{}, errors.New("source binding SHA-256 is not 64 lowercase hexadecimal bytes")
	}
	return SourceBinding{state: &sourceBindingState{
		fileCount:    fileCount,
		payloadBytes: payloadBytes,
		sha256:       digest,
	}}, nil
}

func (binding SourceBinding) validate() error {
	if binding.state == nil {
		return errors.New("source binding is uninitialized")
	}
	_, err := newSourceBinding(binding.state.fileCount, binding.state.payloadBytes, binding.state.sha256)
	return err
}

func (binding SourceBinding) FileCount() int64 {
	if binding.state == nil {
		return 0
	}
	return binding.state.fileCount
}

func (binding SourceBinding) PayloadBytes() int64 {
	if binding.state == nil {
		return 0
	}
	return binding.state.payloadBytes
}

func (binding SourceBinding) SHA256() string {
	if binding.state == nil {
		return ""
	}
	return binding.state.sha256
}

func (binding SourceBinding) Equal(other SourceBinding) bool {
	return binding.state != nil && other.state != nil &&
		binding.FileCount() == other.FileCount() &&
		binding.PayloadBytes() == other.PayloadBytes() &&
		binding.SHA256() == other.SHA256()
}

type evidenceState struct {
	postgresqlFacts ProductFacts
	sqliteFacts     ProductFacts
	postgresql      PostgreSQLFingerprint
	source          SourceBinding
}

// Evidence is a fully validated immutable two-backend SYS-029 live-attestation
// value.
type Evidence struct {
	state *evidenceState
}

func New(
	postgresqlFacts, sqliteFacts ProductFacts,
	postgresql PostgreSQLFingerprint,
	source SourceBinding,
) (Evidence, error) {
	if err := postgresqlFacts.validate(); err != nil {
		return Evidence{}, fmt.Errorf("validate PostgreSQL external operator product facts: %w", err)
	}
	if postgresqlFacts.Observed().Backend != BackendPostgreSQL {
		return Evidence{}, errors.New("first external operator backend is not PostgreSQL 17.10")
	}
	if err := sqliteFacts.validate(); err != nil {
		return Evidence{}, fmt.Errorf("validate SQLite external operator product facts: %w", err)
	}
	if sqliteFacts.Observed().Backend != BackendSQLite {
		return Evidence{}, errors.New("second external operator backend is not SQLite")
	}
	if err := ValidatePostgreSQLFingerprint(postgresql); err != nil {
		return Evidence{}, err
	}
	if err := source.validate(); err != nil {
		return Evidence{}, fmt.Errorf("validate source binding: %w", err)
	}
	return Evidence{state: &evidenceState{
		postgresqlFacts: postgresqlFacts,
		sqliteFacts:     sqliteFacts,
		postgresql:      postgresql,
		source:          source,
	}}, nil
}

func (evidence Evidence) validate() error {
	if evidence.state == nil {
		return errors.New("external operator PostgreSQL attestation is uninitialized")
	}
	_, err := New(evidence.state.postgresqlFacts, evidence.state.sqliteFacts, evidence.state.postgresql, evidence.state.source)
	return err
}

func (evidence Evidence) PostgreSQLFacts() ProductFacts {
	if evidence.state == nil {
		return ProductFacts{}
	}
	return evidence.state.postgresqlFacts
}

func (evidence Evidence) SQLiteFacts() ProductFacts {
	if evidence.state == nil {
		return ProductFacts{}
	}
	return evidence.state.sqliteFacts
}

func (evidence Evidence) PostgreSQLFingerprint() PostgreSQLFingerprint {
	if evidence.state == nil {
		return PostgreSQLFingerprint{}
	}
	return evidence.state.postgresql
}

func (evidence Evidence) SourceBinding() SourceBinding {
	if evidence.state == nil {
		return SourceBinding{}
	}
	return evidence.state.source
}
