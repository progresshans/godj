package identitytest

import (
	"time"

	"github.com/progresshans/godj/auth/passwordpolicy"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/validation"
)

// Shared fixed instant of the retained password/reset reference consumers.
var loginInstant = time.Date(2026, 9, 27, 1, 2, 3, 123456000, time.UTC)

// The remaining historical password/reset consumers compose the common rule
// explicitly until their transaction/session coverage moves to selected owners.
// No fixed-Profile validator factory remains in the product API.
func legacyMinimumPasswordPolicy(minimum int) (identity.PasswordValidator, error) {
	rule, err := passwordpolicy.NewMinimumLengthValidator(minimum)
	if err != nil {
		return nil, err
	}
	return identity.PasswordValidatorFunc(func(raw string, _ identity.Profile) validation.Errors {
		return rule.ValidatePassword(raw, nil)
	}), nil
}
