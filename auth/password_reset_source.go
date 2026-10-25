package auth

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/emailinput"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

// PasswordResetAuthority identifies one in-process immutable reset owner. It
// is not a key, a durable domain or a bearer capability. The mail requester and
// session persistence must share this exact owner, not just matching paths.
type PasswordResetAuthority struct{ identity *byte }

func NewPasswordResetAuthority() PasswordResetAuthority {
	identity := byte(1)
	return PasswordResetAuthority{&identity}
}
func (authority PasswordResetAuthority) Valid() bool { return authority.identity != nil }
func (authority PasswordResetAuthority) Matches(other PasswordResetAuthority) bool {
	return authority.Valid() && authority.identity == other.identity
}
func (PasswordResetAuthority) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("auth.PasswordResetAuthority{redacted}"))
}
func (PasswordResetAuthority) MarshalJSON() ([]byte, error) {
	return []byte(`"auth.PasswordResetAuthority{redacted}"`), nil
}

// MaximumPasswordResetCandidates bounds every active database-iexact match
// before usable-password or Unicode filtering. Overflow publishes no prefix.
const MaximumPasswordResetCandidates = 256

// PasswordResetMailSource reads every active database-iexact candidate before
// usable/Unicode filtering, bounded by MaximumPasswordResetCandidates. Each
// token and its recipient must derive from one coherent snapshot. Read/scope/
// cleanup or bound failures publish no prefix. This privileged source must not
// be exposed as a public lookup. Its metadata methods are pure startup checks.
type PasswordResetMailSource interface {
	PasswordResetAuthority() PasswordResetAuthority
	PasswordResetEmailAvailable() bool
	PasswordResetCandidates(context.Context, string) ([]PasswordResetToken, error)
}

// CleanPasswordResetEmail shares native PasswordResetForm's Unicode stripping,
// required, grammar, character-length and NUL diagnostics with its consumers.
// It returns no cleaned value when any diagnostic exists. UTF-8 and a 4096-byte
// raw input bound are Go transport limits, checked before Unicode work.
func CleanPasswordResetEmail(raw string) (string, validation.Errors) {
	if !utf8.ValidString(raw) || len(raw) > 4096 {
		return "", validation.NewErrors(validation.New("email", "invalid"))
	}
	value := unicode16.TrimSpace(raw)
	if value == "" {
		return "", validation.NewErrors(validation.New("email", "required"))
	}
	if failures := emailinput.FormErrors("email", value, 254); !failures.Empty() {
		return "", failures
	}
	return value, validation.Errors{}
}
