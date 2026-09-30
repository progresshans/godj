package identityapi

import (
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func (a *Application) prepareSpecs() error {
	password, err := serializers.StringField("password", serializers.WithTrimWhitespace(false), serializers.WithMaxLength(4096), serializers.WithNullable())
	if err != nil {
		return err
	}
	a.password, err = serializers.NewSpec([]serializers.Field{password})
	if err != nil {
		return err
	}
	definitions := []struct {
		r       resource
		model   ir.Model
		input   []serializers.ModelField
		output  []string
		related []string
	}{
		{resource{name: "users", title: "User", view: identity.ViewUser, add: identity.AddUser, change: identity.ChangeUser, remove: identity.DeleteUser},
			(models.UserDescriptor{}).Metadata(),
			[]serializers.ModelField{{Name: "username"}, {Name: "first_name", AllowEmpty: true}, {Name: "last_name", AllowEmpty: true}, {Name: "email", AllowEmpty: true}, {Name: "active"}, {Name: "staff"}, {Name: "superuser"}, {Name: "groups", Optional: true}, {Name: "permissions", Optional: true}},
			[]string{"id", "username", "first_name", "last_name", "email", "active", "staff", "superuser", "date_joined", "last_login", "revision"}, []string{"groups", "permissions"}},
		{resource{name: "groups", title: "Group", view: identity.ViewGroup, add: identity.AddGroup, change: identity.ChangeGroup, remove: identity.DeleteGroup},
			(models.GroupDescriptor{}).Metadata(), []serializers.ModelField{{Name: "name"}, {Name: "permissions", Optional: true}}, []string{"id", "name", "revision"}, []string{"permissions"}},
		{resource{name: "permissions", title: "Permission", view: identity.ViewPermission, add: identity.AddPermission, change: identity.ChangePermission, remove: identity.DeletePermission},
			(models.PermissionDescriptor{}).Metadata(), []serializers.ModelField{{Name: "code"}, {Name: "name"}}, []string{"id", "code", "name", "revision"}, nil},
	}
	for _, definition := range definitions {
		r := definition.r
		r.update, err = serializers.FromModel(definition.model, definition.input...)
		if err != nil {
			return err
		}
		r.create = r.update
		if r.name == "users" {
			r.create, err = serializers.NewSpec(append(r.update.Fields(), password))
			if err != nil {
				return err
			}
		}
		selected := make([]serializers.ModelField, 0, len(definition.output)+len(definition.related))
		for _, name := range definition.output {
			allowEmpty := false
			for _, field := range definition.model.Fields {
				if field.Name == name {
					allowEmpty = field.Kind == ir.FieldChar || field.Kind == ir.FieldEmail || field.Kind == ir.FieldURL || field.Kind == ir.FieldText
				}
			}
			selected = append(selected, serializers.ModelField{Name: name, ReadOnly: true, AllowEmpty: allowEmpty})
		}
		r.scalar, err = serializers.FromModel(definition.model, selected...)
		if err != nil {
			return err
		}
		for _, name := range definition.related {
			selected = append(selected, serializers.ModelField{Name: name, ReadOnly: true})
		}
		r.detail, err = serializers.FromModel(definition.model, selected...)
		if err != nil {
			return err
		}
		a.resources = append(a.resources, r)
	}
	return nil
}

func textValue(values serializers.Values, name string) string {
	value, _ := values.Get(name)
	text, _ := value.AsString()
	return text
}

func userPatch(values serializers.Values) identity.UserPatch {
	p := identity.UserPatch{}
	for _, e := range values.All() {
		text, _ := e.Value().AsString()
		flag, _ := e.Value().AsBoolean()
		keys, _ := e.Value().AsIntegers()
		switch e.Name() {
		case "username":
			p = p.WithUsername(text)
		case "first_name":
			p = p.WithFirstName(text)
		case "last_name":
			p = p.WithLastName(text)
		case "email":
			p = p.WithEmail(text)
		case "active":
			p = p.WithActive(flag)
		case "staff":
			p = p.WithStaff(flag)
		case "superuser":
			p = p.WithSuperuser(flag)
		case "groups":
			p = p.WithGroups(keys...)
		case "permissions":
			p = p.WithPermissions(keys...)
		}
	}
	return p
}

func userCreate(values serializers.Values, principalID string) identity.UserCreate {
	p := identity.NewUserCreate(principalID, textValue(values, "username"))
	for _, e := range values.All() {
		text, _ := e.Value().AsString()
		flag, _ := e.Value().AsBoolean()
		keys, _ := e.Value().AsIntegers()
		switch e.Name() {
		case "first_name":
			p = p.WithFirstName(text)
		case "last_name":
			p = p.WithLastName(text)
		case "email":
			p = p.WithEmail(text)
		case "active":
			p = p.WithActive(flag)
		case "staff":
			p = p.WithStaff(flag)
		case "superuser":
			p = p.WithSuperuser(flag)
		case "groups":
			p = p.WithGroups(keys...)
		case "permissions":
			p = p.WithPermissions(keys...)
		}
	}
	return p
}
