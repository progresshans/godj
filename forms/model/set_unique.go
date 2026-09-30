package model

import (
	"slices"
	"strings"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// validateSetUnique checks submitted, cleaned tuples, as ModelFormSet does;
// model-clean candidates and stored rows still need their final DB checks.
// All tuples come from the same pre-rejection snapshot. A failed overlapping
// constraint must not turn another complete tuple into a partial comparison.
func validateSetUnique[M any](metadata ir.Model, set forms.Set, instances map[int]InstanceForm[M], parent string) (forms.Set, error) {
	checks := make([][]string, 0, len(metadata.UniqueConstraints))
	known := make(map[string]bool, len(metadata.Fields))
	seenChecks := map[string]bool{}
	add := func(fields []string) error {
		members := map[string]bool{}
		for _, name := range fields {
			if !known[name] || members[name] {
				return &Error{Path: "set.unique", Code: "invalid_fields"}
			}
			members[name] = true
		}
		if len(fields) == 0 {
			return &Error{Path: "set.unique", Code: "empty"}
		}
		key := strings.Join(fields, "\x00")
		if !seenChecks[key] {
			checks = append(checks, slices.Clone(fields))
			seenChecks[key] = true
		}
		return nil
	}
	for _, field := range metadata.Fields {
		known[field.Name] = true
	}
	for _, field := range metadata.Fields {
		if field.Unique && !field.PrimaryKey {
			if err := add([]string{field.Name}); err != nil {
				return forms.Set{}, err
			}
		}
	}
	for _, constraint := range metadata.UniqueConstraints {
		if err := add(constraint.Fields); err != nil {
			return forms.Set{}, err
		}
	}
	if len(checks) == 0 {
		return set, nil
	}
	type rowValues struct {
		index    int
		cleaned  forms.Values
		excluded []string
	}
	rows := make([]rowValues, 0, set.TotalForms())
	// Django exposes deleted_forms only while the pre-clean set is valid.
	// A different invalid row therefore prevents DELETE from hiding a duplicate
	// between otherwise valid rows. Count checks have already used this rule.
	ignoreDeleted := set.Valid()
	for _, row := range set.Forms() {
		instance, evaluated := instances[row.Index()]
		if !evaluated || !row.Form().Valid() || ignoreDeleted && row.DeletionRequested() {
			continue
		}
		rows = append(rows, rowValues{row.Index(), row.Form().Cleaned(), instance.bound.Excluded()})
	}
	type rejection struct {
		index  int
		fields []string
	}
	var rejected []rejection
	for _, fields := range checks {
		var seen [][]forms.Value
		for _, row := range rows {
			tuple := make([]forms.Value, 0, len(fields))
			for _, name := range fields {
				if name == parent {
					// Every inline row belongs to the same server parent, even
					// before a new parent's generated key exists.
					tuple = append(tuple, forms.String(parent))
					continue
				}
				value, present := row.cleaned.Get(name)
				if !present || value.IsNull() || slices.Contains(row.excluded, name) {
					break
				}
				if file, reference := value.AsFile(); reference {
					if _, pending := file.Upload(); !pending {
						// Content inspected at different times is still the same
						// stored name for model uniqueness purposes.
						value = forms.String(file.Name())
					}
				}
				tuple = append(tuple, value)
			}
			if len(tuple) != len(fields) {
				continue
			}
			duplicate := slices.ContainsFunc(seen, func(previous []forms.Value) bool {
				return slices.EqualFunc(previous, tuple, forms.Value.Equal)
			})
			if duplicate {
				rejected = append(rejected, rejection{row.index, fields})
			} else {
				seen = append(seen, tuple)
			}
		}
	}
	for _, rejected := range rejected {
		failure := validation.NewErrors(validation.New(validation.NonField, "unique", validation.NewParam("fields", strings.Join(rejected.fields, ","))))
		instance, err := instances[rejected.index].WithErrors(failure, rejected.fields...)
		if err != nil {
			return forms.Set{}, err
		}
		set, err = set.WithFormErrors(rejected.index, failure, rejected.fields...)
		if err != nil {
			return forms.Set{}, err
		}
		set, err = set.WithErrors(failure)
		if err != nil {
			return forms.Set{}, err
		}
		instances[rejected.index] = instance
	}
	return set, nil
}
