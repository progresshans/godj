package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"

	"github.com/progresshans/godj/db"
)

const (
	postgresCoordinatedAtomicDomain = "godj/postgres/coordinated-atomic/v1"
	postgresCoordinatedAtomicLock   = `SELECT "pg_catalog"."pg_advisory_xact_lock"($1)`
)

var _ db.CoordinatedAtomic = (*Backend)(nil)
var _ db.CoordinatedRelationAtomic = (*Backend)(nil)

// CoordinatedAtomicRelation uses the same schema advisory lock and transaction
// owner as ordinary cooperative writes; it exposes the existing relation
// mutator without opening or nesting another transaction.
func (b *Backend) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	if callback == nil {
		return b.CoordinatedAtomic(ctx, nil)
	}
	return b.CoordinatedAtomic(ctx, func(session db.Session) error {
		relation, ok := session.(db.RelationSession)
		if !ok || relation == nil {
			return backendInvalid("PostgreSQL coordinated relation session is invalid")
		}
		return callback(relation)
	})
}

// CoordinatedAtomic executes callback through Atomic after acquiring the
// schema-scoped transaction advisory lock. Atomic remains the sole owner of
// callback cleanup, rollback, session expiry, and commit-outcome semantics.
func (b *Backend) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	if callback == nil {
		return b.Atomic(ctx, nil)
	}
	return b.Atomic(ctx, func(session db.Session) error {
		transactionSession, ok := session.(*transactionSession)
		if !ok || transactionSession == nil || transactionSession.transaction == nil {
			return backendInvalid("PostgreSQL coordinated transaction session is invalid")
		}
		if _, err := transactionSession.transaction.ExecContext(
			ctx,
			postgresCoordinatedAtomicLock,
			postgresCoordinatedAtomicAdvisoryLockKey(b.schema),
		); err != nil {
			return classifyDatabaseError(ctx, "acquire coordinated transaction fence", b.schema, "", err)
		}
		return callback(session)
	})
}

func postgresCoordinatedAtomicAdvisoryLockKey(schema string) int64 {
	hash := sha256.New()
	for _, value := range []string{postgresCoordinatedAtomicDomain, schema} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return int64(binary.BigEndian.Uint64(hash.Sum(nil)[:8]))
}
