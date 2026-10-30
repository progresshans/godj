package model

import (
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/internal/emailinput"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// Normalize only explicitly provided values, using the same selected write
// policy for creation and profile editing. The caller owns this map.
func normalizeAccountInput(metadata ir.Model, role ir.AccountRole, values map[string]query.Value) error {
	for _, field := range metadata.Fields {
		value, present := values[field.Name]
		if !present {
			continue
		}
		if field.Name == role.Login {
			login, valid := value.String()
			if !valid || auth.ValidateUsername(login) != nil {
				return profileInputError(field.Name, "invalid")
			}
			login = normalizeAccountLogin(role, login)
			if auth.ValidateUsername(login) != nil {
				return profileInputError(field.Name, "invalid")
			}
			value = query.String(login)
			values[field.Name] = value
		}
		if field.Name == role.Email {
			email, valid := value.String()
			if !valid || len(email) > 4096 || !utf8.ValidString(email) || strings.ContainsRune(email, 0) {
				return profileInputError(field.Name, "invalid")
			}
			email = normalizeAccountEmail(role, email)
			if field.Name == role.Login && auth.ValidateUsername(email) != nil {
				return profileInputError(field.Name, "invalid")
			}
			value = query.String(email)
			values[field.Name] = value
		}
		if text, ok := value.String(); ok {
			if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
				return profileInputError(field.Name, "invalid")
			}
			if field.MaxLength > 0 && utf8.RuneCountInString(text) > field.MaxLength {
				return profileInputError(field.Name, "max_length")
			}
		}
	}
	return nil
}

func normalizeAccountLogin(role ir.AccountRole, value string) string {
	if role.WriteLoginNormalization == ir.AccountUnicode16NFKC {
		return unicode16.NFKC(value)
	}
	return value
}

func normalizeAccountEmail(role ir.AccountRole, value string) string {
	if role.WriteEmailNormalization == ir.AccountUnicode16EmailDomain {
		return emailinput.NormalizeDomain(value)
	}
	return value
}
