package auth

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// LoginNormalization selects the human login form's text transformation. It
// does not change a credential authenticator's own exact/normalized lookup.
type LoginNormalization string

const (
	LoginExact         LoginNormalization = "exact"
	LoginUnicode16NFKC LoginNormalization = "unicode16_nfkc"
)

// LoginInput describes public input metadata owned by the authenticator. It
// carries neither an account value nor authority to read or change one.
type LoginInput struct {
	label         string
	maxLength     int
	normalization LoginNormalization
}

type LoginInputConfig struct {
	Label         string
	MaxLength     int
	Normalization LoginNormalization
}

func NewLoginInput(config LoginInputConfig) (LoginInput, error) {
	input := LoginInput{config.Label, config.MaxLength, config.Normalization}
	if err := input.Validate(); err != nil {
		return LoginInput{}, err
	}
	return input, nil
}

func (input LoginInput) Label() string                     { return input.label }
func (input LoginInput) MaxLength() int                    { return input.maxLength }
func (input LoginInput) Normalization() LoginNormalization { return input.normalization }

func (input LoginInput) Validate() error {
	if input.label == "" || len(input.label) > 128 || !utf8.ValidString(input.label) || strings.TrimSpace(input.label) != input.label ||
		strings.IndexFunc(input.label, unicode.IsControl) >= 0 || input.maxLength < 1 || input.maxLength > MaximumUsernameBytes ||
		input.normalization != LoginExact && input.normalization != LoginUnicode16NFKC {
		return &Error{Code: CodeInvalidConfig, Field: "login_input", Detail: "login input metadata is invalid"}
	}
	return nil
}

// LoginInputProvider returns immutable, construction-time metadata without
// I/O or password work. An authenticator that exposes an account login form
// must explicitly provide it; no default account or normalization is inferred.
type LoginInputProvider interface {
	LoginInput() (LoginInput, error)
}

// LoginInput returns only the declaration supplied at memory-authenticator
// construction. Generic verifier use without a form declaration remains valid;
// a form consumer must reject the unavailable capability at its own startup.
func (a *MemoryAuthenticator) LoginInput() (LoginInput, error) {
	if a == nil || a.state == nil || a.state.loginInput.Validate() != nil {
		return LoginInput{}, &Error{Code: CodeInvalidConfig, Field: "login_input", Detail: "memory authenticator does not declare login input"}
	}
	return a.state.loginInput, nil
}

// LoginInput keeps the stored verifier's input metadata with the same source
// that supplies its credentials. Generic credential stores may omit this
// capability when they do not offer an account login form.
func (a *StoredAuthenticator) LoginInput() (LoginInput, error) {
	if a == nil || a.state == nil || nilAuthValue(a.state.store) {
		return LoginInput{}, &Error{Code: CodeInvalidConfig, Field: "login_input", Detail: "stored login metadata is unavailable"}
	}
	provider, ok := a.state.store.(LoginInputProvider)
	if !ok || nilAuthValue(provider) {
		return LoginInput{}, &Error{Code: CodeInvalidConfig, Field: "login_input", Detail: "credential store does not declare login input"}
	}
	input, err := provider.LoginInput()
	if err == nil {
		err = input.Validate()
	}
	if err != nil {
		return LoginInput{}, &Error{Code: CodeInvalidConfig, Field: "login_input", Detail: "stored login metadata is invalid", Cause: err}
	}
	return input, nil
}
