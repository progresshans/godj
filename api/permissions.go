package api

import (
	"context"
	"errors"
	"slices"

	"github.com/progresshans/godj/auth"
)

// PermissionPolicy requires every Required permission and, when Alternatives
// is nonempty, at least one alternative. The two ordered groups are immutable;
// the zero policy is invalid and never means anonymous or principal-only access.
type PermissionPolicy struct {
	required, alternatives []auth.Permission
	valid                  bool
}

// NewPermissionPolicy validates canonical, distinct permissions across both
// groups. One group may be empty, but their union must be nonempty and bounded.
// Construction performs no authorization or I/O.
func NewPermissionPolicy(required, alternatives []auth.Permission) (PermissionPolicy, error) {
	if len(required) > auth.MaximumPermissions || len(alternatives) > auth.MaximumPermissions-len(required) || len(required)+len(alternatives) == 0 {
		return PermissionPolicy{}, &Error{Code: FailureInvalidConfig, Field: "permissions", Detail: "permission policy must be nonempty and bounded"}
	}
	combined := append(slices.Clone(required), alternatives...)
	owned, err := auth.RequiredPermissions(combined[0], combined[1:]...)
	if err != nil {
		return PermissionPolicy{}, &Error{Code: FailureInvalidConfig, Field: "permissions", Detail: "permission policy must be canonical and distinct", Cause: err}
	}
	return PermissionPolicy{required: owned[:len(required):len(required)], alternatives: owned[len(required):], valid: true}, nil
}

func (policy PermissionPolicy) Valid() bool                 { return policy.valid }
func (policy PermissionPolicy) Required() []auth.Permission { return slices.Clone(policy.required) }
func (policy PermissionPolicy) Alternatives() []auth.Permission {
	return slices.Clone(policy.alternatives)
}

// Check invokes the caller's current-authority check in declaration order:
// all mandatory grants first, then alternatives until the first definite
// grant. The callback owns the principal/source and deny-overlay boundary.
// An error or cancellation never falls back to another alternative. A policy
// and a previous successful check are not authority for a later operation.
func (policy PermissionPolicy) Check(ctx context.Context, allowed func(context.Context, auth.Permission) (bool, error)) (bool, error) {
	if !policy.valid || ctx == nil || allowed == nil {
		return false, &Error{Code: FailureInvalidConfig, Field: "permissions", Detail: "permission policy, context and current authority are required"}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	check := func(permission auth.Permission) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		granted, err := allowed(ctx, permission)
		if canceled := ctx.Err(); canceled != nil {
			return false, errors.Join(err, canceled)
		}
		if err != nil {
			return false, err
		}
		return granted, nil
	}
	for _, permission := range policy.required {
		granted, err := check(permission)
		if err != nil || !granted {
			return false, err
		}
	}
	if len(policy.alternatives) == 0 {
		return true, nil
	}
	for _, permission := range policy.alternatives {
		granted, err := check(permission)
		if err != nil || granted {
			return granted && err == nil, err
		}
	}
	return false, nil
}
