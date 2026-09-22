package postgres

import (
	"context"
	"fmt"
	"slices"

	"github.com/progresshans/godj/internal/migrationgraph"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func compilePostgresStorageOperation(namespace, app string, operation mb.MigrationOperation) ([]string, error) {
	graph, err := migrationgraph.ResolveMigrationGraph(app, operation)
	if err != nil {
		return nil, err
	}
	changes, err := operation.StorageChanges(app)
	if err != nil {
		return nil, err
	}
	var statements []string
	for _, change := range changes {
		switch {
		case change.Before.Name == "":
			model := change.After
			targets, err := graph.Targets(ir.ModelIdentity{AppLabel: app, ModelName: model.Name})
			if err != nil {
				return nil, err
			}
			model.ManyToMany = nil // The same owned operation also creates every auxiliary.
			bodies, err := compilePostgresMigrationCreateModel(namespace, model, targets)
			if err != nil {
				return nil, err
			}
			statements = append(statements, bodies...)
		case change.After.Name == "":
			body, err := compilePostgresMigrationDeleteModel(namespace, change.Before)
			if err != nil {
				return nil, err
			}
			statements = append(statements, body)
		default:
			bodies, err := compilePostgresAutomaticRename(namespace, change.Before, change.After)
			if err != nil {
				return nil, err
			}
			statements = append(statements, bodies...)
		}
	}
	return statements, nil
}

func compilePostgresAutomaticRename(namespace string, before, after ir.Model) ([]string, error) {
	if !slices.EqualFunc(before.Fields, after.Fields, ir.Field.Equal) || !slices.EqualFunc(before.UniqueConstraints, after.UniqueConstraints, ir.UniqueConstraint.Equal) {
		return nil, fmt.Errorf("automatic rename changes storage fields or constraints")
	}
	if before.DBTable == after.DBTable {
		return nil, nil
	}
	oldTable, err := quoteTable(namespace, before.DBTable)
	if err != nil {
		return nil, err
	}
	newTable, err := quoteTable(namespace, after.DBTable)
	if err != nil {
		return nil, err
	}
	newName, err := quoteIdentifier(after.DBTable)
	if err != nil {
		return nil, err
	}
	statements := []string{"ALTER TABLE " + oldTable + " RENAME TO " + newName}
	renameConstraint := func(oldName, newName string) error {
		old, err := quoteIdentifier(oldName)
		if err != nil {
			return err
		}
		next, err := quoteIdentifier(newName)
		if err != nil {
			return err
		}
		statements = append(statements, "ALTER TABLE "+newTable+" RENAME CONSTRAINT "+old+" TO "+next)
		return nil
	}
	primary, err := postgresMigrationPrimaryKey(before)
	if err != nil {
		return nil, err
	}
	oldSequence, err := postgresIdentitySequenceName(before.DBTable, primary.Column)
	if err != nil {
		return nil, err
	}
	newSequence, err := postgresIdentitySequenceName(after.DBTable, primary.Column)
	if err != nil {
		return nil, err
	}
	sequence, err := quoteTable(namespace, oldSequence)
	if err != nil {
		return nil, err
	}
	nextSequence, err := quoteIdentifier(newSequence)
	if err != nil {
		return nil, err
	}
	statements = append(statements, "ALTER SEQUENCE "+sequence+" RENAME TO "+nextSequence)
	oldPK, err := postgresPrimaryKeyConstraintName(before.DBTable)
	if err != nil {
		return nil, err
	}
	newPK, err := postgresPrimaryKeyConstraintName(after.DBTable)
	if err != nil {
		return nil, err
	}
	if err := renameConstraint(oldPK, newPK); err != nil {
		return nil, err
	}
	for _, field := range before.Fields {
		if field.Kind != ir.FieldForeignKey {
			continue
		}
		old, err := postgresForeignKeyConstraintName(before.DBTable, field.Column)
		if err != nil {
			return nil, err
		}
		next, err := postgresForeignKeyConstraintName(after.DBTable, field.Column)
		if err != nil {
			return nil, err
		}
		if err := renameConstraint(old, next); err != nil {
			return nil, err
		}
	}
	for _, constraint := range before.UniqueConstraints {
		old, err := postgresNamedUniqueConstraintName(before.DBTable, constraint.Name)
		if err != nil {
			return nil, err
		}
		next, err := postgresNamedUniqueConstraintName(after.DBTable, constraint.Name)
		if err != nil {
			return nil, err
		}
		if err := renameConstraint(old, next); err != nil {
			return nil, err
		}
	}
	return statements, nil
}

// A rename preserves the table OID. Reject external bindings that PostgreSQL
// would otherwise follow silently. The complete migration transaction holds
// the table lock; checks occur after any earlier authored dependency removal.
func assertPostgresAutomaticRenameDependencies(ctx context.Context, executor migrationSQLExecutor, namespace, table string) error {
	var dependent bool
	err := executor.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM pg_catalog.pg_constraint k
 JOIN pg_catalog.pg_class c ON c.oid = k.confrelid
 JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
 WHERE k.contype = 'f' AND n.nspname = $1 AND c.relname = $2
 UNION ALL
 SELECT 1 FROM pg_catalog.pg_depend d
 JOIN pg_catalog.pg_class c ON c.oid = d.refobjid
 JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
 WHERE d.refclassid = 'pg_catalog.pg_class'::regclass
 AND d.classid = 'pg_catalog.pg_rewrite'::regclass
 AND n.nspname = $1 AND c.relname = $2
)`, namespace, table).Scan(&dependent)
	if err != nil {
		return classifyPostgresRevisionContention(ctx, "inspect automatic storage dependencies", err)
	}
	if dependent {
		return postgresMigrationCatalogDrift(table, "has an inbound foreign key or dependent view outside its rename authority")
	}
	return nil
}
