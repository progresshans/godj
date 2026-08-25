package systemstate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strings"

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

// BootstrapConfig is consumed during Open. The raw password is never retained
// by Runtime or rendered through String/GoString.
type BootstrapConfig struct {
	Username       string
	Password       string `json:"-"`
	PrincipalID    string
	Active         bool
	Permissions    []auth.Permission
	PasswordHasher auth.PasswordHasher `json:"-"`
	SessionLimits  sessions.Limits
	MaxSessions    int
	AuditCapacity  int
}

func (BootstrapConfig) String() string   { return "systemstate.BootstrapConfig{redacted}" }
func (BootstrapConfig) GoString() string { return "systemstate.BootstrapConfig{redacted}" }

type bootstrapMaterial struct {
	username         string
	password         string
	principal        auth.Principal
	permissions      string
	passwordHasher   auth.PasswordHasher
	definitionDigest string
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

func validateBootstrapConfig(config BootstrapConfig) (bootstrapMaterial, error) {
	if isNilInterface(config.PasswordHasher) {
		return bootstrapMaterial{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "password hasher is nil",
		}
	}
	if strings.TrimSpace(config.Password) == "" {
		return bootstrapMaterial{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password",
			Detail: "bootstrap password is empty",
		}
	}
	permissions := append([]auth.Permission(nil), config.Permissions...)
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          config.PrincipalID,
		Active:      config.Active,
		Permissions: permissions,
	})
	if err != nil {
		return bootstrapMaterial{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "principal",
			Detail: "bootstrap principal is invalid",
			Cause:  err,
		}
	}
	if _, err := auth.NewCredential(config.Username, "systemstate-bootstrap-validation", principal); err != nil {
		return bootstrapMaterial{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "username",
			Detail: "bootstrap username is invalid",
			Cause:  err,
		}
	}
	payload, err := encodePermissions(permissions)
	if err != nil {
		return bootstrapMaterial{}, err
	}
	return bootstrapMaterial{
		username:         config.Username,
		password:         config.Password,
		principal:        principal,
		permissions:      payload,
		passwordHasher:   config.PasswordHasher,
		definitionDigest: initialDefinitionDigest,
	}, nil
}

func (material bootstrapMaterial) encodedRow(ctx context.Context) (credentialRow, error) {
	encoded, err := material.passwordHasher.Hash(ctx, material.password)
	if err != nil {
		return credentialRow{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "bootstrap password could not be hashed",
			Cause:  err,
		}
	}
	if len(encoded) > credentialEncodedPasswordMaxLength {
		return credentialRow{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "encoded bootstrap password exceeds the current schema bound",
		}
	}
	if err := material.passwordHasher.ValidateEncoded(encoded); err != nil {
		return credentialRow{}, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "bootstrap password hash is outside the current work profile",
			Cause:  err,
		}
	}
	return credentialRow{
		principalID:      material.principal.ID(),
		username:         material.username,
		encodedPassword:  encoded,
		active:           material.principal.Active(),
		permissions:      material.permissions,
		definitionDigest: material.definitionDigest,
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
		return nil, &Error{
			Code:   CodeSchemaUnavailable,
			Field:  credentialTableName,
			Detail: "credential table is unavailable",
			Cause:  err,
		}
	}
	if rows == nil {
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
			Code:   CodeSchemaUnavailable,
			Field:  credentialTableName,
			Detail: "bootstrap credential could not be stored",
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

func verifyCredential(
	ctx context.Context,
	row credentialRow,
	material bootstrapMaterial,
) (*auth.MemoryAuthenticator, error) {
	if row.id <= 0 || len(row.principalID) > credentialPrincipalIDMaxLength ||
		len(row.username) > credentialUsernameMaxLength ||
		len(row.encodedPassword) > credentialEncodedPasswordMaxLength ||
		len(row.permissions) > credentialPermissionsMaxLength ||
		len(row.definitionDigest) > credentialDefinitionDigestMaxLength {
		return nil, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential row exceeds the current schema profile",
		}
	}
	permissions, err := decodePermissions(row.permissions)
	if err != nil {
		return nil, err
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          row.principalID,
		Active:      row.active,
		Permissions: permissions,
	})
	if err != nil {
		return nil, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential principal is invalid",
			Cause:  err,
		}
	}
	if err := material.passwordHasher.ValidateEncoded(row.encodedPassword); err != nil {
		return nil, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential password profile is invalid",
			Cause:  err,
		}
	}
	verified, err := material.passwordHasher.Verify(ctx, material.password, row.encodedPassword)
	if err != nil {
		return nil, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential password could not be verified",
			Cause:  err,
		}
	}
	if row.principalID != material.principal.ID() || row.username != material.username ||
		row.active != material.principal.Active() || row.permissions != material.permissions ||
		row.definitionDigest != material.definitionDigest || !verified {
		return nil, &Error{
			Code:   CodeCredentialMismatch,
			Field:  "bootstrap",
			Detail: "configured bootstrap material does not match durable credential state",
		}
	}
	credential, err := auth.NewCredential(row.username, row.encodedPassword, principal)
	if err != nil {
		return nil, &Error{
			Code:   CodeCorruptState,
			Field:  "credential",
			Detail: "stored credential is invalid",
			Cause:  err,
		}
	}
	authenticator, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, material.passwordHasher)
	if err != nil {
		return nil, &Error{
			Code:   CodeInvalidConfig,
			Field:  "password_hasher",
			Detail: "credential authenticator could not be initialized",
			Cause:  err,
		}
	}
	return authenticator, nil
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
			Detail: "bootstrap permissions are invalid",
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
			Detail: "bootstrap permissions exceed the current storage bound",
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
