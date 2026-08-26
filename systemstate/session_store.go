package systemstate

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

const (
	defaultMaxDurableSessions = 4096
	hardMaxDurableSessions    = 1 << 16
)

var (
	systemRowIDField    = query.NewFieldRef("id", "id", query.FieldInteger, false)
	sessionDigestField  = query.NewFieldRef(sessionDigestColumn, sessionDigestColumn, query.FieldString, false)
	sessionPayloadField = query.NewFieldRef(sessionPayloadColumn, sessionPayloadColumn, query.FieldString, false)
)

// atomicGate is implemented by Runtime. Every framework system-state adapter
// receives the same database-coordinated gate. Callbacks must not recursively
// invoke another operation on the same gate or acquire a different backend
// coordination domain.
type atomicGate interface {
	withAtomic(context.Context, func(db.Session) error) error
}

type durableSessionStore struct {
	gate       atomicGate
	limits     sessions.Limits
	maxRecords int
}

var _ sessions.Store = (*durableSessionStore)(nil)

func (*durableSessionStore) String() string   { return "systemstate.SessionStore{redacted}" }
func (*durableSessionStore) GoString() string { return "systemstate.SessionStore{redacted}" }

func newDurableSessionStore(gate atomicGate, limits sessions.Limits, maxRecords int) (*durableSessionStore, error) {
	if gate == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "runtime", Detail: "system-state runtime is nil"}
	}
	normalized, err := durableSessionLimits(limits)
	if err != nil {
		return nil, err
	}
	if maxRecords == 0 {
		maxRecords = defaultMaxDurableSessions
	}
	if maxRecords < 1 || maxRecords > hardMaxDurableSessions {
		return nil, &Error{Code: CodeInvalidConfig, Field: "max_sessions", Detail: "durable session capacity is outside the current profile"}
	}
	return &durableSessionStore{gate: gate, limits: normalized, maxRecords: maxRecords}, nil
}

func (store *durableSessionStore) Load(ctx context.Context, id sessions.ID) (sessions.Record, bool, error) {
	if err := store.validCall(ctx, id); err != nil {
		return sessions.Record{}, false, err
	}
	digest, err := sessionDigest(id)
	if err != nil {
		return sessions.Record{}, false, err
	}
	var result sessions.Record
	var found bool
	err = store.gate.withAtomic(ctx, func(session db.Session) error {
		row, present, err := loadSessionRow(ctx, session, digest)
		if err != nil || !present {
			return err
		}
		record, err := decodeSessionPayload(row.payload, id, store.limits)
		if err != nil {
			return err
		}
		result, found = record, true
		return nil
	})
	if err != nil {
		return sessions.Record{}, false, err
	}
	return result, found, nil
}

func (store *durableSessionStore) Create(ctx context.Context, record sessions.Record) (bool, error) {
	if err := store.validCall(ctx, record.ID()); err != nil {
		return false, err
	}
	digest, err := sessionDigest(record.ID())
	if err != nil {
		return false, err
	}
	payload, err := encodeSessionPayload(record, store.limits)
	if err != nil {
		return false, err
	}
	created := false
	err = store.gate.withAtomic(ctx, func(session db.Session) error {
		existing, present, err := loadSessionRow(ctx, session, digest)
		if err != nil {
			return err
		}
		if present {
			if _, err := decodeSessionPayload(existing.payload, record.ID(), store.limits); err != nil {
				return err
			}
			return nil
		}
		if err := store.ensureCapacity(ctx, session, record.CreatedAt()); err != nil {
			return err
		}
		if _, err := session.Insert(ctx, sessionInsertPlan(digest, payload)); err != nil {
			return persistenceFailure("create session", err)
		}
		created = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

func (store *durableSessionStore) Touch(
	ctx context.Context,
	id sessions.ID,
	accessedAt time.Time,
	idleExpiresAt time.Time,
) (sessions.Record, bool, error) {
	if err := store.validCall(ctx, id); err != nil {
		return sessions.Record{}, false, err
	}
	digest, err := sessionDigest(id)
	if err != nil {
		return sessions.Record{}, false, err
	}
	var result sessions.Record
	var found bool
	err = store.gate.withAtomic(ctx, func(session db.Session) error {
		row, present, err := loadSessionRow(ctx, session, digest)
		if err != nil || !present {
			return err
		}
		current, err := decodeSessionPayload(row.payload, id, store.limits)
		if err != nil {
			return err
		}
		accessedAt = accessedAt.Round(0).UTC()
		idleExpiresAt = idleExpiresAt.Round(0).UTC()
		if accessedAt.Before(current.AccessedAt()) {
			accessedAt = current.AccessedAt()
		}
		if idleExpiresAt.Before(current.IdleExpiresAt()) {
			idleExpiresAt = current.IdleExpiresAt()
		}
		if idleExpiresAt.After(current.AbsoluteExpiresAt()) {
			idleExpiresAt = current.AbsoluteExpiresAt()
		}
		touched, err := sessions.RestoreRecord(sessions.RecordSnapshot{
			ID:                id,
			Values:            current.Values(),
			CreatedAt:         current.CreatedAt(),
			AccessedAt:        accessedAt,
			AbsoluteExpiresAt: current.AbsoluteExpiresAt(),
			IdleExpiresAt:     idleExpiresAt,
		}, store.limits)
		if err != nil {
			return &sessions.Error{Code: sessions.CodeInvalidRecord, Field: "expiry", Detail: "session touch timestamps are invalid", Cause: err}
		}
		payload, err := encodeSessionPayload(touched, store.limits)
		if err != nil {
			return err
		}
		if payload == row.payload {
			// A monotonic clamp can reduce an out-of-order or equal touch to the
			// exact stored state. Preserve found=true without manufacturing a
			// database write or a new publication event.
			result, found = current, true
			return nil
		}
		affected, err := session.Update(ctx, query.NewUpdatePlan(
			sessionTableName,
			[]query.Assignment{query.NewAssignment(sessionPayloadField, query.String(payload))},
			systemRowIDField,
			query.Integer(row.id),
		))
		if err != nil {
			return persistenceFailure("touch session", err)
		}
		if affected != 1 {
			return cardinalityFailure("session", fmt.Sprintf("touch affected %d rows, want 1", affected))
		}
		result, found = touched, true
		return nil
	})
	if err != nil {
		return sessions.Record{}, false, err
	}
	return result, found, nil
}

func (store *durableSessionStore) Rotate(ctx context.Context, oldID sessions.ID, replacement sessions.Record) (sessions.Record, bool, error) {
	if err := store.validCall(ctx, oldID); err != nil {
		return sessions.Record{}, false, err
	}
	if !replacement.ID().Valid() || replacement.ID() == oldID {
		return sessions.Record{}, false, &sessions.Error{Code: sessions.CodeInvalidRecord, Field: "replacement", Detail: "replacement session identifier is invalid"}
	}
	oldDigest, err := sessionDigest(oldID)
	if err != nil {
		return sessions.Record{}, false, err
	}
	newDigest, err := sessionDigest(replacement.ID())
	if err != nil {
		return sessions.Record{}, false, err
	}
	var published sessions.Record
	rotated := false
	err = store.gate.withAtomic(ctx, func(session db.Session) error {
		oldRow, present, err := loadSessionRow(ctx, session, oldDigest)
		if err != nil || !present {
			return err
		}
		current, err := decodeSessionPayload(oldRow.payload, oldID, store.limits)
		if err != nil {
			return err
		}
		if !replacement.CreatedAt().Equal(current.CreatedAt()) || !replacement.AbsoluteExpiresAt().Equal(current.AbsoluteExpiresAt()) {
			return &sessions.Error{Code: sessions.CodeInvalidRecord, Field: "replacement", Detail: "rotation must preserve creation and absolute expiry"}
		}
		rotationAt := replacement.AccessedAt()
		if !current.AbsoluteExpiresAt().After(rotationAt) || !current.IdleExpiresAt().After(rotationAt) {
			affected, err := session.Delete(ctx, query.NewDeletePlan(sessionTableName, systemRowIDField, query.Integer(oldRow.id)))
			if err != nil {
				return persistenceFailure("delete expired rotated session", err)
			}
			if affected != 1 {
				return cardinalityFailure("session", fmt.Sprintf("expired rotate delete affected %d rows, want 1", affected))
			}
			return nil
		}
		collision, exists, err := loadSessionRow(ctx, session, newDigest)
		if err != nil {
			return err
		}
		if exists {
			if _, err := decodeSessionPayload(collision.payload, replacement.ID(), store.limits); err != nil {
				return err
			}
			return &sessions.Error{Code: sessions.CodeEntropy, Detail: "replacement session identifier collided"}
		}
		accessedAt := replacement.AccessedAt()
		if accessedAt.Before(current.AccessedAt()) {
			accessedAt = current.AccessedAt()
		}
		idleExpiresAt := replacement.IdleExpiresAt()
		if idleExpiresAt.Before(current.IdleExpiresAt()) {
			idleExpiresAt = current.IdleExpiresAt()
		}
		if idleExpiresAt.After(current.AbsoluteExpiresAt()) {
			idleExpiresAt = current.AbsoluteExpiresAt()
		}
		published, err = sessions.RestoreRecord(sessions.RecordSnapshot{
			ID:                replacement.ID(),
			Values:            replacement.Values(),
			CreatedAt:         current.CreatedAt(),
			AccessedAt:        accessedAt,
			AbsoluteExpiresAt: current.AbsoluteExpiresAt(),
			IdleExpiresAt:     idleExpiresAt,
		}, store.limits)
		if err != nil {
			return &sessions.Error{Code: sessions.CodeInvalidRecord, Field: "replacement", Detail: "rotated session timestamps are invalid", Cause: err}
		}
		payload, err := encodeSessionPayload(published, store.limits)
		if err != nil {
			return err
		}
		affected, err := session.Delete(ctx, query.NewDeletePlan(sessionTableName, systemRowIDField, query.Integer(oldRow.id)))
		if err != nil {
			return persistenceFailure("delete rotated session", err)
		}
		if affected != 1 {
			return cardinalityFailure("session", fmt.Sprintf("rotate delete affected %d rows, want 1", affected))
		}
		if _, err := session.Insert(ctx, sessionInsertPlan(newDigest, payload)); err != nil {
			return persistenceFailure("insert rotated session", err)
		}
		rotated = true
		return nil
	})
	if err != nil {
		return sessions.Record{}, false, err
	}
	return published, rotated, nil
}

func (store *durableSessionStore) Delete(ctx context.Context, id sessions.ID) error {
	if err := store.validCall(ctx, id); err != nil {
		return err
	}
	digest, err := sessionDigest(id)
	if err != nil {
		return err
	}
	return store.gate.withAtomic(ctx, func(session db.Session) error {
		row, present, err := loadSessionRow(ctx, session, digest)
		if err != nil || !present {
			return err
		}
		if _, err := decodeSessionPayload(row.payload, id, store.limits); err != nil {
			return err
		}
		affected, err := session.Delete(ctx, query.NewDeletePlan(sessionTableName, systemRowIDField, query.Integer(row.id)))
		if err != nil {
			return persistenceFailure("delete session", err)
		}
		if affected != 1 {
			return cardinalityFailure("session", fmt.Sprintf("delete affected %d rows, want 1", affected))
		}
		return nil
	})
}

func (store *durableSessionStore) ensureCapacity(ctx context.Context, session db.Session, now time.Time) error {
	count, err := scanSessionInventory(ctx, session, store.maxRecords+1)
	if err != nil {
		return err
	}
	if count > store.maxRecords {
		return &sessions.Error{Code: sessions.CodeStoreFull, Detail: "durable session capacity is inconsistent or exhausted"}
	}
	if count < store.maxRecords {
		return nil
	}
	now = now.Round(0).UTC()
	expiredRowID := int64(0)
	payloadCount, err := scanSessionPayloads(ctx, session, store.limits, store.maxRecords+1, func(rowID int64, metadata sessions.Record) error {
		if now.Before(metadata.AbsoluteExpiresAt()) && now.Before(metadata.IdleExpiresAt()) {
			return nil
		}
		if expiredRowID == 0 || rowID < expiredRowID {
			expiredRowID = rowID
		}
		return nil
	})
	if err != nil {
		return err
	}
	if payloadCount != count {
		return cardinalityFailure("session", "session capacity changed while it was inspected")
	}
	if expiredRowID == 0 {
		return &sessions.Error{Code: sessions.CodeStoreFull, Detail: "durable session capacity is exhausted"}
	}
	// One incoming record needs exactly one slot. Validate the complete bounded
	// stream before performing DML, then reap the lowest-ID expired row so the
	// mutation count and victim selection are deterministic.
	affected, err := session.Delete(ctx, query.NewDeletePlan(sessionTableName, systemRowIDField, query.Integer(expiredRowID)))
	if err != nil {
		return persistenceFailure("reap expired session", err)
	}
	if affected != 1 {
		return cardinalityFailure("session", fmt.Sprintf("reap affected %d rows, want 1", affected))
	}
	return nil
}

func (store *durableSessionStore) validCall(ctx context.Context, id sessions.ID) error {
	if ctx == nil {
		return &sessions.Error{Code: sessions.CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil || store.gate == nil {
		return &sessions.Error{Code: sessions.CodeInvalidConfig, Detail: "durable session store is nil or uninitialized"}
	}
	if !id.Valid() {
		return &sessions.Error{Code: sessions.CodeInvalidInput, Field: "session_id", Detail: "session identifier is invalid"}
	}
	return nil
}

type persistedSessionRow struct {
	id      int64
	digest  string
	payload string
}

func loadSessionRow(ctx context.Context, queryer db.Queryer, digest string) (persistedSessionRow, bool, error) {
	plan, err := query.NewPlan(sessionTableName, []query.FieldRef{
		systemRowIDField,
		sessionDigestField,
		sessionPayloadField,
	}).WithLimit(2)
	if err != nil {
		return persistedSessionRow{}, false, persistenceFailure("build session lookup", err)
	}
	plan = plan.WithConditions(query.NewCondition(sessionDigestField, query.LookupExact, query.String(digest)))
	rows, err := readSessionRows(ctx, queryer, plan, 2)
	if err != nil {
		return persistedSessionRow{}, false, err
	}
	if len(rows) == 0 {
		return persistedSessionRow{}, false, nil
	}
	if len(rows) != 1 {
		return persistedSessionRow{}, false, cardinalityFailure("session_digest", "session digest matched more than one row")
	}
	if rows[0].digest != digest || !validSessionDigest(rows[0].digest) {
		return persistedSessionRow{}, false, &Error{Code: CodeCorruptState, Field: "session_digest", Detail: "stored session digest is malformed"}
	}
	return rows[0], true, nil
}

func listSessionRows(ctx context.Context, queryer db.Queryer, limit int) ([]persistedSessionRow, error) {
	plan, err := query.NewPlan(sessionTableName, []query.FieldRef{
		systemRowIDField,
		sessionDigestField,
		sessionPayloadField,
	}).WithLimit(limit)
	if err != nil {
		return nil, persistenceFailure("build session capacity query", err)
	}
	plan = plan.WithOrderings(query.NewOrdering(systemRowIDField, query.Ascending))
	rows, err := readSessionRows(ctx, queryer, plan, limit)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.id <= 0 || !validSessionDigest(row.digest) {
			return nil, &Error{Code: CodeCorruptState, Field: "session_row", Detail: "stored session row is malformed"}
		}
	}
	return rows, nil
}

// scanSessionInventory streams only row identity and digest. Ordering by
// digest makes duplicates adjacent, so fail-closed cardinality needs constant
// memory even at the hard record cap. Payload bytes are never selected here.
func scanSessionInventory(ctx context.Context, queryer db.Queryer, limit int) (int, error) {
	if ctx == nil {
		return 0, &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if queryer == nil {
		return 0, &Error{Code: CodeInvalidConfig, Field: "backend", Detail: "query backend is nil"}
	}
	plan, err := query.NewPlan(sessionTableName, []query.FieldRef{
		systemRowIDField,
		sessionDigestField,
	}).WithLimit(limit)
	if err != nil {
		return 0, persistenceFailure("build session inventory query", err)
	}
	plan = plan.WithOrderings(
		query.NewOrdering(sessionDigestField, query.Ascending),
		query.NewOrdering(systemRowIDField, query.Ascending),
	)
	result, err := queryer.Query(ctx, plan)
	if err != nil {
		if result != nil {
			_ = result.Close()
		}
		return 0, persistenceFailure("query session inventory", err)
	}
	if result == nil {
		return 0, persistenceFailure("query session inventory", errors.New("backend returned nil rows"))
	}

	count := 0
	previousDigest := ""
	for result.Next() {
		if err := ctx.Err(); err != nil {
			_ = result.Close()
			return 0, err
		}
		var rowID int64
		var digest string
		if err := result.Scan(&rowID, &digest); err != nil {
			_ = result.Close()
			return 0, &Error{Code: CodeCorruptState, Field: "session_row", Detail: "stored session inventory row cannot be decoded", Cause: err}
		}
		count++
		if count > limit {
			_ = result.Close()
			return 0, cardinalityFailure("session", "backend returned more rows than the bounded inventory plan")
		}
		if rowID <= 0 || !validSessionDigest(digest) {
			_ = result.Close()
			return 0, &Error{Code: CodeCorruptState, Field: "session_row", Detail: "stored session inventory row is malformed"}
		}
		if previousDigest != "" && digest == previousDigest {
			_ = result.Close()
			return 0, cardinalityFailure("session_digest", "stored session digest is duplicated")
		}
		previousDigest = digest
	}
	if err := ctx.Err(); err != nil {
		_ = result.Close()
		return 0, err
	}
	iterationErr := result.Err()
	closeErr := result.Close()
	if iterationErr != nil || closeErr != nil {
		return 0, persistenceFailure("iterate session inventory", errors.Join(iterationErr, closeErr))
	}
	return count, nil
}

// scanSessionPayloads holds at most one persisted payload and one decoded
// record at a time. The byte bound is checked immediately after Scan and
// before base64 decoding or value-map allocation.
func scanSessionPayloads(
	ctx context.Context,
	queryer db.Queryer,
	limits sessions.Limits,
	limit int,
	visit func(int64, sessions.Record) error,
) (int, error) {
	if ctx == nil {
		return 0, &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if queryer == nil {
		return 0, &Error{Code: CodeInvalidConfig, Field: "backend", Detail: "query backend is nil"}
	}
	plan, err := query.NewPlan(sessionTableName, []query.FieldRef{
		systemRowIDField,
		sessionDigestField,
		sessionPayloadField,
	}).WithLimit(limit)
	if err != nil {
		return 0, persistenceFailure("build session payload scan", err)
	}
	// The primary-key ordering can stream without a payload-bearing sort. A
	// digest-only inventory scan has already established duplicate cardinality
	// before every caller reaches this path.
	plan = plan.WithOrderings(query.NewOrdering(systemRowIDField, query.Ascending))
	result, err := queryer.Query(ctx, plan)
	if err != nil {
		if result != nil {
			_ = result.Close()
		}
		return 0, persistenceFailure("query session payloads", err)
	}
	if result == nil {
		return 0, persistenceFailure("query session payloads", errors.New("backend returned nil rows"))
	}

	count := 0
	for result.Next() {
		if err := ctx.Err(); err != nil {
			_ = result.Close()
			return 0, err
		}
		var row persistedSessionRow
		if err := result.Scan(&row.id, &row.digest, &row.payload); err != nil {
			_ = result.Close()
			return 0, &Error{Code: CodeCorruptState, Field: "session_row", Detail: "stored session row cannot be decoded", Cause: err}
		}
		count++
		if count > limit {
			_ = result.Close()
			return 0, cardinalityFailure("session", "backend returned more rows than the bounded payload plan")
		}
		if row.id <= 0 || !validSessionDigest(row.digest) {
			_ = result.Close()
			return 0, &Error{Code: CodeCorruptState, Field: "session_row", Detail: "stored session row is malformed"}
		}
		if len(row.payload) > maxSessionPayloadBytes {
			_ = result.Close()
			return 0, &Error{Code: CodeCorruptState, Field: "session_payload", Detail: "stored session payload exceeds the current storage bound"}
		}
		metadata, err := decodeSessionMetadata(row.payload, limits)
		if err != nil {
			_ = result.Close()
			return 0, err
		}
		if visit != nil {
			if err := visit(row.id, metadata); err != nil {
				_ = result.Close()
				return 0, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		_ = result.Close()
		return 0, err
	}
	iterationErr := result.Err()
	closeErr := result.Close()
	if iterationErr != nil || closeErr != nil {
		return 0, persistenceFailure("iterate session payloads", errors.Join(iterationErr, closeErr))
	}
	return count, nil
}

func inspectSessionTable(
	ctx context.Context,
	queryer db.Queryer,
	limits sessions.Limits,
	maxRecords int,
) (bool, error) {
	count, err := scanSessionInventory(ctx, queryer, maxRecords+1)
	if err != nil {
		return false, sessionInspectionFailure(err)
	}
	if count > maxRecords {
		return false, cardinalityFailure("session", "stored sessions exceed configured capacity")
	}
	payloadCount, err := scanSessionPayloads(ctx, queryer, limits, maxRecords+1, nil)
	if err != nil {
		return false, sessionInspectionFailure(err)
	}
	if payloadCount != count {
		return false, cardinalityFailure("session", "session table changed while it was inspected")
	}
	return count != 0, nil
}

func sessionInspectionFailure(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var classified *Error
	if errors.As(err, &classified) && (classified.Code == CodeCardinality || classified.Code == CodeCorruptState) {
		return err
	}
	return &Error{Code: CodeSchemaUnavailable, Field: sessionTableName, Detail: "required session table is unavailable", Cause: err}
}

func readSessionRows(ctx context.Context, queryer db.Queryer, plan query.Plan, limit int) ([]persistedSessionRow, error) {
	if queryer == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "backend", Detail: "query backend is nil"}
	}
	result, err := queryer.Query(ctx, plan)
	if err != nil {
		if result != nil {
			_ = result.Close()
		}
		return nil, persistenceFailure("query sessions", err)
	}
	if result == nil {
		return nil, persistenceFailure("query sessions", errors.New("backend returned nil rows"))
	}
	rows := make([]persistedSessionRow, 0, limit)
	for result.Next() {
		if err := ctx.Err(); err != nil {
			_ = result.Close()
			return nil, err
		}
		var row persistedSessionRow
		if err := result.Scan(&row.id, &row.digest, &row.payload); err != nil {
			_ = result.Close()
			return nil, &Error{Code: CodeCorruptState, Field: "session_row", Detail: "stored session row cannot be decoded", Cause: err}
		}
		if len(row.payload) > maxSessionPayloadBytes {
			_ = result.Close()
			return nil, &Error{Code: CodeCorruptState, Field: "session_payload", Detail: "stored session payload exceeds the current storage bound"}
		}
		rows = append(rows, row)
		if len(rows) > limit {
			_ = result.Close()
			return nil, cardinalityFailure("session", "backend returned more rows than the bounded plan")
		}
	}
	if err := ctx.Err(); err != nil {
		_ = result.Close()
		return nil, err
	}
	iterationErr := result.Err()
	closeErr := result.Close()
	if iterationErr != nil || closeErr != nil {
		return nil, persistenceFailure("iterate sessions", errors.Join(iterationErr, closeErr))
	}
	return rows, nil
}

func sessionInsertPlan(digest, payload string) query.InsertPlan {
	return query.NewInsertPlanReturningKey(
		sessionTableName,
		[]query.Assignment{
			query.NewAssignment(sessionDigestField, query.String(digest)),
			query.NewAssignment(sessionPayloadField, query.String(payload)),
		},
		systemRowIDField,
	)
}

func decodeSessionMetadata(payload string, limits sessions.Limits) (sessions.Record, error) {
	placeholder, err := sessions.ParseID(strings.Repeat("A", 43))
	if err != nil {
		return sessions.Record{}, &Error{Code: CodeInvalidConfig, Field: "session_codec", Detail: "session codec placeholder is invalid", Cause: err}
	}
	return decodeSessionPayload(payload, placeholder, limits)
}

func validSessionDigest(digest string) bool {
	if len(digest) != sessionDigestMaxLength || strings.ToLower(digest) != digest {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32
}

func persistenceFailure(operation string, cause error) error {
	return &Error{Code: CodePersistence, Detail: operation + " failed", Cause: cause}
}

func cardinalityFailure(field, detail string) error {
	return &Error{Code: CodeCardinality, Field: field, Detail: detail}
}
