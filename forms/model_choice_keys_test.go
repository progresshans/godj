package forms_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func TestModelChoiceLookupKeysPreserveCleaningSyntax(t *testing.T) {
	cases := []struct {
		name     string
		raw      []string
		multiple bool
		keys     []int64
		valid    bool
	}{
		{"single_missing", nil, false, nil, true},
		{"single_empty", []string{""}, false, nil, true},
		{"single_alias", []string{" +0_7 "}, false, []int64{7}, true},
		{"single_unicode", []string{"٧"}, false, []int64{7}, true},
		{"single_repeated", []string{"7", "7"}, false, nil, false},
		{"single_fraction", []string{"7.0"}, false, nil, false},
		{"single_nul", []string{"7\x00"}, false, nil, false},
		{"single_overflow", []string{"9223372036854775808"}, false, nil, false},
		{"multiple_empty", nil, true, []int64{}, true},
		{"multiple_duplicate", []string{"7", "7", "-2", "0", "9007199254740993", "9223372036854775807", "-9223372036854775808"}, true, []int64{7, -2, 0, 9007199254740993, math.MaxInt64, math.MinInt64}, true},
		{"multiple_alias", []string{"+07"}, true, nil, false},
		{"multiple_unicode", []string{"٧"}, true, nil, false},
		{"multiple_negative_zero", []string{"-0"}, true, nil, false},
		{"multiple_fraction", []string{"7.0"}, true, nil, false},
		{"multiple_nul", []string{"7\x00"}, true, nil, false},
		{"multiple_blank", []string{""}, true, nil, false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			calls := 0
			option := forms.WithValidators(forms.FieldValidatorFunc(func(forms.Value) validation.Errors { calls++; return validation.Errors{} }))
			field, err := forms.ModelChoiceField("target", option)
			if item.multiple {
				field, err = forms.ModelMultipleChoiceField("target", option)
			}
			if err != nil {
				t.Fatal(err)
			}
			data := forms.NewData(map[string][]string{"target": item.raw})
			keys, valid, err := field.ModelChoiceKeys(data)
			if err != nil || valid != item.valid || !reflect.DeepEqual(keys, item.keys) || calls != 0 {
				t.Fatal("lookup keys changed cleaning or executed validators", keys, valid, err, calls)
			}
			// Resolving syntactically valid keys supplies only membership. Final
			// Bind owns requiredness, diagnostics, and exactly one validator call.
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			choices := make([]forms.Choice, len(keys))
			for i, key := range keys {
				choices[i] = forms.Choice{Value: forms.Integer(key), Label: "Resolved"}
			}
			spec, err = spec.WithModelChoices("target", choices...)
			if err != nil {
				t.Fatal(err)
			}
			bound, err := spec.Bind(t.Context(), data, nil)
			wantValid := item.valid && len(keys) != 0
			if err != nil || bound.Valid() != wantValid || (calls == 1) != wantValid {
				t.Fatal("key resolution changed final binding", bound.Errors(), err, calls)
			}
			if len(keys) != 0 {
				keys[0] = 999
				again, _, _ := field.ModelChoiceKeys(data)
				if !reflect.DeepEqual(again, item.keys) {
					t.Fatal("returned key slice retained")
				}
			}
		})
	}
	ordinary, _ := forms.IntegerField("target")
	parent, _ := forms.InlineParentField("target", forms.Integer(7))
	for _, field := range []forms.Field{ordinary, parent, {}} {
		if _, _, err := field.ModelChoiceKeys(forms.NewData(nil)); err == nil {
			t.Fatal("nonrelation lookup accepted")
		}
	}
}
