package systemstate

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/progresshans/godj/internal/sessionwire"
	"github.com/progresshans/godj/sessions"
)

const (
	sessionPayloadPrefix   = "v1."
	maxSessionPayloadBytes = sessionPayloadMaxLength
	sessionDigestDomain    = "godj.systemstate.session.v1\x00"
)

// The legacy storage owner retains its published error categories and digest
// namespace. Canonical record bytes have one implementation shared with the
// independent database store; this does not move or reinterpret stored rows.
func durableSessionLimits(limits sessions.Limits) (sessions.Limits, error) {
	normalized, err := sessionwire.NormalizeLimits(limits)
	if err != nil {
		return sessions.Limits{}, &Error{Code: CodeInvalidConfig, Field: "session_limits", Detail: "durable session limits are outside the current profile", Cause: err}
	}
	return normalized, nil
}

func sessionDigest(id sessions.ID) (string, error) {
	if !id.Valid() {
		return "", &Error{Code: CodeInvalidInput, Field: "session_id", Detail: "session identifier is invalid"}
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(sessionDigestDomain))
	_, _ = digest.Write([]byte(id.Encoded()))
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func encodeSessionPayload(record sessions.Record, limits sessions.Limits) (string, error) {
	limits, err := durableSessionLimits(limits)
	if err != nil {
		return "", err
	}
	payload, err := sessionwire.Encode(record, limits)
	if err != nil {
		return "", &Error{Code: CodeInvalidInput, Field: "record", Detail: "session record is invalid", Cause: err}
	}
	return payload, nil
}

func decodeSessionPayload(payload string, id sessions.ID, limits sessions.Limits) (sessions.Record, error) {
	limits, err := durableSessionLimits(limits)
	if err != nil {
		return sessions.Record{}, err
	}
	if !id.Valid() {
		return sessions.Record{}, &Error{Code: CodeInvalidInput, Field: "session_id", Detail: "session identifier is invalid"}
	}
	record, err := sessionwire.Decode(payload, id, limits)
	if err != nil {
		return sessions.Record{}, &Error{Code: CodeCorruptState, Field: "session_payload", Detail: "stored session payload is malformed or incompatible", Cause: err}
	}
	return record, nil
}
