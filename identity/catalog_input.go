package identity

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/orm"
)

// GroupCreate and GroupPatch own their permission selections. Omission keeps
// an existing collection, while WithPermissions() explicitly clears it.
type GroupCreate struct{ patch GroupPatch }

func NewGroupCreate(name string) GroupCreate {
	return GroupCreate{patch: GroupPatch{}.WithName(name)}
}
func (input GroupCreate) WithPermissions(keys ...int64) GroupCreate {
	input.patch = input.patch.WithPermissions(keys...)
	return input
}

type GroupPatch struct {
	name        orm.Change[string]
	permissions orm.Change[[]int64]
}

func (input GroupPatch) WithName(value string) GroupPatch {
	input.name = orm.Set(value)
	return input
}
func (input GroupPatch) WithPermissions(keys ...int64) GroupPatch {
	input.permissions = orm.Set(slices.Clone(keys))
	return input
}
func (input GroupPatch) normalize() (GroupPatch, error) {
	if name, set := input.name.Get(); set {
		if err := validateCatalogName(name, 150); err != nil {
			return GroupPatch{}, err
		}
	}
	if keys, set := input.permissions.Get(); set {
		keys, err := normalizeIdentityKeys(keys, auth.MaximumPermissions, "permissions")
		if err != nil {
			return GroupPatch{}, err
		}
		input.permissions = orm.Set(keys)
	}
	return input, nil
}

// Permission code is the exact canonical authorization name. Renaming a code
// preserves its database key and assignments; future resolution sees the name.
type PermissionCreate struct{ patch PermissionPatch }

func NewPermissionCreate(code, name string) PermissionCreate {
	return PermissionCreate{patch: PermissionPatch{}.WithCode(code).WithName(name)}
}

type PermissionPatch struct{ code, name orm.Change[string] }

func (input PermissionPatch) WithCode(value string) PermissionPatch {
	input.code = orm.Set(value)
	return input
}
func (input PermissionPatch) WithName(value string) PermissionPatch {
	input.name = orm.Set(value)
	return input
}
func (input PermissionPatch) normalize() (PermissionPatch, error) {
	if code, set := input.code.Get(); set {
		if _, err := auth.NewPermission(code); err != nil {
			return PermissionPatch{}, managementInputError("code", "invalid")
		}
	}
	if name, set := input.name.Get(); set {
		if err := validateCatalogName(name, 255); err != nil {
			return PermissionPatch{}, err
		}
	}
	return input, nil
}

func validateCatalogName(value string, maximum int) error {
	if value == "" {
		return managementInputError("name", "required")
	}
	// Bound the byte scan before counting Unicode characters. Names are not
	// trimmed or case-folded by the model service; forms own input presentation.
	if len(value) > maximum*utf8.UTFMax || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || utf8.RuneCountInString(value) > maximum {
		return managementInputError("name", "invalid")
	}
	return nil
}
