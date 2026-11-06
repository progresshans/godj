package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func (site *Site) resolveChoiceModel(ctx context.Context, actor auth.Principal, model registeredModel, origin ChoiceOrigin, data forms.Data, initial map[string]forms.Value, unbound bool, path string) (registeredModel, error) {
	spec, err := model.resolveForm(ctx, actor, origin, data, initial, unbound)
	if err != nil {
		return registeredModel{}, err
	}
	model.form = spec
	model.choiceViews, err = choiceViews(ctx, actor, origin, model.choiceSources, path, false, unbound)
	return model, err
}
func (site *Site) resolveCommandChoiceModel(ctx context.Context, actor auth.Principal, model registeredModel, origin ChoiceOrigin, data forms.Data, unbound bool, path string) (registeredModel, error) {
	spec, err := model.formFor(ctx, actor, origin, formChoiceKeys(model.form, data, unbound))
	if err != nil {
		return registeredModel{}, err
	}
	model.form = spec
	model.choiceViews, err = choiceViews(ctx, actor, origin, model.choiceSources, path, false, unbound)
	return model, err
}
func choiceViews(ctx context.Context, actor auth.Principal, origin ChoiceOrigin, sources []RelatedChoices, path string, readOnly, fetch bool) (map[string]choiceFieldView, error) {
	result := make(map[string]choiceFieldView, len(sources))
	for _, source := range sources {
		view := choiceFieldView{Limit: DefaultListLimit, Disabled: readOnly}
		if !readOnly {
			if fetch {
				page, err := source.search(ctx, actor, origin, formmodel.ChoiceRequest{Limit: DefaultListLimit})
				if err != nil {
					return nil, err
				}
				view.Items, view.Offset, view.Limit, view.More = page.Items(), page.Offset(), page.Limit(), page.More()
			}
			view.URL = path + "choices/" + source.choiceField() + "/"
			if origin.objectID != 0 && origin.operation == ChoiceChange || origin.parentID != 0 {
				id := origin.objectID
				if origin.parentID != 0 {
					id = origin.parentID
				}
				view.URL += "?id=" + strconv.FormatInt(id, 10)
			}
		}
		result[source.choiceField()] = view
	}
	return result, nil
}

func choiceRequest(values url.Values) (formmodel.ChoiceRequest, error) {
	request := formmodel.ChoiceRequest{Term: values.Get("q"), Limit: DefaultListLimit}
	if len(request.Term) > formmodel.MaximumModelChoiceTermBytes || !utf8.ValidString(request.Term) || strings.ContainsRune(request.Term, 0) {
		return request, &ConfigError{Path: "choices.q", Code: "invalid"}
	}
	for _, part := range []struct {
		name    string
		target  *int
		maximum int
		minimum int
	}{{"offset", &request.Offset, formmodel.MaximumModelChoiceOffset, 0}, {"limit", &request.Limit, formmodel.MaximumModelChoiceLimit, 1}} {
		if values.Has(part.name) {
			raw := values.Get(part.name)
			number, err := strconv.Atoi(raw)
			if err != nil || strconv.Itoa(number) != raw || number < part.minimum || number > part.maximum {
				return request, &ConfigError{Path: "choices." + part.name, Code: "invalid"}
			}
			*part.target = number
		}
	}
	return request, nil
}
func choiceJSON(page formmodel.ChoicePage) (web.Response, error) {
	type item struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}
	payload := struct {
		Items  []item `json:"items"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
		More   bool   `json:"more"`
	}{Items: []item{}, Offset: page.Offset(), Limit: page.Limit(), More: page.More()}
	for _, choice := range page.Items() {
		payload.Items = append(payload.Items, item{Value: choice.InputValue(), Label: choice.Label})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return web.Response{}, err
	}
	headers := http.Header{"Content-Type": {"application/json; charset=utf-8"}, "Cache-Control": {"no-store"}, "X-Content-Type-Options": {"nosniff"}}
	setSiteFramingHeaders(headers)
	return web.NewResponse(http.StatusOK, headers, body)
}
func (site *Site) modelChoiceSearch(model registeredModel, source RelatedChoices, operation ChoiceOperation) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		rules := inputRules{"q": 1, "offset": 1, "limit": 1}
		if operation == ChoiceChange {
			rules["id"] = 1
		}
		values, err := parseSiteQuery(request, rules)
		if err != nil {
			return siteBadRequest()
		}
		search, err := choiceRequest(values)
		if err != nil {
			return siteBadRequest()
		}
		if allowed, err := site.permissionGranted(request, actor, source.choicePermission()); err != nil {
			return operationResponse(err)
		} else if !allowed {
			return siteForbidden()
		}
		var id int64
		if operation == ChoiceAdd {
			if allowed, err := site.addPermissionsAllowed(request, actor, model); err != nil {
				return operationResponse(err)
			} else if !allowed {
				return siteForbidden()
			}
		} else {
			if allowed, permissionErr := site.modelWriteAllowed(request.Context(), model, actor, model.permissions.Change); permissionErr != nil {
				return operationResponse(permissionErr)
			} else if !allowed {
				return siteForbidden()
			}
			id, err = positiveID(values, "id")
			if err != nil {
				return siteBadRequest()
			}
			if allowed, err := site.modelReadAllowed(request.Context(), actor, model); err != nil {
				return operationResponse(err)
			} else if !allowed {
				return siteForbidden()
			}
			_, found, err := model.get(request.Context(), actor, id)
			if err != nil {
				return operationResponse(err)
			}
			if !found {
				return siteNotFound()
			}
		}
		page, err := source.search(request.Context(), actor, model.origin(operation, id), search)
		if err != nil {
			return operationResponse(err)
		}
		return choiceJSON(page)
	}
}
func (site *Site) commandChoiceSearch(model registeredModel, command registeredCollectionCommand, source RelatedChoices) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		values, err := parseSiteQuery(request, inputRules{"q": 1, "offset": 1, "limit": 1})
		if err != nil {
			return siteBadRequest()
		}
		search, err := choiceRequest(values)
		if err != nil {
			return siteBadRequest()
		}
		if allowed, err := site.collectionCommandAllowed(request.Context(), actor, model, command); err != nil {
			return operationResponse(err)
		} else if !allowed {
			return siteForbidden()
		}
		origin := model.origin(ChoiceCollection, 0)
		origin.name = command.name
		page, err := source.search(request.Context(), actor, origin, search)
		if err != nil {
			return operationResponse(err)
		}
		return choiceJSON(page)
	}
}
func (site *Site) inlineChoiceSearch(model registeredModel, inline Inline, source RelatedChoices, change bool) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		rules := inputRules{"q": 1, "offset": 1, "limit": 1, "index": 1, "row_id": 1}
		if change {
			rules["id"] = 1
		}
		values, err := parseSiteQuery(request, rules)
		if err != nil {
			return siteBadRequest()
		}
		search, err := choiceRequest(values)
		if err != nil {
			return siteBadRequest()
		}
		if _, err := choiceRowIndex(values, inline.config.AbsoluteMax); err != nil {
			return siteBadRequest()
		}
		if allowed, err := site.permissionGranted(request, actor, source.choicePermission()); err != nil {
			return operationResponse(err)
		} else if !allowed {
			return siteForbidden()
		}
		var parentID, rowID int64
		if change {
			parentID, err = positiveID(values, "id")
			if err != nil {
				return siteBadRequest()
			}
			if allowed, err := site.modelReadAllowed(request.Context(), actor, model); err != nil {
				return operationResponse(err)
			} else if !allowed {
				return siteForbidden()
			}
			_, found, err := model.get(request.Context(), actor, parentID)
			if err != nil {
				return operationResponse(err)
			}
			if !found {
				return siteNotFound()
			}
		} else if allowed, err := site.addPermissionsAllowed(request, actor, model.forCreate()); err != nil {
			return operationResponse(err)
		} else if !allowed {
			return siteForbidden()
		}
		if values.Has("row_id") {
			rowID, err = positiveID(values, "row_id")
			if err != nil {
				return siteBadRequest()
			}
		}
		access, err := site.inlineAccess(request, actor, inline, true)
		if err != nil {
			return operationResponse(err)
		}
		origin, err := inline.searchOrigin(request.Context(), actor, parentID, rowID, access)
		if err != nil {
			return operationResponse(err)
		}
		page, err := source.search(request.Context(), actor, origin, search)
		if err != nil {
			return operationResponse(err)
		}
		return choiceJSON(page)
	}
}
func choiceRowIndex(values url.Values, maximum int) (int, error) {
	raw := values.Get("index")
	index, err := strconv.Atoi(raw)
	if err != nil || index < 0 || index >= maximum || strconv.Itoa(index) != raw {
		return 0, &ConfigError{Path: "choices.index", Code: "invalid"}
	}
	return index, nil
}
func (site *Site) collectionSetChoiceSearch(model registeredModel, definition CollectionFormSet, source RelatedChoices) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		values, err := parseSiteQuery(request, inputRules{"q": 1, "offset": 1, "limit": 1, "index": 1, "row_id": 1})
		if err != nil {
			return siteBadRequest()
		}
		search, err := choiceRequest(values)
		if err != nil {
			return siteBadRequest()
		}
		if _, err := choiceRowIndex(values, definition.config.AbsoluteMax); err != nil {
			return siteBadRequest()
		}
		access, err := site.collectionFormSetAccess(request.Context(), actor, model, definition)
		if err != nil {
			return operationResponse(err)
		} else if !access.visible() || !access.writable() {
			return siteForbidden()
		}
		if allowed, err := site.permissionGranted(request, actor, source.choicePermission()); err != nil {
			return operationResponse(err)
		} else if !allowed {
			return siteForbidden()
		}
		var key int64
		if values.Has("row_id") {
			key, err = positiveID(values, "row_id")
			if err != nil {
				return siteBadRequest()
			}
		}
		origin, err := definition.searchOrigin(request.Context(), actor, access, key)
		if err != nil {
			return operationResponse(err)
		}
		page, err := source.search(request.Context(), actor, origin, search)
		if err != nil {
			return operationResponse(err)
		}
		return choiceJSON(page)
	}
}

func (site *Site) hasChoiceSearch() bool {
	for _, model := range site.registry.models {
		if len(model.choiceSources)+len(model.createChoiceSources) > 0 {
			return true
		}
		for _, inline := range model.inlines {
			if len(inline.choiceSources) > 0 {
				return true
			}
		}
		for _, command := range model.collectionCommands {
			if len(command.choiceSources) > 0 {
				return true
			}
		}
		for _, set := range model.collectionFormSets {
			if len(set.choiceSources) > 0 {
				return true
			}
		}
	}
	return false
}
