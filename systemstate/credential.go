package systemstate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

const permissionPayloadPrefix = "v1."

var (
	credentialIDRef          = query.NewFieldRef("id", "id", query.FieldInteger, false)
	credentialPrincipalIDRef = query.NewFieldRef(
		credentialPrincipalIDColumn,
		credentialPrincipalIDColumn,
		query.FieldString,
		false,
	)
	credentialUsernameRef = query.NewFieldRef(
		credentialUsernameColumn,
		credentialUsernameColumn,
		query.FieldString,
		false,
	)
	credentialEncodedPasswordRef = query.NewFieldRef(
		credentialEncodedPasswordColumn,
		credentialEncodedPasswordColumn,
		query.FieldString,
		false,
	)
	credentialActiveRef = query.NewFieldRef(
		credentialActiveColumn,
		credentialActiveColumn,
		query.FieldBoolean,
		false,
	)
	credentialPermissionsRef = query.NewFieldRef(
		credentialPermissionsColumn,
		credentialPermissionsColumn,
		query.FieldString,
		false,
	)
	credentialDefinitionDigestRef = query.NewFieldRef(
		credentialDefinitionDigestColumn,
		credentialDefinitionDigestColumn,
		query.FieldString,
		false,
	)
	credentialFieldRefs = []query.FieldRef{
		credentialIDRef,
		credentialPrincipalIDRef,
		credentialUsernameRef,
		credentialEncodedPasswordRef,
		credentialActiveRef,
		credentialPermissionsRef,
		credentialDefinitionDigestRef,
	}
)

// CredentialPolicy is the project-owned immutable identity, authorization,
// and encoded-password profile expected for the sole durable operator. Stored
// username and encoded password values remain database-owned credential data.
type CredentialPolicy struct {
	Principal      auth.Principal
	PasswordHasher auth.PasswordHasher `json:"-"`
}

func (CredentialPolicy) String() string   { return "systemstate.CredentialPolicy{redacted}" }
func (CredentialPolicy) GoString() string { return "systemstate.CredentialPolicy{redacted}" }

// RuntimeConfig combines the immutable credential policy with the bounded
// durable session and audit deployment profile. It contains no raw password.
type RuntimeConfig struct {
	CredentialPolicy CredentialPolicy
	SessionLimits    sessions.Limits
	MaxSessions      int
	AuditCapacity    int
}

func (RuntimeConfig) String() string   { return "systemstate.RuntimeConfig{redacted}" }
func (RuntimeConfig) GoString() string { return "systemstate.RuntimeConfig{redacted}" }

// ProvisionOperatorConfig supplies the one-shot username/password material
// used only when the migrated durable credential state is empty.
type ProvisionOperatorConfig struct {
	Username         string
	Password         string `json:"-"`
	CredentialPolicy CredentialPolicy
}

func (ProvisionOperatorConfig) String() string {
	return "systemstate.ProvisionOperatorConfig{redacted}"
}

func (ProvisionOperatorConfig) GoString() string {
	return "systemstate.ProvisionOperatorConfig{redacted}"
}

type credentialPolicyMaterial struct {
	principal        auth.Principal
	permissions      string
	passwordHasher   auth.PasswordHasher
	definitionDigest string
}

type provisionOperatorMaterial struct {
	username string
	password string
	policy   credentialPolicyMaterial
}

type credentialRow struct {
	id               int64
	principalID      string
	username         string
	encodedPassword  string
	active           bool
	permissions      string
	definitionDigest string
}

func validateCredentialPolicy(policy CredentialPolicy) (credentialPolicyMaterial, error) {
	if isNilInterface(policy.PasswordHasher) {
		return credentialPolicyMaterial{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "password hasher is nil",
		}
	}
	permissions := policy.Principal.Permissions()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          policy.Principal.ID(),
		Active:      policy.Principal.Active(),
		Permissions: permissions,
	})
	if err != nil {
		return credentialPolicyMaterial{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "principal",
			Detail: "credential policy principal is invalid",
			Cause:  err,
		}
	}
	payload, err := encodePermissions(permissions)
	if err != nil {
		return credentialPolicyMaterial{}, err
	}
	return credentialPolicyMaterial{
		principal:        principal,
		permissions:      payload,
		passwordHasher:   policy.PasswordHasher,
		definitionDigest: initialDefinitionDigest,
	}, nil
}

func validateProvisionOperatorConfig(config ProvisionOperatorConfig) (provisionOperatorMaterial, error) {
	policy, err := validateCredentialPolicy(config.CredentialPolicy)
	if err != nil {
		return provisionOperatorMaterial{}, err
	}
	if strings.TrimSpace(config.Password) == "" {
		return provisionOperatorMaterial{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password",
			Detail: "operator password is empty",
		}
	}
	if !validOperatorUsername(config.Username) {
		return provisionOperatorMaterial{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "username",
			Detail: "operator username is invalid",
		}
	}
	if _, err := auth.NewCredential(config.Username, "systemstate-provision-validation", policy.principal); err != nil {
		return provisionOperatorMaterial{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "username",
			Detail: "operator username is invalid",
			Cause:  err,
		}
	}
	return provisionOperatorMaterial{
		username: config.Username,
		password: config.Password,
		policy:   policy,
	}, nil
}

func (material provisionOperatorMaterial) encodedRow(ctx context.Context) (credentialRow, error) {
	encoded, err := material.policy.passwordHasher.Hash(ctx, material.password)
	if err != nil {
		return credentialRow{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "operator password could not be hashed",
			Cause:  err,
		}
	}
	if len(encoded) > credentialEncodedPasswordMaxLength {
		return credentialRow{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "encoded operator password exceeds the current schema bound",
		}
	}
	if err := material.policy.passwordHasher.ValidateEncoded(encoded); err != nil {
		return credentialRow{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "operator password hash is outside the current work profile",
			Cause:  err,
		}
	}
	if _, err := auth.NewCredential(material.username, encoded, material.policy.principal); err != nil {
		return credentialRow{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "operator password hash is outside the current credential profile",
			Cause:  err,
		}
	}
	return credentialRow{
		principalID:      material.policy.principal.ID(),
		username:         material.username,
		encodedPassword:  encoded,
		active:           material.policy.principal.Active(),
		permissions:      material.policy.permissions,
		definitionDigest: material.policy.definitionDigest,
	}, nil
}

func readCredentialRows(ctx context.Context, queryer db.Queryer) (result []credentialRow, resultErr error) {
	plan, err := query.NewPlan(credentialTableName, credentialFieldRefs).
		WithOrderings(query.NewOrdering(credentialIDRef, query.Ascending)).
		WithLimit(2)
	if err != nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "credential_query", Detail: "credential query is invalid", Cause: err}
	}
	rows, err := queryer.Query(ctx, plan)
	if err != nil {
		if !isNilInterface(rows) {
			_ = rows.Close()
		}
		return nil, &Error{
			Code:   CodeSchemaUnavailable,
			Field:  credentialTableName,
			Detail: "credential table is unavailable",
			Cause:  err,
		}
	}
	if isNilInterface(rows) {
		return nil, &Error{
			Code:   CodeSchemaUnavailable,
			Field:  credentialTableName,
			Detail: "credential query returned nil rows",
		}
	}
	defer func() {
		if err := rows.Close(); err != nil && resultErr == nil {
			result = nil
			resultErr = &Error{
				Code:   CodeSchemaUnavailable,
				Field:  credentialTableName,
				Detail: "credential rows could not be closed",
				Cause:  err,
			}
		}
	}()

	result = make([]credentialRow, 0, 2)
	for rows.Next() {
		var row credentialRow
		if err := rows.Scan(
			&row.id,
			&row.principalID,
			&row.username,
			&row.encodedPassword,
			&row.active,
			&row.permissions,
			&row.definitionDigest,
		); err != nil {
			return nil, &Error{
				Code:   CodeCorruptState,
				Field:  "credential",
				Detail: "stored credential row could not be decoded",
				Cause:  err,
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, &Error{
			Code:   CodeSchemaUnavailable,
			Field:  credentialTableName,
			Detail: "credential rows could not be read",
			Cause:  err,
		}
	}
	return result, nil
}

func insertCredential(ctx context.Context, session db.Session, row credentialRow) (credentialRow, error) {
	identifier, err := session.Insert(ctx, query.NewInsertPlanReturningKey(
		credentialTableName,
		[]query.Assignment{
			query.NewAssignment(credentialPrincipalIDRef, query.String(row.principalID)),
			query.NewAssignment(credentialUsernameRef, query.String(row.username)),
			query.NewAssignment(credentialEncodedPasswordRef, query.String(row.encodedPassword)),
			query.NewAssignment(credentialActiveRef, query.Boolean(row.active)),
			query.NewAssignment(credentialPermissionsRef, query.String(row.permissions)),
			query.NewAssignment(credentialDefinitionDigestRef, query.String(row.definitionDigest)),
		},
		credentialIDRef,
	))
	if err != nil {
		return credentialRow{}, &Error{
			Code:   CodePersistence,
			Field:  "credential",
			Detail: "operator credential could not be stored",
			Cause:  err,
		}
	}
	if identifier <= 0 {
		return credentialRow{}, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "credential insert returned an invalid identifier",
		}
	}
	row.id = identifier
	return row, nil
}

func validateStoredCredential(row credentialRow, policy credentialPolicyMaterial) (auth.Credential, error) {
	if row.id <= 0 || len(row.principalID) > credentialPrincipalIDMaxLength ||
		len(row.username) > credentialUsernameMaxLength ||
		len(row.encodedPassword) > credentialEncodedPasswordMaxLength ||
		len(row.permissions) > credentialPermissionsMaxLength ||
		len(row.definitionDigest) > credentialDefinitionDigestMaxLength {
		return auth.Credential{}, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential row exceeds the current schema profile",
		}
	}
	if !validOperatorUsername(row.username) {
		return auth.Credential{}, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential username is invalid",
		}
	}
	permissions, err := decodePermissions(row.permissions)
	if err != nil {
		return auth.Credential{}, err
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          row.principalID,
		Active:      row.active,
		Permissions: permissions,
	})
	if err != nil {
		return auth.Credential{}, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential principal is invalid",
			Cause:  err,
		}
	}
	if err := policy.passwordHasher.ValidateEncoded(row.encodedPassword); err != nil {
		return auth.Credential{}, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential password profile is invalid",
			Cause:  err,
		}
	}
	if row.definitionDigest != policy.definitionDigest {
		return auth.Credential{}, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential definition digest is incompatible",
		}
	}
	credential, err := auth.NewCredential(row.username, row.encodedPassword, principal)
	if err != nil {
		return auth.Credential{}, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential is invalid",
			Cause:  err,
		}
	}
	if row.principalID != policy.principal.ID() || row.active != policy.principal.Active() ||
		row.permissions != policy.permissions {
		return auth.Credential{}, &Error{
			Code:   CodeCredentialPolicyMismatch,
			Field:  "credential_policy",
			Detail: "stored credential does not match the project credential policy",
		}
	}
	return credential, nil
}

func validOperatorUsername(username string) bool {
	if username == "" || len(username) > credentialUsernameMaxLength || !utf8.ValidString(username) ||
		strings.TrimSpace(username) != username {
		return false
	}
	for _, character := range username {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func encodePermissions(permissions []auth.Permission) (string, error) {
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          "systemstate-codec",
		Active:      true,
		Permissions: append([]auth.Permission(nil), permissions...),
	})
	if err != nil {
		return "", &Error{
			Code:   CodeInvalidConfig,
			Field:  "permissions",
			Detail: "credential policy permissions are invalid",
			Cause:  err,
		}
	}
	values := principal.Permissions()
	buffer := bytes.NewBuffer(make([]byte, 0, 2+len(values)*16))
	_ = binary.Write(buffer, binary.BigEndian, uint16(len(values)))
	for _, permission := range values {
		value := string(permission)
		_ = binary.Write(buffer, binary.BigEndian, uint16(len(value)))
		_, _ = buffer.WriteString(value)
	}
	payload := permissionPayloadPrefix + base64.RawURLEncoding.EncodeToString(buffer.Bytes())
	if len(payload) > credentialPermissionsMaxLength {
		return "", &Error{
			Code:   CodeInvalidConfig,
			Field:  "permissions",
			Detail: "credential policy permissions exceed the current storage bound",
		}
	}
	return payload, nil
}

func decodePermissions(payload string) ([]auth.Permission, error) {
	corrupt := func(cause error) ([]auth.Permission, error) {
		return nil, &Error{
			Code:   CodeCorruptState,
			Field:  "permissions",
			Detail: "stored permission payload is malformed or incompatible",
			Cause:  cause,
		}
	}
	if len(payload) <= len(permissionPayloadPrefix) || len(payload) > credentialPermissionsMaxLength ||
		!strings.HasPrefix(payload, permissionPayloadPrefix) {
		return corrupt(nil)
	}
	wire, err := base64.RawURLEncoding.Strict().DecodeString(payload[len(permissionPayloadPrefix):])
	if err != nil {
		return corrupt(err)
	}
	reader := bytes.NewReader(wire)
	var count uint16
	if err := binary.Read(reader, binary.BigEndian, &count); err != nil {
		return corrupt(err)
	}
	permissions := make([]auth.Permission, int(count))
	for index := range permissions {
		var length uint16
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil || length == 0 || int(length) > reader.Len() {
			return corrupt(err)
		}
		value := make([]byte, int(length))
		if _, err := io.ReadFull(reader, value); err != nil {
			return corrupt(err)
		}
		permission, err := auth.NewPermission(string(value))
		if err != nil {
			return corrupt(err)
		}
		permissions[index] = permission
	}
	if reader.Len() != 0 {
		return corrupt(nil)
	}
	canonical, err := encodePermissions(permissions)
	if err != nil || canonical != payload {
		return corrupt(err)
	}
	return permissions, nil
}
