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
	id, err := r.passwordChangeSession(request)
	if err != nil {
		return PasswordChangeResult{}, err
	}
	committed, err := r.passwordChangePersistence.ChangePassword(request.Context(), id, oldPassword, newPassword)
	if err != nil {
		return PasswordChangeResult{}, passwordChangeError(err)
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

// CheckPasswordChange provides non-mutating selected-field diagnostics for a
// bound form. Callers verify CSRF first. Nil fields have failed form cleaning;
// they are skipped here. This method never returns authority for a later write.
func (r *Runtime) CheckPasswordChange(request *web.Request, oldPassword, newPassword *string) error {
	id, err := r.passwordChangeSession(request)
	if err != nil {
		return err
	}
	return passwordChangeError(r.passwordChangePersistence.CheckPasswordChange(request.Context(), id, oldPassword, newPassword))
}

func (r *Runtime) passwordChangeSession(request *web.Request) (sessions.ID, error) {
	httpRequest, err := r.request(request)
	if err != nil {
		return sessions.ID{}, err
	}
	if r.passwordChangePersistence == nil {
		return sessions.ID{}, &Error{Code: CodeInvalidConfig, Field: "password_change_persistence", Detail: "password change persistence is not configured"}
	}
	encoded, found, cookieErr := r.namedCookie(httpRequest, r.sessionCookie.Name)
	if cookieErr != nil || !found {
		return sessions.ID{}, auth.ErrInvalidCredentials
	}
	id, err := sessions.ParseID(encoded)
	if err != nil {
		return sessions.ID{}, auth.ErrInvalidCredentials
	}
	return id, nil
}

func passwordChangeError(err error) error {
	if err == nil || err == auth.ErrInvalidCredentials {
		return err
	}
	if _, rejected := validation.Rejected(err); rejected && errors.Unwrap(err) == nil {
		return err
	}
	// Retain an execution wrapper even for cancellation. Returning a caused
	// rejection unchanged would expose it as ordinary input again.
	return &Error{Code: CodeSession, Detail: "password change persistence failed", Cause: err}
}
