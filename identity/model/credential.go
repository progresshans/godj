package model

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// RoleFingerprint identifies the normalized account selection and its security
// roles. Ordinary profile additions do not change this identity. The database
// schema and migration history have their own verification boundaries.
func (binding Binding[M]) RoleFingerprint() (string, error) {
	if binding.state == nil {
		return "", invalidAccountCredential()
	}
	return hex.EncodeToString(binding.state.roleFingerprint[:]), nil
}

// CredentialDomain derives the authentication namespace from this exact role
// and a nonzero ownership generation. The durable runtime must verify the role
// and generation in the SAME read/write scope as the account, and persist a new
// generation on every explicit domain transition, including switching back to
// a previous model. This pure derivation neither verifies nor initializes that
// durable state. The generation is public metadata and must not be a password.
func (binding Binding[M]) CredentialDomain(generation [sha256.Size]byte) (auth.CredentialDomain, error) {
	if binding.state == nil || generation == ([sha256.Size]byte{}) {
		return auth.CredentialDomain{}, invalidAccountCredential()
	}
	input := append([]byte("godj.account-domain.v1\x00"), binding.state.roleFingerprint[:]...)
	input = append(input, generation[:]...)
	digest := sha256.Sum256(input)
	return auth.NewCredentialDomain(hex.EncodeToString(digest[:]))
}

// PrepareCredential converts an already observed typed account and its complete
// effective permissions into an immutable credential. It validates the selected
// IR fields, revision and stored key and performs no I/O or password work.
// The caller owns the coherent snapshot, current domain verification, membership
// queries and authorization. This method is not an authenticator or an account
// provisioning API, and does not replace model/profile validation.
func (binding Binding[M]) PrepareCredential(generation [sha256.Size]byte, row M, permissions []auth.Permission) (auth.Credential, error) {
	domain, err := binding.CredentialDomain(generation)
	if err != nil {
		return auth.Credential{}, err
	}
	return binding.prepareCredentialForDomain(domain, row, permissions)
}

// domain was verified in the caller's scope, or derived by PrepareCredential.
func (binding Binding[M]) prepareCredentialForDomain(domain auth.CredentialDomain, row M, permissions []auth.Permission) (auth.Credential, error) {
	key, present, err := binding.state.objects.PrimaryKey(row)
	_, integer := key.Integer()
	if err != nil || !present || !integer {
		return auth.Credential{}, invalidAccountCredential()
	}
	return binding.prepareCandidateCredential(domain, row, permissions)
}

// Creation validates the same credential fields before it has a stored key.
// This private result is never published as an authenticated or persisted row.
func (binding Binding[M]) prepareCandidateCredential(domain auth.CredentialDomain, row M, permissions []auth.Permission) (auth.Credential, error) {
	values, err := binding.state.objects.ModelValues(row)
	if err != nil {
		return auth.Credential{}, invalidAccountCredential()
	}
	role := binding.state.role
	revision, valid := values[role.Revision].Integer()
	if !valid || revision <= 0 {
		return auth.Credential{}, invalidAccountCredential()
	}
	principalID, err := binding.credentialText(values, role.PrincipalID)
	if err != nil {
		return auth.Credential{}, err
	}
	login, err := binding.credentialText(values, role.Login)
	if err != nil {
		return auth.Credential{}, err
	}
	encoded, err := binding.credentialText(values, role.Password)
	if err != nil {
		return auth.Credential{}, err
	}
	active, err := credentialBoolean(values, role.Active)
	if err != nil {
		return auth.Credential{}, err
	}
	staff, err := credentialBoolean(values, role.Staff)
	if err != nil {
		return auth.Credential{}, err
	}
	superuser, err := credentialBoolean(values, role.Superuser)
	if err != nil {
		return auth.Credential{}, err
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID, Active: active, Staff: staff, Superuser: superuser, Permissions: permissions})
	if err != nil {
		return auth.Credential{}, invalidAccountCredential()
	}
	credential, err := auth.NewCredentialForDomain(domain, login, encoded, principal)
	if err != nil {
		return auth.Credential{}, invalidAccountCredential()
	}
	return credential, nil
}

func (binding Binding[M]) credentialText(values map[string]query.Value, name string) (string, error) {
	value, valid := values[name].String()
	field, present := binding.state.credentialFields[name]
	if !present || !valid || !utf8.ValidString(value) || strings.ContainsRune(value, 0) ||
		field.MaxLength > 0 && utf8.RuneCountInString(value) > field.MaxLength {
		return "", invalidAccountCredential()
	}
	return value, nil
}

func credentialBoolean(values map[string]query.Value, role ir.AccountBoolean) (bool, error) {
	if role.Constant != nil {
		return *role.Constant, nil
	}
	value, valid := values[role.Field].Boolean()
	if !valid {
		return false, invalidAccountCredential()
	}
	return value, nil
}

func invalidAccountCredential() error {
	return &auth.Error{Code: auth.CodeCredential, Detail: "account snapshot or credential domain is invalid"}
}
