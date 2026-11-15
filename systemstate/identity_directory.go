package systemstate

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
)

// identityDirectory belongs only to the historical systemstate runtime. Its
// caller verifies the original ownership receipt on every scope, including
// retirement after handoff. Selected accounts use identity/model instead.
type identityDirectory struct{ state *identityDirectoryState }

type identityDirectoryState struct {
	backend   db.SnapshotReader
	relations project.Relations
}

func (identityDirectory) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("systemstate.identityDirectory{redacted}"))
}

func (*identityDirectory) String() string   { return "systemstate.identityDirectory{redacted}" }
func (*identityDirectory) GoString() string { return "systemstate.identityDirectory{redacted}" }

func newIdentityDirectory(backend db.SnapshotReader) (*identityDirectory, error) {
	if isNilInterface(backend) {
		return nil, &auth.Error{Code: auth.CodeInvalidConfig, Field: "identity_backend", Detail: "identity snapshot backend is nil"}
	}
	relations, err := project.BindRelations()
	if err != nil {
		return nil, identityReadFailure(err)
	}
	return &identityDirectory{state: &identityDirectoryState{backend: backend, relations: relations}}, nil
}

func (directory *identityDirectory) CredentialByID(ctx context.Context, principalID string) (auth.Credential, bool, error) {
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil {
		return auth.Credential{}, false, err
	}
	return directory.lookup(ctx, models.UserFields.PrincipalID.Exact(principalID))
}

func (directory *identityDirectory) CredentialByUsername(ctx context.Context, username string) (auth.Credential, bool, error) {
	if err := auth.ValidateUsername(username); err != nil {
		return auth.Credential{}, false, err
	}
	return directory.lookup(ctx, models.UserFields.Username.Exact(username))
}

func (directory *identityDirectory) lookup(ctx context.Context, predicate orm.Predicate[models.User]) (auth.Credential, bool, error) {
	if ctx == nil {
		return auth.Credential{}, false, &auth.Error{Code: auth.CodeInvalidInput, Field: "context", Detail: "identity read context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return auth.Credential{}, false, err
	}
	if directory == nil || directory.state == nil || directory.state.backend == nil {
		return auth.Credential{}, false, &auth.Error{Code: auth.CodeInvalidConfig, Field: "identity_backend", Detail: "identity directory is uninitialized"}
	}
	var candidate auth.Credential
	var found bool
	calls := 0
	read := func(reader db.Queryer) error {
		calls++
		if calls != 1 || isNilInterface(reader) {
			return &auth.Error{Code: auth.CodeCredential, Detail: "identity backend violated the snapshot callback contract"}
		}
		row, present, err := models.UserObjects.Using(reader).Filter(predicate).OrderBy(models.UserFields.ID.Asc()).First(ctx)
		if err != nil || !present {
			return err
		}
		candidate, err = directory.credentialFromRow(ctx, reader, row)
		if err != nil {
			return err
		}
		found = true
		return nil
	}
	var callbackErr error
	err := directory.state.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		callbackErr = read(reader)
		return callbackErr
	})
	err = errors.Join(err, callbackErr, ctx.Err())
	if err != nil {
		return auth.Credential{}, false, identityReadFailure(err)
	}
	if calls != 1 {
		return auth.Credential{}, false, &auth.Error{Code: auth.CodeCredential, Detail: "identity backend did not execute exactly one snapshot callback"}
	}
	return candidate, found, nil
}

// credentialFromRow uses the caller-owned coherent read or coordinated write scope.
func (directory *identityDirectory) credentialFromRow(ctx context.Context, reader db.Queryer, row models.User) (auth.Credential, error) {
	if err := validateUserProfileText(row); err != nil {
		return auth.Credential{}, identityReadFailure(err)
	}
	if row.Revision <= 0 {
		return auth.Credential{}, &auth.Error{Code: auth.CodeCredential, Detail: "stored identity revision is invalid"}
	}
	relations := directory.state.relations.IdentityPermission
	permissionQuery, err := models.PermissionObjects.Using(reader).Filter(orm.Or(
		relations.Users.ID.Exact(row.ID), relations.Groups.Users().ID.Exact(row.ID),
	)).Distinct().OrderBy(models.PermissionFields.Code.Asc()).Limit(auth.MaximumPermissions + 1)
	if err != nil {
		return auth.Credential{}, err
	}
	permissions, err := permissionQuery.All(ctx)
	if err != nil {
		return auth.Credential{}, err
	}
	codes := make([]auth.Permission, len(permissions))
	for index, permission := range permissions {
		if permission.Revision <= 0 {
			return auth.Credential{}, &auth.Error{Code: auth.CodeCredential, Detail: "stored permission revision is invalid"}
		}
		codes[index] = auth.Permission(permission.Code)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: row.PrincipalID, Active: row.Active, Staff: row.Staff, Superuser: row.Superuser, Permissions: codes})
	if err != nil {
		return auth.Credential{}, err
	}
	credential, err := auth.NewCredential(row.Username, row.EncodedPassword, principal)
	if err != nil {
		return auth.Credential{}, err
	}
	return credential, nil
}

func identityReadFailure(cause error) error {
	return &auth.Error{Code: auth.CodeCredential, Detail: "identity snapshot read failed", Cause: cause}
}

func (directory *identityDirectory) LoginInput() (auth.LoginInput, error) {
	binding, err := project.BindAccount()
	if err != nil {
		return auth.LoginInput{}, err
	}
	return binding.LoginInput()
}
