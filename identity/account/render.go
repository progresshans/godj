package identityaccount

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type csrfToken string

func (t csrfToken) Token(context.Context) (string, error) { return string(t), nil }

func (a *Application) render(request *web.Request, kind string, spec forms.Spec, data url.Values, failures validation.Errors, next string) (web.Response, error) {
	token, err := a.auth.CSRFToken(request)
	if err != nil {
		return web.Response{}, err
	}
	fields := []templates.Value{}
	for _, field := range spec.Fields() {
		value := ""
		if field.Widget() != forms.PasswordInput {
			value = safeText(data.Get(field.Name()))
		}
		errors, err := errorValues(failures.ByField(validation.Field(field.Name())))
		if err != nil {
			return web.Response{}, err
		}
		autocomplete := "new-password"
		if field.Name() == "username" {
			autocomplete = "username"
		} else if field.Name() == "old_password" || field.Name() == "password" {
			autocomplete = "current-password"
		}
		item, err := templates.Object(map[string]templates.Value{"name": templates.String(field.Name()), "label": templates.String(field.Label()), "password": templates.Bool(field.Widget() == forms.PasswordInput), "required": templates.Bool(field.Required()), "value": templates.String(value), "autocomplete": templates.String(autocomplete), "errors": templates.List(errors...)})
		if err != nil {
			return web.Response{}, err
		}
		fields = append(fields, item)
	}
	general, err := errorValues(failures.ByField(validation.NonField))
	if err != nil {
		return web.Response{}, err
	}
	title, action, submit := "Change password", a.basePath+"/password/", "Change password"
	if kind == "login" {
		title, action, submit = "Sign in", a.basePath+"/login/", "Sign in"
	}
	if kind == "done" {
		title = "Password changed"
	}
	values, err := templates.NewContext(map[string]templates.Value{"title": templates.String(title), "kind": templates.String(kind), "login": templates.Bool(kind == "login"), "done": templates.Bool(kind == "done"), "signed_in": templates.Bool(kind != "login"), "action": templates.String(action), "submit": templates.String(submit), "next": templates.String(next), "fields": templates.List(fields...), "errors": templates.List(general...), "password_path": templates.String(a.basePath + "/password/"), "logout_path": templates.String(a.basePath + "/logout/")})
	if err != nil {
		return web.Response{}, err
	}
	body, err := a.engine.Render(request.Context(), "account.html", values, templates.Capabilities{CSRF: csrfToken(token.Value())})
	if err != nil {
		return web.Response{}, err
	}
	header := securityHeaders()
	header.Set("Content-Type", "text/html; charset=utf-8")
	response, err := web.NewResponse(http.StatusOK, header, body)
	if err != nil {
		return web.Response{}, err
	}
	return token.Apply(response)
}

func safeText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(value, "\uFFFD"))
}

func errorValues(failures validation.Errors) ([]templates.Value, error) {
	result := []templates.Value{}
	for _, failure := range failures.All() {
		message := "Check this value."
		switch failure.Code() {
		case "required":
			message = "This field is required."
		case "multiple":
			message = "Submit this field once."
		case "max_length":
			message = "This value is too long."
		case "invalid_login":
			message = "Please enter a correct username and password."
		case "password_incorrect":
			message = "Your old password was entered incorrectly. Please enter it again."
		case "password_mismatch":
			message = "The two passwords do not match."
		case "password_too_short":
			message = "This password is too short."
		case "password_too_common":
			message = "This password is too common."
		case "password_entirely_numeric":
			message = "This password is entirely numeric."
		case "password_too_similar":
			message = "This password is too similar to your account information."
		}
		item, err := templates.Object(map[string]templates.Value{"field": templates.String(string(failure.Field())), "code": templates.String(string(failure.Code())), "message": templates.String(message)})
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}
