//go:build darwin || linux

package linked

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
)

// MigrationBackend is the linked migration lifecycle and its project-owned
// outer resource. The outer Close is deliberately distinct from the fenced
// session cleanup owned by migrations.Executor.
type MigrationBackend interface {
	backend.RevisionFencedBackend
	Close() error
}

// MigrateConfig contains only invocation-local explicit-migrate inputs.
// OpenMigrationBackend is called only after the complete definition catalog
// has loaded successfully.
type MigrateConfig struct {
	ProjectRoot                string
	MigrationDefinitionRoots   []string
	MigrationDefinitionSources []definition.Source
	OpenMigrationBackend       func(context.Context) (MigrationBackend, error)
}

// RunMigrate executes the one private explicit-migrate command. Completed
// product failures are written as closed, detail-free responses and return a
// nil Go error. Caller-owned I/O, cancellation before resource acquisition,
// and internal invariant failures remain Go errors.
func RunMigrate(
	ctx context.Context,
	config MigrateConfig,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) (Report, error) {
	roots := append([]string(nil), config.MigrationDefinitionRoots...)
	sources := cloneDefinitionSources(config.MigrationDefinitionSources)
	arguments := append([]string(nil), argv...)
	owned := MigrateConfig{
		ProjectRoot:                config.ProjectRoot,
		MigrationDefinitionRoots:   roots,
		MigrationDefinitionSources: sources,
		OpenMigrationBackend:       config.OpenMigrationBackend,
	}
	return runMigrate(ctx, owned, arguments, stdin, stdout, systemDependencies{})
}

func runMigrate(
	ctx context.Context,
	config MigrateConfig,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
	dependencies systemDependencies,
) (Report, error) {
	var report Report
	if ctx == nil {
		return report, errors.New("project linked migrate: nil context")
	}
	if stdin == nil {
		return report, errors.New("project linked migrate: nil stdin")
	}
	if stdout == nil {
		return report, errors.New("project linked migrate: nil stdout")
	}
	if len(argv) != 1 || argv[0] != migrateprotocol.PrivateArgument {
		return report, errors.New("project linked migrate: invalid private argv")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	requestFailure, requestFailed, err := migrateprotocol.ReadRequest(stdin)
	if err != nil {
		return report, err
	}
	if requestFailed {
		return completeMigrateResponse(ctx, dependencies, stdout, report, migrateprotocol.Response{Failure: requestFailure}, true)
	}
	report.CommandDispatches = 1
	if err := ctx.Err(); err != nil {
		return report, err
	}

	loaded, summary, catalogFailure, catalogFailed, err := loadCatalog(
		ctx,
		Config{
			ProjectRoot:                config.ProjectRoot,
			MigrationDefinitionRoots:   config.MigrationDefinitionRoots,
			MigrationDefinitionSources: config.MigrationDefinitionSources,
		},
		&report,
		dependencies,
	)
	if err != nil {
		return report, err
	}
	if catalogFailed {
		failure := migrateprotocol.Failure{Category: catalogFailure.Category, Code: catalogFailure.Code}
		if !migrateprotocol.IsLinkedFailure(failure) {
			return report, errors.New("project linked migrate: invalid catalog failure")
		}
		return completeMigrateResponse(ctx, dependencies, stdout, report, migrateprotocol.Response{Failure: failure}, true)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if config.OpenMigrationBackend == nil {
		return completeMigrateResponse(ctx, dependencies, stdout, report, migrateprotocol.Response{Failure: migrateprotocol.Failure{
			Category: migrateprotocol.CategoryBackend,
			Code:     migrateprotocol.CodeInvalidBackend,
		}}, true)
	}

	report.BackendOpenCalls++
	opened, openErr := config.OpenMigrationBackend(ctx)
	acquired := !isNilMigrationBackend(opened)
	if openErr != nil {
		cleanupFailed := false
		if acquired {
			report.BackendCloseCalls++
			cleanupFailed = opened.Close() != nil
		}
		return completeMigrateResponse(ctx, dependencies, stdout, report, migrateprotocol.Response{Failure: migrateprotocol.Failure{
			Category:      migrateprotocol.CategoryBackend,
			Code:          migrateprotocol.CodeBackendOpenFailed,
			CleanupFailed: cleanupFailed,
		}}, false)
	}
	if !acquired {
		return completeMigrateResponse(ctx, dependencies, stdout, report, migrateprotocol.Response{Failure: migrateprotocol.Failure{
			Category: migrateprotocol.CategoryBackend,
			Code:     migrateprotocol.CodeInvalidBackend,
		}}, false)
	}

	report.RevisionLifecycleCalls++
	report.GoDjDBCalls++
	_, migrateErr := (migrations.Executor{Backend: opened}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	report.BackendCloseCalls++
	closeErr := opened.Close()

	if migrateErr == nil && closeErr == nil {
		return completeMigrateResponse(ctx, dependencies, stdout, report, migrateprotocol.Response{OK: true, Result: migrateprotocol.Result{
			SourceCount:         summary.sourceCount,
			DefinitionCount:     summary.definitionCount,
			DefinitionSetDigest: summary.digest,
		}}, false)
	}
	if migrateErr == nil {
		return completeMigrateResponse(ctx, dependencies, stdout, report, migrateprotocol.Response{Failure: migrateprotocol.Failure{
			Category:      migrateprotocol.CategoryBackend,
			Code:          migrateprotocol.CodeBackendCloseFailed,
			CleanupFailed: true,
		}}, false)
	}
	failure := classifyMigrationFailure(migrateErr)
	if closeErr != nil {
		failure.CleanupFailed = true
	}
	if !migrateprotocol.IsLinkedFailure(failure) {
		return report, fmt.Errorf("project linked migrate: invalid classified migration failure")
	}
	return completeMigrateResponse(ctx, dependencies, stdout, report, migrateprotocol.Response{Failure: failure}, false)
}

func completeMigrateResponse(
	ctx context.Context,
	dependencies systemDependencies,
	writer io.Writer,
	report Report,
	response migrateprotocol.Response,
	honorCancellation bool,
) (Report, error) {
	if dependencies.beforeResponseWrite != nil {
		dependencies.beforeResponseWrite()
	}
	if honorCancellation {
		if err := ctx.Err(); err != nil {
			return report, err
		}
	}
	report.RunnerResponseWrites++
	if err := migrateprotocol.WriteResponse(writer, response); err != nil {
		return report, err
	}
	return report, nil
}

func classifyMigrationFailure(err error) migrateprotocol.Failure {
	var primary *migrations.Error
	var planning *migrations.PlanningError
	var recorder *migrations.RecorderError
	var commitUnknown bool
	var rollbackFailed bool
	var commitCleanup bool
	var sessionClose bool

	visitErrorTree(err, func(candidate error) {
		switch value := candidate.(type) {
		case *migrations.Error:
			if value == nil {
				return
			}
			if primary == nil {
				primary = value
			}
			commitUnknown = commitUnknown || value.Code == migrations.CodeCommitOutcomeUnknown
			rollbackFailed = rollbackFailed || value.RollbackCause != nil
			commitCleanup = commitCleanup || value.Code == migrations.CodeCommitCleanupFailed
			sessionClose = sessionClose || value.Code == migrations.CodeSessionCloseFailed
		case *migrations.PlanningError:
			if value != nil && planning == nil {
				planning = value
			}
		case *migrations.RecorderError:
			if value != nil && recorder == nil {
				recorder = value
			}
		}
	})

	switch {
	case commitUnknown:
		return migrateprotocol.Failure{Category: migrateprotocol.CategoryTransaction, Code: string(migrations.CodeCommitOutcomeUnknown)}
	case rollbackFailed:
		return migrateprotocol.Failure{Category: migrateprotocol.CategoryTransaction, Code: migrateprotocol.CodeRollbackFailed}
	case commitCleanup:
		return migrateprotocol.Failure{Category: migrateprotocol.CategoryTransaction, Code: string(migrations.CodeCommitCleanupFailed)}
	case sessionClose:
		return migrateprotocol.Failure{Category: migrateprotocol.CategoryTransaction, Code: string(migrations.CodeSessionCloseFailed)}
	case primary != nil:
		return migrateprotocol.Failure{Category: string(primary.Category), Code: string(primary.Code)}
	case planning != nil:
		return migrateprotocol.Failure{Category: string(planning.Category), Code: string(planning.Code)}
	case recorder != nil:
		return migrateprotocol.Failure{Category: string(recorder.Category), Code: string(recorder.Code)}
	default:
		return migrateprotocol.Failure{Category: migrateprotocol.CategoryInternal, Code: migrateprotocol.CodeProjectInternalError}
	}
}

func visitErrorTree(root error, visit func(error)) {
	queue := []error{root}
	for visited := 0; len(queue) != 0 && visited < 256; visited++ {
		current := queue[0]
		queue = queue[1:]
		if current == nil {
			continue
		}
		visit(current)
		// Core carriers deliberately unwrap raw driver/operation causes for
		// errors.Is/errors.As. Those causes are not taxonomy owners and must not
		// be able to spoof a higher-priority migration classification.
		switch current.(type) {
		case *migrations.Error, *migrations.PlanningError, *migrations.RecorderError:
			continue
		}
		switch value := current.(type) {
		case interface{ Unwrap() []error }:
			queue = append(queue, value.Unwrap()...)
		case interface{ Unwrap() error }:
			queue = append(queue, value.Unwrap())
		}
	}
}

func isNilMigrationBackend(value MigrationBackend) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
