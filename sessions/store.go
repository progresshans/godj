package sessions

import (
	"context"
)

// Store is the atomic persistence boundary used by Manager. Implementations
// must detach records and honor context cancellation. ID is a raw bearer secret:
// durable implementations may consume it transiently to derive a lookup key but
// must never persist, log, format, or return its encoded value in diagnostics.
//
// Access must apply its policy to the currently stored record before
// advancing any timestamp. It atomically deletes an expired record and reports
// AccessExpired, or publishes the monotonic active state and reports AccessActive.
// A missing ID reports AccessMissing. Expiry and absence are distinct so callers
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
	Access(context.Context, ID, AccessPolicy) (Record, AccessStatus, error)
	Rotate(context.Context, ID, Record) (published Record, rotated bool, err error)
	Delete(context.Context, ID) error
}

// AccessStatus is the closed outcome of one atomic Store.Access operation.
// Only AccessActive carries a Record; AccessExpired confirms a deletion.
type AccessStatus uint8

const (
	AccessMissing AccessStatus = iota
	AccessActive
	AccessExpired
)
