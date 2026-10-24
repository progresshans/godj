package modeldef

import "github.com/progresshans/godj/schema/ir"

// AccountRole declares the built-in account's meanings using the same IR
// references as a custom model. The host selects this role explicitly; merely
// installing the app does not select, provision or transition an account.
func AccountRole() ir.AccountRole {
	return ir.AccountRole{
		Model:       ir.ModelIdentity{AppLabel: AppLabel, ModelName: "user"},
		PrincipalID: "principal_id", Login: "username", Password: "encoded_password",
		Revision: "revision", LastLogin: "last_login", CreatedAt: "date_joined", Email: "email",
		Active: ir.AccountBoolean{Field: "active"}, Staff: ir.AccountBoolean{Field: "staff"}, Superuser: ir.AccountBoolean{Field: "superuser"},
		Groups: "groups", Permissions: "permissions",
		WriteLoginNormalization: ir.AccountUnicode16NFKC, AuthenticateNormalization: ir.AccountExact,
		WriteEmailNormalization: ir.AccountUnicode16EmailDomain,
		Catalog: ir.AccountCatalog{
			Group:     ir.ModelIdentity{AppLabel: AppLabel, ModelName: "group"},
			GroupName: "name", GroupRevision: "revision", GroupPermissions: "permissions",
			Permission:     ir.ModelIdentity{AppLabel: AppLabel, ModelName: "permission"},
			PermissionCode: "code", PermissionName: "name", PermissionRevision: "revision",
		},
	}
}
