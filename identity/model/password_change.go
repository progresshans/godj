package model

import (
	"context"
	"errors"
	"fmt"
	"math"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

// PasswordChangeConfig selects the same typed strength policy used for account
// creation and administrative password changes. Nil means no strength policy;
// required input, the byte envelope and the hasher's constraints still apply.
// MaxPasswordBytes defaults to 1024, with a supported range of 1..4096.
type PasswordChangeConfig[M any] struct {
	MaxPasswordBytes int
	ValidatePassword PasswordValidator[M]
}

// Authenticator creates a stored verifier over this session owner's exact
// credential store. It performs the ordinary bounded dummy-hash setup outside
// a DB scope. Reuse it for login and PasswordChangePersistence.
func (store *SessionStore[M]) Authenticator(ctx context.Context, hasher auth.PasswordHasher) (*Authenticator[M], error) {
	if err := store.valid(ctx); err != nil {
		return nil, err
	}
	return NewAuthenticator(ctx, store.state.credentials, hasher)
}

type accountPasswordChange[M any] struct {
	store         *SessionStore[M]
	manager       *sessions.Manager
	authenticator *Authenticator[M]
	config        PasswordChangeConfig[M]
}

func (*accountPasswordChange[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.PasswordChangePersistence{redacted}"))
}

// PasswordChangePersistence binds one exact session manager and stored verifier
// to the selected model. Obtain the verifier through this store's Authenticator
// method; matching model names or independently created owners are insufficient.
// Construction performs no I/O, password work or application callbacks.
func (store *SessionStore[M]) PasswordChangePersistence(manager *sessions.Manager, authenticator *Authenticator[M], config PasswordChangeConfig[M]) (auth.PasswordChangePersistence, error) {
	if store == nil || store.state == nil || manager == nil || manager.Store() != store || authenticator == nil || authenticator.inner == nil || authenticator.credentials != store.state.credentials || nilModelValue(authenticator.hasher) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if config.MaxPasswordBytes == 0 {
		config.MaxPasswordBytes = 1024
	}
	if config.MaxPasswordBytes < 1 || config.MaxPasswordBytes > 4096 {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	return &accountPasswordChange[M]{store, manager, authenticator, config}, nil
}

func (owner *accountPasswordChange[M]) Sessions() *sessions.Manager {
	if owner == nil {
		return nil
	}
	return owner.manager
}

type passwordAccount[M any] struct {
	row        M
	id         int64
	domain     auth.CredentialDomain
	credential auth.Credential
	valid      bool
}

// Absence and stale authentication stay data until the read scope has closed.
func (owner *accountPasswordChange[M]) accountIn(ctx context.Context, scope db.Queryer, principalID, stamp string) (passwordAccount[M], error) {
	var zero passwordAccount[M]
	if err := validateAccountSessionScope(ctx, scope); err != nil {
		return zero, err
	}
	store := owner.store.state.credentials
	binding := store.state.binding
	ownership, err := binding.RequireDomainIn(ctx, scope)
	if err != nil {
		return zero, err
	}
	namespace, err := binding.CredentialDomain(ownership.Generation())
	if err != nil {
		return zero, err
	}
	objects := binding.state.objects
	row, present, err := objects.Using(scope).Filter(store.state.principal.Exact(principalID)).OrderBy(store.state.key.Asc()).First(ctx)
	if err != nil || !present {
		return zero, err
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
	credential, err := binding.prepareCredentialForDomain(namespace, row, permissions)
	if err != nil {
		return zero, err
	}
	if !credential.Principal().Authenticated() || credential.Principal().ID() != principalID || !credential.MatchesSessionStamp(stamp) {
		return zero, nil
	}
	return passwordAccount[M]{row, id, namespace, credential, true}, nil
}

func (owner *accountPasswordChange[M]) validate(ctx context.Context, raw string, row M) error {
	if raw == "" {
		return profileInputError("password", "required")
	}
	if len(raw) > owner.config.MaxPasswordBytes {
		return profileInputError("password", "max_length")
	}
	return validateAccountPassword(ctx, owner.store.state.credentials.state.binding, owner.config.ValidatePassword, raw, row)
}

func (owner *accountPasswordChange[M]) check(ctx context.Context, id sessions.ID, oldPassword, newPassword *string) (sessions.Record, passwordAccount[M], error) {
	var zero passwordAccount[M]
	if owner == nil || owner.store == nil || owner.manager == nil || owner.authenticator == nil {
		return sessions.Record{}, zero, managementError(CodeInvalidConfig, nil)
	}
	if err := owner.store.valid(ctx); err != nil {
		return sessions.Record{}, zero, err
	}
	if !id.Valid() {
		return sessions.Record{}, zero, auth.ErrInvalidCredentials
	}
	previous, found, err := owner.manager.Peek(ctx, id)
	if err != nil {
		return sessions.Record{}, zero, passwordChangeFailure(err)
	}
	principalID, hasID := previous.Value(auth.SessionPrincipalIDKey)
	stamp, hasStamp := previous.Value(auth.SessionCredentialStampKey)
	if !found || !hasID || principalID == "" || !hasStamp || stamp == "" {
		return sessions.Record{}, zero, auth.ErrInvalidCredentials
	}
	before, err := db.ReadSnapshotResult(ctx, owner.store.state.backend, func(work context.Context, scope db.Queryer) (passwordAccount[M], error) {
		return owner.accountIn(work, scope, principalID, stamp)
	})
	if err != nil {
		return sessions.Record{}, zero, passwordChangeFailure(err)
	}
	if !before.valid {
		return sessions.Record{}, zero, auth.ErrInvalidCredentials
	}
	var diagnostics validation.Errors
	if oldPassword != nil {
		confirmed, err := owner.authenticator.ConfirmPassword(ctx, principalID, *oldPassword)
		if canceled := ctx.Err(); canceled != nil {
			return sessions.Record{}, zero, passwordChangeFailure(errors.Join(err, canceled))
		}
		if err == auth.ErrInvalidCredentials {
			diagnostics = diagnostics.Append(validation.NewErrors(validation.New("old_password", "password_incorrect")))
		} else if err != nil {
			return sessions.Record{}, zero, passwordChangeFailure(err)
		} else if !confirmed.Principal().Authenticated() || confirmed.Principal().ID() != principalID || !confirmed.MatchesSessionStamp(stamp) {
			return sessions.Record{}, zero, auth.ErrInvalidCredentials
		}
	}
	if newPassword != nil {
		if err := owner.validate(ctx, *newPassword, before.row); err != nil {
			if rejected, ok := validation.Rejected(err); ok && errors.Unwrap(err) == nil {
				diagnostics = diagnostics.Append(rejected)
			} else {
				return sessions.Record{}, zero, passwordChangeFailure(err)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return sessions.Record{}, zero, passwordChangeFailure(err)
	}
	if !diagnostics.Empty() {
		return sessions.Record{}, zero, validation.Reject(diagnostics, nil)
	}
	return previous, before, nil
}

func (owner *accountPasswordChange[M]) CheckPasswordChange(ctx context.Context, id sessions.ID, oldPassword, newPassword *string) error {
	_, _, err := owner.check(ctx, id, oldPassword, newPassword)
	return err
}

type accountPasswordPreparation[M any] struct {
	before       passwordAccount[M]
	after        auth.Credential
	raw, encoded string
}

func (owner *accountPasswordChange[M]) ChangePassword(ctx context.Context, id sessions.ID, oldPassword, newPassword string) (auth.PasswordChangeResult, error) {
	previous, before, err := owner.check(ctx, id, &oldPassword, &newPassword)
	if err != nil {
		return auth.PasswordChangeResult{}, err
	}
	encoded, err := encodeUsablePassword(ctx, owner.authenticator.hasher, newPassword)
	if err != nil {
		return auth.PasswordChangeResult{}, err
	}
	binding := owner.store.state.credentials.state.binding
	row, err := binding.state.objects.ApplyValues(before.row, map[string]query.Value{binding.state.role.Password: query.String(encoded)})
	if err != nil {
		return auth.PasswordChangeResult{}, passwordChangeFailure(err)
	}
	after, err := binding.prepareCredentialForDomain(before.domain, row, before.credential.Principal().Permissions())
	if err != nil || !after.HasUsablePassword() || after.MatchesSessionStamp(before.credential.SessionStamp()) {
		return auth.PasswordChangeResult{}, managementError(CodeInvalidConfig, err)
	}
	prepared := accountPasswordPreparation[M]{before, after, newPassword, encoded}
	interceptor := &accountPasswordStore[M]{SessionStore: owner.store, owner: owner, prepared: &prepared}
	defer func() { interceptor.prepared = nil; prepared = accountPasswordPreparation[M]{} }()
	manager, err := owner.manager.WithStore(interceptor)
	if err != nil {
		return auth.PasswordChangeResult{}, passwordChangeFailure(err)
	}
	record, err := manager.Rotate(ctx, previous)
	if err != nil {
		if interceptor.rejection != nil {
			return auth.PasswordChangeResult{}, interceptor.rejection
		}
		return auth.PasswordChangeResult{}, passwordChangeFailure(err)
	}
	principalID, _ := record.Value(auth.SessionPrincipalIDKey)
	stamp, _ := record.Value(auth.SessionCredentialStampKey)
	if !interceptor.current.Principal().Authenticated() || interceptor.current.Principal().ID() != principalID || !interceptor.current.MatchesSessionStamp(stamp) {
		return auth.PasswordChangeResult{}, passwordChangeFailure(nil)
	}
	return auth.PasswordChangeResult{Credential: interceptor.current, Record: record}, nil
}

type accountPasswordStore[M any] struct {
	*SessionStore[M]
	owner     *accountPasswordChange[M]
	prepared  *accountPasswordPreparation[M]
	current   auth.Credential
	rejection error
}

// Expiry cleanup is part of the password transaction, never an independent
// effect of a rejected password change.
func (store *accountPasswordStore[M]) Delete(context.Context, sessions.ID) error {
	store.rejection = auth.ErrInvalidCredentials
	return store.rejection
}

func (store *accountPasswordStore[M]) Rotate(ctx context.Context, oldID sessions.ID, replacement sessions.Record) (sessions.Record, bool, error) {
	prepared := store.prepared
	result, err := accountSessionWrite(ctx, store.SessionStore, func(work context.Context, scope db.Session, scoped *sessiondb.Store) (auth.PasswordChangeResult, error) {
		var zero auth.PasswordChangeResult
		currentSession, present, err := scoped.Load(work, oldID)
		if err != nil {
			return zero, err
		}
		principalID, _ := currentSession.Value(auth.SessionPrincipalIDKey)
		stamp, _ := currentSession.Value(auth.SessionCredentialStampKey)
		if !present || principalID != prepared.before.credential.Principal().ID() || !prepared.before.credential.MatchesSessionStamp(stamp) {
			return zero, auth.ErrInvalidCredentials
		}
		current, err := store.owner.accountIn(work, scope, principalID, stamp)
		if err != nil {
			return zero, err
		}
		if !current.valid || current.id != prepared.before.id {
			return zero, auth.ErrInvalidCredentials
		}
		if err := store.owner.validate(work, prepared.raw, current.row); err != nil {
			return zero, err
		}
		bound, err := currentSession.WithValue(auth.SessionCredentialStampKey, prepared.after.SessionStamp())
		if err != nil {
			return zero, err
		}
		replacement, err = sessions.RestoreRecord(sessions.RecordSnapshot{
			ID: replacement.ID(), Values: bound.Values(), CreatedAt: currentSession.CreatedAt(),
			AccessedAt: replacement.AccessedAt(), AbsoluteExpiresAt: currentSession.AbsoluteExpiresAt(), IdleExpiresAt: replacement.IdleExpiresAt(),
		}, store.state.limits)
		if err != nil {
			return zero, err
		}
		record, rotated, err := scoped.Rotate(work, oldID, replacement)
		if err != nil {
			return zero, err
		}
		if !rotated {
			return zero, auth.ErrInvalidCredentials
		}
		binding := store.state.credentials.state.binding
		objects, role := binding.state.objects, binding.state.role
		values, err := objects.ModelValues(current.row)
		if err != nil {
			return zero, err
		}
		revision, valid := values[role.Revision].Integer()
		if !valid || revision < 1 || revision == math.MaxInt64 {
			return zero, invalidAccountCredential()
		}
		updated, err := objects.ApplyValues(current.row, map[string]query.Value{role.Password: query.String(prepared.encoded), role.Revision: query.Integer(revision + 1)})
		if err != nil {
			return zero, err
		}
		credential, err := binding.prepareCredentialForDomain(current.domain, updated, current.credential.Principal().Permissions())
		if err != nil || !credential.MatchesSessionStamp(prepared.after.SessionStamp()) {
			return zero, invalidAccountCredential()
		}
		if err := objects.Save(work, scope, &updated, orm.UpdateFieldNames[M](role.Password, role.Revision)); err != nil {
			return zero, err
		}
		if _, err := scoped.DeleteMatching(work, auth.SessionPrincipalIDKey, principalID, record.ID()); err != nil {
			return zero, err
		}
		event, err := accountaudit.Prepare(credential.Principal(), role.Model.AppLabel+"."+role.Model.ModelName, current.id, admin.ActionChange, []string{role.Password}, replacement.AccessedAt())
		if err != nil {
			return zero, err
		}
		if _, err := accountaudit.AppendIn(work, scope, event); err != nil {
			return zero, err
		}
		if err := validateAccountSessionScope(work, scope); err != nil {
			return zero, err
		}
		return auth.PasswordChangeResult{Credential: credential, Record: record}, nil
	})
	if err != nil {
		if err == auth.ErrInvalidCredentials {
			store.rejection = err
		} else if _, rejected := validation.Rejected(err); rejected && errors.Unwrap(err) == nil {
			store.rejection = err
		}
		// Only a direct, cause-free collision from confirmed rollback reaches
		// Manager's bounded ID retry. Unknown/cleanup remains wrapped by write.
		return sessions.Record{}, false, err
	}
	store.current = result.Credential
	return result.Record, true, nil
}

func passwordChangeFailure(cause error) error {
	for _, code := range []string{query.CodeCommitOutcomeUnknown, query.CodeTransactionOutcomeUnknown} {
		if errors.Is(cause, &query.Error{Code: code}) {
			return managementError(CodeOutcomeUnknown, cause)
		}
	}
	return managementError(CodePersistence, cause)
}
