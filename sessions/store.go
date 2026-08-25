package sessions

import (
	"context"
	"time"
)

// Store is the atomic persistence boundary used by Manager. Implementations
// must detach records and honor context cancellation. ID is a raw bearer secret:
// durable implementations may consume it transiently to derive a lookup key but
// must never persist, log, format, or return its encoded value in diagnostics.
//
// Touch must preserve values and clamp out-of-order timestamps monotonically.
// Rotate must atomically delete oldID, preserve the stored record's immutable
// creation/absolute-expiry state and latest confirmed access/idle timestamps,
// and publish replacement's ID and values. It returns the exact record that was
// published so Manager never reports a stale pre-merge snapshot. The stored row
// is authoritative for idle expiry: when its absolute or idle deadline is not
// after replacement.AccessedAt, Rotate must atomically delete oldID, publish
// nothing and return the zero Record, false, nil.
type Store interface {
	Load(context.Context, ID) (Record, bool, error)
	Create(context.Context, Record) (created bool, err error)
	Touch(context.Context, ID, time.Time, time.Time) (Record, bool, error)
	Rotate(context.Context, ID, Record) (published Record, rotated bool, err error)
	Delete(context.Context, ID) error
}
