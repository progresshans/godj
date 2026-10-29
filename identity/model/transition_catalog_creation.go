package model

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

// CatalogCreation is immutable typed intent bound to both selected roles. It
// does not write or grant authority. Assign it to TransitionConfig.CatalogCreation
// to create changed catalog models in the same account transition transaction.
type CatalogCreation struct{ state *transitionCatalogCreation }

func (CatalogCreation) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.CatalogCreation{redacted}"))
}

type transitionCatalogCreation struct {
	source                        string
	oldRole, nextRole             [sha256.Size]byte
	groupCreated                  bool
	permissionCreated             bool
	groupInputs, permissionInputs int
	required                      []auth.Permission
	prepare                       func(context.Context, db.Queryer, transitionCatalogObservation) (preparedCatalogCreation, error)
	accounts                      func(context.Context, db.Queryer, int64, []int64, []int64) ([]orm.CollectionChange, error)
}

// CatalogCreationSource retains reviewed source revision and the exact planned
// target revision. A created target has revision one; a shared row keeps its
// key and may advance revision when its new grant relation changes.
type CatalogCreationSource struct {
	ID             int64 `json:"id"`
	Revision       int64 `json:"revision"`
	TargetRevision int64 `json:"target_revision"`
}

// CatalogCreationManifest contains correspondence evidence without profile
// values or anticipated keys. Both lists cover the complete source inventory,
// including a shared model and rows assigned to no account.
type CatalogCreationManifest struct {
	CreateGroups      bool                    `json:"create_groups"`
	CreatePermissions bool                    `json:"create_permissions"`
	Groups            []CatalogCreationSource `json:"groups"`
	Permissions       []CatalogCreationSource `json:"permissions"`
}

type preparedCatalogCreation struct {
	manifest CatalogCreationManifest
	digest   string
	bytes    int
	apply    func(context.Context, db.RelationSession, auth.Principal, time.Time) (createdCatalogState, error)
}

type createdCatalogState struct {
	mapping       *CatalogMappings
	before, after transitionCatalogInventory
	insertions    []orm.AppliedCollectionChanges
	verifyPolicy  func(context.Context, db.Queryer) error
}

// Creation copies the input lists. A shared model accepts no creation input;
// its complete self correspondence is derived from the reviewed native rows.
// Changed models require exactly one request per source row, checked by Prepare.
func (target TransitionCatalogTarget[G, P]) Creation(groups []CatalogRowCreation[G], permissions []CatalogRowCreation[P]) (CatalogCreation, error) {
	if target.state == nil {
		return CatalogCreation{}, managementError(CodeInvalidConfig, nil)
	}
	state := target.state
	groupInputs, err := normalizeCatalogCreationInputs(state.groups, groups)
	if err != nil {
		return CatalogCreation{}, err
	}
	permissionInputs, err := normalizeCatalogCreationInputs(state.permissions, permissions)
	if err != nil {
		return CatalogCreation{}, err
	}
	return CatalogCreation{&transitionCatalogCreation{source: state.source, oldRole: state.oldHash, nextRole: state.nextHash,
		groupCreated: state.groups.created, permissionCreated: state.permissions.created, groupInputs: len(groupInputs), permissionInputs: len(permissionInputs), required: slices.Clone(state.required), accounts: state.accounts,
		prepare: func(ctx context.Context, scope db.Queryer, observed transitionCatalogObservation) (preparedCatalogCreation, error) {
			return prepareCatalogCreation(ctx, scope, state, groupInputs, permissionInputs, observed)
		},
	}}, nil
}

func normalizeCatalogCreationInputs[M any](model transitionCatalogModel[M], inputs []CatalogRowCreation[M]) ([]CatalogRowCreation[M], error) {
	if len(inputs) > MaximumTransitionCatalogRows || !model.created && len(inputs) != 0 {
		return nil, managementError(CodeInvalidInput, nil)
	}
	owned := slices.Clone(inputs)
	slices.SortFunc(owned, func(a, b CatalogRowCreation[M]) int { return compareInt64(a.Source.ID, b.Source.ID) })
	for i, request := range owned {
		if request.Source.Revision <= 0 || i > 0 && owned[i-1].Source.ID == request.Source.ID {
			return nil, managementError(CodeInvalidInput, nil)
		}
		metadata, valid := request.Input.Model()
		if !valid || !metadata.Equal(model.metadata) || len(request.Input.Collections()) != 0 {
			return nil, managementError(CodeInvalidInput, nil)
		}
		if key, bound := request.Input.TargetKey(); bound && !key.IsNull() {
			return nil, managementError(CodeInvalidInput, nil)
		}
		for name := range request.Input.Values() {
			if !model.allowed[name] {
				return nil, managementError(CodeInvalidInput, nil)
			}
		}
	}
	return owned, nil
}

func cloneCatalogCreationManifest(input *CatalogCreationManifest) *CatalogCreationManifest {
	if input == nil {
		return nil
	}
	result := *input
	result.Groups = append([]CatalogCreationSource{}, input.Groups...)
	result.Permissions = append([]CatalogCreationSource{}, input.Permissions...)
	return &result
}

func validateCatalogCreationManifest(input *CatalogCreationManifest) error {
	if input == nil {
		return nil
	}
	if !input.CreateGroups && !input.CreatePermissions || input.Groups == nil || input.Permissions == nil {
		return managementError(CodeInvalidInput, nil)
	}
	for _, inventory := range []struct {
		created bool
		rows    []CatalogCreationSource
	}{{input.CreateGroups, input.Groups}, {input.CreatePermissions, input.Permissions}} {
		if len(inventory.rows) > MaximumTransitionCatalogRows {
			return managementError(CodeInvalidInput, nil)
		}
		for i, row := range inventory.rows {
			if row.Revision <= 0 || row.TargetRevision <= 0 || inventory.created && row.TargetRevision != 1 || i > 0 && row.ID <= inventory.rows[i-1].ID {
				return managementError(CodeInvalidInput, nil)
			}
		}
	}
	return nil
}

func catalogCreationMatches(manifest CatalogCreationManifest, actual CatalogMappings) bool {
	for _, item := range []struct {
		created bool
		sources []CatalogCreationSource
		pairs   []AccountMapping
	}{{manifest.CreateGroups, manifest.Groups, actual.Groups}, {manifest.CreatePermissions, manifest.Permissions, actual.Permissions}} {
		if len(item.sources) != len(item.pairs) {
			return false
		}
		for i, source := range item.sources {
			pair := item.pairs[i]
			if source.ID != pair.SourceID || source.Revision != pair.SourceRevision || source.TargetRevision != pair.TargetRevision || !item.created && pair.SourceID != pair.TargetID {
				return false
			}
		}
	}
	return true
}
