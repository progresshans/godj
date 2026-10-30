package model

import (
	"context"
	"crypto/sha256"
	"maps"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type transitionCreationPhase[M any] struct {
	allocating bool
	current    *M
	deferred   map[string]bool
}

// Each callback owns a concrete typed INSERT/update. The graph orders only
// reviewed source identities; callbacks return actual native keys. This owner
// never publishes a partial correspondence after a failed allocation.
type transitionAllocation struct {
	reference transitionReferenceNode
	insert    func(context.Context, db.RelationSession, auth.Principal, transitionTargetMappings, map[string]bool) (int64, error)
	finish    func(context.Context, db.RelationSession, auth.Principal, transitionTargetMappings, map[string]bool) error
	values    func() map[string]query.Value
}

func applyTransitionAllocations(ctx context.Context, scope db.RelationSession, actor auth.Principal, references transitionTargetMappings, allocations []transitionAllocation) (transitionTargetMappings, error) {
	if err := validateAccountSessionScope(ctx, scope); err != nil {
		return nil, err
	}
	nodes := make([]transitionReferenceNode, len(allocations))
	for i, allocation := range allocations {
		if allocation.insert == nil || allocation.values == nil {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		namespace, present := references[allocation.reference.kind]
		if !present || allocation.reference.source.Revision <= 0 || namespace.revisions[allocation.reference.source.ID] != allocation.reference.source.Revision {
			return nil, managementError(CodeConflict, nil)
		}
		nodes[i] = allocation.reference
	}
	plan, err := planTransitionReferences(nodes)
	if err != nil {
		return nil, err
	}
	hasDeferred := false
	for i, fields := range plan.deferred {
		if len(fields) != 0 {
			hasDeferred = true
			if allocations[i].finish == nil {
				return nil, managementError(CodeInvalidConfig, nil)
			}
		}
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
	budget := &transitionDigestWriter{hash: sha256.New()}
	for _, index := range plan.order {
		node := allocations[index]
		if err := validateAccountSessionScope(ctx, scope); err != nil {
			return nil, err
		}
		namespace := allocated[node.reference.kind]
		key, err := node.insert(ctx, scope, actor, allocated, maps.Clone(plan.deferred[index]))
		if err != nil {
			return nil, err
		}
		if err := writeTransitionValues(budget, node.values()); err != nil {
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
	for _, index := range plan.order {
		fields := plan.deferred[index]
		if len(fields) == 0 {
			continue
		}
		if err := validateAccountSessionScope(ctx, scope); err != nil {
			return nil, err
		}
		if err := allocations[index].finish(ctx, scope, actor, allocated, maps.Clone(fields)); err != nil {
			return nil, err
		}
	}
	// A nullable placeholder can grow when filled. Reapply the same aggregate
	// limit to complete rows before returning any allocation correspondence.
	if hasDeferred {
		budget = &transitionDigestWriter{hash: sha256.New()}
		for _, node := range allocations {
			if err := writeTransitionValues(budget, node.values()); err != nil {
				return nil, err
			}
		}
	}
	if err := validateAccountSessionScope(ctx, scope); err != nil {
		return nil, err
	}
	return allocated, nil
}

// A staged creation is one logical Add. Finalization may fill only the exact
// nullable FK fields chosen by the reviewed graph; it cannot change revision,
// credentials, defaults or any unrelated scalar while completing those keys.
func validateTransitionDeferredUpdate(changed []string, deferred map[string]bool) error {
	if len(changed) != len(deferred) {
		return managementError(CodeConflict, nil)
	}
	seen := make(map[string]bool, len(changed))
	for _, name := range changed {
		if !deferred[name] || seen[name] {
			return managementError(CodeConflict, nil)
		}
		seen[name] = true
	}
	return nil
}
