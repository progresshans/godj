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

func bindTransitionTargetReferences[M any](target *transitionTarget[M], input orm.ModelInput[M], references map[string]TransitionTargetReference, mappings transitionTargetMappings) (orm.ModelInput[M], map[string]bool, error) {
	return bindTransitionReferences(target.binding.state.objects, target.metadata, target.binding.state.role, target.allowed, input, references, mappings)
}

func bindTransitionReferences[M any](objects orm.Manager[M], metadata ir.Model, role ir.AccountRole, allowed map[string]bool, input orm.ModelInput[M], references map[string]TransitionTargetReference, mappings transitionTargetMappings) (orm.ModelInput[M], map[string]bool, error) {
	if len(references) > len(metadata.Fields) {
		return orm.ModelInput[M]{}, nil, managementError(CodeInvalidInput, nil)
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

// Required and nullable references both wait for a real target identity. A
// cycle cannot be inserted under this native auto-key contract without a
// separate key reservation or staged nullable update protocol; reject it before
// writing rather than substituting NULL/zero or weakening native constraints.
func transitionTargetOrder[M any](requests []AccountCreation[M]) ([]int, error) {
	nodes := make([]transitionReferenceNode, len(requests))
	for i, request := range requests {
		nodes[i] = transitionReferenceNode{kind: TransitionReferenceAccount, source: request.Source, references: request.References}
	}
	return transitionReferenceOrder(nodes)
}

type transitionReferenceIdentity struct {
	kind TransitionReferenceKind
	id   int64
}

type transitionReferenceNode struct {
	kind       TransitionReferenceKind
	source     SourceAccount
	references map[string]TransitionTargetReference
}

func transitionReferenceOrder(requests []transitionReferenceNode) ([]int, error) {
	namespaces := make(map[TransitionReferenceKind]bool)
	indices := make(map[transitionReferenceIdentity]int, len(requests))
	for i, request := range requests {
		namespaces[request.kind] = true
		if _, duplicate := indices[transitionReferenceIdentity{kind: request.kind, id: request.source.ID}]; duplicate {
			return nil, managementError(CodeInvalidInput, nil)
		}
		indices[transitionReferenceIdentity{kind: request.kind, id: request.source.ID}] = i
	}
	dependencies := make([]int, len(requests))
	dependents := make([][]int, len(requests))
	for i, request := range requests {
		seen := make(map[int]bool)
		for _, name := range slices.Sorted(maps.Keys(request.references)) {
			reference := request.references[name]
			if !namespaces[reference.Kind] {
				continue
			}
			index, found := indices[transitionReferenceIdentity{kind: reference.Kind, id: reference.Source.ID}]
			if !found {
				return nil, managementError(CodeInvalidInput, nil)
			}
			if !seen[index] {
				seen[index] = true
				dependencies[i]++
				dependents[index] = append(dependents[index], i)
			}
		}
	}
	order := make([]int, 0, len(requests))
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
	if len(order) != len(requests) {
		return nil, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "transition target references contain an auto-key dependency cycle"}
	}
	return order, nil
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
