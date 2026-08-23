package sessions

import (
	"context"
	"time"
)

// Store is the atomic persistence boundary used by Manager. Implementations
// must detach records and honor context cancellation. Touch must preserve
// values, while Rotate must delete oldID and publish replacement atomically.
type Store interface {
	Load(context.Context, ID) (Record, bool, error)
	Create(context.Context, Record) (created bool, err error)
	Touch(context.Context, ID, time.Time, time.Time) (Record, bool, error)
	Rotate(context.Context, ID, Record) (rotated bool, err error)
	Delete(context.Context, ID) error
}
