package identityadmin

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

func creationField(form forms.Form, name string) *string {
	if !form.Errors().ByField(validation.Field(name)).Empty() {
		return nil
	}
	value, present := form.Cleaned().String(name)
	if !present || value == "" {
		return nil
	}
	return &value
}

// The model's username grammar runs after case-insensitive uniqueness and
// after the candidate profile has been supplied to password policy. A grammar
// error must not erase that profile; an earlier field/duplicate error must.
func creationUsernameErrors(form forms.Form, checked validation.Errors) validation.Errors {
	username := creationField(form, "username")
	if username == nil || !checked.ByField("username").Empty() {
		return validation.Errors{}
	}
	return usernameValidator(150).ValidateField(forms.String(*username))
}

func (a *registration) validateUserCreation(ctx context.Context, actor auth.Principal, form forms.Form) error {
	password := creationField(form, "password2")
	if unusable, _ := form.Cleaned().Boolean("unusable_password"); unusable {
		password = nil
	}
	err := passwordError(a.manager.CheckUserCreation(ctx, actor, creationField(form, "username"), password))
	var checked validation.Errors
	if err != nil {
		var rejected bool
		checked, rejected = validation.Rejected(err)
		if !rejected {
			return err
		}
	}
	checked = validation.Join(creationUsernameErrors(form, checked), checked)
	if checked.Empty() {
		return nil
	}
	return validation.Reject(checked, err)
}

func passwordFields(required bool) ([]forms.Field, error) {
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

func passwordConfirmation() forms.CrossValidator {
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
func creationPasswords() forms.CrossValidator {
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
		return validation.Join(validation.NewErrors(required...), passwordConfirmation().ValidateForm(values))
	})
}

func trimPythonSpace(value string) string {
	return unicode16.TrimSpace(value)
}

// UsernameField normalizes after stripping and avoids expensive normalization
// of an already overlong input. Length validation still observes the result.
func usernameNormalizer(limit int) func(string) string {
	return func(value string) string {
		value = trimPythonSpace(value)
		if utf8.RuneCountInString(value) > limit {
			return value
		}
		return unicode16.NFKC(value)
	}
}

func usernameValidator(limit int) forms.FieldValidator {
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
			return validation.NewErrors(validation.New("username", "invalid"))
		}
		return validation.Errors{}
	})
}

func emailValidator() forms.FieldValidator {
	return forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		text, ok := value.AsString()
		if !ok || text == "" || validation.ValidEmail(text) {
			return validation.Errors{}
		}
		return validation.NewErrors(validation.New("email", "invalid"))
	})
}
