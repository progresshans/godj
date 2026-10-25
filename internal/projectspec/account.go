package projectspec

import "github.com/progresshans/godj/schema/ir"

// AccountRoleNodes is the closed account wire graph: account, three model
// identities, three boolean roles and catalog.
const AccountRoleNodes = 8

func ValidateProjectResources(schemas []ir.Schema, role *ir.AccountRole) error {
	if len(schemas) > MaxApps {
		return resourceError("apps", "apps", MaxApps, len(schemas))
	}
	if err := ValidateAccountRoleResources(role); err != nil {
		return err
	}
	budget := resourceBudget{}
	if role != nil {
		budget.nodes = AccountRoleNodes
	}
	_, err := validateSchemas(schemas, budget)
	return err
}

// ValidateAccountRoleResources bounds the fixed-size account reference graph
// before normalization, cloning or diagnostic construction. Model-sized data
// remains in schemas and is checked by ValidateSchemas.
func ValidateAccountRoleResources(role *ir.AccountRole) error {
	if role == nil {
		return nil
	}
	for _, item := range []struct{ path, value string }{
		{"model.app_label", role.Model.AppLabel}, {"model.model_name", role.Model.ModelName},
		{"principal_id", role.PrincipalID}, {"login", role.Login}, {"password", role.Password},
		{"revision", role.Revision}, {"last_login", role.LastLogin}, {"created_at", role.CreatedAt}, {"email", role.Email},
		{"active.field", role.Active.Field}, {"staff.field", role.Staff.Field}, {"superuser.field", role.Superuser.Field},
		{"groups", role.Groups}, {"permissions", role.Permissions},
		{"write_login_normalization", string(role.WriteLoginNormalization)}, {"authenticate_normalization", string(role.AuthenticateNormalization)},
		{"login_form_normalization", string(role.LoginFormNormalization)},
		{"write_email_normalization", string(role.WriteEmailNormalization)},
		{"catalog.group.app_label", role.Catalog.Group.AppLabel}, {"catalog.group.model_name", role.Catalog.Group.ModelName},
		{"catalog.group_name", role.Catalog.GroupName}, {"catalog.group_revision", role.Catalog.GroupRevision}, {"catalog.group_permissions", role.Catalog.GroupPermissions},
		{"catalog.permission.app_label", role.Catalog.Permission.AppLabel}, {"catalog.permission.model_name", role.Catalog.Permission.ModelName},
		{"catalog.permission_code", role.Catalog.PermissionCode}, {"catalog.permission_name", role.Catalog.PermissionName}, {"catalog.permission_revision", role.Catalog.PermissionRevision},
	} {
		if err := validateString("account."+item.path, item.value); err != nil {
			return err
		}
	}
	return nil
}
