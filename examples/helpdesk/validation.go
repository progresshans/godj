package helpdesk

import (
	"context"
	"errors"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// Called only for the direct insert/update failure inside Atomic. The native
// diagnostic cannot reliably identify a form field across both backends, so a
// concurrent conflict belongs to the whole input. Never parse its value text.
// Atomic returns this rejection unchanged only if its rollback succeeds.
func writeRejection(err error) error {
	conflict, ok := err.(*query.Error)
	if !ok || conflict.Category != query.CategoryIntegrity || conflict.Code != query.CodeUniqueConstraint {
		return err
	}
	return validation.Reject(validation.NewErrors(validation.New(validation.NonField, validation.CodeUnique)), err)
}

// A confirmed rollback's typed rejection stays intact. Cancellation and any
// concurrent execution failure remain errors instead of input diagnostics.
func operationError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return errors.Join(err, contextErr)
	}
	return err
}
