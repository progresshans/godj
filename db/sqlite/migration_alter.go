package sqlite

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/progresshans/godj/internal/decimalstorage"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func (transaction *sqliteRevisionFencedTransaction) AlterField(ctx context.Context, model ir.Model, before, after ir.Field) error {
	return transaction.execute(ctx, "alter field", func(executor migrationSQLExecutor) error {
		state := transaction.relation
		if state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected AlterField after intent cursor %d", stateCursor(state))
		}
		operation := state.seal.intent.Operations[state.cursor]
		if operation.Kind != migrationbackend.MigrationAlterField || !reflect.DeepEqual(model, operation.Before) {
			return relationIntentIntegrity("AlterField differs from the sealed model at cursor %d", state.cursor)
		}
		wantBefore, wantAfter, kind, err := migrationbackend.ChangedField(operation.Before, operation.After)
		if err != nil || !before.Equal(wantBefore) || !after.Equal(wantAfter) {
			return relationIntentIntegrity("AlterField differs from the sealed field transition at cursor %d", state.cursor)
		}
		if kind == ir.ChangeDecimalPrecision {
			if err := validateSQLiteDecimalValues(ctx, executor, model, wantBefore, wantAfter); err != nil {
				return err
			}
		}
		// Both supported changes retain the physical column representation.
		// Decimal BLOB keys have no field-scale dependency. BEGIN IMMEDIATE is
		// held through this scan and the catalog/revision/history publication.
		state.cursor++
		return transaction.completeRelationOperationIfLast(ctx, executor)
	})
}

func (transaction *migrationTransaction) AlterField(ctx context.Context, _ ir.Model, _, _ ir.Field) error {
	return transaction.execute(ctx, func(migrationSQLExecutor) error {
		return migrationbackend.NewCapabilityError("alter_field", "field changes require the revision-fenced migration lifecycle", nil)
	})
}

func validateSQLiteFieldDelta(before, after ir.Model) error {
	if err := validateExactNormalizedRelationModel(before); err != nil {
		return err
	}
	if err := validateExactNormalizedRelationModel(after); err != nil {
		return err
	}
	_, _, _, err := migrationbackend.ChangedField(before, after)
	return err
}

func validateSQLiteDecimalValues(ctx context.Context, executor migrationSQLExecutor, model ir.Model, before, after ir.Field) (err error) {
	table, err := quoteIdentifier(model.DBTable)
	if err != nil {
		return err
	}
	column, err := quoteIdentifier(before.Column)
	if err != nil {
		return err
	}
	rows, err := executor.QueryContext(ctx, "SELECT "+column+" FROM "+table)
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
		key, ok := raw.([]byte)
		if !ok {
			return fmt.Errorf("invalid existing Decimal storage in %s.%s", model.DBTable, before.Column)
		}
		value, err := decimalstorage.Decode(key)
		if err != nil || !value.Fits(before.Decimal.MaxDigits, before.Decimal.DecimalPlaces) {
			return fmt.Errorf("existing Decimal violates historical precision in %s.%s", model.DBTable, before.Column)
		}
		if !value.Fits(after.Decimal.MaxDigits, after.Decimal.DecimalPlaces) {
			return migrationbackend.NewCapabilityError("decimal_precision_change", fmt.Sprintf("existing values in %s.%s require an explicit data migration before changing precision; implicit rounding is disabled", model.DBTable, before.Column), nil)
		}
	}
	return errors.Join(rows.Err(), ctx.Err())
}
