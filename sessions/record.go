package sessions

import (
	"encoding/base64"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	sessionIDBytes        = 32
	sessionIDEncodedBytes = 43
	defaultMaxValues      = 32
	defaultMaxKeyBytes    = 64
	defaultMaxValueBytes  = 4096
	defaultMaxTotalBytes  = 16 * 1024
	hardMaxValues         = 256
	hardMaxKeyBytes       = 256
	hardMaxValueBytes     = 64 * 1024
	hardMaxTotalBytes     = 256 * 1024
)

// ID is an opaque 256-bit session identifier. Encoded is the only operation
// that releases its bearer value; ordinary formatting is deliberately
// redacted.
type ID struct {
	encoded string
}

// ParseID validates the canonical unpadded URL-safe encoding used by Manager.
func ParseID(encoded string) (ID, error) {
	if len(encoded) != sessionIDEncodedBytes {
		return ID{}, &Error{Code: CodeInvalidInput, Field: "session_id", Detail: "session identifier has an invalid length"}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != sessionIDBytes || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return ID{}, &Error{Code: CodeInvalidInput, Field: "session_id", Detail: "session identifier is malformed"}
	}
	return ID{encoded: encoded}, nil
}

// Encoded returns the bearer value for a cookie or Store implementation.
func (id ID) Encoded() string { return id.encoded }

// Valid reports whether ID has the canonical 256-bit representation.
func (id ID) Valid() bool {
	_, err := ParseID(id.encoded)
	return err == nil
}

func (id ID) String() string   { return "[session-id]" }
func (id ID) GoString() string { return "sessions.ID{redacted}" }

// Limits bound one record before it can reach a Store. Zero fields select
// secure defaults; negative or excessively broad values are rejected.
type Limits struct {
	MaxValues     int
	MaxKeyBytes   int
	MaxValueBytes int
	MaxTotalBytes int
}

// DefaultLimits returns the current bounded process-store profile.
func DefaultLimits() Limits {
	return Limits{
		MaxValues:     defaultMaxValues,
		MaxKeyBytes:   defaultMaxKeyBytes,
		MaxValueBytes: defaultMaxValueBytes,
		MaxTotalBytes: defaultMaxTotalBytes,
	}
}

func normalizeLimits(limits Limits) (Limits, error) {
	defaults := DefaultLimits()
	if limits.MaxValues == 0 {
		limits.MaxValues = defaults.MaxValues
	}
	if limits.MaxKeyBytes == 0 {
		limits.MaxKeyBytes = defaults.MaxKeyBytes
	}
	if limits.MaxValueBytes == 0 {
		limits.MaxValueBytes = defaults.MaxValueBytes
	}
	if limits.MaxTotalBytes == 0 {
		limits.MaxTotalBytes = defaults.MaxTotalBytes
	}
	if limits.MaxValues < 1 || limits.MaxValues > hardMaxValues ||
		limits.MaxKeyBytes < 1 || limits.MaxKeyBytes > hardMaxKeyBytes ||
		limits.MaxValueBytes < 1 || limits.MaxValueBytes > hardMaxValueBytes ||
		limits.MaxTotalBytes < 1 || limits.MaxTotalBytes > hardMaxTotalBytes {
		return Limits{}, &Error{Code: CodeInvalidConfig, Field: "limits", Detail: "session record limits are outside the supported range"}
	}
	return limits, nil
}

// Record is an immutable detached session snapshot.
type Record struct {
	id                ID
	values            map[string]string
	createdAt         time.Time
	accessedAt        time.Time
	absoluteExpiresAt time.Time
	idleExpiresAt     time.Time
}

// RecordSnapshot is the detached current persistence representation of one
// Record. It exists so Store adapters can encode and restore the immutable
// session state without gaining mutable access to Record internals. Values is
// copied on both publication and restore.
//
// RecordSnapshot is an in-process SPI, not a wire format. Durable stores must
// define and strictly version their own bounded encoding rather than relying
// on Go field names or a generic serializer.
type RecordSnapshot struct {
	ID                ID
	Values            map[string]string
	CreatedAt         time.Time
	AccessedAt        time.Time
	AbsoluteExpiresAt time.Time
	IdleExpiresAt     time.Time
}

func (RecordSnapshot) String() string   { return "sessions.RecordSnapshot{redacted}" }
func (RecordSnapshot) GoString() string { return "sessions.RecordSnapshot{redacted}" }

func newRecord(id ID, values map[string]string, createdAt, accessedAt, absoluteExpiresAt, idleExpiresAt time.Time) Record {
	return Record{
		id:                id,
		values:            cloneValues(values),
		createdAt:         canonicalTime(createdAt),
		accessedAt:        canonicalTime(accessedAt),
		absoluteExpiresAt: canonicalTime(absoluteExpiresAt),
		idleExpiresAt:     canonicalTime(idleExpiresAt),
	}
}

func (r Record) ID() ID                          { return r.id }
func (r Record) CreatedAt() time.Time            { return r.createdAt }
func (r Record) AccessedAt() time.Time           { return r.accessedAt }
func (r Record) AbsoluteExpiresAt() time.Time    { return r.absoluteExpiresAt }
func (r Record) IdleExpiresAt() time.Time        { return r.idleExpiresAt }
func (r Record) String() string                  { return "sessions.Record{redacted}" }
func (r Record) GoString() string                { return "sessions.Record{redacted}" }
func (r Record) Value(key string) (string, bool) { value, ok := r.values[key]; return value, ok }

// Values returns a detached copy.
func (r Record) Values() map[string]string { return cloneValues(r.values) }

// Snapshot returns a detached current persistence snapshot. A zero or corrupt
// Record produces a correspondingly invalid snapshot; RestoreRecord performs
// the authoritative validation before a Store can publish it again.
func (r Record) Snapshot() RecordSnapshot {
	return RecordSnapshot{
		ID:                r.id,
		Values:            cloneValues(r.values),
		CreatedAt:         canonicalTime(r.createdAt),
		AccessedAt:        canonicalTime(r.accessedAt),
		AbsoluteExpiresAt: canonicalTime(r.absoluteExpiresAt),
		IdleExpiresAt:     canonicalTime(r.idleExpiresAt),
	}
}

// RestoreRecord validates and reconstructs one immutable Record from a Store
// adapter's decoded current snapshot. The same Limits used by Manager should
// be supplied so corrupt or oversized persistent data is rejected before it
// reaches Manager; Manager repeats validation at every Store boundary.
func RestoreRecord(snapshot RecordSnapshot, limits Limits) (Record, error) {
	normalized, err := normalizeLimits(limits)
	if err != nil {
		return Record{}, err
	}
	createdAt := canonicalTime(snapshot.CreatedAt)
	accessedAt := canonicalTime(snapshot.AccessedAt)
	absoluteExpiresAt := canonicalTime(snapshot.AbsoluteExpiresAt)
	idleExpiresAt := canonicalTime(snapshot.IdleExpiresAt)
	if !validRecordState(
		snapshot.ID,
		snapshot.Values,
		createdAt,
		accessedAt,
		absoluteExpiresAt,
		idleExpiresAt,
		normalized,
	) {
		return Record{}, &Error{
			Code:   CodeInvalidRecord,
			Field:  "snapshot",
			Detail: "persistent session snapshot is invalid",
		}
	}
	// Validate the adapter-owned map before detaching it. Successful restore is
	// the sole clone; malformed or oversized persistent input allocates no map
	// proportional to attacker-controlled cardinality.
	return newRecord(
		snapshot.ID,
		snapshot.Values,
		createdAt,
		accessedAt,
		absoluteExpiresAt,
		idleExpiresAt,
	), nil
}

// WithValue derives a detached record. Manager repeats configured bounds before
// the value reaches a Store.
func (r Record) WithValue(key, value string) (Record, error) {
	if !validValuePart(key, hardMaxKeyBytes) || !validValuePart(value, hardMaxValueBytes) || key == "" {
		return Record{}, &Error{Code: CodeInvalidInput, Field: "value", Detail: "session key or value is malformed or too large"}
	}
	clone := r.clone()
	if clone.values == nil {
		clone.values = make(map[string]string)
	}
	clone.values[key] = value
	if len(clone.values) > hardMaxValues || valuesBytes(clone.values) > hardMaxTotalBytes {
		return Record{}, &Error{Code: CodeInvalidInput, Field: "values", Detail: "session values exceed the hard resource limit"}
	}
	return clone, nil
}

// WithoutValue derives a record without key.
func (r Record) WithoutValue(key string) Record {
	clone := r.clone()
	delete(clone.values, key)
	return clone
}

func (r Record) clone() Record {
	clone := r
	clone.values = cloneValues(r.values)
	return clone
}

func (r Record) expired(now time.Time) bool {
	now = canonicalTime(now)
	return !now.Before(r.absoluteExpiresAt) || !now.Before(r.idleExpiresAt)
}

func (r Record) valid(limits Limits) bool {
	return validRecordState(
		r.id,
		r.values,
		r.createdAt,
		r.accessedAt,
		r.absoluteExpiresAt,
		r.idleExpiresAt,
		limits,
	)
}

func validRecordState(
	id ID,
	values map[string]string,
	createdAt time.Time,
	accessedAt time.Time,
	absoluteExpiresAt time.Time,
	idleExpiresAt time.Time,
	limits Limits,
) bool {
	if !id.Valid() || createdAt.IsZero() || accessedAt.IsZero() ||
		absoluteExpiresAt.IsZero() || idleExpiresAt.IsZero() ||
		accessedAt.Before(createdAt) || !absoluteExpiresAt.After(createdAt) ||
		idleExpiresAt.After(absoluteExpiresAt) || !idleExpiresAt.After(accessedAt) {
		return false
	}
	return validateValues(values, limits) == nil
}

func validateValues(values map[string]string, limits Limits) error {
	if len(values) > limits.MaxValues || valuesBytes(values) > limits.MaxTotalBytes {
		return &Error{Code: CodeInvalidRecord, Field: "values", Detail: "session values exceed the configured resource limit"}
	}
	for key, value := range values {
		if key == "" || !validValuePart(key, limits.MaxKeyBytes) || !validValuePart(value, limits.MaxValueBytes) {
			return &Error{Code: CodeInvalidRecord, Field: "values", Detail: "session key or value is malformed or too large"}
		}
	}
	return nil
}

func validValuePart(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func valuesBytes(values map[string]string) int {
	total := 0
	for key, value := range values {
		total += len(key) + len(value)
	}
	return total
}

func cloneValues(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func canonicalTime(value time.Time) time.Time { return value.Round(0).UTC() }

func minimumTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
