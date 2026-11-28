// Package sessionwire owns the canonical bounded record payload shared by
// durable session stores. Storage namespaces and bearer digests remain with
// their database owner; the payload never contains the raw bearer identifier.
package sessionwire

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/progresshans/godj/sessions"
)

const MaximumPayloadBytes = 32768
const sessionPayloadPrefix = "v1."

// NormalizeLimits retains the existing durable record profile. Broader limits
// need an explicit storage/wire change; they are never silently truncated.
func NormalizeLimits(limits sessions.Limits) (sessions.Limits, error) {
	defaults := sessions.DefaultLimits()
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
	if limits.MaxValues < 1 || limits.MaxValues > defaults.MaxValues ||
		limits.MaxKeyBytes < 1 || limits.MaxKeyBytes > defaults.MaxKeyBytes ||
		limits.MaxValueBytes < 1 || limits.MaxValueBytes > defaults.MaxValueBytes ||
		limits.MaxTotalBytes < 1 || limits.MaxTotalBytes > defaults.MaxTotalBytes {
		return sessions.Limits{}, &sessions.Error{
			Code:   sessions.CodeInvalidConfig,
			Field:  "session_limits",
			Detail: "durable session limits are outside the current profile",
		}
	}
	return limits, nil
}

// Encode emits one canonical current-only string. The bearer ID
// is deliberately absent: callers persist its domain-separated digest beside
// this payload and provide the requested ID again during decode.
func Encode(record sessions.Record, limits sessions.Limits) (string, error) {
	limits, err := NormalizeLimits(limits)
	if err != nil {
		return "", err
	}
	snapshot := record.Snapshot()
	if _, err := sessions.RestoreRecord(snapshot, limits); err != nil {
		return "", &sessions.Error{Code: sessions.CodeInvalidRecord, Field: "record", Detail: "session record is invalid", Cause: err}
	}

	timestamps := [...]time.Time{
		snapshot.CreatedAt,
		snapshot.AccessedAt,
		snapshot.AbsoluteExpiresAt,
		snapshot.IdleExpiresAt,
	}
	buffer := bytes.NewBuffer(make([]byte, 0, 64))
	for _, timestamp := range timestamps {
		canonical := timestamp.Round(0).UTC()
		nanoseconds := canonical.UnixNano()
		if !time.Unix(0, nanoseconds).UTC().Equal(canonical) {
			return "", &sessions.Error{Code: sessions.CodeInvalidRecord, Field: "record", Detail: "session timestamp is outside the current wire range"}
		}
		_ = binary.Write(buffer, binary.BigEndian, nanoseconds)
	}

	keys := make([]string, 0, len(snapshot.Values))
	for key := range snapshot.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	_ = binary.Write(buffer, binary.BigEndian, uint16(len(keys)))
	for _, key := range keys {
		value := snapshot.Values[key]
		_ = binary.Write(buffer, binary.BigEndian, uint16(len(key)))
		_, _ = buffer.WriteString(key)
		_ = binary.Write(buffer, binary.BigEndian, uint32(len(value)))
		_, _ = buffer.WriteString(value)
	}

	payload := sessionPayloadPrefix + base64.RawURLEncoding.EncodeToString(buffer.Bytes())
	if len(payload) > MaximumPayloadBytes {
		return "", &sessions.Error{Code: sessions.CodeInvalidRecord, Field: "record", Detail: "encoded session exceeds the current storage bound"}
	}
	return payload, nil
}

func Decode(payload string, id sessions.ID, limits sessions.Limits) (sessions.Record, error) {
	limits, err := NormalizeLimits(limits)
	if err != nil {
		return sessions.Record{}, err
	}
	corrupt := func(cause error) (sessions.Record, error) {
		return sessions.Record{}, &sessions.Error{
			Code:   sessions.CodeInvalidRecord,
			Field:  "session_payload",
			Detail: "stored session payload is malformed or incompatible",
			Cause:  cause,
		}
	}
	if !id.Valid() {
		return sessions.Record{}, &sessions.Error{Code: sessions.CodeInvalidRecord, Field: "session_id", Detail: "session identifier is invalid"}
	}
	if len(payload) <= len(sessionPayloadPrefix) || len(payload) > MaximumPayloadBytes ||
		!strings.HasPrefix(payload, sessionPayloadPrefix) {
		return corrupt(nil)
	}
	wire, err := base64.RawURLEncoding.Strict().DecodeString(payload[len(sessionPayloadPrefix):])
	if err != nil {
		return corrupt(err)
	}
	reader := bytes.NewReader(wire)
	readTime := func() (time.Time, error) {
		var nanoseconds int64
		if err := binary.Read(reader, binary.BigEndian, &nanoseconds); err != nil {
			return time.Time{}, err
		}
		return time.Unix(0, nanoseconds).UTC(), nil
	}
	createdAt, err := readTime()
	if err != nil {
		return corrupt(err)
	}
	accessedAt, err := readTime()
	if err != nil {
		return corrupt(err)
	}
	absoluteExpiresAt, err := readTime()
	if err != nil {
		return corrupt(err)
	}
	idleExpiresAt, err := readTime()
	if err != nil {
		return corrupt(err)
	}

	var count uint16
	if err := binary.Read(reader, binary.BigEndian, &count); err != nil || int(count) > limits.MaxValues {
		return corrupt(err)
	}
	values := make(map[string]string, int(count))
	previousKey := ""
	totalBytes := 0
	for index := 0; index < int(count); index++ {
		var keyLength uint16
		if err := binary.Read(reader, binary.BigEndian, &keyLength); err != nil ||
			keyLength == 0 || int(keyLength) > limits.MaxKeyBytes || int(keyLength) > reader.Len() {
			return corrupt(err)
		}
		keyBytes := make([]byte, int(keyLength))
		if _, err := reader.Read(keyBytes); err != nil {
			return corrupt(err)
		}
		var valueLength uint32
		if err := binary.Read(reader, binary.BigEndian, &valueLength); err != nil ||
			valueLength > uint32(limits.MaxValueBytes) || uint64(valueLength) > uint64(reader.Len()) {
			return corrupt(err)
		}
		valueBytes := make([]byte, int(valueLength))
		if _, err := reader.Read(valueBytes); err != nil {
			return corrupt(err)
		}
		key, value := string(keyBytes), string(valueBytes)
		if !utf8.ValidString(key) || !utf8.ValidString(value) || strings.ContainsRune(key, '\x00') ||
			strings.ContainsRune(value, '\x00') || (index > 0 && key <= previousKey) {
			return corrupt(nil)
		}
		totalBytes += len(key) + len(value)
		if totalBytes > limits.MaxTotalBytes {
			return corrupt(nil)
		}
		values[key] = value
		previousKey = key
	}
	if reader.Len() != 0 {
		return corrupt(nil)
	}

	record, err := sessions.RestoreRecord(sessions.RecordSnapshot{
		ID:                id,
		Values:            values,
		CreatedAt:         createdAt,
		AccessedAt:        accessedAt,
		AbsoluteExpiresAt: absoluteExpiresAt,
		IdleExpiresAt:     idleExpiresAt,
	}, limits)
	if err != nil {
		return corrupt(err)
	}
	canonical, err := Encode(record, limits)
	if err != nil || canonical != payload {
		return corrupt(err)
	}
	return record, nil
}
