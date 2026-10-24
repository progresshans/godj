package ir

import (
	"fmt"
	"slices"
)

// AccountNormalization names a pinned account text transformation. Login,
// authentication and email roles each validate their supported policies;
// neither a field name nor EmailField silently selects normalization.
type AccountNormalization string

const (
	AccountExact                AccountNormalization = "exact"
	AccountUnicode16NFKC        AccountNormalization = "unicode16_nfkc"
	AccountUnicode16EmailDomain AccountNormalization = "unicode16_email_domain"
)

// AccountBoolean selects either a stored Boolean field or an explicit constant.
// Constant false is different from an omitted role. A constant role cannot be
// edited through account management.
type AccountBoolean struct {
	Field    string `json:"field,omitempty"`
	Constant *bool  `json:"constant,omitempty"`
}

func (role AccountBoolean) Clone() AccountBoolean {
	if role.Constant != nil {
		value := *role.Constant
		role.Constant = &value
	}
	return role
}

// AccountCatalog binds permission meanings to declared models and logical
// field names. The catalog may share an app with the account or belong to a
// separate reusable app. Its generated app must still describe that whole app.
type AccountCatalog struct {
	Group              ModelIdentity `json:"group"`
	GroupName          string        `json:"group_name"`
	GroupRevision      string        `json:"group_revision"`
	GroupPermissions   string        `json:"group_permissions"`
	Permission         ModelIdentity `json:"permission"`
	PermissionCode     string        `json:"permission_code"`
	PermissionName     string        `json:"permission_name"`
	PermissionRevision string        `json:"permission_revision"`
}

// AccountRole is the project's account selection in Schema IR. It references
// the installed model declarations; it does not duplicate their types, columns,
// constraints or profile fields. All field names are logical schema names.
//
// This is a project binding, not a DDL operation or authorization to transition
// existing accounts. Runtime startup must also verify the persisted account
// domain and transition policy. A valid role alone cannot authorize I/O.
type AccountRole struct {
	Model       ModelIdentity `json:"model"`
	PrincipalID string        `json:"principal_id"`
	Login       string        `json:"login"`
	Password    string        `json:"password"`
	Revision    string        `json:"revision"`
	// LastLogin optionally selects a server-owned nullable login timestamp.
	// An empty selection never infers a role from an existing model field.
	LastLogin string `json:"last_login"`
	// CreatedAt optionally identifies a server-owned creation timestamp. It is
	// never inferred from field names and is excluded from profile input.
	CreatedAt                 string               `json:"created_at,omitempty"`
	Email                     string               `json:"email,omitempty"`
	Active                    AccountBoolean       `json:"active"`
	Staff                     AccountBoolean       `json:"staff"`
	Superuser                 AccountBoolean       `json:"superuser"`
	Groups                    string               `json:"groups"`
	Permissions               string               `json:"permissions"`
	WriteLoginNormalization   AccountNormalization `json:"write_login_normalization"`
	AuthenticateNormalization AccountNormalization `json:"authenticate_normalization"`
	WriteEmailNormalization   AccountNormalization `json:"write_email_normalization,omitempty"`
	Catalog                   AccountCatalog       `json:"catalog"`
}

func (role AccountRole) Clone() AccountRole {
	role.Active = role.Active.Clone()
	role.Staff = role.Staff.Clone()
	role.Superuser = role.Superuser.Clone()
	return role
}

// NormalizeAccountRole checks one account and its catalog against a detached,
// normalized project schema. It resolves cross-app permission relations using
// the same IR graph as generated relation bindings. No default model or field
// names are inferred, and failures return no usable role.
func NormalizeAccountRole(input AccountRole, schemas ...Schema) (AccountRole, error) {
	role := input.Clone()
	for _, policy := range []struct {
		path  string
		value AccountNormalization
	}{
		{"write_login_normalization", role.WriteLoginNormalization},
		{"authenticate_normalization", role.AuthenticateNormalization},
	} {
		if policy.value != AccountExact && policy.value != AccountUnicode16NFKC {
			return AccountRole{}, validation("account."+policy.path, "unsupported_normalization", string(policy.value))
		}
	}
	if role.Email == "" && role.WriteEmailNormalization != "" || role.Email != "" &&
		role.WriteEmailNormalization != AccountExact && role.WriteEmailNormalization != AccountUnicode16EmailDomain {
		return AccountRole{}, validation("account.write_email_normalization", "unsupported_normalization", string(role.WriteEmailNormalization))
	}
	normalized := make([]Schema, len(schemas))
	models := make(map[ModelIdentity]Model)
	apps := make(map[string]struct{})
	for index, schema := range schemas {
		value, err := Normalize(schema)
		if err != nil {
			return AccountRole{}, err
		}
		if duplicate(apps, value.AppLabel) {
			return AccountRole{}, validation("account.schemas", "duplicate_app", value.AppLabel)
		}
		normalized[index] = value
		for _, model := range value.Models {
			models[ModelIdentity{AppLabel: value.AppLabel, ModelName: model.Name}] = model
		}
	}
	if _, err := ResolveManyToMany(normalized...); err != nil {
		return AccountRole{}, err
	}
	identities := []struct {
		path     string
		identity ModelIdentity
	}{
		{"model", role.Model}, {"catalog.group", role.Catalog.Group}, {"catalog.permission", role.Catalog.Permission},
	}
	seenModels := make(map[ModelIdentity]struct{})
	for _, item := range identities {
		path := "account." + item.path
		if err := validateManyToManyIdentity(item.identity, path); err != nil {
			return AccountRole{}, err
		}
		if _, exists := models[item.identity]; !exists {
			return AccountRole{}, validation(path, "unresolved_model", item.identity.AppLabel+"."+item.identity.ModelName)
		}
		if _, exists := seenModels[item.identity]; exists {
			return AccountRole{}, validation(path, "duplicate_model_role", "account, group and permission must be distinct models")
		}
		seenModels[item.identity] = struct{}{}
	}
	account, group, permission := models[role.Model], models[role.Catalog.Group], models[role.Catalog.Permission]
	if err := normalizeAccountFields(role, account); err != nil {
		return AccountRole{}, err
	}
	for _, item := range []struct {
		model  Model
		name   string
		path   string
		unique bool
		kind   FieldKind
	}{
		{group, role.Catalog.GroupName, "catalog.group_name", true, FieldChar},
		{group, role.Catalog.GroupRevision, "catalog.group_revision", false, FieldInteger},
		{permission, role.Catalog.PermissionCode, "catalog.permission_code", true, FieldChar},
		{permission, role.Catalog.PermissionName, "catalog.permission_name", false, FieldChar},
		{permission, role.Catalog.PermissionRevision, "catalog.permission_revision", false, FieldInteger},
	} {
		if _, err := accountRoleField(item.model, item.name, "account."+item.path, item.kind, false, item.unique); err != nil {
			return AccountRole{}, err
		}
	}
	if role.Catalog.PermissionCode == role.Catalog.PermissionName {
		return AccountRole{}, validation("account.catalog.permission_name", "duplicate_field_role", "permission code and display name must be distinct")
	}
	for _, item := range []struct {
		model  Model
		name   string
		path   string
		target ModelIdentity
	}{
		{account, role.Groups, "groups", role.Catalog.Group},
		{account, role.Permissions, "permissions", role.Catalog.Permission},
		{group, role.Catalog.GroupPermissions, "catalog.group_permissions", role.Catalog.Permission},
	} {
		if err := accountRoleRelation(item.model, item.name, "account."+item.path, item.target); err != nil {
			return AccountRole{}, err
		}
	}
	return role, nil
}

func normalizeAccountFields(role AccountRole, model Model) error {
	seen := make(map[string]string)
	for _, item := range []struct {
		name     string
		path     string
		kind     FieldKind
		nullable bool
		unique   bool
	}{
		{role.PrincipalID, "principal_id", FieldChar, false, true},
		{role.Login, "login", FieldChar, false, true},
		{role.Password, "password", FieldText, false, false},
		{role.Revision, "revision", FieldInteger, false, false},
	} {
		if _, err := accountRoleField(model, item.name, "account."+item.path, item.kind, item.nullable, item.unique); err != nil {
			return err
		}
		if previous, exists := seen[item.name]; exists {
			return validation("account."+item.path, "duplicate_field_role", previous)
		}
		seen[item.name] = item.path
	}
	for _, item := range []struct {
		name, path string
		nullable   bool
	}{
		{role.LastLogin, "last_login", true}, {role.CreatedAt, "created_at", false},
	} {
		if item.name == "" {
			continue
		}
		if _, err := accountRoleField(model, item.name, "account."+item.path, FieldDateTime, item.nullable, false); err != nil {
			return err
		}
		if previous, exists := seen[item.name]; exists {
			return validation("account."+item.path, "duplicate_field_role", previous)
		}
		seen[item.name] = item.path
	}
	if role.Email != "" {
		field, err := accountRoleField(model, role.Email, "account.email", FieldChar, false, false)
		if err != nil {
			return err
		}
		if field.Name != role.Login {
			if previous, exists := seen[field.Name]; exists {
				return validation("account.email", "duplicate_field_role", previous)
			}
			seen[field.Name] = "email"
		}
	}
	for _, item := range []struct {
		path string
		role AccountBoolean
	}{{"active", role.Active}, {"staff", role.Staff}, {"superuser", role.Superuser}} {
		if (item.role.Field != "") == (item.role.Constant != nil) {
			return validation("account."+item.path, "invalid_boolean_role", "select exactly one field or constant")
		}
		if item.role.Constant != nil {
			continue
		}
		if _, err := accountRoleField(model, item.role.Field, "account."+item.path+".field", FieldBoolean, false, false); err != nil {
			return err
		}
		if previous, exists := seen[item.role.Field]; exists {
			return validation("account."+item.path, "duplicate_field_role", previous)
		}
		seen[item.role.Field] = item.path
	}
	return nil
}

func accountRoleField(model Model, name, path string, kind FieldKind, nullable, unique bool) (Field, error) {
	for _, field := range model.Fields {
		if field.Name != name {
			continue
		}
		validKind := field.Kind == kind
		if kind == FieldChar {
			validKind = slices.Contains([]FieldKind{FieldChar, FieldEmail, FieldURL, FieldSlug, FieldText}, field.Kind)
		} else if kind == FieldText {
			// Encoded passwords are opaque text, not email/URL/slug input.
			validKind = field.Kind == FieldChar || field.Kind == FieldText
		}
		if !validKind || field.Nullable != nullable || field.PrimaryKey {
			return Field{}, validation(path, "invalid_field_role", fmt.Sprintf("requires non-primary %s nullable=%t", kind, nullable))
		}
		if unique && !accountRoleFieldUnique(model, field) {
			return Field{}, validation(path, "non_unique_field", name)
		}
		return field, nil
	}
	return Field{}, validation(path, "unresolved_field", name)
}

func accountRoleFieldUnique(model Model, field Field) bool {
	if field.Unique {
		return true
	}
	return slices.ContainsFunc(model.UniqueConstraints, func(constraint UniqueConstraint) bool {
		return len(constraint.Fields) == 1 && constraint.Fields[0] == field.Name
	})
}

func accountRoleRelation(model Model, name, path string, target ModelIdentity) error {
	for _, field := range model.ManyToMany {
		if field.Name != name {
			continue
		}
		if field.Target != target || field.Symmetry != ManyToManyDirected {
			return validation(path, "invalid_relation_role", "requires a directed many-to-many relation to the selected catalog model")
		}
		return nil
	}
	return validation(path, "unresolved_relation", name)
}
