package sessions

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"
)

const (
	defaultAbsoluteLifetime = 24 * time.Hour
	defaultIdleTimeout      = 30 * time.Minute
	maximumLifetime         = 365 * 24 * time.Hour
	maximumIDAttempts       = 4
)

// Config controls session creation and expiry. Zero values select the bounded
// current profile. Clock and Random exist for deterministic testing; Manager
// serializes calls to both sources.
type Config struct {
	AbsoluteLifetime time.Duration
	IdleTimeout      time.Duration
	Limits           Limits
	Clock            func() time.Time
	Random           io.Reader
}

// Manager owns session ID entropy, record validation, sliding idle expiry and
// fixation-safe rotation over one Store.
type Manager struct {
	store            Store
	absoluteLifetime time.Duration
	idleTimeout      time.Duration
	limits           Limits
	clock            func() time.Time
	random           io.Reader
	sourceMu         sync.Mutex
}

func (*Manager) String() string   { return "sessions.Manager{redacted}" }
func (*Manager) GoString() string { return "sessions.Manager{redacted}" }

func NewManager(store Store, config Config) (*Manager, error) {
	if store == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "store", Detail: "store is nil"}
	}
	if config.AbsoluteLifetime == 0 {
		config.AbsoluteLifetime = defaultAbsoluteLifetime
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = defaultIdleTimeout
	}
	if config.AbsoluteLifetime <= 0 || config.AbsoluteLifetime > maximumLifetime {
		return nil, &Error{Code: CodeInvalidConfig, Field: "absolute_lifetime", Detail: "absolute lifetime is outside the supported range"}
	}
	if config.IdleTimeout <= 0 || config.IdleTimeout > config.AbsoluteLifetime {
		return nil, &Error{Code: CodeInvalidConfig, Field: "idle_timeout", Detail: "idle timeout must be positive and no greater than absolute lifetime"}
	}
	limits, err := normalizeLimits(config.Limits)
	if err != nil {
		return nil, err
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &Manager{
		store:            store,
		absoluteLifetime: config.AbsoluteLifetime,
		idleTimeout:      config.IdleTimeout,
		limits:           limits,
		clock:            config.Clock,
		random:           config.Random,
	}, nil
}

// Load returns a detached active record and atomically advances its sliding
// idle deadline. Missing and expired identifiers return found=false; an expired
// record is deleted before return.
func (m *Manager) Load(ctx context.Context, id ID) (Record, bool, error) {
	if err := m.validCall(ctx); err != nil {
		return Record{}, false, err
	}
	if !id.Valid() {
		return Record{}, false, &Error{Code: CodeInvalidInput, Field: "session_id", Detail: "session identifier is invalid"}
	}
	record, found, err := m.store.Load(ctx, id)
	if err != nil {
		return Record{}, false, storeFailure("load", err)
	}
	if !found {
		return Record{}, false, nil
	}
	if !record.valid(m.limits) || record.id != id {
		return Record{}, false, &Error{Code: CodeInvalidRecord, Detail: "store returned an invalid session record"}
	}
	now := m.now()
	if now.IsZero() {
		return Record{}, false, &Error{Code: CodeInvalidConfig, Field: "clock", Detail: "clock returned the zero time"}
	}
	if record.expired(now) {
		if err := m.store.Delete(ctx, id); err != nil {
			return Record{}, false, storeFailure("delete expired", err)
		}
		return Record{}, false, nil
	}
	// A clock moving backwards must not reduce AccessedAt or extend lifetime
	// from an earlier instant.
	if now.Before(record.accessedAt) {
		now = record.accessedAt
	}
	idleExpiresAt := minimumTime(now.Add(m.idleTimeout), record.absoluteExpiresAt)
	touched, present, err := m.store.Touch(ctx, id, now, idleExpiresAt)
	if err != nil {
		return Record{}, false, storeFailure("touch", err)
	}
	if !present {
		return Record{}, false, nil
	}
	if !touched.valid(m.limits) || touched.id != id {
		return Record{}, false, &Error{Code: CodeInvalidRecord, Detail: "store returned an invalid touched record"}
	}
	return touched.clone(), true, nil
}

// Create publishes a new 256-bit session with detached validated values.
func (m *Manager) Create(ctx context.Context, values map[string]string) (Record, error) {
	if err := m.validCall(ctx); err != nil {
		return Record{}, err
	}
	if err := validateValues(values, m.limits); err != nil {
		return Record{}, err
	}
	now := m.now()
	if now.IsZero() {
		return Record{}, &Error{Code: CodeInvalidConfig, Field: "clock", Detail: "clock returned the zero time"}
	}
	for attempt := 0; attempt < maximumIDAttempts; attempt++ {
		id, err := m.newID()
		if err != nil {
			return Record{}, err
		}
		absoluteExpiresAt := now.Add(m.absoluteLifetime)
		record := newRecord(id, values, now, now, absoluteExpiresAt, minimumTime(now.Add(m.idleTimeout), absoluteExpiresAt))
		created, err := m.store.Create(ctx, record)
		if err != nil {
			return Record{}, storeFailure("create", err)
		}
		if created {
			return record.clone(), nil
		}
	}
	return Record{}, &Error{Code: CodeEntropy, Detail: "session identifier collision limit was reached"}
}

// Rotate atomically replaces current's ID while preserving its creation and
// absolute expiry. Values may be derived with Record.WithValue beforehand.
func (m *Manager) Rotate(ctx context.Context, current Record) (Record, error) {
	if err := m.validCall(ctx); err != nil {
		return Record{}, err
	}
	if !current.valid(m.limits) {
		return Record{}, &Error{Code: CodeInvalidRecord, Field: "record", Detail: "session record is invalid"}
	}
	now := m.now()
	if now.IsZero() {
		return Record{}, &Error{Code: CodeInvalidConfig, Field: "clock", Detail: "clock returned the zero time"}
	}
	// Absolute expiry is immutable, so a detached snapshot can decide it safely.
	// Idle expiry can have advanced through a concurrent Load/Touch after current
	// was detached; Store.Rotate owns the authoritative atomic expiry decision.
	if !current.absoluteExpiresAt.After(now) {
		if err := m.store.Delete(ctx, current.id); err != nil {
			return Record{}, storeFailure("delete expired", err)
		}
		return Record{}, &Error{Code: CodeNotFound, Detail: "session is missing or expired"}
	}
	if now.Before(current.accessedAt) {
		now = current.accessedAt
	}
	for attempt := 0; attempt < maximumIDAttempts; attempt++ {
		id, err := m.newID()
		if err != nil {
			return Record{}, err
		}
		replacement := newRecord(
			id,
			current.values,
			current.createdAt,
			now,
			current.absoluteExpiresAt,
			minimumTime(now.Add(m.idleTimeout), current.absoluteExpiresAt),
		)
		published, rotated, err := m.store.Rotate(ctx, current.id, replacement)
		if err != nil {
			var classified *Error
			if errors.As(err, &classified) && classified.Code == CodeEntropy {
				continue
			}
			return Record{}, storeFailure("rotate", err)
		}
		if !rotated {
			return Record{}, &Error{Code: CodeNotFound, Detail: "session is missing or was already rotated"}
		}
		if !published.valid(m.limits) || published.id != replacement.id {
			return Record{}, &Error{Code: CodeInvalidRecord, Detail: "store returned an invalid rotated session record"}
		}
		return published.clone(), nil
	}
	return Record{}, &Error{Code: CodeEntropy, Detail: "session rotation collision limit was reached"}
}

// Delete removes one session. Deleting an absent ID is idempotent.
func (m *Manager) Delete(ctx context.Context, id ID) error {
	if err := m.validCall(ctx); err != nil {
		return err
	}
	if !id.Valid() {
		return &Error{Code: CodeInvalidInput, Field: "session_id", Detail: "session identifier is invalid"}
	}
	if err := m.store.Delete(ctx, id); err != nil {
		return storeFailure("delete", err)
	}
	return nil
}

// Flush is the explicit logout spelling of Delete.
func (m *Manager) Flush(ctx context.Context, id ID) error { return m.Delete(ctx, id) }

func (m *Manager) validCall(ctx context.Context) error {
	if ctx == nil {
		return &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil || m.store == nil || m.clock == nil || m.random == nil {
		return &Error{Code: CodeInvalidConfig, Detail: "session manager is nil or uninitialized"}
	}
	return nil
}

func (m *Manager) newID() (ID, error) {
	buffer := make([]byte, sessionIDBytes)
	m.sourceMu.Lock()
	if _, err := io.ReadFull(m.random, buffer); err != nil {
		m.sourceMu.Unlock()
		return ID{}, &Error{Code: CodeEntropy, Detail: "session entropy source failed", Cause: err}
	}
	m.sourceMu.Unlock()
	return ID{encoded: base64.RawURLEncoding.EncodeToString(buffer)}, nil
}

func (m *Manager) now() time.Time {
	m.sourceMu.Lock()
	now := m.clock()
	m.sourceMu.Unlock()
	return canonicalTime(now)
}

func storeFailure(operation string, cause error) error {
	var classified *Error
	if errors.As(cause, &classified) && classified.Code == CodeStoreFull {
		return classified
	}
	return &Error{Code: CodeStoreFailure, Detail: operation + " operation failed", Cause: cause}
}
