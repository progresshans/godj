package forms

import (
	"context"
	"fmt"
	"strconv"

	"github.com/progresshans/godj/validation"
)

// SetPreflight owns one immutable management/input snapshot. It parses only
// management fields and performs no row, model or set validation. Admission
// permits callers to resolve row-scoped choices before ordinary pure binding;
// it is not validation success or permission to write.
type SetPreflight struct {
	spec       SetSpec
	data       Data
	management Form
	total      int64
	count      int
	initial    int
	valid      bool
}

func (s SetSpec) Preflight(ctx context.Context, data Data, initialCount int) (SetPreflight, error) {
	if !s.valid {
		return SetPreflight{}, &ConfigError{Path: "set", Code: "uninitialized"}
	}
	if initialCount < 0 || initialCount > s.config.AbsoluteMax {
		return SetPreflight{}, &ConfigError{Path: "set.initial", Code: "absolute_max"}
	}
	management, err := s.management.Bind(ctx, prefixedData(data, s.config.Prefix, s.management.fields), nil)
	if err != nil {
		return SetPreflight{}, err
	}
	total, totalPresent := management.Cleaned().Integer("TOTAL_FORMS")
	claimed, initialPresent := management.Cleaned().Integer("INITIAL_FORMS")
	var invalidCounts []validation.Violation
	if totalPresent && total < 0 {
		invalidCounts = append(invalidCounts, validation.New("TOTAL_FORMS", "invalid_count"))
	}
	if initialPresent && (claimed < 0 || claimed != int64(initialCount) || totalPresent && claimed > total) {
		invalidCounts = append(invalidCounts, validation.New("INITIAL_FORMS", "invalid_count"))
	}
	if len(invalidCounts) != 0 {
		management, err = management.WithErrors(validation.NewErrors(invalidCounts...))
		if err != nil {
			return SetPreflight{}, err
		}
	}
	count := s.config.AbsoluteMax
	if total < int64(count) {
		count = int(max(total, 0))
	}
	return SetPreflight{spec: s, data: data, management: management, total: total, count: count, initial: initialCount, valid: true}, nil
}

func (input SetPreflight) AdmissionValid() bool {
	return input.valid && input.management.Valid() && input.total <= int64(input.spec.config.AbsoluteMax)
}
func (input SetPreflight) TotalForms() int   { return input.count }
func (input SetPreflight) InitialForms() int { return input.initial }
func (input SetPreflight) Submitted() Data   { return input.data }
func (input SetPreflight) Management() Form  { return input.management }

// RowData returns the selected row's unprefixed raw fields and file
// capabilities. It is available only after management admission. It does not
// clean values or apply read-only policy; read-only selection must use current
// server values. Returned Data retains the same immutable input ownership.
func (input SetPreflight) RowData(index int) (Data, error) {
	if !input.AdmissionValid() {
		return Data{}, &ConfigError{Path: "set.preflight", Code: "not_admitted"}
	}
	if index < 0 || index >= input.count {
		return Data{}, &ConfigError{Path: "set.index", Code: "invalid"}
	}
	return prefixedData(input.data, input.spec.config.Prefix+"-"+strconv.Itoa(index), input.spec.row.fields), nil
}

func (input SetPreflight) ReadOnly(index int) (bool, error) {
	if !input.valid || index < 0 || index >= input.count {
		return false, &ConfigError{Path: "set.index", Code: "invalid"}
	}
	return input.spec.config.ReadOnlyInitial && index < input.initial, nil
}

// Bind continues the same input without parsing management again. Supplying
// one RowModelChoices enables closed per-row selection; omitting it keeps the
// original declaration's choices. More than one snapshot is a configuration
// error. Initial must have the same server-owned count used by Preflight.
func (input SetPreflight) Bind(ctx context.Context, initial []map[string]Value, choices ...RowModelChoices) (Set, error) {
	return input.bind(ctx, initial, SetProcessor{}, choices)
}

func (input SetPreflight) BindWith(ctx context.Context, initial []map[string]Value, processor SetProcessor, choices ...RowModelChoices) (Set, error) {
	if processor.Form == nil && processor.Clean == nil {
		return Set{}, &ConfigError{Path: "set.processor", Code: "nil"}
	}
	return input.bind(ctx, initial, processor, choices)
}

func (input SetPreflight) bind(ctx context.Context, initial []map[string]Value, processor SetProcessor, choices []RowModelChoices) (Set, error) {
	if !input.valid {
		return Set{}, &ConfigError{Path: "set.preflight", Code: "uninitialized"}
	}
	if nilInterface(ctx) {
		return Set{}, &ConfigError{Path: "set.context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return Set{}, err
	}
	if len(initial) != input.initial {
		return Set{}, &ConfigError{Path: "set.initial", Code: "count_mismatch"}
	}
	if len(choices) > 1 {
		return Set{}, &ConfigError{Path: "set.choices", Code: "multiple_snapshots"}
	}
	if len(choices) == 1 {
		var err error
		input.spec, err = input.spec.WithRowModelChoices(choices[0])
		if err != nil {
			return Set{}, err
		}
	}
	if err := input.spec.checkChoiceRows(input.count); err != nil {
		return Set{}, err
	}
	return input.spec.bindPreflight(ctx, input, initial, processor)
}

func (SetPreflight) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "forms.SetPreflight{redacted}")
}
