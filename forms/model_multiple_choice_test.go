package forms_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func TestModelMultipleChoiceMatchesIndependentDjango(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			data, err := os.ReadFile("testdata/model-multiple-choice-django61-" + backend + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var reference struct {
				Choices      []json.RawMessage `json:"choices"`
				Observations []struct {
					Required bool     `json:"required"`
					Initial  []int64  `json:"initial"`
					Raw      []string `json:"raw"`
					Changed  bool     `json:"changed"`
					Errors   []string `json:"errors"`
					Value    []int64  `json:"value"`
				} `json:"observations"`
			}
			if err = json.Unmarshal(data, &reference); err != nil {
				t.Fatal(err)
			}
			if len(reference.Observations) != 476 {
				t.Fatal("reference coverage changed", len(reference.Observations))
			}
			var choices []forms.Choice
			for _, raw := range reference.Choices {
				var pair [2]json.RawMessage
				if err = json.Unmarshal(raw, &pair); err != nil {
					t.Fatal(err)
				}
				var key int64
				var label string
				if err = json.Unmarshal(pair[0], &key); err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(pair[1], &label); err != nil {
					t.Fatal(err)
				}
				choices = append(choices, forms.Choice{Value: forms.Integer(key), Label: label})
			}
			for i, entry := range reference.Observations {
				field, err := forms.ModelMultipleChoiceField("labels", forms.WithRequired(entry.Required), forms.WithChoices(choices...))
				if err != nil {
					t.Fatal(err)
				}
				spec, err := forms.NewSpec([]forms.Field{field})
				if err != nil {
					t.Fatal(err)
				}
				form, err := spec.Bind(forms.NewData(map[string][]string{"labels": entry.Raw}), map[string]forms.Value{"labels": forms.Integers(entry.Initial...)})
				if err != nil {
					t.Fatal(err)
				}
				codes := []string{}
				for _, failure := range form.Errors().All() {
					codes = append(codes, string(failure.Code()))
				}
				keys, present := form.Cleaned().Integers("labels")
				if !slices.Equal(codes, entry.Errors) || slices.Contains(form.Changed(), "labels") != entry.Changed || present != (len(codes) == 0) || present && !slices.Equal(keys, entry.Value) {
					t.Fatalf("case %d %+v: keys=%v present=%v errors=%v changed=%v", i, entry, keys, present, codes, form.Changed())
				}
			}
		})
	}
}

func TestModelMultipleChoiceOwnsEverySnapshotAndDoesNotPublishPartialValues(t *testing.T) {
	keys := []int64{7, 9}
	value := forms.Integers(keys...)
	keys[0] = 1
	read, ok := value.AsIntegers()
	if !ok || !slices.Equal(read, []int64{7, 9}) {
		t.Fatal(read)
	}
	read[0] = 2
	again, _ := value.AsIntegers()
	if !slices.Equal(again, []int64{7, 9}) {
		t.Fatal(again)
	}
	choices := []forms.Choice{{Value: forms.Integer(7), Label: "seven"}, {Value: forms.Integer(9), Label: "nine"}}
	field, err := forms.ModelMultipleChoiceField("labels", forms.WithRequired(false), forms.WithChoices(choices...), forms.WithValidators(forms.FieldValidatorFunc(func(v forms.Value) validation.Errors {
		keys, _ := v.AsIntegers()
		if len(keys) > 0 {
			keys[0] = 0
		}
		return validation.NewErrors()
	})))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	choices[0].Value = forms.Integer(1)
	next, err := spec.WithModelChoices("labels", forms.Choice{Value: forms.Integer(9), Label: "new nine"})
	if err != nil {
		t.Fatal(err)
	}
	for i, current := range []forms.Spec{spec, next} {
		f, err := current.Bind(forms.NewData(map[string][]string{"labels": {"7", "9"}}), nil)
		if err != nil {
			t.Fatal(err)
		}
		keys, ok := f.Cleaned().Integers("labels")
		if i == 0 {
			if !f.Valid() || !ok || !slices.Equal(keys, []int64{7, 9}) {
				t.Fatal(keys, f.Errors())
			}
		} else if f.Valid() || ok {
			t.Fatal("partial collection escaped", keys)
		}
	}
	empty, err := spec.Bind(forms.NewData(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	keys, ok = empty.Cleaned().Integers("labels")
	if !empty.Valid() || !ok || keys == nil || len(keys) != 0 {
		t.Fatal(empty, keys)
	}
	bad, err := spec.Bind(forms.NewData(map[string][]string{"labels": {"7", "bad"}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bad.Cleaned().Get("labels"); ok {
		t.Fatal("bad collection partially published")
	}
}

func TestModelMultipleChoiceConfigurationAndInt64Boundary(t *testing.T) {
	for _, options := range [][]forms.FieldOption{{forms.WithNullable()}, {forms.WithWidget(forms.Select)}, {forms.WithMaxLength(10)}, {forms.WithDefault(forms.Null())}, {forms.WithChoices(forms.Choice{Value: forms.String("7"), Label: "wrong"})}, {forms.WithChoices(forms.Choice{Value: forms.Integer(7), Label: "first"}, forms.Choice{Value: forms.Integer(7), Label: "duplicate"})}, {nil}} {
		if _, err := forms.ModelMultipleChoiceField("labels", options...); err == nil {
			t.Fatal("invalid field accepted", options)
		}
	}
	field, err := forms.ModelMultipleChoiceField("labels")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"9223372036854775808", "-9223372036854775809"} {
		f, err := spec.Bind(forms.NewData(map[string][]string{"labels": {raw}}), nil)
		if err != nil {
			t.Fatal(err)
		}
		failures := f.Errors().All()
		if len(failures) != 1 || failures[0].Code() != "invalid_pk_value" {
			t.Fatal(failures)
		}
	}
	if _, err := spec.Unbound(map[string]forms.Value{"labels": forms.Null()}); err == nil {
		t.Fatal("NULL collection initial accepted")
	}
	if _, err := spec.WithModelChoices("missing"); err == nil {
		t.Fatal("unknown relation accepted")
	}
}

func TestModelMultipleChoiceConcurrentSpecs(t *testing.T) {
	field, err := forms.ModelMultipleChoiceField("labels")
	if err != nil {
		t.Fatal(err)
	}
	base, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for n := range 8 {
		wg.Go(func() {
			key := int64(n + 1)
			spec, e := base.WithModelChoices("labels", forms.Choice{Value: forms.Integer(key), Label: "own"})
			if e != nil {
				failures <- e
				return
			}
			raw := map[string][]string{"labels": {fmt.Sprint(key)}}
			for range 25 {
				form, e := spec.Bind(forms.NewData(raw), nil)
				if e != nil {
					failures <- e
					return
				}
				keys, ok := form.Cleaned().Integers("labels")
				if !ok || !reflect.DeepEqual(keys, []int64{key}) {
					failures <- fmt.Errorf("foreign values %v", keys)
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if len(base.Fields()[0].Choices()) != 0 {
		t.Fatal("shared base changed")
	}
}
