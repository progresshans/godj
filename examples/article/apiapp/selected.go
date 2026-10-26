package apiapp

import (
	"context"
	"errors"
	"reflect"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
)

type SelectedBackend interface {
	articleapp.Backend
	db.SnapshotReader
	db.CoordinatedAtomic
}

type ScopedAccountAuthorizer interface {
	AuthorizeIn(context.Context, db.Queryer, auth.Principal, auth.Authorizer, auth.Permission, ...auth.Permission) (auth.Principal, bool, error)
}

type selectedAccess struct {
	backend  SelectedBackend
	accounts ScopedAccountAuthorizer
	overlay  auth.Authorizer
}

var errArticleAccessDenied = errors.New("article API: current account permission denied")

// NewSelected composes the same Article representation and business rules with
// current selected-account authorization in each native read/write scope. API
// writes do not create Admin audit events. The backend remains caller-owned.
func NewSelected(backend SelectedBackend, accounts ScopedAccountAuthorizer, overlay auth.Authorizer, authentication api.Authentication) (*Application, error) {
	if nilDependency(backend) || nilDependency(accounts) || nilDependency(overlay) {
		return nil, errors.New("article API: selected backend and account authorizer are required")
	}
	application, err := New(backend, authentication)
	if err != nil {
		return nil, err
	}
	application.selected = &selectedAccess{backend, accounts, overlay}
	return application, nil
}

type articleAccessResult[T any] struct {
	value   T
	allowed bool
}

func withArticleAccess[T any](ctx context.Context, application *Application, actor auth.Principal, permission auth.Permission, write bool, run func(context.Context, articleapp.Repository) (T, error)) (T, error) {
	if application.selected == nil {
		return run(ctx, application.repository)
	}
	selected := application.selected
	var zero T
	borrow := func(work context.Context, scope db.Queryer) (articleAccessResult[T], error) {
		_, allowed, err := selected.accounts.AuthorizeIn(work, scope, actor, selected.overlay, permission)
		if err != nil || !allowed {
			return articleAccessResult[T]{}, err
		}
		repository, err := articleapp.InScope(work, scope)
		if err != nil {
			return articleAccessResult[T]{}, err
		}
		value, err := run(work, repository)
		return articleAccessResult[T]{value, true}, err
	}
	var result articleAccessResult[T]
	var err error
	if write {
		result, err = db.CoordinatedAtomicResult(ctx, selected.backend, func(work context.Context, scope db.Session) (articleAccessResult[T], error) {
			return borrow(work, scope)
		})
	} else {
		result, err = db.ReadSnapshotResult(ctx, selected.backend, borrow)
	}
	if err != nil {
		return zero, err
	}
	if !result.allowed {
		return zero, errArticleAccessDenied
	}
	return result.value, nil
}

type articleLookup struct {
	value articlemodels.Article
	found bool
}

func (a *Application) getSelected(ctx context.Context, actor auth.Principal, permission auth.Permission, id int64) (articlemodels.Article, bool, error) {
	result, err := withArticleAccess(ctx, a, actor, permission, false, func(work context.Context, repository articleapp.Repository) (articleLookup, error) {
		value, found, err := repository.Get(work, id)
		return articleLookup{value, found}, err
	})
	return result.value, result.found, err
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
		return reflect.ValueOf(value).IsNil()
	}
	return false
}
