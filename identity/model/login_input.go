package model

import "github.com/progresshans/godj/auth"

// LoginInput projects the actual selected login field and explicit form policy
// without querying a database. Text fields without a character bound still use
// the credential byte envelope. Model validators and creation choices are not
// authentication gates and are not copied into the login form.
func (binding Binding[M]) LoginInput() (auth.LoginInput, error) {
	if binding.state == nil {
		return auth.LoginInput{}, managementError(CodeInvalidConfig, nil)
	}
	field, exists := binding.state.credentialFields[binding.state.role.Login]
	if !exists {
		return auth.LoginInput{}, managementError(CodeInvalidConfig, nil)
	}
	limit := field.MaxLength
	if limit == 0 || limit > auth.MaximumUsernameBytes {
		limit = auth.MaximumUsernameBytes
	}
	return auth.NewLoginInput(auth.LoginInputConfig{Label: field.GoName, MaxLength: limit, Normalization: auth.LoginNormalization(binding.state.role.LoginFormNormalization)})
}

func (store *CredentialStore[M]) LoginInput() (auth.LoginInput, error) {
	if err := store.valid(); err != nil {
		return auth.LoginInput{}, err
	}
	return store.state.binding.LoginInput()
}

func (authenticator *Authenticator[M]) LoginInput() (auth.LoginInput, error) {
	if authenticator == nil || authenticator.inner == nil {
		return auth.LoginInput{}, invalidCredentialStore()
	}
	return authenticator.inner.LoginInput()
}
