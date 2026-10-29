package model

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// TransitionTargetReference selects an explicit correspondence, not a target
// primary key. Source key zero is present and valid. The referenced source
// revision must match the same reviewed account/catalog snapshot.
type TransitionTargetReference struct {
	Kind   TransitionReferenceKind `json:"kind"`
	Source SourceAccount           `json:"source"`
}

type transitionTargetNamespace struct {
	revisions map[int64]int64
	targets   map[int64]int64 // nil until that namespace has allocated keys
}
type transitionTargetMappings map[TransitionReferenceKind]transitionTargetNamespace

func targetReferenceMappings(accounts []SourceAccount, targets map[int64]int64, catalog transitionCatalogObservation) transitionTargetMappings {
	account := transitionTargetNamespace{revisions: make(map[int64]int64, len(accounts)), targets: targets}
	for _, source := range accounts {
		account.revisions[source.ID] = source.Revision
	}
	catalogNamespace := func(rows map[int64]transitionCatalogRow, targets map[int64]int64, pending bool, mappings []AccountMapping) transitionTargetNamespace {
		result := transitionTargetNamespace{revisions: make(map[int64]int64, len(rows)), targets: targets}
		if pending {
			result.targets = nil
		}
		for id, row := range rows {
			result.revisions[id] = row.revision
		}
		// A shared Group can advance its row revision while installing the
		// target permission relation. References still name the reviewed source
		// revision, as does the verified correspondence saved in the receipt.
		for _, mapping := range mappings {
			if _, present := result.revisions[mapping.SourceID]; present {
				result.revisions[mapping.SourceID] = mapping.SourceRevision
			}
		}
		return result
	}
	var groups, permissions []AccountMapping
	if catalog.mapping != nil {
		groups, permissions = catalog.mapping.Groups, catalog.mapping.Permissions
	}
	return transitionTargetMappings{
		TransitionReferenceAccount:    account,
		TransitionReferenceGroup:      catalogNamespace(catalog.before.groups, catalog.groups, catalog.pendingGroups, groups),
		TransitionReferencePermission: catalogNamespace(catalog.before.permissions, catalog.direct, catalog.pendingPermissions, permissions),
	}
}

func bindTransitionTargetReferences[M any](target *transitionTarget[M], input orm.ModelInput[M], references map[string]TransitionTargetReference, mappings transitionTargetMappings, deferred map[string]bool) (orm.ModelInput[M], map[string]bool, error) {
	return bindTransitionReferences(target.binding.state.objects, target.metadata, target.binding.state.role, target.allowed, input, references, mappings, deferred)
}

func bindTransitionReferences[M any](objects orm.Manager[M], metadata ir.Model, role ir.AccountRole, allowed map[string]bool, input orm.ModelInput[M], references map[string]TransitionTargetReference, mappings transitionTargetMappings, deferred map[string]bool) (orm.ModelInput[M], map[string]bool, error) {
	if len(references) > len(metadata.Fields) {
		return orm.ModelInput[M]{}, nil, managementError(CodeInvalidInput, nil)
	}
	for name, enabled := range deferred {
		field, found := namedField(metadata, name)
		_, referenced := references[name]
		if !enabled || !found || !referenced || !field.Nullable || field.Relation == nil || !field.Relation.Cardinality.SingleValued() {
			return orm.ModelInput[M]{}, nil, managementError(CodeInvalidConfig, nil)
		}
	}
	values := input.Values()
	resolved, pending := make(map[string]query.Value), make(map[string]bool)
	for _, name := range slices.Sorted(maps.Keys(references)) {
		reference := references[name]
		field, found := namedField(metadata, name)
		identity, err := transitionReferenceModel(role, reference.Kind)
		_, supplied := values[name]
		if err != nil || !found || !allowed[name] || field.Relation == nil || field.Relation.Target != identity || supplied || reference.Source.Revision <= 0 {
			return orm.ModelInput[M]{}, nil, managementError(CodeInvalidInput, err)
		}
		namespace, found := mappings[reference.Kind]
		if !found || namespace.revisions[reference.Source.ID] != reference.Source.Revision {
			return orm.ModelInput[M]{}, nil, managementError(CodeConflict, nil)
		}
		if deferred[name] {
			resolved[name] = query.Null()
			continue
		}
		if namespace.targets == nil {
			pending[name] = true
			continue
		}
		key, found := namespace.targets[reference.Source.ID]
		if !found {
			return orm.ModelInput[M]{}, nil, managementError(CodeConflict, nil)
		}
		resolved[name] = query.Integer(key)
	}
	bound, err := objects.WithServerValues(input, resolved)
	return bound, pending, err
}

// The plan is derived from normalized field nullability. Acyclic inputs keep
// their direct INSERT order; only nullable edges inside a cycle may be staged.
func transitionTargetPlan[M any](requests []AccountCreation[M], metadata ir.Model) (transitionReferencePlan, error) {
	nodes := make([]transitionReferenceNode, len(requests))
	for i, request := range requests {
		nodes[i] = newTransitionReferenceNode(TransitionReferenceAccount, request.Source, request.References, metadata)
	}
	return planTransitionReferences(nodes)
}

type transitionReferenceIdentity struct {
	kind TransitionReferenceKind
	id   int64
}
type transitionReferenceNode struct {
	kind       TransitionReferenceKind
	source     SourceAccount
	references map[string]TransitionTargetReference
	nullable   map[string]bool
}
type transitionReferencePlan struct {
	order    []int
	deferred []map[string]bool
}
type transitionReferenceEdge struct {
	target int
	field  string
}

func newTransitionReferenceNode(kind TransitionReferenceKind, source SourceAccount, references map[string]TransitionTargetReference, metadata ir.Model) transitionReferenceNode {
	node := transitionReferenceNode{kind: kind, source: source, references: references, nullable: make(map[string]bool)}
	for _, field := range metadata.Fields {
		if _, referenced := references[field.Name]; referenced && field.Nullable && field.Relation != nil && field.Relation.Cardinality.SingleValued() {
			node.nullable[field.Name] = true
		}
	}
	return node
}

func planTransitionReferences(requests []transitionReferenceNode) (transitionReferencePlan, error) {
	var zero transitionReferencePlan
	namespaces := make(map[TransitionReferenceKind]bool)
	indices := make(map[transitionReferenceIdentity]int, len(requests))
	for i, request := range requests {
		namespaces[request.kind] = true
		key := transitionReferenceIdentity{request.kind, request.source.ID}
		if _, duplicate := indices[key]; duplicate {
			return zero, managementError(CodeInvalidInput, nil)
		}
		indices[key] = i
	}
	edges := make([][]transitionReferenceEdge, len(requests))
	for i, request := range requests {
		for _, name := range slices.Sorted(maps.Keys(request.references)) {
			reference := request.references[name]
			if !namespaces[reference.Kind] {
				continue
			}
			index, found := indices[transitionReferenceIdentity{reference.Kind, reference.Source.ID}]
			if !found {
				return zero, managementError(CodeInvalidInput, nil)
			}
			edges[i] = append(edges[i], transitionReferenceEdge{index, name})
		}
	}
	deferred := make([]map[string]bool, len(requests))
	if order := transitionReferenceTopological(edges, deferred); len(order) == len(requests) {
		return transitionReferencePlan{order: order, deferred: deferred}, nil
	}
	components, sizes := transitionReferenceComponents(edges)
	for i, dependencies := range edges {
		for _, edge := range dependencies {
			if components[i] == components[edge.target] && (sizes[components[i]] > 1 || edge.target == i) && requests[i].nullable[edge.field] {
				if deferred[i] == nil {
					deferred[i] = make(map[string]bool)
				}
				deferred[i][edge.field] = true
			}
		}
	}
	order := transitionReferenceTopological(edges, deferred)
	if len(order) != len(requests) {
		return zero, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "transition references contain a required auto-key dependency cycle"}
	}
	return transitionReferencePlan{order: order, deferred: deferred}, nil
}

func transitionReferenceTopological(edges [][]transitionReferenceEdge, deferred []map[string]bool) []int {
	dependencies := make([]int, len(edges))
	dependents := make([][]int, len(edges))
	for i, rows := range edges {
		for _, edge := range rows {
			if !deferred[i][edge.field] {
				dependencies[i]++
				dependents[edge.target] = append(dependents[edge.target], i)
			}
		}
	}
	order := make([]int, 0, len(edges))
	for i, count := range dependencies {
		if count == 0 {
			order = append(order, i)
		}
	}
	for i := 0; i < len(order); i++ {
		for _, dependent := range dependents[order[i]] {
			dependencies[dependent]--
			if dependencies[dependent] == 0 {
				order = append(order, dependent)
			}
		}
	}
	return order
}

// Iterative Kosaraju traversal keeps stack use bounded by the admitted graph,
// including long chains and branches that do not themselves belong to a cycle.
func transitionReferenceComponents(edges [][]transitionReferenceEdge) ([]int, []int) {
	type frame struct{ node, next int }
	seen := make([]bool, len(edges))
	post := make([]int, 0, len(edges))
	reverse := make([][]int, len(edges))
	for i, rows := range edges {
		for _, edge := range rows {
			reverse[edge.target] = append(reverse[edge.target], i)
		}
	}
	for root := range edges {
		if seen[root] {
			continue
		}
		seen[root] = true
		stack := []frame{{node: root}}
		for len(stack) > 0 {
			current := &stack[len(stack)-1]
			if current.next == len(edges[current.node]) {
				post = append(post, current.node)
				stack = stack[:len(stack)-1]
				continue
			}
			target := edges[current.node][current.next].target
			current.next++
			if !seen[target] {
				seen[target] = true
				stack = append(stack, frame{node: target})
			}
		}
	}
	component := make([]int, len(edges))
	for i := range component {
		component[i] = -1
	}
	var sizes []int
	for i := len(post) - 1; i >= 0; i-- {
		root := post[i]
		if component[root] >= 0 {
			continue
		}
		id := len(sizes)
		sizes = append(sizes, 0)
		component[root] = id
		stack := []int{root}
		for len(stack) > 0 {
			node := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			sizes[id]++
			for _, target := range reverse[node] {
				if component[target] < 0 {
					component[target] = id
					stack = append(stack, target)
				}
			}
		}
	}
	return component, sizes
}

func writeTransitionTargetReferences(digest *transitionDigestWriter, references map[string]TransitionTargetReference) error {
	if len(references) == 0 {
		return nil
	}
	digest.part([]byte("godj.transition-target-references.v1"))
	encoded, err := json.Marshal(references)
	if err != nil {
		return managementError(CodeInvalidInput, err)
	}
	digest.part(encoded)
	return digest.err
}
