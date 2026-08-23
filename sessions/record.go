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
	if !r.id.Valid() || r.createdAt.IsZero() || r.accessedAt.IsZero() ||
		r.absoluteExpiresAt.IsZero() || r.idleExpiresAt.IsZero() ||
		r.accessedAt.Before(r.createdAt) || !r.absoluteExpiresAt.After(r.createdAt) ||
		r.idleExpiresAt.After(r.absoluteExpiresAt) || !r.idleExpiresAt.After(r.accessedAt) {
		return false
	}
	return validateValues(r.values, limits) == nil
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
