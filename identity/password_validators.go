package identity

import (
	"github.com/progresshans/godj/auth/passwordpolicy"
	"github.com/progresshans/godj/validation"
)

// Built-in account policies select attributes from this package's Profile.
// The algorithms and immutable dictionary are shared with selected typed
// account models through auth/passwordpolicy; neither path creates user rows.
type profilePasswordValidator struct {
	validator  passwordpolicy.Validator
	attributes []string
}

func NewMinimumLengthValidator(minimum int) (PasswordValidator, error) {
	validator, err := passwordpolicy.NewMinimumLengthValidator(minimum)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, "password", err)
	}
	return profilePasswordValidator{validator: validator}, nil
}

type NumericPasswordValidator struct{}

func (NumericPasswordValidator) ValidatePassword(password string, _ Profile) validation.Errors {
	return (passwordpolicy.NumericPasswordValidator{}).ValidatePassword(password, nil)
}

// SimilarityConfig selects public string attributes of the built-in Profile.
// Nil Attributes selects username, first_name, last_name and email; an explicit
// empty slice selects none. Zero MaxSimilarity selects 0.7.
type SimilarityConfig struct {
	Attributes    []string
	MaxSimilarity float64
}

func NewUserAttributeSimilarityValidator(config SimilarityConfig) (PasswordValidator, error) {
	validator, err := passwordpolicy.NewSimilarityValidator(config.MaxSimilarity)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, "password", err)
	}
	attributes := append([]string{}, config.Attributes...)
	if config.Attributes == nil {
		attributes = []string{"username", "first_name", "last_name", "email"}
	}
	for _, name := range attributes {
		if _, label := passwordAttribute(Profile{}, name); label == "" {
			return nil, managementError(CodeInvalidConfig, "password", nil)
		}
	}
	return profilePasswordValidator{validator, attributes}, nil
}

// NewCommonPasswordValidator snapshots a supplied dictionary; nil selects the
// pinned Django list, while an explicit empty slice selects an empty list.
func NewCommonPasswordValidator(words []string) (PasswordValidator, error) {
	validator, err := passwordpolicy.NewCommonPasswordValidator(words)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, "password_dictionary", err)
	}
	return profilePasswordValidator{validator: validator}, nil
}

func (policy profilePasswordValidator) ValidatePassword(password string, profile Profile) validation.Errors {
	attributes := make([]passwordpolicy.Attribute, len(policy.attributes))
	for i, name := range policy.attributes {
		attributes[i].Value, attributes[i].Label = passwordAttribute(profile, name)
	}
	return policy.validator.ValidatePassword(password, attributes)
}
func (profilePasswordValidator) String() string   { return "identity.PasswordValidator{redacted}" }
func (profilePasswordValidator) GoString() string { return "identity.PasswordValidator{redacted}" }

func passwordAttribute(profile Profile, name string) (string, string) {
	switch name {
	case "username":
		return profile.Username, "username"
	case "first_name":
		return profile.FirstName, "first name"
	case "last_name":
		return profile.LastName, "last name"
	case "email":
		return profile.Email, "email address"
	default:
		return "", ""
	}
}

// DefaultPasswordValidators preserves the declared rule order for the built-in
// account. Selection is invocation-owned; there is no global installation.
func DefaultPasswordValidators() ([]PasswordValidator, error) {
	validators, err := passwordpolicy.Default()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, "password", err)
	}
	result := make([]PasswordValidator, len(validators))
	for i, validator := range validators {
		result[i] = profilePasswordValidator{validator: validator}
	}
	result[0] = profilePasswordValidator{validators[0], []string{"username", "first_name", "last_name", "email"}}
	return result, nil
}
