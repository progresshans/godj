package adminapp

import (
	"context"
	"fmt"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
)

// SelectedBackend owns native read snapshots and coordinated writes. Article
// and account state must share this database; a process-local mutex is not a
// replacement for the native cross-runtime fence.
type SelectedBackend interface {
	db.SnapshotReader
	db.CoordinatedAtomic
}

type ScopedAccountAuthorizer interface {
	AuthorizeIn(context.Context, db.Queryer, auth.Principal, auth.Authorizer, auth.Permission, ...auth.Permission) (auth.Principal, bool, error)
}

// SelectedService rechecks the complete account actor in the same native scope
// as each Article operation. Its audit belongs to that actor's exact domain;
// historical domains and the old operator audit are never adopted implicitly.
type SelectedService struct {
	backend  SelectedBackend
	accounts ScopedAccountAuthorizer
	overlay  auth.Authorizer
}

func (SelectedService) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("article.admin.SelectedService{redacted}"))
}

func NewSelectedService(backend SelectedBackend, accounts ScopedAccountAuthorizer, overlay auth.Authorizer) (SelectedService, error) {
	service := SelectedService{backend, accounts, overlay}
	if !service.Valid() {
		return SelectedService{}, invalid("service", "native backend and selected account authorizer are required")
	}
	return service, nil
}

func (s SelectedService) Valid() bool {
	return !interfaceNil(s.backend) && !interfaceNil(s.accounts) && !interfaceNil(s.overlay)
}

func (s SelectedService) authorize(ctx context.Context, scope db.Queryer, actor auth.Principal, permission auth.Permission, read bool) (auth.Principal, bool, error) {
	current, allowed, err := s.accounts.AuthorizeIn(ctx, scope, actor, s.overlay, permission)
	if err == nil && !allowed && read {
		current, allowed, err = s.accounts.AuthorizeIn(ctx, scope, actor, s.overlay, ArticleChangePermission)
	}
	if err != nil {
		return auth.Principal{}, false, err
	}
	if !allowed || !current.Staff() {
		return auth.Principal{}, false, nil
	}
	return current, true, nil
}

type selectedResult[T any] struct {
	value   T
	allowed bool
}

func selectedCompletion[T any](result selectedResult[T], err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	if !result.allowed {
		return zero, admin.NewOperationError(admin.OperationDenied, nil)
	}
	return result.value, nil
}

func selectedRead[T any](ctx context.Context, s SelectedService, actor auth.Principal, run func(context.Context, Service, auth.Principal) (T, error)) (T, error) {
	var zero T
	if !s.Valid() {
		return zero, invalid("service", "selected account service is invalid")
	}
	result, err := db.ReadSnapshotResult(ctx, s.backend, func(work context.Context, scope db.Queryer) (selectedResult[T], error) {
		current, allowed, err := s.authorize(work, scope, actor, ArticleViewPermission, true)
		if err != nil || !allowed {
			return selectedResult[T]{}, err
		}
		service, err := borrowedArticleService(work, scope, current)
		if err != nil {
			return selectedResult[T]{}, err
		}
		value, err := run(work, service, current)
		return selectedResult[T]{value, true}, err
	})
	return selectedCompletion(result, err)
}

func selectedWrite[T any](ctx context.Context, s SelectedService, actor auth.Principal, permission auth.Permission, run func(context.Context, Service, auth.Principal) (T, error)) (T, error) {
	var zero T
	if !s.Valid() {
		return zero, invalid("service", "selected account service is invalid")
	}
	result, err := db.CoordinatedAtomicResult(ctx, s.backend, func(work context.Context, scope db.Session) (selectedResult[T], error) {
		current, allowed, err := s.authorize(work, scope, actor, permission, false)
		if err != nil || !allowed {
			return selectedResult[T]{}, err
		}
		service, err := borrowedArticleService(work, scope, current)
		if err != nil {
			return selectedResult[T]{}, err
		}
		value, err := run(work, service, current)
		return selectedResult[T]{value, true}, err
	})
	return selectedCompletion(result, err)
}

func (s SelectedService) List(ctx context.Context, actor auth.Principal, options articleapp.ListOptions) (articleapp.Page, error) {
	return selectedRead(ctx, s, actor, func(work context.Context, service Service, current auth.Principal) (articleapp.Page, error) {
		return service.List(work, current, options)
	})
}

func (s SelectedService) Get(ctx context.Context, actor auth.Principal, id int64) (articlemodels.Article, bool, error) {
	type result struct {
		value articlemodels.Article
		found bool
	}
	value, err := selectedRead(ctx, s, actor, func(work context.Context, service Service, current auth.Principal) (result, error) {
		value, found, err := service.Get(work, current, id)
		return result{value, found}, err
	})
	return value.value, value.found, err
}

func (s SelectedService) Create(ctx context.Context, actor auth.Principal, input orm.ModelInput[articlemodels.Article]) (articlemodels.Article, error) {
	return selectedWrite(ctx, s, actor, ArticleAddPermission, func(work context.Context, service Service, current auth.Principal) (articlemodels.Article, error) {
		return service.Create(work, current, input)
	})
}

func (s SelectedService) Update(ctx context.Context, actor auth.Principal, id int64, input orm.ModelInput[articlemodels.Article]) (articlemodels.Article, []string, error) {
	type result struct {
		value   articlemodels.Article
		changed []string
	}
	value, err := selectedWrite(ctx, s, actor, ArticleChangePermission, func(work context.Context, service Service, current auth.Principal) (result, error) {
		value, changed, err := service.Update(work, current, id, input)
		return result{value, changed}, err
	})
	return value.value, value.changed, err
}

func (s SelectedService) Delete(ctx context.Context, actor auth.Principal, id int64) (articlemodels.Article, error) {
	return selectedWrite(ctx, s, actor, ArticleDeletePermission, func(work context.Context, service Service, current auth.Principal) (articlemodels.Article, error) {
		return service.Delete(work, current, id)
	})
}

func (s SelectedService) Publish(ctx context.Context, actor auth.Principal, ids []int64) (articleapp.PublishResult, error) {
	return selectedWrite(ctx, s, actor, ArticleChangePermission, func(work context.Context, service Service, current auth.Principal) (articleapp.PublishResult, error) {
		return service.Publish(work, current, ids)
	})
}

func (s SelectedService) HistoryLimited(ctx context.Context, actor auth.Principal, id int64, limit int) ([]admin.AuditEntry, error) {
	return selectedRead(ctx, s, actor, func(work context.Context, service Service, current auth.Principal) ([]admin.AuditEntry, error) {
		return service.HistoryLimited(work, current, id, limit)
	})
}

func borrowedArticleService(ctx context.Context, scope db.Queryer, actor auth.Principal) (Service, error) {
	repository, err := articleapp.InScope(ctx, scope)
	if err != nil {
		return Service{}, err
	}
	return Service{repository: &repository, durableAudit: articleDomainAudit{scope, actor}}, nil
}

type articleDomainAudit struct {
	scope db.Queryer
	actor auth.Principal
}

func (a articleDomainAudit) AppendAudit(ctx context.Context, _ db.Session, event admin.PreparedEvent) error {
	scope, ok := a.scope.(db.Session)
	if !ok || event.ActorID() != a.actor.ID() || event.Model() != articleModelIdentity {
		return accountaudit.ErrInvalid
	}
	prepared, err := accountaudit.Prepare(a.actor, event.Model(), event.ObjectID(), event.Action(), event.ChangedFields(), time.Now())
	if err != nil {
		return err
	}
	_, err = accountaudit.AppendIn(ctx, scope, prepared)
	return err
}

func (a articleDomainAudit) AuditHistory(ctx context.Context, model string, id int64, limit int) ([]admin.AuditEntry, error) {
	domain, bound := a.actor.CredentialDomain()
	if !bound || model != articleModelIdentity {
		return nil, accountaudit.ErrInvalid
	}
	entries, err := accountaudit.HistoryIn(ctx, a.scope, domain, model, id, limit)
	if err != nil {
		return nil, err
	}
	result := make([]admin.AuditEntry, len(entries))
	for i, entry := range entries {
		result[i] = admin.AuditEntry{Sequence: uint64(entry.Sequence()), ActorID: entry.ActorID(), Model: entry.Model(), ObjectID: entry.ObjectID(), Action: entry.Action(), ChangedFields: entry.ChangedFields()}
	}
	return result, nil
}
