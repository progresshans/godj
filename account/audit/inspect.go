package audit

import (
	"context"
	"errors"
	"math"

	"github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/rowset"
)

const inspectionBatchSize = 256

// InspectIn reads the complete append-only inventory in bounded, closed pages.
// Historical domains and deleted actors/targets remain legitimate audit data.
// It validates event encoding, not an actor's current permission or row presence.
// It performs no pruning or repair and publishes no event values. The count is
// provisional until the caller's borrowed scope has successfully completed.
func InspectIn(ctx context.Context, reader db.Queryer) (count int64, err error) {
	if err := checkScope(ctx, reader); err != nil {
		return 0, err
	}
	switch reader.(type) {
	case db.Atomic, db.CoordinatedAtomic, db.RelationAtomic, db.CoordinatedRelationAtomic, db.SnapshotReader:
		return 0, ErrInvalid
	}
	defer func() {
		if completion := checkScope(ctx, reader); completion != nil {
			err = failure(errors.Join(err, completion))
		}
		if err != nil {
			count = 0
		}
	}()
	var last int64
	started := false
	for {
		source := models.EventObjects.Using(reader).OrderBy(models.EventFields.ID.Asc())
		if started {
			source = source.Filter(models.EventFields.ID.GreaterThan(last))
		}
		bounded, err := source.Limit(inspectionBatchSize)
		if err != nil {
			return 0, failure(err)
		}
		page := 0
		err = rowset.Scan(ctx, reader, bounded.Plan(), failure, func(rows db.Rows) (bool, error) {
			row, err := (models.EventDescriptor{}).Scan(rows)
			if err != nil {
				return false, failure(err)
			}
			if page >= inspectionBatchSize || started && row.ID <= last || count == math.MaxInt64 {
				return false, ErrCorrupt
			}
			if _, err := fromRow(row); err != nil {
				return false, err
			}
			last, started, count, page = row.ID, true, count+1, page+1
			return true, nil
		})
		if err != nil {
			return 0, err
		}
		if page < inspectionBatchSize {
			return count, nil
		}
	}
}
