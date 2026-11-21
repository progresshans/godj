package systemstate

import (
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func identityInputError(field validation.Field, code validation.Code) error {
	return validation.Reject(validation.NewErrors(validation.New(field, code)), nil)
}

// normalizeIdentityUsername applies the pinned Unicode profile and the User's Schema
// IR storage limit. It does not strip input or select a case-insensitive policy.
// Historical provisioning uses this policy before explicit account handoff.
// Selected runtime writes use their declared binding policy instead.
func normalizeIdentityUsername(value string) (string, error) {
	// Bound work before normalization as well as the final credential envelope.
	if auth.ValidateUsername(value) != nil {
		return "", identityInputError("username", "invalid")
	}
	value = unicode16.NFKC(value)
	if auth.ValidateUsername(value) != nil {
		return "", identityInputError("username", "invalid")
	}
	if err := validateIdentityTexts(identityText{"username", value}); err != nil {
		return "", err
	}
	return value, nil
}

// Model text bounds are read from the generated projection of Schema IR.
// The credential's byte envelope must not silently replace character limits.
type identityText struct{ name, value string }

func validateIdentityTexts(values ...identityText) error {
	metadata := models.UserDescriptor{}.Metadata()
	for _, item := range values {
		if !utf8.ValidString(item.value) || strings.ContainsRune(item.value, 0) {
			return identityInputError(validation.Field(item.name), "invalid")
		}
		found := false
		for _, field := range metadata.Fields {
			if field.Name != item.name {
				continue
			}
			found = true
			if (field.Kind != ir.FieldChar && field.Kind != ir.FieldEmail) || field.MaxLength <= 0 {
				return &Error{Code: CodeInvalidConfig, Field: "user", Detail: "historical identity text metadata is invalid"}
			}
			if utf8.RuneCountInString(item.value) > field.MaxLength {
				return identityInputError(validation.Field(item.name), "invalid")
			}
			break
		}
		if !found {
			return &Error{Code: CodeInvalidConfig, Field: "user", Detail: "historical identity text metadata is invalid"}
		}
	}
	return nil
}

func validateUserProfileText(row models.User) error {
	return validateIdentityTexts(identityText{"username", row.Username}, identityText{"first_name", row.FirstName}, identityText{"last_name", row.LastName}, identityText{"email", row.Email})
}
