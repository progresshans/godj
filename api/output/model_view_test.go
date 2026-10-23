package output_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func TestModelViewOutputRequiresExactSourceAndLoadedSelection(t *testing.T) {
	metadata, _ := models.UserObjects.Metadata()
	spec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "first_name"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := output.New(output.ModelView(models.UserObjects, spec), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	row := models.NewUserWithID(0)
	row.FirstName, row.EncodedPassword, row.PrincipalID = "  exact name  ", "hidden hash", "hidden principal"
	view, err := models.UserObjects.View(row, "id", "first_name")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := response.Encode(t.Context(), view)
	if err != nil || string(encoded) != `{"id":0,"first_name":"  exact name  "}` {
		t.Fatal("model view output changed stored data", string(encoded), err)
	}
	for _, selection := range [][]string{{"id"}, {"first_name"}, nil} {
		partial, err := models.UserObjects.View(row, selection...)
		if err != nil {
			t.Fatal(err)
		}
		if body, err := response.Encode(t.Context(), partial); err == nil || body != nil {
			t.Fatal("missing view field supplied a default response", string(body), err)
		}
	}
	foreign := orm.NewManager[models.User](outputDifferentUserSource{})
	other, err := foreign.View(row, "id", "first_name")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []orm.ModelView[models.User]{other, {}} {
		if body, err := response.Encode(t.Context(), invalid); err == nil || body != nil {
			t.Fatal("foreign/zero view became output", err)
		}
	}
	if _, err := output.New(output.ModelView(foreign, spec), serializers.Limits{}); err == nil {
		t.Fatal("foreign output declaration accepted")
	}
	if _, err := output.New(output.ModelView(orm.Manager[models.User]{}, spec), serializers.Limits{}); err == nil {
		t.Fatal("unbound output source accepted")
	}
	schema, err := serializers.Encode(response.Schema().Value(), serializers.Limits{})
	if err != nil || strings.Contains(string(schema), "password") || strings.Contains(string(schema), "principal_id") {
		t.Fatal("hidden field entered output schema", string(schema), err)
	}
}

type outputDifferentUserSource struct{ models.UserDescriptor }

func (outputDifferentUserSource) Metadata() ir.Model {
	model := (models.UserDescriptor{}).Metadata()
	model.DBTable = "different_account_output"
	return model
}
