//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate"
)

// GenerationFailure is the detail-free public failure vocabulary for
// `godj generate`. It is deliberately separate from migrations check.
type GenerationFailure struct {
	Category string
	Code     string
}

const (
	GenerationCategoryCommand     = "project_generation_command_error"
	GenerationCategorySelection   = "project_generation_selection_error"
	GenerationCategoryBuild       = "project_generation_build_error"
	GenerationCategoryProtocol    = "project_generation_protocol_error"
	GenerationCategoryDeclaration = "project_generation_declaration_error"
	GenerationCategoryGeneration  = "project_generation_error"
	GenerationCategoryProcess     = "project_generation_process_error"
	GenerationCategoryInternal    = "project_generation_internal_error"

	GenerationCodeInvalidArguments              = "invalid_arguments"
	GenerationCodeProjectNotFound               = "project_not_found"
	GenerationCodeProjectSearchLimitExceeded    = "project_search_limit_exceeded"
	GenerationCodeInvalidProjectDescriptor      = "invalid_project_descriptor"
	GenerationCodeProjectDescriptorIncompatible = "project_descriptor_incompatible"
	GenerationCodeProjectSelectionFailed        = "project_selection_failed"
	GenerationCodeProjectTemporaryStorageFailed = "project_temporary_storage_failed"
	GenerationCodeProjectBuildFailed            = "project_build_failed"
	GenerationCodeProjectGenerateFailed         = "project_generate_failed"
	GenerationCodeProjectCheckFailed            = "project_generate_check_failed"
	GenerationCodeProjectPublishFailed          = "project_generate_publish_failed"
	GenerationCodeCandidateVerificationFailed   = "project_generate_candidate_verification_failed"
	GenerationCodePublicationRecoveryRequired   = "project_generate_publication_recovery_required"
	GenerationCodeProjectCanceled               = "project_canceled"
	GenerationCodeProjectInterrupted            = "project_interrupted"
	GenerationCodeProjectCleanupFailed          = "project_cleanup_failed"
	GenerationCodeProjectInternalError          = "project_internal_error"
)

// GenerationResult is the deterministic public result of generation or a
// read-only generation check.
type GenerationResult struct {
	Status               string
	SnapshotSHA256       string
	FileCount            int
	ActualSnapshotSHA256 string
	Interrupted          bool
	Drifts               []projectgenerate.Drift
}

// GenerationReport contains generation-specific outcome plus the existing
// global process and cleanup observations embedded in Report.
type GenerationReport struct {
	Report
	HasGenerationResult  bool
	GenerationResult     GenerationResult
	HasGenerationFailure bool
	GenerationFailure    GenerationFailure
}

// GenerationInvocation is snapshotted by RunGenerate before any filesystem
// selection. Unexported hooks are test-only seams for failure atomicity.
type GenerationInvocation struct {
	Context     context.Context
	CWD         string
	Args        []string
	Environment []string
	Stdout      io.Writer
	Stderr      io.Writer
	Interrupt   <-chan struct{}
	Backend     Backend
	workspace   workspaceHooks
	generation  generationHooks
}

type generationHooks struct {
	generate    func(codegen.ProjectSpec) (codegen.GeneratedBundle, error)
	sealRoot    func(string, uint64, uint64) (projectgenerate.ProjectRoot, error)
	check       func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CheckReport, error)
	newVerifier func(projectgenerate.ProjectRoot, codegen.GeneratedBundle) (projectgenerate.CandidateVerifier, error)
	publish     func(context.Context, projectgenerate.ProjectRoot, codegen.GeneratedBundle, projectgenerate.CandidateVerifier) error
}

func defaultGenerationHooks() generationHooks {
	return generationHooks{
		generate:    codegen.GenerateProject,
		sealRoot:    projectgenerate.SealProjectRoot,
		check:       projectgenerate.CheckRoot,
		newVerifier: projectgenerate.NewGoCandidateVerifierRoot,
		publish:     projectgenerate.PublishRoot,
	}
}
