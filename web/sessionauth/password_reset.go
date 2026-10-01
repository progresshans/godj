package sessionauth

import (
	"crypto/hmac"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type PasswordResetResult struct{ change ResponseChange }

func (result PasswordResetResult) Apply(response web.Response) (web.Response, error) {
	return result.change.Apply(response)
}
func (PasswordResetResult) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("sessionauth.PasswordResetResult{redacted}"))
}

func (r *Runtime) CanResetPassword() bool { return r != nil && r.passwordResetPersistence != nil }

// StartPasswordReset exchanges a recipient-only token for server session state.
// The caller redirects to a token-free URL with no-store/no-referrer headers and
// must never reflect the token. No target authentication is granted. Only a
// confirmed create/rotation publishes a cookie; the CSRF cookie is retained.
func (r *Runtime) StartPasswordReset(request *web.Request, principalID, token string) (PasswordResetResult, error) {
	id, err := r.passwordResetSession(request, true)
	if err != nil {
		return PasswordResetResult{}, err
	}
	record, err := r.passwordResetPersistence.StartPasswordReset(request.Context(), id, principalID, token)
	if err != nil {
		return PasswordResetResult{}, passwordResetError(err)
	}
	target, _ := record.Value(auth.SessionResetPrincipalIDKey)
	stored, _ := record.Value(auth.SessionResetTokenKey)
	if err := r.sessions.CheckRecord(record); err != nil || record.ID() == id || target != principalID || token == "" || !hmac.Equal([]byte(stored), []byte(token)) {
		return PasswordResetResult{}, &Error{Code: CodeSession, Detail: "password reset persistence returned an invalid proof; reconciliation is required"}
	}
	return PasswordResetResult{ResponseChange{cookies: []http.Cookie{r.sessionResponseCookie(record.ID().Encoded(), record.AbsoluteExpiresAt())}}}, nil
}

// CheckPasswordReset reads the presented proof without sliding expiry, cleanup,
// hashing or writes. Nil password requests token admission only. Form callers
// verify CSRF before checking submitted password fields; this grants no later
// write authority.
func (r *Runtime) CheckPasswordReset(request *web.Request, principalID string, password *string) error {
	id, err := r.passwordResetSession(request, false)
	if err != nil {
		return err
	}
	return passwordResetError(r.passwordResetPersistence.CheckPasswordReset(request.Context(), id, principalID, password))
}

// ResetPassword consumes the current server proof after the caller has checked
// CSRF and form confirmation. Success clears the target's current auth cookie
// or rotates an anonymous/other-account session with its proof removed. Failure
// publishes no cookie, including an uncertain commit; there is no retry.
func (r *Runtime) ResetPassword(request *web.Request, principalID, password string) (PasswordResetResult, error) {
	id, err := r.passwordResetSession(request, false)
	if err != nil {
		return PasswordResetResult{}, err
	}
	committed, err := r.passwordResetPersistence.ResetPassword(request.Context(), id, principalID, password)
	if err != nil {
		return PasswordResetResult{}, passwordResetError(err)
	}
	record := committed.Record
	var cookie http.Cookie
	if committed.ClearSession {
		if !reflect.DeepEqual(record, sessions.Record{}) {
			return PasswordResetResult{}, &Error{Code: CodeSession, Detail: "password reset persistence returned conflicting session outcomes; reconciliation is required"}
		}
		cookie = r.deletionCookie(r.sessionCookie)
	} else {
		_, hasTarget := record.Value(auth.SessionResetPrincipalIDKey)
		_, hasToken := record.Value(auth.SessionResetTokenKey)
		currentID, _ := record.Value(auth.SessionPrincipalIDKey)
		if err := r.sessions.CheckRecord(record); err != nil || record.ID() == id || hasTarget || hasToken || currentID == principalID {
			return PasswordResetResult{}, &Error{Code: CodeSession, Detail: "password reset persistence returned an invalid session; reconciliation is required"}
		}
		cookie = r.sessionResponseCookie(record.ID().Encoded(), record.AbsoluteExpiresAt())
	}
	return PasswordResetResult{ResponseChange{cookies: []http.Cookie{cookie}}}, nil
}

func (r *Runtime) passwordResetSession(request *web.Request, optional bool) (sessions.ID, error) {
	httpRequest, err := r.request(request)
	if err != nil {
		return sessions.ID{}, err
	}
	if r.passwordResetPersistence == nil {
		return sessions.ID{}, &Error{Code: CodeInvalidConfig, Field: "password_reset_persistence", Detail: "password reset persistence is not configured"}
	}
	encoded, found, cookieErr := r.namedCookie(httpRequest, r.sessionCookie.Name)
	if cookieErr != nil {
		return sessions.ID{}, auth.ErrInvalidResetProof
	}
	if !found {
		if optional {
			return sessions.ID{}, nil
		}
		return sessions.ID{}, auth.ErrInvalidResetProof
	}
	id, err := sessions.ParseID(encoded)
	if err != nil {
		return sessions.ID{}, auth.ErrInvalidResetProof
	}
	return id, nil
}

func passwordResetError(err error) error {
	if err == nil || err == auth.ErrInvalidResetProof {
		return err
	}
	if _, rejected := validation.Rejected(err); rejected && errors.Unwrap(err) == nil {
		return err
	}
	return &Error{Code: CodeSession, Detail: "password reset persistence failed", Cause: err}
}
