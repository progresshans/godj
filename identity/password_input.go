package identity

import (
	"context"

	"github.com/progresshans/godj/auth"
)

// passwordInput is selected only by the explicit service entry point. An empty
// or omitted raw password never silently disables an existing credential.
type passwordInput struct {
	raw      string
	unusable bool
}

func (p passwordInput) validate(ctx context.Context, manager *Manager, profile Profile) error {
	if p.unusable {
		return ctx.Err()
	}
	return manager.validatePassword(ctx, p.raw, profile)
}

func (p passwordInput) encode(ctx context.Context, hasher auth.PasswordHasher) (string, error) {
	if p.unusable {
		encoded, err := auth.MakeUnusablePassword(ctx)
		if err != nil {
			return "", managementError(CodePersistence, "password", err)
		}
		return encoded, nil
	}
	encoded, err := managementPassword(ctx, hasher, p.raw)
	if err != nil {
		return "", err
	}
	if err := hasher.ValidateEncoded(encoded); err != nil {
		return "", managementError(CodeInvalidConfig, "password_hasher", err)
	}
	return encoded, nil
}
