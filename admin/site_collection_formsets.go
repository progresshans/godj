package admin

import (
	"context"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func (site *Site) collectionFormSetPath(model registeredModel, definition CollectionFormSet) string {
	return site.modelPath(model) + "collection-set/" + definition.name + "/"
}

func (site *Site) collectionFormSetAllowed(ctx context.Context, actor auth.Principal, model registeredModel, definition CollectionFormSet) (bool, error) {
	if allowed, err := site.modelReadAllowed(ctx, actor, model); err != nil || !allowed {
		return false, err
	}
	return site.modelWriteAllowed(ctx, model, actor, definition.permissions...)
}

func (site *Site) collectionFormSetGet(model registeredModel, definition CollectionFormSet) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		if allowed, err := site.collectionFormSetAllowed(request.Context(), actor, model, definition); err != nil {
			return operationResponse(err)
		} else if !allowed {
			return siteForbidden()
		}
		if _, err := parseSiteQuery(request, inputRules{}); err != nil {
			return siteBadRequest()
		}
		bound, err := definition.bind(request.Context(), actor, nil)
		if err != nil {
			return operationResponse(err)
		}
		return site.renderCollectionFormSet(request, model, definition, bound)
	}
}

func (site *Site) collectionFormSetPost(model registeredModel, definition CollectionFormSet) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		return site.adminAuthorize(request, definition.permissions[0], func(actor auth.Principal) (web.Response, error) {
			if allowed, err := site.collectionFormSetAllowed(request.Context(), actor, model, definition); err != nil {
				return operationResponse(err)
			} else if !allowed {
				return siteForbidden()
			}
			if _, err := parseSiteQuery(request, inputRules{}); err != nil {
				return siteBadRequest()
			}
			values, err := parseSiteForm(request, definition.rules)
			if err != nil {
				return siteFormResponse(err)
			}
			if response, rejected, err := site.verifyCSRF(request, values["csrfmiddlewaretoken"]); rejected || err != nil {
				return response, err
			}
			delete(values, "csrfmiddlewaretoken")
			data := forms.NewData(values)
			bound, err := definition.bind(request.Context(), actor, &data)
			if err != nil {
				return operationResponse(err)
			}
			if bound.set.Valid() {
				if _, err := bound.run(request.Context()); err == nil {
					return siteRedirect(site.signedNoticeLocation(model, "added", ""))
				} else {
					bound, err = bound.withRejection(request.Context(), err)
					if err != nil {
						return operationResponse(err)
					}
				}
			}
			return site.renderCollectionFormSet(request, model, definition, bound)
		})
	}
}

func (site *Site) renderCollectionFormSet(request *web.Request, model registeredModel, definition CollectionFormSet, bound collectionFormSetBound) (web.Response, error) {
	rows := make([]templates.Value, 0, bound.set.TotalForms())
	for _, row := range bound.set.Forms() {
		value, err := formSetRowContext(row, "", forms.Null(), false, 0, true)
		if err != nil {
			return web.Response{}, err
		}
		rows = append(rows, value)
	}
	empty, err := formSetRowContext(bound.empty, "", forms.Null(), false, 0, true)
	if err != nil {
		return web.Response{}, err
	}
	failures, err := violationValues(validation.Join(bound.set.NonFormErrors(), bound.set.Management().Errors()))
	if err != nil {
		return web.Response{}, err
	}
	group, err := templateObject(map[string]templates.Value{
		"prefix": templates.String(definition.config.Prefix), "label": templates.String(definition.label),
		"total": templates.Integer(int64(bound.set.TotalForms())), "initial": templates.Integer(0),
		"rows": templates.List(rows...), "errors": templates.List(failures...),
		"can_add": templates.Bool(true), "empty": templates.List(empty),
		"minimum": templates.Integer(int64(definition.config.MinForms)), "maximum": templates.Integer(int64(definition.config.MaxForms)),
	})
	if err != nil {
		return web.Response{}, err
	}
	return site.render(request, "form.html", map[string]templates.Value{
		"title": templates.String(definition.label), "has_revision": templates.Bool(false),
		"commands": templates.List(), "readonly_fields": templates.List(), "inlines": templates.List(group),
		"revision": templates.Integer(0), "action": templates.String(site.collectionFormSetPath(model, definition)),
		"submit_label": templates.String(definition.label), "list_path": templates.String(site.modelPath(model)),
		"fields": templates.List(), "non_field_errors": templates.List(), "bound": templates.Bool(bound.set.Bound()), "multipart": templates.Bool(false),
	})
}
