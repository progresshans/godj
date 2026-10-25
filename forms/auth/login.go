// Package authform composes login Forms from an authenticator's explicit,
// immutable input declaration. It does not read credentials or acquire a DB.
package authform

import (
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

// NewLoginSpec retains the shared username/password wire names. They are form
// protocol names, not lookups of model fields. The selected source supplies the
// human label, character limit and exact/NFKC form policy independently of its
// authentication lookup policy. Password bytes are preserved without trimming.
func NewLoginSpec(input auth.LoginInput, maxPasswordBytes int) (forms.Spec, error) {
	if err := input.Validate(); err != nil {
		return forms.Spec{}, err
	}
	if maxPasswordBytes < 1 || maxPasswordBytes > 1<<20 {
		return forms.Spec{}, &forms.ConfigError{Path: "login.password", Code: "invalid_limit"}
	}
	username, err := forms.CharField("username", forms.WithLabel(input.Label()), forms.WithMaxLength(input.MaxLength()), forms.WithTrimWhitespace(false), forms.WithStringNormalizer(func(value string) string {
		value = unicode16.TrimSpace(value)
		// Reject an overlong input through normal field validation rather than
		// normalizing it down into the declared limit. Bound normalization work.
		if input.Normalization() == auth.LoginExact || len(value) > auth.MaximumUsernameBytes || utf8.RuneCountInString(value) > input.MaxLength() {
			return value
		}
		return unicode16.NFKC(value)
	}), forms.WithValidators(byteLimit("username", auth.MaximumUsernameBytes)))
	if err != nil {
		return forms.Spec{}, err
	}
	password, err := forms.CharField("password", forms.WithLabel("Password"), forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithMaxLength(maxPasswordBytes), forms.WithValidators(byteLimit("password", maxPasswordBytes)))
	if err != nil {
		return forms.Spec{}, err
	}
	return forms.NewSpec([]forms.Field{username, password})
}

func byteLimit(field string, maximum int) forms.FieldValidator {
	return forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		raw, present := value.AsString()
		if present && len(raw) > maximum {
			return validation.NewErrors(validation.New(validation.Field(field), "max_length"))
		}
		return validation.Errors{}
	})
}
