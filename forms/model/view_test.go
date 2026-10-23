package model_test

import (
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

type viewDifferentUserSource struct{ models.UserDescriptor }

func (viewDifferentUserSource) Metadata() ir.Model {
	model := (models.UserDescriptor{}).Metadata()
	model.DBTable = "other_account_table"
	return model
}
