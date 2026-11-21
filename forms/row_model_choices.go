package forms

import (
	"fmt"
	"slices"
)

// RowModelChoices owns per-row relation selections. It can replace only the
// choices of ModelChoice fields, never field structure or validators. Missing
// rows and fields have no permitted choices; they do not inherit the original
// form's choices. The zero value is an empty, closed snapshot.
//
// Prototype is separate from submitted rows and is used only by EmptyForm.
// Its choices are never a fallback for a missing ordinary row.
type RowModelChoices struct {
	rows      map[int]map[string][]Choice
	prototype map[string][]Choice
}

func NewRowModelChoices(rows map[int]map[string][]Choice, prototype map[string][]Choice) RowModelChoices {
	owned := make(map[int]map[string][]Choice, len(rows))
	for index, fields := range rows {
		owned[index] = cloneModelChoices(fields)
	}
	return RowModelChoices{rows: owned, prototype: cloneModelChoices(prototype)}
}

func cloneModelChoices(fields map[string][]Choice) map[string][]Choice {
	owned := make(map[string][]Choice, len(fields))
	for name, choices := range fields {
		owned[name] = slices.Clone(choices)
	}
	return owned
}

// WithRowModelChoices derives a request-local specification. Choice snapshots
// cannot target scalar fields or the server-owned inline parent field.
func (s SetSpec) WithRowModelChoices(choices RowModelChoices) (SetSpec, error) {
	if !s.valid {
		return SetSpec{}, &ConfigError{Path: "set", Code: "uninitialized"}
	}
	indices := make([]int, 0, len(choices.rows))
	for index := range choices.rows {
		indices = append(indices, index)
	}
	slices.Sort(indices)
	for _, index := range indices {
		if index < 0 || index >= s.config.AbsoluteMax {
			return SetSpec{}, &ConfigError{Path: "set.choices.rows", Code: "invalid_index"}
		}
		if err := s.validateModelChoices(choices.rows[index]); err != nil {
			return SetSpec{}, err
		}
	}
	if err := s.validateModelChoices(choices.prototype); err != nil {
		return SetSpec{}, err
	}
	s.rowChoices = &choices
	return s, nil
}

func (s SetSpec) validateModelChoices(choices map[string][]Choice) error {
	names := make([]string, 0, len(choices))
	for name := range choices {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if _, err := s.row.WithModelChoices(name, choices[name]...); err != nil {
			return err
		}
	}
	return nil
}

func (s SetSpec) rowFor(index int) (Spec, error) {
	if s.rowChoices == nil {
		return s.row, nil
	}
	choices := s.rowChoices.rows[index]
	if index == -1 {
		choices = s.rowChoices.prototype
	}
	row := s.row
	for _, field := range s.row.fields {
		if !field.ModelChoice() {
			continue
		}
		var err error
		row, err = row.WithModelChoices(field.Name(), choices[field.Name()]...)
		if err != nil {
			return Spec{}, err
		}
	}
	return row, nil
}

func (s SetSpec) checkChoiceRows(count int) error {
	if s.rowChoices != nil {
		for index := range s.rowChoices.rows {
			if index >= count {
				return &ConfigError{Path: "set.choices.rows", Code: "unknown_row"}
			}
		}
	}
	return nil
}

func (RowModelChoices) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "forms.RowModelChoices{redacted}")
}
