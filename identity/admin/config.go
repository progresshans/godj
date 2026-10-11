// Package identityadmin registers the built-in identity models with Admin.
// Schema IR owns fields; the identity manager owns current authority, writes,
// revisions, credentials and audit. No generated model or hash reaches HTML.
package identityadmin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
)

type Backend interface {
	identity.ManagementBackend
	identity.ManagementHistoryReader
	db.CoordinatedRelationAtomic
}

// DeletionPolicies must be bound from the complete host project. The adapter
// never substitutes the built-in app's smaller graph for external references.
type DeletionPolicies struct {
	Users       orm.RelationDeleter[models.User]
	Groups      orm.RelationDeleter[models.Group]
	Permissions orm.RelationDeleter[models.Permission]
}

type Config struct{ state *configState }
type configState struct {
	backend     Backend
	hasher      auth.PasswordHasher
	authorizer  auth.Authorizer
	deletions   DeletionPolicies
	validators  []identity.PasswordValidator
	principalID func(context.Context) (string, error)
}

func NewConfig(backend Backend, hasher auth.PasswordHasher, authorizer auth.Authorizer, deletions DeletionPolicies) Config {
	return Config{state: &configState{backend: backend, hasher: hasher, authorizer: authorizer, deletions: deletions, principalID: randomPrincipalID}}
}

func (config Config) WithPasswordValidators(validators ...identity.PasswordValidator) Config {
	var state configState
	if config.state != nil {
		state = *config.state
	}
	state.validators = append([]identity.PasswordValidator(nil), validators...)
	return Config{state: &state}
}

// WithPrincipalIDs accepts a host-owned, concurrency-safe opaque ID source.
// Browser input never supplies principal IDs. Its context/error remain visible.
func (config Config) WithPrincipalIDs(source func(context.Context) (string, error)) Config {
	var state configState
	if config.state != nil {
		state = *config.state
	}
	state.principalID = source
	return Config{state: &state}
}

func (Config) Format(s fmt.State, _ rune)   { _, _ = s.Write([]byte("identityadmin.Config{redacted}")) }
func (Config) MarshalJSON() ([]byte, error) { return []byte(`"identityadmin.Config{redacted}"`), nil }

type registration struct {
	manager     *identity.Manager
	deletions   DeletionPolicies
	principalID func(context.Context) (string, error)
}

// Register performs no I/O. Publish the Builder only after this returns nil.
func Register(builder *admin.Builder, config Config) error {
	if config.state == nil || config.state.principalID == nil {
		return &admin.ConfigError{Path: "identity.config", Code: "invalid"}
	}
	s := config.state
	manager, err := identity.NewManager(s.backend, s.hasher, s.authorizer, identity.WithPasswordValidators(s.validators...))
	if err != nil {
		return err
	}
	for _, check := range []func() error{s.deletions.Users.ValidateBinding, s.deletions.Groups.ValidateBinding, s.deletions.Permissions.ValidateBinding} {
		if err := check(); err != nil {
			return err
		}
	}
	a := &registration{manager: manager, deletions: s.deletions, principalID: s.principalID}
	if err := a.registerPermission(builder); err != nil {
		return err
	}
	if err := a.registerGroup(builder); err != nil {
		return err
	}
	return a.registerUser(builder)
}

func randomPrincipalID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "user-" + hex.EncodeToString(raw[:]), ctx.Err()
}
