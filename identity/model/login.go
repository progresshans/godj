package model

import (
	"context"
	"fmt"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

// LoginRecorder rechecks the selected account in an already coordinated scope
// and updates only its optional LastLogin role. The owner couples this result
// to session persistence and publishes it only after confirmed commit.
type LoginRecorder[M any] struct{ credentials *CredentialStore[M] }

func (*LoginRecorder[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.LoginRecorder{redacted}"))
}
func NewLoginRecorder[M any](store *CredentialStore[M]) (*LoginRecorder[M], error) {
	if store == nil || store.state == nil {
		return nil, invalidCredentialStore()
	}
	return &LoginRecorder[M]{store}, nil
}
func (recorder *LoginRecorder[M]) RecordIn(ctx context.Context, scope db.Session, login auth.SessionLogin) (auth.Credential, error) {
	observed, err := recorder.observeIn(ctx, scope, login)
	if err != nil {
		return auth.Credential{}, err
	}
	if err := recorder.recordIn(ctx, scope, observed, login.At); err != nil {
		return auth.Credential{}, err
	}
	return observed.credential, nil
}

type accountLoginObservation[M any] struct {
	row        M
	credential auth.Credential
}

// observeIn authorizes the current credential before any session mutation.
// A deactivation can have already revoked the old session; its absence must
// not hide the confirmed credential rejection. The observation stays inside
// the same write fence until session publication and last-login are coupled.
func (recorder *LoginRecorder[M]) observeIn(ctx context.Context, scope db.Session, login auth.SessionLogin) (accountLoginObservation[M], error) {
	var zero accountLoginObservation[M]
	if recorder == nil || recorder.credentials == nil {
		return zero, invalidCredentialStore()
	}
	if err := validateAccountSessionScope(ctx, scope); err != nil {
		return zero, err
	}
	if err := login.Validate(); err != nil {
		return zero, err
	}
	store := recorder.credentials
	binding := store.state.binding
	ownership, err := binding.RequireDomainIn(ctx, scope)
	if err != nil {
		return zero, err
	}
	namespace, err := binding.CredentialDomain(ownership.Generation())
	if err != nil {
		return zero, err
	}
	if !namespace.MatchesPrincipal(login.Credential.Principal()) {
		return zero, auth.ErrInvalidCredentials
	}
	objects := binding.state.objects
	row, present, err := objects.Using(scope).Filter(store.state.principal.Exact(login.Credential.Principal().ID())).OrderBy(store.state.key.Asc()).First(ctx)
	if err != nil {
		return zero, err
	}
	if !present {
		return zero, auth.ErrInvalidCredentials
	}
	key, present, err := objects.PrimaryKey(row)
	id, integer := key.Integer()
	if err != nil || !present || !integer {
		return zero, invalidAccountCredential()
	}
	permissions, err := store.state.permissions(ctx, scope, id)
	if err != nil {
		return zero, err
	}
	current, err := binding.prepareCredentialForDomain(namespace, row, permissions)
	if err != nil {
		return zero, err
	}
	if !current.Principal().Authenticated() || !current.MatchesSessionStamp(login.Credential.SessionStamp()) {
		return zero, auth.ErrInvalidCredentials
	}
	if err := login.Admit(ctx, current.Principal()); err != nil {
		return zero, err
	}
	if err := validateAccountSessionScope(ctx, scope); err != nil {
		return zero, err
	}
	return accountLoginObservation[M]{row, current}, nil
}

func (recorder *LoginRecorder[M]) recordIn(ctx context.Context, scope db.Session, observed accountLoginObservation[M], at time.Time) error {
	if err := validateAccountSessionScope(ctx, scope); err != nil {
		return err
	}
	binding := recorder.credentials.state.binding
	objects := binding.state.objects
	if field := binding.state.role.LastLogin; field != "" {
		instant, err := temporal.Canonical(at)
		if err != nil {
			return err
		}
		updated, err := objects.ApplyValues(observed.row, map[string]query.Value{field: query.DateTime(instant)})
		if err != nil {
			return err
		}
		// Explicit update_fields must fail if the observed account disappeared;
		// it cannot fall back to INSERT or write another management field.
		if err := objects.Save(ctx, scope, &updated, orm.UpdateFieldNames[M](field)); err != nil {
			return err
		}
	}
	return validateAccountSessionScope(ctx, scope)
}

type accountLoginPersistence[M any] struct {
	store    *SessionStore[M]
	manager  *sessions.Manager
	recorder *LoginRecorder[M]
}

func (*accountLoginPersistence[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.LoginPersistence{redacted}"))
}

// LoginPersistence binds this exact session manager to the same account and
// database owner. Password work, time and entropy are prepared outside the DB
// scope; admission, current credentials, LastLogin and session publication are
// coupled inside it. Construction performs no I/O.
func (store *SessionStore[M]) LoginPersistence(manager *sessions.Manager) (auth.LoginPersistence, error) {
	if store == nil || store.state == nil || manager == nil || manager.Store() != store {
		return nil, accountSessionFailure(sessions.CodeInvalidConfig, nil)
	}
	recorder, err := NewLoginRecorder(store.state.credentials)
	if err != nil {
		return nil, err
	}
	return &accountLoginPersistence[M]{store, manager, recorder}, nil
}
func (owner *accountLoginPersistence[M]) Sessions() *sessions.Manager {
	if owner == nil {
		return nil
	}
	return owner.manager
}
func (owner *accountLoginPersistence[M]) Login(ctx context.Context, login auth.SessionLogin) (auth.SessionLoginResult, error) {
	if owner == nil || owner.store == nil || owner.manager == nil || owner.recorder == nil {
		return auth.SessionLoginResult{}, accountSessionFailure(sessions.CodeInvalidConfig, nil)
	}
	if err := owner.store.valid(ctx); err != nil {
		return auth.SessionLoginResult{}, err
	}
	if err := login.Validate(); err != nil {
		return auth.SessionLoginResult{}, err
	}
	interceptor := &accountLoginStore[M]{SessionStore: owner.store, owner: owner, login: login}
	manager, err := owner.manager.WithStore(interceptor)
	if err != nil {
		return auth.SessionLoginResult{}, err
	}
	record, err := auth.EstablishSession(ctx, manager, login.Credential, login.Previous)
	if err != nil {
		if interceptor.denied {
			return auth.SessionLoginResult{}, auth.ErrInvalidCredentials
		}
		return auth.SessionLoginResult{}, accountSessionFailure(sessions.CodeStoreFailure, err)
	}
	if !interceptor.current.Principal().Authenticated() || !interceptor.current.MatchesSessionStamp(login.Credential.SessionStamp()) {
		return auth.SessionLoginResult{}, accountSessionFailure(sessions.CodeStoreFailure, nil)
	}
	return auth.SessionLoginResult{Credential: interceptor.current, Record: record}, nil
}

type accountLoginStore[M any] struct {
	*SessionStore[M]
	owner   *accountLoginPersistence[M]
	login   auth.SessionLogin
	current auth.Credential
	denied  bool
}
type accountLoginPublication struct {
	record     sessions.Record
	changed    bool
	credential auth.Credential
}

func (store *accountLoginStore[M]) Create(ctx context.Context, record sessions.Record) (bool, error) {
	if err := store.validRecord(ctx, record); err != nil {
		return false, err
	}
	result, err := store.write(ctx, func(work context.Context, scoped *sessiondb.Store) (accountSessionRecord, error) {
		created, err := scoped.Create(work, record)
		return accountSessionRecord{record, created}, err
	})
	return result.present, err
}
func (store *accountLoginStore[M]) Rotate(ctx context.Context, id sessions.ID, record sessions.Record) (sessions.Record, bool, error) {
	return store.replaceLogin(ctx, id, record, false)
}
func (store *accountLoginStore[M]) Replace(ctx context.Context, id sessions.ID, record sessions.Record) (sessions.Record, bool, error) {
	return store.replaceLogin(ctx, id, record, true)
}
func (store *accountLoginStore[M]) replaceLogin(ctx context.Context, id sessions.ID, record sessions.Record, fresh bool) (sessions.Record, bool, error) {
	if err := store.validRecord(ctx, record); err != nil {
		return sessions.Record{}, false, err
	}
	result, err := store.write(ctx, func(work context.Context, scoped *sessiondb.Store) (accountSessionRecord, error) {
		var result accountSessionRecord
		var err error
		if fresh {
			result.record, result.present, err = scoped.Replace(work, id, record)
		} else {
			result.record, result.present, err = scoped.Rotate(work, id, record)
		}
		if err == nil && !result.present {
			err = &sessions.Error{Code: sessions.CodeNotFound, Detail: "login session is missing or expired"}
		}
		return result, err
	})
	return result.record, result.present, err
}

// Manager's early absolute-expiry decision must not commit cleanup on its own
// when the enclosing login cannot publish a session or LastLogin observation.
func (*accountLoginStore[M]) Delete(context.Context, sessions.ID) error {
	return &sessions.Error{Code: sessions.CodeNotFound, Detail: "login session is expired"}
}
func (store *accountLoginStore[M]) write(ctx context.Context, mutate func(context.Context, *sessiondb.Store) (accountSessionRecord, error)) (accountSessionRecord, error) {
	result, err := accountSessionWrite(ctx, store.SessionStore, func(work context.Context, scope db.Session, scoped *sessiondb.Store) (accountLoginPublication, error) {
		observed, err := store.owner.recorder.observeIn(work, scope, store.login)
		if err != nil {
			return accountLoginPublication{}, err
		}
		record, err := mutate(work, scoped)
		if err != nil || !record.present {
			return accountLoginPublication{}, err
		}
		if err := store.owner.recorder.recordIn(work, scope, observed, store.login.At); err != nil {
			return accountLoginPublication{}, err
		}
		return accountLoginPublication{record.record, true, observed.credential}, nil
	})
	if err != nil {
		// Only accountSessionWrite's exact confirmed rejection can be this
		// sentinel. Wrapped/joined/unknown failures never become a denial.
		store.denied = err == auth.ErrInvalidCredentials
		return accountSessionRecord{}, err
	}
	if result.changed {
		store.current = result.credential
	}
	return accountSessionRecord{result.record, result.changed}, nil
}
