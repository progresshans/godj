package consumer_test

import (
	"testing"

	"example.com/godj-forward-scalar/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
)

func checkTypedPreparation(t *testing.T, data []models.Datum, holders []models.Holder) {
	t.Helper()
	metadata, err := models.DatumObjects.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := formmodel.NewSpecForFields(metadata, []string{"label"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stored := range data {
		snapshot, err := models.DatumObjects.ModelValues(stored)
		if err != nil {
			t.Fatal("all scalar snapshot", err)
		}
		changes := map[string]query.Value{}
		for name, value := range snapshot {
			if name != "id" {
				changes[name] = value
			}
		}
		decoded, err := models.DatumObjects.ApplyValues(models.Datum{}, changes)
		if err != nil {
			t.Fatal("all scalar assignment", err)
		}
		again, err := models.DatumObjects.ModelValues(decoded)
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range changes {
			if !again[name].Equal(want) {
				t.Fatal("scalar round trip differs", name)
			}
		}
		if !again["id"].IsNull() {
			t.Fatal("value assignment forged primary key presence")
		}
		instance, err := formmodel.BindInstance(t.Context(), models.DatumObjects, spec, forms.NewData(map[string][]string{"label": {stored.Label + "-prepared"}}), &stored, formmodel.PostClean{})
		if err != nil {
			t.Fatal("typed initial scalar conversion", err)
		}
		prepared, err := instance.Prepare()
		if err != nil {
			t.Fatal("typed scalar preparation", err)
		}
		result, err := prepared.Model()
		if err != nil {
			t.Fatal(err)
		}
		final, err := models.DatumObjects.ModelValues(result)
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range snapshot {
			if name == "label" {
				want = query.String(stored.Label + "-prepared")
			}
			if !final[name].Equal(want) {
				t.Fatal("typed form changed excluded scalar", name)
			}
		}
		// Every nullable member can be cleared through its own generated branch.
		nulls := map[string]query.Value{}
		for _, field := range metadata.Fields {
			if field.Nullable {
				nulls[field.Name] = query.Null()
			}
		}
		cleared, err := models.DatumObjects.ApplyValues(stored, nulls)
		if err != nil {
			t.Fatal(err)
		}
		clearedValues, err := models.DatumObjects.ModelValues(cleared)
		if err != nil {
			t.Fatal(err)
		}
		for name := range nulls {
			if !clearedValues[name].IsNull() {
				t.Fatal("nullable scalar was not cleared", name)
			}
		}
		unchanged, err := models.DatumObjects.ModelValues(stored)
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range snapshot {
			if !unchanged[name].Equal(want) {
				t.Fatal("typed preparation changed source model", name)
			}
		}
	}
	for _, stored := range holders {
		snapshot, err := models.HolderObjects.ModelValues(stored)
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]query.Value{"primary": snapshot["primary"], "secondary": snapshot["secondary"]}
		decoded, err := models.HolderObjects.ApplyValues(models.Holder{}, values)
		if err != nil {
			t.Fatal("required/nullable foreign key preparation", err)
		}
		decodedValues, err := models.HolderObjects.ModelValues(decoded)
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range values {
			if !decodedValues[name].Equal(want) {
				t.Fatal("foreign key assignment differs", name)
			}
		}
		if _, err := models.HolderObjects.ApplyValues(stored, map[string]query.Value{"primary": query.Null()}); err == nil {
			t.Fatal("required FK NULL became zero")
		}
	}
}
