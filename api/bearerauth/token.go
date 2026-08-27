package bearerauth

import (
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
)

const tokenRedacted = "bearerauth.Token{redacted}"

// Token is one opaque Bearer credential borrowed for the duration of a
// Verifier call. Its zero value carries no credential material.
//
// The state is deliberately pointer-backed. Combined with the formatting
// methods below, this prevents structural formatting from walking into a raw
// string field. Runtime marks the borrowed value released when Verify returns.
type Token struct {
	state *tokenState
}

type tokenState struct {
	encoded  string
	released atomic.Bool
}

func newToken(encoded string) Token {
	return Token{state: &tokenState{encoded: encoded}}
}

// Encoded returns the raw credential material for verification. Callers must
// not persist, log, format, or return this value. A zero Token returns "".
func (token Token) Encoded() string {
	if token.state == nil || token.state.released.Load() {
		return ""
	}
	return token.state.encoded
}

func (token Token) release() {
	if token.state != nil {
		token.state.released.Store(true)
	}
}

func (Token) String() string   { return tokenRedacted }
func (Token) GoString() string { return tokenRedacted }

// Format fixes every fmt verb to one redacted representation. In particular,
// unsupported or structural verbs cannot fall back to tokenState formatting.
func (Token) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, tokenRedacted)
}

// MarshalJSON publishes only the fixed redacted representation.
func (Token) MarshalJSON() ([]byte, error) {
	return json.Marshal(tokenRedacted)
}
