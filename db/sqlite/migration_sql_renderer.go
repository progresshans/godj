package sqlite

import (
	"context"
	"errors"
	"unicode/utf8"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

type migrationSQLRenderer struct{}

var _ migrationbackend.MigrationSQLRenderer = migrationSQLRenderer{}

// NewMigrationSQLRenderer returns the immutable, zero-configuration SQLite
// SQL projector. It owns no database handle or credential.
func NewMigrationSQLRenderer() migrationbackend.MigrationSQLRenderer {
	return migrationSQLRenderer{}
}

func (migrationSQLRenderer) RenderMigrationSQL(
	ctx context.Context,
	request migrationbackend.MigrationSQLRequest,
) ([][]string, error) {
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
	for _, operation := range request.Intent.Operations {
		switch operation.Kind {
		case migrationbackend.MigrationDeleteModel:
			if request.Backwards {
				continue
			}
		case migrationbackend.MigrationRunData, migrationbackend.MigrationRenameField, migrationbackend.MigrationCreateModel,
			migrationbackend.MigrationAddField, migrationbackend.MigrationRemoveField, migrationbackend.MigrationAlterField,
			migrationbackend.MigrationAddConstraint, migrationbackend.MigrationRemoveConstraint, migrationbackend.MigrationAlterManyToMany:
			continue
		}
		return nil, migrationbackend.NewCapabilityError("sqlite_migration_sql", "unsupported operation for this SQL direction", nil)
	}
	transition := request.Transition()
	seal, err := validateAndSealSQLiteRelationIntent(transition, request.Intent)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	groups := make([][]string, len(seal.intent.Operations))
	for index := range seal.intent.Operations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		operation := seal.intent.Operations[index]
		switch operation.Kind {
		case migrationbackend.MigrationRunData:
			groups[index] = []string{migrationbackend.RunDataSQLComment}
			continue
		case migrationbackend.MigrationCreateModel, migrationbackend.MigrationDeleteModel, migrationbackend.MigrationAlterManyToMany:
			groups[index], err = compileSQLiteStorageOperation(request.App, operation)
		case migrationbackend.MigrationAddConstraint, migrationbackend.MigrationRemoveConstraint:
			constraint, deltaErr := operation.ChangedConstraint()
			if deltaErr != nil {
				return nil, relationIntentIntegrity("invalid constraint delta: %v", deltaErr)
			}
			var statement string
			statement, err = compileSQLiteNamedUniqueAlter(operation.Before, constraint, operation.Kind == migrationbackend.MigrationAddConstraint)
			groups[index] = []string{statement}
		case migrationbackend.MigrationRenameField:
			before, after, deltaErr := operation.RenamedField(request.App)
			if deltaErr != nil {
				return nil, relationIntentIntegrity("invalid field rename: %v", deltaErr)
			}
			groups[index], err = compileSQLiteFieldRename(operation.Before, before, after)
		case migrationbackend.MigrationAlterField:
			before, field, _, deltaErr := migrationbackend.ChangedField(operation.Before, operation.After)
			if deltaErr != nil {
				return nil, relationIntentIntegrity("invalid AlterField delta: %v", deltaErr)
			}
			if sqliteRelationOperationNeedsRemake(operation) {
				groups[index], err = compileSQLiteRelationRemakeSQL(transition, operation)
			} else {
				groups[index], err = compileSQLiteFieldIndexes(operation.After, before, field)
			}
		case migrationbackend.MigrationAddField:
			field, deltaErr := operation.ChangedField()
			if deltaErr != nil {
				return nil, relationIntentIntegrity("invalid AddField delta: %v", deltaErr)
			}
			if sqliteRelationOperationNeedsRemake(operation) {
				groups[index], err = compileSQLiteRelationRemakeSQL(transition, operation)
			} else {
				groups[index], err = compileSQLiteAddFieldStatements(operation.Before, field, operation.Targets)
			}
		case migrationbackend.MigrationRemoveField:
			field, deltaErr := operation.ChangedField()
			if deltaErr != nil {
				return nil, relationIntentIntegrity("invalid RemoveField delta: %v", deltaErr)
			}
			if sqliteRelationOperationNeedsRemake(operation) {
				groups[index], err = compileSQLiteRelationRemakeSQL(transition, operation)
			} else {
				var statement string
				statement, err = compileMigrationRemoveField(operation.Before, field)
				if err == nil {
					removed := field.Clone()
					removed.Unique, removed.DBIndex = false, false
					groups[index], err = compileSQLiteFieldIndexes(operation.Before, field, removed)
					groups[index] = append(groups[index], statement)
				}
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
	return groups, nil
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
