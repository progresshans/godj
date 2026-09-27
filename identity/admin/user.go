package identityadmin

import (
	"context"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/internal/forminput"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
)

var userFields = []string{"username", "first_name", "last_name", "email", "active", "staff", "superuser", "groups", "permissions"}

type userRow struct {
	identity.UserDetails
	full bool
}

func (a *registration) registerUser(builder *admin.Builder) error {
	passwords, err := forminput.PasswordFields(true)
	if err != nil {
		return err
	}
	passwordSpec, err := forms.NewSpec(passwords, forminput.PasswordConfirmation())
	if err != nil {
		return err
	}
	creationForm, err := identity.NewAdminUserCreationForm(a.manager)
	if err != nil {
		return err
	}
	confirmation, err := forms.BooleanField("confirm", forms.WithRequired(true), forms.WithLabel("Disable password login and revoke this user's sessions"))
	if err != nil {
		return err
	}
	disableSpec, err := forms.NewSpec([]forms.Field{confirmation})
	if err != nil {
		return err
	}
	metadata := models.UserDescriptor{}.Metadata()
	usernameLimit := 0
	for _, field := range metadata.Fields {
		if field.Name == "username" && field.Kind == ir.FieldChar {
			usernameLimit = field.MaxLength
		}
	}
	if usernameLimit < 150 {
		return &admin.ConfigError{Path: "identity.username", Code: "invalid_storage_limit"}
	}
	// Creation adopts the default UserCreationForm input limit. Editing follows
	// the declared storage field so an existing longer API/CLI-created username
	// can still be displayed and retained without a hidden form-initial failure.
	username := formmodel.OverrideField("username", formmodel.WithLabel("Username"), formmodel.WithStringNormalizer(forminput.UsernameNormalizer(usernameLimit)), formmodel.WithValidators(forminput.UsernameValidator(usernameLimit)))
	return admin.RegisterModel(builder, admin.ModelConfig[userRow]{
		AppLabel: "godj_identity", Slug: "users", Model: metadata,
		FormFields: userFields, RevisionField: "revision",
		ReadOnlyFields: []admin.ReadOnlyField[userRow]{{Name: "password_usable", Label: "Password login", Value: func(value userRow) (string, error) {
			if value.PasswordUsable {
				return "Enabled", nil
			}
			return "Disabled", nil
		}}, {Name: "last_login", Label: "Last login", Value: func(value userRow) (string, error) {
			if value.LastLogin == nil {
				return "Never", nil
			}
			return value.LastLogin.UTC().Format(time.RFC3339Nano), nil
		}}},
		FormOverrides: []formmodel.Override{username,
			formmodel.OverrideField("first_name", formmodel.WithRequired(false)), formmodel.OverrideField("last_name", formmodel.WithRequired(false)),
			formmodel.OverrideField("email", formmodel.WithRequired(false)),
			formmodel.OverrideField("groups", formmodel.WithRequired(false)), formmodel.OverrideField("permissions", formmodel.WithRequired(false)),
		},
		CreateForm: &admin.FormConfig{Definition: creationForm.Definition()},
		ValidateCreate: func(ctx context.Context, actor auth.Principal, form forms.Form) error {
			return operationError(creationForm.Check(ctx, actor, form))
		},
		AdditionalAddPermissions: []auth.Permission{identity.ChangeUser}, AdditionalAuditFields: []string{"password"},
		RelatedChoices: []admin.RelatedChoices{
			{Field: "groups", Permission: identity.ChangeUser, Load: func(ctx context.Context, p auth.Principal) ([]forms.Choice, error) {
				return formChoices(a.manager.UserGroupChoices(ctx, p))
			}},
			{Field: "permissions", Permission: identity.ChangeUser, Load: func(ctx context.Context, p auth.Principal) ([]forms.Choice, error) {
				return formChoices(a.manager.UserPermissionChoices(ctx, p))
			}},
		},
		ListFields:  []string{"id", "username", "email", "active", "staff"},
		Permissions: admin.Permissions{View: identity.ViewUser, Add: identity.AddUser, Change: identity.ChangeUser, Delete: identity.DeleteUser},
		List: func(ctx context.Context, p auth.Principal, r admin.ListRequest) (admin.Page[userRow], error) {
			page, err := a.manager.Users(ctx, p, r.Offset, r.Limit)
			if err != nil {
				return admin.Page[userRow]{}, operationError(err)
			}
			items := make([]userRow, len(page.Users))
			for i, value := range page.Users {
				items[i] = userRow{UserDetails: identity.UserDetails{Profile: value}}
			}
			return admin.Page[userRow]{Items: items, Total: page.Total, Offset: r.Offset, Limit: r.Limit}, nil
		},
		Get: func(ctx context.Context, p auth.Principal, id int64) (userRow, bool, error) {
			value, err := a.manager.User(ctx, p, id)
			if notFound(err) {
				return userRow{}, false, nil
			}
			return userRow{value, true}, err == nil, operationError(err)
		},
		Snapshot: userSnapshot, Initial: func(value userRow) (map[string]forms.Value, error) { return userInitial(value.UserDetails), nil },
		Create: func(ctx context.Context, actor auth.Principal, values forms.Values) (userRow, error) {
			principalID, err := a.principalID(ctx)
			if err != nil {
				return userRow{}, err
			}
			created, err := creationForm.Create(ctx, actor, principalID, values)
			return userRow{created, true}, operationError(err)
		},
		Update: func(ctx context.Context, p auth.Principal, m admin.Mutation, values forms.Values) (userRow, []string, error) {
			before, err := a.manager.User(ctx, p, m.ID)
			if err != nil {
				return userRow{}, nil, operationError(err)
			}
			if err := checkRevision(m.Revision, before.Revision); err != nil {
				return userRow{}, nil, err
			}
			patch, err := userPatch(values)
			if err != nil {
				return userRow{}, nil, err
			}
			after, err := a.manager.UpdateUser(ctx, p, m.ID, m.Revision, patch)
			if err != nil {
				return userRow{}, nil, operationError(err)
			}
			return userRow{after, true}, changedFields(userFields, userInitial(before), userInitial(after)), nil
		},
		Delete: func(ctx context.Context, p auth.Principal, m admin.Mutation) (userRow, error) {
			before, err := a.manager.User(ctx, p, m.ID)
			if err != nil {
				return userRow{}, operationError(err)
			}
			if err := checkRevision(m.Revision, before.Revision); err != nil {
				return userRow{}, err
			}
			_, err = a.manager.DeleteUser(ctx, p, m.ID, m.Revision, a.deletions.Users)
			if err != nil {
				return userRow{}, operationError(err)
			}
			return userRow{before, true}, nil
		},
		History: func(ctx context.Context, p auth.Principal, id int64, r admin.HistoryRequest) ([]admin.AuditEntry, error) {
			entries, err := a.manager.UserHistory(ctx, p, id, r.Limit)
			return entries, operationError(err)
		},
		Commands: []admin.CommandConfig{{Name: "password", Label: "Change password", Permission: identity.ChangeUser, Form: passwordSpec, Run: func(ctx context.Context, p auth.Principal, m admin.Mutation, values forms.Values) (admin.CommandResult, error) {
			password, ok := values.String("password1")
			if !ok {
				return admin.CommandResult{}, invalidInput()
			}
			profile, err := a.manager.SetPassword(ctx, p, m.ID, m.Revision, password)
			if err != nil {
				return admin.CommandResult{}, passwordError(err)
			}
			return admin.CommandResult{ID: profile.ID, Revision: profile.Revision}, nil
		}}, {Name: "disable-password", Label: "Disable password login", Permission: identity.ChangeUser, Form: disableSpec, Run: func(ctx context.Context, p auth.Principal, m admin.Mutation, values forms.Values) (admin.CommandResult, error) {
			confirmed, ok := values.Boolean("confirm")
			if !ok || !confirmed {
				return admin.CommandResult{}, invalidInput()
			}
			profile, err := a.manager.SetUnusablePassword(ctx, p, m.ID, m.Revision)
			if err != nil {
				return admin.CommandResult{}, operationError(err)
			}
			return admin.CommandResult{ID: profile.ID, Revision: profile.Revision}, nil
		}}},
	})
}

func userSnapshot(value userRow) (admin.Object, error) {
	values := map[string]templates.Value{"id": templates.Integer(value.ID), "username": templates.String(value.Username), "first_name": templates.String(value.FirstName), "last_name": templates.String(value.LastName), "email": templates.String(value.Email), "active": templates.Bool(value.Active), "staff": templates.Bool(value.Staff), "superuser": templates.Bool(value.Superuser), "revision": templates.Integer(value.Revision)}
	if value.full {
		values["groups"] = integerValues(value.GroupIDs)
		values["permissions"] = integerValues(value.PermissionIDs)
	}
	return admin.NewObject(value.ID, value.Username, values)
}

func userInitial(value identity.UserDetails) map[string]forms.Value {
	return map[string]forms.Value{"username": forms.String(value.Username), "first_name": forms.String(value.FirstName), "last_name": forms.String(value.LastName), "email": forms.String(value.Email), "active": forms.Boolean(value.Active), "staff": forms.Boolean(value.Staff), "superuser": forms.Boolean(value.Superuser), "groups": forms.Integers(value.GroupIDs...), "permissions": forms.Integers(value.PermissionIDs...)}
}

func userPatch(values forms.Values) (identity.UserPatch, error) {
	username, a := values.String("username")
	first, b := values.String("first_name")
	last, c := values.String("last_name")
	email, d := values.String("email")
	active, e := values.Boolean("active")
	staff, f := values.Boolean("staff")
	superuser, g := values.Boolean("superuser")
	groups, h := values.Integers("groups")
	permissions, i := values.Integers("permissions")
	if !(a && b && c && d && e && f && g && h && i) {
		return identity.UserPatch{}, invalidInput()
	}
	return identity.UserPatch{}.WithUsername(username).WithFirstName(first).WithLastName(last).WithEmail(email).WithActive(active).WithStaff(staff).WithSuperuser(superuser).WithGroups(groups...).WithPermissions(permissions...), nil
}
