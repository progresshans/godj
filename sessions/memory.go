package sessions

import (
	"context"
	"sync"
	"time"
)

const (
	defaultMaxMemoryRecords = 4096
	hardMaxMemoryRecords    = 1 << 20
)

// MemoryStore is a concurrent bounded process-lifetime Store. Its contents do
// not survive restart and are not shared across processes.
type MemoryStore struct {
	mu         sync.RWMutex
	records    map[ID]Record
	maxRecords int
}

func (*MemoryStore) String() string   { return "sessions.MemoryStore{redacted}" }
func (*MemoryStore) GoString() string { return "sessions.MemoryStore{redacted}" }

// NewMemoryStore constructs an empty process store. Zero selects 4096 records.
func NewMemoryStore(maxRecords int) (*MemoryStore, error) {
	if maxRecords == 0 {
		maxRecords = defaultMaxMemoryRecords
	}
	if maxRecords < 1 || maxRecords > hardMaxMemoryRecords {
		return nil, &Error{Code: CodeInvalidConfig, Field: "max_records", Detail: "memory store capacity is outside the supported range"}
	}
	return &MemoryStore{records: make(map[ID]Record), maxRecords: maxRecords}, nil
}

func (s *MemoryStore) Load(ctx context.Context, id ID) (Record, bool, error) {
	if err := validStoreCall(ctx, s, id); err != nil {
		return Record{}, false, err
	}
	s.mu.RLock()
	record, ok := s.records[id]
	s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return Record{}, false, err
	}
	return record.clone(), ok, nil
}

func (s *MemoryStore) Create(ctx context.Context, record Record) (bool, error) {
	if err := validStoreCall(ctx, s, record.id); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, exists := s.records[record.id]; exists {
		return false, nil
	}
	if len(s.records) >= s.maxRecords {
		// The process store has no background goroutine. Reap only when a
		// bounded create would otherwise fail, using the incoming record's
		// creation instant as the manager-controlled clock authority.
		for id, existing := range s.records {
			if existing.expired(record.createdAt) {
				delete(s.records, id)
			}
		}
	}
	if len(s.records) >= s.maxRecords {
		return false, &Error{Code: CodeStoreFull, Detail: "memory session capacity is exhausted"}
	}
	s.records[record.id] = record.clone()
	return true, nil
}

func (s *MemoryStore) Touch(ctx context.Context, id ID, accessedAt, idleExpiresAt time.Time) (Record, bool, error) {
	if err := validStoreCall(ctx, s, id); err != nil {
		return Record{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Record{}, false, err
	}
	record, exists := s.records[id]
	if !exists {
		return Record{}, false, nil
	}
	accessedAt = canonicalTime(accessedAt)
	idleExpiresAt = canonicalTime(idleExpiresAt)
	// Concurrent loads can arrive out of order after their initial snapshot.
	// Never let an older touch shorten or move the record backwards.
	if accessedAt.Before(record.accessedAt) {
		accessedAt = record.accessedAt
	}
	if idleExpiresAt.Before(record.idleExpiresAt) {
		idleExpiresAt = record.idleExpiresAt
	}
	if idleExpiresAt.After(record.absoluteExpiresAt) {
		idleExpiresAt = record.absoluteExpiresAt
	}
	if !record.absoluteExpiresAt.After(accessedAt) || !idleExpiresAt.After(accessedAt) {
		return Record{}, false, &Error{Code: CodeInvalidRecord, Field: "expiry", Detail: "session touch timestamps are invalid"}
	}
	record.accessedAt = accessedAt
	record.idleExpiresAt = idleExpiresAt
	s.records[id] = record.clone()
	return record.clone(), true, nil
}

func (s *MemoryStore) Rotate(ctx context.Context, oldID ID, replacement Record) (bool, error) {
	if err := validStoreCall(ctx, s, oldID); err != nil {
		return false, err
	}
	if !replacement.id.Valid() || replacement.id == oldID {
		return false, &Error{Code: CodeInvalidRecord, Field: "replacement", Detail: "replacement session identifier is invalid"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, exists := s.records[oldID]; !exists {
		return false, nil
	}
	if _, collision := s.records[replacement.id]; collision {
		return false, &Error{Code: CodeEntropy, Detail: "replacement session identifier collided"}
	}
	delete(s.records, oldID)
	s.records[replacement.id] = replacement.clone()
	return true, nil
}

func (s *MemoryStore) Delete(ctx context.Context, id ID) error {
	if err := validStoreCall(ctx, s, id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(s.records, id)
	return nil
}

func validStoreCall(ctx context.Context, store *MemoryStore, id ID) error {
	if ctx == nil {
		return &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil || store.records == nil {
		return &Error{Code: CodeInvalidConfig, Detail: "memory store is nil or uninitialized"}
	}
	if !id.Valid() {
		return &Error{Code: CodeInvalidInput, Field: "session_id", Detail: "session identifier is invalid"}
	}
	return nil
}
