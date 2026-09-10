package sessionauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
)

const maxCSRFVerificationKeys = 8

// CSRFKeyRing is an opaque immutable set of already-loaded CSRF MAC keys.
// The active key signs new tokens; the remaining bounded keys only validate
// tokens during a staged deployment rotation. Its zero value requests the
// legacy process-local random key behavior from New.
type CSRFKeyRing struct {
	state *csrfKeyRingState
}

type csrfKeyRingState struct {
	keys  [maxCSRFVerificationKeys][csrfSecretBytes]byte
	count uint8
}

// NewCSRFKeyRing copies one active key and an optional bounded set of
// validation-only keys. Every key must contain exactly 32 bytes and each key
// may appear at most once in the ring.
func NewCSRFKeyRing(active []byte, validation ...[]byte) (CSRFKeyRing, error) {
	if len(active) != csrfSecretBytes {
		return CSRFKeyRing{}, invalidCSRFKeyRing("active CSRF key must contain exactly 32 bytes")
	}
	if 1+len(validation) > maxCSRFVerificationKeys {
		return CSRFKeyRing{}, invalidCSRFKeyRing("CSRF verification key count exceeds the hard limit")
	}

	var state csrfKeyRingState
	copy(state.keys[0][:], active)
	state.count = uint8(1 + len(validation))
	for index, material := range validation {
		if len(material) != csrfSecretBytes {
			return CSRFKeyRing{}, invalidCSRFKeyRing("validation CSRF key must contain exactly 32 bytes")
		}
		copy(state.keys[index+1][:], material)
	}
	ring := CSRFKeyRing{state: &state}
	if !ring.Valid() {
		return CSRFKeyRing{}, invalidCSRFKeyRing("CSRF keys must be distinct")
	}
	return ring, nil
}

// Valid reports whether the value is a complete constructor-produced ring.
// The zero value is intentionally not valid because Config uses it to select
// a process-local random key.
func (ring CSRFKeyRing) Valid() bool {
	if ring.state == nil {
		return false
	}
	count := int(ring.state.count)
	if count < 1 || count > maxCSRFVerificationKeys {
		return false
	}
	for index := 0; index < count; index++ {
		for previous := 0; previous < index; previous++ {
			if ring.state.keys[index] == ring.state.keys[previous] {
				return false
			}
		}
	}
	var empty [csrfSecretBytes]byte
	for index := count; index < len(ring.state.keys); index++ {
		if ring.state.keys[index] != empty {
			return false
		}
	}
	return true
}

func (ring CSRFKeyRing) String() string   { return "sessionauth.CSRFKeyRing{redacted}" }
func (ring CSRFKeyRing) GoString() string { return "sessionauth.CSRFKeyRing{redacted}" }

// Format prevents ordinary numeric verbs from bypassing String and GoString.
// The state indirection also keeps fmt's special-cased %p and %w fallbacks free
// of key material.
func (CSRFKeyRing) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("sessionauth.CSRFKeyRing{redacted}"))
}

// MarshalJSON retains an explicitly redacted diagnostic if a ring is ever
// marshaled on its own. Config excludes the field entirely.
func (CSRFKeyRing) MarshalJSON() ([]byte, error) {
	return []byte(`"sessionauth.CSRFKeyRing{redacted}"`), nil
}

func (ring CSRFKeyRing) isZero() bool {
	return ring.state == nil
}

func (ring CSRFKeyRing) sign(message []byte) [sha256.Size]byte {
	return csrfMACForKey(ring.state.keys[0], message)
}

func (ring CSRFKeyRing) verify(message, candidate []byte) int {
	return csrfMACMatches(
		ring.state.keys[:int(ring.state.count)],
		message,
		candidate,
		csrfMACForKey,
	)
}

type csrfMACComputer func([csrfSecretBytes]byte, []byte) [sha256.Size]byte

// csrfMACMatches deliberately visits every configured key even after a match.
// The injected computer keeps that traversal directly testable without a
// mutable production hook.
func csrfMACMatches(
	keys [][csrfSecretBytes]byte,
	message, candidate []byte,
	compute csrfMACComputer,
) int {
	matches := 0
	for index := range keys {
		expected := compute(keys[index], message)
		matches |= subtle.ConstantTimeCompare(candidate, expected[:])
	}
	return subtle.ConstantTimeEq(int32(matches), 1)
}

func csrfMACForKey(key [csrfSecretBytes]byte, message []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(message)
	var result [sha256.Size]byte
	copy(result[:], mac.Sum(nil))
	return result
}

func invalidCSRFKeyRing(detail string) error {
	return &Error{Code: CodeInvalidConfig, Field: "csrf_key_ring", Detail: detail}
}
