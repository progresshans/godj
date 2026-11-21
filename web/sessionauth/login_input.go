package sessionauth

import "github.com/progresshans/godj/auth"

// LoginInput obtains the public form declaration from this runtime's exact
// authenticator. It performs no I/O and never substitutes a built-in User.
func (r *Runtime) LoginInput() (auth.LoginInput, error) {
	if r == nil {
		return auth.LoginInput{}, &Error{Code: CodeInvalidConfig, Field: "login_input", Detail: "login input requires an initialized runtime"}
	}
	provider, ok := r.authenticator.(auth.LoginInputProvider)
	if !ok {
		return auth.LoginInput{}, &Error{Code: CodeInvalidConfig, Field: "login_input", Detail: "authenticator does not declare login input"}
	}
	input, err := provider.LoginInput()
	if err == nil {
		err = input.Validate()
	}
	if err != nil {
		return auth.LoginInput{}, &Error{Code: CodeInvalidConfig, Field: "login_input", Detail: "authenticator login input is invalid", Cause: err}
	}
	return input, nil
}
