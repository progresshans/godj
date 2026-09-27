package admin

import (
	"context"
	"net/url"
	"strconv"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func (site *Site) commandPath(model registeredModel, command registeredCommand, id int64) string {
	return site.modelPath(model) + "command/" + command.name + "/?id=" + strconv.FormatInt(id, 10)
}

func (site *Site) commandContext(model registeredModel, command registeredCommand, record registeredRecord, form forms.Form, raw url.Values, revision int64) (map[string]templates.Value, error) {
	commandModel := model
	commandModel.form = command.form
	values, err := site.formContext(commandModel, command.label+": "+record.object.label, site.commandPath(model, command, record.object.id), command.label, form, raw)
	if err != nil {
		return nil, err
	}
	revisionContext(values, revision)
	return values, nil
}

func (site *Site) modelCommandGet(model registeredModel, command registeredCommand) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, principal auth.Principal) (web.Response, error) {
		if allowed, err := site.modelReadAllowed(request.Context(), principal, model); err != nil {
			return operationResponse(err)
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
			return operationResponse(err)
		}
		if !found {
			return siteNotFound()
		}
		form, err := command.form.Unbound(nil)
		if err != nil {
			return operationResponse(err)
		}
		revision, err := model.revision(record.object)
		if err != nil {
			return operationResponse(err)
		}
		values, err := site.commandContext(model, command, record, form, nil, revision)
		if err != nil {
			return operationResponse(err)
		}
		return site.render(request, "form.html", values)
	}
}

func (site *Site) modelCommandPost(model registeredModel, command registeredCommand) web.Handler {
	commandModel := model
	commandModel.form = command.form
	return func(request *web.Request) (web.Response, error) {
		query, err := parseSiteQuery(request, inputRules{"id": 1})
		if err != nil {
			return siteBadRequest()
		}
		id, err := positiveID(query, "id")
		if err != nil {
			return siteBadRequest()
		}
		values, err := parseSiteForm(request, modelFormRules(commandModel))
		if err != nil {
			return siteBadRequest()
		}
		if response, rejected, err := site.verifyCSRF(request, values["csrfmiddlewaretoken"]); rejected || err != nil {
			return response, err
		}
		return site.adminAuthorize(request, command.permission, func(principal auth.Principal) (web.Response, error) {
			if allowed, err := site.modelReadAllowed(request.Context(), principal, model); err != nil {
				return operationResponse(err)
			} else if !allowed {
				return siteForbidden()
			}
			mutation, err := model.submittedMutation(id, values)
			if err != nil {
				return siteBadRequest()
			}
			record, found, err := model.get(request.Context(), principal, id)
			if err != nil {
				return operationResponse(err)
			}
			if !found {
				return siteNotFound()
			}
			if err := model.checkObservedMutation(mutation, record.object); err != nil {
				return operationResponse(err)
			}
			form, err := command.form.Bind(modelData(commandModel, values), nil)
			if err != nil {
				return operationResponse(err)
			}
			if form.Valid() {
				_, saveErr := command.run(request.Context(), principal, mutation, form)
				if saveErr == nil {
					return siteRedirect(site.signedNoticeLocation(model, "changed", ""))
				}
				form, err = formWithRejection(request.Context(), form, saveErr)
				if err != nil {
					return operationResponse(err)
				}
			}
			context, err := site.commandContext(model, command, record, form, values, mutation.Revision)
			if err != nil {
				return operationResponse(err)
			}
			return site.render(request, "form.html", context)
		})
	}
}

func (site *Site) commandLinks(ctx context.Context, principal auth.Principal, model registeredModel, id int64) (templates.Value, error) {
	links := make([]templates.Value, 0, len(model.commands))
	for _, command := range model.commands {
		allowed, err := site.auth.Authorized(ctx, principal, command.permission)
		if err != nil {
			return templates.Value{}, err
		}
		if !allowed {
			continue
		}
		link, err := templates.Object(map[string]templates.Value{"label": templates.String(command.label), "path": templates.String(site.commandPath(model, command, id))})
		if err != nil {
			return templates.Value{}, err
		}
		links = append(links, link)
	}
	return templates.List(links...), nil
}
