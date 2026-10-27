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

type preparedMappings struct {
	rows            []Mapping
	payload, digest string
}

func prepareMappings(mappings []Mapping) (preparedMappings, error) {
	if len(mappings) < 1 || len(mappings) > MaximumMappings {
		return preparedMappings{}, ErrInvalid
	}
	owned := slices.Clone(mappings)
	slices.SortFunc(owned, func(a, b Mapping) int { return cmp.Compare(a.SourceID, b.SourceID) })
	targets := make(map[int64]bool, len(owned))
	for index, row := range owned {
		if row.SourceRevision <= 0 || row.TargetRevision <= 0 || index > 0 && row.SourceID == owned[index-1].SourceID || targets[row.TargetID] {
			return preparedMappings{}, ErrInvalid
		}
		targets[row.TargetID] = true
	}
	payload, err := json.Marshal(owned)
	if err != nil || len(payload) > MaximumMappingBytes {
		return preparedMappings{}, ErrInvalid
	}
	digest := sha256.Sum256(append([]byte("godj.account-mapping.v1\x00"), payload...))
	return preparedMappings{rows: owned, payload: string(payload), digest: hex.EncodeToString(digest[:])}, nil
}

func decodeMappings(receipt Receipt, row models.Mapping) ([]Mapping, error) {
	if receipt.state == nil || row.ID <= 0 || row.TransitionID != receipt.state.id || receipt.MappingCount() == 0 || len(row.Payload) > MaximumMappingBytes {
		return nil, ErrCorrupt
	}
	var mappings []Mapping
	decoder := json.NewDecoder(strings.NewReader(row.Payload))
	decoder.DisallowUnknownFields()
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
	prepared, err := prepareMappings(mappings)
	if err != nil || len(prepared.rows) != receipt.MappingCount() || prepared.digest != receipt.MappingSHA256() || prepared.payload != row.Payload {
		return nil, ErrCorrupt
	}
	return prepared.rows, nil
}

// LookupMappingsIn verifies the exact receipt, one canonical mapping payload,
// its count/digest and the one-to-one source/target keys in the same borrowed
// scope. A mapping-free low-level ownership receipt returns an empty slice.
// Failure/cleanup never publishes a partial correspondence table.
func LookupMappingsIn(ctx context.Context, reader db.Queryer, expected Receipt) (result []Mapping, err error) {
	if expected.state == nil {
		return nil, ErrInvalid
	}
	validator, err := lookupScope(ctx, reader)
	if err != nil {
		return nil, err
	}
	defer func() {
		if completion := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); completion != nil {
			err = failure(errors.Join(err, completion))
		}
		if err != nil {
			result = nil
		}
	}()
	actual, found, err := LookupIn(ctx, reader, expected.OperationID())
	if err != nil {
		return nil, err
	}
	if !found || *actual.state != *expected.state {
		return nil, ErrStale
	}
	selected, err := models.MappingObjects.Using(reader).Filter(models.MappingFields.TransitionID.Exact(expected.state.id)).OrderBy(models.MappingFields.ID.Asc()).Limit(2)
	if err != nil {
		return nil, failure(err)
	}
	rows, err := selected.All(ctx)
	if err != nil {
		return nil, failure(err)
	}
	if expected.MappingCount() == 0 {
		if len(rows) != 0 {
			return nil, ErrCorrupt
		}
		return []Mapping{}, nil
	}
	if len(rows) != 1 {
		return nil, ErrCorrupt
	}
	return decodeMappings(expected, rows[0])
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
			if _, err := decodeMappings(receipt, row); err != nil {
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
