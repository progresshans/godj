package forms_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/progresshans/godj/forms"
)

func TestModelChoiceSnapshotsMatchIndependentDjango(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			data, err := os.ReadFile("testdata/model-choice-django61-" + backend + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var reference struct {
				Choices      [][]json.RawMessage `json:"choices"`
				Observations []struct {
					Required bool     `json:"required"`
					Initial  *int64   `json:"initial"`
					Raw      *string  `json:"raw"`
					Changed  bool     `json:"changed"`
					Errors   []string `json:"errors"`
					Value    *int64   `json:"value"`
				} `json:"observations"`
			}
			if err = json.Unmarshal(data, &reference); err != nil {
				t.Fatal(err)
			}
			if len(reference.Observations) != 216 {
				t.Fatal("incomplete ModelChoice oracle")
			}
			choices := make([]forms.Choice, len(reference.Choices))
			for i, pair := range reference.Choices {
				var key int64
				var label string
				if len(pair) != 2 || json.Unmarshal(pair[0], &key) != nil || json.Unmarshal(pair[1], &label) != nil {
					t.Fatal("bad reference choice")
				}
				choices[i] = forms.Choice{Value: forms.Integer(key), Label: label}
			}
			for i, observation := range reference.Observations {
				options := []forms.FieldOption{}
				if !observation.Required {
					options = append(options, forms.WithRequired(false), forms.WithNullable())
				}
				field, err := forms.ModelChoiceField("ticket", options...)
				if err != nil {
					t.Fatal(err)
				}
				spec, err := forms.NewSpec([]forms.Field{field})
				if err != nil {
					t.Fatal(err)
				}
				spec, err = spec.WithModelChoices("ticket", choices...)
				if err != nil {
					t.Fatal(err)
				}
				var initial map[string]forms.Value
				if observation.Initial != nil {
					initial = map[string]forms.Value{"ticket": forms.Integer(*observation.Initial)}
				}
				input := map[string][]string{}
				if observation.Raw != nil {
					input["ticket"] = []string{*observation.Raw}
				}
				form, err := spec.Bind(forms.NewData(input), initial)
				if err != nil {
					t.Fatal(err)
				}
				codes := []string{}
				for _, violation := range form.Errors().All() {
					if violation.Field() != "ticket" {
						t.Fatal("wrong error field")
					}
					codes = append(codes, string(violation.Code()))
				}
				if !reflect.DeepEqual(codes, observation.Errors) || (len(form.Changed()) != 0) != observation.Changed {
					t.Fatalf("case %d: errors=%v changed=%v; expected %+v", i, codes, form.Changed(), observation)
				}
				if form.Valid() {
					value, found := form.Cleaned().Get("ticket")
					if !found {
						t.Fatal("missing cleaned key")
					}
					if observation.Value == nil {
						if !value.IsNull() {
							t.Fatal("empty choice is not null")
						}
					} else if key, ok := value.AsInteger(); !ok || key != *observation.Value {
						t.Fatal("wrong key", key)
					}
				} else if len(observation.Errors) == 0 {
					t.Fatal("unexpected invalid form")
				}
			}
		})
	}
}

func TestModelChoiceSnapshotsAreExplicitIndependentAndFailClosed(t *testing.T) {
	field, err := forms.ModelChoiceField("ticket")
	if err != nil {
		t.Fatal(err)
	}
	base, err := forms.NewSpec([]forms.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	bind := func(spec forms.Spec, raw string) forms.Form {
		form, err := spec.Bind(forms.NewData(map[string][]string{"ticket": {raw}}), nil)
		if err != nil {
			t.Fatal(err)
		}
		return form
	}
	if bind(base, "7").Valid() {
		t.Fatal("unpopulated relation accepted a key")
	}
	choices := []forms.Choice{{Value: forms.Integer(7), Label: "visible"}}
	first, err := base.WithModelChoices("ticket", choices...)
	if err != nil {
		t.Fatal(err)
	}
	choices[0] = forms.Choice{Value: forms.Integer(12), Label: "private"}
	if !bind(first, "7").Valid() || bind(first, "12").Valid() || bind(base, "7").Valid() {
		t.Fatal("caller or derived choices mutated another snapshot")
	}
	fields := first.Fields()
	returned := fields[0].Choices()
	returned[0] = choices[0]
	if !bind(first, "7").Valid() {
		t.Fatal("returned choices leaked ownership")
	}
	empty, err := first.WithModelChoices("ticket")
	if err != nil {
		t.Fatal(err)
	}
	if bind(empty, "7").Valid() || !bind(first, "7").Valid() {
		t.Fatal("empty snapshot widened or mutated membership")
	}
	for _, invalid := range [][]forms.Choice{{{Value: forms.String("7"), Label: "bad"}}, {{Value: forms.Integer(7), Label: "first"}, {Value: forms.Integer(7), Label: "duplicate"}}, {{Value: forms.Integer(7), Label: "bad\x00label"}}} {
		if _, err := first.WithModelChoices("ticket", invalid...); err == nil {
			t.Fatal("invalid choice result accepted")
		}
	}
	ordinary, err := forms.IntegerField("ticket")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{ordinary})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spec.WithModelChoices("ticket", choices...); err == nil {
		t.Fatal("ordinary field became a relation")
	}
	if _, err := first.WithModelChoices("other", choices...); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := forms.ModelChoiceField("ticket", forms.WithWidget(forms.TextInput)); err == nil {
		t.Fatal("relation selection widget disabled")
	}
	repeated, err := first.Bind(forms.NewData(map[string][]string{"ticket": {"7", "7"}}), nil)
	if err != nil || repeated.Valid() {
		t.Fatal("duplicate scalar input accepted", err)
	}
	if bind(first, string([]byte{0xff})).Valid() {
		t.Fatal("invalid UTF-8 key accepted")
	}
}
