package orm_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	hostproject "github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type collectionPreparationScope struct {
	db.RelationSession
	queries, closes, writes int
	expired                 error
}

func (scope *collectionPreparationScope) ValidateSession(context.Context) error { return scope.expired }
func (scope *collectionPreparationScope) Query(context.Context, query.Plan) (db.Rows, error) {
	scope.queries++
	return &emptyCollectionRows{scope: scope}, nil
}
func (scope *collectionPreparationScope) InsertOnConflict(context.Context, query.ConflictInsertPlan) (bool, error) {
	scope.writes++
	return false, errors.New("unexpected insertion")
}

type emptyCollectionRows struct{ scope *collectionPreparationScope }

func (*emptyCollectionRows) Next() bool        { return false }
func (*emptyCollectionRows) Scan(...any) error { return errors.New("unexpected scan") }
func (*emptyCollectionRows) Err() error        { return nil }
func (rows *emptyCollectionRows) Close() error { rows.scope.closes++; return nil }

func TestCollectionChangeCompositionRejectsInvalidOwnershipBeforeIO(t *testing.T) {
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	host, err := hostproject.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	owner := models.NewUserWithID(0)
	change, err := collections.IdentityUserGroups.SetKeysChange(owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := host.IdentityUserPermissions.SetKeysChange(owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unbound", "duplicate", "foreign_source", "nil_context", "canceled", "nil_session", "typed_nil_session", "root_session", "expired"} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			scope := &collectionPreparationScope{}
			var session db.RelationSession = scope
			changes := []orm.CollectionChange{change}
			expired := errors.New("expired borrowed collection scope")
			switch name {
			case "unbound":
				changes = []orm.CollectionChange{{}}
			case "duplicate":
				changes = append(changes, change)
			case "foreign_source":
				changes = append(changes, foreign)
			case "nil_context":
				ctx = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil_session":
				session = nil
			case "typed_nil_session":
				session = (*collectionPreparationScope)(nil)
			case "root_session":
				session = &struct{ db.RelationSession }{}
			case "expired":
				scope.expired = expired
			}
			result, err := orm.PrepareCollectionChangesInSession(ctx, session, changes...)
			if err == nil || scope.queries != 0 || scope.writes != 0 || len(result.Effects().Insertions()) != 0 {
				t.Fatal("invalid collection ownership reached I/O or published a preparation", err)
			}
			if err := result.Apply(t.Context(), nil); err == nil {
				t.Fatal("failed preparation became executable")
			}
		})
	}
	if _, err := collections.IdentityUserGroups.SetKeysChange(models.User{}, nil); err == nil {
		t.Fatal("missing owner key became a zero stored key")
	}
	if _, err := collections.IdentityUserGroups.SetKeysChange(owner, nil, orm.ManyToManySetOptions[models.UserGroupsLink]{}, orm.ManyToManySetOptions[models.UserGroupsLink]{}); err == nil {
		t.Fatal("multiple set options accepted")
	}
}

func TestPreparedCollectionChangesAreBoundOneAttemptAndPreserveCallbackFailure(t *testing.T) {
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	host, err := hostproject.Bind()
	if err != nil {
		t.Fatal(err)
	}
	owner := models.NewUserWithID(-7)
	change, err := collections.IdentityUserGroups.SetKeysChange(owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "callback", "callback_expiry", "expired_before_apply", "canceled_before_apply"} {
		t.Run(mode, func(t *testing.T) {
			scope := &collectionPreparationScope{}
			prepared, err := orm.PrepareCollectionChangesInSession(t.Context(), scope, change)
			if err != nil {
				t.Fatal(err)
			}
			if err := prepared.ValidateSource(binding); err != nil {
				t.Fatal(err)
			}
			if err := prepared.ValidateSource(host); err == nil {
				t.Fatal("local identity schema substituted for full host source")
			}
			if scope.queries != 1 || scope.closes != 1 || scope.writes != 0 {
				t.Fatal("preparation wrote or retained rows")
			}
			owned := prepared
			cause, expired := errors.New("private callback failure"), errors.New("private expiry")
			ctx := t.Context()
			if mode == "expired_before_apply" {
				scope.expired = expired
			}
			if mode == "canceled_before_apply" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			calls := 0
			err = prepared.Apply(ctx, func(context.Context, orm.CollectionChangeEffects) error {
				calls++
				if mode == "callback_expiry" {
					scope.expired = expired
				}
				if mode == "callback" || mode == "callback_expiry" {
					return cause
				}
				return nil
			})
			if mode == "success" {
				if err != nil || calls != 1 || scope.queries != 2 || scope.closes != 2 {
					t.Fatal("no-op execution lost its postcondition", err)
				}
			} else if err == nil {
				t.Fatal("failed execution reported success")
			}
			if mode == "callback" && err != cause {
				t.Fatal("valid-scope callback error identity changed", err)
			}
			if mode == "callback_expiry" && (!errors.Is(err, cause) || !errors.Is(err, expired)) {
				t.Fatal("callback or lifetime cause lost", err)
			}
			if strings.HasPrefix(mode, "expired_before") || strings.HasPrefix(mode, "canceled_before") {
				if calls != 0 {
					t.Fatal("invalid scope invoked callback")
				}
			}
			before := scope.queries
			if err := owned.Apply(t.Context(), nil); err == nil || scope.queries != before || scope.writes != 0 {
				t.Fatal("copy replayed an attempted batch", err)
			}
			for _, value := range []any{change, prepared, prepared.Effects()} {
				if !strings.Contains(fmt.Sprintf("%#v", value), "redacted") {
					t.Fatal("collection diagnostic exposed private state")
				}
			}
		})
	}
}

func TestPreparedCollectionChangesCoalesceOverlappingForwardReverseInserts(t *testing.T) {
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	user := models.NewUserWithID(0)
	group := models.NewGroupWithID(-7)
	forward, err := collections.IdentityUserGroups.SetKeysChange(user, []int64{group.ID})
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := collections.IdentityGroupUsers.SetKeysChange(group, []int64{user.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, changes := range [][]orm.CollectionChange{{forward, reverse}, {reverse, forward}} {
		scope := &collectionPreparationScope{}
		prepared, err := orm.PrepareCollectionChangesInSession(t.Context(), scope, changes...)
		if err != nil {
			t.Fatal(err)
		}
		effects := prepared.Effects().Insertions()
		if len(effects) != 1 || len(prepared.Effects().Deletions().Rows()) != 0 {
			t.Fatal("one physical pair produced repeated insertions")
		}
		source, sourceOK := effects[0].Values["source"].Integer()
		target, targetOK := effects[0].Values["target"].Integer()
		if !sourceOK || !targetOK || source != user.ID || target != group.ID || scope.queries != 2 || scope.closes != 2 || scope.writes != 0 {
			t.Fatal("forward/reverse composition lost signed endpoint identity")
		}
	}
}
