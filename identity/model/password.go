package model

import (
	"context"
	"errors"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// PasswordValidator is a pure, concurrent-safe strength policy over the raw
// password and actual detached typed profile, with its encoded password blank.
// Return value-free diagnostics on "password" or NonField; do not retain either
// argument or publish external effects. Creation and password changes apply the
// selected policy before hashing and again to current state at the write fence.
type PasswordValidator[M any] func(context.Context, string, M) (validation.Errors, error)

// Keep the private callback cause even when it is itself a rejection. Only
// value-free diagnostics returned without a failure become form validation.
func validateAccountPassword[M any](ctx context.Context, binding Binding[M], validate PasswordValidator[M], raw string, row M) error {
	if validate == nil {
		return ctx.Err()
	}
	detached, err := binding.state.objects.ApplyValues(row, map[string]query.Value{binding.state.role.Password: query.String("")})
	if err != nil {
		return managementError(CodePersistence, err)
	}
	diagnostics, failure := validate(ctx, raw, detached)
	if err := errors.Join(failure, ctx.Err()); err != nil {
		return managementError(CodePersistence, err)
	}
	return passwordPolicyDiagnostics(diagnostics)
}

func passwordPolicyDiagnostics(diagnostics validation.Errors) error {
	for _, diagnostic := range diagnostics.All() {
		if diagnostic.Field() != "password" && diagnostic.Field() != validation.NonField {
			return managementError(CodeInvalidConfig, nil)
		}
	}
	if !diagnostics.Empty() {
		return validation.Reject(diagnostics, nil)
	}
	return nil
}

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
