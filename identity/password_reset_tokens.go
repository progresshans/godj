package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const maxPasswordResetKeys = 8

// PasswordResetKeyRing owns already-loaded reset signing keys, independently
// of session and CSRF keys. The first key issues tokens; the remaining keys
// only verify tokens during deployment rotation. Copies share immutable data.
type PasswordResetKeyRing struct{ state *passwordResetKeys }
type passwordResetKeys struct{ keys [][sha256.Size]byte }

// NewPasswordResetKeyRing copies one active and at most seven distinct fallback
// keys. Each must contain 32 bytes of host-generated cryptographic key material.
// Loading, persistence and distribution of those keys belong to the host.
func NewPasswordResetKeyRing(active []byte, fallback ...[]byte) (PasswordResetKeyRing, error) {
	if len(active) != sha256.Size || len(fallback) >= maxPasswordResetKeys {
		return PasswordResetKeyRing{}, managementError(CodeInvalidConfig, "password_reset_keys", nil)
	}
	keys := make([][sha256.Size]byte, 1, len(fallback)+1)
	copy(keys[0][:], active)
	for _, material := range fallback {
		if len(material) != sha256.Size {
			return PasswordResetKeyRing{}, managementError(CodeInvalidConfig, "password_reset_keys", nil)
		}
		var key [sha256.Size]byte
		copy(key[:], material)
		for _, previous := range keys {
			if key == previous {
				return PasswordResetKeyRing{}, managementError(CodeInvalidConfig, "password_reset_keys", nil)
			}
		}
		keys = append(keys, key)
	}
	return PasswordResetKeyRing{&passwordResetKeys{keys}}, nil
}

func (PasswordResetKeyRing) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PasswordResetKeyRing{redacted}"))
}
func (PasswordResetKeyRing) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.PasswordResetKeyRing{redacted}"`), nil
}

// PasswordResetToken is secret delivery material, not an authenticated session
// or a reusable write admission. Hosts explicitly extract Encoded only for the
// intended recipient's reset link. Diagnostics and JSON do not publish it.
type PasswordResetToken struct{ state *passwordResetToken }
type passwordResetToken struct{ principalID, encoded string }

func (token PasswordResetToken) PrincipalID() string {
	if token.state == nil {
		return ""
	}
	return token.state.principalID
}
func (token PasswordResetToken) Encoded() string {
	if token.state == nil {
		return ""
	}
	return token.state.encoded
}
func (PasswordResetToken) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PasswordResetToken{redacted}"))
}

type parsedResetToken struct {
	timestamp int64
	mac       []byte
}

func parsePasswordResetToken(encoded string) (parsedResetToken, bool) {
	// A supported timestamp needs at most nine base-36 digits; the full SHA-256
	// MAC has 43 raw URL-base64 characters. Bound parsing before any DB work.
	if len(encoded) > 56 {
		return parsedResetToken{}, false
	}
	parts := strings.Split(encoded, ".")
	if len(parts) != 3 || parts[0] != "r1" {
		return parsedResetToken{}, false
	}
	timestamp, err := strconv.ParseInt(parts[1], 36, 64)
	if err != nil || timestamp < 0 || timestamp > 253402300799 || strconv.FormatInt(timestamp, 36) != parts[1] {
		return parsedResetToken{}, false
	}
	mac, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(mac) != sha256.Size || base64.RawURLEncoding.EncodeToString(mac) != parts[2] {
		return parsedResetToken{}, false
	}
	return parsedResetToken{timestamp, mac}, true
}

func passwordResetMessage(account Account, timestamp int64) []byte {
	profile := account.Profile()
	lastLogin := ""
	if profile.LastLogin != nil {
		lastLogin = profile.LastLogin.UTC().Format(time.RFC3339Nano)
	}
	message := []byte("godj.identity.password-reset.v1\x00")
	// Length frames prevent user-controlled email or identifier boundaries from
	// changing the meaning. No password, stamp or email is included in the URL.
	for _, field := range []string{strconv.FormatInt(profile.ID, 10), profile.PrincipalID,
		account.value().credential.SessionStamp(), profile.Email, lastLogin, strconv.FormatInt(timestamp, 10)} {
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

func (keys PasswordResetKeyRing) issue(account Account, timestamp int64) PasswordResetToken {
	mac := passwordResetMAC(keys.state.keys[0], passwordResetMessage(account, timestamp))
	encoded := "r1." + strconv.FormatInt(timestamp, 36) + "." + base64.RawURLEncoding.EncodeToString(mac)
	return PasswordResetToken{&passwordResetToken{account.Profile().PrincipalID, encoded}}
}

func (keys PasswordResetKeyRing) accepts(account Account, token parsedResetToken, now int64, timeout time.Duration) bool {
	if !account.value().credential.Principal().Authenticated() || !account.HasUsablePassword() ||
		token.timestamp > now || now-token.timestamp > int64(timeout/time.Second) {
		return false
	}
	message := passwordResetMessage(account, token.timestamp)
	matches := 0
	for _, key := range keys.state.keys {
		matches |= subtle.ConstantTimeCompare(token.mac, passwordResetMAC(key, message))
	}
	return matches == 1
}
