package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"strings"
)

// MakeUnusablePassword creates a fresh encoded value that cannot authenticate
// any password. It can be stored through NewCredential just like an encoded
// hash. Every call changes the session stamp, including repeated disablement.
// The "!" prefix is reserved across all PasswordHasher implementations.
func MakeUnusablePassword(ctx context.Context) (string, error) {
	return makeUnusablePassword(ctx, rand.Reader)
}

func makeUnusablePassword(ctx context.Context, random io.Reader) (string, error) {
	if err := authContext(ctx); err != nil {
		return "", err
	}
	var entropy [32]byte
	if _, err := io.ReadFull(random, entropy[:]); err != nil {
		return "", &Error{Code: CodeEntropy, Detail: "unusable password source failed", Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "!" + base64.RawURLEncoding.EncodeToString(entropy[:]), nil
}

// HasUsablePassword reports whether this snapshot permits password checking.
// It does not validate the hash algorithm or admit the account. In particular,
// an active account without a usable password can still resolve through a
// different authentication mechanism. A zero Credential has no usable password.
func (c Credential) HasUsablePassword() bool {
	return c.state != nil && IsPasswordUsable(c.state.hash)
}

// IsPasswordUsable classifies a stored representation without verifying its
// algorithm, work profile or account state. Empty and reserved unusable values
// are false; this predicate is never an authentication or validation decision.
func IsPasswordUsable(encoded string) bool { return encoded != "" && !unusablePassword(encoded) }

func unusablePassword(encoded string) bool { return strings.HasPrefix(encoded, "!") }

// Constructors own the bounded encoding envelope. Only the deliberate reserved
// state bypasses hash parsing; corrupt/unsupported hashes remain store failures.
func validateCredentialPassword(credential Credential, hasher PasswordHasher) error {
	if unusablePassword(credential.value().hash) {
		return nil
	}
	return hasher.ValidateEncoded(credential.value().hash)
}
