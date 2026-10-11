//go:build darwin || linux

package projectcheck

import (
	"context"
	"io"
	"time"
)

// RunServerFailure is the detail-free public failure vocabulary for
// `godj runserver`. Project declaration wire failures retain their existing
// private protocol category/code so the global command does not invent a
// second interpretation of the same response.
type RunServerFailure struct {
	Category string
	Code     string
}

const (
	RunServerCategoryCommand       = "project_runserver_command_error"
	RunServerCategorySelection     = "project_runserver_selection_error"
	RunServerCategoryConfiguration = "project_runserver_configuration_error"
	RunServerCategoryBuild         = "project_runserver_build_error"
	RunServerCategoryGeneration    = "project_runserver_generation_error"
	RunServerCategoryRuntime       = "project_runserver_runtime_error"
	RunServerCategoryProcess       = "project_runserver_process_error"
	RunServerCategoryInternal      = "project_runserver_internal_error"

	RunServerCodeInvalidArguments              = "invalid_arguments"
	RunServerCodeProjectNotFound               = "project_not_found"
	RunServerCodeProjectSearchLimitExceeded    = "project_search_limit_exceeded"
	RunServerCodeInvalidProjectDescriptor      = "invalid_project_descriptor"
	RunServerCodeProjectDescriptorIncompatible = "project_descriptor_incompatible"
	RunServerCodeProjectSelectionFailed        = "project_selection_failed"
	RunServerCodeNotConfigured                 = "runserver_not_configured"
	RunServerCodeProjectTemporaryStorageFailed = "project_temporary_storage_failed"
	RunServerCodeProjectBuildFailed            = "project_build_failed"
	RunServerCodeRuntimeBuildFailed            = "project_runtime_build_failed"
	RunServerCodeProjectGenerateFailed         = "project_generate_failed"
	RunServerCodeGeneratedBundleStale          = "generated_bundle_stale"
	RunServerCodeProjectCheckFailed            = "project_generate_check_failed"
	RunServerCodeRuntimeStartFailed            = "project_runtime_start_failed"
	RunServerCodeRuntimeExited                 = "project_runtime_exited"
	RunServerCodeRuntimeStreamFailed           = "project_runtime_stream_failed"
	RunServerCodeProjectCanceled               = "project_canceled"
	RunServerCodeProjectInterrupted            = "project_interrupted"
	RunServerCodeProjectCleanupFailed          = "project_cleanup_failed"
	RunServerCodeProjectInternalError          = "project_internal_error"
)

// RunServerResult records the selected public address after the runtime child
// completed a clean operator-requested shutdown. Runtime output itself streams
// directly to the invocation writers and is not republished here.
type RunServerResult struct {
	Address string
}

// RunServerReport combines selection/build cleanup observations with the
// runserver-specific preflight and live-child lifecycle.
type RunServerReport struct {
	Report
	RuntimeBuildCalls   int
	RuntimeStartCalls   int
	PreflightChecks     int
	GeneratedDriftCount int
	HasRunServerResult  bool
	RunServerResult     RunServerResult
	HasRunServerFailure bool
	RunServerFailure    RunServerFailure
}

// RunServerInvocation is snapshotted before argument validation performs any
// filesystem selection. Backend owns the two short-lived build/runner stages;
// the live runtime has a separate streaming lifecycle.
type RunServerInvocation struct {
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
	runtime     runserverRuntimeHooks
}

const defaultRunserverGrace = 7 * time.Second

type runserverRuntimeHooks struct {
	execute func(context.Context, <-chan struct{}, Command, io.Writer, io.Writer, time.Duration) runserverProcessResult
	grace   time.Duration
}

func completeRunserverRuntimeHooks(hooks runserverRuntimeHooks) runserverRuntimeHooks {
	if hooks.execute == nil {
		hooks.execute = executeRunserverProcess
	}
	if hooks.grace <= 0 {
		hooks.grace = defaultRunserverGrace
	}
	return hooks
}
