package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalidResetToken combines missing, inactive, unusable, expired, changed
// or incorrectly signed account observations. Wrapped errors remain failures.
var ErrInvalidResetToken = errors.New("auth: invalid password reset token")

const maximumPasswordResetKeys = 8

// PasswordResetKeyRing owns host-loaded signing keys independently of session
// and CSRF keys. The first key issues tokens; up to seven fallbacks only verify.
// Copies share immutable key material. Construction performs no I/O or entropy.
type PasswordResetKeyRing struct{ state *passwordResetKeys }
type passwordResetKeys struct{ keys [][sha256.Size]byte }

func NewPasswordResetKeyRing(active []byte, fallback ...[]byte) (PasswordResetKeyRing, error) {
	if len(active) != sha256.Size || len(fallback) >= maximumPasswordResetKeys {
		return PasswordResetKeyRing{}, invalidPasswordResetConfig()
	}
	keys := make([][sha256.Size]byte, 1, len(fallback)+1)
	copy(keys[0][:], active)
	for _, material := range fallback {
		if len(material) != sha256.Size {
			return PasswordResetKeyRing{}, invalidPasswordResetConfig()
		}
		var key [sha256.Size]byte
		copy(key[:], material)
		for _, previous := range keys {
			if key == previous {
				return PasswordResetKeyRing{}, invalidPasswordResetConfig()
			}
		}
		keys = append(keys, key)
	}
	return PasswordResetKeyRing{&passwordResetKeys{keys}}, nil
}

func (keys PasswordResetKeyRing) Valid() bool { return keys.state != nil }
func (PasswordResetKeyRing) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("auth.PasswordResetKeyRing{redacted}"))
}
func (PasswordResetKeyRing) MarshalJSON() ([]byte, error) {
	return []byte(`"auth.PasswordResetKeyRing{redacted}"`), nil
}

// PasswordResetState is a detached, bounded observation, never authorization
// to read or write an account. The persistence owner supplies these values from
// one coherent snapshot and must reobserve them inside its final write fence.
// A scoped credential's stamp includes its exact account ownership domain.
type PasswordResetState struct{ state *passwordResetState }
type passwordResetState struct {
	id               int64
	credential       Credential
	email, lastLogin string
}

func NewPasswordResetState(id int64, credential Credential, email string, lastLogin *time.Time) (PasswordResetState, error) {
	if credential.state == nil || credential.SessionStamp() == "" || !utf8.ValidString(email) || len(email) > MaximumPasswordResetEmailBytes || strings.ContainsRune(email, 0) {
		return PasswordResetState{}, &Error{Code: CodeCredential, Detail: "password reset observation is invalid"}
	}
	last := ""
	if lastLogin != nil {
		instant := lastLogin.UTC()
		if instant.Year() < 1 || instant.Year() > 9999 {
			return PasswordResetState{}, &Error{Code: CodeCredential, Detail: "password reset observation is invalid"}
		}
		last = instant.Format(time.RFC3339Nano)
	}
	return PasswordResetState{&passwordResetState{id, credential, email, last}}, nil
}

func (PasswordResetState) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("auth.PasswordResetState{redacted}"))
}
func (PasswordResetState) MarshalJSON() ([]byte, error) {
	return []byte(`"auth.PasswordResetState{redacted}"`), nil
}

// PasswordResetToken contains recipient-only delivery material from the SAME
// snapshot as its signature. It retains no encoded password or credential.
// Explicit accessors are for the mail/broker consumer, never public responses.
type PasswordResetToken struct{ state *passwordResetToken }
type passwordResetToken struct{ principalID, username, email, encoded string }

func (token PasswordResetToken) PrincipalID() string {
	if token.state == nil {
		return ""
	}
	return token.state.principalID
}
func (token PasswordResetToken) Username() string {
	if token.state == nil {
		return ""
	}
	return token.state.username
}
func (token PasswordResetToken) Email() string {
	if token.state == nil {
		return ""
	}
	return token.state.email
}
func (token PasswordResetToken) Encoded() string {
	if token.state == nil {
		return ""
	}
	return token.state.encoded
}
func (PasswordResetToken) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("auth.PasswordResetToken{redacted}"))
}
func (PasswordResetToken) MarshalJSON() ([]byte, error) {
	return []byte(`"auth.PasswordResetToken{redacted}"`), nil
}

// PasswordResetSignature is syntax-checked secret input, not a verified token.
// Its zero value is invalid. Parsing is bounded and performs no I/O.
type PasswordResetSignature struct {
	timestamp int64
	mac       [sha256.Size]byte
	valid     bool
}

func (PasswordResetSignature) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("auth.PasswordResetSignature{redacted}"))
}
func (PasswordResetSignature) MarshalJSON() ([]byte, error) {
	return []byte(`"auth.PasswordResetSignature{redacted}"`), nil
}

func ParsePasswordResetToken(encoded string) (PasswordResetSignature, bool) {
	if len(encoded) > 56 {
		return PasswordResetSignature{}, false
	}
	parts := strings.Split(encoded, ".")
	if len(parts) != 3 || parts[0] != "r1" {
		return PasswordResetSignature{}, false
	}
	timestamp, err := strconv.ParseInt(parts[1], 36, 64)
	if err != nil || timestamp < 0 || timestamp > 253402300799 || strconv.FormatInt(timestamp, 36) != parts[1] {
		return PasswordResetSignature{}, false
	}
	mac, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(mac) != sha256.Size || base64.RawURLEncoding.EncodeToString(mac) != parts[2] {
		return PasswordResetSignature{}, false
	}
	result := PasswordResetSignature{timestamp: timestamp, valid: true}
	copy(result.mac[:], mac)
	return result, true
}

func passwordResetMessage(observation PasswordResetState, timestamp int64) []byte {
	state := observation.state
	// This is GoDj's existing r1 protocol namespace. The credential stamp
	// separates selected model/domain generations, including the unscoped
	// default runtime. Length framing prevents ambiguous identity boundaries.
	message := []byte("godj.identity.password-reset.v1\x00")
	for _, field := range []string{strconv.FormatInt(state.id, 10), state.credential.Principal().ID(), state.credential.SessionStamp(), state.email, state.lastLogin, strconv.FormatInt(timestamp, 10)} {
		message = binary.BigEndian.AppendUint64(message, uint64(len(field)))
		message = append(message, field...)
	}
	return message
}

func passwordResetMAC(key [sha256.Size]byte, message []byte) []byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

func (keys PasswordResetKeyRing) Issue(observation PasswordResetState, timestamp int64) (PasswordResetToken, error) {
	if !keys.Valid() || timestamp < 0 || timestamp > 253402300799 {
		return PasswordResetToken{}, invalidPasswordResetConfig()
	}
	if !observation.eligible() {
		return PasswordResetToken{}, ErrInvalidResetToken
	}
	state := observation.state
	mac := passwordResetMAC(keys.state.keys[0], passwordResetMessage(observation, timestamp))
	encoded := "r1." + strconv.FormatInt(timestamp, 36) + "." + base64.RawURLEncoding.EncodeToString(mac)
	return PasswordResetToken{&passwordResetToken{state.credential.Principal().ID(), state.credential.value().username, state.email, encoded}}, nil
}

func (keys PasswordResetKeyRing) Accepts(observation PasswordResetState, token PasswordResetSignature, now int64, timeout time.Duration) bool {
	if !keys.Valid() || !token.valid || !observation.eligible() || now < 0 || now > 253402300799 || timeout < time.Second || timeout%time.Second != 0 || token.timestamp > now || now-token.timestamp > int64(timeout/time.Second) {
		return false
	}
	message := passwordResetMessage(observation, token.timestamp)
	matches := 0
	for _, key := range keys.state.keys {
		matches |= subtle.ConstantTimeCompare(token.mac[:], passwordResetMAC(key, message))
	}
	return matches == 1
}

func (state PasswordResetState) eligible() bool {
	return state.state != nil && state.state.credential.Principal().Authenticated() && state.state.credential.HasUsablePassword()
}
func invalidPasswordResetConfig() error {
	return &Error{Code: CodeInvalidConfig, Field: "password_reset", Detail: "password reset signing configuration is invalid"}
}
