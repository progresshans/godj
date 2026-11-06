package bearerauth

import "github.com/progresshans/godj/api"

// DescribeAuthentication describes the resource-server transport only. The
// verifier's token format and credential lifecycle are deliberately absent.
func (r *Runtime) DescribeAuthentication() (api.AuthenticationDescription, error) {
	if r == nil || nilInterface(r.verifier) || nilInterface(r.authorizer) {
		return api.AuthenticationDescription{}, &Error{Code: CodeInvalidConfig, Field: "runtime", Detail: "Bearer runtime is nil or uninitialized"}
	}
	return api.AuthenticationDescription{Kind: api.AuthenticationBearer}, nil
}
