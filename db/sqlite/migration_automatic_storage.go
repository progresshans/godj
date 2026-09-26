package sqlite

import (
	"fmt"
	"slices"

	"github.com/progresshans/godj/internal/migrationgraph"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func compileSQLiteStorageOperation(app string, operation mb.MigrationOperation) ([]string, error) {
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
			model.ManyToMany = nil // Auxiliaries are owned by this complete statement group.
			bodies, err := compileSQLiteCreateModelStatements(model, targets)
			if err != nil {
				return nil, err
			}
			statements = append(statements, bodies...)
		case change.After.Name == "":
			body, err := compileMigrationDeleteModel(change.Before)
			if err != nil {
				return nil, err
			}
			statements = append(statements, body)
		default:
			bodies, err := compileSQLiteAutomaticRename(change.Before, change.After)
			if err != nil {
				return nil, err
			}
			statements = append(statements, bodies...)
		}
	}
	return statements, nil
}

func compileSQLiteAutomaticRename(before, after ir.Model) ([]string, error) {
	if !slices.EqualFunc(before.Fields, after.Fields, ir.Field.Equal) || !slices.EqualFunc(before.UniqueConstraints, after.UniqueConstraints, ir.UniqueConstraint.Equal) {
		return nil, fmt.Errorf("automatic rename changes storage fields or constraints")
	}
	if before.DBTable == after.DBTable {
		return nil, nil
	}
	oldTable, err := quoteIdentifier(before.DBTable)
	if err != nil {
		return nil, err
	}
	newTable, err := quoteIdentifier(after.DBTable)
	if err != nil {
		return nil, err
	}
	statements := []string{"ALTER TABLE " + oldTable + " RENAME TO " + newTable}
	for _, constraint := range before.UniqueConstraints {
		body, err := compileSQLiteNamedUniqueAlter(before, constraint, false)
		if err != nil {
			return nil, err
		}
		statements = append(statements, body)
	}
	indexes, err := compileSQLiteUniqueIndexes(after)
	if err != nil {
		return nil, err
	}
	return append(statements, indexes...), nil
}
