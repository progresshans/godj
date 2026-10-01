package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

// PasswordChangeBackend supplies coherent reads and an audit append inside the
// caller's transaction. ApplyIn does not own a commit: its caller must couple
// the password and audit with current-session rotation and other-session
// revocation under the same native coordination fence.
type PasswordChangeBackend interface {
	db.SnapshotReader
	AppendAudit(context.Context, db.Session, admin.PreparedEvent) error
}

type PasswordChanger struct {
	backend    PasswordChangeBackend
	directory  *Directory
	confirmer  auth.PasswordConfirmer
	hasher     auth.PasswordHasher
	validators []PasswordValidator
}

// PreparedPasswordChange owns password material for one operation; callers
// must drop it when that operation returns. No raw or encoded password getter
// is exposed. Copies share immutable data; formatting and JSON disclose none.
type PreparedPasswordChange struct{ state *preparedPasswordChange }
type preparedPasswordChange struct {
	owner    *PasswordChanger
	userID   int64
	before   auth.Credential
	after    auth.Credential
	password string
	encoded  string
}

func (*PasswordChanger) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PasswordChanger{redacted}"))
}
func (PreparedPasswordChange) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PreparedPasswordChange{redacted}"))
}

func NewPasswordChanger(backend PasswordChangeBackend, confirmer auth.PasswordConfirmer, hasher auth.PasswordHasher, validators ...PasswordValidator) (*PasswordChanger, error) {
	if nilIdentityValue(backend) || nilIdentityValue(confirmer) || nilIdentityValue(hasher) {
		return nil, managementError(CodeInvalidConfig, "password_change", nil)
	}
	for _, validator := range validators {
		if nilIdentityValue(validator) {
			return nil, managementError(CodeInvalidConfig, "password", nil)
		}
	}
	directory, err := NewDirectory(backend)
	if err != nil {
		return nil, err
	}
	return &PasswordChanger{backend: backend, directory: directory, confirmer: confirmer, hasher: hasher, validators: append([]PasswordValidator(nil), validators...)}, nil
}

// Prepare confirms a session-bound user's old password by stable principal ID.
// No administrative permission is required. Password work runs outside read
// scopes. The preflight profile is validated again by ApplyIn at the final
// fence; an unrelated profile/revision change need not discard this operation.
func (changer *PasswordChanger) Prepare(ctx context.Context, previous sessions.Record, oldPassword, newPassword string) (PreparedPasswordChange, error) {
	before, confirmed, err := changer.check(ctx, previous, &oldPassword, &newPassword)
	if err != nil {
		return PreparedPasswordChange{}, err
	}
	encoded, err := (passwordInput{raw: newPassword}).encode(ctx, changer.hasher)
	if err != nil {
		return PreparedPasswordChange{}, err
	}
	candidate, err := auth.NewCredential(before.Profile().Username, encoded, confirmed.Principal())
	if err != nil || !candidate.HasUsablePassword() || candidate.MatchesSessionStamp(confirmed.SessionStamp()) {
		return PreparedPasswordChange{}, managementError(CodeInvalidConfig, "password_hasher", err)
	}
	if err := ctx.Err(); err != nil {
		return PreparedPasswordChange{}, err
	}
	return PreparedPasswordChange{&preparedPasswordChange{owner: changer, userID: before.Profile().ID, before: confirmed, after: candidate, password: newPassword, encoded: encoded}}, nil
}

// Check performs selected field checks without new hash work or mutations.
// A nil field was rejected by the form's own cleaning and is not checked again.
// It produces diagnostics only, never a reusable password or write authority.
func (changer *PasswordChanger) Check(ctx context.Context, previous sessions.Record, oldPassword, newPassword *string) error {
	_, _, err := changer.check(ctx, previous, oldPassword, newPassword)
	return err
}

func (changer *PasswordChanger) check(ctx context.Context, previous sessions.Record, oldPassword, newPassword *string) (Account, auth.Credential, error) {
	if ctx == nil || changer == nil || changer.directory == nil {
		return Account{}, auth.Credential{}, managementError(CodeInvalidInput, "password_change", nil)
	}
	if err := ctx.Err(); err != nil {
		return Account{}, auth.Credential{}, err
	}
	id, hasID := previous.Value(auth.SessionPrincipalIDKey)
	stamp, hasStamp := previous.Value(auth.SessionCredentialStampKey)
	if !previous.ID().Valid() || !hasID || id == "" || !hasStamp || stamp == "" {
		return Account{}, auth.Credential{}, auth.ErrInvalidCredentials
	}
	before, found, err := changer.directory.ByPrincipalID(ctx, id)
	if err != nil {
		return Account{}, auth.Credential{}, err
	}
	if !found || !before.value().credential.Principal().Authenticated() || !before.value().credential.MatchesSessionStamp(stamp) {
		return Account{}, auth.Credential{}, auth.ErrInvalidCredentials
	}
	var confirmed auth.Credential
	var failures validation.Errors
	if oldPassword != nil {
		confirmed, err = changer.confirmer.ConfirmPassword(ctx, id, *oldPassword)
		if canceled := ctx.Err(); canceled != nil {
			return Account{}, auth.Credential{}, errors.Join(err, canceled)
		}
		if err == auth.ErrInvalidCredentials {
			failures = validation.NewErrors(validation.New("old_password", "password_incorrect"))
		} else if err != nil {
			return Account{}, auth.Credential{}, err
		} else if !confirmed.Principal().Authenticated() || confirmed.Principal().ID() != id || !confirmed.MatchesSessionStamp(stamp) {
			return Account{}, auth.Credential{}, auth.ErrInvalidCredentials
		}
	}
	if newPassword != nil {
		if *newPassword == "" {
			failures = failures.Append(validation.NewErrors(validation.New("password", "required")))
		} else if err := validatePassword(ctx, changer.validators, *newPassword, before.Profile()); err != nil {
			if diagnostics, rejected := validation.Rejected(err); rejected && errors.Unwrap(err) == nil {
				failures = failures.Append(diagnostics)
			} else {
				return Account{}, auth.Credential{}, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Account{}, auth.Credential{}, err
	}
	if !failures.Empty() {
		return Account{}, auth.Credential{}, validation.Reject(failures, nil)
	}
	return before, confirmed, nil
}

// BindSession verifies the OLD authentication binding and replaces only its
// stamp. A transaction owner calls it on the authoritative stored record, not
// just the earlier detached observation, before rotating that record's ID.
func (change PreparedPasswordChange) BindSession(record sessions.Record) (sessions.Record, error) {
	if change.state == nil || !record.ID().Valid() {
		return sessions.Record{}, auth.ErrInvalidCredentials
	}
	id, _ := record.Value(auth.SessionPrincipalIDKey)
	stamp, _ := record.Value(auth.SessionCredentialStampKey)
	if id != change.state.before.Principal().ID() || !change.state.before.MatchesSessionStamp(stamp) {
		return sessions.Record{}, auth.ErrInvalidCredentials
	}
	return record.WithValue(auth.SessionCredentialStampKey, change.state.after.SessionStamp())
}

// ApplyIn updates only password and CURRENT revision and appends a value-free
// self-authored password audit. The result remains provisional until the owner
// commits the session effects too. A renamed user, new grants or last_login
// survive; a changed credential, inactive/deleted user or current policy
// refusal prevents the entire mutation. A prepared value belongs to one owner.
func (changer *PasswordChanger) ApplyIn(ctx context.Context, session db.Session, change PreparedPasswordChange) (auth.Credential, error) {
	if ctx == nil || nilIdentityValue(session) || changer == nil || change.state == nil || change.state.owner != changer {
		return auth.Credential{}, managementError(CodeInvalidInput, "password_change", nil)
	}
	if err := ctx.Err(); err != nil {
		return auth.Credential{}, err
	}
	prepared := change.state
	row, found, err := models.UserObjects.Using(session).Filter(models.UserFields.ID.Exact(prepared.userID)).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil {
		return auth.Credential{}, err
	}
	if !found || row.PrincipalID != prepared.before.Principal().ID() {
		return auth.Credential{}, auth.ErrInvalidCredentials
	}
	current, err := changer.directory.accountFromRow(ctx, session, row)
	if err != nil {
		return auth.Credential{}, err
	}
	credential := current.value().credential
	if !credential.Principal().Authenticated() || !credential.MatchesSessionStamp(prepared.before.SessionStamp()) {
		return auth.Credential{}, auth.ErrInvalidCredentials
	}
	if err := validUserRevision(row.ID, row.Revision); err != nil {
		return auth.Credential{}, err
	}
	if err := validatePassword(ctx, changer.validators, prepared.password, current.Profile()); err != nil {
		return auth.Credential{}, err
	}
	credential, err = auth.NewCredential(row.Username, prepared.encoded, credential.Principal())
	if err != nil {
		return auth.Credential{}, err
	}
	if _, err := models.UserObjects.Update(ctx, session, row, models.UserPatch{}.WithEncodedPassword(prepared.encoded).WithRevision(row.Revision+1)); err != nil {
		return auth.Credential{}, err
	}
	event, err := admin.PrepareEvent(row.PrincipalID, "godj_identity.user", row.ID, admin.ActionChange, []string{"password"}, "")
	if err != nil {
		return auth.Credential{}, err
	}
	if err := changer.backend.AppendAudit(ctx, session, event); err != nil {
		return auth.Credential{}, err
	}
	if err := ctx.Err(); err != nil {
		return auth.Credential{}, err
	}
	return credential, nil
}
