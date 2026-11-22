package sqlite

import (
	"context"

	"github.com/progresshans/godj/internal/migrationgraph"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

var _ backend.ModelRenameEditor = (*sqliteRevisionFencedTransaction)(nil)

func (tx *sqliteRevisionFencedTransaction) RenameModel(ctx context.Context, before, after ir.Model) error {
	return tx.execute(ctx, "rename SQLite model", func(exec migrationSQLExecutor) error {
		state := tx.relation
		if tx.recorderCalled || state == nil || state.cursor >= len(state.seal.intent.Operations) {
			return relationIntentIntegrity("unexpected RenameModel after the sealed cursor")
		}
		if err := verifySQLiteRelationIntentSeal(&state.seal); err != nil {
			return err
		}
		op := state.seal.intent.Operations[state.cursor]
		graph, ok := state.seal.graphPlan.Operation(state.cursor)
		if !ok || op.Kind != backend.MigrationRenameModel || !op.Before.Equal(before) || !op.After.Equal(after) {
			return relationIntentIntegrity("RenameModel arguments differ from the sealed transition")
		}
		tables, err := op.RenamedTables(tx.transition.Migration.App, graph)
		if err != nil {
			return err
		}
		if before.DBTable != after.DBTable {
			catalog, err := loadSQLiteRelationCatalog(ctx, exec)
			if err != nil {
				return err
			}
			physical, err := buildSQLiteRelationPhysicalGraph(catalog)
			if err != nil {
				return err
			}
			inspected := 0
			for _, table := range tables {
				key, err := exactRelationTargetPrimaryKey(table.Before)
				if err != nil {
					return err
				}
				if err := assertSQLiteRenameForeignKeys(ctx, exec, physical, table.Before.DBTable, key.Column, true, table.Incoming, &inspected); err != nil {
					return err
				}
			}
			for _, table := range tables {
				statements, err := compileSQLiteTableRename(table.Before, table.After)
				if err != nil {
					return err
				}
				if err := executeSQLiteMigrationStatements(ctx, exec, statements); err != nil {
					return err
				}
			}
		}
		state.cursor++
		return tx.completeRelationOperationIfLast(ctx, exec)
	})
}

// Project the keys that SQLite will rebind by name, so later operations in
// the same migration start from the correct physical graph.
func (graph sqliteRelationPhysicalGraph) renameTables(tables []migrationgraph.RenamedTable) error {
	names := make(map[string]string)
	type edge struct{ source, target string }
	var edges []edge
	for _, table := range tables {
		old, next := sqliteRelationIdentifierKey(table.Before.DBTable), sqliteRelationIdentifierKey(table.After.DBTable)
		if old == next {
			continue
		}
		if len(graph.incoming[next]) != 0 {
			return relationPhysicalDrift("table rename destination %q already has incoming foreign keys", next)
		}
		names[old] = next
		for owner := range graph.incoming[old] {
			if len(table.Incoming[owner]) == 0 {
				return relationPhysicalDrift("table %q has an undeclared incoming foreign key on %q", old, owner)
			}
			edges = append(edges, edge{owner, old})
		}
		for target := range graph.outgoing[old] {
			edges = append(edges, edge{old, target})
		}
	}
	for _, e := range edges {
		delete(graph.outgoing[e.source], e.target)
		delete(graph.incoming[e.target], e.source)
	}
	for _, e := range edges {
		if next, ok := names[e.source]; ok {
			e.source = next
		}
		if next, ok := names[e.target]; ok {
			e.target = next
		}
		graph.add(e.source, e.target)
	}
	return nil
}
