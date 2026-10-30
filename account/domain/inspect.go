package domain

import (
	"context"
	"errors"
	"strings"

	"github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/rowset"
	"github.com/progresshans/godj/orm"
)

const inspectionBatchSize = 256

// InspectIn validates the complete ownership chain in a borrowed snapshot or
// transaction and returns its latest receipt. Unlike a normal current-domain
// lookup, startup inspection checks older links and unique operation/generation
// identities too. It never initializes or transitions ownership;
// ErrUninitialized means that the whole history is empty.
// The receipt becomes publishable only after the outer scope has succeeded.
func InspectIn(ctx context.Context, reader db.Queryer) (result Receipt, err error) {
	if nilValue(ctx) {
		return Receipt{}, ErrInvalid
	}
	if err := checkContext(ctx); err != nil {
		return Receipt{}, err
	}
	if nilValue(reader) {
		return Receipt{}, ErrInvalid
	}
	validator, ok := reader.(db.SessionValidator)
	if !ok || nilValue(validator) {
		return Receipt{}, ErrInvalid
	}
	switch reader.(type) {
	case db.Atomic, db.CoordinatedAtomic, db.RelationAtomic, db.CoordinatedRelationAtomic, db.SnapshotReader:
		return Receipt{}, ErrInvalid
	}
	if err := validator.ValidateSession(ctx); err != nil {
		return Receipt{}, failure(err)
	}
	defer func() {
		if completion := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); completion != nil {
			err = failure(errors.Join(err, completion))
		}
		if err != nil {
			result = Receipt{}
		}
	}()
	var previous models.Transition
	var mapped int64
	started := false
	for {
		source := models.TransitionObjects.Using(reader).OrderBy(models.TransitionFields.Sequence.Asc(), models.TransitionFields.ID.Asc())
		if started {
			source = source.Filter(orm.Or(models.TransitionFields.Sequence.GreaterThan(previous.Sequence),
				orm.And(models.TransitionFields.Sequence.Exact(previous.Sequence), models.TransitionFields.ID.GreaterThan(previous.ID))))
		}
		bounded, err := source.Limit(inspectionBatchSize)
		if err != nil {
			return Receipt{}, failure(err)
		}
		page := 0
		err = rowset.Scan(ctx, reader, bounded.Plan(), failure, func(rows db.Rows) (bool, error) {
			row, err := (models.TransitionDescriptor{}).Scan(rows)
			if err != nil {
				return false, failure(err)
			}
			if page >= inspectionBatchSize {
				return false, ErrCorrupt
			}
			candidate, err := fromRow(row)
			if err != nil {
				return false, err
			}
			if !started {
				if row.Sequence != 1 {
					return false, ErrCorrupt
				}
			} else if validatePredecessor(row, previous) != nil {
				return false, ErrCorrupt
			}
			result, previous, started = candidate, row, true
			if candidate.MappingCount() > 0 {
				mapped++
			}
			page++
			return true, nil
		})
		if err != nil {
			return Receipt{}, err
		}
		if page < inspectionBatchSize {
			if !started {
				if err := inspectMappings(ctx, reader, 0); err != nil {
					return Receipt{}, err
				}
				return Receipt{}, ErrUninitialized
			}
			if err := inspectUniqueHistory(ctx, reader, result.Sequence()); err != nil {
				return Receipt{}, err
			}
			if err := inspectMappings(ctx, reader, mapped); err != nil {
				return Receipt{}, err
			}
			return result, nil
		}
	}
}

type historyIdentity struct {
	id    int64
	value string
}

// An old generation must never be resurrected, even when a damaged database
// has lost its uniqueness constraint. Ordered projections detect nonadjacent
// duplicates without retaining the full history or any unbounded set.
func inspectUniqueHistory(ctx context.Context, reader db.Queryer, expected int64) error {
	for _, field := range []orm.StringField[models.Transition]{models.TransitionFields.OperationID, models.TransitionFields.Generation} {
		var last historyIdentity
		var count int64
		for {
			source := models.TransitionObjects.Using(reader).OrderBy(field.Asc(), models.TransitionFields.ID.Asc())
			if count != 0 {
				source = source.Filter(orm.Or(field.GreaterThan(last.value), orm.And(field.Exact(last.value), models.TransitionFields.ID.GreaterThan(last.id))))
			}
			selected, err := orm.SelectQuery(source, orm.Project2(models.TransitionFields.ID, field, func(id int64, value string) historyIdentity { return historyIdentity{id, value} }))
			if err != nil {
				return failure(err)
			}
			selected, err = selected.Limit(inspectionBatchSize)
			if err != nil {
				return failure(err)
			}
			page := 0
			err = rowset.Scan(ctx, reader, selected.Plan(), failure, func(rows db.Rows) (bool, error) {
				var row historyIdentity
				if err := rows.Scan(&row.id, &row.value); err != nil {
					return false, failure(err)
				}
				if _, valid := digest(row.value); !valid || row.id <= 0 || page >= inspectionBatchSize || count >= expected || count > 0 && strings.Compare(last.value, row.value) >= 0 {
					return false, ErrCorrupt
				}
				last, count, page = row, count+1, page+1
				return true, nil
			})
			if err != nil {
				return err
			}
			if page < inspectionBatchSize {
				break
			}
		}
		if count != expected {
			return ErrCorrupt
		}
	}
	return nil
}
