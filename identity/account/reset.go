package identityaccount

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

const resetTokenMarker = "set-password"

// PasswordResetErrorReporter receives private execution failures separately
// from public responses. It must be concurrency-safe and should retain causes
// without logging addresses, links or other credential material. It is never
// called for ordinary invalid email/proof/password input. A reporter panic is
// contained so it cannot turn an eligible-email failure into a public signal.
type PasswordResetErrorReporter func(context.Context, error)

// PasswordResetConfig enables the reset surface. Construct Mailer from the
// same resetter/key/policy owner as the Web runtime's reset persistence. Its
// normalized ConfirmPath must equal BasePath + "/reset". Nothing is inferred
// from Host or forwarding headers. Startup performs no mail or database I/O.
type PasswordResetConfig struct {
	Mailer      *identity.PasswordResetMailer
	ReportError PasswordResetErrorReporter
}

func (PasswordResetConfig) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identityaccount.PasswordResetConfig{redacted}"))
}
func (PasswordResetConfig) MarshalJSON() ([]byte, error) {
	return []byte(`"identityaccount.PasswordResetConfig{redacted}"`), nil
}

func (a *Application) prepareReset(config *PasswordResetConfig) error {
	if config == nil {
		return nil
	}
	if !a.auth.CanResetPassword() || config.Mailer.ConfirmPath() != a.basePath+"/reset" || config.ReportError == nil {
		return invalidConfig("password_reset")
	}
	for _, path := range []string{a.basePath + "/reset/", a.apiBasePath + "/reset/"} {
		if !a.auth.CookiesApplyTo(path) {
			return invalidConfig("password_reset_cookie_paths")
		}
	}
	coverage, err := web.DescribeRoutePrefix(a.basePath+"/reset/<str:uid>/<str:token>/", a.apiBasePath+"/")
	if err != nil || coverage.Some {
		return invalidConfig("password_reset_paths")
	}
	a.resetMailer, a.reportResetError = config.Mailer, config.ReportError
	// The shared email cleaner owns Unicode trimming and ordered diagnostics;
	// this field describes rendering and the bounded Form key allowlist.
	email, err := forms.EmailField("email", forms.WithLabel("Email address"), forms.WithMaxLength(254))
	if err != nil {
		return err
	}
	a.resetEmail, err = forms.NewSpec([]forms.Field{email})
	if err != nil {
		return err
	}
	first, err := passwordField("new_password1", "New password")
	if err != nil {
		return err
	}
	second, err := passwordField("new_password2", "New password confirmation")
	if err != nil {
		return err
	}
	a.resetPassword, err = forms.NewSpec([]forms.Field{first, second}, forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		first, a := values.String("new_password1")
		second, b := values.String("new_password2")
		if a && b && first != "" && second != "" && first != second {
			return validation.NewErrors(validation.New("new_password2", "password_mismatch"))
		}
		return validation.Errors{}
	}))
	return err
}

func (a *Application) reportReset(ctx context.Context, err error) {
	if err == nil {
		return
	}
	defer func() { _ = recover() }()
	a.reportResetError(ctx, err)
}

func (a *Application) requestResetMail(ctx context.Context, email string) {
	defer func() {
		if recover() != nil {
			a.reportReset(ctx, errors.New("account password reset request panicked"))
		}
	}()
	if err := a.resetMailer.Request(ctx, email); err != nil {
		a.reportReset(ctx, err)
	}
}

// Reset responses preserve their privacy headers even when an execution or
// template error occurs. A raw link is never returned in a response or error.
func (a *Application) protectResetResponse(handler web.Handler) web.Handler {
	return func(request *web.Request) (response web.Response, err error) {
		defer func() {
			if recover() != nil {
				err = errors.New("account password reset handler panicked")
			}
			if err != nil {
				a.reportReset(request.Context(), err)
				response, err = textResponse(http.StatusInternalServerError)
			}
			if err == nil {
				header := response.Header()
				if header == nil {
					header = make(http.Header)
				}
				header.Set("Cache-Control", "no-store")
				header.Set("Referrer-Policy", "no-referrer")
				response, err = response.WithHeaders(header)
			}
		}()
		return handler(request)
	}
}

func (a *Application) resetRoutes() []web.Route {
	if a.resetMailer == nil {
		return nil
	}
	routes := []web.Route{
		{Name: a.namespace + ":account-reset-request", Method: "GET", Path: a.basePath + "/reset/", Handler: a.resetRequestGet},
		{Name: a.namespace + ":account-reset-submit", Method: "POST", Path: a.basePath + "/reset/", Handler: a.resetRequestPost},
		{Name: a.namespace + ":account-reset-sent", Method: "GET", Path: a.basePath + "/reset/sent/", Handler: a.resetSent},
		{Name: a.namespace + ":account-reset-complete", Method: "GET", Path: a.basePath + "/reset/complete/", Handler: a.resetComplete},
		{Name: a.namespace + ":account-reset-link", Method: "GET", Path: a.basePath + "/reset/<str:uid>/<str:token>/", Handler: a.resetLinkGet},
		{Name: a.namespace + ":account-reset-confirm", Method: "POST", Path: a.basePath + "/reset/<str:uid>/<str:token>/", Handler: a.resetLinkPost},
	}
	return routes
}
func (a *Application) resetRequestGet(r *web.Request) (web.Response, error) {
	if !noQuery(r) {
		return textResponse(400)
	}
	return a.render(r, "reset-request", a.resetEmail, nil, validation.Errors{}, "")
}
func (a *Application) resetSent(r *web.Request) (web.Response, error) {
	if !noQuery(r) {
		return textResponse(400)
	}
	return a.render(r, "reset-sent", forms.Spec{}, nil, validation.Errors{}, "")
}
func (a *Application) resetComplete(r *web.Request) (web.Response, error) {
	if !noQuery(r) {
		return textResponse(400)
	}
	return a.render(r, "reset-complete", forms.Spec{}, nil, validation.Errors{}, "")
}
func (a *Application) resetInvalid(r *web.Request) (web.Response, error) {
	return a.render(r, "reset-invalid", forms.Spec{}, nil, validation.Errors{}, "")
}
func (a *Application) resetRequestPost(r *web.Request) (web.Response, error) {
	if !noQuery(r) {
		return textResponse(400)
	}
	data, status, err := parseForm(r, a.resetEmail, false)
	if err != nil {
		return web.Response{}, err
	}
	if status != 0 {
		return textResponse(status)
	}
	if err := a.auth.VerifyCSRF(r, data["csrfmiddlewaretoken"]); err != nil {
		return csrfFailure(err)
	}
	email, failures := identity.CleanPasswordResetEmail(data.Get("email"))
	if len(data["email"]) > 1 {
		failures = validation.NewErrors(validation.New("email", "multiple"))
	}
	if !failures.Empty() {
		return a.render(r, "reset-request", a.resetEmail, data, failures, "")
	}
	a.requestResetMail(r.Context(), email)
	return redirect(a.basePath + "/reset/sent/")
}

func resetPrincipal(r *web.Request) (string, bool) {
	uid, ok := r.StringParameter("uid")
	if !ok || len(uid) > 172 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(uid)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != uid {
		return "", false
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: string(raw)})
	if err != nil {
		return "", false
	}
	return principal.ID(), true
}
func (a *Application) exchangeResetToken(r *web.Request, principal, token string) (web.Response, error) {
	uid, _ := r.StringParameter("uid")
	location, err := r.ReverseWith(a.namespace+":account-reset-link", web.StringArgument("uid", uid), web.StringArgument("token", resetTokenMarker))
	if err != nil {
		return web.Response{}, err
	}
	response, err := redirect(location)
	if err != nil {
		return web.Response{}, err
	}
	result, err := a.auth.StartPasswordReset(r, principal, token)
	if err == auth.ErrInvalidResetProof {
		return a.resetInvalid(r)
	}
	if err != nil {
		return a.resetFailure(r, err)
	}
	return result.Apply(response)
}
func (a *Application) resetLinkGet(r *web.Request) (web.Response, error) {
	if !noQuery(r) {
		return textResponse(400)
	}
	principal, ok := resetPrincipal(r)
	if !ok {
		return a.resetInvalid(r)
	}
	token, _ := r.StringParameter("token")
	if token != resetTokenMarker {
		return a.exchangeResetToken(r, principal, token)
	}
	if err := a.auth.CheckPasswordReset(r, principal, nil); err != nil {
		if err == auth.ErrInvalidResetProof {
			return a.resetInvalid(r)
		}
		return a.resetFailure(r, err)
	}
	return a.render(r, "reset-confirm", a.resetPassword, nil, validation.Errors{}, "")
}
func (a *Application) resetLinkPost(r *web.Request) (web.Response, error) {
	if !noQuery(r) {
		return textResponse(400)
	}
	data, status, err := parseForm(r, a.resetPassword, false)
	if err != nil {
		return web.Response{}, err
	}
	if status != 0 {
		return textResponse(status)
	}
	if err := a.auth.VerifyCSRF(r, data["csrfmiddlewaretoken"]); err != nil {
		return csrfFailure(err)
	}
	principal, ok := resetPrincipal(r)
	if !ok {
		return a.resetInvalid(r)
	}
	token, _ := r.StringParameter("token")
	if token != resetTokenMarker {
		return a.exchangeResetToken(r, principal, token)
	}
	if err := a.auth.CheckPasswordReset(r, principal, nil); err != nil {
		if err == auth.ErrInvalidResetProof {
			return a.resetInvalid(r)
		}
		return a.resetFailure(r, err)
	}
	form, err := bind(a.resetPassword, data)
	if err != nil {
		return web.Response{}, err
	}
	if !form.Valid() {
		err := a.auth.CheckPasswordReset(r, principal, checkedField(form, "new_password2"))
		if err == auth.ErrInvalidResetProof {
			return a.resetInvalid(r)
		}
		failures, rejected := validation.Rejected(err)
		if err != nil && !rejected {
			return a.resetFailure(r, err)
		}
		failures, err = presentPasswordErrors(failures, "new_password2")
		if err != nil {
			return web.Response{}, err
		}
		return a.render(r, "reset-confirm", a.resetPassword, data, orderedErrors(a.resetPassword, form.Errors(), failures), "")
	}
	password, _ := form.Cleaned().String("new_password1")
	result, err := a.auth.ResetPassword(r, principal, password)
	if err == auth.ErrInvalidResetProof {
		return a.resetInvalid(r)
	}
	if failures, rejected := validation.Rejected(err); rejected {
		failures, err = presentPasswordErrors(failures, "new_password2")
		if err != nil {
			return web.Response{}, err
		}
		return a.render(r, "reset-confirm", a.resetPassword, data, failures, "")
	}
	if err != nil {
		return a.resetFailure(r, err)
	}
	response, err := redirect(a.basePath + "/reset/complete/")
	if err != nil {
		return web.Response{}, err
	}
	return result.Apply(response)
}

func (a *Application) resetFailure(r *web.Request, err error) (web.Response, error) {
	if unknownOutcome(err) {
		a.reportReset(r.Context(), err)
	}
	return formFailure(err)
}

// Privacy covers routing and negotiation refusals as well as matched handlers.
func (a *Application) resetPrivacy(next web.Handler) web.Handler {
	protected := a.protectResetResponse(next)
	return func(r *web.Request) (web.Response, error) {
		if strings.HasPrefix(r.Path(), a.basePath+"/reset/") || strings.HasPrefix(r.Path(), a.apiBasePath+"/reset/") || r.Path() == a.OpenAPIPath() {
			return protected(r)
		}
		return next(r)
	}
}
