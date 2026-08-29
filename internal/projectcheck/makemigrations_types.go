//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"
)

// MakemigrationsFailure is the detail-free public failure vocabulary for the
// project-linked migration writer. Linked protocol pairs are preserved when
// they are already part of the closed writer taxonomy.
type MakemigrationsFailure struct {
	Category string
	Code     string
}

const (
	MakemigrationsCategoryCommand     = "migration_writer_command_error"
	MakemigrationsCategorySelection   = "migration_writer_selection_error"
	MakemigrationsCategoryBuild       = "migration_writer_build_error"
	MakemigrationsCategorySource      = "migration_writer_source_error"
	MakemigrationsCategoryPublication = "migration_writer_publication_error"
	MakemigrationsCategoryProcess     = "migration_writer_process_error"
	MakemigrationsCategoryInternal    = "migration_writer_internal_error"

	MakemigrationsCodeInvalidArguments              = "invalid_arguments"
	MakemigrationsCodeProjectNotFound               = "project_not_found"
	MakemigrationsCodeProjectSearchLimitExceeded    = "project_search_limit_exceeded"
	MakemigrationsCodeInvalidProjectDescriptor      = "invalid_project_descriptor"
	MakemigrationsCodeProjectDescriptorIncompatible = "project_descriptor_incompatible"
	MakemigrationsCodeProjectSelectionFailed        = "project_selection_failed"
	MakemigrationsCodeProjectTemporaryStorageFailed = "project_temporary_storage_failed"
	MakemigrationsCodeProjectInventoryFailed        = "project_build_inventory_failed"
	MakemigrationsCodeProjectBuildFailed            = "project_build_failed"
	MakemigrationsCodeSourceFingerprintFailed       = "source_fingerprint_failed"
	MakemigrationsCodeSourceConflict                = "source_conflict"
	MakemigrationsCodePublicationUnavailable        = "publication_unavailable"
	MakemigrationsCodeProjectCanceled               = "project_canceled"
	MakemigrationsCodeProjectInterrupted            = "project_interrupted"
	MakemigrationsCodeProjectCleanupFailed          = "project_cleanup_failed"
	MakemigrationsCodeProjectInternalError          = "project_internal_error"
)

// MakemigrationsCandidate is the host-independent public inventory for one
// strictly preflighted migration definition candidate. It never exposes raw
// definition bytes or an absolute path.
type MakemigrationsCandidate struct {
	App      string
	Name     string
	Path     string
	SourceID string
	SHA256   string
}

// MakemigrationsResult is the deterministic read-only command result.
type MakemigrationsResult struct {
	Status         string
	CandidateCount int
	Candidates     []MakemigrationsCandidate
}

// MakemigrationsReport combines writer outcome with process, selection and
// cleanup observations. InventoryCalls are separate bounded go-list children;
// BuildCalls and RunnerCalls retain their existing exact meanings.
type MakemigrationsReport struct {
	Report
	InventoryCalls              int
	IndependentCatalogSnapshots int
	HasMakemigrationsResult     bool
	MakemigrationsResult        MakemigrationsResult
	HasMakemigrationsFailure    bool
	MakemigrationsFailure       MakemigrationsFailure
}

// MakemigrationsInvocation is fully snapshotted before project selection.
type MakemigrationsInvocation struct {
	Context     context.Context
	CWD         string
	Args        []string
	Environment []string
	Stdout      io.Writer
	Stderr      io.Writer
	Interrupt   <-chan struct{}
	Backend     Backend
	workspace   workspaceHooks

	afterFinalCatalogSnapshot func()
}
