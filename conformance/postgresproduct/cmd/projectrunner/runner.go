package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
)

var (
	productIDField      = query.NewFieldRef("id", "id", query.FieldInteger, false)
	productTitleField   = query.NewFieldRef("title", "title", query.FieldString, false)
	productSummaryField = query.NewFieldRef("summary", "summary", query.FieldString, true)
)

type productRow struct {
	id      int64
	title   string
	summary sql.NullString
}

type catalogColumn struct {
	name               string
	typeName           string
	nullable           string
	maximumLength      int64
	identity           string
	identityGeneration string
}

func executeMode(ctx context.Context, config runnerConfig, mode string) (modeResult, error) {
	switch mode {
	case "prepare":
		return prepareProduct(ctx, config)
	case "probe":
		return probePreparedProduct(ctx, config)
	case "resume":
		return resumeProduct(ctx, config)
	case "verify":
		return verifyProduct(ctx, config)
	case "cleanup":
		return cleanupProduct(ctx, config)
	default:
		return modeResult{}, errInvalidArguments
	}
}

func prepareProduct(ctx context.Context, config runnerConfig) (modeResult, error) {
	if err := createExactSchema(ctx, config); err != nil {
		return modeResult{}, err
	}
	err := withProductBackend(ctx, config, func(backend *postgres.Backend) error {
		if err := requireHistory(ctx, backend, nil); err != nil {
			return err
		}
		loaded, err := loadProductDefinitions()
		if err != nil {
			return err
		}
		if _, err := (migrations.Executor{Backend: backend}).Migrate(
			ctx,
			loaded,
			migrations.TargetedLifecycleRequest(migrations.NamedTarget(initialMigration)),
		); err != nil {
			return fmt.Errorf("apply initial PostgreSQL product migration: %w", err)
		}
		if err := requireHistory(ctx, backend, initialHistory()); err != nil {
			return err
		}
		identifier, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(
			productTable,
			[]query.Assignment{query.NewAssignment(productTitleField, query.String("prepared"))},
			productIDField,
		))
		if err != nil {
			return fmt.Errorf("insert prepared PostgreSQL product row: %w", err)
		}
		if identifier != 1 {
			return fmt.Errorf("prepared PostgreSQL product key = %d, want 1", identifier)
		}
		rows, err := readProductRows(ctx, backend, false)
		if err != nil {
			return err
		}
		return requireProductRows(rows, []productRow{{id: 1, title: "prepared"}})
	})
	if err != nil {
		return modeResult{}, err
	}
	return modeResult{Mode: "prepare", Status: "ok", History: 1, Rows: 1}, nil
}

func probePreparedProduct(ctx context.Context, config runnerConfig) (modeResult, error) {
	if err := withProductBackend(ctx, config, func(backend *postgres.Backend) error {
		return requirePreparedProduct(ctx, backend)
	}); err != nil {
		return modeResult{}, err
	}
	return modeResult{Mode: "probe", Status: "ok", History: 1, Rows: 1}, nil
}

func resumeProduct(ctx context.Context, config runnerConfig) (modeResult, error) {
	err := withProductBackend(ctx, config, func(backend *postgres.Backend) error {
		if err := requirePreparedProduct(ctx, backend); err != nil {
			return err
		}
		loaded, err := loadProductDefinitions()
		if err != nil {
			return err
		}
		if _, err := (migrations.Executor{Backend: backend}).Migrate(
			ctx,
			loaded,
			migrations.LatestLifecycleRequest(),
		); err != nil {
			return fmt.Errorf("resume PostgreSQL product migration lifecycle: %w", err)
		}
		if err := requireHistory(ctx, backend, completeHistory()); err != nil {
			return err
		}
		afterMigration, err := readProductRows(ctx, backend, true)
		if err != nil {
			return err
		}
		if err := requireProductRows(afterMigration, []productRow{{id: 1, title: "prepared"}}); err != nil {
			return err
		}
		identifier, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(
			productTable,
			[]query.Assignment{
				query.NewAssignment(productTitleField, query.String("resumed")),
				query.NewAssignment(productSummaryField, query.String("after-restart")),
			},
			productIDField,
		))
		if err != nil {
			return fmt.Errorf("insert resumed PostgreSQL product row: %w", err)
		}
		// PostgreSQL sequences guarantee uniqueness, not gapless numbering.
		if identifier <= 1 {
			return fmt.Errorf("resumed PostgreSQL product key = %d, want greater than 1", identifier)
		}
		rows, err := readProductRows(ctx, backend, true)
		if err != nil {
			return err
		}
		return requireProductRows(rows, []productRow{
			{id: 1, title: "prepared"},
			{id: identifier, title: "resumed", summary: sql.NullString{String: "after-restart", Valid: true}},
		})
	})
	if err != nil {
		return modeResult{}, err
	}
	return modeResult{Mode: "resume", Status: "ok", History: 2, Rows: 2}, nil
}

func requirePreparedProduct(ctx context.Context, backend *postgres.Backend) error {
	if err := requireHistory(ctx, backend, initialHistory()); err != nil {
		return err
	}
	rows, err := readProductRows(ctx, backend, false)
	if err != nil {
		return err
	}
	return requireProductRows(rows, []productRow{{id: 1, title: "prepared"}})
}

func verifyProduct(ctx context.Context, config runnerConfig) (modeResult, error) {
	if err := withProductBackend(ctx, config, func(backend *postgres.Backend) error {
		if err := requireHistory(ctx, backend, completeHistory()); err != nil {
			return err
		}
		rows, err := readProductRows(ctx, backend, true)
		if err != nil {
			return err
		}
		return requireCompleteProductRows(rows)
	}); err != nil {
		return modeResult{}, err
	}
	if err := requireProductCatalog(ctx, config); err != nil {
		return modeResult{}, err
	}
	return modeResult{Mode: "verify", Status: "ok", History: 2, Rows: 2}, nil
}

func cleanupProduct(ctx context.Context, config runnerConfig) (modeResult, error) {
	// Revalidate at the destructive boundary even though environment parsing has
	// already validated the value. Only this exact quoted schema is dropped.
	if err := validateRunnerSchema(config.schema); err != nil {
		return modeResult{}, err
	}
	if err := withControlConnection(ctx, config.databaseURL, func(connection *pgx.Conn) error {
		statement := "DROP SCHEMA IF EXISTS " + pgx.Identifier{config.schema}.Sanitize() + " CASCADE"
		if _, err := connection.Exec(ctx, statement); err != nil {
			return sanitizeControlError(ctx, "drop exact schema", err)
		}
		return nil
	}); err != nil {
		return modeResult{}, err
	}
	return modeResult{Mode: "cleanup", Status: "ok"}, nil
}

func createExactSchema(ctx context.Context, config runnerConfig) error {
	if err := validateRunnerSchema(config.schema); err != nil {
		return err
	}
	return withControlConnection(ctx, config.databaseURL, func(connection *pgx.Conn) error {
		statement := "CREATE SCHEMA " + pgx.Identifier{config.schema}.Sanitize()
		if _, err := connection.Exec(ctx, statement); err != nil {
			return sanitizeControlError(ctx, "create exact schema", err)
		}
		return nil
	})
}

func withControlConnection(
	ctx context.Context,
	databaseURL string,
	operation func(*pgx.Conn) error,
) (resultErr error) {
	connectionConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return errors.New("PostgreSQL control URL is invalid")
	}
	if connectionConfig.RuntimeParams == nil {
		connectionConfig.RuntimeParams = make(map[string]string)
	}
	connectionConfig.RuntimeParams["client_encoding"] = "UTF8"
	connectionConfig.RuntimeParams["search_path"] = "pg_catalog"
	connectionConfig.RuntimeParams["standard_conforming_strings"] = "on"
	connectionConfig.RuntimeParams["timezone"] = "UTC"
	connection, err := pgx.ConnectConfig(ctx, connectionConfig)
	if err != nil {
		return sanitizeControlError(ctx, "connect", err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if closeErr := connection.Close(cleanupContext); closeErr != nil {
			resultErr = errors.Join(resultErr, sanitizeControlError(cleanupContext, "close", closeErr))
		}
	}()
	return operation(connection)
}

func sanitizeControlError(ctx context.Context, operation string, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		return fmt.Errorf("PostgreSQL control %s failed with SQLSTATE %s", operation, sqlState.SQLState())
	}
	return fmt.Errorf("PostgreSQL control %s failed", operation)
}

func withProductBackend(
	ctx context.Context,
	config runnerConfig,
	operation func(*postgres.Backend) error,
) (resultErr error) {
	backend, err := postgres.Open(ctx, postgres.Config{URL: config.databaseURL, Schema: config.schema})
	if err != nil {
		return fmt.Errorf("open PostgreSQL product backend: %w", err)
	}
	defer func() {
		if closeErr := backend.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, errors.New("close PostgreSQL product backend failed"))
		}
	}()
	return operation(backend)
}

func initialHistory() []migrationbackend.AppliedMigration {
	return []migrationbackend.AppliedMigration{{App: productApp, Name: initialMigration.Name}}
}

func completeHistory() []migrationbackend.AppliedMigration {
	return []migrationbackend.AppliedMigration{
		{App: productApp, Name: initialMigration.Name},
		{App: productApp, Name: summaryMigration.Name},
	}
}

func requireHistory(
	ctx context.Context,
	backend *postgres.Backend,
	want []migrationbackend.AppliedMigration,
) error {
	got, err := backend.ReadAppliedMigrations(ctx)
	if err != nil {
		return fmt.Errorf("read PostgreSQL product migration history: %w", err)
	}
	if len(got) != len(want) {
		return fmt.Errorf("PostgreSQL product history length = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			return fmt.Errorf("PostgreSQL product history[%d] = %+v, want %+v", index, got[index], want[index])
		}
	}
	return nil
}

func readProductRows(ctx context.Context, backend *postgres.Backend, includeSummary bool) (_ []productRow, resultErr error) {
	fields := []query.FieldRef{productIDField, productTitleField}
	if includeSummary {
		fields = append(fields, productSummaryField)
	}
	plan := query.NewPlan(productTable, fields).WithOrderings(query.NewOrdering(productIDField, query.Ascending))
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("query PostgreSQL product rows: %w", err)
	}
	if rows == nil {
		return nil, errors.New("PostgreSQL product query returned nil rows")
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close PostgreSQL product rows: %w", closeErr))
		}
	}()
	values := make([]productRow, 0, 2)
	for rows.Next() {
		var value productRow
		if includeSummary {
			err = rows.Scan(&value.id, &value.title, &value.summary)
		} else {
			err = rows.Scan(&value.id, &value.title)
		}
		if err != nil {
			return nil, fmt.Errorf("scan PostgreSQL product row: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL product rows: %w", err)
	}
	return values, nil
}

func requireProductRows(got, want []productRow) error {
	if len(got) != len(want) {
		return fmt.Errorf("PostgreSQL product row count = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			return fmt.Errorf("PostgreSQL product row[%d] = %+v, want %+v", index, got[index], want[index])
		}
	}
	return nil
}

func requireCompleteProductRows(got []productRow) error {
	if len(got) != 2 {
		return fmt.Errorf("PostgreSQL product row count = %d, want 2", len(got))
	}
	wantPrepared := productRow{id: 1, title: "prepared"}
	if got[0] != wantPrepared {
		return fmt.Errorf("PostgreSQL product row[0] = %+v, want %+v", got[0], wantPrepared)
	}
	wantResumed := productRow{
		id:      got[1].id,
		title:   "resumed",
		summary: sql.NullString{String: "after-restart", Valid: true},
	}
	if got[1].id <= got[0].id || got[1] != wantResumed {
		return fmt.Errorf("PostgreSQL product row[1] = %+v, want resumed row with key greater than %d", got[1], got[0].id)
	}
	return nil
}

func requireProductCatalog(ctx context.Context, config runnerConfig) error {
	return withControlConnection(ctx, config.databaseURL, func(connection *pgx.Conn) error {
		rows, err := connection.Query(ctx, `SELECT
			column_name,
			data_type,
			is_nullable,
			COALESCE(character_maximum_length, 0)::bigint,
			is_identity,
			COALESCE(identity_generation, '')
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
		ORDER BY ordinal_position`, config.schema, productTable)
		if err != nil {
			return sanitizeControlError(ctx, "query product catalog", err)
		}
		defer rows.Close()
		got := make([]catalogColumn, 0, 3)
		for rows.Next() {
			var column catalogColumn
			if err := rows.Scan(
				&column.name,
				&column.typeName,
				&column.nullable,
				&column.maximumLength,
				&column.identity,
				&column.identityGeneration,
			); err != nil {
				return sanitizeControlError(ctx, "scan product catalog", err)
			}
			got = append(got, column)
		}
		if err := rows.Err(); err != nil {
			return sanitizeControlError(ctx, "iterate product catalog", err)
		}
		want := []catalogColumn{
			{name: "id", typeName: "bigint", nullable: "NO", identity: "YES", identityGeneration: "BY DEFAULT"},
			{name: "title", typeName: "character varying", nullable: "NO", maximumLength: 120, identity: "NO"},
			{name: "summary", typeName: "character varying", nullable: "YES", maximumLength: 200, identity: "NO"},
		}
		if len(got) != len(want) {
			return fmt.Errorf("PostgreSQL product catalog column count = %d, want %d", len(got), len(want))
		}
		for index := range want {
			if got[index] != want[index] {
				return fmt.Errorf("PostgreSQL product catalog[%d] = %+v, want %+v", index, got[index], want[index])
			}
		}
		return nil
	})
}
