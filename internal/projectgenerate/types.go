// Package projectgenerate owns whole-project candidate checking and
// recoverable publication. It is internal so callers cannot assemble
// arbitrary file bundles that bypass codegen.ProjectSpec provenance.
package projectgenerate

import (
	"context"
	"errors"

	"github.com/progresshans/godj/codegen"
)

var (
	ErrInvalidGeneratedBundle      = errors.New("generated project bundle is invalid")
	ErrGeneratedDrift              = errors.New("generated project differs from committed output")
	ErrGeneratedConflict           = errors.New("generated project conflicts with local files")
	ErrCandidateVerification       = errors.New("generated project candidate verification failed")
	ErrPublicationInterrupted      = errors.New("generated project publication was interrupted")
	ErrPublicationRecoveryRequired = errors.New("generated project publication requires recovery")
)

const (
	generatedManifestRelativePath       = codegen.GeneratedManifestPath
	publicationJournalRelativePath      = ".godj/publication-journal.json"
	publicationLockRelativePath         = ".godj/generate.lock"
	publicationTransactionDirectoryPath = ".godj/transactions"
)

// DriftKind classifies read-only project bundle differences.
type DriftKind string

const (
	DriftMissing     DriftKind = "missing"
	DriftUnexpected  DriftKind = "unexpected"
	DriftModified    DriftKind = "modified"
	DriftManifest    DriftKind = "manifest"
	DriftSnapshot    DriftKind = "snapshot"
	DriftInterrupted DriftKind = "interrupted"
)

// Drift is one deterministic project-root-relative difference.
type Drift struct {
	Path           string
	Kind           DriftKind
	ExpectedSHA256 string
	ActualSHA256   string
}

// CheckReport is a caller-owned read-only check result.
type CheckReport struct {
	ExpectedSnapshotSHA256 string
	ActualSnapshotSHA256   string
	Drifts                 []Drift
	Interrupted            bool
}

// Clean reports whether the committed generated project exactly matches the
// requested bundle and no interrupted publication is present.
func (report CheckReport) Clean() bool {
	return len(report.Drifts) == 0 && !report.Interrupted
}

// CandidateVerifier compiles or otherwise validates the complete generated
// overlay. candidateRoot is a private bundle-backing directory that mirrors
// canonical bundle paths; the verifier overlays it on the immutable project
// root captured at construction time. It must not execute a test binary or
// retain candidateRoot.
type CandidateVerifier interface {
	Verify(context.Context, string) error
}

// CandidateVerifyFunc adapts a function to CandidateVerifier.
type CandidateVerifyFunc func(context.Context, string) error

func (verify CandidateVerifyFunc) Verify(ctx context.Context, candidateRoot string) error {
	if verify == nil {
		return ErrCandidateVerification
	}
	return verify(ctx, candidateRoot)
}

// ProjectRoot is an opaque seal over one physical project-directory identity.
// Callers obtain it with SealProjectRoot and cannot retarget a check, verifier,
// or publication by replacing the pathname after project selection.
type ProjectRoot struct {
	absolute string
	device   uint64
	inode    uint64
}
