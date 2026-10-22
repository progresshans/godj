package model

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// InlinePreflight owns the canonical parent snapshot and the submitted child
// identity mapping. It has not cleaned editable row data. Admission includes
// management, the complete current cohort and raw parent/identity controls,
// even for deleted or unchanged extra rows. It grants no write authority.
type InlinePreflight[P, C any] struct {
	inline InlineSet[P, C]
	input  SetPreflight[C]
}

func (spec InlineSpec[P, C]) Preflight(ctx context.Context, data forms.Data, parent P, current []C, postClean PostClean, related ...func(C, ir.ManyToManyField) ([]int64, bool)) (InlinePreflight[P, C], error) {
	result, row, err := spec.start(parent, current)
	if err != nil {
		return InlinePreflight[P, C]{}, err
	}
	if slices.Contains(postClean.Fields, spec.foreignKey) {
		return InlinePreflight[P, C]{}, &Error{Path: "inline.post_clean", Code: "parent_is_server_owned"}
	}
	input, err := preflightSet(ctx, spec.child, row, data, current, postClean, related, spec.foreignKey)
	if err != nil {
		return InlinePreflight[P, C]{}, err
	}
	expected := ""
	if key, present := result.key.AsInteger(); present {
		expected = strconv.FormatInt(key, 10)
	}
	var failures []validation.Violation
	for index := 0; index < input.TotalForms(); index++ {
		name := row.Config().Prefix + "-" + strconv.Itoa(index) + "-" + spec.foreignKey
		if raw, present := data.Get(name); present && (len(raw) != 1 || raw[0] != "" && (result.key.IsNull() || raw[0] != expected)) {
			failures = append(failures, validation.New(validation.NonField, "invalid_parent", validation.NewParam("index", strconv.Itoa(index))))
		}
	}
	input.errors = validation.Join(input.errors, validation.NewErrors(failures...))
	return InlinePreflight[P, C]{inline: result, input: input}, nil
}

func (input InlinePreflight[P, C]) AdmissionValid() bool  { return input.input.AdmissionValid() }
func (input InlinePreflight[P, C]) TotalForms() int       { return input.input.TotalForms() }
func (input InlinePreflight[P, C]) InitialForms() int     { return input.input.InitialForms() }
func (input InlinePreflight[P, C]) Submitted() forms.Data { return input.input.Submitted() }
func (input InlinePreflight[P, C]) RowData(index int) (forms.Data, error) {
	return input.input.RowData(index)
}

func (input InlinePreflight[P, C]) ChoiceKeys(index int, fieldName string) ([]int64, bool, error) {
	return input.input.ChoiceKeys(index, fieldName)
}

func (input InlinePreflight[P, C]) ParentIdentity() forms.Value { return input.inline.key }
func (input InlinePreflight[P, C]) ReadOnly(index int) (bool, error) {
	return input.input.ReadOnly(index)
}
func (input InlinePreflight[P, C]) Current(index int) (C, bool, error) {
	return input.input.Current(index)
}
func (input InlinePreflight[P, C]) Identity(index int) (forms.Value, bool) {
	return input.input.Identity(index)
}
func (input InlinePreflight[P, C]) IdentityName(index int) (string, error) {
	return input.input.IdentityName(index)
}

// Bind uses only row-specific relation snapshots and preserves all preflight
// admission failures. A new parent's key remains pending until the resulting
// InlineSet is prepared with the saved parent in the same write transaction.
func (input InlinePreflight[P, C]) Bind(ctx context.Context, choices forms.RowModelChoices) (InlineSet[P, C], error) {
	return input.bind(ctx, []forms.RowModelChoices{choices})
}

func (input InlinePreflight[P, C]) bind(ctx context.Context, choices []forms.RowModelChoices) (InlineSet[P, C], error) {
	set, err := input.input.bind(ctx, choices)
	if err != nil {
		return InlineSet[P, C]{}, err
	}
	result := input.inline
	result.set = set
	return result, nil
}

func (InlinePreflight[P, C]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.InlinePreflight{redacted}")
}
