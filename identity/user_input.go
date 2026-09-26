package identity

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/validation"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

const MaximumUserGroups = 256

// UserCreate owns detached administrative input. PrincipalID is chosen by the
// host and never changes. Password is a separate argument to CreateUser, so it
// cannot enter a profile, relation selection or diagnostic input snapshot.
type UserCreate struct {
	principalID string
	patch       UserPatch
}

func NewUserCreate(principalID, username string) UserCreate {
	return UserCreate{principalID: principalID, patch: UserPatch{}.WithUsername(username).WithActive(true)}
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

func userInputError(field validation.Field, code validation.Code) error {
	return validation.Reject(validation.NewErrors(validation.New(field, code)), nil)
}

func normalizeIdentityUsername(value string) (string, error) {
	// Bound work before normalization as well as the final credential envelope.
	if auth.ValidateUsername(value) != nil {
		return "", userInputError("username", "invalid")
	}
	value = norm.NFKC.String(value)
	if auth.ValidateUsername(value) != nil {
		return "", userInputError("username", "invalid")
	}
	return value, nil
}

func normalizeIdentityEmail(value string) string {
	// Python's str.strip also includes the four C0 information separators.
	trimmed := strings.TrimFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
	if at := strings.LastIndexByte(trimmed, '@'); at >= 0 {
		// Full Unicode lowercasing can expand characters or depend on context.
		// A fresh Caser owns its transformation state for this call.
		return trimmed[:at+1] + cases.Lower(language.Und).String(trimmed[at+1:])
	}
	return value
}

func normalizeIdentityKeys(keys []int64, maximum int, field validation.Field) ([]int64, error) {
	if len(keys) > maximum {
		return nil, userInputError(field, "max_items")
	}
	result := append([]int64{}, keys...)
	slices.Sort(result)
	for _, key := range result {
		if key <= 0 {
			return nil, userInputError(field, "invalid_choice")
		}
	}
	return slices.Compact(result), nil
}

func (input UserPatch) normalize() (UserPatch, error) {
	if username, ok := input.username.Get(); ok {
		normalized, err := normalizeIdentityUsername(username)
		if err != nil {
			return UserPatch{}, err
		}
		input.username = orm.Set(normalized)
	}
	if email, ok := input.email.Get(); ok {
		if len(email) > 4096 || !utf8.ValidString(email) || strings.ContainsRune(email, 0) {
			return UserPatch{}, userInputError("email", "invalid")
		}
		input.email = orm.Set(normalizeIdentityEmail(email))
	}
	for _, item := range []struct {
		field   validation.Field
		value   orm.Change[string]
		maximum int
	}{
		{"first_name", input.firstName, 150}, {"last_name", input.lastName, 150}, {"email", input.email, 254},
	} {
		value, set := item.value.Get()
		if set && (!utf8.ValidString(value) || strings.ContainsRune(value, 0) || utf8.RuneCountInString(value) > item.maximum) {
			return UserPatch{}, userInputError(item.field, "invalid")
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
