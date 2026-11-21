package accounts

import (
	access "example.com/started-site/apps/access/models"
	people "example.com/started-site/apps/people/models"
	"example.com/started-site/project"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
)

type Runtime = accountmodel.Runtime[people.Member, access.Team, access.Grant]

func OpenAPIAccess() (api.PermissionPolicy, error) {
	return api.NewPermissionPolicy([]auth.Permission{"people.read_schema"}, nil)
}

func Policy() (accountmodel.RuntimeConfig[people.Member, access.Team, access.Grant], error) {
	var config accountmodel.RuntimeConfig[people.Member, access.Team, access.Grant]
	binding, err := project.BindAccount()
	if err != nil {
		return config, err
	}
	passwords, err := binding.DefaultPasswordValidator(accountmodel.PasswordAttribute{Field: "contact", Label: "contact"})
	if err != nil {
		return config, err
	}
	member := accountmodel.AccountPermissions{View: "people.view_member", Add: "people.add_member", Change: "people.change_member", Delete: "people.delete_member"}
	team := accountmodel.CatalogPermissions{View: "access.view_team", Add: "access.add_team", Change: "access.change_team", Delete: "access.delete_team"}
	grant := accountmodel.CatalogPermissions{View: "access.view_grant", Add: "access.add_grant", Change: "access.change_grant", Delete: "access.delete_grant"}
	fields := []string{"contact", "region", "enabled", "root", "teams", "grants"}
	config.Accounts = accountmodel.AccountManagementConfig[people.Member]{
		ReadFields: append([]string{"seen"}, fields...), CreateFields: fields, EditFields: fields,
		Permissions: member, MaxPasswordBytes: 1024, ValidatePassword: passwords,
		GroupPermissions:      accountmodel.DeletionPermissions{Delete: team.Delete, Change: team.Change},
		PermissionPermissions: accountmodel.DeletionPermissions{Delete: grant.Delete, Change: grant.Change},
	}
	config.Catalog = accountmodel.CatalogConfig[access.Team, access.Grant]{
		Group:              accountmodel.CatalogModelConfig[access.Team]{ReadFields: []string{"title", "priority", "grants"}, WriteFields: []string{"title", "grants"}, Permissions: team},
		Permission:         accountmodel.CatalogModelConfig[access.Grant]{ReadFields: []string{"key", "title", "description"}, WriteFields: []string{"key", "title"}, Permissions: grant},
		AccountPermissions: accountmodel.DeletionPermissions{Delete: member.Delete, Change: member.Change},
	}
	config.SessionStorage.MaxRecords = 256
	return config, nil
}

func firstAccountConfig(management accountmodel.AccountManagementConfig[people.Member]) accountmodel.FirstAccountConfig[people.Member] {
	return accountmodel.FirstAccountConfig[people.Member]{CreationPolicy: accountmodel.CreationPolicy[people.Member]{
		Fields: []string{"contact", "region"}, MaxPasswordBytes: management.MaxPasswordBytes,
		ValidatePassword: management.ValidatePassword, Validate: management.ValidateCreate,
		CaseInsensitiveLogin: management.CaseInsensitiveCreationLogin,
		PrincipalID:          management.PrincipalID, Now: management.Now,
	}}
}
