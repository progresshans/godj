package model

import (
	"maps"

	"github.com/progresshans/godj/schema/ir"
)

type transitionReferenceKeys struct {
	sources map[int64]bool
	targets map[int64]int64 // nil only while creation has not allocated keys
}

type transitionReferenceMappings map[TransitionReferenceKind]transitionReferenceKeys

func transitionReferenceModel(role ir.AccountRole, kind TransitionReferenceKind) (ir.ModelIdentity, error) {
	switch kind {
	case TransitionReferenceAccount:
		return role.Model, nil
	case TransitionReferenceGroup:
		return role.Catalog.Group, nil
	case TransitionReferencePermission:
		return role.Catalog.Permission, nil
	default:
		return ir.ModelIdentity{}, managementError(CodeInvalidConfig, nil)
	}
}

func referenceMappings(sources map[int64]bool, targets map[int64]int64, catalog transitionCatalogObservation) transitionReferenceMappings {
	known := func(pairs map[int64]int64) transitionReferenceKeys {
		keys := make(map[int64]bool, len(pairs))
		for key := range pairs {
			keys[key] = true
		}
		return transitionReferenceKeys{sources: keys, targets: maps.Clone(pairs)}
	}
	groups, permissions := known(catalog.groups), known(catalog.direct)
	pending := func(rows map[int64]transitionCatalogRow) transitionReferenceKeys {
		keys := make(map[int64]bool, len(rows))
		for id := range rows {
			keys[id] = true
		}
		return transitionReferenceKeys{sources: keys}
	}
	if catalog.pendingGroups {
		groups = pending(catalog.before.groups)
	}
	if catalog.pendingPermissions {
		permissions = pending(catalog.before.permissions)
	}
	return transitionReferenceMappings{
		TransitionReferenceAccount:    {sources: maps.Clone(sources), targets: maps.Clone(targets)},
		TransitionReferenceGroup:      groups,
		TransitionReferencePermission: permissions,
	}
}

// A prepared account creation may leave account targets unknown. At the
// write boundary every referenced namespace must be complete, and a catalog
// mapping already reviewed during preparation cannot silently change.
func (before transitionReferenceMappings) validateResolved(fields []TransitionReferenceFields, after transitionReferenceMappings) error {
	checked := make(map[TransitionReferenceKind]bool)
	for _, field := range fields {
		if checked[field.Kind] {
			continue
		}
		checked[field.Kind] = true
		old, oldPresent := before[field.Kind]
		next, nextPresent := after[field.Kind]
		if !oldPresent || !nextPresent || old.sources == nil || next.sources == nil || next.targets == nil ||
			!maps.Equal(old.sources, next.sources) || len(next.sources) != len(next.targets) ||
			old.targets != nil && !maps.Equal(old.targets, next.targets) {
			return managementError(CodeConflict, nil)
		}
		seen := make(map[int64]bool, len(next.targets))
		for source, target := range next.targets {
			if !next.sources[source] || seen[target] {
				return managementError(CodeConflict, nil)
			}
			seen[target] = true
		}
	}
	return nil
}

func transitionManifestVersion(catalog *CatalogMappings, references []TransitionReferenceManifest, creation *CatalogCreationManifest) int {
	if creation != nil {
		return 7
	}
	for _, reference := range references {
		for _, field := range reference.Fields {
			if field.Kind != TransitionReferenceAccount {
				return 6
			}
		}
	}
	if catalog != nil {
		return 5
	}
	return 4
}
