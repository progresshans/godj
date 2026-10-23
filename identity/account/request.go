package identityaccount

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/web"
)

func noQuery(request *web.Request) bool {
	return request != nil && request.HTTP() != nil && request.HTTP().URL != nil && request.HTTP().URL.RawQuery == "" && !request.HTTP().URL.ForceQuery
}

func loginQuery(request *web.Request) (url.Values, bool) {
	if request == nil || request.HTTP() == nil || request.HTTP().URL == nil {
		return nil, false
	}
	u := request.HTTP().URL
	if len(u.RawQuery) > MaximumQueryBytes || u.ForceQuery {
		return nil, false
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, false
	}
	for key, items := range values {
		if key != "next" || len(items) != 1 || !utf8.ValidString(items[0]) || strings.ContainsRune(items[0], 0) {
			return nil, false
		}
	}
	return values, true
}

func parseForm(request *web.Request, spec forms.Spec, next bool) (url.Values, int, error) {
	h := request.HTTP()
	if err := request.Context().Err(); err != nil {
		return nil, 0, err
	}
	if h == nil || h.Body == nil {
		return nil, http.StatusBadRequest, nil
	}
	content := h.Header.Values("Content-Type")
	if len(content) != 1 {
		return nil, http.StatusUnsupportedMediaType, nil
	}
	media, params, err := mime.ParseMediaType(content[0])
	if err != nil || media != "application/x-www-form-urlencoded" {
		return nil, http.StatusUnsupportedMediaType, nil
	}
	for name, value := range params {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return nil, http.StatusUnsupportedMediaType, nil
		}
	}
	if h.ContentLength > MaximumBodyBytes {
		return nil, http.StatusRequestEntityTooLarge, nil
	}
	body, err := io.ReadAll(io.LimitReader(h.Body, MaximumBodyBytes+1))
	if err = errors.Join(err, request.Context().Err()); err != nil {
		return nil, 0, &api.Error{Code: api.FailureBodyRead, Detail: "account form body read failed", Cause: err}
	}
	if len(body) > MaximumBodyBytes {
		return nil, http.StatusRequestEntityTooLarge, nil
	}
	data, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, http.StatusBadRequest, nil
	}
	allowed := map[string]bool{"csrfmiddlewaretoken": true}
	for _, field := range spec.Fields() {
		allowed[field.Name()] = true
	}
	if next {
		allowed["next"] = true
	}
	count := 0
	for key, items := range data {
		if !allowed[key] || key == "next" && len(items) != 1 {
			return nil, http.StatusBadRequest, nil
		}
		count += len(items)
		if count > 16 {
			return nil, http.StatusBadRequest, nil
		}
		for _, value := range items {
			if len(value) > MaximumInputBytes {
				return nil, http.StatusRequestEntityTooLarge, nil
			}
		}
	}
	return data, 0, nil
}

func (a *Application) next(raw string) string {
	if a.auth.AllowsNext(raw) {
		return raw
	}
	return a.basePath + "/password/"
}

func securityHeaders() http.Header {
	h := make(http.Header)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("X-Frame-Options", "DENY")
	return h
}

func textResponse(status int) (web.Response, error) {
	h := securityHeaders()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	return web.NewResponse(status, h, []byte(http.StatusText(status)+"\n"))
}
func redirect(location string) (web.Response, error) {
	h := securityHeaders()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Location", location)
	return web.NewResponse(http.StatusFound, h, []byte("Found\n"))
}
func noStore(handler web.Handler) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		response, err := handler(request)
		if err != nil {
			return web.Response{}, err
		}
		h := response.Header()
		if h == nil {
			h = make(http.Header)
		}
		h.Set("Cache-Control", "no-store")
		return response.WithHeaders(h)
	}
}
