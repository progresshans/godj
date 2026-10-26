package endpoint

import (
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web"
)

// Admission describes the same explicit policy used by the actual adapter and
// operation. Its zero value is invalid; no empty permission means anonymous.
type Admission struct {
	kind         admissionKind
	policy       api.PermissionPolicy
	sessionProof bool
	err          error
}

type admissionKind uint8

const (
	permissionPolicy admissionKind = iota + 1
	principalOnly
	csrfOnly
)

func All(first auth.Permission, additional ...auth.Permission) Admission {
	policy, err := api.NewPermissionPolicy(append([]auth.Permission{first}, additional...), nil)
	return Admission{kind: permissionPolicy, policy: policy, err: err}
}

func Any(first auth.Permission, alternatives ...auth.Permission) Admission {
	policy, err := api.NewPermissionPolicy(nil, append([]auth.Permission{first}, alternatives...))
	return Admission{kind: permissionPolicy, policy: policy, err: err}
}

// Permissions declares mandatory grants and an optional alternative group as
// one admission. Compound alternatives require a PolicyAuthentication adapter;
// the endpoint never composes two credential or CSRF resolutions.
func Permissions(policy api.PermissionPolicy) Admission {
	return Admission{kind: permissionPolicy, policy: policy}
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
	case permissionPolicy:
		if !admission.policy.Valid() {
			return configError("admission", "permissions are missing", nil)
		}
		required, alternatives := admission.policy.Required(), admission.policy.Alternatives()
		if len(alternatives) > 1 {
			operation.Permission = alternatives[0]
			operation.AlternativePermissions = alternatives[1:]
			operation.AdditionalPermissions = required
		} else {
			// One alternative is mandatory, so preserve the declared evaluation
			// order using ordinary conjunction metadata and capability.
			required = append(required, alternatives...)
			operation.Permission = required[0]
			operation.AdditionalPermissions = required[1:]
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
	switch admission.kind {
	case permissionPolicy:
		required, alternatives := admission.policy.Required(), admission.policy.Alternatives()
		if len(alternatives) <= 1 {
			required = append(required, alternatives...)
			return authentication.Require(required[0], handler, required[1:]...)
		}
		if len(required) != 0 {
			adapter, ok := authentication.(api.PolicyAuthentication)
			if !ok {
				return nil, configError("admission", "compound permission authentication capability is required", nil)
			}
			return adapter.RequirePolicy(admission.policy, handler)
		}
		adapter, ok := authentication.(api.AlternativeAuthentication)
		if !ok {
			return nil, configError("admission", "alternative authentication capability is required", nil)
		}
		return adapter.RequireAny(alternatives[0], handler, alternatives[1:]...)
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
