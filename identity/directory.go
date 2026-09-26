package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
)

// Profile contains account data that may be explicitly presented after the
// caller's authorization. Encoded passwords and session stamps never appear.
type Profile struct {
	ID          int64      `json:"id"`
	PrincipalID string     `json:"principal_id"`
	Username    string     `json:"username"`
	FirstName   string     `json:"first_name"`
	LastName    string     `json:"last_name"`
	Email       string     `json:"email"`
	Active      bool       `json:"active"`
	Staff       bool       `json:"staff"`
	Superuser   bool       `json:"superuser"`
	DateJoined  time.Time  `json:"date_joined"`
	LastLogin   *time.Time `json:"last_login"`
	Revision    int64      `json:"revision"`
}

// Account is one immutable, coherent observation of stored identity and direct
// plus group permissions. It is data, not an admission decision. Inactive and
// superuser flags remain explicit for the authentication/authorization owner.
type Account struct{ state *accountState }

type accountState struct {
	profile    Profile
	credential auth.Credential
}

func (a Account) value() accountState {
	if a.state == nil {
		return accountState{}
	}
	return *a.state
}

func (Account) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.Account{redacted}"))
}

func (Account) String() string   { return "identity.Account{redacted}" }
func (Account) GoString() string { return "identity.Account{redacted}" }

func (value Account) Profile() Profile {
	profile := value.value().profile
	if profile.LastLogin != nil {
		instant := *profile.LastLogin
		profile.LastLogin = &instant
	}
	return profile
}

func (value Account) Permissions() []auth.Permission {
	return value.value().credential.Principal().Permissions()
}

// MarshalJSON makes accidental encoding safe while keeping the public profile
// explicit. A serializer can select a narrower projection of Profile instead.
func (value Account) MarshalJSON() ([]byte, error) { return json.Marshal(value.Profile()) }

// Directory reads current account state without retaining per-user or group
// caches. Its backend must supply a stable read snapshot, not an ordinary
// READ COMMITTED transaction around separate SELECTs.
type Directory struct{ state *directoryState }

type directoryState struct {
	backend   db.SnapshotReader
	relations project.Relations
}

func (Directory) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.Directory{redacted}"))
}

func (*Directory) String() string   { return "identity.Directory{redacted}" }
func (*Directory) GoString() string { return "identity.Directory{redacted}" }

func NewDirectory(backend db.SnapshotReader) (*Directory, error) {
	if nilIdentityValue(backend) {
		return nil, &auth.Error{Code: auth.CodeInvalidConfig, Field: "identity_backend", Detail: "identity snapshot backend is nil"}
	}
	relations, err := project.BindRelations()
	if err != nil {
		return nil, identityReadFailure(err)
	}
	return &Directory{state: &directoryState{backend: backend, relations: relations}}, nil
}

func (directory *Directory) ByPrincipalID(ctx context.Context, principalID string) (Account, bool, error) {
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil {
		return Account{}, false, err
	}
	return directory.lookup(ctx, models.UserFields.PrincipalID.Exact(principalID))
}

func (directory *Directory) ByUsername(ctx context.Context, username string) (Account, bool, error) {
	if err := auth.ValidateUsername(username); err != nil {
		return Account{}, false, err
	}
	return directory.lookup(ctx, models.UserFields.Username.Exact(username))
}

func (directory *Directory) lookup(ctx context.Context, predicate orm.Predicate[models.User]) (Account, bool, error) {
	if ctx == nil {
		return Account{}, false, &auth.Error{Code: auth.CodeInvalidInput, Field: "context", Detail: "identity read context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return Account{}, false, err
	}
	if directory == nil || directory.state == nil || directory.state.backend == nil {
		return Account{}, false, &auth.Error{Code: auth.CodeInvalidConfig, Field: "identity_backend", Detail: "identity directory is uninitialized"}
	}
	var candidate Account
	var found bool
	calls := 0
	read := func(reader db.Queryer) error {
		calls++
		if calls != 1 || nilIdentityValue(reader) {
			return &auth.Error{Code: auth.CodeCredential, Detail: "identity backend violated the snapshot callback contract"}
		}
		row, present, err := models.UserObjects.Using(reader).Filter(predicate).OrderBy(models.UserFields.ID.Asc()).First(ctx)
		if err != nil || !present {
			return err
		}
		if row.Revision <= 0 {
			return &auth.Error{Code: auth.CodeCredential, Detail: "stored identity revision is invalid"}
		}
		relations := directory.state.relations.IdentityPermission
		permissionQuery, err := models.PermissionObjects.Using(reader).Filter(orm.Or(
			relations.Users.ID.Exact(row.ID), relations.Groups.Users().ID.Exact(row.ID),
		)).Distinct().OrderBy(models.PermissionFields.Code.Asc()).Limit(auth.MaximumPermissions + 1)
		if err != nil {
			return err
		}
		permissions, err := permissionQuery.All(ctx)
		if err != nil {
			return err
		}
		codes := make([]auth.Permission, len(permissions))
		for index, permission := range permissions {
			codes[index] = auth.Permission(permission.Code)
		}
		principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: row.PrincipalID, Active: row.Active, Staff: row.Staff, Superuser: row.Superuser, Permissions: codes})
		if err != nil {
			return err
		}
		credential, err := auth.NewCredential(row.Username, row.EncodedPassword, principal)
		if err != nil {
			return err
		}
		candidate = Account{state: &accountState{credential: credential, profile: Profile{
			ID: row.ID, PrincipalID: row.PrincipalID, Username: row.Username,
			FirstName: row.FirstName, LastName: row.LastName, Email: row.Email,
			Active: row.Active, Staff: row.Staff, Superuser: row.Superuser,
			DateJoined: row.DateJoined, LastLogin: row.LastLogin, Revision: row.Revision,
		}}}
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
		return Account{}, false, identityReadFailure(err)
	}
	if calls != 1 {
		return Account{}, false, &auth.Error{Code: auth.CodeCredential, Detail: "identity backend did not execute exactly one snapshot callback"}
	}
	return candidate, found, nil
}

func identityReadFailure(cause error) error {
	return &auth.Error{Code: auth.CodeCredential, Detail: "identity snapshot read failed", Cause: cause}
}

func nilIdentityValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}
