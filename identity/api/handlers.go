package identityapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) bind(request *web.Request, spec serializers.Spec, mode serializers.Mode) (serializers.Values, web.Response, bool, error) {
	object, err := a.parser.ParseObjectFor(request, spec)
	if err != nil {
		response, handled, e := api.RequestErrorResponse(err)
		if handled {
			return serializers.Values{}, response, true, e
		}
		return serializers.Values{}, web.Response{}, false, err
	}
	result, err := spec.Bind(object, mode)
	if err != nil {
		return serializers.Values{}, web.Response{}, false, err
	}
	if !result.Valid() {
		response, e := api.ErrorResponse(http.StatusBadRequest, api.CodeValidationError, result.Errors())
		return serializers.Values{}, response, true, e
	}
	return result.Values(), web.Response{}, false, nil
}

// The row revision condition accepts one canonical positive decimal. It is
// distinct from HTTP representation validators and also applies to commands.
func (a *Application) precondition(request *web.Request) (int64, int, error) {
	value, diagnostics, err := a.revision.Parse(request.HTTP().Header)
	if err != nil {
		return 0, 0, err
	}
	if diagnostics.Empty() {
		return value, 0, nil
	}
	first, _ := diagnostics.At(0)
	if first.Code() == "required" {
		return 0, http.StatusPreconditionRequired, nil
	}
	return 0, http.StatusBadRequest, nil
}

func objectID(request *web.Request) (int64, bool) {
	id, ok := request.Int64Parameter("id")
	return id, ok && id > 0 && request.HTTP().URL.RawQuery == "" && !request.HTTP().URL.ForceQuery
}

func pageInput(request *web.Request) (int, int, bool) {
	raw := request.HTTP().URL.RawQuery
	if len(raw) > 1024 || request.HTTP().URL.ForceQuery {
		return 0, 0, false
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return 0, 0, false
	}
	offset, limit := 0, 20
	for key, list := range values {
		if len(list) != 1 || (key != "offset" && key != "limit") {
			return 0, 0, false
		}
		text := list[0]
		number, err := strconv.ParseInt(text, 10, 32)
		if err != nil || number < 0 || strconv.FormatInt(number, 10) != text {
			return 0, 0, false
		}
		if key == "limit" {
			if number < 1 || number > 100 {
				return 0, 0, false
			}
			limit = int(number)
		} else {
			offset = int(number)
		}
	}
	return offset, limit, true
}

func (a *Application) list(r resource) api.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		offset, limit, ok := pageInput(request)
		if !ok {
			return invalidRequest()
		}
		values := []serializers.Value{}
		var total int64
		switch r.name {
		case "users":
			page, err := a.manager.Users(request.Context(), actor, offset, limit)
			if err != nil {
				return failure(err)
			}
			total = page.Total
			for _, p := range page.Users {
				value, err := a.userSummary.Encode(identity.UserDetails{Profile: p})
				if err != nil {
					return web.Response{}, err
				}
				values = append(values, value)
			}
		case "groups":
			page, err := a.manager.Groups(request.Context(), actor, offset, limit)
			if err != nil {
				return failure(err)
			}
			total = page.Total
			for _, p := range page.Groups {
				value, err := a.groupSummary.Encode(identity.GroupDetails{GroupProfile: p})
				if err != nil {
					return web.Response{}, err
				}
				values = append(values, value)
			}
		case "permissions":
			page, err := a.manager.Permissions(request.Context(), actor, offset, limit)
			if err != nil {
				return failure(err)
			}
			total = page.Total
			for _, p := range page.Permissions {
				value, err := a.permission.Encode(p)
				if err != nil {
					return web.Response{}, err
				}
				values = append(values, value)
			}
		}
		return pageResponse(values, total, offset, limit)
	}
}

func (a *Application) detail(r resource) api.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		id, ok := objectID(request)
		if !ok {
			return invalidRequest()
		}
		switch r.name {
		case "users":
			p, err := a.manager.User(request.Context(), actor, id)
			if err != nil {
				return failure(err)
			}
			value, err := a.userDetail.Encode(p)
			return represented(http.StatusOK, p.Revision, value, err)
		case "groups":
			p, err := a.manager.Group(request.Context(), actor, id)
			if err != nil {
				return failure(err)
			}
			value, err := a.groupDetail.Encode(p)
			return represented(http.StatusOK, p.Revision, value, err)
		default:
			p, err := a.manager.Permission(request.Context(), actor, id)
			if err != nil {
				return failure(err)
			}
			value, err := a.permission.Encode(p)
			return represented(http.StatusOK, p.Revision, value, err)
		}
	}
}

func (a *Application) create(r resource) api.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		if request.HTTP().URL.RawQuery != "" || request.HTTP().URL.ForceQuery {
			return invalidRequest()
		}
		values, response, handled, err := a.bind(request, r.create, serializers.ModeFull)
		if handled || err != nil {
			return response, err
		}
		switch r.name {
		case "users":
			// A client never chooses the durable authentication/audit identity.
			var random [32]byte
			if _, err := rand.Read(random[:]); err != nil {
				return web.Response{}, err
			}
			input := userCreate(values, hex.EncodeToString(random[:]))
			password, _ := values.Get("password") // The required field was bound above.
			var p identity.UserDetails
			if password.IsNull() {
				p, err = a.manager.CreateUserWithUnusablePassword(request.Context(), actor, input)
			} else {
				p, err = a.manager.CreateUser(request.Context(), actor, input, textValue(values, "password"))
			}
			if err != nil {
				return failure(err)
			}
			value, err := a.userDetail.Encode(p)
			return represented(http.StatusCreated, p.Revision, value, err)
		case "groups":
			input := identity.NewGroupCreate(textValue(values, "name"))
			if value, ok := values.Get("permissions"); ok {
				keys, _ := value.AsIntegers()
				input = input.WithPermissions(keys...)
			}
			p, err := a.manager.CreateGroup(request.Context(), actor, input)
			if err != nil {
				return failure(err)
			}
			value, err := a.groupDetail.Encode(p)
			return represented(http.StatusCreated, p.Revision, value, err)
		default:
			p, err := a.manager.CreatePermission(request.Context(), actor, identity.NewPermissionCreate(textValue(values, "code"), textValue(values, "name")))
			if err != nil {
				return failure(err)
			}
			value, err := a.permission.Encode(p)
			return represented(http.StatusCreated, p.Revision, value, err)
		}
	}
}

func (a *Application) update(r resource, mode serializers.Mode) api.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		id, ok := objectID(request)
		if !ok {
			return invalidRequest()
		}
		revision, status, err := a.precondition(request)
		if err != nil {
			return web.Response{}, err
		}
		if status != 0 {
			return preconditionFailure(status)
		}
		values, response, handled, err := a.bind(request, r.update, mode)
		if handled || err != nil {
			return response, err
		}
		switch r.name {
		case "users":
			p, err := a.manager.UpdateUser(request.Context(), actor, id, revision, userPatch(values))
			if err != nil {
				return failure(err)
			}
			value, err := a.userDetail.Encode(p)
			return represented(http.StatusOK, p.Revision, value, err)
		case "groups":
			patch := identity.GroupPatch{}
			if _, ok := values.Get("name"); ok {
				patch = patch.WithName(textValue(values, "name"))
			}
			if v, ok := values.Get("permissions"); ok {
				keys, _ := v.AsIntegers()
				patch = patch.WithPermissions(keys...)
			}
			p, err := a.manager.UpdateGroup(request.Context(), actor, id, revision, patch)
			if err != nil {
				return failure(err)
			}
			value, err := a.groupDetail.Encode(p)
			return represented(http.StatusOK, p.Revision, value, err)
		default:
			patch := identity.PermissionPatch{}
			if _, ok := values.Get("code"); ok {
				patch = patch.WithCode(textValue(values, "code"))
			}
			if _, ok := values.Get("name"); ok {
				patch = patch.WithName(textValue(values, "name"))
			}
			p, err := a.manager.UpdatePermission(request.Context(), actor, id, revision, patch)
			if err != nil {
				return failure(err)
			}
			value, err := a.permission.Encode(p)
			return represented(http.StatusOK, p.Revision, value, err)
		}
	}
}

func (a *Application) remove(r resource) api.AuthenticatedHandler {
	return func(request *web.Request, actor auth.Principal) (web.Response, error) {
		id, ok := objectID(request)
		if !ok {
			return invalidRequest()
		}
		revision, status, err := a.precondition(request)
		if err != nil {
			return web.Response{}, err
		}
		if status != 0 {
			return preconditionFailure(status)
		}
		// DELETE has no request body; do not silently accept conflicting input.
		if request.HTTP().ContentLength != 0 || len(request.HTTP().TransferEncoding) != 0 {
			return invalidRequest()
		}
		switch r.name {
		case "users":
			_, err = a.manager.DeleteUser(request.Context(), actor, id, revision, a.users)
		case "groups":
			_, err = a.manager.DeleteGroup(request.Context(), actor, id, revision, a.groups)
		case "permissions":
			_, err = a.manager.DeletePermission(request.Context(), actor, id, revision, a.permissions)
		}
		if err != nil {
			return failure(err)
		}
		return api.NoContent()
	}
}

func (a *Application) setPassword(request *web.Request, actor auth.Principal) (web.Response, error) {
	id, ok := objectID(request)
	if !ok {
		return invalidRequest()
	}
	revision, status, err := a.precondition(request)
	if err != nil {
		return web.Response{}, err
	}
	if status != 0 {
		return preconditionFailure(status)
	}
	values, response, handled, err := a.bind(request, a.password, serializers.ModeFull)
	if handled || err != nil {
		return response, err
	}
	password, _ := values.Get("password") // An omitted password is not a disable command.
	var p identity.Profile
	if password.IsNull() {
		p, err = a.manager.SetUnusablePassword(request.Context(), actor, id, revision)
	} else {
		p, err = a.manager.SetPassword(request.Context(), actor, id, revision, textValue(values, "password"))
	}
	if err != nil {
		return failure(err)
	}
	value, err := a.userSummary.Encode(identity.UserDetails{Profile: p})
	return represented(http.StatusOK, p.Revision, value, err)
}

func preconditionFailure(status int) (web.Response, error) {
	code := api.ResponseCode("invalid_precondition")
	if status == http.StatusPreconditionRequired {
		code = "precondition_required"
	}
	return api.ErrorResponse(status, code, validation.NewErrors())
}
