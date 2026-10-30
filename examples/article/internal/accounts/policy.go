// Package accounts owns this project's selected account and catalog policy.
// Change these typed declarations alongside modeldef when selecting another
// model. No policy here opens a database or supplies a default account row.
package accounts

import (
	"github.com/progresshans/godj/examples/article/project"
	"github.com/progresshans/godj/identity"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
)

type Runtime = accountmodel.Runtime[models.User, models.Group, models.Permission]

func Policy() (accountmodel.RuntimeConfig[models.User, models.Group, models.Permission], error) {
	var result accountmodel.RuntimeConfig[models.User, models.Group, models.Permission]
	binding, err := project.BindAccount()
	if err != nil {
		return result, err
	}
	passwords, err := binding.DefaultPasswordValidator(
		accountmodel.PasswordAttribute{Field: "username", Label: "username"},
		accountmodel.PasswordAttribute{Field: "first_name", Label: "first name"},
		accountmodel.PasswordAttribute{Field: "last_name", Label: "last name"},
		accountmodel.PasswordAttribute{Field: "email", Label: "email address"},
	)
	if err != nil {
		return result, err
	}
	accountPermissions := accountmodel.AccountPermissions{View: identity.ViewUser, Add: identity.AddUser, Change: identity.ChangeUser, Delete: identity.DeleteUser}
	groupPermissions := accountmodel.CatalogPermissions{View: identity.ViewGroup, Add: identity.AddGroup, Change: identity.ChangeGroup, Delete: identity.DeleteGroup}
	permissionPermissions := accountmodel.CatalogPermissions{View: identity.ViewPermission, Add: identity.AddPermission, Change: identity.ChangePermission, Delete: identity.DeletePermission}
	fields := []string{"username", "email", "first_name", "last_name", "active", "staff", "superuser", "groups", "permissions"}
	result.Accounts = accountmodel.AccountManagementConfig[models.User]{
		ReadFields: fields, CreateFields: fields, EditFields: fields,
		Permissions:           accountPermissions,
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: groupPermissions.Delete, Change: groupPermissions.Change},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: permissionPermissions.Delete, Change: permissionPermissions.Change},
		MaxPasswordBytes:      1024, ValidatePassword: passwords,
	}
	result.Catalog = accountmodel.CatalogConfig[models.Group, models.Permission]{
		Group:              accountmodel.CatalogModelConfig[models.Group]{ReadFields: []string{"name", "permissions"}, WriteFields: []string{"name", "permissions"}, Permissions: groupPermissions},
		Permission:         accountmodel.CatalogModelConfig[models.Permission]{ReadFields: []string{"code", "name"}, WriteFields: []string{"code", "name"}, Permissions: permissionPermissions},
		AccountPermissions: accountmodel.DeletionPermissions{Delete: accountPermissions.Delete, Change: accountPermissions.Change},
	}
	result.SessionStorage.MaxRecords = 256
	return result, nil
}

// FirstAccountPolicy selects operator prompts explicitly. Server identity,
// active/staff/superuser and revision stay with the first-account owner. A
// custom model must include every additional required profile field here.
func FirstAccountPolicy() (accountmodel.CreationPolicy[models.User], error) {
	config, err := Policy()
	if err != nil {
		return accountmodel.CreationPolicy[models.User]{}, err
	}
	return accountmodel.CreationPolicy[models.User]{
		Fields: []string{"username", "email"}, MaxPasswordBytes: config.Accounts.MaxPasswordBytes,
		ValidatePassword: config.Accounts.ValidatePassword, Validate: config.Accounts.ValidateCreate,
		CaseInsensitiveLogin: config.Accounts.CaseInsensitiveCreationLogin,
		PrincipalID:          config.Accounts.PrincipalID, Now: config.Accounts.Now,
	}, nil
}
