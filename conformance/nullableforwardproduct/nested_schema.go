package nullableforwardproduct

import (
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

// NestedSchemas is the independently declared Go consumer input. Three apps
// share the reference's tables; self edges stay in their owning app packages.
func NestedSchemas() ([]ir.Schema, error) {
	definitions := []schema.Definition{
		{AppLabel: "directory", Models: []schema.Model{
			{Name: "organization", GoName: "Organization", DBTable: "nested_reference_organization", Fields: []schema.Field{
				schema.CharField("name", "Name", 40, schema.Nullable()), schema.BooleanField("active", "Active"), schema.DateTimeField("updated", "Updated", schema.Nullable()),
			}},
			{Name: "team", GoName: "Team", DBTable: "nested_reference_team", Fields: []schema.Field{
				schema.CharField("label", "Label", 40),
				schema.ForeignKey("organization", "OrganizationID", schema.Target("directory", "organization"), schema.RelatedName("teams"), schema.Protect),
				schema.ForeignKey("parent", "ParentID", schema.Target("directory", "team"), schema.RelatedName("children"), schema.SetNull, schema.Nullable()),
			}},
		}},
		{AppLabel: "people", Models: []schema.Model{
			{Name: "person", GoName: "Person", DBTable: "nested_reference_person", Fields: []schema.Field{
				schema.CharField("name", "Name", 40), schema.IntegerField("points", "Points", schema.Nullable()),
				schema.ForeignKey("team", "TeamID", schema.Target("directory", "team"), schema.RelatedName("members"), schema.Protect),
				schema.ForeignKey("backup", "BackupID", schema.Target("directory", "team"), schema.RelatedName("reserves"), schema.SetNull, schema.Nullable()),
				schema.ForeignKey("manager", "ManagerID", schema.Target("people", "person"), schema.RelatedName("reports"), schema.SetNull, schema.Nullable()),
			}},
		}},
		{AppLabel: "blog", Models: []schema.Model{
			{Name: "post", GoName: "Post", DBTable: "nested_reference_post", Fields: []schema.Field{
				schema.CharField("title", "Title", 40),
				schema.ForeignKey("author", "AuthorID", schema.Target("people", "person"), schema.RelatedName("posts"), schema.Protect),
				schema.ForeignKey("reviewer", "ReviewerID", schema.Target("people", "person"), schema.RelatedName("reviews"), schema.SetNull, schema.Nullable()),
			}},
			{Name: "comment", GoName: "Comment", DBTable: "nested_reference_comment", Fields: []schema.Field{
				schema.ForeignKey("post", "PostID", schema.Target("blog", "post"), schema.RelatedName("comments"), schema.Protect), schema.CharField("body", "Body", 40),
			}},
		}},
	}
	result := make([]ir.Schema, len(definitions))
	for index, definition := range definitions {
		s, err := schema.Build(definition)
		if err != nil {
			return nil, err
		}
		result[index] = s
	}
	return result, nil
}
