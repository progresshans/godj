package auth

import "context"

// Authorizer evaluates one explicit permission for one typed principal.
type Authorizer interface {
	Allowed(context.Context, Principal, Permission) (bool, error)
}

// PrincipalAuthorizer uses the immutable permission snapshot on Principal.
type PrincipalAuthorizer struct{}

func (PrincipalAuthorizer) Allowed(ctx context.Context, principal Principal, permission Permission) (bool, error) {
	if ctx == nil {
		return false, &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !validPermission(string(permission)) {
		return false, &Error{Code: CodeInvalidInput, Field: "permission", Detail: "permission is invalid"}
	}
	return principal.Has(permission), nil
}
