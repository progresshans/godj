// Package attestation defines the source-bound PostgreSQL backend facts used by
// the portable SYS-020 product comparison. It deliberately carries observations
// rather than an expected result or a pass decision.
package attestation

import (
	"errors"
	"fmt"
	"regexp"
)

const (
	// FileName is the only checked PostgreSQL live-attestation name accepted by
	// Load. Keeping one name prevents a caller from silently selecting a weaker
	// profile from the same directory.
	FileName = "postgresql-17.10-two-process-v1.json"

	// ChecksumFileName is the sibling checksum inventory verified by Load.
	ChecksumFileName = "SHA256SUMS"

	Format          = "godj.system-state.postgresql-live-attestation/v1"
	Kind            = "godj.system-state.postgresql-live-backend-facts"
	Contract        = "SYS-020"
	Scenario        = "godj.system_state.two_process_backend_restart"
	HarnessVersion  = "godj.system-state.two-process/v1"
	SourceScope     = "godj.system-state.postgresql-two-process-source/v1"
	ProducerGo      = "go1.26.5"
	ProducerOS      = "linux"
	ProducerArch    = "amd64"
	MaxDocumentSize = 16 << 10

	maxWriterProcesses = 64
	maxFactCount       = 1_000_000_000
)

var lowercaseSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ObservedFacts is the mutable capture input. NewBackendFacts copies it into an
// immutable value before publication.
type ObservedFacts struct {
	WriterProcesses   int64
	SameSchema        bool
	BarrierLinearized bool
	RestartPreserved  bool
	DivergenceCount   int64
	LossCount         int64
	DriftCount        int64
	SecretOccurrences int64
}

type backendFactsState struct {
	writerProcesses   int64
	sameSchema        bool
	barrierLinearized bool
	restartPreserved  bool
	divergenceCount   int64
	lossCount         int64
	driftCount        int64
	secretOccurrences int64
}

// BackendFacts is an immutable, secret-free set of observed PostgreSQL facts.
// False booleans and non-zero failure counts are valid observations: neither the
// constructor nor the codec turns them into success.
type BackendFacts struct {
	state *backendFactsState
}

func NewBackendFacts(observed ObservedFacts) (BackendFacts, error) {
	if err := validateObservedFacts(observed); err != nil {
		return BackendFacts{}, err
	}
	return BackendFacts{state: &backendFactsState{
		writerProcesses:   observed.WriterProcesses,
		sameSchema:        observed.SameSchema,
		barrierLinearized: observed.BarrierLinearized,
		restartPreserved:  observed.RestartPreserved,
		divergenceCount:   observed.DivergenceCount,
		lossCount:         observed.LossCount,
		driftCount:        observed.DriftCount,
		secretOccurrences: observed.SecretOccurrences,
	}}, nil
}

func validateObservedFacts(observed ObservedFacts) error {
	if observed.WriterProcesses < 0 || observed.WriterProcesses > maxWriterProcesses {
		return fmt.Errorf("writer process count %d is outside [0,%d]", observed.WriterProcesses, maxWriterProcesses)
	}
	counts := []struct {
		name  string
		count int64
	}{
		{name: "divergence", count: observed.DivergenceCount},
		{name: "loss", count: observed.LossCount},
		{name: "drift", count: observed.DriftCount},
		{name: "secret occurrences", count: observed.SecretOccurrences},
	}
	for _, item := range counts {
		name, count := item.name, item.count
		if count < 0 || count > maxFactCount {
			return fmt.Errorf("%s count %d is outside [0,%d]", name, count, maxFactCount)
		}
	}
	return nil
}

func (facts BackendFacts) validate() error {
	if facts.state == nil {
		return errors.New("backend facts are uninitialized")
	}
	return validateObservedFacts(facts.Observed())
}

func (facts BackendFacts) Observed() ObservedFacts {
	if facts.state == nil {
		return ObservedFacts{}
	}
	return ObservedFacts{
		WriterProcesses:   facts.state.writerProcesses,
		SameSchema:        facts.state.sameSchema,
		BarrierLinearized: facts.state.barrierLinearized,
		RestartPreserved:  facts.state.restartPreserved,
		DivergenceCount:   facts.state.divergenceCount,
		LossCount:         facts.state.lossCount,
		DriftCount:        facts.state.driftCount,
		SecretOccurrences: facts.state.secretOccurrences,
	}
}

func (facts BackendFacts) WriterProcesses() int64   { return facts.Observed().WriterProcesses }
func (facts BackendFacts) SameSchema() bool         { return facts.Observed().SameSchema }
func (facts BackendFacts) BarrierLinearized() bool  { return facts.Observed().BarrierLinearized }
func (facts BackendFacts) RestartPreserved() bool   { return facts.Observed().RestartPreserved }
func (facts BackendFacts) DivergenceCount() int64   { return facts.Observed().DivergenceCount }
func (facts BackendFacts) LossCount() int64         { return facts.Observed().LossCount }
func (facts BackendFacts) DriftCount() int64        { return facts.Observed().DriftCount }
func (facts BackendFacts) SecretOccurrences() int64 { return facts.Observed().SecretOccurrences }

type sourceBindingState struct {
	fileCount    int64
	payloadBytes int64
	sha256       string
}

// SourceBinding is an immutable digest of the frozen behavioral source scope.
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
	return binding.FileCount() == other.FileCount() &&
		binding.PayloadBytes() == other.PayloadBytes() &&
		binding.SHA256() == other.SHA256() &&
		binding.state != nil && other.state != nil
}

type evidenceState struct {
	facts  BackendFacts
	source SourceBinding
}

// Evidence is an immutable validated live-attestation value.
type Evidence struct {
	state *evidenceState
}

func New(facts BackendFacts, source SourceBinding) (Evidence, error) {
	if err := facts.validate(); err != nil {
		return Evidence{}, fmt.Errorf("validate PostgreSQL backend facts: %w", err)
	}
	if err := source.validate(); err != nil {
		return Evidence{}, fmt.Errorf("validate source binding: %w", err)
	}
	return Evidence{state: &evidenceState{facts: facts, source: source}}, nil
}

func (evidence Evidence) validate() error {
	if evidence.state == nil {
		return errors.New("PostgreSQL live attestation is uninitialized")
	}
	_, err := New(evidence.state.facts, evidence.state.source)
	return err
}

func (evidence Evidence) BackendFacts() BackendFacts {
	if evidence.state == nil {
		return BackendFacts{}
	}
	return evidence.state.facts
}

func (evidence Evidence) SourceBinding() SourceBinding {
	if evidence.state == nil {
		return SourceBinding{}
	}
	return evidence.state.source
}
