//go:build darwin || linux

package linked

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
)

// SQLMigrateConfig contains only invocation-local definition inputs and the
// immutable database-free renderer. It deliberately has no backend opener.
type SQLMigrateConfig struct {
	ProjectRoot                string
	MigrationDefinitionRoots   []string
	MigrationDefinitionSources []definition.Source
	MigrationSQLRenderer       backend.MigrationSQLRenderer
}

// RunSQLMigrate executes one private exact-name forward SQL projection.
// Completed domain failures are written as closed detail-free responses and
// return nil. Caller I/O, cancellation and invariant failures remain Go errors.
func RunSQLMigrate(
	ctx context.Context,
	config SQLMigrateConfig,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) (Report, error) {
	owned := SQLMigrateConfig{
		ProjectRoot:                config.ProjectRoot,
		MigrationDefinitionRoots:   append([]string(nil), config.MigrationDefinitionRoots...),
		MigrationDefinitionSources: cloneDefinitionSources(config.MigrationDefinitionSources),
		MigrationSQLRenderer:       config.MigrationSQLRenderer,
	}
	return runSQLMigrate(ctx, owned, append([]string(nil), argv...), stdin, stdout, systemDependencies{})
}

func runSQLMigrate(
	ctx context.Context,
	config SQLMigrateConfig,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
	dependencies systemDependencies,
) (Report, error) {
	var report Report
	if ctx == nil {
		return report, errors.New("project linked sqlmigrate: nil context")
	}
	if stdin == nil {
		return report, errors.New("project linked sqlmigrate: nil stdin")
	}
	if stdout == nil {
		return report, errors.New("project linked sqlmigrate: nil stdout")
	}
	if len(argv) != 1 || argv[0] != sqlmigrateprotocol.PrivateArgument {
		return report, errors.New("project linked sqlmigrate: invalid private argv")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	request, requestFailure, requestFailed, err := sqlmigrateprotocol.ReadRequest(stdin)
	if err != nil {
		return report, err
	}
	if requestFailed {
		return completeSQLMigrateResponse(
			ctx,
			dependencies,
			stdout,
			report,
			sqlmigrateprotocol.Response{Failure: requestFailure},
			true,
		)
	}
	report.CommandDispatches = 1
	if err := ctx.Err(); err != nil {
		return report, err
	}

	loaded, _, catalogFailure, catalogFailed, err := loadCatalog(
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
		failure := sqlmigrateprotocol.Failure{Category: catalogFailure.Category, Code: catalogFailure.Code}
		if !sqlmigrateprotocol.IsLinkedFailure(failure) {
			return report, errors.New("project linked sqlmigrate: invalid catalog failure")
		}
		return completeSQLMigrateResponse(
			ctx,
			dependencies,
			stdout,
			report,
			sqlmigrateprotocol.Response{Failure: failure},
			true,
		)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	statements, renderErr := migrations.RenderMigrationSQL(
		ctx,
		loaded,
		migrations.MigrationKey{App: request.App, Name: request.Name},
		config.MigrationSQLRenderer,
	)
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if renderErr != nil {
		failure, classified := classifySQLMigrateFailure(renderErr)
		if !classified || !sqlmigrateprotocol.IsLinkedFailure(failure) {
			return report, errors.New("project linked sqlmigrate: invalid SQL projection failure")
		}
		return completeSQLMigrateResponse(
			ctx,
			dependencies,
			stdout,
			report,
			sqlmigrateprotocol.Response{Failure: failure},
			true,
		)
	}

	result := sqlmigrateprotocol.Result{Statements: append([]string(nil), statements...)}
	if len(statements) == 0 {
		result.Statements = make([]string, 0)
	}
	return completeSQLMigrateResponse(
		ctx,
		dependencies,
		stdout,
		report,
		sqlmigrateprotocol.Response{OK: true, Result: result},
		true,
	)
}

func classifySQLMigrateFailure(err error) (sqlmigrateprotocol.Failure, bool) {
	if sqlError, ok := err.(*migrations.MigrationSQLError); ok && sqlError != nil {
		failure := sqlmigrateprotocol.Failure{Category: string(sqlError.Category), Code: string(sqlError.Code)}
		return failure, sqlmigrateprotocol.IsLinkedFailure(failure)
	}
	if planningError, ok := err.(*migrations.PlanningError); ok && planningError != nil {
		failure := sqlmigrateprotocol.Failure{Category: string(planningError.Category), Code: string(planningError.Code)}
		return failure, sqlmigrateprotocol.IsLinkedFailure(failure)
	}
	if migrationError, ok := err.(*migrations.Error); ok && migrationError != nil {
		failure := sqlmigrateprotocol.Failure{Category: string(migrationError.Category), Code: string(migrationError.Code)}
		return failure, sqlmigrateprotocol.IsLinkedFailure(failure)
	}
	return sqlmigrateprotocol.Failure{}, false
}

func completeSQLMigrateResponse(
	ctx context.Context,
	dependencies systemDependencies,
	writer io.Writer,
	report Report,
	response sqlmigrateprotocol.Response,
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
	if err := sqlmigrateprotocol.WriteResponse(writer, response); err != nil {
		return report, fmt.Errorf("project linked sqlmigrate: write response: %w", err)
	}
	return report, nil
}
