package orm

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/relationpolicy"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type relationDeleteNode struct {
	model    ir.Model
	key      ir.Field
	incoming []relationDeleteEdge
}

type relationDeleteRow struct {
	model ir.ModelIdentity
	key   int64
}

func relationDeleteGraph(snapshot *projectBindingSnapshot, target ir.ModelIdentity) (map[ir.ModelIdentity]relationDeleteNode, error) {
	incoming := make(map[ir.ModelIdentity][]RelationMetadata)
	for _, metadata := range snapshot.forward {
		incoming[metadata.Target] = append(incoming[metadata.Target], metadata)
	}
	graph := make(map[ir.ModelIdentity]relationDeleteNode)
	seen := map[ir.ModelIdentity]bool{target: true}
	queue := []ir.ModelIdentity{target}
	for cursor := 0; cursor < len(queue); cursor++ {
		identity := queue[cursor]
		model, exists := snapshot.models[identity]
		key, valid := relationDeleteTargetKey(model)
		if !exists || !valid {
			return nil, relationInvalidPlan("relation delete graph model must contain supported fields and exactly one AutoField primary key")
		}
		node := relationDeleteNode{model: model.Clone(), key: key.Clone()}
		for _, metadata := range incoming[identity] {
			if !metadata.Cardinality.SingleValued() || !metadata.OnDelete.Valid() || metadata.OnDelete == ir.DeleteSetNull && !metadata.Nullable {
				return nil, relationInvalidPlan("incoming relation uses an unsupported cardinality, policy, or nullability")
			}
			source, exists := snapshot.models[metadata.Source]
			primaryKey, valid := relationAutoPrimaryKey(source)
			if !exists || !valid {
				return nil, relationInvalidPlan("incoming relation source must have exactly one AutoField primary key")
			}
			foreignKey, exists := findField(source.Fields, metadata.Field)
			if !exists || foreignKey.Kind != ir.FieldForeignKey || foreignKey.PrimaryKey ||
				foreignKey.Column != metadata.Column || foreignKey.Nullable != metadata.Nullable ||
				foreignKey.Relation == nil || foreignKey.Relation.Target != metadata.Target ||
				foreignKey.Relation.Cardinality != metadata.Cardinality || foreignKey.Relation.Reverse != metadata.Reverse ||
				foreignKey.Relation.OnDelete != metadata.OnDelete {
				return nil, relationInvalidPlan("incoming relation metadata disagrees with its source ForeignKey")
			}
			node.incoming = append(node.incoming, relationDeleteEdge{metadata: metadata, sourceModel: source.Clone(),
				sourcePrimaryKey: primaryKey.Clone(), sourceForeignKey: foreignKey.Clone()})
			if metadata.OnDelete == ir.DeleteCascade && !seen[metadata.Source] {
				seen[metadata.Source] = true
				queue = append(queue, metadata.Source)
			}
		}
		slices.SortFunc(node.incoming, func(left, right relationDeleteEdge) int { return compareForward(left.metadata, right.metadata) })
		graph[identity] = node
	}
	return graph, nil
}

func relationDeletePolicyFingerprint(target ir.ModelIdentity, model ir.Model, key ir.Field, graph map[ir.ModelIdentity]relationDeleteNode) string {
	incoming := make(map[ir.ModelIdentity][]relationpolicy.Edge, len(graph))
	for identity, node := range graph {
		for _, edge := range node.incoming {
			incoming[identity] = append(incoming[identity], relationpolicy.Edge{
				Source: relationpolicy.ModelKey{Identity: edge.metadata.Source, Table: edge.sourceModel.DBTable,
					PrimaryKeyName: edge.sourcePrimaryKey.Name, PrimaryKeyColumn: edge.sourcePrimaryKey.Column},
				Field: edge.sourceForeignKey.Name, Column: edge.sourceForeignKey.Column,
				Nullable: edge.metadata.Nullable, Cardinality: edge.metadata.Cardinality, OnDelete: edge.metadata.OnDelete,
			})
		}
	}
	return relationpolicy.Fingerprint(relationpolicy.ModelKey{Identity: target, Table: model.DBTable,
		PrimaryKeyName: key.Name, PrimaryKeyColumn: key.Column}, incoming)
}

func (state relationDeleteState[M]) execute(ctx context.Context, session db.RelationSession, targetKey int64) (int64, error) {
	if interfaceIsNil(session) {
		return 0, relationBackendInvalidPlan("relation atomic backend supplied a nil session")
	}
	root := relationDeleteRow{model: state.target, key: targetKey}
	queue := []relationDeleteRow{root}
	seen := map[relationDeleteRow]bool{root: true}
	protected := make(map[relationDeleteRow]bool)
	var setNull []query.RelationSetNullPlan
	for cursor := 0; cursor < len(queue); cursor++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		row := queue[cursor]
		for _, edge := range state.graph[row.model].incoming {
			if edge.metadata.OnDelete == ir.DeleteSetNull {
				setNull = append(setNull, query.NewRelationSetNullPlan(edge.sourceModel.DBTable, fieldReference(edge.sourceForeignKey), query.Integer(row.key)))
				continue
			}
			keys, err := collectRelationDeleteRows(ctx, session, edge, row.key)
			if err != nil {
				return 0, err
			}
			for _, key := range keys {
				source := relationDeleteRow{model: edge.metadata.Source, key: key}
				if edge.metadata.OnDelete == ir.DeleteProtect {
					protected[source] = true
				} else if !seen[source] {
					seen[source] = true
					queue = append(queue, source)
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(protected) != 0 {
		err, constructionErr := query.NewProtectedForeignKeyError(int64(len(protected)))
		if constructionErr != nil {
			return 0, constructionErr
		}
		return 0, err
	}
	for _, plan := range setNull {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		count, err := session.RelationSetNull(ctx, plan)
		if err != nil {
			return 0, err
		}
		if count < 0 {
			return 0, relationBackendInvalidPlan("relation SET_NULL returned a negative affected-row count")
		}
	}
	// Leaves normally go first. Required cycles are handled by the backend's
	// deferred CASCADE constraints, not by a guessed topological order.
	for index := len(queue) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		row := queue[index]
		node := state.graph[row.model]
		count, err := session.Delete(ctx, query.NewDeletePlan(node.model.DBTable, fieldReference(node.key), query.Integer(row.key)))
		if err != nil {
			return 0, err
		}
		if count != 1 {
			return 0, unexpectedRows("relation delete target", count)
		}
	}
	return int64(len(queue)), ctx.Err()
}

func collectRelationDeleteRows(ctx context.Context, session db.RelationSession, edge relationDeleteEdge, targetKey int64) ([]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	foreignKey := fieldReference(edge.sourceForeignKey)
	expression, err := query.NewExpression(query.NewCondition(foreignKey, query.LookupExact, query.Integer(targetKey)))
	if err != nil {
		return nil, err
	}
	plan, err := query.NewPlan(edge.sourceModel.DBTable, []query.FieldRef{fieldReference(edge.sourcePrimaryKey), foreignKey}).WithWhere(expression)
	if err != nil {
		return nil, err
	}
	rows, err := session.Query(ctx, plan)
	if err != nil {
		if !interfaceIsNil(rows) {
			if closeErr := rows.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close relation delete rows returned with backend error: %w", closeErr))
			}
		}
		return nil, joinContextErr(err, ctx)
	}
	if interfaceIsNil(rows) {
		return nil, joinContextErr(relationBackendInvalidPlan("relation session returned nil delete rows without an error"), ctx)
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	keys := make(map[int64]struct{})
	for rows.Next() {
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
			break
		}
		var sourcePrimaryKey, sourceForeignKey any
		if scanErr := rows.Scan(&sourcePrimaryKey, &sourceForeignKey); scanErr != nil {
			err = fmt.Errorf("scan relation delete row: %w", scanErr)
			break
		}
		primaryKey, primaryOK := sourcePrimaryKey.(int64)
		foreignKey, foreignOK := sourceForeignKey.(int64)
		if !primaryOK || !foreignOK || foreignKey != targetKey {
			err = relationBackendInvalidPlan("relation delete row did not contain the exact integer primary and target keys")
			break
		}
		keys[primaryKey] = struct{}{}
	}
	if err := lifecycle.finish(ctx, err); err != nil {
		return nil, err
	}
	result := make([]int64, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	slices.Sort(result)
	return result, nil
}
