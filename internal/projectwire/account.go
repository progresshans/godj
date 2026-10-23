package projectwire

import (
	"encoding/json"
	"errors"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func parseAccountRole(decoder *json.Decoder, budget *specBudget) error {
	// Account, three model identities, three boolean roles and catalog. There
	// are no unbounded arrays or arbitrary metadata objects in this binding.
	if err := budget.consumeNodes(projectspec.AccountRoleNodes); err != nil {
		return err
	}
	text := func() error { _, err := wirejson.String(decoder, projectspec.MaxSchemaStringBytes); return err }
	identity := func() error {
		return wirejson.Object(decoder, []string{"app_label", "model_name"}, map[string]func() error{
			"app_label": text, "model_name": text,
		})
	}
	boolean := func() error {
		field, constant := false, false
		err := wirejson.Object(decoder, nil, map[string]func() error{
			"field":    func() error { field = true; return text() },
			"constant": func() error { constant = true; return wirejson.Bool(decoder) },
		})
		if err == nil && field == constant {
			return errors.New("account boolean role requires exactly one field or constant")
		}
		return err
	}
	return wirejson.Object(decoder, []string{
		"model", "principal_id", "login", "password", "revision", "last_login", "active", "staff", "superuser",
		"groups", "permissions", "write_login_normalization", "authenticate_normalization", "catalog",
	}, map[string]func() error{
		"model": identity, "principal_id": text, "login": text, "password": text, "revision": text, "last_login": text, "email": text,
		"active": boolean, "staff": boolean, "superuser": boolean, "groups": text, "permissions": text,
		"write_login_normalization": text, "authenticate_normalization": text,
		"catalog": func() error {
			return wirejson.Object(decoder, []string{
				"group", "group_name", "group_revision", "group_permissions", "permission", "permission_code", "permission_name", "permission_revision",
			}, map[string]func() error{
				"group": identity, "group_name": text, "group_revision": text, "group_permissions": text,
				"permission": identity, "permission_code": text, "permission_name": text, "permission_revision": text,
			})
		},
	})
}

func measureAccountRole(sizer *wirejson.Sizer, role ir.AccountRole) bool {
	identity := func(value ir.ModelIdentity) bool {
		return sizer.Literal(`{"app_label":`) && sizer.String(value.AppLabel) &&
			sizer.Literal(`,"model_name":`) && sizer.String(value.ModelName) && sizer.Literal(`}`)
	}
	boolean := func(value ir.AccountBoolean) bool {
		if !sizer.Literal(`{`) {
			return false
		}
		if value.Field != "" && (!sizer.Literal(`"field":`) || !sizer.String(value.Field)) {
			return false
		}
		if value.Constant != nil {
			if value.Field != "" && !sizer.Literal(`,`) {
				return false
			}
			if !sizer.Literal(`"constant":`) || !sizer.Boolean(*value.Constant) {
				return false
			}
		}
		return sizer.Literal(`}`)
	}
	if !sizer.Literal(`{"model":`) || !identity(role.Model) ||
		!sizer.Literal(`,"principal_id":`) || !sizer.String(role.PrincipalID) ||
		!sizer.Literal(`,"login":`) || !sizer.String(role.Login) ||
		!sizer.Literal(`,"password":`) || !sizer.String(role.Password) ||
		!sizer.Literal(`,"revision":`) || !sizer.String(role.Revision) ||
		!sizer.Literal(`,"last_login":`) || !sizer.String(role.LastLogin) {
		return false
	}
	if role.Email != "" && (!sizer.Literal(`,"email":`) || !sizer.String(role.Email)) {
		return false
	}
	return sizer.Literal(`,"active":`) && boolean(role.Active) &&
		sizer.Literal(`,"staff":`) && boolean(role.Staff) &&
		sizer.Literal(`,"superuser":`) && boolean(role.Superuser) &&
		sizer.Literal(`,"groups":`) && sizer.String(role.Groups) &&
		sizer.Literal(`,"permissions":`) && sizer.String(role.Permissions) &&
		sizer.Literal(`,"write_login_normalization":`) && sizer.String(string(role.WriteLoginNormalization)) &&
		sizer.Literal(`,"authenticate_normalization":`) && sizer.String(string(role.AuthenticateNormalization)) &&
		sizer.Literal(`,"catalog":{"group":`) && identity(role.Catalog.Group) &&
		sizer.Literal(`,"group_name":`) && sizer.String(role.Catalog.GroupName) &&
		sizer.Literal(`,"group_revision":`) && sizer.String(role.Catalog.GroupRevision) &&
		sizer.Literal(`,"group_permissions":`) && sizer.String(role.Catalog.GroupPermissions) &&
		sizer.Literal(`,"permission":`) && identity(role.Catalog.Permission) &&
		sizer.Literal(`,"permission_code":`) && sizer.String(role.Catalog.PermissionCode) &&
		sizer.Literal(`,"permission_name":`) && sizer.String(role.Catalog.PermissionName) &&
		sizer.Literal(`,"permission_revision":`) && sizer.String(role.Catalog.PermissionRevision) &&
		sizer.Literal(`}}`)
}
