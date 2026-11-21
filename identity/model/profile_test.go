package model_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

type untouchedProfileBackend struct {
	untouchedAccountBackend
	writes int
}

func (backend *untouchedProfileBackend) CoordinatedAtomic(context.Context, func(db.Session) error) error {
	backend.writes++
	return errors.New("unexecuted native transaction")
}

func TestAccountProfilePolicyAndInputRejectBeforeIO(t *testing.T) {
	binding, err := accountmodel.Bind(modeldef.AccountRole(), models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	backend := &untouchedProfileBackend{}
	store, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	config := accountmodel.ProfileConfig[models.User]{Fields: []string{"first_name", "username"}, ChangePermission: "account.change"}
	for _, fields := range [][]string{nil, {"first_name", "first_name"}, {"unknown"}, {"id"}, {"principal_id"}, {"encoded_password"}, {"revision"}, {"last_login"}, {"date_joined"}, {"active"}, {"staff"}, {"superuser"}, {"groups"}, {"permissions"}} {
		invalid := config
		invalid.Fields = fields
		if editor, err := accountmodel.NewProfileEditor(store, auth.PrincipalAuthorizer{}, invalid); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) || editor != nil {
			t.Fatal("invalid profile fields accepted", fields, err)
		}
	}
	for _, overlay := range []auth.Authorizer{nil, (*auth.PrincipalAuthorizer)(nil)} {
		if editor, err := accountmodel.NewProfileEditor(store, overlay, config); err == nil || editor != nil {
			t.Fatal("missing overlay accepted")
		}
	}
	for _, permission := range []auth.Permission{"", "invalid permission"} {
		invalid := config
		invalid.ChangePermission = permission
		if editor, err := accountmodel.NewProfileEditor(store, auth.PrincipalAuthorizer{}, invalid); err == nil || editor != nil {
			t.Fatal("invalid permission accepted")
		}
	}
	readOnly, err := accountmodel.NewCredentialStore(&untouchedAccountBackend{}, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	if editor, err := accountmodel.NewProfileEditor(readOnly, auth.PrincipalAuthorizer{}, config); err == nil || editor != nil {
		t.Fatal("unfenced backend accepted")
	}
	editor, err := accountmodel.NewProfileEditor(store, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Fields[0] = "encoded_password"
	editor.Fields()[0] = "encoded_password"
	if editor.Fields()[0] != "first_name" {
		t.Fatal("profile selection aliases caller")
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "actor", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []orm.ModelInput[models.User]{{}}
	for name, value := range map[string]query.Value{
		"principal_id": query.String("forged"), "encoded_password": query.String("private-secret"),
		"revision": query.Integer(7), "date_joined": query.DateTime(time.Now()), "last_login": query.Null(),
		"active": query.Boolean(false), "staff": query.Boolean(true), "superuser": query.Boolean(true),
	} {
		input, err := models.UserObjects.Input(map[string]query.Value{name: value})
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input)
	}
	valid, err := models.UserObjects.Input(map[string]query.Value{"first_name": query.String("new")})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []query.Value{query.Integer(1), query.Null()} {
		input, err := valid.WithTargetKey(key)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input)
	}
	collection, err := valid.WithCollections(map[string][]int64{"groups": {}})
	if err != nil {
		t.Fatal(err)
	}
	inputs = append(inputs, collection)
	foreign, err := orm.NewManager[models.User](changedProfileDescriptor{}).Input(map[string]query.Value{"first_name": query.String("new")})
	if err != nil {
		t.Fatal(err)
	}
	inputs = append(inputs, foreign)
	for _, input := range inputs {
		result, err := editor.Update(t.Context(), principal, 0, 1, input)
		if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidInput}) || result.Revision != 0 {
			t.Fatal("invalid profile input admitted", err)
		}
	}
	for _, revision := range []int64{0, -1, math.MaxInt64} {
		if result, err := editor.Update(t.Context(), principal, 0, revision, valid); err == nil || result.Revision != 0 {
			t.Fatal("invalid revision admitted")
		}
	}
	for name, value := range map[string]string{"first_name": strings.Repeat("x", 151), "username": strings.Repeat("㍿", 256)} {
		input, err := models.UserObjects.Input(map[string]query.Value{name: query.String(value)})
		if err != nil {
			t.Fatal(err)
		}
		if result, err := editor.Update(t.Context(), principal, 0, 1, input); result.Revision != 0 {
			t.Fatal("invalid text published result")
		} else if _, rejected := validation.Rejected(err); !rejected {
			t.Fatal("text bounds not enforced", err)
		}
	}
	if _, err := editor.Update(nil, principal, 0, 1, valid); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := editor.Update(ctx, principal, 0, 1, valid); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if _, err := editor.Update(t.Context(), auth.Anonymous(), 0, 1, valid); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
		t.Fatal("anonymous input admitted", err)
	}
	if backend.calls != 0 || backend.writes != 0 {
		t.Fatal("invalid profile work performed I/O")
	}
	// A valid intent reaches the configured owner, so these negatives cannot
	// pass solely because every editor is unusable or skips all writes.
	if result, err := editor.Update(t.Context(), principal, 0, 1, valid); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePersistence}) || result.Revision != 0 || backend.writes != 1 {
		t.Fatal("valid input did not reach its owner", err)
	}
}

type changedProfileDescriptor struct{ models.UserDescriptor }

func (changedProfileDescriptor) Metadata() ir.Model {
	model := (models.UserDescriptor{}).Metadata()
	model.DBTable = "foreign_accounts"
	return model
}
