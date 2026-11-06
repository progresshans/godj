package model

import (
	"context"
	"slices"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/internal/forminput"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// CreationLoginInput is an optional input policy for the selected login role.
// Its zero value uses the model's ordinary Form projection. It cannot change
// stored metadata, the declared normalization or the owner's unique policy.
type CreationLoginInput struct {
	username bool
	maximum  int
}

// UnicodeUsernameInput opts into pinned Unicode 16 username input rules on
// the actual login field, regardless of its name. It strips Python whitespace,
// bounds both raw and normalized rune counts, and accepts Unicode letters and
// numbers plus _, ., @, + and -. An already overlong input skips normalization.
// Model-only grammar errors retain the candidate for password strength checks.
//
// NewCreationForm validates maximum against the actual stored field. It must
// be positive and cannot widen a finite model limit. Use 150 for the default
// UserCreationForm input convention; storage and non-Form creation keep their
// own limits. Case-insensitive creation uniqueness remains an owner setting.
func UnicodeUsernameInput(maximum int) CreationLoginInput {
	return CreationLoginInput{username: true, maximum: maximum}
}

func (input CreationLoginInput) validate(model ir.Model, login string, fields []string) error {
	if !input.username {
		if input.maximum != 0 {
			return managementError(CodeInvalidConfig, nil)
		}
		return nil
	}
	field, found := namedField(model, login)
	if !found || !slices.Contains(fields, login) || input.maximum <= 0 || field.MaxLength > 0 && input.maximum > field.MaxLength {
		return managementError(CodeInvalidConfig, nil)
	}
	switch field.Kind {
	case ir.FieldChar, ir.FieldText, ir.FieldEmail:
		return nil
	default:
		return managementError(CodeInvalidConfig, nil)
	}
}

func (form *CreationForm[M]) loginDiagnostics(bound formmodel.BoundForm, failures validation.Errors) validation.Errors {
	if !form.loginInput.username {
		return validation.Errors{}
	}
	name := validation.Field(form.management.state.creator.state.binding.state.role.Login)
	if !bound.Form().Errors().ByField(name).Empty() || !failures.ByField(name).Empty() {
		return validation.Errors{}
	}
	value, present := bound.Candidate().Get(string(name))
	if !present {
		return validation.Errors{}
	}
	return forminput.UsernameValidator(name, form.loginInput.maximum).ValidateField(value)
}

// Preparation checks the actual candidate again, including a valid BoundForm
// made with a weaker projection or a model-clean change. No file inspection,
// database read, identity allocation or password work occurs in this check.
func (form *CreationForm[M]) prepareLogin(ctx context.Context, values forms.Values) (validation.Errors, error) {
	if !form.loginInput.username {
		return validation.Errors{}, nil
	}
	name := form.management.state.creator.state.binding.state.role.Login
	value, present := values.String(name)
	if !present {
		return validation.Errors{}, managementError(CodeInvalidInput, nil)
	}
	checked, err := form.loginSpec.Bind(ctx, forms.NewData(map[string][]string{name: {value}}), nil)
	if err != nil {
		return validation.Errors{}, err
	}
	if !checked.Valid() {
		return checked.Errors(), nil
	}
	cleaned, _ := checked.Cleaned().Get(name)
	canonical, _ := cleaned.AsString()
	if canonical != value {
		// The creator consumes the candidate, not this temporary projection.
		// A weaker form or model-clean callback must not validate a different
		// trimmed/normalized value from the one it would actually prepare.
		return validation.NewErrors(validation.New(validation.Field(name), "invalid")), nil
	}
	return forminput.UsernameValidator(validation.Field(name), form.loginInput.maximum).ValidateField(cleaned), nil
}
