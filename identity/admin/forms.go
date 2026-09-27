package identityadmin

import (
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

func trimPythonSpace(value string) string { return unicode16.TrimSpace(value) }

func emailValidator() forms.FieldValidator {
	return forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		text, ok := value.AsString()
		if !ok || text == "" || validation.ValidEmail(text) {
			return validation.Errors{}
		}
		return validation.NewErrors(validation.New("email", "invalid"))
	})
}
