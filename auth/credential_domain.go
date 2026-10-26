package auth

import (
	"crypto/sha256"
	"encoding/hex"
)

// CredentialDomain identifies one credential ownership generation. Its ID is
// public metadata, not a secret or a bearer credential. A persistent identity
// owner must bind it to its selected account source and change it during an
// explicit ownership transition; merely constructing this value proves no DB
// state or permission to transition an existing domain.
type CredentialDomain struct {
	identifier [sha256.Size]byte
	valid      bool
}

// NewCredentialDomain accepts an exact, nonzero, lower-case SHA-256-sized ID.
// The caller owns generation and durable storage; this function performs no
// I/O and never infers a domain from a username, password or principal ID.
func NewCredentialDomain(identifier string) (CredentialDomain, error) {
	if len(identifier) != sha256.Size*2 {
		return CredentialDomain{}, invalidCredentialDomain()
	}
	decoded, err := hex.DecodeString(identifier)
	if err != nil || hex.EncodeToString(decoded) != identifier {
		return CredentialDomain{}, invalidCredentialDomain()
	}
	var result CredentialDomain
	copy(result.identifier[:], decoded)
	if result.identifier == ([sha256.Size]byte{}) {
		return CredentialDomain{}, invalidCredentialDomain()
	}
	result.valid = true
	return result, nil
}

func (domain CredentialDomain) Identifier() string {
	if !domain.valid {
		return ""
	}
	return hex.EncodeToString(domain.identifier[:])
}

// MatchesPrincipal checks the credential observation's ownership domain. It
// does not check current permissions or active status. Mutation owners must
// compare the authenticated actor with the current durable domain before
// resolving permissions by ID, so a reused ID cannot promote an old actor.
func (domain CredentialDomain) MatchesPrincipal(principal Principal) bool {
	return domain.valid && principal.domain == domain
}

// NewCredentialForDomain keeps the normal credential validation and binds its
// session stamp to an explicit ownership generation. The unscoped constructor
// remains available for non-project authenticators; neither form accepts the
// other form's session stamp, even for identical principal IDs and passwords.
func NewCredentialForDomain(domain CredentialDomain, username, encodedHash string, principal Principal) (Credential, error) {
	if !domain.valid {
		return Credential{}, invalidCredentialDomain()
	}
	return newCredential(username, encodedHash, principal, domain)
}

func invalidCredentialDomain() error {
	return &Error{Code: CodeInvalidInput, Field: "credential_domain", Detail: "credential ownership domain is absent or malformed"}
}
