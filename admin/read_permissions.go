package admin

import (
	"context"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

func (site *Site) modelReadAllowed(ctx context.Context, principal auth.Principal, model registeredModel) (bool, error) {
	for _, permission := range []auth.Permission{model.permissions.View, model.permissions.Change} {
		if permission == "" {
			continue
		}
		allowed, err := site.auth.Authorized(ctx, principal, permission)
		if err != nil || allowed {
			return allowed, err
		}
	}
	return false, nil
}

func (site *Site) adminRequireRead(model registeredModel, handler sessionauth.AuthenticatedHandler) web.Handler {
	return site.adminRequire("", func(request *web.Request, principal auth.Principal) (web.Response, error) {
		allowed, err := site.modelReadAllowed(request.Context(), principal, model)
		if err != nil {
			return operationResponse(err)
		}
		if !allowed {
			return siteForbidden()
		}
		return handler(request, principal)
	})
}
