package identityadmin

import (
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
	"strconv"
)

type accountPasswordInputs struct {
	definition               formmodel.Definition
	first, confirm, unusable string
}

func accountCreationForm(model ir.Model, definition formmodel.Definition, maximum int) (accountPasswordInputs, error) {
	names := make(map[string]bool, len(model.Fields)+len(model.ManyToMany)+3)
	for _, field := range model.Fields {
		names[field.Name] = true
	}
	for _, field := range model.ManyToMany {
		names[field.Name] = true
	}
	// An application may use the familiar command names for real profile
	// fields. Allocate deterministic, separate names without shadowing them.
	name := func(base string) string {
		value := base
		for suffix := 1; names[value]; suffix++ {
			value = base + "_command" + strconv.Itoa(suffix)
		}
		names[value] = true
		return value
	}
	result := accountPasswordInputs{definition: definition.Clone(), first: name("password1"), confirm: name("password2"), unusable: name("unusable_password")}
	first, err := forms.CharField(result.first, forms.WithLabel("Password"), forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithRequired(false))
	if err != nil {
		return accountPasswordInputs{}, err
	}
	confirm, err := forms.CharField(result.confirm, forms.WithLabel("Password confirmation"), forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithRequired(false))
	if err != nil {
		return accountPasswordInputs{}, err
	}
	unusable, err := forms.BooleanField(result.unusable, forms.WithLabel("Disable password login"))
	if err != nil {
		return accountPasswordInputs{}, err
	}
	result.definition.ExtraFields = append(result.definition.ExtraFields, first, confirm, unusable)
	result.definition.Validators = append(result.definition.Validators, forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		if disabled, _ := values.Boolean(result.unusable); disabled {
			return validation.Errors{}
		}
		var failures []validation.Violation
		for _, field := range []string{result.first, result.confirm} {
			if raw, present := values.String(field); present {
				if raw == "" {
					failures = append(failures, validation.New(validation.Field(field), "required"))
				}
				if len(raw) > maximum {
					failures = append(failures, validation.New(validation.Field(field), "max_length"))
				}
			}
		}
		a, ap := values.String(result.first)
		b, bp := values.String(result.confirm)
		if ap && bp && a != "" && b != "" && a != b {
			failures = append(failures, validation.New(validation.Field(result.confirm), "password_mismatch"))
		}
		return validation.NewErrors(failures...)
	}))
	if _, err := result.definition.Spec(model); err != nil {
		return accountPasswordInputs{}, err
	}
	return result, nil
}
