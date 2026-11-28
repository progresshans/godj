package model

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/internal/forminput"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// CreationFormConfig selects command inputs, never additional model exposure
// or authority. By default both password inputs are required.
type CreationFormConfig struct {
	AllowUnusablePassword bool
	LoginInput            CreationLoginInput
}

// CreationPasswordFields names private command inputs. Names are chosen around
// every stored field, including fields excluded from the form. Unusable is
// empty when the form does not offer that explicit choice.
type CreationPasswordFields struct {
	First, Confirmation, Unusable string
}

// CreationForm shares the selected model's structural, advisory and creation
// policies between direct callers and Admin. It is immutable and reusable;
// neither a bound form nor its diagnostic result grants authority to save.
// It adds no staff requirement, anonymous signup route or automatic login.
type CreationForm[M any] struct {
	management *AccountManagement[M]
	inputs     creationPasswordInputs
	spec       forms.Spec
	fields     []string
	loginInput CreationLoginInput
	loginSpec  forms.Spec
}

type creationPasswordInputs struct {
	definition formmodel.Definition
	names      CreationPasswordFields
	spec       forms.Spec
}

func (*CreationForm[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.CreationForm{redacted}"))
}

// NewCreationForm performs pure construction from the owner's exact selection,
// normalization, file policy and credential limits. It reads no stored state.
func NewCreationForm[M any](management *AccountManagement[M], config CreationFormConfig) (*CreationForm[M], error) {
	if management == nil || management.state == nil {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	creator := management.state.creator
	definition, err := creator.state.binding.formDefinition(creator.state.fields, profileFileOverrides(creator.state.files), config.LoginInput)
	if err != nil {
		return nil, err
	}
	inputs, err := newCreationPasswordInputs(creator.state.metadata, definition, creator.state.config.MaxPasswordBytes, config.AllowUnusablePassword)
	if err != nil {
		return nil, err
	}
	spec, err := inputs.definition.Spec(creator.state.metadata)
	if err != nil {
		return nil, err
	}
	form := &CreationForm[M]{management: management, inputs: inputs, spec: spec, loginInput: config.LoginInput}
	for _, field := range spec.Fields() {
		form.fields = append(form.fields, field.Name())
		if config.LoginInput.username && field.Name() == creator.state.binding.state.role.Login {
			form.loginSpec, err = forms.NewSpec([]forms.Field{field})
			if err != nil {
				return nil, err
			}
		}
	}
	return form, nil
}

func newCreationPasswordInputs(model ir.Model, definition formmodel.Definition, maximum int, allowUnusable bool) (creationPasswordInputs, error) {
	name := forminput.AuxiliaryNames(model)
	inputs := creationPasswordInputs{definition: definition.Clone(), names: CreationPasswordFields{First: name("password1", "_command"), Confirmation: name("password2", "_command")}}
	var fields []forms.Field
	for _, input := range []struct{ name, label string }{{inputs.names.First, "Password"}, {inputs.names.Confirmation, "Password confirmation"}} {
		field, err := forms.CharField(input.name, forms.WithLabel(input.label), forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithRequired(!allowUnusable))
		if err != nil {
			return creationPasswordInputs{}, err
		}
		fields = append(fields, field)
	}
	if allowUnusable {
		inputs.names.Unusable = name("unusable_password", "_command")
		field, err := forms.BooleanField(inputs.names.Unusable, forms.WithLabel("Disable password login"))
		if err != nil {
			return creationPasswordInputs{}, err
		}
		fields = append(fields, field)
	}
	names := inputs.names
	validate := forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		if disabled, _ := values.Boolean(names.Unusable); names.Unusable != "" && disabled {
			return validation.Errors{}
		}
		var failures []validation.Violation
		for _, name := range []string{names.First, names.Confirmation} {
			if raw, present := values.String(name); present {
				if raw == "" {
					failures = append(failures, validation.New(validation.Field(name), "required"))
				}
				if len(raw) > maximum {
					failures = append(failures, validation.New(validation.Field(name), "max_length"))
				}
			}
		}
		a, ap := values.String(names.First)
		b, bp := values.String(names.Confirmation)
		if ap && bp && a != "" && b != "" && a != b {
			failures = append(failures, validation.New(validation.Field(names.Confirmation), "password_mismatch"))
		}
		return validation.NewErrors(failures...)
	})
	var err error
	inputs.spec, err = forms.NewSpec(fields, validate)
	if err != nil {
		return creationPasswordInputs{}, err
	}
	inputs.definition.ExtraFields = append(inputs.definition.ExtraFields, fields...)
	inputs.definition.Validators = append(inputs.definition.Validators, validate)
	if _, err := inputs.definition.Spec(model); err != nil {
		return creationPasswordInputs{}, err
	}
	return inputs, nil
}

func (form *CreationForm[M]) Definition() formmodel.Definition {
	if form == nil || form.management == nil {
		return formmodel.Definition{Fields: []string{}}
	}
	return form.inputs.definition.Clone()
}

func (form *CreationForm[M]) Spec() forms.Spec {
	if form == nil {
		return forms.Spec{}
	}
	return form.spec
}

func (form *CreationForm[M]) PasswordFields() CreationPasswordFields {
	if form == nil {
		return CreationPasswordFields{}
	}
	return form.inputs.names
}

func (form *CreationForm[M]) validCall(ctx context.Context, actor auth.Principal) error {
	if form == nil || form.management == nil {
		return managementError(CodeInvalidConfig, nil)
	}
	return form.management.validCall(ctx, actor)
}

func (form *CreationForm[M]) validBound(bound formmodel.BoundForm) error {
	if !bound.Form().Bound() || bound.Form().ReadOnly() || !bound.Model().Equal(form.management.state.creator.state.metadata) || !slices.Equal(bound.FieldNames(), form.fields) {
		return managementError(CodeInvalidInput, nil)
	}
	return nil
}

// Check is the read-only post-clean phase for this exact model and selection.
// It keeps model diagnostics separate from the private credential namespace.
// Cleanup, cancellation and execution failures publish no input diagnostics.
func (form *CreationForm[M]) Check(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
	if err := form.validCall(ctx, actor); err != nil {
		return err
	}
	if err := form.validBound(bound); err != nil {
		return err
	}
	names := form.inputs.names
	var password *string
	cleaned := bound.Form().Cleaned()
	disabled, _ := cleaned.Boolean(names.Unusable)
	if (names.Unusable == "" || !disabled) && bound.Form().Errors().ByField(validation.Field(names.Confirmation)).Empty() {
		if raw, present := cleaned.String(names.Confirmation); present && raw != "" {
			password = &raw
		}
	}
	diagnostics, err := form.management.CheckCreateForm(ctx, actor, bound, password)
	if err != nil {
		return err
	}
	return validation.Reject(validation.Join(diagnostics.Fields, form.loginDiagnostics(bound, diagnostics.Fields), form.passwordDiagnostics(diagnostics.Password)), nil)
}

// Bind admits current creation authority before file inspection, then binds
// pure input and checks all present values in a fresh authorized snapshot.
// Numeric relation labels are submitted keys only; current target existence,
// filters and limits come from the management owner, never these placeholders.
// Admin can supply its own authorized labels through Definition and Check.
func (form *CreationForm[M]) Bind(ctx context.Context, actor auth.Principal, data forms.Data) (formmodel.BoundForm, error) {
	if err := form.validCall(ctx, actor); err != nil {
		return formmodel.BoundForm{}, err
	}
	state := form.management.state.creator.state
	_, err := managementSnapshot(ctx, state.store.state.backend, func(work context.Context, scope db.Queryer) (auth.Principal, bool, error) {
		return state.store.AuthorizeIn(work, scope, actor, state.overlay, state.required[0], state.required[1:]...)
	}, func(context.Context, db.Queryer, auth.CredentialDomain) (struct{}, bool, error) {
		return struct{}{}, true, nil
	})
	if err != nil {
		return formmodel.BoundForm{}, err
	}
	spec := form.spec
	input, err := formmodel.PrepareChoiceInput(state.metadata, spec, data, nil)
	if err != nil {
		return formmodel.BoundForm{}, err
	}
	limits := profileCollectionLimits(state.collections)
	role := state.binding.state.role
	limits[role.Groups], limits[role.Permissions] = MaximumAccountGroups, auth.MaximumPermissions
	for _, field := range spec.Fields() {
		if !field.ModelChoice() {
			continue
		}
		if raw, _ := data.Get(field.Name()); limits[field.Name()] > 0 && len(raw) > limits[field.Name()] {
			continue // The owner reports max_items without resolving this input.
		}
		keys, valid, err := input.ChoiceKeys(field.Name())
		if err != nil {
			return formmodel.BoundForm{}, err
		}
		if !valid {
			continue
		}
		choices := make([]forms.Choice, len(keys))
		for index, key := range keys {
			choices[index] = forms.Choice{Value: forms.Integer(key), Label: strconv.FormatInt(key, 10)}
		}
		spec, err = spec.WithModelChoices(field.Name(), choices...)
		if err != nil {
			return formmodel.BoundForm{}, err
		}
	}
	instance, err := formmodel.BindInstance(ctx, state.binding.state.objects, spec, data, nil, form.inputs.definition.PostClean)
	if err != nil {
		return formmodel.BoundForm{}, err
	}
	bound := instance.BoundForm()
	if err := form.Check(ctx, actor, bound); err != nil {
		if failures, rejected := validation.Rejected(err); rejected {
			return bound.WithErrors(failures)
		}
		return formmodel.BoundForm{}, err
	}
	return bound, nil
}

// Prepare defensively validates the private command inputs again, then uses
// the creator's publication and preparation path. Preserve every returned file
// record, including on failure or when the prepared account is abandoned.
func (form *CreationForm[M]) Prepare(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) (PreparedCreation[M], []formmodel.FilePublication, error) {
	if err := form.validCall(ctx, actor); err != nil {
		return PreparedCreation[M]{}, nil, err
	}
	if err := form.validBound(bound); err != nil {
		return PreparedCreation[M]{}, nil, err
	}
	if !bound.Form().Valid() {
		return PreparedCreation[M]{}, nil, validation.Reject(bound.Form().Errors(), nil)
	}
	values, err := bound.Input()
	if err != nil {
		return PreparedCreation[M]{}, nil, err
	}
	loginFailures, err := form.prepareLogin(ctx, values)
	if err != nil {
		return PreparedCreation[M]{}, nil, err
	}
	names := form.inputs.names
	data := make(map[string][]string, 3)
	for _, name := range []string{names.First, names.Confirmation} {
		raw, present := values.String(name)
		if !present {
			return PreparedCreation[M]{}, nil, managementError(CodeInvalidInput, nil)
		}
		data[name] = []string{raw}
	}
	if names.Unusable != "" {
		disabled, present := values.Boolean(names.Unusable)
		if !present {
			return PreparedCreation[M]{}, nil, managementError(CodeInvalidInput, nil)
		}
		data[names.Unusable] = []string{strconv.FormatBool(disabled)}
	}
	passwords, err := form.inputs.spec.Bind(ctx, forms.NewData(data), nil)
	if err != nil {
		return PreparedCreation[M]{}, nil, err
	}
	if !passwords.Valid() || !loginFailures.Empty() {
		return PreparedCreation[M]{}, nil, validation.Reject(validation.Join(loginFailures, passwords.Errors()), nil)
	}
	creator := form.management.state.creator
	if disabled, _ := passwords.Cleaned().Boolean(names.Unusable); names.Unusable != "" && disabled {
		return creator.PrepareUnusableForm(ctx, actor, bound)
	}
	raw, _ := passwords.Cleaned().String(names.First)
	prepared, publications, err := creator.PrepareForm(ctx, actor, bound, raw)
	return prepared, publications, form.passwordError(err)
}

// Commit uses the same creator/actor/domain and single-attempt contract as
// Creator.Commit. It never reopens inputs, republishes files or hashes again.
func (form *CreationForm[M]) Commit(ctx context.Context, actor auth.Principal, prepared PreparedCreation[M]) (Creation, error) {
	if err := form.validCall(ctx, actor); err != nil {
		return Creation{}, err
	}
	result, err := form.management.state.creator.Commit(ctx, actor, prepared)
	return result, form.passwordError(err)
}

func (form *CreationForm[M]) Create(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) (Creation, []formmodel.FilePublication, error) {
	prepared, publications, err := form.Prepare(ctx, actor, bound)
	if err != nil {
		return Creation{}, publications, err
	}
	result, err := form.Commit(ctx, actor, prepared)
	return result, publications, err
}

func (form *CreationForm[M]) passwordDiagnostics(diagnostics validation.Errors) validation.Errors {
	items := diagnostics.All()
	for index, item := range items {
		if item.Field() == "password" {
			items[index] = validation.New(validation.Field(form.inputs.names.Confirmation), item.Code(), item.Params()...)
		}
	}
	return validation.NewErrors(items...)
}

func (form *CreationForm[M]) passwordError(err error) error {
	if diagnostics, credential := PasswordInputDiagnostics(err); credential {
		return validation.Reject(form.passwordDiagnostics(diagnostics), err)
	}
	return err
}
