package sqlite

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

type migrationSQLRenderer struct{}

var _ migrationbackend.MigrationSQLRenderer = migrationSQLRenderer{}

// NewMigrationSQLRenderer returns the immutable, zero-configuration SQLite
// forward SQL projector. It owns no database handle or credential.
func NewMigrationSQLRenderer() migrationbackend.MigrationSQLRenderer {
	return migrationSQLRenderer{}
}

func (migrationSQLRenderer) RenderForwardMigrationSQL(
	ctx context.Context,
	request migrationbackend.ForwardMigrationSQLRequest,
) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("render SQLite migration SQL: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validMigrationSQLRequestApp(request.App) || !validMigrationSQLRequestName(request.Name) {
		return nil, errors.New("render SQLite migration SQL: migration identity is invalid")
	}
	if request.Intent.Operations == nil {
		return nil, errors.New("render SQLite migration SQL: migration intent is missing")
	}
	for index := range request.Intent.Operations {
		kind := request.Intent.Operations[index].Kind
		if kind != migrationbackend.MigrationCreateModel && kind != migrationbackend.MigrationAddField {
			return nil, migrationbackend.NewCapabilityError(
				"sqlite_migration_sql",
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
	seal, err := validateAndSealSQLiteRelationIntent(transition, request.Intent)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	statements := make([]string, len(seal.intent.Operations))
	for index := range seal.intent.Operations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		operation := seal.intent.Operations[index]
		switch operation.Kind {
		case migrationbackend.MigrationCreateModel:
			statements[index], err = compileSQLiteRelationCreateModel(operation.After, operation.Targets)
		case migrationbackend.MigrationAddField:
			field := operation.After.Fields[len(operation.After.Fields)-1]
			if field.Kind == ir.FieldForeignKey {
				statements[index], err = compileSQLiteRelationAddField(operation.Before, field, operation.Targets)
			} else {
				statements[index], err = compileMigrationAddField(operation.Before, field)
			}
		default:
			return nil, migrationbackend.NewCapabilityError(
				"sqlite_migration_sql",
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

func validMigrationSQLRequestApp(value string) bool {
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

func validMigrationSQLRequestName(value string) bool {
	return value != "" && utf8.ValidString(value)
}
