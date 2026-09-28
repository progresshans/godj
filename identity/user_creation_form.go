package identity

import (
	"context"
	"fmt"
	"strconv"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/internal/forminput"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
)

// UserCreationForm is an immutable, reusable form over the built-in User IR.
// It owns input presentation and delegates current authority and persistence
// to its Manager. It does not publish an anonymous signup route or log in the
// created account. Definitions/specifications are safe for concurrent reuse.
type UserCreationForm struct {
	manager        *Manager
	definition     formmodel.Definition
	spec           forms.Spec
	unusableChoice bool
}

func (*UserCreationForm) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.UserCreationForm{redacted}"))
}

func NewUserCreationForm(manager *Manager) (*UserCreationForm, error) {
	return newUserCreationForm(manager, false)
}

// NewAdminUserCreationForm adds an explicit optional unusable-password choice.
// Omitting that choice still requires both password fields. This form can be
// used without the Admin Site; its manager's current add/change policy remains.
func NewAdminUserCreationForm(manager *Manager) (*UserCreationForm, error) {
	return newUserCreationForm(manager, true)
}

func newUserCreationForm(manager *Manager, unusableChoice bool) (*UserCreationForm, error) {
	if manager == nil || manager.state == nil {
		return nil, managementError(CodeInvalidConfig, "user_creation_form", nil)
	}
	passwords, err := forminput.PasswordFields(!unusableChoice)
	if err != nil {
		return nil, err
	}
	validator := forminput.PasswordConfirmation()
	if unusableChoice {
		choice, err := forms.BooleanField("unusable_password", forms.WithLabel("Create without password login"))
		if err != nil {
			return nil, err
		}
		passwords = append(passwords, choice)
		validator = forminput.CreationPasswords()
	}
	definition := formmodel.Definition{Fields: []string{"username"}, Overrides: []formmodel.Override{
		formmodel.OverrideField("username", formmodel.WithLabel("Username"), formmodel.WithMaxLength(150), formmodel.WithStringNormalizer(forminput.UsernameNormalizer(150))),
	}, ExtraFields: passwords, Validators: []forms.CrossValidator{validator}}
	spec, err := definition.Spec(models.UserDescriptor{}.Metadata())
	if err != nil {
		return nil, err
	}
	return &UserCreationForm{manager: manager, definition: definition, spec: spec, unusableChoice: unusableChoice}, nil
}

// Definition returns detached projection options for another model-form
// consumer, including Admin. Spec is the same projection bound to User IR.
func (form *UserCreationForm) Definition() formmodel.Definition {
	if form == nil || form.manager == nil {
		// nil Fields means all model fields. An uninitialized identity form
		// must instead produce an explicitly invalid, empty selection.
		return formmodel.Definition{Fields: []string{}}
	}
	return form.definition.Clone()
}
func (form *UserCreationForm) Spec() forms.Spec {
	if form == nil {
		return forms.Spec{}
	}
	return form.spec
}

func creationFormField(form forms.Form, name string) *string {
	if !form.Errors().ByField(validation.Field(name)).Empty() {
		return nil
	}
	value, present := form.Cleaned().String(name)
	if !present || value == "" {
		return nil
	}
	return &value
}

// Check is the read-only post-clean phase for an already-bound form. A
// confirmed rejection is input diagnostics; read/cleanup/cancel failures are
// execution errors. Bind applies these diagnostics to its immutable result.
func (form *UserCreationForm) Check(ctx context.Context, actor auth.Principal, bound forms.Form) error {
	if form == nil || form.manager == nil || !bound.Bound() {
		return managementError(CodeInvalidInput, "user_creation_form", nil)
	}
	password := creationFormField(bound, "password2")
	if disabled, _ := bound.Cleaned().Boolean("unusable_password"); form.unusableChoice && disabled {
		password = nil
	}
	err := form.manager.CheckUserCreation(ctx, actor, creationFormField(bound, "username"), password)
	var checked validation.Errors
	if err != nil {
		var rejected bool
		checked, rejected = validation.Rejected(err)
		if !rejected {
			return err
		}
	}
	items := checked.All()
	for i, item := range items {
		if item.Field() == "password" {
			items[i] = validation.New("password2", item.Code(), item.Params()...)
		}
	}
	checked = validation.NewErrors(items...)
	if username := creationFormField(bound, "username"); username != nil && checked.ByField("username").Empty() {
		checked = validation.Join(forminput.UsernameValidator(150).ValidateField(forms.String(*username)), checked)
	}
	if !checked.Empty() {
		return validation.Reject(checked, err)
	}
	return nil
}

func (form *UserCreationForm) Bind(ctx context.Context, actor auth.Principal, data forms.Data) (forms.Form, error) {
	if form == nil || form.manager == nil {
		return forms.Form{}, managementError(CodeInvalidConfig, "user_creation_form", nil)
	}
	modelBound, err := form.definition.Bind(models.UserDescriptor{}.Metadata(), data, nil)
	if err != nil {
		return forms.Form{}, err
	}
	bound := modelBound.Form()
	if err := form.Check(ctx, actor, bound); err != nil {
		if failures, rejected := validation.Rejected(err); rejected {
			return bound.WithErrors(failures)
		}
		return forms.Form{}, err
	}
	return bound, nil
}

// Prepare accepts cleaned values from a valid bound form. It defensively
// checks their selected names, types and pure input rules again, then prepares
// the credential under current manager authority. No user is stored. The
// principal ID is host-owned and is never accepted as a submitted form field.
func (form *UserCreationForm) Prepare(ctx context.Context, actor auth.Principal, principalID string, values forms.Values) (PreparedUserCreation, error) {
	if form == nil || form.manager == nil {
		return PreparedUserCreation{}, managementError(CodeInvalidConfig, "user_creation_form", nil)
	}
	if err := form.manager.validCall(ctx, actor); err != nil {
		return PreparedUserCreation{}, err
	}
	data := make(map[string][]string)
	for _, entry := range values.All() {
		name := entry.Name()
		switch name {
		case "username", "password1", "password2":
			value, ok := entry.Value().AsString()
			if !ok {
				return PreparedUserCreation{}, managementError(CodeInvalidInput, "user_creation_form", nil)
			}
			data[name] = []string{value}
		case "unusable_password":
			value, ok := entry.Value().AsBoolean()
			if !ok || !form.unusableChoice {
				return PreparedUserCreation{}, managementError(CodeInvalidInput, "user_creation_form", nil)
			}
			data[name] = []string{strconv.FormatBool(value)}
		default:
			return PreparedUserCreation{}, managementError(CodeInvalidInput, "user_creation_form", nil)
		}
	}
	modelBound, err := form.definition.Bind(models.UserDescriptor{}.Metadata(), forms.NewData(data), nil)
	if err != nil {
		return PreparedUserCreation{}, err
	}
	bound := modelBound.Form()
	if !bound.Valid() {
		return PreparedUserCreation{}, validation.Reject(bound.Errors(), nil)
	}
	username, _ := bound.Cleaned().String("username")
	if failures := forminput.UsernameValidator(150).ValidateField(forms.String(username)); !failures.Empty() {
		return PreparedUserCreation{}, validation.Reject(failures, nil)
	}
	input := NewUserCreate(principalID, username).WithCaseInsensitiveUsernameCheck()
	disabled, _ := bound.Cleaned().Boolean("unusable_password")
	if form.unusableChoice && disabled {
		return form.manager.PrepareUserCreationWithUnusablePassword(ctx, actor, input)
	}
	password, _ := bound.Cleaned().String("password1")
	prepared, err := form.manager.PrepareUserCreation(ctx, actor, input, password)
	return prepared, userCreationPasswordError(err)
}

func userCreationPasswordError(err error) error {
	if failures, rejected := validation.Rejected(err); rejected {
		items := failures.All()
		for i, item := range items {
			if item.Field() == "password" {
				items[i] = validation.New("password2", item.Code(), item.Params()...)
			}
		}
		return validation.Reject(validation.NewErrors(items...), err)
	}
	return err
}

func (form *UserCreationForm) Commit(ctx context.Context, actor auth.Principal, prepared PreparedUserCreation) (UserDetails, error) {
	if form == nil || form.manager == nil {
		return UserDetails{}, managementError(CodeInvalidConfig, "user_creation_form", nil)
	}
	value, err := form.manager.CommitUserCreation(ctx, actor, prepared)
	return value, userCreationPasswordError(err)
}

// Create is the immediate prepare/commit convenience path. Keep PreparedUserCreation
// only when the application deliberately separates those phases.
func (form *UserCreationForm) Create(ctx context.Context, actor auth.Principal, principalID string, values forms.Values) (UserDetails, error) {
	prepared, err := form.Prepare(ctx, actor, principalID, values)
	if err != nil {
		return UserDetails{}, err
	}
	return form.Commit(ctx, actor, prepared)
}
