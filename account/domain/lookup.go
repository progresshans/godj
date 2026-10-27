package domain

import (
	"context"
	"errors"

	"github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
)

// LookupIn observes one operation and its actual predecessor in a borrowed
// snapshot or coordinated transaction. Absence remains ordinary data until
// the outer owner confirms cleanup. Retained/root scopes and duplicate
// operation rows are refused. No current-authority or rollback claim is made.
func LookupIn(ctx context.Context, reader db.Queryer, operationID string) (receipt Receipt, found bool, err error) {
	if nilValue(ctx) || nilValue(reader) {
		return Receipt{}, false, ErrInvalid
	}
	if _, valid := digest(operationID); !valid {
		return Receipt{}, false, ErrInvalid
	}
	validator, valid := reader.(db.SessionValidator)
	if !valid || nilValue(validator) {
		return Receipt{}, false, ErrInvalid
	}
	switch reader.(type) {
	case db.Atomic, db.CoordinatedAtomic, db.RelationAtomic, db.CoordinatedRelationAtomic, db.SnapshotReader:
		return Receipt{}, false, ErrInvalid
	}
	if err := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return Receipt{}, false, failure(err)
	}
	defer func() {
		if completion := errors.Join(ctx.Err(), validator.ValidateSession(ctx)); completion != nil {
			err = failure(errors.Join(err, completion))
		}
		if err != nil {
			receipt, found = Receipt{}, false
		}
	}()
	selected, err := models.TransitionObjects.Using(reader).Filter(models.TransitionFields.OperationID.Exact(operationID)).OrderBy(models.TransitionFields.ID.Asc()).Limit(2)
	if err != nil {
		return Receipt{}, false, failure(err)
	}
	rows, err := selected.All(ctx)
	if err != nil {
		return Receipt{}, false, failure(err)
	}
	if len(rows) == 0 {
		return Receipt{}, false, nil
	}
	if len(rows) != 1 {
		return Receipt{}, false, ErrCorrupt
	}
	row := rows[0]
	receipt, err = fromRow(row)
	if err != nil {
		return Receipt{}, false, err
	}
	if row.Sequence > 1 {
		prior, present, err := models.TransitionObjects.Using(reader).Filter(models.TransitionFields.ID.Exact(*row.PreviousID)).OrderBy(models.TransitionFields.ID.Asc()).First(ctx)
		if err != nil {
			return Receipt{}, false, failure(err)
		}
		if !present || validatePredecessor(row, prior) != nil {
			return Receipt{}, false, ErrCorrupt
		}
	}
	return receipt, true, nil
}
