package admin

import (
	"context"
	"net/url"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/templates"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func (site *Site) collectionCommandPath(model registeredModel, command registeredCollectionCommand) string {
	return site.modelPath(model) + "collection/" + command.name + "/"
}

func collectionCommandModel(model registeredModel, command registeredCollectionCommand) registeredModel {
	model.form, model.revisionField = command.form, ""
	return model
}

func (site *Site) collectionCommandContext(model registeredModel, command registeredCollectionCommand, form forms.Form, raw url.Values) (map[string]templates.Value, error) {
	return site.formContext(collectionCommandModel(model, command), command.label, site.collectionCommandPath(model, command), command.label, form, raw)
}

func (site *Site) collectionCommandAllowed(ctx context.Context, principal auth.Principal, model registeredModel, command registeredCollectionCommand) (bool, error) {
	if allowed, err := site.modelReadAllowed(ctx, principal, model); err != nil || !allowed {
		return false, err
	}
	return site.modelWriteAllowed(ctx, model, principal, command.permissions...)
}

func (site *Site) collectionCommandGet(model registeredModel, command registeredCollectionCommand) sessionauth.AuthenticatedHandler {
	return func(request *web.Request, principal auth.Principal) (web.Response, error) {
		if allowed, err := site.collectionCommandAllowed(request.Context(), principal, model, command); err != nil {
			return operationResponse(err)
		} else if !allowed {
			return siteForbidden()
		}
		if _, err := parseSiteQuery(request, inputRules{}); err != nil {
			return siteBadRequest()
		}
		form, err := command.form.Unbound(nil)
		if err != nil {
			return operationResponse(err)
		}
		values, err := site.collectionCommandContext(model, command, form, nil)
		if err != nil {
			return operationResponse(err)
		}
		return site.render(request, "form.html", values)
	}
}

func (site *Site) collectionCommandPost(model registeredModel, command registeredCollectionCommand) web.Handler {
	commandModel := collectionCommandModel(model, command)
	return func(request *web.Request) (web.Response, error) {
		if _, err := parseSiteQuery(request, inputRules{}); err != nil {
			return siteBadRequest()
		}
		if response, rejected, err := site.admitMultipart(request, command.permissions[0], command.permissions[1:]...); rejected || err != nil {
			return response, err
		}
		input, err := site.parseModelForm(request, commandModel)
		if err != nil {
			return siteFormResponse(err)
		}
		if response, rejected, err := site.verifyCSRF(request, input.values["csrfmiddlewaretoken"]); rejected || err != nil {
			return response, err
		}
		return site.adminAuthorize(request, command.permissions[0], func(principal auth.Principal) (web.Response, error) {
			if allowed, err := site.collectionCommandAllowed(request.Context(), principal, model, command); err != nil {
				return operationResponse(err)
			} else if !allowed {
				return siteForbidden()
			}
			form, err := command.form.Bind(request.Context(), modelData(commandModel, input), nil)
			if err != nil {
				return operationResponse(err)
			}
			if form.Valid() {
				result, saveErr := command.run(request.Context(), principal, form)
				if saveErr == nil {
					notice := "existing"
					if result.Created {
						notice = "added"
					}
					return siteRedirect(site.signedNoticeLocation(model, notice, ""))
				}
				form, err = formWithRejection(request.Context(), form, saveErr)
				if err != nil {
					return operationResponse(err)
				}
			}
			values, err := site.collectionCommandContext(model, command, form, input.values)
			if err != nil {
				return operationResponse(err)
			}
			return site.render(request, "form.html", values)
		})
	}
}

func (site *Site) collectionCommandLinks(ctx context.Context, principal auth.Principal, model registeredModel) (templates.Value, error) {
	links := make([]templates.Value, 0, len(model.collectionCommands))
	for _, command := range model.collectionCommands {
		allowed, err := site.collectionCommandAllowed(ctx, principal, model, command)
		if err != nil {
			return templates.Value{}, err
		}
		if !allowed {
			continue
		}
		link, err := templates.Object(map[string]templates.Value{"label": templates.String(command.label), "path": templates.String(site.collectionCommandPath(model, command))})
		if err != nil {
			return templates.Value{}, err
		}
		links = append(links, link)
	}
	return templates.List(links...), nil
}
