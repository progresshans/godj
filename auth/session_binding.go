package auth

// These reserved server-session keys are shared by login/resolution and durable
// identity maintenance. Neither value belongs in a client cookie or response.
const (
	SessionPrincipalIDKey     = "_godj_principal_id"
	SessionCredentialStampKey = "_godj_credential_stamp"
)
