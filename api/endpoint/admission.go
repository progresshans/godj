package endpoint

import (
	"slices"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

// Admission describes the same explicit policy used by the actual adapter and
// operation. Its zero value is invalid; no empty permission means anonymous.
type Admission struct {
	kind         admissionKind
	permissions  []auth.Permission
	sessionProof bool
	err          error
}

type admissionKind uint8

const (
	allPermissions admissionKind = iota + 1
	anyPermission
	principalOnly
	csrfOnly
)

func All(first auth.Permission, additional ...auth.Permission) Admission {
	permissions, err := auth.RequiredPermissions(first, additional...)
	return Admission{kind: allPermissions, permissions: permissions, err: err}
}

func Any(first auth.Permission, alternatives ...auth.Permission) Admission {
	permissions, err := auth.RequiredPermissions(first, alternatives...)
	return Admission{kind: anyPermission, permissions: permissions, err: err}
}

func Authenticated() Admission { return Admission{kind: principalOnly} }

// CSRFOnly uses the explicit anonymous-capable Session adapter. The handler
// receives a zero Principal. requireSessionProof declares an application-owned
// session proof; the application must validate that proof itself.
func CSRFOnly(requireSessionProof bool) Admission {
	return Admission{kind: csrfOnly, sessionProof: requireSessionProof}
}

func (admission Admission) describe(operation *openapi.Operation) error {
	if admission.err != nil {
		return configError("admission", "permission declaration is invalid", admission.err)
	}
	switch admission.kind {
	case allPermissions, anyPermission:
		if len(admission.permissions) == 0 {
			return configError("admission", "permissions are missing", nil)
		}
		operation.Permission = admission.permissions[0]
		remaining := append([]auth.Permission(nil), admission.permissions[1:]...)
		if admission.kind == allPermissions {
			operation.AdditionalPermissions = remaining
		} else {
			// RequireAny with one permission is equivalent to Require and needs
			// no alternative capability or a synthetic extra permission.
			operation.AlternativePermissions = remaining
		}
	case principalOnly:
		operation.AuthenticatedOnly = true
	case csrfOnly:
		operation.CSRFOnly, operation.SessionCookieRequired = true, admission.sessionProof
	default:
		return configError("admission", "admission is zero or unsupported", nil)
	}
	return nil
}

func (admission Admission) wrap(authentication api.Authentication, handler api.AuthenticatedHandler) (web.Handler, error) {
	permissions := slices.Clone(admission.permissions)
	switch admission.kind {
	case allPermissions:
		return authentication.Require(permissions[0], handler, permissions[1:]...)
	case anyPermission:
		if len(permissions) == 1 {
			return authentication.Require(permissions[0], handler)
		}
		adapter, ok := authentication.(api.AlternativeAuthentication)
		if !ok {
			return nil, configError("admission", "alternative authentication capability is required", nil)
		}
		return adapter.RequireAny(permissions[0], handler, permissions[1:]...)
	case principalOnly:
		adapter, ok := authentication.(api.PrincipalAuthentication)
		if !ok {
			return nil, configError("admission", "principal authentication capability is required", nil)
		}
		return adapter.RequireAuthenticated(handler)
	case csrfOnly:
		adapter, ok := authentication.(api.CSRFAuthentication)
		if !ok {
			return nil, configError("admission", "anonymous CSRF capability is required", nil)
		}
		return adapter.RequireCSRF(func(request *web.Request) (web.Response, error) {
			return handler(request, auth.Principal{})
		})
	default:
		return nil, configError("admission", "admission is zero or unsupported", nil)
	}
}
