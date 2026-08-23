package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func (site *Site) indexGet(request *web.Request, principal auth.Principal) (web.Response, error) {
	if _, err := parseSiteQuery(request, inputRules{}); err != nil {
		return siteBadRequest()
	}
	values, err := site.indexContext(request.Context(), principal)
	if err != nil {
		return web.Response{}, err
	}
	return site.render(request, "index.html", values)
}

func (site *Site) loginGet(request *web.Request, principal auth.Principal) (web.Response, error) {
	query, err := parseSiteQuery(request, inputRules{"next": 1})
	if err != nil {
		return siteBadRequest()
	}
	next := site.auth.SafeNext(query.Get("next"))
	if principal.Authenticated() {
		allowed, err := site.permissionGranted(request, principal, site.access)
		if err != nil {
			return web.Response{}, err
		}
		if allowed {
			return siteRedirect(next)
		}
	}
	values, err := site.loginContext("", next, false)
	if err != nil {
		return web.Response{}, err
	}
	return site.render(request, "login.html", values)
}

func (site *Site) loginPost(request *web.Request) (web.Response, error) {
	if _, err := parseSiteQuery(request, inputRules{}); err != nil {
		return siteBadRequest()
	}
	values, err := parseSiteForm(request, inputRules{
		"csrfmiddlewaretoken": MaximumInputValues,
		"username":            1,
		"password":            1,
		"next":                1,
	})
	if err != nil {
		return siteBadRequest()
	}
	if response, rejected, err := site.verifyCSRF(request, values["csrfmiddlewaretoken"]); rejected || err != nil {
		return response, err
	}
	username, usernameOK := exactValue(values, "username")
	password, passwordOK := exactValue(values, "password")
	next := site.auth.SafeNext(values.Get("next"))
	if !usernameOK || !passwordOK {
		context, contextErr := site.loginContext(username, next, true)
		if contextErr != nil {
			return web.Response{}, contextErr
		}
		return site.render(request, "login.html", context)
	}
	result, err := site.auth.LoginAuthorized(request, username, password, site.access)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		context, contextErr := site.loginContext(username, next, true)
		if contextErr != nil {
			return web.Response{}, contextErr
		}
		return site.render(request, "login.html", context)
	}
	if err != nil {
		return web.Response{}, err
	}
	response, err := siteRedirect(next)
	if err != nil {
		return web.Response{}, err
	}
	return result.Apply(response)
}

func (site *Site) logoutPost(request *web.Request) (web.Response, error) {
	if _, err := parseSiteQuery(request, inputRules{}); err != nil {
		return siteBadRequest()
	}
	values, err := parseSiteForm(request, inputRules{"csrfmiddlewaretoken": MaximumInputValues})
	if err != nil {
		return siteBadRequest()
	}
	if response, rejected, err := site.verifyCSRF(request, values["csrfmiddlewaretoken"]); rejected || err != nil {
		return response, err
	}
	change, err := site.auth.Logout(request)
	if err != nil {
		return web.Response{}, err
	}
	response, err := siteRedirect(site.basePath + "/login/")
	if err != nil {
		return web.Response{}, err
	}
	return change.Apply(response)
}

func (site *Site) modelList(model registeredModel) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, principal auth.Principal) (web.Response, error) {
		query, err := parseSiteQuery(request, inputRules{"q": 1, "p": 1, "notice": 1, "count": 1, "sig": 1})
		if err != nil {
			return siteBadRequest()
		}
		if err := site.validateNotice(model, query); err != nil {
			return siteBadRequest()
		}
		pageNumber, offset, invalidPage := pageOffset(query, site.pageSize)
		search := query.Get("q")
		page, err := model.list(request.Context(), principal, ListRequest{Search: search, Offset: offset, Limit: site.pageSize})
		if err != nil {
			var configError *ConfigError
			if errors.As(err, &configError) && listInputError(configError) {
				return siteBadRequest()
			}
			return web.Response{}, err
		}
		// A non-first empty page can be a stale/out-of-range request or a
		// concurrent delete between count and row reads. Re-reading page one is
		// bounded and avoids turning either case into an empty or failed Admin
		// response. A non-empty page is retained even if its best-effort count
		// came from an earlier storage snapshot.
		if pageNumber > 1 && len(page.objects) == 0 {
			pageNumber, offset, invalidPage = 1, 0, true
			page, err = model.list(request.Context(), principal, ListRequest{Search: search, Offset: offset, Limit: site.pageSize})
			if err != nil {
				var configError *ConfigError
				if errors.As(err, &configError) && listInputError(configError) {
					return siteBadRequest()
				}
				return web.Response{}, err
			}
		}
		context, err := site.listContext(request.Context(), model, principal, page, pageNumber, search, query, invalidPage)
		if err != nil {
			return web.Response{}, err
		}
		return site.render(request, "list.html", context)
	}
}

func (site *Site) modelAddGet(model registeredModel) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, principal auth.Principal) (web.Response, error) {
		if _, err := parseSiteQuery(request, inputRules{}); err != nil {
			return siteBadRequest()
		}
		form, err := model.form.Unbound(nil)
		if err != nil {
			return web.Response{}, err
		}
		context, err := site.formContext(model, "Add "+model.model.GoName, site.modelPath(model)+"add/", "Add", form, nil)
		if err != nil {
			return web.Response{}, err
		}
		return site.render(request, "form.html", context)
	}
}

func (site *Site) modelAddPost(model registeredModel) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		if _, err := parseSiteQuery(request, inputRules{}); err != nil {
			return siteBadRequest()
		}
		values, err := parseSiteForm(request, modelFormRules(model))
		if err != nil {
			return siteBadRequest()
		}
		if response, rejected, err := site.verifyCSRF(request, values["csrfmiddlewaretoken"]); rejected || err != nil {
			return response, err
		}
		return site.adminAuthorize(request, model.permissions.Add, func(principal auth.Principal) (web.Response, error) {
			form, err := model.form.Bind(modelData(model, values), nil)
			if err != nil {
				return web.Response{}, err
			}
			if !form.Valid() {
				context, contextErr := site.formContext(model, "Add "+model.model.GoName, site.modelPath(model)+"add/", "Add", form, values)
				if contextErr != nil {
					return web.Response{}, contextErr
				}
				return site.render(request, "form.html", context)
			}
			if _, err := model.create(request.Context(), principal, form); err != nil {
				return web.Response{}, err
			}
			return siteRedirect(site.signedNoticeLocation(model, "added", ""))
		})
	}
}

func (site *Site) modelChangeGet(model registeredModel) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, principal auth.Principal) (web.Response, error) {
		if allowed, err := site.permissionGranted(request, principal, model.permissions.View); err != nil {
			return web.Response{}, err
		} else if !allowed {
			return siteForbidden()
		}
		query, err := parseSiteQuery(request, inputRules{"id": 1})
		if err != nil {
			return siteBadRequest()
		}
		id, err := positiveID(query, "id")
		if err != nil {
			return siteBadRequest()
		}
		record, found, err := model.get(request.Context(), principal, id)
		if err != nil {
			return web.Response{}, err
		}
		if !found {
			return siteNotFound()
		}
		form, err := model.form.Unbound(record.initial)
		if err != nil {
			return web.Response{}, err
		}
		context, err := site.formContext(model, "Change "+record.object.label, site.modelPath(model)+"change/?id="+strconv.FormatInt(id, 10), "Save", form, nil)
		if err != nil {
			return web.Response{}, err
		}
		return site.render(request, "form.html", context)
	}
}

func (site *Site) modelChangePost(model registeredModel) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		query, err := parseSiteQuery(request, inputRules{"id": 1})
		if err != nil {
			return siteBadRequest()
		}
		id, err := positiveID(query, "id")
		if err != nil {
			return siteBadRequest()
		}
		values, err := parseSiteForm(request, modelFormRules(model))
		if err != nil {
			return siteBadRequest()
		}
		if response, rejected, err := site.verifyCSRF(request, values["csrfmiddlewaretoken"]); rejected || err != nil {
			return response, err
		}
		return site.adminAuthorize(request, model.permissions.Change, func(principal auth.Principal) (web.Response, error) {
			if allowed, err := site.permissionGranted(request, principal, model.permissions.View); err != nil {
				return web.Response{}, err
			} else if !allowed {
				return siteForbidden()
			}
			record, found, err := model.get(request.Context(), principal, id)
			if err != nil {
				return web.Response{}, err
			}
			if !found {
				return siteNotFound()
			}
			form, err := model.form.Bind(modelData(model, values), record.initial)
			if err != nil {
				return web.Response{}, err
			}
			if !form.Valid() {
				context, contextErr := site.formContext(model, "Change "+record.object.label, site.modelPath(model)+"change/?id="+strconv.FormatInt(id, 10), "Save", form, values)
				if contextErr != nil {
					return web.Response{}, contextErr
				}
				return site.render(request, "form.html", context)
			}
			if _, _, err := model.update(request.Context(), principal, id, form); err != nil {
				if errors.Is(err, ErrObjectNotFound) {
					return siteNotFound()
				}
				return web.Response{}, err
			}
			return siteRedirect(site.signedNoticeLocation(model, "changed", ""))
		})
	}
}

func (site *Site) modelDeleteGet(model registeredModel) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, principal auth.Principal) (web.Response, error) {
		if allowed, err := site.permissionGranted(request, principal, model.permissions.View); err != nil {
			return web.Response{}, err
		} else if !allowed {
			return siteForbidden()
		}
		query, err := parseSiteQuery(request, inputRules{"id": 1})
		if err != nil {
			return siteBadRequest()
		}
		id, err := positiveID(query, "id")
		if err != nil {
			return siteBadRequest()
		}
		record, found, err := model.get(request.Context(), principal, id)
		if err != nil {
			return web.Response{}, err
		}
		if !found {
			return siteNotFound()
		}
		context, err := site.deleteContext(model, record.object)
		if err != nil {
			return web.Response{}, err
		}
		return site.render(request, "delete.html", context)
	}
}

func (site *Site) modelDeletePost(model registeredModel) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		query, err := parseSiteQuery(request, inputRules{"id": 1})
		if err != nil {
			return siteBadRequest()
		}
		id, err := positiveID(query, "id")
		if err != nil {
			return siteBadRequest()
		}
		values, err := parseSiteForm(request, inputRules{"csrfmiddlewaretoken": MaximumInputValues, "confirm": 1})
		if err != nil {
			return siteBadRequest()
		}
		if response, rejected, err := site.verifyCSRF(request, values["csrfmiddlewaretoken"]); rejected || err != nil {
			return response, err
		}
		if values.Get("confirm") != "yes" {
			return siteBadRequest()
		}
		return site.adminAuthorize(request, model.permissions.Delete, func(principal auth.Principal) (web.Response, error) {
			if allowed, err := site.permissionGranted(request, principal, model.permissions.View); err != nil {
				return web.Response{}, err
			} else if !allowed {
				return siteForbidden()
			}
			if _, found, err := model.get(request.Context(), principal, id); err != nil {
				return web.Response{}, err
			} else if !found {
				return siteNotFound()
			}
			if _, err := model.delete(request.Context(), principal, id); err != nil {
				if errors.Is(err, ErrObjectNotFound) {
					return siteNotFound()
				}
				return web.Response{}, err
			}
			return siteRedirect(site.signedNoticeLocation(model, "deleted", ""))
		})
	}
}

func (site *Site) modelHistory(model registeredModel) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, principal auth.Principal) (web.Response, error) {
		query, err := parseSiteQuery(request, inputRules{"id": 1})
		if err != nil {
			return siteBadRequest()
		}
		id, err := positiveID(query, "id")
		if err != nil {
			return siteBadRequest()
		}
		entries, err := model.history(request.Context(), principal, id)
		if err != nil {
			return web.Response{}, err
		}
		context, err := site.historyContext(model, id, entries)
		if err != nil {
			return web.Response{}, err
		}
		return site.render(request, "history.html", context)
	}
}

func (site *Site) modelAction(model registeredModel, action registeredAction) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		if _, err := parseSiteQuery(request, inputRules{}); err != nil {
			return siteBadRequest()
		}
		values, err := parseSiteForm(request, inputRules{"csrfmiddlewaretoken": MaximumInputValues, "selected": MaximumSelectedIDs})
		if err != nil {
			return siteBadRequest()
		}
		if response, rejected, err := site.verifyCSRF(request, values["csrfmiddlewaretoken"]); rejected || err != nil {
			return response, err
		}
		return site.adminAuthorize(request, action.permission, func(principal auth.Principal) (web.Response, error) {
			ids, err := selectedIDs(values)
			if err != nil {
				return siteBadRequest()
			}
			result, err := action.run(request.Context(), principal, ids)
			if err != nil {
				return web.Response{}, err
			}
			location := site.signedNoticeLocation(model, "published", strconv.Itoa(result.Matched()))
			return siteRedirect(location)
		})
	}
}

func (site *Site) adminRequire(permission auth.Permission, handler sessionauth.AuthenticatedHandler) web.Handler {
	return site.auth.Optional(func(request *web.Request, principal auth.Principal) (web.Response, error) {
		return site.authorizePrincipal(request, principal, permission, func() (web.Response, error) {
			return handler(request, principal)
		})
	})
}

func (site *Site) adminAuthorize(
	request *web.Request,
	permission auth.Permission,
	handler func(auth.Principal) (web.Response, error),
) (web.Response, error) {
	principal, err := site.auth.Principal(request)
	if err != nil {
		return web.Response{}, err
	}
	return site.authorizePrincipal(request, principal, permission, func() (web.Response, error) {
		return handler(principal)
	})
}

func (site *Site) authorizePrincipal(
	request *web.Request,
	principal auth.Principal,
	permission auth.Permission,
	handler func() (web.Response, error),
) (web.Response, error) {
	if !principal.Authenticated() {
		return site.loginRedirect(request)
	}
	access, err := site.permissionGranted(request, principal, site.access)
	if err != nil {
		return web.Response{}, err
	}
	if !access {
		return site.loginRedirect(request)
	}
	if permission != "" {
		allowed, err := site.permissionGranted(request, principal, permission)
		if err != nil {
			return web.Response{}, err
		}
		if !allowed {
			return siteForbidden()
		}
	}
	return handler()
}

func (site *Site) permissionGranted(request *web.Request, principal auth.Principal, permission auth.Permission) (bool, error) {
	return site.auth.Authorized(request.Context(), principal, permission)
}

func (site *Site) loginRedirect(request *web.Request) (web.Response, error) {
	next := site.basePath + "/"
	if httpRequest := request.HTTP(); httpRequest != nil && httpRequest.URL != nil {
		next = site.auth.SafeNext(httpRequest.URL.RequestURI())
	}
	return siteRedirect(site.basePath + "/login/?next=" + url.QueryEscape(next))
}

func (site *Site) verifyCSRF(request *web.Request, tokens []string) (web.Response, bool, error) {
	err := site.auth.VerifyCSRF(request, tokens)
	if err == nil {
		return web.Response{}, false, nil
	}
	if errors.Is(err, &sessionauth.Error{Code: sessionauth.CodeCSRFRejected}) {
		response, responseErr := siteForbidden()
		return response, true, responseErr
	}
	return web.Response{}, true, err
}

func siteRedirect(location string) (web.Response, error) {
	header := make(http.Header)
	header.Set("Location", location)
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "text/plain; charset=utf-8")
	setSiteFramingHeaders(header)
	return web.NewResponse(http.StatusFound, header, []byte("Found\n"))
}

func siteBadRequest() (web.Response, error) {
	return siteText(http.StatusBadRequest, "Bad Request\n")
}

func siteForbidden() (web.Response, error) {
	return siteText(http.StatusForbidden, "Forbidden\n")
}

func siteNotFound() (web.Response, error) {
	return siteText(http.StatusNotFound, "Not Found\n")
}

func siteText(status int, body string) (web.Response, error) {
	header := make(http.Header)
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("X-Content-Type-Options", "nosniff")
	setSiteFramingHeaders(header)
	return web.NewResponse(status, header, []byte(body))
}

func setSiteFramingHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "frame-ancestors 'none'")
	header.Set("X-Frame-Options", "DENY")
}

func listInputError(err *ConfigError) bool {
	if err == nil {
		return false
	}
	switch err.Path {
	case "list.offset", "list.limit", "list.search":
		return true
	default:
		return false
	}
}
