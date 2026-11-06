package helpdesk

import (
	"context"
	"errors"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
)

// SelectedBackend shares the native account fence with business mutations.
// The application never opens, closes, migrates or owns this connection.
type SelectedBackend interface {
	Backend
	db.CoordinatedAtomic
	db.CoordinatedRelationAtomic
}

type ScopedAccountAuthorizer interface {
	AuthorizeIn(context.Context, db.Queryer, auth.Principal, auth.Authorizer, auth.Permission, ...auth.Permission) (auth.Principal, bool, error)
}

type selectedAccounts struct {
	backend  SelectedBackend
	accounts ScopedAccountAuthorizer
	overlay  auth.Authorizer
}

// NewSelected retains the same business rules and representations, checking
// current account authority in each data snapshot or coordinated transaction.
// Existing audited workflows append to that current credential domain in the
// same transaction. No authentication, hashing or I/O occurs at construction.
func NewSelected(backend SelectedBackend, accounts ScopedAccountAuthorizer, overlay auth.Authorizer, categoryID int64) (*Application, error) {
	if interfaceNil(backend) || interfaceNil(accounts) || interfaceNil(overlay) {
		return nil, errors.New("helpdesk: selected backend, account authorizer and deny overlay are required")
	}
	application, err := New(backend, categoryID)
	if err != nil {
		return nil, err
	}
	application.selected = &selectedAccounts{backend, accounts, overlay}
	application.registry, err = application.AdminRegistry(AdminConfig{})
	if err != nil {
		return nil, err
	}
	return application, nil
}

type accessPolicy struct {
	required     []auth.Permission
	alternatives [][]auth.Permission
}

func requireAccess(permissions ...auth.Permission) accessPolicy {
	var owned []auth.Permission
	for _, permission := range permissions {
		if !slices.Contains(owned, permission) {
			owned = append(owned, permission)
		}
	}
	return accessPolicy{required: owned}
}

func allAccess(policies ...accessPolicy) accessPolicy {
	var result accessPolicy
	for _, policy := range policies {
		result.required = append(result.required, policy.required...)
		for _, alternatives := range policy.alternatives {
			result.alternatives = append(result.alternatives, append([]auth.Permission(nil), alternatives...))
		}
	}
	return result
}

func readAccess(view, change auth.Permission, additional ...auth.Permission) accessPolicy {
	if change == "" {
		return requireAccess(append([]auth.Permission{view}, additional...)...)
	}
	return accessPolicy{required: requireAccess(additional...).required, alternatives: [][]auth.Permission{{view, change}}}
}

// currentIn borrows the operation's actual native scope. Denial is data until
// the owner confirms read cleanup; database failures are never permission data.
func (a *Application) currentIn(ctx context.Context, scope db.Queryer, actor auth.Principal, policy accessPolicy) (auth.Principal, bool, error) {
	if a.selected == nil {
		return actor, true, nil
	}
	selected := a.selected
	authorize := func(permissions ...auth.Permission) (auth.Principal, bool, error) {
		unique := requireAccess(permissions...).required
		if len(unique) == 0 {
			return auth.Principal{}, false, errors.New("helpdesk: empty account authorization policy")
		}
		return selected.accounts.AuthorizeIn(ctx, scope, actor, selected.overlay, unique[0], unique[1:]...)
	}
	var current auth.Principal
	allowed := false
	if len(policy.alternatives) == 0 {
		if len(policy.required) == 0 {
			return current, false, errors.New("helpdesk: empty account authorization policy")
		}
		var err error
		current, allowed, err = authorize(policy.required...)
		if err != nil {
			return auth.Principal{}, false, err
		}
	} else {
		for _, alternatives := range policy.alternatives {
			if len(alternatives) == 0 {
				return auth.Principal{}, false, errors.New("helpdesk: empty alternative account policy")
			}
			allowed = false
			for _, permission := range alternatives {
				var err error
				current, allowed, err = authorize(append([]auth.Permission{permission}, policy.required...)...)
				if err != nil {
					return auth.Principal{}, false, err
				}
				if allowed {
					break
				}
			}
			if !allowed {
				return auth.Principal{}, false, nil
			}
		}
	}
	if !allowed || a.administrative && !current.Staff() {
		return auth.Principal{}, false, nil
	}
	return current, true, nil
}

type accessResult[T any] struct {
	value   T
	allowed bool
}

// Basic CRUD can borrow the base application's queryer directly. The selected
// composition needs a snapshot that also owns the current-account observation.
// Aggregate services instead use readApplication unconditionally because their
// category/count/page contract already requires one snapshot without accounts.
func readBusiness[T any](ctx context.Context, a *Application, actor auth.Principal, policy accessPolicy, run func(context.Context, db.Queryer, auth.Principal) (T, error)) (T, error) {
	if a.selected == nil {
		return run(ctx, a.backend, actor)
	}
	return readApplication(ctx, a, actor, policy, run)
}

func (a *Application) readPolicy(view, change auth.Permission, additional ...auth.Permission) accessPolicy {
	if a.administrative {
		return readAccess(view, change, additional...)
	}
	return requireAccess(append([]auth.Permission{view}, additional...)...)
}

func readApplication[T any](ctx context.Context, a *Application, actor auth.Principal, policy accessPolicy, run func(context.Context, db.Queryer, auth.Principal) (T, error)) (T, error) {
	read := func(work context.Context, scope db.Queryer) (accessResult[T], error) {
		current, allowed, err := a.currentIn(work, scope, actor, policy)
		if err != nil || !allowed {
			return accessResult[T]{}, err
		}
		value, err := run(work, scope, current)
		return accessResult[T]{value, true}, err
	}
	var result accessResult[T]
	var err error
	if a.selected != nil {
		result, err = db.ReadSnapshotResult(ctx, a.selected.backend, read)
	} else {
		result, err = readSnapshotValue(ctx, a.backend, read)
	}
	if err != nil {
		var zero T
		return zero, err
	}
	if !result.allowed {
		var zero T
		return zero, admin.NewOperationError(admin.OperationDenied, nil)
	}
	return result.value, nil
}

func writeApplication[T any](ctx context.Context, a *Application, actor auth.Principal, policy accessPolicy, run func(context.Context, db.Session, auth.Principal) (T, error)) (T, error) {
	if a.selected == nil {
		return runApplicationAtomic(ctx, a.backend, func(work context.Context, scope db.Session) (T, error) { return run(work, scope, actor) })
	}
	value, err := db.CoordinatedAtomicResult(ctx, a.selected.backend, func(work context.Context, scope db.Session) (T, error) {
		current, allowed, err := a.currentIn(work, scope, actor, policy)
		if err != nil {
			var zero T
			return zero, err
		}
		if !allowed {
			var zero T
			return zero, admin.NewOperationError(admin.OperationDenied, nil)
		}
		return run(work, scope, current)
	})
	if err != nil {
		var zero T
		return zero, operationError(ctx, err)
	}
	return value, nil
}

func writeApplicationRelations[T any](ctx context.Context, a *Application, actor auth.Principal, policy accessPolicy, run func(context.Context, db.RelationSession, auth.Principal) (T, error)) (T, error) {
	if a.selected == nil {
		return runApplicationRelationAtomic(ctx, a.backend, func(work context.Context, scope db.RelationSession) (T, error) { return run(work, scope, actor) })
	}
	value, err := db.CoordinatedRelationAtomicResult(ctx, a.selected.backend, func(work context.Context, scope db.RelationSession) (T, error) {
		current, allowed, err := a.currentIn(work, scope, actor, policy)
		if err != nil {
			var zero T
			return zero, err
		}
		if !allowed {
			var zero T
			return zero, admin.NewOperationError(admin.OperationDenied, nil)
		}
		return run(work, scope, current)
	})
	if err != nil {
		var zero T
		return zero, operationError(ctx, err)
	}
	return value, nil
}

func (a *Application) auditEnabled(appendAudit func(context.Context, db.Session, admin.PreparedEvent) error) bool {
	return a.selected != nil || appendAudit != nil
}

func (a *Application) validAuditConfiguration(appendAudit func(context.Context, db.Session, admin.PreparedEvent) error) bool {
	return a.selected != nil && appendAudit == nil || a.selected == nil && appendAudit != nil
}

// The complete current principal is supplied by the enclosing authorized
// scope. A PreparedEvent's principal ID alone cannot select an audit domain.
func (a *Application) appendAuditIn(ctx context.Context, scope db.Session, actor auth.Principal, event admin.PreparedEvent, fallback func(context.Context, db.Session, admin.PreparedEvent) error) error {
	if a.selected == nil {
		if fallback == nil {
			return errors.New("helpdesk: transactional audit is missing")
		}
		return fallback(ctx, scope, event)
	}
	if fallback != nil || event.ActorID() != actor.ID() {
		return accountaudit.ErrInvalid
	}
	switch event.Model() {
	case "helpdesk.ticket", "helpdesk.label", "helpdesk.service_report", "helpdesk.ticket_label":
	default:
		return accountaudit.ErrInvalid
	}
	prepared, err := accountaudit.Prepare(actor, event.Model(), event.ObjectID(), event.Action(), event.ChangedFields(), time.Now())
	if err != nil {
		return err
	}
	_, err = accountaudit.AppendIn(ctx, scope, prepared)
	return err
}

func accountAccessDenied(err error) bool {
	failure, ok := err.(*admin.OperationError)
	return ok && failure != nil && failure.Code == admin.OperationDenied && failure.Unwrap() == nil
}

func (a *Application) reportWritePolicy(permissions ...auth.Permission) accessPolicy {
	if a.administrative {
		permissions = append(append([]auth.Permission(nil), permissions...), ViewTicket)
	}
	return requireAccess(permissions...)
}

// The base composition retains its original pagination semantics.
func (a *Application) pageConsistencyDescription() string {
	if a.selected != nil {
		return "Current account authority, count and items share one read snapshot."
	}
	return "Count and items are separate reads, so concurrent changes can shift pages."
}
