package identityapi

import (
	"net/http"
	"strconv"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func (a *Application) prepareEncoders() error {
	var err error
	user := models.UserDescriptor{}
	readUser := func(value identity.UserDetails, field ir.Field) (query.Value, bool) {
		p := value.Profile
		return user.WriteFieldValue(models.User{ID: p.ID, Username: p.Username, FirstName: p.FirstName, LastName: p.LastName, Email: p.Email, Active: p.Active, Staff: p.Staff, Superuser: p.Superuser, DateJoined: p.DateJoined, LastLogin: p.LastLogin, Revision: p.Revision}, field)
	}
	readUserMany := func(value identity.UserDetails, field ir.ManyToManyField) ([]int64, bool) {
		switch field.Name {
		case "groups":
			return value.GroupIDs, true
		case "permissions":
			return value.PermissionIDs, true
		}
		return nil, false
	}
	a.userSummary, err = serializers.NewModelEncoder(a.resources[0].scalar, user.Metadata(), readUser)
	if err != nil {
		return err
	}
	a.userDetail, err = serializers.NewModelEncoder(a.resources[0].detail, user.Metadata(), readUser, readUserMany)
	if err != nil {
		return err
	}
	passwordUsable, err := serializers.BooleanField("password_usable", serializers.WithReadOnly())
	if err != nil {
		return err
	}
	passwordState := serializers.ComputedField[identity.UserDetails]{Field: passwordUsable, Read: func(value identity.UserDetails) (serializers.Value, bool) {
		return serializers.Boolean(value.PasswordUsable), true
	}}
	a.userSummary, err = a.userSummary.WithComputed(passwordState)
	if err != nil {
		return err
	}
	a.userDetail, err = a.userDetail.WithComputed(passwordState)
	if err != nil {
		return err
	}
	a.resources[0].scalar, a.resources[0].detail = a.userSummary.Spec(), a.userDetail.Spec()
	group := models.GroupDescriptor{}
	readGroup := func(value identity.GroupDetails, field ir.Field) (query.Value, bool) {
		return group.WriteFieldValue(models.Group{ID: value.ID, Name: value.Name, Revision: value.Revision}, field)
	}
	a.groupSummary, err = serializers.NewModelEncoder(a.resources[1].scalar, group.Metadata(), readGroup)
	if err != nil {
		return err
	}
	a.groupDetail, err = serializers.NewModelEncoder(a.resources[1].detail, group.Metadata(), readGroup, func(value identity.GroupDetails, field ir.ManyToManyField) ([]int64, bool) {
		return value.PermissionIDs, field.Name == "permissions"
	})
	if err != nil {
		return err
	}
	permission := models.PermissionDescriptor{}
	a.permission, err = serializers.NewModelEncoder(a.resources[2].detail, permission.Metadata(), func(value identity.PermissionProfile, field ir.Field) (query.Value, bool) {
		return permission.WriteFieldValue(models.Permission{ID: value.ID, Code: value.Code, Name: value.Name, Revision: value.Revision}, field)
	})
	return err
}

func represented(status int, revision int64, value serializers.Value, err error) (web.Response, error) {
	if err != nil {
		return web.Response{}, err
	}
	response, err := api.JSONWithLimits(status, value, serializers.Limits{MaxStringBytes: 4096, MaxValues: 8192})
	if err != nil {
		return web.Response{}, err
	}
	header := response.Header()
	header.Set("Revision", strconv.FormatInt(revision, 10))
	return response.WithHeaders(header)
}

func pageResponse(values []serializers.Value, total int64, offset, limit int) (web.Response, error) {
	items, err := serializers.NewList(values...)
	if err != nil {
		return web.Response{}, err
	}
	body, err := serializers.NewObject(serializers.MemberOf("items", items), serializers.MemberOf("count", serializers.Integer(total)), serializers.MemberOf("offset", serializers.Integer(int64(offset))), serializers.MemberOf("limit", serializers.Integer(int64(limit))))
	if err != nil {
		return web.Response{}, err
	}
	return api.JSONWithLimits(http.StatusOK, body.Value(), serializers.Limits{MaxStringBytes: 4096, MaxValues: 8192})
}

func failure(err error) (web.Response, error) {
	if response, handled, e := api.ValidationErrorResponse(err); handled {
		return response, e
	}
	// Classify only the manager's outer result. A storage failure may contain a
	// permission/not-found/validation-shaped cause and must remain an error.
	classified, ok := err.(*identity.Error)
	if !ok || classified == nil {
		return web.Response{}, err
	}
	switch classified.Code {
	case identity.CodePermission:
		return api.ErrorResponse(http.StatusForbidden, api.CodePermissionDenied, validation.NewErrors())
	case identity.CodeNotFound:
		return api.ErrorResponse(http.StatusNotFound, api.CodeNotFound, validation.NewErrors())
	case identity.CodeConflict:
		return api.ErrorResponse(http.StatusPreconditionFailed, "revision_conflict", validation.NewErrors())
	case identity.CodeOutcomeUnknown:
		// Deliberately omit Retry-After. A client must reconcile durable state;
		// this is neither a retryable success nor a confirmed validation rejection.
		body, e := serializers.NewObject(serializers.MemberOf("code", serializers.String("outcome_unknown")), serializers.MemberOf("errors", serializers.Integers()))
		if e != nil {
			return web.Response{}, e
		}
		return api.JSON(http.StatusServiceUnavailable, body.Value())
	default:
		return web.Response{}, err
	}
}

func invalidRequest() (web.Response, error) {
	return api.ErrorResponse(http.StatusBadRequest, api.CodeParseError, validation.NewErrors())
}

func noStore(handler web.Handler) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		response, err := handler(request)
		if err != nil {
			return response, err
		}
		header := response.Header()
		header.Set("Cache-Control", "no-store")
		return response.WithHeaders(header)
	}
}
