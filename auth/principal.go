package auth

import (
	"strings"
	"unicode/utf8"
)

const (
	maxPrincipalIDBytes = 128
	maxPermissionBytes  = 128
	maxPermissions      = 256
)

// Permission is one exact, application-defined authorization capability.
type Permission string

// NewPermission validates a lowercase dotted permission namespace.
func NewPermission(value string) (Permission, error) {
	if !validPermission(value) {
		return "", &Error{Code: CodeInvalidInput, Field: "permission", Detail: "permission is not a canonical lowercase dotted name"}
	}
	return Permission(value), nil
}

// PrincipalConfig is immutable startup input for one principal snapshot.
type PrincipalConfig struct {
	ID          string
	Active      bool
	Permissions []Permission
}

// Principal is an immutable identity and permission snapshot. Its zero value
// is anonymous.
type Principal struct {
	id          string
	active      bool
	permissions []Permission
	permission  map[Permission]struct{}
}

func NewPrincipal(config PrincipalConfig) (Principal, error) {
	if !validIdentity(config.ID) {
		return Principal{}, &Error{Code: CodeInvalidInput, Field: "principal_id", Detail: "principal identifier is malformed or too large"}
	}
	if len(config.Permissions) > maxPermissions {
		return Principal{}, &Error{Code: CodeInvalidInput, Field: "permissions", Detail: "permission count exceeds the supported limit"}
	}
	permissions := make([]Permission, len(config.Permissions))
	index := make(map[Permission]struct{}, len(config.Permissions))
	for offset, permission := range config.Permissions {
		if !validPermission(string(permission)) {
			return Principal{}, &Error{Code: CodeInvalidInput, Field: "permissions", Detail: "permission contains a noncanonical name"}
		}
		if _, duplicate := index[permission]; duplicate {
			return Principal{}, &Error{Code: CodeInvalidInput, Field: "permissions", Detail: "permission is duplicated"}
		}
		permissions[offset] = permission
		index[permission] = struct{}{}
	}
	return Principal{id: config.ID, active: config.Active, permissions: permissions, permission: index}, nil
}

func Anonymous() Principal { return Principal{} }

func (p Principal) ID() string          { return p.id }
func (p Principal) Active() bool        { return p.active && p.id != "" }
func (p Principal) Authenticated() bool { return p.Active() }
func (p Principal) String() string      { return "auth.Principal{redacted}" }
func (p Principal) GoString() string    { return "auth.Principal{redacted}" }

// Permissions returns a detached declaration-order snapshot.
func (p Principal) Permissions() []Permission {
	return append([]Permission(nil), p.permissions...)
}

func (p Principal) Has(permission Permission) bool {
	if !p.Authenticated() || !validPermission(string(permission)) {
		return false
	}
	_, ok := p.permission[permission]
	return ok
}

func validIdentity(value string) bool {
	return value != "" && len(value) <= maxPrincipalIDBytes && utf8.ValidString(value) &&
		!strings.ContainsRune(value, '\x00') && strings.TrimSpace(value) == value
}

func validPermission(value string) bool {
	if value == "" || len(value) > maxPermissionBytes || !utf8.ValidString(value) {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for index := 0; index < len(part); index++ {
			character := part[index]
			if character >= 'a' && character <= 'z' || index > 0 && character >= '0' && character <= '9' ||
				index > 0 && (character == '_' || character == '-') {
				continue
			}
			return false
		}
	}
	return true
}
