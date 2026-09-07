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
// Touch must use the currently stored record for its expiry decision, before
// advancing any timestamp. It atomically deletes an expired record and reports
// TouchExpired, or publishes the monotonic active state and reports TouchActive.
// A missing ID reports TouchMissing. Expiry and absence are distinct so callers
// can observe an actual deletion without a second, racy lookup or Delete call.
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
	Touch(context.Context, ID, time.Time, time.Time) (Record, TouchStatus, error)
	Rotate(context.Context, ID, Record) (published Record, rotated bool, err error)
	Delete(context.Context, ID) error
}

// TouchStatus is the closed outcome of one atomic Store.Touch operation.
// Only TouchActive carries a Record; TouchExpired confirms a deletion.
type TouchStatus uint8

const (
	TouchMissing TouchStatus = iota
	TouchActive
	TouchExpired
)
