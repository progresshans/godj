package orm_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestModelViewOwnsSelectionSourceAndStoredKeyPresence(t *testing.T) {
	objects := models.UserObjects
	instant := time.Date(2026, 10, 24, 1, 2, 3, 123456000, time.UTC)
	row := models.NewUserWithID(0)
	row.FirstName, row.Username = "selected private name", "alice"
	row.PrincipalID, row.EncodedPassword = "hidden principal", "hidden password"
	row.LastLogin = &instant
	fields := []string{"first_name", "last_login"}
	view, err := objects.View(row, fields...)
	if err != nil {
		t.Fatal(err)
	}
	fields[0], row.FirstName, *row.LastLogin = "encoded_password", "mutated", instant.Add(time.Hour)
	if value, present := view.Get("first_name"); !present || !value.Equal(query.String("selected private name")) {
		t.Fatal("view borrowed field values", value, present)
	}
	if value, present := view.Get("last_login"); !present || !value.Equal(query.DateTime(time.Date(2026, 10, 24, 1, 2, 3, 123456000, time.UTC))) {
		t.Fatal("view borrowed nullable field", value, present)
	}
	key, present := view.PrimaryKey()
	if !present || !key.Equal(query.Integer(0)) {
		t.Fatal("present zero became unsaved", key, present)
	}
	for _, name := range []string{"id", "username", "principal_id", "encoded_password", "revision", "superuser", "staff", "active", "date_joined", "groups", "permissions"} {
		if _, present := view.Get(name); present {
			t.Fatal("unselected field became readable", name)
		}
	}
	copy := view.Values()
	copy["first_name"], copy["encoded_password"] = query.String("changed copy"), query.String("forged")
	if got := view.Values(); len(got) != 2 || !got["first_name"].Equal(query.String("selected private name")) {
		t.Fatal("view map ownership lost", got)
	}
	model, valid := view.Model()
	if !valid {
		t.Fatal("valid view has no source")
	}
	for index, field := range model.Fields {
		if field.Name == "first_name" {
			if _, found := view.ReadField(field); !found {
				t.Fatal("selected field was not readable")
			}
			model.Fields[index].Column = "changed_column"
			if _, found := view.ReadField(model.Fields[index]); found {
				t.Fatal("foreign field metadata accepted")
			}
		}
	}
	if current, _ := view.Model(); current.Equal(model) {
		t.Fatal("metadata is mutable through getter")
	}
	selection := view.Fields()
	selection[0] = "encoded_password"
	if !reflect.DeepEqual(view.Fields(), []string{"first_name", "last_login"}) {
		t.Fatal("field selection is mutable")
	}
	for _, printed := range []string{fmt.Sprintf("%v", view), fmt.Sprintf("%+v", view), fmt.Sprintf("%#v", &view)} {
		if strings.Contains(printed, "selected private name") || strings.Contains(printed, "hidden") {
			t.Fatal("view formatting exposed model data", printed)
		}
	}
	encoded, err := json.Marshal(view)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("view bypassed explicit output", string(encoded), err)
	}
	for _, id := range []int64{0, -17, 42} {
		selected, err := objects.View(models.NewUserWithID(id), "id")
		key, present := selected.PrimaryKey()
		value, found := selected.Get("id")
		if err != nil || !present || !found || !key.Equal(query.Integer(id)) || !value.Equal(key) {
			t.Fatal("stored key changed", id, err)
		}
	}
	unsaved, err := objects.View(models.User{}, "id")
	key, present = unsaved.PrimaryKey()
	if err != nil || present || !key.IsNull() {
		t.Fatal("unsaved key became present", err)
	}
	keyOnly, err := objects.View(models.NewUserWithID(9))
	if _, valid := keyOnly.Model(); err != nil || !valid || len(keyOnly.Values()) != 0 {
		t.Fatal("explicit empty selection is invalid", err)
	}
	for _, fields := range [][]string{{"missing"}, {"first_name", "first_name"}, {"groups"}} {
		if view, err := objects.View(row, fields...); err == nil || view.Values() != nil {
			t.Fatal("invalid selection retained view", fields, err)
		}
	}
	if view, err := (orm.Manager[models.User]{}).View(row, "first_name"); err == nil || view.Values() != nil {
		t.Fatal("unbound source retained view", err)
	}
	if _, valid := (orm.ModelView[models.User]{}).Model(); valid {
		t.Fatal("zero view is valid")
	}
}
