// Package forminput owns the pure input rules shared by identity forms and Admin.
package forminput

import (
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
	"strconv"
	"strings"
	"unicode/utf8"
)

// AuxiliaryNames allocates command/presentation names around every real model
// field. The local allocator also reserves each name it returns.
func AuxiliaryNames(model ir.Model) func(string, string) string {
	names := make(map[string]bool, len(model.Fields)+len(model.ManyToMany))
	for _, field := range model.Fields {
		names[field.Name] = true
	}
	for _, field := range model.ManyToMany {
		names[field.Name] = true
	}
	return func(base, suffix string) string {
		value := base
		for index := 1; names[value]; index++ {
			value = base + suffix + strconv.Itoa(index)
		}
		names[value] = true
		return value
	}
}

func PasswordFields(required bool) ([]forms.Field, error) {
	first, err := forms.CharField("password1", forms.WithLabel("Password"), forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithRequired(required))
	if err != nil {
		return nil, err
	}
	second, err := forms.CharField("password2", forms.WithLabel("Password confirmation"), forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithRequired(required))
	if err != nil {
		return nil, err
	}
	return []forms.Field{first, second}, nil
}

func PasswordConfirmation() forms.CrossValidator {
	return forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		first, a := values.String("password1")
		second, b := values.String("password2")
		if a && b && first != "" && second != "" && first != second {
			return validation.NewErrors(validation.New("password2", "password_mismatch"))
		}
		return validation.Errors{}
	})
}

// The creation choice owns whether password fields are required. A deliberate
// unusable selection ignores their contents, matching AdminUserCreationForm;
// transport/resource bounds and private rendering still apply to both fields.
func CreationPasswords() forms.CrossValidator {
	return forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		unusable, _ := values.Boolean("unusable_password")
		if unusable {
			return validation.NewErrors()
		}
		var required []validation.Violation
		for _, name := range []string{"password1", "password2"} {
			value, present := values.String(name)
			if present && value == "" {
				required = append(required, validation.New(validation.Field(name), "required"))
			}
		}
		return validation.Join(validation.NewErrors(required...), PasswordConfirmation().ValidateForm(values))
	})
}

// UsernameNormalizer strips first and avoids expensive normalization
// of an already overlong input. Length validation still observes the result.
func UsernameNormalizer(limit int, normalize func(string) string) func(string) string {
	return func(value string) string {
		value = unicode16.TrimSpace(value)
		if utf8.RuneCountInString(value) > limit {
			return value
		}
		return normalize(value)
	}
}

func UsernameValidator(field validation.Field, limit int) forms.FieldValidator {
	return forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		text, ok := value.AsString()
		if !ok || text == "" {
			return validation.Errors{}
		}
		// Django's model username validator runs only after the form field
		// succeeds. Preserve that boundary instead of adding a second error
		// to a rejected length, NUL or malformed encoding.
		if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || utf8.RuneCountInString(text) > limit {
			return validation.Errors{}
		}
		valid := true
		for _, r := range text {
			valid = valid && (unicode16.IsAlphanumeric(r) || strings.ContainsRune("_.@+-", r))
		}
		if !valid {
			return validation.NewErrors(validation.New(field, "invalid"))
		}
		return validation.Errors{}
	})
}
