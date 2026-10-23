package identity

import (
	"context"
	"math"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
)

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

type GroupProfile struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
}

type GroupDetails struct {
	GroupProfile
	PermissionIDs []int64 `json:"permissions"`
}

type GroupPage struct {
	Groups []GroupProfile `json:"groups"`
	Total  int64          `json:"total"`
}

type PermissionProfile struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
}

type PermissionPage struct {
	Permissions []PermissionProfile `json:"permissions"`
	Total       int64               `json:"total"`
}

// CatalogDeletion reveals no profile or membership data to a delete-only actor.
type CatalogDeletion struct {
	ID          int64 `json:"id"`
	Revision    int64 `json:"revision"`
	DeletedRows int64 `json:"deleted_rows"`
}

func validCatalogRevision(id, revision int64, field string) error {
	if id <= 0 || revision <= 0 || revision == math.MaxInt64 {
		return managementError(CodeInvalidInput, field, nil)
	}
	return nil
}

func (manager *Manager) auditCatalog(ctx context.Context, session db.Session, actorID, object string, id int64, action admin.Action, fields []string) error {
	event, err := admin.PrepareEvent(actorID, "godj_identity."+object, id, action, fields, "")
	if err != nil {
		return err
	}
	return manager.state.backend.AppendAudit(ctx, session, event)
}

func groupProfile(row models.Group) (GroupProfile, error) {
	if row.ID <= 0 || row.Revision <= 0 || validateCatalogName(row.Name, 150) != nil {
		return GroupProfile{}, managementError(CodePersistence, "group", nil)
	}
	return GroupProfile{ID: row.ID, Name: row.Name, Revision: row.Revision}, nil
}

func permissionProfile(row models.Permission) (PermissionProfile, error) {
	_, err := auth.NewPermission(row.Code)
	if row.ID <= 0 || row.Revision <= 0 || err != nil || validateCatalogName(row.Name, 255) != nil {
		return PermissionProfile{}, managementError(CodePersistence, "permission", nil)
	}
	return PermissionProfile{ID: row.ID, Code: row.Code, Name: row.Name, Revision: row.Revision}, nil
}
