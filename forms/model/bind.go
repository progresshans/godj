package model

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// Validator checks the model candidate after selected model fields have been
// validated. Unlike a form cross-validator, it also sees default/initial values
// of fields whose input failed cleaning or was excluded from the form. It must
// be pure and safe for concurrent use; database checks remain explicit.
type Validator interface {
	ValidateModel(forms.Values) validation.Errors
}

type ValidatorFunc func(forms.Values) validation.Errors

func (f ValidatorFunc) ValidateModel(values forms.Values) validation.Errors { return f(values) }

// BoundForm keeps form cleaning separate from the model candidate. Neither
// successful validation nor a candidate grants permission to persist it.
// Database checks and the final authorized write remain explicit operations.
type BoundForm struct {
	form      forms.Form
	candidate forms.Values
	model     ir.Model
	fields    []forms.Field
	changed   []string
	parent    string
}

func (BoundForm) Format(state fmt.State, _ rune) { fmt.Fprint(state, "model.BoundForm{redacted}") }
func (bound BoundForm) Form() forms.Form         { return bound.form }
func (bound BoundForm) Candidate() forms.Values  { return bound.candidate }

// Model returns a detached copy of the model policy used to build the form.
// A read-check adapter can reject a form belonging to a different model before
// entering its database scope. This metadata is not persistence authority.
func (bound BoundForm) Model() ir.Model { return bound.model.Clone() }

// Excluded returns model fields in declaration order that must not participate
// in field/uniqueness/constraint validation. A constraint with any excluded
// member is excluded as a whole. Collection fields are not scalar model fields.
func (bound BoundForm) Excluded() []string {
	inputs := make(map[string]forms.Field, len(bound.fields))
	for _, field := range bound.fields {
		inputs[field.Name()] = field
	}
	var excluded []string
	for _, field := range bound.model.Fields {
		input, selected := inputs[field.Name]
		value, present := bound.form.Cleaned().Get(field.Name)
		if field.PrimaryKey || !selected || !present || !bound.form.Errors().ByField(validation.Field(field.Name)).Empty() ||
			!field.Blank && !input.Required() && emptyInput(value) {
			excluded = append(excluded, field.Name)
		}
	}
	return excluded
}

// Input returns selected stored values, image-derived dimensions, explicitly
// declared model-clean changes and command inputs after every check succeeded. Omitted defaults come
// from the candidate; command inputs continue to come from the cleaned data.
func (bound BoundForm) Input() (forms.Values, error) {
	if bound.form.ReadOnly() {
		return forms.Values{}, &Error{Path: "form", Code: "read_only"}
	}
	if !bound.form.Bound() || !bound.form.Valid() {
		return forms.Values{}, &Error{Path: "form", Code: "not_bound_valid"}
	}
	if bound.parent != "" {
		value, present := bound.candidate.Get(bound.parent)
		if !present || value.IsNull() {
			return forms.Values{}, &Error{Path: "parent." + bound.parent, Code: "unsaved_parent"}
		}
	}
	input := make(map[string]forms.Value, len(bound.fields))
	for _, field := range bound.fields {
		value, found := bound.candidate.Get(field.Name())
		if !found {
			value, found = bound.form.Cleaned().Get(field.Name())
		}
		if !found {
			return forms.Values{}, &Error{Path: "form." + field.Name(), Code: "missing_value"}
		}
		// Pending model uploads must retain their capability, independently of
		// the filename-only candidate used by model/uniqueness validation.
		if field.IsFile() {
			if cleaned, present := bound.form.Cleaned().Get(field.Name()); present && !cleaned.IsNull() {
				value = cleaned
			}
		}
		input[field.Name()] = value
	}
	for _, name := range bound.changed {
		value, _ := bound.candidate.Get(name)
		input[name] = value
	}
	// A valid form can contain a model-clean NULL after field validation. Do
	// not let a typed adapter turn that NULL into an apparently valid Go zero
	// value. Keep the candidate/cleaned data intact; this is a preparation error,
	// not a second round of field validation or permission to perform a write.
	for _, field := range bound.model.Fields {
		if value, present := input[field.Name]; present && value.IsNull() && !field.Nullable {
			return forms.Values{}, &Error{Path: "input." + field.Name, Code: "nonnullable"}
		}
	}
	return forms.NewValues(input), nil
}

// WithErrors retains the candidate for later model checks, while removing
// rejected form values and recomputing the exclusions of subsequent checks.
func (bound BoundForm) WithErrors(failures validation.Errors, rejectedFields ...string) (BoundForm, error) {
	form, err := bound.form.WithErrors(failures, rejectedFields...)
	if err != nil {
		return BoundForm{}, err
	}
	bound.form = form
	return bound, nil
}

// Bind constructs an immutable model candidate, validates selected model
// fields, then runs pure model clean and validators even if fields failed.
// Initial may contain explicitly supplied model values outside the form; such
// values enter Input only through image dimension derivation or explicitly
// declared PostClean changes. Omitted initial model values use model defaults
// or the unsaved empty state.
// No relation existence, uniqueness or persistence I/O is performed here.
func Bind(ctx context.Context, model ir.Model, spec forms.Spec, data forms.Data, initial map[string]forms.Value, postClean PostClean) (BoundForm, error) {
	binding, formInitial, err := prepareModelBinding(model, spec.Fields(), initial, postClean)
	if err != nil {
		return BoundForm{}, err
	}
	form, err := spec.Bind(ctx, data, formInitial)
	if err != nil {
		return BoundForm{}, err
	}
	return binding.finish(form)
}

type modelBinding struct {
	model     ir.Model
	fields    []forms.Field
	candidate map[string]forms.Value
	byName    map[string]ir.Field
	many      map[string]bool
	postClean PostClean
}

func prepareModelBinding(model ir.Model, fields []forms.Field, initial map[string]forms.Value, postClean PostClean) (modelBinding, map[string]forms.Value, error) {
	postClean = postClean.Clone()
	if err := postClean.validate(model); err != nil {
		return modelBinding{}, nil, err
	}
	dimensions, err := ir.ImageDimensionOwners(model)
	if err != nil {
		return modelBinding{}, nil, &Error{Path: "model", Code: "invalid_image_dimensions"}
	}
	byName := make(map[string]ir.Field, len(model.Fields))
	candidate := make(map[string]forms.Value, len(model.Fields)+len(model.ManyToMany))
	for _, field := range model.Fields {
		if _, duplicate := byName[field.Name]; duplicate {
			return modelBinding{}, nil, &Error{Path: "model." + field.Name, Code: "duplicate"}
		}
		byName[field.Name] = field
		value := forms.Null()
		if field.Default != nil {
			projected, err := projectField(field, overrideConfig{hasRequired: true, required: true})
			if err != nil {
				return modelBinding{}, nil, err
			}
			var present bool
			value, present = projected.Default()
			if !present {
				return modelBinding{}, nil, &Error{Path: "model." + field.Name, Code: "missing_default"}
			}
		} else if !field.Nullable && (field.Kind == ir.FieldChar || field.Kind == ir.FieldEmail || field.Kind.IsFile() || field.Kind == ir.FieldText) {
			value = forms.String("")
		}
		if field.Kind.IsFile() {
			var err error
			value, err = fileReferenceValue(value)
			if err != nil {
				return modelBinding{}, nil, err
			}
		}
		candidate[field.Name] = value
	}
	many := make(map[string]bool, len(model.ManyToMany))
	for _, field := range model.ManyToMany {
		if _, duplicate := byName[field.Name]; duplicate || many[field.Name] {
			return modelBinding{}, nil, &Error{Path: "model." + field.Name, Code: "duplicate"}
		}
		many[field.Name] = true
	}
	selected := make(map[string]forms.Field, len(fields))
	for _, field := range fields {
		selected[field.Name()] = field
		if metadata, stored := byName[field.Name()]; stored {
			if metadata.PrimaryKey || dimensions[field.Name()] != "" {
				return modelBinding{}, nil, &Error{Path: "fields." + field.Name(), Code: "non_editable"}
			}
			if !inputKindMatches(metadata.Kind, field.Kind()) {
				return modelBinding{}, nil, &Error{Path: "fields." + field.Name(), Code: "type_mismatch"}
			}
		} else if many[field.Name()] && field.Kind() != forms.FieldIntegerList {
			return modelBinding{}, nil, &Error{Path: "fields." + field.Name(), Code: "type_mismatch"}
		}
	}
	formInitial := make(map[string]forms.Value, len(fields))
	for name, input := range selected {
		if metadata, found := byName[name]; found && metadata.Kind.IsFile() && input.IsFile() {
			value, err := fileInitialValue(candidate[name])
			if err != nil {
				return modelBinding{}, nil, err
			}
			formInitial[name] = value
		}
	}
	for name, value := range initial {
		_, scalar := byName[name]
		_, input := selected[name]
		if scalar && byName[name].Kind.IsFile() {
			var err error
			value, err = fileReferenceValue(value)
			if err != nil {
				return modelBinding{}, nil, err
			}
		}
		if !scalar && !many[name] && !input {
			return modelBinding{}, nil, &Error{Path: "initial." + name, Code: "unknown_field"}
		}
		if scalar && !initialModelValueMatches(byName[name], value) {
			return modelBinding{}, nil, &Error{Path: "initial." + name, Code: "type_or_constraint_mismatch"}
		}
		if many[name] {
			if _, ok := value.AsIntegers(); !ok {
				return modelBinding{}, nil, &Error{Path: "initial." + name, Code: "type_mismatch"}
			}
		}
		if scalar || many[name] {
			candidate[name] = value
		}
		if input {
			if scalar && byName[name].Kind.IsFile() {
				var err error
				value, err = fileInitialValue(value)
				if err != nil {
					return modelBinding{}, nil, err
				}
			}
			formInitial[name] = value
		}
	}
	return modelBinding{model: model.Clone(), fields: fields, candidate: candidate, byName: byName, many: many, postClean: postClean}, formInitial, nil
}

// finish attaches model semantics to an already-cleaned form. Both single forms
// and formsets use this path; it never serializes or binds cleaned values again.
func (binding modelBinding) finish(form forms.Form) (BoundForm, error) {
	model, fields, candidate := binding.model, binding.fields, binding.candidate
	byName, many := binding.byName, binding.many
	data := form.Submitted()
	var changed []string
	for _, field := range fields {
		value, present := form.Cleaned().Get(field.Name())
		if !present || !form.Errors().ByField(validation.Field(field.Name())).Empty() {
			continue
		}
		metadata, scalar := byName[field.Name()]
		if !scalar && !many[field.Name()] {
			continue
		}
		if scalar && metadata.Kind.IsFile() {
			// Missing file input leaves the stored/default reference unchanged.
			// Clear is a present FileValue with an empty name, including null=True.
			if value.IsNull() {
				continue
			}
			file, ok := value.AsFile()
			if !ok {
				return BoundForm{}, &Error{Path: "form." + field.Name(), Code: "type_mismatch"}
			}
			candidate[field.Name()] = forms.String(file.Name())
			if metadata.Kind == ir.FieldImage {
				info, verified := file.Image()
				if _, uploaded := file.Upload(); uploaded && !verified {
					return BoundForm{}, &Error{Path: "form." + field.Name(), Code: "unverified_image"}
				}
				if file.Clear() || verified {
					for _, dimension := range []struct {
						name  string
						value int
					}{{metadata.WidthField, info.Width()}, {metadata.HeightField, info.Height()}} {
						if dimension.name == "" {
							continue
						}
						value := forms.Null()
						if verified {
							value = forms.Integer(int64(dimension.value))
						}
						candidate[dimension.name] = value
						changed = append(changed, dimension.name)
					}
				}
			}

			continue
		}
		_, submitted := data.Get(field.Name())
		// Checkbox/select-multiple omission is an actual false/empty value.
		// Other widgets may leave an existing/default value unchanged.
		if scalar && metadata.Default != nil && !submitted && field.Widget() != forms.Checkbox && field.Widget() != forms.SelectMultiple && emptyInput(value) {
			continue
		}
		candidate[field.Name()] = value
	}
	bound := BoundForm{form: form, candidate: forms.NewValues(candidate), model: model.Clone(), fields: fields, changed: changed}
	excluded := make(map[string]bool)
	for _, name := range bound.Excluded() {
		excluded[name] = true
	}
	var failures []validation.Errors
	for _, field := range model.Fields {
		if excluded[field.Name] {
			continue
		}
		failures = append(failures, modelFieldErrors(field, candidate[field.Name]))
	}
	bound, err := bound.WithErrors(validation.Join(failures...))
	if err != nil {
		return BoundForm{}, err
	}
	return binding.postClean.apply(bound)
}

func (definition Definition) Bind(ctx context.Context, model ir.Model, data forms.Data, initial map[string]forms.Value) (BoundForm, error) {
	spec, err := definition.Spec(model)
	if err != nil {
		return BoundForm{}, err
	}
	return Bind(ctx, model, spec, data, initial, definition.PostClean)
}

func nilValidator(value Validator) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func inputKindMatches(model ir.FieldKind, input forms.FieldKind) bool {
	switch model {
	case ir.FieldFile:
		return input == forms.FieldFile
	case ir.FieldImage:
		return input == forms.FieldImage
	case ir.FieldChar, ir.FieldEmail, ir.FieldText:
		return input == forms.FieldChar || input == forms.FieldEmail
	case ir.FieldAuto, ir.FieldInteger, ir.FieldForeignKey:
		return input == forms.FieldInteger
	case ir.FieldBoolean:
		return input == forms.FieldBoolean
	case ir.FieldDate:
		return input == forms.FieldDate
	case ir.FieldDateTime:
		return input == forms.FieldDateTime
	case ir.FieldTime:
		return input == forms.FieldTime
	case ir.FieldDuration:
		return input == forms.FieldDuration
	case ir.FieldFloat:
		return input == forms.FieldFloat
	case ir.FieldDecimal:
		return input == forms.FieldDecimal
	case ir.FieldUUID:
		return input == forms.FieldUUID
	case ir.FieldJSON:
		return input == forms.FieldJSON
	default:
		return false
	}
}

// Initial values describe the unsaved/current model, not new input. Preserve
// legacy email grammar and choices, while rejecting malformed representations.
func initialModelValueMatches(field ir.Field, value forms.Value) bool {
	if value.IsNull() {
		return true
	}
	switch field.Kind {
	case ir.FieldChar, ir.FieldEmail, ir.FieldFile, ir.FieldImage, ir.FieldText:
		text, ok := value.AsString()
		return ok && utf8.ValidString(text) && !strings.ContainsRune(text, 0) && (field.MaxLength == 0 || utf8.RuneCountInString(text) <= field.MaxLength)
	case ir.FieldAuto, ir.FieldInteger, ir.FieldForeignKey:
		_, ok := value.AsInteger()
		return ok
	case ir.FieldBoolean:
		_, ok := value.AsBoolean()
		return ok
	case ir.FieldDate:
		_, ok := value.AsDate()
		return ok
	case ir.FieldDateTime:
		_, ok := value.AsDateTime()
		return ok
	case ir.FieldTime:
		_, ok := value.AsTime()
		return ok
	case ir.FieldDuration:
		_, ok := value.AsDuration()
		return ok
	case ir.FieldFloat:
		_, ok := value.AsFloat()
		return ok
	case ir.FieldDecimal:
		number, ok := value.AsDecimal()
		return ok && field.Decimal != nil && number.Fits(field.Decimal.MaxDigits, field.Decimal.DecimalPlaces)
	case ir.FieldUUID:
		_, ok := value.AsUUID()
		return ok
	case ir.FieldJSON:
		_, ok := value.AsJSON()
		return ok
	default:
		return false
	}
}

func emptyInput(value forms.Value) bool {
	if file, ok := value.AsFile(); ok {
		return file.Name() == ""
	}
	if value.IsNull() {
		return true
	}
	if text, ok := value.AsString(); ok {
		return text == ""
	}
	if keys, ok := value.AsIntegers(); ok {
		return len(keys) == 0
	}
	if document, ok := value.AsJSON(); ok {
		return document.Text == "null" || document.Text == `""` || document.Text == "[]" || document.Text == "{}"
	}
	return false
}

func modelFieldErrors(field ir.Field, value forms.Value) validation.Errors {
	name := validation.Field(field.Name)
	reject := func(code validation.Code) validation.Errors { return validation.NewErrors(validation.New(name, code)) }
	// Model.clean_fields skips an empty value when blank is allowed, before
	// Field.clean can reject null. This never relaxes the eventual write's
	// stored-type/nullability checks or the database's NOT NULL constraint.
	if field.Blank && emptyInput(value) {
		return validation.Errors{}
	}
	if value.IsNull() {
		if !field.Nullable {
			return reject("null")
		}
		if !field.Blank {
			return reject("blank")
		}
		return validation.Errors{}
	}
	if emptyInput(value) {
		if !field.Blank {
			return reject("blank")
		}
		return validation.Errors{}
	}
	if len(field.Choices) > 0 {
		valid := false
		for _, choice := range field.Choices {
			if text, ok := value.AsString(); ok && choice.Value.Kind == ir.ScalarString {
				valid = valid || text == choice.Value.String
			}
			if number, ok := value.AsInteger(); ok && choice.Value.Kind == ir.ScalarInteger {
				valid = valid || number == choice.Value.Integer
			}
		}
		if !valid {
			return reject("invalid_choice")
		}
	}
	var failures []validation.Violation
	switch field.Kind {
	case ir.FieldChar, ir.FieldEmail, ir.FieldFile, ir.FieldImage, ir.FieldText:
		text, ok := value.AsString()
		if !ok {
			return reject("invalid")
		}
		if field.Kind == ir.FieldEmail && !validation.ValidEmail(text) {
			failures = append(failures, validation.New(name, "invalid"))
		}
		if length := utf8.RuneCountInString(text); field.MaxLength > 0 && length > field.MaxLength {
			failures = append(failures, validation.New(name, "max_length", validation.NewParam("limit_value", strconv.Itoa(field.MaxLength)), validation.NewParam("show_value", strconv.Itoa(length))))
		}
	case ir.FieldAuto, ir.FieldInteger, ir.FieldForeignKey:
		if _, ok := value.AsInteger(); !ok {
			return reject("invalid")
		}
	case ir.FieldBoolean:
		if _, ok := value.AsBoolean(); !ok {
			return reject("invalid")
		}
	case ir.FieldDate:
		if _, ok := value.AsDate(); !ok {
			return reject("invalid")
		}
	case ir.FieldDateTime:
		if _, ok := value.AsDateTime(); !ok {
			return reject("invalid")
		}
	case ir.FieldTime:
		if _, ok := value.AsTime(); !ok {
			return reject("invalid")
		}
	case ir.FieldDuration:
		if _, ok := value.AsDuration(); !ok {
			return reject("invalid")
		}
	case ir.FieldFloat:
		if _, ok := value.AsFloat(); !ok {
			return reject("invalid")
		}
	case ir.FieldDecimal:
		number, ok := value.AsDecimal()
		if !ok || field.Decimal == nil || !number.Fits(field.Decimal.MaxDigits, field.Decimal.DecimalPlaces) {
			return reject("invalid")
		}
	case ir.FieldUUID:
		if _, ok := value.AsUUID(); !ok {
			return reject("invalid")
		}
	case ir.FieldJSON:
		if _, ok := value.AsJSON(); !ok {
			return reject("invalid")
		}
	default:
		return reject("invalid")
	}
	return validation.NewErrors(failures...)
}
