package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/progresshans/godj/decimal"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func (transaction *postgresRevisionFencedTransaction) AlterField(ctx context.Context, model ir.Model, before, after ir.Field) error {
	return transaction.executeSchema(ctx, "alter PostgreSQL field", func(executor migrationSQLExecutor) error {
		return transaction.schema.AlterField(ctx, executor, model, before, after)
	})
}

func (schema *postgresMigrationSchema) AlterField(ctx context.Context, executor migrationSQLExecutor, model ir.Model, before, after ir.Field) error {
	operation, err := schema.postgresMigrationCurrentOperation(ctx, executor, migrationbackend.MigrationAlterField)
	if err != nil {
		return err
	}
	wantBefore, wantAfter, kind, err := migrationbackend.ChangedField(operation.Before, operation.After)
	if err != nil || !reflect.DeepEqual(model, operation.Before) || !before.Equal(wantBefore) || !after.Equal(wantAfter) {
		return postgresMigrationIntentIntegrity("AlterField arguments differ from the sealed field transition", err)
	}
	if kind == ir.ChangeUnique {
		return postgresMigrationCapability("UniqueConstraints is not implemented by the PostgreSQL migration backend", nil)
	}
	if kind == ir.ChangeDecimalPrecision {
		if err := schema.validateDecimalValues(ctx, executor, model, wantBefore, wantAfter); err != nil {
			return err
		}
		statement, err := compilePostgresDecimalPrecision(schema.namespace, model, wantAfter)
		if err != nil {
			return err
		}
		if _, err := executor.ExecContext(ctx, statement); err != nil {
			return classifyPostgresRevisionContention(ctx, "alter PostgreSQL Decimal precision", err)
		}
	}
	// Existing ACCESS EXCLUSIVE locks cover both the value check and DDL. The
	// complete intent verifies the resulting catalog before history publication.
	schema.cursor++
	return nil
}

func compilePostgresDecimalPrecision(namespace string, model ir.Model, after ir.Field) (string, error) {
	table, err := quoteTable(namespace, model.DBTable)
	if err != nil {
		return "", err
	}
	column, err := quoteIdentifier(after.Column)
	if err != nil {
		return "", err
	}
	if after.Kind != ir.FieldDecimal || after.Decimal == nil || !after.Decimal.Valid() {
		return "", errors.New("invalid Decimal precision for PostgreSQL alteration")
	}
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE NUMERIC(%d,%d)", table, column, after.Decimal.MaxDigits, after.Decimal.DecimalPlaces), nil
}

func (schema *postgresMigrationSchema) validateDecimalValues(ctx context.Context, executor migrationSQLExecutor, model ir.Model, before, after ir.Field) (err error) {
	table, err := quoteTable(schema.namespace, model.DBTable)
	if err != nil {
		return err
	}
	column, err := quoteIdentifier(before.Column)
	if err != nil {
		return err
	}
	// Explicit numeric-to-text output preserves all decimal digits and avoids
	// any floating-point conversion in database/sql. Physical NUMERIC typmod
	// and table identity were checked after acquiring the existing table locks.
	rows, err := executor.QueryContext(ctx, "SELECT "+column+"::text FROM "+table)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw any
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if raw == nil && before.Nullable {
			continue
		}
		text, ok := raw.(string)
		if !ok {
			return fmt.Errorf("invalid existing Decimal storage in %s.%s", model.DBTable, before.Column)
		}
		value, err := decimal.Parse(text)
		if err != nil || !value.Fits(before.Decimal.MaxDigits, before.Decimal.DecimalPlaces) {
			return fmt.Errorf("existing Decimal violates historical precision in %s.%s", model.DBTable, before.Column)
		}
		if !value.Fits(after.Decimal.MaxDigits, after.Decimal.DecimalPlaces) {
			return migrationbackend.NewCapabilityError("decimal_precision_change", fmt.Sprintf("existing values in %s.%s require an explicit data migration before changing precision; implicit rounding is disabled", model.DBTable, before.Column), nil)
		}
	}
	return errors.Join(rows.Err(), ctx.Err())
}
