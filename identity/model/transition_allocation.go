package model

import (
	"context"
	"maps"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
)

// Each callback owns a concrete typed INSERT/update. The graph orders only
// reviewed source identities; callbacks return actual native keys. This owner
// never publishes a partial correspondence after a failed allocation.
type transitionAllocation struct {
	reference transitionReferenceNode
	insert    func(context.Context, db.RelationSession, auth.Principal, transitionTargetMappings) (int64, error)
}

func applyTransitionAllocations(ctx context.Context, scope db.RelationSession, actor auth.Principal, references transitionTargetMappings, allocations []transitionAllocation) (transitionTargetMappings, error) {
	if err := validateAccountSessionScope(ctx, scope); err != nil {
		return nil, err
	}
	nodes := make([]transitionReferenceNode, len(allocations))
	for i, allocation := range allocations {
		if allocation.insert == nil {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		namespace, present := references[allocation.reference.kind]
		if !present || allocation.reference.source.Revision <= 0 || namespace.revisions[allocation.reference.source.ID] != allocation.reference.source.Revision {
			return nil, managementError(CodeConflict, nil)
		}
		nodes[i] = allocation.reference
	}
	order, err := transitionReferenceOrder(nodes)
	if err != nil {
		return nil, err
	}
	allocated := make(transitionTargetMappings, len(references))
	seen := make(map[TransitionReferenceKind]map[int64]int64, len(references))
	for kind, namespace := range references {
		namespace.revisions = maps.Clone(namespace.revisions)
		namespace.targets = maps.Clone(namespace.targets)
		if namespace.targets == nil {
			namespace.targets = make(map[int64]int64)
		}
		allocated[kind] = namespace
		seen[kind] = make(map[int64]int64, len(namespace.targets))
		for source, target := range namespace.targets {
			if _, duplicate := seen[kind][target]; duplicate {
				return nil, managementError(CodeConflict, nil)
			}
			seen[kind][target] = source
		}
	}
	for _, index := range order {
		node := allocations[index]
		if err := validateAccountSessionScope(ctx, scope); err != nil {
			return nil, err
		}
		namespace := allocated[node.reference.kind]
		key, err := node.insert(ctx, scope, actor, allocated)
		if err != nil {
			return nil, err
		}
		if source, duplicate := seen[node.reference.kind][key]; duplicate && source != node.reference.source.ID {
			return nil, managementError(CodeConflict, nil)
		}
		if prior, exists := namespace.targets[node.reference.source.ID]; exists && prior != key {
			return nil, managementError(CodeConflict, nil)
		}
		seen[node.reference.kind][key] = node.reference.source.ID
		namespace.targets[node.reference.source.ID] = key
	}
	if err := validateAccountSessionScope(ctx, scope); err != nil {
		return nil, err
	}
	return allocated, nil
}
