package sessionauth

import (
	"errors"
	"net/http"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type PasswordChangeResult struct {
	principal auth.Principal
	change    ResponseChange
}

func (result PasswordChangeResult) Principal() auth.Principal { return result.principal }
func (result PasswordChangeResult) Apply(response web.Response) (web.Response, error) {
	return result.change.Apply(response)
}
func (PasswordChangeResult) String() string   { return "sessionauth.PasswordChangeResult{redacted}" }
func (PasswordChangeResult) GoString() string { return "sessionauth.PasswordChangeResult{redacted}" }

// CanChangePassword reports an explicitly configured capability without I/O.
func (r *Runtime) CanChangePassword() bool {
	return r != nil && r.passwordChangePersistence != nil
}

// ChangePassword changes the owner of the presented server session. Callers
// verify CSRF and form confirmation first. No target identity is accepted from
// input. Failure publishes no cookie or session touch; only a confirmed commit
// replaces the session cookie. The existing independent CSRF secret is retained.
func (r *Runtime) ChangePassword(request *web.Request, oldPassword, newPassword string) (PasswordChangeResult, error) {
	httpRequest, err := r.request(request)
	if err != nil {
		return PasswordChangeResult{}, err
	}
	if r.passwordChangePersistence == nil {
		return PasswordChangeResult{}, &Error{Code: CodeInvalidConfig, Field: "password_change_persistence", Detail: "password change persistence is not configured"}
	}
	encoded, found, cookieErr := r.namedCookie(httpRequest, r.sessionCookie.Name)
	if cookieErr != nil || !found {
		return PasswordChangeResult{}, auth.ErrInvalidCredentials
	}
	id, err := sessions.ParseID(encoded)
	if err != nil {
		return PasswordChangeResult{}, auth.ErrInvalidCredentials
	}
	committed, err := r.passwordChangePersistence.ChangePassword(httpRequest.Context(), id, oldPassword, newPassword)
	if err != nil {
		if _, rejected := validation.Rejected(err); rejected && errors.Unwrap(err) == nil || err == auth.ErrInvalidCredentials {
			return PasswordChangeResult{}, err
		}
		// Keep an execution wrapper even when the cause contains cancellation.
		// Returning the original caused rejection would make it renderable as
		// ordinary input again. errors.Is/As still retain the execution cause.
		return PasswordChangeResult{}, &Error{Code: CodeSession, Detail: "password change persistence failed", Cause: err}
	}
	record := committed.Record
	principal := committed.Credential.Principal()
	storedID, _ := record.Value(auth.SessionPrincipalIDKey)
	stamp, _ := record.Value(auth.SessionCredentialStampKey)
	if !record.ID().Valid() || record.ID() == id || !principal.Authenticated() || storedID != principal.ID() || !committed.Credential.HasUsablePassword() || !committed.Credential.MatchesSessionStamp(stamp) {
		return PasswordChangeResult{}, &Error{Code: CodeSession, Detail: "password change persistence returned an invalid result; reconciliation is required"}
	}
	change := ResponseChange{cookies: []http.Cookie{r.sessionResponseCookie(record.ID().Encoded(), record.AbsoluteExpiresAt())}}
	return PasswordChangeResult{principal: principal, change: change}, nil
}
