package forms_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/validation"
)

func TestFormSetProcessorOwnsBindingAndRunsBeforeSetLimits(t *testing.T) {
	fieldCalls, modelCalls := 0, 0
	row := setRowSpec(t, forms.CrossValidatorFunc(func(forms.Values) validation.Errors {
		fieldCalls++
		return validation.Errors{}
	}))
	config := forms.DefaultSetConfig()
	config.Prefix, config.CanDelete = "items", true
	config.MaxForms, config.ValidateMax = 1, true
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	initial := []map[string]forms.Value{{"title": forms.String("old")}}
	data := setData(2, 1, map[string]string{"title": "old", "DELETE": "on"}, map[string]string{"title": "bad"})
	bound, err := spec.BindWith(data, initial, forms.SetProcessorFunc(func(row forms.SetForm) (forms.Form, error) {
		modelCalls++
		title, _ := row.Form().Cleaned().String("title")
		if title == "bad" {
			return row.Form().WithErrors(validation.NewErrors(validation.New("title", "model_rejected")))
		}
		return row.Form(), nil
	}))
	if err != nil || bound.Valid() || fieldCalls != 2 || modelCalls != 2 {
		t.Fatal("processor did not run after one field binding", err, fieldCalls, modelCalls)
	}
	if got := setErrorCodes(bound.NonFormErrors())[string(validation.NonField)]; len(got) != 1 || got[0] != "too_many_forms" {
		t.Fatal("set limits ran before model errors", got)
	}
	config.ValidateMax = false
	spec, err = forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	fieldCalls, modelCalls = 0, 0
	bound, err = spec.BindWith(setData(2, 1, map[string]string{"title": "old"}, nil), initial, forms.SetProcessorFunc(func(row forms.SetForm) (forms.Form, error) { modelCalls++; return row.Form(), nil }))
	if err != nil || !bound.Valid() || fieldCalls != 1 || modelCalls != 1 {
		t.Fatal("empty extra ran cleaning/processor", err, fieldCalls, modelCalls)
	}
	operationErr := errors.New("post-processing stopped")
	if _, err := spec.BindWith(data, initial, forms.SetProcessorFunc(func(forms.SetForm) (forms.Form, error) { return forms.Form{}, operationErr })); !errors.Is(err, operationErr) {
		t.Fatal("operation failure was treated as deletable diagnostics", err)
	}
	var nilProcessor forms.SetProcessorFunc
	if _, err := spec.BindWith(data, initial, nilProcessor); err == nil {
		t.Fatal("typed nil processor accepted")
	}
}

func TestFormSetProcessorRejectsRebindingAndOtherRows(t *testing.T) {
	base := setRowSpec(t)
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	spec, err := forms.NewSetSpec(base, config)
	if err != nil {
		t.Fatal(err)
	}
	data := setData(2, 0, map[string]string{"title": "one"}, map[string]string{"title": "two"})
	for _, mode := range []string{"rebound", "other_row", "unbound", "zero"} {
		t.Run(mode, func(t *testing.T) {
			var first forms.Form
			_, err := spec.BindWith(data, nil, forms.SetProcessorFunc(func(row forms.SetForm) (forms.Form, error) {
				switch mode {
				case "rebound":
					return base.Bind(row.Form().Submitted(), nil)
				case "unbound":
					return base.Unbound(nil)
				case "zero":
					return forms.Form{}, nil
				case "other_row":
					if row.Index() == 0 {
						first = row.Form()
						return first, nil
					}
					return first, nil
				}
				return row.Form(), nil
			}))
			var configErr *forms.ConfigError
			if !errors.As(err, &configErr) || configErr.Code != "binding_mismatch" {
				t.Fatal("row binding replacement accepted", err)
			}
		})
	}
}

func TestFormSetRowRejectionsRetainWholeRequestAdmission(t *testing.T) {
	config := forms.DefaultSetConfig()
	config.Prefix = "items"
	config.CanDelete = true
	spec, err := forms.NewSetSpec(setRowSpec(t), config)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(setData(2, 1, map[string]string{"title": "old", "DELETE": "on"}, map[string]string{"title": "new"}), []map[string]forms.Value{{"title": forms.String("old")}})
	if err != nil || !bound.Valid() {
		t.Fatal(err)
	}
	deleted, err := bound.WithFormErrors(0, validation.NewErrors(validation.New("title", "data_error")))
	if err != nil || !deleted.Valid() || !bound.Forms()[0].Form().Errors().Empty() {
		t.Fatal("row rejection mutated source or blocked deletion", err)
	}
	rejected, err := deleted.WithErrors(validation.NewErrors(validation.New(validation.NonField, "admission_denied")))
	if err != nil || rejected.Valid() {
		t.Fatal("delete hid whole-request denial", err)
	}
	if _, err := rejected.DeletedForms(); err == nil {
		t.Fatal("denied deletion selection was published")
	}
	if _, err := bound.WithFormErrors(2, validation.Errors{}); err == nil {
		t.Fatal("out of range row accepted")
	}
	if _, err := bound.WithFormErrors(1, validation.NewErrors(validation.New("unknown", "invalid"))); err == nil {
		t.Fatal("unknown field accepted")
	}
	changed, err := bound.WithFormErrors(1, validation.NewErrors(validation.New("title", "unique")))
	if err != nil || changed.Valid() || !bound.Valid() {
		t.Fatal("active row rejection was ignored or mutated source", err)
	}
}
