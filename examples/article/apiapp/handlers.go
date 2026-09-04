package apiapp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) list(request *web.Request, _ auth.Principal) (web.Response, error) {
	query, diagnostics, pageNotFound := parseListQuery(request.HTTP().URL.RawQuery)
	if pageNotFound {
		return notFoundResponse()
	}
	if !diagnostics.Empty() {
		return api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, diagnostics)
	}
	page, err := a.repository.List(request.Context(), query.options())
	if err != nil {
		return web.Response{}, err
	}
	if query.page > 1 && int64(page.Offset) >= page.Total {
		return notFoundResponse()
	}
	results := make([]serializers.Value, len(page.Articles))
	for index := range page.Articles {
		value, err := articleValue(page.Articles[index])
		if err != nil {
			return web.Response{}, err
		}
		results[index] = value
	}
	var next api.Link
	if int64(page.Offset)+int64(len(page.Articles)) < page.Total {
		next, err = api.RelativeLink(query.relativeURI(query.page + 1))
		if err != nil {
			return web.Response{}, err
		}
	}
	var previous api.Link
	if query.page > 1 {
		previous, err = api.RelativeLink(query.relativeURI(query.page - 1))
		if err != nil {
			return web.Response{}, err
		}
	}
	envelope, err := api.NewPage(page.Total, next, previous, results)
	if err != nil {
		return web.Response{}, err
	}
	return envelope.Response()
}

func (a *Application) listHead(request *web.Request, principal auth.Principal) (web.Response, error) {
	response, err := a.list(request, principal)
	return emptyBody(response, err)
}

func (a *Application) create(request *web.Request, _ auth.Principal) (web.Response, error) {
	values, response, handled, err := a.bind(request, serializers.ModeFull)
	if handled || err != nil {
		return response, err
	}
	created, err := a.repository.Create(request.Context(), fullInput(values))
	if err != nil {
		return a.writeError(err)
	}
	value, err := articleValue(created)
	if err != nil {
		return web.Response{}, err
	}
	response, err = api.JSON(http.StatusCreated, value)
	if err != nil {
		return web.Response{}, err
	}
	location, err := request.ReverseWith(DetailRouteName, web.Int64Argument("id", created.ID))
	if err != nil {
		return web.Response{}, err
	}
	header := response.Header()
	header.Set("Location", location)
	return web.NewResponse(response.Status(), header, response.Body())
}

func (a *Application) retrieve(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, ok := detailID(request)
	if !ok {
		return notFoundResponse()
	}
	article, found, err := a.repository.Get(request.Context(), id)
	if err != nil {
		return a.readError(err)
	}
	if !found {
		return notFoundResponse()
	}
	value, err := articleValue(article)
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(http.StatusOK, value)
}

func (a *Application) retrieveHead(request *web.Request, principal auth.Principal) (web.Response, error) {
	response, err := a.retrieve(request, principal)
	return emptyBody(response, err)
}

func (a *Application) update(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, ok := detailID(request)
	if !ok {
		return notFoundResponse()
	}
	current, response, handled, err := a.requireArticle(request, id)
	if handled || err != nil {
		return response, err
	}
	values, response, handled, err := a.bind(request, serializers.ModeFull)
	if handled || err != nil {
		return response, err
	}
	updated, _, err := a.repository.Update(request.Context(), id, fullUpdateInput(current, values))
	if err != nil {
		return a.writeError(err)
	}
	value, err := articleValue(updated)
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(http.StatusOK, value)
}

func (a *Application) patch(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, ok := detailID(request)
	if !ok {
		return notFoundResponse()
	}
	_, response, handled, err := a.requireArticle(request, id)
	if handled || err != nil {
		return response, err
	}
	values, response, handled, err := a.bind(request, serializers.ModePartial)
	if handled || err != nil {
		return response, err
	}
	updated, _, err := a.repository.Patch(request.Context(), id, partialInput(values))
	if err != nil {
		return a.writeError(err)
	}
	value, err := articleValue(updated)
	if err != nil {
		return web.Response{}, err
	}
	return api.JSON(http.StatusOK, value)
}

func (a *Application) delete(request *web.Request, _ auth.Principal) (web.Response, error) {
	id, ok := detailID(request)
	if !ok {
		return notFoundResponse()
	}
	if _, err := a.repository.Delete(request.Context(), id); err != nil {
		return a.writeError(err)
	}
	return api.NoContent()
}

func (a *Application) listOptions(*web.Request, auth.Principal) (web.Response, error) {
	return optionsResponse(http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost)
}

func (a *Application) detailOptions(*web.Request, auth.Principal) (web.Response, error) {
	return optionsResponse(http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPatch, http.MethodPut)
}

func optionsResponse(methods ...string) (web.Response, error) {
	values := make([]serializers.Value, len(methods))
	for index := range methods {
		values[index] = serializers.String(methods[index])
	}
	list, err := serializers.NewList(values...)
	if err != nil {
		return web.Response{}, err
	}
	object, err := serializers.NewObject(serializers.MemberOf("methods", list))
	if err != nil {
		return web.Response{}, err
	}
	response, err := api.JSON(http.StatusOK, object.Value())
	if err != nil {
		return web.Response{}, err
	}
	header := response.Header()
	header.Set("Allow", strings.Join(methods, ", "))
	return web.NewResponse(response.Status(), header, response.Body())
}

func detailID(request *web.Request) (int64, bool) {
	id, ok := request.Int64Parameter("id")
	return id, ok && id > 0
}

// DRF resolves a detail object before parsing update input. Preserve that
// observable precedence so a missing target remains 404 even when its body is
// malformed, while authentication, CSRF, and permission still run first in
// the outer session wrapper.
func (a *Application) requireArticle(request *web.Request, id int64) (articleapp.Article, web.Response, bool, error) {
	article, found, err := a.repository.Get(request.Context(), id)
	if err != nil {
		response, responseErr := a.readError(err)
		return articleapp.Article{}, response, responseErr == nil, responseErr
	}
	if !found {
		response, err := notFoundResponse()
		return articleapp.Article{}, response, true, err
	}
	return article, web.Response{}, false, nil
}

func (a *Application) readError(err error) (web.Response, error) {
	if articleapp.IsCode(err, articleapp.CodeNotFound) || errors.Is(err, articleapp.ErrNotFound) {
		return notFoundResponse()
	}
	// A non-positive identifier is client-visible 404; every handler rejects it
	// before calling the repository. Remaining invalid-input errors indicate an
	// adapter invariant failure and stay on the lower Web 500 boundary.
	return web.Response{}, err
}

func (a *Application) writeError(err error) (web.Response, error) {
	return a.readError(err)
}

func notFoundResponse() (web.Response, error) {
	return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
}

func emptyBody(response web.Response, err error) (web.Response, error) {
	if err != nil {
		return web.Response{}, err
	}
	return web.NewResponse(response.Status(), response.Header(), nil)
}
