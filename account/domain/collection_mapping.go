package domain

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/progresshans/godj/db"
)

// CollectionIdentity records the actual typed row satisfying one reviewed
// collection creation intent. Additional distinguishes an extra identity from
// a row already allocated by the explicit account/catalog correspondence.
// The list preserves reviewed intent order; keys are never anticipated.
type CollectionIdentity struct {
	Kind       string `json:"kind"`
	Key        int64  `json:"key"`
	Revision   int64  `json:"revision"`
	Additional bool   `json:"additional"`
}

// MappingInventory is the complete evidence written alongside an ownership
// receipt. Catalog absence keeps the shared-catalog meaning. Collections are
// separate from correspondence: an additional identity has no source row.
type MappingInventory struct {
	Accounts    []Mapping
	Catalog     *CatalogMappings
	Collections []CollectionIdentity
}

func normalizeCollectionIdentities(input []CollectionIdentity, accounts []Mapping, catalog *CatalogMappings) ([]CollectionIdentity, error) {
	if len(input) > MaximumMappings {
		return nil, ErrInvalid
	}
	if len(input) == 0 {
		return nil, nil
	}
	correspondence := map[string]map[int64]int64{"account": {}, "group": {}, "permission": {}}
	for _, row := range accounts {
		correspondence["account"][row.TargetID] = row.TargetRevision
	}
	if catalog != nil {
		for _, row := range catalog.Groups {
			correspondence["group"][row.TargetID] = row.TargetRevision
		}
		for _, row := range catalog.Permissions {
			correspondence["permission"][row.TargetID] = row.TargetRevision
		}
	}
	seen := make(map[struct {
		kind string
		key  int64
	}]bool)
	for _, row := range input {
		targets, valid := correspondence[row.Kind]
		if !valid || row.Revision <= 0 {
			return nil, ErrInvalid
		}
		revision, mapped := targets[row.Key]
		if row.Additional {
			key := struct {
				kind string
				key  int64
			}{row.Kind, row.Key}
			if row.Revision != 1 || mapped || seen[key] {
				return nil, ErrInvalid
			}
			seen[key] = true
		} else if !mapped || revision != row.Revision {
			return nil, ErrInvalid
		}
	}
	return slices.Clone(input), nil
}

func decodeCollectionIdentities(decoder *json.Decoder) ([]CollectionIdentity, error) {
	if !mappingToken(decoder, json.Delim('[')) {
		return nil, ErrCorrupt
	}
	rows := []CollectionIdentity{}
	for decoder.More() {
		if len(rows) >= MaximumMappings {
			return nil, ErrCorrupt
		}
		var row CollectionIdentity
		if decoder.Decode(&row) != nil {
			return nil, ErrCorrupt
		}
		rows = append(rows, row)
	}
	if !mappingToken(decoder, json.Delim(']')) {
		return nil, ErrCorrupt
	}
	return rows, nil
}

// LookupMappingInventoryIn validates the exact immutable receipt and the whole
// canonical payload in one borrowed snapshot. Errors never publish partial data.
func LookupMappingInventoryIn(ctx context.Context, reader db.Queryer, expected Receipt) (MappingInventory, error) {
	result, err := lookupMappingSetIn(ctx, reader, expected)
	if err != nil {
		return MappingInventory{}, err
	}
	return MappingInventory{Accounts: result.rows, Catalog: result.catalog, Collections: result.collections}, nil
}
