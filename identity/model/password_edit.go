package model

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

// PasswordEditConfig selects current management authority and the typed
// password policy. Every declared permission is required; the authorizer is
// only a deny overlay. The pure clock and policy must be concurrent-safe.
type PasswordEditConfig[M any] struct {
	ChangePermission      auth.Permission
	AdditionalPermissions []auth.Permission
	MaxPasswordBytes      int
	ValidatePassword      PasswordValidator[M]
	Now                   func() time.Time
}

// PasswordEditor replaces a selected account's password administratively. It
// never authenticates the caller or preserves the target's current session.
// It uses its exact session/credential owner for every read and write.
type PasswordEditor[M any] struct{ state *passwordEditState[M] }
type passwordEditState[M any] struct {
	sessions *SessionStore[M]
	hasher   auth.PasswordHasher
	overlay  auth.Authorizer
	required []auth.Permission
	maximum  int
	validate PasswordValidator[M]
	now      func() time.Time
}

func (PasswordEditor[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.PasswordEditor{redacted}"))
}

// NewPasswordEditor is normally called by the generated project factory.
// The verifier must come from these exact sessions. Expected binds the actual
// generated model and complete host source; matching names are insufficient.
// Construction performs no I/O, hashing, entropy or application callbacks.
func NewPasswordEditor[M any](store *SessionStore[M], expected Binding[M], verifier *Authenticator[M], overlay auth.Authorizer, config PasswordEditConfig[M]) (*PasswordEditor[M], error) {
	if store == nil || store.state == nil || expected.state == nil || verifier == nil || verifier.inner == nil ||
		verifier.credentials != store.state.credentials || nilModelValue(verifier.hasher) || nilModelValue(overlay) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if err := store.state.credentials.valid(); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	binding := store.state.credentials.state.binding
	actualSource, err := binding.state.project.SourceSHA256()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	expectedSource, err := expected.state.project.SourceSHA256()
	if err != nil || actualSource != expectedSource || binding.state.roleFingerprint != expected.state.roleFingerprint {
		return nil, managementError(CodeInvalidConfig, err)
	}
	required, err := auth.RequiredPermissions(config.ChangePermission, config.AdditionalPermissions...)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	maximum := config.MaxPasswordBytes
	if maximum == 0 {
		maximum = 1024
	}
	if maximum < 1 || maximum > 4096 {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	role := binding.state.role
	if _, err := admin.PrepareEventTemplate("password-startup", role.Model.AppLabel+"."+role.Model.ModelName, admin.ActionChange, []string{role.Password}, ""); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &PasswordEditor[M]{state: &passwordEditState[M]{store, verifier.hasher, overlay, required, maximum, config.ValidatePassword, now}}, nil
}

// Permissions and MaxPasswordBytes expose immutable input/admission metadata
// for Form/Admin/API composition without granting authority to change a row.
func (editor *PasswordEditor[M]) Permissions() []auth.Permission {
	if editor == nil || editor.state == nil {
		return nil
	}
	return slices.Clone(editor.state.required)
}
func (editor *PasswordEditor[M]) MaxPasswordBytes() int {
	if editor == nil || editor.state == nil {
		return 0
	}
	return editor.state.maximum
}

type PasswordEditResult struct {
	ID, Revision    int64
	RevokedSessions int
}

// SetPassword hashes once outside DB scopes, then rechecks the actor, domain,
// target revision, prior credential and typed policy under one write fence.
// Only Password/Revision change; all target sessions and the value-free audit
// commit with them. Errors publish no result and never trigger a retry.
func (editor *PasswordEditor[M]) SetPassword(ctx context.Context, actor auth.Principal, id, revision int64, raw string) (PasswordEditResult, error) {
	return editor.replace(ctx, actor, id, revision, raw, false)
}

// SetUnusablePassword rotates an unusable marker even when already disabled.
// It revokes the target's sessions, including its own managing browser, and
// performs neither password hashing nor password validation.
func (editor *PasswordEditor[M]) SetUnusablePassword(ctx context.Context, actor auth.Principal, id, revision int64) (PasswordEditResult, error) {
	return editor.replace(ctx, actor, id, revision, "", true)
}

type passwordEditObservation[M any] struct {
	row        M
	actor      auth.Principal
	credential auth.Credential
	rejection  error
}

// Definite denials remain data until successful read cleanup. In a write
// transaction the caller propagates them to the rollback owner unchanged.
func (state *passwordEditState[M]) observe(ctx context.Context, scope db.Queryer, actor auth.Principal, id, revision int64) (passwordEditObservation[M], error) {
	var zero passwordEditObservation[M]
	store := state.sessions.state.credentials
	currentActor, allowed, err := store.AuthorizeIn(ctx, scope, actor, state.overlay, state.required[0], state.required[1:]...)
	if err != nil {
		return zero, err
	}
	if !allowed {
		return passwordEditObservation[M]{rejection: managementError(CodePermission, nil)}, nil
	}
	binding := store.state.binding
	row, present, err := binding.state.objects.Using(scope).Filter(store.state.key.Exact(id)).OrderBy(store.state.key.Asc()).First(ctx)
	if err != nil {
		return zero, err
	}
	if !present {
		return passwordEditObservation[M]{rejection: managementError(CodeNotFound, nil)}, nil
	}
	key, present, err := binding.state.objects.PrimaryKey(row)
	actualID, integer := key.Integer()
	if err != nil || !present || !integer || actualID != id {
		return zero, invalidAccountCredential()
	}
	values, err := binding.state.objects.ModelValues(row)
	if err != nil {
		return zero, err
	}
	actualRevision, valid := values[binding.state.role.Revision].Integer()
	if !valid || actualRevision < 1 {
		return zero, invalidAccountCredential()
	}
	if actualRevision != revision {
		return passwordEditObservation[M]{rejection: managementError(CodeConflict, nil)}, nil
	}
	permissions, err := store.state.permissions(ctx, scope, id)
	if err != nil {
		return zero, err
	}
	namespace, bound := currentActor.CredentialDomain()
	if !bound {
		return zero, invalidAccountCredential()
	}
	credential, err := binding.prepareCredentialForDomain(namespace, row, permissions)
	if err != nil {
		return zero, err
	}
	return passwordEditObservation[M]{row: row, actor: currentActor, credential: credential}, nil
}

func (editor *PasswordEditor[M]) replace(ctx context.Context, actor auth.Principal, id, revision int64, raw string, unusable bool) (PasswordEditResult, error) {
	var zero PasswordEditResult
	if editor == nil || editor.state == nil {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) || revision < 1 || revision == math.MaxInt64 {
		return zero, managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return zero, managementError(CodeInvalidInput, err)
	}
	if !actor.Authenticated() {
		return zero, managementError(CodePermission, nil)
	}
	state := editor.state
	before, err := db.ReadSnapshotResult(ctx, state.sessions.state.backend, func(work context.Context, scope db.Queryer) (passwordEditObservation[M], error) {
		return state.observe(work, scope, actor, id, revision)
	})
	if err != nil {
		return zero, managementError(CodePersistence, err)
	}
	if before.rejection != nil {
		return zero, before.rejection
	}
	if err := state.checkPassword(ctx, raw, unusable, before.row); err != nil {
		return zero, err
	}
	var encoded string
	if unusable {
		encoded, err = auth.MakeUnusablePassword(ctx)
		if err != nil {
			return zero, managementError(CodePersistence, err)
		}
	} else {
		encoded, err = encodeUsablePassword(ctx, state.hasher, raw)
	}
	if err != nil {
		return zero, err
	}
	binding := state.sessions.state.credentials.state.binding
	role, objects := binding.state.role, binding.state.objects
	candidate, err := objects.ApplyValues(before.row, map[string]query.Value{role.Password: query.String(encoded)})
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	namespace, _ := before.actor.CredentialDomain()
	next, err := binding.prepareCredentialForDomain(namespace, candidate, before.credential.Principal().Permissions())
	if err != nil || next.HasUsablePassword() == unusable || next.MatchesSessionStamp(before.credential.SessionStamp()) {
		return zero, managementError(CodeInvalidConfig, err)
	}
	return managementWrite(ctx, state.sessions.state.backend, func(work context.Context, scope db.Session) (PasswordEditResult, error) {
		current, err := state.observe(work, scope, actor, id, revision)
		if err != nil {
			return zero, err
		}
		if current.rejection != nil {
			return zero, current.rejection
		}
		if !current.credential.MatchesSessionStamp(before.credential.SessionStamp()) {
			return zero, managementError(CodeConflict, nil)
		}
		if err := state.checkPassword(work, raw, unusable, current.row); err != nil {
			return zero, err
		}
		updated, err := objects.ApplyValues(current.row, map[string]query.Value{role.Password: query.String(encoded), role.Revision: query.Integer(revision + 1)})
		if err != nil {
			return zero, err
		}
		if err := objects.Save(work, scope, &updated, orm.UpdateFieldNames[M](role.Password, role.Revision)); err != nil {
			return zero, err
		}
		revoked, err := state.sessions.RevokeIn(work, scope, current.credential.Principal().ID(), sessions.ID{})
		if err != nil {
			return zero, err
		}
		event, err := accountaudit.Prepare(current.actor, role.Model.AppLabel+"."+role.Model.ModelName, id, admin.ActionChange, []string{role.Password}, state.now())
		if err != nil {
			return zero, err
		}
		if _, err := accountaudit.AppendIn(work, scope, event); err != nil {
			return zero, err
		}
		return PasswordEditResult{ID: id, Revision: revision + 1, RevokedSessions: revoked}, nil
	})
}

func (state *passwordEditState[M]) checkPassword(ctx context.Context, raw string, unusable bool, row M) error {
	if unusable {
		return ctx.Err()
	}
	if raw == "" {
		return profileInputError("password", "required")
	}
	if len(raw) > state.maximum {
		return profileInputError("password", "max_length")
	}
	return validateAccountPassword(ctx, state.sessions.state.credentials.state.binding, state.validate, raw, row)
}
