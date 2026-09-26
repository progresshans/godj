// Package modeldef declares the built-in identity models without importing
// their generated representation. Applications can include this app in their
// project when declaring relations to users, groups or permissions.
package modeldef

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

const AppLabel = "godj_identity"

func Schema() (ir.Schema, error) {
	return schema.Build(schema.Definition{AppLabel: AppLabel, Models: []schema.Model{
		{Name: "permission", GoName: "Permission", Fields: []schema.Field{
			schema.CharField("code", "Code", 128, schema.Unique()),
			schema.CharField("name", "Name", 255),
		}},
		{Name: "group", GoName: "Group", Fields: []schema.Field{
			schema.CharField("name", "Name", 150, schema.Unique()),
			schema.IntegerField("revision", "Revision", schema.Default(int64(1))),
		}, ManyToMany: []schema.ManyToManyField{
			schema.ManyToMany("permissions", "Permissions", schema.Target(AppLabel, "permission"), schema.RelatedName("groups")),
		}},
		{Name: "user", GoName: "User", Fields: []schema.Field{
			// Principal IDs remain opaque authentication/audit identities. They
			// are independent of integer relation keys and editable usernames.
			schema.CharField("principal_id", "PrincipalID", 128, schema.Unique()),
			schema.CharField("username", "Username", 256, schema.Unique()),
			schema.CharField("encoded_password", "EncodedPassword", 2048),
			schema.CharField("first_name", "FirstName", 150, schema.Default("")),
			schema.CharField("last_name", "LastName", 150, schema.Default("")),
			schema.CharField("email", "Email", 254, schema.Default("")),
			schema.BooleanField("active", "Active", schema.Default(true)),
			schema.BooleanField("staff", "Staff", schema.Default(false)),
			schema.BooleanField("superuser", "Superuser", schema.Default(false)),
			schema.DateTimeField("date_joined", "DateJoined"),
			schema.DateTimeField("last_login", "LastLogin", schema.Nullable()),
			schema.IntegerField("revision", "Revision", schema.Default(int64(1))),
		}, ManyToMany: []schema.ManyToManyField{
			schema.ManyToMany("groups", "Groups", schema.Target(AppLabel, "group"), schema.RelatedName("users")),
			schema.ManyToMany("permissions", "Permissions", schema.Target(AppLabel, "permission"), schema.RelatedName("users")),
		}},
	}})
}

// AppSpec is reusable by a host project that also declares models referring to
// the identity app. Its schema and generator input are fresh caller snapshots.
func AppSpec() (codegen.AppSpec, error) {
	definition, err := Schema()
	if err != nil {
		return codegen.AppSpec{}, err
	}
	return codegen.AppSpec{
		Alias:    "identity",
		Package:  codegen.PackageSpec{PackageName: "models", ImportPath: "github.com/progresshans/godj/identity/models"},
		Schema:   definition,
		External: true,
	}, nil
}

func ProjectSpec(ctx context.Context) (codegen.ProjectSpec, error) {
	if ctx == nil {
		return codegen.ProjectSpec{}, fmt.Errorf("identity schema: nil context")
	}
	if err := ctx.Err(); err != nil {
		return codegen.ProjectSpec{}, err
	}
	app, err := AppSpec()
	if err != nil {
		return codegen.ProjectSpec{}, err
	}
	app.External = false
	app.Package.Directory = "models"
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "github.com/progresshans/godj/identity/project", Directory: "project"},
		Apps:    []codegen.AppSpec{app},
	}, nil
}
