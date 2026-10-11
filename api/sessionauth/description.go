package sessionauth

import "github.com/progresshans/godj/api"

// DescribeAuthentication reports the actual normalized public transport names
// without reading a session or generating a CSRF token.
func (r *Runtime) DescribeAuthentication() (api.AuthenticationDescription, error) {
	if r == nil || r.runtime == nil || r.runtime.CSRFHeader() == "" {
		return api.AuthenticationDescription{}, &Error{Code: CodeInvalidConfig, Field: "runtime", Detail: "API session runtime is nil or uninitialized"}
	}
	session, csrf := r.runtime.CookieNames()
	return api.AuthenticationDescription{
		Kind: api.AuthenticationSession, SessionCookieName: session, CSRFCookieName: csrf, CSRFHeader: r.runtime.CSRFHeader(),
	}, nil
}
