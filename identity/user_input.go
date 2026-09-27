package identity

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

const MaximumUserGroups = 256

// UserCreate owns detached administrative input. PrincipalID is chosen by the
// host and never changes. Password is a separate argument to CreateUser, so it
// cannot enter a profile, relation selection or diagnostic input snapshot.
type UserCreate struct {
	principalID                  string
	patch                        UserPatch
	caseInsensitiveUsernameCheck bool
}

func NewUserCreate(principalID, username string) UserCreate {
	return UserCreate{principalID: principalID, patch: UserPatch{}.WithUsername(username).WithActive(true)}
}

// WithCaseInsensitiveUsernameCheck selects UserCreationForm's additional
// duplicate check. It preserves the normalized username and login behavior;
// this is a creation policy, not a new case-insensitive database constraint.
// The manager checks it before hashing and again inside its write fence.
func (input UserCreate) WithCaseInsensitiveUsernameCheck() UserCreate {
	input.caseInsensitiveUsernameCheck = true
	return input
}
func (input UserCreate) WithFirstName(value string) UserCreate {
	input.patch = input.patch.WithFirstName(value)
	return input
}
func (input UserCreate) WithLastName(value string) UserCreate {
	input.patch = input.patch.WithLastName(value)
	return input
}
func (input UserCreate) WithEmail(value string) UserCreate {
	input.patch = input.patch.WithEmail(value)
	return input
}
func (input UserCreate) WithActive(value bool) UserCreate {
	input.patch = input.patch.WithActive(value)
	return input
}
func (input UserCreate) WithStaff(value bool) UserCreate {
	input.patch = input.patch.WithStaff(value)
	return input
}
func (input UserCreate) WithSuperuser(value bool) UserCreate {
	input.patch = input.patch.WithSuperuser(value)
	return input
}
func (input UserCreate) WithGroups(keys ...int64) UserCreate {
	input.patch = input.patch.WithGroups(keys...)
	return input
}
func (input UserCreate) WithPermissions(keys ...int64) UserCreate {
	input.patch = input.patch.WithPermissions(keys...)
	return input
}

// UserPatch deliberately has no password/hash, principal ID, timestamp or
// revision setter. Omitted collections are kept; an explicit empty set clears.
type UserPatch struct {
	username, firstName, lastName, email orm.Change[string]
	active, staff, superuser             orm.Change[bool]
	groups, permissions                  orm.Change[[]int64]
}

func (input UserPatch) WithUsername(value string) UserPatch {
	input.username = orm.Set(value)
	return input
}
func (input UserPatch) WithFirstName(value string) UserPatch {
	input.firstName = orm.Set(value)
	return input
}
func (input UserPatch) WithLastName(value string) UserPatch {
	input.lastName = orm.Set(value)
	return input
}
func (input UserPatch) WithEmail(value string) UserPatch { input.email = orm.Set(value); return input }
func (input UserPatch) WithActive(value bool) UserPatch  { input.active = orm.Set(value); return input }
func (input UserPatch) WithStaff(value bool) UserPatch   { input.staff = orm.Set(value); return input }
func (input UserPatch) WithSuperuser(value bool) UserPatch {
	input.superuser = orm.Set(value)
	return input
}
func (input UserPatch) WithGroups(keys ...int64) UserPatch {
	input.groups = orm.Set(slices.Clone(keys))
	return input
}
func (input UserPatch) WithPermissions(keys ...int64) UserPatch {
	input.permissions = orm.Set(slices.Clone(keys))
	return input
}

func managementInputError(field validation.Field, code validation.Code) error {
	return validation.Reject(validation.NewErrors(validation.New(field, code)), nil)
}

// NormalizeUsername applies the pinned Unicode profile and the User's Schema
// IR storage limit. It does not strip input or select a case-insensitive policy.
// Forms may impose a narrower input limit before calling the manager.
func NormalizeUsername(value string) (string, error) {
	// Bound work before normalization as well as the final credential envelope.
	if auth.ValidateUsername(value) != nil {
		return "", managementInputError("username", "invalid")
	}
	value = unicode16.NFKC(value)
	if auth.ValidateUsername(value) != nil {
		return "", managementInputError("username", "invalid")
	}
	if err := validateIdentityTexts(identityText{"username", value}); err != nil {
		return "", err
	}
	return value, nil
}

// Model text bounds are read from the generated projection of Schema IR.
// The credential's byte envelope must not silently replace character limits.
type identityText struct{ name, value string }

func validateIdentityTexts(values ...identityText) error {
	metadata := models.UserDescriptor{}.Metadata()
	for _, item := range values {
		if !utf8.ValidString(item.value) || strings.ContainsRune(item.value, 0) {
			return managementInputError(validation.Field(item.name), "invalid")
		}
		found := false
		for _, field := range metadata.Fields {
			if field.Name != item.name {
				continue
			}
			found = true
			if field.Kind != ir.FieldChar || field.MaxLength <= 0 {
				return managementError(CodeInvalidConfig, "user", nil)
			}
			if utf8.RuneCountInString(item.value) > field.MaxLength {
				return managementInputError(validation.Field(item.name), "invalid")
			}
			break
		}
		if !found {
			return managementError(CodeInvalidConfig, "user", nil)
		}
	}
	return nil
}

func validateUserProfileText(row models.User) error {
	return validateIdentityTexts(identityText{"username", row.Username}, identityText{"first_name", row.FirstName}, identityText{"last_name", row.LastName}, identityText{"email", row.Email})
}

func normalizeIdentityEmail(value string) string {
	trimmed := unicode16.TrimSpace(value)
	if at := strings.LastIndexByte(trimmed, '@'); at >= 0 {
		return trimmed[:at+1] + unicode16.Lower(trimmed[at+1:])
	}
	return value
}

func normalizeIdentityKeys(keys []int64, maximum int, field validation.Field) ([]int64, error) {
	if len(keys) > maximum {
		return nil, managementInputError(field, "max_items")
	}
	result := append([]int64{}, keys...)
	slices.Sort(result)
	for _, key := range result {
		if key <= 0 {
			return nil, managementInputError(field, "invalid_choice")
		}
	}
	return slices.Compact(result), nil
}

func (input UserPatch) normalize() (UserPatch, error) {
	if username, ok := input.username.Get(); ok {
		normalized, err := NormalizeUsername(username)
		if err != nil {
			return UserPatch{}, err
		}
		input.username = orm.Set(normalized)
	}
	if email, ok := input.email.Get(); ok {
		if len(email) > 4096 || !utf8.ValidString(email) || strings.ContainsRune(email, 0) {
			return UserPatch{}, managementInputError("email", "invalid")
		}
		input.email = orm.Set(normalizeIdentityEmail(email))
	}
	var texts []identityText
	for _, item := range []struct {
		field validation.Field
		value orm.Change[string]
	}{
		{"first_name", input.firstName}, {"last_name", input.lastName}, {"email", input.email},
	} {
		value, set := item.value.Get()
		if set {
			texts = append(texts, identityText{string(item.field), value})
		}
	}
	if len(texts) > 0 {
		if err := validateIdentityTexts(texts...); err != nil {
			return UserPatch{}, err
		}
	}
	if keys, set := input.groups.Get(); set {
		values, err := normalizeIdentityKeys(keys, MaximumUserGroups, "groups")
		if err != nil {
			return UserPatch{}, err
		}
		input.groups = orm.Set(values)
	}
	if keys, set := input.permissions.Get(); set {
		values, err := normalizeIdentityKeys(keys, auth.MaximumPermissions, "permissions")
		if err != nil {
			return UserPatch{}, err
		}
		input.permissions = orm.Set(values)
	}
	return input, nil
}

func (input UserPatch) apply(row models.User) (models.User, models.UserPatch, []string) {
	patch := models.UserPatch{}
	changed := []string{}
	if value, set := input.username.Get(); set && value != row.Username {
		row.Username = value
		patch = patch.WithUsername(value)
		changed = append(changed, "username")
	}
	if value, set := input.firstName.Get(); set && value != row.FirstName {
		row.FirstName = value
		patch = patch.WithFirstName(value)
		changed = append(changed, "first_name")
	}
	if value, set := input.lastName.Get(); set && value != row.LastName {
		row.LastName = value
		patch = patch.WithLastName(value)
		changed = append(changed, "last_name")
	}
	if value, set := input.email.Get(); set && value != row.Email {
		row.Email = value
		patch = patch.WithEmail(value)
		changed = append(changed, "email")
	}
	if value, set := input.active.Get(); set && value != row.Active {
		row.Active = value
		patch = patch.WithActive(value)
		changed = append(changed, "active")
	}
	if value, set := input.staff.Get(); set && value != row.Staff {
		row.Staff = value
		patch = patch.WithStaff(value)
		changed = append(changed, "staff")
	}
	if value, set := input.superuser.Get(); set && value != row.Superuser {
		row.Superuser = value
		patch = patch.WithSuperuser(value)
		changed = append(changed, "superuser")
	}
	return row, patch, changed
}
