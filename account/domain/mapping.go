package domain

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

const MaximumMappings = 65536
const MaximumMappingBytes = 16 << 20

// Mapping records actual native keys, not anticipated generated IDs. Keys can
// be zero/negative; positive revisions describe the reviewed source and the
// adopted target. No principal, password, profile value or bearer is stored.
type Mapping struct {
	SourceID       int64 `json:"source_id"`
	SourceRevision int64 `json:"source_revision"`
	TargetID       int64 `json:"target_id"`
	TargetRevision int64 `json:"target_revision"`
}

// CatalogMappings pairs the complete reviewed group and permission inventories.
// Their model identities and field roles are bound by the predecessor and new
// ownership receipts. The outer typed transition owner must verify those roles,
// permission codes, group grants and account memberships before storing this
// evidence. Empty inventories are explicit; nil absence denotes an older or
// shared-catalog transition without a separate catalog mapping.
type CatalogMappings struct {
	Groups      []Mapping `json:"groups"`
	Permissions []Mapping `json:"permissions"`
}

type preparedMappings struct {
	rows            []Mapping
	catalog         *CatalogMappings
	payload, digest string
}

func normalizeMappings(mappings []Mapping, required bool) ([]Mapping, error) {
	if required && len(mappings) == 0 || len(mappings) > MaximumMappings {
		return nil, ErrInvalid
	}
	owned := append([]Mapping{}, mappings...)
	slices.SortFunc(owned, func(a, b Mapping) int { return cmp.Compare(a.SourceID, b.SourceID) })
	targets := make(map[int64]bool, len(owned))
	for index, row := range owned {
		if row.SourceRevision <= 0 || row.TargetRevision <= 0 || index > 0 && row.SourceID == owned[index-1].SourceID || targets[row.TargetID] {
			return nil, ErrInvalid
		}
		targets[row.TargetID] = true
	}
	return owned, nil
}

func prepareMappingSet(mappings []Mapping, catalog *CatalogMappings) (preparedMappings, error) {
	owned, err := normalizeMappings(mappings, true)
	if err != nil {
		return preparedMappings{}, err
	}
	result := preparedMappings{rows: owned}
	var value any = owned
	prefix := "godj.account-mapping.v1\x00"
	if catalog != nil {
		groups, err := normalizeMappings(catalog.Groups, false)
		if err != nil {
			return preparedMappings{}, err
		}
		permissions, err := normalizeMappings(catalog.Permissions, false)
		if err != nil {
			return preparedMappings{}, err
		}
		result.catalog = &CatalogMappings{Groups: groups, Permissions: permissions}
		value = struct {
			Version  int              `json:"version"`
			Accounts []Mapping        `json:"accounts"`
			Catalog  *CatalogMappings `json:"catalog"`
		}{2, owned, result.catalog}
		prefix = "godj.account-mapping.v2\x00"
	}
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > MaximumMappingBytes {
		return preparedMappings{}, ErrInvalid
	}
	digest := sha256.Sum256(append([]byte(prefix), payload...))
	result.payload, result.digest = string(payload), hex.EncodeToString(digest[:])
	return result, nil
}

func decodeMappingSet(receipt Receipt, row models.Mapping) (preparedMappings, error) {
	if receipt.state == nil || row.ID <= 0 || row.TransitionID != receipt.state.id || receipt.MappingCount() == 0 || len(row.Payload) > MaximumMappingBytes {
		return preparedMappings{}, ErrCorrupt
	}
	decoder := json.NewDecoder(strings.NewReader(row.Payload))
	decoder.DisallowUnknownFields()
	var mappings []Mapping
	var catalog *CatalogMappings
	var err error
	if strings.HasPrefix(row.Payload, "{") {
		// Read bounded arrays individually. A compact hostile array must not be
		// materialized before its item limit is checked, even under 16 MiB.
		if !mappingToken(decoder, json.Delim('{')) || !mappingToken(decoder, "version") {
			return preparedMappings{}, ErrCorrupt
		}
		var version int
		if decoder.Decode(&version) != nil || version != 2 || !mappingToken(decoder, "accounts") {
			return preparedMappings{}, ErrCorrupt
		}
		mappings, err = decodeMappingRows(decoder)
		if err != nil || !mappingToken(decoder, "catalog") || !mappingToken(decoder, json.Delim('{')) || !mappingToken(decoder, "groups") {
			return preparedMappings{}, ErrCorrupt
		}
		groups, err := decodeMappingRows(decoder)
		if err != nil || !mappingToken(decoder, "permissions") {
			return preparedMappings{}, ErrCorrupt
		}
		permissions, err := decodeMappingRows(decoder)
		if err != nil || !mappingToken(decoder, json.Delim('}')) || !mappingToken(decoder, json.Delim('}')) {
			return preparedMappings{}, ErrCorrupt
		}
		catalog = &CatalogMappings{Groups: groups, Permissions: permissions}
	} else {
		mappings, err = decodeMappingRows(decoder)
		if err != nil {
			return preparedMappings{}, ErrCorrupt
		}
	}
	prepared, err := prepareMappingSet(mappings, catalog)
	if err != nil || len(prepared.rows) != receipt.MappingCount() || prepared.digest != receipt.MappingSHA256() || prepared.payload != row.Payload {
		return preparedMappings{}, ErrCorrupt
	}
	return prepared, nil
}

func mappingToken(decoder *json.Decoder, expected any) bool {
	actual, err := decoder.Token()
	return err == nil && actual == expected
}

func decodeMappingRows(decoder *json.Decoder) ([]Mapping, error) {
	mappings := []Mapping{}
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return nil, ErrCorrupt
	}
	for decoder.More() {
		if len(mappings) >= MaximumMappings {
			return nil, ErrCorrupt
		}
		var entry Mapping
		if err := decoder.Decode(&entry); err != nil {
			return nil, ErrCorrupt
		}
		mappings = append(mappings, entry)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return nil, ErrCorrupt
	}
	return mappings, nil
}

// LookupMappingsIn verifies the exact receipt, one canonical mapping payload,
// its count/digest and the one-to-one source/target keys in the same borrowed
// scope. A mapping-free low-level ownership receipt returns an empty slice.
// Failure/cleanup never publishes a partial correspondence table.
func LookupMappingsIn(ctx context.Context, reader db.Queryer, expected Receipt) ([]Mapping, error) {
	result, err := lookupMappingSetIn(ctx, reader, expected)
	return result.rows, err
}

// LookupCatalogMappingsIn reads catalog correspondence with the same receipt,
// payload and scope checks as LookupMappingsIn. found distinguishes an explicit
// empty catalog from historical/shared-catalog receipts without that evidence.
func LookupCatalogMappingsIn(ctx context.Context, reader db.Queryer, expected Receipt) (CatalogMappings, bool, error) {
	result, err := lookupMappingSetIn(ctx, reader, expected)
	if err != nil || result.catalog == nil {
		return CatalogMappings{}, false, err
	}
	return *result.catalog, true, nil
}

func lookupMappingSetIn(ctx context.Context, reader db.Queryer, expected Receipt) (result preparedMappings, err error) {
	if expected.state == nil {
		return preparedMappings{}, ErrInvalid
	}
	validator, err := lookupScope(ctx, reader)
	if err != nil {
		return preparedMappings{}, err
	}
	defer func() {
		if completion := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); completion != nil {
			err = failure(errors.Join(err, completion))
		}
		if err != nil {
			result = preparedMappings{}
		}
	}()
	actual, found, err := LookupIn(ctx, reader, expected.OperationID())
	if err != nil {
		return preparedMappings{}, err
	}
	if !found || *actual.state != *expected.state {
		return preparedMappings{}, ErrStale
	}
	selected, err := models.MappingObjects.Using(reader).Filter(models.MappingFields.TransitionID.Exact(expected.state.id)).OrderBy(models.MappingFields.ID.Asc()).Limit(2)
	if err != nil {
		return preparedMappings{}, failure(err)
	}
	rows, err := selected.All(ctx)
	if err != nil {
		return preparedMappings{}, failure(err)
	}
	if expected.MappingCount() == 0 {
		if len(rows) != 0 {
			return preparedMappings{}, ErrCorrupt
		}
		return preparedMappings{rows: []Mapping{}}, nil
	}
	if len(rows) != 1 {
		return preparedMappings{}, ErrCorrupt
	}
	return decodeMappingSet(expected, rows[0])
}

// The history scan counts headers declaring mappings. This independent scan
// validates every payload and its referenced header, including orphan/extra
// payloads and duplicate parent links after lost database constraints. Pages
// contain only keys; each bounded payload is loaded separately after the page
// closes. A long history never retains a page of maximum-sized payloads, and
// no open cursor is re-entered.
func inspectMappings(ctx context.Context, reader db.Queryer, expected int64) error {
	type link struct{ id, transition int64 }
	var parent, key, count int64
	started := false
	for {
		source := models.MappingObjects.Using(reader).OrderBy(models.MappingFields.TransitionID.Asc(), models.MappingFields.ID.Asc())
		if started {
			source = source.Filter(orm.Or(models.MappingFields.TransitionID.GreaterThan(parent), orm.And(models.MappingFields.TransitionID.Exact(parent), models.MappingFields.ID.GreaterThan(key))))
		}
		selected, err := orm.SelectQuery(source, orm.Project2(models.MappingFields.ID, models.MappingFields.TransitionID, func(id, transition int64) link { return link{id, transition} }))
		if err != nil {
			return failure(err)
		}
		bounded, err := selected.Limit(inspectionBatchSize)
		if err != nil {
			return failure(err)
		}
		rows, err := bounded.All(ctx)
		if err != nil {
			return failure(err)
		}
		if len(rows) > inspectionBatchSize {
			return ErrCorrupt
		}
		for _, entry := range rows {
			if entry.id <= 0 || entry.transition <= 0 || started && entry.transition <= parent || count >= expected {
				return ErrCorrupt
			}
			header, err := models.TransitionObjects.Using(reader).Filter(models.TransitionFields.ID.Exact(entry.transition)).Get(ctx)
			if err != nil {
				return failure(err)
			}
			receipt, err := fromRow(header)
			if err != nil {
				return err
			}
			row, err := models.MappingObjects.Using(reader).Filter(models.MappingFields.ID.Exact(entry.id)).Get(ctx)
			if err != nil {
				return failure(err)
			}
			if _, err := decodeMappingSet(receipt, row); err != nil {
				return err
			}
			parent, key, count, started = entry.transition, entry.id, count+1, true
		}
		if len(rows) < inspectionBatchSize {
			if count != expected {
				return ErrCorrupt
			}
			return nil
		}
	}
}

func (preparedMappings) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("account.domain.preparedMappings{redacted}"))
}
