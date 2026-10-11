package identityaccount

import (
	"context"
	"net/url"
	"unicode/utf8"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

func (a *Application) prepareForms() error {
	usernameLimit := 0
	for _, field := range (models.UserDescriptor{}).Metadata().Fields {
		if field.Name == "username" {
			usernameLimit = field.MaxLength
		}
	}
	if usernameLimit < 1 {
		return invalidConfig("username")
	}
	username, err := forms.CharField("username", forms.WithLabel("Username"), forms.WithMaxLength(usernameLimit), forms.WithTrimWhitespace(false), forms.WithStringNormalizer(func(value string) string {
		value = unicode16.TrimSpace(value)
		if utf8.RuneCountInString(value) > usernameLimit {
			return value
		}
		return unicode16.NFKC(value)
	}))
	if err != nil {
		return err
	}
	password, err := passwordField("password", "Password")
	if err != nil {
		return err
	}
	a.login, err = forms.NewSpec([]forms.Field{username, password})
	if err != nil {
		return err
	}
	var fields []forms.Field
	for _, field := range []struct{ name, label string }{{"old_password", "Old password"}, {"new_password1", "New password"}, {"new_password2", "New password confirmation"}} {
		value, err := passwordField(field.name, field.label)
		if err != nil {
			return err
		}
		fields = append(fields, value)
	}
	a.password, err = forms.NewSpec(fields, forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		first, a := values.String("new_password1")
		second, b := values.String("new_password2")
		if a && b && first != "" && second != "" && first != second {
			return validation.NewErrors(validation.New("new_password2", "password_mismatch"))
		}
		return validation.Errors{}
	}))
	return err
}

func passwordField(name, label string) (forms.Field, error) {
	return forms.CharField(name, forms.WithLabel(label), forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithMaxLength(MaximumInputBytes))
}

func bind(ctx context.Context, spec forms.Spec, data url.Values) (forms.Form, error) {
	values := make(map[string][]string)
	for _, field := range spec.Fields() {
		if raw, ok := data[field.Name()]; ok {
			values[field.Name()] = raw
		}
	}
	return spec.Bind(ctx, forms.NewData(values), nil)
}

func checkedField(form forms.Form, name string) *string {
	if !form.Errors().ByField(validation.Field(name)).Empty() {
		return nil
	}
	value, present := form.Cleaned().String(name)
	if !present {
		return nil
	}
	return &value
}

func remapPasswordErrors(failures validation.Errors, field validation.Field) validation.Errors {
	values := make([]validation.Violation, 0, failures.Len())
	for _, item := range failures.All() {
		name := item.Field()
		if name == "password" {
			name = field
		}
		values = append(values, validation.New(name, item.Code(), item.Params()...))
	}
	return validation.NewErrors(values...)
}

// Field-order grouping matches form presentation even when storage-backed old
// password validation finishes after the local confirmation/required checks.
func orderedErrors(spec forms.Spec, groups ...validation.Errors) validation.Errors {
	joined := validation.Join(groups...)
	result := []validation.Errors{}
	for _, field := range spec.Fields() {
		result = append(result, joined.ByField(validation.Field(field.Name())))
	}
	result = append(result, joined.ByField(validation.NonField))
	return validation.Join(result...)
}
