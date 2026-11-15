package identity

import "github.com/progresshans/godj/auth"

// Built-in permission names remain available to selected-model application
// policies. Account and catalog I/O belong to identity/model owners.
const (
	ViewUser   auth.Permission = "godj_identity.view_user"
	AddUser    auth.Permission = "godj_identity.add_user"
	ChangeUser auth.Permission = "godj_identity.change_user"
	DeleteUser auth.Permission = "godj_identity.delete_user"

	ViewGroup        auth.Permission = "godj_identity.view_group"
	AddGroup         auth.Permission = "godj_identity.add_group"
	ChangeGroup      auth.Permission = "godj_identity.change_group"
	DeleteGroup      auth.Permission = "godj_identity.delete_group"
	ViewPermission   auth.Permission = "godj_identity.view_permission"
	AddPermission    auth.Permission = "godj_identity.add_permission"
	ChangePermission auth.Permission = "godj_identity.change_permission"
	DeletePermission auth.Permission = "godj_identity.delete_permission"
)
