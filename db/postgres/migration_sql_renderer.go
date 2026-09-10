package postgres

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

// MigrationSQLConfig is the complete immutable PostgreSQL SQL projection
// profile. It deliberately contains no URL, credential, or database handle.
type MigrationSQLConfig struct {
	Schema string
}

type migrationSQLRenderer struct {
	schema string
	valid  bool
}

var _ migrationbackend.MigrationSQLRenderer = migrationSQLRenderer{}

// NewMigrationSQLRenderer snapshots a schema-only PostgreSQL projection
// profile. Invalid configuration is retained as a closed renderer value so a
// later complete-load/target failure keeps precedence over renderer failure.
func NewMigrationSQLRenderer(config MigrationSQLConfig) migrationbackend.MigrationSQLRenderer {
	if err := validateSchemaIdentifier(config.Schema); err != nil {
		return migrationSQLRenderer{}
	}
	return migrationSQLRenderer{schema: strings.Clone(config.Schema), valid: true}
}

func (renderer migrationSQLRenderer) RenderForwardMigrationSQL(
	ctx context.Context,
	request migrationbackend.ForwardMigrationSQLRequest,
) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("render PostgreSQL migration SQL: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !renderer.valid {
		return nil, errors.New("render PostgreSQL migration SQL: renderer configuration is invalid")
	}
	if !validPostgresMigrationSQLApp(request.App) || !validPostgresMigrationSQLName(request.Name) {
		return nil, errors.New("render PostgreSQL migration SQL: migration identity is invalid")
	}
	if request.Intent.Operations == nil {
		return nil, errors.New("render PostgreSQL migration SQL: migration intent is missing")
	}
	for index := range request.Intent.Operations {
		kind := request.Intent.Operations[index].Kind
		if kind != migrationbackend.MigrationCreateModel && kind != migrationbackend.MigrationAddField {
			return nil, migrationbackend.NewCapabilityError(
				"postgres_migration_sql",
				"current SQL projection supports only forward CreateModel and AddField operations",
				nil,
			)
		}
	}

	transition := migrationbackend.HistoryTransition{
		Migration: migrationbackend.AppliedMigration{
			App:  strings.Clone(request.App),
			Name: strings.Clone(request.Name),
		},
		Kind: migrationbackend.HistoryTransitionApply,
	}
	prepared, err := preparePostgresMigrationIntent(transition, request.Intent)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	statements := make([]string, len(prepared.intent.Operations))
	for index := range prepared.intent.Operations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		operation := prepared.intent.Operations[index]
		switch operation.Kind {
		case migrationbackend.MigrationCreateModel:
			statements[index], err = compilePostgresMigrationCreateModel(
				renderer.schema,
				operation.After,
				operation.Targets,
			)
		case migrationbackend.MigrationAddField:
			field := operation.After.Fields[len(operation.After.Fields)-1]
			target, targetErr := postgresMigrationAddFieldTarget(operation, field)
			if targetErr != nil {
				return nil, targetErr
			}
			statements[index], err = compilePostgresMigrationAddField(
				renderer.schema,
				operation.Before,
				field,
				target,
			)
		default:
			return nil, migrationbackend.NewCapabilityError(
				"postgres_migration_sql",
				"validated SQL projection contains an unsupported operation",
				nil,
			)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return statements, nil
}

func validPostgresMigrationSQLApp(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if character == '_' || character >= 'a' && character <= 'z' ||
			index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func validPostgresMigrationSQLName(value string) bool {
	return value != "" && utf8.ValidString(value)
}
