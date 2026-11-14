package identity

import "github.com/progresshans/godj/auth"

// Built-in catalog permission names remain available to selected-model
// application policies. Catalog I/O belongs to identity/model.Catalog.
const (
	ViewGroup        auth.Permission = "godj_identity.view_group"
	AddGroup         auth.Permission = "godj_identity.add_group"
	ChangeGroup      auth.Permission = "godj_identity.change_group"
	DeleteGroup      auth.Permission = "godj_identity.delete_group"
	ViewPermission   auth.Permission = "godj_identity.view_permission"
	AddPermission    auth.Permission = "godj_identity.add_permission"
	ChangePermission auth.Permission = "godj_identity.change_permission"
	DeletePermission auth.Permission = "godj_identity.delete_permission"
)
