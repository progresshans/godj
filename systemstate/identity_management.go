package systemstate

import (
	"context"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/query"
)

var _ identity.ManagementBackend = (*Runtime)(nil)

// ReadSnapshot delegates to the native stable snapshot. Identity password work
// runs only after this scope has ended; it must not borrow Runtime's write gate.
func (runtime *Runtime) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	if err := runtime.validBackendCall(ctx); err != nil {
		return err
	}
	backend, ok := runtime.backend.(db.SnapshotReader)
	if !ok || isNilInterface(backend) {
		return &Error{Code: CodeInvalidConfig, Field: "snapshot_backend", Detail: "native read snapshots are unavailable"}
	}
	return backend.ReadSnapshot(ctx, callback)
}

// CoordinatedAtomic exposes the same fence used by Runtime.Atomic, sessions and
// audit to identity maintenance. All effects use the one borrowed Session.
func (runtime *Runtime) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	return runtime.withAtomic(ctx, callback)
}

// CoordinatedAtomicRelation exposes that same domain with the borrowed
// capabilities needed by identity collections and host relation deletion.
func (runtime *Runtime) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	return runtime.AtomicRelation(ctx, callback)
}

// RevokePrincipalSessions deletes only the target's authenticated session rows
// through an already coordinated transaction. It never opens a transaction or
// publishes a commit result itself. Anonymous and other users' rows are kept.
// Batches bound memory and close SELECT rows before any DELETE; a later error
// must be returned by the outer callback so all earlier deletes roll back.
func (runtime *Runtime) RevokePrincipalSessions(ctx context.Context, session db.Session, principalID string) (int, error) {
	if err := runtime.validBackendCall(ctx); err != nil {
		return 0, err
	}
	if isNilInterface(session) || runtime.sessionStore == nil {
		return 0, &Error{Code: CodeInvalidInput, Field: "session", Detail: "identity session maintenance is uninitialized"}
	}
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil {
		return 0, &Error{Code: CodeInvalidInput, Field: "principal", Detail: "identity session target is invalid", Cause: err}
	}
	// Validate the complete bounded inventory before deletion. This also catches
	// nonpositive IDs that keyset pagination must not silently skip.
	inventory, err := scanSessionInventory(ctx, session, runtime.sessionStore.maxRecords+1)
	if err != nil {
		return 0, err
	}
	if inventory > runtime.sessionStore.maxRecords {
		return 0, cardinalityFailure("session", "session revocation exceeds configured capacity")
	}
	const batchSize = 256
	var after int64
	scanned, deleted := 0, 0
	for {
		plan, err := query.NewPlan(sessionTableName, []query.FieldRef{systemRowIDField, sessionDigestField, sessionPayloadField}).
			WithOrderings(query.NewOrdering(systemRowIDField, query.Ascending)).WithLimit(batchSize)
		if err != nil {
			return 0, err
		}
		plan, err = plan.WithConditions(query.NewCondition(systemRowIDField, query.LookupGreaterThan, query.Integer(after)))
		if err != nil {
			return 0, err
		}
		rows, err := readSessionRows(ctx, session, plan, batchSize)
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			if scanned != inventory {
				return 0, cardinalityFailure("session", "session revocation inventory changed inside its scope")
			}
			return deleted, nil
		}
		for _, row := range rows {
			scanned++
			if scanned > runtime.sessionStore.maxRecords {
				return 0, cardinalityFailure("session", "session revocation exceeds configured capacity")
			}
			if row.id <= after || !validSessionDigest(row.digest) {
				return 0, &Error{Code: CodeCorruptState, Field: "session", Detail: "session revocation inventory is invalid"}
			}
			after = row.id
			record, err := decodeSessionMetadata(row.payload, runtime.sessionStore.limits)
			if err != nil {
				return 0, err
			}
			if id, _ := record.Value(auth.SessionPrincipalIDKey); id != principalID {
				continue
			}
			affected, err := session.Delete(ctx, query.NewDeletePlan(sessionTableName, systemRowIDField, query.Integer(row.id)))
			if err != nil {
				return 0, persistenceFailure("revoke identity session", err)
			}
			if affected != 1 {
				return 0, cardinalityFailure("session", "identity session revocation did not delete one row")
			}
			deleted++
		}
	}
}
