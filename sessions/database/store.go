package database

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/sessionwire"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/sessions/database/models"
)

type loadedRecord struct {
	record  sessions.Record
	present bool
}
type accessedRecord struct {
	record sessions.Record
	status sessions.AccessStatus
}

func (store *Store) Load(ctx context.Context, id sessions.ID) (sessions.Record, bool, error) {
	if err := store.validCall(ctx); err != nil {
		return sessions.Record{}, false, err
	}
	digest, err := bearerDigest(store.state.namespace, id)
	if err != nil {
		return sessions.Record{}, false, err
	}
	result, err := readStore(ctx, store, func(work context.Context, reader db.Queryer) (loadedRecord, error) {
		return store.loadIn(work, reader, id, digest)
	})
	if err != nil {
		return sessions.Record{}, false, err
	}
	return result.record, result.present, nil
}

// LoadIn borrows a live native read snapshot or coordinated write scope. It
// performs no nested transaction. The result is provisional until its owner
// confirms successful cleanup or commit and must not outlive that decision.
func (store *Store) LoadIn(ctx context.Context, scope db.Queryer, id sessions.ID) (sessions.Record, bool, error) {
	if err := store.validCall(ctx); err != nil {
		return sessions.Record{}, false, err
	}
	digest, err := bearerDigest(store.state.namespace, id)
	if err != nil {
		return sessions.Record{}, false, err
	}
	result, err := readBorrowed(ctx, store, scope, func(work context.Context, reader db.Queryer) (loadedRecord, error) {
		return store.loadIn(work, reader, id, digest)
	})
	if err != nil {
		return sessions.Record{}, false, err
	}
	return result.record, result.present, nil
}

func (store *Store) loadIn(ctx context.Context, reader db.Queryer, id sessions.ID, digest string) (loadedRecord, error) {
	row, present, err := store.lookup(ctx, reader, digest)
	if err != nil || !present {
		return loadedRecord{}, err
	}
	record, err := sessionwire.Decode(row.Payload, id, store.state.database.limits)
	if err != nil {
		return loadedRecord{}, err
	}
	return loadedRecord{record: record, present: true}, nil
}

func (store *Store) Create(ctx context.Context, record sessions.Record) (bool, error) {
	if err := store.validCall(ctx); err != nil {
		return false, err
	}
	digest, err := bearerDigest(store.state.namespace, record.ID())
	if err != nil {
		return false, err
	}
	payload, err := sessionwire.Encode(record, store.state.database.limits)
	if err != nil {
		return false, err
	}
	return writeStore(ctx, store, func(work context.Context, scope db.Session) (bool, error) {
		existing, present, err := store.lookup(work, scope, digest)
		if err != nil {
			return false, err
		}
		if present {
			_, err := sessionwire.Decode(existing.Payload, record.ID(), store.state.database.limits)
			return false, err
		}
		if err := store.ensureCapacity(work, scope, record.CreatedAt()); err != nil {
			return false, err
		}
		if _, err := models.SessionObjects.Create(work, scope, models.NewSessionCreate(store.state.namespace, digest, payload)); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (store *Store) Access(ctx context.Context, id sessions.ID, policy sessions.AccessPolicy) (sessions.Record, sessions.AccessStatus, error) {
	if err := store.validCall(ctx); err != nil {
		return sessions.Record{}, sessions.AccessMissing, err
	}
	digest, err := bearerDigest(store.state.namespace, id)
	if err != nil {
		return sessions.Record{}, sessions.AccessMissing, err
	}
	result, err := writeStore(ctx, store, func(work context.Context, scope db.Session) (accessedRecord, error) {
		row, present, err := store.lookup(work, scope, digest)
		if err != nil || !present {
			return accessedRecord{}, err
		}
		current, err := sessionwire.Decode(row.Payload, id, store.state.database.limits)
		if err != nil {
			return accessedRecord{}, err
		}
		next, status, err := policy.Apply(id, current)
		if err != nil {
			return accessedRecord{}, &accessRejection{cause: err}
		}
		if status == sessions.AccessExpired {
			if err := deleteRow(work, scope, row); err != nil {
				return accessedRecord{}, err
			}
			return accessedRecord{status: sessions.AccessExpired}, nil
		}
		if status != sessions.AccessActive {
			return accessedRecord{}, failure(sessions.CodeInvalidRecord, nil)
		}
		payload, err := sessionwire.Encode(next, store.state.database.limits)
		if err != nil {
			return accessedRecord{}, err
		}
		if payload != row.Payload {
			if _, err := models.SessionObjects.Patch(work, scope, row, models.SessionPatch{}.WithPayload(payload)); err != nil {
				return accessedRecord{}, err
			}
		}
		return accessedRecord{record: next, status: sessions.AccessActive}, nil
	})
	if err != nil {
		return sessions.Record{}, sessions.AccessMissing, err
	}
	return result.record, result.status, nil
}

func (store *Store) Rotate(ctx context.Context, oldID sessions.ID, replacement sessions.Record) (sessions.Record, bool, error) {
	return store.replace(ctx, oldID, replacement, false)
}
func (store *Store) Replace(ctx context.Context, oldID sessions.ID, replacement sessions.Record) (sessions.Record, bool, error) {
	return store.replace(ctx, oldID, replacement, true)
}
func (store *Store) replace(ctx context.Context, oldID sessions.ID, replacement sessions.Record, fresh bool) (sessions.Record, bool, error) {
	if err := store.validCall(ctx); err != nil {
		return sessions.Record{}, false, err
	}
	oldDigest, err := bearerDigest(store.state.namespace, oldID)
	if err != nil {
		return sessions.Record{}, false, err
	}
	newDigest, err := bearerDigest(store.state.namespace, replacement.ID())
	if err != nil {
		return sessions.Record{}, false, err
	}
	if replacement.ID() == oldID {
		return sessions.Record{}, false, failure(sessions.CodeInvalidRecord, nil)
	}
	if _, err := sessionwire.Encode(replacement, store.state.database.limits); err != nil {
		return sessions.Record{}, false, err
	}
	result, err := writeStore(ctx, store, func(work context.Context, scope db.Session) (loadedRecord, error) {
		row, present, err := store.lookup(work, scope, oldDigest)
		if err != nil || !present {
			return loadedRecord{}, err
		}
		current, err := sessionwire.Decode(row.Payload, oldID, store.state.database.limits)
		if err != nil {
			return loadedRecord{}, err
		}
		if !fresh && (!current.CreatedAt().Equal(replacement.CreatedAt()) || !current.AbsoluteExpiresAt().Equal(replacement.AbsoluteExpiresAt())) {
			return loadedRecord{}, failure(sessions.CodeInvalidRecord, nil)
		}
		at := replacement.AccessedAt()
		if !current.AbsoluteExpiresAt().After(at) || !current.IdleExpiresAt().After(at) {
			if err := deleteRow(work, scope, row); err != nil {
				return loadedRecord{}, err
			}
			return loadedRecord{}, nil
		}
		collision, exists, err := store.lookup(work, scope, newDigest)
		if err != nil {
			return loadedRecord{}, err
		}
		if exists {
			if _, err := sessionwire.Decode(collision.Payload, replacement.ID(), store.state.database.limits); err != nil {
				return loadedRecord{}, err
			}
			return loadedRecord{}, &sessions.Error{Code: sessions.CodeEntropy, Detail: "replacement session identifier collided"}
		}
		published := replacement
		if !fresh {
			accessed, idle := replacement.AccessedAt(), replacement.IdleExpiresAt()
			if accessed.Before(current.AccessedAt()) {
				accessed = current.AccessedAt()
			}
			if idle.Before(current.IdleExpiresAt()) {
				idle = current.IdleExpiresAt()
			}
			if idle.After(current.AbsoluteExpiresAt()) {
				idle = current.AbsoluteExpiresAt()
			}
			published, err = sessions.RestoreRecord(sessions.RecordSnapshot{ID: replacement.ID(), Values: replacement.Values(), CreatedAt: current.CreatedAt(), AccessedAt: accessed, AbsoluteExpiresAt: current.AbsoluteExpiresAt(), IdleExpiresAt: idle}, store.state.database.limits)
			if err != nil {
				return loadedRecord{}, err
			}
		}
		payload, err := sessionwire.Encode(published, store.state.database.limits)
		if err != nil {
			return loadedRecord{}, err
		}
		if err := deleteRow(work, scope, row); err != nil {
			return loadedRecord{}, err
		}
		if _, err := models.SessionObjects.Create(work, scope, models.NewSessionCreate(store.state.namespace, newDigest, payload)); err != nil {
			return loadedRecord{}, err
		}
		return loadedRecord{record: published, present: true}, nil
	})
	if err != nil {
		return sessions.Record{}, false, err
	}
	return result.record, result.present, nil
}

func (store *Store) Delete(ctx context.Context, id sessions.ID) error {
	if err := store.validCall(ctx); err != nil {
		return err
	}
	digest, err := bearerDigest(store.state.namespace, id)
	if err != nil {
		return err
	}
	_, err = writeStore(ctx, store, func(work context.Context, scope db.Session) (struct{}, error) {
		row, present, err := store.lookup(work, scope, digest)
		if err != nil || !present {
			return struct{}{}, err
		}
		if _, err := sessionwire.Decode(row.Payload, id, store.state.database.limits); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, deleteRow(work, scope, row)
	})
	return err
}

// DeleteMatching removes records with one exact server-side value in this
// namespace. An optional valid keep ID preserves a rotated current session.
// The complete owned payload inventory is checked, including preserved rows;
// a later failure rolls back earlier deletions in the same owner transaction.
func (store *Store) DeleteMatching(ctx context.Context, key, value string, keep sessions.ID) (int, error) {
	if err := store.validCall(ctx); err != nil {
		return 0, err
	}
	limits := store.state.database.limits
	if key == "" || len(key) > limits.MaxKeyBytes || len(value) > limits.MaxValueBytes || len(key)+len(value) > limits.MaxTotalBytes || !utf8.ValidString(key) || !utf8.ValidString(value) || strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
		return 0, failure(sessions.CodeInvalidInput, nil)
	}
	return store.deleteMatching(ctx, key, value, keep, false)
}

// DeleteAll retires this namespace explicitly. It never clears other domains
// and does not infer that an account-domain transition has been authorized.
func (store *Store) DeleteAll(ctx context.Context) (int, error) {
	if err := store.validCall(ctx); err != nil {
		return 0, err
	}
	return store.deleteMatching(ctx, "", "", sessions.ID{}, true)
}
func (store *Store) deleteMatching(ctx context.Context, key, value string, keep sessions.ID, all bool) (int, error) {
	var keepDigest string
	if keep.Valid() {
		var err error
		keepDigest, err = bearerDigest(store.state.namespace, keep)
		if err != nil {
			return 0, err
		}
	}
	return writeStore(ctx, store, func(work context.Context, scope db.Session) (int, error) {
		inventory, err := store.inventory(work, scope)
		if err != nil {
			return 0, err
		}
		deleted := 0
		count, err := store.payloads(work, scope, true, func(row models.Session, metadata sessionwire.Metadata) error {
			actual, present := metadata.Value(key)
			if row.Digest == keepDigest || !all && (!present || actual != value) {
				return nil
			}
			if err := deleteRow(work, scope, row); err != nil {
				return err
			}
			deleted++
			return nil
		})
		if err != nil {
			return 0, err
		}
		if count != inventory.owned {
			return 0, failure(sessions.CodeInvalidRecord, nil)
		}
		return deleted, nil
	})
}
