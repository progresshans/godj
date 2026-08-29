//go:build darwin || linux

package linked

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/migrationautodetect"
	checkprotocol "github.com/progresshans/godj/internal/projectcheck/protocol"
	"github.com/progresshans/godj/internal/projectmigration"
	writerprotocol "github.com/progresshans/godj/internal/projectmigration/protocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

// ProjectSpecLoader owns the declaration snapshot for one private writer
// request. Project code must implement it as a pure, DB-free declaration read.
type ProjectSpecLoader func(context.Context) (codegen.ProjectSpec, error)

// MakemigrationsConfig contains the invocation-local declaration and source
// inputs used by one child snapshot. The writer root is the sole configured
// filesystem source root in this bounded product.
type MakemigrationsConfig struct {
	ProjectRoot                string
	MigrationDefinitionRoots   []string
	MigrationDefinitionSources []definition.Source
	LoadProjectSpec            ProjectSpecLoader
}

type makemigrationsConfigSnapshot struct {
	ProjectRoot                            string
	MigrationDefinitionRoots               []string
	MigrationDefinitionSources             []definition.Source
	LoadProjectSpec                        ProjectSpecLoader
	invalidProjectSourceConfig             bool
	programmaticCatalogResourceLimitFailed bool
}

// MakemigrationsConfigSnapshot is an opaque, bounded ownership copy. Its
// project-owned values cannot be replaced between public dispatch and private
// request decoding.
type MakemigrationsConfigSnapshot struct {
	config makemigrationsConfigSnapshot
}

// SnapshotMakemigrationsConfig synchronously takes one bounded ownership copy
// for the public project facade. Resource-invalid inputs are represented in
// the returned opaque state so the runner can preserve request-first response
// precedence without allocating proportional attacker-controlled storage.
func SnapshotMakemigrationsConfig(config MakemigrationsConfig) MakemigrationsConfigSnapshot {
	return MakemigrationsConfigSnapshot{config: snapshotMakemigrationsConfig(config)}
}

// MakemigrationsReport records only observations made at actual child
// callsites. BuildSnapshotCalls represents one complete pure detect/encode/
// strict-prefix preflight, not the number of internal definition.Load calls.
type MakemigrationsReport struct {
	Report
	ProjectSpecLoaderCalls int
	BuildSnapshotCalls     int
	CandidatesProduced     int
}

// RunMakemigrations serves the separate version-1 writer snapshot protocol.
// It never opens a migration backend and never mutates the project tree.
func RunMakemigrations(
	ctx context.Context,
	config MakemigrationsConfig,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) (MakemigrationsReport, error) {
	return runMakemigrations(ctx, config, nil, argv, stdin, stdout, systemDependencies{})
}

// RunSnapshottedMakemigrations serves a config snapshotted synchronously by
// the public project facade before request I/O can block.
func RunSnapshottedMakemigrations(
	ctx context.Context,
	config MakemigrationsConfigSnapshot,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) (MakemigrationsReport, error) {
	return runMakemigrations(ctx, MakemigrationsConfig{}, &config.config, argv, stdin, stdout, systemDependencies{})
}

func runMakemigrations(
	ctx context.Context,
	config MakemigrationsConfig,
	preowned *makemigrationsConfigSnapshot,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
	dependencies systemDependencies,
) (MakemigrationsReport, error) {
	var report MakemigrationsReport
	if ctx == nil {
		return report, errors.New("project makemigrations linked: nil context")
	}
	if stdin == nil {
		return report, errors.New("project makemigrations linked: nil stdin")
	}
	if stdout == nil {
		return report, errors.New("project makemigrations linked: nil stdout")
	}
	if len(argv) != 1 || argv[0] != writerprotocol.PrivateArgument {
		return report, errors.New("project makemigrations linked: invalid private argv")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	failure, failed, err := writerprotocol.ReadRequest(stdin)
	if err != nil {
		return report, err
	}
	if failed {
		return completeMakemigrationsResponse(ctx, dependencies, stdout, report, writerprotocol.Response{Failure: failure})
	}
	report.CommandDispatches = 1
	if err := ctx.Err(); err != nil {
		return report, err
	}
	var owned makemigrationsConfigSnapshot
	if preowned == nil {
		owned = snapshotMakemigrationsConfig(config)
	} else {
		owned = *preowned
	}
	if owned.invalidProjectSourceConfig {
		return completeMakemigrationsResponse(ctx, dependencies, stdout, report, writerprotocol.Response{Failure: writerprotocol.Failure{
			Category: writerprotocol.CategoryDiscovery,
			Code:     writerprotocol.CodeInvalidProjectSourceConfig,
		}})
	}
	if owned.programmaticCatalogResourceLimitFailed {
		return completeMakemigrationsResponse(ctx, dependencies, stdout, report, writerprotocol.Response{Failure: writerprotocol.Failure{
			Category: writerprotocol.CategoryCandidate,
			Code:     writerprotocol.CodeCandidateResourceLimitExceeded,
		}})
	}

	roots, rootFailure, rootsFailed := canonicalRoots(owned.MigrationDefinitionRoots)
	if rootsFailed || len(roots) != 1 {
		failure := writerprotocol.Failure{
			Category: writerprotocol.CategoryDiscovery,
			Code:     writerprotocol.CodeInvalidProjectSourceConfig,
		}
		if rootsFailed && rootFailure.Code != checkprotocol.CodeInvalidProjectSourceConfig {
			return report, errors.New("project makemigrations linked: invalid root classification")
		}
		return completeMakemigrationsResponse(ctx, dependencies, stdout, report, writerprotocol.Response{Failure: failure})
	}
	if owned.LoadProjectSpec == nil {
		return completeMakemigrationsResponse(ctx, dependencies, stdout, report, writerprotocol.Response{Failure: writerprotocol.Failure{
			Category: writerprotocol.CategoryDeclaration,
			Code:     writerprotocol.CodeProjectSpecLoadFailed,
		}})
	}

	report.ProjectSpecLoaderCalls = 1
	spec, loadErr := owned.LoadProjectSpec(ctx)
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if loadErr != nil {
		return completeMakemigrationsResponse(ctx, dependencies, stdout, report, writerprotocol.Response{Failure: writerprotocol.Failure{
			Category: writerprotocol.CategoryDeclaration,
			Code:     writerprotocol.CodeProjectSpecLoadFailed,
		}})
	}

	discovered, discoveryFailure, discoveryFailed, err := discover(
		ctx,
		owned.ProjectRoot,
		roots,
		&report.Report,
		dependencies,
	)
	if err != nil {
		return report, err
	}
	if discoveryFailed {
		mapped := writerprotocol.Failure{Category: discoveryFailure.Category, Code: discoveryFailure.Code}
		if !writerprotocol.IsLinkedFailure(mapped) {
			return report, errors.New("project makemigrations linked: invalid discovery failure")
		}
		return completeMakemigrationsResponse(ctx, dependencies, stdout, report, writerprotocol.Response{Failure: mapped})
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	report.BuildSnapshotCalls = 1
	snapshot, snapshotErr := projectmigration.BuildSnapshot(projectmigration.Request{
		ProjectSpec:         spec,
		FilesystemSources:   discovered,
		ProgrammaticSources: owned.MigrationDefinitionSources,
		WriterRoot:          roots[0],
	})
	if snapshotErr != nil {
		mapped, ok := classifyMakemigrationsSnapshotFailure(snapshotErr)
		if !ok {
			return report, errors.New("project makemigrations linked: invalid snapshot failure")
		}
		return completeMakemigrationsResponse(ctx, dependencies, stdout, report, writerprotocol.Response{Failure: mapped})
	}
	report.CandidatesProduced = len(snapshot.Candidates())
	result := makemigrationsProtocolResult(snapshot)
	return completeMakemigrationsResponse(ctx, dependencies, stdout, report, writerprotocol.Response{OK: true, Result: result})
}

func makemigrationsProtocolResult(snapshot projectmigration.Snapshot) writerprotocol.Result {
	filesystem := snapshot.FilesystemSources()
	programmaticSources := snapshot.ProgrammaticSources()
	programmatic := make([]writerprotocol.Source, len(programmaticSources))
	programmaticBytes := 0
	for index := range programmaticSources {
		programmaticBytes += len(programmaticSources[index].Document)
		programmatic[index] = writerprotocol.Source{
			SourceID: programmaticSources[index].SourceID,
			Document: append([]byte(nil), programmaticSources[index].Document...),
		}
	}
	filesystemBytes := 0
	for index := range filesystem {
		filesystemBytes += len(filesystem[index].Document)
	}
	candidates := snapshot.Candidates()
	wireCandidates := make([]writerprotocol.Candidate, len(candidates))
	for index := range candidates {
		wireCandidates[index] = writerprotocol.Candidate{
			App:      candidates[index].App(),
			Name:     candidates[index].Name(),
			Document: candidates[index].Document(),
		}
	}
	return writerprotocol.Result{
		WriterRoot:            snapshot.WriterRoot(),
		ProjectSpec:           snapshot.ProjectSpec(),
		ProjectSpecDigest:     snapshot.ProjectSpecDigest(),
		ProjectSnapshotSHA256: snapshot.GeneratedBundleSnapshotSHA256(),
		FilesystemCatalog: writerprotocol.CatalogSummary{
			SourceCount:   len(filesystem),
			DocumentBytes: filesystemBytes,
			Digest:        snapshot.FilesystemCatalogDigest(),
		},
		ProgrammaticCatalog: writerprotocol.ProgrammaticCatalog{
			SourceCount:   len(programmatic),
			DocumentBytes: programmaticBytes,
			Digest:        snapshot.ProgrammaticCatalogDigest(),
			Sources:       programmatic,
		},
		DefinitionSetDigest: snapshot.ExistingSemanticDigest(),
		Candidates:          wireCandidates,
	}
}

func classifyMakemigrationsSnapshotFailure(err error) (writerprotocol.Failure, bool) {
	var sourceError *definition.Error
	if errors.As(err, &sourceError) && sourceError != nil {
		candidate := writerprotocol.Failure{Category: sourceError.Category, Code: string(sourceError.Code)}
		return candidate, writerprotocol.IsLinkedFailure(candidate)
	}
	var graphError *migrations.PlanningError
	if errors.As(err, &graphError) && graphError != nil {
		candidate := writerprotocol.Failure{Category: string(graphError.Category), Code: string(graphError.Code)}
		return candidate, writerprotocol.IsLinkedFailure(candidate)
	}
	var detectionError *migrationautodetect.Error
	if errors.As(err, &detectionError) && detectionError != nil {
		candidate := writerprotocol.Failure{Category: writerprotocol.CategoryDetection, Code: string(detectionError.Code)}
		return candidate, writerprotocol.IsLinkedFailure(candidate)
	}
	var snapshotError *projectmigration.Error
	if !errors.As(err, &snapshotError) || snapshotError == nil {
		return writerprotocol.Failure{}, false
	}
	switch snapshotError.Code {
	case projectmigration.CodeInvalidWriterRoot:
		return writerprotocol.Failure{Category: writerprotocol.CategoryDiscovery, Code: writerprotocol.CodeInvalidProjectSourceConfig}, true
	case projectmigration.CodeInvalidProjectSpec, projectmigration.CodeInvalidGeneratorIdentity:
		return writerprotocol.Failure{Category: writerprotocol.CategoryDeclaration, Code: writerprotocol.CodeProjectSpecLoadFailed}, true
	case projectmigration.CodeCatalogResourceLimit:
		return writerprotocol.Failure{Category: writerprotocol.CategoryCandidate, Code: writerprotocol.CodeCandidateResourceLimitExceeded}, true
	case projectmigration.CodeInvalidCatalog:
		return writerprotocol.Failure{Category: writerprotocol.CategoryCandidate, Code: writerprotocol.CodeCandidateValidationFailed}, true
	case projectmigration.CodeUnsupportedChange:
		return writerprotocol.Failure{Category: writerprotocol.CategoryDetection, Code: string(migrationautodetect.CodeUnsupportedChange)}, true
	case projectmigration.CodeInvalidPlan:
		return writerprotocol.Failure{Category: writerprotocol.CategoryDetection, Code: string(migrationautodetect.CodeInvalidGeneratedPlan)}, true
	case projectmigration.CodeCandidateEncodingFailed:
		return writerprotocol.Failure{Category: writerprotocol.CategoryCandidate, Code: writerprotocol.CodeCandidateEncodeFailed}, true
	case projectmigration.CodeInvalidCandidateCatalog, projectmigration.CodeFinalStateMismatch:
		return writerprotocol.Failure{Category: writerprotocol.CategoryCandidate, Code: writerprotocol.CodeCandidateValidationFailed}, true
	default:
		return writerprotocol.Failure{}, false
	}
}

func snapshotMakemigrationsConfig(config MakemigrationsConfig) makemigrationsConfigSnapshot {
	owned := makemigrationsConfigSnapshot{
		ProjectRoot:     config.ProjectRoot,
		LoadProjectSpec: config.LoadProjectSpec,
	}
	if len(config.MigrationDefinitionRoots) > maxRoots {
		owned.invalidProjectSourceConfig = true
	} else {
		for _, root := range config.MigrationDefinitionRoots {
			if len([]byte(root)) > definition.MaxSourceIDBytes {
				owned.invalidProjectSourceConfig = true
				break
			}
		}
		if !owned.invalidProjectSourceConfig {
			owned.MigrationDefinitionRoots = make([]string, len(config.MigrationDefinitionRoots))
			for index := range config.MigrationDefinitionRoots {
				owned.MigrationDefinitionRoots[index] = strings.Clone(config.MigrationDefinitionRoots[index])
			}
		}
	}

	var failed bool
	owned.MigrationDefinitionSources, failed = cloneMakemigrationsSourcesBounded(config.MigrationDefinitionSources)
	owned.programmaticCatalogResourceLimitFailed = failed
	return owned
}

func cloneMakemigrationsSourcesBounded(sources []definition.Source) ([]definition.Source, bool) {
	if len(sources) > definition.MaxSources {
		return nil, true
	}
	var batchBytes uint64
	for index := range sources {
		if len([]byte(sources[index].SourceID)) > definition.MaxSourceIDBytes {
			return nil, true
		}
		documentBytes := uint64(len(sources[index].Document))
		if documentBytes > definition.MaxDocumentBytes || batchBytes > definition.MaxBatchBytes-documentBytes {
			return nil, true
		}
		batchBytes += documentBytes
	}
	if len(sources) == 0 {
		return nil, false
	}
	cloned := make([]definition.Source, len(sources))
	for index := range sources {
		cloned[index] = definition.Source{
			SourceID: strings.Clone(sources[index].SourceID),
			Document: append([]byte(nil), sources[index].Document...),
		}
	}
	return cloned, false
}

func completeMakemigrationsResponse(
	ctx context.Context,
	dependencies systemDependencies,
	writer io.Writer,
	report MakemigrationsReport,
	response writerprotocol.Response,
) (MakemigrationsReport, error) {
	if dependencies.beforeResponseWrite != nil {
		dependencies.beforeResponseWrite()
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	report.RunnerResponseWrites++
	if err := writerprotocol.WriteResponse(writer, response); err != nil {
		return report, err
	}
	return report, nil
}
