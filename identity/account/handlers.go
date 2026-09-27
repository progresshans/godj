package identityaccount

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func (a *Application) loginGet(request *web.Request) (web.Response, error) {
	values, ok := loginQuery(request)
	if !ok {
		return textResponse(400)
	}
	principal, err := a.auth.InspectPrincipal(request)
	if err != nil {
		return formFailure(err)
	}
	next := a.next(values.Get("next"))
	if principal.Authenticated() {
		return redirect(next)
	}
	return a.render(request, "login", a.login, nil, validation.Errors{}, next)
}

func (a *Application) loginPost(request *web.Request) (web.Response, error) {
	if !noQuery(request) {
		return textResponse(400)
	}
	data, status, err := parseForm(request, a.login, true)
	if err != nil {
		return web.Response{}, err
	}
	if status != 0 {
		return textResponse(status)
	}
	if err := a.auth.VerifyCSRF(request, data["csrfmiddlewaretoken"]); err != nil {
		return csrfFailure(err)
	}
	form, err := bind(a.login, data)
	if err != nil {
		return web.Response{}, err
	}
	next := a.next(data.Get("next"))
	if !form.Valid() {
		return a.render(request, "login", a.login, data, form.Errors(), next)
	}
	username, _ := form.Cleaned().String("username")
	password, _ := form.Cleaned().String("password")
	result, err := a.auth.Login(request, username, password)
	if err == auth.ErrInvalidCredentials {
		return a.render(request, "login", a.login, data, validation.NewErrors(validation.New(validation.NonField, "invalid_login")), next)
	}
	if err != nil {
		return formFailure(err)
	}
	response, err := redirect(next)
	if err != nil {
		return web.Response{}, err
	}
	return result.Apply(response)
}

func (a *Application) logoutPost(request *web.Request) (web.Response, error) {
	if !noQuery(request) {
		return textResponse(400)
	}
	data, status, err := parseForm(request, forms.Spec{}, false)
	if err != nil {
		return web.Response{}, err
	}
	if status != 0 {
		return textResponse(status)
	}
	if err := a.auth.VerifyCSRF(request, data["csrfmiddlewaretoken"]); err != nil {
		return csrfFailure(err)
	}
	change, err := a.auth.Logout(request)
	if err != nil {
		return formFailure(err)
	}
	response, err := redirect(a.basePath + "/login/")
	if err != nil {
		return web.Response{}, err
	}
	return change.Apply(response)
}

func (a *Application) authenticated(request *web.Request) (bool, error) {
	principal, err := a.auth.InspectPrincipal(request)
	return principal.Authenticated(), err
}
func (a *Application) loginRedirect() (web.Response, error) {
	return redirect(a.basePath + "/login/?next=" + url.QueryEscape(a.basePath+"/password/"))
}

func (a *Application) passwordGet(request *web.Request) (web.Response, error) {
	ok, err := a.authenticated(request)
	if err != nil {
		return formFailure(err)
	}
	if !ok {
		return a.loginRedirect()
	}
	if !noQuery(request) {
		return textResponse(400)
	}
	return a.render(request, "password", a.password, nil, validation.Errors{}, "")
}
func (a *Application) passwordDone(request *web.Request) (web.Response, error) {
	ok, err := a.authenticated(request)
	if err != nil {
		return formFailure(err)
	}
	if !ok {
		return a.loginRedirect()
	}
	if !noQuery(request) {
		return textResponse(400)
	}
	return a.render(request, "done", forms.Spec{}, nil, validation.Errors{}, "")
}

func (a *Application) passwordPost(request *web.Request) (web.Response, error) {
	ok, err := a.authenticated(request)
	if err != nil {
		return formFailure(err)
	}
	if !ok {
		return a.loginRedirect()
	}
	if !noQuery(request) {
		return textResponse(400)
	}
	data, status, err := parseForm(request, a.password, false)
	if err != nil {
		return web.Response{}, err
	}
	if status != 0 {
		return textResponse(status)
	}
	if err := a.auth.VerifyCSRF(request, data["csrfmiddlewaretoken"]); err != nil {
		return csrfFailure(err)
	}
	form, err := bind(a.password, data)
	if err != nil {
		return web.Response{}, err
	}
	if !form.Valid() {
		// Django validates each successfully cleaned field even when another
		// field failed. A confirmation error suppresses new-password policy,
		// but it does not suppress the old-password check. This path never hashes.
		err := a.auth.CheckPasswordChange(request, checkedField(form, "old_password"), checkedField(form, "new_password2"))
		if err == auth.ErrInvalidCredentials {
			return a.loginRedirect()
		}
		failures, rejected := validation.Rejected(err)
		if err != nil && !rejected {
			return formFailure(err)
		}
		failures, err = presentPasswordErrors(failures, "new_password2")
		if err != nil {
			return web.Response{}, err
		}
		return a.render(request, "password", a.password, data, orderedErrors(a.password, form.Errors(), failures), "")
	}
	old, _ := form.Cleaned().String("old_password")
	next, _ := form.Cleaned().String("new_password1")
	result, err := a.auth.ChangePassword(request, old, next)
	if err == auth.ErrInvalidCredentials {
		return a.loginRedirect()
	}
	if failures, rejected := validation.Rejected(err); rejected {
		failures, err = presentPasswordErrors(failures, "new_password2")
		if err != nil {
			return web.Response{}, err
		}
		return a.render(request, "password", a.password, data, orderedErrors(a.password, failures), "")
	}
	if err != nil {
		return formFailure(err)
	}
	response, err := redirect(a.basePath + "/password/done/")
	if err != nil {
		return web.Response{}, err
	}
	return result.Apply(response)
}

func presentPasswordErrors(failures validation.Errors, field validation.Field) (validation.Errors, error) {
	for _, item := range failures.All() {
		if item.Field() != "old_password" && item.Field() != "password" && item.Field() != validation.NonField {
			return validation.Errors{}, invalidConfig("password_diagnostics")
		}
	}
	return remapPasswordErrors(failures, field), nil
}
func csrfFailure(err error) (web.Response, error) {
	if classified, ok := err.(*sessionauth.Error); ok && classified != nil && classified.Code == sessionauth.CodeCSRFRejected && classified.Cause == nil {
		return textResponse(http.StatusForbidden)
	}
	return web.Response{}, err
}
func unknownOutcome(err error) bool {
	return errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown})
}
func formFailure(err error) (web.Response, error) {
	if unknownOutcome(err) {
		h := securityHeaders()
		h.Set("Content-Type", "text/plain; charset=utf-8")
		return web.NewResponse(http.StatusServiceUnavailable, h, []byte("The result could not be confirmed. Check your account before making another change.\n"))
	}
	return web.Response{}, err
}
