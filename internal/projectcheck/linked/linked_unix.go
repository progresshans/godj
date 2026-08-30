//go:build darwin || linux

// Package linked implements the project-owned side of the private migration
// check and explicit-migrate boundaries. Both paths share flat source
// discovery and one pure definition load; only RunMigrate may open a database
// and execute a migration lifecycle.
package linked

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

const maxRoots = 256

// Config contains only invocation-local linked project inputs. An empty
// ProjectRoot snapshots the process cwd after request/config preflight; an
// explicit ProjectRoot must be a physical absolute directory. Run never
// changes process cwd.
type Config struct {
	ProjectRoot                string
	MigrationDefinitionRoots   []string
	MigrationDefinitionSources []definition.Source
}

// Report contains only actual counters observed by this linked invocation.
// It is returned by value and carries no source bytes, handles, or mutable
// caller input. Loader counters come only from definition.Load's LoadReport.
type Report struct {
	RunnerResponseWrites    int
	SourceReads             int
	LoadCalls               int
	DocumentsReceived       int
	HeadersValidated        int
	OperationsDecoded       int
	PlannerConstruction     int
	DefinitionsPublished    int
	DefinitionSetsPublished int
	DirectPlannerCalls      int
	GoDjDBCalls             int
	RevisionLifecycleCalls  int
	BackendOpenCalls        int
	BackendCloseCalls       int
	RevisionSessionOpens    int
	AppliedHistoryReads     int
	RevisionSessionCloses   int
	CommandDispatches       int
	RootsOpened             int
	DirectoryEntriesSeen    int

	LoadFailure    definition.FailureContext
	HasLoadFailure bool
}

// Run executes the one private linked command. Known completed logical
// failures are written as a closed response and return nil; caller-owned I/O,
// cancellation, nil dependency, and invariant failures return a Go error.
func Run(
	ctx context.Context,
	config Config,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) (Report, error) {
	roots := append([]string(nil), config.MigrationDefinitionRoots...)
	sources := cloneDefinitionSources(config.MigrationDefinitionSources)
	arguments := append([]string(nil), argv...)
	owned := Config{
		ProjectRoot:                config.ProjectRoot,
		MigrationDefinitionRoots:   roots,
		MigrationDefinitionSources: sources,
	}
	return run(ctx, owned, arguments, stdin, stdout, systemDependencies{})
}

func run(
	ctx context.Context,
	config Config,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
	dependencies systemDependencies,
) (Report, error) {
	var report Report
	if ctx == nil {
		return report, errors.New("project linked: nil context")
	}
	if stdin == nil {
		return report, errors.New("project linked: nil stdin")
	}
	if stdout == nil {
		return report, errors.New("project linked: nil stdout")
	}
	if len(argv) != 1 || argv[0] != protocol.PrivateArgument {
		return report, errors.New("project linked: invalid private argv")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	requestFailure, requestFailed, err := protocol.ReadRequest(stdin)
	if err != nil {
		return report, err
	}
	if requestFailed {
		return completeFailure(ctx, dependencies, stdout, report, requestFailure)
	}
	report.CommandDispatches = 1
	if err := ctx.Err(); err != nil {
		return report, err
	}

	_, summary, failure, failed, err := loadCatalog(ctx, config, &report, dependencies)
	if err != nil {
		return report, err
	}
	if failed {
		return completeFailure(ctx, dependencies, stdout, report, failure)
	}
	result := protocol.Result{
		SourceCount:         summary.sourceCount,
		DefinitionCount:     summary.definitionCount,
		DefinitionSetDigest: summary.digest,
	}
	return completeResponse(ctx, dependencies, stdout, report, protocol.Response{OK: true, Result: result})
}

type catalogSummary struct {
	sourceCount     int
	definitionCount int
	digest          string
}

func loadCatalog(
	ctx context.Context,
	config Config,
	report *Report,
	dependencies systemDependencies,
) (migrations.LoadedDefinitionSet, catalogSummary, protocol.Failure, bool, error) {
	discovered, failure, failed, err := discover(ctx, config.ProjectRoot, config.MigrationDefinitionRoots, report, dependencies)
	if err != nil || failed {
		return migrations.LoadedDefinitionSet{}, catalogSummary{}, failure, failed, err
	}
	if err := ctx.Err(); err != nil {
		return migrations.LoadedDefinitionSet{}, catalogSummary{}, protocol.Failure{}, false, err
	}

	sources := make([]definition.Source, 0, len(config.MigrationDefinitionSources)+len(discovered))
	sources = append(sources, cloneDefinitionSources(config.MigrationDefinitionSources)...)
	sources = append(sources, discovered...)
	report.LoadCalls++
	set, loadReport, loadErr := definition.Load(sources...)
	report.DocumentsReceived = loadReport.DocumentsReceived
	report.HeadersValidated = loadReport.HeadersValidated
	report.OperationsDecoded = loadReport.OperationsDecoded
	report.PlannerConstruction = loadReport.PlannerConstruction
	report.DefinitionsPublished = loadReport.DefinitionsPublished
	report.DefinitionSetsPublished = loadReport.DefinitionSetsPublished
	if failureContext, exists := loadReport.Failure(); exists {
		report.LoadFailure = failureContext
		report.HasLoadFailure = true
	}
	if loadErr != nil {
		failure, classified := classifyLoadFailure(loadErr)
		if !classified || !report.HasLoadFailure {
			return migrations.LoadedDefinitionSet{}, catalogSummary{}, protocol.Failure{}, false, fmt.Errorf("project linked: invalid definition load failure: %w", loadErr)
		}
		return migrations.LoadedDefinitionSet{}, catalogSummary{}, failure, true, nil
	}
	if report.HasLoadFailure {
		return migrations.LoadedDefinitionSet{}, catalogSummary{}, protocol.Failure{}, false, errors.New("project linked: successful definition load reported a failure")
	}
	return set, catalogSummary{
		sourceCount:     len(sources),
		definitionCount: len(set.Definitions()),
		digest:          set.Digest(),
	}, protocol.Failure{}, false, nil
}

func cloneDefinitionSources(sources []definition.Source) []definition.Source {
	if len(sources) == 0 {
		return nil
	}
	cloned := make([]definition.Source, len(sources))
	for index := range sources {
		cloned[index] = definition.Source{
			SourceID: sources[index].SourceID,
			Document: append([]byte(nil), sources[index].Document...),
		}
	}
	return cloned
}

func completeFailure(
	ctx context.Context,
	dependencies systemDependencies,
	writer io.Writer,
	report Report,
	failure protocol.Failure,
) (Report, error) {
	if !protocol.IsLinkedFailure(failure) {
		return report, errors.New("project linked: invalid logical failure")
	}
	return completeResponse(ctx, dependencies, writer, report, protocol.Response{Failure: failure})
}

func completeResponse(
	ctx context.Context,
	dependencies systemDependencies,
	writer io.Writer,
	report Report,
	response protocol.Response,
) (Report, error) {
	if dependencies.beforeResponseWrite != nil {
		dependencies.beforeResponseWrite()
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	return writeResponse(writer, report, response)
}

func writeResponse(writer io.Writer, report Report, response protocol.Response) (Report, error) {
	report.RunnerResponseWrites++
	if err := protocol.WriteResponse(writer, response); err != nil {
		return report, err
	}
	return report, nil
}

func classifyLoadFailure(err error) (protocol.Failure, bool) {
	var sourceError *definition.Error
	if errors.As(err, &sourceError) && sourceError != nil {
		failure := protocol.Failure{Category: sourceError.Category, Code: string(sourceError.Code)}
		return failure, protocol.IsLinkedFailure(failure)
	}
	var planningError *migrations.PlanningError
	if errors.As(err, &planningError) && planningError != nil {
		failure := protocol.Failure{Category: string(planningError.Category), Code: string(planningError.Code)}
		return failure, protocol.IsLinkedFailure(failure)
	}
	return protocol.Failure{}, false
}
