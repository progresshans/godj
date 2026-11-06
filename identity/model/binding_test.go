package model_test

import (
	"reflect"
	"testing"

	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
)

func TestBindingOwnsRoleAndTypedDefaultModel(t *testing.T) {
	definition, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	role := modeldef.AccountRole()
	staff := false
	role.Staff = ir.AccountBoolean{Constant: &staff}
	binding, err := accountmodel.Bind(role, models.UserDescriptor{}, definition)
	if err != nil {
		t.Fatal(err)
	}
	staff = true
	role.Model.ModelName = "other"
	definition.Models[0].Fields[1].Name = "other"
	actual, err := binding.Role()
	if err != nil || actual.Model.ModelName != "user" || *actual.Staff.Constant {
		t.Fatal("binding retained declaration ownership", actual, err)
	}
	*actual.Staff.Constant = true
	next, err := binding.Role()
	if err != nil || *next.Staff.Constant {
		t.Fatal("role accessor exposes binding state", err)
	}
	project, err := binding.Project()
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := project.Model(next.Model)
	if !ok || !reflect.DeepEqual(metadata, (models.UserDescriptor{}).Metadata()) {
		t.Fatal("bound model differs from generated account descriptor")
	}
	catalog, ok := project.Model(next.Catalog.Permission)
	if !ok || catalog.Fields[1].Name != "code" {
		t.Fatal("binding retained mutable catalog metadata")
	}
	if _, err := binding.Model(); err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Objects(); err != nil {
		t.Fatal(err)
	}
}

func TestBindingRejectsForeignDescriptorAndIncompleteProject(t *testing.T) {
	definition, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	role := modeldef.AccountRole()
	if binding, err := accountmodel.Bind(role, models.GroupDescriptor{}, definition); err == nil {
		t.Fatal("foreign Go model accepted for the account role", binding)
	}
	if binding, err := accountmodel.Bind(role, (*models.UserDescriptor)(nil), definition); err == nil {
		t.Fatal("typed nil descriptor accepted", binding)
	}
	if binding, err := accountmodel.Bind(role, &models.UserDescriptor{}, definition); err == nil {
		t.Fatal("mutable descriptor ownership accepted", binding)
	}
	definition.Models = []ir.Model{(models.UserDescriptor{}).Metadata()}
	if binding, err := accountmodel.Bind(role, models.UserDescriptor{}, definition); err == nil {
		t.Fatal("missing permission models accepted", binding)
	}
	var zero accountmodel.Binding[models.User]
	if _, err := zero.Role(); err == nil {
		t.Fatal("zero binding supplied an account role")
	}
	if _, err := zero.Project(); err == nil {
		t.Fatal("zero binding supplied a project")
	}
	if _, err := zero.Model(); err == nil {
		t.Fatal("zero binding supplied a model")
	}
	if _, err := zero.Objects(); err == nil {
		t.Fatal("zero binding supplied account queries")
	}
}
