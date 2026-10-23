package testschema

import (
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

// CustomAccount declares a separately owned account, a permission catalog and
// a host model referring to that account. It intentionally has no User model,
// username field, staff column or built-in identity app.
func CustomAccount() (ir.AccountRole, []ir.Schema, error) {
	catalog, err := schema.Build(schema.Definition{AppLabel: "access", Models: []schema.Model{
		{Name: "grant", GoName: "Grant", Fields: []schema.Field{
			schema.CharField("key", "Key", 128, schema.Unique()), schema.CharField("title", "Title", 255), schema.IntegerField("version", "Version", schema.Default(int64(1))),
		}},
		{Name: "team", GoName: "Team", Fields: []schema.Field{
			schema.CharField("title", "Title", 150, schema.Unique()), schema.IntegerField("version", "Version", schema.Default(int64(1))),
		}, ManyToMany: []schema.ManyToManyField{
			schema.ManyToMany("grants", "Grants", schema.Target("access", "grant"), schema.NoReverse()),
		}},
	}})
	if err != nil {
		return ir.AccountRole{}, nil, err
	}
	people, err := schema.Build(schema.Definition{AppLabel: "people", Models: []schema.Model{{
		Name: "member", GoName: "Member", Fields: []schema.Field{
			schema.CharField("subject", "Subject", 128, schema.Unique()),
			schema.EmailField("contact", "Contact", schema.Unique()),
			schema.CharField("secret", "Secret", 2048),
			schema.IntegerField("version", "Version", schema.Default(int64(1))),
			schema.DateTimeField("seen", "Seen", schema.Nullable()),
			schema.BooleanField("enabled", "Enabled", schema.Default(true)),
			schema.BooleanField("root", "Root", schema.Default(false)),
			schema.CharField("region", "Region", 24),
		}, ManyToMany: []schema.ManyToManyField{
			schema.ManyToMany("teams", "Teams", schema.Target("access", "team"), schema.NoReverse()),
			schema.ManyToMany("grants", "Grants", schema.Target("access", "grant"), schema.NoReverse()),
		},
	}}})
	if err != nil {
		return ir.AccountRole{}, nil, err
	}
	tickets, err := schema.Build(schema.Definition{AppLabel: "tickets", Models: []schema.Model{{
		Name: "ticket", GoName: "Ticket", Fields: []schema.Field{
			schema.CharField("title", "Title", 200),
			schema.ForeignKey("owner", "OwnerID", schema.Target("people", "member"), schema.RelatedName("tickets"), schema.Protect),
		},
	}}})
	if err != nil {
		return ir.AccountRole{}, nil, err
	}
	staff := false
	role := ir.AccountRole{
		Model: schema.Target("people", "member"), PrincipalID: "subject", Login: "contact", Password: "secret",
		Revision: "version", LastLogin: "seen", Email: "contact",
		Active: ir.AccountBoolean{Field: "enabled"}, Staff: ir.AccountBoolean{Constant: &staff}, Superuser: ir.AccountBoolean{Field: "root"},
		Groups: "teams", Permissions: "grants", WriteLoginNormalization: ir.AccountUnicode16NFKC, AuthenticateNormalization: ir.AccountExact,
		Catalog: ir.AccountCatalog{
			Group: schema.Target("access", "team"), GroupName: "title", GroupRevision: "version", GroupPermissions: "grants",
			Permission: schema.Target("access", "grant"), PermissionCode: "key", PermissionName: "title", PermissionRevision: "version",
		},
	}
	return role, []ir.Schema{catalog, people, tickets}, nil
}
