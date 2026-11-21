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
	access, err := site.collectionFormSetAccess(ctx, actor, model, definition)
	return access.visible() && access.writable(), err
}

func (site *Site) collectionFormSetAccess(ctx context.Context, actor auth.Principal, model registeredModel, definition CollectionFormSet) (CollectionSetAccess, error) {
	if allowed, err := site.modelReadAllowed(ctx, actor, model); err != nil || !allowed {
		return CollectionSetAccess{}, err
	}
	if model.readOnly {
		return CollectionSetAccess{}, nil
	}
	access := CollectionSetAccess{choices: &inlineChoiceAccess{allowed: make(map[string]bool)}}
	for _, right := range []struct {
		target     *bool
		permission auth.Permission
	}{{&access.View, definition.permissions.View}, {&access.Add, definition.permissions.Add}, {&access.Change, definition.permissions.Change}, {&access.Delete, definition.permissions.Delete}} {
		allowed, err := site.auth.Authorized(ctx, actor, right.permission)
		if err != nil {
			return CollectionSetAccess{}, err
		}
		*right.target = allowed
	}
	access.View = access.read()
	for _, permission := range definition.additional {
		allowed, err := site.auth.Authorized(ctx, actor, permission)
		if err != nil || !allowed {
			return CollectionSetAccess{}, err
		}
	}
	for _, source := range definition.choiceSources {
		allowed, err := site.auth.Authorized(ctx, actor, source.choicePermission())
		if err != nil {
			return CollectionSetAccess{}, err
		}
		if !allowed && (access.Add || access.Change) {
			return CollectionSetAccess{}, nil
		}
		access.choices.allowed[source.choiceField()] = allowed
	}
	return access, nil
}

func (site *Site) collectionFormSetGet(model registeredModel, definition CollectionFormSet) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		access, err := site.collectionFormSetAccess(request.Context(), actor, model, definition)
		if err != nil {
			return operationResponse(err)
		} else if !access.visible() || !access.writable() {
			return siteForbidden()
		}
		if _, err := parseSiteQuery(request, inputRules{}); err != nil {
			return siteBadRequest()
		}
		bound, err := definition.bind(request.Context(), actor, access, nil)
		if err != nil {
			return operationResponse(err)
		}
		return site.renderCollectionFormSet(request, model, definition, bound)
	}
}

func (site *Site) collectionFormSetPost(model registeredModel, definition CollectionFormSet) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		if response, rejected, err := site.admitMultipart(request, ""); rejected || err != nil {
			return response, err
		}
		return site.adminAuthorize(request, "", func(actor auth.Principal) (web.Response, error) {
			access, err := site.collectionFormSetAccess(request.Context(), actor, model, definition)
			if err != nil {
				return operationResponse(err)
			} else if !access.visible() || !access.writable() {
				return siteForbidden()
			}
			if _, err := parseSiteQuery(request, inputRules{}); err != nil {
				return siteBadRequest()
			}
			input, err := site.parseCollectionFormSet(request, definition)
			if err != nil {
				return siteFormResponse(err)
			}
			if response, rejected, err := site.verifyCSRF(request, input.values["csrfmiddlewaretoken"]); rejected || err != nil {
				return response, err
			}
			delete(input.values, "csrfmiddlewaretoken")
			data := forms.NewDataWithFiles(input.values, input.files)
			bound, err := definition.bind(request.Context(), actor, access, &data)
			if err != nil {
				return operationResponse(err)
			}
			if bound.set.Valid() {
				if result, err := bound.run(request.Context()); err == nil {
					notice := "existing"
					if len(result.writes) != 0 {
						notice = result.writes[0].Kind
						for _, write := range result.writes {
							if write.Kind != notice {
								notice = "update"
								break
							}
						}
						switch notice {
						case "create":
							notice = "added"
						case "delete":
							notice = "deleted"
						default:
							notice = "changed"
						}
					}
					return siteRedirect(site.signedNoticeLocation(model, notice, ""))
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
	viewsFor := func(index int) map[string]choiceFieldView {
		views := make(map[string]choiceFieldView, len(bound.choiceViews[index]))
		for name, view := range bound.choiceViews[index] {
			if !view.Disabled {
				view.URL = site.collectionFormSetPath(model, definition) + "choices/" + name + "/"
			}
			views[name] = view
		}
		return views
	}
	rows := make([]templates.Value, 0, bound.set.TotalForms())
	for _, row := range bound.set.Forms() {
		value, err := formSetRowContext(row, definition.primary, bound.identities[row.Index()], definition.revision != "", bound.revisions[row.Index()], bound.access.Add && row.Index() >= bound.set.InitialForms(), viewsFor(row.Index()))
		if err != nil {
			return web.Response{}, err
		}
		rows = append(rows, value)
	}
	empty := templates.List()
	if bound.empty != nil {
		prototype, err := formSetRowContext(*bound.empty, definition.primary, forms.Null(), definition.revision != "", 0, true, viewsFor(-1))
		if err != nil {
			return web.Response{}, err
		}
		empty = templates.List(prototype)
	}
	failures, err := violationValues(validation.Join(bound.set.NonFormErrors(), bound.set.Management().Errors()))
	if err != nil {
		return web.Response{}, err
	}
	group, err := templateObject(map[string]templates.Value{
		"prefix": templates.String(definition.config.Prefix), "label": templates.String(definition.label),
		"total": templates.Integer(int64(bound.set.TotalForms())), "initial": templates.Integer(int64(bound.set.InitialForms())),
		"rows": templates.List(rows...), "errors": templates.List(failures...),
		"can_add": templates.Bool(bound.empty != nil), "empty": empty,
		"minimum": templates.Integer(int64(bound.policy.MinForms)), "maximum": templates.Integer(int64(min(bound.policy.MaxForms, bound.policy.AbsoluteMax))),
	})
	if err != nil {
		return web.Response{}, err
	}
	multipart := (bound.access.Add || bound.access.Change) && len(definition.files) != 0
	return site.render(request, "form.html", map[string]templates.Value{
		"title": templates.String(definition.label), "has_revision": templates.Bool(false),
		"commands": templates.List(), "readonly_fields": templates.List(), "inlines": templates.List(group),
		"revision": templates.Integer(0), "action": templates.String(site.collectionFormSetPath(model, definition)),
		"submit_label": templates.String(definition.label), "list_path": templates.String(site.modelPath(model)),
		"fields": templates.List(), "non_field_errors": templates.List(), "bound": templates.Bool(bound.set.Bound()), "multipart": templates.Bool(multipart),
	})
}
