package ir_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestAccountRoleResolvesRenamedFieldsAndCrossAppCatalog(t *testing.T) {
	t.Parallel()
	role, schemas := customAccountRole(t)
	before := make([]ir.Schema, len(schemas))
	for index := range schemas {
		before[index] = schemas[index].Clone()
	}
	normalized, err := ir.NormalizeAccountRole(role, schemas...)
	if err != nil || !reflect.DeepEqual(normalized, role) {
		t.Fatalf("normalize custom account = %#v, %v", normalized, err)
	}
	if !reflect.DeepEqual(before, schemas) {
		t.Fatal("account binding mutated model definitions")
	}
	// Reordering installed apps does not select a different model or policy.
	reordered, err := ir.NormalizeAccountRole(role, schemas[1], schemas[0])
	if err != nil || !reflect.DeepEqual(reordered, normalized) {
		t.Fatalf("reordered binding = %#v, %v", reordered, err)
	}
	// The selected email field can also be the unique login. A separate profile
	// field remains ordinary model metadata rather than a second account row.
	if normalized.Login != normalized.Email || len(schemas[0].Models) != 1 || len(schemas[0].Models[0].Fields) != 10 {
		t.Fatal("custom account did not retain its independent model shape")
	}
}

func TestAccountRoleConstantsAreExplicitAndDetached(t *testing.T) {
	t.Parallel()
	role, schemas := customAccountRole(t)
	active, staff, superuser := true, false, false
	role.Active = ir.AccountBoolean{Constant: &active}
	role.Staff = ir.AccountBoolean{Constant: &staff}
	role.Superuser = ir.AccountBoolean{Constant: &superuser}
	normalized, err := ir.NormalizeAccountRole(role, schemas...)
	if err != nil {
		t.Fatal(err)
	}
	active, staff, superuser = false, true, true
	if !*normalized.Active.Constant || *normalized.Staff.Constant || *normalized.Superuser.Constant {
		t.Fatal("caller changed normalized constant roles")
	}
	clone := normalized.Clone()
	*clone.Active.Constant = false
	*clone.Staff.Constant = true
	*clone.Superuser.Constant = true
	if !*normalized.Active.Constant || *normalized.Staff.Constant || *normalized.Superuser.Constant {
		t.Fatal("clone retained role constant ownership")
	}
}

func TestAccountRoleLastLoginIsOptionalWithoutInferringExistingFields(t *testing.T) {
	for _, remove := range []bool{false, true} {
		role, schemas := customAccountRole(t)
		name := role.LastLogin
		role.LastLogin = ""
		if remove {
			schemas[0].Models[0].Fields = slices.DeleteFunc(schemas[0].Models[0].Fields, func(field ir.Field) bool { return field.Name == name })
		}
		normalized, err := ir.NormalizeAccountRole(role, schemas...)
		if err != nil || normalized.LastLogin != "" {
			t.Fatal("optional login role inferred or required", err)
		}
		if remove {
			role.LastLogin = name
			if _, err := ir.NormalizeAccountRole(role, schemas...); err == nil {
				t.Fatal("explicit missing login role accepted")
			}
		}
	}
}

func TestAccountRoleRejectsAmbiguousAndForeignSecurityBindings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*ir.AccountRole, []ir.Schema)
		path   string
		code   string
	}{
		{"unselected model", func(r *ir.AccountRole, _ []ir.Schema) { r.Model.ModelName = "user" }, "account.model", "unresolved_model"},
		{"foreign principal field", func(r *ir.AccountRole, _ []ir.Schema) { r.PrincipalID = "principal_id" }, "account.principal_id", "unresolved_field"},
		{"shared principal and login", func(r *ir.AccountRole, _ []ir.Schema) { r.PrincipalID = r.Login }, "account.login", "duplicate_field_role"},
		{"missing write policy", func(r *ir.AccountRole, _ []ir.Schema) { r.WriteLoginNormalization = "" }, "account.write_login_normalization", "unsupported_normalization"},
		{"missing form policy", func(r *ir.AccountRole, _ []ir.Schema) { r.LoginFormNormalization = "" }, "account.login_form_normalization", "unsupported_normalization"},
		{"unknown form policy", func(r *ir.AccountRole, _ []ir.Schema) { r.LoginFormNormalization = ir.AccountUnicode16EmailDomain }, "account.login_form_normalization", "unsupported_normalization"},
		{"missing email policy", func(r *ir.AccountRole, _ []ir.Schema) { r.WriteEmailNormalization = "" }, "account.write_email_normalization", "unsupported_normalization"},
		{"unknown email policy", func(r *ir.AccountRole, _ []ir.Schema) { r.WriteEmailNormalization = ir.AccountUnicode16NFKC }, "account.write_email_normalization", "unsupported_normalization"},
		{"email policy without role", func(r *ir.AccountRole, _ []ir.Schema) { r.Email = "" }, "account.write_email_normalization", "unsupported_normalization"},
		{"implicit email case folding", func(r *ir.AccountRole, _ []ir.Schema) { r.AuthenticateNormalization = "email" }, "account.authenticate_normalization", "unsupported_normalization"},
		{"missing constant", func(r *ir.AccountRole, _ []ir.Schema) { r.Staff = ir.AccountBoolean{} }, "account.staff", "invalid_boolean_role"},
		{"field plus constant", func(r *ir.AccountRole, _ []ir.Schema) { value := false; r.Staff.Constant = &value }, "account.staff", "invalid_boolean_role"},
		{"shared role field", func(r *ir.AccountRole, _ []ir.Schema) { r.Staff = r.Active }, "account.staff", "duplicate_field_role"},
		{"credential as email", func(r *ir.AccountRole, _ []ir.Schema) { r.Email = r.Password }, "account.email", "duplicate_field_role"},
		{"foreign group relation", func(r *ir.AccountRole, _ []ir.Schema) { r.Groups = "groups" }, "account.groups", "unresolved_relation"},
		{"wrong catalog target", func(r *ir.AccountRole, _ []ir.Schema) { r.Groups = r.Permissions }, "account.groups", "invalid_relation_role"},
		{"duplicate model roles", func(r *ir.AccountRole, _ []ir.Schema) { r.Catalog.Group = r.Model }, "account.catalog.group", "duplicate_model_role"},
		{"nonunique login", func(_ *ir.AccountRole, s []ir.Schema) { s[0].Models[0].Fields[2].Unique = false }, "account.login", "non_unique_field"},
		{"compound uniqueness is not login uniqueness", func(_ *ir.AccountRole, s []ir.Schema) {
			s[0].Models[0].Fields[2].Unique = false
			s[0].Models[0].UniqueConstraints = []ir.UniqueConstraint{{Name: "contact_with_region", Fields: []string{"contact", "region"}}}
		}, "account.login", "non_unique_field"},
		{"nullable login", func(_ *ir.AccountRole, s []ir.Schema) { s[0].Models[0].Fields[2].Nullable = true }, "account.login", "invalid_field_role"},
		{"nullable revision", func(_ *ir.AccountRole, s []ir.Schema) { s[0].Models[0].Fields[4].Nullable = true }, "account.revision", "invalid_field_role"},
		{"nonnullable last login", func(_ *ir.AccountRole, s []ir.Schema) { s[0].Models[0].Fields[5].Nullable = false }, "account.last_login", "invalid_field_role"},
		{"password has email semantics", func(r *ir.AccountRole, _ []ir.Schema) { r.Password = r.Login }, "account.password", "invalid_field_role"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			role, schemas := customAccountRole(t)
			test.change(&role, schemas)
			value, err := ir.NormalizeAccountRole(role, schemas...)
			var invalid *ir.ValidationError
			if !errors.As(err, &invalid) || invalid.Path != test.path || invalid.Code != test.code {
				t.Fatalf("binding error = %v, want %s %s", err, test.path, test.code)
			}
			if !reflect.DeepEqual(value, ir.AccountRole{}) {
				t.Fatal("invalid binding published a partial account role")
			}
		})
	}
}

func TestAccountRoleUsesDeclaredUniqueConstraintAndDetectsRename(t *testing.T) {
	t.Parallel()
	role, schemas := customAccountRole(t)
	model := &schemas[0].Models[0]
	model.Fields[2].Unique = false
	model.UniqueConstraints = []ir.UniqueConstraint{{Name: "account_login", Fields: []string{"contact"}}}
	if _, err := ir.NormalizeAccountRole(role, schemas...); err != nil {
		t.Fatalf("declared single-column uniqueness = %v", err)
	}
	model.Fields[2].Name = "login_address"
	model.UniqueConstraints[0].Fields[0] = "login_address"
	if _, err := ir.NormalizeAccountRole(role, schemas...); err == nil {
		t.Fatal("stale account role followed an unrelated field by Go name or column")
	}
	role.Login, role.Email = "login_address", "login_address"
	if _, err := ir.NormalizeAccountRole(role, schemas...); err != nil {
		t.Fatalf("explicit renamed field binding = %v", err)
	}
}

func TestAccountRoleCreationTimestampIsExplicitAndSourceBound(t *testing.T) {
	role, schemas := customAccountRole(t)
	// A custom account need not have a creation timestamp. If it declares one,
	// the role owns the exact server field instead of discovering a name.
	schemas[0].Models[0].Fields = append(schemas[0].Models[0].Fields, ir.Field{
		Name: "registered", GoName: "Registered", Column: "registered_at", Kind: ir.FieldDateTime,
	})
	role.CreatedAt = "registered"
	got, err := ir.NormalizeAccountRole(role, schemas...)
	if err != nil || got.CreatedAt != "registered" {
		t.Fatal("explicit creation role lost", err)
	}
	for _, bad := range []string{"unknown", role.Login, role.LastLogin} {
		role.CreatedAt = bad
		if _, err := ir.NormalizeAccountRole(role, schemas...); err == nil {
			t.Fatal("invalid server timestamp accepted", bad)
		}
	}
	role.CreatedAt = "registered"
	schemas[0].Models[0].Fields[len(schemas[0].Models[0].Fields)-1].Nullable = true
	if _, err := ir.NormalizeAccountRole(role, schemas...); err == nil {
		t.Fatal("nullable creation timestamp accepted")
	}
}

func customAccountRole(t *testing.T) (ir.AccountRole, []ir.Schema) {
	t.Helper()
	account, err := schema.Build(schema.Definition{AppLabel: "people", Models: []schema.Model{{
		Name: "member", GoName: "Member", Fields: []schema.Field{
			schema.CharField("subject", "Subject", 128, schema.Unique()),
			schema.EmailField("contact", "Contact", schema.Unique()),
			schema.CharField("secret", "Secret", 2048),
			schema.IntegerField("version", "Version"),
			schema.DateTimeField("seen", "Seen", schema.Nullable()),
			schema.BooleanField("enabled", "Enabled"),
			schema.BooleanField("operator", "Operator"),
			schema.BooleanField("root", "Root"),
			schema.CharField("region", "Region", 24),
		}, ManyToMany: []schema.ManyToManyField{
			schema.ManyToMany("teams", "Teams", schema.Target("access", "team"), schema.RelatedName("members")),
			schema.ManyToMany("grants", "Grants", schema.Target("access", "grant"), schema.RelatedName("members")),
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := schema.Build(schema.Definition{AppLabel: "access", Models: []schema.Model{
		{Name: "team", GoName: "Team", Fields: []schema.Field{
			schema.CharField("title", "Title", 150, schema.Unique()), schema.IntegerField("version", "Version"),
		}, ManyToMany: []schema.ManyToManyField{
			schema.ManyToMany("grants", "Grants", schema.Target("access", "grant"), schema.RelatedName("teams")),
		}},
		{Name: "grant", GoName: "Grant", Fields: []schema.Field{
			schema.CharField("key", "Key", 128, schema.Unique()), schema.CharField("title", "Title", 255), schema.IntegerField("version", "Version"),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	role := ir.AccountRole{
		Model: schema.Target("people", "member"), PrincipalID: "subject", Login: "contact", Password: "secret",
		Revision: "version", LastLogin: "seen", Email: "contact",
		Active: ir.AccountBoolean{Field: "enabled"}, Staff: ir.AccountBoolean{Field: "operator"}, Superuser: ir.AccountBoolean{Field: "root"},
		Groups: "teams", Permissions: "grants", WriteLoginNormalization: ir.AccountUnicode16NFKC, LoginFormNormalization: ir.AccountExact, AuthenticateNormalization: ir.AccountExact,
		WriteEmailNormalization: ir.AccountExact,
		Catalog: ir.AccountCatalog{
			Group: schema.Target("access", "team"), GroupName: "title", GroupRevision: "version", GroupPermissions: "grants",
			Permission: schema.Target("access", "grant"), PermissionCode: "key", PermissionName: "title", PermissionRevision: "version",
		},
	}
	return role, []ir.Schema{account, catalog}
}
