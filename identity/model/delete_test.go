package model_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	hostdef "github.com/progresshans/godj/conformance/identityfixture/modeldef"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

func unitDeletionConfig() accountmodel.AccountDeleteConfig {
	return accountmodel.AccountDeleteConfig{
		Account:    accountmodel.DeletionPermissions{Delete: "account.delete", Change: "account.change", Additional: []auth.Permission{"account.confirm"}},
		Group:      accountmodel.DeletionPermissions{Delete: "catalog.delete_group", Change: "catalog.change_group"},
		Permission: accountmodel.DeletionPermissions{Delete: "catalog.delete_permission", Change: "catalog.change_permission"},
	}
}

func TestAccountDeletionCompositionAndInputRejectBeforeIO(t *testing.T) {
	backend := &untouchedCatalogBackend{}
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	policies, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	build := func(store *accountmodel.SessionStore[models.User], expected accountmodel.Binding[models.User], policy orm.RelationDeleter[models.User], overlay auth.Authorizer, config accountmodel.AccountDeleteConfig) (*accountmodel.AccountDeleter[models.User], error) {
		return accountmodel.NewAccountDeleter(store, expected, models.GroupDescriptor{}, models.PermissionDescriptor{}, policy, overlay, config)
	}
	config := unitDeletionConfig()
	clockCalls := 0
	config.Now = func() time.Time { clockCalls++; return time.Now() }
	for _, item := range []struct {
		name   string
		mutate func(*accountmodel.AccountDeleteConfig)
	}{
		{"account_delete", func(c *accountmodel.AccountDeleteConfig) { c.Account.Delete = "" }},
		{"account_change", func(c *accountmodel.AccountDeleteConfig) { c.Account.Change = "" }},
		{"group_delete", func(c *accountmodel.AccountDeleteConfig) { c.Group.Delete = "" }},
		{"permission_change", func(c *accountmodel.AccountDeleteConfig) { c.Permission.Change = "Bad Permission" }},
		{"additional_duplicate", func(c *accountmodel.AccountDeleteConfig) { c.Account.Additional = []auth.Permission{c.Account.Delete} }},
	} {
		t.Run(item.name, func(t *testing.T) {
			config := unitDeletionConfig()
			item.mutate(&config)
			value, err := build(store, binding, policies.IdentityUser, auth.PrincipalAuthorizer{}, config)
			if value != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
				t.Fatal("invalid deletion policy accepted", err)
			}
		})
	}
	foreign, err := hostproject.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	host, err := hostdef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	otherSource, err := accountmodel.Bind(modeldef.AccountRole(), models.UserDescriptor{}, models.GoDjRelationSchema(), host)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name     string
		store    *accountmodel.SessionStore[models.User]
		expected accountmodel.Binding[models.User]
		policy   orm.RelationDeleter[models.User]
		overlay  auth.Authorizer
	}{
		{"nil_sessions", nil, binding, policies.IdentityUser, auth.PrincipalAuthorizer{}},
		{"unbound_expected", store, accountmodel.Binding[models.User]{}, policies.IdentityUser, auth.PrincipalAuthorizer{}},
		{"different_host", store, otherSource, policies.IdentityUser, auth.PrincipalAuthorizer{}},
		{"different_policy", store, binding, foreign.AccountsUser, auth.PrincipalAuthorizer{}},
		{"unbound_policy", store, binding, orm.RelationDeleter[models.User]{}, auth.PrincipalAuthorizer{}},
		{"nil_overlay", store, binding, policies.IdentityUser, nil},
		{"typed_nil_overlay", store, binding, policies.IdentityUser, (*auth.PrincipalAuthorizer)(nil)},
	} {
		t.Run(item.name, func(t *testing.T) {
			value, err := build(item.store, item.expected, item.policy, item.overlay, config)
			if value != nil || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
				t.Fatal("foreign deletion owner accepted", err)
			}
		})
	}
	deleter, err := build(store, binding, policies.IdentityUser, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Account.Additional[0] = "foreign.permission"
	deleter.Permissions()[0] = "foreign.permission"
	if !reflect.DeepEqual(deleter.Permissions(), []auth.Permission{"account.delete", "account.confirm"}) {
		t.Fatal("deletion permissions alias caller storage")
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "private-actor", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, call := range []struct {
		ctx      context.Context
		actor    auth.Principal
		revision int64
	}{
		{nil, actor, 1}, {canceled, actor, 1}, {t.Context(), auth.Anonymous(), 1}, {t.Context(), actor, 0}, {t.Context(), actor, -1}, {t.Context(), actor, math.MaxInt64},
	} {
		if result, err := deleter.Delete(call.ctx, call.actor, 0, call.revision); err == nil || result != (accountmodel.AccountDeletion{}) {
			t.Fatal("invalid deletion call accepted", err)
		}
	}
	if backend.calls != 0 || backend.writes != 0 || clockCalls != 0 {
		t.Fatal("construction or invalid deletion performed effects", backend.calls, backend.writes, clockCalls)
	}
	for _, key := range []int64{0, -7} {
		if result, err := deleter.Delete(t.Context(), actor, key, 1); err == nil || result != (accountmodel.AccountDeletion{}) {
			t.Fatal("failed transaction published deletion", err)
		}
	}
	if backend.writes != 2 {
		t.Fatal("present signed key was rejected before the real owner", backend.writes)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%q"} {
		if text := fmt.Sprintf(format, deleter); strings.Contains(text, "account.confirm") || strings.Contains(text, "private-actor") {
			t.Fatal("deletion diagnostic leaked state")
		}
	}
}
