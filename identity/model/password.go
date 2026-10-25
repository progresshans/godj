package model

import (
	"context"
	"errors"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
)

// PasswordValidator is a pure, concurrent-safe strength policy over the raw
// password and actual detached typed profile, with its encoded password blank.
// Return value-free diagnostics on "password" or NonField; do not retain either
// argument or publish external effects. Creation and password changes apply the
// selected policy before hashing and again to current state at the write fence.
type PasswordValidator[M any] func(context.Context, string, M) (validation.Errors, error)

func encodeUsablePassword(ctx context.Context, hasher auth.PasswordHasher, raw string) (string, error) {
	encoded, err := hasher.Hash(ctx, raw)
	if canceled := ctx.Err(); canceled != nil {
		return "", managementError(CodeInvalidInput, errors.Join(err, canceled))
	}
	if err != nil {
		if invalid, ok := err.(*auth.Error); ok && invalid != nil && invalid.Code == auth.CodeInvalidInput && invalid.Field == "password" && invalid.Unwrap() == nil {
			return "", profileInputError("password", "invalid")
		}
		return "", managementError(CodeInvalidInput, err)
	}
	if err := hasher.ValidateEncoded(encoded); err != nil {
		return "", managementError(CodeInvalidConfig, err)
	}
	return encoded, nil
}
