package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
)

// PasswordResetConfig binds recipient signing, lifetime and the same typed
// strength policy used for creation and other password changes. Keys are
// host-loaded, independent of session/CSRF keys. Clock and policy must be pure
// and concurrent-safe. Construction does not call either function.
type PasswordResetConfig[M any] struct {
	Keys             auth.PasswordResetKeyRing `json:"-"`
	Timeout          time.Duration
	Clock            func() time.Time `json:"-"`
	MaxPasswordBytes int
	ValidatePassword PasswordValidator[M] `json:"-"`
}

func (PasswordResetConfig[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.PasswordResetConfig{redacted}"))
}

// PasswordResetter owns the selected account's reset tokens and session proof.
// It reads and writes the actual typed model, never a default account copy.
// Reset never authenticates a user or changes LastLogin. The mail requester
// and Web persistence must use this exact immutable owner.
type PasswordResetter[M any] struct{ state *passwordResetState[M] }
type passwordResetState[M any] struct {
	store     *SessionStore[M]
	manager   *sessions.Manager
	hasher    auth.PasswordHasher
	config    PasswordResetConfig[M]
	authority auth.PasswordResetAuthority
	email     ir.Field
	active    ir.Field
}

func (*PasswordResetter[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.PasswordResetter{redacted}"))
}
func (*PasswordResetter[M]) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.model.PasswordResetter{redacted}"`), nil
}

// NewPasswordResetter is normally called by the generated project factory.
// Expected binds the entire host graph as well as the selected role. The
// manager and verifier must belong to this exact SessionStore. No I/O, entropy,
// password work or application callbacks occur during construction.
func NewPasswordResetter[M any](store *SessionStore[M], expected Binding[M], manager *sessions.Manager, verifier *Authenticator[M], config PasswordResetConfig[M]) (*PasswordResetter[M], error) {
	if store == nil || store.state == nil || expected.state == nil || manager == nil || manager.Store() != store || verifier == nil || verifier.inner == nil || verifier.credentials != store.state.credentials || nilModelValue(verifier.hasher) || !config.Keys.Valid() {
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
	if config.Timeout == 0 {
		config.Timeout = 72 * time.Hour
	}
	if config.Timeout < time.Second || config.Timeout%time.Second != 0 {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if config.MaxPasswordBytes == 0 {
		config.MaxPasswordBytes = 1024
	}
	if config.MaxPasswordBytes < 1 || config.MaxPasswordBytes > 4096 {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	role := binding.state.role
	if _, err := admin.PrepareEventTemplate("reset-startup", role.Model.AppLabel+"."+role.Model.ModelName, admin.ActionChange, []string{role.Password}, ""); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	metadata, exists := binding.state.project.Model(role.Model)
	if !exists {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	state := &passwordResetState[M]{store: store, manager: manager, hasher: verifier.hasher, config: config, authority: auth.NewPasswordResetAuthority()}
	if role.Email != "" {
		state.email, exists = namedField(metadata, role.Email)
		if !exists {
			return nil, managementError(CodeInvalidConfig, nil)
		}
	}
	if role.Active.Field != "" {
		state.active, exists = namedField(metadata, role.Active.Field)
		if !exists {
			return nil, managementError(CodeInvalidConfig, nil)
		}
	}
	return &PasswordResetter[M]{state}, nil
}

func (owner *PasswordResetter[M]) Sessions() *sessions.Manager {
	if owner == nil || owner.state == nil {
		return nil
	}
	return owner.state.manager
}
func (owner *PasswordResetter[M]) PasswordResetAuthority() auth.PasswordResetAuthority {
	if owner == nil || owner.state == nil {
		return auth.PasswordResetAuthority{}
	}
	return owner.state.authority
}
func (owner *PasswordResetter[M]) PasswordResetEmailAvailable() bool {
	return owner != nil && owner.state != nil && owner.state.email.Name != ""
}
func (owner *PasswordResetter[M]) MaxPasswordBytes() int {
	if owner == nil || owner.state == nil {
		return 0
	}
	return owner.state.config.MaxPasswordBytes
}

func (owner *PasswordResetter[M]) valid(ctx context.Context) error {
	if owner == nil || owner.state == nil {
		return managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) {
		return managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return accountPasswordFailure(err)
	}
	return nil
}
func (state *passwordResetState[M]) instant() (time.Time, error) {
	now := state.config.Clock().UTC()
	if now.Year() < 1970 || now.Year() > 9999 {
		return time.Time{}, managementError(CodeInvalidConfig, nil)
	}
	return now, nil
}

type passwordResetAccount[M any] struct {
	row         M
	id          int64
	domain      auth.CredentialDomain
	credential  auth.Credential
	observation auth.PasswordResetState
	present     bool
}

func (state *passwordResetState[M]) domainIn(ctx context.Context, scope db.Queryer) (auth.CredentialDomain, error) {
	if err := validateAccountSessionScope(ctx, scope); err != nil {
		return auth.CredentialDomain{}, err
	}
	binding := state.store.state.credentials.state.binding
	receipt, err := binding.RequireDomainIn(ctx, scope)
	if err != nil {
		return auth.CredentialDomain{}, err
	}
	return binding.CredentialDomain(receipt.Generation())
}

// The caller observed this domain in the SAME borrowed scope as row. All
// delivery and signature material is detached from this single observation.
func (state *passwordResetState[M]) accountFromRow(ctx context.Context, scope db.Queryer, namespace auth.CredentialDomain, row M) (passwordResetAccount[M], error) {
	var zero passwordResetAccount[M]
	store := state.store.state.credentials
	binding := store.state.binding
	objects := binding.state.objects
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
	values, err := objects.ModelValues(row)
	if err != nil {
		return zero, err
	}
	email, err := state.emailValue(values)
	if err != nil {
		return zero, err
	}
	var lastLogin *time.Time
	if field := binding.state.role.LastLogin; field != "" {
		value, exists := values[field]
		if !exists {
			return zero, invalidAccountCredential()
		}
		if !value.IsNull() {
			at, valid := value.DateTime()
			if !valid {
				return zero, invalidAccountCredential()
			}
			lastLogin = &at
		}
	}
	observation, err := auth.NewPasswordResetState(id, credential, email, lastLogin)
	if err != nil {
		return zero, err
	}
	return passwordResetAccount[M]{row, id, namespace, credential, observation, true}, nil
}

func (state *passwordResetState[M]) emailValue(values map[string]query.Value) (string, error) {
	if state.email.Name == "" {
		return "", nil
	}
	email, valid := values[state.email.Name].String()
	if !valid || len(email) > auth.MaximumPasswordResetEmailBytes || !utf8.ValidString(email) || strings.ContainsRune(email, 0) || state.email.MaxLength > 0 && utf8.RuneCountInString(email) > state.email.MaxLength {
		return "", invalidAccountCredential()
	}
	return email, nil
}

func (state *passwordResetState[M]) accountIn(ctx context.Context, scope db.Queryer, principalID string) (passwordResetAccount[M], error) {
	var zero passwordResetAccount[M]
	namespace, err := state.domainIn(ctx, scope)
	if err != nil {
		return zero, err
	}
	store := state.store.state.credentials
	row, present, err := store.state.binding.state.objects.Using(scope).Filter(store.state.principal.Exact(principalID)).OrderBy(store.state.key.Asc()).First(ctx)
	if err != nil || !present {
		return zero, err
	}
	current, err := state.accountFromRow(ctx, scope, namespace, row)
	if err != nil {
		return zero, err
	}
	if current.credential.Principal().ID() != principalID {
		return zero, invalidAccountCredential()
	}
	return current, nil
}

func (owner *PasswordResetter[M]) observe(ctx context.Context, principalID string) (passwordResetAccount[M], error) {
	return db.ReadSnapshotResult(ctx, owner.state.store.state.backend, func(work context.Context, scope db.Queryer) (passwordResetAccount[M], error) {
		return owner.state.accountIn(work, scope, principalID)
	})
}

// IssueToken is privileged recipient-only delivery. It never exposes an
// account lookup response, hashes a password or grants a session. Final reset
// admission always checks the current row/domain and lifetime again.
func (owner *PasswordResetter[M]) IssueToken(ctx context.Context, principalID string) (auth.PasswordResetToken, error) {
	if err := owner.valid(ctx); err != nil {
		return auth.PasswordResetToken{}, err
	}
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil {
		return auth.PasswordResetToken{}, auth.ErrInvalidResetToken
	}
	current, err := owner.observe(ctx, principalID)
	if err != nil {
		return auth.PasswordResetToken{}, accountPasswordFailure(err)
	}
	if !current.present {
		return auth.PasswordResetToken{}, auth.ErrInvalidResetToken
	}
	now, err := owner.state.instant()
	if err = errors.Join(err, ctx.Err()); err != nil {
		return auth.PasswordResetToken{}, accountPasswordFailure(err)
	}
	return owner.state.config.Keys.Issue(current.observation, now.Unix())
}

func (state *passwordResetState[M]) validatePassword(ctx context.Context, password string, row M) error {
	if password == "" {
		return profileInputError("password", "required")
	}
	if len(password) > state.config.MaxPasswordBytes {
		return profileInputError("password", "max_length")
	}
	return validateAccountPassword(ctx, state.store.state.credentials.state.binding, state.config.ValidatePassword, password, row)
}

func (owner *PasswordResetter[M]) preflight(ctx context.Context, principalID, encoded string, password *string) (passwordResetAccount[M], auth.PasswordResetSignature, error) {
	var zero passwordResetAccount[M]
	if err := owner.valid(ctx); err != nil {
		return zero, auth.PasswordResetSignature{}, err
	}
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil {
		return zero, auth.PasswordResetSignature{}, auth.ErrInvalidResetProof
	}
	token, valid := auth.ParsePasswordResetToken(encoded)
	if !valid {
		return zero, auth.PasswordResetSignature{}, auth.ErrInvalidResetProof
	}
	current, err := owner.observe(ctx, principalID)
	if err != nil {
		return zero, auth.PasswordResetSignature{}, accountPasswordFailure(err)
	}
	now, err := owner.state.instant()
	if err = errors.Join(err, ctx.Err()); err != nil {
		return zero, auth.PasswordResetSignature{}, accountPasswordFailure(err)
	}
	if !current.present || !owner.state.config.Keys.Accepts(current.observation, token, now.Unix(), owner.state.config.Timeout) {
		return zero, auth.PasswordResetSignature{}, auth.ErrInvalidResetProof
	}
	if password != nil {
		if err := owner.state.validatePassword(ctx, *password, current.row); err != nil {
			return zero, auth.PasswordResetSignature{}, err
		}
	}
	return current, token, nil
}
