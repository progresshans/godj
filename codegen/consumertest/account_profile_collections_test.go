package codegen_test

import (
	_ "embed"
	"fmt"
	"go/format"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/accountbinding/profile_collections_test.go.tmpl
var accountProfileCollectionsConsumer string

func TestGeneratedAccountProfileCollections(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "default"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			root := writeAccountProfileCollectionsConsumer(t, custom)
			required := []string{"TestGeneratedProfileCollections", "TestGeneratedProfileCollectionHTTP"}
			for _, child := range []string{"configuration", "first_form", "create", "required", "raw_limit", "current_choice", "partial_omission", "no_op", "symmetric", "cross_collection", "related_deletion", "validator", "policy_failure", "late_choice", "late_readback", "rollback", "unknown_commit", "read_cleanup", "read_overflow", "zero_key", "negative_key", "revision_race", "restart"} {
				required = append(required, "TestGeneratedProfileCollections/"+child)
			}
			for _, child := range []string{"choice_scope", "admin_create", "admin_edit", "json_create", "json_edit", "invalid_choice", "partial_form", "raw_form_limit", "selected_reads", "stale", "revoked_authority"} {
				required = append(required, "TestGeneratedProfileCollectionHTTP/"+child)
			}
			runAccountConsumerDatabases(t, root, required)
		})
	}
}

func writeAccountProfileCollectionsConsumer(t *testing.T, custom bool) string {
	t.Helper()
	spec := accountBindingConsumerSpec(t, custom)
	prefix, profile := "IdentityUser", "first_name"
	if custom {
		*spec.Account.Staff.Constant = true
		prefix, profile = "PeopleMember", "region"
	}
	topic := schema.Target("profiles", "topic")
	category := schema.Target("profiles", "category_link")
	tag := schema.Target("profiles", "tag_link")
	profiles, err := schema.Build(schema.Definition{AppLabel: "profiles", Models: []schema.Model{
		{Name: "topic", GoName: "Topic", Fields: []schema.Field{schema.CharField("title", "Title", 60, schema.Unique()), schema.BooleanField("enabled", "Enabled", schema.Default(true))}},
		{Name: "category_link", GoName: "CategoryLink", Fields: []schema.Field{
			schema.ForeignKey("owner", "OwnerID", spec.Account.Model, schema.NoReverse(), schema.Cascade),
			schema.ForeignKey("topic", "TopicID", topic, schema.NoReverse(), schema.Cascade),
		}},
		{Name: "tag_link", GoName: "TagLink", Fields: []schema.Field{
			schema.ForeignKey("owner", "OwnerID", spec.Account.Model, schema.NoReverse(), schema.Cascade),
			schema.ForeignKey("topic", "TopicID", topic, schema.NoReverse(), schema.Cascade),
			schema.ForeignKey("depends_on", "DependsOnID", category, schema.NoReverse(), schema.Cascade, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: "profiles", Package: codegen.PackageSpec{PackageName: "profiles", ImportPath: "example.com/godj-project-bundle/profiles", Directory: "profiles"}, Schema: profiles})
	var imports, schemas []string
	types := make(map[ir.ModelIdentity]string)
	for i := range spec.Apps {
		app := &spec.Apps[i]
		alias := fmt.Sprintf("model%d", i)
		imports = append(imports, fmt.Sprintf("%s %q", alias, app.Package.ImportPath))
		schemas = append(schemas, alias+".GoDjRelationSchema()")
		for j := range app.Schema.Models {
			model := &app.Schema.Models[j]
			identity := ir.ModelIdentity{AppLabel: app.Schema.AppLabel, ModelName: model.Name}
			types[identity] = alias + "." + model.GoName
			if identity != spec.Account.Model {
				continue
			}
			model.ManyToMany = append(model.ManyToMany,
				ir.ManyToManyField{Name: "topics", GoName: "Topics", Target: topic, Reverse: ir.ReverseRelation{Disabled: true}, Through: &ir.ThroughModel{Model: tag, SourceField: "owner", TargetField: "topic"}},
				ir.ManyToManyField{Name: "categories", GoName: "Categories", Target: topic, Blank: true, Reverse: ir.ReverseRelation{Disabled: true}, Through: &ir.ThroughModel{Model: category, SourceField: "owner", TargetField: "topic"}},
				ir.ManyToManyField{Name: "peers", GoName: "Peers", Target: identity, Blank: true, Symmetry: ir.ManyToManySymmetrical, Reverse: ir.ReverseRelation{Disabled: true}})
			model.Fields = append(model.Fields, ir.Field{Name: "category_anchor", GoName: "CategoryAnchorID", Column: "category_anchor_id", Kind: ir.FieldForeignKey, Nullable: true, Blank: true,
				Relation: &ir.ForeignKeyRelation{Target: category, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteCascade, Reverse: ir.ReverseRelation{Disabled: true}}})
		}
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := writeProjectBundleModule(t, bundle)
	source := strings.NewReplacer("@@IMPORTS@@", strings.Join(imports, "\n"), "@@SCHEMAS@@", strings.Join(schemas, ","),
		"@@MIGRATIONS@@", accountConsumerMigrationSources(t, spec),
		"@@ACCOUNT@@", types[spec.Account.Model], "@@GROUP@@", types[spec.Account.Catalog.Group], "@@PERMISSION@@", types[spec.Account.Catalog.Permission],
		"@@PROFILES@@", strings.TrimSuffix(types[topic], "Topic"), "@@TOPIC@@", types[topic], "@@COLLECTION@@", prefix, "@@PROFILE@@", profile).Replace(accountProfileCollectionsConsumer)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/profile_collections_test.go", formatted)
	writeGeneratedTestFile(t, root, "consumer/database_test.go", accountBindingDatabaseConsumer)
	return root
}
