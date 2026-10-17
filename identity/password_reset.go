package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
)

// ErrInvalidResetToken deliberately does not distinguish unknown, inactive,
// unusable, expired, malformed or changed accounts from an invalid signature.
var ErrInvalidResetToken = errors.New("identity: invalid password reset token")

type PasswordResetConfig struct {
	Keys PasswordResetKeyRing `json:"-"`
	// Timeout defaults to 72 hours and must be a positive whole-second duration.
	Timeout time.Duration
	// Clock must be pure and concurrency-safe; it is also called under the
	// final write fence. Nil selects time.Now. Future-issued tokens are denied.
	Clock      func() time.Time    `json:"-"`
	Validators []PasswordValidator `json:"-"`
}

func (PasswordResetConfig) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PasswordResetConfig{redacted}"))
}

// PasswordResetter owns token admission and the atomic password/revocation/audit
// boundary. It grants no login session and requires no administrative permission.
// Email recipient selection and delivery belong to its separate request consumer.
type PasswordResetter struct{ state *passwordResetState }
type passwordResetState struct {
	backend    ManagementBackend
	directory  *Directory
	hasher     auth.PasswordHasher
	keys       PasswordResetKeyRing
	timeout    time.Duration
	clock      func() time.Time
	validators []PasswordValidator
}

func (*PasswordResetter) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PasswordResetter{redacted}"))
}

func NewPasswordResetter(backend ManagementBackend, hasher auth.PasswordHasher, config PasswordResetConfig) (*PasswordResetter, error) {
	if nilIdentityValue(backend) || nilIdentityValue(hasher) || config.Keys.state == nil {
		return nil, managementError(CodeInvalidConfig, "password_reset", nil)
	}
	if config.Timeout == 0 {
		config.Timeout = 72 * time.Hour
	}
	if config.Timeout < time.Second || config.Timeout%time.Second != 0 {
		return nil, managementError(CodeInvalidConfig, "password_reset_timeout", nil)
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	for _, validator := range config.Validators {
		if nilIdentityValue(validator) {
			return nil, managementError(CodeInvalidConfig, "password", nil)
		}
	}
	directory, err := NewDirectory(backend)
	if err != nil {
		return nil, err
	}
	return &PasswordResetter{&passwordResetState{backend, directory, hasher, config.Keys, config.Timeout, config.Clock, append([]PasswordValidator(nil), config.Validators...)}}, nil
}

func (resetter *PasswordResetter) validCall(ctx context.Context, principalID string) error {
	if ctx == nil {
		return managementError(CodeInvalidInput, "context", nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if resetter == nil || resetter.state == nil {
		return managementError(CodeInvalidConfig, "password_reset", nil)
	}
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil {
		return ErrInvalidResetToken
	}
	return nil
}

func (resetter *PasswordResetter) instant() (int64, error) {
	now := resetter.state.clock().UTC()
	if now.Year() < 1970 || now.Year() > 9999 {
		return 0, managementError(CodeInvalidConfig, "password_reset_clock", nil)
	}
	return now.Unix(), nil
}

// IssueToken reads a current eligible identity and issues recipient-only secret
// material. This method must never be exposed as a public lookup by identifier.
// It performs no hash work, last_login/session mutation or delivery. A later
// account change may immediately invalidate the issued token.
func (resetter *PasswordResetter) IssueToken(ctx context.Context, principalID string) (PasswordResetToken, error) {
	if err := resetter.validCall(ctx, principalID); err != nil {
		return PasswordResetToken{}, err
	}
	account, found, err := resetter.state.directory.ByPrincipalID(ctx, principalID)
	if err != nil {
		return PasswordResetToken{}, managementWriteFailure(err)
	}
	if !found || !account.value().credential.Principal().Authenticated() || !account.HasUsablePassword() {
		return PasswordResetToken{}, ErrInvalidResetToken
	}
	now, err := resetter.instant()
	if err = errors.Join(err, ctx.Err()); err != nil {
		return PasswordResetToken{}, err
	}
	return resetter.state.keys.issue(account, now), nil
}

// CheckPassword checks token admission and, when non-nil, the cleaned new
// password. It performs no writes or new hash work and grants no reusable write
// authority. Forms may omit a password that their own cleaning already rejected.
func (resetter *PasswordResetter) CheckPassword(ctx context.Context, principalID, token string, password *string) error {
	_, _, err := resetter.preflight(ctx, principalID, token, password)
	return err
}

func (resetter *PasswordResetter) preflight(ctx context.Context, principalID, encoded string, password *string) (Account, parsedResetToken, error) {
	if err := resetter.validCall(ctx, principalID); err != nil {
		return Account{}, parsedResetToken{}, err
	}
	token, valid := parsePasswordResetToken(encoded)
	if !valid {
		return Account{}, parsedResetToken{}, ErrInvalidResetToken
	}
	account, found, err := resetter.state.directory.ByPrincipalID(ctx, principalID)
	if err != nil {
		return Account{}, parsedResetToken{}, managementWriteFailure(err)
	}
	now, err := resetter.instant()
	if err = errors.Join(err, ctx.Err()); err != nil {
		return Account{}, parsedResetToken{}, err
	}
	if !found || !resetter.state.keys.accepts(account, token, now, resetter.state.timeout) {
		return Account{}, parsedResetToken{}, ErrInvalidResetToken
	}
	if password != nil {
		if *password == "" {
			return Account{}, parsedResetToken{}, validation.Reject(validation.NewErrors(validation.New("password", "required")), nil)
		}
		if err := validatePassword(ctx, resetter.state.validators, *password, account.Profile()); err != nil {
			return Account{}, parsedResetToken{}, err
		}
	}
	return account, token, nil
}

// PreparedPasswordReset owns immutable material for one reset operation. A
// caller drops it when that operation finishes. Preparation performs password
// work outside DB scopes; ApplyIn rechecks all authority in the caller's fence.
// The value belongs to exactly one PasswordResetter and exposes no secrets.
type PreparedPasswordReset struct{ state *preparedPasswordReset }
type preparedPasswordReset struct {
	owner             *PasswordResetter
	userID            int64
	principalID       string
	token             parsedResetToken
	password, encoded string
}

func (PreparedPasswordReset) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.PreparedPasswordReset{redacted}"))
}
func (PreparedPasswordReset) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.PreparedPasswordReset{redacted}"`), nil
}

// CheckTokenIn observes token validity inside a caller-owned coherent read or
// write scope. It performs no password work, writes or nested transaction. A
// nil result is an observation only; ApplyIn always checks the current state.
func (resetter *PasswordResetter) CheckTokenIn(ctx context.Context, reader db.Queryer, principalID, encodedToken string) error {
	if err := resetter.validCall(ctx, principalID); err != nil {
		return err
	}
	if nilIdentityValue(reader) {
		return managementError(CodeInvalidInput, "password_reset", nil)
	}
	token, valid := parsePasswordResetToken(encodedToken)
	if !valid {
		return ErrInvalidResetToken
	}
	row, found, err := models.UserObjects.Using(reader).Filter(models.UserFields.PrincipalID.Exact(principalID)).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil {
		return err
	}
	if !found {
		return ErrInvalidResetToken
	}
	current, err := resetter.state.directory.accountFromRow(ctx, reader, row)
	if err != nil {
		return err
	}
	now, err := resetter.instant()
	if err = errors.Join(err, ctx.Err()); err != nil {
		return err
	}
	if !resetter.state.keys.accepts(current, token, now, resetter.state.timeout) {
		return ErrInvalidResetToken
	}
	return nil
}

// Prepare validates a token and current password policy, then hashes once
// outside database scopes. It grants no reusable authorization for a write.
func (resetter *PasswordResetter) Prepare(ctx context.Context, principalID, encodedToken, password string) (PreparedPasswordReset, error) {
	before, token, err := resetter.preflight(ctx, principalID, encodedToken, &password)
	if err != nil {
		return PreparedPasswordReset{}, err
	}
	state := resetter.state
	encoded, err := (passwordInput{raw: password}).encode(ctx, state.hasher)
	if err != nil {
		return PreparedPasswordReset{}, err
	}
	candidate, err := auth.NewCredential(before.Profile().Username, encoded, before.value().credential.Principal())
	if err != nil || !candidate.HasUsablePassword() || candidate.MatchesSessionStamp(before.value().credential.SessionStamp()) {
		return PreparedPasswordReset{}, managementError(CodeInvalidConfig, "password_hasher", err)
	}
	if err := ctx.Err(); err != nil {
		return PreparedPasswordReset{}, err
	}
	return PreparedPasswordReset{&preparedPasswordReset{resetter, before.Profile().ID, principalID, token, password, encoded}}, nil
}

// ApplyIn patches password/current revision, revokes all target sessions and
// appends a value-free audit in the caller's native coordinated transaction.
// It never opens or commits a transaction itself. Its result is provisional:
// the owner must roll back on any failure, including later session work, and
// must not publish cookies or success until the enclosing commit is confirmed.
func (resetter *PasswordResetter) ApplyIn(ctx context.Context, session db.Session, change PreparedPasswordReset) error {
	if ctx == nil || resetter == nil || resetter.state == nil || nilIdentityValue(session) || change.state == nil || change.state.owner != resetter {
		return managementError(CodeInvalidInput, "password_reset", nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state := resetter.state
	row, found, err := models.UserObjects.Using(session).Filter(models.UserFields.ID.Exact(change.state.userID)).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil {
		return err
	}
	if !found || row.PrincipalID != change.state.principalID {
		return ErrInvalidResetToken
	}
	current, err := state.directory.accountFromRow(ctx, session, row)
	if err != nil {
		return err
	}
	now, err := resetter.instant()
	if err != nil {
		return err
	}
	if !state.keys.accepts(current, change.state.token, now, state.timeout) {
		return ErrInvalidResetToken
	}
	if err := validUserRevision(row.ID, row.Revision); err != nil {
		return err
	}
	if err := validatePassword(ctx, state.validators, change.state.password, current.Profile()); err != nil {
		return err
	}
	if _, err := models.UserObjects.Patch(ctx, session, row, models.UserPatch{}.WithEncodedPassword(change.state.encoded).WithRevision(row.Revision+1)); err != nil {
		return err
	}
	if _, err := state.backend.RevokePrincipalSessions(ctx, session, change.state.principalID); err != nil {
		return err
	}
	event, err := admin.PrepareEvent(change.state.principalID, "godj_identity.user", row.ID, admin.ActionChange, []string{"password"}, "")
	if err != nil {
		return err
	}
	if err := state.backend.AppendAudit(ctx, session, event); err != nil {
		return err
	}
	return ctx.Err()
}

// ResetPassword performs one complete standalone reset. Consumers that couple
// reset with other durable state use Prepare/ApplyIn in their own native fence.
// No result is published on rollback or unknown outcome; there is no retry.
func (resetter *PasswordResetter) ResetPassword(ctx context.Context, principalID, encodedToken, password string) error {
	change, err := resetter.Prepare(ctx, principalID, encodedToken, password)
	if err != nil {
		return err
	}
	var callbackErr error
	calls, applied := 0, false
	err = resetter.state.backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		calls++
		if calls != 1 || nilIdentityValue(session) {
			callbackErr = managementError(CodePersistence, "transaction_contract", nil)
			return callbackErr
		}
		callbackErr = resetter.ApplyIn(ctx, session, change)
		applied = callbackErr == nil
		return callbackErr
	})
	// Only a direct refusal returned after confirmed rollback is presentable.
	// Cleanup errors, swallowed callbacks and unknown outcomes remain failures.
	if callbackErr == ErrInvalidResetToken && err == callbackErr {
		return ErrInvalidResetToken
	}
	if diagnostics, rejected := validation.Rejected(callbackErr); rejected && errors.Unwrap(callbackErr) == nil && err == callbackErr {
		return validation.Reject(diagnostics, nil)
	}
	if err = errors.Join(err, callbackErr); err != nil {
		return managementWriteFailure(err)
	}
	if calls != 1 || !applied {
		return managementError(CodePersistence, "transaction_contract", nil)
	}
	return nil // A confirmed commit is not undone by a later context cancellation.
}
