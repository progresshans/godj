package forms

import (
	"fmt"
	"slices"
	"sort"
	"strconv"

	"github.com/progresshans/godj/validation"
)

// SetConfig is server-owned formset policy. Use DefaultSetConfig for Django's
// usual counts, then change the limits needed by the application. Zero counts
// are explicit; posted MIN_NUM_FORMS/MAX_NUM_FORMS never override this policy.
type SetConfig struct {
	Prefix         string
	ExtraForms     int
	MinForms       int
	MaxForms       int
	AbsoluteMax    int
	ValidateMin    bool
	ValidateMax    bool
	CanOrder       bool
	CanDelete      bool
	CanDeleteExtra bool
}

func DefaultSetConfig() SetConfig {
	return SetConfig{Prefix: "form", ExtraForms: 1, MaxForms: 1000, AbsoluteMax: 2000, CanDeleteExtra: true}
}

// SetValidator checks the whole set after all row forms were evaluated. It
// must be pure and safe for concurrent use. Return only non-field diagnostics;
// model/database checks and persistence remain explicit operations.
type SetValidator interface {
	ValidateSet([]SetForm) validation.Errors
}
type SetValidatorFunc func([]SetForm) validation.Errors

func (f SetValidatorFunc) ValidateSet(rows []SetForm) validation.Errors { return f(rows) }

// SetProcessor adds pure model processing to the normal binding lifecycle.
// Form runs after field cleaning, before count validation; unchanged optional
// extras skip it. Clean runs after count validation, before SetValidators, and
// can reject both rows and the whole set. Each callback must return its input
// binding or a WithErrors derivative, never another binding. No I/O is allowed.
type SetProcessor struct {
	Form  func(SetForm) (Form, error)
	Clean func(Set) (Set, error)
}

// SetSpec is immutable. Each binding owns its rows, management form and initial
// snapshots. It does not load models or confer any permission to persist them.
type SetSpec struct {
	row        Spec
	management Spec
	config     SetConfig
	validators []SetValidator
	valid      bool
}

func NewSetSpec(row Spec, config SetConfig, validators ...SetValidator) (SetSpec, error) {
	if !row.valid {
		return SetSpec{}, &ConfigError{Path: "set.form", Code: "uninitialized"}
	}
	if !validName(config.Prefix) || len(config.Prefix) > 128 {
		return SetSpec{}, &ConfigError{Path: "set.prefix", Code: "invalid"}
	}
	if config.ExtraForms < 0 || config.MinForms < 0 || config.MaxForms < config.MinForms || config.AbsoluteMax < config.MaxForms {
		return SetSpec{}, &ConfigError{Path: "set.limits", Code: "invalid"}
	}
	for _, field := range row.fields {
		if config.CanOrder && field.name == "ORDER" || config.CanDelete && field.name == "DELETE" {
			return SetSpec{}, &ConfigError{Path: "set.form." + field.name, Code: "reserved"}
		}
	}
	for index, validator := range validators {
		if nilInterface(validator) {
			return SetSpec{}, &ConfigError{Path: fmt.Sprintf("set.validators[%d]", index), Code: "nil"}
		}
	}
	fields := make([]Field, 0, 4)
	for _, name := range []string{"TOTAL_FORMS", "INITIAL_FORMS", "MIN_NUM_FORMS", "MAX_NUM_FORMS"} {
		required := name == "TOTAL_FORMS" || name == "INITIAL_FORMS"
		field, err := IntegerField(name, WithRequired(required), WithNullable())
		if err != nil {
			return SetSpec{}, err
		}
		fields = append(fields, field)
	}
	management, err := NewSpec(fields)
	if err != nil {
		return SetSpec{}, err
	}
	return SetSpec{row: row, management: management, config: config, validators: slices.Clone(validators), valid: true}, nil
}

func (s SetSpec) Config() SetConfig { return s.config }
func (s SetSpec) FormSpec() Spec    { return s.row }

// WithFormField replaces a row field in place, or appends a new field, while
// preserving the row and set validators. The original specification is intact.
func (s SetSpec) WithFormField(field Field) (SetSpec, error) {
	if !s.valid {
		return SetSpec{}, &ConfigError{Path: "set", Code: "uninitialized"}
	}
	fields := s.row.Fields()
	if index, found := s.row.index[field.Name()]; found {
		fields[index] = field
	} else {
		fields = append(fields, field)
	}
	row, err := NewSpec(fields, s.row.cross...)
	if err != nil {
		return SetSpec{}, err
	}
	return NewSetSpec(row, s.config, s.validators...)
}

// WithConfig keeps the row and validators while validating new server policy.
func (s SetSpec) WithConfig(config SetConfig) (SetSpec, error) {
	if !s.valid {
		return SetSpec{}, &ConfigError{Path: "set", Code: "uninitialized"}
	}
	return NewSetSpec(s.row, config, s.validators...)
}

// SetForm identifies one display row. FieldName returns its prefixed HTML
// input name; Form uses the ordinary, unprefixed field names for diagnostics.
type SetForm struct {
	index          int
	prefix         string
	spec           Spec
	form           Form
	emptyPermitted bool
	canDelete      bool
}

func (row SetForm) Index() int           { return row.index }
func (row SetForm) Prefix() string       { return row.prefix }
func (row SetForm) Form() Form           { return row.form }
func (row SetForm) Fields() []Field      { return row.spec.Fields() }
func (row SetForm) EmptyPermitted() bool { return row.emptyPermitted }
func (row SetForm) CanDelete() bool      { return row.canDelete }
func (row SetForm) DeletionRequested() bool {
	value, _ := row.form.Cleaned().Boolean("DELETE")
	return row.canDelete && value
}
func (row SetForm) FieldName(name string) (string, error) {
	if _, present := row.spec.index[name]; !row.spec.valid || !present {
		return "", &ConfigError{Path: "set.form.field", Code: "unknown_field"}
	}
	return row.prefix + "-" + name, nil
}

// Set retains row errors for redisplay, including errors on deleted rows. Its
// validity ignores deleted row errors, while its non-form errors remain fatal.
type Set struct {
	binding    *formBindingToken
	config     SetConfig
	submitted  Data
	management Form
	rows       []SetForm
	initial    int
	bound      bool
	valid      bool
	errors     validation.Errors
}

func (set Set) Bound() bool                      { return set.bound }
func (set Set) Valid() bool                      { return set.valid }
func (set Set) Forms() []SetForm                 { return slices.Clone(set.rows) }
func (set Set) TotalForms() int                  { return len(set.rows) }
func (set Set) InitialForms() int                { return set.initial }
func (set Set) Management() Form                 { return set.management }
func (set Set) NonFormErrors() validation.Errors { return set.errors }
func (set Set) Submitted() Data                  { return set.submitted }
func (set Set) Prefix() string                   { return set.config.Prefix }
func (set Set) Changed() bool {
	for _, row := range set.rows {
		if len(row.form.changed) != 0 {
			return true
		}
	}
	return false
}

// Unbound computes the display count without running field or set validators.
// Existing rows may exceed MaxForms, but never the explicit allocation cap.
func (s SetSpec) Unbound(initial []map[string]Value) (Set, error) {
	if err := s.checkInitial(initial); err != nil {
		return Set{}, err
	}
	count := max(len(initial), s.config.MinForms)
	if count > s.config.MaxForms {
		count = len(initial)
	} else if s.config.ExtraForms > s.config.MaxForms-count {
		count = s.config.MaxForms
	} else {
		count += s.config.ExtraForms
	}
	management, err := s.management.Unbound(map[string]Value{
		"TOTAL_FORMS": Integer(int64(count)), "INITIAL_FORMS": Integer(int64(len(initial))),
		"MIN_NUM_FORMS": Integer(int64(s.config.MinForms)), "MAX_NUM_FORMS": Integer(int64(s.config.MaxForms)),
	})
	if err != nil {
		return Set{}, err
	}
	set := Set{config: s.config, initial: len(initial), management: management, rows: make([]SetForm, 0, count)}
	for index := 0; index < count; index++ {
		row, err := s.makeRow(index, initial, Data{}, false)
		if err != nil {
			return Set{}, err
		}
		set.rows = append(set.rows, row)
	}
	return set, nil
}

// Bind uses the server's initial row count. Forged INITIAL_FORMS cannot turn a
// required current row into an unchanged, optional extra row. Counts and the
// hard cap are checked before constructing any row or running its callbacks.
func (s SetSpec) Bind(data Data, initial []map[string]Value) (Set, error) {
	return s.bind(data, initial, SetProcessor{})
}

// BindWith evaluates fields once, then applies the supplied pure row processor.
// Processor failures are operational/configuration errors, not deletable row
// diagnostics. Database I/O and final write admission remain separate.
func (s SetSpec) BindWith(data Data, initial []map[string]Value, processor SetProcessor) (Set, error) {
	if processor.Form == nil && processor.Clean == nil {
		return Set{}, &ConfigError{Path: "set.processor", Code: "nil"}
	}
	return s.bind(data, initial, processor)
}

func (s SetSpec) bind(data Data, initial []map[string]Value, processor SetProcessor) (Set, error) {
	if err := s.checkInitial(initial); err != nil {
		return Set{}, err
	}
	management, err := s.management.Bind(prefixedData(data, s.config.Prefix, s.management.fields), nil)
	if err != nil {
		return Set{}, err
	}
	total, totalPresent := management.Cleaned().Integer("TOTAL_FORMS")
	claimed, initialPresent := management.Cleaned().Integer("INITIAL_FORMS")
	var invalidCounts []validation.Violation
	if totalPresent && total < 0 {
		invalidCounts = append(invalidCounts, validation.New("TOTAL_FORMS", "invalid_count"))
	}
	if initialPresent && (claimed < 0 || claimed != int64(len(initial)) || totalPresent && claimed > total) {
		invalidCounts = append(invalidCounts, validation.New("INITIAL_FORMS", "invalid_count"))
	}
	if len(invalidCounts) != 0 {
		management, err = management.WithErrors(validation.NewErrors(invalidCounts...))
		if err != nil {
			return Set{}, err
		}
	}
	count := s.config.AbsoluteMax
	if total < int64(count) {
		count = int(max(total, 0))
	}
	set := Set{binding: &formBindingToken{}, config: s.config, submitted: data, initial: len(initial), management: management, bound: true, rows: make([]SetForm, 0, count)}
	if !management.Valid() {
		set.errors = validation.NewErrors(validation.New(validation.NonField, "missing_management_form"))
	}
	empty := 0
	for index := 0; index < count; index++ {
		row, err := s.makeRow(index, initial, data, true)
		if err != nil {
			return Set{}, err
		}
		if processor.Form != nil && !(row.emptyPermitted && len(row.form.changed) == 0) {
			processed, err := processor.Form(row)
			if err != nil {
				return Set{}, err
			}
			if !processed.bound || processed.binding == nil || processed.binding != row.form.binding {
				return Set{}, &ConfigError{Path: "set.processor", Code: "binding_mismatch"}
			}
			row.form = processed
		}
		if index >= len(initial) && len(row.form.changed) == 0 {
			empty++
		}
		set.rows = append(set.rows, row)
	}
	// Django's deleted selection is available only after all remaining rows
	// and management pass. Invalid surviving rows cannot relax count limits.
	deleted := 0
	if set.rowsValid() && set.errors.Empty() {
		for _, row := range set.rows {
			if row.DeletionRequested() && !(row.index >= set.initial && len(row.form.changed) == 0) {
				deleted++
			}
		}
	}
	switch {
	case total > int64(s.config.AbsoluteMax) || s.config.ValidateMax && count-deleted > s.config.MaxForms:
		set.errors = validation.NewErrors(validation.New(validation.NonField, "too_many_forms", validation.NewParam("num", strconv.Itoa(s.config.MaxForms))))
	case s.config.ValidateMin && count-deleted-empty < s.config.MinForms:
		set.errors = validation.NewErrors(validation.New(validation.NonField, "too_few_forms", validation.NewParam("num", strconv.Itoa(s.config.MinForms))))
	default:
		if processor.Clean != nil {
			set.valid = management.Valid() && set.rowsValid() && set.errors.Empty()
			processed, err := processor.Clean(set)
			if err != nil {
				return Set{}, err
			}
			if !processed.bound || processed.binding != set.binding {
				return Set{}, &ConfigError{Path: "set.processor", Code: "binding_mismatch"}
			}
			set = processed
		}
		var failures []validation.Errors
		for _, validator := range s.validators {
			errors := validator.ValidateSet(set.Forms())
			if err := validateSetErrors(errors); err != nil {
				return Set{}, err
			}
			failures = append(failures, errors)
		}
		if errors := validation.Join(failures...); !errors.Empty() {
			// As with BaseFormSet.clean(), cross-form diagnostics own the
			// non-form result. Detailed management errors remain available.
			if processor.Clean != nil {
				// Later custom diagnostics cannot erase a model rejection.
				set.errors = validation.Join(set.errors, errors)
			} else {
				set.errors = errors
			}
		}
	}
	set.valid = management.Valid() && set.rowsValid() && set.errors.Empty()
	return set, nil
}

func (s SetSpec) checkInitial(initial []map[string]Value) error {
	if !s.valid {
		return &ConfigError{Path: "set", Code: "uninitialized"}
	}
	if len(initial) > s.config.AbsoluteMax {
		return &ConfigError{Path: "set.initial", Code: "absolute_max"}
	}
	return nil
}

func (s SetSpec) makeRow(index int, initial []map[string]Value, data Data, bound bool) (SetForm, error) {
	fields := s.row.Fields()
	values := map[string]Value{}
	if index >= 0 && index < len(initial) {
		values = cloneValueMap(initial[index])
	}
	if s.config.CanOrder {
		order, err := IntegerField("ORDER", WithNullable(), WithRequired(false), WithLabel("Order"))
		if err != nil {
			return SetForm{}, err
		}
		fields = append(fields, order)
		if index >= 0 && index < len(initial) {
			if _, supplied := values["ORDER"]; !supplied {
				values["ORDER"] = Integer(int64(index + 1))
			}
		}
	}
	canDelete := s.config.CanDelete && (s.config.CanDeleteExtra || index >= 0 && index < len(initial))
	if canDelete {
		field, err := BooleanField("DELETE", WithRequired(false), WithLabel("Delete"))
		if err != nil {
			return SetForm{}, err
		}
		fields = append(fields, field)
	}
	spec, err := NewSpec(fields, s.row.cross...)
	if err != nil {
		return SetForm{}, err
	}
	name := strconv.Itoa(index)
	if index < 0 {
		name = "__prefix__"
	}
	row := SetForm{index: index, prefix: s.config.Prefix + "-" + name, spec: spec, emptyPermitted: index < 0 || index >= len(initial) && index >= s.config.MinForms, canDelete: canDelete}
	if !bound {
		row.form, err = spec.Unbound(values)
		return row, err
	}
	submitted := prefixedData(data, row.prefix, spec.fields)
	resolved, err := spec.resolveInitial(values)
	if err != nil {
		return SetForm{}, err
	}
	if row.emptyPermitted {
		changed := false
		for _, field := range spec.fields {
			value, _ := resolved.Get(field.name)
			if fieldChanged(field, submitted, value) {
				changed = true
				break
			}
		}
		if !changed {
			row.form = Form{binding: &formBindingToken{}, submitted: submitted, initial: resolved, bound: true, valid: true}
			return row, nil
		}
	}
	row.form, err = spec.Bind(submitted, values)
	return row, err
}

// EmptyForm is an unbound template for client-added rows. It owns no model ID
// and its __prefix__ marker must be replaced before submitting an added row.
func (s SetSpec) EmptyForm() (SetForm, error) {
	if !s.valid {
		return SetForm{}, &ConfigError{Path: "set", Code: "uninitialized"}
	}
	return s.makeRow(-1, nil, Data{}, false)
}

func prefixedData(data Data, prefix string, fields []Field) Data {
	values := make(map[string][]string, len(fields))
	for _, field := range fields {
		if raw, present := data.raw(prefix + "-" + field.name); present {
			values[field.name] = raw
		}
	}
	return NewData(values)
}

func (set Set) rowsValid() bool {
	for _, row := range set.rows {
		if !row.DeletionRequested() && !row.form.Valid() {
			return false
		}
	}
	return true
}
func (set Set) requireValid() error {
	if !set.bound || !set.valid {
		return &ConfigError{Path: "set", Code: "not_bound_valid"}
	}
	return nil
}

// ActiveForms excludes deleted rows and unchanged extra rows. Selection does
// not re-run validators and is not a model write authorization.
func (set Set) ActiveForms() ([]SetForm, error) {
	if err := set.requireValid(); err != nil {
		return nil, err
	}
	rows := []SetForm{}
	for _, row := range set.rows {
		if !row.DeletionRequested() && !(row.index >= set.initial && len(row.form.changed) == 0) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (set Set) DeletedForms() ([]SetForm, error) {
	if err := set.requireValid(); err != nil {
		return nil, err
	}
	rows := []SetForm{}
	for _, row := range set.rows {
		if row.DeletionRequested() && !(row.index >= set.initial && len(row.form.changed) == 0) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (set Set) OrderedForms() ([]SetForm, error) {
	rows, err := set.ActiveForms()
	if err != nil {
		return nil, err
	}
	if !set.config.CanOrder {
		return nil, &ConfigError{Path: "set.ordering", Code: "disabled"}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left, lok := rows[i].form.Cleaned().Integer("ORDER")
		right, rok := rows[j].form.Cleaned().Integer("ORDER")
		return lok && (!rok || left < right)
	})
	return rows, nil
}

// WithFormErrors appends row data diagnostics without running callbacks.
// Deleted-row data errors remain deletable. A whole-request admission failure
// must instead be attached through WithErrors or returned as an operation error.
func (set Set) WithFormErrors(index int, diagnostics validation.Errors, rejectedFields ...string) (Set, error) {
	if !set.bound || index < 0 || index >= len(set.rows) {
		return Set{}, &ConfigError{Path: "set.row", Code: "invalid"}
	}
	form, err := set.rows[index].form.WithErrors(diagnostics, rejectedFields...)
	if err != nil {
		return Set{}, err
	}
	set.rows = slices.Clone(set.rows)
	set.rows[index].form = form
	set.valid = set.valid && set.rowsValid()
	return set, nil
}

func validateSetErrors(errors validation.Errors) error {
	for _, diagnostic := range errors.All() {
		if diagnostic.Field() != validation.NonField {
			return &ConfigError{Path: "set.errors", Code: "not_non_field"}
		}
	}
	return nil
}

// WithErrors attaches a non-form rejection without changing rows or running
// callbacks again. Field/model rejections continue to belong to row forms.
func (set Set) WithErrors(errors validation.Errors) (Set, error) {
	if !set.bound {
		return Set{}, &ConfigError{Path: "set", Code: "unbound"}
	}
	if err := validateSetErrors(errors); err != nil {
		return Set{}, err
	}
	if !errors.Empty() {
		set.errors = validation.Join(set.errors, errors)
		set.valid = false
	}
	return set, nil
}

func (SetSpec) Format(state fmt.State, _ rune) { fmt.Fprint(state, "forms.SetSpec{redacted}") }
func (SetForm) Format(state fmt.State, _ rune) { fmt.Fprint(state, "forms.SetForm{redacted}") }
func (Set) Format(state fmt.State, _ rune)     { fmt.Fprint(state, "forms.Set{redacted}") }
