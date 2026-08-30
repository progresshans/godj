package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

type postgresMigrationPreflightTable struct {
	model   ir.Model
	targets []migrationbackend.MigrationTarget
	target  *ir.Field
	oid     int64
}

func (schema *postgresMigrationSchema) Preflight(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace string,
) error {
	if schema == nil {
		return postgresMigrationIntentIntegrity("migration schema is nil", nil)
	}
	if ctx == nil {
		return errors.New("preflight PostgreSQL migration schema: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if executor == nil {
		return postgresMigrationIntentIntegrity("migration schema executor is nil", nil)
	}
	if schema.preflight || schema.namespace != "" {
		return postgresMigrationIntentIntegrity("migration schema physical preflight already completed", nil)
	}
	if err := schema.verifySeal(); err != nil {
		return err
	}
	if err := validateSchemaIdentifier(namespace); err != nil {
		return postgresMigrationIntentIntegrity("migration schema namespace is invalid", err)
	}

	existing, absent, err := schema.postgresMigrationPreflightTables()
	if err != nil {
		return err
	}
	pendingAdds := schema.postgresMigrationPendingAdds()
	for table, model := range absent {
		if err := validatePostgresMigrationAttributeCapacity(table, len(model.Fields), pendingAdds[table]); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(existing))
	for table := range existing {
		names = append(names, table)
	}
	sort.Strings(names)

	// Resolve and validate every pre-existing object before asking PostgreSQL
	// for a table lock. This prevents LOCK TABLE from turning a non-table name
	// or an absent source into a search_path-sensitive error path.
	for _, table := range names {
		catalog, present, err := loadPostgresMigrationTableCatalog(ctx, executor, namespace, table)
		if err != nil {
			return err
		}
		if !present {
			return postgresMigrationCatalogDrift(table, "is missing before the sealed migration")
		}
		if err := assertPostgresMigrationOrdinaryTable(catalog, table); err != nil {
			return err
		}
		entry := existing[table]
		entry.oid = catalog.oid
		existing[table] = entry
	}
	for _, table := range sortedPostgresMigrationModelNames(absent) {
		_, present, err := loadPostgresMigrationTableCatalog(ctx, executor, namespace, table)
		if err != nil {
			return err
		}
		if present {
			return postgresMigrationCatalogDrift(table, "already exists before its sealed CreateModel")
		}
	}

	for _, table := range names {
		qualified, err := quoteTable(namespace, table)
		if err != nil {
			return postgresMigrationIntentIntegrity("quote PostgreSQL migration lock target", err)
		}
		if _, err := executor.ExecContext(ctx, "LOCK TABLE "+qualified+" IN ACCESS EXCLUSIVE MODE NOWAIT"); err != nil {
			return classifyPostgresRevisionContention(ctx, "lock PostgreSQL migration table "+table, err)
		}
	}

	// Re-read the exact OIDs and complete catalog after all locks are held. An
	// OID change between preliminary lookup and lock acquisition is a physical
	// replacement outside the supported current profile.
	for _, table := range names {
		entry := existing[table]
		catalog, present, err := loadPostgresMigrationTableCatalog(ctx, executor, namespace, table)
		if err != nil {
			return err
		}
		if !present || catalog.oid != entry.oid {
			return postgresMigrationCatalogDrift(table, "changed identity during physical preflight")
		}
		if err := validatePostgresMigrationAttributeCapacity(table, catalog.attributeSlots, pendingAdds[table]); err != nil {
			return err
		}
		if entry.target == nil {
			if err := assertPostgresMigrationModelCatalog(catalog, namespace, entry.model, entry.targets); err != nil {
				return err
			}
		} else if len(postgresMigrationRelationFields(entry.model)) == 0 {
			if err := assertPostgresMigrationModelCatalog(catalog, namespace, entry.model, nil); err != nil {
				return err
			}
		} else if err := assertPostgresMigrationTargetCatalog(catalog, namespace, entry.model, *entry.target); err != nil {
			return err
		}
	}
	if err := schema.preflightPostgresRequiredAdds(ctx, executor, namespace, absent); err != nil {
		return err
	}

	schema.namespace = namespace
	schema.preflight = true
	return nil
}

func (schema *postgresMigrationSchema) postgresMigrationPendingAdds() map[string]int {
	result := make(map[string]int)
	for index := range schema.intent.Operations {
		operation := schema.intent.Operations[index]
		if operation.Kind == migrationbackend.MigrationAddField {
			result[operation.Before.DBTable]++
		}
	}
	return result
}

func validatePostgresMigrationAttributeCapacity(table string, physicalSlots, pendingAdds int) error {
	if physicalSlots < 0 || pendingAdds < 0 {
		return postgresMigrationIntentIntegrity("PostgreSQL migration attribute capacity is negative", nil)
	}
	if physicalSlots > postgresMigrationMaxAttributeSlots ||
		pendingAdds > postgresMigrationMaxAttributeSlots-physicalSlots {
		return postgresMigrationCapability(
			fmt.Sprintf(
				"table %s uses %d physical attribute slots and cannot claim %d additions within the PostgreSQL limit %d",
				table,
				physicalSlots,
				pendingAdds,
				postgresMigrationMaxAttributeSlots,
			),
			nil,
		)
	}
	return nil
}

func (schema *postgresMigrationSchema) CreateModel(
	ctx context.Context,
	executor migrationSQLExecutor,
	model ir.Model,
) error {
	operation, err := schema.postgresMigrationCurrentOperation(ctx, executor, migrationbackend.MigrationCreateModel)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(model, operation.After) {
		return postgresMigrationIntentIntegrity("CreateModel arguments differ from the sealed migration operation", nil)
	}
	statement, err := compilePostgresMigrationCreateModel(schema.namespace, operation.After, operation.Targets)
	if err != nil {
		return postgresMigrationIntentIntegrity("compile sealed PostgreSQL CreateModel", err)
	}
	if _, err := executor.ExecContext(ctx, statement); err != nil {
		return classifyPostgresRevisionContention(ctx, "create PostgreSQL model "+model.DBTable, err)
	}
	schema.cursor++
	return nil
}

func (schema *postgresMigrationSchema) DeleteModel(
	ctx context.Context,
	executor migrationSQLExecutor,
	model ir.Model,
) error {
	operation, err := schema.postgresMigrationCurrentOperation(ctx, executor, migrationbackend.MigrationDeleteModel)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(model, operation.Before) {
		return postgresMigrationIntentIntegrity("DeleteModel arguments differ from the sealed migration operation", nil)
	}
	statement, err := compilePostgresMigrationDeleteModel(schema.namespace, operation.Before)
	if err != nil {
		return postgresMigrationIntentIntegrity("compile sealed PostgreSQL DeleteModel", err)
	}
	if _, err := executor.ExecContext(ctx, statement); err != nil {
		return classifyPostgresRevisionContention(ctx, "delete PostgreSQL model "+model.DBTable, err)
	}
	schema.cursor++
	return nil
}

func (schema *postgresMigrationSchema) AddField(
	ctx context.Context,
	executor migrationSQLExecutor,
	model ir.Model,
	field ir.Field,
) error {
	operation, err := schema.postgresMigrationCurrentOperation(ctx, executor, migrationbackend.MigrationAddField)
	if err != nil {
		return err
	}
	changed := operation.After.Fields[len(operation.After.Fields)-1]
	if !reflect.DeepEqual(model, operation.Before) || !migrationFieldsEqual(field, changed) {
		return postgresMigrationIntentIntegrity("AddField arguments differ from the sealed migration operation", nil)
	}
	target, err := postgresMigrationAddFieldTarget(operation, field)
	if err != nil {
		return err
	}
	statement, err := compilePostgresMigrationAddField(schema.namespace, operation.Before, field, target)
	if err != nil {
		return postgresMigrationIntentIntegrity("compile sealed PostgreSQL AddField", err)
	}
	if _, err := executor.ExecContext(ctx, statement); err != nil {
		return classifyPostgresRevisionContention(ctx, "add PostgreSQL field "+model.DBTable+"."+field.Column, err)
	}
	schema.cursor++
	return nil
}

func (schema *postgresMigrationSchema) RemoveField(
	ctx context.Context,
	executor migrationSQLExecutor,
	model ir.Model,
	field ir.Field,
) error {
	operation, err := schema.postgresMigrationCurrentOperation(ctx, executor, migrationbackend.MigrationRemoveField)
	if err != nil {
		return err
	}
	changed := operation.Before.Fields[len(operation.Before.Fields)-1]
	if !reflect.DeepEqual(model, operation.Before) || !migrationFieldsEqual(field, changed) {
		return postgresMigrationIntentIntegrity("RemoveField arguments differ from the sealed migration operation", nil)
	}
	statement, err := compilePostgresMigrationRemoveField(schema.namespace, operation.Before, field)
	if err != nil {
		return postgresMigrationIntentIntegrity("compile sealed PostgreSQL RemoveField", err)
	}
	if _, err := executor.ExecContext(ctx, statement); err != nil {
		return classifyPostgresRevisionContention(ctx, "remove PostgreSQL field "+model.DBTable+"."+field.Column, err)
	}
	schema.cursor++
	return nil
}

func (schema *postgresMigrationSchema) VerifyComplete(
	ctx context.Context,
	executor migrationSQLExecutor,
) error {
	if err := schema.validateOperationContext(ctx); err != nil {
		return err
	}
	if executor == nil {
		return postgresMigrationIntentIntegrity("migration schema executor is nil", nil)
	}
	if schema.cursor != len(schema.intent.Operations) {
		return postgresMigrationIntentIntegrity(
			fmt.Sprintf("migration schema executed %d operations, want %d", schema.cursor, len(schema.intent.Operations)),
			nil,
		)
	}

	finalByTable := make(map[string]postgresMigrationPreflightTable, len(schema.final.models))
	for identity, model := range schema.final.models {
		entry := postgresMigrationPreflightTable{
			model:   model.Clone(),
			targets: clonePostgresMigrationTargets(schema.final.targets[identity]),
		}
		finalByTable[model.DBTable] = entry
	}
	for _, table := range sortedPostgresMigrationPreflightNames(finalByTable) {
		entry := finalByTable[table]
		catalog, present, err := loadPostgresMigrationTableCatalog(ctx, executor, schema.namespace, table)
		if err != nil {
			return err
		}
		if !present {
			return postgresMigrationCatalogDrift(table, "is missing after the sealed migration")
		}
		if err := assertPostgresMigrationModelCatalog(catalog, schema.namespace, entry.model, entry.targets); err != nil {
			return err
		}
	}
	for identity, model := range schema.initial.models {
		if _, remains := schema.final.models[identity]; remains {
			continue
		}
		_, present, err := loadPostgresMigrationTableCatalog(ctx, executor, schema.namespace, model.DBTable)
		if err != nil {
			return err
		}
		if present {
			return postgresMigrationCatalogDrift(model.DBTable, "still exists after its sealed DeleteModel")
		}
	}
	if err := schema.verifyPostgresMigrationTargets(ctx, executor, finalByTable); err != nil {
		return err
	}
	schema.verified = true
	return nil
}

func (schema *postgresMigrationSchema) postgresMigrationCurrentOperation(
	ctx context.Context,
	executor migrationSQLExecutor,
	want migrationbackend.MigrationOperationKind,
) (migrationbackend.MigrationOperation, error) {
	if err := schema.validateOperationContext(ctx); err != nil {
		return migrationbackend.MigrationOperation{}, err
	}
	if executor == nil {
		return migrationbackend.MigrationOperation{}, postgresMigrationIntentIntegrity("migration schema executor is nil", nil)
	}
	if schema.cursor >= len(schema.intent.Operations) {
		return migrationbackend.MigrationOperation{}, postgresMigrationIntentIntegrity("migration schema received an operation after the sealed cursor ended", nil)
	}
	operation := schema.intent.Operations[schema.cursor]
	if operation.Kind != want {
		return migrationbackend.MigrationOperation{}, postgresMigrationIntentIntegrity(
			fmt.Sprintf("migration schema cursor %d has kind %d, caller requested %d", schema.cursor, operation.Kind, want),
			nil,
		)
	}
	return operation, nil
}

func (schema *postgresMigrationSchema) postgresMigrationPreflightTables() (
	map[string]postgresMigrationPreflightTable,
	map[string]ir.Model,
	error,
) {
	existing := make(map[string]postgresMigrationPreflightTable)
	absent := make(map[string]ir.Model)
	for identity, model := range schema.initial.models {
		existing[model.DBTable] = postgresMigrationPreflightTable{
			model:   model.Clone(),
			targets: clonePostgresMigrationTargets(schema.initial.targets[identity]),
		}
	}
	for index := range schema.intent.Operations {
		operation := schema.intent.Operations[index]
		if operation.Kind == migrationbackend.MigrationCreateModel {
			if previous, exists := absent[operation.After.DBTable]; exists && !reflect.DeepEqual(previous, operation.After) {
				return nil, nil, postgresMigrationIntentIntegrity("different sealed CreateModel snapshots share a PostgreSQL table", nil)
			}
			absent[operation.After.DBTable] = operation.After.Clone()
		}
		for targetIndex := range operation.Targets {
			target := operation.Targets[targetIndex]
			if _, created := absent[target.TargetModel.DBTable]; created {
				continue
			}
			if entry, exists := existing[target.TargetModel.DBTable]; exists {
				if !reflect.DeepEqual(entry.model, target.TargetModel) {
					return nil, nil, postgresMigrationIntentIntegrity("sealed source and target snapshots disagree for one PostgreSQL table", nil)
				}
				continue
			}
			targetKey := target.TargetKey.Clone()
			existing[target.TargetModel.DBTable] = postgresMigrationPreflightTable{
				model:  target.TargetModel.Clone(),
				target: &targetKey,
			}
		}
	}
	for table := range absent {
		if _, exists := existing[table]; exists {
			return nil, nil, postgresMigrationIntentIntegrity("sealed PostgreSQL table is both initially present and created", nil)
		}
	}
	return existing, absent, nil
}

func (schema *postgresMigrationSchema) preflightPostgresRequiredAdds(
	ctx context.Context,
	executor migrationSQLExecutor,
	namespace string,
	created map[string]ir.Model,
) error {
	createdAt := make(map[string]int, len(created))
	for position := range schema.intent.Operations {
		operation := schema.intent.Operations[position]
		if operation.Kind == migrationbackend.MigrationCreateModel {
			createdAt[operation.After.DBTable] = position
		}
	}
	for position := range schema.intent.Operations {
		operation := schema.intent.Operations[position]
		if operation.Kind != migrationbackend.MigrationAddField {
			continue
		}
		field := operation.After.Fields[len(operation.After.Fields)-1]
		if !postgresMigrationAddRequiresEmptyTable(field) {
			continue
		}
		if createPosition, createdInIntent := createdAt[operation.Before.DBTable]; createdInIntent {
			if createPosition >= position {
				return postgresMigrationIntentIntegrity("required AddField precedes its sealed CreateModel", nil)
			}
			continue
		}
		table, err := quoteTable(namespace, operation.Before.DBTable)
		if err != nil {
			return postgresMigrationIntentIntegrity("quote PostgreSQL required AddField source", err)
		}
		var populated bool
		if err := executor.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM "+table+" LIMIT 1)").Scan(&populated); err != nil {
			return classifyPostgresRevisionContention(ctx, "inspect PostgreSQL required AddField source "+operation.Before.DBTable, err)
		}
		if populated {
			return postgresMigrationCapability(
				fmt.Sprintf("table %s contains rows; adding field %s with a required value or logical default requires an explicit backfill", operation.Before.DBTable, field.Column),
				nil,
			)
		}
	}
	return nil
}

func postgresMigrationAddRequiresEmptyTable(field ir.Field) bool {
	return field.Default != nil || !field.Nullable
}

func (schema *postgresMigrationSchema) verifyPostgresMigrationTargets(
	ctx context.Context,
	executor migrationSQLExecutor,
	finalByTable map[string]postgresMigrationPreflightTable,
) error {
	deleted := make(map[string]struct{})
	for identity, model := range schema.initial.models {
		if _, remains := schema.final.models[identity]; !remains {
			deleted[model.DBTable] = struct{}{}
		}
	}
	verified := make(map[string]struct{}, len(finalByTable))
	for table := range finalByTable {
		verified[table] = struct{}{}
	}
	for operationIndex := range schema.intent.Operations {
		operation := schema.intent.Operations[operationIndex]
		for targetIndex := range operation.Targets {
			target := operation.Targets[targetIndex]
			table := target.TargetModel.DBTable
			if _, removed := deleted[table]; removed {
				continue
			}
			if _, done := verified[table]; done {
				continue
			}
			catalog, present, err := loadPostgresMigrationTableCatalog(ctx, executor, schema.namespace, table)
			if err != nil {
				return err
			}
			if !present {
				return postgresMigrationCatalogDrift(table, "is missing as a sealed relation target after migration")
			}
			if len(postgresMigrationRelationFields(target.TargetModel)) == 0 {
				if err := assertPostgresMigrationModelCatalog(catalog, schema.namespace, target.TargetModel, nil); err != nil {
					return err
				}
			} else if err := assertPostgresMigrationTargetCatalog(catalog, schema.namespace, target.TargetModel, target.TargetKey); err != nil {
				return err
			}
			verified[table] = struct{}{}
		}
	}
	return nil
}

func sortedPostgresMigrationModelNames(models map[string]ir.Model) []string {
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedPostgresMigrationPreflightNames(entries map[string]postgresMigrationPreflightTable) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
