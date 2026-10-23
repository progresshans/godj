package model_test

import (
	"slices"
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func TestModelViewFormPreservesSourceTargetAndLoadedValues(t *testing.T) {
	row := models.NewUserWithID(0)
	row.FirstName, row.Email = "old name", "current@example.test"
	row.EncodedPassword, row.PrincipalID = "private hash", "private principal"
	view, err := models.UserObjects.View(row, "first_name", "email")
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := models.UserObjects.Metadata()
	spec, err := formmodel.NewSpecForFields(metadata, []string{"first_name"})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := formmodel.InitialView(models.UserObjects, spec, view)
	if err != nil || len(initial) != 1 || !initial["first_name"].Equal(forms.String("old name")) {
		t.Fatal("view form initial selection", initial, err)
	}
	if form, err := spec.Unbound(initial); err != nil || form.Bound() {
		t.Fatal("view cannot produce unbound display", err)
	}
	cleaned := 0
	bound, err := formmodel.BindView(t.Context(), models.UserObjects, spec, forms.NewData(map[string][]string{"first_name": {"  new name  "}}), view,
		formmodel.PostClean{Validators: []formmodel.Validator{formmodel.ValidatorFunc(func(values forms.Values) validation.Errors {
			cleaned++
			for _, hidden := range []string{"encoded_password", "principal_id", "active", "staff", "superuser", "revision", "date_joined"} {
				if _, present := values.Get(hidden); present {
					t.Error("unloaded field impersonated current state", hidden)
				}
			}
			if value, present := values.Get("email"); !present || !value.Equal(forms.String("current@example.test")) {
				t.Error("explicit loaded non-input value was lost")
			}
			return validation.Errors{}
		})}})
	if err != nil || cleaned != 1 {
		t.Fatal("view form binding", err, cleaned)
	}
	input, err := formmodel.PrepareInput(models.UserObjects, bound)
	key, assigned := input.TargetKey()
	if err != nil || !assigned || !key.Equal(query.Integer(0)) || len(input.Values()) != 1 || !input.Values()["first_name"].Equal(query.String("new name")) {
		t.Fatal("view lost typed input target or selection", err)
	}
	if _, err := input.WithTargetKey(query.Integer(4)); err == nil {
		t.Fatal("view form target could be replaced")
	}
	for _, name := range []string{"username", "encoded_password", "groups"} {
		unloaded, err := formmodel.NewSpecForFields(metadata, []string{name})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := formmodel.InitialView(models.UserObjects, unloaded, view); err == nil {
			t.Fatal("unloaded value became form initial", name)
		}
		if _, err := formmodel.BindView(t.Context(), models.UserObjects, unloaded, forms.NewData(nil), view, formmodel.PostClean{}); err == nil {
			t.Fatal("unloaded value became writable form field", name)
		}
	}
	foreign := orm.NewManager[models.User](viewDifferentUserSource{})
	if _, err := formmodel.BindView(t.Context(), foreign, spec, forms.NewData(nil), view, formmodel.PostClean{}); err == nil {
		t.Fatal("same Go type with foreign model source accepted")
	}
	if _, err := formmodel.InitialView(models.UserObjects, spec, orm.ModelView[models.User]{}); err == nil {
		t.Fatal("zero view accepted")
	}
}

func TestModelViewFormCollectionsPreserveLoadedStateAndExplicitEditIntent(t *testing.T) {
	metadata, _ := models.UserObjects.Metadata()
	base, err := models.UserObjects.View(models.NewUserWithID(0), "first_name")
	if err != nil {
		t.Fatal(err)
	}
	view, err := base.WithCollections(map[string][]int64{"groups": {7, 0}, "permissions": {-17}})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"selected", "clear", "excluded", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			fields := []string{"first_name", "groups"}
			if mode == "excluded" {
				fields = []string{"first_name"}
			}
			spec, err := formmodel.NewSpecForFields(metadata, fields)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "excluded" {
				spec, err = spec.WithModelChoices("groups", forms.Choice{Value: forms.Integer(7), Label: "Seven"}, forms.Choice{Value: forms.Integer(0), Label: "Zero"})
				if err != nil {
					t.Fatal(err)
				}
			}
			initial, err := formmodel.InitialView(models.UserObjects, spec, view)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "excluded" {
				keys, ok := initial["groups"].AsIntegers()
				if !ok || !slices.Equal(keys, []int64{7, 0}) {
					t.Fatal("loaded collection lost initial keys")
				}
			}
			data := map[string][]string{"first_name": {"updated"}, "groups": {"0", "7"}}
			if mode == "clear" {
				delete(data, "groups")
			}
			if mode == "invalid" {
				data["groups"] = []string{"8"}
			}
			bound, err := formmodel.BindView(t.Context(), models.UserObjects, spec, forms.NewData(data), view,
				formmodel.PostClean{Validators: []formmodel.Validator{formmodel.ValidatorFunc(func(candidate forms.Values) validation.Errors {
					value, loaded := candidate.Get("permissions")
					keys, valid := value.AsIntegers()
					if !loaded || !valid || !slices.Equal(keys, []int64{-17}) {
						t.Error("non-input loaded collection disappeared from candidate")
					}
					return validation.Errors{}
				})}})
			if err != nil {
				t.Fatal(err)
			}
			input, err := formmodel.PrepareInput(models.UserObjects, bound)
			if mode == "invalid" {
				if err == nil {
					t.Fatal("view initial keys bypassed current choices")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			key, boundKey := input.TargetKey()
			if !boundKey || !key.Equal(query.Integer(0)) {
				t.Fatal("collection form lost stored zero target")
			}
			keys, present := input.Collection("groups")
			if mode == "excluded" && present || mode != "excluded" && !present || mode == "clear" && len(keys) != 0 || mode == "selected" && !slices.Equal(keys, []int64{0, 7}) {
				t.Fatal("selected/clear/excluded collection intent changed", keys, present)
			}
			if _, present := input.Collection("permissions"); present {
				t.Fatal("loaded excluded collection became write intent")
			}
		})
	}
	spec, err := formmodel.NewSpecForFields(metadata, []string{"groups"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := formmodel.InitialView(models.UserObjects, spec, base); err == nil {
		t.Fatal("unloaded collection became empty initial")
	}
	empty, err := base.WithCollections(map[string][]int64{"groups": nil})
	if err != nil {
		t.Fatal(err)
	}
	if initial, err := formmodel.InitialView(models.UserObjects, spec, empty); err != nil || !initial["groups"].Equal(forms.Integers()) {
		t.Fatal("explicit empty collection lost loaded presence", err)
	}
}

type viewDifferentUserSource struct{ models.UserDescriptor }

func (viewDifferentUserSource) Metadata() ir.Model {
	model := (models.UserDescriptor{}).Metadata()
	model.DBTable = "other_account_table"
	return model
}
